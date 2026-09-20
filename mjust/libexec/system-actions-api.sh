#!/usr/bin/bash
set -euo pipefail

readonly JV_SYSTEM_ACTIONS_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_system_actions_api_error() {
    local response="${1:-}" fallback="${2:-System action request failed.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // .message // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_system_actions_get() {
    local response rc
    set +e
    response="$("${JV_SYSTEM_ACTIONS_API_CLIENT}" GET /v1/admin/system/actions)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_system_actions_api_error "${response}" 'System action status is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_system_actions_apply() {
    local action="$1" players_confirmed="${2:-false}" payload response rc
    payload="$(jq -cn --argjson players_confirmed "${players_confirmed}"         '{action_confirmed:true,confirm_players:$players_confirmed}')"
    set +e
    response="$(printf '%s\n' "${payload}" | "${JV_SYSTEM_ACTIONS_API_CLIENT}" POST "/v1/admin/system/${action}" --data)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_system_actions_api_error "${response}" 'System action request failed through the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_system_actions_handle_players() {
    local action="$1" response="$2" label="$3" names online answer
    if [[ $(jq -r '.confirmation_required // false' <<<"${response}") != true ]]; then
        printf '%s' "${response}"
        return 0
    fi

    names="$(jq -r '.players // [] | join(", ")' <<<"${response}")"
    if [[ -n ${names} ]]; then
        echo "Players online: ${names}" >&2
    else
        online="$(jq -r '.online // 0' <<<"${response}")"
        echo "Players online: ${online}" >&2
    fi
    read -r -p "Players appear to be online. ${label} anyway? [y/N]: " answer
    if [[ ${answer,,} != y && ${answer,,} != yes ]]; then
        echo 'Cancelled.' >&2
        return 20
    fi
    jv_system_actions_apply "${action}" true
}
