#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_helper="${repo_root}/mjust/libexec/admin-setup-runtime-transaction-json"
common="${repo_root}/mjust/libexec/common.sh"
discovery="${repo_root}/mjust/libexec/admin-discovery-json"
apply="${repo_root}/management/cmd/justvoxel-management-agent/admin_setup_apply.go"
worker="${repo_root}/management/cmd/justvoxel-management-agent/setup_worker.go"
backup="${repo_root}/runtime/minecraft-backup"
validator="${repo_root}/mjust/libexec/validate-backend"
runner="${repo_root}/management/cmd/justvoxel-management-agent/setup_runtime_transaction.go"
planner="${repo_root}/mjust/libexec/admin-setup-plan-json"

for file in "${runtime_helper}" "${common}" "${discovery}" "${apply}" "${worker}" "${runner}" "${planner}"; do
    [[ -f ${file} ]] || { echo "missing A5.5 file: ${file}" >&2; exit 1; }
done
bash -n "${runtime_helper}"
bash -n "${common}"
bash -n "${backup}"
bash -n "${validator}"
bash -n "${discovery}"
bash -n "${planner}"

# A stopping container may answer RCON while its systemd unit is deactivating.
source "${common}"
mock_service_state=active
mock_rcon_state=ready
systemctl() {
    [[ $# -eq 3 && $1 == is-active && $2 == --quiet && $3 == minecraft.service && ${mock_service_state} == active ]]
}
podman() {
    [[ $# -eq 4 && $1 == exec && $2 == minecraft && $3 == rcon-cli && $4 == list && ${mock_rcon_state} == ready ]]
}
sleep() { :; }
wait_for_rcon 1 || { echo 'active service with working RCON was not ready' >&2; exit 1; }
mock_service_state=deactivating
if wait_for_rcon 1; then
    echo 'deactivating service with working RCON was incorrectly ready' >&2
    exit 1
fi

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
grep -Fq '_a55_wait_for_rcon 900' "${runtime_helper}"
grep -Fq '/usr/libexec/justvoxel/mjust/validate-backend' "${runtime_helper}"
if grep -Fq '/usr/libexec/justvoxel/mjust/validate ' "${runtime_helper}"; then
    echo 'A5.5 runtime transaction must not recurse through the mJust validation API frontend.' >&2
    exit 1
fi
grep -Fq 'runtime_rollback' "${runner}"
grep -Fq 'rollbackSetupStorage' "${runner}"
grep -Fq 'MinecraftUID' "${runner}"
grep -Fq 'minecraft_uid' "${planner}"
grep -Fq 'GAME_MODE="$(jq -r '\''.minecraft.game_mode'\'' <<< "${A55_REQUEST}")"' "${runtime_helper}"
grep -Fq 'validate_game_mode "${GAME_MODE}"' "${runtime_helper}"
grep -Fq 'shell_quote_assignment GAME_MODE "${GAME_MODE:-survival}"' "${common}"
grep -Fq 'MODE=@@GAME_MODE@@' "${repo_root}/templates/config/minecraft.env.in"

if grep -Eq 'smb_password|SMBPassword|password[[:space:]]*:' "${runtime_helper}" "${runner}"; then
    echo 'A5.5 runtime transaction must not contain the SMB execution secret.' >&2
    exit 1
fi

if grep -Eq 'exec\.Command(Context)?\([^,]+,[[:space:]]*request|/bin/(sh|bash)[[:space:]]+-c' "${runner}" "${worker}"; then
    echo 'A5.5 introduced an arbitrary command execution surface.' >&2
    exit 1
fi

# Podman relabels only Minecraft DATA_PATH when the Quadlet starts, for both
# system storage and an external partition shared with host-managed backups.
grep -Fq 'Volume=@@DATA_PATH@@:/data:Z' "${repo_root}/templates/quadlets/minecraft.container.in"
grep -Fq 'DATA_PATH="$(jq -r '\''.storage.path'\'' <<< "${A55_REQUEST}")"' "${runtime_helper}"
grep -Fq 'BACKUP_PATH="$(jq -r '\''.backups.path'\'' <<< "${A55_REQUEST}")"' "${runtime_helper}"
grep -Fq 'if [[ -n ${DATA_MOUNT_POINT} ]]; then mountpoint -q -- "${DATA_MOUNT_POINT}" || return 1; fi' "${runtime_helper}"
grep -Fq 'if [[ -n ${BACKUP_MOUNT_POINT} ]]; then mountpoint -q -- "${BACKUP_MOUNT_POINT}" || return 1; fi' "${runtime_helper}"
if grep -Eq '_a55_apply_selinux|selinux_rule_added|selinux_data_label|semanage fcontext|restorecon|container_file_t' "${runtime_helper}"; then
    echo 'first-run runtime transaction still manually manages SELinux labeling' >&2
    exit 1
fi
grep -Fq 'output="$(systemctl start minecraft.service 2>&1)"' "${runtime_helper}"
grep -Fq 'validation_output="$(/usr/libexec/justvoxel/mjust/validate-backend --first-run 2>&1)"' "${runtime_helper}"
render_line="$(grep -nF '    render_runtime_files || return 1' "${runtime_helper}" | cut -d: -f1)"
start_line="$(grep -nF '    output="$(systemctl start minecraft.service 2>&1)"' "${runtime_helper}" | cut -d: -f1)"
validation_line="$(grep -nF '    validation_output="$(/usr/libexec/justvoxel/mjust/validate-backend --first-run 2>&1)"' "${runtime_helper}" | cut -d: -f1)"
[[ -n ${render_line} && -n ${start_line} && -n ${validation_line} && ${render_line} -lt ${start_line} && ${start_line} -lt ${validation_line} ]] || { echo 'runtime render, service start, and final validation are out of order' >&2; exit 1; }

# The backup timer must not interrupt first-run readiness checks.
verify_body="$(sed -n '/^_a55_verify_action() {/,/^}/p' "${runtime_helper}")"
commit_body="$(sed -n '/^_a55_commit_action() {/,/^}/p' "${runtime_helper}")"
if grep -Fq 'activate_backup_timer' <<< "${verify_body}"; then
    echo 'backup timer can interrupt first-run runtime verification' >&2
    exit 1
fi
grep -Fq 'activate_backup_timer' <<< "${commit_body}"
grep -Fq 'flock -n 9' <<< "${commit_body}"
grep -Fq 'flock -n 9' "${runtime_helper}"
grep -Fq 'Skipping backup: first-run setup or rollback is in progress.' "${backup}"
grep -Fq 'elif [[ -f ${JV_SETUP_IN_PROGRESS} ]]; then' "${validator}"
grep -Fq "fail 'Minecraft backup timer should be enabled but is not'" "${validator}"
marker_line="$(grep -nF 'if [[ -e ${setup_marker} ]]; then' "${backup}" | head -n1 | cut -d: -f1)"
config_line="$(grep -nF 'jv_backup_load_config "${config_file}"' "${backup}" | cut -d: -f1)"
[[ -n ${marker_line} && -n ${config_line} && ${marker_line} -lt ${config_line} ]] || {
    echo 'backup setup guard must precede config access' >&2
    exit 1
}

# Exercise the actual first-start function with all appliance operations mocked.
# Replace only the absolute final validator command, never invoke host services.
(
    A55_EVIDENCE='{}'
    runtime_log="$(mktemp)"
    trap 'rm -f -- "${runtime_log}"' EXIT
    source <(sed -n '/^_a55_verify_action() {/,/^}/p' "${runtime_helper}" | sed -e 's|/usr/libexec/justvoxel/mjust/validate-backend|mock_validate_backend|g' -e 's|/usr/libexec/justvoxel/mjust/verify-minecraft-stack|mock_verify_stack|g')
    _a55_load_values() { MINECRAFT_IMAGE_TAG=testing; BEDROCK_ENABLED=no; }
    _a55_evidence_set() { printf 'evidence %s %s\n' "$1" "$2" >> "${runtime_log}"; }
    _a55_evidence_set_bounded() { :; }
    _a55_manifest_update() { :; }
    _a55_json() { printf 'verified %s\n' "$3" >> "${runtime_log}"; }
    _a55_wait_for_rcon() { return 0; }
    activate_backup_timer() { echo 'timer activated during verify' >&2; return 1; }
    mock_validate_backend() { return 0; }
    mock_verify_stack() { return 0; }
    MINECRAFT_VERSION=26.2
    systemctl() { printf 'systemctl %s\n' "$*" >> "${runtime_log}"; if [[ $1 == show ]]; then printf '0\n'; fi; }
    podman() {
        printf 'podman %s\n' "$*" >> "${runtime_log}"
        if [[ $1 == pull ]]; then return "${pull_rc}"; fi
        [[ $1 == exec && $2 == minecraft && $3 == rcon-cli && $4 == version ]]
    }

    pull_rc=1
    if _a55_verify_action; then echo 'failed setup image pull returned success' >&2; exit 1; fi
    grep -Fxq "podman pull ${JV_MINECRAFT_IMAGE_REPO}:testing" "${runtime_log}"
    grep -Fxq 'evidence minecraft_image_pull failed' "${runtime_log}"
    if grep -q '^systemctl\|^verified' "${runtime_log}"; then echo 'failed pull continued setup' >&2; exit 1; fi

    : > "${runtime_log}"
    pull_rc=0
    _a55_verify_action || { echo 'successful image pull did not continue setup' >&2; exit 1; }
    pull_line="$(grep -n '^podman pull ' "${runtime_log}" | cut -d: -f1)"
    start_line="$(grep -n '^systemctl start minecraft.service$' "${runtime_log}" | cut -d: -f1)"
    [[ -n ${pull_line} && -n ${start_line} && ${pull_line} -lt ${start_line} ]]
    grep -Fxq 'verified runtime_verified' "${runtime_log}"
)
if grep -Eiq '^[[:space:]]*Pull[[:space:]]*=[[:space:]]*always' "${repo_root}/templates/quadlets/minecraft.container.in"; then
    echo 'normal Minecraft startup must not force an image pull' >&2
    exit 1
fi
grep -Fq 'if grep -q '\''container_file_t'\'' <<< "${data_context}"; then' "${repo_root}/mjust/libexec/validate-backend"
grep -Fq 'fail "Minecraft data is not labeled container_file_t:' "${repo_root}/mjust/libexec/validate-backend"

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
