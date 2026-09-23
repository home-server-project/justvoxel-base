#!/usr/bin/bash

readonly JV_MINECRAFT_STOP_TIMEOUT_SECONDS="${JV_MINECRAFT_STOP_TIMEOUT_SECONDS:-150}"

jv_player_list_raw() {
    podman exec minecraft rcon-cli 'list' 2>/dev/null
}

jv_player_online_count() {
    local raw="$1"
    if [[ ${raw} =~ There[[:space:]]are[[:space:]]([0-9]+)[[:space:]]of[[:space:]]a[[:space:]]max[[:space:]]of[[:space:]]([0-9]+)[[:space:]]players[[:space:]]online:? ]]; then
        printf '%s\n' "${BASH_REMATCH[1]}"
        return 0
    fi
    return 1
}

jv_minecraft_still_running() {
    if systemctl is-active --quiet minecraft.service 2>/dev/null; then
        return 0
    fi
    podman ps --format '{{.Names}}' 2>/dev/null | grep -Fxq minecraft
}

jv_wait_minecraft_stopped() {
    local waited=0
    while jv_minecraft_still_running; do
        if (( waited >= JV_MINECRAFT_STOP_TIMEOUT_SECONDS )); then
            echo 'ERROR: Minecraft did not finish its graceful shutdown before the safety timeout.' >&2
            return 1
        fi
        sleep 1
        waited=$((waited + 1))
    done
    return 0
}

jv_minecraft_announce_shutdown() {
    local seconds="$1" unit=seconds
    (( seconds == 1 )) && unit=second
    podman exec minecraft rcon-cli "say Server shutting down in ${seconds} ${unit}" >/dev/null 2>&1 || true
}

jv_begin_minecraft_stop() {
    if ! systemctl --no-block stop minecraft.service; then
        echo 'ERROR: Minecraft graceful stop request failed.' >&2
        return 1
    fi
}

jv_bypass_empty_server_shutdown_delay() {
    local attempt
    for attempt in 1 2 3 4 5; do
        if podman kill --signal SIGUSR1 minecraft >/dev/null 2>&1; then
            return 0
        fi
        if ! jv_minecraft_still_running; then
            return 0
        fi
        sleep 0.1
    done

    echo 'WARNING: could not bypass the container shutdown announcement delay; waiting for the normal graceful stop.' >&2
    return 0
}

jv_countdown_online_server_shutdown() {
    local previous=60 next delay
    for next in 30 15 10 5 4 3 2 1; do
        delay=$((previous - next))
        sleep "${delay}"
        if ! jv_minecraft_still_running; then
            return 0
        fi
        jv_minecraft_announce_shutdown "${next}"
        previous="${next}"
    done

    sleep 1
    jv_wait_minecraft_stopped
}

jv_player_check_before_interrupt() {
    local action="${1:-interrupt the server}" players mode online

    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
        return 0
    fi

    players="$(jv_player_list_raw 2>/dev/null || true)"
    if [[ -z ${players} ]]; then
        echo 'ERROR: could not confirm player status through RCON. Refusing to interrupt Minecraft.' >&2
        return 1
    fi
    online="$(jv_player_online_count "${players}" 2>/dev/null || true)"
    if [[ ! ${online} =~ ^[0-9]+$ ]]; then
        echo 'ERROR: could not parse player status through RCON. Refusing to interrupt Minecraft.' >&2
        return 1
    fi

    printf '%s\n' "${players}"
    if (( online == 0 )); then
        return 0
    fi

    mode="${JV_INTERRUPT_CONFIRMATION_MODE:-interactive}"
    case "${mode}" in
        interactive)
            confirm "Players appear to be online. ${action} anyway?"
            ;;
        required)
            return 10
            ;;
        confirmed)
            return 0
            ;;
        *)
            echo "ERROR: invalid interruption confirmation mode: ${mode}" >&2
            return 2
            ;;
    esac
}

jv_stop_minecraft_adaptive() {
    local action="${1:-continue}" players mode online

    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
        return 0
    fi

    players="$(jv_player_list_raw 2>/dev/null || true)"
    if [[ -z ${players} ]]; then
        echo 'ERROR: could not confirm player status through RCON. Refusing to stop Minecraft.' >&2
        return 1
    fi
    online="$(jv_player_online_count "${players}" 2>/dev/null || true)"
    if [[ ! ${online} =~ ^[0-9]+$ ]]; then
        echo 'ERROR: could not parse player status through RCON. Refusing to stop Minecraft.' >&2
        return 1
    fi

    mode="${JV_INTERRUPT_CONFIRMATION_MODE:-interactive}"
    if (( online > 0 )); then
        case "${mode}" in
            interactive)
                printf '%s\n' "${players}"
                confirm "Players appear to be online. ${action} anyway?" || return 20
                ;;
            required)
                printf '%s\n' "${players}"
                return 10
                ;;
            confirmed)
                ;;
            *)
                echo "ERROR: invalid interruption confirmation mode: ${mode}" >&2
                return 2
                ;;
        esac

        jv_begin_minecraft_stop || return 1
        jv_countdown_online_server_shutdown
        return $?
    fi

    jv_begin_minecraft_stop || return 1
    jv_bypass_empty_server_shutdown_delay
    jv_wait_minecraft_stopped
}

jv_prepare_minecraft_for_host_shutdown() {
    local players online

    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
        return 0
    fi

    players="$(timeout 5s podman exec minecraft rcon-cli 'list' 2>/dev/null || true)"
    if [[ -z ${players} ]]; then
        echo 'WARNING: could not confirm Minecraft player status during host shutdown; preserving the normal container shutdown delay.' >&2
        return 0
    fi
    online="$(jv_player_online_count "${players}" 2>/dev/null || true)"
    if [[ ! ${online} =~ ^[0-9]+$ ]]; then
        echo 'WARNING: could not parse Minecraft player status during host shutdown; preserving the normal container shutdown delay.' >&2
        return 0
    fi

    if (( online > 0 )); then
        echo "Minecraft has ${online} player(s) online; preserving the configured shutdown announcement delay."
        return 0
    fi

    echo 'Minecraft has no players online; bypassing the container shutdown announcement delay.'
    if ! podman kill --signal SIGUSR1 minecraft >/dev/null 2>&1; then
        echo 'WARNING: could not bypass the Minecraft shutdown announcement delay; normal graceful shutdown will continue.' >&2
    fi
    return 0
}

jv_stop_minecraft_for_system_action() {
    local action="${1:-continue}"
    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
        return 0
    fi

    echo 'Stopping Minecraft through the player-aware graceful shutdown path.'
    jv_stop_minecraft_adaptive "${action}"
}
