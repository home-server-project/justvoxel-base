#!/usr/bin/bash
# Isolated production-helper regressions: no real mounts or appliance writes.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/operation-trace.sh"
source "${repo_root}/mjust/libexec/common.sh"
source "${repo_root}/mjust/libexec/storage-common-base.sh"
source "${repo_root}/mjust/libexec/storage-lifecycle.sh"
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-common.sh"
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-apply.sh"
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-actions.sh"
base="$(mktemp -d)"
trap 'rm -rf -- "${base}"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
# Only appliance boundaries are mocked; ownership/journaling/recovery stay real.
systemctl() { return 0; }
install() {
    local -a args=()
    while (( $# )); do
        case "$1" in -o|-g) shift 2 ;; *) args+=("$1"); shift ;; esac
    done
    command install "${args[@]}"
}
chown() { return 0; }
stat() {
    if [[ ${2:-} == '%u:%g:%a:%h' ]]; then
        printf '0:0:%s\n' "$(command stat -c '%a:%h' -- "${@: -1}")"
    else command stat "$@"; fi
}
lsblk() { return 0; }
mountpoint() { [[ -f ${case_root}/mounted && $(cat "${case_root}/mounted") == "${@: -1}" ]]; }
mount() {
    [[ $# == 1 && $1 == "${point}" ]] || fail 'unexpected mount target'
    printf '%s\n' "$1" > "${case_root}/mounted"
    printf 'mount %s\n' "$1" >> "${case_root}/calls"
}
umount() {
    [[ $* == "-- ${point}" ]] || fail 'unexpected unmount target'
    rm -f -- "${case_root}/mounted"
    printf 'umount %s\n' "${point}" >> "${case_root}/calls"
}
jv_exact_mount_identity() {
    mountpoint -q -- "$2" || return 1
    case "$1" in UUID) printf '%s\n' "${uuid}" ;; SOURCE) printf '%s\n' "${live_source}" ;; *) return 1 ;; esac
}
blkid() {
    case "$*" in
        "-U ${uuid}") printf '/dev/vdb1\n' ;;
        '-s UUID -o value /dev/vdb1') printf '%s\n' "${uuid}" ;;
        '-s TYPE -o value /dev/vdb1') printf 'xfs\n' ;;
        *) return 1 ;;
    esac
}
fixture() {
    case_root="${base}/$1"
    mkdir -p "${case_root}"
    A53_TRANSACTION_ROOT="${case_root}/transactions"
    A53_INSTANCES_ROOT="${case_root}/instances"
    A53_SETUP_INSTANCE_ROOT="${case_root}/staging"
    A53_FSTAB="${case_root}/fstab"
    A54_SMB_CREDENTIALS="${case_root}/credentials"
    A53_OPERATION_ID=12345678-1234-4123-8123-123456789abc
    A53_PLAN_FINGERPRINT="sha256:$(printf 'a%.0s' {1..64})"
    A53_FSTAB_CHANGED=false; A53_FSTAB_EXISTED=false
    A54_CREDENTIALS_CHANGED=false; A54_CREDENTIALS_EXISTED=false
    A53_MOUNTS_BY_US=(); A53_DIRS_CREATED=(); A53_EVIDENCE='{}'
    A54_SMB_PASSWORD=''; A53_TX_DIR=''; A53_MANIFEST=''
    point="${case_root}/mount"
    uuid=5f5bd265-9c4a-4a8f-81cd-40b4efb9d050
    live_source=/dev/vdb1
    printf '# original\nUUID=unrelated /srv/other ext4 defaults 0 2\n' > "${A53_FSTAB}"
    cp "${A53_FSTAB}" "${case_root}/unrelated"
    : > "${case_root}/calls"
    A53_REQUEST="$(jq -cn --arg id "${A53_OPERATION_ID}" --arg fp "${A53_PLAN_FINGERPRINT}" \
        '{schema_version:"v1",operation_id:$id,plan_fingerprint:$fp,storage:{type:"system",path:"/var/lib/justvoxel/minecraft",mount_point:""},backups:{type:"system",path:"/var/lib/justvoxel/backups",mount_point:""}}')"
    if [[ ${2:-} == nfs || ${2:-} == smb ]]; then
        if [[ $2 == nfs ]]; then live_source=nas:/backups; else live_source=//nas/backups; fi
        A53_REQUEST="$(jq --arg kind "$2" --arg mp "${point}" --arg src "${live_source}" \
            '.backups={type:$kind,mount_point:$mp,path:($mp+"/backups"),source:$src,expected_source:$src,username:"user",domain:""}' <<< "${A53_REQUEST}")"
    fi
    if [[ ${3:-} == existing ]]; then
        printf 'username=old\npassword=old-secret\n' > "${A54_SMB_CREDENTIALS}"
        chmod 0600 "${A54_SMB_CREDENTIALS}"
        cp "${A54_SMB_CREDENTIALS}" "${case_root}/credentials.before"
    fi
    recovery_request="$(jq '{schema_version,operation_id,plan_fingerprint}' <<< "${A53_REQUEST}")"
    _a53_prepare_transaction
    jq -e '.schema_version == "v2" and .fstab_resources == []' "${A53_MANIFEST}" >/dev/null || fail 'new manifest resource schema'
}
local_apply() {
    local target
    target="$(jq -cn --arg mp "${point}" --arg uuid "${uuid}" '{type:"partition",mount_point:$mp,expected_uuid:$uuid,device:"/dev/vdb1",filesystem:"xfs"}')"
    _a53_mount_target "${target}" data
    _a53_manifest_set_rollback failed needs_attention
    mountpoint -q -- "${point}"
    jq -e '.fstab_resources | length == 1 and .[0].action == "created"' "${A53_MANIFEST}" >/dev/null || fail 'local resource creation evidence'
}
recovery_response() {
    # Production rejects deliberately malformed foreign evidence on stderr.
    # Keep those diagnostics separate from the complete, unfiltered JSON reply.
    # Route the production trace pipe separately as well, even when inherited.
    a53_recover_action <<< "${recovery_request}" > "${case_root}/result" \
        2> "${case_root}/diagnostics" 3> "${case_root}/trace" || fail 'recovery helper exited unsuccessfully'
    jq -es 'length == 1 and (.[0] | type == "object")' "${case_root}/result" >/dev/null || {
        cat "${case_root}/diagnostics" >&2
        fail 'recovery did not return exactly one JSON object'
    }
    if [[ ${1:-} == malformed_foreign_config ]]; then
        # This parse failure belongs to the intentionally corrupt config, never
        # the JSON reply. Assert that rejection instead of hiding diagnostics.
        grep -Fq 'parse error:' "${case_root}/diagnostics" || fail 'malformed foreign config was not rejected'
    elif [[ -s ${case_root}/diagnostics ]]; then
        cat "${case_root}/diagnostics" >&2
        fail 'unexpected recovery diagnostic'
    fi
}
recover() {
    recovery_response "${1:-}"
    jq -e '.ok and .rollback_result == "rolled_back"' "${case_root}/result" >/dev/null || fail "recovery: $(cat "${case_root}/result")"
}
committed() {
    local id="$1" role="$2"
    mkdir -p "${A53_INSTANCES_ROOT}/${id}" "${case_root}/server-data/${id}"
    printf 'server contents\n' > "${case_root}/server-data/${id}/world"
    jq -cn --arg mp "${point}" --arg uuid "${uuid}" --arg source "${live_source}" --arg role "${role}" --arg id "${id}" \
        '{schema_version:1,runtime:{id:$id},storage:{mount_point:"",uuid:"",source:""},backups:{mount_point:"",uuid:"",source:""}} | .[$role] = {mount_point:$mp,uuid:$uuid,source:$source} | .storage += {root:"/srv/data",data_path:("/srv/data/minecraft-instances/"+$id+"/data")}' > "${A53_INSTANCES_ROOT}/${id}/config.json"
    cp "${A53_INSTANCES_ROOT}/${id}/config.json" "${case_root}/${id}.expected"
}
assert_preserved() {
    mountpoint -q -- "${point}" || fail 'shared mount was unmounted'
    grep -Fq " ${point} " "${A53_FSTAB}" || fail 'shared persistence removed'
    if grep -q '^umount ' "${case_root}/calls"; then fail 'unmount attempted before preservation'; fi
    jq -e --arg mp "${point}" 'any(.preserved_resources[]; .mountpoint == $mp and (.reason|length > 0))' "${A53_MANIFEST}" >/dev/null || fail 'shared resource retention reason missing'
    for config in "${A53_INSTANCES_ROOT}"/jv-*/config.json; do
        [[ -f ${config} ]] || continue
        local id="${config%/config.json}"; id="${id##*/}"
        cmp "${config}" "${case_root}/${id}.expected"
        grep -Fxq 'server contents' "${case_root}/server-data/${id}/world"
    done
}

