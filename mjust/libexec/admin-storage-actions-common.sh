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
    lsblk -s -npo NAME,TYPE "${real}" 2>/dev/null | awk '$2 == "disk" {print $1; exit}'
}

storage_action_is_system_partition() {
    local device parent
    device="$1"
    parent="$(storage_action_parent_disk "${device}" || true)"
    [[ -n ${parent} ]] || return 1
    storage_disk_is_system "${parent}"
}

storage_action_mountpoint() {
    lsblk -nro MOUNTPOINTS "$1" 2>/dev/null | sed '/^$/d' | head -n1
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

# Compatibility alias for existing storage-management paths.
# Step 2 will opt generic mount operations into storage_action_mountable_filesystem.
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
    local device="$1" filesystem
    storage_require_identified_system_disk >/dev/null 2>&1 || {
        echo 'ERROR: JustVoxel could not identify the system disk safely.' >&2
        return 1
    }
    storage_validate_partition "${device}" >/dev/null 2>&1 || {
        echo 'ERROR: target must be a writable partition.' >&2
        return 1
    }
    if storage_action_is_system_partition "${device}"; then
        echo 'ERROR: partitions on the JustVoxel system disk are protected.' >&2
        return 1
    fi
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
    mountpoint -q -- "${mountpoint}" && {
        echo "ERROR: mount point is already occupied: ${mountpoint}" >&2
        return 1
    }
    if [[ -d ${mountpoint} ]] && find "${mountpoint}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null | grep -q .; then
        echo "ERROR: mount point is not empty: ${mountpoint}" >&2
        return 1
    fi
}

storage_action_fingerprint() {
    local device="$1"
    {
        printf 'device=%s\n' "$(storage_action_real_device "${device}")"
        lsblk -b -P -o PATH,PKNAME,TYPE,SIZE,FSTYPE,UUID,PARTUUID,MOUNTPOINTS,START,RO,MODEL,SERIAL,WWN,TRAN "${device}" 2>/dev/null || true
        blkid "${device}" 2>/dev/null || true
    } | sha256sum | awk '{print $1}'
}
