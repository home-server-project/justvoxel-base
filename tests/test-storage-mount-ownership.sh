#!/usr/bin/bash
# Behavioral regression harness: all appliance commands are mocked; no real disks.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf -- "${work}"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

# Load production functions without invoking their command entrypoints.
python3 - "${repo_root}" "${work}" <<'PY'
from pathlib import Path
import sys
root, work = map(Path, sys.argv[1:])
for name, dispatch in [('admin-storage-mounts-json', 'case "${1:-status}" in'),
                       ('admin-storage-actions-json', 'action="${1:-plan}"')]:
    text = (root / 'mjust/libexec' / name).read_text()
    text = text[:text.rindex(dispatch)]
    text = '\n'.join(line for line in text.splitlines()
                     if not line.startswith('source ') and line not in ['require_root', 'storage_action_load_config'])
    (work / name).write_text(text + '\n')
PY
# Only shared policy functions are loaded from this file.
source <(sed '/^source /d' "${repo_root}/mjust/libexec/admin-storage-actions-common.sh")
source "${repo_root}/mjust/libexec/storage-mount-ownership.sh"
source "${work}/admin-storage-mounts-json"

old=5f99d8c9-443b-42b6-963e-d60242de9944
new=de6fca50-875a-4b67-b1f3-12e486e82c98
other=11111111-2222-3333-4444-555555555555
point="${work}/vdc1"
STORAGE_MOUNT_FSTAB="${work}/fstab"
STORAGE_MOUNT_STATE_DIR="${work}/storage-mounts"
JV_CONFIG="${work}/missing-config"
JV_SETUP_IN_PROGRESS="${work}/setup-in-progress"
STORAGE_ACTION_CONFIGURED=false
admin_row='UUID=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee /var/mnt/admin xfs defaults 0 0'