# 1: v2 local mount, exact resource deletion, unrelated lines unchanged.
fixture local
local_apply
recover
if mountpoint -q -- "${point}"; then fail 'exclusive local mount retained'; fi
cmp "${A53_FSTAB}" "${case_root}/unrelated"
[[ $(grep -c '^umount ' "${case_root}/calls") == 1 ]]

# 2: another committed server owns a data namespace on the infrastructure.
fixture shared-data
local_apply
committed jv-aaaaaa storage
recover
assert_preserved

# 3: backup-only references are equally authoritative (mandatory regression).
fixture shared-backup
local_apply
committed jv-bbbbbb backups
recover
assert_preserved

# 4: multiple committed servers may share the filesystem.
fixture multiple
local_apply
committed jv-aaaaaa storage
committed jv-bbbbbb backups
recover
assert_preserved

# 5: foreign unfinished operation-bound storage and provisional Setup evidence.
for evidence in storage provisional; do
    fixture "foreign-${evidence}"
    local_apply
    foreign=22345678-1234-4123-8123-123456789abc
    if [[ ${evidence} == storage ]]; then
        mkdir -p "${A53_TRANSACTION_ROOT}/${foreign}"
        jq --arg id "${foreign}" --arg mp "${point}" --arg uuid "${uuid}" \
            '.operation_id=$id | .storage_request.operation_id=$id | .storage_request.backups={type:"partition",mount_point:$mp,expected_uuid:$uuid} | .rollback={state:"not_started",result:""}' \
            "${A53_MANIFEST}" > "${A53_TRANSACTION_ROOT}/${foreign}/manifest.json"
    else
        mkdir -p "${A53_SETUP_INSTANCE_ROOT}"
        jq -cn --arg id "${foreign}" --arg mp "${point}" --arg uuid "${uuid}" \
            '{schema:2,rolled_back:false,request:{operation_id:$id,storage:{type:"system",mount_point:""},backups:{type:"partition",mount_point:$mp,expected_uuid:$uuid}}}' > "${A53_SETUP_INSTANCE_ROOT}/setup-${foreign}.json"
    fi
    recover
    assert_preserved
