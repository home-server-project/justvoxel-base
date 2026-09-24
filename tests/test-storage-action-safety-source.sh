#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base="${repo_root}/mjust/libexec/storage-common-base.sh"
discovery="${repo_root}/mjust/libexec/admin-discovery-json"
common="${repo_root}/mjust/libexec/admin-storage-actions-common.sh"
planner="${repo_root}/mjust/libexec/admin-storage-actions-json"
apply="${repo_root}/mjust/libexec/admin-storage-actions-apply.sh"
provision="${repo_root}/mjust/libexec/admin-storage-provision-json"
migration_transaction="${repo_root}/mjust/libexec/admin-data-migration-transaction-json"

for script in "${base}" "${discovery}" "${common}" "${planner}" "${apply}" "${provision}" "${migration_transaction}"; do
    bash -n "${script}"
done

grep -Fq 'storage_mountpoint_is_system' "${base}" || {
    echo 'ERROR: shared system-mount classification is missing.' >&2
    exit 1
}
grep -Fq 'startsWith("/sysroot/")' "${base}" && {
    echo 'ERROR: invalid jq spelling in system-mount detection.' >&2
    exit 1
}
grep -Fq 'startswith("/sysroot/")' "${base}" || {
    echo 'ERROR: bootc/OSTree /sysroot mount detection is missing.' >&2
    exit 1
}
grep -Fq 'storage_create_partition' "${base}" || {
    echo 'ERROR: shared partition creation helper is missing.' >&2
    exit 1
}
grep -Fq 'mkpart justvoxel' "${base}" || {
    echo 'ERROR: GPT partition creation no longer uses a GPT partition name.' >&2
    exit 1
}
if grep -Fq 'mkpart primary xfs' "${base}" "${provision}" "${migration_transaction}"; then
    echo 'ERROR: invalid duplicated mkpart primary xfs command remains.' >&2
    exit 1
fi
grep -Fq 'storage_create_partition "${DEVICE}"' "${provision}" || {
    echo 'ERROR: backup provisioning does not use shared partition creation.' >&2
    exit 1
}
grep -Fq 'storage_create_partition "${device}"' "${migration_transaction}" || {
    echo 'ERROR: Minecraft migration does not use shared partition creation.' >&2
    exit 1
}

grep -Fq 'if (( ${#label} > 12 )); then' "${base}" || {
    echo 'ERROR: XFS label length guard is missing from shared storage formatting.' >&2
    exit 1
}
for invalid_label in JUSTVOXEL_BACKUP JUSTVOXEL_DATA JUSTVOXEL_STORAGE; do
    if grep -Fq "${invalid_label}" "${base}" "${apply}" "${provision}" "${migration_transaction}"; then
        echo "ERROR: overlong XFS label remains: ${invalid_label}" >&2
        exit 1
    fi
done
for valid_label in JV_BACKUP JV_DATA JV_STORAGE; do
    if (( ${#valid_label} > 12 )); then
        echo "ERROR: XFS label exceeds 12 characters: ${valid_label}" >&2
        exit 1
    fi
done
if grep -Fq 'mkfs.xfs' "${provision}" "${migration_transaction}"; then
    echo 'ERROR: storage provisioning bypasses the shared XFS label-length guard.' >&2
    exit 1
fi
grep -Fq 'storage_mkfs_xfs JV_BACKUP' "${provision}" || {
    echo 'ERROR: backup provisioning is not using the validated short XFS label.' >&2
    exit 1
}
grep -Fq 'storage_mkfs_xfs JV_DATA' "${migration_transaction}" || {
    echo 'ERROR: Minecraft migration is not using the validated short XFS label.' >&2
    exit 1
}
grep -Fq 'storage_mkfs_xfs JV_STORAGE "${device}"' "${apply}" || {
    echo 'ERROR: generic partition formatting is not using the validated short XFS label.' >&2
    exit 1
}
if grep -Fq 'mkfs.xfs' "${apply}"; then
    echo 'ERROR: generic partition formatting bypasses the shared XFS label-length guard.' >&2
    exit 1
fi
grep -Fq 'storage_target_block_device' "${base}" || {
    echo 'ERROR: system-disk detection lost bootc/OSTree block-device resolution.' >&2
    exit 1
}
grep -Fq 'source="${source%%' "${base}" || {
    echo 'ERROR: system-disk detection no longer strips findmnt subpath notation.' >&2
    exit 1
}
grep -Fq 'MAJ:MIN' "${base}" || {
    echo 'ERROR: system-disk detection lost major:minor fallback identity.' >&2
    exit 1
}
grep -Fq 'startswith("zram")' "${discovery}" || {
    echo 'ERROR: storage discovery can expose zram as physical storage.' >&2
    exit 1
}
grep -Fq 'storage_require_identified_system_disk' "${common}" || {
    echo 'ERROR: generic storage actions must fail closed when the system disk cannot be identified.' >&2
    exit 1
}
grep -Fq 'storage_action_is_system_partition' "${common}" || {
    echo 'ERROR: generic storage actions lost system-partition protection.' >&2
    exit 1
}
grep -Fq 'swap partitions are not managed from the storage browser' "${common}" || {
    echo 'ERROR: generic storage actions lost swap protection.' >&2
    exit 1
}
grep -Fq 'confirmation="FORMAT ${device}"' "${planner}" || {
    echo 'ERROR: destructive partition formatting lost exact typed confirmation.' >&2
    exit 1
}
grep -Fq 'storage_action_mountable_filesystem "${filesystem}"' "${planner}" || {
    echo 'ERROR: generic mount planning is not using the expanded mountable-filesystem policy.' >&2
    exit 1
}
grep -Fq 'storage_action_managed_filesystem "${filesystem}"' "${planner}" || {
    echo 'ERROR: format planning lost the managed-filesystem boundary.' >&2
    exit 1
}
grep -Fq 'storage_action_mount_device "${device}" "${mountpoint}" "${filesystem}"' "${apply}" || {
    echo 'ERROR: temporary mount apply is not using filesystem-specific mount behavior.' >&2
    exit 1
}
grep -Fq 'mounted filesystem did not match the reviewed filesystem' "${apply}" || {
    echo 'ERROR: temporary mount apply lost post-mount identity verification.' >&2
    exit 1
}
grep -Fq 'submitted_fingerprint' "${apply}" || {
    echo 'ERROR: storage apply lost reviewed device fingerprint verification.' >&2
    exit 1
}
grep -Fq 'The selected partition changed after Review. Nothing was changed.' "${apply}" || {
    echo 'ERROR: storage apply lost its bounded changed-device failure.' >&2
    exit 1
}

echo 'New Storage action safety source checks passed.'
