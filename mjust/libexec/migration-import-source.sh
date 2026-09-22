#!/usr/bin/bash
source /usr/libexec/justvoxel/mjust/backup-common.sh
source /usr/libexec/justvoxel/mjust/storage-common.sh
source /usr/libexec/justvoxel/mjust/migration-transport.sh

jv_migration_import_source_cleanup() {
    jv_migration_transport_cleanup >/dev/null 2>&1 || true
}

jv_migration_import_source_entries_json() {
    local base="$1" path rel kind canonical entries='[]' count=0
    canonical="$(realpath -m -- "${base}")"
    while IFS= read -r path; do
        [[ -n ${path} ]] || continue
        case "${path}/" in
            "${canonical}/"*) ;;
            *) continue ;;
        esac
        if [[ ${path} == "${canonical}" ]]; then rel='.'; else rel="${path#${canonical}/}"; fi
        if [[ -d ${path} ]]; then kind=directory; else kind=archive; fi
        entries="$(jq -cn --argjson a "${entries}" --arg path "${rel}" --arg kind "${kind}" '$a + [{path:$path,kind:$kind}]')"
        count=$((count + 1))
        (( count >= 100 )) && break
    done < <(
        find "${canonical}" -maxdepth 5             \( -type f \( -iname '*.tar.gz' -o -iname '*.tgz' -o -iname '*.tar' -o -iname '*.zip' \)                -o -type f -name 'server.properties' \) -print 2>/dev/null             | while IFS= read -r p; do
                if [[ $(basename -- "${p}") == server.properties ]]; then dirname -- "${p}"; else printf '%s\n' "${p}"; fi
              done | awk '!seen[$0]++' | sort
    )
    printf '%s' "${entries}"
}