# Ignore root ownership arguments so this harness also works as an ordinary user.
install() {
    local -a args=()
    while (( $# )); do
        case "$1" in -o|-g) shift 2 ;; *) args+=("$1"); shift ;; esac
    done
    command install "${args[@]}"
}
storage_action_real_device() { printf '%s\n' "$1"; }
storage_action_validate_target() { [[ "$1" == /dev/vdc1 || "$1" == /dev/vdc ]]; }
storage_validate_partition() { [[ "$1" == /dev/vdc1 ]]; }
storage_validate_mountpoint_path() { [[ "$1" == "${work}/"* ]]; }
storage_action_filesystem() { printf '%s\n' "${selected_fs}"; }
storage_action_uuid() { cat "${work}/uuid"; }
storage_action_size_bytes() { printf '1000000000\n'; }
storage_action_role() { printf 'Available\n'; }
storage_action_fingerprint() { printf 'reviewed-device\n'; }
storage_action_host_mountpoints() { [[ ! -f ${work}/mounted ]] || cat "${work}/mounted"; return 0; }
storage_action_mountpoint() { storage_action_host_mountpoints "$1"; }
storage_mount_point_state() {
    [[ ${mount_probe_fail} == false ]] || return 1
    if [[ -f ${work}/mounted && $(cat "${work}/mounted") == "$1" ]]; then printf 'mounted\n'; else printf 'unmounted\n'; fi
}
storage_action_host_mountpoint_occupied() { [[ -f ${work}/mounted && $(cat "${work}/mounted") == "$1" ]]; }
storage_action_host_mount_identity() {
    [[ -f ${work}/mounted && $(cat "${work}/mounted") == "$2" ]] || return 1
    case "$1" in UUID) printf '%s\n' "${live_uuid:-$(cat "${work}/uuid")}" ;; SOURCE) printf '%s\n' "${live_device:-/dev/vdc1}" ;; esac
}
storage_action_host_mount() {
    [[ ${mount_fail} == false ]] || return 1
    printf '%s\n' "$1" > "${work}/mounted"
    printf 'mount\n' >> "${work}/events"
}
storage_action_host_unmount() {
    [[ ${unmount_fail} == false ]] || return 1
    rm -f -- "${work}/mounted"
    printf 'unmount\n' >> "${work}/events"
}
systemctl() {
    printf '%s\n' "$*" >> "${work}/events"
    if [[ ${reload_fail} == true && "$1" == daemon-reload ]]; then reload_fail=false; return 1; fi
}
blkid() {
    local arg requested=''
    for arg in "$@"; do [[ ${arg} != UUID=* ]] || requested="${arg#UUID=}"; done
    [[ ${probe_fail} == false ]] || return 4
    if [[ ${requested} == "$(cat "${work}/uuid")" ]]; then
        printf '%s\n' "${selected_device:-/dev/vdc1}"
        [[ ${cloned_uuid} == false ]] || printf '/dev/vdd1\n'
    elif [[ ${requested} == "${old}" && ${old_exists} == true ]]; then printf '/dev/vdd1\n'
    else return 2; fi
}
fixture() {
    rm -rf -- "${STORAGE_MOUNT_STATE_DIR}"
    rm -f -- "${work}/mounted" "${work}/events" "${work}/destroyed"
    rm -f -- "${JV_SETUP_IN_PROGRESS}"
    mkdir -p -- "${point}"
    selected_fs=xfs; selected_device=/dev/vdc1
    old_exists=false; cloned_uuid=false; probe_fail=false; mount_probe_fail=false
    mount_fail=false; unmount_fail=false; reload_fail=false
    live_uuid=''; live_device=''
    DATA_EXPECTED_UUID=''; BACKUP_EXPECTED_UUID=''; DATA_MOUNT_POINT=''; BACKUP_MOUNT_POINT=''
    printf '%s\n' "${new}" > "${work}/uuid"
    stale="$(storage_mount_fstab_line "${old}" "${point}" xfs)"
    printf '# Administrator configuration\n%s\n%s\n' "${admin_row}" "${stale}" > "${STORAGE_MOUNT_FSTAB}"
}
request() { jq -n --arg operation "${1:-persist}" --arg point "${point}" --arg fingerprint "${2:-}" '{operation:$operation,device:"/dev/vdc1",mount_point:$point,fingerprint:$fingerprint}'; }
assert_admin() { grep -Fxq "${admin_row}" "${STORAGE_MOUNT_FSTAB}" || fail 'Administrator row changed'; }
assert_rejected() {
    cp -- "${STORAGE_MOUNT_FSTAB}" "${work}/before"
    result="$(plan_json <<< "$(request)")"
    jq -e '.ok == false' <<< "${result}" >/dev/null || fail "unsafe plan accepted: $1"
    result="$(apply_json <<< "$(request persist ignored)")"
    jq -e '.ok == false' <<< "${result}" >/dev/null || fail "unsafe apply accepted: $1"
    cmp -s "${work}/before" "${STORAGE_MOUNT_FSTAB}" || fail "rejected operation changed fstab: $1"
}

# Exact VM failure: status remains unowned, normal plan/apply recovers it.
fixture
result="$(status_json <<< '{"device":"/dev/vdc1"}')"
jq -e '.ok and .proposed.persistence == "none" and .proposed.mounted == false' <<< "${result}" >/dev/null || fail 'unexpected orphan status'
result="$(plan_json <<< "$(request remove)")"
jq -e '.ok == false' <<< "${result}" >/dev/null || fail 'orphan was adopted outside explicit persist'
result="$(plan_json <<< "$(request)")"
jq -e '.ok and .proposed.uuid == "de6fca50-875a-4b67-b1f3-12e486e82c98"' <<< "${result}" >/dev/null || fail 'real orphan plan rejected'
fingerprint="$(jq -r '.proposed.fingerprint' <<< "${result}")"
result="$(apply_json <<< "$(request persist "${fingerprint}")")"
jq -e '.ok and .applied and .proposed.mounted and .proposed.persistence == "justvoxel"' <<< "${result}" >/dev/null || fail 'real orphan apply failed'
! grep -Fq "UUID=${old}" "${STORAGE_MOUNT_FSTAB}" || fail 'old row survived'
grep -Fxq "$(storage_mount_fstab_line "${new}" "${point}" xfs)" "${STORAGE_MOUNT_FSTAB}" || fail 'new row missing'
storage_mount_load_state "${new}" || fail 'new ownership missing'
[[ $(storage_action_host_mount_identity UUID "${point}") == "${new}" ]] || fail 'live UUID mismatch'
assert_admin

