#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

helper=mjust/libexec/web-status-json
interrupt=mjust/libexec/interrupt-safety.sh
agent=management/cmd/justvoxel-management-agent/main.go
agent_control=management/cmd/justvoxel-management-agent/minecraft.go
management_unit=system_files/usr/lib/systemd/system/justvoxel-management.service

grep -Fq "mc_version='Not configured'" "${helper}"
grep -Fq "mc_version='Unavailable'" "${helper}"
grep -Fq 'MINECRAFT_VERSION} != LATEST' "${helper}"
grep -Fq 'Starting minecraft server version' "${helper}"
grep -Fq 'configured:$configured' "${helper}"
grep -Fq 'web_players' "${helper}"
grep -Fq 'web_action' "${helper}"
grep -Fq -- '--confirm-players' "${helper}"
grep -Fq "printf '%s\\n'" "${helper}"
grep -Fq 'systemctl --no-block start minecraft.service' "${helper}"
grep -Fq 'jv_stop_minecraft_adaptive "${action^} Minecraft"' "${helper}"

grep -Fq 'NoNewPrivileges=no' "${management_unit}"
if grep -Fq 'NoNewPrivileges=yes' "${management_unit}"; then
    echo 'ERROR: Management Agent cannot use NoNewPrivileges=yes with its SELinux Podman helper path.' >&2
    exit 1
fi

empty_names="$(printf '%s\n' '' | jq -R 'if length == 0 then [] else split(",") | map(gsub("^ +| +$"; "")) | map(select(length > 0)) end')"
[[ ${empty_names} == '[]' ]] || { echo "ERROR: zero-player names must encode as []; got ${empty_names}" >&2; exit 1; }
multiple_names="$(printf '%s\n' 'Alex, Steve' | jq -cR 'if length == 0 then [] else split(",") | map(gsub("^ +| +$"; "")) | map(select(length > 0)) end')"
[[ ${multiple_names} == '["Alex","Steve"]' ]] || { echo "ERROR: player names parser regression: ${multiple_names}" >&2; exit 1; }

grep -Fq 'JV_INTERRUPT_CONFIRMATION_MODE:-interactive' "${interrupt}"
grep -Fq 'required)' "${interrupt}"
grep -Fq 'return 10' "${interrupt}"
grep -Fq 'confirmed)' "${interrupt}"
grep -Fq 'podman kill --signal SIGUSR1 minecraft' "${interrupt}"
grep -Fq 'warning_points=(30 15 10 5 4 3 2 1)' "${interrupt}"
grep -Fq 'jv_minecraft_announce_shutdown' "${interrupt}"
grep -Fq 'jv_wait_minecraft_stopped' "${interrupt}"
grep -Fq 'jv_stop_minecraft_adaptive' "${interrupt}"
grep -Fq '180*time.Second' "${agent_control}"
grep -Fq 'STOP_SERVER_ANNOUNCE_DELAY=60' templates/config/minecraft.env.in

grep -Fq 'registerMinecraftRoutes(mux, s)' "${agent}"
grep -Fq 'GET /v1/players' "${agent_control}"
grep -Fq 'POST /v1/minecraft/start' "${agent_control}"
grep -Fq 'POST /v1/minecraft/stop' "${agent_control}"
grep -Fq 'POST /v1/minecraft/restart' "${agent_control}"
if grep -Eq 'exec\.Command(Context)?\([^,]+,[[:space:]]*request\.' "${agent_control}"; then
    echo 'ERROR: Minecraft management endpoint passes request-controlled commands to exec.' >&2
    exit 1
fi

echo 'JustVoxel WebUI Minecraft control policy checks passed.'
