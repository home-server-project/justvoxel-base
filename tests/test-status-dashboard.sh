#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/status-lib.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "missing $2 in $1"; }
absent() { if grep -Fq -- "$2" "$1"; then fail "unexpected $2 in $1"; fi; }

[[ $(jv_status_format_duration 90) == '1m' ]] || fail 'minute duration'
[[ $(jv_status_format_duration 3660) == '1h 1m' ]] || fail 'hour duration'
[[ $(jv_status_format_duration 90061) == '1d 1h 1m' ]] || fail 'day duration'
[[ $(jv_status_overall 0 running healthy healthy) == Healthy ]] || fail 'host health'
[[ $(jv_status_overall 2 running healthy healthy) == 'Attention needed' ]] || fail 'failed services'
[[ $(jv_status_overall 0 degraded healthy healthy) == 'Attention needed' ]] || fail 'system state'
[[ $(jv_status_overall 0 running problem healthy) == 'Attention needed' ]] || fail 'network health'
[[ $(jv_status_overall 0 running healthy problem) == 'Attention needed' ]] || fail 'system storage health'
[[ $(jv_status_overall 0 unknown healthy healthy) == Unknown ]] || fail 'unknown host'

status="${repo_root}/mjust/libexec/status"
collector="${repo_root}/mjust/libexec/web-status-json"
for forbidden in minecraft.service minecraft-backup.timer podman JV_CONFIG minecraft.env require_config DATA_PATH BACKUP_PATH JAVA_PORT BEDROCK_PORT MINECRAFT_VERSION minecraft_systemd details_podman interrupt-safety web_players web_action emit_confirmation trim_player_names 'players)' 'start|stop|restart' 'minecraft:{' 'backup:{'; do
    absent "${collector}" "${forbidden}"
done
contains "${collector}" 'Usage: web-status-json [status [--details]]'
for preserved in jv_variant_name physical_ram_kib hostname uptime_seconds failed_services 'df -Pk -- /var' 'findmnt --target /var' 'remote-access-status tailscale' 'remote-access-status netbird' 'ip route' 'resolvectl status' 'justvoxel:{version:' 'network:{interface:' 'storage:{system:'; do
    contains "${collector}" "${preserved}"
done
contains "${repo_root}/management/cmd/justvoxel-management-agent/main.go" 'mux.HandleFunc("GET /v1/status", s.status)'
contains "${repo_root}/management/cmd/justvoxel-management-agent/main.go" 'statusHelper  = "/usr/libexec/justvoxel/mjust/web-status-json"'
contains "${repo_root}/management/cmd/justvoxel-management-agent/main.go" 'args := []string{"status"}'
for section in System 'Minecraft servers' Network Storage 'Advanced details'; do
    contains "${status}" "section '${section}'"
done
for forbidden in 'systemctl ' podman rcon-cli findmnt 'df -' JV_CONFIG DATA_PATH BACKUP_PATH minecraft.service minecraft.env '.extended.minecraft' '.minecraft.configured' '.backup.enabled' '/v1/minecraft/status' '/v1/minecraft/players'; do
    absent "${status}" "${forbidden}"
done
contains "${status}" 'path=/v1/status'
contains "${status}" "path='/v1/status?details=1'"
contains "${status}" 'read_instance_data /v1/minecraft/instances)'
contains "${status}" 'read_instance_data /v1/minecraft/instances/runtime)'
contains "${status}" 'base="/v1/minecraft/instances/${instance_id}"'
for suffix in configuration players backups/settings backups/status; do
    contains "${status}" "\${base}/${suffix}"
done

# Isolated API fixtures: never invoke real host/runtime commands.
fixture="$(mktemp -d)"
trap 'rm -rf -- "${fixture}"' EXIT
export STATUS_FIXTURE="${fixture}"
sed -e "s|source /usr/libexec/justvoxel/mjust/status-lib.sh|source ${repo_root}/mjust/libexec/status-lib.sh|" \
    -e "s|readonly api_client=.*|readonly api_client=${fixture}/api-client|" \
    -e 's/if (( EUID != 0 )); then/if false; then/' "${status}" >"${fixture}/status"
