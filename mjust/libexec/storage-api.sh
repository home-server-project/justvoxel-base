#!/usr/bin/bash
set -euo pipefail

readonly JV_STORAGE_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_storage_api_error() {
    local response="${1:-}" fallback="${2:-Storage information is unavailable.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // .message // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_storage_get() {
    local path="$1" response rc
    set +e
    response="$("${JV_STORAGE_API_CLIENT}" GET "${path}")"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_storage_api_error "${response}" 'Storage information is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_storage_discovery() {
    local response
    response="$(jv_storage_get /v1/admin/storage)" || return 1
    if ! jq -e '
        type == "object" and
        (.system_disks | type == "array") and
        (.devices | type == "array") and
        all(.system_disks[]; type == "string") and
        all(.devices[];
            type == "object" and
            (.path | type == "string") and
            (.parent | type == "string") and
            (.type | type == "string") and
            (.size_bytes | type == "number") and
            (.filesystem | type == "string") and
            (.label | type == "string") and
            (.uuid | type == "string") and
            (.mountpoints | type == "array") and
            (.model | type == "string") and
            (.transport | type == "string") and
            (.read_only | type == "boolean") and
            (.system | type == "boolean")
        )
    ' <<<"${response}" >/dev/null 2>&1; then
        echo 'ERROR: JustVoxel Management API returned invalid storage discovery data.' >&2
        return 1
    fi
    printf '%s' "${response}"
}

jv_storage_status() {
    local response
    response="$(jv_storage_get /v1/status)" || return 1
    if ! jq -e '
        type == "object" and
        (.system | type == "object") and
        (.system.variant | type == "string")
    ' <<<"${response}" >/dev/null 2>&1; then
        echo 'ERROR: JustVoxel Management API returned invalid system status data.' >&2
        return 1
    fi
    printf '%s' "${response}"
}