# Active first-run setup blocks orphan recovery even without configuration.
fixture
[[ ! -e ${JV_CONFIG} ]] || fail 'setup regression requires absent configuration'
result="$(plan_json <<< "$(request)")"
jq -e '.ok' <<< "${result}" >/dev/null || fail 'setup regression orphan plan rejected before setup'
fingerprint="$(jq -r '.proposed.fingerprint' <<< "${result}")"
touch "${JV_SETUP_IN_PROGRESS}"
cp -- "${STORAGE_MOUNT_FSTAB}" "${work}/before"
result="$(plan_json <<< "$(request)")"
jq -e '.ok == false' <<< "${result}" >/dev/null || fail 'active setup orphan plan accepted'
result="$(apply_json <<< "$(request persist "${fingerprint}")")"
jq -e '.ok == false' <<< "${result}" >/dev/null || fail 'active setup orphan apply accepted'
cmp -s "${work}/before" "${STORAGE_MOUNT_FSTAB}" || fail 'active setup rejection changed fstab'

for negative in existing_uuid duplicate conflicting occupied options filesystem critical contradictory old_ownership configured malformed admin old_elsewhere cloned probe_error mount_probe_error; do
    fixture
    case "${negative}" in
        existing_uuid) old_exists=true ;;
        duplicate) printf '%s\n' "${stale}" >> "${STORAGE_MOUNT_FSTAB}" ;;
        conflicting) printf '/dev/vdd1 %s xfs defaults 0 0\n' "${point}" >> "${STORAGE_MOUNT_FSTAB}" ;;
        occupied) printf '%s\n' "${point}" > "${work}/mounted" ;;
        options) sed -i 's/noatime,nofail/noatime,rw,nofail/' "${STORAGE_MOUNT_FSTAB}" ;;
        filesystem) selected_fs=ext4 ;;
        critical) point_before="${point}"; point=/etc ;;
        contradictory) storage_mount_write_state "${other}" "${point}" xfs false ;;
        old_ownership) storage_mount_write_state "${old}" "${point}" xfs false ;;
        configured) DATA_EXPECTED_UUID="${old}" ;;
        malformed) storage_mount_write_state "${old}" "${point}" xfs false; printf 'schema=v1\n' >> "${STORAGE_MOUNT_STATE_DIR}/${old}.conf" ;;
        admin) sed -i 's/noatime,nofail,x-systemd.device-timeout=10s/defaults/' "${STORAGE_MOUNT_FSTAB}" ;;
        old_elsewhere) printf 'UUID=%s %s/elsewhere xfs defaults 0 0\n' "${old}" "${work}" >> "${STORAGE_MOUNT_FSTAB}" ;;
        cloned) cloned_uuid=true ;;
        probe_error) probe_fail=true ;;
        mount_probe_error) mount_probe_fail=true ;;
    esac
    assert_rejected "${negative}"
    [[ ${negative} != critical ]] || point="${point_before}"
done

