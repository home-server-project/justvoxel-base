#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/restore-common.sh"
source "${repo_root}/mjust/libexec/restore-discovery-common.sh"
source "${repo_root}/mjust/libexec/backup-common.sh"

fail(){ echo "FAIL: $*" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

mkdir -p "${tmp}/backups" "${tmp}/data"
touch -d '2026-09-13 04:30:00' "${tmp}/backups/minecraft-2026-09-13-043000.tar.gz"
touch -d '2026-09-15 04:30:00' "${tmp}/backups/minecraft-2026-09-15-043000.tar.gz"
touch -d '2026-09-16 04:30:00' "${tmp}/backups/minecraft-2026-09-16-043000.tar.gz.partial"
printf '{}\n' > "${tmp}/backups/minecraft-2026-09-15-043000.tar.gz.meta.json"

mapfile -t records < <(jv_restore_list_archives "${tmp}/backups")
(( ${#records[@]} == 2 )) || fail 'restore discovery must include only completed archives'
[[ ${records[0]} == *'minecraft-2026-09-15-043000.tar.gz' ]] || fail 'backups are not sorted newest first'
[[ ${records[1]} == *'minecraft-2026-09-13-043000.tar.gz' ]] || fail 'older backup ordering is wrong'


cat > "${tmp}/backups/minecraft-2026-09-13-043000.tar.gz.meta.json" <<'JSON'
{
  "schemaVersion": 1,
  "createdAt": "2026-09-13T08:30:00Z",
  "minecraft": {
    "versionMode": "pinned",
    "configuredVersion": "26.1",
    "serverReportedVersion": "Paper 26.1"
  },
  "bedrock": {
    "enabled": false,
    "geyserReportedVersion": null,
    "floodgateConfigured": false
  },
  "justvoxel": {
    "variant": "justvoxel-vm"
  }
}
JSON
touch "${tmp}/backups/minecraft-manual.tar.gz"
discovery="$(jv_restore_discovery_json "${tmp}/backups")"
[[ $(jq -r '.backups | length' <<< "${discovery}") == 2 ]] || fail 'API restore discovery must expose only canonical completed backups'
[[ $(jq -r '.backups[0].id' <<< "${discovery}") == minecraft-2026-09-15-043000.tar.gz ]] || fail 'API restore discovery ordering is wrong'
[[ $(jq -r '.backups[0].metadata_status' <<< "${discovery}") == invalid ]] || fail 'invalid restore metadata must be marked invalid'
[[ $(jq -r '.backups[1].metadata_status' <<< "${discovery}") == valid ]] || fail 'valid restore metadata was not recognized'
[[ $(jq -r '.backups[1].metadata.minecraft.configured_version' <<< "${discovery}") == 26.1 ]] || fail 'safe restore metadata summary is incomplete'
if grep -Fq "${tmp}/backups" <<< "${discovery}"; then
    fail 'API restore discovery must not expose backup filesystem paths'
fi

[[ $(jv_restore_version_relation pinned 26.1 pinned 26.2) == backup_older ]] || fail 'older backup version relation wrong'
[[ $(jv_restore_version_relation pinned 26.2 pinned 26.2) == same ]] || fail 'same version relation wrong'
[[ $(jv_restore_version_relation pinned 26.3 pinned 26.2) == backup_newer ]] || fail 'newer backup version relation wrong'
[[ $(jv_restore_version_relation latest LATEST pinned 26.2) == unknown ]] || fail 'moving version policy must remain unknown'

cat > "${tmp}/backup.env" <<EOF2
MINECRAFT_DATA_PATH=${tmp}/data
MINECRAFT_BACKUP_PATH=${tmp}/backups
MINECRAFT_SERVICE=minecraft.service
BACKUP_KEEP=7
BACKUP_MOUNT_POINT=
BACKUP_EXPECTED_UUID=
BACKUP_EXPECTED_SOURCE=
EOF2
jv_backup_load_config "${tmp}/backup.env"
jv_backup_require_read_target || fail 'ordinary readable system backup path rejected'

BACKUP_MOUNT_POINT="${tmp}/not-mounted"
if jv_backup_validate_mount_identity >/dev/null 2>&1; then
    fail 'missing configured backup mount must fail closed'
fi

plan_helper="${repo_root}/mjust/libexec/admin-restore-plan-json"
for text in \
    'jv_backup_require_read_target' \
    'validate-data-mount' \
    'jv_restore_validate_data_layout' \
    '.justvoxel-restore-*' \
    'jv_restore_archive_identity' \
    'web-status-json players' \
    'backup_newer' \
    'archive_integrity_validation_on_apply' \
    'archive_safety_validation_on_apply' \
    'staging_space_validation_on_apply'; do
    grep -Fq "${text}" "${plan_helper}" || fail "Restore API plan safety behavior missing: ${text}"
done
if grep -Eq 'systemctl[[:space:]]+(stop|restart)[[:space:]]+minecraft|restore-archive[[:space:]]+extract-|chown[[:space:]]+-R|restorecon[[:space:]]+-R' "${plan_helper}"; then
    fail 'Restore API planning helper must remain read-only'
fi

restore="${repo_root}/mjust/libexec/restore"
backup="${repo_root}/runtime/minecraft-backup"
menu="${repo_root}/mjust/libexec/menu"
justfile="${repo_root}/mjust/justfile"

for text in \
    'Type RESTORE to continue:' \
    'JV_MAINTENANCE_LOCK' \
    'pre-restore' \
    'failed-restored' \
    'rollback_restore' \
    'restore-runtime-validate' \
    'jv_backup_require_read_target' \
    'gzip -t' \
    'apply_data_selinux'; do
    grep -Fq "${text}" "${restore}" || fail "restore safety behavior missing: ${text}"
done

grep -Fq 'restore world' "${justfile}" || fail 'mjust restore world recipe missing'
grep -Fq 'restore full' "${justfile}" || fail 'mjust restore-full recipe missing'
grep -Fq 'Restore world' "${menu}" || fail 'TUI world restore missing'
grep -Fq 'Restore full Minecraft data' "${menu}" || fail 'TUI full restore missing'
grep -Fq 'mjust restore' "${menu}" || fail 'direct world restore command not discoverable'
grep -Fq 'mjust restore-full' "${menu}" || fail 'direct full restore command not discoverable'

grep -Fq '.meta.json' "${backup}" || fail 'new backups do not create restore metadata sidecars'
grep -Fq 'jv_backup_write_metadata' "${backup}" || fail 'backup metadata writer is not used'
grep -Fq 'rm -f -- "${minecraft_archives[index]}.meta.json"' "${backup}" || fail 'retention does not remove matching metadata sidecars'

if grep -Eq '/etc/containers/systemd/minecraft\.container.*(cp|mv|tar)|bootc rollback' "${restore}"; then
    fail 'Minecraft restore must not restore Quadlet or bootc deployment'
fi

echo 'restore workflow regression tests passed.'
