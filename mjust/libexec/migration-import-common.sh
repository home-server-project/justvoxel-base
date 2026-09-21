#!/usr/bin/bash
# Sourced by migration-import: transaction helpers and fresh-destination prompts.
rollback_import() {
    local rollback_ok=yes validation_started=no
    rollback_attempted=yes
    echo >&2
    if [[ ${configured} == yes ]]; then
        echo 'Import did not validate. Attempting automatic rollback to the original JustVoxel server.' >&2
    else
        echo 'Import did not validate. Returning JustVoxel to its previous unconfigured state.' >&2
    fi
    jv_migration_write_state "${transaction}" rollback "${JV_MIGRATION_SOURCE:-unknown}" "${source_class}" || true

    systemctl stop minecraft.service >/dev/null 2>&1 || true

    if ! jv_migration_rollback_data "${transaction}" "${DATA_PATH}" "${had_existing_data}"; then
        rollback_ok=no
    fi

    if [[ ${configured} == no ]]; then
        jv_migration_remove_fresh_container || true
        jv_migration_remove_fresh_selinux_rule "${DATA_PATH}"
    fi

    if ! jv_migration_restore_runtime "${transaction}"; then
        rollback_ok=no
    fi

    if [[ ${configured} == yes && ${rollback_ok} == yes ]]; then
        # Reload destination variables from the restored administrator configuration.
        # shellcheck disable=SC1090
        source "${JV_CONFIG}" || rollback_ok=no
        GAME_MODE="${GAME_MODE:-survival}"
        DIFFICULTY="${DIFFICULTY:-normal}"
        WHITELIST_ENABLED="${WHITELIST_ENABLED:-yes}"
        ENFORCE_WHITELIST="${ENFORCE_WHITELIST:-yes}"
        BEDROCK_MANAGED_PLUGINS="${BEDROCK_MANAGED_PLUGINS:-yes}"
        if [[ ${rollback_ok} == yes ]]; then
            apply_data_selinux || rollback_ok=no
        fi
    fi

    if [[ ${configured} == yes && ${rollback_ok} == yes ]]; then
        # Validate the recovered server. If it was stopped before import, start it
        # temporarily for validation and return it to the stopped state afterward.
        if systemctl start minecraft.service; then
            validation_started=yes
            if /usr/libexec/justvoxel/mjust/restore-runtime-validate \
                && /usr/libexec/justvoxel/mjust/validate; then
                if [[ ${minecraft_was_active} != yes ]]; then
                    systemctl stop minecraft.service || rollback_ok=no
                fi
            else
                rollback_ok=no
            fi
        else
            rollback_ok=no
        fi
    elif [[ ${configured} == no && ${rollback_ok} == yes ]]; then
        # Fresh import rollback must not leave generated JustVoxel runtime files.
        while IFS= read -r path; do
            [[ -e ${path} || -L ${path} ]] && rollback_ok=no
        done < <(jv_migration_runtime_paths)
    fi

    if [[ ${rollback_ok} == yes ]]; then
        live_modified=no
        if [[ ${configured} == yes ]]; then
            jv_migration_write_state "${transaction}" rolled-back "${JV_MIGRATION_SOURCE:-unknown}" "${source_class}" || true
            jv_migration_register_recovery "${transaction}" || echo "WARNING: retained migration recovery state could not be registered automatically." >&2
            echo 'Import failed.' >&2
            echo 'Original server restored and validated.' >&2
            echo "Failed imported data was retained at: ${transaction}/failed-import" >&2
        else
            jv_migration_write_state "${transaction}" rolled-back-fresh "${JV_MIGRATION_SOURCE:-unknown}" "${source_class}" || true
            jv_migration_register_recovery "${transaction}" || echo "WARNING: retained fresh-import recovery state could not be registered automatically." >&2
            echo 'Import failed.' >&2
            echo 'JustVoxel returned to its previous unconfigured runtime state.' >&2
            echo "Failed imported data was retained at: ${transaction}/failed-import" >&2
        fi
        return 0
    fi

    jv_migration_write_state "${transaction}" critical-rollback "${JV_MIGRATION_SOURCE:-unknown}" "${source_class}" || true
    jv_migration_register_recovery "${transaction}" || echo "WARNING: critical migration recovery state could not be registered automatically." >&2
    echo 'CRITICAL: automatic import rollback could not be fully validated.' >&2
    echo "ALL recovery state was retained at: ${transaction}" >&2
    echo 'Do not delete that directory until the destination server has been recovered.' >&2
    return 1
}

