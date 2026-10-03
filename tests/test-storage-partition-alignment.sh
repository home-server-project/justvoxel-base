#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
common="${repo_root}/mjust/libexec/admin-storage-actions-common.sh"
planner="${repo_root}/mjust/libexec/admin-storage-actions-json"
apply="${repo_root}/mjust/libexec/admin-storage-actions-apply.sh"

# Load only functions. All disk commands below are deterministic mocks; neither
# planning nor applying this fixture touches a device.
source <(sed -n '/^storage_action_plan_partition() {/,/^}/p' "${common}")
source <(sed -n '/^json_error() {/,/^}/p; /^append_warning() {/,/^}/p; /^prepare_plan() {/,/^}/p' "${planner}")
source "${apply}"
fake_logical=512
fake_optimal=0
fake_offset=0
free_first=34
free_last=20971486
selected_start=0.02MiB
selected_end=10240MiB
interior=false
trailing=false
changed_during_read=false
parted() {
    if [[ $* == *'unit s print free'* ]]; then
        printf 'BYT;\n/dev/disk:20971520s:virtblk:%s:%s:gpt:Virtio Block Device:;\n' "${fake_logical}" "${fake_logical}"
        if [[ ${interior} == true || ${trailing} == true ]]; then
            printf '1:2048s:%ss:%ss:xfs:existing:;\n' "$((free_first - 1))" "$((free_first - 2048))"
        fi
        printf '1:%ss:%ss:%ss:free;\n' "${free_first}" "${free_last}" "$((free_last - free_first + 1))"
        if [[ ${interior} == true ]]; then
            printf '2:%ss:20971486s:%ss:xfs:neighbor:;\n' "$((free_last + 1))" "$((20971486 - free_last))"
        fi
    elif [[ $* == *'unit MiB print free'* ]]; then
        printf 'BYT;\n/dev/disk:10240MiB:virtblk:%s:%s:gpt:Virtio Block Device:;\n' "${fake_logical}" "${fake_logical}"
        if [[ ${interior} == true || ${trailing} == true ]]; then
            printf '1:1MiB:2048MiB:2047MiB:xfs:existing:;\n'
        fi
        printf '1:%s:%s:10240MiB:free;\n' "${selected_start}" "${selected_end}"
        if [[ ${interior} == true || ${changed_during_read} == true ]]; then
            printf '2:8192MiB:10240MiB:2048MiB:xfs:neighbor:;\n'
        fi
    else
        echo 'ERROR: unexpected partition operation.' >&2
        return 1
    fi
}
blockdev() {
    case "$1" in
        --getss|--getpbsz|--getiomin) printf '%s\n' "${fake_logical}" ;;
        --getioopt) printf '%s\n' "${fake_optimal}" ;;
        --getalignoff) printf '%s\n' "${fake_offset}" ;;
        *) return 1 ;;
    esac
}
storage_action_real_device() { printf '%s\n' "$1"; }
storage_require_identified_system_disk() { return 0; }
storage_validate_disk() { [[ $1 == /dev/disk || $1 == /dev/system ]]; }
storage_partition_table_type() { printf 'gpt\n'; }
storage_disk_is_system() { [[ $1 == /dev/system ]]; }
storage_action_fingerprint() { printf '%s\n' "$2"; }
lsblk() { printf '%s disk\n' "${*: -1}"; }

prepare_plan <<< '{"operation":"create_partition","device":"/dev/disk","free_start":"0.02MiB","size_gib":"5"}'
[[ ${STORAGE_ACTION_START} == 2048s ]]
[[ ${STORAGE_ACTION_END} == 10487807s ]]
[[ $(jq -r '.size_bytes' <<< "${PLAN_JSON}") == 5368709120 ]]
[[ $(jq -r '.free_start' <<< "${PLAN_JSON}") == 0.02MiB ]]
[[ $(jq -r '.target_filesystem' <<< "${PLAN_JSON}") == xfs ]]

# Exercise the actual Apply call, stopping at the mocked destructive boundary.
# It must receive exact aligned sectors, never the display selection coordinate.
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT
storage_create_partition() { printf '%s\n' "$*" > "${work_dir}/create"; return 1; }
reviewed="$(jq '{operation,device,free_start,size_gib,fingerprint,confirmation}' <<< "${PLAN_JSON}")"
result="$(storage_action_apply_json <<< "${reviewed}")"
[[ $(cat "${work_dir}/create") == '/dev/disk 2048s 10487807s' ]]
[[ $(jq -r '.applied' <<< "${result}") == false ]]
rm "${work_dir}/create"

