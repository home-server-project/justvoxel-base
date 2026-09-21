#!/usr/bin/bash
# Sourced by migration-import: portable settings and destination plan.
# Move only the selected server root into the clean activation staging path.
staged_server="${transaction}/staged-server"
[[ ! -e ${staged_server} ]] || { echo 'ERROR: staged-server path already exists.' >&2; exit 1; }
mv -- "${selected_root}" "${staged_server}"
redetect="$(/usr/libexec/justvoxel/mjust/migration-archive detect "${staged_server}")"
[[ $(jq -r '.candidates | length' <<< "${redetect}") == 1 ]] || {
    echo 'ERROR: selected staged server did not remain a single recognizable server root.' >&2
    exit 1
}
selected="$(jq -c '.candidates[0]' <<< "${redetect}")"
jv_migration_write_state "${transaction}" prepared "${JV_MIGRATION_SOURCE}" "${source_class}"

# Source-derived portable settings.
if [[ ${source_class} == justvoxel ]]; then
    source_game_mode="$(jq -r '.minecraft.gameMode // empty' <<< "${native_manifest}")"
    source_difficulty="$(jq -r '.minecraft.difficulty // empty' <<< "${native_manifest}")"
    source_whitelist="$(jq -r 'if .minecraft.whitelistEnabled == null then empty else (.minecraft.whitelistEnabled|tostring) end' <<< "${native_manifest}")"
    source_enforce="$(jq -r 'if .minecraft.enforceWhitelist == null then empty else (.minecraft.enforceWhitelist|tostring) end' <<< "${native_manifest}")"
    source_max_players="$(jq -r '.minecraft.maxPlayers // empty' <<< "${native_manifest}")"
    source_motd="$(jq -r '.minecraft.motd // empty' <<< "${native_manifest}")"
    java_hint="$(jq -r '.networkHints.javaPort // empty' <<< "${native_manifest}")"
    bedrock_hint="$(jq -r '.networkHints.bedrockPort // empty' <<< "${native_manifest}")"
    source_bedrock_manifest="$(jq -r 'if .bedrock.enabled == null then empty else (.bedrock.enabled|tostring) end' <<< "${native_manifest}")"
else
    source_game_mode="$(jq -r '.gameMode // empty' <<< "${selected}")"
    source_difficulty="$(jq -r '.difficulty // empty' <<< "${selected}")"
    source_whitelist="$(jq -r 'if .whitelistEnabled == null then empty else (.whitelistEnabled|tostring) end' <<< "${selected}")"
    source_enforce="$(jq -r 'if .enforceWhitelist == null then empty else (.enforceWhitelist|tostring) end' <<< "${selected}")"
    source_max_players="$(jq -r '.maxPlayers // empty' <<< "${selected}")"
    source_motd="$(jq -r '.motd // empty' <<< "${selected}")"
    java_hint="$(jq -r '.javaPortHint // empty' <<< "${selected}")"
    bedrock_hint="$(jq -r '.bedrockPortHint // empty' <<< "${selected}")"
    source_bedrock_manifest=''
fi

GAME_MODE="${source_game_mode:-${GAME_MODE:-survival}}"
DIFFICULTY="${source_difficulty:-${DIFFICULTY:-normal}}"
if [[ -n ${source_whitelist} ]]; then WHITELIST_ENABLED="$(jv_migration_bool_to_yesno "${source_whitelist}")"; else WHITELIST_ENABLED="${WHITELIST_ENABLED:-yes}"; fi
if [[ -n ${source_enforce} ]]; then ENFORCE_WHITELIST="$(jv_migration_bool_to_yesno "${source_enforce}")"; else ENFORCE_WHITELIST="${ENFORCE_WHITELIST:-yes}"; fi
MAX_PLAYERS="${source_max_players:-${MAX_PLAYERS:-10}}"
MOTD="${source_motd:-${MOTD:-JustVoxel Java and Bedrock Server}}"
validate_positive_int "${MAX_PLAYERS}" || { echo 'ERROR: imported max-players value is invalid.' >&2; exit 1; }
validate_simple_text "${MOTD}" || { echo 'ERROR: imported MOTD is not a safe single-line value.' >&2; exit 1; }
jv_migration_validate_setting_values

