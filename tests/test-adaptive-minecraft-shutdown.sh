#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/interrupt-safety.sh"

fail(){ echo "FAIL: $*" >&2; exit 1; }

calls=''
announcements=''
running=yes

confirm(){ return 0; }
systemctl(){
    calls+=" systemctl:$*"
    if [[ $1 == is-active ]]; then
        [[ ${running} == yes ]]
        return
    fi
    return 0
}
podman(){
    calls+=" podman:$*"
    if [[ $1 == kill ]]; then
        running=no
        return 0
    fi
    if [[ $1 == ps ]]; then
        [[ ${running} == yes ]] && printf 'minecraft\n'
        return 0
    fi
    if [[ $1 == exec && ${3:-} == rcon-cli && ${4:-} == list ]]; then
        printf 'There are 0 of a max of 20 players online:\n'
        return 0
    fi
    if [[ $1 == exec && ${3:-} == rcon-cli ]]; then
        announcements+=" ${4:-}"
        return 0
    fi
    return 1
}
sleep(){ :; }

JV_INTERRUPT_CONFIRMATION_MODE=required jv_stop_minecraft_adaptive 'Stop Minecraft' || fail 'zero-player adaptive stop failed'
[[ ${calls} == *'systemctl:--no-block stop minecraft.service'* ]] || fail 'zero-player stop did not request systemd stop'
[[ ${calls} == *'podman:kill --signal SIGUSR1 minecraft'* ]] || fail 'zero-player stop did not bypass announce delay'

calls=''
announcements=''
running=yes
podman(){
    calls+=" podman:$*"
    if [[ $1 == ps ]]; then
        printf 'minecraft\n'
        return 0
    fi
    if [[ $1 == exec && ${3:-} == rcon-cli && ${4:-} == list ]]; then
        printf 'There are 1 of a max of 20 players online: Steve\n'
        return 0
    fi
    if [[ $1 == exec && ${3:-} == rcon-cli ]]; then
        announcements+="|${4:-}"
        return 0
    fi
    return 1
}
jv_minecraft_still_running(){ return 0; }
jv_wait_minecraft_stopped(){ running=no; return 0; }

set +e
JV_INTERRUPT_CONFIRMATION_MODE=required jv_stop_minecraft_adaptive 'Restart Minecraft' >/tmp/jv-player-required.out
rc=$?
set -e
[[ ${rc} == 10 ]] || fail "online-player required mode returned ${rc}, want 10"
grep -Fq 'Steve' /tmp/jv-player-required.out || fail 'online-player confirmation did not expose player state'
[[ ${calls} != *'systemctl:--no-block stop minecraft.service'* ]] || fail 'required confirmation mode stopped Minecraft before confirmation'

calls=''
JV_INTERRUPT_CONFIRMATION_MODE=confirmed jv_stop_minecraft_adaptive 'Restart Minecraft' || fail 'confirmed online-player stop failed'
[[ ${calls} == *'systemctl:--no-block stop minecraft.service'* ]] || fail 'confirmed online-player stop did not request systemd stop'
[[ ${calls} != *'podman:kill --signal SIGUSR1 minecraft'* ]] || fail 'online-player stop must not bypass the countdown'
for seconds in 30 15 10 5 4 3 2 1; do
    [[ ${announcements} == *"Server shutting down in ${seconds} "* ]] || fail "missing ${seconds}-second shutdown announcement"
done

calls=''
announcements=''
running=yes
podman(){
    calls+=" podman:$*"
    if [[ $1 == ps ]]; then
        [[ ${running} == yes ]] && printf 'minecraft\n'
        return 0
    fi
    if [[ $1 == kill && ${2:-} == --signal && ${3:-} == SIGUSR1 && ${4:-} == minecraft ]]; then
        running=no
        return 0
    fi
    if [[ $1 == exec && ${3:-} == rcon-cli && ${4:-} == list ]]; then
        printf 'There are 1 of a max of 20 players online: Steve\n'
        return 0
    fi
    if [[ $1 == exec && ${3:-} == rcon-cli ]]; then
        announcements+="|${4:-}"
        return 0
    fi
    return 1
}
jv_minecraft_still_running(){
    [[ ${running} == yes ]]
}
JV_INTERRUPT_CONFIRMATION_MODE=confirmed JV_INTERRUPT_WARNING_SECONDS=10 jv_stop_minecraft_adaptive 'Reboot JustVoxel' || fail 'quick online-player stop failed'
for seconds in 10 5 4 3 2 1; do
    [[ ${announcements} == *"Server shutting down in ${seconds} "* ]] || fail "quick reboot missing ${seconds}-second shutdown announcement"
