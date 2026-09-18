# shellcheck shell=bash

A53_FSTAB="${A53_FSTAB:-/etc/fstab}"
A53_TRANSACTION_ROOT="${A53_TRANSACTION_ROOT:-/var/lib/justvoxel/management/transactions}"
A53_SCHEMA_VERSION=v1
A53_DEFAULT_DATA_PATH=/var/lib/justvoxel/minecraft
A53_DEFAULT_BACKUP_PATH=/var/lib/justvoxel/backups
A54_SMB_CREDENTIALS="${A54_SMB_CREDENTIALS:-/etc/justvoxel/smb-backup.credentials}"

A53_REQUEST=''
A53_OPERATION_ID=''
A53_PLAN_FINGERPRINT=''
A53_TX_DIR=''
A53_MANIFEST=''
A53_FSTAB_EXISTED=false
A53_FSTAB_CHANGED=false
A53_FSTAB_MODE=''
A53_FSTAB_UID=''
A53_FSTAB_GID=''
A53_MUTATION_STARTED=false
A53_MOUNTS_BY_US=()
A53_DIRS_CREATED=()
A54_SMB_PASSWORD=''
A54_CREDENTIALS_EXISTED=false
A54_CREDENTIALS_CHANGED=false
A54_CREDENTIALS_MODE=''
A54_CREDENTIALS_UID=''
A54_CREDENTIALS_GID=''

_a53_json() {
    local ok="$1" applied="$2" phase="$3" rollback_state="$4" rollback_result="$5" error="$6"
    jq -n \
        --argjson ok "${ok}" \
        --argjson applied "${applied}" \
        --arg phase "${phase}" \
        --arg rollback_state "${rollback_state}" \
        --arg rollback_result "${rollback_result}" \
        --arg error "${error}" \
        '{ok:$ok,applied:$applied,phase:$phase,rollback_state:$rollback_state,rollback_result:$rollback_result} + (if $error == "" then {} else {error:$error} end)'
}

_a53_normalize_path() {
    realpath -m -- "$1" 2>/dev/null || true
}

_a53_nearest_existing_path() {
    local path="$1"
    while [[ ${path} != / && ! -e ${path} ]]; do
        path="$(dirname -- "${path}")"
    done
    [[ -e ${path} ]] || path=/
    printf '%s\n' "${path}"
}

_a53_path_within() {
    local path="${1%/}" mountpoint="${2%/}"
    [[ ${path} == "${mountpoint}" || ${path} == "${mountpoint}/"* ]]
}

_a53_paths_overlap() {
    local first="${1%/}" second="${2%/}"
    [[ ${first} == "${second}" || ${first} == "${second}/"* || ${second} == "${first}/"* ]]
}

_a53_parent_disk() {
    local real
    real="$(readlink -f -- "$1" 2>/dev/null || true)"
    [[ -n ${real} ]] || return 0
    lsblk -s -npo NAME,TYPE "${real}" 2>/dev/null | awk '$2 == "disk" {print $1; exit}'
}

_a53_file_sha256() {
    local file="$1"
    [[ -e ${file} ]] || { printf 'absent\n'; return 0; }
    sha256sum -- "${file}" 2>/dev/null | awk '{print $1}'
}

_a53_manifest_write() {
    local data="$1" tmp
    tmp="$(mktemp "${A53_TX_DIR}/.manifest.XXXXXX")" || return 1
    printf '%s\n' "${data}" > "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    chmod 0600 "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    mv -f -- "${tmp}" "${A53_MANIFEST}" || { rm -f -- "${tmp}"; return 1; }
    chmod 0600 "${A53_MANIFEST}" || return 1
    sync -f "${A53_MANIFEST}" >/dev/null 2>&1 || true
}

_a53_manifest_update() {
    local filter="$1" current updated
    current="$(cat -- "${A53_MANIFEST}" 2>/dev/null)" || return 1
    updated="$(jq -c "${filter}" <<< "${current}" 2>/dev/null)" || return 1
    _a53_manifest_write "${updated}"
}

_a53_manifest_set_phase() {
    local phase="$1"
    _a53_manifest_update ".phase = \"${phase}\""
}

