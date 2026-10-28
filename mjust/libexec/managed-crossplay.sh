#!/usr/bin/bash
# Fixed JustVoxel platform components only. No caller-supplied download URLs.
# Independent startup overrides this hook: Podman :Z owns its data labels.
jv_crossplay_label() { restorecon -F "$1"; }
jv_crossplay_platform() {
    case "${MINECRAFT_SERVER_TYPE:-paper}" in
        paper|purpur|pufferfish|leaf|spigot) printf spigot ;;
        fabric|neoforge) printf '%s' "${MINECRAFT_SERVER_TYPE}" ;;
        *) return 1 ;;
    esac
}
jv_crossplay_directory() {
    case "${MINECRAFT_SERVER_TYPE:-paper}" in fabric|neoforge) printf mods ;; *) printf plugins ;; esac
}
jv_crossplay_projects() {
    printf 'geyser floodgate'
    [[ ${MINECRAFT_SERVER_TYPE:-paper} != fabric ]] || printf ' fabric-api'
    printf '\n'
}
jv_crossplay_mod_compatible() {
    local staging result=0
    staging="$(mktemp -d)" || return 1
    /usr/libexec/justvoxel/mjust/managed-crossplay-mods "$1" "$2" "$3" "${staging}" || result=$?
    rm -rf -- "${staging}"
    return "${result}"
}
jv_crossplay_metadata() {
    local project="$1" metadata
    case "${project}" in geyser|floodgate) ;; *) return 1 ;; esac
    metadata="$(curl --connect-timeout 5 --max-time 15 -fsSL --proto '=https' --proto-redir '=https' \
        -H "User-Agent: ${JV_PAPER_USER_AGENT}" \
        "https://download.geysermc.org/v2/projects/${project}/versions/latest/builds/latest")" || return 1
    jv_crossplay_parse_metadata "${project}" "${metadata}"
}

jv_crossplay_parse_metadata() {
    local project="$1" metadata="$2"
    case "${project}" in geyser|floodgate) ;; *) return 1 ;; esac
    jq -ces --arg project "${project}" '
        select(length == 1) | .[0]
        | select(.project_id == $project and .channel == "default")
        | select(.version | type == "string" and test("^[0-9]+([.][0-9]+)*(-SNAPSHOT)?$"))
        | select(.build | type == "number" and . > 0 and . == floor)
        | select(.downloads.spigot.name | type == "string" and test("^[A-Za-z0-9._-]+[.]jar$"))
        | select(.downloads.spigot.sha256 | type == "string" and test("^[a-f0-9]{64}$"))
        | {project:.project_id,version,build,artifact:.downloads.spigot.name,sha256:.downloads.spigot.sha256}
    ' <<< "${metadata}" 2>/dev/null
}

jv_crossplay_jar_name() {
    local platform
    platform="$(jv_crossplay_platform)" || return 1
    case "$1:${platform}" in
        geyser:spigot) printf Geyser-Spigot.jar ;; floodgate:spigot) printf floodgate-spigot.jar ;;
        geyser:fabric) printf Geyser-Fabric.jar ;; floodgate:fabric) printf floodgate-fabric.jar ;;
        geyser:neoforge) printf Geyser-NeoForge.jar ;; floodgate:neoforge) printf floodgate-neoforge.jar ;;
        fabric-api:fabric) printf fabric-api.jar ;; *) return 1 ;;
    esac
}

jv_crossplay_artifact_state() {
    local project="$1" metadata="$2" expected path actual
    [[ ! -L ${DATA_PATH}/$(jv_crossplay_directory) ]] || { printf unavailable; return; }
    expected="$(jq -er '.sha256 | select(test("^[a-f0-9]{64}$"))' <<< "${metadata}" 2>/dev/null)" || { printf unavailable; return; }
    path="${DATA_PATH}/$(jv_crossplay_directory)/$(jv_crossplay_jar_name "${project}")" || { printf unavailable; return; }
    if [[ ! -e ${path} && ! -L ${path} ]]; then printf updates_available; return; fi
    [[ -f ${path} && ! -L ${path} ]] || { printf unavailable; return; }
    actual="$(sha256sum -- "${path}")" || { printf unavailable; return; }
    if [[ ${actual%% *} != "${expected}" ]]; then printf updates_available; return; fi
    # Mod transactions own only their fixed canonical names, never Bukkit JARs.
    if [[ $(jv_crossplay_directory) == mods ]]; then printf up_to_date; return; fi
    local result=0
    /usr/libexec/justvoxel/mjust/managed-plugin-files "${DATA_PATH}/$(jv_crossplay_directory)" "${project}" "${path##*/}" --check || result=$?
    case "${result}" in 0) printf up_to_date ;; 2) printf updates_available ;; *) printf unavailable ;; esac
}

# Prepare BOTH downloads away from the live plugin root before any replacement.
jv_crossplay_prepare() {
    local staging="$1" project metadata expected url
    case "${MINECRAFT_SERVER_TYPE:-paper}" in fabric|neoforge)
        /usr/libexec/justvoxel/mjust/managed-crossplay-mods "${MINECRAFT_SERVER_TYPE}" "${MINECRAFT_VERSION}" "${MINECRAFT_LOADER_VERSION}" "${staging}"
        return ;;
    esac
    for project in $(jv_crossplay_projects); do
        metadata="$(jv_crossplay_metadata "${project}")" || { echo "ERROR: ${project} release information is unavailable." >&2; return 1; }
        printf '%s\n' "${metadata}" > "${staging}/${project}.json" || return 1
        expected="$(jq -r '.sha256' <<< "${metadata}")"
        url="https://download.geysermc.org/v2/projects/${project}/versions/$(jq -r '.version' <<< "${metadata}")/builds/$(jq -r '.build' <<< "${metadata}")/downloads/spigot"
        curl --connect-timeout 10 --max-time 180 -fsSL --proto '=https' --proto-redir '=https' \
            -H "User-Agent: ${JV_PAPER_USER_AGENT}" -o "${staging}/${project}.jar" "${url}" || {
                echo "ERROR: ${project} update download failed; installed plugins were preserved." >&2
                return 1
            }
        [[ $(sha256sum -- "${staging}/${project}.jar" | cut -d ' ' -f1) == "${expected}" ]] || {
            echo "ERROR: ${project} download verification failed; installed plugins were preserved." >&2
            return 1
        }
    done
}

