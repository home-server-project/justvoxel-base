# shellcheck shell=bash

A53_FSTAB="${A53_FSTAB:-/etc/fstab}"
A53_TRANSACTION_ROOT="${A53_TRANSACTION_ROOT:-/var/lib/justvoxel/management/transactions}"
A53_SCHEMA_VERSION=v2
A53_INSTANCES_ROOT="${A53_INSTANCES_ROOT:-/etc/justvoxel/instances}"
A53_SETUP_INSTANCE_ROOT="${A53_SETUP_INSTANCE_ROOT:-/var/lib/justvoxel-instance-runtime}"
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
A53_EVIDENCE='{}'

_a53_evidence_set() {
    local key="$1" value="$2" updated
    updated="$(jq -c --arg key "${key}" --arg value "${value}" '. + {($key):$value}' <<< "${A53_EVIDENCE}" 2>/dev/null)" || return 0
    A53_EVIDENCE="${updated}"
    return 0
}

_a53_evidence_set_bounded() {
    local key="$1" value="$2" max="${3:-12000}"
    if (( ${#value} > max )); then
        value="${value:0:max}…"
    fi
    _a53_evidence_set "${key}" "${value}"
    return 0
}

_a53_evidence_from_manifest() {
    [[ -n ${A53_MANIFEST} && -f ${A53_MANIFEST} ]] || return 0
    local phase fstab_changed credentials_changed mount_count directory_count rollback_state rollback_result
    phase="$(jq -r '.phase // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
    fstab_changed="$(jq -r '.fstab_changed // false' "${A53_MANIFEST}" 2>/dev/null || true)"
    credentials_changed="$(jq -r '.credentials_changed // false' "${A53_MANIFEST}" 2>/dev/null || true)"
    mount_count="$(jq -r '(.mounts_by_transaction // []) | length' "${A53_MANIFEST}" 2>/dev/null || true)"
    directory_count="$(jq -r '(.directories_created // []) | length' "${A53_MANIFEST}" 2>/dev/null || true)"
    rollback_state="$(jq -r '.rollback.state // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
    rollback_result="$(jq -r '.rollback.result // ""' "${A53_MANIFEST}" 2>/dev/null || true)"
    [[ -n ${phase} ]] && _a53_evidence_set transaction_phase "${phase}"
    [[ -n ${fstab_changed} ]] && _a53_evidence_set fstab_changed "${fstab_changed}"
    [[ -n ${credentials_changed} ]] && _a53_evidence_set credentials_changed "${credentials_changed}"
    [[ -n ${mount_count} ]] && _a53_evidence_set transaction_mount_count "${mount_count}"
    [[ -n ${directory_count} ]] && _a53_evidence_set created_directory_count "${directory_count}"
    [[ -n ${rollback_state} ]] && _a53_evidence_set rollback_state "${rollback_state}"
    [[ -n ${rollback_result} ]] && _a53_evidence_set rollback_result "${rollback_result}"
    return 0
}

_a53_json() {
    local ok="$1" applied="$2" phase="$3" rollback_state="$4" rollback_result="$5" error="$6" evidence="${7:-}"
    [[ -n ${evidence} ]] || evidence="${A53_EVIDENCE}"
    [[ -n ${evidence} ]] || evidence='{}'
    jq -n \
        --argjson ok "${ok}" \
        --argjson applied "${applied}" \
        --arg phase "${phase}" \
        --arg rollback_state "${rollback_state}" \
        --arg rollback_result "${rollback_result}" \
        --arg error "${error}" \
        --argjson evidence "${evidence}" \
        '{ok:$ok,applied:$applied,phase:$phase,rollback_state:$rollback_state,rollback_result:$rollback_result}
         + (if $error == "" then {} else {error:$error} end)
         + (if ($evidence|length) == 0 then {} else {evidence:$evidence} end)'
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
    jv_trace_run lsblk -s -nrpo NAME,TYPE "${real}" 2>/dev/null | awk '$2 == "disk" {print $1; exit}'
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
    local mountpoint="$1" uuid="$2" source="$3" object line
    line="$(_a53_fstab_lines "${A53_FSTAB}" "${mountpoint}")" || return 1
    [[ -n ${line} && ${line} != *$'\n'* ]] || return 1
    object="$(jq -cn --arg mountpoint "${mountpoint}" --arg uuid "${uuid}" --arg source "${source}" --arg line "${line}" '{mountpoint:$mountpoint,uuid:$uuid,source:$source,fstab_line:$line}')" || return 1
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
      ((keys - ["schema_version","operation_id","plan_fingerprint","storage","backups","smb_password","mounted_only","isolated_credentials"]) | length == 0) and
      ((.mounted_only // false)|type == "boolean") and
      ((.isolated_credentials // false)|type == "boolean") and
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
    if [[ $(jq -r '.backups.type' <<< "${A53_REQUEST}") == smb &&
          $(jq -r '.isolated_credentials // false' <<< "${A53_REQUEST}") == true ]]; then
        A54_SMB_CREDENTIALS="/etc/justvoxel/smb-backup-${A53_OPERATION_ID}.credentials"
    fi
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
    current_fs="$(jv_trace_run blkid -s TYPE -o value "${device}" 2>/dev/null || true)"
    current_uuid="$(jv_trace_run blkid -s UUID -o value "${device}" 2>/dev/null || true)"
    [[ ${current_fs} == "${filesystem}" && ${current_uuid} == "${expected_uuid}" ]] || return 1

    current_parent="$(_a53_parent_disk "${device}")"
    real_parent="$(readlink -f -- "${parent}" 2>/dev/null || true)"
    [[ -n ${current_parent} && -n ${real_parent} && ${current_parent} == "${real_parent}" ]] || return 1

    while IFS= read -r mounted; do
        [[ -n ${mounted} ]] || continue
        storage_mount_is_critical "${mounted}" && return 1
    done < <(lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d')

    mounted_count="$(jv_trace_run lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d' | wc -l)"
    (( mounted_count <= 1 )) || return 1
    mounted="$(jv_trace_run lsblk -nro MOUNTPOINT "${device}" 2>/dev/null | sed '/^$/d' | head -n1)"
    normalized_mount="$(_a53_normalize_path "${mountpoint}")"
    normalized_path="$(_a53_normalize_path "${path}")"
    [[ -n ${normalized_mount} && -n ${normalized_path} ]] || return 1
    storage_validate_mountpoint_path "${normalized_mount}" >/dev/null 2>&1 || return 1
    validate_storage_path "${normalized_path}" >/dev/null 2>&1 || return 1
    _a53_path_within "${normalized_path}" "${normalized_mount}" || return 1

    if [[ -n ${mounted} ]]; then
        mounted="$(_a53_normalize_path "${mounted}")"
        [[ ${mounted} == "${normalized_mount}" ]] || return 1
        actual_uuid="$(jv_exact_mount_identity UUID "${normalized_mount}" 2>/dev/null || true)"
        actual_source="$(jv_exact_mount_identity SOURCE "${normalized_mount}" 2>/dev/null || true)"
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
    local target_json="$2" type path mountpoint source expected_source username domain credentials_required actual_source
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
        actual_source="$(jv_exact_mount_identity SOURCE "${mountpoint}" 2>/dev/null || true)"
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
    if [[ $(jq -r '.mounted_only // false' <<< "${A53_REQUEST}") == true ]]; then
        local point
        while IFS= read -r point; do
            [[ -n ${point} ]] && mountpoint -q -- "${point}" || return 1
        done < <(jq -r '[.storage,.backups][] | select(.type != "system") | .mount_point' <<< "${A53_REQUEST}")
    fi
    storage="$(jq -c '.storage' <<< "${A53_REQUEST}")"
    backups="$(jq -c '.backups' <<< "${A53_REQUEST}")"
    _a53_validate_target data "${storage}" || return $?
    _a53_validate_target backup "${backups}" || return $?
    if [[ $(jq -r '.type' <<< "${storage}") == partition && $(jq -r '.type' <<< "${backups}") == partition ]]; then
        local data_uuid backup_uuid data_mount backup_mount
        data_uuid="$(jq -r '.expected_uuid' <<< "${storage}")"
        backup_uuid="$(jq -r '.expected_uuid' <<< "${backups}")"
        data_mount="$(jq -r '.mount_point' <<< "${storage}")"
        backup_mount="$(jq -r '.mount_point' <<< "${backups}")"
        [[ ${data_uuid} == "${backup_uuid}" && ${data_mount} == "${backup_mount}" && $(jq -r '.device' <<< "${storage}") == "$(jq -r '.device' <<< "${backups}")" ||
           ${data_uuid} != "${backup_uuid}" && ${data_mount} != "${backup_mount}" ]] || return 1
    fi
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

# Infrastructure ownership is deliberately independent of current global defaults.
_a53_resource_path_safe() {
    [[ -n $1 && $1 != *[[:space:]]* && $(_a53_normalize_path "$1") == "$1" ]] &&
        storage_validate_mountpoint_path "$1" >/dev/null 2>&1
}

_a53_fstab_lines() {
    [[ -e $1 ]] || return 0
    [[ -f $1 && ! -L $1 ]] || return 1
    awk -v mp="$2" '$0 !~ /^[[:space:]]*#/ && NF >= 2 && $2 == mp {print}' "$1"
}

_a53_preserve_mount() {
    local object
    object="$(jq -cn --arg point "$1" --arg reason "$2" '{mountpoint:$point,reason:$reason}')" || return 1
    _a53_evidence_set infrastructure_preserved "$2"
    _a53_manifest_update ".preserved_resources = ((.preserved_resources // []) + [${object}] | unique_by(.mountpoint))"
}

_a53_mount_preserved() {
    jq -e --arg point "$1" 'any(.preserved_resources[]?; .mountpoint == $point)' "${A53_MANIFEST}" >/dev/null
}

# Return success only on positive proof of no foreign references. Unreadable,
# malformed, or incomplete evidence retains infrastructure, with a durable reason.
_a53_mount_unused() {
    local point="$1" uuid="$2" source="$3" root path json targets operation instance_id
    A53_REFERENCE_REASON=uncertain_foreign_evidence
    for root in "${A53_INSTANCES_ROOT}" "${A53_TRANSACTION_ROOT}" "${A53_SETUP_INSTANCE_ROOT}"; do
        [[ ! -L ${root} ]] || return 1
        [[ ! -e ${root} || ( -d ${root} && -r ${root} && -x ${root} ) ]] || return 1
    done
    local -a files=()
    for path in "${A53_INSTANCES_ROOT}"/jv-*; do
        [[ -e ${path} || -L ${path} ]] || continue
        [[ -d ${path} && ! -L ${path} && -r ${path} && -x ${path} ]] || return 1
        files+=("${path}/config.json")
    done
    for path in "${files[@]}"; do
        [[ -f ${path} && ! -L ${path} && -r ${path} ]] || return 1
        json="$(cat -- "${path}")" || return 1
        instance_id="${path%/config.json}"; instance_id="${instance_id##*/}"
        jq -e --arg id "${instance_id}" '.schema_version == 1 and .runtime.id == $id and
          (.storage.root|type == "string") and (.storage.data_path|type == "string")' <<< "${json}" >/dev/null || return 1
        # Legacy records without backup placement cannot prove backup non-use.
        targets="$(jq -ce '[.storage,.backups] | if all(.[]; type == "object") then . else error("missing placement") end' <<< "${json}")" || return 1
        _a53_targets_unused "${targets}" "${point}" "${uuid}" "${source}" || return 1
    done
    for path in "${A53_TRANSACTION_ROOT}"/*; do
        [[ -e ${path} || -L ${path} ]] || continue
        [[ ${path} != "${A53_TX_DIR}" ]] || continue
        [[ -d ${path} && ! -L ${path} && -r ${path} && -x ${path} ]] || return 1
        [[ -f ${path}/manifest.json && ! -L ${path}/manifest.json && -r ${path}/manifest.json ]] || return 1
        json="$(cat -- "${path}/manifest.json")" || return 1
        operation="${path##*/}"
        jq -e --arg operation "${operation}" '(.schema_version == "v1" or .schema_version == "v2") and .operation_id == $operation and (.phase|type == "string") and (.rollback.state|type == "string")' <<< "${json}" >/dev/null || return 1
        if jq -e '.phase == "storage_rolled_back" and .rollback.state == "succeeded" and .rollback.result == "rolled_back"' <<< "${json}" >/dev/null; then continue; fi
        targets="$(jq -ce '[.storage_request.storage,.storage_request.backups] | if all(.[]; type == "object") then . else error("missing request") end' <<< "${json}")" || return 1
        _a53_targets_unused "${targets}" "${point}" "${uuid}" "${source}" || return 1
    done
    # Phase-1 operation-bound provisional evidence is authoritative before its
    # instance config is published. Never interpret friendly names as identity.
    for path in "${A53_SETUP_INSTANCE_ROOT}"/setup-*.json; do
        [[ -e ${path} || -L ${path} ]] || continue
        [[ ${path##*/} != "setup-${A53_OPERATION_ID}.json" ]] || continue
        [[ -f ${path} && ! -L ${path} && -r ${path} ]] || return 1
        json="$(cat -- "${path}")" || return 1
        operation="${path##*/setup-}"; operation="${operation%.json}"
        jq -e --arg operation "${operation}" '(.schema == 1 or .schema == 2) and .request.operation_id == $operation and (.rolled_back|type == "boolean")' <<< "${json}" >/dev/null || return 1
        if jq -e '.rolled_back == true' <<< "${json}" >/dev/null; then continue; fi
        targets="$(jq -ce '[.request.storage,.request.backups] | if all(.[]; type == "object") then . else error("missing request") end' <<< "${json}")" || return 1
        _a53_targets_unused "${targets}" "${point}" "${uuid}" "${source}" || return 1
    done
    A53_REFERENCE_REASON=unused
    return 0
}

_a53_targets_unused() {
    local targets="$1" point="$2" uuid="$3" source="$4" target_point
    jq -e 'type == "array" and all(.[];
        ((.mount_point|type == "string") or (.mount_point == null and .type == "system")) and
        ((.uuid // "")|type == "string") and ((.source // "")|type == "string") and
        ((.expected_uuid // "")|type == "string") and ((.expected_source // "")|type == "string") and
        ((.device // "")|type == "string") and
        ((.uuid // "") == "" or (.expected_uuid // "") == "" or .uuid == .expected_uuid) and
        ((.source // "") == "" or (.expected_source // "") == "" or .source == .expected_source) and
        (if (.mount_point // "") == "" then
          ([.uuid,.source,.expected_uuid,.expected_source] | all(.[]; . == null or . == ""))
        else ((.mount_point|startswith("/")) and
          ([.uuid,.source,.expected_uuid,.expected_source] | any(.[]; type == "string" and length > 0))) end))' <<< "${targets}" >/dev/null || return 1
    while IFS= read -r target_point; do
        [[ -z ${target_point} ]] || _a53_resource_path_safe "${target_point}" || return 1
    done < <(jq -r '.[].mount_point // ""' <<< "${targets}")
    if jq -e --arg mp "${point}" --arg uuid "${uuid}" --arg source "${source}" 'any(.[];
        .mount_point == $mp or
        ($uuid != "" and (.uuid == $uuid or .expected_uuid == $uuid)) or
        ($source != "" and (.source == $source or .expected_source == $source or .device == $source)))' <<< "${targets}" >/dev/null; then
        A53_REFERENCE_REASON=foreign_storage_or_backup_reference
        return 1
    fi
    return 0
}

_a53_snapshot_valid() {
    local snapshot="${A53_TX_DIR}/fstab.before" expected
    expected="$(jq -r '.fstab_before_sha256' "${A53_MANIFEST}")" || return 1
    [[ -f ${snapshot} && ! -L ${snapshot} && ${expected} =~ ^[0-9a-f]{64}$ &&
       $(_a53_file_sha256 "${snapshot}") == "${expected}" ]]
}

_a53_resource_object() {
    local point="$1" line="$2" source filesystem rest uuid=''
    [[ -n ${line} && ${line} != *$'\n'* && ${line} != *$'\r'* ]] || return 1
    read -r source rest filesystem rest <<< "${line}"
    case "${filesystem}:${source}" in
        xfs:UUID=?*|ext4:UUID=?*|btrfs:UUID=?*) uuid="${source#UUID=}" ;;
        nfs:*:/*|nfs4:*:/*|cifs://*/*) ;;
        *) return 1 ;;
    esac
    [[ $(awk '{print NF}' <<< "${line}") == 6 && $(awk '{print $2}' <<< "${line}") == "${point}" ]] || return 1
    jq -cn --arg point "${point}" --arg line "${line}" --arg source "${source}" --arg uuid "${uuid}" --arg fs "${filesystem}" \
        '{mountpoint:$point,exact_line:$line,source:$source,uuid:$uuid,filesystem:$fs,action:"created"}'
}

_a53_prepare_rollback_resources() {
    local schema entry point line old resource resources='[]' retired='[]' source uuid expected after
    schema="$(jq -r '.schema_version' "${A53_MANIFEST}")" || return 1
    [[ ! -L ${A53_FSTAB} && ( ! -e ${A53_FSTAB} || -f ${A53_FSTAB} ) ]] || return 1
    jq -e '(.mounts_by_transaction|type == "array") and
      all(.mounts_by_transaction[]; (.mountpoint|type == "string") and (.source|type == "string") and (.uuid|type == "string")) and
      ((.preserved_resources // []) | type == "array" and all(.[]; (.mountpoint|type == "string") and (.reason|type == "string")))' "${A53_MANIFEST}" >/dev/null || return 1
    if [[ ${schema} == v2 ]]; then
        jq -e '(.fstab_resources|type == "array") and
          all(.fstab_resources[]; (.mountpoint|type == "string") and (.exact_line|type == "string") and
            (.source|type == "string") and (.uuid|type == "string") and (.filesystem|type == "string") and
            (.action == "created" or .action == "pending")) and ((.retired_mounts // [])|length == 0)' "${A53_MANIFEST}" >/dev/null || return 1
        while IFS= read -r entry; do
            point="$(jq -r '.mountpoint' <<< "${entry}")"
            _a53_resource_path_safe "${point}" || return 1
            resource="$(_a53_resource_object "${point}" "$(jq -r '.exact_line' <<< "${entry}")")" || return 1
            jq -e --argjson expected "${resource}" '(.mountpoint == $expected.mountpoint and .source == $expected.source and .uuid == $expected.uuid and .filesystem == $expected.filesystem)' <<< "${entry}" >/dev/null || return 1
            if [[ $(jq -r '.action' <<< "${entry}") == pending ]]; then
                _a53_preserve_mount "${point}" unconfirmed_fstab_creation || return 1
            fi
        done < <(jq -c '.fstab_resources[]' "${A53_MANIFEST}")
        return 0
    fi
    [[ ${schema} == v1 ]] || return 1
    # Derive bounded legacy ownership once, and persist it before any mutation.
    # The original v1 schema and snapshot remain immutable compatibility evidence.
    _a53_snapshot_valid || return 1
    if jq -e 'has("legacy_fstab_resources") and has("legacy_retired_resources")' "${A53_MANIFEST}" >/dev/null; then
        _a53_validate_legacy_resources
        return $?
    fi
    jq -e '(.retired_mounts // []) | type == "array" and all(.[];
      (.mountpoint|type == "string") and (.source|type == "string") and (.was_mounted|type == "boolean"))' "${A53_MANIFEST}" >/dev/null || return 1
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        source="$(jq -r '.source' <<< "${entry}")"
        uuid="$(jq -r '.uuid // ""' <<< "${entry}")"
        _a53_resource_path_safe "${point}" || return 1
        old="$(_a53_fstab_lines "${A53_TX_DIR}/fstab.before" "${point}")" || return 1
        resource="$(_a53_resource_object "${point}" "${old}")" || return 1
        [[ $(jq -r '.source' <<< "${resource}") == "${source}" ]] || return 1
        [[ -z ${uuid} || $(jq -r '.uuid' <<< "${resource}") == "${uuid}" ]] || return 1
        resource="$(jq -c --argjson mounted "$(jq '.was_mounted' <<< "${entry}")" '. + {was_mounted:$mounted}' <<< "${resource}")" || return 1
        retired="$(jq -c --argjson r "${resource}" '. + [$r]' <<< "${retired}")" || return 1
    done < <(jq -c '.retired_mounts[]?' "${A53_MANIFEST}")
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        source="$(jq -r '.source' <<< "${entry}")"
        uuid="$(jq -r '.uuid' <<< "${entry}")"
        _a53_resource_path_safe "${point}" || return 1
        old="$(_a53_fstab_lines "${A53_TX_DIR}/fstab.before" "${point}")" || return 1
        [[ -z ${old} && ${A53_FSTAB_CHANGED} == true ]] || continue
        line="$(_a53_fstab_lines "${A53_FSTAB}" "${point}")" || return 1
        [[ -n ${line} ]] || continue
        after="$(jq -r '.fstab_after_sha256' "${A53_MANIFEST}")"
        if [[ $(_a53_file_sha256 "${A53_FSTAB}") != "${after}" ]]; then
            _a53_preserve_mount "${point}" insufficient_legacy_fstab_ownership || return 1
            continue
        fi
        resource="$(_a53_resource_object "${point}" "${line}")" || return 1
        expected="$(jq -r '.source' <<< "${resource}")"
        if [[ ${expected} == UUID=* ]]; then
            [[ -n ${uuid} && ${expected#UUID=} == "${uuid}" ]] || return 1
        else
            [[ -n ${source} && ${expected} == "${source}" ]] || return 1
        fi
        resources="$(jq -c --argjson r "${resource}" '. + [$r]' <<< "${resources}")" || return 1
    done < <(jq -c '.mounts_by_transaction[]' "${A53_MANIFEST}")
    _a53_manifest_update ".legacy_fstab_resources = ${resources} | .legacy_retired_resources = ${retired}" || return 1
    _a53_validate_legacy_resources
}

_a53_mount_definition_safe() {
    local point="$1" resource line expected
    resource="$(jq -c --arg mp "${point}" '(if .schema_version == "v2" then .fstab_resources else .legacy_fstab_resources end)[] | select(.mountpoint == $mp)' "${A53_MANIFEST}")" || return 1
    if [[ -n ${resource} ]]; then
        expected="$(jq -r '.exact_line' <<< "${resource}")" || return 1
    else
        # Mounting an existing entry grants no fstab ownership. It still must
        # retain the reviewed definition before we may undo our live mount.
        expected="$(jq -r --arg mp "${point}" '.mounts_by_transaction[] | select(.mountpoint == $mp) | .fstab_line // empty' "${A53_MANIFEST}")" || return 1
        [[ -n ${expected} ]] || return 0
    fi
    line="$(_a53_fstab_lines "${A53_FSTAB}" "${point}")" || return 1
    [[ -z ${line} || ${line} == "${expected}" ]]
}

_a53_rollback_fstab_resources() {
    local entry point line expected source uuid failure=0
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        _a53_mount_preserved "${point}" && continue
        source="$(jq -r '.source' <<< "${entry}")"
        uuid="$(jq -r '.uuid' <<< "${entry}")"
        if ! _a53_mount_unused "${point}" "${uuid}" "${source}"; then
            _a53_preserve_mount "${point}" "${A53_REFERENCE_REASON}" || failure=1
            continue
        fi
        line="$(_a53_fstab_lines "${A53_FSTAB}" "${point}")" || { failure=1; continue; }
        [[ -n ${line} ]] || continue
        expected="$(jq -r '.exact_line' <<< "${entry}")"
        if [[ ${line} != "${expected}" ]]; then
            _a53_preserve_mount "${point}" conflicting_fstab || true
            failure=1
            continue
        fi
        # A failed unmount or a subsequently mounted filesystem still needs its entry.
        if mountpoint -q -- "${point}"; then failure=1; continue; fi
        jv_storage_remove_entry "${A53_FSTAB}" "${point}" || failure=1
    done < <(jq -c '(if .schema_version == "v2" then .fstab_resources else .legacy_fstab_resources end)[] | select(.action == "created")' "${A53_MANIFEST}")
    jv_trace_run systemctl daemon-reload >/dev/null 2>&1 || failure=1
    (( failure == 0 ))
}

_a53_restore_retired_entries() {
    [[ $(jq -r .schema_version "${A53_MANIFEST}") == v1 ]] || return 0
    local entry point line expected tmp
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        expected="$(jq -r '.exact_line' <<< "${entry}")"
        line="$(_a53_fstab_lines "${A53_FSTAB}" "${point}")" || return 1
        if [[ -n ${line} ]]; then
            [[ ${line} == "${expected}" ]] || return 1
            continue
        fi
        [[ -e ${A53_FSTAB} ]] || : > "${A53_FSTAB}" || return 1
        tmp="$(mktemp "$(dirname -- "${A53_FSTAB}")/.fstab.retired.XXXXXX")" || return 1
        if ! cat -- "${A53_FSTAB}" > "${tmp}" || ! printf '%s\n' "${expected}" >> "${tmp}" ||
           ! cp --attributes-only --preserve=all -- "${A53_FSTAB}" "${tmp}" ||
           ! sync -f -- "${tmp}" || ! mv -fT -- "${tmp}" "${A53_FSTAB}"; then
            rm -f -- "${tmp}"
            return 1
        fi
    done < <(jq -c '.legacy_retired_resources[]?' "${A53_MANIFEST}")
    jv_trace_run systemctl daemon-reload >/dev/null 2>&1
}

# Revalidate derived compatibility resources on every retry against immutable
# evidence; the cached lists grant no appliance-wide authority.
_a53_validate_legacy_resources() {
    local entry point resource old source uuid
    jq -e '(.legacy_fstab_resources|type == "array") and (.legacy_retired_resources|type == "array")' "${A53_MANIFEST}" >/dev/null || return 1
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        _a53_resource_path_safe "${point}" || return 1
        resource="$(_a53_resource_object "${point}" "$(jq -r '.exact_line' <<< "${entry}")")" || return 1
        jq -e --argjson r "${resource}" '.source == $r.source and .uuid == $r.uuid and .filesystem == $r.filesystem and .action == "created"' <<< "${entry}" >/dev/null || return 1
    done < <(jq -c '.legacy_fstab_resources[],.legacy_retired_resources[]' "${A53_MANIFEST}")
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        old="$(_a53_fstab_lines "${A53_TX_DIR}/fstab.before" "${point}")" || return 1
        [[ ${old} == "$(jq -r '.exact_line' <<< "${entry}")" ]] || return 1
        source="$(jq -r '.source' <<< "${entry}")"
        jq -e --arg mp "${point}" --arg src "${source}" --argjson mounted "$(jq '.was_mounted' <<< "${entry}")" \
          'any(.retired_mounts[]?; .mountpoint == $mp and .source == $src and .was_mounted == $mounted)' "${A53_MANIFEST}" >/dev/null || return 1
    done < <(jq -c '.legacy_retired_resources[]' "${A53_MANIFEST}")
    while IFS= read -r entry; do
        point="$(jq -r '.mountpoint' <<< "${entry}")"
        [[ -z $(_a53_fstab_lines "${A53_TX_DIR}/fstab.before" "${point}") ]] || return 1
        source="$(jq -r '.source' <<< "${entry}")"
        uuid="$(jq -r '.uuid' <<< "${entry}")"
        jq -e --arg mp "${point}" --arg src "${source}" --arg uuid "${uuid}" \
          'any(.mounts_by_transaction[]; .mountpoint == $mp and (if $uuid != "" then .uuid == $uuid else .source == $src end))' "${A53_MANIFEST}" >/dev/null || return 1
    done < <(jq -c '.legacy_fstab_resources[]' "${A53_MANIFEST}")
}
