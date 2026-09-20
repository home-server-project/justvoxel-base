#!/usr/bin/bash

readonly JV_BACKUP_STORAGE_API_CLIENT=/usr/libexec/justvoxel/mjust/api-client

jv_backup_storage_error() {
    local response="${1:-}" fallback="${2:-Backup storage request failed.}" message
    if jq -e 'type == "object"' <<<"${response}" >/dev/null 2>&1; then
        message="$(jq -r '.error // empty' <<<"${response}")"
        if [[ -n ${message} ]]; then
            printf 'ERROR: %s\n' "${message}" >&2
            return
        fi
    fi
    printf 'ERROR: %s\n' "${fallback}" >&2
}

jv_backup_storage_get() {
    local path="$1" response rc
    set +e
    response="$("${JV_BACKUP_STORAGE_API_CLIENT}" GET "${path}")"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_backup_storage_error "${response}" 'Backup storage information is unavailable from the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_backup_storage_post() {
    local path="$1" payload="$2" response rc
    set +e
    response="$(printf '%s\n' "${payload}" | "${JV_BACKUP_STORAGE_API_CLIENT}" POST "${path}" --data)"
    rc=$?
    set -e
    if (( rc != 0 )); then
        jv_backup_storage_error "${response}" 'Backup storage operation failed through the JustVoxel Management API.'
        return 1
    fi
    printf '%s' "${response}"
}

jv_backup_storage_status() {
    jv_backup_storage_get /v1/admin/backup-storage
}

jv_backup_storage_discover() {
    jv_backup_storage_get /v1/admin/storage
}

jv_backup_storage_provision_discover() {
    jv_backup_storage_get /v1/admin/storage-provision
}

jv_backup_storage_show_warnings() {
    local response="$1"
    if [[ $(jq '.warnings // [] | length' <<<"${response}") -gt 0 ]]; then
        echo 'Warning:'
        jq -r '.warnings[] | "  " + .' <<<"${response}"
        echo
    fi
}

jv_backup_storage_apply_target() {
    local payload="$1" plan answer response path
    plan="$(jv_backup_storage_post /v1/admin/backup-storage/plan "${payload}")" || return 1
    if ! jq -e 'type == "object" and (.ok == true) and (.changed | type == "boolean")' <<<"${plan}" >/dev/null 2>&1; then
        jv_backup_storage_error "${plan}" 'Backup storage validation returned invalid data.'
        return 1
    fi

    if [[ $(jq -r '.changed' <<<"${plan}") != true ]]; then
        echo 'This backup storage location is already configured.'
        return 0
    fi

    jv_backup_storage_show_warnings "${plan}"
    path="$(jq -r '.proposed.path // "Unknown"' <<<"${plan}")"
    echo "Proposed backup location: ${path}"
    read -r -p 'Apply this backup storage change? [y/N]: ' answer
    if [[ ${answer,,} != y && ${answer,,} != yes ]]; then
        echo 'Cancelled. No settings were changed.'
        return 0
    fi

    response="$(jv_backup_storage_post /v1/admin/backup-storage/apply "${payload}")" || return 1
    if ! jq -e 'type == "object" and (.ok == true) and (.applied == true)' <<<"${response}" >/dev/null 2>&1; then
        jv_backup_storage_error "${response}" 'Backup storage change was not applied.'
        return 1
    fi
    echo "Backup storage updated: $(jq -r '.proposed.path' <<<"${response}")"
}

jv_backup_storage_apply_provision() {
    local payload="$1" plan confirmation fingerprint typed response
    plan="$(jv_backup_storage_post /v1/admin/storage-provision/plan "${payload}")" || return 1
    if ! jq -e 'type == "object" and (.ok == true) and (.proposed.confirmation | type == "string") and (.proposed.fingerprint | type == "string")' <<<"${plan}" >/dev/null 2>&1; then
        jv_backup_storage_error "${plan}" 'Storage provisioning validation returned invalid data.'
        return 1
    fi

    jv_backup_storage_show_warnings "${plan}"
    echo "Device: $(jq -r '.proposed.device' <<<"${plan}")"
    echo "Backup directory: $(jq -r '.proposed.path' <<<"${plan}")"
    confirmation="$(jq -r '.proposed.confirmation' <<<"${plan}")"
    fingerprint="$(jq -r '.proposed.fingerprint' <<<"${plan}")"
    echo "Type exactly: ${confirmation}"
    read -r typed
    if [[ ${typed} != "${confirmation}" ]]; then
        echo 'Cancelled. Confirmation did not match.'
        return 0
    fi

    payload="$(jq -c --arg confirmation "${confirmation}" --arg fingerprint "${fingerprint}" '.confirmation=$confirmation | .fingerprint=$fingerprint' <<<"${payload}")"
    response="$(jv_backup_storage_post /v1/admin/storage-provision/apply "${payload}")" || return 1
    if ! jq -e 'type == "object" and (.ok == true) and (.applied == true)' <<<"${response}" >/dev/null 2>&1; then
        jv_backup_storage_error "${response}" 'Storage provisioning was not applied.'
        return 1
    fi
    echo "Backup storage updated: $(jq -r '.proposed.path' <<<"${response}")"
}
