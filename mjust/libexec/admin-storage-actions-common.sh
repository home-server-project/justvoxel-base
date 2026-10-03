#!/usr/bin/bash
set -euo pipefail

source /usr/libexec/justvoxel/mjust/common.sh
source /usr/libexec/justvoxel/mjust/storage-common-base.sh

STORAGE_ACTION_CONFIGURED=false

storage_action_load_config() {
    if [[ -r ${JV_CONFIG} && ! -e ${JV_SETUP_IN_PROGRESS} ]]; then
        require_config
        STORAGE_ACTION_CONFIGURED=true
    fi
}

storage_action_real_device() {
    readlink -f -- "$1" 2>/dev/null || true
}

storage_action_parent_disk() {
    local device real
    device="$1"
    real="$(storage_action_real_device "${device}")"
    [[ -n ${real} ]] || return 1
    lsblk -s -nrpo NAME,TYPE "${real}" 2>/dev/null | awk '$2 == "disk" {print $1; exit}'
}

storage_action_is_system_partition() {
    local device requested system_partition real
    device="$1"
    requested="$(storage_action_real_device "${device}")"
    [[ -n ${requested} ]] || return 1
    while IFS= read -r system_partition; do
        [[ -n ${system_partition} ]] || continue
        real="$(storage_action_real_device "${system_partition}")"
        [[ -n ${real} && ${requested} == "${real}" ]] && return 0
    done < <(storage_system_partitions)
    return 1
}

storage_action_mountpoint() {
    storage_action_host_mountpoints "$1" | awk 'NF && !found++ {print}'
}

storage_action_host_mountpoints() {
    # lsblk reads /proc/self/mountinfo. Query it in the appliance mount namespace.
    nsenter --mount=/proc/1/ns/mnt -- lsblk -nro MOUNTPOINTS "$1" | sed '/^$/d'
}

storage_action_host_mountpoint_occupied() {
    nsenter --mount=/proc/1/ns/mnt -- mountpoint -q -- "$1"
}

storage_action_host_unmount() {
    nsenter --mount=/proc/1/ns/mnt -- umount -- "$1"
}

storage_action_host_mount() {
    nsenter --mount=/proc/1/ns/mnt -- mount -- "$1"
}

storage_action_host_mount_identity() {
    nsenter --mount=/proc/1/ns/mnt -- bash -c 'source /usr/libexec/justvoxel/mjust/common.sh; jv_exact_mount_identity "$1" "$2"' bash "$1" "$2"
}

storage_action_mkfs_error() {
    local message
    message="$(printf '%s' "$1" | LC_ALL=C tr -cd '\11\12\15\40-\176' | tr '\r\n' '  ' | cut -c1-240)"
    printf '%s\n' "${message:-XFS did not provide an error message. Inspect the partition before retrying.}"
}

storage_action_filesystem() {
    blkid -s TYPE -o value "$1" 2>/dev/null || true
}

storage_action_uuid() {
    blkid -s UUID -o value "$1" 2>/dev/null || true
}

storage_action_size_bytes() {
    lsblk -bdnro SIZE "$1" 2>/dev/null | head -n1
}

storage_action_mountable_filesystem() {
    case "$1" in
        xfs|ext4|btrfs|ntfs|vfat|exfat) return 0 ;;
        *) return 1 ;;
    esac
}

storage_action_managed_filesystem() {
    case "$1" in
        xfs|ext4|btrfs) return 0 ;;
        *) return 1 ;;
    esac
}

storage_action_mount_type() {
    case "$1" in
        ntfs) printf 'ntfs-3g\n' ;;
        xfs|ext4|btrfs|vfat|exfat) printf '%s\n' "$1" ;;
        *) return 1 ;;
    esac
}

storage_action_mount_options() {
    case "$1" in
        xfs|ext4|btrfs)
            printf 'noatime\n'
            ;;
        ntfs)
            printf 'rw,noatime,uid=0,gid=0,fmask=0133,dmask=0022,windows_names\n'
            ;;
        vfat)
            printf 'rw,noatime,uid=0,gid=0,fmask=0133,dmask=0022,utf8=1\n'
            ;;
        exfat)
            printf 'rw,noatime,uid=0,gid=0,fmask=0133,dmask=0022\n'
            ;;
        *)
            return 1
            ;;
    esac
}

