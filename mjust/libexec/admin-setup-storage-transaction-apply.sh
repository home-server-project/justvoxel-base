_a53_prepare_transaction() {
    local fstab_snapshot before manifest credentials_snapshot credentials_before
    install -d -m0700 -o root -g root "${A53_TRANSACTION_ROOT}" || return 1
    A53_TX_DIR="${A53_TRANSACTION_ROOT}/${A53_OPERATION_ID}"
    [[ ! -e ${A53_TX_DIR} ]] || return 2
    install -d -m0700 -o root -g root "${A53_TX_DIR}" || return 1
    A53_MANIFEST="${A53_TX_DIR}/manifest.json"
    fstab_snapshot="${A53_TX_DIR}/fstab.before"
    if [[ -e ${A53_FSTAB} ]]; then
        cat -- "${A53_FSTAB}" > "${fstab_snapshot}" || return 1
        A53_FSTAB_EXISTED=true
        A53_FSTAB_MODE="$(stat -c '%a' "${A53_FSTAB}" 2>/dev/null || true)"
        A53_FSTAB_UID="$(stat -c '%u' "${A53_FSTAB}" 2>/dev/null || true)"
        A53_FSTAB_GID="$(stat -c '%g' "${A53_FSTAB}" 2>/dev/null || true)"
        [[ -n ${A53_FSTAB_MODE} && -n ${A53_FSTAB_UID} && -n ${A53_FSTAB_GID} ]] || return 1
    else
        : > "${fstab_snapshot}" || return 1
        A53_FSTAB_EXISTED=false
        A53_FSTAB_MODE=''
        A53_FSTAB_UID=''
        A53_FSTAB_GID=''
    fi
    chmod 0600 "${fstab_snapshot}" || return 1
    before="$(_a53_file_sha256 "${fstab_snapshot}")"

    credentials_snapshot="${A53_TX_DIR}/smb.credentials.before"
    if [[ -e ${A54_SMB_CREDENTIALS} ]]; then
        cat -- "${A54_SMB_CREDENTIALS}" > "${credentials_snapshot}" || return 1
        chmod 0600 "${credentials_snapshot}" || return 1
        A54_CREDENTIALS_EXISTED=true
        A54_CREDENTIALS_MODE="$(stat -c '%a' "${A54_SMB_CREDENTIALS}" 2>/dev/null || true)"
        A54_CREDENTIALS_UID="$(stat -c '%u' "${A54_SMB_CREDENTIALS}" 2>/dev/null || true)"
        A54_CREDENTIALS_GID="$(stat -c '%g' "${A54_SMB_CREDENTIALS}" 2>/dev/null || true)"
        [[ -n ${A54_CREDENTIALS_MODE} && -n ${A54_CREDENTIALS_UID} && -n ${A54_CREDENTIALS_GID} ]] || return 1
    else
        : > "${credentials_snapshot}" || return 1
        chmod 0600 "${credentials_snapshot}" || return 1
        A54_CREDENTIALS_EXISTED=false
        A54_CREDENTIALS_MODE=''
        A54_CREDENTIALS_UID=''
        A54_CREDENTIALS_GID=''
    fi
    credentials_before="$(_a53_file_sha256 "${credentials_snapshot}")"

    manifest="$(jq -cn \
        --arg operation_id "${A53_OPERATION_ID}" \
        --arg plan_fingerprint "${A53_PLAN_FINGERPRINT}" \
        --argjson fstab_existed "${A53_FSTAB_EXISTED}" \
        --arg fstab_before_sha256 "${before}" \
        --arg fstab_mode "${A53_FSTAB_MODE}" \
        --arg fstab_uid "${A53_FSTAB_UID}" \
        --arg fstab_gid "${A53_FSTAB_GID}" \
        --argjson credentials_existed "${A54_CREDENTIALS_EXISTED}" \
        --arg credentials_before_sha256 "${credentials_before}" \
        --arg credentials_mode "${A54_CREDENTIALS_MODE}" \
        --arg credentials_uid "${A54_CREDENTIALS_UID}" \
        --arg credentials_gid "${A54_CREDENTIALS_GID}" \
        '{schema_version:"v1",operation_id:$operation_id,plan_fingerprint:$plan_fingerprint,phase:"storage_snapshot",fstab_existed:$fstab_existed,fstab_before_sha256:$fstab_before_sha256,fstab_mode:$fstab_mode,fstab_uid:$fstab_uid,fstab_gid:$fstab_gid,fstab_changed:false,fstab_after_sha256:"",credentials_existed:$credentials_existed,credentials_before_sha256:$credentials_before_sha256,credentials_mode:$credentials_mode,credentials_uid:$credentials_uid,credentials_gid:$credentials_gid,credentials_changed:false,credentials_after_sha256:"",mounts_by_transaction:[],directories_created:[],rollback:{state:"not_started",result:""}}')" || return 1
    _a53_manifest_write "${manifest}" || return 1
    return 0
}

