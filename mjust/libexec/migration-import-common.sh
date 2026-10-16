#!/usr/bin/bash
# Sourced by migration-import: selected-instance transaction helpers.
jv_migration_api_result() {
    local outcome="$1" phase="$2" recovery_transaction="$3" status="$4" result_file="${JV_MIGRATION_API_RESULT_FILE:-}" tmp
    [[ ${JV_MIGRATION_API_MODE:-0} == 1 && -n ${result_file} ]] || return 0
    case "${result_file}" in "${JV_MIGRATION_RUN_ROOT}/"*) ;; *) echo 'ERROR: invalid Management API Import result path.' >&2; return 1 ;; esac
    install -d -m0700 -o root -g root "${JV_MIGRATION_RUN_ROOT}"
    tmp="${result_file}.tmp.$"
    jq -cn --arg outcome "${outcome}" --arg phase "${phase}" --arg transaction "${recovery_transaction}" --arg status "${status}"         '{outcome:$outcome,phase:$phase,transaction:$transaction,status:$status}' > "${tmp}"
    chmod 0600 "${tmp}"
    mv -f -- "${tmp}" "${result_file}"
}
rollback_import() {
    rollback_attempted=yes
    echo 'Import did not validate. Restoring the selected destination instance.' >&2
    jv_migration_write_state "${transaction}" rollback "${JV_MIGRATION_SOURCE:-unknown}" "${source_class}" || return 1
    if jv_migration_recover_selected "${transaction}"; then
        live_modified=no
        jv_migration_register_recovery "${transaction}" || return 1
        jv_migration_api_result rolled_back rolled-back "${transaction}" 'Import failed; the selected destination instance was restored and validated.'
        return 0
    fi
    jv_migration_write_state "${transaction}" critical-rollback "${JV_MIGRATION_SOURCE:-unknown}" "${source_class}" || true
    jv_migration_register_recovery "${transaction}" || true
    jv_migration_api_result needs_attention critical-rollback "${transaction}" 'Import rollback requires recovery of the selected destination instance.' || true
    return 1
}

on_exit() {
    local rc=$? preactivation_outcome=rolled_back
    local preactivation_status='Server migration Import stopped before live activation; no Minecraft data was changed.'
    trap - EXIT INT TERM
    if (( rc != 0 )); then
        if [[ ${live_modified} == yes && ${validated} != yes && -n ${transaction:-} && -d ${transaction} ]]; then
            rollback_import || true
        elif [[ ${minecraft_stopped_by_import} == yes && ${live_modified} != yes && ${minecraft_was_active} == yes ]]; then
            echo 'Import failed before live data changed; restarting the previously running Minecraft server.' >&2
            if systemctl start "${JV_INSTANCE_UNIT}" >/dev/null 2>&1; then
                minecraft_stopped_by_import=no
            else
                jv_migration_register_recovery "${transaction}" || true
                preactivation_outcome=needs_attention
                preactivation_status='Server migration Import failed before activation, but the previously running Minecraft server could not be restarted automatically.'
            fi
        fi
    fi
    # Staging created before live activation is disposable. Preserve transaction
    # evidence only after a rollback attempt or a critical/live-state failure.
    if [[ ${preactivation_outcome} != needs_attention && ${live_modified} != yes && ${rollback_attempted} != yes && -n ${transaction:-} && -d ${transaction} ]]; then
        rm -rf -- "${transaction}" || true
        transaction=''
    fi
    if [[ ${transport_started} == yes ]]; then
        jv_migration_transport_cleanup || true
    fi
    if (( rc != 0 )) && [[ ${live_modified} != yes && ${rollback_attempted} != yes ]]; then
        jv_migration_api_result "${preactivation_outcome}" pre-activation '' "${preactivation_status}" || true
    fi
    exit "${rc}"
}
trap on_exit EXIT INT TERM

select_candidate() {
    local detection="$1" base="$2" count selection index candidate label
    local -a labels=() candidates=()
    count="$(jq -r '.candidates | length' <<< "${detection}")"
    (( count > 0 )) || { echo 'ERROR: no recognizable Minecraft server root was found.' >&2; return 1; }
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        local requested="${JV_MIGRATION_API_SELECTED_ROOT_REL:-}"
        if (( count == 1 )) && [[ -z ${requested} ]]; then
            jq -c '.candidates[0]' <<< "${detection}"
            return 0
        fi
        [[ -n ${requested} ]] || { echo 'ERROR: multiple Minecraft roots were detected and no reviewed root was supplied.' >&2; return 1; }
        for (( index=0; index<count; index++ )); do
            candidate="$(jq -c ".candidates[${index}]" <<< "${detection}")"
            root="$(jq -r '.root' <<< "${candidate}")"
            if [[ "${root#${base}/}" == "${requested}" ]]; then
                printf '%s' "${candidate}"
                return 0
            fi
        done
        echo 'ERROR: the reviewed Minecraft root no longer exists in staged source data.' >&2
        return 1
    fi
    if (( count == 1 )); then
        jq -c '.candidates[0]' <<< "${detection}"
        return 0
    fi
    echo 'Multiple possible Minecraft server roots were found. JustVoxel will not guess.'
    for (( index=0; index<count; index++ )); do
        candidate="$(jq -c ".candidates[${index}]" <<< "${detection}")"
        root="$(jq -r '.root' <<< "${candidate}")"
        type="$(jq -r '.sourceType' <<< "${candidate}")"
        version="$(jq -r '.minecraftVersion // "unknown"' <<< "${candidate}")"
        label="${root#${base}/}   [${type}, Minecraft ${version}]"
        labels+=("${label}")
        candidates+=("${candidate}")
    done
    labels+=('Back')
    selection="$(jui_choose 'Select Minecraft server root' "${labels[@]}")" || return 2
    [[ ${selection} == Back ]] && return 2
    for index in "${!labels[@]}"; do
        if [[ ${labels[index]} == "${selection}" && ${index} -lt ${#candidates[@]} ]]; then
            printf '%s' "${candidates[index]}"
            return 0
        fi
    done
    return 1
}

prompt_source_online_mode() {
    local answer
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        [[ ${JV_MIGRATION_API_ONLINE_MODE_CONFIRMED:-no} == yes ]]
        return
    fi
    echo
    echo 'JustVoxel could not determine the source Java online-mode setting.'
    echo 'Player UUID identity must not change during migration.'
    echo 'Only continue if you have verified that the source server used online-mode=true.'
    printf 'Type ONLINE to confirm source online-mode=true: ' >/dev/tty
    IFS= read -r answer </dev/tty
    [[ ${answer} == ONLINE ]]
}

