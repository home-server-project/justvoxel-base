#!/usr/bin/bash
# Source after common.sh. Explicit immutable ID only; never load global server
# settings. The committed record, registry and runtime hashes must agree.
jv_load_instance() {
    local id="$1" record key value
    [[ ${id} =~ ^jv-[a-z0-9]{6,12}$ ]] || return 1
    /usr/libexec/justvoxel/mjust/validate-instance-runtime-commit "${id}" >/dev/null || return 1
    record="/etc/justvoxel/instances/${id}/config.json"
    JV_INSTANCE_ID=${id}
    JV_INSTANCE_CONTAINER="minecraft-${id}"
    JV_INSTANCE_UNIT="${JV_INSTANCE_CONTAINER}.service"
    DATA_PATH="$(jq -er '.storage.data_path' "${record}")" || return 1
    [[ ${DATA_PATH} == "$(jq -er '.storage.root' "${record}")/minecraft-instances/${id}/data" ]] || return 1
    MINECRAFT_SERVER_TYPE="$(jq -er '.runtime.software' "${record}")" || return 1
    MINECRAFT_IMAGE_TAG="$(jq -er '.runtime.image_tag' "${record}")" || return 1
    JV_INSTANCE_IMAGE_DIGEST="$(jq -r '.runtime.image_digest // ""' "${record}")" || return 1
    JV_INSTANCE_IMAGE_REF="docker.io/itzg/minecraft-server:${MINECRAFT_IMAGE_TAG}"
    if [[ -n ${JV_INSTANCE_IMAGE_DIGEST} ]]; then
        [[ ${JV_INSTANCE_IMAGE_DIGEST} =~ ^sha256:[a-f0-9]{64}$ ]] || return 1
        JV_INSTANCE_IMAGE_REF="docker.io/itzg/minecraft-server@${JV_INSTANCE_IMAGE_DIGEST}"
    fi
    MINECRAFT_VERSION="$(jq -er '.runtime.version' "${record}")" || return 1
    MINECRAFT_VERSION_MODE="$(jq -r '.runtime.version_policy // (if .runtime.version == "LATEST" then "latest" else "pinned" end)' "${record}")" || return 1
    MINECRAFT_UID="$(jq -er '.runtime.uid' "${record}")" || return 1
    MINECRAFT_GID="$(jq -er '.runtime.gid' "${record}")" || return 1
    BEDROCK_ENABLED="$(jq -r 'if .runtime.bedrock_enabled then "yes" else "no" end' "${record}")" || return 1
    BEDROCK_MANAGED_PLUGINS=yes
    validate_server_capabilities || return 1
    wait_for_rcon() {
        local timeout="${1:-900}" elapsed=0
        while (( elapsed < timeout )); do
            if podman exec "${JV_INSTANCE_CONTAINER}" rcon-cli list >/dev/null 2>&1 && systemctl is-active --quiet "${JV_INSTANCE_UNIT}"; then return 0; fi
            sleep 5
            elapsed=$((elapsed + 5))
        done
        return 1
    }
}
