#!/usr/bin/bash
# Sourced by migration-import: source selection, staging and compatibility checks.
# Select the source.
if (( $# == 1 )); then
    JV_MIGRATION_SOURCE="$(realpath -m -- "$1")"
    [[ -e ${JV_MIGRATION_SOURCE} ]] || { echo "ERROR: source does not exist: ${JV_MIGRATION_SOURCE}" >&2; exit 1; }
else
    if [[ ${configured} == yes ]]; then jv_backup_load_config; fi
    transport_started=yes
    if jv_migration_select_import_source "${configured}"; then
        :
    else
        rc=$?
        (( rc == 2 )) && { echo 'Import cancelled.'; exit 0; }
        exit "${rc}"
    fi
fi

jv_migration_validate_source_name "${JV_MIGRATION_SOURCE}" || exit 1
source_identity="$(jv_migration_source_identity "${JV_MIGRATION_SOURCE}")"
source_info="$(/usr/libexec/justvoxel/mjust/migration-archive inspect "${JV_MIGRATION_SOURCE}")" || exit 1
expanded_bytes="$(jq -r '.expandedBytes' <<< "${source_info}")"
is_native="$(jq -r '.nativeBundle' <<< "${source_info}")"

# Capture the only source-side metadata needed after staging before copying begins.
# After the post-copy identity check below, compatibility analysis must use only
# the staged copy and these bounded captured values.
source_is_file=no
source_basename=''
source_backup_meta_mode=''
source_backup_meta_version=''
if [[ -f ${JV_MIGRATION_SOURCE} ]]; then
    source_is_file=yes
    source_basename="$(basename -- "${JV_MIGRATION_SOURCE}")"
    if [[ ${source_basename} == minecraft-*.tar.gz && -f "${JV_MIGRATION_SOURCE}.meta.json" ]]; then
        if jq -e '.schemaVersion == 1' "${JV_MIGRATION_SOURCE}.meta.json" >/dev/null 2>&1; then
            source_backup_meta_mode="$(jq -r '.minecraft.versionMode // "unknown"' "${JV_MIGRATION_SOURCE}.meta.json")"
            source_backup_meta_version="$(jq -r '.minecraft.configuredVersion // empty' "${JV_MIGRATION_SOURCE}.meta.json")"
        fi
    fi
fi

native_manifest=''
if [[ ${is_native} == true ]]; then
    echo 'Verifying native JustVoxel bundle SHA-256 integrity.'
    native_manifest="$(/usr/libexec/justvoxel/mjust/migration-archive verify-native "${JV_MIGRATION_SOURCE}")" || exit 1
fi

# Existing destinations already know their data filesystem. Fresh destinations
# choose data storage before extraction so staging can live on that same filesystem.
if [[ ${configured} == yes ]]; then
    /usr/libexec/justvoxel/mjust/validate-data-mount
    jv_restore_validate_data_layout "${DATA_PATH}"
    had_existing_data=yes
else
    if prepare_fresh_data_destination; then :; else rc=$?; (( rc == 2 )) && { echo 'Import cancelled.'; exit 0; }; exit "${rc}"; fi
    had_existing_data=no
fi

data_parent="$(dirname -- "${DATA_PATH}")"
install -d -m0755 -o root -g root "${data_parent}"
stale="$(find "${data_parent}" -maxdepth 1 -type d -name '.justvoxel-import-*' -print -quit 2>/dev/null || true)"
if [[ -n ${stale} ]]; then
    echo 'ERROR: an earlier JustVoxel import transaction still exists.' >&2
    echo "Recovery state: ${stale}" >&2
    echo 'Use the guided recovery flow before starting another import:' >&2
    echo '  mjust migration-recover' >&2
    exit 1
fi
jv_migration_require_staging_space "${data_parent}" "${expanded_bytes}"

transaction="${data_parent}/.justvoxel-import-$(date +%Y%m%d-%H%M%S)-$$"
install -d -m0700 -o root -g root "${transaction}"
jv_migration_write_state "${transaction}" staging "${JV_MIGRATION_SOURCE}" unknown

staging_source="${transaction}/staging-source"
echo 'Copying and validating the source into migration staging.'
/usr/libexec/justvoxel/mjust/migration-archive extract "${JV_MIGRATION_SOURCE}" "${staging_source}"
if [[ $(jv_migration_source_identity "${JV_MIGRATION_SOURCE}") != "${source_identity}" ]]; then
    echo 'ERROR: migration source changed while it was being staged.' >&2
    exit 1
fi

# ORIGINAL SOURCE FILESYSTEM ACCESS ENDS HERE.
# From this point through activation, the staged copy is authoritative. The
# original SMB/NFS/device/local source may disappear without changing the Import.
if [[ ${is_native} == true ]]; then
    detection_base="${staging_source}/justvoxel-migration/server"
    [[ -d ${detection_base} ]] || { echo 'ERROR: native bundle server directory is missing after extraction.' >&2; exit 1; }
    detection="$(/usr/libexec/justvoxel/mjust/migration-archive detect "${detection_base}")"
    selected="$(select_candidate "${detection}" "${detection_base}")" || { rc=$?; (( rc == 2 )) && { echo 'Import cancelled.'; exit 0; }; exit "${rc}"; }
    source_class=justvoxel
else
    detection_base="${staging_source}"
    detection="$(/usr/libexec/justvoxel/mjust/migration-archive detect "${detection_base}")"
    selected="$(select_candidate "${detection}" "${detection_base}")" || { rc=$?; (( rc == 2 )) && { echo 'Import cancelled.'; exit 0; }; exit "${rc}"; }
    candidate_type="$(jq -r '.sourceType' <<< "${selected}")"
    if [[ ${source_is_file} == yes && ${source_basename} == minecraft-*.tar.gz ]]; then
        source_class=justvoxel-backup
    else
        source_class="${candidate_type}"
    fi
fi

candidate_type="$(jq -r '.sourceType' <<< "${selected}")"
case "${candidate_type}" in
    paper|itzg-paper) ;;
    vanilla)
        echo
        echo 'WARNING: vanilla Minecraft server detected.'
        echo 'JustVoxel runs Paper. The world can normally be opened by Paper at the same Minecraft version,'
        echo 'but this is a server-software conversion and is not identical to a Paper-to-Paper migration.'
        if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
            [[ ${JV_MIGRATION_API_VANILLA_CONFIRMED:-no} == yes ]] || { echo 'ERROR: vanilla to Paper conversion was not explicitly confirmed.' >&2; exit 1; }
        else
            jui_confirm 'Continue with vanilla -> Paper conversion?' || { echo 'Import cancelled.'; exit 0; }
        fi
        ;;
    fabric|forge|neoforge|modded-unknown)
        echo "ERROR: unsupported Minecraft source type detected: ${candidate_type}" >&2
        echo 'Migration v1 supports JustVoxel, Paper, itzg/minecraft-server TYPE=PAPER, and guarded vanilla -> Paper conversion.' >&2
        exit 1
        ;;
    *)
        echo "ERROR: unrecognized Minecraft source type: ${candidate_type}" >&2
        exit 1
        ;;
