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
    jv_backup_valid_archive_ref "$1"
}

# Current instance archives are owned only with matching bounded metadata.
# Legacy root/folder archives retain their existing contract.
backup_delete_instance_metadata() {
    python3 - "$1" "$2" <<'PYMETA'
import datetime, json, os, re, stat, sys
root, ref = sys.argv[1:]
identity, filename = ref.split('/')
fd = None
try:
    root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fd = os.open(identity, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=root_fd)
    finally:
        os.close(root_fd)
    def read_at(directory, name, limit):
        f = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
        with os.fdopen(f, 'rb') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > limit:
                raise ValueError('metadata file')
            return json.loads(stream.read(limit + 1))
    m = read_at(fd, filename + '.meta.json', 16384)
    software = m.get('software')
    keys = {'schema','id','name','software','version','created','archive','size','configuration_sha256','sha256'}
    if software in ('forge','neoforge','fabric','quilt'):
        keys.add('loader_version')
        if not isinstance(m.get('loader_version'), str) or len(m['loader_version']) > 64 or not re.fullmatch(r'[0-9]+(?:\.[0-9]+){1,3}(?:-[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*)?', m['loader_version']):
            raise ValueError('loader')
    if set(m) != keys or type(m['schema']) != int or m['schema'] != 1 or m['id'] != identity or m['archive'] != filename:
        raise ValueError('identity')
    if software not in ('paper','purpur','pufferfish','leaf','vanilla','spigot','forge','neoforge','fabric','quilt','bedrock') or not isinstance(m['version'], str) or len(m['version']) > 64:
        raise ValueError('software')
    if not isinstance(m['name'], str) or len(m['name']) > 64 or any(ord(c) < 32 for c in m['name']):
        raise ValueError('name')
    match = re.fullmatch(r"(.{1,64})-backup-([a-z][a-z0-9-]{0,70})-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{4}(-[1-9][0-9]{0,2})?\.tar\.gz", filename)
    if not match or match[2] != software or (match[1] != 'instance' and match[1] != m['name']):
        raise ValueError('archive binding')
    if not isinstance(m['created'], str) or len(m['created']) != 20:
        raise ValueError('created')
    datetime.datetime.strptime(m['created'], '%Y-%m-%dT%H:%M:%SZ')
    for key in ('sha256', 'configuration_sha256'):
        if not isinstance(m[key], str) or not re.fullmatch(r'[a-f0-9]{64}', m[key]):
            raise ValueError('hash')
    info = os.stat(filename, dir_fd=fd, follow_symlinks=False)
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or type(m['size']) != int or m['size'] <= 0 or info.st_size != m['size']:
        raise ValueError('archive')
    # Preserve a Restore selection after process death until safe recovery.
    config = '/etc/justvoxel/instances/' + identity
    if os.path.lexists(config + '/restore-journal.json'):
        cfg = os.open(config, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            journal = read_at(cfg, 'restore-journal.json', 65536)
        finally:
            os.close(cfg)
        if journal['schema'] != 1 or journal['id'] != identity:
            raise ValueError('journal')
        if journal['archive'] == filename and journal['stage'] not in ('completed', 'rolled_back'):
            raise ValueError('restore selection')
except Exception:
    sys.exit(1)
finally:
    if fd is not None:
        os.close(fd)
PYMETA
}

backup_delete_archive_path() {
    local id="$1" root archive real
    backup_delete_validate_id "${id}" || return 1
    root="$(readlink -f -- "${minecraft_backup_path}" 2>/dev/null || true)"
    archive="${minecraft_backup_path}/${id}"
    [[ -n ${root} && -f ${archive} && ! -L ${archive} ]] || return 1
    real="$(readlink -f -- "${archive}" 2>/dev/null || true)"
    [[ -n ${real} && ${real} == "${root}/${id}" ]] || return 1
    if [[ ${id} == */* ]] && jv_instance_backup_valid_archive_id "${id#*/}"; then
        backup_delete_instance_metadata "${root}" "${id}" || return 1
    fi
    printf '%s\n' "${real}"
}

backup_delete_metadata_path() {
    local archive="$1" metadata root real
    metadata="${archive}.meta.json"
    [[ -e ${metadata} || -L ${metadata} ]] || return 0
    [[ -f ${metadata} && ! -L ${metadata} ]] || return 1
    root="$(readlink -f -- "${minecraft_backup_path}" 2>/dev/null || true)"
    real="$(readlink -f -- "${metadata}" 2>/dev/null || true)"
    [[ -n ${root} && -n ${real} && ${real} == "${archive}.meta.json" ]] || return 1
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
            fingerprint_input+="$(basename -- "${metadata}")|$(stat -Lc '%s|%Y|%Z|%i' -- "${metadata}")|$(sha256sum -- "${metadata}" | cut -d' ' -f1)"$'\n'
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
