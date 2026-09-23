#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base="${repo_root}/mjust/libexec/storage-common-base.sh"
discovery="${repo_root}/mjust/libexec/admin-discovery-json"
common="${repo_root}/mjust/libexec/admin-storage-actions-common.sh"
planner="${repo_root}/mjust/libexec/admin-storage-actions-json"
apply="${repo_root}/mjust/libexec/admin-storage-actions-apply.sh"

for script in "${base}" "${discovery}" "${common}" "${planner}" "${apply}"; do
    bash -n "${script}"
done

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
