#!/usr/bin/bash
set -euo pipefail

helper=mjust/libexec/admin-storage-provision-json

for required in \
    'ERASE ${DEVICE}' \
    'FORMAT ${DEVICE}' \
    'CREATE PARTITION ${DEVICE}' \
    'EXPECTED_FINGERPRINT' \
    'device or disk layout changed after Review' \
    'storage_require_identified_system_disk' \
    'is_system_disk "${DEVICE}"' \
    'currently unallocated space' \
    'wipefs -a' \
    'storage_mkfs_xfs JV_BACKUP' \
    'storage_create_partition "${DEVICE}"' \
    'parted -s -a optimal'; do
    grep -Fq -- "${required}" "${helper}" || {
        echo "missing advanced-storage safety invariant: ${required}" >&2
        exit 1
    }
done

# Whole-disk provisioning must reject the identified system disk before wipefs.
system_guard_line="$(grep -nF 'is_system_disk "${DEVICE}" &&' "${helper}" | head -n1 | cut -d: -f1)"
wipe_line="$(grep -nF 'wipefs -a -- "${DEVICE}"' "${helper}" | head -n1 | cut -d: -f1)"
[[ -n ${system_guard_line} && -n ${wipe_line} && ${system_guard_line} -lt ${wipe_line} ]] || {
    echo 'system-disk guard must execute before whole-disk wipefs' >&2
    exit 1
}

echo 'advanced WebUI storage provisioning safety checks passed'
