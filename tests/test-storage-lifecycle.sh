#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$repo_root/mjust/libexec/storage-lifecycle.sh"
source "$repo_root/mjust/libexec/minecraft-reset-common.sh"
source "$repo_root/mjust/libexec/admin-backup-storage-apply.sh"
TMP="$(mktemp -d)"
trap 'rm -rf -- "$TMP"' EXIT
fail(){ echo "FAIL: $*" >&2; exit 1; }
# Only ownership is simulated; mode, content, links, symlinks are checked for real.
stat(){
    if [[ ${2:-} == '%u:%g:%a:%h' ]]; then
        printf '0:0:%s\n' "$(command stat -c '%a:%h' -- "${@: -1}")"
    else command stat "$@"; fi
}
chown(){ :; }
firewall-cmd(){ :; }
systemctl(){
    printf '%s\n' "$*" >> "$TMP/systemctl.log"
    case "${1:-}:${2:-}" in
        show:--property=LoadState) printf 'loaded\n' ;;
        show:--property=ActiveState) printf 'inactive\n' ;;
    esac
}
JV_CONFIG_DIR="$TMP/config"
JV_CONFIG="$JV_CONFIG_DIR/justvoxel.conf"
JV_FSTAB="$TMP/fstab"
JV_QUADLET="$TMP/quadlet"
JV_BACKUP_SERVICE="$TMP/backup.service"
JV_BACKUP_TIMER="$TMP/backup.timer"
JV_PREVIOUS_IMAGE_STATE="$TMP/previous-image"
credentials(){
    mkdir -p "$JV_CONFIG_DIR"
    printf 'username=backup\npassword=secret\n' > "$JV_CONFIG_DIR/smb-backup.credentials"
    chmod 0600 "$JV_CONFIG_DIR/smb-backup.credentials"
}
BACKUP_TYPE=smb
BACKUP_MOUNT_POINT=/var/mnt/justvoxel-backup
BACKUP_EXPECTED_SOURCE=//nas/backups
credentials
printf '//nas/backups %s cifs credentials=%s/smb-backup.credentials,nofail 0 0\n' "$BACKUP_MOUNT_POINT" "$JV_CONFIG_DIR" > "$JV_FSTAB"
cp "$JV_FSTAB" "$TMP/reset.fstab.expected"
cp "$JV_CONFIG_DIR/smb-backup.credentials" "$TMP/credentials.expected"
# Start Over still preserves the appliance storage configuration.
grep -Fq 'StorageLayoutAction: "preserve"' "$repo_root/management/cmd/justvoxel-management-agent/admin_start_over.go" || fail 'Start Over did not request preservation'
printf 'configuration\n' > "$JV_CONFIG"
jv_reset_remove_active_configuration 25565 no 19132 preserve-storage
cmp "$JV_FSTAB" "$TMP/reset.fstab.expected"
cmp "$JV_CONFIG_DIR/smb-backup.credentials" "$TMP/credentials.expected"
[[ $(command stat -c %a "$JV_CONFIG_DIR/smb-backup.credentials") == 600 ]] || fail 'credential mode changed'
[[ ! -e $JV_CONFIG ]] || fail 'active configuration survived'
chmod 0644 "$JV_CONFIG_DIR/smb-backup.credentials"
if jv_reset_validate_storage_preservation; then fail 'unsafe mode accepted'; fi
chmod 0600 "$JV_CONFIG_DIR/smb-backup.credentials"
mv "$JV_CONFIG_DIR/smb-backup.credentials" "$TMP/real-credentials"
ln -s "$TMP/real-credentials" "$JV_CONFIG_DIR/smb-backup.credentials"
if jv_reset_validate_storage_preservation; then fail 'symlink accepted'; fi
rm "$JV_CONFIG_DIR/smb-backup.credentials"
if jv_reset_validate_storage_preservation; then fail 'missing credentials accepted'; fi
credentials
printf 'malformed\n' >> "$JV_CONFIG_DIR/smb-backup.credentials"
if jv_reset_validate_storage_preservation; then fail 'malformed credentials accepted'; fi
credentials
# Factory Reset owns configuration removal after all-instance retirement.
grep -Fq 'jv_trace_run rm -rf -- "${JV_CONFIG_DIR}"' "$repo_root/mjust/libexec/admin-factory-reset-json"
jv_reset_remove_active_configuration 25565 no 19132
[[ ! -e $JV_CONFIG_DIR ]] || fail 'factory mode preserved credentials'