source_geyser="$(jq -r '.geyserEnabled' <<< "${selected}")"
source_geyser_auth="$(jq -r '.geyserAuthType // empty' <<< "${selected}")"
source_floodgate="$(jq -r '.floodgateEnabled' <<< "${selected}")"
if [[ ${source_bedrock_manifest} == true && ${source_geyser_auth} != floodgate ]]; then
    echo 'ERROR: native manifest says Bedrock/Floodgate is enabled, but staged Geyser auth-type is not floodgate.' >&2
    exit 1
elif [[ ${source_geyser} == true && ${source_floodgate} == true && ${source_geyser_auth} == floodgate ]]; then
    BEDROCK_ENABLED=yes
else
    BEDROCK_ENABLED=no
    if [[ ${source_geyser} == true ]]; then
        echo 'WARNING: Geyser was detected without a complete Floodgate-authenticated configuration. Bedrock host publishing will remain disabled.'
    fi
fi

plugin_names="$(jq -r '.pluginJars[]? | ascii_downcase' <<< "${selected}" 2>/dev/null || true)"
if [[ ${BEDROCK_ENABLED} == yes ]] && grep -q 'geyser' <<< "${plugin_names}" && grep -q 'floodgate' <<< "${plugin_names}"; then
    BEDROCK_MANAGED_PLUGINS=no
else
    BEDROCK_MANAGED_PLUGINS=yes
fi

# Destination-specific configuration.
MINECRAFT_VERSION_MODE=pinned
MINECRAFT_VERSION="${source_version}"
if [[ ${configured} == no ]]; then
    read -r MINECRAFT_UID MINECRAFT_GID _ < <(resolve_minecraft_ids)
    read -r JAVA_MEMORY CONTAINER_MEMORY < <(suggest_memory_values)
    TIMEZONE="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
    TIMEZONE="${TIMEZONE:-UTC}"
    MINECRAFT_IMAGE_TAG=stable
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        JAVA_MEMORY="${JV_MIGRATION_API_JAVA_MEMORY:-${JAVA_MEMORY}}"
        CONTAINER_MEMORY="${JV_MIGRATION_API_CONTAINER_MEMORY:-${CONTAINER_MEMORY}}"
        TIMEZONE="${JV_MIGRATION_API_TIMEZONE:-${TIMEZONE}}"
        MINECRAFT_IMAGE_TAG="${JV_MIGRATION_API_IMAGE_TAG:-stable}"
    fi
else
    # Destination ownership, memory, timezone, image channel and backup policy remain destination-specific.
    :
fi

if [[ -n ${java_hint} && ${java_hint} =~ ^[0-9]+$ ]]; then
    echo "Source Java port hint: ${java_hint}/tcp"
fi
if [[ ${configured} == yes ]]; then default_java="${old_java}"; else default_java="${java_hint:-25565}"; fi
if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
    JAVA_PORT="${JV_MIGRATION_API_JAVA_PORT:-${default_java}}"
else
    JAVA_PORT="$(jui_input 'Destination Java host TCP port' "${default_java}")" || exit 1
fi
validate_port "${JAVA_PORT}" || { echo 'ERROR: invalid Java port.' >&2; exit 1; }
jv_migration_check_candidate_port tcp "${JAVA_PORT}" "${old_java}" || exit 1

if [[ ${BEDROCK_ENABLED} == yes ]]; then
    [[ -n ${bedrock_hint} && ${bedrock_hint} =~ ^[0-9]+$ ]] && echo "Source Bedrock port hint: ${bedrock_hint}/udp"
    if [[ ${configured} == yes && ${old_bedrock_enabled} == yes ]]; then default_bedrock="${old_bedrock}"; else default_bedrock="${bedrock_hint:-19132}"; fi
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        BEDROCK_PORT="${JV_MIGRATION_API_BEDROCK_PORT:-${default_bedrock}}"
    else
        BEDROCK_PORT="$(jui_input 'Destination Bedrock host UDP port' "${default_bedrock}")" || exit 1
    fi
    validate_port "${BEDROCK_PORT}" || { echo 'ERROR: invalid Bedrock port.' >&2; exit 1; }
    jv_migration_check_candidate_port udp "${BEDROCK_PORT}" "$([[ ${old_bedrock_enabled} == yes ]] && printf '%s' "${old_bedrock}" || true)" || exit 1
