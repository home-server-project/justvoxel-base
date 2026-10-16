#!/usr/bin/bash
# Local-root frontends share authoritative selection, never a synthetic default.
jv_select_registered_instance() {
    local selected="${1:-}" inventory
    [[ -z ${selected} || ${selected} =~ ^jv-[a-z0-9]{6,12}$ ]] || { echo 'ERROR: invalid Minecraft server ID.' >&2; return 1; }
    inventory="$(/usr/libexec/justvoxel/mjust/api-client GET /v1/minecraft/instances/runtime)" || return 1
    jq -e '.instances | type == "array" and all(.[]; .id | test("^jv-[a-z0-9]{6,12}$"))' <<<"${inventory}" >/dev/null || return 1
    if [[ -z ${selected} ]]; then
        [[ $(jq '.instances | length' <<<"${inventory}") == 1 ]] || {
            echo 'ERROR: choose a Minecraft server by its jv-... ID; automatic selection requires exactly one server.' >&2
            return 1
        }
        selected="$(jq -r '.instances[0].id' <<<"${inventory}")"
    fi
    jq -e --arg id "${selected}" 'any(.instances[]; .id == $id)' <<<"${inventory}" >/dev/null || { echo 'ERROR: selected Minecraft server is no longer registered.' >&2; return 1; }
    printf '%s' "${selected}"
}
