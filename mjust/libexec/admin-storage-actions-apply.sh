#!/usr/bin/bash

storage_action_apply_json() {
    local submitted_fingerprint submitted_confirmation
    local operation device mountpoint current_mountpoint expected_confirmation
    local filesystem uuid mounted_after result_role size actual_uuid
    local free_start planned_end before after partition new_partitions

    prepare_plan || return 0

    submitted_fingerprint="$(jq -r '.fingerprint // ""' <<< "${REQUEST_JSON}")"
    submitted_confirmation="$(jq -r '.confirmation // ""' <<< "${REQUEST_JSON}")"
    operation="$(jq -r '.operation' <<< "${PLAN_JSON}")"
    device="$(jq -r '.device' <<< "${PLAN_JSON}")"
    mountpoint="$(jq -r '.mount_point' <<< "${PLAN_JSON}")"
    current_mountpoint="$(jq -r '.current_mount_point' <<< "${PLAN_JSON}")"
    expected_confirmation="$(jq -r '.confirmation' <<< "${PLAN_JSON}")"
    free_start="$(jq -r '.free_start // ""' <<< "${PLAN_JSON}")"
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
            if ! umount -- "${current_mountpoint}"; then
                json_error 'Unmount failed. The partition may still be in use.'
                return 0
            fi
            ;;
        format)
            if [[ -n ${current_mountpoint} ]] && ! umount -- "${current_mountpoint}"; then
                json_error 'The partition is still in use and could not be unmounted. It was not formatted.'
                return 0
            fi
            if ! storage_mkfs_xfs JV_STORAGE "${device}" >/dev/null 2>&1; then
                json_error 'Formatting failed. Inspect the partition before retrying.'
                return 0
            fi
            udevadm settle >/dev/null 2>&1 || true
            ;;
        create_partition)
            before="$(mktemp)"
            after="$(mktemp)"
            lsblk -nrpo NAME,TYPE "${device}" | awk '$2 == "part" {print $1}' | sort > "${before}"
            if ! storage_create_partition "${device}" "${free_start}" "${planned_end}" >/dev/null 2>&1 \
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
            if ! storage_mkfs_xfs JV_STORAGE "${partition}" >/dev/null 2>&1; then
                json_error 'The partition was created, but creating the XFS filesystem failed. Inspect the new partition before retrying.'
                return 0
            fi
            udevadm settle >/dev/null 2>&1 || true
            device="${partition}"
            ;;
    esac

    filesystem="$(storage_action_filesystem "${device}")"
    uuid="$(storage_action_uuid "${device}")"
    mounted_after="$(storage_action_mountpoint "${device}")"
    result_role="$(storage_action_role "${device}" "${filesystem}" "${uuid}" "${mounted_after}")"
    size="$(storage_action_size_bytes "${device}")"
    size="${size:-0}"

    case "${operation}" in
        mount)
            [[ -n ${mounted_after} ]] || {
                json_error 'Mount command completed, but the partition is not mounted.'
                return 0
            }
            actual_uuid="$(findmnt -n -o UUID --target "${mounted_after}" 2>/dev/null || true)"
            [[ -n ${uuid} && ${actual_uuid} == "${uuid}" ]] || {
                umount -- "${mounted_after}" >/dev/null 2>&1 || true
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
