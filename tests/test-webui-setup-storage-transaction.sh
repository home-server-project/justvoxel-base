#!/usr/bin/bash
set -uo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-common.sh"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-apply.sh"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-actions.sh"

fail(){ echo "FAIL: $*" >&2; exit 1; }
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
A53_FSTAB="$TMP/fstab"
A53_TRANSACTION_ROOT="$TMP/transactions"
A53_DEFAULT_DATA_PATH="$TMP/system/minecraft"
A53_DEFAULT_BACKUP_PATH="$TMP/system/backups"
A54_SMB_CREDENTIALS="$TMP/etc/justvoxel/smb-backup.credentials"
printf '# original fstab\n' > "$A53_FSTAB"
chmod 0640 "$A53_FSTAB"
cp "$A53_FSTAB" "$TMP/fstab.expected"

validate_storage_path(){ [[ "$1" == /* && "$1" != *' '* ]]; }
validate_simple_text(){ [[ "$1" != *{ case "$1" in /|/boot|/boot/efi|/var) return 0;; *) return 1;; esac; }
storage_validate_mountpoint_path(){ validate_storage_path "$1" && ! storage_mount_is_critical "$1"; }
storage_validate_partition(){ return 0; }

FAKE_DISK="$TMP/fakedisk"
FAKE_PART="$TMP/fakepart"
: > "$FAKE_DISK"; : > "$FAKE_PART"
MOUNT="$TMP/local-mount"
MOUNTED=0
MOUNT_CALLS=0
CURRENT_FS=xfs
CURRENT_UUID=11111111-2222-3333-4444-555555555555
PROBE_FAIL_PATH=''
MOUNT_SOURCE="$FAKE_PART"

lsblk(){
  local args="$*"
  if [[ "$args" == *'NAME,TYPE'* ]]; then
    printf '%s part\n%s disk\n' "$FAKE_PART" "$FAKE_DISK"
    return 0
  fi
  if [[ "$args" == *'MOUNTPOINT'* ]]; then
    (( MOUNTED == 1 )) && printf '%s\n' "$MOUNT"
    return 0
  fi
  return 0
}
blkid(){
  local key=''
  while (( $# )); do
    if [[ $1 == -s ]]; then key="$2"; shift 2; else shift; fi
  done
  case "$key" in TYPE) printf '%s\n' "$CURRENT_FS";; UUID) printf '%s\n' "$CURRENT_UUID";; esac
}
findmnt(){
  local out=''
  while (( $# )); do
    case "$1" in
      -o) out="$2"; shift 2;;
      --target) shift 2;;
      *) shift;;
    esac
  done
  case "$out" in
    TARGET,FSTYPE) printf '/var ext4\n' ;;
    UUID) (( MOUNTED == 1 )) && printf '%s\n' "$CURRENT_UUID" ;;
    SOURCE) (( MOUNTED == 1 )) && printf '%s\n' "$MOUNT_SOURCE" ;;
  esac
}
mountpoint(){
  local target="${@: -1}"
  [[ "$target" == "$MOUNT" && $MOUNTED -eq 1 ]]
}
mount(){ (( MOUNT_CALLS++ )); MOUNTED=1; case "$MOUNT_SOURCE" in "$FAKE_PART") ;; *) : ;; esac; }
umount(){ MOUNTED=0; }
systemctl(){ return 0; }
_a53_write_probe(){ [[ "$1" != "$PROBE_FAIL_PATH" ]]; }

make_system_request(){
  local op="$1"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg d "$A53_DEFAULT_DATA_PATH" --arg b "$A53_DEFAULT_BACKUP_PATH" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"system",path:$d},backups:{type:"system",path:$b}}'
}
make_partition_request(){
  local op="$1"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg dev "$FAKE_PART" --arg parent "$FAKE_DISK" --arg fs "$CURRENT_FS" --arg uuid "$CURRENT_UUID" --arg m "$MOUNT" --arg d "$MOUNT/minecraft" --arg b "$MOUNT/backups" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"partition",path:$d,device:$dev,parent_disk:$parent,filesystem:$fs,uuid:$uuid,mount_point:$m,expected_uuid:$uuid,expected_source:""},backups:{type:"partition",path:$b,device:$dev,parent_disk:$parent,filesystem:$fs,uuid:$uuid,mount_point:$m,expected_uuid:$uuid,expected_source:""}}'
}
make_network_request(){
  local op="$1" type="$2" source="$3" password="${4:-}"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg d "$A53_DEFAULT_DATA_PATH" --arg m "$MOUNT" --arg b "$MOUNT/backups" --arg type "$type" --arg source "$source" --arg password "$password" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"system",path:$d},backups:{type:$type,path:$b,mount_point:$m,source:$source,expected_source:$source,username:(if $type=="smb" then "backup-user" else "" end),domain:"",credentials_required:($type=="smb")},smb_password:$password}'
}
reset_runtime(){ A53_MOUNTS_BY_US=(); A53_DIRS_CREATED=(); A53_TX_DIR=''; A53_MANIFEST=''; A53_FSTAB_CHANGED=false; A53_FSTAB_EXISTED=false; A53_FSTAB_MODE=''; A53_FSTAB_UID=''; A53_FSTAB_GID=''; A54_SMB_PASSWORD=''; A54_CREDENTIALS_EXISTED=false; A54_CREDENTIALS_CHANGED=false; A54_CREDENTIALS_MODE=''; A54_CREDENTIALS_UID=''; A54_CREDENTIALS_GID=''; }
run_action(){ local func="$1" request="$2" file="$TMP/action-output.json"; "$func" <<< "$request" > "$file"; ACTION_OUT="$(cat "$file")"; }

req="$(make_system_request 11111111-1111-4111-8111-111111111111)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied and .phase=="storage_verified"' >/dev/null <<< "$out" || fail "system apply: $out"
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'system apply changed fstab'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "system rollback: $out"
[[ ! -e $A53_DEFAULT_DATA_PATH && ! -e $A53_DEFAULT_BACKUP_PATH ]] || fail 'system rollback left created paths'

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 22222222-2222-4222-8222-222222222222)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "partition apply: $out"
[[ $MOUNTED -eq 1 && $MOUNT_CALLS -eq 1 ]] || fail "mount count/state $MOUNT_CALLS/$MOUNTED"
[[ $(grep -c "UUID=$CURRENT_UUID $MOUNT xfs" "$A53_FSTAB") -eq 1 ]] || fail 'expected exactly one UUID fstab entry'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "partition rollback: $out"
[[ $MOUNTED -eq 0 ]] || fail 'rollback did not unmount owned mount'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'rollback did not restore exact fstab bytes'
[[ $(stat -c '%a' "$A53_FSTAB") == 640 ]] || fail 'rollback did not restore fstab mode'

CURRENT_UUID=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
req="$(make_partition_request 33333333-3333-4333-8333-333333333333)"
req="${req//aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/11111111-2222-3333-4444-555555555555}"
run_action a53_validate_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .phase=="storage_preflight"' >/dev/null <<< "$out" || fail "stale UUID not rejected: $out"

CURRENT_UUID=11111111-2222-3333-4444-555555555555
MOUNTED=0; MOUNT_CALLS=0; MOUNT_SOURCE='nas:/backup'
req="$(make_network_request 44444444-4444-4444-8444-444444444444 nfs 'nas:/backup')"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "NFS apply failed: $out"
grep -Fq 'nas:/backup '"$MOUNT"' nfs rw,_netdev,nofail,x-systemd.mount-timeout=20s' "$A53_FSTAB" || fail 'NFS fstab entry missing'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "NFS rollback failed: $out"
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'NFS rollback did not restore fstab'

MOUNTED=0; MOUNT_CALLS=0; MOUNT_SOURCE='//nas/backups'
req="$(make_network_request 44444444-4444-4444-8444-555555555555 smb '//nas/backups' 'super-secret')"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "SMB apply failed: $out"
[[ -f $A54_SMB_CREDENTIALS && $(stat -c '%a' "$A54_SMB_CREDENTIALS") == 600 ]] || fail 'SMB credentials not root-private'
grep -Fq 'password=super-secret' "$A54_SMB_CREDENTIALS" || fail 'SMB password not written to credential file'
if grep -R -Fq 'super-secret' "$A53_TRANSACTION_ROOT"; then fail 'SMB password leaked into transaction evidence'; fi
reset_runtime
req="$(jq 'del(.smb_password)' <<< "$req")"
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "SMB rollback failed without secret: $out"
[[ ! -e $A54_SMB_CREDENTIALS ]] || fail 'SMB rollback left new credential file'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'SMB rollback did not restore fstab'
MOUNT_SOURCE="$FAKE_PART"

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 55555555-5555-4555-8555-555555555555)"
PROBE_FAIL_PATH="$MOUNT/backups"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .rollback_state=="succeeded" and .rollback_result=="rolled_back"' >/dev/null <<< "$out" || fail "automatic rollback response: $out"
[[ $MOUNTED -eq 0 ]] || fail 'automatic rollback left mount active'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'automatic rollback did not restore fstab'
PROBE_FAIL_PATH=''

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 66666666-6666-4666-8666-666666666666)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "conflict setup apply: $out"
printf '# concurrent admin change\n' >> "$A53_FSTAB"
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .rollback_state=="failed" and .rollback_result=="needs_attention"' >/dev/null <<< "$out" || fail "rollback conflict response: $out"
[[ $MOUNTED -eq 1 ]] || fail 'conflicted rollback changed mount before detecting fstab drift'

echo 'A5.4 storage transaction NFS/SMB tests passed.'
\n'* && "$1" != *{ case "$1" in /|/boot|/boot/efi|/var) return 0;; *) return 1;; esac; }
storage_validate_mountpoint_path(){ validate_storage_path "$1" && ! storage_mount_is_critical "$1"; }
storage_validate_partition(){ return 0; }

FAKE_DISK="$TMP/fakedisk"
FAKE_PART="$TMP/fakepart"
: > "$FAKE_DISK"; : > "$FAKE_PART"
MOUNT="$TMP/local-mount"
MOUNTED=0
MOUNT_CALLS=0
CURRENT_FS=xfs
CURRENT_UUID=11111111-2222-3333-4444-555555555555
PROBE_FAIL_PATH=''
MOUNT_SOURCE="$FAKE_PART"

lsblk(){
  local args="$*"
  if [[ "$args" == *'NAME,TYPE'* ]]; then
    printf '%s part\n%s disk\n' "$FAKE_PART" "$FAKE_DISK"
    return 0
  fi
  if [[ "$args" == *'MOUNTPOINT'* ]]; then
    (( MOUNTED == 1 )) && printf '%s\n' "$MOUNT"
    return 0
  fi
  return 0
}
blkid(){
  local key=''
  while (( $# )); do
    if [[ $1 == -s ]]; then key="$2"; shift 2; else shift; fi
  done
  case "$key" in TYPE) printf '%s\n' "$CURRENT_FS";; UUID) printf '%s\n' "$CURRENT_UUID";; esac
}
findmnt(){
  local out=''
  while (( $# )); do
    case "$1" in
      -o) out="$2"; shift 2;;
      --target) shift 2;;
      *) shift;;
    esac
  done
  case "$out" in
    TARGET,FSTYPE) printf '/var ext4\n' ;;
    UUID) (( MOUNTED == 1 )) && printf '%s\n' "$CURRENT_UUID" ;;
    SOURCE) (( MOUNTED == 1 )) && printf '%s\n' "$MOUNT_SOURCE" ;;
  esac
}
mountpoint(){
  local target="${@: -1}"
  [[ "$target" == "$MOUNT" && $MOUNTED -eq 1 ]]
}
mount(){ (( MOUNT_CALLS++ )); MOUNTED=1; case "$MOUNT_SOURCE" in "$FAKE_PART") ;; *) : ;; esac; }
umount(){ MOUNTED=0; }
systemctl(){ return 0; }
_a53_write_probe(){ [[ "$1" != "$PROBE_FAIL_PATH" ]]; }

make_system_request(){
  local op="$1"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg d "$A53_DEFAULT_DATA_PATH" --arg b "$A53_DEFAULT_BACKUP_PATH" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"system",path:$d},backups:{type:"system",path:$b}}'
}
make_partition_request(){
  local op="$1"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg dev "$FAKE_PART" --arg parent "$FAKE_DISK" --arg fs "$CURRENT_FS" --arg uuid "$CURRENT_UUID" --arg m "$MOUNT" --arg d "$MOUNT/minecraft" --arg b "$MOUNT/backups" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"partition",path:$d,device:$dev,parent_disk:$parent,filesystem:$fs,uuid:$uuid,mount_point:$m,expected_uuid:$uuid,expected_source:""},backups:{type:"partition",path:$b,device:$dev,parent_disk:$parent,filesystem:$fs,uuid:$uuid,mount_point:$m,expected_uuid:$uuid,expected_source:""}}'
}
make_network_request(){
  local op="$1" type="$2" source="$3" password="${4:-}"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg d "$A53_DEFAULT_DATA_PATH" --arg m "$MOUNT" --arg b "$MOUNT/backups" --arg type "$type" --arg source "$source" --arg password "$password" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"system",path:$d},backups:{type:$type,path:$b,mount_point:$m,source:$source,expected_source:$source,username:(if $type=="smb" then "backup-user" else "" end),domain:"",credentials_required:($type=="smb")},smb_password:$password}'
}
reset_runtime(){ A53_MOUNTS_BY_US=(); A53_DIRS_CREATED=(); A53_TX_DIR=''; A53_MANIFEST=''; A53_FSTAB_CHANGED=false; A53_FSTAB_EXISTED=false; A53_FSTAB_MODE=''; A53_FSTAB_UID=''; A53_FSTAB_GID=''; A54_SMB_PASSWORD=''; A54_CREDENTIALS_EXISTED=false; A54_CREDENTIALS_CHANGED=false; A54_CREDENTIALS_MODE=''; A54_CREDENTIALS_UID=''; A54_CREDENTIALS_GID=''; }
run_action(){ local func="$1" request="$2" file="$TMP/action-output.json"; "$func" <<< "$request" > "$file"; ACTION_OUT="$(cat "$file")"; }

req="$(make_system_request 11111111-1111-4111-8111-111111111111)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied and .phase=="storage_verified"' >/dev/null <<< "$out" || fail "system apply: $out"
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'system apply changed fstab'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "system rollback: $out"
[[ ! -e $A53_DEFAULT_DATA_PATH && ! -e $A53_DEFAULT_BACKUP_PATH ]] || fail 'system rollback left created paths'

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 22222222-2222-4222-8222-222222222222)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "partition apply: $out"
[[ $MOUNTED -eq 1 && $MOUNT_CALLS -eq 1 ]] || fail "mount count/state $MOUNT_CALLS/$MOUNTED"
[[ $(grep -c "UUID=$CURRENT_UUID $MOUNT xfs" "$A53_FSTAB") -eq 1 ]] || fail 'expected exactly one UUID fstab entry'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "partition rollback: $out"
[[ $MOUNTED -eq 0 ]] || fail 'rollback did not unmount owned mount'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'rollback did not restore exact fstab bytes'
[[ $(stat -c '%a' "$A53_FSTAB") == 640 ]] || fail 'rollback did not restore fstab mode'

CURRENT_UUID=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
req="$(make_partition_request 33333333-3333-4333-8333-333333333333)"
req="${req//aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/11111111-2222-3333-4444-555555555555}"
run_action a53_validate_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .phase=="storage_preflight"' >/dev/null <<< "$out" || fail "stale UUID not rejected: $out"

CURRENT_UUID=11111111-2222-3333-4444-555555555555
MOUNTED=0; MOUNT_CALLS=0; MOUNT_SOURCE='nas:/backup'
req="$(make_network_request 44444444-4444-4444-8444-444444444444 nfs 'nas:/backup')"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "NFS apply failed: $out"
grep -Fq 'nas:/backup '"$MOUNT"' nfs rw,_netdev,nofail,x-systemd.mount-timeout=20s' "$A53_FSTAB" || fail 'NFS fstab entry missing'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "NFS rollback failed: $out"
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'NFS rollback did not restore fstab'

MOUNTED=0; MOUNT_CALLS=0; MOUNT_SOURCE='//nas/backups'
req="$(make_network_request 44444444-4444-4444-8444-555555555555 smb '//nas/backups' 'super-secret')"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "SMB apply failed: $out"
[[ -f $A54_SMB_CREDENTIALS && $(stat -c '%a' "$A54_SMB_CREDENTIALS") == 600 ]] || fail 'SMB credentials not root-private'
grep -Fq 'password=super-secret' "$A54_SMB_CREDENTIALS" || fail 'SMB password not written to credential file'
if grep -R -Fq 'super-secret' "$A53_TRANSACTION_ROOT"; then fail 'SMB password leaked into transaction evidence'; fi
reset_runtime
req="$(jq 'del(.smb_password)' <<< "$req")"
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "SMB rollback failed without secret: $out"
[[ ! -e $A54_SMB_CREDENTIALS ]] || fail 'SMB rollback left new credential file'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'SMB rollback did not restore fstab'
MOUNT_SOURCE="$FAKE_PART"

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 55555555-5555-4555-8555-555555555555)"
PROBE_FAIL_PATH="$MOUNT/backups"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .rollback_state=="succeeded" and .rollback_result=="rolled_back"' >/dev/null <<< "$out" || fail "automatic rollback response: $out"
[[ $MOUNTED -eq 0 ]] || fail 'automatic rollback left mount active'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'automatic rollback did not restore fstab'
PROBE_FAIL_PATH=''

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 66666666-6666-4666-8666-666666666666)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "conflict setup apply: $out"
printf '# concurrent admin change\n' >> "$A53_FSTAB"
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .rollback_state=="failed" and .rollback_result=="needs_attention"' >/dev/null <<< "$out" || fail "rollback conflict response: $out"
[[ $MOUNTED -eq 1 ]] || fail 'conflicted rollback changed mount before detecting fstab drift'

echo 'A5.4 storage transaction NFS/SMB tests passed.'
\r'* ]]; }
storage_mount_is_critical(){ case "$1" in /|/boot|/boot/efi|/var) return 0;; *) return 1;; esac; }
storage_validate_mountpoint_path(){ validate_storage_path "$1" && ! storage_mount_is_critical "$1"; }
storage_validate_partition(){ return 0; }

FAKE_DISK="$TMP/fakedisk"
FAKE_PART="$TMP/fakepart"
: > "$FAKE_DISK"; : > "$FAKE_PART"
MOUNT="$TMP/local-mount"
MOUNTED=0
MOUNT_CALLS=0
CURRENT_FS=xfs
CURRENT_UUID=11111111-2222-3333-4444-555555555555
PROBE_FAIL_PATH=''
MOUNT_SOURCE="$FAKE_PART"

lsblk(){
  local args="$*"
  if [[ "$args" == *'NAME,TYPE'* ]]; then
    printf '%s part\n%s disk\n' "$FAKE_PART" "$FAKE_DISK"
    return 0
  fi
  if [[ "$args" == *'MOUNTPOINT'* ]]; then
    (( MOUNTED == 1 )) && printf '%s\n' "$MOUNT"
    return 0
  fi
  return 0
}
blkid(){
  local key=''
  while (( $# )); do
    if [[ $1 == -s ]]; then key="$2"; shift 2; else shift; fi
  done
  case "$key" in TYPE) printf '%s\n' "$CURRENT_FS";; UUID) printf '%s\n' "$CURRENT_UUID";; esac
}
findmnt(){
  local out=''
  while (( $# )); do
    case "$1" in
      -o) out="$2"; shift 2;;
      --target) shift 2;;
      *) shift;;
    esac
  done
  case "$out" in
    TARGET,FSTYPE) printf '/var ext4\n' ;;
    UUID) (( MOUNTED == 1 )) && printf '%s\n' "$CURRENT_UUID" ;;
    SOURCE) (( MOUNTED == 1 )) && printf '%s\n' "$MOUNT_SOURCE" ;;
  esac
}
mountpoint(){
  local target="${@: -1}"
  [[ "$target" == "$MOUNT" && $MOUNTED -eq 1 ]]
}
mount(){ (( MOUNT_CALLS++ )); MOUNTED=1; case "$MOUNT_SOURCE" in "$FAKE_PART") ;; *) : ;; esac; }
umount(){ MOUNTED=0; }
systemctl(){ return 0; }
_a53_write_probe(){ [[ "$1" != "$PROBE_FAIL_PATH" ]]; }

make_system_request(){
  local op="$1"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg d "$A53_DEFAULT_DATA_PATH" --arg b "$A53_DEFAULT_BACKUP_PATH" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"system",path:$d},backups:{type:"system",path:$b}}'
}
make_partition_request(){
  local op="$1"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg dev "$FAKE_PART" --arg parent "$FAKE_DISK" --arg fs "$CURRENT_FS" --arg uuid "$CURRENT_UUID" --arg m "$MOUNT" --arg d "$MOUNT/minecraft" --arg b "$MOUNT/backups" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"partition",path:$d,device:$dev,parent_disk:$parent,filesystem:$fs,uuid:$uuid,mount_point:$m,expected_uuid:$uuid,expected_source:""},backups:{type:"partition",path:$b,device:$dev,parent_disk:$parent,filesystem:$fs,uuid:$uuid,mount_point:$m,expected_uuid:$uuid,expected_source:""}}'
}
make_network_request(){
  local op="$1" type="$2" source="$3" password="${4:-}"
  jq -cn --arg op "$op" --arg fp 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' --arg d "$A53_DEFAULT_DATA_PATH" --arg m "$MOUNT" --arg b "$MOUNT/backups" --arg type "$type" --arg source "$source" --arg password "$password" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp,storage:{type:"system",path:$d},backups:{type:$type,path:$b,mount_point:$m,source:$source,expected_source:$source,username:(if $type=="smb" then "backup-user" else "" end),domain:"",credentials_required:($type=="smb")},smb_password:$password}'
}
reset_runtime(){ A53_MOUNTS_BY_US=(); A53_DIRS_CREATED=(); A53_TX_DIR=''; A53_MANIFEST=''; A53_FSTAB_CHANGED=false; A53_FSTAB_EXISTED=false; A53_FSTAB_MODE=''; A53_FSTAB_UID=''; A53_FSTAB_GID=''; A54_SMB_PASSWORD=''; A54_CREDENTIALS_EXISTED=false; A54_CREDENTIALS_CHANGED=false; A54_CREDENTIALS_MODE=''; A54_CREDENTIALS_UID=''; A54_CREDENTIALS_GID=''; }
run_action(){ local func="$1" request="$2" file="$TMP/action-output.json"; "$func" <<< "$request" > "$file"; ACTION_OUT="$(cat "$file")"; }

req="$(make_system_request 11111111-1111-4111-8111-111111111111)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied and .phase=="storage_verified"' >/dev/null <<< "$out" || fail "system apply: $out"
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'system apply changed fstab'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "system rollback: $out"
[[ ! -e $A53_DEFAULT_DATA_PATH && ! -e $A53_DEFAULT_BACKUP_PATH ]] || fail 'system rollback left created paths'

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 22222222-2222-4222-8222-222222222222)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "partition apply: $out"
[[ $MOUNTED -eq 1 && $MOUNT_CALLS -eq 1 ]] || fail "mount count/state $MOUNT_CALLS/$MOUNTED"
[[ $(grep -c "UUID=$CURRENT_UUID $MOUNT xfs" "$A53_FSTAB") -eq 1 ]] || fail 'expected exactly one UUID fstab entry'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "partition rollback: $out"
[[ $MOUNTED -eq 0 ]] || fail 'rollback did not unmount owned mount'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'rollback did not restore exact fstab bytes'
[[ $(stat -c '%a' "$A53_FSTAB") == 640 ]] || fail 'rollback did not restore fstab mode'

CURRENT_UUID=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
req="$(make_partition_request 33333333-3333-4333-8333-333333333333)"
req="${req//aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/11111111-2222-3333-4444-555555555555}"
run_action a53_validate_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .phase=="storage_preflight"' >/dev/null <<< "$out" || fail "stale UUID not rejected: $out"

CURRENT_UUID=11111111-2222-3333-4444-555555555555
MOUNTED=0; MOUNT_CALLS=0; MOUNT_SOURCE='nas:/backup'
req="$(make_network_request 44444444-4444-4444-8444-444444444444 nfs 'nas:/backup')"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "NFS apply failed: $out"
grep -Fq 'nas:/backup '"$MOUNT"' nfs rw,_netdev,nofail,x-systemd.mount-timeout=20s' "$A53_FSTAB" || fail 'NFS fstab entry missing'
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "NFS rollback failed: $out"
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'NFS rollback did not restore fstab'

MOUNTED=0; MOUNT_CALLS=0; MOUNT_SOURCE='//nas/backups'
req="$(make_network_request 44444444-4444-4444-8444-555555555555 smb '//nas/backups' 'super-secret')"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "SMB apply failed: $out"
[[ -f $A54_SMB_CREDENTIALS && $(stat -c '%a' "$A54_SMB_CREDENTIALS") == 600 ]] || fail 'SMB credentials not root-private'
grep -Fq 'password=super-secret' "$A54_SMB_CREDENTIALS" || fail 'SMB password not written to credential file'
if grep -R -Fq 'super-secret' "$A53_TRANSACTION_ROOT"; then fail 'SMB password leaked into transaction evidence'; fi
reset_runtime
req="$(jq 'del(.smb_password)' <<< "$req")"
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .rollback_state=="succeeded"' >/dev/null <<< "$out" || fail "SMB rollback failed without secret: $out"
[[ ! -e $A54_SMB_CREDENTIALS ]] || fail 'SMB rollback left new credential file'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'SMB rollback did not restore fstab'
MOUNT_SOURCE="$FAKE_PART"

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 55555555-5555-4555-8555-555555555555)"
PROBE_FAIL_PATH="$MOUNT/backups"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .rollback_state=="succeeded" and .rollback_result=="rolled_back"' >/dev/null <<< "$out" || fail "automatic rollback response: $out"
[[ $MOUNTED -eq 0 ]] || fail 'automatic rollback left mount active'
cmp -s "$A53_FSTAB" "$TMP/fstab.expected" || fail 'automatic rollback did not restore fstab'
PROBE_FAIL_PATH=''

MOUNTED=0; MOUNT_CALLS=0
req="$(make_partition_request 66666666-6666-4666-8666-666666666666)"
run_action a53_apply_action "$req"; out="$ACTION_OUT"
jq -e '.ok and .applied' >/dev/null <<< "$out" || fail "conflict setup apply: $out"
printf '# concurrent admin change\n' >> "$A53_FSTAB"
reset_runtime
run_action a53_rollback_action "$req"; out="$ACTION_OUT"
jq -e '(.ok|not) and .rollback_state=="failed" and .rollback_result=="needs_attention"' >/dev/null <<< "$out" || fail "rollback conflict response: $out"
[[ $MOUNTED -eq 1 ]] || fail 'conflicted rollback changed mount before detecting fstab drift'

echo 'A5.4 storage transaction NFS/SMB tests passed.'
