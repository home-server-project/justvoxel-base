#!/usr/bin/bash
# Sourced by migration-import: maintenance lock, activation, validation and cleanup.
query_players() { podman exec minecraft rcon-cli 'list' 2>/dev/null || true; }
if [[ ${configured} == yes ]] && systemctl is-active --quiet minecraft.service; then
    minecraft_was_active=yes
    initial_players="$(query_players)"
    [[ -n ${initial_players} ]] || { echo 'ERROR: could not confirm player state. Nothing was activated.' >&2; exit 1; }
    echo "${initial_players}"
    if ! grep -q 'There are 0 of' <<< "${initial_players}"; then
        echo 'Players are online. Import will use the normal graceful shutdown path.'
        if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
            [[ ${JV_MIGRATION_API_PLAYERS_CONFIRMED:-no} == yes ]] || { echo 'ERROR: players are online and interruption was not explicitly confirmed.' >&2; exit 1; }
            players_override=yes
        elif jui_confirm 'Continue preparing this import?'; then players_override=yes; else echo 'Import cancelled.'; exit 0; fi
    fi
fi

echo
if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
    [[ ${JV_MIGRATION_API_IMPORT_CONFIRMED:-no} == yes ]] || { echo 'ERROR: destructive import activation was not explicitly confirmed.' >&2; exit 1; }
else
    printf 'Type IMPORT to continue: ' >/dev/tty
    IFS= read -r confirmation </dev/tty
    [[ ${confirmation} == IMPORT ]] || { echo 'Import cancelled.'; exit 0; }
fi

exec 9>"${JV_MAINTENANCE_LOCK}"
if ! flock -n 9; then
    echo 'ERROR: another JustVoxel Minecraft maintenance operation is already running.' >&2
    exit 1
fi

# The source was already copied and validated into local transaction staging.
# Activation uses staged data only; the original source is no longer consulted.

if [[ ${configured} == no ]]; then
    jv_migration_assert_fresh_selinux_path "${DATA_PATH}"
fi
jv_migration_snapshot_runtime "${transaction}" "${old_java}" "${old_bedrock_enabled}" "${old_bedrock}" "${JAVA_PORT}" "${BEDROCK_ENABLED}" "${BEDROCK_PORT}"
jv_migration_write_state "${transaction}" ready-to-switch "${JV_MIGRATION_SOURCE}" "${source_class}"

if [[ ${configured} == yes && ${minecraft_was_active} == yes ]]; then
    echo 'Final player check immediately before graceful stop.'
    final_players="$(query_players)"
    [[ -n ${final_players} ]] || { echo 'ERROR: player state became unknown. Import cancelled before live data changed.' >&2; exit 1; }
    echo "${final_players}"
    if ! grep -q 'There are 0 of' <<< "${final_players}" && [[ ${players_override} != yes ]]; then
        if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
            echo 'ERROR: a player joined after planning and interruption was not confirmed.' >&2
            exit 1
        elif ! jui_confirm 'A player joined. Continue with the normal graceful shutdown?'; then
            echo 'Import cancelled before live data changed.'
            exit 0
        fi
    fi
    echo 'Stopping Minecraft through the player-aware graceful shutdown path.'
    stop_mode=required
    [[ ${players_override} == yes ]] && stop_mode=confirmed
    set +e
    JV_INTERRUPT_CONFIRMATION_MODE="${stop_mode}" \
        jv_stop_minecraft_adaptive 'Import Minecraft data'
    stop_rc=$?
    set -e
    if (( stop_rc == 10 )); then
        if [[ ${JV_MIGRATION_API_MODE:-0} == 1 && ${JV_MIGRATION_API_PLAYERS_CONFIRMED:-no} == yes ]]; then
            set +e
            JV_INTERRUPT_CONFIRMATION_MODE=confirmed \
                jv_stop_minecraft_adaptive 'Import Minecraft data'
            stop_rc=$?
            set -e
        elif [[ ${JV_MIGRATION_API_MODE:-0} != 1 ]] && jui_confirm 'A player joined after the final check. Continue with the 60-second shutdown countdown?'; then
            set +e
            JV_INTERRUPT_CONFIRMATION_MODE=confirmed \
                jv_stop_minecraft_adaptive 'Import Minecraft data'
            stop_rc=$?
            set -e
        fi
    fi
    if (( stop_rc != 0 )); then
        echo 'ERROR: Minecraft could not be stopped safely. Live data was not changed.' >&2
        exit 1
    fi
    minecraft_stopped_by_import=yes
