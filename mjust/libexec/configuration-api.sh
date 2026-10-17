#!/usr/bin/bash
# Selected immutable-instance configuration; appliance discovery is separate.
readonly JV_CONFIG_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_config_api_error() {
    local response="${1:-}" fallback="${2:-Configuration request failed.}" message
    message="$(jq -r '.error // .message // empty' <<<"${response}" 2>/dev/null)" || message=''
    printf 'ERROR: %s\n' "${message:-${fallback}}" >&2
}

jv_config_request() {
    local method="$1" path="$2" payload="${3:-}" response
    if [[ ${method} == PUT ]]; then
        response="$(printf '%s\n' "${payload}" | "${JV_CONFIG_API_CLIENT}" PUT "${path}" --data)" || {
            jv_config_api_error "${response}"; return 1;
        }
    else
        response="$("${JV_CONFIG_API_CLIENT}" GET "${path}")" || {
            jv_config_api_error "${response}"; return 1;
        }
    fi
    jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1 || {
        echo 'ERROR: Management API returned invalid configuration data.' >&2; return 1;
    }
    printf '%s' "${response}"
}

jv_config_validate_id() {
    [[ ${1:-} =~ ^jv-[a-z0-9]{6,12}$ ]] || {
        echo 'ERROR: invalid Minecraft server ID.' >&2; return 1;
    }
}

jv_config_get() {
    local id="$1" response
    jv_config_validate_id "${id}" || return 1
    response="$(jv_config_request GET "/v1/minecraft/instances/${id}/configuration")" || return 1
    jq -e --arg id "${id}" '.id == $id and (.runtime | type == "object")' <<<"${response}" >/dev/null || {
        echo 'ERROR: configuration does not match the selected Minecraft server.' >&2; return 1;
    }
    printf '%s' "${response}"
}

jv_config_status() {
    jv_config_request GET /v1/status
}

jv_config_payload() {
    jq -c '{runtime:.runtime}'
}

jv_config_apply_payload() {
    local id="$1" payload="$2" status response
    jv_config_validate_id "${id}" || return 1
    status="$(jv_config_request GET "/v1/minecraft/instances/${id}/status")" || return 1
    jq -e --arg id "${id}" '.id == $id and .state == "stopped"' <<<"${status}" >/dev/null || {
        echo "ERROR: stop Minecraft server ${id} through its normal lifecycle controls before applying settings." >&2
        return 1
    }
    response="$(jv_config_request PUT "/v1/minecraft/instances/${id}/configuration" "${payload}")" || return 1
    jq -e --arg id "${id}" '.ok == true and .id == $id' <<<"${response}" >/dev/null || {
        echo 'ERROR: configuration update did not confirm the selected Minecraft server.' >&2; return 1;
    }
    echo 'Server settings saved. The server stays stopped. Start it normally when ready.'
}

jv_config_backup_get() {
    local id="$1" response
    jv_config_validate_id "${id}" || return 1
    response="$(jv_config_request GET "/v1/minecraft/instances/${id}/backups/settings")" || return 1
    jq -e '(.enabled | type == "boolean") and (.time | type == "string") and (.retention | type == "number")' <<<"${response}" >/dev/null || {
        echo 'ERROR: invalid selected-server backup settings.' >&2; return 1;
    }
    printf '%s' "${response}"
}

jv_config_backup_put() {
    local id="$1" payload="$2" response
    jv_config_validate_id "${id}" || return 1
    response="$(jv_config_request PUT "/v1/minecraft/instances/${id}/backups/settings" "${payload}")" || return 1
    jq -e --arg id "${id}" '.ok == true and .id == $id' <<<"${response}" >/dev/null || {
        echo 'ERROR: backup update did not confirm the selected Minecraft server.' >&2; return 1;
    }
    echo 'Selected-server backup settings saved.'
}
