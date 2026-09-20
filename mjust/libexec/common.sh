#!/usr/bin/bash
set -euo pipefail

readonly JV_CONFIG_DIR=/etc/justvoxel
readonly JV_CONFIG=${JV_CONFIG_DIR}/justvoxel.conf
readonly JV_MC_ENV=${JV_CONFIG_DIR}/minecraft.env
readonly JV_BACKUP_ENV=${JV_CONFIG_DIR}/minecraft-backup.env
readonly JV_TEMPLATE_ROOT=/usr/share/justvoxel/templates
readonly JV_QUADLET=/etc/containers/systemd/minecraft.container
readonly JV_BACKUP_SERVICE=/etc/systemd/system/minecraft-backup.service
readonly JV_BACKUP_TIMER=/etc/systemd/system/minecraft-backup.timer
readonly JV_MAINTENANCE_LOCK=/run/justvoxel-minecraft-maintenance.lock
readonly JV_STATE_DIR=/var/lib/justvoxel/state
readonly JV_SETUP_IN_PROGRESS=/var/lib/justvoxel/management/setup-in-progress
readonly JV_PREVIOUS_IMAGE_STATE=${JV_STATE_DIR}/minecraft-previous-image-id
readonly JV_MINECRAFT_IMAGE_REPO=docker.io/itzg/minecraft-server
readonly JV_ITZG_IMAGES_URL=https://raw.githubusercontent.com/itzg/docker-minecraft-server/refs/heads/master/images.json
readonly JV_GEYSER_VERSIONS_URL=https://raw.githubusercontent.com/GeyserMC/GeyserWebsite/refs/heads/master/src/data/versions.json
readonly JV_PAPER_USER_AGENT='JustVoxel/0.1 (https://github.com/home-server-project/justvoxel)'

jv_variant_raw() {
    cat /usr/lib/justvoxel/variant 2>/dev/null || printf 'unknown\n'
}

jv_variant_kind() {
    local raw="${1:-}"
    [[ -n ${raw} ]] || raw="$(jv_variant_raw)"
    case "${raw}" in
        vm|justvoxel-vm) printf 'vm\n' ;;
        hwe|justvoxel-hwe|baremetal|justvoxel-baremetal) printf 'hwe\n' ;;
        *) printf 'unknown\n' ;;
    esac
}

jv_variant_name() {
    case "$(jv_variant_kind "${1:-}")" in
        vm) printf 'VM\n' ;;
        hwe) printf 'HWE\n' ;;
        *) printf 'Unknown\n' ;;
    esac
}

jv_variant_is_vm() {
    [[ $(jv_variant_kind "${1:-}") == vm ]]
}

jv_variant_is_hwe() {
    [[ $(jv_variant_kind "${1:-}") == hwe ]]
}

require_root() {
    if [[ ${EUID} -ne 0 ]]; then
        echo 'ERROR: this operation must run as root.' >&2
        exit 1
    fi
}

require_config() {
    if [[ ! -r ${JV_CONFIG} ]]; then
        echo 'ERROR: JustVoxel has not been configured yet. Run: mjust setup' >&2
        exit 1
    fi
    # shellcheck disable=SC1090
    source "${JV_CONFIG}"

    # Compatibility defaults for configurations created before image/version
    # policy and portable Minecraft settings became administrator-configurable.
    MINECRAFT_IMAGE_TAG="${MINECRAFT_IMAGE_TAG:-latest}"
    GAME_MODE="${GAME_MODE:-survival}"
    DIFFICULTY="${DIFFICULTY:-normal}"
    WHITELIST_ENABLED="${WHITELIST_ENABLED:-yes}"
    ENFORCE_WHITELIST="${ENFORCE_WHITELIST:-yes}"
    BEDROCK_MANAGED_PLUGINS="${BEDROCK_MANAGED_PLUGINS:-yes}"
    if [[ -z ${MINECRAFT_VERSION_MODE:-} ]]; then
        if [[ ${MINECRAFT_VERSION:-} == LATEST ]]; then
            MINECRAFT_VERSION_MODE=latest
        else
            MINECRAFT_VERSION_MODE=pinned
        fi
    fi
}

