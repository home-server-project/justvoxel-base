#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/common.sh"
source "${repo_root}/mjust/libexec/storage-common-base.sh"
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-common.sh"
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-apply.sh"
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-actions.sh"

systemctl() { return 0; }
MOUNTED=false
MOUNT_SOURCE=/dev/test
MOUNT_UUID=test-uuid
mountpoint() { [[ ${MOUNTED} == true ]]; }
jv_exact_mount_identity() {
    case "$1" in SOURCE) printf '%s\n' "${MOUNT_SOURCE}" ;; UUID) printf '%s\n' "${MOUNT_UUID}" ;; esac
}
umount() { MOUNTED=false; }
install() {
    [[ $# == 7 && $1 == -d && $2 == -m0700 && $3 == -o && $4 == root && $5 == -g && $6 == root && $7 == "${base}" ]] || return 1
    mkdir -p -m 0700 -- "$7"
}

base="$(mktemp -d)"
trap 'rm -rf -- "${base}"' EXIT
A53_TRANSACTION_ROOT="${base}/transactions"
A53_OPERATION_ID=12345678-1234-4123-8123-123456789abc
A53_PLAN_FINGERPRINT="sha256:$(printf 'a%.0s' {1..64})"
A53_FSTAB="${base}/fstab"
A54_SMB_CREDENTIALS="${base}/credentials"
request="$(jq -cn --arg id "${A53_OPERATION_ID}" --arg fp "${A53_PLAN_FINGERPRINT}" '{schema_version:"v1",operation_id:$id,plan_fingerprint:$fp}')"

fixture() {
    rm -rf -- "${A53_TRANSACTION_ROOT}"
    mkdir -p -- "${A53_TRANSACTION_ROOT}/${A53_OPERATION_ID}"
    A53_TX_DIR="${A53_TRANSACTION_ROOT}/${A53_OPERATION_ID}"
    A53_MANIFEST="${A53_TX_DIR}/manifest.json"
    printf '# keep exactly\n//old/share /var/mnt/justvoxel-backup cifs defaults 0 0\n' > "${A53_TX_DIR}/fstab.before"
    cp -- "${A53_TX_DIR}/fstab.before" "${A53_FSTAB}"
    printf 'UUID=new /var/mnt/justvoxel-data xfs defaults 0 2\n' >> "${A53_FSTAB}"
    printf 'username=old\npassword=old\n' > "${A53_TX_DIR}/smb.credentials.before"
    printf 'username=new\npassword=new\n' > "${A54_SMB_CREDENTIALS}"
    local before after credentials_before credentials_after
    before="$(_a53_file_sha256 "${A53_TX_DIR}/fstab.before")"
    after="$(_a53_file_sha256 "${A53_FSTAB}")"
    credentials_before="$(_a53_file_sha256 "${A53_TX_DIR}/smb.credentials.before")"
    credentials_after="$(_a53_file_sha256 "${A54_SMB_CREDENTIALS}")"
    local uid gid
    uid="$(id -u)"
    gid="$(id -g)"
    jq -cn --arg id "${A53_OPERATION_ID}" --arg fp "${A53_PLAN_FINGERPRINT}" \
        --arg before "${before}" --arg after "${after}" \
        --arg cb "${credentials_before}" --arg ca "${credentials_after}" --arg uid "${uid}" --arg gid "${gid}" \
        '{schema_version:"v1",operation_id:$id,plan_fingerprint:$fp,phase:"storage_rollback",fstab_existed:true,fstab_changed:true,fstab_before_sha256:$before,fstab_after_sha256:$after,fstab_mode:"644",fstab_uid:$uid,fstab_gid:$gid,credentials_existed:true,credentials_changed:true,credentials_before_sha256:$cb,credentials_after_sha256:$ca,credentials_mode:"600",credentials_uid:$uid,credentials_gid:$gid,mounts_by_transaction:[{mountpoint:"/var/mnt/justvoxel-data",source:"/dev/test",uuid:"test-uuid"}],directories_created:[],rollback:{state:"failed",result:"needs_attention"}}' > "${A53_MANIFEST}"
    MOUNTED=false
    MOUNT_SOURCE=/dev/test
    MOUNT_UUID=test-uuid
}

recover() { a53_recover_action <<< "${request}"; }
assert_result() { [[ $(jq -r '.rollback_result' <<< "$1") == "$2" ]] || { echo "unexpected recovery result: $1" >&2; exit 1; }; }

fixture
cp -- "${A53_TX_DIR}/fstab.before" "${A53_FSTAB}"
cp -- "${A53_TX_DIR}/smb.credentials.before" "${A54_SMB_CREDENTIALS}"
assert_result "$(recover)" rolled_back
cmp -s -- "${A53_FSTAB}" "${A53_TX_DIR}/fstab.before"
assert_result "$(recover)" rolled_back

fixture
assert_result "$(recover)" rolled_back
cmp -s -- "${A53_FSTAB}" "${A53_TX_DIR}/fstab.before"
cmp -s -- "${A54_SMB_CREDENTIALS}" "${A53_TX_DIR}/smb.credentials.before"
jq -e '.phase == "storage_rolled_back" and .rollback.state == "succeeded" and .rollback.result == "rolled_back"' "${A53_MANIFEST}" >/dev/null

fixture
printf '# unexpected\n' >> "${A53_FSTAB}"
assert_result "$(recover)" needs_attention
grep -q '^# unexpected$' "${A53_FSTAB}"

fixture
jq '.fstab_changed = false | .fstab_after_sha256 = ""' "${A53_MANIFEST}" > "${base}/manifest.updated"
mv -- "${base}/manifest.updated" "${A53_MANIFEST}"
cp -- "${A53_TX_DIR}/fstab.before" "${A53_FSTAB}"
printf '# unrelated fstab entry\n' >> "${A53_FSTAB}"
cp -- "${A53_FSTAB}" "${base}/fstab.current"
MOUNTED=true
recover > "${base}/result"
assert_result "$(cat "${base}/result")" rolled_back
jq -e '.evidence.fstab_recovery_state == "not_changed"' "${base}/result" >/dev/null
cmp -s -- "${A53_FSTAB}" "${base}/fstab.current"
cmp -s -- "${A54_SMB_CREDENTIALS}" "${A53_TX_DIR}/smb.credentials.before"
[[ ${MOUNTED} == false ]]

for rollback in \
    '{"state":"failed","result":"rolled_back"}' \
    '{"state":"succeeded","result":"needs_attention"}' \
    '{"state":"running","result":"needs_attention"}'; do
    fixture
    jq --argjson rollback "${rollback}" '.rollback = $rollback' "${A53_MANIFEST}" > "${base}/manifest.updated"
    mv -- "${base}/manifest.updated" "${A53_MANIFEST}"
    cp -- "${A53_FSTAB}" "${base}/fstab.current"
    cp -- "${A54_SMB_CREDENTIALS}" "${base}/credentials.current"
    MOUNTED=true
    recover > "${base}/result"
    assert_result "$(cat "${base}/result")" needs_attention
    [[ ${MOUNTED} == true ]]
    cmp -s -- "${A53_FSTAB}" "${base}/fstab.current"
    cmp -s -- "${A54_SMB_CREDENTIALS}" "${base}/credentials.current"
done

fixture
MOUNTED=true
recover > "${base}/result"
assert_result "$(cat "${base}/result")" rolled_back
[[ ${MOUNTED} == false ]]

fixture
MOUNTED=true
MOUNT_SOURCE=/dev/other
recover > "${base}/result"
assert_result "$(cat "${base}/result")" needs_attention
[[ ${MOUNTED} == true ]]

fixture
MOUNTED=true
MOUNT_UUID=other-uuid
recover > "${base}/result"
assert_result "$(cat "${base}/result")" needs_attention
[[ ${MOUNTED} == true ]]

fixture
cp -- "${A53_TX_DIR}/smb.credentials.before" "${A54_SMB_CREDENTIALS}"
assert_result "$(recover)" rolled_back

fixture
jq '.credentials_existed = false | .credentials_before_sha256 = "absent"' "${A53_MANIFEST}" > "${base}/manifest.updated"
mv -- "${base}/manifest.updated" "${A53_MANIFEST}"
assert_result "$(recover)" rolled_back
[[ ! -e ${A54_SMB_CREDENTIALS} ]]

fixture
printf 'unexpected\n' > "${A54_SMB_CREDENTIALS}"
assert_result "$(recover)" needs_attention
grep -q '^unexpected$' "${A54_SMB_CREDENTIALS}"

echo 'Setup storage recovery cases passed.'