# Exercise the real backup apply with all file mutations confined to this fixture.
mkdir(){
    local -a args=()
    local arg
    for arg in "$@"; do
        case "$arg" in
            /var/mnt/justvoxel-backup/backups) args+=("$TMP/managed-backup/backups") ;;
            *) args+=("$arg") ;;
        esac
    done
    command mkdir "${args[@]}"
}
mktemp(){
    case "${1:-}" in /etc/*) command mktemp "$TMP/temporary.XXXXXX" ;; *) command mktemp "$@" ;; esac
}
install(){
    local -a args=()
    while (( $# )); do
        case "$1" in -o|-g) shift 2 ;; /etc/justvoxel) args+=("$JV_CONFIG_DIR"); shift ;; *) args+=("$1"); shift ;; esac
    done
    command install "${args[@]}"
}
A31_FSTAB="$JV_FSTAB"
A31_SMB_CREDENTIALS="$JV_CONFIG_DIR/smb-backup.credentials"
validate_request(){ return 0; }
current_status_json(){ printf '{"current":{}}\n'; }
proposed_json(){ printf '{}\n'; }
json_error(){ jq -n --arg error "$1" '{ok:false,error:$error}'; }
available_bytes_for_path(){ printf '1\n'; }
filesystem_bytes_for_path(){ printf '1\n'; }
write_probe(){ return 0; }
write_main_config(){ printf 'new configuration\n' > "$JV_CONFIG"; [[ $COMMIT_FAIL == no ]]; }
render_runtime(){ return 0; }
DATA_PATH="$TMP/minecraft"
DATA_MOUNT_POINT=''
WARNINGS='[]'
TARGET_PATH="$TMP/new/backups"
TARGET_MOUNT="$TMP/new"
TARGET_UUID=new-uuid
TARGET_FILESYSTEM=xfs
TARGET_SOURCE=//new/backups
TARGET_USERNAME=backup
TARGET_PASSWORD=new-secret
TARGET_DOMAIN=''
TARGET_EXPECTED_SOURCE=/dev/new
TARGET_NEEDS_CREDENTIALS=false
TARGET_ALREADY_MOUNTED=false
COMMIT_FAIL=no
OLD_MOUNTED=yes
NEW_MOUNTED=no
IDENTITY_MISMATCH=no
mountpoint(){
    local point="${@: -1}"
    [[ $point == /var/mnt/justvoxel-backup && $OLD_MOUNTED == yes || $point == "$TARGET_MOUNT" && $NEW_MOUNTED == yes ]]
}
jv_exact_mount_identity(){
    if [[ $2 == /var/mnt/justvoxel-backup ]]; then
        if [[ $IDENTITY_MISMATCH == yes ]]; then printf 'wrong\n';
        elif [[ $1 == UUID ]]; then printf 'old-uuid\n'; else printf '%s\n' "$BACKUP_EXPECTED_SOURCE"; fi
    elif [[ $1 == UUID ]]; then printf '%s\n' "$TARGET_UUID";
    elif [[ $TARGET_TYPE == smb ]]; then printf '%s\n' "$TARGET_SOURCE";
    else printf '/dev/new\n'; fi
}
mount(){ if [[ $1 == /var/mnt/justvoxel-backup ]]; then OLD_MOUNTED=yes; else NEW_MOUNTED=yes; fi; }
umount(){
    printf '%s\n' "${@: -1}" >> "$TMP/unmount.log"
    if [[ ${@: -1} == /var/mnt/justvoxel-backup ]]; then OLD_MOUNTED=no; else NEW_MOUNTED=no; fi
}
storage_remove_fstab_mountpoint(){ jv_storage_remove_entry "$A31_FSTAB" "$1"; }
fixture(){
    local old_type="$1"
    mkdir -p "$JV_CONFIG_DIR"
    printf 'old configuration\n' > "$JV_CONFIG"
    BACKUP_TYPE="$old_type"
    BACKUP_PATH=/var/mnt/justvoxel-backup/backups
    BACKUP_MOUNT_POINT=/var/mnt/justvoxel-backup
    BACKUP_EXPECTED_UUID=''
    case "$old_type" in
        smb) BACKUP_EXPECTED_SOURCE=//old/backups; credentials; old_entry="//old/backups $BACKUP_MOUNT_POINT cifs credentials=$A31_SMB_CREDENTIALS,nofail 0 0" ;;
        nfs) BACKUP_EXPECTED_SOURCE=nas:/backups; old_entry="nas:/backups $BACKUP_MOUNT_POINT nfs rw,nofail 0 0" ;;
        partition) BACKUP_EXPECTED_UUID=old-uuid; BACKUP_EXPECTED_SOURCE=/dev/old; old_entry="UUID=old-uuid $BACKUP_MOUNT_POINT xfs noatime,nofail 0 2" ;;
    esac
    printf '# administrator comment\nUUID=admin /mnt/admin ext4 defaults 0 2\n%s\n' "$old_entry" > "$A31_FSTAB"
    cp "$A31_FSTAB" "$TMP/backup.fstab.expected"
    cp "$JV_CONFIG" "$TMP/config.expected"
    OLD_MOUNTED=yes; NEW_MOUNTED=no
    TARGET_MOUNT="$TMP/new"
    TARGET_PATH="$TARGET_MOUNT/backups"
    TARGET_SOURCE=//new/backups
    TARGET_EXPECTED_SOURCE=/dev/new
    TARGET_TYPE=partition
    TARGET_ALREADY_MOUNTED=false
    TARGET_NEEDS_CREDENTIALS=false
    COMMIT_FAIL=no
    IDENTITY_MISMATCH=no
    : > "$TMP/unmount.log"
}
# Execute Factory Reset's unchanged mount cleanup against the isolated fstab.
source <(sed -n '/^factory_reset_managed_mounts() {/,/^}$/p' "$repo_root/mjust/libexec/admin-factory-reset-json" | sed 's|local fstab=/etc/fstab |local fstab="$A31_FSTAB" |')
fixture smb
factory_reset_managed_mounts
jv_reset_remove_active_configuration 25565 no 19132
! grep -Fq '/var/mnt/justvoxel-backup' "$A31_FSTAB" || fail 'factory retained managed mount'
[[ ! -e $A31_SMB_CREDENTIALS ]] || fail 'factory retained credentials'
for old_type in smb nfs partition; do
    fixture "$old_type"
    apply_json > "$TMP/result"
    jq -e '.ok' "$TMP/result" >/dev/null || fail "$old_type -> local failed"
    ! grep -Fq '/var/mnt/justvoxel-backup' "$A31_FSTAB" || fail 'old mount remained persistent'
    [[ $OLD_MOUNTED == no ]] || fail 'old mount remained active'
    grep -Fxq 'UUID=admin /mnt/admin ext4 defaults 0 2' "$A31_FSTAB" || fail 'administrator entry changed'
    [[ $old_type != smb || ! -e $A31_SMB_CREDENTIALS ]] || fail 'obsolete credentials survived'
done
fixture partition
TARGET_TYPE=smb
TARGET_NEEDS_CREDENTIALS=true
apply_json > "$TMP/result"
jq -e '.ok' "$TMP/result" >/dev/null || fail 'local -> SMB failed'
jv_storage_credentials_safe "$A31_SMB_CREDENTIALS" || fail 'new SMB credentials unsafe'
fixture smb
TARGET_TYPE=smb
TARGET_NEEDS_CREDENTIALS=true
apply_json > "$TMP/result"
jq -e '.ok' "$TMP/result" >/dev/null || fail 'SMB -> different SMB failed'
[[ $OLD_MOUNTED == no ]] || fail 'different SMB left old mount active'
for old_type in nfs partition; do
    fixture "$old_type"
    TARGET_TYPE=system
    TARGET_MOUNT=''
    apply_json > "$TMP/result"
    jq -e '.ok' "$TMP/result" >/dev/null || fail "$old_type -> system failed"
    [[ $OLD_MOUNTED == no ]] || fail 'system replacement retained old mount'
done
fixture smb
TARGET_TYPE=system
TARGET_MOUNT=''
apply_json > "$TMP/result"
jq -e '.ok' "$TMP/result" >/dev/null || fail 'SMB -> system failed'
[[ $OLD_MOUNTED == no && ! -e $A31_SMB_CREDENTIALS ]] || fail 'system transition left old storage'
fixture smb
TARGET_TYPE=smb
TARGET_MOUNT="$BACKUP_MOUNT_POINT"
TARGET_PATH="$BACKUP_PATH"
TARGET_SOURCE="$BACKUP_EXPECTED_SOURCE"
TARGET_EXPECTED_SOURCE="$BACKUP_EXPECTED_SOURCE"
TARGET_ALREADY_MOUNTED=true
apply_json > "$TMP/result"
jq -e '.ok' "$TMP/result" >/dev/null || fail 'same mount failed'
[[ $OLD_MOUNTED == yes && -e $A31_SMB_CREDENTIALS ]] || fail 'selected old mount was removed'
cmp "$A31_FSTAB" "$TMP/backup.fstab.expected"
# Moving backups onto Minecraft's selected data filesystem retires the old
# dedicated backup filesystem, but a shared old data mount must stay mounted.
fixture partition
TARGET_MOUNT="$TMP/data-mount"
TARGET_PATH="$TARGET_MOUNT/backups"
DATA_MOUNT_POINT="$TARGET_MOUNT"
TARGET_ALREADY_MOUNTED=true
NEW_MOUNTED=yes
apply_json > "$TMP/result"
jq -e '.ok' "$TMP/result" >/dev/null || fail 'dedicated partition -> data filesystem failed'
[[ $OLD_MOUNTED == no ]] || fail 'dedicated backup mount survived replacement'
fixture partition
DATA_MOUNT_POINT="$BACKUP_MOUNT_POINT"
DATA_PATH="$BACKUP_MOUNT_POINT/minecraft"
apply_json > "$TMP/result"
jq -e '.ok' "$TMP/result" >/dev/null || fail 'shared data mount transition failed'
[[ $OLD_MOUNTED == yes ]] || fail 'Minecraft data mount was retired'
DATA_MOUNT_POINT=''
DATA_PATH="$TMP/minecraft"
fixture smb
IDENTITY_MISMATCH=yes
apply_json > "$TMP/result"
jq -e '.ok|not' "$TMP/result" >/dev/null || fail 'identity mismatch accepted'
[[ ! -s $TMP/unmount.log ]] || fail 'identity mismatch unmounted storage'
cmp "$A31_FSTAB" "$TMP/backup.fstab.expected"
fixture smb
COMMIT_FAIL=yes
apply_json > "$TMP/result"
jq -e '.ok|not' "$TMP/result" >/dev/null || fail 'commit failure accepted'
cmp "$A31_FSTAB" "$TMP/backup.fstab.expected"
cmp "$JV_CONFIG" "$TMP/config.expected"
[[ $OLD_MOUNTED == yes ]] || fail 'rollback did not restore active old mount'
jv_storage_credentials_safe "$A31_SMB_CREDENTIALS" || fail 'rollback lost credentials'
fixture smb
cp "$A31_SMB_CREDENTIALS" "$TMP/old.smb.expected"
TARGET_TYPE=smb
TARGET_NEEDS_CREDENTIALS=true
COMMIT_FAIL=yes
apply_json > "$TMP/result"
jq -e '.ok|not' "$TMP/result" >/dev/null || fail 'SMB commit failure accepted'
cmp "$A31_SMB_CREDENTIALS" "$TMP/old.smb.expected"
cmp "$A31_FSTAB" "$TMP/backup.fstab.expected"
[[ $OLD_MOUNTED == yes && $NEW_MOUNTED == no ]] || fail 'SMB rollback lost old mount state'
if grep -Fq 'new-secret' "$TMP/result" "$TMP/systemctl.log" "$TMP/unmount.log"; then fail 'password leaked to output'; fi
# No lifecycle helper may contain a data-erasing operation.
if rg 'find .*delete|rm -rf|mkfs|wipefs|parted' "$repo_root/mjust/libexec/storage-lifecycle.sh"; then fail 'retirement can erase data'; fi
# Normal validation must continue to count a real failed mount as an error.
(
    source <(sed -n '/^validate_failed_units() {/,/^validation_mode=/{ /^validation_mode=/d; p; }' "$repo_root/mjust/libexec/validate-backend")
    failures=0
    fail(){ failures=$((failures+1)); }
    pass(){ :; }
    systemctl(){ printf 'var-mnt-justvoxel\\x2dbackup.mount loaded failed failed obsolete mount\n'; }
    validate_failed_units global 2>/dev/null
    [[ $failures == 1 ]]
) || fail 'normal validation did not reject a failed mount'
# Startup inspects historical reserved storage without retiring any topology.
(
    JV_SETUP_IN_PROGRESS="$TMP/setup-in-progress"
    require_config(){ source "$JV_CONFIG"; }
    mkdir -p "$JV_CONFIG_DIR"
    credentials
    cp "$A31_SMB_CREDENTIALS" "$TMP/startup.credentials.expected"
    cat > "$JV_CONFIG" <<'CONFIG'
DATA_MOUNT_POINT=''
DATA_PATH=/var/lib/justvoxel/minecraft
BACKUP_TYPE=system
BACKUP_MOUNT_POINT=''
BACKUP_PATH=/var/lib/justvoxel/backups
CONFIG
    printf 'UUID=data /var/mnt/justvoxel-data xfs defaults 0 2\n//nas/backups /var/mnt/justvoxel-backup cifs credentials=%s,nofail 0 0\n' "$A31_SMB_CREDENTIALS" > "$JV_FSTAB"
    cp "$JV_FSTAB" "$TMP/startup.fstab.expected"
    umount(){ fail 'startup unmounted storage'; }
    mount(){ fail 'startup mounted storage'; }
    rm(){ fail 'startup removed credentials or definitions'; }
    mv(){ fail 'startup replaced fstab'; }
    systemctl(){ fail 'startup changed unit state'; }
    for mounted in yes no; do
        mountpoint(){ [[ $mounted == yes ]]; }
        jv_storage_reconcile_configured "$JV_FSTAB" "$A31_SMB_CREDENTIALS"
        cmp "$JV_FSTAB" "$TMP/startup.fstab.expected"
        cmp "$A31_SMB_CREDENTIALS" "$TMP/startup.credentials.expected"
    done
)
printf 'Storage lifecycle regressions passed.\n'
