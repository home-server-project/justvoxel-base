#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
common="${repo_root}/mjust/libexec/admin-storage-actions-common.sh"
planner="${repo_root}/mjust/libexec/admin-storage-actions-json"
apply="${repo_root}/mjust/libexec/admin-storage-actions-apply.sh"

# Exercise the shared target validator with deterministic device discovery.
# No real device or mount operations are used.
source <(sed -n '/^storage_action_validate_target() {/,/^}/p' "${common}")
storage_require_identified_system_disk() { return 0; }
storage_validate_disk() { [[ $1 == /dev/system || $1 == /dev/usb ]]; }
storage_validate_partition() { [[ $1 == /dev/system1 || $1 == /dev/system4 ]]; }
storage_action_parent_disk() { printf '/dev/system\n'; }
storage_action_is_system_partition() { [[ $1 == /dev/system1 ]]; }
storage_disk_is_system() { [[ $1 == /dev/system ]]; }
storage_action_filesystem() { printf '%s\n' "${fake_filesystem}"; }
source <(sed -n '/^storage_action_mountable_filesystem() {/,/^}/p' "${common}")
storage_action_host_mountpoints() { return 0; }
storage_mount_is_critical() { return 1; }
lsblk() {
    if [[ $* == *'-dnro TYPE'* ]]; then
        case "${*: -1}" in
            /dev/system|/dev/usb) printf 'disk\n' ;;
            *) printf 'part\n' ;;
        esac
    else
        printf '%s %s\n' "${*: -1}" 'device'
    fi
}
fake_filesystem=xfs
if storage_action_validate_target /dev/system1; then
    echo 'ERROR: exact system partition was accepted.' >&2; exit 1
fi
storage_action_validate_target /dev/system4
for fake_filesystem in xfs ext4 btrfs ntfs vfat exfat; do
    storage_action_validate_target /dev/system4
    storage_action_validate_target /dev/usb
    if storage_action_validate_target /dev/system; then
        echo 'ERROR: whole system disk was accepted.' >&2; exit 1
    fi
done

source <(sed -n '/^storage_action_validate_deletion() {/,/^}/p' "${common}")
cat() { printf '4\n'; }
storage_partition_table_type() { printf '%s\n' "${fake_table}"; }
parted() { printf '4:1MiB:4096MiB:4095MiB:%s:;\n' "${fake_partition_type}"; }
fake_table=gpt
fake_partition_type=xfs
storage_action_validate_deletion /dev/system4
if storage_action_validate_deletion /dev/system1; then
    echo 'ERROR: delete accepted a system partition.' >&2; exit 1
fi
if storage_action_validate_deletion /dev/usb; then
    echo 'ERROR: delete accepted a whole disk.' >&2; exit 1
fi
fake_table=msdos
fake_partition_type=extended
if storage_action_validate_deletion /dev/system4; then
    echo 'ERROR: delete accepted an extended partition.' >&2; exit 1
fi
fake_partition_type=primary
storage_action_validate_deletion /dev/system4

# Both format and mount use the same supported-filesystem policy.
format_block="$(sed -n '/^        format)/,/^        delete_partition|initialize_disk)/p' "${planner}")"
grep -Fq 'storage_action_mountable_filesystem "${filesystem}"' <<< "${format_block}"
if grep -Fq 'storage_action_managed_filesystem' <<< "${format_block}"; then
    echo 'ERROR: portable formatting remains blocked.' >&2; exit 1
fi
delete_block="$(sed -n '/^        delete_partition|initialize_disk)/,/^        create_partition)/p' "${planner}")"
grep -Fq 'storage_action_is_system_partition "${device}"' <<< "${delete_block}"
grep -Fq 'storage_validate_partition "${device}"' <<< "${delete_block}"
grep -Fq 'confirmation="DELETE PARTITION ${device}"' <<< "${delete_block}"
grep -Fq 'confirmation="ERASE DISK ${device}"' <<< "${delete_block}"
grep -Fq 'parted -s -- "${parent}" rm "${number}"' "${apply}"
grep -Fq 'mklabel gpt' "${apply}"
grep -Fq 'storage_action_plan_partition' "${planner}"
grep -Fq 'submitted_fingerprint' "${apply}"
grep -Fq 'jv_stop_minecraft_adaptive' "${apply}"
if grep -Eq 'resizepart|smb|nfs|credentials' "${planner}" "${apply}"; then
    echo 'ERROR: local block actions expanded into resizing or network storage.' >&2; exit 1
fi

echo 'Local block storage contracts passed.'
