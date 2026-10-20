_a53_prepare_transaction() {
    local fstab_snapshot before manifest credentials_snapshot credentials_before backup_type backup_mount snapshot_credentials=false
    [[ ! -L ${A53_FSTAB} && ( ! -e ${A53_FSTAB} || -f ${A53_FSTAB} ) ]] || return 1
    backup_type="$(jq -r '.backups.type' <<< "${A53_REQUEST}")"
    backup_mount="$(_a53_normalize_path "$(jq -r '.backups.mount_point // ""' <<< "${A53_REQUEST}")")"
    if [[ ${backup_type} == smb ]] && {
       { [[ -n ${backup_mount} ]] && ! mountpoint -q -- "${backup_mount}"; } ||
       jv_storage_credentials_referenced "${A53_FSTAB}" "${A54_SMB_CREDENTIALS}";
    }; then
        [[ ! -L ${A54_SMB_CREDENTIALS} && $(_a53_normalize_path "${A54_SMB_CREDENTIALS}") == "${A54_SMB_CREDENTIALS}" ]] || return 1
        if [[ -e ${A54_SMB_CREDENTIALS} || -L ${A54_SMB_CREDENTIALS} ]]; then
            jv_storage_credentials_safe "${A54_SMB_CREDENTIALS}" || return 1
        fi
        snapshot_credentials=true
    fi

    jv_trace_run install -d -m0700 -o root -g root "${A53_TRANSACTION_ROOT}" || return 1
    A53_TX_DIR="${A53_TRANSACTION_ROOT}/${A53_OPERATION_ID}"
    [[ ! -e ${A53_TX_DIR} ]] || return 2
    jv_trace_run install -d -m0700 -o root -g root "${A53_TX_DIR}" || return 1
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
    credentials_before=absent
    if [[ ${snapshot_credentials} == true ]]; then
        if [[ -e ${A54_SMB_CREDENTIALS} ]]; then
            cat -- "${A54_SMB_CREDENTIALS}" > "${credentials_snapshot}" || return 1
            chmod 0600 "${credentials_snapshot}" || return 1
            A54_CREDENTIALS_EXISTED=true
            A54_CREDENTIALS_MODE="$(stat -c '%a' "${A54_SMB_CREDENTIALS}" 2>/dev/null || true)"
            A54_CREDENTIALS_UID="$(stat -c '%u' "${A54_SMB_CREDENTIALS}" 2>/dev/null || true)"
            A54_CREDENTIALS_GID="$(stat -c '%g' "${A54_SMB_CREDENTIALS}" 2>/dev/null || true)"
            [[ -n ${A54_CREDENTIALS_MODE} && -n ${A54_CREDENTIALS_UID} && -n ${A54_CREDENTIALS_GID} ]] || return 1
            credentials_before="$(_a53_file_sha256 "${credentials_snapshot}")"
        else
            A54_CREDENTIALS_EXISTED=false
            A54_CREDENTIALS_MODE=''
            A54_CREDENTIALS_UID=''
            A54_CREDENTIALS_GID=''
        fi
    else
        A54_CREDENTIALS_EXISTED=false
        A54_CREDENTIALS_MODE=''
        A54_CREDENTIALS_UID=''
        A54_CREDENTIALS_GID=''
    fi

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
        '{schema_version:"v2",fstab_resources:[],preserved_resources:[],operation_id:$operation_id,plan_fingerprint:$plan_fingerprint,phase:"storage_snapshot",fstab_existed:$fstab_existed,fstab_before_sha256:$fstab_before_sha256,fstab_mode:$fstab_mode,fstab_uid:$fstab_uid,fstab_gid:$fstab_gid,fstab_changed:false,fstab_after_sha256:"",credentials_existed:$credentials_existed,credentials_before_sha256:$credentials_before_sha256,credentials_mode:$credentials_mode,credentials_uid:$credentials_uid,credentials_gid:$credentials_gid,credentials_changed:false,credentials_after_sha256:"",mounts_by_transaction:[],retired_mounts:[],directories_created:[],rollback:{state:"not_started",result:""}}')" || return 1
    manifest="$(jq -c --arg credentials_path "${A54_SMB_CREDENTIALS}" --argjson request "${A53_REQUEST}" '. + {storage_request:$request} | if $request.backups.type == "smb" then . + {credentials_path:$credentials_path} else . end' <<< "${manifest}")" || return 1
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
            jv_trace_run install -d -m"${mode}" -o root -g root "${missing[$i]}" || return 1
        else
            jv_trace_run install -d -m0755 -o root -g root "${missing[$i]}" || return 1
        fi
        _a53_record_created_dir "${missing[$i]}" || return 1
    done
}