jv_migration_import_source_resolve_under() {
    local base="$1" relative="$2" canonical candidate
    canonical="$(realpath -m -- "${base}")"
    [[ -n ${relative} && ${relative} != /* && ${relative} != *$'\n'* && ${relative} != *$'\r'* ]] || return 1
    candidate="$(realpath -m -- "${canonical}/${relative}")"
    case "${candidate}/" in
        "${canonical}/"*) ;;
        *) return 1 ;;
    esac
    [[ -e ${candidate} ]] || return 1
    printf '%s' "${candidate}"
}

jv_migration_import_transport_identity() {
    local source_json="$1" kind device source username domain configured_id
    kind="$(jq -r '.kind // "local"' <<< "${source_json}")"
    case "${kind}" in
        local) printf 'local' ;;
        backup)
            jv_backup_load_config >/dev/null 2>&1 || return 1
            configured_id="$(printf '%s\n%s\n%s\n%s\n' "${BACKUP_TYPE:-}" "${BACKUP_MOUNT_POINT:-}" "${BACKUP_EXPECTED_UUID:-}" "${BACKUP_EXPECTED_SOURCE:-}" | sha256sum | awk '{print $1}')"
            printf 'backup:%s' "${configured_id}"
            ;;
        device)
            device="$(readlink -f -- "$(jq -r '.device // ""' <<< "${source_json}")" 2>/dev/null || true)"
            [[ -n ${device} ]] || return 1
            {
                printf '%s\n' "${device}"
                blkid "${device}" 2>/dev/null || true
                lsblk -b -P -o PATH,PKNAME,TYPE,SIZE,FSTYPE,UUID,PARTUUID,MODEL,SERIAL,WWN,TRAN,HOTPLUG,RM "${device}" 2>/dev/null || true
            } | sha256sum | awk '{print "device:"$1}'
            ;;
        nfs)
            source="$(jq -r '.source // ""' <<< "${source_json}")"
            printf 'nfs:%s' "$(printf '%s' "${source}" | sha256sum | awk '{print $1}')"
            ;;
        smb)
            source="$(jq -r '.source // ""' <<< "${source_json}")"
            username="$(jq -r '.username // ""' <<< "${source_json}")"
            domain="$(jq -r '.domain // ""' <<< "${source_json}")"
            printf 'smb:%s' "$(printf '%s\n%s\n%s\n' "${source}" "${username}" "${domain}" | sha256sum | awk '{print $1}')"
            ;;
        *) return 1 ;;
    esac
}

jv_migration_import_source_content_identity() {
    local kind="$1" path="$2"
    if [[ ${kind} == local ]]; then
        jv_migration_source_identity "${path}"
    elif [[ -f ${path} ]]; then
        sha256sum -- "${path}" | awk '{print "filesha256:"$1}'
    elif [[ -d ${path} ]]; then
        stat -Lc '%s:%Y' -- "${path}" | sha256sum | awk '{print "dirmeta256:"$1}'
    else
        return 1
    fi
}

jv_migration_import_source_prepare() {
    local source_json="$1" kind relative device removable source username password domain base transport_identity content_identity
    kind="$(jq -r '.kind // "local"' <<< "${source_json}")"
    relative="$(jq -r '.path // ""' <<< "${source_json}")"
    JV_MIGRATION_IMPORT_SOURCE_KIND="${kind}"
    JV_MIGRATION_IMPORT_SOURCE_PATH=''
    JV_MIGRATION_IMPORT_SOURCE_BASE=''
    JV_MIGRATION_IMPORT_SOURCE_IDENTITY=''

    case "${kind}" in
        local)
            [[ ${relative} == /* && ${relative} != *$'\n'* && ${relative} != *$'\r'* ]] || return 1
            JV_MIGRATION_IMPORT_SOURCE_PATH="$(realpath -m -- "${relative}")"
            [[ -e ${JV_MIGRATION_IMPORT_SOURCE_PATH} ]] || return 1
            ;;
        backup)
            jv_backup_load_config >/dev/null 2>&1 || return 1
            jv_backup_require_read_target >/dev/null 2>&1 || return 1
            JV_MIGRATION_IMPORT_SOURCE_BASE="$(realpath -m -- "${minecraft_backup_path}")"
            [[ -n ${relative} ]] || return 3
            JV_MIGRATION_IMPORT_SOURCE_PATH="$(jv_migration_import_source_resolve_under "${JV_MIGRATION_IMPORT_SOURCE_BASE}" "${relative}")" || return 1
            ;;
        device)
            device="$(jq -r '.device // ""' <<< "${source_json}")"
            removable="$(jq -r '.removable // false' <<< "${source_json}")"
            if [[ ${removable} == true ]]; then removable=yes; else removable=no; fi
            jv_migration_mount_device "${device}" import "${removable}" >/dev/null || return 1
            JV_MIGRATION_IMPORT_SOURCE_BASE="$(realpath -m -- "${JV_MIGRATION_MEDIA_MOUNT}")"
            [[ -n ${relative} ]] || return 3
            JV_MIGRATION_IMPORT_SOURCE_PATH="$(jv_migration_import_source_resolve_under "${JV_MIGRATION_IMPORT_SOURCE_BASE}" "${relative}")" || return 1
            ;;
        nfs)
            source="$(jq -r '.source // ""' <<< "${source_json}")"
            jv_migration_mount_nfs_noninteractive "${source}" import >/dev/null || return 1
            JV_MIGRATION_IMPORT_SOURCE_BASE="$(realpath -m -- "${JV_MIGRATION_MEDIA_MOUNT}")"
            [[ -n ${relative} ]] || return 3
            JV_MIGRATION_IMPORT_SOURCE_PATH="$(jv_migration_import_source_resolve_under "${JV_MIGRATION_IMPORT_SOURCE_BASE}" "${relative}")" || return 1
            ;;
        smb)
            source="$(jq -r '.source // ""' <<< "${source_json}")"
            username="$(jq -r '.username // ""' <<< "${source_json}")"
            password="$(jq -r '.smb_password // ""' <<< "${source_json}")"
            domain="$(jq -r '.domain // ""' <<< "${source_json}")"
            jv_migration_mount_smb_noninteractive "${source}" "${username}" "${password}" "${domain}" import >/dev/null || return 1
            unset password
            JV_MIGRATION_IMPORT_SOURCE_BASE="$(realpath -m -- "${JV_MIGRATION_MEDIA_MOUNT}")"
            [[ -n ${relative} ]] || return 3
            JV_MIGRATION_IMPORT_SOURCE_PATH="$(jv_migration_import_source_resolve_under "${JV_MIGRATION_IMPORT_SOURCE_BASE}" "${relative}")" || return 1
            ;;
        *) return 1 ;;
    esac

    jv_migration_validate_source_name "${JV_MIGRATION_IMPORT_SOURCE_PATH}" >/dev/null 2>&1 || return 1
    transport_identity="$(jv_migration_import_transport_identity "${source_json}")" || return 1
    content_identity="$(jv_migration_import_source_content_identity "${kind}" "${JV_MIGRATION_IMPORT_SOURCE_PATH}")" || return 1
    JV_MIGRATION_IMPORT_SOURCE_IDENTITY="$(printf '%s\n%s\n%s\n' "${transport_identity}" "${relative}" "${content_identity}" | sha256sum | awk '{print $1}')"
}