storage_action_mount_device() {
    local device="$1" mountpoint="$2" filesystem="$3" mount_type mount_options
    mount_type="$(storage_action_mount_type "${filesystem}")" || return 1
    mount_options="$(storage_action_mount_options "${filesystem}")" || return 1
    nsenter --mount=/proc/1/ns/mnt -- mount -t "${mount_type}" -o "${mount_options}" -- "${device}" "${mountpoint}"
}

# Compatibility alias for storage-management paths that intentionally remain
# restricted to Linux-native managed filesystems.
storage_action_supported_filesystem() {
    storage_action_managed_filesystem "$1"
}

storage_action_path_on_mount() {
    local path="$1" mountpoint="$2"
    [[ -n ${path} && -n ${mountpoint} ]] || return 1
    [[ ${path} == "${mountpoint}" || ${path} == "${mountpoint}/"* ]]
}

storage_action_role() {
    local device="$1" filesystem="$2" uuid="$3" mountpoint="$4"
    local roles=()

    if [[ ${filesystem} == swap ]]; then
        printf 'Swap\n'
        return 0
    fi
    if storage_action_is_system_partition "${device}"; then
        printf 'System\n'
        return 0
    fi

    if [[ ${STORAGE_ACTION_CONFIGURED} == true ]]; then
        if [[ -n ${DATA_EXPECTED_UUID:-} && -n ${uuid} && ${uuid} == "${DATA_EXPECTED_UUID}" ]] \
            || [[ -n ${DATA_MOUNT_POINT:-} && ${mountpoint} == "${DATA_MOUNT_POINT}" ]] \
            || storage_action_path_on_mount "${DATA_PATH:-}" "${mountpoint}"; then
            roles+=("Minecraft")
        fi
        if [[ -n ${BACKUP_EXPECTED_UUID:-} && -n ${uuid} && ${uuid} == "${BACKUP_EXPECTED_UUID}" ]] \
            || [[ -n ${BACKUP_MOUNT_POINT:-} && ${mountpoint} == "${BACKUP_MOUNT_POINT}" ]] \
            || storage_action_path_on_mount "${BACKUP_PATH:-}" "${mountpoint}"; then
            roles+=("Backups")
        fi
    fi

    if (( ${#roles[@]} > 0 )); then
        local joined=''
        local item
        for item in "${roles[@]}"; do
            if [[ -n ${joined} ]]; then
                joined+=" + "
            fi
            joined+="${item}"
        done
        printf '%s\n' "${joined}"
    elif [[ -z ${filesystem} ]]; then
        printf 'Not formatted\n'
    elif [[ -n ${mountpoint} ]]; then
        printf 'Mounted\n'
    else
        printf 'Available\n'
    fi
}

storage_action_validate_target() {
    local device="$1" filesystem type mountpoint mounts
    storage_require_identified_system_disk >/dev/null 2>&1 || {
        echo 'ERROR: JustVoxel could not identify the system disk safely.' >&2
        return 1
    }
    type="$(lsblk -dnro TYPE "${device}" 2>/dev/null || true)"
    if [[ ${type} == disk ]]; then
        storage_validate_disk "${device}" || return 1
        storage_disk_is_system "${device}" && return 1
        # Whole-device filesystems must not overlap a partitioned layout.
        [[ $(lsblk -nrpo NAME,TYPE "${device}" | wc -l) -eq 1 ]] || return 1
        storage_action_mountable_filesystem "$(storage_action_filesystem "${device}")" || return 1
    else
        storage_validate_partition "${device}" || return 1
        storage_validate_disk "$(storage_action_parent_disk "${device}")" || return 1
        if storage_action_is_system_partition "${device}"; then
            echo 'ERROR: JustVoxel system partitions are protected.' >&2
            return 1
        fi
    fi
    # Mapped children/holders must never be overwritten by raw-device actions.
    [[ $(lsblk -nrpo NAME,TYPE "${device}" | wc -l) -eq 1 ]] || return 1
    mounts="$(storage_action_host_mountpoints "${device}")" || return 1
    while IFS= read -r mountpoint; do
        storage_mount_is_critical "${mountpoint}" && return 1
    done <<< "${mounts}"
    filesystem="$(storage_action_filesystem "${device}")"
    if [[ ${filesystem} == swap ]]; then
        echo 'ERROR: swap partitions are not managed from the storage browser.' >&2
        return 1
    fi
}

storage_action_validate_mountpoint() {
    local mountpoint="$1"
    validate_storage_path "${mountpoint}" || {
        echo 'ERROR: mount point must be an absolute path without spaces or shell-special characters.' >&2
        return 1
    }
    storage_mount_is_critical "${mountpoint}" && {
        echo "ERROR: refusing to manage critical mount point: ${mountpoint}" >&2
        return 1
    }
    storage_action_host_mountpoint_occupied "${mountpoint}" && {
        echo "ERROR: mount point is already occupied: ${mountpoint}" >&2
        return 1
    }
    if [[ -d ${mountpoint} ]] && find "${mountpoint}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null | grep -q .; then
        echo "ERROR: mount point is not empty: ${mountpoint}" >&2
        return 1
    fi
}

storage_action_validate_deletion() {
    local device="$1" parent number record table
    storage_action_validate_target "${device}" || return 1
    storage_validate_partition "${device}" || return 1
    parent="$(storage_action_parent_disk "${device}")" || return 1
    storage_validate_disk "${parent}" || return 1
    number="$(cat "/sys/class/block/${device##*/}/partition")" || return 1
    [[ ${number} =~ ^[1-9][0-9]*$ ]] || return 1
    table="$(storage_partition_table_type "${parent}")" || return 1
    [[ ${table} == gpt || ${table} == msdos ]] || return 1
    record="$(parted -s -m "${parent}" print | awk -F: -v n="${number}" '$1 == n {print; exit}')" || return 1
    [[ -n ${record} ]] || return 1
    # Removing an MBR extended partition would also remove logical partitions.
    [[ ${table} != msdos || $(cut -d: -f5 <<< "${record}") != extended ]]
}

# Keep the browser's MiB selection identity, but use exact sectors for mkpart.
# Parted's end sector is inclusive; alignment is calculated on the exclusive end.
storage_action_plan_partition() {
    local device="$1" wanted="$2" requested="$3"
    local sectors display recheck index record start end length logical physical minimum optimal offset
    local grain value a b remainder aligned_start aligned_limit count
    sectors="$(LC_ALL=C parted -s -m "${device}" unit s print free)" || return 1
    display="$(LC_ALL=C parted -s -m "${device}" unit MiB print free)" || return 1
    recheck="$(LC_ALL=C parted -s -m "${device}" unit s print free)" || return 1
    [[ ${sectors} == "${recheck}" ]] || return 1
    # Matching record positions must describe the same layout in both units.
    [[ $(awk -F: 'NR > 2 {print $1, ($5 == "free;")}' <<< "${sectors}") == \
       "$(awk -F: 'NR > 2 {print $1, ($5 == "free;")}' <<< "${display}")" ]] || return 1
    index="$(awk -F: -v wanted="${wanted}" '$5 == "free;" && $2 == wanted {n++; row=NR} END {if (n == 1) print row}' <<< "${display}")"
    [[ -n ${index} ]] || return 1
    record="$(awk -v row="${index}" 'NR == row' <<< "${sectors}")"
    IFS=: read -r _ start end length value <<< "${record}"
    [[ ${value} == 'free;' && ${start} =~ ^[0-9]+s$ && ${end} =~ ^[0-9]+s$ && ${length} =~ ^[0-9]+s$ ]] || return 1
    start="${start%s}"; end="${end%s}"; length="${length%s}"
    [[ ${#start} -le 15 && ${#end} -le 15 && ${#length} -le 15 ]] || return 1
    start=$((10#${start})); end=$((10#${end})); length=$((10#${length}))
    (( end >= start && length == end - start + 1 )) || return 1
    logical="$(blockdev --getss "${device}")" || return 1
    physical="$(blockdev --getpbsz "${device}")" || return 1
    minimum="$(blockdev --getiomin "${device}")" || return 1
    optimal="$(blockdev --getioopt "${device}")" || return 1
    offset="$(blockdev --getalignoff "${device}")" || return 1
    for value in "${logical}" "${physical}" "${minimum}" "${optimal}" "${offset}"; do
        [[ ${value} =~ ^[0-9]+$ && ${#value} -le 10 ]] || return 1
    done
    (( logical >= 512 && logical <= 65536 && 1048576 % logical == 0 &&
       end < 9223372036854775807 / logical && offset % logical == 0 )) || return 1
    # A multiple of 1 MiB and every advertised I/O grain satisfies optimal
    # alignment, including devices with an alignment offset. Unknown topology
    # fails closed rather than letting parted choose a point outside the extent.
    grain=1048576
    for value in "${physical}" "${minimum}" "${optimal}"; do
        (( value == 0 )) && continue
        (( value % logical == 0 )) || return 1
        a=${grain}; b=${value}
        while (( b > 0 )); do
            remainder=$((a % b)); a=${b}; b=${remainder}
        done
        (( grain / a <= 1099511627776 / value )) || return 1
        grain=$((grain / a * value))
        (( grain > 0 && grain <= 1099511627776 )) || return 1
    done
    grain=$((grain / logical)); offset=$((offset / logical))
    aligned_start=$((start + (grain - (start + offset) % grain) % grain))
    aligned_limit=$((end + 1 - (end + 1 + offset) % grain))
    (( aligned_limit > aligned_start )) || return 1
    if [[ ${requested} == all ]]; then
        count=$((aligned_limit - aligned_start))
    elif [[ ${requested} =~ ^[1-9][0-9]*$ && ${#requested} -le 9 ]]; then
        count=$((requested * (1073741824 / logical)))
        # Fixed sizes are exact from the aligned start, with an inclusive end.
        (( count <= aligned_limit - aligned_start )) || return 1
    else
        return 1
    fi
    (( count >= 1073741824 / logical )) || return 1
    STORAGE_ACTION_START="${aligned_start}s"
    STORAGE_ACTION_END="$((aligned_start + count - 1))s"
    STORAGE_ACTION_SIZE=$((count * logical))
    STORAGE_ACTION_GEOMETRY="$(printf '%s\n' "${sectors}" "${logical}:${physical}:${minimum}:${optimal}:${offset}:${grain}" | sha256sum | awk '{print $1}')"
}

storage_action_fingerprint() {
    local device="$1" type
    type="$(lsblk -dnro TYPE "${device}" 2>/dev/null || true)"
    {
        if [[ -r ${JV_CONFIG} ]]; then
            sha256sum "${JV_CONFIG}"
        fi
        if [[ -n ${2:-} ]]; then
            printf 'creation-geometry=%s\n' "$2"
        fi
        printf 'device=%s\n' "$(storage_action_real_device "${device}")"
        printf 'host-mounts=%s\n' "$(storage_action_host_mountpoints "${device}")"
        lsblk -b -P -o PATH,PKNAME,TYPE,SIZE,FSTYPE,UUID,PARTUUID,MOUNTPOINTS,START,RO,MODEL,SERIAL,WWN,TRAN "${device}" 2>/dev/null || true
        blkid "${device}" 2>/dev/null || true
        if [[ ${type} == part ]]; then
            parted -s -m "$(storage_action_parent_disk "${device}")" unit MiB print free 2>/dev/null || true
        fi
        if [[ ${type} == disk ]]; then
            parted -s -m "${device}" unit MiB print free 2>/dev/null || true
        fi
    } | sha256sum | awk '{print $1}'
}
