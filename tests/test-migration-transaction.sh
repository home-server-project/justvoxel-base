#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/migration-common.sh"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/migration-transport.sh"
fail(){ echo "FAIL: $*" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
# Existing-server data transaction and successful rollback.
mkdir -p "${tmp}/tx-existing/staged" "${tmp}/live"
printf old > "${tmp}/live/state"
printf new > "${tmp}/tx-existing/staged/state"
jv_migration_activate_data "${tmp}/tx-existing" "${tmp}/live" "${tmp}/tx-existing/staged" yes
[[ $(cat "${tmp}/live/state") == new ]] || fail 'existing import activation failed'
jv_migration_rollback_data "${tmp}/tx-existing" "${tmp}/live" yes
[[ $(cat "${tmp}/live/state") == old ]] || fail 'existing import rollback did not restore original data'
[[ $(cat "${tmp}/tx-existing/failed-import/state") == new ]] || fail 'failed candidate was not retained'

# Fresh-server data transaction and rollback to no active DATA_PATH.
mkdir -p "${tmp}/tx-fresh/staged" "${tmp}/fresh-live"
printf fresh > "${tmp}/tx-fresh/staged/state"
jv_migration_activate_data "${tmp}/tx-fresh" "${tmp}/fresh-live" "${tmp}/tx-fresh/staged" no
[[ $(cat "${tmp}/fresh-live/state") == fresh ]] || fail 'fresh import activation failed'
jv_migration_rollback_data "${tmp}/tx-fresh" "${tmp}/fresh-live" no
[[ ! -e ${tmp}/fresh-live ]] || fail 'fresh rollback left active imported DATA_PATH'
[[ -f ${tmp}/tx-fresh/failed-import/state ]] || fail 'fresh failed candidate not retained'

# Failed rollback must preserve recovery evidence rather than cleaning it.
mkdir -p "${tmp}/tx-failed/staged" "${tmp}/failed-live"
printf original > "${tmp}/failed-live/state"
printf candidate > "${tmp}/tx-failed/staged/state"
jv_migration_activate_data "${tmp}/tx-failed" "${tmp}/failed-live" "${tmp}/tx-failed/staged" yes
rm -rf "${tmp}/tx-failed/pre-import"
if jv_migration_rollback_data "${tmp}/tx-failed" "${tmp}/failed-live" yes >/dev/null 2>&1; then
    fail 'rollback without pre-import safety data unexpectedly succeeded'
fi
[[ -d ${tmp}/tx-failed ]] || fail 'failed rollback recovery transaction was removed'
[[ -f ${tmp}/tx-failed/failed-import/state ]] || fail 'failed rollback did not preserve candidate evidence'

# Portable import restores selected runtime files without changing firewall ownership.
stub="${tmp}/stub"
mkdir -p "${stub}" "${tmp}/runtime"
readonly JV_INSTANCE_ID=jv-fixture1
DATA_PATH="${tmp}/runtime-data"
JV_MIGRATION_INSTANCE_CONFIG="${tmp}/runtime/config.json"
minecraft_was_active=yes
mkdir -p "${DATA_PATH}"
printf old-runtime > "${tmp}/runtime/minecraft.env"
printf old-quadlet > "${tmp}/runtime/minecraft-${JV_INSTANCE_ID}.container"
jq -cn --arg id "${JV_INSTANCE_ID}" --arg data "${DATA_PATH}" \
    --arg env "$(sha256sum "${tmp}/runtime/minecraft.env" | awk '{print $1}')" \
    --arg quadlet "$(sha256sum "${tmp}/runtime/minecraft-${JV_INSTANCE_ID}.container" | awk '{print $1}')" \
    '{runtime:{id:$id},storage:{data_path:$data},environment_sha256:$env,quadlet_sha256:$quadlet}' > "${JV_MIGRATION_INSTANCE_CONFIG}"
chmod 0600 "${tmp}/runtime/"*
printf 'tcp 25565 1\nudp 19132 1\ntcp 25566 0\nudp 19133 0\n' > "${tmp}/firewall-state"
cat > "${stub}/systemctl" <<'SH'
#!/usr/bin/bash
[[ $* == daemon-reload ]] || exit 1
SH
cat > "${stub}/install" <<'SH'
#!/usr/bin/bash
set -euo pipefail
# Test-only stand-in for root-owned install -d used by runtime snapshots.
destination="${@: -1}"
mkdir -p -- "${destination}"
chmod 0700 "${destination}"
SH
cat > "${stub}/firewall-cmd" <<'SH'
#!/usr/bin/bash
printf '%s\n' "$*" >> "${JV_TEST_FIREWALL_STATE}.unexpected"
echo 'Portable import must preserve appliance firewall ownership.' >&2
exit 1
SH
chmod +x "${stub}/systemctl" "${stub}/install" "${stub}/firewall-cmd"
old_path="${PATH}"
PATH="${stub}:${PATH}"
export PATH JV_TEST_FIREWALL_STATE="${tmp}/firewall-state"
jv_migration_runtime_paths() {
    printf '%s\n' "${JV_TEST_MIGRATION_ROOT}/runtime/config.json" "${JV_TEST_MIGRATION_ROOT}/runtime/minecraft.env" "${JV_TEST_MIGRATION_ROOT}/runtime/minecraft-${JV_INSTANCE_ID}.container"
}
jv_migration_load_instance() {
    [[ $# == 1 && $1 == "${JV_INSTANCE_ID}" ]] || return 1
    jq -e --arg id "$1" '.runtime.id == $id' "${JV_MIGRATION_INSTANCE_CONFIG}" >/dev/null
}
# Model appliance root ownership only for caller-owned fixture files. Real
# file types, symlinks, permissions, hashes and instance evidence remain checked.
readonly JV_TEST_MIGRATION_ROOT="${tmp}"
stat() {
    if [[ $# == 3 && $1 == -c && $2 == '%u:%g:%a' && $3 == "${JV_TEST_MIGRATION_ROOT}/"* ]]; then
        [[ $(command stat -c '%u:%g' "$3") == "$(id -u):$(id -g)" ]] || return 1
        printf '0:0:%s\n' "$(command stat -c '%a' "$3")"
    else
        command stat "$@"
    fi
}
runtime_tx="${tmp}/.justvoxel-import-runtime"
mkdir -p "${runtime_tx}"
jv_migration_write_state "${runtime_tx}" prepared fixture paper
jv_migration_snapshot_runtime "${runtime_tx}"
cp "${JV_MIGRATION_INSTANCE_CONFIG}" "${tmp}/expected-config"
printf changed > "${tmp}/runtime/minecraft.env"
printf changed > "${tmp}/runtime/minecraft-${JV_INSTANCE_ID}.container"
jv_migration_restore_runtime "${runtime_tx}"
[[ $(cat "${tmp}/runtime/minecraft.env") == old-runtime ]] || fail 'runtime environment was not restored'
[[ $(cat "${tmp}/runtime/minecraft-${JV_INSTANCE_ID}.container") == old-quadlet ]] || fail 'selected Quadlet was not restored'
cmp -s "${JV_MIGRATION_INSTANCE_CONFIG}" "${tmp}/expected-config" || fail 'selected config was not restored'
[[ ! -e ${tmp}/firewall-state.unexpected ]] || fail 'portable import attempted firewall mutation'
awk '$1=="tcp"&&$2==25565&&$3==1{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'old Java firewall state changed'
awk '$1=="udp"&&$2==19132&&$3==1{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'old Bedrock firewall state changed'
awk '$1=="tcp"&&$2==25566&&$3==0{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'candidate Java firewall rule added'
awk '$1=="udp"&&$2==19133&&$3==0{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'candidate Bedrock firewall rule added'
# Foreign instance evidence cannot authorize restoration or erase recovery files.
jq '.instance_id="jv-foreign1"' "${runtime_tx}/evidence.json" > "${runtime_tx}/foreign"
mv "${runtime_tx}/foreign" "${runtime_tx}/evidence.json"
chmod 0600 "${runtime_tx}/evidence.json"
if jv_migration_restore_runtime "${runtime_tx}"; then fail 'foreign instance evidence accepted'; fi
[[ -f ${runtime_tx}/destination-state/config.json ]] || fail 'rejected recovery evidence was discarded'
unset -f stat
PATH="${old_path}"; export PATH

# Temporary mount and SMB credential cleanup.
cleanup_stub="${tmp}/cleanup-stub"
mkdir -p "${cleanup_stub}" "${tmp}/transport-root/mount"
cat > "${cleanup_stub}/mountpoint" <<'SH'
#!/usr/bin/bash
exit 0
SH
cat > "${cleanup_stub}/umount" <<'SH'
#!/usr/bin/bash
printf '%s\n' "$*" >> "${JV_TEST_UMOUNT_LOG}"
exit 0
SH
chmod +x "${cleanup_stub}/mountpoint" "${cleanup_stub}/umount"
old_path="${PATH}"; PATH="${cleanup_stub}:${PATH}"; export PATH JV_TEST_UMOUNT_LOG="${tmp}/umount.log"
JV_MIGRATION_TRANSPORT_ROOT="${tmp}/transport-root"
JV_MIGRATION_OWNED_MOUNT="${tmp}/transport-root/mount"
JV_MIGRATION_SMB_CREDENTIALS="${tmp}/transport-root/smb.credentials"
printf secret > "${JV_MIGRATION_SMB_CREDENTIALS}"
chmod 0600 "${JV_MIGRATION_SMB_CREDENTIALS}"
jv_migration_transport_cleanup
[[ ! -e ${tmp}/transport-root ]] || fail 'temporary transport root/credentials not cleaned'
grep -Fq "${tmp}/transport-root/mount" "${tmp}/umount.log" || fail 'owned temporary mount was not unmounted'
PATH="${old_path}"; export PATH

transaction_api="${repo_root}/mjust/libexec/admin-migration-import-transaction-json"
for text in \
    'final_result_emitted=no' \
    'current_stage=initializing' \
    'transaction_exit()' \
    'no automatic retry was attempted and retained state requires review.'; do
    grep -Fq "${text}" "${transaction_api}" || fail "migration Import transaction diagnostics missing: ${text}"
done

echo 'migration transaction, rollback and transport cleanup tests passed.'