# Only append-only definitions are owned. Pending evidence is intentionally
# conservative after interruption: an unconfirmed resource is retained.
_a53_append_fstab_resource() {
    local point="$1" source="$2" uuid="$3" filesystem="$4" line="$5" object tmp
    [[ ! -L ${A53_FSTAB} && ( ! -e ${A53_FSTAB} || -f ${A53_FSTAB} ) ]] || return 1
    [[ -e ${A53_FSTAB} ]] || : > "${A53_FSTAB}" || return 1
    [[ -z $(_a53_fstab_lines "${A53_FSTAB}" "${point}") ]] || return 1
    object="$(jq -cn --arg mp "${point}" --arg src "${source}" --arg uuid "${uuid}" --arg fs "${filesystem}" --arg line "${line}" \
        '{mountpoint:$mp,source:$src,uuid:$uuid,filesystem:$fs,exact_line:$line,action:"pending"}')" || return 1
    _a53_manifest_update ".fstab_resources += [${object}]" || return 1
    tmp="$(mktemp "$(dirname -- "${A53_FSTAB}")/.fstab.resource.XXXXXX")" || return 1
    if ! cat -- "${A53_FSTAB}" > "${tmp}" ||
       ! printf '%s\n' "${line}" >> "${tmp}" ||
       ! cp --attributes-only --preserve=all -- "${A53_FSTAB}" "${tmp}" ||
       ! sync -f -- "${tmp}" || ! mv -fT -- "${tmp}" "${A53_FSTAB}"; then
        rm -f -- "${tmp}"
        return 1
    fi
    _a53_manifest_update '.fstab_resources[-1].action = "created"' || return 1
    A53_MUTATION_STARTED=true
    _a53_manifest_set_fstab_changed || return 1
    jv_trace_run systemctl daemon-reload >/dev/null 2>&1
}

_a53_write_local_fstab() {
    local uuid="$1" mountpoint="$2" fstype="$3" line
    if [[ -n $(_a53_fstab_lines "${A53_FSTAB}" "${mountpoint}") ]]; then
        awk -v mp="${mountpoint}" -v src="UUID=${uuid}" -v fs="${fstype}" '$0 !~ /^[[:space:]]*#/ && $2==mp {n++; if ($1!=src || $3!=fs) bad=1} END {exit (n!=1 || bad)}' "${A53_FSTAB}"
        return $?
    fi
    printf -v line 'UUID=%s %s %s noatime,nofail,x-systemd.device-timeout=10s 0 2' "${uuid}" "${mountpoint}" "${fstype}"
    _a53_append_fstab_resource "${mountpoint}" "UUID=${uuid}" "${uuid}" "${fstype}" "${line}" || return 1
    _a53_evidence_set fstab_update ok
}

_a54_write_network_fstab() {
    local source="$1" mountpoint="$2" fstype="$3" options="$4" line
    printf -v line '%s %s %s %s 0 0' "${source}" "${mountpoint}" "${fstype}" "${options}"
    _a53_append_fstab_resource "${mountpoint}" "${source}" '' "${fstype}" "${line}" || return 1
    _a53_evidence_set network_fstab_update ok
}

