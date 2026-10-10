#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
expected_stderr="${fixture}/expected.stderr"
cleanup() {
    local status="$1"
    if (( status != 0 )); then
        echo "Setup runtime repair regression checks failed (exit ${status})." >&2
        if [[ -s ${expected_stderr} ]]; then
            echo 'Captured stderr from expected failure paths:' >&2
            cat "${expected_stderr}" >&2
        fi
    fi
    rm -rf -- "${fixture}"
    exit "${status}"
}
trap 'cleanup "$?"' EXIT
helper="${repo_root}/mjust/libexec/admin-setup-runtime-transaction-json"
source "${repo_root}/mjust/libexec/minecraft-reset-common.sh"
# Load the production functions only, without executing appliance entrypoints.
source <(sed -n '/^_a55_evidence_set() {/,/^action=/{ /^action=/d; p; }' "${helper}" |
    sed -e 's|/usr/libexec/justvoxel/mjust/verify-minecraft-stack|mock_verify_stack|g' \
        -e 's|/usr/libexec/justvoxel/mjust/validate-backend|mock_validate_backend|g')
A55_EVIDENCE='{}'
A53_TX_DIR="${fixture}"
A53_MANIFEST="${fixture}/manifest.json"
A55_SETUP_MARKER="${fixture}/setup-in-progress"
JV_QUADLET="${fixture}/minecraft.container"
JV_MC_ENV="${fixture}/minecraft.env"
JV_BACKUP_ENV="${fixture}/backup.env"
JV_BACKUP_SERVICE="${fixture}/backup.service"
JV_BACKUP_TIMER="${fixture}/backup.timer"
JV_CONFIG="${fixture}/config"
JAVA_PORT=25565 BEDROCK_PORT=19132 BEDROCK_ENABLED=no
JV_MAINTENANCE_LOCK="${fixture}/minecraft-maintenance.lock"
_a55_load_values() { :; }
firewall-cmd() { [[ $* != *--query-port* ]]; }
systemctl() {
    case "$1" in
        show) printf '%s\n' "$((restart_base + restart_increase))" ;;
        is-active) [[ ${ready:-no} == yes ]] ;;
        stop) [[ ${stop_fails:-no} == no ]] ;;
        *) return 0 ;;
    esac
}
podman() {
    case "$1" in
        pull) return 0 ;;
        exec) [[ ${ready:-no} == yes ]] ;;
        rm) [[ ${remove_fails:-no} == no ]] ;;
        container) return 2 ;;
        *) return 1 ;;
    esac
}
sleep() {
    ((ticks += 1))
    case "${startup}" in
        slow) (( ticks < 100 )) || ready=yes ;;
        transient) (( ticks > 2 )) || restart_increase=$ticks; (( ticks < 100 )) || ready=yes ;;
        crash) restart_increase=$ticks ;;
        reset) restart_base=0; restart_increase=$ticks ;;
    esac
    return 0
}
for startup in slow transient; do
    ready=no ticks=0 restart_base=40 restart_increase=0
    _a55_wait_for_rcon 900 40
    [[ $ticks == 100 ]] # 500 seconds, including up to two harmless restarts.
done
for startup in crash reset; do
    ready=no ticks=0 restart_base=40 restart_increase=0 A55_EVIDENCE='{}'
    if _a55_wait_for_rcon 900 40 2>> "${expected_stderr}"; then echo 'Crash loop accepted' >&2; exit 1; fi
    [[ $ticks == 3 ]]
    jq -e '.rcon_result == "crash_loop"' <<< "${A55_EVIDENCE}" >/dev/null
done
startup=never ready=no ticks=0 restart_base=40 restart_increase=0 A55_EVIDENCE='{}'
if _a55_wait_for_rcon 900 40 2>> "${expected_stderr}"; then echo 'RCON timeout accepted' >&2; exit 1; fi
[[ $ticks == 180 ]]
jq -e '.rcon_result == "timeout"' <<< "${A55_EVIDENCE}" >/dev/null