cat >"${fixture}/api-client" <<'API'
#!/usr/bin/bash
set -euo pipefail
printf '%s\n' "$2" >>"${STATUS_FIXTURE}/calls"
[[ $1 == GET ]] || exit 1
case "$2" in
    /v1/status*)
        echo '{"system":{"health":"Healthy","variant":"JustVoxel VM","ipv4":"192.0.2.10","tailscale":"100.64.0.1","netbird":"100.80.0.1"},"extended":{"system":{"hostname":"appliance","uptime_seconds":90061,"physical_ram_kib":8388608,"failed_count":0},"network":{"interface":"eth0","gateway":"192.0.2.1","dns":"192.0.2.1"},"storage":{"system":{"source":"system-disk","mount":"/var","available_kib":1048576,"used":"25%"}},"details":{"failed_services":"HOST SERVICES","storage":"HOST STORAGE","network":"HOST NETWORK"}}}' ;;
    /v1/minecraft/instances)
        [[ ${SCENARIO} != registry_error ]] || exit 1
        if [[ ${SCENARIO} == zero ]]; then echo '{"instances":[]}';
        elif [[ ${SCENARIO} == sole ]]; then echo '{"instances":[{"id":"jv-kids001","display_name":"Only configured name"}]}'; else
            echo '{"instances":[{"id":"jv-family1","display_name":"Family Survival"},{"id":"jv-kids001","display_name":"Family Survival"}]}'
        fi ;;
    /v1/minecraft/instances/runtime)
        [[ ${SCENARIO} != runtime_error ]] || exit 1
        if [[ ${SCENARIO} == zero ]]; then echo '{"instances":[]}'; else
            jq -n --arg scenario "${SCENARIO}" --arg state "${INSTANCE_STATE:-running}" '
                {instances:([{id:"jv-family1",state:$state},{id:"jv-kids001",state:"stopped"}]
                    | if $scenario == "orphan" then . + [{id:"jv-orphan1",state:"running"}]
                      elif $scenario == "missing" or $scenario == "sole" then map(select(.id != "jv-family1"))
                      elif $scenario == "duplicate" then . + [.[0]] else . end)}'
        fi ;;
    /v1/minecraft/instances/*/configuration)
        id="${2%/configuration}"; id="${id##*/}"
        [[ ${SCENARIO}:${id} != config_error:jv-family1 ]] || exit 1
        [[ ${SCENARIO} != foreign_config ]] || id=jv-foreign1
        jq -n --arg id "${id}" '{id:$id,runtime:{software:"paper",version:"1.21.8",version_policy:"pinned",java_port:25567,bedrock_enabled:false,java_memory:"1G",container_memory:"2G",image_tag:"java21",start_on_boot:true,timezone:"UTC"}}' ;;
    /v1/minecraft/instances/*/players)
        id="${2%/players}"; id="${id##*/}"
        [[ ${SCENARIO}:${id} != players_error:jv-family1 ]] || exit 1
        [[ ${SCENARIO} != foreign_players ]] || id=jv-foreign1
        jq -n --arg id "${id}" '{id:$id,state:"running",online:3,maximum:14}' ;;
    /v1/minecraft/instances/*/backups/settings)
        [[ ${SCENARIO} != settings_error ]] || exit 1
        echo '{"enabled":false,"time":"04:00","retention":7}' ;;
    /v1/minecraft/instances/*/backups/status)
        [[ ${SCENARIO} != backup_error ]] || exit 1
        jq -n --arg state "${BACKUP_STATE:-idle}" '{state:$state,last_success:"2026-10-17T04:00:00Z",last_attempt:"2026-10-17T04:00:00Z"}' ;;
    *) exit 1 ;;
esac
API
chmod +x "${fixture}/api-client"
run_status() {
    : >"${fixture}/calls"
    SCENARIO="$1" bash "${fixture}/status" "${@:2}" >"${fixture}/output"
}
healthy() { contains "${fixture}/output" Healthy; absent "${fixture}/output" 'Attention needed'; }
attention() { contains "${fixture}/output" 'Attention needed'; }
run_status zero
healthy
contains "${fixture}/output" 'No Minecraft servers configured.'
[[ $(wc -l <"${fixture}/calls") == 3 ]] || fail 'zero instances must not select a fallback'
run_status sole
healthy
contains "${fixture}/output" 'Only configured name (jv-kids001)'
absent "${fixture}/calls" /v1/minecraft/instances/jv-family1/
[[ $(wc -l <"${fixture}/calls") == 7 ]] || fail 'sole server must use only its immutable ID'
run_status normal
healthy
for id in jv-family1 jv-kids001; do
    contains "${fixture}/output" "Family Survival (${id})"
    for suffix in configuration players backups/settings backups/status; do
        contains "${fixture}/calls" "/v1/minecraft/instances/${id}/${suffix}"
    done
done
for preserved in appliance 'JustVoxel VM' '8.0 GiB' eth0 system-disk '100.64.0.1' '100.80.0.1' '3 / 14' '192.0.2.10:25567' 'java21' '04:00' '2026-10-17T04:00:00Z'; do
    contains "${fixture}/output" "${preserved}"
done
INSTANCE_STATE=stopped run_status normal
healthy
for state in failed unavailable invalid_configuration recovery_required; do
    INSTANCE_STATE="${state}" run_status normal
    attention
done
for state in failed interrupted; do
    BACKUP_STATE="${state}" run_status normal
    attention
done
for scenario in orphan missing duplicate config_error foreign_config registry_error runtime_error; do
    run_status "${scenario}"
    attention
done
run_status orphan
contains "${fixture}/output" jv-orphan1
absent "${fixture}/calls" /v1/minecraft/instances/jv-orphan1/
for scenario in config_error players_error foreign_players settings_error backup_error; do
    run_status "${scenario}"
    contains "${fixture}/output" Unavailable
    contains "${fixture}/output" 'Family Survival (jv-kids001)'
done
run_status normal --details
healthy
contains "${fixture}/calls" '/v1/status?details=1'
for detail in 'HOST SERVICES' 'HOST STORAGE' 'HOST NETWORK' 'Server details' 'Last backup attempt'; do
    contains "${fixture}/output" "${detail}"
done
absent "${fixture}/output" minecraft.service
absent "${fixture}/output" Podman

echo 'focused host and all-instance status regression tests passed.'
