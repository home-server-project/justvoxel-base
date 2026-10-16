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

# Imported configuration is data; managed binaries belong to JustVoxel.
BEDROCK_MANAGED_PLUGINS=yes

# Capture the reviewed Minecraft-side settings, never source appliance identity.
jq -cn --arg id "${JV_INSTANCE_ID}" --arg game_mode "${GAME_MODE}" --arg difficulty "${DIFFICULTY}" --argjson whitelist "$([[ ${WHITELIST_ENABLED} == yes ]] && echo true || echo false)" --argjson enforce "$([[ ${ENFORCE_WHITELIST} == yes ]] && echo true || echo false)" --argjson max_players "${MAX_PLAYERS}" --arg motd "${MOTD}" '{instance_id:$id,game_mode:$game_mode,difficulty:$difficulty,whitelist:$whitelist,enforce_whitelist:$enforce,max_players:$max_players,motd:$motd}' > "${transaction}/portable.json"
chmod 0600 "${transaction}/portable.json"

# The destination's committed runtime owns ports, resources, software and identity.
# Source runtime settings never replace the selected instance configuration.
jv_migration_load_instance "${JV_INSTANCE_ID}"

# Recalculate data destination ownership and normalize only appliance-specific runtime values.
jv_migration_normalize_staged_runtime_files "${staged_server}" "${BEDROCK_ENABLED}"
/usr/libexec/justvoxel/mjust/managed-plugin-files "${staged_server}/plugins" all ''

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
echo 'Destination mode: REPLACE selected Minecraft instance data'
echo 'The selected destination instance configuration remains authoritative.'

players_override=no