prepare_plan <<< '{"operation":"create_partition","device":"/dev/disk","free_start":"0.02MiB","size_gib":"all"}'
[[ ${STORAGE_ACTION_START} == 2048s && ${STORAGE_ACTION_END} == 20969471s ]]
(( ${STORAGE_ACTION_START%s} >= free_first && ${STORAGE_ACTION_END%s} <= free_last ))

# A sector change hidden by the rounded MiB display invalidates Review.
reviewed="$(jq '{operation,device,free_start,size_gib,fingerprint,confirmation}' <<< "${PLAN_JSON}")"
free_last=$((free_last - 1))
result="$(storage_action_apply_json <<< "${reviewed}")"
[[ $(jq -r '.ok' <<< "${result}") == false && ! -e ${work_dir}/create ]]
free_last=$((free_last + 1))
if storage_action_plan_partition /dev/disk 1MiB all; then
    echo 'ERROR: stale free-space identity was accepted.' >&2; exit 1
fi
if storage_action_plan_partition /dev/disk 0.02MiB 10; then
    echo 'ERROR: alignment slack was counted as usable capacity.' >&2; exit 1
fi
changed_during_read=true
if storage_action_plan_partition /dev/disk 0.02MiB all; then
    echo 'ERROR: inconsistent geometry snapshots were accepted.' >&2; exit 1
fi
changed_during_read=false

# Interior space starts beyond an unaligned neighbor and ends before another.
interior=true
free_first=4194305
free_last=16777214
selected_start=2048MiB
selected_end=8192MiB
prepare_plan <<< '{"operation":"create_partition","device":"/dev/system","free_start":"2048MiB","size_gib":"5"}'
[[ ${STORAGE_ACTION_START} == 4196352s ]]
[[ ${STORAGE_ACTION_SIZE} == 5368709120 ]]
(( ${STORAGE_ACTION_END%s} <= free_last ))
[[ ${PLAN_WARNINGS} == *'system disk'* ]]
storage_action_plan_partition /dev/system 2048MiB all
[[ ${STORAGE_ACTION_END} == 16775167s ]]
(( ${STORAGE_ACTION_START%s} >= free_first && ${STORAGE_ACTION_END%s} <= free_last ))

# Trailing free space uses the same alignment calculation.
interior=false
trailing=true
storage_action_plan_partition /dev/disk 2048MiB all
(( ${STORAGE_ACTION_START%s} >= free_first && ${STORAGE_ACTION_END%s} <= free_last ))
fake_optimal=4194304
fake_offset=512
storage_action_plan_partition /dev/disk 2048MiB all
(( (${STORAGE_ACTION_START%s} * fake_logical + fake_offset) % fake_optimal == 0 ))
(( ((${STORAGE_ACTION_END%s} + 1) * fake_logical + fake_offset) % fake_optimal == 0 ))
(( ${STORAGE_ACTION_START%s} >= free_first && ${STORAGE_ACTION_END%s} <= free_last ))

# Use the unchanged exact system-partition guard, rather than a disk-wide ban.
source <(sed -n '/^storage_action_is_system_partition() {/,/^}/p; /^storage_action_validate_target() {/,/^}/p' "${common}")
storage_system_partitions() { printf '/dev/system1\n/dev/system2\n'; }
storage_validate_partition() { return 0; }
storage_action_parent_disk() { printf '/dev/system\n'; }
lsblk() {
    if [[ $* == *'-dnro TYPE'* ]]; then printf 'part\n'; else printf '%s part\n' "${*: -1}"; fi
}
storage_action_host_mountpoints() { return 0; }
storage_action_filesystem() { printf 'xfs\n'; }
storage_mount_is_critical() { return 1; }
if storage_action_validate_target /dev/system1; then
    echo 'ERROR: exact system partition protection was lost.' >&2; exit 1
fi
storage_action_validate_target /dev/system4

if grep -Eq 'resizepart|smb|nfs|credentials' "${planner}" "${apply}" "${common}"; then
    echo 'ERROR: alignment correction expanded into resizing or network storage.' >&2; exit 1
fi
printf 'Local partition alignment contracts passed.\n'
