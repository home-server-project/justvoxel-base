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

# Runtime/firewall state rollback with command stubs.
stub="${tmp}/stub"
mkdir -p "${stub}" "${tmp}/runtime"
printf old-runtime > "${tmp}/runtime/a"
printf 'tcp 25565 1\nudp 19132 1\ntcp 25566 0\nudp 19133 0\n' > "${tmp}/firewall-state"
cat > "${stub}/systemctl" <<'SH'
#!/usr/bin/bash
case "${1:-}" in
  is-enabled) echo enabled; exit 0 ;;
  is-active) echo active; exit 0 ;;
  *) exit 0 ;;
esac
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
set -euo pipefail
state="${JV_TEST_FIREWALL_STATE}"
for arg in "$@"; do
  case "$arg" in
    --query-port=*) spec="${arg#--query-port=}"; proto="${spec#*/}"; port="${spec%/*}"; awk -v p="$proto" -v n="$port" '$1==p && $2==n {exit $3!=1} END {if(NR==0) exit 1}' "$state"; exit $? ;;
    --add-port=*) spec="${arg#--add-port=}"; proto="${spec#*/}"; port="${spec%/*}"; awk -v p="$proto" -v n="$port" 'BEGIN{f=0} $1==p&&$2==n{$3=1;f=1} {print} END{if(!f) print p,n,1}' "$state" > "$state.tmp"; mv "$state.tmp" "$state"; exit 0 ;;
    --remove-port=*) spec="${arg#--remove-port=}"; proto="${spec#*/}"; port="${spec%/*}"; awk -v p="$proto" -v n="$port" 'BEGIN{f=0} $1==p&&$2==n{$3=0;f=1} {print} END{if(!f) print p,n,0}' "$state" > "$state.tmp"; mv "$state.tmp" "$state"; exit 0 ;;
  esac
done
exit 0
SH
chmod +x "${stub}/systemctl" "${stub}/install" "${stub}/firewall-cmd"
old_path="${PATH}"
PATH="${stub}:${PATH}"
export PATH JV_TEST_FIREWALL_STATE="${tmp}/firewall-state"
jv_migration_runtime_paths() { printf '%s\n' "${tmp}/runtime/a" "${tmp}/runtime/b"; }
mkdir -p "${tmp}/runtime-tx"
jv_migration_snapshot_runtime "${tmp}/runtime-tx" 25565 yes 19132 25566 yes 19133
printf changed > "${tmp}/runtime/a"
printf generated > "${tmp}/runtime/b"
"${stub}/firewall-cmd" --permanent --remove-port=25565/tcp
"${stub}/firewall-cmd" --permanent --remove-port=19132/udp
"${stub}/firewall-cmd" --permanent --add-port=25566/tcp
"${stub}/firewall-cmd" --permanent --add-port=19133/udp
jv_migration_restore_runtime "${tmp}/runtime-tx"
[[ $(cat "${tmp}/runtime/a") == old-runtime ]] || fail 'runtime rollback did not restore previous file'
[[ ! -e ${tmp}/runtime/b ]] || fail 'runtime rollback did not remove newly generated file'
awk '$1=="tcp"&&$2==25565&&$3==1{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'old Java firewall state not restored'
awk '$1=="udp"&&$2==19132&&$3==1{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'old Bedrock firewall state not restored'
awk '$1=="tcp"&&$2==25566&&$3==0{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'candidate Java firewall rule not removed'
awk '$1=="udp"&&$2==19133&&$3==0{ok=1} END{exit !ok}' "${tmp}/firewall-state" || fail 'candidate Bedrock firewall rule not removed'
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
