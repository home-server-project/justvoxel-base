json_error() {
    jq -n --arg error "$1" '{ok:false,error:$error,warnings:[]}'
}

json_warning_append() {
    local json="$1" message="$2"
    jq -c --arg message "${message}" '. + [$message]' <<< "${json}"
}

read_request() {
    REQUEST="$(cat)"
    jq -e 'type == "object"' >/dev/null 2>&1 <<< "${REQUEST}"
}

real_path() {
    local value="${1:-}"
    [[ -n ${value} ]] || return 0
    realpath -m -- "${value}" 2>/dev/null || true
}

nearest_existing_path() {
    local path="$1"
    while [[ ${path} != / && ! -e ${path} ]]; do
        path="$(dirname -- "${path}")"
    done
    [[ -e ${path} ]] || path=/
    printf '%s\n' "${path}"
}

available_bytes_for_path() {
    local probe
    probe="$(nearest_existing_path "$1")"
    df -B1 --output=avail "${probe}" 2>/dev/null | awk 'NR == 2 && $1 ~ /^[0-9]+$/ {print $1}'
}

filesystem_bytes_for_path() {
    local probe
    probe="$(nearest_existing_path "$1")"
    df -B1 --output=size "${probe}" 2>/dev/null | awk 'NR == 2 && $1 ~ /^[0-9]+$/ {print $1}'
}

source_for_path() {
    local probe
    probe="$(nearest_existing_path "$1")"
    findmnt -n -o SOURCE --target "${probe}" 2>/dev/null || true
}