_a54_write_smb_credentials() {
    local username="$1" password="$2" domain="$3" after tmp credentials_path_json
    [[ ! -L ${A54_SMB_CREDENTIALS} && $(_a53_normalize_path "${A54_SMB_CREDENTIALS}") == "${A54_SMB_CREDENTIALS}" ]] || return 1
    if [[ -e ${A54_SMB_CREDENTIALS} ]]; then
        jv_storage_credentials_safe "${A54_SMB_CREDENTIALS}" || return 1
    fi
    ! jv_storage_credentials_referenced "${A53_FSTAB}" "${A54_SMB_CREDENTIALS}" || return 1
    if [[ $(jq -r '.isolated_credentials // false' <<< "${A53_REQUEST}") == true ]]; then
        [[ ! -e ${A54_SMB_CREDENTIALS} && ! -L ${A54_SMB_CREDENTIALS} ]] || return 1
    fi
    if [[ ! -d $(dirname -- "${A54_SMB_CREDENTIALS}") ]]; then
        _a53_ensure_dir "$(dirname -- "${A54_SMB_CREDENTIALS}")" 0700 || return 1
    fi
    umask 077
    tmp="$(mktemp "$(dirname -- "${A54_SMB_CREDENTIALS}")/.credentials.XXXXXX")" || return 1
    {
        printf 'username=%s\n' "${username}"
        printf 'password=%s\n' "${password}"
        if [[ -n ${domain} ]]; then
            printf 'domain=%s\n' "${domain}"
        fi
    } > "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    after="$(_a53_file_sha256 "${tmp}")"
    A54_CREDENTIALS_CHANGED=true
    credentials_path_json="$(jq -Rn --arg path "${A54_SMB_CREDENTIALS}" '$path')" || { rm -f -- "${tmp}"; return 1; }
    _a53_manifest_update ".credentials_path = ${credentials_path_json} | .credentials_changed = true | .credentials_after_sha256 = \"${after}\"" || { rm -f -- "${tmp}"; return 1; }
    chown root:root "${tmp}" && chmod 0600 "${tmp}" && sync -f -- "${tmp}" &&
        mv -fT -- "${tmp}" "${A54_SMB_CREDENTIALS}" || { rm -f -- "${tmp}"; return 1; }
    chmod 0600 "${A54_SMB_CREDENTIALS}" || return 1
    _a53_evidence_set smb_credentials_written yes
}

