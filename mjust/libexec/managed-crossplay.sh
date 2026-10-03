#!/usr/bin/bash
# Fixed JustVoxel Spigot components only. No caller-supplied download URLs.
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
    case "$1" in geyser) printf 'Geyser-Spigot.jar' ;; floodgate) printf 'floodgate-spigot.jar' ;; *) return 1 ;; esac
}

jv_crossplay_artifact_state() {
    local project="$1" metadata="$2" expected path actual
    [[ ! -L ${DATA_PATH}/plugins ]] || { printf unavailable; return; }
    expected="$(jq -er '.sha256 | select(test("^[a-f0-9]{64}$"))' <<< "${metadata}" 2>/dev/null)" || { printf unavailable; return; }
    path="${DATA_PATH}/plugins/$(jv_crossplay_jar_name "${project}")" || { printf unavailable; return; }
    if [[ ! -e ${path} && ! -L ${path} ]]; then printf updates_available; return; fi
    [[ -f ${path} && ! -L ${path} ]] || { printf unavailable; return; }
    actual="$(sha256sum -- "${path}")" || { printf unavailable; return; }
    if [[ ${actual%% *} != "${expected}" ]]; then printf updates_available; return; fi
    local result=0
    /usr/libexec/justvoxel/mjust/managed-plugin-files "${DATA_PATH}/plugins" "${project}" "${path##*/}" --check || result=$?
    case "${result}" in 0) printf up_to_date ;; 2) printf updates_available ;; *) printf unavailable ;; esac
}

# Prepare BOTH downloads away from the live plugin root before any replacement.
jv_crossplay_prepare() {
    local staging="$1" project metadata expected url
    for project in geyser floodgate; do
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
    [[ ! -L ${DATA_PATH} && ! -L ${DATA_PATH}/plugins ]] || return 1
    mkdir -m0700 "${staging}/previous" || return 1
    for project in geyser floodgate; do
        path="${DATA_PATH}/plugins/$(jv_crossplay_jar_name "${project}")"
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
    [[ ! -L ${DATA_PATH} && ! -L ${DATA_PATH}/plugins && -d ${DATA_PATH}/plugins ]] || return 1
    for project in geyser floodgate; do
        path="${DATA_PATH}/plugins/$(jv_crossplay_jar_name "${project}")"
        if [[ -L ${path} || ( -e ${path} && ! -f ${path} ) ]]; then
            failed=1
            continue
        fi
        if [[ -f ${staging}/previous/${project}.absent ]]; then
            rm -f -- "${path}" || failed=1
        elif [[ -f ${staging}/previous/${project}.jar ]]; then
            temporary="$(mktemp "${DATA_PATH}/plugins/.justvoxel-rollback-${project}.XXXXXX")" || { failed=1; continue; }
            if ! install -m0644 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${staging}/previous/${project}.jar" "${temporary}" ||
                ! mv -fT -- "${temporary}" "${path}" || ! restorecon -F "${path}"; then
                rm -f -- "${temporary}"
                failed=1
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
    for project in geyser floodgate; do
        expected="$(jq -er '.sha256 | select(test("^[a-f0-9]{64}$"))' "${staging}/${project}.json")" || return 1
        [[ $(sha256sum -- "${staging}/${project}.jar" | cut -d ' ' -f1) == "${expected}" ]] || return 1
    done
    [[ ! -L ${DATA_PATH} && ! -L ${DATA_PATH}/plugins ]] || return 1
    install -d -m0755 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${DATA_PATH}/plugins" || return 1
    jv_crossplay_preserve "${staging}" || return 1
    for project in geyser floodgate; do
        name="$(jv_crossplay_jar_name "${project}")"
        if [[ $(jv_crossplay_artifact_state "${project}" "$(cat "${staging}/${project}.json")") == up_to_date ]]; then
            continue
        fi
        temporary="$(mktemp "${DATA_PATH}/plugins/.justvoxel-${project}.XXXXXX")" || return 1
        if ! install -m0644 -o "${MINECRAFT_UID}" -g "${MINECRAFT_GID}" "${staging}/${project}.jar" "${temporary}" ||
            ! restorecon -F "${temporary}" || ! mv -fT -- "${temporary}" "${DATA_PATH}/plugins/${name}" ||
            ! restorecon -F "${DATA_PATH}/plugins/${name}"; then
            rm -f -- "${temporary}"
            return 1
        fi
        /usr/libexec/justvoxel/mjust/managed-plugin-files "${DATA_PATH}/plugins" "${project}" "${name}" || return 1
    done
}

jv_crossplay_verify() {
    local staging="$1" project
    for project in geyser floodgate; do
        [[ $(jv_crossplay_artifact_state "${project}" "$(cat "${staging}/${project}.json")") == up_to_date ]] || return 1
    done
}
