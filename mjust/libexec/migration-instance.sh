#!/usr/bin/bash
# Portable server migration context. The caller must consume --instance ID.
source /usr/libexec/justvoxel/mjust/instance-context.sh
jv_migration_load_instance() {
    jv_load_instance "$1" "${2:-}" || { echo 'ERROR: selected Minecraft instance is unavailable.' >&2; return 1; }
    case "${MINECRAFT_SERVER_TYPE-paper}" in paper|purpur|vanilla) ;; *)
        echo 'ERROR: migration for this server software requires separate compatibility support.' >&2
        return 1 ;;
    esac
    JV_MIGRATION_INSTANCE_CONFIG="/etc/justvoxel/instances/${JV_INSTANCE_ID}/config.json"
    JV_MIGRATION_INSTANCE_QUADLET="/etc/containers/systemd/${JV_INSTANCE_CONTAINER}.container"
    local record="${JV_MIGRATION_INSTANCE_CONFIG}"
    JAVA_PORT="$(jq -er '.runtime.java_port' "${record}")"
    BEDROCK_PORT="$(jq -er '.runtime.bedrock_port' "${record}")"
    JAVA_MEMORY="$(jq -er '.runtime.java_memory' "${record}")"
    CONTAINER_MEMORY="$(jq -er '.runtime.container_memory' "${record}")"
    TIMEZONE="$(jq -er '.runtime.timezone' "${record}")"
    GAME_MODE="$(jq -er '.runtime.game_mode' "${record}")"
    DIFFICULTY="$(jq -er '.runtime.difficulty' "${record}")"
    MAX_PLAYERS="$(jq -er '.runtime.max_players' "${record}")"
    MOTD="$(jq -r '.runtime.motd' "${record}")"
    WHITELIST_ENABLED="$(jq -r 'if .runtime.whitelist then "yes" else "no" end' "${record}")"
    ENFORCE_WHITELIST="$(jq -r 'if .runtime.enforce_whitelist then "yes" else "no" end' "${record}")"
    # Backup storage policy remains appliance-owned. Loading it never selects a server.
    if [[ -r /etc/justvoxel/minecraft-backup.env ]]; then
        source /etc/justvoxel/minecraft-backup.env
    fi
    BACKUP_PATH="${MINECRAFT_BACKUP_PATH:-}"
    BACKUP_KEEP="${BACKUP_KEEP:-7}"
    BACKUP_SCHEDULE="${BACKUP_SCHEDULE:-*-*-* 04:30:00}"
    BACKUP_TIMER_ENABLED="${BACKUP_TIMER_ENABLED:-yes}"
    MINECRAFT_DATA_PATH="${DATA_PATH}"
    MINECRAFT_SERVICE="${JV_INSTANCE_UNIT}"
    minecraft_data_path="${DATA_PATH}"
    minecraft_backup_path="${BACKUP_PATH}"
}
require_config() { jv_migration_load_instance "${JV_INSTANCE_ID:?}"; }
jv_migration_validate_data() {
    local -a fields
    mapfile -t fields < <(jq -er '[.runtime.id,.storage.root,.storage.data_path,.storage.mount_point,.storage.uuid,.storage.source] | .[]' "${JV_MIGRATION_INSTANCE_CONFIG}")
    [[ ${#fields[@]} == 6 && ${fields[0]} == "${JV_INSTANCE_ID}" && ${fields[2]} == "${DATA_PATH}" ]] || return 1
    /usr/libexec/justvoxel/mjust/validate-instance-data-mount "${fields[@]}"
}
jv_migration_validate_runtime() {
    /usr/libexec/justvoxel/mjust/validate-instance-runtime-commit "${JV_INSTANCE_ID}" || return 1
    jv_migration_validate_data || return 1
    JUSTVOXEL_MAINTENANCE_LOCK_HELD=1 /usr/libexec/justvoxel/mjust/verify-minecraft-stack --instance "${JV_INSTANCE_ID}" "${1:-${MINECRAFT_VERSION}}"
}
jv_migration_players_json() {
    local raw online state
    state="$(systemctl show --property=ActiveState --value "${JV_INSTANCE_UNIT}")" || return 1
    case "${state}" in
        inactive|failed) jq -cn '{state:"stopped",online:0,names:[]}' ;;
        active)
            raw="$(podman exec "${JV_INSTANCE_CONTAINER}" rcon-cli list)" || return 1
            [[ ${raw} =~ There[[:space:]]are[[:space:]]([0-9]+)[[:space:]]of[[:space:]]a[[:space:]]max[[:space:]]of[[:space:]]([0-9]+)[[:space:]]players[[:space:]]online:[[:space:]]*(.*)$ ]] || return 1
            online="${BASH_REMATCH[1]}"
            jq -cn --argjson online "${online}" --arg names "${BASH_REMATCH[3]}" '{state:"running",online:$online,names:(if $online==0 then [] else ($names|split(", ")) end)}' ;;
        *) return 1 ;;
    esac
}
# These migration-local interruption functions preserve the established bounded
# countdown while targeting only the immutable selected unit/container.
jv_player_list_raw() { podman exec "${JV_INSTANCE_CONTAINER}" rcon-cli list; }
jv_minecraft_still_running() {
    systemctl is-active --quiet "${JV_INSTANCE_UNIT}" || [[ $(podman inspect --format '{{.State.Running}}' "${JV_INSTANCE_CONTAINER}" 2>/dev/null) == true ]]
}
jv_minecraft_announce_shutdown() { podman exec "${JV_INSTANCE_CONTAINER}" rcon-cli "say Server shutting down in $1 seconds" >/dev/null; }
jv_begin_minecraft_stop() { systemctl --no-block stop "${JV_INSTANCE_UNIT}"; }
jv_bypass_container_shutdown_delay() {
    local attempt
    for attempt in 1 2 3 4 5; do
        podman kill --signal SIGUSR1 "${JV_INSTANCE_CONTAINER}" >/dev/null 2>&1 && return 0
        jv_minecraft_still_running || return 0
        sleep 0.1
    done
}
jv_player_check_before_interrupt() {
    local players online
    systemctl is-active --quiet "${JV_INSTANCE_UNIT}" || return 0
    players="$(jv_player_list_raw)" || return 1
    online="$(jv_player_online_count "${players}")" || return 1
    (( online == 0 )) && return 0
    case "${JV_INTERRUPT_CONFIRMATION_MODE:-interactive}" in
        confirmed) return 0 ;; required) return 10 ;; interactive) confirm "Players appear to be online. $1 anyway?" ;; *) return 1 ;;
    esac
}
jv_stop_minecraft_adaptive() {
    local raw online
    systemctl is-active --quiet "${JV_INSTANCE_UNIT}" || return 0
    raw="$(jv_player_list_raw)" || return 1
    online="$(jv_player_online_count "${raw}")" || return 1
    if (( online > 0 )); then
        jv_player_check_before_interrupt "$1" || return $?
        jv_begin_minecraft_stop || return 1
        jv_countdown_online_server_shutdown
    else
        jv_begin_minecraft_stop || return 1
        jv_bypass_container_shutdown_delay || true
        jv_wait_minecraft_stopped
    fi
}

jv_migration_validate_current() {
    local was_running=no valid=yes
    systemctl is-active --quiet "${JV_INSTANCE_UNIT}" && was_running=yes
    if ! systemctl start "${JV_INSTANCE_UNIT}"; then
        if [[ ${was_running} != yes ]]; then systemctl stop "${JV_INSTANCE_UNIT}" || true; fi
        return 1
    fi
    jv_migration_validate_runtime || valid=no
    if [[ ${was_running} != yes ]]; then systemctl stop "${JV_INSTANCE_UNIT}" || valid=no; fi
    [[ ${valid} == yes ]]
}

jv_migration_backup_load_config() {
    local software="${MINECRAFT_SERVER_TYPE}" rc=0
    jv_backup_load_config "$@" || rc=$?
    MINECRAFT_SERVER_TYPE="${software}"
    MINECRAFT_DATA_PATH="${DATA_PATH}"
    MINECRAFT_SERVICE="${JV_INSTANCE_UNIT}"
    minecraft_data_path="${DATA_PATH}"
    return "${rc}"
}
