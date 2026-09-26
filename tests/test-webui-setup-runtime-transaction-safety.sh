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
grep -Fq '/usr/libexec/justvoxel/mjust/validate-backend' "${runtime_helper}"
if grep -Fq '/usr/libexec/justvoxel/mjust/validate ' "${runtime_helper}"; then
    echo 'A5.5 runtime transaction must not recurse through the mJust validation API frontend.' >&2
    exit 1
fi
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

# Exercise the runtime SELinux step with a newly mounted, unlabeled XFS root.
# Mocks keep the contract check independent of host SELinux state.
eval "$(sed -n '/^_a55_apply_selinux() {/,/^}/p' "${runtime_helper}")"
DATA_MOUNT_POINT=/var/mnt/justvoxel-data
DATA_PATH=/var/mnt/justvoxel-data/minecraft
a55_mount_labeled=false
a55_data_labeled=false
a55_calls=()
path_regex_escape() { printf '%s' "$1"; }
semanage() { a55_calls+=("rule:$*"); }
_a55_manifest_update() { a55_calls+=("manifest:$*"); }
restorecon() {
    a55_calls+=("restore:$*")
    case "$*" in
        '-F /var/mnt/justvoxel-data') a55_mount_labeled=true ;;
        '-RF /var/mnt/justvoxel-data/minecraft')
            [[ ${a55_mount_labeled} == true ]] || return 1
            a55_data_labeled=true ;;
        *) return 1 ;;
    esac
}
ls() {
    [[ $1 == -Zd ]] || return 1
    if [[ $2 == "${DATA_MOUNT_POINT}" ]]; then
        if [[ ${a55_mount_labeled} == true ]]; then
            printf 'system_u:object_r:mnt_t:s0 %s\n' "$2"
        else
            printf 'system_u:object_r:unlabeled_t:s0 %s\n' "$2"
        fi
    elif [[ $2 == "${DATA_PATH}" ]]; then
        [[ ${a55_data_labeled} == true ]] || return 1
        printf 'system_u:object_r:container_file_t:s0 %s\n' "$2"
    else
        return 1
    fi
}
_a55_apply_selinux
[[ ${a55_mount_labeled} == true && ${a55_data_labeled} == true ]] || { echo 'fresh XFS mount root or Minecraft directory was not labeled' >&2; exit 1; }
[[ ${a55_calls[0]-} == 'rule:fcontext -a -t container_file_t /var/mnt/justvoxel-data/minecraft(/.*)?' ]] || { echo 'SELinux fcontext rule was not added for Minecraft data' >&2; exit 1; }
[[ ${a55_calls[1]-} == 'manifest:.runtime.selinux_rule_added = true' ]] || { echo 'SELinux rule addition was not recorded in the manifest' >&2; exit 1; }
[[ ${a55_calls[2]-} == 'restore:-F /var/mnt/justvoxel-data' ]] || { echo 'SELinux mount root was not relabeled first' >&2; exit 1; }
[[ ${a55_calls[3]-} == 'restore:-RF /var/mnt/justvoxel-data/minecraft' ]] || { echo 'SELinux Minecraft data directory was not relabeled after the mount root' >&2; exit 1; }
[[ ${#a55_calls[@]} == 4 ]] || { echo 'SELinux relabel touched the shared backup directory or another path' >&2; exit 1; }

# System storage has no external mount root to relabel.
DATA_MOUNT_POINT=''
DATA_PATH=/var/lib/justvoxel/minecraft
a55_calls=()
restorecon() {
    a55_calls+=("restore:$*")
    [[ $* == '-RF /var/lib/justvoxel/minecraft' ]] || return 1
    a55_data_labeled=true
}
_a55_apply_selinux
[[ ${a55_calls[2]-} == 'restore:-RF /var/lib/justvoxel/minecraft' && ${#a55_calls[@]} == 3 ]] || { echo 'SELinux system storage relabel call sequence was incorrect' >&2; exit 1; }
grep -Fq 'if [[ ${selinux_added} == true ]]; then' "${runtime_helper}"
grep -Fq 'semanage fcontext -d "${escaped}(/.*)?"' "${runtime_helper}"

# Normal mjust setup is the single supported first-run setup path and uses the Management API.
grep -Fq 'setup-api.sh' "${repo_root}/mjust/libexec/setup"
if grep -Fq 'render_runtime' "${repo_root}/mjust/libexec/setup"; then
    echo 'normal mJust setup must not render runtime files directly' >&2
    exit 1
fi
if grep -Fq 'setup-legacy' "${repo_root}/mjust/libexec/setup" || grep -Fq -- '--advanced' "${repo_root}/mjust/libexec/setup"; then
    echo 'normal mJust setup must not expose the legacy Advanced Setup path' >&2
    exit 1
fi

echo 'WebUI first-run A5.5 runtime transaction safety checks passed.'
