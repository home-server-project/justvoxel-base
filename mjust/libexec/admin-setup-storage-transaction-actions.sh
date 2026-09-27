_a53_apply_failure() {
    local message="$1"
    if [[ -n ${A53_MANIFEST} && -f ${A53_MANIFEST} ]]; then
        if _a53_rollback immediate; then
            _a53_evidence_set rollback_result succeeded
            _a53_evidence_from_manifest
            _a53_json false false storage_failed succeeded rolled_back "${message}"
        else
            _a53_evidence_set rollback_result failed
            _a53_evidence_from_manifest
            _a53_json false false storage_failed failed needs_attention "${message}"
        fi
    else
        _a53_evidence_set rollback_result no_changes
        _a53_json false false storage_failed not_started no_changes "${message}"
    fi
}

a53_validate_action() {
    if ! _a53_parse_request; then
        _a53_json false false storage_preflight not_started no_changes 'Setup storage transaction request is invalid.'
        return 0
    fi
    _a53_evidence_set data_type "$(jq -r '.storage.type' <<< "${A53_REQUEST}")"
    _a53_evidence_set backup_type "$(jq -r '.backups.type' <<< "${A53_REQUEST}")"
    local validate_rc=0
    _a53_validate_all || validate_rc=$?
    if (( validate_rc != 0 )); then
        _a53_evidence_set storage_validation failed
        case ${validate_rc} in
            2) _a53_json false false storage_preflight not_started no_changes 'A5.4 supports system storage, existing local filesystems, NFS, and SMB backups.' ;;
            3) _a53_json false false storage_preflight not_started no_changes 'SMB password is required only when setup is executed.' ;;
            *) _a53_json false false storage_preflight not_started no_changes 'Reviewed storage no longer matches the current appliance state.' ;;
        esac
        return 0
    fi
    _a53_evidence_set storage_validation passed
    _a53_json true false storage_preflight not_started no_changes ''
}

a53_apply_action() {
    local storage backups prep_rc
    if ! _a53_parse_request; then
        _a53_json false false storage_preflight not_started no_changes 'Setup storage transaction request is invalid.'
        return 0
    fi
    _a53_evidence_set data_type "$(jq -r '.storage.type' <<< "${A53_REQUEST}")"
    _a53_evidence_set backup_type "$(jq -r '.backups.type' <<< "${A53_REQUEST}")"
    local validate_rc=0
    _a53_validate_all || validate_rc=$?
    if (( validate_rc != 0 )); then
        _a53_evidence_set storage_validation failed
        case ${validate_rc} in
            2) _a53_json false false storage_preflight not_started no_changes 'A5.4 supports system storage, existing local filesystems, NFS, and SMB backups.' ;;
            3) _a53_json false false storage_preflight not_started no_changes 'SMB password is required only when setup is executed.' ;;
            *) _a53_json false false storage_preflight not_started no_changes 'Reviewed storage no longer matches the current appliance state.' ;;
        esac
        return 0
    fi
    _a53_evidence_set storage_validation passed

    _a53_prepare_transaction
    prep_rc=$?
    if (( prep_rc == 2 )); then
        _a53_json false false storage_snapshot not_started no_changes 'A storage transaction already exists for this operation.'
        return 0
    elif (( prep_rc != 0 )); then
        _a53_json false false storage_snapshot not_started no_changes 'Could not create the private storage rollback point.'
        return 0
    fi
    _a53_evidence_set transaction_snapshot created

    storage="$(jq -c '.storage' <<< "${A53_REQUEST}")"
    backups="$(jq -c '.backups' <<< "${A53_REQUEST}")"
    _a53_manifest_set_phase storage_mount || { _a53_apply_failure 'Could not persist storage transaction state.'; return 0; }
    _a53_mount_target "${storage}" data || { _a53_apply_failure 'Minecraft local storage could not be mounted safely.'; return 0; }

    case "$(jq -r '.type' <<< "${backups}")" in
        partition)
            if [[ $(jq -r '.mount_point' <<< "${backups}") != "$(jq -r '.mount_point' <<< "${storage}")" || $(jq -r '.expected_uuid' <<< "${backups}") != "$(jq -r '.expected_uuid' <<< "${storage}")" ]]; then
                _a53_mount_target "${backups}" backup || { _a53_apply_failure 'Backup local storage could not be mounted safely.'; return 0; }
            else
                _a53_evidence_set backup_mount_result shared_with_data
            fi
            ;;
        nfs|smb)
            _a54_mount_network_target "${backups}" || { _a53_apply_failure 'Network backup storage could not be mounted and verified safely.'; return 0; }
            ;;
        *)
            _a53_evidence_set backup_mount_result not_applicable
            ;;
    esac

    _a53_manifest_set_phase storage_paths || { _a53_apply_failure 'Could not persist storage transaction state.'; return 0; }
    _a53_prepare_paths || { _a53_apply_failure 'Local storage directories could not be prepared or verified writable.'; return 0; }
    _a53_manifest_set_phase storage_verified || { _a53_apply_failure 'Could not persist the verified storage transaction state.'; return 0; }
    _a53_evidence_from_manifest
    _a53_json true true storage_verified not_started '' ''
}

