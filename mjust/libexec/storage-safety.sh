#!/usr/bin/bash
# Safety/compatibility overrides layered on the original storage implementation.

storage_write_local_fstab() {
    local uuid="$1" mountpoint="$2" fstype="$3" passno=0
    [[ ${fstype} == ext4 ]] && passno=2
    storage_backup_fstab
    storage_remove_fstab_mountpoint "${mountpoint}"
    printf 'UUID=%s %s %s noatime,nofail,x-systemd.device-timeout=10s 0 %s\n' \
        "${uuid}" "${mountpoint}" "${fstype}" "${passno}" >> "${JV_FSTAB}"
    systemctl daemon-reload
}

storage_assert_writable_dir() {
    local path="$1" probe
    mkdir -p -- "${path}" || return 1
    probe="$(mktemp "${path}/.justvoxel-write-test.XXXXXX" 2>/dev/null)" || {
        echo "ERROR: storage path is not actually writable: ${path}" >&2
        return 1
    }
    rm -f -- "${probe}"
}

storage_target_path() {
    local purpose="$1" default_path
    if [[ ${purpose} == data ]]; then
        default_path="${STORAGE_MOUNT_POINT}/minecraft"
        STORAGE_PATH="$(prompt_default 'Minecraft data directory on this storage' "${default_path}")"
    else
        default_path="${STORAGE_MOUNT_POINT}/minecraft"
        STORAGE_PATH="$(prompt_default 'Backup directory on this storage' "${default_path}")"
    fi
    STORAGE_PATH="$(realpath -m -- "${STORAGE_PATH}")"
    validate_storage_path "${STORAGE_PATH}" || {
        echo 'ERROR: invalid storage directory.' >&2
        return 1
    }
    case "${STORAGE_PATH}/" in
        "${STORAGE_MOUNT_POINT}/"*) ;;
        *) echo 'ERROR: selected directory is outside the storage mount.' >&2; return 1 ;;
    esac
    if [[ ${purpose} == backup ]]; then
        storage_assert_writable_dir "${STORAGE_PATH}"
    fi
}

storage_prepare_existing_partition() {
    local purpose="$1" partition fstype mounted default_mount
    storage_show_devices
    read -r -p 'Existing partition to use: ' partition
    storage_validate_partition "${partition}"

    mounted="$(lsblk -nro MOUNTPOINT "${partition}" 2>/dev/null | head -n1)"
    if [[ -n ${mounted} ]] && storage_mount_is_critical "${mounted}"; then
        echo "ERROR: refusing to use critical mounted partition: ${partition} -> ${mounted}" >&2
        return 1
    fi

    fstype="$(blkid -s TYPE -o value "${partition}" 2>/dev/null || true)"
    if [[ -z ${fstype} ]]; then
        echo "Partition ${partition} has no detected filesystem."
        storage_confirm_phrase "FORMAT ${partition}" || { echo 'Cancelled.'; return 1; }
        mkfs.xfs -f -L JUSTVOXEL_STORAGE "${partition}"
        fstype=xfs
    fi

    case "${fstype}" in
        xfs|ext4|btrfs) ;;
        *) echo "ERROR: unsupported local filesystem: ${fstype}" >&2; return 1 ;;
    esac

    if [[ -n ${mounted} ]]; then
        STORAGE_MOUNT_POINT="$(realpath -m -- "${mounted}")"
        storage_validate_mountpoint_path "${STORAGE_MOUNT_POINT}"
        STORAGE_EXPECTED_UUID="$(jv_exact_mount_identity UUID "${STORAGE_MOUNT_POINT}" 2>/dev/null || true)"
        STORAGE_EXPECTED_SOURCE="$(jv_exact_mount_identity SOURCE "${STORAGE_MOUNT_POINT}" 2>/dev/null || true)"
        [[ -n ${STORAGE_EXPECTED_UUID} ]] || {
            echo 'ERROR: mounted local filesystem has no UUID.' >&2
            return 1
        }
        echo 'Existing local mount adopted as-is; its current mount configuration is not rewritten.'
    else
        if [[ ${purpose} == data ]]; then
            default_mount=/var/mnt/justvoxel-data
        else
            default_mount=/var/mnt/justvoxel-backup
        fi
        STORAGE_MOUNT_POINT="$(prompt_default 'Mount point' "${default_mount}")"
        STORAGE_MOUNT_POINT="$(realpath -m -- "${STORAGE_MOUNT_POINT}")"
        storage_mount_local "${partition}" "${STORAGE_MOUNT_POINT}"
    fi
    STORAGE_TYPE=partition
    storage_target_path "${purpose}"
}

