#!/usr/bin/bash
set -euo pipefail

source /usr/libexec/justvoxel/mjust/common.sh
source /usr/libexec/justvoxel/mjust/backup-common.sh

require_root
require_config
jv_backup_load_config
jv_backup_require_read_target

backup_delete_json_error() {
    jq -n --arg error "$1" '{ok:false,error:$error,warnings:[],applied:false}'
}

backup_delete_validate_id() {
    [[ $1 =~ ^minecraft-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}\.tar\.gz$ ]]
}

backup_delete_archive_path() {
    local id="$1" root archive real
    backup_delete_validate_id "${id}" || return 1
    root="$(readlink -f -- "${minecraft_backup_path}" 2>/dev/null || true)"
    archive="${minecraft_backup_path}/${id}"
    [[ -n ${root} && -f ${archive} && ! -L ${archive} ]] || return 1
    real="$(readlink -f -- "${archive}" 2>/dev/null || true)"
    [[ -n ${real} && ${real} == "${root}/${id}" ]] || return 1
    printf '%s\n' "${real}"
}

backup_delete_metadata_path() {
    local archive="$1" metadata root real
    metadata="${archive}.meta.json"
    [[ -e ${metadata} || -L ${metadata} ]] || return 0
    [[ -f ${metadata} && ! -L ${metadata} ]] || return 1
    root="$(readlink -f -- "${minecraft_backup_path}" 2>/dev/null || true)"
    real="$(readlink -f -- "${metadata}" 2>/dev/null || true)"
    [[ -n ${root} && -n ${real} && ${real} == "${root}/$(basename -- "${metadata}")" ]] || return 1
    printf '%s\n' "${real}"
}

backup_delete_collect_plan() {
    local request ids_json id archive metadata size total=0 count=0
    local records='[]' fingerprint_input='' confirmation

    request="$(cat)"
    BACKUP_DELETE_REQUEST_JSON="${request}"
    jq -e 'type == "object" and (.backup_ids | type == "array") and (.backup_ids | length >= 1 and length <= 100) and all(.backup_ids[]; type == "string")' >/dev/null 2>&1 <<< "${request}" || {
        backup_delete_json_error 'Choose between 1 and 100 backups to delete.'
        return 1
    }

    ids_json="$(jq -c '.backup_ids' <<< "${request}")"
    if [[ "$(jq -r 'unique | length' <<< "${ids_json}")" != "$(jq -r 'length' <<< "${ids_json}")" ]]; then
        backup_delete_json_error 'The backup selection contains duplicates.'
        return 1
    fi

    while IFS= read -r id; do
        archive="$(backup_delete_archive_path "${id}" || true)"
        [[ -n ${archive} ]] || {
            backup_delete_json_error "Backup '${id}' is unavailable or unsafe to delete."
            return 1
        }
        metadata="$(backup_delete_metadata_path "${archive}" || true)"
        if [[ ( -e ${archive}.meta.json || -L ${archive}.meta.json ) && -z ${metadata} ]]; then
            backup_delete_json_error "Metadata for backup '${id}' is unsafe to delete."
            return 1
        fi
        size="$(stat -Lc '%s' -- "${archive}" 2>/dev/null || true)"
        [[ ${size} =~ ^[0-9]+$ ]] || {
            backup_delete_json_error "Backup '${id}' could not be inspected safely."
            return 1
        }
        total=$((total + size))
        count=$((count + 1))
        fingerprint_input+="${id}|$(stat -Lc '%s|%Y|%Z|%i' -- "${archive}")"$'\n'
        if [[ -n ${metadata} ]]; then
            fingerprint_input+="$(basename -- "${metadata}")|$(stat -Lc '%s|%Y|%Z|%i' -- "${metadata}")"$'\n'
        fi
        records="$(jq -cn --argjson records "${records}" --arg id "${id}" --argjson size_bytes "${size}" '$records + [{id:$id,size_bytes:$size_bytes}]')"
    done < <(jq -r '.[]' <<< "${ids_json}")

    if (( count == 1 )); then
        confirmation="DELETE $(jq -r '.[0].id' <<< "${records}")"
    else
        confirmation="DELETE ${count} BACKUPS"
    fi

    BACKUP_DELETE_PLAN_JSON="$(jq -n \
        --argjson backups "${records}" \
        --argjson count "${count}" \
        --argjson total_size_bytes "${total}" \
        --arg confirmation "${confirmation}" \
        --arg fingerprint "sha256:$(printf '%s' "${fingerprint_input}" | sha256sum | awk '{print $1}')" \
        '{backups:$backups,count:$count,total_size_bytes:$total_size_bytes,confirmation:$confirmation,fingerprint:$fingerprint}')"
}
