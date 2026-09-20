#!/usr/bin/bash

readonly JV_CONFIG_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_config_api_error() {
    local response="${1:-}" fallback="${2:-Configuration request failed.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // .message // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_config_get() {
    local response rc
    set +e
    response="$("${JV_CONFIG_API_CLIENT}" GET /v1/admin/configuration)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_config_api_error "${response}" 'Configuration is unavailable from the JustVoxel Management API.'
        return 1
    fi
    if ! jq -e '
        type == "object" and
        (.configured | type == "boolean") and
        (.minecraft | type == "object") and
        (.backup | type == "object")
    ' <<<"${response}" >/dev/null 2>&1; then
        echo 'ERROR: JustVoxel Management API returned invalid configuration data.' >&2
        return 1
    fi
    if [[ $(jq -r '.configured' <<<"${response}") != true ]]; then
        echo 'ERROR: JustVoxel has not been configured yet. Run: mjust setup' >&2
        return 1
    fi
    printf '%s' "${response}"
}

jv_config_status() {
    local response rc
    set +e
    response="$("${JV_CONFIG_API_CLIENT}" GET /v1/status)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_config_api_error "${response}" 'Status is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_config_payload() {
    jq -c '{
        java_memory:.minecraft.java_memory,
        container_memory:.minecraft.container_memory,
        java_port:.minecraft.java_port,
        bedrock_enabled:.minecraft.bedrock_enabled,
        bedrock_port:.minecraft.bedrock_port,
        timezone:.minecraft.timezone,
        max_players:.minecraft.max_players,
        motd:.minecraft.motd,
        image_tag:.minecraft.image_tag,
        version_policy:.minecraft.version_mode,
        version:.minecraft.version,
        backup_keep:.backup.keep,
        backup_schedule:.backup.schedule,
        backup_timer_enabled:.backup.timer_enabled,
        confirm_players:false
    }'
}

jv_config_post() {
    local path="$1" payload="$2" response rc
    set +e
    response="$(printf '%s\n' "${payload}" | "${JV_CONFIG_API_CLIENT}" POST "${path}" --data)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_config_api_error "${response}" 'Configuration operation failed through the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_config_apply_payload() {
    local payload="$1" plan response answer names message
    plan="$(jv_config_post /v1/admin/configuration/plan "${payload}")" || return 1

    if ! jq -e 'type == "object" and (.ok == true) and (.changes | type == "array") and (.warnings | type == "array")' <<<"${plan}" >/dev/null 2>&1; then
        jv_config_api_error "${plan}" 'Configuration validation returned invalid data.'
        return 1
    fi

    if [[ $(jq '.changes | length' <<<"${plan}") -eq 0 ]]; then
        echo 'No configuration changes are needed.'
        return 0
    fi

    if [[ $(jq '.warnings | length' <<<"${plan}") -gt 0 ]]; then
        echo 'Notice:'
        jq -r '.warnings[] | "  " + .' <<<"${plan}"
        echo
    fi

    response="$(jv_config_post /v1/admin/configuration/apply "${payload}")" || return 1

    if [[ $(jq -r '.confirmation_required // false' <<<"${response}") == true ]]; then
        names="$(jq -r '.players // [] | join(", ")' <<<"${response}")"
        if [[ -n ${names} ]]; then
            echo "Players online: ${names}"
        else
            echo "Players online: $(jq -r '.online // 0' <<<"${response}")"
        fi
        read -r -p 'Apply the memory change and restart Minecraft anyway? [y/N]: ' answer
        if [[ ${answer,,} != y && ${answer,,} != yes ]]; then
            echo 'Cancelled. No settings were changed.'
            return 0
        fi
        payload="$(jq -c '.confirm_players = true' <<<"${payload}")"
        response="$(jv_config_post /v1/admin/configuration/apply "${payload}")" || return 1
    fi

    if ! jq -e 'type == "object" and (.ok == true)' <<<"${response}" >/dev/null 2>&1; then
        jv_config_api_error "${response}" 'Configuration update was not accepted.'
        return 1
    fi

    message="$(jq -r '.message // empty' <<<"${response}")"
    if [[ -n ${message} ]]; then
        printf '%s\n' "${message}"
    elif [[ $(jq -r '.applied // false' <<<"${response}") == true ]]; then
        echo 'Configuration updated.'
    else
        echo 'No configuration changes were applied.'
    fi
}