_a53_record_created_dir() {
    local path="$1"
    A53_DIRS_CREATED+=("${path}")
    _a53_manifest_record_dir "${path}"
}

_a53_ensure_dir() {
    local path="$1" mode="$2" current i
    local -a missing=()
    if [[ -e ${path} ]]; then
        [[ -d ${path} ]] || return 1
        return 0
    fi
    current="${path}"
    while [[ ! -e ${current} ]]; do
        missing+=("${current}")
        [[ ${current} != / ]] || break
        current="$(dirname -- "${current}")"
    done
    [[ -d ${current} ]] || return 1
    for (( i=${#missing[@]}-1; i>=0; i-- )); do
        if (( i == 0 )); then
            install -d -m"${mode}" -o root -g root "${missing[$i]}" || return 1
        else
            install -d -m0755 -o root -g root "${missing[$i]}" || return 1
        fi
        _a53_record_created_dir "${missing[$i]}" || return 1
    done
}

_a53_write_local_fstab() {
    local uuid="$1" mountpoint="$2" fstype="$3" tmp
    [[ -e ${A53_FSTAB} ]] || : > "${A53_FSTAB}" || return 1
    tmp="$(mktemp "$(dirname -- "${A53_FSTAB}")/.fstab.justvoxel-a53.XXXXXX")" || return 1
    awk -v mp="${mountpoint}" '/^[[:space:]]*#/ || NF < 2 || $2 != mp {print}' "${A53_FSTAB}" > "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    printf 'UUID=%s %s %s noatime,nofail,x-systemd.device-timeout=10s 0 2\n' "${uuid}" "${mountpoint}" "${fstype}" >> "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    install -o root -g root -m0644 "${tmp}" "${A53_FSTAB}" || { rm -f -- "${tmp}"; return 1; }
    rm -f -- "${tmp}"
    A53_MUTATION_STARTED=true
    _a53_manifest_set_fstab_changed || return 1
    systemctl daemon-reload >/dev/null 2>&1 || return 1
}


_a54_write_network_fstab() {
    local source="$1" mountpoint="$2" fstype="$3" options="$4" tmp
    [[ -e ${A53_FSTAB} ]] || : > "${A53_FSTAB}" || return 1
    tmp="$(mktemp "$(dirname -- "${A53_FSTAB}")/.fstab.justvoxel-a54.XXXXXX")" || return 1
    awk -v mp="${mountpoint}" '/^[[:space:]]*#/ || NF < 2 || $2 != mp {print}' "${A53_FSTAB}" > "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    printf '%s %s %s %s 0 0\n' "${source}" "${mountpoint}" "${fstype}" "${options}" >> "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    install -o root -g root -m0644 "${tmp}" "${A53_FSTAB}" || { rm -f -- "${tmp}"; return 1; }
    rm -f -- "${tmp}"
    A53_MUTATION_STARTED=true
    _a53_manifest_set_fstab_changed || return 1
    systemctl daemon-reload >/dev/null 2>&1 || return 1
}

_a54_write_smb_credentials() {
    local username="$1" password="$2" domain="$3" after
    if [[ ! -d $(dirname -- "${A54_SMB_CREDENTIALS}") ]]; then
        _a53_ensure_dir "$(dirname -- "${A54_SMB_CREDENTIALS}")" 0700 || return 1
    fi
    umask 077
    {
        printf 'username=%s\n' "${username}"
        printf 'password=%s\n' "${password}"
        [[ -n ${domain} ]] && printf 'domain=%s\n' "${domain}"
    } > "${A54_SMB_CREDENTIALS}" || return 1
    chown root:root "${A54_SMB_CREDENTIALS}" || return 1
    chmod 0600 "${A54_SMB_CREDENTIALS}" || return 1
    A54_CREDENTIALS_CHANGED=true
    after="$(_a53_file_sha256 "${A54_SMB_CREDENTIALS}")"
    _a53_manifest_update ".credentials_changed = true | .credentials_after_sha256 = \"${after}\"" || return 1
}

_a54_mount_network_target() {
    local target_json="$1" type mountpoint source username domain actual_source options
    type="$(jq -r '.type' <<< "${target_json}")"
    [[ ${type} == nfs || ${type} == smb ]] || return 0
    mountpoint="$(_a53_normalize_path "$(jq -r '.mount_point' <<< "${target_json}")")"
    source="$(jq -r '.source' <<< "${target_json}")"
    username="$(jq -r '.username // ""' <<< "${target_json}")"
    domain="$(jq -r '.domain // ""' <<< "${target_json}")"

    if mountpoint -q -- "${mountpoint}"; then
        actual_source="$(findmnt -n -o SOURCE --target "${mountpoint}" 2>/dev/null || true)"
        [[ ${actual_source} == "${source}" ]] || return 1
        return 0
    fi
    if [[ ! -d ${mountpoint} ]]; then
        _a53_ensure_dir "${mountpoint}" 0755 || return 1
    fi

    if [[ ${type} == nfs ]]; then
        _a54_write_network_fstab "${source}" "${mountpoint}" nfs 'rw,_netdev,nofail,x-systemd.mount-timeout=20s' || return 1
    else
        _a54_write_smb_credentials "${username}" "${A54_SMB_PASSWORD}" "${domain}" || return 1
        options="credentials=${A54_SMB_CREDENTIALS},vers=3.0,rw,_netdev,nofail,x-systemd.mount-timeout=20s,uid=0,gid=0,file_mode=0600,dir_mode=0700"
        _a54_write_network_fstab "${source}" "${mountpoint}" cifs "${options}" || return 1
    fi

    mount "${mountpoint}" >/dev/null 2>&1 || return 1
    A53_MUTATION_STARTED=true
    actual_source="$(findmnt -n -o SOURCE --target "${mountpoint}" 2>/dev/null || true)"
    A53_MOUNTS_BY_US+=("${mountpoint}||${actual_source}")
    _a53_manifest_record_mount "${mountpoint}" "" "${actual_source}" || return 1
    [[ ${actual_source} == "${source}" ]] || return 1
    return 0
}

_a53_mount_target() {
    local target_json="$1" type mountpoint expected_uuid filesystem device actual_uuid actual_source mounted
    type="$(jq -r '.type' <<< "${target_json}")"
    [[ ${type} == partition ]] || return 0
    mountpoint="$(_a53_normalize_path "$(jq -r '.mount_point' <<< "${target_json}")")"
    expected_uuid="$(jq -r '.expected_uuid' <<< "${target_json}")"
    filesystem="$(jq -r '.filesystem' <<< "${target_json}")"
    device="$(jq -r '.device' <<< "${target_json}")"
    mounted="$(lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d' | head -n1)"
    if [[ -n ${mounted} ]]; then
        return 0
    fi

    if [[ ! -d ${mountpoint} ]]; then
        _a53_ensure_dir "${mountpoint}" 0755 || return 1
    fi
    _a53_write_local_fstab "${expected_uuid}" "${mountpoint}" "${filesystem}" || return 1
    mount "${mountpoint}" >/dev/null 2>&1 || return 1
    A53_MUTATION_STARTED=true
    actual_uuid="$(findmnt -n -o UUID --target "${mountpoint}" 2>/dev/null || true)"
    actual_source="$(findmnt -n -o SOURCE --target "${mountpoint}" 2>/dev/null || true)"
    A53_MOUNTS_BY_US+=("${mountpoint}|${actual_uuid}|${actual_source}")
    _a53_manifest_record_mount "${mountpoint}" "${actual_uuid}" "${actual_source}" || return 1
    [[ ${actual_uuid} == "${expected_uuid}" ]] || return 1
    return 0
}

_a53_write_probe() {
    local path="$1" probe
    probe="${path}/.justvoxel-a53-write-test.$$"
    : > "${probe}" 2>/dev/null || return 1
    rm -f -- "${probe}" || return 1
}

_a53_prepare_paths() {
    local storage backups data_path backup_path
    storage="$(jq -c '.storage' <<< "${A53_REQUEST}")"
    backups="$(jq -c '.backups' <<< "${A53_REQUEST}")"
    data_path="$(_a53_normalize_path "$(jq -r '.path' <<< "${storage}")")"
    backup_path="$(_a53_normalize_path "$(jq -r '.path' <<< "${backups}")")"
    _a53_ensure_dir "${data_path}" 0750 || return 1
    _a53_ensure_dir "${backup_path}" 0700 || return 1
    _a53_write_probe "${data_path}" || return 1
    _a53_write_probe "${backup_path}" || return 1
    return 0
}

_a53_restore_fstab() {
    local snapshot="${A53_TX_DIR}/fstab.before"
    [[ -f ${snapshot} ]] || return 1
    if [[ ${A53_FSTAB_EXISTED} == true ]]; then
        cat -- "${snapshot}" > "${A53_FSTAB}" || return 1
        chmod "${A53_FSTAB_MODE}" "${A53_FSTAB}" || return 1
        chown "${A53_FSTAB_UID}:${A53_FSTAB_GID}" "${A53_FSTAB}" || return 1
    else
        rm -f -- "${A53_FSTAB}" || return 1
    fi
    systemctl daemon-reload >/dev/null 2>&1 || return 1
    return 0
}


_a54_restore_smb_credentials() {
    local snapshot="${A53_TX_DIR}/smb.credentials.before"
    [[ -f ${snapshot} ]] || return 1
    if [[ ${A54_CREDENTIALS_EXISTED} == true ]]; then
        install -d -m0700 -o root -g root "$(dirname -- "${A54_SMB_CREDENTIALS}")" || return 1
        cat -- "${snapshot}" > "${A54_SMB_CREDENTIALS}" || return 1
        chmod "${A54_CREDENTIALS_MODE}" "${A54_SMB_CREDENTIALS}" || return 1
        chown "${A54_CREDENTIALS_UID}:${A54_CREDENTIALS_GID}" "${A54_SMB_CREDENTIALS}" || return 1
    else
        rm -f -- "${A54_SMB_CREDENTIALS}" || return 1
    fi
    return 0
}

_a53_load_manifest_for_rollback() {
    local operation_id fingerprint
    A53_TX_DIR="${A53_TRANSACTION_ROOT}/${A53_OPERATION_ID}"
    A53_MANIFEST="${A53_TX_DIR}/manifest.json"
    [[ -d ${A53_TX_DIR} && -f ${A53_MANIFEST} ]] || return 1
    operation_id="$(jq -r '.operation_id // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
    fingerprint="$(jq -r '.plan_fingerprint // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
    [[ ${operation_id} == "${A53_OPERATION_ID}" && ${fingerprint} == "${A53_PLAN_FINGERPRINT}" ]] || return 1
    A53_FSTAB_EXISTED="$(jq -r '.fstab_existed' "${A53_MANIFEST}")"
    A53_FSTAB_CHANGED="$(jq -r '.fstab_changed' "${A53_MANIFEST}")"
    A53_FSTAB_MODE="$(jq -r '.fstab_mode // ""' "${A53_MANIFEST}")"
    A53_FSTAB_UID="$(jq -r '.fstab_uid // ""' "${A53_MANIFEST}")"
    A53_FSTAB_GID="$(jq -r '.fstab_gid // ""' "${A53_MANIFEST}")"
    A54_CREDENTIALS_EXISTED="$(jq -r '.credentials_existed // false' "${A53_MANIFEST}")"
    A54_CREDENTIALS_CHANGED="$(jq -r '.credentials_changed // false' "${A53_MANIFEST}")"
    A54_CREDENTIALS_MODE="$(jq -r '.credentials_mode // ""' "${A53_MANIFEST}")"
    A54_CREDENTIALS_UID="$(jq -r '.credentials_uid // ""' "${A53_MANIFEST}")"
    A54_CREDENTIALS_GID="$(jq -r '.credentials_gid // ""' "${A53_MANIFEST}")"
    return 0
}

_a53_remove_created_empty_dirs() {
    local path i
    if (( ${#A53_DIRS_CREATED[@]} == 0 )); then
        while IFS= read -r path; do
            [[ -n ${path} ]] && A53_DIRS_CREATED+=("${path}")
        done < <(jq -r '.directories_created[]?' "${A53_MANIFEST}" 2>/dev/null)
    fi
    for (( i=${#A53_DIRS_CREATED[@]}-1; i>=0; i-- )); do
        path="${A53_DIRS_CREATED[$i]}"
        if [[ -d ${path} ]]; then
            rmdir -- "${path}" >/dev/null 2>&1 || true
        fi
    done
}

_a53_rollback() {
    local mode="${1:-explicit}" failure=0 entry mountpoint uuid source current_uuid current_source path expected_after current_after
    _a53_manifest_set_rollback running rollback_in_progress >/dev/null 2>&1 || true

    if [[ ${mode} == explicit && ${A53_FSTAB_CHANGED} == true ]]; then
        expected_after="$(jq -r '.fstab_after_sha256 // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
        current_after="$(_a53_file_sha256 "${A53_FSTAB}")"
        if [[ -z ${expected_after} || ${expected_after} != "${current_after}" ]]; then
            _a53_manifest_set_rollback failed needs_attention >/dev/null 2>&1 || true
            return 1
        fi
    fi


    if [[ ${mode} == explicit && ${A54_CREDENTIALS_CHANGED} == true ]]; then
        local expected_credentials current_credentials
        expected_credentials="$(jq -r '.credentials_after_sha256 // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
        current_credentials="$(_a53_file_sha256 "${A54_SMB_CREDENTIALS}")"
        if [[ -z ${expected_credentials} || ${expected_credentials} != "${current_credentials}" ]]; then
            _a53_manifest_set_rollback failed needs_attention >/dev/null 2>&1 || true
            return 1
        fi
    fi

    _a53_remove_created_empty_dirs

    if (( ${#A53_MOUNTS_BY_US[@]} == 0 )); then
        while IFS=$'\t' read -r mountpoint uuid source; do
            [[ -n ${mountpoint} ]] || continue
            A53_MOUNTS_BY_US+=("${mountpoint}|${uuid}|${source}")
        done < <(jq -r '.mounts_by_transaction[]? | [.mountpoint,.uuid,.source] | @tsv' "${A53_MANIFEST}" 2>/dev/null)
    fi

    local i
    for (( i=${#A53_MOUNTS_BY_US[@]}-1; i>=0; i-- )); do
        entry="${A53_MOUNTS_BY_US[$i]}"
        IFS='|' read -r mountpoint uuid source <<< "${entry}"
        if mountpoint -q -- "${mountpoint}"; then
            current_uuid="$(findmnt -n -o UUID --target "${mountpoint}" 2>/dev/null || true)"
            current_source="$(findmnt -n -o SOURCE --target "${mountpoint}" 2>/dev/null || true)"
            if [[ -n ${uuid} && ${current_uuid} != "${uuid}" ]]; then
                failure=1
                continue
            fi
            if [[ -n ${source} && ${current_source} != "${source}" ]]; then
                failure=1
                continue
            fi
            umount -- "${mountpoint}" >/dev/null 2>&1 || failure=1
        fi
    done

    if [[ ${A53_FSTAB_CHANGED} == true ]]; then
        _a53_restore_fstab || failure=1
    fi
    if [[ ${A54_CREDENTIALS_CHANGED} == true ]]; then
        _a54_restore_smb_credentials || failure=1
    fi

    _a53_remove_created_empty_dirs

    if (( failure != 0 )); then
        _a53_manifest_set_rollback failed needs_attention >/dev/null 2>&1 || true
        return 1
    fi
    _a53_manifest_set_phase storage_rolled_back >/dev/null 2>&1 || true
    _a53_manifest_set_rollback succeeded rolled_back >/dev/null 2>&1 || true
    return 0
}