prompt_default() {
    local prompt="$1" default="$2" value
    read -r -p "${prompt} [${default}]: " value
    printf '%s' "${value:-${default}}"
}

confirm() {
    local prompt="$1" answer
    read -r -p "${prompt} [y/N]: " answer
    [[ ${answer,,} == y || ${answer,,} == yes ]]
}

# Menu/help text is deliberately written to stderr. Many callers use command
# substitution and stdout must contain only the selected value.
choose() {
    local prompt="$1"
    shift
    local options=("$@") choice index
    printf '%s\n' "${prompt}" >&2
    for index in "${!options[@]}"; do
        printf '  %d. %s\n' "$((index + 1))" "${options[index]}" >&2
    done
    while true; do
        read -r -p 'Selection: ' choice
        if [[ ${choice} =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#options[@]} )); then
            printf '%s' "${choice}"
            return 0
        fi
        echo 'Please enter a valid number.' >&2
    done
}

# Defaults are opt-in and limited to menus where pressing Enter is safe.
# Destructive/storage menus continue to use choose(), which has no default.
choose_default() {
    local prompt="$1" default="$2"
    shift 2
    local options=("$@") choice index
    [[ ${default} =~ ^[0-9]+$ ]] && (( default >= 1 && default <= ${#options[@]} )) || {
        echo 'ERROR: invalid menu default.' >&2
        return 2
    }
    printf '%s\n' "${prompt}" >&2
    for index in "${!options[@]}"; do
        printf '  %d. %s\n' "$((index + 1))" "${options[index]}" >&2
    done
    while true; do
        read -r -p "Selection [${default}]: " choice
        choice="${choice:-${default}}"
        if [[ ${choice} =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#options[@]} )); then
            printf '%s' "${choice}"
            return 0
        fi
        echo 'Please enter a valid number.' >&2
    done
}

validate_port() {
    local port="$1"
    [[ ${port} =~ ^[0-9]+$ ]] && (( port >= 1 && port <= 65535 ))
}

validate_memory() {
    [[ $1 =~ ^[1-9][0-9]*[MmGg]$ ]]
}

memory_to_mib() {
    local value="${1^^}" number="${1%?}"
    if [[ ${value} == *G ]]; then
        printf '%d' $((number * 1024))
    else
        printf '%d' "${number}"
    fi
}

suggest_memory_values() {
    local total_mib
    total_mib=$(( $(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null || echo 0) / 1024 ))
    if (( total_mib >= 32768 )); then
        printf '8G 12G\n'
    elif (( total_mib >= 16384 )); then
        printf '6G 8G\n'
    elif (( total_mib >= 8192 )); then
        printf '4G 6G\n'
    elif (( total_mib >= 4096 )); then
        printf '2G 3G\n'
    else
        printf '1G 2G\n'
    fi
}

normalize_daily_backup_time() {
    local value="$1" hour minute
    [[ ${value} =~ ^([0-9]{1,2}):([0-9]{2})$ ]] || return 1
    hour="${BASH_REMATCH[1]}"
    minute="${BASH_REMATCH[2]}"
    (( 10#${hour} <= 23 && 10#${minute} <= 59 )) || return 1
    printf '%02d:%02d' "$((10#${hour}))" "$((10#${minute}))"
}

daily_backup_schedule_from_time() {
    local normalized
    normalized="$(normalize_daily_backup_time "$1")" || return 1
    printf '*-*-* %s:00' "${normalized}"
}

validate_positive_int() {
    [[ $1 =~ ^[1-9][0-9]*$ ]]
}

validate_nonroot_id() {
    local value="${1:-}"
    [[ ${value} =~ ^[0-9]+$ ]] || return 1
    (( 10#${value} > 0 && 10#${value} <= 4294967294 ))
}

resolve_minecraft_ids() {
    local uid gid

    if validate_nonroot_id "${SUDO_UID:-}" && validate_nonroot_id "${SUDO_GID:-}"; then
        printf '%s %s sudo\n' "${SUDO_UID}" "${SUDO_GID}"
        return 0
    fi

    if id voxel >/dev/null 2>&1; then
        uid="$(id -u voxel)"
        gid="$(id -g voxel)"
        if validate_nonroot_id "${uid}" && validate_nonroot_id "${gid}"; then
            printf '%s %s voxel\n' "${uid}" "${gid}"
            return 0
        fi
    fi

    printf '1000 1000 fallback\n'
}

validate_storage_path() {
    [[ $1 =~ ^/[A-Za-z0-9._/-]+$ && $1 != / ]]
}

validate_simple_text() {
    local value="$1"
    [[ ${value} != *$'\n'* && ${value} != *$'\r'* ]]
}

shell_quote_assignment() {
    local key="$1" value="$2"
    printf '%s=' "${key}"
    printf '%q' "${value}"
    printf '\n'
}

minecraft_image_ref() {
    printf '%s:%s' "${JV_MINECRAFT_IMAGE_REPO}" "${MINECRAFT_IMAGE_TAG}"
}

validate_minecraft_image_tag() {
    local tag="$1" catalog
    [[ ${tag} =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || return 1

    skopeo inspect "docker://${JV_MINECRAFT_IMAGE_REPO}:${tag}" >/dev/null 2>&1 || return 1

    # Upstream publishes a machine-readable list of moving Java/image tags.
    # Reject entries upstream explicitly marks deprecated. Exact release tags are
    # not in this catalog and are accepted when the registry confirms they exist.
    catalog="$(curl -fsSL "${JV_ITZG_IMAGES_URL}" 2>/dev/null || true)"
    if [[ -n ${catalog} ]] && jq -e --arg tag "${tag}" \
        '.[] | select(.tag == $tag and (.deprecated // false) == true)' \
        <<< "${catalog}" >/dev/null 2>&1; then
        return 2
    fi
    return 0
}

resolve_latest_itzg_release() {
    curl -fsSL -H "User-Agent: ${JV_PAPER_USER_AGENT}" \
        https://api.github.com/repos/itzg/docker-minecraft-server/releases/latest 2>/dev/null \
        | jq -r '.tag_name // empty'
}

write_main_config() {
    install -d -m0700 -o root -g root "${JV_CONFIG_DIR}"
    local tmp
    tmp="$(mktemp "${JV_CONFIG_DIR}/.justvoxel.conf.XXXXXX")"
    chmod 0600 "${tmp}"
    {
        echo '# JustVoxel administrator-owned configuration.'
        echo '# Generated by mjust. bootc image updates do not overwrite this file.'
        shell_quote_assignment DATA_PATH "${DATA_PATH}"
        shell_quote_assignment DATA_MOUNT_POINT "${DATA_MOUNT_POINT}"
        shell_quote_assignment DATA_EXPECTED_UUID "${DATA_EXPECTED_UUID}"
        shell_quote_assignment DATA_EXPECTED_SOURCE "${DATA_EXPECTED_SOURCE}"
        shell_quote_assignment BACKUP_TYPE "${BACKUP_TYPE}"
        shell_quote_assignment BACKUP_PATH "${BACKUP_PATH}"
        shell_quote_assignment BACKUP_MOUNT_POINT "${BACKUP_MOUNT_POINT}"
        shell_quote_assignment BACKUP_EXPECTED_UUID "${BACKUP_EXPECTED_UUID}"
        shell_quote_assignment BACKUP_EXPECTED_SOURCE "${BACKUP_EXPECTED_SOURCE}"
        shell_quote_assignment BACKUP_KEEP "${BACKUP_KEEP}"
        shell_quote_assignment BACKUP_SCHEDULE "${BACKUP_SCHEDULE}"
        shell_quote_assignment BACKUP_TIMER_ENABLED "${BACKUP_TIMER_ENABLED}"
        shell_quote_assignment JAVA_PORT "${JAVA_PORT}"
        shell_quote_assignment BEDROCK_ENABLED "${BEDROCK_ENABLED}"
        shell_quote_assignment BEDROCK_PORT "${BEDROCK_PORT}"
        shell_quote_assignment BEDROCK_MANAGED_PLUGINS "${BEDROCK_MANAGED_PLUGINS:-yes}"
        shell_quote_assignment JAVA_MEMORY "${JAVA_MEMORY}"
        shell_quote_assignment CONTAINER_MEMORY "${CONTAINER_MEMORY}"
        shell_quote_assignment MINECRAFT_IMAGE_TAG "${MINECRAFT_IMAGE_TAG}"
        shell_quote_assignment MINECRAFT_VERSION_MODE "${MINECRAFT_VERSION_MODE}"
        shell_quote_assignment MINECRAFT_VERSION "${MINECRAFT_VERSION}"
        shell_quote_assignment MINECRAFT_UID "${MINECRAFT_UID}"
        shell_quote_assignment MINECRAFT_GID "${MINECRAFT_GID}"
        shell_quote_assignment TIMEZONE "${TIMEZONE}"
        shell_quote_assignment GAME_MODE "${GAME_MODE:-survival}"
        shell_quote_assignment DIFFICULTY "${DIFFICULTY:-normal}"
        shell_quote_assignment WHITELIST_ENABLED "${WHITELIST_ENABLED:-yes}"
        shell_quote_assignment ENFORCE_WHITELIST "${ENFORCE_WHITELIST:-yes}"
        shell_quote_assignment MAX_PLAYERS "${MAX_PLAYERS}"
        shell_quote_assignment MOTD "${MOTD}"
    } > "${tmp}"
    mv -f "${tmp}" "${JV_CONFIG}"
    chown root:root "${JV_CONFIG}"
    chmod 0600 "${JV_CONFIG}"
}

sed_replacement_escape() {
    local value="$1"
    value=${value//\\/\\\\}
    value=${value//&/\\&}
    value=${value//|/\\|}
    printf '%s' "${value}"
}

replace_token() {
    local file="$1" token="$2" value="$3" escaped
    escaped="$(sed_replacement_escape "${value}")"
    sed -i "s|@@${token}@@|${escaped}|g" "${file}"
}

systemd_env_escape() {
    local value="$1"
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    printf '%s' "${value}"
}

path_regex_escape() {
    printf '%s' "$1" | sed 's/[][\\.^$*+?(){}|]/\\&/g'
}

apply_data_selinux() {
    local escaped
    escaped="$(path_regex_escape "${DATA_PATH}")"
    if ! semanage fcontext -a -t container_file_t "${escaped}(/.*)?" 2>/dev/null; then
        semanage fcontext -m -t container_file_t "${escaped}(/.*)?"
    fi
    restorecon -RF "${DATA_PATH}"
}

capture_backup_mount_identity() {
    BACKUP_EXPECTED_UUID=''
    BACKUP_EXPECTED_SOURCE=''
    if [[ -z ${BACKUP_MOUNT_POINT} ]]; then
        return 0
    fi
    if ! mountpoint -q -- "${BACKUP_MOUNT_POINT}"; then
        echo "ERROR: selected backup mount is not mounted: ${BACKUP_MOUNT_POINT}" >&2
        return 1
    fi
    BACKUP_EXPECTED_SOURCE="$(findmnt -n -o SOURCE --target "${BACKUP_MOUNT_POINT}" 2>/dev/null || true)"
    if [[ ${BACKUP_TYPE} == disk || ${BACKUP_TYPE} == partition ]]; then
        BACKUP_EXPECTED_UUID="$(findmnt -n -o UUID --target "${BACKUP_MOUNT_POINT}" 2>/dev/null || true)"
        if [[ -z ${BACKUP_EXPECTED_UUID} ]]; then
            echo 'ERROR: could not determine a filesystem UUID for the selected local backup mount.' >&2
            return 1
        fi
    fi
}

geyser_supported_java_version_from_json() {
    local metadata="$1" version
    version="$(jq -er '.java.supported | select(type == "string")' <<< "${metadata}" 2>/dev/null)" || return 1
    [[ ${version} =~ ^[0-9]+([.][0-9]+){1,2}$ ]] || return 1
    printf '%s' "${version}"
}

resolve_geyser_supported_java_version() {
    local metadata
    metadata="$(curl -fsSL -H "User-Agent: ${JV_PAPER_USER_AGENT}" "${JV_GEYSER_VERSIONS_URL}" 2>/dev/null)" || return 1
    geyser_supported_java_version_from_json "${metadata}"
}
paper_version_has_stable_build() {
    local version="$1" builds
    builds="$(curl -fsSL -H "User-Agent: ${JV_PAPER_USER_AGENT}" \
        "https://fill.papermc.io/v3/projects/paper/versions/${version}/builds" 2>/dev/null)" || return 2
    if jq -e '.ok == false' <<< "${builds}" >/dev/null 2>&1; then
        return 1
    fi
    jq -e 'map(select(.channel == "STABLE")) | length > 0' <<< "${builds}" >/dev/null
}

resolve_latest_stable_paper_version() {
    local project versions version builds
    project="$(curl -fsSL -H "User-Agent: ${JV_PAPER_USER_AGENT}" https://fill.papermc.io/v3/projects/paper 2>/dev/null)" || return 1
    versions="$(jq -r '.versions | to_entries[] | .value[]' <<< "${project}" | sort -V -r)"
    while IFS= read -r version; do
        [[ -n ${version} ]] || continue
        builds="$(curl -fsSL -H "User-Agent: ${JV_PAPER_USER_AGENT}" \
            "https://fill.papermc.io/v3/projects/paper/versions/${version}/builds" 2>/dev/null)" || continue
        if jq -e 'map(select(.channel == "STABLE")) | length > 0' <<< "${builds}" >/dev/null; then
            printf '%s' "${version}"
            return 0
        fi
    done <<< "${versions}"
    return 1
}

render_runtime_files() {
    require_config
    install -d -m0700 -o root -g root "${JV_CONFIG_DIR}"
    install -d -m0755 -o root -g root /etc/containers/systemd

    local rcon_password env_tmp quadlet_tmp backup_tmp service_tmp timer_tmp image_ref
    if [[ ${JUSTVOXEL_REGENERATE_RCON:-0} == 1 ]]; then
        rcon_password="$(openssl rand -hex 24)"
    elif [[ -r ${JV_MC_ENV} ]]; then
        rcon_password="$(sed -n 's/^RCON_PASSWORD=//p' "${JV_MC_ENV}" | head -n1)"
    else
        rcon_password="$(openssl rand -hex 24)"
    fi
    [[ -n ${rcon_password} ]] || rcon_password="$(openssl rand -hex 24)"

    env_tmp="$(mktemp "${JV_CONFIG_DIR}/.minecraft.env.XXXXXX")"
    cp "${JV_TEMPLATE_ROOT}/config/minecraft.env.in" "${env_tmp}"
    replace_token "${env_tmp}" EULA TRUE
    replace_token "${env_tmp}" MINECRAFT_VERSION "${MINECRAFT_VERSION}"
    replace_token "${env_tmp}" MINECRAFT_UID "${MINECRAFT_UID}"
    replace_token "${env_tmp}" MINECRAFT_GID "${MINECRAFT_GID}"
    replace_token "${env_tmp}" TIMEZONE "$(systemd_env_escape "${TIMEZONE}")"
    replace_token "${env_tmp}" JAVA_MEMORY "${JAVA_MEMORY^^}"
    replace_token "${env_tmp}" GAME_MODE "${GAME_MODE}"
    replace_token "${env_tmp}" DIFFICULTY "${DIFFICULTY}"
    replace_token "${env_tmp}" WHITELIST_ENABLED "$([[ ${WHITELIST_ENABLED} == yes ]] && printf TRUE || printf FALSE)"
    replace_token "${env_tmp}" ENFORCE_WHITELIST "$([[ ${ENFORCE_WHITELIST} == yes ]] && printf TRUE || printf FALSE)"
    replace_token "${env_tmp}" MAX_PLAYERS "${MAX_PLAYERS}"
    replace_token "${env_tmp}" RCON_PASSWORD "${rcon_password}"
    replace_token "${env_tmp}" MOTD "$(systemd_env_escape "${MOTD}")"
    if [[ ${BEDROCK_ENABLED} == yes && ${BEDROCK_MANAGED_PLUGINS} == yes ]]; then
        replace_token "${env_tmp}" PLUGINS_LINE 'PLUGINS=https://download.geysermc.org/v2/projects/geyser/versions/latest/builds/latest/downloads/spigot,https://download.geysermc.org/v2/projects/floodgate/versions/latest/builds/latest/downloads/spigot'
    elif [[ ${BEDROCK_ENABLED} == yes ]]; then
        replace_token "${env_tmp}" PLUGINS_LINE '# Geyser/Floodgate binaries are preserved from imported persistent server data.'
    else
        replace_token "${env_tmp}" PLUGINS_LINE '# Bedrock cross-play disabled; Geyser and Floodgate are not installed by JustVoxel.'
    fi
    install -o root -g root -m0600 "${env_tmp}" "${JV_MC_ENV}"
    rm -f "${env_tmp}"

    image_ref="$(minecraft_image_ref)"
    quadlet_tmp="$(mktemp /etc/containers/systemd/.minecraft.container.XXXXXX)"
    cp "${JV_TEMPLATE_ROOT}/quadlets/minecraft.container.in" "${quadlet_tmp}"
    replace_token "${quadlet_tmp}" MINECRAFT_IMAGE "${image_ref}"
    replace_token "${quadlet_tmp}" DATA_PATH "${DATA_PATH}"
    replace_token "${quadlet_tmp}" JAVA_PORT "${JAVA_PORT}"
    replace_token "${quadlet_tmp}" CONTAINER_MEMORY "${CONTAINER_MEMORY^^}"
    if [[ ${BEDROCK_ENABLED} == yes ]]; then
        replace_token "${quadlet_tmp}" BEDROCK_PUBLISH_PORT "PublishPort=${BEDROCK_PORT}:19132/udp"
    else
        replace_token "${quadlet_tmp}" BEDROCK_PUBLISH_PORT '# Bedrock port disabled.'
    fi
    install -o root -g root -m0644 "${quadlet_tmp}" "${JV_QUADLET}"
    rm -f "${quadlet_tmp}"

    backup_tmp="$(mktemp "${JV_CONFIG_DIR}/.minecraft-backup.env.XXXXXX")"
    cp "${JV_TEMPLATE_ROOT}/config/minecraft-backup.env.in" "${backup_tmp}"
    replace_token "${backup_tmp}" DATA_PATH "${DATA_PATH}"
    replace_token "${backup_tmp}" BACKUP_PATH "${BACKUP_PATH}"
    replace_token "${backup_tmp}" BACKUP_KEEP "${BACKUP_KEEP}"
    replace_token "${backup_tmp}" BACKUP_MOUNT_POINT "${BACKUP_MOUNT_POINT}"
    replace_token "${backup_tmp}" BACKUP_EXPECTED_UUID "${BACKUP_EXPECTED_UUID}"
    replace_token "${backup_tmp}" BACKUP_EXPECTED_SOURCE "${BACKUP_EXPECTED_SOURCE}"
    install -o root -g root -m0600 "${backup_tmp}" "${JV_BACKUP_ENV}"
    rm -f "${backup_tmp}"

    service_tmp="$(mktemp /etc/systemd/system/.minecraft-backup.service.XXXXXX)"
    cp "${JV_TEMPLATE_ROOT}/systemd/minecraft-backup.service.in" "${service_tmp}"
    replace_token "${service_tmp}" DATA_PATH "${DATA_PATH}"
    if [[ -n ${BACKUP_MOUNT_POINT} ]]; then
        replace_token "${service_tmp}" BACKUP_REQUIREMENT_PATH "${BACKUP_MOUNT_POINT}"
    else
        replace_token "${service_tmp}" BACKUP_REQUIREMENT_PATH "${BACKUP_PATH}"
    fi
    install -o root -g root -m0644 "${service_tmp}" "${JV_BACKUP_SERVICE}"
    rm -f "${service_tmp}"

    timer_tmp="$(mktemp /etc/systemd/system/.minecraft-backup.timer.XXXXXX)"
    cp "${JV_TEMPLATE_ROOT}/systemd/minecraft-backup.timer.in" "${timer_tmp}"
    replace_token "${timer_tmp}" BACKUP_SCHEDULE "${BACKUP_SCHEDULE}"
    install -o root -g root -m0644 "${timer_tmp}" "${JV_BACKUP_TIMER}"
    rm -f "${timer_tmp}"

    systemctl daemon-reload
}

activate_backup_timer() {
    if [[ ${BACKUP_TIMER_ENABLED} == yes ]]; then
        systemctl enable minecraft-backup.timer >/dev/null
        systemctl restart minecraft-backup.timer
    else
        systemctl disable --now minecraft-backup.timer 2>/dev/null || true
    fi
}

render_runtime() {
    render_runtime_files
    activate_backup_timer
}

configure_firewall_initial() {
    firewall-cmd --permanent --add-port="${JAVA_PORT}/tcp" >/dev/null
    if [[ ${BEDROCK_ENABLED} == yes ]]; then
        firewall-cmd --permanent --add-port="${BEDROCK_PORT}/udp" >/dev/null
    fi
    firewall-cmd --reload >/dev/null
}

update_firewall_ports() {
    local old_java="$1" old_bedrock_enabled="$2" old_bedrock="$3"
    if [[ ${old_java} != ${JAVA_PORT} ]]; then
        firewall-cmd --permanent --remove-port="${old_java}/tcp" >/dev/null 2>&1 || true
    fi
    if [[ ${old_bedrock_enabled} == yes && ( ${BEDROCK_ENABLED} != yes || ${old_bedrock} != ${BEDROCK_PORT} ) ]]; then
        firewall-cmd --permanent --remove-port="${old_bedrock}/udp" >/dev/null 2>&1 || true
    fi
    firewall-cmd --permanent --add-port="${JAVA_PORT}/tcp" >/dev/null
    if [[ ${BEDROCK_ENABLED} == yes ]]; then
        firewall-cmd --permanent --add-port="${BEDROCK_PORT}/udp" >/dev/null
    fi
    firewall-cmd --reload >/dev/null
}

wait_for_rcon() {
    local timeout="${1:-900}" elapsed=0
    while (( elapsed < timeout )); do
        if podman exec minecraft rcon-cli 'list' >/dev/null 2>&1; then
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done
    return 1
}

configure_geyser_floodgate() {
    [[ ${BEDROCK_ENABLED} == yes ]] || return 0
    local geyser_config="${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
    if [[ ! -f ${geyser_config} ]]; then
        echo "ERROR: Geyser configuration was not generated: ${geyser_config}" >&2
        return 1
    fi
    if grep -Eq '^[[:space:]]*auth-type:[[:space:]]*floodgate[[:space:]]*$' "${geyser_config}"; then
        return 0
    fi
    echo 'Configuring Geyser to use Floodgate authentication.'
    systemctl stop minecraft.service
    sed -i -E 's/^([[:space:]]*auth-type:).*/\1 floodgate/' "${geyser_config}"
    chown "${MINECRAFT_UID}:${MINECRAFT_GID}" "${geyser_config}"
    restorecon -F "${geyser_config}" 2>/dev/null || true
    grep -Eq '^[[:space:]]*auth-type:[[:space:]]*floodgate[[:space:]]*$' "${geyser_config}"
    systemctl start minecraft.service
    wait_for_rcon 900
}