# Save only the canonical managed pair, never configuration or identity material.
jv_crossplay_preserve() {
    local staging="$1" project path
    [[ ! -L ${DATA_PATH} && ! -L ${DATA_PATH}/$(jv_crossplay_directory) ]] || return 1
    mkdir -m0700 "${staging}/previous" || return 1
    for project in $(jv_crossplay_projects); do
        path="${DATA_PATH}/$(jv_crossplay_directory)/$(jv_crossplay_jar_name "${project}")"
        [[ ! -L ${path} ]] || return 1
        if [[ -e ${path} ]]; then
            [[ -f ${path} ]] || return 1
            cp --no-dereference -- "${path}" "${staging}/previous/${project}.jar" || return 1
            [[ -f ${staging}/previous/${project}.jar && ! -L ${staging}/previous/${project}.jar ]] || return 1
        else
            : > "${staging}/previous/${project}.absent" || return 1
        fi
    done
    : > "${staging}/replacement-started"
}

jv_crossplay_rollback() {
    local staging="$1" project path temporary failed=0
    [[ -f ${staging}/replacement-started ]] || return 0
    [[ ! -L ${DATA_PATH} && ! -L ${DATA_PATH}/$(jv_crossplay_directory) && -d ${DATA_PATH}/$(jv_crossplay_directory) ]] || return 1
    for project in $(jv_crossplay_projects); do
        path="${DATA_PATH}/$(jv_crossplay_directory)/$(jv_crossplay_jar_name "${project}")"
        if [[ -L ${path} || ( -e ${path} && ! -f ${path} ) ]]; then
            failed=1
            continue
        fi
        if [[ -f ${staging}/previous/${project}.absent ]]; then
            rm -f -- "${path}" || failed=1
        elif [[ -f ${staging}/previous/${project}.jar ]]; then
            temporary="$(mktemp "${DATA_PATH}/$(jv_crossplay_directory)/.justvoxel-rollback-${project}.XXXXXX")" || { failed=1; continue; }
            if [[ $(jv_crossplay_directory) == mods ]]; then
                if ! install -m0644 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${staging}/previous/${project}.jar" "${temporary}" ||
                    ! mv -fT -- "${temporary}" "${path}"; then
                    rm -f -- "${temporary}"
                    failed=1
                fi
            else
            if ! install -m0644 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${staging}/previous/${project}.jar" "${temporary}" ||
                ! mv -fT -- "${temporary}" "${path}" || ! jv_crossplay_label "${path}"; then
                rm -f -- "${temporary}"
                failed=1
            fi
            fi
        else
            failed=1
        fi
    done
    (( failed == 0 ))
}

jv_crossplay_install() {
    local staging="$1" project name expected temporary
    # Verify the complete prepared pair again before touching installed files.
    for project in $(jv_crossplay_projects); do
        expected="$(jq -er '.sha256 | select(test("^[a-f0-9]{64}$"))' "${staging}/${project}.json")" || return 1
        [[ $(sha256sum -- "${staging}/${project}.jar" | cut -d ' ' -f1) == "${expected}" ]] || return 1
    done
    [[ ! -L ${DATA_PATH} && ! -L ${DATA_PATH}/$(jv_crossplay_directory) ]] || return 1
    install -d -m0755 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${DATA_PATH}/$(jv_crossplay_directory)" || return 1
    jv_crossplay_preserve "${staging}" || return 1
    for project in $(jv_crossplay_projects); do
        name="$(jv_crossplay_jar_name "${project}")"
        if [[ $(jv_crossplay_artifact_state "${project}" "$(cat "${staging}/${project}.json")") == up_to_date ]]; then
            continue
        fi
        temporary="$(mktemp "${DATA_PATH}/$(jv_crossplay_directory)/.justvoxel-${project}.XXXXXX")" || return 1
        if [[ $(jv_crossplay_directory) == mods ]]; then
            if ! install -m0644 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${staging}/${project}.jar" "${temporary}" ||
                ! mv -fT -- "${temporary}" "${DATA_PATH}/mods/${name}"; then
                rm -f -- "${temporary}"
                return 1
            fi
        else
        if ! install -m0644 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${staging}/${project}.jar" "${temporary}" ||
            ! jv_crossplay_label "${temporary}" || ! mv -fT -- "${temporary}" "${DATA_PATH}/plugins/${name}" ||
            ! jv_crossplay_label "${DATA_PATH}/plugins/${name}"; then
            rm -f -- "${temporary}"
            return 1
        fi
        fi
        if [[ $(jv_crossplay_directory) == plugins ]]; then
            /usr/libexec/justvoxel/mjust/managed-plugin-files "${DATA_PATH}/plugins" "${project}" "${name}" || return 1
        fi
    done
}

jv_crossplay_verify() {
    local staging="$1" project
    for project in $(jv_crossplay_projects); do
        [[ $(jv_crossplay_artifact_state "${project}" "$(cat "${staging}/${project}.json")") == up_to_date ]] || return 1
    done
}
