#!/usr/bin/bash
# Route future backups into a single child of the configured BACKUP_PATH.
# Only reads the local identity record. Never puts that ID into archives.
jv_backup_prepare_instance_directory() {
    local root="$1" identity_root=/var/lib/justvoxel/instances
    local record=/var/lib/justvoxel/instances/minecraft.json
    local id folder
    if [[ ! -e ${record} && ! -L ${record} ]]; then return 2; fi
    [[ -d ${identity_root} && ! -L ${identity_root} ]] || return 1
    [[ -f ${record} && ! -L ${record} ]] || return 1
    [[ $(stat -c '%u:%g:%a' -- "${identity_root}") == '0:0:700' ]] || return 1
    [[ $(stat -c '%u:%g:%a' -- "${record}") == '0:0:600' ]] || return 1
    [[ $(stat -c '%s' -- "${record}") -le 1024 ]] || return 1
    id="$(jq -ers '
        if length == 1 and (.[0] | type == "object" and (keys == ["id"]) and
          (.id | type == "string" and test("^jv-[a-z0-9]{6,12}$")))
        then .[0].id else empty end
    ' "${record}")" || return 1
    [[ -n ${id} ]] || return 1
    folder="${root}/${id}"
    [[ ! -L ${folder} && ( ! -e ${folder} || -d ${folder} ) ]] || return 1
    mkdir -p -- "${folder}" || return 1
    [[ -d ${folder} && ! -L ${folder} && -w ${folder} ]] || return 1
    printf '%s\n' "${folder}"
}