disk_for_block_device() {
    local device real
    device="$1"
    real="$(readlink -f -- "${device}" 2>/dev/null || true)"
    [[ ${real} == /dev/* ]] || return 0
    lsblk -s -npo NAME,TYPE "${real}" 2>/dev/null | awk '$2 == "disk" {print $1; exit}'
}

disk_for_path() {
    local source
    source="$(source_for_path "$1")"
    [[ ${source} == /dev/* ]] || return 0
    disk_for_block_device "${source}"
}

data_disk() {
    disk_for_path "${DATA_PATH}"
}

path_within_mount() {
    local path="$1" mountpoint="$2"
    [[ ${path} == "${mountpoint}" || ${path} == "${mountpoint}/"* ]]
}

backup_path_is_safe() {
    local path="$1"
    validate_storage_path "${path}" || return 1
    [[ ${path} != "${DATA_PATH}" && ${path} != "${DATA_PATH}/"* ]]
}

write_probe() {
    local path="$1" probe
    probe="${path}/.justvoxel-write-test.$$"
    if ! : > "${probe}" 2>/dev/null; then
        return 1
    fi
    rm -f -- "${probe}"
}

current_status_json() {
    require_config
    local status=ready detail='Backup destination is ready.' actual_source actual_uuid available total same=false backup_disk minecraft_disk
    local target="${BACKUP_PATH}"

    if [[ -n ${BACKUP_MOUNT_POINT} ]]; then
        if ! mountpoint -q -- "${BACKUP_MOUNT_POINT}"; then
            status=unavailable
            detail='Expected backup mount is not mounted.'
        else
            actual_source="$(jv_exact_mount_identity SOURCE "${BACKUP_MOUNT_POINT}" 2>/dev/null || true)"
            actual_uuid="$(jv_exact_mount_identity UUID "${BACKUP_MOUNT_POINT}" 2>/dev/null || true)"
            if [[ -n ${BACKUP_EXPECTED_SOURCE} && ${actual_source} != "${BACKUP_EXPECTED_SOURCE}" ]]; then
                status=source-mismatch
                detail='Mounted backup source does not match the configured source.'
            elif [[ -n ${BACKUP_EXPECTED_UUID} && ${actual_uuid} != "${BACKUP_EXPECTED_UUID}" ]]; then
                status=source-mismatch
                detail='Mounted backup filesystem UUID does not match the configured filesystem.'
            fi
        fi
    fi

    if [[ ${status} == ready ]]; then
        if [[ ! -d ${BACKUP_PATH} ]]; then
            status=unavailable
            detail='Backup directory does not exist.'
        elif ! write_probe "${BACKUP_PATH}"; then
            status=read-only
            detail='Backup directory is not writable.'
        fi
    fi

    available=0
    total=0
    if [[ ${status} == ready ]]; then
        available="$(available_bytes_for_path "${target}")"
        total="$(filesystem_bytes_for_path "${target}")"
        available="${available:-0}"
        total="${total:-0}"
    fi

    case "${BACKUP_TYPE}" in
        nfs|smb) ;;
        *)
            backup_disk="$(disk_for_path "${BACKUP_PATH}")"
            minecraft_disk="$(data_disk)"
            if [[ -n ${backup_disk} && -n ${minecraft_disk} && ${backup_disk} == "${minecraft_disk}" ]]; then
                same=true
            fi
            ;;
    esac

    jq -n \
        --arg status "${status}" \
        --arg detail "${detail}" \
        --arg type "${BACKUP_TYPE}" \
        --arg path "${BACKUP_PATH}" \
        --arg mount_point "${BACKUP_MOUNT_POINT}" \
        --arg expected_uuid "${BACKUP_EXPECTED_UUID}" \
        --arg expected_source "${BACKUP_EXPECTED_SOURCE}" \
        --argjson available_bytes "${available}" \
        --argjson filesystem_bytes "${total}" \
        --argjson same_physical_disk "${same}" \
        '{ok:true,current:{status:$status,status_detail:$detail,type:$type,path:$path,mount_point:$mount_point,expected_uuid:$expected_uuid,expected_source:$expected_source,available_bytes:$available_bytes,filesystem_bytes:$filesystem_bytes,same_physical_disk:$same_physical_disk}}'
}

validate_request() {
    require_config
    if ! read_request; then
        json_error 'Invalid backup-storage request.'
        return 1
    fi

    TARGET_TYPE="$(jq -r '.type // ""' <<< "${REQUEST}")"
    TARGET_PATH="$(real_path "$(jq -r '.path // ""' <<< "${REQUEST}")")"
    TARGET_DEVICE="$(jq -r '.device // ""' <<< "${REQUEST}")"
    TARGET_MOUNT="$(real_path "$(jq -r '.mount_point // ""' <<< "${REQUEST}")")"
    TARGET_SOURCE="$(jq -r '.source // ""' <<< "${REQUEST}")"
    TARGET_USERNAME="$(jq -r '.username // ""' <<< "${REQUEST}")"
    TARGET_PASSWORD="$(jq -r '.password // ""' <<< "${REQUEST}")"
    TARGET_DOMAIN="$(jq -r '.domain // ""' <<< "${REQUEST}")"

    TARGET_UUID=''
    TARGET_EXPECTED_SOURCE=''
    TARGET_FILESYSTEM=''
    TARGET_MODEL=''
    TARGET_SIZE_BYTES=0
    TARGET_AVAILABLE_BYTES=0
    TARGET_FILESYSTEM_BYTES=0
    TARGET_SAME_DISK=false
    TARGET_ALREADY_MOUNTED=false
    TARGET_NEEDS_CREDENTIALS=false
    WARNINGS='[]'

    if [[ -z ${TARGET_PATH} ]] || ! backup_path_is_safe "${TARGET_PATH}"; then
        json_error 'Backup directory must be a safe absolute path and cannot be the Minecraft data directory or a child of it.'
        return 1
    fi

    local minecraft_disk target_disk mounted actual_source actual_uuid
    minecraft_disk="$(data_disk)"

    case "${TARGET_TYPE}" in
        system)
            TARGET_MOUNT=''
            TARGET_EXPECTED_SOURCE=''
            TARGET_UUID=''
            local system_probe system_mount system_fstype
            system_probe="$(nearest_existing_path "${TARGET_PATH}")"
            system_mount="$(findmnt -n -o TARGET --target "${system_probe}" 2>/dev/null || true)"
            system_fstype="$(findmnt -n -o FSTYPE --target "${system_probe}" 2>/dev/null || true)"
            case "${system_fstype}" in
                nfs|nfs4|cifs|smb3)
                    json_error 'This path is on network storage. Choose NFS or SMB so JustVoxel can track and validate the expected share.'
                    return 1
                    ;;
            esac
            case "${system_mount}" in
                /|/var) ;;
                *)
                    json_error 'This path is on a separate mounted filesystem. Choose Existing local filesystem / partition so JustVoxel can track its UUID safely.'
                    return 1
                    ;;
            esac
            target_disk="$(disk_for_path "${TARGET_PATH}")"
            if [[ -n ${target_disk} && -n ${minecraft_disk} && ${target_disk} == "${minecraft_disk}" ]]; then
                TARGET_SAME_DISK=true
                WARNINGS="$(json_warning_append "${WARNINGS}" 'This backup destination is on the same physical disk as Minecraft data. It can help with accidental file loss or reinstall recovery, but it does not protect against failure of that disk.')"
            fi
            ;;
        partition)
            if [[ -z ${TARGET_DEVICE} || ! -b ${TARGET_DEVICE} ]]; then
                json_error 'Choose an existing local partition.'
                return 1
            fi
            if [[ $(lsblk -dnro TYPE "${TARGET_DEVICE}" 2>/dev/null || true) != part || $(lsblk -dnro RO "${TARGET_DEVICE}" 2>/dev/null || true) != 0 ]]; then
                json_error 'The selected device must be a writable partition.'
                return 1
            fi
            TARGET_FILESYSTEM="$(blkid -s TYPE -o value "${TARGET_DEVICE}" 2>/dev/null || true)"
            case "${TARGET_FILESYSTEM}" in
                xfs|ext4|btrfs) ;;
                '')
                    json_error 'The selected partition has no filesystem. Formatting belongs to Advanced storage provisioning (A3.2) and is not performed here.'
                    return 1
                    ;;
                *)
                    json_error 'The selected filesystem is not supported for safe adoption. JustVoxel supports XFS, ext4, and Btrfs.'
                    return 1
                    ;;
            esac
            TARGET_UUID="$(blkid -s UUID -o value "${TARGET_DEVICE}" 2>/dev/null || true)"
            [[ -n ${TARGET_UUID} ]] || { json_error 'The selected local filesystem does not have a usable UUID.'; return 1; }
            mounted="$(lsblk -nro MOUNTPOINT "${TARGET_DEVICE}" 2>/dev/null | sed '/^$/d' | head -n1)"
            if [[ -n ${mounted} ]]; then
                mounted="$(real_path "${mounted}")"
                storage_mount_is_critical "${mounted}" && { json_error 'JustVoxel will not use a critical system mount as backup storage.'; return 1; }
                TARGET_MOUNT="${mounted}"
                TARGET_ALREADY_MOUNTED=true
                actual_uuid="$(jv_exact_mount_identity UUID "${TARGET_MOUNT}" 2>/dev/null || true)"
                [[ ${actual_uuid} == "${TARGET_UUID}" ]] || { json_error 'Mounted filesystem identity could not be verified.'; return 1; }
                TARGET_EXPECTED_SOURCE="$(jv_exact_mount_identity SOURCE "${TARGET_MOUNT}" 2>/dev/null || true)"
            else
                [[ -n ${TARGET_MOUNT} ]] || { json_error 'Choose a mount point for the unmounted filesystem.'; return 1; }
                storage_validate_mountpoint_path "${TARGET_MOUNT}" >/dev/null 2>&1 || { json_error 'The requested mount point is not safe for JustVoxel storage.'; return 1; }
                if mountpoint -q -- "${TARGET_MOUNT}"; then
                    json_error 'The requested mount point is already occupied.'
                    return 1
                fi
            fi
            path_within_mount "${TARGET_PATH}" "${TARGET_MOUNT}" || { json_error 'Backup directory must be inside the selected filesystem mount point.'; return 1; }
            [[ -n ${TARGET_EXPECTED_SOURCE} ]] || TARGET_EXPECTED_SOURCE="${TARGET_DEVICE}"
            TARGET_MODEL="$(lsblk -dnro MODEL "${TARGET_DEVICE}" 2>/dev/null | xargs || true)"
            TARGET_SIZE_BYTES="$(lsblk -bdnro SIZE "${TARGET_DEVICE}" 2>/dev/null | head -n1)"
            TARGET_SIZE_BYTES="${TARGET_SIZE_BYTES:-0}"
            target_disk="$(disk_for_block_device "${TARGET_DEVICE}")"
            if [[ -n ${target_disk} && -n ${minecraft_disk} && ${target_disk} == "${minecraft_disk}" ]]; then
                TARGET_SAME_DISK=true
                WARNINGS="$(json_warning_append "${WARNINGS}" 'The selected backup filesystem is on the same physical disk as Minecraft data. This is a weaker failure boundary than a separate disk or NAS.')"
            fi
            ;;
        nfs)
            [[ ${TARGET_SOURCE} == *:* && ${TARGET_SOURCE} != *[[:space:]]* ]] || { json_error 'NFS source must look like server:/export and cannot contain spaces.'; return 1; }
            [[ -n ${TARGET_MOUNT} ]] || { json_error 'Choose a local mount point for the NFS share.'; return 1; }
            storage_validate_mountpoint_path "${TARGET_MOUNT}" >/dev/null 2>&1 || { json_error 'The requested NFS mount point is not safe.'; return 1; }
            path_within_mount "${TARGET_PATH}" "${TARGET_MOUNT}" || { json_error 'Backup directory must be inside the NFS mount point.'; return 1; }
            if mountpoint -q -- "${TARGET_MOUNT}"; then
                actual_source="$(jv_exact_mount_identity SOURCE "${TARGET_MOUNT}" 2>/dev/null || true)"
                [[ ${actual_source} == "${TARGET_SOURCE}" ]] || { json_error 'The requested NFS mount point is already occupied by a different source.'; return 1; }
                TARGET_ALREADY_MOUNTED=true
                TARGET_EXPECTED_SOURCE="${actual_source}"
            else
                TARGET_EXPECTED_SOURCE="${TARGET_SOURCE}"
            fi
            WARNINGS="$(json_warning_append "${WARNINGS}" 'Network backups depend on the NFS server being reachable. JustVoxel uses nofail and fails closed if the expected share is unavailable.')"
            ;;
        smb)
            [[ ${TARGET_SOURCE} == //*/* && ${TARGET_SOURCE} != *[[:space:]]* ]] || { json_error 'SMB source must look like //server/share and cannot contain spaces.'; return 1; }
            [[ -n ${TARGET_MOUNT} ]] || { json_error 'Choose a local mount point for the SMB share.'; return 1; }
            storage_validate_mountpoint_path "${TARGET_MOUNT}" >/dev/null 2>&1 || { json_error 'The requested SMB mount point is not safe.'; return 1; }
            path_within_mount "${TARGET_PATH}" "${TARGET_MOUNT}" || { json_error 'Backup directory must be inside the SMB mount point.'; return 1; }
            if mountpoint -q -- "${TARGET_MOUNT}"; then
                actual_source="$(jv_exact_mount_identity SOURCE "${TARGET_MOUNT}" 2>/dev/null || true)"
                [[ ${actual_source} == "${TARGET_SOURCE}" ]] || { json_error 'The requested SMB mount point is already occupied by a different source.'; return 1; }
                TARGET_ALREADY_MOUNTED=true
                TARGET_EXPECTED_SOURCE="${actual_source}"
            else
                TARGET_NEEDS_CREDENTIALS=true
                [[ -n ${TARGET_USERNAME} ]] || { json_error 'SMB username is required when JustVoxel needs to mount the share.'; return 1; }
                [[ ${TARGET_USERNAME} != *$'\n'* && ${TARGET_USERNAME} != *$'\r'* && ${TARGET_DOMAIN} != *$'\n'* && ${TARGET_DOMAIN} != *$'\r'* ]] || { json_error 'SMB username or domain is invalid.'; return 1; }
                TARGET_EXPECTED_SOURCE="${TARGET_SOURCE}"
            fi
            WARNINGS="$(json_warning_append "${WARNINGS}" 'Network backups depend on the SMB server being reachable. JustVoxel stores credentials in a root-only file and fails closed if the expected share is unavailable.')"
            ;;
        *)
            json_error 'Choose a supported backup destination type.'
            return 1
            ;;
    esac

    if [[ ${TARGET_ALREADY_MOUNTED} == true || ${TARGET_TYPE} == system ]]; then
        TARGET_AVAILABLE_BYTES="$(available_bytes_for_path "${TARGET_PATH}")"
        TARGET_FILESYSTEM_BYTES="$(filesystem_bytes_for_path "${TARGET_PATH}")"
        TARGET_AVAILABLE_BYTES="${TARGET_AVAILABLE_BYTES:-0}"
        TARGET_FILESYSTEM_BYTES="${TARGET_FILESYSTEM_BYTES:-0}"
    fi

    return 0
}

