#!/usr/bin/bash
set -euo pipefail

backup_delete_apply() {
    local request submitted_fingerprint submitted_confirmation current_fingerprint expected_confirmation
    local archive metadata index deleted=0

    request="$(cat)"

    exec 9>"${JV_MAINTENANCE_LOCK}"
    if ! flock -n 9; then
        backup_delete_json_error 'Another JustVoxel maintenance operation is running. Nothing was deleted.'
        return 0
    fi

    backup_delete_collect_plan <<< "${request}" || return 0

    submitted_fingerprint="$(jq -r '.fingerprint // ""' <<< "${request}")"
    submitted_confirmation="$(jq -r '.confirmation // ""' <<< "${request}")"
    current_fingerprint="$(jq -r '.fingerprint' <<< "${BACKUP_DELETE_PLAN_JSON}")"
    expected_confirmation="$(jq -r '.confirmation' <<< "${BACKUP_DELETE_PLAN_JSON}")"

    if [[ -z ${submitted_fingerprint} || ${submitted_fingerprint} != "${current_fingerprint}" ]]; then
        backup_delete_json_error 'The selected backups changed after Review. Nothing was deleted. Review the selection again.'
        return 0
    fi
    if [[ ${submitted_confirmation} != "${expected_confirmation}" ]]; then
        backup_delete_json_error "Confirmation does not match. Type exactly: ${expected_confirmation}"
        return 0
    fi

    # Validate every selected directory before the first removal. These exact
    # paths were independently resolved and fingerprinted under the lock above.
    for archive in "${BACKUP_DELETE_ARCHIVES[@]}"; do
        [[ -w ${archive%/*} && -x ${archive%/*} ]] || {
            backup_delete_json_error 'A selected backup directory is not writable. Nothing was deleted.'
            return 0
        }
    done
    for index in "${!BACKUP_DELETE_ARCHIVES[@]}"; do
        archive="${BACKUP_DELETE_ARCHIVES[index]}"
        metadata="${BACKUP_DELETE_METADATA[index]}"
        rm -f -- "${archive}"
        rm -f -- "${metadata}"
        deleted=$((deleted + 1))
    done

    jq -n \
        --argjson proposed "${BACKUP_DELETE_PLAN_JSON}" \
        --argjson deleted "${deleted}" \
        '{ok:true,proposed:$proposed,deleted:$deleted,warnings:[],applied:true}'
}
