restore_file_or_remove() {
    local backup="$1" target="$2" existed="$3"
    [[ ! -L ${target} && $(realpath -m -- "${target}") == "${target}" ]] || return 1
    [[ ! -e ${target} || -f ${target} ]] || return 1
    if [[ ${existed} == yes ]]; then
        [[ -f ${backup} && ! -L ${backup} ]] || return 1
        cp -a -- "${backup}" "${target}"
    else
        rm -f -- "${target}"
    fi
}
bounded_smb_mount_error() {
    local value="${1:-}"
    value="$(printf '%s' "${value}" | tr '\r\n' '  ' 2>/dev/null || printf '%s' "${value}")"
    value="$(printf '%s' "${value}" | sed -E \
        -e 's/(password|passwd)=[^,[:space:]]+/\1=[redacted]/Ig' \
        -e 's/credentials=[^,[:space:]]+/credentials=[redacted]/Ig' \
        2>/dev/null || printf '%s' "${value}")"
    value="$(printf '%s' "${value}" | tr -s '[:space:]' ' ' 2>/dev/null || printf '%s' "${value}")"
    value="${value#" "}"
    value="${value%" "}"
    if [[ -z ${value} ]]; then
        value='mount.cifs returned no diagnostic text'
    fi
    printf '%s' "${value:0:512}"
}

