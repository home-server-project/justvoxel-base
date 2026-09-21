#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail(){ echo "FAIL: $*" >&2; exit 1; }
# Static workflow invariants that require the full appliance at runtime.
import_files=(
    "${repo_root}/mjust/libexec/migration-import"
    "${repo_root}/mjust/libexec/migration-import-common.sh"
    "${repo_root}/mjust/libexec/migration-import-plan-source.sh"
    "${repo_root}/mjust/libexec/migration-import-plan-destination.sh"
    "${repo_root}/mjust/libexec/migration-import-activate.sh"
)
import_text="$(cat "${import_files[@]}")"
import_frontend="${repo_root}/mjust/libexec/migration-import"
import_backend="${repo_root}/mjust/libexec/migration-import-backend"
grep -Fq 'migration-api.sh' "${import_frontend}" || fail 'migration Import frontend does not use the shared API helper'
grep -Fq 'jv_migration_import_plan_review' "${import_frontend}" || fail 'migration Import frontend does not plan through the Agent'
grep -Fq 'jv_migration_import_apply' "${import_frontend}" || fail 'migration Import frontend does not apply through the Agent'
grep -Fq 'jv_migration_monitor_operation' "${import_frontend}" || fail 'migration Import frontend does not monitor the persistent Agent operation'
grep -Fq 'interrupt-safety.sh' "${import_backend}" || fail 'shared migration import backend does not load interruption safety'
grep -Fq 'JV_MIGRATION_API_MODE' "${import_backend}" || fail 'shared migration Import backend has no non-interactive Agent mode'
for api_file in "${repo_root}/mjust/libexec/migration-import-common.sh" "${repo_root}/mjust/libexec/migration-import-plan-source.sh" "${repo_root}/mjust/libexec/migration-import-plan-destination.sh" "${repo_root}/mjust/libexec/migration-import-activate.sh"; do
    grep -Fq 'JV_MIGRATION_API_MODE' "${api_file}" || fail "Import backend component lacks Agent-mode handling: ${api_file}"
done
export_frontend="${repo_root}/mjust/libexec/migration-export"
exporter="${repo_root}/mjust/libexec/migration-export-backend"
recovery="${repo_root}/mjust/libexec/migration-recover"
recovery_backend="${repo_root}/mjust/libexec/migration-recover-backend"
transport_files=(
    "${repo_root}/mjust/libexec/migration-transport.sh"
    "${repo_root}/mjust/libexec/migration-transport-device.sh"
    "${repo_root}/mjust/libexec/migration-transport-network.sh"
    "${repo_root}/mjust/libexec/migration-transport-ui.sh"
)
transport_text="$(cat "${transport_files[@]}")"
import_source_helper="${repo_root}/mjust/libexec/migration-import-source.sh"
common="${repo_root}/mjust/libexec/common.sh"
migration_common="${repo_root}/mjust/libexec/migration-common.sh"
validate_backend="${repo_root}/mjust/libexec/validate-backend"
template="${repo_root}/templates/config/minecraft.env.in"
menu="${repo_root}/mjust/libexec/menu"
justfile="${repo_root}/mjust/justfile"

# shellcheck disable=SC1090
source "${migration_common}"
available_bytes="$(jv_migration_available_bytes "${repo_root}")" || fail 'migration free-space helper failed on a local path'
[[ ${available_bytes} =~ ^[0-9]+$ ]] || fail 'migration free-space helper did not return a numeric byte count'
(( available_bytes > 0 )) || fail 'migration free-space helper returned no available space'

for text in \
    'Type IMPORT to continue:' \
    'The source eula.txt, if present, is NOT accepted' \
    'plugins are executable server code' \
    'jv_migration_snapshot_runtime' \
    'jv_migration_rollback_data' \
    'jv_migration_assert_fresh_selinux_path' \
    'jv_migration_remove_fresh_selinux_rule' \
    'restore-runtime-validate' \
    'jv_stop_minecraft_adaptive' \
    '/usr/libexec/justvoxel/mjust/validate' \
    'JUSTVOXEL_REGENERATE_RCON=1' \
    'online-mode=false' \
    'MINECRAFT_VERSION_MODE=pinned' \
    'mjust migration-recover'; do
    grep -Fq "${text}" <<< "${import_text}" || fail "import workflow invariant missing: ${text}"
done
grep -Fq 'migration-api.sh' "${export_frontend}" || fail 'migration Export frontend does not use shared API helper'
for text in '.partial' 'verify-native' 'flock -n' 'jv_player_check_before_interrupt' 'sync -f' 'mv -- "${partial}"'; do
    grep -Fq "${text}" "${exporter}" || fail "shared export backend invariant missing: ${text}"
