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
# Configured-state startup compatibility repair. Production /etc files are mapped
# into this fixture; reserved mountpoints and the historical SMB source stay exact.
(
    JV_SETUP_IN_PROGRESS="$TMP/setup-in-progress"
    require_config(){ source "$JV_CONFIG"; }
    ACTIVE=no
    LIVE_SOURCE=//192.168.0.50/storage/TEMP
    LIVE_UUID=old-local
    CREDENTIAL_OWNER=0:0
    stat(){
        if [[ ${2:-} == '%u:%g:%a:%h' ]]; then
            printf '%s:%s\n' "$CREDENTIAL_OWNER" "$(command stat -c '%a:%h' -- "${@: -1}")"
        else command stat "$@"; fi
    }
    touch "$TMP/block-device" "$TMP/wrong-device"
    blkid(){ [[ $* == '-U old-local' ]] || return 1; printf '%s\n' "$TMP/block-device"; }
    mountpoint(){ [[ ${@: -1} == /var/mnt/justvoxel-backup && $ACTIVE == yes ]]; }
    jv_exact_mount_identity(){
        case "$1" in SOURCE) printf '%s\n' "$LIVE_SOURCE" ;; UUID) printf '%s\n' "$LIVE_UUID" ;; esac
    }
    umount(){ printf '%s\n' "$*" >> "$TMP/reconcile-unmount"; ACTIVE=no; }
    cp(){
        if [[ ${1:-} == --attributes-only ]]; then
            printf 'metadata\n' >> "$TMP/reconcile-replacement"
            [[ $REPLACEMENT_FAIL != metadata ]] || return 1
        fi
        command cp "$@"
    }
    sync(){
        printf 'sync\n' >> "$TMP/reconcile-replacement"
        [[ ${1:-} == -f && ${2:-} == -- && $# == 3 &&
           $(dirname -- "$3") == "$(dirname -- "$JV_FSTAB")" ]] || fail 'replacement sync not in fstab directory'
        [[ $REPLACEMENT_FAIL != sync ]] || return 1
        command sync "$@"
    }
    mv(){
        printf 'rename\n' >> "$TMP/reconcile-replacement"
        [[ ${1:-} == -fT && ${2:-} == -- && $# == 4 && $4 == "$JV_FSTAB" &&
           $(dirname -- "$3") == "$(dirname -- "$JV_FSTAB")" ]] || fail 'replacement rename not in fstab directory'
        [[ $REPLACEMENT_FAIL != rename ]] || return 1
        command mv "$@"
    }
    systemctl(){
        printf '%s\n' "$*" >> "$TMP/reconcile-systemctl"
        if [[ ${1:-} == daemon-reload && $RESET_BEHAVIOR == unloaded ]]; then
            UNIT_LOAD=not-found
            HISTORICAL_FAILED=no
        fi
        if [[ ${1:-} == reset-failed ]]; then
            [[ $# == 2 && $2 == 'var-mnt-justvoxel\x2dbackup.mount' ||
               $# == 2 && $2 == 'var-mnt-justvoxel\x2ddata.mount' ]] || fail 'unrelated or broad reset-failed'
            [[ $RESET_BEHAVIOR != unloaded ]] || return 1
            if [[ $2 == 'var-mnt-justvoxel\x2dbackup.mount' && $RESET_BEHAVIOR == clear ]]; then
                HISTORICAL_FAILED=no
            fi
        elif [[ ${1:-} == show ]]; then
            [[ $# == 5 && $3 == --value && $4 == -- &&
               ( $5 == 'var-mnt-justvoxel\x2dbackup.mount' || $5 == 'var-mnt-justvoxel\x2ddata.mount' ) ]] || fail 'unrelated unit queried'
            case "$2" in
                --property=LoadState) printf '%s\n' "$UNIT_LOAD" ;;
                --property=ActiveState)
                    if [[ $5 == 'var-mnt-justvoxel\x2dbackup.mount' && $HISTORICAL_FAILED == yes ]]; then
                        printf 'failed\n'
                    else printf 'inactive\n'; fi
                    ;;
                *) fail 'unexpected unit property' ;;
            esac
        elif [[ ${1:-} == --failed ]]; then
            if [[ $HISTORICAL_FAILED == yes ]]; then
                printf 'var-mnt-justvoxel\\x2dbackup.mount loaded failed failed obsolete mount\n'
            fi
            if [[ $UNRELATED_FAILED == yes ]]; then
                printf 'administrator.service loaded failed failed administrator service\n'
            fi
        fi
    }
    rm(){
        printf '%s\n' "$*" >> "$TMP/reconcile-rm"
        case "${@: -1}" in
            "$A31_SMB_CREDENTIALS"|"$TMP"/.fstab.lifecycle.*) command rm "$@" ;;
            *) fail 'reconciliation attempted data removal' ;;
        esac
    }
    rmdir(){ fail 'reconciliation attempted directory removal'; }
    mkfs(){ fail 'reconciliation attempted formatting'; }
    wipefs(){ fail 'reconciliation attempted wiping'; }
    parted(){ fail 'reconciliation attempted partitioning'; }
    reconcile(){ jv_storage_reconcile_configured "$JV_FSTAB" "$A31_SMB_CREDENTIALS"; }
    startup_fixture(){
        command mkdir -p "$JV_CONFIG_DIR"
        command rm -f "$A31_SMB_CREDENTIALS" "$JV_SETUP_IN_PROGRESS"
        cat > "$JV_CONFIG" <<'CONFIG'
DATA_MOUNT_POINT=/var/mnt/justvoxel-data
DATA_PATH=/var/mnt/justvoxel-data/minecraft
BACKUP_TYPE=partition
BACKUP_MOUNT_POINT=/var/mnt/justvoxel-data
BACKUP_PATH=/var/mnt/justvoxel-data/backups
CONFIG
        printf '# administrator comment\nUUID=current-data /var/mnt/justvoxel-data xfs noatime,nofail 0 2\n//192.168.0.50/storage/TEMP /var/mnt/justvoxel-backup cifs credentials=%s,nofail 0 0\nUUID=admin /var/mnt/admin ext4 defaults 0 2\n' "$A31_SMB_CREDENTIALS" > "$JV_FSTAB"
        cp "$JV_FSTAB" "$TMP/reconcile-before"
        ACTIVE=no; LIVE_SOURCE=//192.168.0.50/storage/TEMP; LIVE_UUID=old-local
        CREDENTIAL_OWNER=0:0; HISTORICAL_FAILED=yes; UNRELATED_FAILED=yes
        REPLACEMENT_FAIL=none; RESET_BEHAVIOR=clear; UNIT_LOAD=loaded
        : > "$TMP/reconcile-unmount"; : > "$TMP/reconcile-systemctl"; : > "$TMP/reconcile-rm"
        : > "$TMP/reconcile-replacement"
    }
    unchanged(){
        cmp "$JV_FSTAB" "$TMP/reconcile-before"
        [[ ! -s $TMP/reconcile-unmount && ! -s $TMP/reconcile-systemctl && ! -s $TMP/reconcile-rm ]] || fail 'unsafe/no-op reconciliation mutated state'
    }
    # Failure at metadata preservation, sync, or immediately before rename must
    # leave the complete original live file intact and remove the temporary file.
    for stage in metadata sync rename; do
        startup_fixture
        REPLACEMENT_FAIL="$stage"
        if reconcile 2> "$TMP/reconcile-diagnostic"; then fail "$stage failure accepted"; fi
        cmp "$JV_FSTAB" "$TMP/reconcile-before" || fail "$stage failure changed live fstab"
        [[ ! -s $TMP/reconcile-systemctl ]] || fail 'failed replacement reloaded systemd'
        compgen -G "$TMP/.fstab.lifecycle.*" >/dev/null && fail 'failed replacement left a temporary file'
        grep -Fq 'could not safely retire' "$TMP/reconcile-diagnostic"
    done
    # Exact installed-machine bug: current local data/backups, missing old SMB
    # credentials and a failed historical mount unit. Only that unit is cleared.
    startup_fixture
    chmod 0640 "$JV_FSTAB"
    original_metadata="$(command stat -c '%a:%u:%g' -- "$JV_FSTAB")"
    original_inode="$(command stat -c '%i' -- "$JV_FSTAB")"
    reconcile
    awk '$2!="/var/mnt/justvoxel-backup"' "$TMP/reconcile-before" > "$TMP/reconcile-after"
    cmp "$JV_FSTAB" "$TMP/reconcile-after"
    [[ $(command stat -c '%a:%u:%g' -- "$JV_FSTAB") == "$original_metadata" ]] || fail 'fstab mode/ownership changed'
    [[ $(command stat -c '%i' -- "$JV_FSTAB") != "$original_inode" ]] || fail 'fstab was written in place'
    printf 'metadata\nsync\nrename\n' > "$TMP/reconcile-replacement-expected"
    cmp "$TMP/reconcile-replacement" "$TMP/reconcile-replacement-expected"
    printf 'daemon-reload\nreset-failed var-mnt-justvoxel\\x2dbackup.mount\nshow --property=LoadState --value -- var-mnt-justvoxel\\x2dbackup.mount\nshow --property=ActiveState --value -- var-mnt-justvoxel\\x2dbackup.mount\n' > "$TMP/reconcile-systemctl-expected"
    cmp "$TMP/reconcile-systemctl" "$TMP/reconcile-systemctl-expected"
    [[ $HISTORICAL_FAILED == no && $UNRELATED_FAILED == yes ]] || fail 'failed-unit state not narrowly healed'
    [[ ! -s $TMP/reconcile-unmount ]] || fail 'inactive historical mount was unmounted'
    # Only fstab temporary files were removed; neither data nor share contents.
    if rg -v -- '^(-f -- )?.*/\.fstab\.lifecycle\.' "$TMP/reconcile-rm"; then fail 'unexpected removal'; fi
    cp "$JV_FSTAB" "$TMP/reconcile-before"
    : > "$TMP/reconcile-rm"; : > "$TMP/reconcile-systemctl"
    reconcile
    unchanged
    startup_fixture
    printf 'BACKUP_MOUNT_POINT=""\n' >> "$JV_CONFIG"
    reconcile
    cmp "$JV_FSTAB" "$TMP/reconcile-after"

    # A successful reset-failed command is insufficient if the exact mount is
    # still failed. Retirement remains committed and normal validation sees it.
    startup_fixture
    RESET_BEHAVIOR=persistent
    if reconcile 2> "$TMP/reconcile-diagnostic"; then fail 'persistent failed mount accepted'; fi
    cmp "$JV_FSTAB" "$TMP/reconcile-after"
    [[ $HISTORICAL_FAILED == yes && $UNRELATED_FAILED == yes ]] || fail 'persistent failure state changed'
    cmp "$TMP/reconcile-systemctl" "$TMP/reconcile-systemctl-expected"
    grep -Fq 'could not reload or clear the failed state' "$TMP/reconcile-diagnostic"
    (
        source <(sed -n '/^validate_failed_units() {/,/^validation_mode=/{ /^validation_mode=/d; p; }' "$repo_root/mjust/libexec/validate-backend")
        failures=0
        fail(){ failures=$((failures+1)); }
        pass(){ :; }
        validate_failed_units global 2> "$TMP/persistent-validation-diagnostic"
        [[ $failures == 1 ]]
        grep -Fq 'var-mnt-justvoxel\x2dbackup.mount' "$TMP/persistent-validation-diagnostic"
    ) || fail 'normal validation lost the unresolved failed mount'

    # daemon-reload may unload the removed generated unit; reset-failed can then
    # fail because it disappeared. That exact not-found unit is healthy cleanup.
    startup_fixture
    RESET_BEHAVIOR=unloaded
    reconcile
    cmp "$JV_FSTAB" "$TMP/reconcile-after"
    [[ $UNIT_LOAD == not-found && $HISTORICAL_FAILED == no && $UNRELATED_FAILED == yes ]] || fail 'unloaded unit cleanup failed'
    printf 'daemon-reload\nreset-failed var-mnt-justvoxel\\x2dbackup.mount\nshow --property=LoadState --value -- var-mnt-justvoxel\\x2dbackup.mount\n' > "$TMP/unloaded-systemctl-expected"
    cmp "$TMP/reconcile-systemctl" "$TMP/unloaded-systemctl-expected"

    # A: no configuration, or setup still in progress, must not mutate anything.
    startup_fixture
    command rm "$JV_CONFIG"
    reconcile
    unchanged
    startup_fixture
    touch "$JV_SETUP_IN_PROGRESS"
    reconcile
    unchanged
    # B: the old SMB mount is still selected.
    startup_fixture
    printf 'BACKUP_TYPE=smb\nBACKUP_MOUNT_POINT=/var/mnt/justvoxel-backup\nBACKUP_PATH=/var/mnt/justvoxel-backup/backups\n' >> "$JV_CONFIG"
    reconcile
    unchanged
    # System paths do not claim any managed mounts; both obsolete reserved
    # definitions can be retired while the administrator entry stays byte exact.
    startup_fixture
    printf 'DATA_MOUNT_POINT=""\nDATA_PATH=/var/lib/justvoxel/minecraft\nBACKUP_TYPE=system\nBACKUP_MOUNT_POINT=""\nBACKUP_PATH=/var/lib/justvoxel/backups\n' >> "$JV_CONFIG"
    reconcile
    printf '# administrator comment\nUUID=admin /var/mnt/admin ext4 defaults 0 2\n' > "$TMP/system-storage-expected"
    cmp "$JV_FSTAB" "$TMP/system-storage-expected"
    # C/D: active network source must match the persisted identity.
    startup_fixture
    ACTIVE=yes
    reconcile
    [[ $ACTIVE == no ]] || fail 'matching obsolete mount was not unmounted'
    grep -Fxq -- '-- /var/mnt/justvoxel-backup' "$TMP/reconcile-unmount"
    startup_fixture
    ACTIVE=yes; LIVE_SOURCE=//different/share
    if reconcile 2> "$TMP/reconcile-diagnostic"; then fail 'source mismatch accepted'; fi
    unchanged
    grep -Fq 'skipped for safety' "$TMP/reconcile-diagnostic"
    # E: local UUID and SOURCE must both agree, including cloned-UUID cases.
    for mismatch in uuid source neither; do
        startup_fixture
        printf '# administrator comment\nUUID=current-data /var/mnt/justvoxel-data xfs noatime,nofail 0 2\nUUID=old-local /var/mnt/justvoxel-backup xfs noatime,nofail 0 2\nUUID=admin /var/mnt/admin ext4 defaults 0 2\n' > "$JV_FSTAB"
        cp "$JV_FSTAB" "$TMP/reconcile-before"
        ACTIVE=yes; LIVE_SOURCE="$TMP/block-device"
        case "$mismatch" in uuid) LIVE_UUID=wrong ;; source) LIVE_SOURCE="$TMP/wrong-device" ;; esac
        if [[ $mismatch == neither ]]; then
            reconcile
            [[ $ACTIVE == no ]] || fail 'matching UUID/SOURCE not retired'
        else
            if reconcile 2> "$TMP/reconcile-diagnostic"; then fail 'local identity mismatch accepted'; fi
            unchanged
        fi
    done
    # Ambiguous duplicate or unowned reserved entries stay untouched.
    for ambiguity in duplicate unowned; do
        startup_fixture
        if [[ $ambiguity == duplicate ]]; then
            grep '//192.168.0.50' "$JV_FSTAB" >> "$TMP/duplicate-entry"
            cat "$TMP/duplicate-entry" >> "$JV_FSTAB"
        else
            sed -i 's|credentials=[^,]*|credentials=/etc/administrator.credentials|' "$JV_FSTAB"
        fi
        cp "$JV_FSTAB" "$TMP/reconcile-before"
        if reconcile 2> "$TMP/reconcile-diagnostic"; then fail 'ambiguous ownership accepted'; fi
        unchanged
    done
    # F/G: even a non-reserved administrator SMB reference is preserved, and
    # conservatively prevents credential deletion. No arbitrary /var/mnt cleanup.
    startup_fixture
    credentials
    printf '//administrator/share /var/mnt/admin-share cifs credentials=%s,nofail 0 0\n' "$A31_SMB_CREDENTIALS" >> "$JV_FSTAB"
    reconcile
    [[ -f $A31_SMB_CREDENTIALS ]] || fail 'referenced credentials deleted'
    grep -Fq '//administrator/share /var/mnt/admin-share' "$JV_FSTAB"
    startup_fixture
    credentials
    printf 'BACKUP_TYPE=smb\nBACKUP_MOUNT_POINT=/var/mnt/justvoxel-backup\nBACKUP_PATH=/var/mnt/justvoxel-backup/backups\n' >> "$JV_CONFIG"
    reconcile
    unchanged
    [[ -f $A31_SMB_CREDENTIALS ]] || fail 'selected SMB credentials deleted'
    # H/I: only a safe, unused credential file can be removed.
    startup_fixture
    credentials
    reconcile
    [[ ! -e $A31_SMB_CREDENTIALS ]] || fail 'unused safe credentials survived'
    for unsafe in symlink mode owner malformed hardlink; do
        startup_fixture
        credentials
        case "$unsafe" in
            symlink) command rm "$A31_SMB_CREDENTIALS"; ln -s "$TMP/absent-credentials" "$A31_SMB_CREDENTIALS" ;;
            mode) chmod 0644 "$A31_SMB_CREDENTIALS" ;;
            owner) CREDENTIAL_OWNER=1000:1000 ;;
            malformed) printf 'invalid\n' >> "$A31_SMB_CREDENTIALS" ;;
            hardlink) ln "$A31_SMB_CREDENTIALS" "$TMP/linked-credentials" ;;
        esac
        if reconcile 2> "$TMP/reconcile-diagnostic"; then fail 'unsafe credentials accepted'; fi
        [[ -e $A31_SMB_CREDENTIALS || -L $A31_SMB_CREDENTIALS ]] || fail 'unsafe credentials removed'
        grep -Fq 'unsafe obsolete SMB credentials' "$TMP/reconcile-diagnostic"
    done
    # New helper uses the existing wildcard install and fixed authoritative paths.
    grep -Fq 'install -m0755 /ctx/mjust/libexec/* /usr/libexec/justvoxel/mjust/' "$repo_root/build_files/build-common.sh"
    grep -Fq 'jv_storage_reconcile_configured /etc/fstab /etc/justvoxel/smb-backup.credentials' "$repo_root/mjust/libexec/storage-reconcile-configured"
)
printf 'Storage lifecycle regressions passed.\n'