storage_prepare_nfs() {
    local source mountpoint actual_source
    source="$(prompt_default 'NFS source (server:/export)' 'server:/export')"
    [[ ${source} == *:* && ${source} != *' '* ]] || { echo 'ERROR: invalid NFS source.' >&2; return 1; }
    mountpoint="$(prompt_default 'NFS mount point' '/var/mnt/justvoxel-backup')"
    mountpoint="$(realpath -m -- "${mountpoint}")"
    storage_validate_mountpoint_path "${mountpoint}"

    if mountpoint -q -- "${mountpoint}"; then
        actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
        [[ ${actual_source} == "${source}" ]] || {
            echo "ERROR: ${mountpoint} is already mounted from ${actual_source:-unknown}, not ${source}." >&2
            return 1
        }
        echo 'Existing NFS mount adopted as-is; its current mount configuration is not rewritten.'
    else
        install -d -m0755 -o root -g root "${mountpoint}"
        storage_write_network_fstab "${source}" "${mountpoint}" nfs 'rw,_netdev,nofail,x-systemd.mount-timeout=20s'
        if ! mount "${mountpoint}"; then
            storage_restore_fstab_backup
            echo 'ERROR: NFS mount failed.' >&2
            return 1
        fi
        actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
    fi
    [[ -n ${actual_source} ]] || { echo 'ERROR: NFS source could not be verified.' >&2; return 1; }
    STORAGE_TYPE=nfs
    STORAGE_MOUNT_POINT="${mountpoint}"
    STORAGE_EXPECTED_UUID=''
    STORAGE_EXPECTED_SOURCE="${actual_source}"
    storage_target_path backup
}

storage_prepare_smb() {
    local source mountpoint username password domain credentials options actual_source
    source="$(prompt_default 'SMB source (//server/share)' '//server/share')"
    [[ ${source} == //*/* && ${source} != *' '* ]] || { echo 'ERROR: invalid SMB source.' >&2; return 1; }
    mountpoint="$(prompt_default 'SMB mount point' '/var/mnt/justvoxel-backup')"
    mountpoint="$(realpath -m -- "${mountpoint}")"
    storage_validate_mountpoint_path "${mountpoint}"

    if mountpoint -q -- "${mountpoint}"; then
        actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
        [[ ${actual_source} == "${source}" ]] || {
            echo "ERROR: ${mountpoint} is already mounted from ${actual_source:-unknown}, not ${source}." >&2
            return 1
        }
        echo 'Existing SMB/CIFS mount adopted as-is; its current mount configuration is not rewritten.'
    else
        install -d -m0755 -o root -g root "${mountpoint}"
        read -r -p 'SMB username: ' username
        read -r -s -p 'SMB password: ' password
        echo
        read -r -p 'SMB domain/workgroup (optional): ' domain
        [[ -n ${username} && ${username} != *$'\n'* && ${username} != *$'\r'* \
            && ${password} != *$'\n'* && ${password} != *$'\r'* \
            && ${domain} != *$'\n'* && ${domain} != *$'\r'* ]] || {
            echo 'ERROR: SMB credentials are invalid.' >&2
            return 1
        }
        install -d -m0700 -o root -g root /etc/justvoxel
        credentials="${JV_SMB_CREDENTIALS}"
        umask 077
        {
            printf 'username=%s\n' "${username}"
            printf 'password=%s\n' "${password}"
            [[ -n ${domain} ]] && printf 'domain=%s\n' "${domain}"
        } > "${credentials}"
        chown root:root "${credentials}"
        chmod 0600 "${credentials}"

        options="credentials=${credentials},vers=3.0,rw,_netdev,nofail,x-systemd.mount-timeout=20s,uid=0,gid=0,file_mode=0600,dir_mode=0700"
        storage_write_network_fstab "${source}" "${mountpoint}" cifs "${options}"
        if ! mount "${mountpoint}"; then
            storage_restore_fstab_backup
            echo 'ERROR: SMB mount failed.' >&2
            return 1
        fi
        actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
    fi
    [[ -n ${actual_source} ]] || { echo 'ERROR: SMB source could not be verified.' >&2; return 1; }
    STORAGE_TYPE=smb
    STORAGE_MOUNT_POINT="${mountpoint}"
    STORAGE_EXPECTED_UUID=''
    STORAGE_EXPECTED_SOURCE="${actual_source}"
    storage_target_path backup
}
