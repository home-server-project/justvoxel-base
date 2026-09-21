#!/usr/bin/bash

readonly JV_DATA_MIGRATION_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_data_migration_api_error() {
    local response="${1:-}" fallback="${2:-Minecraft data migration request failed.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // .message // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_data_migration_get() {
    local path="$1" response rc
    set +e
    response="$("${JV_DATA_MIGRATION_API_CLIENT}" GET "${path}")"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_data_migration_api_error "${response}" 'Minecraft data migration information is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_data_migration_post() {
    local path="$1" payload="$2" response rc
    set +e
    response="$(printf '%s\n' "${payload}" | "${JV_DATA_MIGRATION_API_CLIENT}" POST "${path}" --data)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_data_migration_api_error "${response}" 'Minecraft data migration request failed through the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_data_migration_discovery() {
    jv_data_migration_get /v1/admin/data-migration
}

jv_data_migration_current_operation() {
    jv_data_migration_get /v1/admin/data-migration/current-operation
}

jv_data_migration_plan() {
    jv_data_migration_post /v1/admin/data-migration/plan "$1"
}

jv_data_migration_apply() {
    jv_data_migration_post /v1/admin/data-migration/apply "$1"
}

jv_data_migration_operation() {
    local id="$1"
    [[ ${id} =~ ^[0-9a-f-]+$ ]] || { echo 'ERROR: invalid Minecraft data migration operation id.' >&2; return 1; }
    jv_data_migration_get "/v1/admin/operations/${id}"
}

jv_data_migration_monitor_operation() {
    local id="$1" response state stage status operation_type last=''
    echo
    echo 'Minecraft data migration is running through the JustVoxel Management Agent.'
    echo 'You may leave this terminal; rerun mjust storage-migrate to reconnect to the operation.'

    while true; do
        response="$(jv_data_migration_operation "${id}")" || return 1
        operation_type="$(jq -r '.operation.operation_type // "unknown"' <<<"${response}")"
        if [[ ${operation_type} != data_migration ]]; then
            echo 'ERROR: Management Agent returned a non-migration operation.' >&2
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
                echo 'Minecraft data migration completed successfully.'
                echo 'The old Minecraft data was intentionally retained for administrator verification.'
                return 0
                ;;
            rolled_back)
                echo
                echo 'ERROR: Minecraft data migration did not complete. The original Minecraft configuration/runtime remains active.' >&2
                echo 'Reviewed target-storage preparation may remain and can be inspected safely.' >&2
                return 1
                ;;
            needs_attention)
                echo
                echo 'ERROR: Minecraft data migration needs administrator attention. Preserved recovery state must be reviewed before another migration.' >&2
                return 1
                ;;
        esac
        sleep 2
    done
}