esac

selected_root="$(jq -r '.root' <<< "${selected}")"
case "${selected_root}/" in
    "$(realpath -m -- "${staging_source}")/"*) ;;
    *) echo 'ERROR: selected server root escaped migration staging.' >&2; exit 1 ;;
esac

# Validate source identity model.
props_online="$(jq -r 'if .onlineMode == null then "unknown" else (.onlineMode|tostring) end' <<< "${selected}")"
manifest_online='unknown'
if [[ ${source_class} == justvoxel ]]; then
    manifest_online="$(jq -r 'if .minecraft.onlineMode == null then "unknown" else (.minecraft.onlineMode|tostring) end' <<< "${native_manifest}")"
fi
online_state="$(jv_migration_online_mode_state "${manifest_online}" "${props_online}")"
case "${online_state}" in
    conflict)
        echo 'ERROR: source metadata and server.properties disagree about online-mode.' >&2
        exit 1
        ;;
    offline)
        echo 'ERROR: source uses online-mode=false. Automatic complete migration is refused in version 1.' >&2
        echo 'Changing online/offline UUID identity can disconnect inventories, positions, whitelist, ops and plugin data from the correct players.' >&2
        exit 1
        ;;
    unknown)
        prompt_source_online_mode || { echo 'Import cancelled because source identity mode was not resolved.'; exit 1; }
        ;;
    online) : ;;