done

# 6: malformed, unreadable (dangling link), and conflicting identity evidence.
for evidence in malformed unreadable conflict; do
    fixture "uncertain-${evidence}"
    local_apply
    committed jv-aaaaaa storage
    case "${evidence}" in
        malformed) printf '{broken\n' > "${A53_INSTANCES_ROOT}/jv-aaaaaa/config.json" ;;
        unreadable) rm "${A53_INSTANCES_ROOT}/jv-aaaaaa/config.json"; ln -s /does-not-exist "${A53_INSTANCES_ROOT}/jv-aaaaaa/config.json" ;;
        conflict) jq '.storage.uuid="conflicting" | .storage.source="/dev/other"' "${A53_INSTANCES_ROOT}/jv-aaaaaa/config.json" > "${case_root}/changed"; mv "${case_root}/changed" "${A53_INSTANCES_ROOT}/jv-aaaaaa/config.json" ;;
    esac
    if [[ ${evidence} == malformed ]]; then recover malformed_foreign_config; else recover; fi
    mountpoint -q -- "${point}"
    if grep -q '^umount ' "${case_root}/calls"; then fail 'uncertain mount was unmounted'; fi
    grep -Fq " ${point} " "${A53_FSTAB}"
    jq -e '(.preserved_resources|length)>0' "${A53_MANIFEST}" >/dev/null || fail 'uncertain resource retention evidence missing'
