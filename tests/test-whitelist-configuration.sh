#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
# Execute the actual helper with fixture configuration and mocked appliance IO.
cat > "${fixture_dir}/common.sh" <<'STUB'
require_root() { :; }
require_config() { source "${JV_CONFIG}"; }
validate_memory() { [[ $1 =~ ^[0-9]+G$ ]]; }
memory_to_mib() { printf '%s' "$(( ${1%G} * 1024 ))"; }
validate_port() { [[ $1 == 25565 || $1 == 19132 ]]; }
validate_positive_int() { [[ $1 =~ ^[1-9][0-9]*$ ]]; }
validate_simple_text() { [[ -n $1 && $1 != *$'\n'* ]]; }
validate_game_mode() { [[ $1 == survival || $1 == creative ]]; }
write_main_config() {
    printf 'WHITELIST_ENABLED=%q\nENFORCE_WHITELIST=%q\nMOTD=%q\n' "$WHITELIST_ENABLED" "$ENFORCE_WHITELIST" "$MOTD" > "${JV_CONFIG}"
}
render_runtime() { printf '%s\n' "$WHITELIST_ENABLED" > "${JV_CONFIG_DIR}/runtime"; }
update_firewall_ports() { :; }
systemd-analyze() { :; }
systemctl() {
    printf '%s\n' "$*" >> "${JV_CONFIG_DIR}/systemctl"
    if [[ $1 == is-active ]]; then [[ ${RUNNING:-no} == yes ]]; elif [[ $1 == stop ]]; then RUNNING=no; else return 99; fi
}
timeout() {
    [[ $1 == 10 || $1 == 15 ]] || return 99
    shift
    "$@"
}
podman() {
    [[ $# == 4 && $1 == exec && $2 == minecraft && $3 == rcon-cli ]] || return 99
    printf '%s\n' "$4" >> "${JV_CONFIG_DIR}/rcon"
    if [[ ${FAIL_LIVE:-no} == all || ( ${FAIL_LIVE:-no} == yes && $4 == "whitelist off" ) ]]; then return 1; fi
    case "$4" in
        'whitelist on') printf 'Whitelist is now turned on\n' ;;
        'whitelist off') printf 'Whitelist is now turned off\n' ;;
        *) return 99 ;;
    esac
}
STUB
sed -e "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    -e '\|^source /usr/libexec/justvoxel/mjust/storage-common-base.sh$|d' \
    -e '\|^source /usr/libexec/justvoxel/mjust/interrupt-safety.sh$|d' \
    "${repo_root}/mjust/libexec/admin-discovery-json" > "${fixture_dir}/helper"