# Apply re-plans: newly surviving UUID/ownership or edited row invalidates Review.
for change in uuid ownership row; do
    fixture
    result="$(plan_json <<< "$(request)")"; fingerprint="$(jq -r '.proposed.fingerprint' <<< "${result}")"
    case "${change}" in
        uuid) old_exists=true ;;
        ownership) storage_mount_write_state "${old}" "${point}" xfs false ;;
        row) sed -i 's/noatime,nofail/noatime,rw,nofail/' "${STORAGE_MOUNT_FSTAB}" ;;
    esac
    cp -- "${STORAGE_MOUNT_FSTAB}" "${work}/before"
    result="$(apply_json <<< "$(request persist "${fingerprint}")")"
    jq -e '.ok == false' <<< "${result}" >/dev/null || fail 'changed evidence accepted'
    cmp -s "${work}/before" "${STORAGE_MOUNT_FSTAB}" || fail 'changed evidence overwritten'
done

for failure in mount identity reload state_write; do
    fixture
    result="$(plan_json <<< "$(request)")"; fingerprint="$(jq -r '.proposed.fingerprint' <<< "${result}")"
    cp -- "${STORAGE_MOUNT_FSTAB}" "${work}/before"
    case "${failure}" in
        mount) mount_fail=true ;;
        identity) live_uuid="${old}" ;;
        reload) reload_fail=true ;;
        state_write) mkdir -p "${STORAGE_MOUNT_STATE_DIR}"; chmod 0500 "${STORAGE_MOUNT_STATE_DIR}" ;;
    esac
    # Inject a write error deterministically even when the harness runs as root.
    if [[ ${failure} == state_write ]]; then
        result="$(storage_mount_write_state() { return 1; }; apply_json <<< "$(request persist "${fingerprint}")")"
    else result="$(apply_json <<< "$(request persist "${fingerprint}")")"; fi
    jq -e '.ok == false' <<< "${result}" >/dev/null || fail 'transaction failure accepted'
    cmp -s "${work}/before" "${STORAGE_MOUNT_FSTAB}" || fail 'rollback lost stale row'
    [[ ! -e ${STORAGE_MOUNT_STATE_DIR}/${new}.conf && ! -f ${work}/mounted ]] || fail 'rollback left state or live mount'
    chmod 0700 "${STORAGE_MOUNT_STATE_DIR}" 2>/dev/null || true
    assert_admin
done

owned_fixture() {
    fixture
    printf '%s\n%s\n' "${admin_row}" "$(storage_mount_fstab_line "${new}" "${point}" xfs)" > "${STORAGE_MOUNT_FSTAB}"
    storage_mount_write_state "${new}" "${point}" xfs false
    printf '%s\n' "${point}" > "${work}/mounted"
}
assert_retired() {
    [[ ! -e ${STORAGE_MOUNT_STATE_DIR}/${new}.conf ]] || fail 'ownership survived destruction'
    ! grep -Fq "UUID=${new}" "${STORAGE_MOUNT_FSTAB}" || fail 'owned fstab row survived destruction'
    assert_admin
}

