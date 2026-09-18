#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/admin-setup-storage-transaction-json"
common="${repo_root}/mjust/libexec/admin-setup-storage-transaction-common.sh"
apply="${repo_root}/mjust/libexec/admin-setup-storage-transaction-apply.sh"
actions="${repo_root}/mjust/libexec/admin-setup-storage-transaction-actions.sh"
agent_apply="${repo_root}/management/cmd/justvoxel-management-agent/admin_setup_apply.go"
agent_runner="${repo_root}/management/cmd/justvoxel-management-agent/setup_storage_transaction.go"

for file in "${helper}" "${common}" "${apply}" "${actions}" "${agent_apply}" "${agent_runner}"; do
    [[ -f ${file} ]] || { echo "missing A5.4 file: ${file}" >&2; exit 1; }
    bash -n "${file}" 2>/dev/null || [[ ${file} == *.go ]]
done

for forbidden in     'mkfs(\.[[:alnum:]]+)?[[:space:]]'     'wipefs[[:space:]]'     'parted[[:space:]]'     'fdisk[[:space:]]'     'sfdisk[[:space:]]'     'storage-provision'     'admin-backup-storage-json'; do
    if grep -Eq "${forbidden}" "${helper}" "${common}" "${apply}" "${actions}"; then
        echo "A5.4 storage helper contains forbidden operation: ${forbidden}" >&2
        exit 1
    fi
done

grep -Fq 'del(.smb_password)' "${common}"
grep -Fq 'A54_SMB_CREDENTIALS' "${common}"
grep -Fq 'A53_TRANSACTION_ROOT' "${common}"
grep -Fq 'fstab.before' "${apply}"
grep -Fq 'mounts_by_transaction' "${apply}"
grep -Fq 'directories_created' "${apply}"
grep -Fq 'fstab_after_sha256' "${apply}"
grep -Fq 'credentials_after_sha256' "${apply}"
grep -Fq 'chmod 0600 "${A54_SMB_CREDENTIALS}"' "${apply}"
grep -Fq 'rw,_netdev,nofail,x-systemd.mount-timeout=20s' "${apply}"
grep -Fq 'vers=3.0,rw,_netdev,nofail,x-systemd.mount-timeout=20s' "${apply}"
grep -Fq 'rollback' "${helper}"

if grep -Eq 'executeSetupStorage|executeSetupLocalStorage|admin-setup-storage-transaction-json|runAdminSetupStorageTransactionHelper' "${agent_apply}"; then
    echo 'A5.4 storage execution was wired into production Apply too early.' >&2
    exit 1
fi

grep -Fq 'storage_preflight' "${agent_runner}"
grep -Fq 'storage_verified' "${agent_runner}"
grep -Fq 'operationNeedsAttention' "${agent_runner}"

echo 'WebUI first-run A5.4 network storage transaction safety checks passed.'
