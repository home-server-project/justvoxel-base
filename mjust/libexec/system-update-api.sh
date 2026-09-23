#!/usr/bin/bash
set -euo pipefail

readonly JV_SYSTEM_UPDATE_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_system_update_api_error() {
    local response="${1:-}" fallback="${2:-System update request failed.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // .message // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_system_update_get() {
    local response rc
    set +e
    response="$("${JV_SYSTEM_UPDATE_API_CLIENT}" GET /v1/admin/system/updates)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_system_update_api_error "${response}" 'System update status is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_system_update_apply() {
    local response rc
    set +e
    response="$("${JV_SYSTEM_UPDATE_API_CLIENT}" POST /v1/admin/system/updates)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_system_update_api_error "${response}" 'System update request failed through the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}
