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

for mount_path in "${actions}" "${mounts}"; do
    grep -Fq 'storage_action_mountable_filesystem' "${mount_path}" || {
        echo "ERROR: generic mount path is not using the expanded mountable-filesystem policy: ${mount_path}" >&2
        exit 1
    }
done

grep -Fq 'storage_action_mount_type()' "${common}"
grep -Fq "ntfs) printf 'ntfs-3g" "${common}"
grep -Fq 'storage_action_mount_options()' "${common}"
grep -Fq 'windows_names' "${common}"
grep -Fq 'fmask=0133,dmask=0022' "${common}"
grep -Fq 'utf8=1' "${common}"
grep -Fq 'storage_action_mount_device()' "${common}"
grep -Fq 'storage_action_managed_filesystem "${filesystem}"' "${actions}"

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

echo 'Storage filesystem mount behavior and policy source checks passed.'
