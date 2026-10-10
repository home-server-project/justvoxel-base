#!/usr/bin/bash
set -uo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/admin-backup-storage-common.sh"
source "${repo_root}/mjust/libexec/admin-backup-storage-apply.sh"
base="$(mktemp -d)"
trap 'rm -rf -- "${base}"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

# Execute the real planning, replacement and rollback functions with all host
# operations redirected to the fixture or mocked. Never touch appliance paths.
require_config() { :; }
validate_storage_path() { [[ $1 == "${base}/"* ]]; }
storage_validate_mountpoint_path() { validate_storage_path "$1"; }
data_disk() { :; }
disk_for_path() { :; }
available_bytes_for_path() { printf '1000\n'; }
filesystem_bytes_for_path() { printf '2000\n'; }
findmnt() { case "$*" in *FSTYPE*) printf 'ext4\n';; *TARGET*) printf '/var\n';; esac; }
mountpoint() { [[ -f "${base}/.test-mounted" && ${@: -1} == "${base}/mount" ]]; }
systemctl() { :; }
chown() { :; }
mount() { [[ ${mount_fails} == no ]] || return 1; : > "${base}/.test-mounted"; }
umount() { rm -f -- "${base}/.test-mounted"; }
jv_exact_mount_identity() { [[ $1 == SOURCE ]] && printf '%s\n' "${test_actual_source}"; }
write_main_config() { printf 'updated\n' > "${JV_CONFIG}"; }
render_runtime() { [[ ${render_fails} == no ]]; }
storage_remove_fstab_mountpoint() {
    local tmp="${base}/fstab.updated"
    awk -v mp="$1" 'NF < 2 || $2 != mp' "${A31_FSTAB}" > "${tmp}"
    mv -- "${tmp}" "${A31_FSTAB}"
}
install() {
    [[ ${@: -1} == /etc/justvoxel || ${@: -1} == "${base}/"* ]] || fail 'unexpected install target'
    local target="${@: -1}"
    [[ ${target} != /etc/justvoxel ]] || target="${base}/etc"
    mkdir -p -- "${target}"
}
mktemp() {
    case "$1" in
        /etc/justvoxel/.smb-backup.credentials.XXXXXX|/tmp/.smb-backup.credentials.XXXXXX)
            [[ ${snapshot_fails} != create ]] || return 1
            command mktemp "${base}/credentials.snapshot.XXXXXX" ;;
        /etc/justvoxel/.smb-backup.credentials.new.XXXXXX)
            command mktemp "${base}/credentials.new.XXXXXX" ;;
        /etc/.fstab.justvoxel-webui.XXXXXX) command mktemp "${base}/fstab.snapshot.XXXXXX" ;;
        "${base}/"*) command mktemp "$@" ;;
        *) fail 'unexpected temporary-file path' ;;
    esac
}
cp() {
    if [[ ${snapshot_fails} == copy && ${@: -1} == "${base}/credentials.snapshot."* ]]; then return 1; fi
    command cp "$@"
}
fixture() {
    rm -f -- "${base}/.test-mounted"
    test_mounted=no mount_fails=no render_fails=no snapshot_fails=no test_actual_source=//saved/backups
    JV_CONFIG_DIR="${base}/config" JV_CONFIG="${base}/config/justvoxel.conf"
    A31_FSTAB="${base}/fstab" A31_SMB_CREDENTIALS="${base}/etc/credentials"
    DATA_PATH="${base}/minecraft"
    BACKUP_TYPE=smb BACKUP_PATH="${base}/mount/backups" BACKUP_MOUNT_POINT="${base}/mount"
    BACKUP_EXPECTED_UUID='' BACKUP_EXPECTED_SOURCE=//saved/backups
    mkdir -p -- "${JV_CONFIG_DIR}" "${base}/etc" "${BACKUP_PATH}"
    printf 'saved configuration\n' > "${JV_CONFIG}"
    printf '//saved/backups %s cifs credentials=%s,nofail 0 0\n' "${BACKUP_MOUNT_POINT}" "${A31_SMB_CREDENTIALS}" > "${A31_FSTAB}"
    printf 'username=old\npassword=saved-secret\n' > "${A31_SMB_CREDENTIALS}"
    chmod 0600 "${A31_SMB_CREDENTIALS}"
    command cp -p -- "${A31_SMB_CREDENTIALS}" "${base}/credentials.expected"
    command cp -- "${A31_FSTAB}" "${base}/fstab.expected"
    command cp -- "${JV_CONFIG}" "${base}/config.expected"
    request="$(jq -cn --arg mount "${BACKUP_MOUNT_POINT}" --arg path "${BACKUP_PATH}" '{type:"smb",path:$path,mount_point:$mount,source:"//saved/backups",username:"reentered",domain:"",password:"fresh-secret"}')"
}
apply() { apply_json <<< "${request}" > "${base}/result"; }
preserved() {
    cmp -s -- "${A31_SMB_CREDENTIALS}" "${base}/credentials.expected" || fail 'credentials changed'
    [[ $(stat -c '%a' "${A31_SMB_CREDENTIALS}") == 600 ]] || fail 'credential mode changed'
    cmp -s -- "${A31_FSTAB}" "${base}/fstab.expected" || fail 'saved mount changed'
    cmp -s -- "${JV_CONFIG}" "${base}/config.expected" || fail 'saved configuration changed'
}