done

# 7: unrelated later edits do not require a whole-file hash match.
fixture later-edit
local_apply
printf 'nas:/later /srv/later nfs defaults 0 0\n' >> "${A53_FSTAB}"
printf 'nas:/later /srv/later nfs defaults 0 0\n' >> "${case_root}/unrelated"
recover
cmp "${A53_FSTAB}" "${case_root}/unrelated"
if mountpoint -q -- "${point}"; then fail 'later fstab edit retained exclusive mount'; fi

# 8: a different definition at the same mountpoint is never overwritten.
fixture current-conflict
local_apply
printf 'UUID=other %s xfs defaults 0 2\n' "${point}" > "${A53_FSTAB}"
cp "${A53_FSTAB}" "${case_root}/conflict.expected"
recovery_response
jq -e '(.ok|not) and .rollback_result == "needs_attention"' "${case_root}/result" >/dev/null || fail 'conflicting mount definition accepted'
cmp "${A53_FSTAB}" "${case_root}/conflict.expected"
mountpoint -q -- "${point}"
if grep -q '^umount ' "${case_root}/calls"; then fail 'conflicting mount was unmounted'; fi

# 9: surviving fstab resources retain exact private SMB credentials.
fixture smb-referenced
A53_REQUEST="$(jq '.backups.type="smb"' <<< "${A53_REQUEST}")"
_a54_write_smb_credentials user secret ''
cp "${A54_SMB_CREDENTIALS}" "${case_root}/credentials.expected"
printf '//nas/share /srv/surviving cifs credentials=%s,nofail 0 0\n' "${A54_SMB_CREDENTIALS}" >> "${A53_FSTAB}"
_a53_manifest_set_rollback failed needs_attention
recover
cmp "${A54_SMB_CREDENTIALS}" "${case_root}/credentials.expected"
[[ $(command stat -c '%a' "${A54_SMB_CREDENTIALS}") == 600 ]]
jq -e '.credentials_retained_reason == "surviving_infrastructure_reference"' "${A53_MANIFEST}" >/dev/null || fail 'shared credential retention reason missing'
if grep -q secret "${A53_MANIFEST}"; then fail 'credential secret leaked into manifest'; fi

# The current operation's request, fstab resource and provisional evidence are
# self references. Exclusive network rollback must undo its exact live mount.
for kind in nfs smb; do
    for credential_state in absent existing; do
        [[ ${kind} == smb || ${credential_state} == absent ]] || continue
        fixture "exclusive-${kind}-${credential_state}" "${kind}" "${credential_state}"
        A54_SMB_PASSWORD=secret
        target="$(jq -c '.backups' <<< "${A53_REQUEST}")"
        _a54_mount_network_target "${target}"
        mkdir -p "${A53_SETUP_INSTANCE_ROOT}"
        jq -n --argjson request "${A53_REQUEST}" '{schema:2,rolled_back:false,request:$request}' \
            > "${A53_SETUP_INSTANCE_ROOT}/setup-${A53_OPERATION_ID}.json"
        _a53_manifest_set_rollback failed needs_attention
        recover
        if mountpoint -q -- "${point}"; then fail 'exclusive network mount retained'; fi
        [[ $(grep -c '^umount ' "${case_root}/calls") == 1 ]] || fail 'exclusive mount not unmounted exactly once'
        cmp "${A53_FSTAB}" "${case_root}/unrelated"
        jq -e '(.preserved_resources|length) == 0' "${A53_MANIFEST}" >/dev/null || fail 'self reference preserved infrastructure'
        if [[ ${credential_state} == existing ]]; then
            cmp "${A54_SMB_CREDENTIALS}" "${case_root}/credentials.before"
            [[ $(command stat -c '%a' "${A54_SMB_CREDENTIALS}") == 600 ]] || fail 'restored SMB credential mode'
        else
            [[ ! -e ${A54_SMB_CREDENTIALS} ]] || fail 'exclusive rollback retained new credentials'
        fi
    done
