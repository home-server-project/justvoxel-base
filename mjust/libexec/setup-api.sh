#!/usr/bin/bash

readonly JV_SETUP_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_setup_api_error() {
    local response="${1:-}" fallback="${2:-Setup request failed.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_setup_get() {
    local path="$1" response rc
    set +e
    response="$("${JV_SETUP_API_CLIENT}" GET "${path}")"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_setup_api_error "${response}" 'Setup information is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_setup_post() {
    local path="$1" payload="$2" response rc
    set +e
    response="$(printf '%s\n' "${payload}" | "${JV_SETUP_API_CLIENT}" POST "${path}" --data)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_setup_api_error "${response}" 'Setup operation failed through the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_setup_defaults() {
    jv_setup_get /v1/admin/setup-defaults
}

jv_setup_storage() {
    jv_setup_get /v1/admin/storage
}

jv_setup_current_operation() {
    jv_setup_get /v1/admin/setup/current-operation
}

jv_setup_plan() {
    jv_setup_post /v1/admin/setup/plan "$1"
}

jv_setup_apply() {
    jv_setup_post /v1/admin/setup/apply "$1"
}

jv_setup_operation() {
    local id="$1"
    [[ ${id} =~ ^[0-9a-f-]+$ ]] || { echo 'ERROR: invalid setup operation id.' >&2; return 1; }
    jv_setup_get "/v1/admin/operations/${id}"
}

jv_setup_diagnostic_start() {
    local response rc
    set +e
    response="$(printf '%s\n' '{"interface":"mjust"}' | "${JV_SETUP_API_CLIENT}" POST /v1/admin/setup/diagnostics/session --data 2>/dev/null)"
    rc=$?
    set -e
    (( rc == 0 )) || return 1
    jq -er '.session_id | select(type == "string" and length > 0)' <<<"${response}" 2>/dev/null
}

jv_setup_diagnostic_event() {
    local id="$1" event="$2" values="${3:-{}}" payload
    [[ ${id} =~ ^[0-9a-f-]+$ ]] || return 0
    payload="$(jq -cn --arg event "${event}" --argjson values "${values}" '{event:$event,values:$values}')" || return 0
    printf '%s\n' "${payload}" | "${JV_SETUP_API_CLIENT}" POST "/v1/admin/setup/diagnostics/${id}/event" --data >/dev/null 2>&1 || true
}

jv_setup_diagnostic_path() {
    local id="$1"
    printf '/var/lib/justvoxel/management/setup-logs/%s.log\n' "${id}"
}

jv_setup_monitor_operation() {
    local id="$1" response state stage status last=''
    echo
    echo 'Setup is running through the JustVoxel Management Agent.'
    echo 'You may leave this terminal; rerun mjust setup to reconnect to the operation.'
    while true; do
        response="$(jv_setup_operation "${id}")" || return 1
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
                echo 'JustVoxel setup completed.'
                echo "Setup diagnostic log: $(jv_setup_diagnostic_path "${id}")"
                return 0
                ;;
            rolled_back)
                echo
                echo 'ERROR: setup failed and the Agent rolled back the changes.' >&2
                echo "Setup diagnostic log: $(jv_setup_diagnostic_path "${id}")" >&2
                return 1
                ;;
            needs_attention)
                echo
                echo 'ERROR: setup needs administrator attention. Review mjust status --details.' >&2
                echo "Setup diagnostic log: $(jv_setup_diagnostic_path "${id}")" >&2
                return 1
                ;;
        esac
        sleep 2
    done
}