# Use the real Reset Minecraft classifier/deleter with mocked storage discovery.
findmnt() {
    case "$*" in
        '-n -o SOURCE,FSTYPE,TARGET --target '*) printf '/dev/fixture %s /fixture-mount\n' "${mock_fstype}" ;;
        '-n -o SOURCE --target '*) printf '/dev/fixture\n' ;;
        '-n -o FSTYPE --target '*) printf '%s\n' "${mock_fstype}" ;;
        '-rn -o TARGET') [[ ${nested:-no} != yes ]] || printf '%s/nested\n' "${DATA_PATH}" ;;
        *) return 1 ;;
    esac
    return 0
}
lsblk() { printf '%s\n' "${mock_transport}"; }
new_case() {
    DATA_PATH="${fixture}/$1/data" BACKUP_PATH="${fixture}/$1/backups"
    mkdir -p "${DATA_PATH}" "${BACKUP_PATH}"
    printf 'saved backup\n' > "${BACKUP_PATH}/saved.zip"
    printf '{}\n' > "${A53_MANIFEST}"
    mock_fstype=xfs mock_transport=sata nested=no stop_fails=no remove_fails=no ready=no
}
generate_paper() {
    mkdir -p "${DATA_PATH}/world/datapacks" "${DATA_PATH}/plugins"
    printf paper > "${DATA_PATH}/world/datapacks/paper.zip"
    printf paper > "${DATA_PATH}/paper-global.yml"
}
rollback() {
    _a55_rollback_action > "${fixture}/rollback.json"
    jq -e '.ok == true and .phase == "runtime_rolled_back"' "${fixture}/rollback.json" >/dev/null
    [[ -f ${BACKUP_PATH}/saved.zip ]]
}
new_case fresh
_a55_record_initial_state
generate_paper
# Runtime-created symlinks are removed without touching their targets.
ln -s "${BACKUP_PATH}" "${DATA_PATH}/backup-link"
rollback
[[ -z $(find "${DATA_PATH}" -mindepth 1 -print -quit) ]]
# This is the empty data area a subsequent Vanilla attempt will receive.
_a55_record_initial_state
jq -e '.runtime.empty_data_identity != null' "${A53_MANIFEST}" >/dev/null

new_case preexisting
generate_paper
_a55_record_initial_state
printf new > "${DATA_PATH}/new-file"
rollback
[[ -f ${DATA_PATH}/world/datapacks/paper.zip && -f ${DATA_PATH}/new-file ]]

for policy in external network unknown nested backup-inside symlink parent-symlink; do
    new_case "${policy}"
    case "${policy}" in
        external) mock_transport=usb ;;
        network) mock_fstype=nfs4 ;;
        unknown) mock_transport='' ;;
        nested) nested=yes; mkdir "${DATA_PATH}/nested" ;;
        backup-inside) BACKUP_PATH="${DATA_PATH}/backups"; mkdir "${BACKUP_PATH}"; touch "${BACKUP_PATH}/saved.zip" ;;
        symlink) mv "${DATA_PATH}" "${DATA_PATH}-real"; ln -s "${DATA_PATH}-real" "${DATA_PATH}" ;;
        parent-symlink) ln -s "${fixture}/${policy}" "${fixture}/alias"; DATA_PATH="${fixture}/alias/data" ;;
    esac
    _a55_record_initial_state
    jq -e '.runtime.empty_data_identity == null' "${A53_MANIFEST}" >/dev/null
    generate_paper
    rollback
    [[ -f ${DATA_PATH}/paper-global.yml ]]
done
for unsafe in / /var /var/lib /var/lib/justvoxel; do
    DATA_PATH="${unsafe}"
    if _a55_data_identity 2>> "${expected_stderr}"; then echo "Unsafe path accepted: ${unsafe}" >&2; exit 1; fi
done

# Changes after recording must fail closed and preserve both data and backups.
for change in nested backup-inside backup-alias symlink replaced network container stop; do
    new_case "changed-${change}"
    _a55_record_initial_state
    generate_paper
    case "${change}" in
        nested) nested=yes ;;
        backup-inside) BACKUP_PATH="${DATA_PATH}/backups"; mkdir "${BACKUP_PATH}"; touch "${BACKUP_PATH}/saved.zip" ;;
        backup-alias) mkdir "${DATA_PATH}/backups"; touch "${DATA_PATH}/backups/saved.zip"; ln -s "${DATA_PATH}/backups" "${fixture}/backup-alias"; BACKUP_PATH="${fixture}/backup-alias" ;;
        symlink) mv "${DATA_PATH}" "${DATA_PATH}-real"; ln -s "${DATA_PATH}-real" "${DATA_PATH}" ;;
        replaced) mv "${DATA_PATH}" "${DATA_PATH}-old"; mkdir "${DATA_PATH}"; generate_paper ;;
        network) mock_fstype=cifs ;;
        container) remove_fails=yes ;;
        stop) stop_fails=yes ;;
    esac
    _a55_rollback_action > "${fixture}/rollback.json" 2>> "${expected_stderr}"
    jq -e '.ok == false and .phase == "runtime_rollback"' "${fixture}/rollback.json" >/dev/null
    [[ -f ${DATA_PATH}/paper-global.yml && -f ${BACKUP_PATH}/saved.zip ]]