export JV_CONFIG_DIR="${fixture_dir}" JV_CONFIG="${fixture_dir}/config" JV_SETUP_IN_PROGRESS="${fixture_dir}/not-present"
export DATA_PATH=/fixture DATA_MOUNT_POINT='' DATA_EXPECTED_UUID='' DATA_EXPECTED_SOURCE=''
export JAVA_MEMORY=1G CONTAINER_MEMORY=2G JAVA_PORT=25565 BEDROCK_ENABLED=no BEDROCK_PORT=19132 TIMEZONE=UTC
export MAX_PLAYERS=10 MOTD=JustVoxel MINECRAFT_IMAGE_TAG=stable MINECRAFT_VERSION_MODE=pinned MINECRAFT_VERSION=1.21.8
export GAME_MODE=survival DIFFICULTY=normal BACKUP_TYPE=system BACKUP_PATH=/fixture/backup BACKUP_MOUNT_POINT=''
export BACKUP_EXPECTED_UUID='' BACKUP_EXPECTED_SOURCE='' BACKUP_KEEP=7 BACKUP_SCHEDULE='*-*-* 04:30:00' BACKUP_TIMER_ENABLED=yes
source "${repo_root}/mjust/libexec/configuration-api.sh"
reset_fixture() {
    printf 'WHITELIST_ENABLED=%s\nENFORCE_WHITELIST=yes\nMOTD=JustVoxel\n' "$1" > "${JV_CONFIG}"
    : > "${fixture_dir}/rcon"
    : > "${fixture_dir}/systemctl"
}
reset_fixture yes
current="$(bash "${fixture_dir}/helper" configuration)"
payload="$(jv_config_payload <<< "$current")"
jq -e '.whitelist_enabled == true' <<< "$payload" >/dev/null
# Old callers that omit the field preserve the existing setting.
legacy="$(jq 'del(.whitelist_enabled) | .motd="New"' <<< "$payload")"
result="$(bash "${fixture_dir}/helper" apply <<< "$legacy")"
jq -e '.ok and .applied and .proposed.minecraft.whitelist_enabled and .proposed.minecraft.enforce_whitelist' <<< "$result" >/dev/null
reset_fixture yes
for enabled in false true; do
    [[ $enabled == true ]] && previous=no || previous=yes
    reset_fixture "$previous"
    request="$(jq --argjson enabled "$enabled" '.whitelist_enabled=$enabled' <<< "$payload")"
    plan="$(bash "${fixture_dir}/helper" plan <<< "$request")"
    jq -e --argjson enabled "$enabled" '.ok and (.restart_required|not) and (.proposed.minecraft.whitelist_enabled == $enabled) and .changes[0].field == "whitelist_enabled"' <<< "$plan" >/dev/null
    grep -q "WHITELIST_ENABLED=$previous" "$JV_CONFIG"
    result="$(RUNNING=yes bash "${fixture_dir}/helper" apply <<< "$request")"
    jq -e --argjson enabled "$enabled" '.ok and .applied and (.restarted|not) and (.proposed.minecraft.whitelist_enabled == $enabled) and .proposed.minecraft.enforce_whitelist' <<< "$result" >/dev/null
    [[ $enabled == true ]] && expected='whitelist on' || expected='whitelist off'
    [[ $(cat "${fixture_dir}/rcon") == "$expected" ]]
    ! grep -Eq 'start|restart|stop' "${fixture_dir}/systemctl"
done
reset_fixture yes
request="$(jq '.whitelist_enabled=false' <<< "$payload")"
result="$(bash "${fixture_dir}/helper" apply <<< "$request")"
jq -e '.ok and .applied and (.proposed.minecraft.whitelist_enabled|not) and (.restarted|not)' <<< "$result" >/dev/null
[[ ! -s ${fixture_dir}/rcon ]]
[[ $(cat "${fixture_dir}/runtime") == no ]]
! grep -Eq 'start|restart|stop' "${fixture_dir}/systemctl"
reset_fixture yes
result="$(RUNNING=yes FAIL_LIVE=yes bash "${fixture_dir}/helper" apply <<< "$request")"
jq -e '(.ok|not) and (.error|contains("Previous settings were restored"))' <<< "$result" >/dev/null
grep -q WHITELIST_ENABLED=yes "$JV_CONFIG"
grep -q ENFORCE_WHITELIST=yes "$JV_CONFIG"
[[ $(cat "${fixture_dir}/runtime") == yes ]]
[[ $(cat "${fixture_dir}/rcon") == $'whitelist off\nwhitelist on' ]]
! grep -Eq 'start|restart' "${fixture_dir}/systemctl"
reset_fixture yes
result="$(RUNNING=yes FAIL_LIVE=all bash "${fixture_dir}/helper" apply <<< "$request")"
jq -e '(.ok|not) and (.error|contains("Previous settings were restored"))' <<< "$result" >/dev/null
grep -q WHITELIST_ENABLED=yes "$JV_CONFIG"
grep -q 'stop minecraft.service' "${fixture_dir}/systemctl"
! grep -Eq 'start|restart' "${fixture_dir}/systemctl"
invalid="$(jq '.whitelist_enabled="false"' <<< "$payload")"
result="$(bash "${fixture_dir}/helper" apply <<< "$invalid")"
jq -e '(.ok|not) and (.error|contains("boolean"))' <<< "$result" >/dev/null
printf 'PASS: whitelist configuration preservation, plan/apply, fixed live commands, stopped server, rollback\n'
