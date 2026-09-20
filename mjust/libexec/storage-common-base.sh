#!/usr/bin/bash
set -euo pipefail

if ! declare -F jv_variant_name >/dev/null; then
    source /usr/libexec/justvoxel/mjust/common.sh
fi

readonly JV_FSTAB=/etc/fstab
readonly JV_SMB_CREDENTIALS=/etc/justvoxel/smb-backup.credentials

storage_variant() {
    jv_variant_name
}

storage_is_vm() {
    jv_variant_is_vm
}

storage_is_hwe() {
    jv_variant_is_hwe
}

storage_system_disks() {
    local target source real
    for target in / /boot /boot/efi /var; do
        source="$(findmnt -n -o SOURCE --target "${target}" 2>/dev/null || true)"
        [[ ${source} == /dev/* ]] || continue
        real="$(readlink -f -- "${source}" 2>/dev/null || true)"
        [[ -n ${real} ]] || continue
        lsblk -s -npo NAME,TYPE "${real}" 2>/dev/null \
            | awk '$2 == "disk" {print $1}'
    done | sort -u
}

storage_show_devices() {
    echo 'Detected block storage:'
    lsblk -e7 -o NAME,PATH,TYPE,SIZE,FSTYPE,LABEL,UUID,MOUNTPOINTS,MODEL,TRAN,RO
}

storage_disk_is_system() {
    local requested real system_disk
    requested="$(readlink -f -- "$1" 2>/dev/null || true)"
    [[ -n ${requested} ]] || return 1
    while IFS= read -r system_disk; do
        [[ -n ${system_disk} ]] || continue
        real="$(readlink -f -- "${system_disk}" 2>/dev/null || true)"
        [[ ${requested} == "${real}" ]] && return 0
    done < <(storage_system_disks)
    return 1
}

storage_require_identified_system_disk() {
    local count
    count="$(storage_system_disks | sed '/^$/d' | wc -l)"
    if (( count < 1 )); then
        echo 'ERROR: JustVoxel could not identify the system disk safely.' >&2
        echo 'Destructive storage provisioning is disabled until the system disk can be identified.' >&2
        return 1
    fi
}

storage_validate_disk() {
    local device="$1" type ro
    [[ -b ${device} ]] || { echo "ERROR: not a block device: ${device}" >&2; return 1; }
    type="$(lsblk -dnro TYPE "${device}" 2>/dev/null || true)"
    ro="$(lsblk -dnro RO "${device}" 2>/dev/null || true)"
    [[ ${type} == disk && ${ro} == 0 ]] || {
        echo "ERROR: target must be a writable whole disk: ${device}" >&2
        return 1
    }
}

storage_validate_partition() {
    local device="$1" type ro
    [[ -b ${device} ]] || { echo "ERROR: not a block device: ${device}" >&2; return 1; }
    type="$(lsblk -dnro TYPE "${device}" 2>/dev/null || true)"
    ro="$(lsblk -dnro RO "${device}" 2>/dev/null || true)"
    [[ ${type} == part && ${ro} == 0 ]] || {
        echo "ERROR: target must be a writable partition: ${device}" >&2
        return 1
    }
}

storage_has_mounted_children() {
    lsblk -nrpo MOUNTPOINT "$1" 2>/dev/null | sed '/^$/d' | grep -q .
}

storage_mount_is_critical() {
    case "$1" in
        /|/boot|/boot/efi|/var) return 0 ;;
        *) return 1 ;;
    esac
}

storage_confirm_phrase() {
    local expected="$1" answer
    echo
    echo 'DESTRUCTIVE OPERATION'
    echo "Type exactly: ${expected}"
    read -r -p '> ' answer
    [[ ${answer} == "${expected}" ]]
}

storage_backup_fstab() {
    [[ -e ${JV_FSTAB} ]] || touch "${JV_FSTAB}"
    cp -a "${JV_FSTAB}" "${JV_FSTAB}.justvoxel.bak"
}

storage_restore_fstab_backup() {
    if [[ -f ${JV_FSTAB}.justvoxel.bak ]]; then
        cp -a "${JV_FSTAB}.justvoxel.bak" "${JV_FSTAB}"
        systemctl daemon-reload
    fi
}

storage_remove_fstab_mountpoint() {
    local mountpoint="$1" tmp
    tmp="$(mktemp /etc/.fstab.justvoxel.XXXXXX)"
    awk -v mp="${mountpoint}" '
        /^[[:space:]]*#/ || NF < 2 || $2 != mp {print}
    ' "${JV_FSTAB}" > "${tmp}"
    install -o root -g root -m0644 "${tmp}" "${JV_FSTAB}"
    rm -f "${tmp}"
}

storage_write_local_fstab() {
    local uuid="$1" mountpoint="$2" fstype="$3"
    storage_backup_fstab
    storage_remove_fstab_mountpoint "${mountpoint}"
    printf 'UUID=%s %s %s noatime,nofail,x-systemd.device-timeout=10s 0 2\n' \
        "${uuid}" "${mountpoint}" "${fstype}" >> "${JV_FSTAB}"
    systemctl daemon-reload
}

storage_write_network_fstab() {
    local source="$1" mountpoint="$2" fstype="$3" options="$4"
    storage_backup_fstab
    storage_remove_fstab_mountpoint "${mountpoint}"
    printf '%s %s %s %s 0 0\n' "${source}" "${mountpoint}" "${fstype}" "${options}" >> "${JV_FSTAB}"
    systemctl daemon-reload
}

storage_validate_mountpoint_path() {
    local mountpoint="$1"
    validate_storage_path "${mountpoint}" || {
        echo 'ERROR: mount point must be an absolute path without spaces or shell-special characters.' >&2
        return 1
    }
    storage_mount_is_critical "${mountpoint}" && {
        echo "ERROR: refusing to manage critical mount point: ${mountpoint}" >&2
        return 1
    }
    return 0
}

storage_mount_local() {
    local device="$1" mountpoint="$2" uuid fstype actual_uuid
    uuid="$(blkid -s UUID -o value "${device}" 2>/dev/null || true)"
    fstype="$(blkid -s TYPE -o value "${device}" 2>/dev/null || true)"
    [[ -n ${uuid} && -n ${fstype} ]] || {
        echo "ERROR: filesystem UUID/type could not be determined for ${device}" >&2
        return 1
    }
    case "${fstype}" in
        xfs|ext4|btrfs) ;;
        *)
            echo "ERROR: existing filesystem '${fstype}' is not supported for JustVoxel local storage adoption." >&2
            echo 'Supported existing local filesystems: XFS, ext4, Btrfs.' >&2
            return 1
            ;;
    esac
    storage_validate_mountpoint_path "${mountpoint}"
    if mountpoint -q -- "${mountpoint}"; then
        echo "ERROR: mount point is already occupied: ${mountpoint}" >&2
        return 1
    fi
    install -d -m0755 -o root -g root "${mountpoint}"
    storage_write_local_fstab "${uuid}" "${mountpoint}" "${fstype}"
    if ! mount "${mountpoint}"; then
        storage_restore_fstab_backup
        echo "ERROR: mount failed: ${mountpoint}" >&2
        return 1
    fi
    mountpoint -q -- "${mountpoint}" || { echo "ERROR: mount failed: ${mountpoint}" >&2; return 1; }
    actual_uuid="$(findmnt -n -o UUID --target "${mountpoint}" 2>/dev/null || true)"
    [[ ${actual_uuid} == "${uuid}" ]] || {
        echo "ERROR: mounted UUID mismatch at ${mountpoint}" >&2
        return 1
    }
    STORAGE_MOUNT_POINT="${mountpoint}"
    STORAGE_EXPECTED_UUID="${uuid}"
    STORAGE_EXPECTED_SOURCE="$(findmnt -n -o SOURCE --target "${mountpoint}")"
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
}

storage_prepare_whole_disk() {
    local purpose="$1" disk model transport size partition label default_mount
    local -a candidates=()

    storage_require_identified_system_disk
    storage_show_devices
    echo
    echo 'Safe whole-disk candidates (system disks and mounted disks are excluded):'
    while IFS= read -r disk; do
        [[ -n ${disk} ]] || continue
        storage_validate_disk "${disk}" || continue
        storage_disk_is_system "${disk}" && continue
        storage_has_mounted_children "${disk}" && continue
        candidates+=("${disk}")
        model="$(lsblk -dnro MODEL "${disk}" | xargs || true)"
        transport="$(lsblk -dnro TRAN "${disk}" | xargs || true)"
        size="$(lsblk -dnro SIZE "${disk}" | xargs || true)"
        printf '  %s  %s  %s  %s\n' "${disk}" "${size:-?}" "${transport:-unknown}" "${model:-unknown}"
    done < <(lsblk -dnpo NAME,TYPE | awk '$2 == "disk" {print $1}')

    if (( ${#candidates[@]} == 0 )); then
        if storage_is_vm; then
            echo 'No safe secondary disk was found.' >&2
            echo 'Add a second virtual disk in the hypervisor, then rerun this storage operation.' >&2
        else
            echo 'No safe unused whole disk was found. Attach an internal or USB disk, or use another storage method.' >&2
        fi
        return 1
    fi

    read -r -p 'Whole disk to erase and provision: ' disk
    [[ " ${candidates[*]} " == *" ${disk} "* ]] || {
        echo 'ERROR: device is not in the safe candidate list.' >&2
        return 1
    }

    echo
    lsblk -o NAME,PATH,TYPE,SIZE,FSTYPE,LABEL,UUID,MOUNTPOINTS,MODEL,TRAN "${disk}"
    storage_confirm_phrase "ERASE ${disk}" || { echo 'Cancelled.'; return 1; }

    wipefs -a -- "${disk}"
    parted -s -a optimal -- "${disk}" mklabel gpt
    parted -s -a optimal -- "${disk}" mkpart primary xfs 1MiB 100%
    partprobe "${disk}"
    udevadm settle
    partition="$(lsblk -nrpo NAME,TYPE "${disk}" | awk '$2 == "part" {print $1; exit}')"
    [[ -b ${partition} ]] || { echo 'ERROR: new partition did not appear.' >&2; return 1; }

    if [[ ${purpose} == data ]]; then
        label=JUSTVOXEL_DATA
        default_mount=/var/mnt/justvoxel-data
    else
        label=JUSTVOXEL_BACKUP
        default_mount=/var/mnt/justvoxel-backup
    fi
    mkfs.xfs -f -L "${label}" "${partition}"
    STORAGE_MOUNT_POINT="$(prompt_default 'Mount point' "${default_mount}")"
    STORAGE_MOUNT_POINT="$(realpath -m -- "${STORAGE_MOUNT_POINT}")"
    storage_mount_local "${partition}" "${STORAGE_MOUNT_POINT}"
    STORAGE_TYPE=disk
    storage_target_path "${purpose}"
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
        STORAGE_EXPECTED_UUID="$(findmnt -n -o UUID --target "${STORAGE_MOUNT_POINT}" 2>/dev/null || true)"
        STORAGE_EXPECTED_SOURCE="$(findmnt -n -o SOURCE --target "${STORAGE_MOUNT_POINT}" 2>/dev/null || true)"
        [[ -n ${STORAGE_EXPECTED_UUID} ]] || { echo 'ERROR: mounted local filesystem has no UUID.' >&2; return 1; }
        storage_validate_mountpoint_path "${STORAGE_MOUNT_POINT}"
        storage_write_local_fstab "${STORAGE_EXPECTED_UUID}" "${STORAGE_MOUNT_POINT}" "${fstype}"
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

storage_prepare_free_partition() {
    local purpose="$1" disk table free_line start end free_size size_gib part_end
    local before after partition default_mount label

    storage_require_identified_system_disk
    storage_show_devices
    echo
    echo 'This operation uses only already-unallocated disk space. It never shrinks an existing filesystem.'
    read -r -p 'Disk containing unallocated free space: ' disk
    storage_validate_disk "${disk}"

    table="$(parted -s -m "${disk}" print 2>/dev/null | awk -F: 'NR == 2 {print $6}' || true)"
    [[ -n ${table} && ${table} != unknown ]] || {
        echo 'ERROR: no usable partition table was found. Use whole-disk provisioning for an empty disk.' >&2
        return 1
    }

    parted -s "${disk}" unit MiB print free
    free_line="$(parted -s -m "${disk}" unit MiB print free 2>/dev/null \
        | awk -F: '$5 == "free;" {s=$4; gsub("MiB","",s); if ((s+0) > max) {max=s+0; line=$0}} END {print line}')"
    [[ -n ${free_line} ]] || { echo 'ERROR: no unallocated segment was found.' >&2; return 1; }
    IFS=: read -r _ start end free_size _ <<< "${free_line}"
    free_size="${free_size%MiB}"
    awk -v n="${free_size}" 'BEGIN {exit !(n >= 1024)}' || {
        echo 'ERROR: the largest unallocated segment is smaller than 1 GiB.' >&2
        return 1
    }

    echo "Largest unallocated segment: ${start} -> ${end} (${free_size} MiB)"
    size_gib="$(prompt_default 'Partition size in GiB, or all' 'all')"
    if [[ ${size_gib} == all ]]; then
        part_end="${end}"
    elif [[ ${size_gib} =~ ^[1-9][0-9]*$ ]]; then
        awk -v free="${free_size}" -v requested="${size_gib}" 'BEGIN {exit !((requested * 1024) <= free)}' || {
            echo 'ERROR: requested partition is larger than the free segment.' >&2
            return 1
        }
        part_end="$(awk -v s="${start%MiB}" -v g="${size_gib}" 'BEGIN {printf "%.2fMiB", s + (g * 1024)}')"
    else
        echo 'ERROR: enter an integer GiB value or all.' >&2
        return 1
    fi

    echo
    echo "Disk: ${disk}"
    echo "New partition: ${start} -> ${part_end}"
    storage_confirm_phrase "CREATE PARTITION ${disk}" || { echo 'Cancelled.'; return 1; }

    before="$(mktemp)"
    after="$(mktemp)"
    lsblk -nrpo NAME,TYPE "${disk}" | awk '$2 == "part" {print $1}' | sort > "${before}"
    parted -s -a optimal -- "${disk}" mkpart primary xfs "${start}" "${part_end}"
    partprobe "${disk}"
    udevadm settle
    lsblk -nrpo NAME,TYPE "${disk}" | awk '$2 == "part" {print $1}' | sort > "${after}"
    partition="$(comm -13 "${before}" "${after}" | head -n1)"
    rm -f "${before}" "${after}"
    [[ -b ${partition} ]] || { echo 'ERROR: new partition could not be identified safely.' >&2; return 1; }

    if [[ ${purpose} == data ]]; then
        label=JUSTVOXEL_DATA
        default_mount=/var/mnt/justvoxel-data
    else
        label=JUSTVOXEL_BACKUP
        default_mount=/var/mnt/justvoxel-backup
    fi
    mkfs.xfs -f -L "${label}" "${partition}"
    STORAGE_MOUNT_POINT="$(prompt_default 'Mount point' "${default_mount}")"
    STORAGE_MOUNT_POINT="$(realpath -m -- "${STORAGE_MOUNT_POINT}")"
    storage_mount_local "${partition}" "${STORAGE_MOUNT_POINT}"
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
    install -d -m0755 -o root -g root "${mountpoint}"
    storage_write_network_fstab "${source}" "${mountpoint}" nfs 'rw,_netdev,nofail,x-systemd.mount-timeout=20s'
    if ! mount "${mountpoint}"; then
        storage_restore_fstab_backup
        echo 'ERROR: NFS mount failed.' >&2
        return 1
    fi
    mountpoint -q -- "${mountpoint}" || { echo 'ERROR: NFS mount failed.' >&2; return 1; }
    actual_source="$(findmnt -n -o SOURCE --target "${mountpoint}" 2>/dev/null || true)"
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
    read -r -p 'SMB username: ' username
    read -r -s -p 'SMB password: ' password
    echo
    read -r -p 'SMB domain/workgroup (optional): ' domain
    [[ -n ${username} && ${username} != *$'\n'* && ${password} != *$'\n'* ]] || {
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

    install -d -m0755 -o root -g root "${mountpoint}"
    options="credentials=${credentials},vers=3.0,rw,_netdev,nofail,x-systemd.mount-timeout=20s,uid=0,gid=0,file_mode=0600,dir_mode=0700"
    storage_write_network_fstab "${source}" "${mountpoint}" cifs "${options}"
    if ! mount "${mountpoint}"; then
        storage_restore_fstab_backup
        echo 'ERROR: SMB mount failed.' >&2
        return 1
    fi
    mountpoint -q -- "${mountpoint}" || { echo 'ERROR: SMB mount failed.' >&2; return 1; }
    actual_source="$(findmnt -n -o SOURCE --target "${mountpoint}" 2>/dev/null || true)"
    [[ -n ${actual_source} ]] || { echo 'ERROR: SMB source could not be verified.' >&2; return 1; }
    STORAGE_TYPE=smb
    STORAGE_MOUNT_POINT="${mountpoint}"
    STORAGE_EXPECTED_UUID=''
    STORAGE_EXPECTED_SOURCE="${actual_source}"
    storage_target_path backup
}

storage_apply_backup_globals() {
    BACKUP_TYPE="${STORAGE_TYPE}"
    BACKUP_MOUNT_POINT="${STORAGE_MOUNT_POINT}"
    BACKUP_EXPECTED_UUID="${STORAGE_EXPECTED_UUID}"
    BACKUP_EXPECTED_SOURCE="${STORAGE_EXPECTED_SOURCE}"
    BACKUP_PATH="${STORAGE_PATH}"
}

storage_prepare_backup_interactive() {
    local choice
    if storage_is_vm; then
        echo 'VM storage recommendation: use a second virtual disk for backups, or a network share.'
        choice="$(choose 'Backup storage' \
            'Provision a dedicated second virtual disk (recommended)' \
            'Use an existing filesystem/partition' \
            'NFS network share' \
            'SMB/CIFS network share' \
            'Directory on the system filesystem')"
        case "${choice}" in
            1) storage_prepare_whole_disk backup ;;
            2) storage_prepare_existing_partition backup ;;
            3) storage_prepare_nfs ;;
            4) storage_prepare_smb ;;
            5)
                STORAGE_TYPE=system
                STORAGE_MOUNT_POINT=''
                STORAGE_EXPECTED_UUID=''
                STORAGE_EXPECTED_SOURCE=''
                STORAGE_PATH="$(prompt_default 'Backup directory' '/var/lib/justvoxel/backups')"
                STORAGE_PATH="$(realpath -m -- "${STORAGE_PATH}")"
                validate_storage_path "${STORAGE_PATH}" || return 1
                ;;
        esac
    else
        choice="$(choose 'Backup storage' \
            'Provision a dedicated whole disk or USB drive' \
            'Use an existing filesystem/partition' \
            'Create a partition in already-unallocated disk space' \
            'NFS network share' \
            'SMB/CIFS network share' \
            'Directory on the system filesystem')"
        case "${choice}" in
            1) storage_prepare_whole_disk backup ;;
            2) storage_prepare_existing_partition backup ;;
            3) storage_prepare_free_partition backup ;;
            4) storage_prepare_nfs ;;
            5) storage_prepare_smb ;;
            6)
                STORAGE_TYPE=system
                STORAGE_MOUNT_POINT=''
                STORAGE_EXPECTED_UUID=''
                STORAGE_EXPECTED_SOURCE=''
                STORAGE_PATH="$(prompt_default 'Backup directory' '/var/lib/justvoxel/backups')"
                STORAGE_PATH="$(realpath -m -- "${STORAGE_PATH}")"
                validate_storage_path "${STORAGE_PATH}" || return 1
                ;;
        esac
    fi
    storage_apply_backup_globals
}

storage_prepare_data_target_interactive() {
    local choice
    choice="$(choose 'Minecraft data target' \
        'Provision a dedicated whole disk' \
        'Use an existing filesystem/partition' \
        'Create a partition in already-unallocated disk space' \
        'Cancel')"
    case "${choice}" in
        1) storage_prepare_whole_disk data ;;
        2) storage_prepare_existing_partition data ;;
        3) storage_prepare_free_partition data ;;
        4) return 1 ;;
    esac
}