# Invoke the real destructive Apply function with a reviewed plan and mocked
# destructive commands. Each command asserts retirement happened before erasure.
source "${repo_root}/mjust/libexec/admin-storage-actions-apply.sh"
storage_mkfs_xfs() { assert_retired; printf 'format\n' > "${work}/destroyed"; }
wipefs() { assert_retired; printf 'initialize_disk\n' > "${work}/destroyed"; }
parted() { if [[ "$*" == *' rm '* ]]; then assert_retired; printf 'delete_partition\n' > "${work}/destroyed"; fi; }
partprobe() { :; }
udevadm() { :; }
lsblk() { :; }
storage_partition_table_type() { printf 'gpt\n'; }
storage_action_parent_disk() { printf '/dev/vdc\n'; }
cat() {
    if [[ ${1:-} == /sys/class/block/vdc1/partition ]]; then printf '1\n'; else command cat "$@"; fi
}
# Only plan preparation is stubbed here; destructive Apply and retirement are real.
prepare_plan() {
    REQUEST_JSON="$(cat)"
    PLAN_WARNINGS='[]'
    PLAN_JSON="$(jq -n --arg op "${destructive_op}" --arg device "${selected_device}" --arg point "${point}" --arg uuid "${new}" '{operation:$op,device:$device,mount_point:"",current_mount_point:$point,confirmation:"ERASE",fingerprint:"reviewed",destructive:true,role:"Available",uuid:$uuid,filesystem:"xfs"}')"
}
for destructive_op in format delete_partition initialize_disk; do
    owned_fixture
    neighbor_row="$(storage_mount_fstab_line "${other}" "${work}/neighbor" xfs)"
    printf '%s\n' "${neighbor_row}" >> "${STORAGE_MOUNT_FSTAB}"
    storage_mount_write_state "${other}" "${work}/neighbor" xfs false
    cp -- "${STORAGE_MOUNT_STATE_DIR}/${other}.conf" "${work}/neighbor-state"
    [[ ${destructive_op} != initialize_disk ]] || selected_device=/dev/vdc
    live_device="${selected_device}"
    result="$(storage_action_apply_json <<< '{"fingerprint":"reviewed","confirmation":"ERASE"}')"
    jq -e '.ok and .applied' <<< "${result}" >/dev/null || fail "destructive Apply failed: ${destructive_op}"
    [[ $(cat "${work}/destroyed") == "${destructive_op}" ]] || fail 'destruction did not execute'
    assert_retired
    grep -Fxq "${neighbor_row}" "${STORAGE_MOUNT_FSTAB}" || fail 'neighbor mount row changed'
    cmp -s "${work}/neighbor-state" "${STORAGE_MOUNT_STATE_DIR}/${other}.conf" || fail 'neighbor ownership changed'
    grep -Fxq daemon-reload "${work}/events" || fail 'retirement did not reload systemd'
done

# Owned mismatch must prevent erasure, unmounting, and row changes.
for mismatch in row uuid source duplicate_record unmount reload; do
    owned_fixture
    case "${mismatch}" in
        row) sed -i 's/noatime,nofail/defaults,nofail/' "${STORAGE_MOUNT_FSTAB}" ;;
        uuid) live_uuid="${old}" ;;
        source) live_device=/dev/vdd1 ;;
        duplicate_record) storage_mount_write_state "${other}" "${point}" xfs false ;;
        unmount) unmount_fail=true ;;
        reload) reload_fail=true ;;
    esac
    cp -- "${STORAGE_MOUNT_FSTAB}" "${work}/before"
    destructive_op=format
    result="$(storage_action_apply_json <<< '{"fingerprint":"reviewed","confirmation":"ERASE"}')"
    jq -e '.ok == false' <<< "${result}" >/dev/null || fail "unsafe erasure accepted: ${mismatch}"
    [[ ! -e ${work}/destroyed && -f ${work}/mounted && -f ${STORAGE_MOUNT_STATE_DIR}/${new}.conf ]] || fail 'failed retirement lost state'
    cmp -s "${work}/before" "${STORAGE_MOUNT_FSTAB}" || fail 'failed retirement changed fstab'
done

# Factory Reset's generic stage uses these real helpers before config deletion.
owned_fixture
storage_mount_retire_all || fail 'generic reset retirement failed'
assert_retired
[[ ! -f ${work}/mounted ]] || fail 'reset left generic mount live'
# Ownership without a current filesystem is still safe to retire when unmounted.
owned_fixture
rm -f -- "${work}/mounted"
printf '%s\n' "${old}" > "${work}/uuid"
storage_mount_retire_all || fail 'reset could not retire proven absent filesystem'
assert_retired
# Preflight every reset record before touching any.
owned_fixture
storage_mount_write_state "${other}" "${work}/other" xfs false
cp -- "${STORAGE_MOUNT_FSTAB}" "${work}/before"
if storage_mount_retire_all; then fail 'reset accepted mismatched record'; fi
cmp -s "${work}/before" "${STORAGE_MOUNT_FSTAB}" || fail 'reset mutated before full validation'
[[ -f ${work}/mounted ]] || fail 'reset unmounted before full validation'
# Administrator-only entries are never adopted or retired.
fixture
storage_mount_retire_device /dev/vdc1 || fail 'unowned device rejected'
storage_mount_retire_all || fail 'empty reset ownership rejected'
grep -Fxq "${stale}" "${STORAGE_MOUNT_FSTAB}" || fail 'unowned historical-looking row retired'
assert_admin

