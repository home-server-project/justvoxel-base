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
    # Instance creation never retires existing global mounts.
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
    # Credentials belonging to other destinations remain untouched.
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
    local request
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
        (.schema_version == "v1" or .schema_version == "v2") and
        (.fstab_existed|type == "boolean") and (.fstab_changed|type == "boolean") and
        (.credentials_existed|type == "boolean") and (.credentials_changed|type == "boolean") and
        (.mounts_by_transaction|type == "array") and
        all(.mounts_by_transaction[]; (.mountpoint|type == "string") and (.source|type == "string") and (.uuid|type == "string")) and
        ((.rollback.state == "failed" and .rollback.result == "needs_attention") or
         (.rollback.state == "running" and (.rollback.result == "recovery_in_progress" or .rollback.result == "rollback_in_progress")) or
         (.runtime_rollback_verified == true and .rollback.state == "not_started") or
         (.phase == "storage_rolled_back" and .rollback.state == "succeeded" and .rollback.result == "rolled_back"))
    ' "${A53_MANIFEST}" >/dev/null 2>&1; then
        _a53_recovery_fail
        return 0
    fi

    if ! _a53_rollback recovery; then
        _a53_recovery_fail
        return 0
    fi
    _a53_evidence_set recovery_result rolled_back
    _a53_evidence_set fstab_recovery_state "$([[ ${A53_FSTAB_CHANGED} == true ]] && echo resource_rollback || echo not_changed)"
    _a53_evidence_from_manifest
    _a53_json true false storage_rolled_back succeeded rolled_back ''
}