on_exit() {
    local rc=$?
    trap - EXIT INT TERM
    if (( rc != 0 )); then
        if [[ ${live_modified} == yes && ${validated} != yes && -n ${transaction:-} && -d ${transaction} ]]; then
            rollback_import || true
        elif [[ ${minecraft_stopped_by_import} == yes && ${live_modified} != yes && ${minecraft_was_active} == yes ]]; then
            echo 'Import failed before live data changed; restarting the previously running Minecraft server.' >&2
            systemctl start minecraft.service || true
        fi
    fi
    # Staging created before live activation is disposable. Preserve transaction
    # evidence only after a rollback attempt or a critical/live-state failure.
    if [[ ${live_modified} != yes && ${rollback_attempted} != yes && -n ${transaction:-} && -d ${transaction} ]]; then
        rm -rf -- "${transaction}" || true
        transaction=''
    fi
    if [[ ${transport_started} == yes ]]; then
        jv_migration_transport_cleanup || true
    fi
    exit "${rc}"
}
trap on_exit EXIT INT TERM

prepare_fresh_data_destination() {
    local choice rc
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        DATA_PATH="${JV_MIGRATION_API_DATA_PATH:-}"
        DATA_MOUNT_POINT="${JV_MIGRATION_API_DATA_MOUNT_POINT:-}"
        DATA_EXPECTED_UUID="${JV_MIGRATION_API_DATA_EXPECTED_UUID:-}"
        DATA_EXPECTED_SOURCE="${JV_MIGRATION_API_DATA_EXPECTED_SOURCE:-}"
        [[ -n ${DATA_PATH} ]] || { echo 'ERROR: Management API import did not provide a data path.' >&2; return 1; }
        DATA_PATH="$(realpath -m -- "${DATA_PATH}")"
        validate_storage_path "${DATA_PATH}" || { echo 'ERROR: invalid Minecraft data path.' >&2; return 1; }
        if [[ -n ${DATA_MOUNT_POINT} ]]; then
            DATA_MOUNT_POINT="$(realpath -m -- "${DATA_MOUNT_POINT}")"
            mountpoint -q -- "${DATA_MOUNT_POINT}" || { echo 'ERROR: reviewed Minecraft data mount is not active.' >&2; return 1; }
            case "${DATA_PATH}/" in "${DATA_MOUNT_POINT}/"*) ;; *) echo 'ERROR: reviewed Minecraft data path is outside its mount.' >&2; return 1 ;; esac
        fi
        if [[ -e ${DATA_PATH} ]]; then
            [[ -d ${DATA_PATH} && ! -L ${DATA_PATH} ]] || { echo 'ERROR: fresh DATA_PATH must be a real directory.' >&2; return 1; }
            if find "${DATA_PATH}" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
                echo "ERROR: fresh import destination is not empty: ${DATA_PATH}" >&2
                return 1
            fi
        else
            install -d -m0750 -o root -g root "${DATA_PATH}"
        fi
        return 0
    fi
    choice="$(jui_choose 'Destination Minecraft storage' \
        'Directory on the JustVoxel system filesystem' \
        'Dedicated/local disk or partition' \
        'Back')" || return 2
    case "${choice}" in
        'Directory on the JustVoxel system filesystem')
            DATA_PATH="$(jui_input 'Minecraft data path' '/var/lib/justvoxel/minecraft')" || return 2
            DATA_PATH="$(realpath -m -- "${DATA_PATH}")"
            validate_storage_path "${DATA_PATH}" || { echo 'ERROR: invalid Minecraft data path.' >&2; return 1; }
            DATA_MOUNT_POINT=''
            DATA_EXPECTED_UUID=''
            DATA_EXPECTED_SOURCE=''
            ;;
        'Dedicated/local disk or partition')
            if storage_prepare_data_target_interactive; then
                DATA_PATH="${STORAGE_PATH}"
                DATA_MOUNT_POINT="${STORAGE_MOUNT_POINT}"
                DATA_EXPECTED_UUID="${STORAGE_EXPECTED_UUID}"
                DATA_EXPECTED_SOURCE="${STORAGE_EXPECTED_SOURCE}"
            else
                rc=$?
                return "${rc}"
            fi
            ;;
        'Back') return 2 ;;
        *) return 2 ;;
    esac

    if [[ -e ${DATA_PATH} ]]; then
        [[ -d ${DATA_PATH} && ! -L ${DATA_PATH} ]] || { echo 'ERROR: fresh DATA_PATH must be a real directory.' >&2; return 1; }
        if find "${DATA_PATH}" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
            echo "ERROR: fresh import destination is not empty: ${DATA_PATH}" >&2
            return 1
        fi
    else
        install -d -m0750 -o root -g root "${DATA_PATH}"
    fi
}