_a54_mount_network_target() {
    local target_json="$1" type mountpoint source username domain actual_source options output rc
    type="$(jq -r '.type' <<< "${target_json}")"
    [[ ${type} == nfs || ${type} == smb ]] || return 0
    mountpoint="$(_a53_normalize_path "$(jq -r '.mount_point' <<< "${target_json}")")"
    source="$(jq -r '.source' <<< "${target_json}")"
    username="$(jq -r '.username // ""' <<< "${target_json}")"
    domain="$(jq -r '.domain // ""' <<< "${target_json}")"

    if mountpoint -q -- "${mountpoint}"; then
        actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
        _a53_evidence_set backup_mount_result already_mounted
        if [[ ${actual_source} == "${source}" ]]; then
            _a53_evidence_set backup_source_match yes
            return 0
        fi
        _a53_evidence_set backup_source_match no
        return 1
    fi
    [[ $(jq -r '.mounted_only // false' <<< "${A53_REQUEST}") != true ]] || return 1
    if [[ ! -d ${mountpoint} ]]; then
        _a53_ensure_dir "${mountpoint}" 0755 || return 1
    fi

    if awk -v mp="${mountpoint}" '$0 !~ /^[[:space:]]*#/ && $2==mp {found=1} END {exit !found}' "${A53_FSTAB}"; then
        awk -v mp="${mountpoint}" -v src="${source}" -v fs="$([[ ${type} == smb ]] && echo cifs || echo nfs)" '$0 !~ /^[[:space:]]*#/ && $2==mp {n++; if ($1!=src || $3!=fs) bad=1} END {exit (n!=1 || bad)}' "${A53_FSTAB}" || return 1
    elif [[ ${type} == nfs ]]; then
        _a54_write_network_fstab "${source}" "${mountpoint}" nfs 'rw,_netdev,nofail,x-systemd.mount-timeout=20s' || return 1
    else
        _a54_write_smb_credentials "${username}" "${A54_SMB_PASSWORD}" "${domain}" || return 1
        options="credentials=${A54_SMB_CREDENTIALS},vers=3.0,rw,_netdev,nofail,x-systemd.mount-timeout=20s,uid=0,gid=0,file_mode=0600,dir_mode=0700"
        _a54_write_network_fstab "${source}" "${mountpoint}" cifs "${options}" || return 1
    fi

    local mount_output_file
    mount_output_file="$(mktemp "${A53_TX_DIR}/.mount-output.XXXXXX")" || return 1
    _a53_manifest_record_mount "${mountpoint}" "" "${source}" || return 1
    jv_trace_command mount "${mountpoint}"
    mount "${mountpoint}" >"${mount_output_file}" 2>&1
    rc=$?
    output="$(cat -- "${mount_output_file}" 2>/dev/null || true)"
    jv_trace_output "${output}"
    jv_trace_result "${rc}"
    rm -f -- "${mount_output_file}"
    _a53_evidence_set_bounded backup_mount_output "${output}"
    if (( rc != 0 )); then
        _a53_evidence_set backup_mount_result failed
        return 1
    fi
    _a53_evidence_set backup_mount_result mounted
    A53_MUTATION_STARTED=true
    actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
    A53_MOUNTS_BY_US+=("$(jq -cn --arg mountpoint "${mountpoint}" --arg uuid "" --arg source "${actual_source}" '{mountpoint:$mountpoint,uuid:$uuid,source:$source}')")
    if [[ ${actual_source} == "${source}" ]]; then
        _a53_evidence_set backup_source_match yes
        return 0
    fi
    _a53_evidence_set backup_source_match no
    return 1
}

_a53_mount_target() {
    local target_json="$1" role="${2:-local}" type mountpoint expected_uuid filesystem device actual_uuid actual_source mounted output rc
    type="$(jq -r '.type' <<< "${target_json}")"
    if [[ ${type} != partition ]]; then
        _a53_evidence_set "${role}_mount_result" not_applicable
        return 0
    fi
    mountpoint="$(_a53_normalize_path "$(jq -r '.mount_point' <<< "${target_json}")")"
    expected_uuid="$(jq -r '.expected_uuid' <<< "${target_json}")"
    filesystem="$(jq -r '.filesystem' <<< "${target_json}")"
    device="$(jq -r '.device' <<< "${target_json}")"
    mounted="$(lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d' | head -n1)"
    if [[ -n ${mounted} ]]; then
        _a53_evidence_set "${role}_mount_result" already_mounted
        return 0
    fi

    [[ $(jq -r '.mounted_only // false' <<< "${A53_REQUEST}") != true ]] || return 1
    if [[ ! -d ${mountpoint} ]]; then
        _a53_ensure_dir "${mountpoint}" 0755 || return 1
    fi
    _a53_write_local_fstab "${expected_uuid}" "${mountpoint}" "${filesystem}" || return 1
    local mount_output_file
    mount_output_file="$(mktemp "${A53_TX_DIR}/.mount-output.XXXXXX")" || return 1
    _a53_manifest_record_mount "${mountpoint}" "${expected_uuid}" "${device}" || return 1
    jv_trace_command mount "${mountpoint}"
    mount "${mountpoint}" >"${mount_output_file}" 2>&1
    rc=$?
    output="$(cat -- "${mount_output_file}" 2>/dev/null || true)"
    jv_trace_output "${output}"
    jv_trace_result "${rc}"
    rm -f -- "${mount_output_file}"
    _a53_evidence_set_bounded "${role}_mount_output" "${output}"
    if (( rc != 0 )); then
        _a53_evidence_set "${role}_mount_result" failed
        return 1
    fi
    _a53_evidence_set "${role}_mount_result" mounted
    A53_MUTATION_STARTED=true
    actual_uuid="$(jv_exact_mount_identity UUID "${mountpoint}" 2>/dev/null || true)"
    actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
    A53_MOUNTS_BY_US+=("$(jq -cn --arg mountpoint "${mountpoint}" --arg uuid "${actual_uuid}" --arg source "${actual_source}" '{mountpoint:$mountpoint,uuid:$uuid,source:$source}')")
    if [[ ${actual_uuid} == "${expected_uuid}" ]]; then
        _a53_evidence_set "${role}_uuid_match" yes
        return 0
    fi
    _a53_evidence_set "${role}_uuid_match" no
    return 1
}