fi

if [[ ${configured} == yes ]]; then /usr/libexec/justvoxel/mjust/validate-data-mount; fi
jv_migration_write_state "${transaction}" switching "${JV_MIGRATION_SOURCE}" "${source_class}"
live_modified=yes
jv_migration_activate_data "${transaction}" "${DATA_PATH}" "${staged_server}" "${had_existing_data}"

chown -R "${MINECRAFT_UID}:${MINECRAFT_GID}" "${DATA_PATH}"

# Fresh import now creates JustVoxel configuration; replacement import updates
# only portable/source-derived settings while destination-specific settings remain local.
write_main_config
apply_data_selinux
JUSTVOXEL_REGENERATE_RCON=1 render_runtime
if [[ ${configured} == yes ]]; then
    jv_migration_apply_candidate_firewall "${old_java}" "${old_bedrock_enabled}" "${old_bedrock}"
else
    configure_firewall_initial
fi
jv_migration_write_state "${transaction}" imported-active "${JV_MIGRATION_SOURCE}" "${source_class}"

echo 'Starting imported Minecraft server.'
systemctl start minecraft.service
jv_migration_write_state "${transaction}" validating "${JV_MIGRATION_SOURCE}" "${source_class}"
/usr/libexec/justvoxel/mjust/restore-runtime-validate

reported="$(podman exec minecraft rcon-cli 'version' 2>/dev/null | head -n1 || true)"
if [[ -z ${reported} || ${reported} != *"${source_version}"* ]]; then
    echo "ERROR: imported server did not report the required source Minecraft version ${source_version}." >&2
    echo "Reported: ${reported:-unknown}" >&2
    exit 1
fi

post_plugin_count="$(find "${DATA_PATH}/plugins" -maxdepth 1 -type f -iname '*.jar' 2>/dev/null | wc -l | tr -d ' ')"
[[ ${post_plugin_count} =~ ^[0-9]+$ ]] || post_plugin_count=0
if [[ ${plugin_count} =~ ^[0-9]+$ ]] && (( post_plugin_count < plugin_count )); then
    echo "ERROR: plugin JAR count after import (${post_plugin_count}) is smaller than staged source count (${plugin_count})." >&2
    exit 1
fi

/usr/libexec/justvoxel/mjust/validate
validated=yes
jv_migration_write_state "${transaction}" validated "${JV_MIGRATION_SOURCE}" "${source_class}"

if ! rm -rf -- "${transaction}"; then
    echo 'ERROR: imported server is healthy, but transaction cleanup is incomplete.' >&2
    echo "Recovery/safety state remains under: ${transaction}" >&2
    echo 'The healthy imported server was NOT rolled back.' >&2
    exit 1
fi
transaction=''
live_modified=no
minecraft_stopped_by_import=no
jv_migration_api_result succeeded validated '' 'Server migration Import completed successfully and the imported Minecraft runtime validated.' || true

removable_owned=no
if [[ ${transport_started} == yes ]]; then
    [[ ${JV_MIGRATION_MEDIA_REMOVABLE:-no} == yes && -n ${JV_MIGRATION_OWNED_MOUNT:-} ]] && removable_owned=yes
    jv_migration_transport_cleanup
    transport_started=no
fi
trap - EXIT INT TERM

echo
echo 'Import completed successfully.'
echo "Source type: ${source_class}"
echo "Minecraft version: ${source_version}"
echo "Minecraft data: ${DATA_PATH}"
echo "Java: ${JAVA_PORT}/tcp"
if [[ ${BEDROCK_ENABLED} == yes ]]; then echo "Bedrock: ${BEDROCK_PORT}/udp"; fi
echo 'Migration and Minecraft-version upgrade remain separate; run mjust update-minecraft later if you intentionally want to upgrade.'
if [[ ${removable_owned} == yes ]]; then
    echo 'Device unmounted.'
    echo 'It is safe to remove the USB device.'
fi
