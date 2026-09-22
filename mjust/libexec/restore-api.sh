#!/usr/bin/bash

readonly JV_RESTORE_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_restore_api_error() {
    local response="${1:-}" fallback="${2:-Restore request failed.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_restore_get() {
    local path="$1" response rc
    set +e
    response="$("${JV_RESTORE_API_CLIENT}" GET "${path}")"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_restore_api_error "${response}" 'Restore information is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_restore_post() {
    local path="$1" payload="$2" response rc
    set +e
    response="$(printf '%s\n' "${payload}" | "${JV_RESTORE_API_CLIENT}" POST "${path}" --data)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_restore_api_error "${response}" 'Restore request failed through the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_restore_backups() {
    jv_restore_get /v1/admin/restore/backups
}

jv_restore_current_operation() {
    jv_restore_get /v1/admin/restore/current-operation
}

jv_restore_plan() {
    jv_restore_post /v1/admin/restore/plan "$1"
}

jv_restore_apply() {
    jv_restore_post /v1/admin/restore/apply "$1"
}

jv_restore_operation() {
    local id="$1"
    [[ ${id} =~ ^[0-9a-f-]+$ ]] || { echo 'ERROR: invalid Restore operation id.' >&2; return 1; }
    jv_restore_get "/v1/admin/operations/${id}"
}

jv_restore_monitor_operation() {
    local id="$1" response state stage status operation_type last=''
    echo
    echo 'Restore is running through the JustVoxel Management Agent.'
    echo 'You may leave this terminal; rerun mjust restore to reconnect to the operation.'
    while true; do
        response="$(jv_restore_operation "${id}")" || return 1
        operation_type="$(jq -r '.operation.operation_type // "unknown"' <<<"${response}")"
        if [[ ${operation_type} != restore ]]; then
            echo 'ERROR: Management Agent returned a non-Restore operation.' >&2
            return 1
        fi
        state="$(jq -r '.operation.state // "unknown"' <<<"${response}")"
        stage="$(jq -r '.operation.stage // "unknown"' <<<"${response}")"
        status="$(jq -r '.operation.status // "Working"' <<<"${response}")"
        if [[ "${state}|${stage}|${status}" != "${last}" ]]; then
            printf '[%s / %s] %s\n' "${state}" "${stage}" "${status}"
            last="${state}|${stage}|${status}"
        fi
        case "${state}" in
            succeeded)
                echo
                echo 'Minecraft Restore completed successfully.'
                return 0
                ;;
            rolled_back)
                echo
                echo 'ERROR: Restore did not complete; the Agent restored and validated the previous Minecraft data.' >&2
                return 1
                ;;
            needs_attention)
                echo
                echo 'ERROR: Restore needs administrator attention. Preserved recovery state must be reviewed before another Restore.' >&2
                return 1
                ;;
        esac
        sleep 2
    done
}