done

# Network mounts follow the same shared-backup preservation contract.
for kind in nfs smb; do
    fixture "shared-${kind}" "${kind}"
    A54_SMB_PASSWORD=secret
    target="$(jq -cn --arg kind "${kind}" --arg mp "${point}" --arg src "${live_source}" '{type:$kind,mount_point:$mp,source:$src,username:"user",domain:""}')"
    _a54_mount_network_target "${target}"
    committed jv-aaaaaa backups
    _a53_manifest_set_rollback failed needs_attention
    recover
    assert_preserved
    if [[ ${kind} == smb ]]; then [[ -f ${A54_SMB_CREDENTIALS} ]]; fi
done

# 10: twice-run recovery performs no second destructive operation.
fixture retry
local_apply
recover
cp "${A53_FSTAB}" "${case_root}/after-first"
cp "${case_root}/calls" "${case_root}/first-calls"
recover
cmp "${A53_FSTAB}" "${case_root}/after-first"
cmp "${case_root}/calls" "${case_root}/first-calls"

legacy_fixture() {
    fixture "$1"
    point=/var/mnt/justvoxel-backup
    historical_line="UUID=${uuid} ${point} xfs noatime,nofail,x-systemd.device-timeout=10s 0 2"
    printf '%s\n' "${historical_line}" >> "${A53_TX_DIR}/fstab.before"
    snapshot_hash="$(_a53_file_sha256 "${A53_TX_DIR}/fstab.before")"
    after_hash="$(_a53_file_sha256 "${A53_FSTAB}")"
    jq --arg before "${snapshot_hash}" --arg after "${after_hash}" --arg point "${point}" --arg src "UUID=${uuid}" --arg uuid "${uuid}" \
        '.schema_version="v1" | del(.fstab_resources) | .fstab_changed=true | .fstab_before_sha256=$before | .fstab_after_sha256=$after | .mounts_by_transaction=[] | .retired_mounts=[{mountpoint:$point,source:$src,uuid:$uuid,was_mounted:true}] | .rollback={state:"failed",result:"needs_attention"}' \
        "${A53_MANIFEST}" > "${case_root}/updated"
    mv "${case_root}/updated" "${A53_MANIFEST}"
    printf '# unrelated later edit\n' >> "${A53_FSTAB}"
}

# 11: historical 5ee3-style retirement restores only its exact old resource.
legacy_fixture historical
recover
mountpoint -q -- "${point}"
grep -Fxq '# unrelated later edit' "${A53_FSTAB}"
grep -Fxq "${historical_line}" "${A53_FSTAB}"
[[ $(grep -Fc "${point}" "${A53_FSTAB}") == 1 ]]
[[ $(grep -c '^mount ' "${case_root}/calls") == 1 ]]
cp "${A53_FSTAB}" "${case_root}/historical-first"
recover
cmp "${A53_FSTAB}" "${case_root}/historical-first"
[[ $(grep -c '^mount ' "${case_root}/calls") == 1 ]]

# 12: historical conflicting definitions are preserved; never mount that target.
legacy_fixture historical-conflict
printf 'UUID=other %s xfs defaults 0 2\n' "${point}" >> "${A53_FSTAB}"
cp "${A53_FSTAB}" "${case_root}/conflict.expected"
recovery_response
jq -e '(.ok|not) and .rollback_result == "needs_attention"' "${case_root}/result" >/dev/null || fail 'historical conflicting definition accepted'
cmp "${A53_FSTAB}" "${case_root}/conflict.expected"
[[ ! -s ${case_root}/calls ]]

printf 'Setup storage resource rollback cases passed.\n'