prepare_fresh_backup_configuration() {
    local rc answer daily_time
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        BACKUP_TYPE="${JV_MIGRATION_API_BACKUP_TYPE:-system}"
        BACKUP_PATH="${JV_MIGRATION_API_BACKUP_PATH:-}"
        BACKUP_MOUNT_POINT="${JV_MIGRATION_API_BACKUP_MOUNT_POINT:-}"
        BACKUP_EXPECTED_UUID="${JV_MIGRATION_API_BACKUP_EXPECTED_UUID:-}"
        BACKUP_EXPECTED_SOURCE="${JV_MIGRATION_API_BACKUP_EXPECTED_SOURCE:-}"
        BACKUP_KEEP="${JV_MIGRATION_API_BACKUP_KEEP:-7}"
        BACKUP_SCHEDULE="${JV_MIGRATION_API_BACKUP_SCHEDULE:-*-*-* 04:30:00}"
        BACKUP_TIMER_ENABLED="${JV_MIGRATION_API_BACKUP_TIMER_ENABLED:-yes}"
        [[ -n ${BACKUP_PATH} ]] || { echo 'ERROR: Management API import did not provide a backup path.' >&2; return 1; }
        BACKUP_PATH="$(realpath -m -- "${BACKUP_PATH}")"
        validate_storage_path "${BACKUP_PATH}" || { echo 'ERROR: invalid backup path.' >&2; return 1; }
        [[ ${BACKUP_PATH} != "${DATA_PATH}" && ${BACKUP_PATH} != "${DATA_PATH}/"* ]] || { echo 'ERROR: backup path cannot be the Minecraft data directory or a child of it.' >&2; return 1; }
        validate_positive_int "${BACKUP_KEEP}" || { echo 'ERROR: backup retention must be a positive integer.' >&2; return 1; }
        [[ ${BACKUP_TIMER_ENABLED} == yes || ${BACKUP_TIMER_ENABLED} == no ]] || { echo 'ERROR: invalid backup timer state.' >&2; return 1; }
        if [[ -n ${BACKUP_MOUNT_POINT} ]]; then
            BACKUP_MOUNT_POINT="$(realpath -m -- "${BACKUP_MOUNT_POINT}")"
            mountpoint -q -- "${BACKUP_MOUNT_POINT}" || { echo 'ERROR: reviewed backup mount is not active.' >&2; return 1; }
            case "${BACKUP_PATH}/" in "${BACKUP_MOUNT_POINT}/"*) ;; *) echo 'ERROR: reviewed backup path is outside its mount.' >&2; return 1 ;; esac
        fi
        install -d -m0750 -o root -g root "${BACKUP_PATH}"
        return 0
    fi
    if storage_prepare_backup_interactive yes; then
        :
    else
        rc=$?
        return "${rc}"
    fi
    BACKUP_PATH="$(realpath -m -- "${BACKUP_PATH}")"
    validate_storage_path "${BACKUP_PATH}" || { echo 'ERROR: invalid backup path.' >&2; return 1; }
    if [[ ${BACKUP_PATH} == "${DATA_PATH}" || ${BACKUP_PATH} == "${DATA_PATH}/"* ]]; then
        echo 'ERROR: backup path cannot be the Minecraft data directory or a child of it.' >&2
        return 1
    fi
    BACKUP_KEEP="$(jui_input 'Number of backups to keep' '7')" || return 2
    validate_positive_int "${BACKUP_KEEP}" || { echo 'ERROR: backup retention must be a positive integer.' >&2; return 1; }

    while true; do
        daily_time="$(jui_input 'Daily backup time' '04:30')" || return 2
        if daily_time="$(normalize_daily_backup_time "${daily_time}")"; then break; fi
        echo 'ERROR: enter a valid daily time such as 04:30 or 21:15.' >&2
    done
    BACKUP_SCHEDULE="$(daily_backup_schedule_from_time "${daily_time}")"
    answer="$(jui_input 'Enable automatic daily backups? (yes/no)' 'yes')" || return 2
    case "${answer,,}" in
        yes|y) BACKUP_TIMER_ENABLED=yes ;;
        no|n) BACKUP_TIMER_ENABLED=no ;;
        *) echo 'ERROR: enter yes or no.' >&2; return 1 ;;
    esac
}

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