else
    BEDROCK_PORT="${BEDROCK_PORT:-19132}"
fi

if [[ ${configured} == no ]]; then
    if [[ ${JV_MIGRATION_API_MODE:-0} != 1 ]]; then
        JAVA_MEMORY="$(jui_input 'Minecraft Java heap' "${JAVA_MEMORY}")" || exit 1
        CONTAINER_MEMORY="$(jui_input 'Maximum total Minecraft memory' "${CONTAINER_MEMORY}")" || exit 1
    fi
    validate_memory "${JAVA_MEMORY}" && validate_memory "${CONTAINER_MEMORY}" || { echo 'ERROR: invalid memory values.' >&2; exit 1; }
    (( $(memory_to_mib "${CONTAINER_MEMORY}") > $(memory_to_mib "${JAVA_MEMORY}") )) || { echo 'ERROR: container memory must be larger than Java heap.' >&2; exit 1; }

    if prepare_fresh_backup_configuration; then :; else rc=$?; (( rc == 2 )) && { echo 'Import cancelled.'; exit 0; }; exit "${rc}"; fi

    echo
    echo 'Minecraft EULA acceptance is required before JustVoxel can start the imported server.'
    echo 'The source eula.txt, if present, is NOT accepted as destination administrator consent.'
    echo 'Review: https://aka.ms/MinecraftEULA'
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        [[ ${JV_MIGRATION_API_EULA_ACCEPTED:-no} == yes ]] || { echo 'ERROR: Minecraft EULA acceptance was not explicitly confirmed.' >&2; exit 1; }
    else
        printf 'Type ACCEPT to confirm that you accept the Minecraft EULA: ' >/dev/tty
        IFS= read -r eula_acceptance </dev/tty
        [[ ${eula_acceptance} == ACCEPT ]] || { echo 'EULA not accepted. Imported data was not activated.'; exit 1; }
    fi
fi

# Confirm the destination container image policy is still available before live data changes.
if validate_minecraft_image_tag "${MINECRAFT_IMAGE_TAG}"; then
    :
else
    image_rc=$?
    if (( image_rc == 2 )); then
        echo "ERROR: destination container tag '${MINECRAFT_IMAGE_TAG}' is deprecated upstream." >&2
    else
        echo "ERROR: destination container tag '${MINECRAFT_IMAGE_TAG}' could not be verified." >&2
    fi
    exit 1
fi

# Recalculate data destination ownership and normalize only appliance-specific runtime values.
jv_migration_normalize_staged_runtime_files "${staged_server}" "${BEDROCK_ENABLED}"

# Show the final plan before any live destination changes.
echo
echo 'Import plan'
echo '-----------'
echo "Source type: ${source_class} (${candidate_type})"
echo "Minecraft version: ${MINECRAFT_VERSION} (pinned for migration)"
echo "Source online-mode: true"
echo "Destination data: ${DATA_PATH}"
echo "Destination UID:GID: ${MINECRAFT_UID}:${MINECRAFT_GID}"
echo "Java port: ${JAVA_PORT}/tcp"
if [[ ${BEDROCK_ENABLED} == yes ]]; then echo "Bedrock port: ${BEDROCK_PORT}/udp"; else echo 'Bedrock host publishing: disabled'; fi
echo "Game mode / difficulty: ${GAME_MODE} / ${DIFFICULTY}"
echo "Whitelist / enforce whitelist: ${WHITELIST_ENABLED} / ${ENFORCE_WHITELIST}"
echo "Maximum players: ${MAX_PLAYERS}"
echo "Plugins preserved: ${plugin_count} JAR(s) detected"
if [[ ${configured} == yes ]]; then
    echo 'Destination mode: REPLACE existing JustVoxel persistent Minecraft server state'
    echo 'The original data/config/runtime/firewall state will be protected until validation succeeds.'
else
    echo 'Destination mode: first-server import into fresh JustVoxel'
fi

players_override=no