proposed_json() {
    jq -n \
        --arg type "${TARGET_TYPE}" \
        --arg path "${TARGET_PATH}" \
        --arg device "${TARGET_DEVICE}" \
        --arg mount_point "${TARGET_MOUNT}" \
        --arg expected_uuid "${TARGET_UUID}" \
        --arg expected_source "${TARGET_EXPECTED_SOURCE}" \
        --arg filesystem "${TARGET_FILESYSTEM}" \
        --arg model "${TARGET_MODEL}" \
        --argjson size_bytes "${TARGET_SIZE_BYTES}" \
        --argjson available_bytes "${TARGET_AVAILABLE_BYTES}" \
        --argjson filesystem_bytes "${TARGET_FILESYSTEM_BYTES}" \
        --argjson same_physical_disk "${TARGET_SAME_DISK}" \
        --argjson already_mounted "${TARGET_ALREADY_MOUNTED}" \
        --argjson credentials_required "${TARGET_NEEDS_CREDENTIALS}" \
        '{type:$type,path:$path,device:$device,mount_point:$mount_point,expected_uuid:$expected_uuid,expected_source:$expected_source,filesystem:$filesystem,model:$model,size_bytes:$size_bytes,available_bytes:$available_bytes,filesystem_bytes:$filesystem_bytes,same_physical_disk:$same_physical_disk,already_mounted:$already_mounted,credentials_required:$credentials_required}'
}

plan_json() {
    if ! validate_request; then
        return 0
    fi
    local current proposed changed=false
    current="$(current_status_json | jq '.current')"
    proposed="$(proposed_json)"
    if ! jq -e --argjson p "${proposed}" '.type == $p.type and .path == $p.path and .mount_point == $p.mount_point and .expected_uuid == $p.expected_uuid and .expected_source == $p.expected_source' >/dev/null 2>&1 <<< "${current}"; then
        changed=true
    fi
    jq -n --argjson current "${current}" --argjson proposed "${proposed}" --argjson warnings "${WARNINGS}" --argjson changed "${changed}" '{ok:true,current:$current,proposed:$proposed,warnings:$warnings,changed:$changed,applied:false}'
}
