#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"
fail() { echo "FAIL: $*" >&2; exit 1; }
# Old journals retain a read-only compatibility endpoint; execution must be gone.
if rg -n '/v1/admin/restore/(backups|plan|apply)|adminRestoreBackups|adminRestorePlan|adminRestoreApply|authoritativeAdminRestorePlan|adminRestoreDiscoveryHelper|adminRestorePlanHelper|adminRestoreTransactionHelper|runAdminRestoreTransactionHelper|admin-restore-(discovery|plan|transaction)-json|restore-api\.sh|restoreProgressStatus|/api/restore/progress/' --glob '!*_test.go' management webui mjust; then
    fail 'legacy global Restore execution or presentation remains'
fi
for file in admin_restore.go admin_restore_plan.go admin_restore_apply.go restore_worker.go; do
    [[ ! -e management/cmd/justvoxel-management-agent/${file} ]] || fail "legacy worker remains: ${file}"
done
for file in instance-restore restore backup-common.sh restore-common.sh restore-archive restore-runtime-validate; do
    [[ -f mjust/libexec/${file} ]] || fail "shared/selected helper removed: ${file}"
done
for file in management/cmd/justvoxel-management-agent/minecraft_instance_restore.go mjust/libexec/instance-restore mjust/libexec/restore webui/internal/server/selected_backups.go; do
    if rg -n 'minecraft\.service|podman (exec|start|stop|restart) minecraft\b|/v1/admin/restore/' "${file}"; then
        fail "selected Restore uses a fixed target: ${file}"
    fi
done
for action in plan apply status recover; do
    grep -Fq "/backups/restore/${action}" management/cmd/justvoxel-management-agent/minecraft_instance_restore.go || fail "selected route missing: ${action}"
done
grep -Fq 'o.currentRestoreID != ""' management/cmd/justvoxel-management-agent/minecraft_instance_restore.go || fail 'legacy journal exclusion removed'
grep -Fq 'acquireInstanceRestoreExclusion()' management/cmd/justvoxel-management-agent/minecraft_instance_restore.go || fail 'backup exclusion removed'
grep -Fq 'acquirePluginRuntimeLock()' management/cmd/justvoxel-management-agent/minecraft_instance_restore.go || fail 'maintenance exclusion removed'
grep -Fq 'restore-runtime-validate' mjust/libexec/admin-data-migration-transaction-json || fail 'physical migration validator removed'
echo 'PASS: global Restore retired; selected identity paths and shared safety retained.'