_a53_write_probe() {
    local path="$1" role="${2:-path}" probe
    probe="${path}/.justvoxel-a53-write-test.$$"
    if ! : > "${probe}" 2>/dev/null; then
        _a53_evidence_set "${role}_write_probe" failed
        return 1
    fi
    if ! rm -f -- "${probe}"; then
        _a53_evidence_set "${role}_write_probe" cleanup_failed
        return 1
    fi
    _a53_evidence_set "${role}_write_probe" passed
    return 0
}

_a53_prepare_paths() {
    local storage backups data_path backup_path
    storage="$(jq -c '.storage' <<< "${A53_REQUEST}")"
    backups="$(jq -c '.backups' <<< "${A53_REQUEST}")"
    data_path="$(_a53_normalize_path "$(jq -r '.path' <<< "${storage}")")"
    backup_path="$(_a53_normalize_path "$(jq -r '.path' <<< "${backups}")")"
    _a53_ensure_dir "${data_path}" 0750 || return 1
    _a53_ensure_dir "${backup_path}" 0700 || return 1
    _a53_write_probe "${data_path}" data || return 1
    _a53_write_probe "${backup_path}" backup || return 1
    _a53_evidence_set storage_paths prepared
    return 0
}

_a53_restore_fstab() {
    _a53_rollback_fstab_resources
}