done
new_case missing-evidence
generate_paper
rollback
[[ -f ${DATA_PATH}/paper-global.yml ]]

# Rollback must preserve data while a backup owns the maintenance lock.
new_case rollback-lock-busy
_a55_record_initial_state
generate_paper
exec 9>&-
exec 8>"${JV_MAINTENANCE_LOCK}"
flock -n 8
_a55_rollback_action > "${fixture}/rollback-busy.json"
jq -e '.ok == false and .phase == "runtime_rollback"' "${fixture}/rollback-busy.json" >/dev/null
[[ -f ${DATA_PATH}/paper-global.yml ]]
flock -u 8
exec 8>&-
rollback

# Exercise the real verification failure followed by the existing rollback action.
new_case verification-crash
_a55_record_initial_state
generate_paper
startup=crash ready=no ticks=0 restart_base=50 restart_increase=0 A55_EVIDENCE='{}'
MINECRAFT_VERSION=26.2
minecraft_image_ref() { printf 'fixture:testing\n'; }
mock_verify_stack() { return 0; }
mock_validate_backend() { return 0; }
activate_backup_timer() { touch "${fixture}/timer-activated"; }
if _a55_verify_action 2>> "${expected_stderr}"; then echo 'Crash loop verification succeeded' >&2; exit 1; fi
[[ $ticks == 3 && ! -e ${fixture}/timer-activated ]]
jq -e '.rcon_result == "crash_loop"' <<< "${A55_EVIDENCE}" >/dev/null
jq -e '.runtime.minecraft_started == true' "${A53_MANIFEST}" >/dev/null
rollback
[[ -z $(find "${DATA_PATH}" -mindepth 1 -print -quit) ]]

# Healthy startup followed by a required-unit validation failure still takes
# the real guarded rollback path and preserves existing backups.
new_case verification-required-mount
_a55_record_initial_state
generate_paper
ready=yes restart_base=0 restart_increase=0 A55_EVIDENCE='{}'
mock_validate_backend() { [[ $1 == --first-run ]] || return 1; echo 'FAIL: selected setup storage mount is failed'; return 1; }
if _a55_verify_action 2>> "${expected_stderr}"; then echo 'Required mount failure accepted' >&2; exit 1; fi
jq -e '.final_validation == "failed" and (.final_validation_output | contains("selected setup storage mount"))' <<< "${A55_EVIDENCE}" >/dev/null
ready=no
rollback
[[ -z $(find "${DATA_PATH}" -mindepth 1 -print -quit) ]]

# Commit activates backups after verification with the setup marker in place.
# Failed activation must preserve the marker so rollback remains possible.
new_case commit-backup
touch "${JV_CONFIG}" "${JV_QUADLET}" "${A55_SETUP_MARKER}"
ready=yes
activate_backup_timer() { [[ -f ${A55_SETUP_MARKER} ]] && touch "${fixture}/timer-activated"; }
_a55_commit_action > "${fixture}/commit.json"
jq -e '.ok == true and .phase == "runtime_committed"' "${fixture}/commit.json" >/dev/null
[[ -f ${fixture}/timer-activated && ! -e ${A55_SETUP_MARKER} ]]

new_case commit-timer-failure
touch "${JV_CONFIG}" "${JV_QUADLET}" "${A55_SETUP_MARKER}"
ready=yes
activate_backup_timer() { return 1; }
_a55_commit_action > "${fixture}/commit-failure.json"
jq -e '.ok == false and .phase == "runtime_commit"' "${fixture}/commit-failure.json" >/dev/null
[[ -e ${A55_SETUP_MARKER} ]]

# Cleanup must keep using the shared reset implementation.
grep -Fq 'source /usr/libexec/justvoxel/mjust/minecraft-reset-common.sh' "${helper}"
grep -Fq 'jv_reset_delete_internal_data "${DATA_PATH}" "${BACKUP_PATH}"' "${helper}"
if grep -E 'find .*-(delete|exec)|rm -rf' "${helper}"; then echo 'Duplicated data deletion' >&2; exit 1; fi
echo 'Setup runtime repair regression checks passed.'