done
for fs in ext4 xfs btrfs vfat exfat; do
    grep -Fq "${fs}" <<< "${transport_text}" || fail "temporary media allowlist missing ${fs}"
done
grep -Fq 'NTFS removable media is not supported' <<< "${transport_text}" || fail 'NTFS refusal is missing'
grep -Fq 'jv_migration_import_source_prepare' "${import_source_helper}" || fail 'shared Import source resolver is missing'
grep -Fq 'jv_migration_import_source_entries_json' "${import_source_helper}" || fail 'shared Import source candidate discovery is missing'
grep -Fq 'jv_migration_import_source_cleanup' "${import_source_helper}" || fail 'shared Import source cleanup is missing'
if grep -Fq 'storage_write_network_fstab' <<< "${transport_text}"; then fail 'temporary migration transport must not write fstab'; fi
grep -Fq 'JV_MIGRATION_SMB_CREDENTIALS="${JV_MIGRATION_TRANSPORT_ROOT}/smb.credentials"' <<< "${transport_text}" || fail 'temporary SMB credentials are not under /run transport state'
grep -Fq 'chmod 0600 "${JV_MIGRATION_SMB_CREDENTIALS}"' <<< "${transport_text}" || fail 'temporary SMB credentials are not mode 0600'
for token in GAME_MODE DIFFICULTY WHITELIST_ENABLED ENFORCE_WHITELIST; do
    grep -Fq "@@${token}@@" "${template}" || fail "Minecraft template missing migration-safe token ${token}"
done
grep -Fq 'GAME_MODE="${GAME_MODE:-survival}"' "${common}" || fail 'backward-compatible game-mode default missing'
grep -Fq 'WHITELIST_ENABLED="${WHITELIST_ENABLED:-yes}"' "${common}" || fail 'backward-compatible whitelist default missing'
grep -Fq 'JUSTVOXEL_REGENERATE_RCON' "${common}" || fail 'RCON regeneration support missing'

grep -Fq 'migration-api.sh' "${recovery}" || fail 'migration Recovery frontend does not use shared API helper'
grep -Fq 'jv_migration_recovery_plan' "${recovery}" || fail 'migration Recovery frontend does not plan through the Agent'
grep -Fq 'jv_migration_recovery_apply' "${recovery}" || fail 'migration Recovery frontend does not apply through the Agent'
grep -Fq 'rolled-back-fresh' "${recovery_backend}" || fail 'fresh unconfigured rollback recovery is not supported'
grep -Fq '/usr/libexec/justvoxel/mjust/restore-runtime-validate' "${recovery_backend}" || fail 'configured migration recovery must validate the restored Minecraft runtime'
grep -Fq 'jv_migration_runtime_paths' "${recovery_backend}" || fail 'fresh migration recovery does not prove generated runtime files are absent'
grep -Fq 'FINALIZE ROLLBACK' "${recovery_backend}" || fail 'migration recovery destructive confirmation missing'
grep -Fq 'jv_migration_clear_recovery' "${recovery_backend}" || fail 'migration recovery registry cleanup missing'

grep -Fq "warn '/dev/zram0 is not available" "${validate_backend}" || fail 'missing advisory zram-disabled validation path'
grep -Fq "warn 'zram0 is not active as swap" "${validate_backend}" || fail 'missing advisory zram-inactive validation path'
if grep -Fq "fail '/dev/zram0 is not available" "${validate_backend}"; then fail 'zram absence must not be a fatal appliance validation failure'; fi
if grep -Fq "fail 'zram0 is not active as swap" "${validate_backend}"; then fail 'zram inactivity must not be a fatal appliance validation failure'; fi

grep -Fq 'Migration' "${menu}" || fail 'Migration TUI missing'
grep -Fq 'Import existing Minecraft server' "${menu}" || fail 'fresh-appliance import entry missing'
grep -Fq 'Recover / finalize interrupted import' "${menu}" || fail 'migration recovery entry missing from TUI'
grep -Fq 'mjust export' "${menu}" || fail 'export command not discoverable in TUI'
grep -Fq 'mjust import' "${menu}" || fail 'import command not discoverable in TUI'
grep -Fq 'mjust migration-recover' "${menu}" || fail 'migration recovery command not discoverable in TUI'
grep -Fq 'export destination=""' "${justfile}" || fail 'mjust export recipe missing'
grep -Fq 'import source=""' "${justfile}" || fail 'mjust import recipe missing'
grep -Fq 'migration-recover transaction=""' "${justfile}" || fail 'mjust migration-recover recipe missing'

echo 'migration workflow invariant tests passed.'
