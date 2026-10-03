#!/usr/bin/bash

storage_action_apply_json() {
    local submitted_fingerprint submitted_confirmation
    local operation device mountpoint current_mountpoint expected_confirmation
    local filesystem uuid mounted_after result_role size actual_uuid
    local planned_end before after partition new_partitions mkfs_output

    prepare_plan || return 0

    submitted_fingerprint="$(jq -r '.fingerprint // ""' <<< "${REQUEST_JSON}")"
    submitted_confirmation="$(jq -r '.confirmation // ""' <<< "${REQUEST_JSON}")"
    operation="$(jq -r '.operation' <<< "${PLAN_JSON}")"
    device="$(jq -r '.device' <<< "${PLAN_JSON}")"
    mountpoint="$(jq -r '.mount_point' <<< "${PLAN_JSON}")"
    current_mountpoint="$(jq -r '.current_mount_point' <<< "${PLAN_JSON}")"
    expected_confirmation="$(jq -r '.confirmation' <<< "${PLAN_JSON}")"
    planned_end="$(jq -r '.planned_end // ""' <<< "${PLAN_JSON}")"
    partition=''

    if [[ -z ${submitted_fingerprint} || ${submitted_fingerprint} != "$(jq -r '.fingerprint' <<< "${PLAN_JSON}")" ]]; then
        json_error 'The selected partition changed after Review. Nothing was changed. Review the action again.'
        return 0
    fi
    if [[ -n ${expected_confirmation} && ${submitted_confirmation} != "${expected_confirmation}" ]]; then
        json_error "Confirmation does not match. Type exactly: ${expected_confirmation}"
        return 0
    fi

    if [[ $(jq -r '.destructive' <<< "${PLAN_JSON}") == true && $(jq -r '.role' <<< "${PLAN_JSON}") == *Minecraft* ]]; then
        source /usr/libexec/justvoxel/mjust/interrupt-safety.sh
        if ! JV_INTERRUPT_CONFIRMATION_MODE=confirmed jv_stop_minecraft_adaptive 'erase Minecraft storage' >/dev/null 2>&1; then
            json_error 'Minecraft could not be stopped safely. No storage was erased.'
            return 0
        fi
    fi

    case "${operation}" in
        mount)
            install -d -m0755 -o root -g root "${mountpoint}"
            filesystem="$(storage_action_filesystem "${device}")"
            if ! storage_action_mount_device "${device}" "${mountpoint}" "${filesystem}"; then
                json_error 'Mount failed. Nothing else was changed.'
                return 0
            fi
            ;;
        unmount)
            if ! storage_action_host_unmount "${current_mountpoint}"; then
                json_error 'Unmount failed. The partition may still be in use.'
                return 0
            fi
            ;;
        format)
            if [[ -n ${current_mountpoint} ]] && ! storage_action_host_unmount "${current_mountpoint}"; then
                json_error 'The partition is still in use and could not be unmounted. It was not formatted.'
                return 0
            fi
            mounted_after="$(storage_action_host_mountpoints "${device}")" || {
                json_error 'The host mount state could not be verified. It was not formatted.'
                return 0
            }
            if [[ -n ${mounted_after} ]]; then
                json_error 'The partition is still mounted on the host. It was not formatted.'
                return 0
            fi
            if ! mkfs_output="$(storage_mkfs_xfs JV_STORAGE "${device}" 2>&1)"; then
                json_error "Formatting failed: $(storage_action_mkfs_error "${mkfs_output}")"
                return 0
            fi
            udevadm settle >/dev/null 2>&1 || true
            ;;
        delete_partition|initialize_disk)
            if [[ -n ${current_mountpoint} ]] && ! storage_action_host_unmount "${current_mountpoint}"; then
                json_error 'Storage is still in use. Nothing was erased.'; return 0
            fi
            mounted_after="$(storage_action_host_mountpoints "${device}")" || { json_error 'Host mount state could not be verified. Nothing was erased.'; return 0; }
            [[ -z ${mounted_after} ]] || { json_error 'Storage is still mounted. Nothing was erased.'; return 0; }
            if [[ ${operation} == delete_partition ]]; then
                local parent number
                parent="$(storage_action_parent_disk "${device}")"
                number="$(cat "/sys/class/block/${device##*/}/partition")" || { json_error 'Partition number could not be read safely.'; return 0; }
                [[ ${number} =~ ^[1-9][0-9]*$ ]] || { json_error 'Partition number could not be identified safely.'; return 0; }
                if ! parted -s -- "${parent}" rm "${number}" || ! partprobe "${parent}" || ! udevadm settle; then
                    json_error 'Partition deletion did not complete cleanly. Inspect the disk before retrying.'; return 0
                fi
                if lsblk -nrpo NAME "${parent}" | grep -Fxq "${device}"; then
                    json_error 'The deleted partition is still visible. Inspect the disk before retrying.'; return 0
                fi
            else
                if ! wipefs --all -- "${device}" >/dev/null || ! parted -s -- "${device}" mklabel gpt >/dev/null || ! partprobe "${device}" >/dev/null || ! udevadm settle >/dev/null; then
                    json_error 'Disk reinitialization did not complete cleanly. Inspect the disk before retrying.'; return 0
                fi
                [[ $(storage_partition_table_type "${device}") == gpt ]] || { json_error 'The new GPT table could not be verified.'; return 0; }
            fi
            jq -n --argjson proposed "${PLAN_JSON}" --argjson warnings "${PLAN_WARNINGS}" '{ok:true,proposed:$proposed,warnings:$warnings,applied:true}'
            return 0
            ;;
        create_partition)
            before="$(mktemp)"
            after="$(mktemp)"
            lsblk -nrpo NAME,TYPE "${device}" | awk '$2 == "part" {print $1}' | sort > "${before}"
            if ! storage_create_partition "${device}" "${STORAGE_ACTION_START}" "${planned_end}" >/dev/null 2>&1 \
                || ! partprobe "${device}" >/dev/null 2>&1 \
                || ! udevadm settle >/dev/null 2>&1; then
                rm -f "${before}" "${after}"
                json_error 'Creating the new partition failed after the partition-table operation began. Inspect the disk before retrying.'
                return 0
            fi
            lsblk -nrpo NAME,TYPE "${device}" | awk '$2 == "part" {print $1}' | sort > "${after}"
            new_partitions="$(comm -13 "${before}" "${after}")"
            rm -f "${before}" "${after}"
            if [[ "$(printf '%s\n' "${new_partitions}" | sed '/^$/d' | wc -l)" -ne 1 ]]; then
                json_error 'The new partition could not be identified uniquely. Inspect the disk before retrying.'
                return 0
            fi
            partition="$(printf '%s\n' "${new_partitions}" | sed '/^$/d' | head -n1)"
            [[ -b ${partition} ]] || {
                json_error 'The new partition could not be identified safely. Inspect the disk before retrying.'
                return 0
            }
            mounted_after="$(storage_action_host_mountpoints "${partition}")" || {
                json_error 'The new partition host mount state could not be verified. It was not formatted.'
                return 0
            }
            if [[ -n ${mounted_after} ]]; then
                json_error 'The new partition became mounted on the host. It was not formatted.'
                return 0
            fi
            if ! mkfs_output="$(storage_mkfs_xfs JV_STORAGE "${partition}" 2>&1)"; then
                json_error "The partition was created, but creating the XFS filesystem failed: $(storage_action_mkfs_error "${mkfs_output}")"
                return 0
            fi
            udevadm settle >/dev/null 2>&1 || true
            device="${partition}"
            ;;
    esac

    filesystem="$(storage_action_filesystem "${device}")"
    uuid="$(storage_action_uuid "${device}")"
    mounted_after="$(storage_action_mountpoint "${device}")" || {
        json_error 'The host mount state could not be verified after the storage action.'
        return 0
    }
    result_role="$(storage_action_role "${device}" "${filesystem}" "${uuid}" "${mounted_after}")"
    size="$(storage_action_size_bytes "${device}")"
    size="${size:-0}"

    case "${operation}" in
        mount)
            [[ -n ${mounted_after} ]] || {
                json_error 'Mount command completed, but the partition is not mounted.'
                return 0
            }
            actual_uuid="$(storage_action_host_mount_identity UUID "${mounted_after}" 2>/dev/null || true)"
            [[ -n ${uuid} && ${actual_uuid} == "${uuid}" ]] || {
                storage_action_host_unmount "${mounted_after}" >/dev/null 2>&1 || true
                json_error 'The mounted filesystem did not match the reviewed filesystem.'
                return 0
            }
            ;;
        unmount)
            [[ -z ${mounted_after} ]] || {
                json_error 'Unmount command completed, but the partition is still mounted.'
                return 0
            }
            ;;
        format|create_partition)
            [[ ${filesystem} == xfs ]] || {
                json_error 'Storage action completed, but XFS could not be verified.'
                return 0
            }
            ;;
    esac

    jq -n \
        --argjson proposed "${PLAN_JSON}" \
        --argjson warnings "${PLAN_WARNINGS}" \
        --arg filesystem "${filesystem}" \
        --arg uuid "${uuid}" \
        --arg mount_point "${mounted_after}" \
        --arg role "${result_role}" \
        --arg created_device "${partition}" \
        --argjson size_bytes "${size}" \
        '{ok:true,proposed:($proposed + {filesystem:$filesystem,uuid:$uuid,current_mount_point:$mount_point,role:$role,created_device:$created_device,size_bytes:$size_bytes}),warnings:$warnings,applied:true}'
}
