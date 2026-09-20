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

rm -f /tmp/jv-player-required.out
echo 'adaptive Minecraft shutdown tests passed.'