_a53_manifest_record_dir() {
    local path="$1" encoded
    encoded="$(jq -Rn --arg v "${path}" '$v')" || return 1
    _a53_manifest_update ".directories_created += [${encoded}]"
}

_a53_manifest_record_mount() {
    local mountpoint="$1" uuid="$2" source="$3" object
    object="$(jq -cn --arg mountpoint "${mountpoint}" --arg uuid "${uuid}" --arg source "${source}" '{mountpoint:$mountpoint,uuid:$uuid,source:$source}')" || return 1
    _a53_manifest_update ".mounts_by_transaction += [${object}]"
}

_a53_manifest_set_fstab_changed() {
    local after
    A53_FSTAB_CHANGED=true
    after="$(_a53_file_sha256 "${A53_FSTAB}")"
    _a53_manifest_update ".fstab_changed = true | .fstab_after_sha256 = \"${after}\""
}

_a53_manifest_set_rollback() {
    local state="$1" result="$2" state_json result_json
    state_json="$(jq -Rn --arg v "${state}" '$v')" || return 1
    result_json="$(jq -Rn --arg v "${result}" '$v')" || return 1
    _a53_manifest_update ".rollback.state = ${state_json} | .rollback.result = ${result_json}"
}

_a53_parse_request() {
    A53_REQUEST="$(cat)"
    jq -e '
      type == "object" and
      ((keys - ["schema_version","operation_id","plan_fingerprint","storage","backups","smb_password"]) | length == 0) and
      .schema_version == "v1" and
      (.operation_id|type == "string") and
      (.plan_fingerprint|type == "string") and
      ((.smb_password // "")|type == "string") and
      (.storage|type == "object") and
      (.backups|type == "object") and
      ((.storage|keys) - ["type","path","device","parent_disk","filesystem","uuid","mount_point","expected_uuid","expected_source"] | length == 0) and
      ((.backups|keys) - ["type","path","device","parent_disk","filesystem","uuid","mount_point","expected_uuid","expected_source","source","username","domain","credentials_required"] | length == 0) and
      ([.storage,.backups][] | .type|type == "string") and
      ([.storage,.backups][] | .path|type == "string") and
      ([.storage,.backups][] | (.device // "")|type == "string") and
      ([.storage,.backups][] | (.parent_disk // "")|type == "string") and
      ([.storage,.backups][] | (.filesystem // "")|type == "string") and
      ([.storage,.backups][] | (.uuid // "")|type == "string") and
      ([.storage,.backups][] | (.mount_point // "")|type == "string") and
      ([.storage,.backups][] | (.expected_uuid // "")|type == "string") and
      ([.storage,.backups][] | (.expected_source // "")|type == "string") and
      ((.backups.source // "")|type == "string") and
      ((.backups.username // "")|type == "string") and
      ((.backups.domain // "")|type == "string") and
      ((.backups.credentials_required // false)|type == "boolean")
    ' >/dev/null 2>&1 <<< "${A53_REQUEST}" || return 1

    A54_SMB_PASSWORD="$(jq -r '.smb_password // ""' <<< "${A53_REQUEST}")"
    [[ ${A54_SMB_PASSWORD} != *$'\n'* && ${A54_SMB_PASSWORD} != *$'\r'* && ${#A54_SMB_PASSWORD} -le 4096 ]] || return 1
    A53_REQUEST="$(jq -c 'del(.smb_password)' <<< "${A53_REQUEST}")" || return 1

    A53_OPERATION_ID="$(jq -r '.operation_id' <<< "${A53_REQUEST}")"
    A53_PLAN_FINGERPRINT="$(jq -r '.plan_fingerprint' <<< "${A53_REQUEST}")"
    [[ ${A53_OPERATION_ID} =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]] || return 1
    [[ ${A53_PLAN_FINGERPRINT} =~ ^sha256:[0-9a-f]{64}$ ]] || return 1
    return 0
}

_a53_validate_system_target() {
    local purpose="$1" path="$2" normalized probe target fstype expected
    normalized="$(_a53_normalize_path "${path}")"
    [[ -n ${normalized} ]] || return 1
    validate_storage_path "${normalized}" >/dev/null 2>&1 || return 1
    if [[ ${purpose} == data ]]; then
        expected="${A53_DEFAULT_DATA_PATH}"
    else
        expected="${A53_DEFAULT_BACKUP_PATH}"
    fi
    [[ ${normalized} == "${expected}" ]] || return 1
    probe="$(_a53_nearest_existing_path "${normalized}")"
    read -r target fstype < <(findmnt -n -o TARGET,FSTYPE --target "${probe}" 2>/dev/null || true)
    case "${target}" in /|/var) ;; *) return 1 ;; esac
    case "${fstype}" in nfs|nfs4|cifs|smb3) return 1 ;; esac
    return 0
}

_a53_validate_partition_target() {
    local purpose="$1" target_json device parent filesystem uuid mountpoint expected_uuid expected_source path
    local real_device real_parent current_parent current_fs current_uuid mounted_count mounted actual_uuid actual_source normalized_path normalized_mount
    target_json="$2"
    device="$(jq -r '.device // ""' <<< "${target_json}")"
    parent="$(jq -r '.parent_disk // ""' <<< "${target_json}")"
    filesystem="$(jq -r '.filesystem // ""' <<< "${target_json}")"
    uuid="$(jq -r '.uuid // ""' <<< "${target_json}")"
    mountpoint="$(jq -r '.mount_point // ""' <<< "${target_json}")"
    expected_uuid="$(jq -r '.expected_uuid // ""' <<< "${target_json}")"
    expected_source="$(jq -r '.expected_source // ""' <<< "${target_json}")"
    path="$(jq -r '.path' <<< "${target_json}")"

    [[ -n ${device} && -n ${parent} && -n ${filesystem} && -n ${uuid} && -n ${mountpoint} && -n ${expected_uuid} ]] || return 1
    [[ ${filesystem} == xfs || ${filesystem} == ext4 || ${filesystem} == btrfs ]] || return 1
    [[ ${uuid} == "${expected_uuid}" ]] || return 1
    storage_validate_partition "${device}" >/dev/null 2>&1 || return 1

    real_device="$(readlink -f -- "${device}" 2>/dev/null || true)"
    [[ -n ${real_device} && ${real_device} == "${device}" ]] || return 1
    current_fs="$(blkid -s TYPE -o value "${device}" 2>/dev/null || true)"
    current_uuid="$(blkid -s UUID -o value "${device}" 2>/dev/null || true)"
    [[ ${current_fs} == "${filesystem}" && ${current_uuid} == "${expected_uuid}" ]] || return 1

    current_parent="$(_a53_parent_disk "${device}")"
    real_parent="$(readlink -f -- "${parent}" 2>/dev/null || true)"
    [[ -n ${current_parent} && -n ${real_parent} && ${current_parent} == "${real_parent}" ]] || return 1

    while IFS= read -r mounted; do
        [[ -n ${mounted} ]] || continue
        storage_mount_is_critical "${mounted}" && return 1
    done < <(lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d')

    mounted_count="$(lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d' | wc -l)"
    (( mounted_count <= 1 )) || return 1
    mounted="$(lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d' | head -n1)"
    normalized_mount="$(_a53_normalize_path "${mountpoint}")"
    normalized_path="$(_a53_normalize_path "${path}")"
    [[ -n ${normalized_mount} && -n ${normalized_path} ]] || return 1
    storage_validate_mountpoint_path "${normalized_mount}" >/dev/null 2>&1 || return 1
    validate_storage_path "${normalized_path}" >/dev/null 2>&1 || return 1
    _a53_path_within "${normalized_path}" "${normalized_mount}" || return 1

    if [[ -n ${mounted} ]]; then
        mounted="$(_a53_normalize_path "${mounted}")"
        [[ ${mounted} == "${normalized_mount}" ]] || return 1
        actual_uuid="$(findmnt -n -o UUID --target "${normalized_mount}" 2>/dev/null || true)"
        actual_source="$(findmnt -n -o SOURCE --target "${normalized_mount}" 2>/dev/null || true)"
        [[ ${actual_uuid} == "${expected_uuid}" ]] || return 1
        if [[ -n ${expected_source} && ${actual_source} != "${expected_source}" ]]; then
            return 1
        fi
    elif mountpoint -q -- "${normalized_mount}"; then
        return 1
    fi
    return 0
}

_a54_validate_network_target() {
    local target_json="$1" type path mountpoint source expected_source username domain credentials_required actual_source
    target_json="$2"
    type="$(jq -r '.type' <<< "${target_json}")"
    path="$(_a53_normalize_path "$(jq -r '.path' <<< "${target_json}")")"
    mountpoint="$(_a53_normalize_path "$(jq -r '.mount_point // ""' <<< "${target_json}")")"
    source="$(jq -r '.source // ""' <<< "${target_json}")"
    expected_source="$(jq -r '.expected_source // ""' <<< "${target_json}")"
    username="$(jq -r '.username // ""' <<< "${target_json}")"
    domain="$(jq -r '.domain // ""' <<< "${target_json}")"
    credentials_required="$(jq -r '.credentials_required // false' <<< "${target_json}")"

    [[ -n ${path} && -n ${mountpoint} && -n ${source} ]] || return 1
    validate_storage_path "${path}" >/dev/null 2>&1 || return 1
    storage_validate_mountpoint_path "${mountpoint}" >/dev/null 2>&1 || return 1
    _a53_path_within "${path}" "${mountpoint}" || return 1
    [[ -z ${expected_source} || ${expected_source} == "${source}" ]] || return 1

    case "${type}" in
        nfs)
            [[ ${source} == *:* && ${source} != *[[:space:]]* ]] || return 1
            [[ ${credentials_required} == false && -z ${username} && -z ${domain} ]] || return 1
            ;;
        smb)
            [[ ${source} == //*/* && ${source} != *[[:space:]]* ]] || return 1
            [[ ${credentials_required} == true && -n ${username} ]] || return 1
            validate_simple_text "${username}" >/dev/null 2>&1 || return 1
            validate_simple_text "${domain}" >/dev/null 2>&1 || return 1
            [[ ${username} != *$'\t'* && ${domain} != *$'\t'* ]] || return 1
            ;;
        *) return 2 ;;
    esac

    if mountpoint -q -- "${mountpoint}"; then
        actual_source="$(findmnt -n -o SOURCE --target "${mountpoint}" 2>/dev/null || true)"
        [[ ${actual_source} == "${source}" ]] || return 1
    fi
    return 0
}

_a53_validate_target() {
    local purpose="$1" target_json="$2" type path
    type="$(jq -r '.type' <<< "${target_json}")"
    path="$(jq -r '.path' <<< "${target_json}")"
    case "${type}" in
        system) _a53_validate_system_target "${purpose}" "${path}" ;;
        partition) _a53_validate_partition_target "${purpose}" "${target_json}" ;;
        nfs|smb)
            [[ ${purpose} == backup ]] || return 2
            _a54_validate_network_target "${purpose}" "${target_json}"
            ;;
        *) return 2 ;;
    esac
}

_a53_validate_all() {
    local storage backups storage_path backup_path backup_type
    storage="$(jq -c '.storage' <<< "${A53_REQUEST}")"
    backups="$(jq -c '.backups' <<< "${A53_REQUEST}")"
    _a53_validate_target data "${storage}" || return $?
    _a53_validate_target backup "${backups}" || return $?
    storage_path="$(_a53_normalize_path "$(jq -r '.path' <<< "${storage}")")"
    backup_path="$(_a53_normalize_path "$(jq -r '.path' <<< "${backups}")")"
    _a53_paths_overlap "${storage_path}" "${backup_path}" && return 1
    backup_type="$(jq -r '.type' <<< "${backups}")"
    if [[ ${backup_type} == smb && -z ${A54_SMB_PASSWORD} ]]; then
        return 3
    fi
    if [[ ${backup_type} != smb && -n ${A54_SMB_PASSWORD} ]]; then
        return 1
    fi
    return 0
}