done
[[ ${announcements} != *'Server shutting down in 30 '* ]] || fail 'quick reboot unexpectedly used the normal 30-second warning'
[[ ${calls} == *'podman:kill --signal SIGUSR1 minecraft'* ]] || fail 'quick reboot did not bypass remaining container shutdown delay after 10-second warning'

rm -f /tmp/jv-player-required.out

calls=''
running=yes
timeout(){
    calls+=" timeout:$*"
    shift
    "$@"
}
podman(){
    calls+=" podman:$*"
    if [[ $1 == exec && ${3:-} == rcon-cli && ${4:-} == list ]]; then
        printf 'There are 0 of a max of 20 players online:\n'
        return 0
    fi
    if [[ $1 == kill && ${2:-} == --signal && ${3:-} == SIGUSR1 && ${4:-} == minecraft ]]; then
        return 0
    fi
    return 1
}
jv_prepare_minecraft_for_host_shutdown || fail 'zero-player host-shutdown preparation failed'
grep -Fq "timeout 5s podman exec minecraft rcon-cli 'list'" "${repo_root}/mjust/libexec/interrupt-safety.sh" || fail 'host-shutdown preparation did not bound the RCON query'
[[ ${calls} == *'podman:kill --signal SIGUSR1 minecraft'* ]] || fail 'zero-player host shutdown did not bypass the announcement delay'

calls=''
podman(){
    calls+=" podman:$*"
    if [[ $1 == exec && ${3:-} == rcon-cli && ${4:-} == list ]]; then
        printf 'There are 2 of a max of 20 players online: Steve, Alex\n'
        return 0
    fi
    return 1
}
jv_prepare_minecraft_for_host_shutdown || fail 'online-player host-shutdown preparation failed'
[[ ${calls} != *'podman:kill --signal SIGUSR1 minecraft'* ]] || fail 'online-player host shutdown bypassed the configured countdown'

calls=''
podman(){
    calls+=" podman:$*"
    if [[ $1 == exec && ${3:-} == rcon-cli && ${4:-} == list ]]; then
        return 1
    fi
    return 1
}
jv_prepare_minecraft_for_host_shutdown >/tmp/jv-host-shutdown-unknown.out 2>&1 || fail 'unknown-player host-shutdown preparation must fail open to normal graceful stop'
[[ ${calls} != *'podman:kill --signal SIGUSR1 minecraft'* ]] || fail 'unknown player state bypassed the configured countdown'
grep -Fq 'preserving the normal container shutdown delay' /tmp/jv-host-shutdown-unknown.out || fail 'unknown player state did not explain the fallback'

guard_unit="${repo_root}/system_files/usr/lib/systemd/system/justvoxel-minecraft-shutdown-guard.service"
guard_script="${repo_root}/mjust/libexec/host-shutdown-guard"
grep -Fqx 'After=minecraft.service' "${guard_unit}" || fail 'shutdown guard is not ordered after minecraft.service for reverse shutdown ordering'
grep -Fqx 'ExecStop=/usr/libexec/justvoxel/mjust/host-shutdown-guard' "${guard_unit}" || fail 'shutdown guard ExecStop is not wired to the helper'
grep -Fqx 'WantedBy=multi-user.target' "${guard_unit}" || fail 'shutdown guard is not enabled from multi-user.target'
grep -Fq 'systemctl is-system-running' "${guard_script}" || fail 'shutdown guard does not distinguish host shutdown from manual service stop'
grep -Fq 'jv_prepare_minecraft_for_host_shutdown' "${guard_script}" || fail 'shutdown guard does not call the adaptive host-shutdown helper'

rm -f /tmp/jv-host-shutdown-unknown.out
echo 'adaptive Minecraft shutdown tests passed.'