esac

# Resolve exact source Minecraft version. Migration never uses LATEST automatically.
source_version="$(jq -r '.minecraftVersion // empty' <<< "${selected}")"
if [[ ${source_class} == justvoxel ]]; then
    manifest_version="$(jq -r '.minecraft.version // empty' <<< "${native_manifest}")"
    [[ -n ${manifest_version} ]] || { echo 'ERROR: native migration manifest has no exact Minecraft version.' >&2; exit 1; }
    if resolved_version="$(jv_migration_version_resolution "${manifest_version}" "${source_version}")"; then
        source_version="${resolved_version}"
    else
        echo "ERROR: native manifest Minecraft version (${manifest_version}) conflicts with staged server evidence (${source_version:-unknown})." >&2
        exit 1
    fi
elif [[ ${source_class} == justvoxel-backup ]]; then
    if [[ ${source_backup_meta_mode} == pinned && -n ${source_backup_meta_version} && ${source_backup_meta_version} != LATEST ]]; then
        source_version="${source_version:-${source_backup_meta_version}}"
    fi
fi
if [[ -z ${source_version} ]]; then
    echo
    echo 'The exact source Minecraft version could not be determined automatically.'
    echo 'Migration will not start this world on LATEST or guess a newer version.'
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        source_version="${JV_MIGRATION_API_SOURCE_VERSION:-}"
        [[ -n ${source_version} ]] || { echo 'ERROR: exact source Minecraft version is required for this import.' >&2; exit 1; }
    else
        source_version="$(jui_input 'Exact source Minecraft version (example 1.21.8)')" || exit 1
    fi
fi
[[ ${source_version} =~ ^[0-9]+([.][0-9]+){1,2}$ ]] || {
    echo "ERROR: source Minecraft version is not an exact supported version string: ${source_version}" >&2
    exit 1
}
if paper_version_has_stable_build "${source_version}"; then
    :
else
    version_rc=$?
    if (( version_rc == 2 )); then
        echo "ERROR: PaperMC availability for Minecraft ${source_version} could not be verified." >&2
    else
        echo "ERROR: no stable Paper build was confirmed for Minecraft ${source_version}." >&2
    fi
    echo 'Import stopped before activation. Migration and Minecraft version upgrade remain separate operations.' >&2
    exit 1
fi

plugin_count="$(jq -r '.pluginJarCount // 0' <<< "${selected}")"
if [[ ${source_class} != justvoxel && ${source_class} != justvoxel-backup && ${plugin_count} =~ ^[0-9]+$ && ${plugin_count} -gt 0 ]]; then
    echo
    echo "WARNING: this external server contains ${plugin_count} plugin JAR(s)."
    echo 'Minecraft plugins are executable server code and will run inside the Minecraft container.'
    echo 'JustVoxel will preserve them; it will not silently remove or disable them.'
    if [[ ${JV_MIGRATION_API_MODE:-0} == 1 ]]; then
        [[ ${JV_MIGRATION_API_PLUGINS_CONFIRMED:-no} == yes ]] || { echo 'ERROR: external plugin execution was not explicitly confirmed.' >&2; exit 1; }
    else
        jui_confirm 'Continue with these external plugins?' || { echo 'Import cancelled.'; exit 0; }
    fi
fi