apply_json() {
    if ! validate_request; then
        return 0
    fi

    local current proposed config_backup fstab_backup credentials_backup='' credentials_new=''
    local fstab_existed=no credentials_existed=no mounted_by_us=no credentials_changed=no
    local old_backup_type old_backup_path old_backup_mount old_backup_uuid old_backup_source
    local retired_source='' old_unmounted=no
    current="$(current_status_json | jq '.current')"
    proposed="$(proposed_json)"

    if [[ ${TARGET_TYPE} == smb && ${TARGET_NEEDS_CREDENTIALS} == true ]]; then
        [[ -n ${TARGET_PASSWORD} && ${TARGET_PASSWORD} != *$'\n'* && ${TARGET_PASSWORD} != *$'\r'* ]] || {
            json_error 'SMB password is required when applying a new SMB mount.'
            return 0
        }
    fi

    config_backup="$(mktemp "${JV_CONFIG_DIR}/.justvoxel.conf.pre-storage.XXXXXX")" || { json_error 'Could not prepare a configuration rollback point.'; return 0; }
    cp -a -- "${JV_CONFIG}" "${config_backup}" || { rm -f "${config_backup}"; json_error 'Could not prepare a configuration rollback point.'; return 0; }
    fstab_backup="$(mktemp /etc/.fstab.justvoxel-webui.XXXXXX)" || { rm -f "${config_backup}"; json_error 'Could not prepare a mount rollback point.'; return 0; }
    if [[ -e ${A31_FSTAB} || -L ${A31_FSTAB} ]]; then
        if [[ -L ${A31_FSTAB} || ! -f ${A31_FSTAB} ]] || ! cp -a -- "${A31_FSTAB}" "${fstab_backup}"; then
            rm -f -- "${config_backup}" "${fstab_backup}"
            json_error 'Could not snapshot safe persistent mount configuration.'
            return 0
        fi
        fstab_existed=yes
    fi
    # Snapshot credentials when replacing them or retiring the old SMB destination.
    # A replacement must have a confirmed rollback point before any mutation.
    if { [[ ${TARGET_TYPE} == smb && ${TARGET_ALREADY_MOUNTED} != true ]]; } || [[ ${BACKUP_TYPE} == smb ]]; then
        if [[ -L ${A31_SMB_CREDENTIALS} || $(realpath -m -- "${A31_SMB_CREDENTIALS}") != "${A31_SMB_CREDENTIALS}" ]]; then
            rm -f -- "${config_backup}" "${fstab_backup}"
            json_error 'Existing SMB credential path is not a safe regular file.'
            return 0
        fi
        if [[ -e ${A31_SMB_CREDENTIALS} || -L ${A31_SMB_CREDENTIALS} ]]; then
            if ! jv_storage_credentials_safe "${A31_SMB_CREDENTIALS}"; then
                rm -f -- "${config_backup}" "${fstab_backup}"
                json_error 'Existing SMB credential path is not a safe regular file.'
                return 0
            fi
            credentials_existed=yes
        fi
        credentials_backup="$(mktemp /etc/justvoxel/.smb-backup.credentials.XXXXXX 2>/dev/null || mktemp /tmp/.smb-backup.credentials.XXXXXX)" || {
            rm -f -- "${config_backup}" "${fstab_backup}"
            json_error 'Could not prepare a secure SMB credential rollback point.'
            return 0
        }
        if [[ ${credentials_existed} == yes ]] && ! cp -a -- "${A31_SMB_CREDENTIALS}" "${credentials_backup}"; then
            rm -f -- "${config_backup}" "${fstab_backup}" "${credentials_backup}"
            json_error 'Could not snapshot existing SMB credentials; they were preserved.'
            return 0
        fi
    fi

    old_backup_type="${BACKUP_TYPE}"
    old_backup_path="${BACKUP_PATH}"
    old_backup_mount="${BACKUP_MOUNT_POINT}"
    old_backup_uuid="${BACKUP_EXPECTED_UUID}"
    old_backup_source="${BACKUP_EXPECTED_SOURCE}"

    # A data filesystem or an administrator-adopted mount remains in use.
    if [[ -n ${old_backup_mount} && ${old_backup_mount} != "${TARGET_MOUNT}" &&
          ${old_backup_mount} != "${DATA_MOUNT_POINT:-}" &&
          ${DATA_PATH:-} != "${old_backup_mount}" && ${DATA_PATH:-} != "${old_backup_mount}/"* ]]; then
        retired_source="$(jv_storage_managed_entry "${A31_FSTAB}" "${old_backup_mount}" "${A31_SMB_CREDENTIALS}" configured)" || retired_source=''
        if [[ -n ${retired_source} ]]; then
            if [[ ${old_backup_type} == partition || ${old_backup_type} == disk ]]; then
                [[ -n ${old_backup_uuid} && ${retired_source} == "UUID=${old_backup_uuid}" ]] || {
                    json_error 'Previous managed backup mount does not match saved identity.'; return 0;
                }
            else
                [[ -n ${old_backup_source} && ${retired_source} == "${old_backup_source}" ]] || {
                    json_error 'Previous managed backup mount does not match saved identity.'; return 0;
                }
            fi
            if mountpoint -q -- "${old_backup_mount}"; then
                jv_storage_identity_matches "${old_backup_mount}" "${retired_source}" "${old_backup_uuid}" || {
                    json_error 'Previous backup mount has a different live identity.'; return 0;
                }
            fi
        elif [[ ${old_backup_mount} == /var/mnt/justvoxel-backup || ${old_backup_mount} == /var/mnt/justvoxel-data ]]; then
            # Missing definitions are harmless; ambiguous existing definitions are not.
            if awk -v mp="${old_backup_mount}" '$0 !~ /^[[:space:]]*#/ && $2==mp {found=1} END {exit !found}' "${A31_FSTAB}"; then
                json_error 'Previous managed backup mount configuration is inconsistent.'; return 0;
            fi
        fi
    fi

    rollback() {
        local restore_failed=0
        if [[ -n ${credentials_new:-} ]]; then
            rm -f -- "${credentials_new}" >/dev/null 2>&1 || restore_failed=1
            credentials_new=''
        fi
        if [[ ${mounted_by_us} == yes && -n ${TARGET_MOUNT} ]]; then
            umount -- "${TARGET_MOUNT}" >/dev/null 2>&1 || restore_failed=1
        fi
        if [[ ${fstab_existed} == yes ]]; then
            cp -a -- "${fstab_backup}" "${A31_FSTAB}" >/dev/null 2>&1 || restore_failed=1
        else
            rm -f -- "${A31_FSTAB}" >/dev/null 2>&1 || restore_failed=1
        fi
        systemctl daemon-reload >/dev/null 2>&1 || restore_failed=1
        if [[ ${credentials_changed} == yes && -n ${credentials_backup:-} ]]; then
            restore_file_or_remove "${credentials_backup}" "${A31_SMB_CREDENTIALS}" "${credentials_existed}" || restore_failed=1
        fi
        cp -a -- "${config_backup}" "${JV_CONFIG}" >/dev/null 2>&1 || restore_failed=1
        BACKUP_TYPE="${old_backup_type}"
        BACKUP_PATH="${old_backup_path}"
        BACKUP_MOUNT_POINT="${old_backup_mount}"
        BACKUP_EXPECTED_UUID="${old_backup_uuid}"
        BACKUP_EXPECTED_SOURCE="${old_backup_source}"
        render_runtime >/dev/null 2>&1 || restore_failed=1
        (( restore_failed == 0 )) || return 1
        if [[ ${old_unmounted} == yes ]]; then
            [[ $(jv_storage_managed_entry "${A31_FSTAB}" "${old_backup_mount}" "${A31_SMB_CREDENTIALS}" configured) == "${retired_source}" ]] || return 1
            if mountpoint -q -- "${old_backup_mount}"; then
                jv_storage_identity_matches "${old_backup_mount}" "${retired_source}" "${old_backup_uuid}" || return 1
            else
                mount "${old_backup_mount}" >/dev/null 2>&1 || return 1
                jv_storage_identity_matches "${old_backup_mount}" "${retired_source}" "${old_backup_uuid}" || return 1
            fi
        fi
        return 0
    }


    backup_storage_fail() {
        local message="$1"
        if rollback; then
            json_error "${message}"
        else
            json_error "${message} Previous storage could not be restored completely; rollback snapshots were preserved."
        fi
    }

    if [[ ${TARGET_TYPE} == partition && ${TARGET_ALREADY_MOUNTED} != true ]]; then
        install -d -m0755 -o root -g root "${TARGET_MOUNT}" || { backup_storage_fail 'Could not create the selected local mount point.'; return 0; }
        storage_remove_fstab_mountpoint "${TARGET_MOUNT}" >/dev/null 2>&1 || { backup_storage_fail 'Could not update persistent local mount configuration.'; return 0; }
        printf 'UUID=%s %s %s noatime,nofail,x-systemd.device-timeout=10s 0 2\n' "${TARGET_UUID}" "${TARGET_MOUNT}" "${TARGET_FILESYSTEM}" >> "${A31_FSTAB}" || { backup_storage_fail 'Could not update persistent local mount configuration.'; return 0; }
        systemctl daemon-reload >/dev/null 2>&1 || { backup_storage_fail 'Could not reload mount configuration.'; return 0; }
        mount "${TARGET_MOUNT}" >/dev/null 2>&1 || { backup_storage_fail 'The selected local filesystem could not be mounted.'; return 0; }
        mounted_by_us=yes
        [[ $(jv_exact_mount_identity UUID "${TARGET_MOUNT}" 2>/dev/null || true) == "${TARGET_UUID}" ]] || { backup_storage_fail 'Mounted filesystem identity did not match the selected partition.'; return 0; }
        TARGET_EXPECTED_SOURCE="$(jv_exact_mount_identity SOURCE "${TARGET_MOUNT}" 2>/dev/null || true)"
    elif [[ ${TARGET_TYPE} == nfs && ${TARGET_ALREADY_MOUNTED} != true ]]; then
        install -d -m0755 -o root -g root "${TARGET_MOUNT}" || { backup_storage_fail 'Could not create the NFS mount point.'; return 0; }
        storage_remove_fstab_mountpoint "${TARGET_MOUNT}" >/dev/null 2>&1 || { backup_storage_fail 'Could not update NFS mount configuration.'; return 0; }
        printf '%s %s nfs rw,_netdev,nofail,x-systemd.mount-timeout=20s 0 0\n' "${TARGET_SOURCE}" "${TARGET_MOUNT}" >> "${A31_FSTAB}" || { backup_storage_fail 'Could not update NFS mount configuration.'; return 0; }
        systemctl daemon-reload >/dev/null 2>&1 || { backup_storage_fail 'Could not reload NFS mount configuration.'; return 0; }
        mount "${TARGET_MOUNT}" >/dev/null 2>&1 || { backup_storage_fail 'NFS share could not be mounted. Check the server, export, network, and permissions.'; return 0; }
        mounted_by_us=yes
        TARGET_EXPECTED_SOURCE="$(jv_exact_mount_identity SOURCE "${TARGET_MOUNT}" 2>/dev/null || true)"
        [[ ${TARGET_EXPECTED_SOURCE} == "${TARGET_SOURCE}" ]] || { backup_storage_fail 'Mounted NFS source did not match the requested share.'; return 0; }
    elif [[ ${TARGET_TYPE} == smb && ${TARGET_ALREADY_MOUNTED} != true ]]; then
        install -d -m0755 -o root -g root "${TARGET_MOUNT}" || { backup_storage_fail 'Could not create the SMB mount point.'; return 0; }
        install -d -m0700 -o root -g root /etc/justvoxel || { backup_storage_fail 'Could not prepare secure SMB credential storage.'; return 0; }
        if [[ -e ${A31_SMB_CREDENTIALS} || -L ${A31_SMB_CREDENTIALS} ]]; then
            if ! jv_storage_credentials_safe "${A31_SMB_CREDENTIALS}"; then
                backup_storage_fail 'Existing SMB credential path is not a safe regular file.'
                return 0
            fi
        fi
        credentials_new="$(mktemp /etc/justvoxel/.smb-backup.credentials.new.XXXXXX 2>/dev/null)" || {
            backup_storage_fail 'Could not create secure SMB credential storage in /etc/justvoxel.'
            return 0
        }
        umask 077
        {
            printf 'username=%s\n' "${TARGET_USERNAME}"
            printf 'password=%s\n' "${TARGET_PASSWORD}"
            if [[ -n ${TARGET_DOMAIN} ]]; then
                printf 'domain=%s\n' "${TARGET_DOMAIN}"
            fi
        } > "${credentials_new}" || { backup_storage_fail 'Could not write temporary SMB credentials securely.'; return 0; }
        chown root:root "${credentials_new}" >/dev/null 2>&1 || { backup_storage_fail 'Could not set SMB credential ownership.'; return 0; }
        chmod 0600 "${credentials_new}" || { backup_storage_fail 'Could not set SMB credential permissions.'; return 0; }
        mv -fT -- "${credentials_new}" "${A31_SMB_CREDENTIALS}" || { backup_storage_fail 'Could not install SMB credentials securely.'; return 0; }
        credentials_new=''
        credentials_changed=yes
        storage_remove_fstab_mountpoint "${TARGET_MOUNT}" >/dev/null 2>&1 || { backup_storage_fail 'Could not update SMB mount configuration.'; return 0; }
        printf '%s %s cifs credentials=%s,vers=3.0,rw,_netdev,nofail,x-systemd.mount-timeout=20s,uid=0,gid=0,file_mode=0600,dir_mode=0700 0 0\n' "${TARGET_SOURCE}" "${TARGET_MOUNT}" "${A31_SMB_CREDENTIALS}" >> "${A31_FSTAB}" || { backup_storage_fail 'Could not update SMB mount configuration.'; return 0; }
        systemctl daemon-reload >/dev/null 2>&1 || { backup_storage_fail 'Could not reload SMB mount configuration.'; return 0; }
        local smb_mount_error smb_mount_rc smb_mount_detail
        smb_mount_error="$(mount "${TARGET_MOUNT}" 2>&1)"
        smb_mount_rc=$?
        if (( smb_mount_rc != 0 )); then
            smb_mount_detail="$(bounded_smb_mount_error "${smb_mount_error}")"
            backup_storage_fail "SMB share could not be mounted: ${smb_mount_detail}"
            return 0
        fi
        mounted_by_us=yes
        TARGET_EXPECTED_SOURCE="$(jv_exact_mount_identity SOURCE "${TARGET_MOUNT}" 2>/dev/null || true)"
        [[ ${TARGET_EXPECTED_SOURCE} == "${TARGET_SOURCE}" ]] || { backup_storage_fail 'Mounted SMB source did not match the requested share.'; return 0; }
    fi

    if [[ ${TARGET_TYPE} == nfs || ${TARGET_TYPE} == smb ]]; then
        mkdir -p -- "${TARGET_PATH}" >/dev/null 2>&1 || { backup_storage_fail 'Could not create the backup directory on the network share.'; return 0; }
    else
        install -d -m0700 -o root -g root "${TARGET_PATH}" >/dev/null 2>&1 || { backup_storage_fail 'Could not create the backup directory.'; return 0; }
    fi
    write_probe "${TARGET_PATH}" || { backup_storage_fail 'The selected backup directory is not writable.'; return 0; }

    if [[ -n ${retired_source} ]]; then
        if mountpoint -q -- "${old_backup_mount}"; then
            jv_storage_identity_matches "${old_backup_mount}" "${retired_source}" "${old_backup_uuid}" || { backup_storage_fail 'Previous backup mount identity changed.'; return 0; }
            umount -- "${old_backup_mount}" || { backup_storage_fail 'Previous backup mount could not be retired.'; return 0; }
            old_unmounted=yes
        fi
        jv_storage_remove_entry "${A31_FSTAB}" "${old_backup_mount}" || { backup_storage_fail 'Previous backup mount definition could not be retired.'; return 0; }
        jv_storage_clear_retired_failure "${old_backup_mount}" || { backup_storage_fail 'Retired mount configuration could not be reloaded.'; return 0; }
    fi

    BACKUP_TYPE="${TARGET_TYPE}"
    BACKUP_PATH="${TARGET_PATH}"
    BACKUP_MOUNT_POINT="${TARGET_MOUNT}"
    BACKUP_EXPECTED_UUID="${TARGET_UUID}"
    BACKUP_EXPECTED_SOURCE="${TARGET_EXPECTED_SOURCE}"
    if ! write_main_config >/dev/null 2>&1 || ! render_runtime >/dev/null 2>&1; then
        backup_storage_fail 'Backup storage could not be applied.'
        return 0
    fi

    if [[ ${old_backup_type} == smb && ${TARGET_TYPE} != smb && ${credentials_existed} == yes ]] &&
       ! jv_storage_credentials_referenced "${A31_FSTAB}" "${A31_SMB_CREDENTIALS}"; then
        jv_storage_credentials_safe "${A31_SMB_CREDENTIALS}" || { backup_storage_fail 'Obsolete SMB credentials are unsafe.'; return 0; }
        credentials_changed=yes
        rm -f -- "${A31_SMB_CREDENTIALS}" || { backup_storage_fail 'Obsolete SMB credentials could not be removed.'; return 0; }
    fi

    rm -f -- "${config_backup}" "${fstab_backup}" "${credentials_backup:-}" >/dev/null 2>&1 || true
    TARGET_AVAILABLE_BYTES="$(available_bytes_for_path "${TARGET_PATH}")"
    TARGET_FILESYSTEM_BYTES="$(filesystem_bytes_for_path "${TARGET_PATH}")"
    TARGET_AVAILABLE_BYTES="${TARGET_AVAILABLE_BYTES:-0}"
    TARGET_FILESYSTEM_BYTES="${TARGET_FILESYSTEM_BYTES:-0}"
    proposed="$(proposed_json)"
    jq -n --argjson current "${current}" --argjson proposed "${proposed}" --argjson warnings "${WARNINGS}" '{ok:true,current:$current,proposed:$proposed,warnings:$warnings,changed:true,applied:true}'
}