# Run the actual unconfigured Full Factory Reset entrypoint in a subshell.
# Redirect fixed appliance paths into the fixture and mock runtime commands.
python3 - "${repo_root}" "${work}" <<'PY'
from pathlib import Path
import sys
root, work = map(Path, sys.argv[1:])
text = (root / 'mjust/libexec/admin-factory-reset-json').read_text()
text = '\n'.join(line for line in text.splitlines() if not line.startswith('source '))
text = text.replace('local fstab=/etc/fstab', 'local fstab="${STORAGE_MOUNT_FSTAB}"')
text = text.replace('config_backup_dir=/var/lib/justvoxel/config-backups', 'config_backup_dir="${work}/config-backups"')
(work / 'factory-reset').write_text(text + '\n')
PY
require_root() { :; }
jv_reset_remove_minecraft_container() { :; }
jv_reset_delete_tree_same_filesystem() { :; }
systemd-escape() { printf 'fixture.mount\n'; }
mountpoint() { storage_action_host_mountpoint_occupied "${@: -1}"; }
podman() { return 1; }
STORAGE_MOUNT_STATE_DIR="${work}/config/storage-mounts"
JV_CONFIG_DIR="${work}/config"
JV_CONFIG="${JV_CONFIG_DIR}/justvoxel.conf"
JV_QUADLET="${work}/minecraft.container"
JV_BACKUP_SERVICE="${work}/backup.service"
JV_BACKUP_TIMER="${work}/backup.timer"
JV_PREVIOUS_IMAGE_STATE="${work}/previous-image"
owned_fixture
result="$(
    systemctl() { [[ ${1:-} != is-active ]]; }
    set -- apply --confirm-players
    source "${work}/factory-reset"
)"
jq -e '.ok and .external_storage_action == "preserve" and .network_storage_action == "preserve" and .storage_layout_action == "preserve"' <<< "${result}" >/dev/null || fail 'Full Factory Reset failed'
assert_retired
[[ ! -d ${JV_CONFIG_DIR} && ! -f ${work}/mounted ]] || fail 'Full Factory Reset left config or live mount'
owned_fixture
sed -i 's/noatime,nofail/defaults,nofail/' "${STORAGE_MOUNT_FSTAB}"
cp -- "${STORAGE_MOUNT_FSTAB}" "${work}/before"
if result="$(
    systemctl() { [[ ${1:-} != is-active ]]; }
    set -- apply --confirm-players
    source "${work}/factory-reset"
)"; then fail 'Full Factory Reset accepted mismatched generic ownership'; fi
jq -e '.ok == false and .code == "mount_cleanup_failed"' <<< "${result}" >/dev/null || fail 'unexpected reset failure'
[[ -f ${STORAGE_MOUNT_STATE_DIR}/${new}.conf && -f ${work}/mounted ]] || fail 'failed reset removed ownership or unmounted'
cmp -s "${work}/before" "${STORAGE_MOUNT_FSTAB}" || fail 'failed reset changed fstab'

python3 - "${repo_root}" <<'PY'
from pathlib import Path
import sys
text = (Path(sys.argv[1]) / 'mjust/libexec/admin-factory-reset-json').read_text()
assert text.index('if ! storage_mount_retire_all') < text.index('rm -rf -- "${JV_CONFIG_DIR}"')
assert 'external_storage_action:"preserve"' in text
assert 'network_storage_action:"preserve"' in text
PY
printf 'Generic mount compatibility and lifecycle regressions passed.\n'
