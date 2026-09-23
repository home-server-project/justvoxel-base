#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
common="${repo_root}/mjust/libexec/admin-storage-actions-common.sh"
actions="${repo_root}/mjust/libexec/admin-storage-actions-json"
mounts="${repo_root}/mjust/libexec/admin-storage-mounts-json"
backup="${repo_root}/mjust/libexec/admin-backup-storage-common.sh"
migration="${repo_root}/mjust/libexec/admin-data-migration-plan-json"
setup_storage="${repo_root}/mjust/libexec/admin-setup-storage-transaction-common.sh"
health="${repo_root}/build_files/validate/common.sh"
packages="${repo_root}/build_files/packages.env"

for script in "${common}" "${actions}" "${mounts}" "${backup}" "${migration}" "${setup_storage}" "${health}"; do
    bash -n "${script}"
done

grep -Fq 'storage_action_mountable_filesystem()' "${common}"
grep -Fq 'xfs|ext4|btrfs|ntfs|vfat|exfat) return 0' "${common}"
grep -Fq 'storage_action_managed_filesystem()' "${common}"
grep -Fq 'xfs|ext4|btrfs) return 0' "${common}"
grep -Fq 'storage_action_managed_filesystem "$1"' "${common}"

for existing_path in "${actions}" "${mounts}"; do
    grep -Fq 'storage_action_supported_filesystem' "${existing_path}" || {
        echo "ERROR: Step 1 must not change existing mount behavior yet: ${existing_path}" >&2
        exit 1
    }
    if grep -Fq 'storage_action_mountable_filesystem' "${existing_path}"; then
        echo "ERROR: Step 1 must not wire the expanded mountable policy into runtime behavior yet: ${existing_path}" >&2
        exit 1
    fi
done

grep -Fq 'xfs|ext4|btrfs' "${backup}"
grep -Fq 'xfs|ext4|btrfs' "${migration}"
grep -Fq 'filesystem} == xfs || ${filesystem} == ext4 || ${filesystem} == btrfs' "${setup_storage}"

for restricted in "${backup}" "${migration}" "${setup_storage}"; do
    if grep -Eq 'ntfs|vfat|exfat' "${restricted}"; then
        echo "ERROR: managed Minecraft/Backup/setup storage policy must remain Linux-native only: ${restricted}" >&2
        exit 1
    fi
done

grep -Fq 'ntfs-3g \' "${packages}"
grep -Fq 'mount.ntfs-3g' "${health}"
grep -Fq "'*/kernel/fs/fat/fat.ko*'" "${health}"
grep -Fq "'*/kernel/fs/fat/vfat.ko*'" "${health}"
grep -Fq "'*/kernel/fs/exfat/exfat.ko*'" "${health}"

echo 'Storage filesystem capability and policy source checks passed.'
