_a53_apply_failure() {
    local message="$1"
    if [[ -n ${A53_MANIFEST} && -f ${A53_MANIFEST} ]]; then
        if _a53_rollback immediate; then
            _a53_json false false storage_failed succeeded rolled_back "${message}"
        else
            _a53_json false false storage_failed failed needs_attention "${message}"
        fi
    else
        _a53_json false false storage_failed not_started no_changes "${message}"
    fi
}

a53_validate_action() {
    if ! _a53_parse_request; then
        _a53_json false false storage_preflight not_started no_changes 'Setup storage transaction request is invalid.'
        return 0
    fi
    local validate_rc=0
    _a53_validate_all || validate_rc=$?
    if (( validate_rc != 0 )); then
        case ${validate_rc} in
            2) _a53_json false false storage_preflight not_started no_changes 'A5.3 supports only system storage and existing local filesystems.' ;;
            *) _a53_json false false storage_preflight not_started no_changes 'Reviewed local storage no longer matches the current appliance state.' ;;
        esac
        return 0
    fi
    _a53_json true false storage_preflight not_started no_changes ''
}

a53_apply_action() {
    local storage backups prep_rc
    if ! _a53_parse_request; then
        _a53_json false false storage_preflight not_started no_changes 'Setup storage transaction request is invalid.'
        return 0
    fi
    local validate_rc=0
    _a53_validate_all || validate_rc=$?
    if (( validate_rc != 0 )); then
        case ${validate_rc} in
            2) _a53_json false false storage_preflight not_started no_changes 'A5.3 supports only system storage and existing local filesystems.' ;;
            *) _a53_json false false storage_preflight not_started no_changes 'Reviewed local storage no longer matches the current appliance state.' ;;
        esac
        return 0
    fi

    _a53_prepare_transaction
    prep_rc=$?
    if (( prep_rc == 2 )); then
        _a53_json false false storage_snapshot not_started no_changes 'A storage transaction already exists for this operation.'
        return 0
    elif (( prep_rc != 0 )); then
        _a53_json false false storage_snapshot not_started no_changes 'Could not create the private storage rollback point.'
        return 0
    fi

    storage="$(jq -c '.storage' <<< "${A53_REQUEST}")"
    backups="$(jq -c '.backups' <<< "${A53_REQUEST}")"
    _a53_manifest_set_phase storage_mount || { _a53_apply_failure 'Could not persist storage transaction state.'; return 0; }
    _a53_mount_target "${storage}" || { _a53_apply_failure 'Minecraft local storage could not be mounted safely.'; return 0; }

    if [[ $(jq -r '.type' <<< "${backups}") == partition ]]; then
        if [[ $(jq -r '.mount_point' <<< "${backups}") != "$(jq -r '.mount_point' <<< "${storage}")" || $(jq -r '.expected_uuid' <<< "${backups}") != "$(jq -r '.expected_uuid' <<< "${storage}")" ]]; then
            _a53_mount_target "${backups}" || { _a53_apply_failure 'Backup local storage could not be mounted safely.'; return 0; }
        fi
    fi

    _a53_manifest_set_phase storage_paths || { _a53_apply_failure 'Could not persist storage transaction state.'; return 0; }
    _a53_prepare_paths || { _a53_apply_failure 'Local storage directories could not be prepared or verified writable.'; return 0; }
    _a53_manifest_set_phase storage_verified || { _a53_apply_failure 'Could not persist the verified storage transaction state.'; return 0; }
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
    if _a53_rollback explicit; then
        _a53_json true false storage_rolled_back succeeded rolled_back ''
    else
        _a53_json false false storage_rollback failed needs_attention 'Storage rollback could not be completed safely; recovery evidence was preserved.'
    fi
}
