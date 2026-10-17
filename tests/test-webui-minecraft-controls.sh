#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

interrupt=mjust/libexec/interrupt-safety.sh
agent=management/cmd/justvoxel-management-agent/main.go
agent_control=management/cmd/justvoxel-management-agent/minecraft.go
agent_runtime=management/cmd/justvoxel-management-agent/minecraft_instance_runtime_api.go
agent_players=management/cmd/justvoxel-management-agent/minecraft_instance_maintenance.go
management_unit=system_files/usr/lib/systemd/system/justvoxel-management.service

grep -Fq 'NoNewPrivileges=no' "${management_unit}"
if grep -Fq 'NoNewPrivileges=yes' "${management_unit}"; then
    echo 'ERROR: Management Agent cannot use NoNewPrivileges=yes with its SELinux Podman helper path.' >&2
    exit 1
fi

grep -Fq 'JV_INTERRUPT_CONFIRMATION_MODE:-interactive' "${interrupt}"
grep -Fq 'required)' "${interrupt}"
grep -Fq 'return 10' "${interrupt}"
grep -Fq 'confirmed)' "${interrupt}"
grep -Fq 'podman kill --signal SIGUSR1 minecraft' "${interrupt}"
grep -Fq 'warning_points=(30 15 10 5 4 3 2 1)' "${interrupt}"
grep -Fq 'jv_minecraft_announce_shutdown' "${interrupt}"
grep -Fq 'jv_wait_minecraft_stopped' "${interrupt}"
grep -Fq 'jv_stop_minecraft_adaptive' "${interrupt}"
grep -Fq 'STOP_SERVER_ANNOUNCE_DELAY=60' templates/config/minecraft.env.in

grep -Fq 'registerMinecraftRoutes(mux, s)' "${agent}"
for route in 'GET /v1/players' 'POST /v1/minecraft/start' 'POST /v1/minecraft/stop' 'POST /v1/minecraft/restart'; do
    if grep -Fq "${route}" "${agent_control}"; then
        echo "ERROR: retired global runtime route remains registered: ${route}" >&2
        exit 1
    fi
done
grep -Fq 'registerInstanceRuntimeRoutes(mux, s)' "${agent_control}"
grep -Fq 'GET /v1/minecraft/instances/{id}/status' "${agent_runtime}"
grep -Fq 'GET /v1/minecraft/instances/{id}/players' "${agent_players}"
grep -Fq '[]string{"start", "stop", "restart"}' "${agent_runtime}"
grep -Fq '"POST /v1/minecraft/instances/{id}/"+action' "${agent_runtime}"
if grep -Eq 'exec\.Command(Context)?\([^,]+,[[:space:]]*request\.' "${agent_control}"; then
    echo 'ERROR: Minecraft management endpoint passes request-controlled commands to exec.' >&2
    exit 1
fi

echo 'JustVoxel WebUI Minecraft control policy checks passed.'
