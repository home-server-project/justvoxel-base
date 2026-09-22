#!/usr/bin/bash
set -euo pipefail

backup_delete_apply() {
    local request submitted_fingerprint submitted_confirmation current_fingerprint expected_confirmation
    local id archive metadata deleted=0

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

    while IFS= read -r id; do
        archive="$(backup_delete_archive_path "${id}" || true)"
        [[ -n ${archive} ]] || {
            backup_delete_json_error "Backup '${id}' changed before deletion. Nothing further was deleted."
            return 0
        }
        metadata="$(backup_delete_metadata_path "${archive}" || true)"
        rm -f -- "${archive}"
        if [[ -n ${metadata} ]]; then
            rm -f -- "${metadata}"
        fi
        deleted=$((deleted + 1))
    done < <(jq -r '.backups[].id' <<< "${BACKUP_DELETE_PLAN_JSON}")

    jq -n \
        --argjson proposed "${BACKUP_DELETE_PLAN_JSON}" \
        --argjson deleted "${deleted}" \
        '{ok:true,proposed:$proposed,deleted:$deleted,warnings:[],applied:true}'
}