_a54_restore_smb_credentials() {
    local snapshot="${A53_TX_DIR}/smb.credentials.before" tmp
    # An unrelated transaction has no authority over this shared credential file,
    # even when credentials_existed is false because no snapshot was needed.
    [[ ${A54_CREDENTIALS_CHANGED} == true ]] || return 0
    if [[ $(jq -r '.schema_version' "${A53_MANIFEST}") == v2 ]]; then
        [[ $(jq -r '.credentials_path // ""' "${A53_MANIFEST}") == "${A54_SMB_CREDENTIALS}" ]] || return 1
    fi
    [[ ! -L ${A54_SMB_CREDENTIALS} && $(_a53_normalize_path "${A54_SMB_CREDENTIALS}") == "${A54_SMB_CREDENTIALS}" ]] || return 1
    [[ ! -e ${A54_SMB_CREDENTIALS} || -f ${A54_SMB_CREDENTIALS} ]] || return 1
    local before after current references line
    before="$(jq -r '.credentials_before_sha256' "${A53_MANIFEST}")"
    after="$(jq -r '.credentials_after_sha256' "${A53_MANIFEST}")"
    current="$(_a53_file_sha256 "${A54_SMB_CREDENTIALS}")"
    [[ ${current} != "${before}" ]] || return 0
    [[ ! -L ${A53_FSTAB} && -f ${A53_FSTAB} ]] || return 1
    if jv_storage_credentials_referenced "${A53_FSTAB}" "${A54_SMB_CREDENTIALS}" ||
       jq -e '(.preserved_resources // []) | length > 0' "${A53_MANIFEST}" >/dev/null; then
        # Legacy retirement may have removed the file. Only absent credentials
        # with validated exact historical references can be restored here.
        if [[ ${current} == absent && ${after} == absent && ${A54_CREDENTIALS_EXISTED} == true &&
              $(jq -r '.schema_version' "${A53_MANIFEST}") == v1 ]]; then
            references="$(awk -v credential="${A54_SMB_CREDENTIALS}" '$0 !~ /^[[:space:]]*#/ && NF >= 4 {n=split($4,a,","); for(i=1;i<=n;i++) if(a[i]=="credentials=" credential) print}' "${A53_FSTAB}")" || return 1
            while IFS= read -r line; do
                [[ -n ${line} ]] || continue
                jq -e --arg line "${line}" 'any(.legacy_retired_resources[]?; .exact_line == $line)' "${A53_MANIFEST}" >/dev/null || return 1
            done <<< "${references}"
        else
            _a53_evidence_set credentials_recovery_state retained_referenced
            _a53_manifest_update '.credentials_retained_reason = "surviving_infrastructure_reference"'
            return $?
        fi
    fi
    # An unrelated writer's bytes are never overwritten.
    [[ -n ${after} && ${current} == "${after}" ]] || return 1
    if [[ ${A54_CREDENTIALS_EXISTED} == true ]]; then
        [[ $(_a53_file_sha256 "${snapshot}") == "${before}" ]] || return 1
    fi
    if [[ ${A54_CREDENTIALS_EXISTED} == true ]]; then
        [[ -f ${snapshot} && ! -L ${snapshot} ]] || return 1
        [[ ! -e ${A54_SMB_CREDENTIALS} ]] || jv_storage_credentials_safe "${A54_SMB_CREDENTIALS}" || return 1
        jv_trace_run install -d -m0700 -o root -g root "$(dirname -- "${A54_SMB_CREDENTIALS}")" || return 1
        tmp="$(mktemp "$(dirname -- "${A54_SMB_CREDENTIALS}")/.credentials.restore.XXXXXX")" || return 1
        if ! cat -- "${snapshot}" > "${tmp}" ||
           ! chmod "${A54_CREDENTIALS_MODE}" "${tmp}" ||
           ! chown "${A54_CREDENTIALS_UID}:${A54_CREDENTIALS_GID}" "${tmp}" ||
           ! sync -f -- "${tmp}" || ! mv -fT -- "${tmp}" "${A54_SMB_CREDENTIALS}"; then
            rm -f -- "${tmp}"
            return 1
        fi
    else
        [[ ! -e ${A54_SMB_CREDENTIALS} ]] || jv_storage_credentials_safe "${A54_SMB_CREDENTIALS}" || return 1
        rm -f -- "${A54_SMB_CREDENTIALS}" || return 1
    fi
    return 0
}

_a53_load_manifest_for_rollback() {
    local operation_id fingerprint
    A53_TX_DIR="${A53_TRANSACTION_ROOT}/${A53_OPERATION_ID}"
    A53_MANIFEST="${A53_TX_DIR}/manifest.json"
    [[ -d ${A53_TX_DIR} && ! -L ${A53_TX_DIR} && -f ${A53_MANIFEST} && ! -L ${A53_MANIFEST} ]] || return 1
    jq -e '(.schema_version == "v1" or .schema_version == "v2") and (.mounts_by_transaction|type == "array") and all(.mounts_by_transaction[]; (.mountpoint|type == "string") and (.source|type == "string") and (.uuid|type == "string"))' "${A53_MANIFEST}" >/dev/null || return 1
    operation_id="$(jq -r '.operation_id // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
    fingerprint="$(jq -r '.plan_fingerprint // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
    [[ ${operation_id} == "${A53_OPERATION_ID}" && ${fingerprint} == "${A53_PLAN_FINGERPRINT}" ]] || return 1
    local credentials_path
    credentials_path="$(jq -r '.credentials_path // ""' "${A53_MANIFEST}")" || return 1
    if [[ -n ${credentials_path} ]]; then
        [[ ${credentials_path} == "/etc/justvoxel/smb-backup-${A53_OPERATION_ID}.credentials" || ${credentials_path} == "${A54_SMB_CREDENTIALS}" ]] || return 1
        A54_SMB_CREDENTIALS="${credentials_path}"
    fi
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
            jv_trace_run rmdir -- "${path}" >/dev/null 2>&1 || true
        fi
    done
}

