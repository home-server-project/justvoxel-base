#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf -- "${fixture}"' EXIT
JV_CONFIG_DIR="${fixture}/config"
source <(sed -n '/^validate_port() {/,/^}/p' "${repo_root}/mjust/libexec/common.sh")
source <(sed -n '/^geyser_tcp_firewall_add() {/,/^wait_for_rcon() {/p' "${repo_root}/mjust/libexec/common.sh" | sed '$d')
# Simulate root ownership only; permissions and private evidence remain real.
# Strip the two ownership options without requiring root on the test host.
install() {
    local args=()
    while (($#)); do
        case "$1" in -o|-g) shift 2 ;; *) args+=("$1"); shift ;; esac
    done
    command install "${args[@]}"
}
stat() {
    if [[ $2 == '%u:%g:%a' ]]; then
        printf '0:0:%s\n' "$(command stat -c '%a' "${@: -1}")"
    else command stat "$@"; fi
}
declare -A rules=([public:443/tcp]=1 [public:19132/tcp]=1)
fixture_default_zone=public
firewall-cmd() {
    local selected="${fixture_default_zone}" arg key
    for arg in "$@"; do
        case "${arg}" in
            --get-default-zone) printf '%s\n' "${fixture_default_zone}"; return ;;
            --zone=*) selected="${arg#*=}" ;;
            --query-port=*) key="${selected}:${arg#*=}"; [[ ${rules[$key]:-0} == 1 ]]; return ;;
            --add-port=*) rules["${selected}:${arg#*=}"]=1 ;;
            --remove-port=*) unset 'rules['"${selected}:${arg#*=}"']' ;;
        esac
    done
}
# Pre-existing TCP access must never be claimed or removed.
geyser_tcp_firewall_add 19132
geyser_tcp_firewall_remove 19132
[[ ${rules[public:19132/tcp]} == 1 ]]
# A port change removes only the TCP rule created by JustVoxel, in its zone.
JAVA_PORT=25565 BEDROCK_ENABLED=yes BEDROCK_PORT=19133
configure_firewall_initial
[[ ${rules[public:19133/tcp]} == 1 && ${rules[public:19133/udp]} == 1 ]]
BEDROCK_PORT=19134
update_firewall_ports 25565 yes 19133
[[ ${rules[public:19133/tcp]:-0} == 0 && ${rules[public:19133/udp]:-0} == 0 ]]
[[ ${rules[public:19134/tcp]} == 1 && ${rules[public:19134/udp]} == 1 ]]
BEDROCK_ENABLED=no
update_firewall_ports 25565 yes 19134
[[ ${rules[public:19134/tcp]:-0} == 0 && ${rules[public:19134/udp]:-0} == 0 ]]
geyser_tcp_firewall_add 19135
fixture_default_zone=home
geyser_tcp_firewall_remove 19135
[[ ${rules[public:19135/tcp]:-0} == 0 ]]
[[ ${rules[public:443/tcp]} == 1 && ${rules[public:19132/tcp]} == 1 ]]