a53_rollback_action() {
    if ! _a53_parse_request; then
        _a53_json false false storage_rollback failed needs_attention 'Setup storage rollback request is invalid.'
        return 0
    fi
    if ! _a53_load_manifest_for_rollback; then
        _a53_json false false storage_rollback failed needs_attention 'Storage rollback evidence is missing or does not match this operation.'
        return 0
    fi
    _a53_evidence_from_manifest
    if _a53_rollback explicit; then
        _a53_evidence_set rollback_result succeeded
        _a53_evidence_from_manifest
        _a53_json true false storage_rolled_back succeeded rolled_back ''
    else
        _a53_evidence_set rollback_result failed
        _a53_evidence_from_manifest
        _a53_json false false storage_rollback failed needs_attention 'Storage rollback could not be completed safely; recovery evidence was preserved.'
    fi
}

_a53_recovery_fail() {
    _a53_evidence_set recovery_result needs_attention
    _a53_manifest_set_rollback failed needs_attention >/dev/null 2>&1 || true
    _a53_evidence_from_manifest
    _a53_json false false storage_rollback failed needs_attention 'Storage recovery could not be completed safely; recovery evidence was preserved.'
}

a53_recover_action() {
    local request before after current credentials_before credentials_after credentials_current
    local entry mountpoint uuid source current_source current_uuid i
    request="$(cat)"
    if ! jq -e 'type == "object" and (keys | sort) == ["operation_id","plan_fingerprint","schema_version"] and .schema_version == "v1" and (.operation_id|type == "string") and (.plan_fingerprint|type == "string")' >/dev/null 2>&1 <<< "${request}"; then
        _a53_json false false storage_rollback failed needs_attention 'Setup storage recovery request is invalid.'
        return 0
    fi
    A53_OPERATION_ID="$(jq -r '.operation_id' <<< "${request}")"
    A53_PLAN_FINGERPRINT="$(jq -r '.plan_fingerprint' <<< "${request}")"
    if [[ ! ${A53_OPERATION_ID} =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ || ! ${A53_PLAN_FINGERPRINT} =~ ^sha256:[0-9a-f]{64}$ ]] || ! _a53_load_manifest_for_rollback; then
        _a53_json false false storage_rollback failed needs_attention 'Storage recovery evidence is missing or does not match this operation.'
        return 0
    fi
    _a53_evidence_from_manifest
    if ! jq -e '
        .schema_version == "v1" and
        (.fstab_existed|type == "boolean") and (.fstab_changed|type == "boolean") and
        (.credentials_existed|type == "boolean") and (.credentials_changed|type == "boolean") and
        (.mounts_by_transaction|type == "array") and
        all(.mounts_by_transaction[]; (.mountpoint|type == "string") and (.source|type == "string") and (.uuid|type == "string")) and
        ((.rollback.state == "failed" and .rollback.result == "needs_attention") or
         (.rollback.state == "running" and .rollback.result == "recovery_in_progress") or
         (.phase == "storage_rolled_back" and .rollback.state == "succeeded" and .rollback.result == "rolled_back"))
    ' "${A53_MANIFEST}" >/dev/null 2>&1; then
        _a53_recovery_fail
        return 0
    fi

    if [[ ${A53_FSTAB_CHANGED} == true ]]; then
        before="$(jq -r '.fstab_before_sha256 // ""' "${A53_MANIFEST}")"
        after="$(jq -r '.fstab_after_sha256 // ""' "${A53_MANIFEST}")"
        current="$(_a53_file_sha256 "${A53_FSTAB}")"
        [[ ${A53_FSTAB_EXISTED} == true ]] || before=absent
        if [[ ${A53_FSTAB_EXISTED} == true ]]; then
            [[ -f ${A53_TX_DIR}/fstab.before && $(_a53_file_sha256 "${A53_TX_DIR}/fstab.before") == "${before}" ]] || { _a53_recovery_fail; return 0; }
        fi
        if [[ ${current} != "${before}" ]]; then
            [[ ${after} =~ ^[0-9a-f]{64}$ && ${current} == "${after}" ]] || { _a53_recovery_fail; return 0; }
        fi
        _a53_evidence_set fstab_recovery_state "$([[ ${current} == "${before}" ]] && echo already_restored || echo after_state)"
    else
        _a53_evidence_set fstab_recovery_state not_changed
    fi

    if [[ ${A54_CREDENTIALS_CHANGED} == true ]]; then
        credentials_before="$(jq -r '.credentials_before_sha256 // ""' "${A53_MANIFEST}")"
        credentials_after="$(jq -r '.credentials_after_sha256 // ""' "${A53_MANIFEST}")"
        credentials_current="$(_a53_file_sha256 "${A54_SMB_CREDENTIALS}")"
        [[ ${A54_CREDENTIALS_EXISTED} == true ]] || credentials_before=absent
        if [[ ${A54_CREDENTIALS_EXISTED} == true ]]; then
            [[ -f ${A53_TX_DIR}/smb.credentials.before && $(_a53_file_sha256 "${A53_TX_DIR}/smb.credentials.before") == "${credentials_before}" ]] || { _a53_recovery_fail; return 0; }
        fi
        if [[ ${credentials_current} != "${credentials_before}" ]]; then
            [[ ${credentials_after} =~ ^[0-9a-f]{64}$ && ${credentials_current} == "${credentials_after}" ]] || { _a53_recovery_fail; return 0; }
        fi
        _a53_evidence_set credentials_recovery_state "$([[ ${credentials_current} == "${credentials_before}" ]] && echo already_restored || echo after_state)"
    fi

    mapfile -t A53_MOUNTS_BY_US < <(jq -c '.mounts_by_transaction[]' "${A53_MANIFEST}")
    for entry in "${A53_MOUNTS_BY_US[@]}"; do
        mountpoint="$(jq -r '.mountpoint' <<< "${entry}")"
        uuid="$(jq -r '.uuid' <<< "${entry}")"
        source="$(jq -r '.source' <<< "${entry}")"
        [[ -n ${source} && $(_a53_normalize_path "${mountpoint}") == "${mountpoint}" ]] && storage_validate_mountpoint_path "${mountpoint}" >/dev/null 2>&1 || { _a53_recovery_fail; return 0; }
        if mountpoint -q -- "${mountpoint}"; then
            current_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null)" || { _a53_recovery_fail; return 0; }
            [[ ${current_source} == "${source}" ]] || { _a53_recovery_fail; return 0; }
            if [[ -n ${uuid} ]]; then
                current_uuid="$(jv_exact_mount_identity UUID "${mountpoint}" 2>/dev/null)" || { _a53_recovery_fail; return 0; }
                [[ ${current_uuid} == "${uuid}" ]] || { _a53_recovery_fail; return 0; }
            fi
        fi
    done

    _a53_manifest_set_rollback running recovery_in_progress >/dev/null 2>&1 || { _a53_recovery_fail; return 0; }
    for (( i=${#A53_MOUNTS_BY_US[@]}-1; i>=0; i-- )); do
        mountpoint="$(jq -r '.mountpoint' <<< "${A53_MOUNTS_BY_US[$i]}")"
        if mountpoint -q -- "${mountpoint}"; then
            source="$(jq -r '.source' <<< "${A53_MOUNTS_BY_US[$i]}")"
            uuid="$(jq -r '.uuid' <<< "${A53_MOUNTS_BY_US[$i]}")"
            current_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null)" || { _a53_recovery_fail; return 0; }
            [[ ${current_source} == "${source}" ]] || { _a53_recovery_fail; return 0; }
            if [[ -n ${uuid} ]]; then
                current_uuid="$(jv_exact_mount_identity UUID "${mountpoint}" 2>/dev/null)" || { _a53_recovery_fail; return 0; }
                [[ ${current_uuid} == "${uuid}" ]] || { _a53_recovery_fail; return 0; }
            fi
            umount -- "${mountpoint}" >/dev/null 2>&1 || { _a53_recovery_fail; return 0; }
            ! mountpoint -q -- "${mountpoint}" || { _a53_recovery_fail; return 0; }
        fi
    done
    if [[ ${A53_FSTAB_CHANGED} == true ]]; then
        current="$(_a53_file_sha256 "${A53_FSTAB}")"
        if [[ ${current} != "${before}" ]]; then
            [[ ${current} == "${after}" ]] || { _a53_recovery_fail; return 0; }
            _a53_restore_fstab || { _a53_recovery_fail; return 0; }
        fi
        [[ $(_a53_file_sha256 "${A53_FSTAB}") == "${before}" ]] || { _a53_recovery_fail; return 0; }
    fi
    if [[ ${A54_CREDENTIALS_CHANGED} == true ]]; then
        credentials_current="$(_a53_file_sha256 "${A54_SMB_CREDENTIALS}")"
        if [[ ${credentials_current} != "${credentials_before}" ]]; then
            [[ ${credentials_current} == "${credentials_after}" ]] || { _a53_recovery_fail; return 0; }
            _a54_restore_smb_credentials || { _a53_recovery_fail; return 0; }
        fi
        [[ $(_a53_file_sha256 "${A54_SMB_CREDENTIALS}") == "${credentials_before}" ]] || { _a53_recovery_fail; return 0; }
    fi
    _a53_remove_created_empty_dirs
    _a53_manifest_set_phase storage_rolled_back && _a53_manifest_set_rollback succeeded rolled_back || { _a53_recovery_fail; return 0; }
    _a53_evidence_set recovery_result rolled_back
    _a53_evidence_from_manifest
    _a53_json true false storage_rolled_back succeeded rolled_back ''
}
