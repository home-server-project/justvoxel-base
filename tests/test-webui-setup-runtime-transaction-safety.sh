#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_helper="${repo_root}/mjust/libexec/admin-setup-runtime-transaction-json"
common="${repo_root}/mjust/libexec/common.sh"
discovery="${repo_root}/mjust/libexec/admin-discovery-json"
apply="${repo_root}/management/cmd/justvoxel-management-agent/admin_setup_apply.go"
worker="${repo_root}/management/cmd/justvoxel-management-agent/setup_worker.go"
runner="${repo_root}/management/cmd/justvoxel-management-agent/setup_runtime_transaction.go"
planner="${repo_root}/mjust/libexec/admin-setup-plan-json"

for file in "${runtime_helper}" "${common}" "${discovery}" "${apply}" "${worker}" "${runner}" "${planner}"; do
    [[ -f ${file} ]] || { echo "missing A5.5 file: ${file}" >&2; exit 1; }
done
bash -n "${runtime_helper}"
bash -n "${common}"
bash -n "${discovery}"
bash -n "${planner}"

grep -Fq 'eula_accepted' "${apply}"
grep -Fq 'startSetupWorker' "${apply}"
grep -Fq 'context.WithTimeout(context.Background(), setupWorkerTimeout)' "${worker}"
grep -Fq 'JV_SETUP_IN_PROGRESS' "${runtime_helper}"
grep -Fq 'JV_SETUP_IN_PROGRESS' "${discovery}"
grep -Fq 'JV_SETUP_IN_PROGRESS' "${repo_root}/mjust/libexec/web-status-json"
grep -Fq 'readonly JV_SETUP_IN_PROGRESS=/var/lib/justvoxel/management/setup-in-progress' "${common}"
grep -Fq 'render_runtime_files' "${common}"
grep -Fq 'activate_backup_timer' "${common}"
grep -Fq 'render_runtime_files' "${runtime_helper}"
grep -Fq 'activate_backup_timer' "${runtime_helper}"
grep -Fq 'wait_for_rcon 900' "${runtime_helper}"
grep -Fq '/usr/libexec/justvoxel/mjust/validate' "${runtime_helper}"
grep -Fq 'runtime_rollback' "${runner}"
grep -Fq 'rollbackSetupStorage' "${runner}"
grep -Fq 'MinecraftUID' "${runner}"
grep -Fq 'minecraft_uid' "${planner}"

if grep -Eq 'smb_password|SMBPassword|password[[:space:]]*:' "${runtime_helper}" "${runner}"; then
    echo 'A5.5 runtime transaction must not contain the SMB execution secret.' >&2
    exit 1
fi

if grep -Eq 'exec\.Command(Context)?\([^,]+,[[:space:]]*request|/bin/(sh|bash)[[:space:]]+-c' "${runner}" "${worker}"; then
    echo 'A5.5 introduced an arbitrary command execution surface.' >&2
    exit 1
fi

# Manual mjust setup must retain its existing wrapper behavior.
grep -Fq 'render_runtime' "${repo_root}/mjust/libexec/setup"

echo 'WebUI first-run A5.5 runtime transaction safety checks passed.'