_a53_rollback() {
    jv_trace_stage "ROLLBACK Storage recovery"
    local entry point source uuid failure=0
    _a53_manifest_set_rollback running rollback_in_progress || return 1
    _a53_prepare_rollback_resources || { _a53_manifest_set_rollback failed needs_attention; return 1; }
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        source="$(jq -r '.source' <<< "${entry}")"
        uuid="$(jq -r '.uuid' <<< "${entry}")"
        if ! _a53_resource_path_safe "${point}"; then failure=1; continue; fi
        if _a53_mount_preserved "${point}"; then continue; fi
        if ! _a53_mount_unused "${point}" "${uuid}" "${source}"; then
            _a53_preserve_mount "${point}" "${A53_REFERENCE_REASON}" || failure=1
            continue
        fi
        # Check fstab ownership/conflicts before changing live topology.
        if ! _a53_mount_definition_safe "${point}"; then
            _a53_preserve_mount "${point}" conflicting_fstab || true
            failure=1
            continue
        fi
        if mountpoint -q -- "${point}"; then
            if [[ -z ${source} ]] || ! jv_storage_identity_matches "${point}" "${source}" "${uuid}"; then
                _a53_preserve_mount "${point}" different_or_uncertain_live_identity || true
                failure=1
                continue
            fi
            # References are checked again immediately before disruption.
            if ! _a53_mount_unused "${point}" "${uuid}" "${source}"; then
                _a53_preserve_mount "${point}" "${A53_REFERENCE_REASON}" || failure=1
                continue
            fi
            jv_trace_run umount -- "${point}" >/dev/null 2>&1 || { failure=1; continue; }
            ! mountpoint -q -- "${point}" || failure=1
        fi
    done < <(jq -c '.mounts_by_transaction | reverse[]' "${A53_MANIFEST}")
    _a53_restore_fstab || failure=1
    _a54_restore_smb_credentials || failure=1
    _a53_restore_retired_entries || failure=1
    _a53_restore_retired_mounts || failure=1
    _a53_remove_created_empty_dirs
    if (( failure )); then
        _a53_manifest_set_rollback failed needs_attention || true
        return 1
    fi
    # Keep immutable private snapshots for idempotent retries and diagnosis.
    _a53_manifest_set_phase storage_rolled_back && _a53_manifest_set_rollback succeeded rolled_back
}