fixture
rm -- "${A31_SMB_CREDENTIALS}"
plan_json <<< "${request}" > "${base}/result"
jq -e '.ok and .changed and .proposed.credentials_required and (.current.expected_source == .proposed.expected_source)' "${base}/result" >/dev/null || fail 'saved SMB recovery did not expose Apply'
apply
jq -e '.ok and .applied' "${base}/result" >/dev/null || fail 'missing credentials could not be restored with an empty domain'
[[ -f "${base}/.test-mounted" && $(stat -c '%a' "${A31_SMB_CREDENTIALS}") == 600 ]] || fail 'recovered mount/credential mode incorrect'
grep -Fqx 'password=fresh-secret' "${A31_SMB_CREDENTIALS}" || fail 'submitted password not installed'
if grep -Eq 'fresh-secret|saved-secret' "${base}/result"; then fail 'password leaked'; fi

for snapshot_fails_case in create copy; do
    fixture
    snapshot_fails=${snapshot_fails_case}
    apply
    jq -e '.ok|not' "${base}/result" >/dev/null || fail 'unsafe snapshot accepted'
    preserved
    [[ ! -f "${base}/.test-mounted" ]] || fail 'mount attempted after snapshot failure'
done

fixture
mount_fails=yes
apply
jq -e '.ok|not' "${base}/result" >/dev/null || fail 'failed SMB mount accepted'
preserved

fixture
test_actual_source=//unexpected/backups
apply
jq -e '.ok|not' "${base}/result" >/dev/null || fail 'mismatched mount source accepted'
preserved

for render_fails_case in no yes; do
    fixture
    render_fails=${render_fails_case}
    # Failure to create an SMB snapshot must not affect an unrelated local action.
    snapshot_fails=create
    request="$(jq -cn --arg path "${base}/local-backups" '{type:"system",path:$path}')"
    apply
    cmp -s -- "${A31_SMB_CREDENTIALS}" "${base}/credentials.expected" || fail 'local apply/rollback modified SMB credentials'
    cmp -s -- "${A31_FSTAB}" "${base}/fstab.expected" || fail 'local apply/rollback modified saved SMB mount'
    if [[ ${render_fails} == no ]]; then
        jq -e '.ok and .applied' "${base}/result" >/dev/null || fail 'local apply failed'
    else
        jq -e '.ok|not' "${base}/result" >/dev/null || fail 'local failure accepted'
        preserved
    fi
done
echo 'SMB credential lifecycle regression checks passed.'
