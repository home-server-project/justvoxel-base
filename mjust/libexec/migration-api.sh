#!/usr/bin/bash

readonly JV_MIGRATION_API_CLIENT="${JV_MIGRATION_API_CLIENT:-/usr/libexec/justvoxel/mjust/api-client}"

jv_migration_api_error() {
    local response="${1:-}" fallback="${2:-Server migration request failed.}" message
    if jq -e 'type=="object"' <<< "${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // .message // empty' <<< "${response}")"
        if [[ -n ${message} ]]; then printf 'ERROR: %s\n' "${message}" >&2; return; fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_migration_get() {
    local path="$1" response rc
    set +e; response="$("${JV_MIGRATION_API_CLIENT}" GET "${path}")"; rc=$?; set -e
    if (( rc != 0 )); then jv_migration_api_error "${response}" "Server migration information is unavailable from the JustVoxel Management API."; return 1; fi
    printf '%s' "${response}"
}

jv_migration_post() {
    local path="$1" payload="$2" response rc
    set +e; response="$(printf '%s\n' "${payload}" | "${JV_MIGRATION_API_CLIENT}" POST "${path}" --data)"; rc=$?; set -e
    if (( rc != 0 )); then jv_migration_api_error "${response}" "Server migration request failed through the JustVoxel Management API."; return 1; fi
    printf '%s' "${response}"
}

jv_migration_current_operation() { jv_migration_get /v1/admin/migration/current-operation; }
jv_migration_operation() {
    local id="$1"; [[ ${id} =~ ^[0-9a-f-]+$ ]] || { echo "ERROR: invalid server migration operation id." >&2; return 1; }
    jv_migration_get "/v1/admin/operations/${id}"
}
jv_migration_post_review() {
    local path="$1" payload="$2" response rc stderr_file code
    stderr_file="$(mktemp)"
    set +e
    response="$(printf '%s\n' "${payload}" | "${JV_MIGRATION_API_CLIENT}" POST "${path}" --data 2>"${stderr_file}")"
    rc=$?
    set -e
    if (( rc != 0 )); then
        code="$(jq -r '.code // empty' <<< "${response}" 2>/dev/null || true)"
        case "${code}" in
            source_selection_required|multiple_roots|source_version_required) ;;
            *) cat "${stderr_file}" >&2 ;;
        esac
    fi
    rm -f -- "${stderr_file}"
    printf '%s' "${response}"
    return "${rc}"
}

jv_migration_import_discovery() { jv_migration_get /v1/admin/migration/import; }
jv_migration_storage_discovery() { jv_migration_get /v1/admin/storage; }
jv_migration_import_plan_review() { jv_migration_post_review /v1/admin/migration/import/plan "$1"; }
jv_migration_import_apply() { jv_migration_post /v1/admin/migration/import/apply "$1"; }
jv_migration_export_discovery() { jv_migration_get /v1/admin/migration/export; }
jv_migration_export_plan() { jv_migration_post /v1/admin/migration/export/plan "$1"; }
jv_migration_export_apply() { jv_migration_post /v1/admin/migration/export/apply "$1"; }
jv_migration_recovery_discovery() { jv_migration_get /v1/admin/migration/recovery; }
jv_migration_recovery_plan() { jv_migration_post /v1/admin/migration/recovery/plan "$1"; }
jv_migration_recovery_apply() { jv_migration_post /v1/admin/migration/recovery/apply "$1"; }

jv_migration_monitor_operation() {
    local id="$1" expected="$2" label="$3" response type state stage status last=""
    echo
    echo "${label} is running through the JustVoxel Management Agent."
    echo "You may leave this terminal and rerun the same mjust migration command to reconnect."
    while true; do
        response="$(jv_migration_operation "${id}")" || return 1
        type="$(jq -r '.operation.operation_type // "unknown"' <<< "${response}")"
        [[ ${type} == "${expected}" ]] || { echo "ERROR: Management Agent returned a different server migration operation." >&2; return 1; }
        state="$(jq -r '.operation.state // "unknown"' <<< "${response}")"
        stage="$(jq -r '.operation.stage // "unknown"' <<< "${response}")"
        status="$(jq -r '.operation.status // "Working"' <<< "${response}")"
        if [[ "${state}|${stage}|${status}" != "${last}" ]]; then printf '[%s / %s] %s\n' "${state}" "${stage}" "${status}"; last="${state}|${stage}|${status}"; fi
        case "${state}" in
            succeeded) echo; echo "${label} completed successfully."; return 0 ;;
            rolled_back) echo "ERROR: ${label} rolled back." >&2; return 1 ;;
            needs_attention) echo "ERROR: ${label} needs administrator attention. Preserved migration state must be reviewed." >&2; return 1 ;;
        esac
        sleep 2
    done
}