# The original complete fstab and private credentials are already snapshotted.
_a53_retire_obsolete_mounts() {
    # Legacy fixture support only; never reachable from v2 Apply.
    [[ $(jq -r .schema_version "${A53_MANIFEST}") == v1 ]] || return 1
    local point source selected entry was_mounted
    selected="$(jq -r '[.storage.mount_point // "", .backups.mount_point // ""][]' <<< "${A53_REQUEST}")"
    [[ ! -e ${A53_FSTAB} ]] && return 0
    [[ -f ${A53_FSTAB} && ! -L ${A53_FSTAB} ]] || return 1
    local -a points=()
    mapfile -t points < <(awk '$0 !~ /^[[:space:]]*#/ && NF >= 4 {print $2}' "${A53_FSTAB}" | sort -u)
    for point in "${points[@]}"; do
        [[ ${point} == /* && ${point} != *[[:space:]]* ]] || continue
        grep -Fxq -- "${point}" <<< "${selected}" && continue
        source="$(jv_storage_managed_entry "${A53_FSTAB}" "${point}" "${A54_SMB_CREDENTIALS}")" || {
            # Ambiguous definitions at reserved paths must not be guessed away.
            case "${point}" in /var/mnt/justvoxel-data|/var/mnt/justvoxel-backup) return 1 ;; esac
            continue
        }
        was_mounted=false
        if mountpoint -q -- "${point}"; then
            jv_storage_identity_matches "${point}" "${source}" || return 1
            was_mounted=true
        fi
        entry="$(jq -cn --arg mountpoint "${point}" --arg source "${source}" --argjson mounted "${was_mounted}" '{mountpoint:$mountpoint,source:$source,was_mounted:$mounted}')" || return 1
        _a53_manifest_update ".retired_mounts += [${entry}]" || return 1
        if [[ ${was_mounted} == true ]]; then
            # Recheck immediately before disruption.
            jv_storage_identity_matches "${point}" "${source}" || return 1
            jv_trace_run umount -- "${point}" || return 1
        fi
        jv_storage_remove_entry "${A53_FSTAB}" "${point}" || return 1
        _a53_manifest_set_fstab_changed || return 1
        jv_storage_clear_retired_failure "${point}" || return 1
    done
}

_a53_remove_obsolete_credentials() {
    [[ $(jq -r '.backups.type' <<< "${A53_REQUEST}") != smb ]] || return 0
    [[ ${A54_CREDENTIALS_EXISTED} == true ]] || return 0
    jv_storage_credentials_referenced "${A53_FSTAB}" "${A54_SMB_CREDENTIALS}" && return 0
    # A snapshot alone does not imply ownership: only a retired managed reference does.
    jq -e 'any(.retired_mounts[]?; .source | startswith("//"))' "${A53_MANIFEST}" >/dev/null || return 0
    jv_storage_credentials_safe "${A54_SMB_CREDENTIALS}" || return 1
    A54_CREDENTIALS_CHANGED=true
    _a53_manifest_update '.credentials_changed = true | .credentials_after_sha256 = "absent"' || return 1
    rm -f -- "${A54_SMB_CREDENTIALS}"
}

_a53_restore_retired_mounts() {
    [[ $(jq -r .schema_version "${A53_MANIFEST}") == v1 ]] || return 0
    local entry point source uuid device filesystem
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        source="$(jq -r '.source' <<< "${entry}")"
        uuid="$(jq -r '.uuid // ""' <<< "${entry}")"
        [[ $(_a53_fstab_lines "${A53_FSTAB}" "${point}") == "$(jq -r '.exact_line' <<< "${entry}")" ]] || return 1
        if mountpoint -q -- "${point}"; then
            jv_storage_identity_matches "${point}" "${source}" "${uuid}" || return 1
        else
            # A local historical UUID must resolve to its expected filesystem
            # before mount is attempted. Network identity is the exact old entry.
            if [[ ${source} == UUID=* ]]; then
                device="$(jv_trace_run blkid -U "${source#UUID=}" 2>/dev/null)" || return 1
                [[ -n ${device} ]] || return 1
                [[ $(jv_trace_run blkid -s UUID -o value "${device}") == "${source#UUID=}" ]] || return 1
                filesystem="$(jq -r '.filesystem' <<< "${entry}")"
                [[ $(jv_trace_run blkid -s TYPE -o value "${device}") == "${filesystem}" ]] || return 1
            fi
            jv_trace_run mount "${point}" >/dev/null 2>&1 || return 1
            jv_storage_identity_matches "${point}" "${source}" "${uuid}" || return 1
        fi
    done < <(jq -c '.legacy_retired_resources[]? | select(.was_mounted == true)' "${A53_MANIFEST}")
}
