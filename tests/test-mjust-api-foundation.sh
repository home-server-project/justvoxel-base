#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
api_client="${repo_root}/mjust/libexec/api-client"
authorization="${repo_root}/management/cmd/justvoxel-management-agent/authorization.go"
identity="${repo_root}/management/cmd/justvoxel-management-agent/identity.go"
main="${repo_root}/management/cmd/justvoxel-management-agent/main.go"
players="${repo_root}/mjust/libexec/players"
service="${repo_root}/mjust/libexec/service"
justfile="${repo_root}/mjust/justfile"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

for file in "${api_client}" "${authorization}" "${identity}" "${main}" "${players}" "${service}" "${justfile}"; do
    [[ -f ${file} ]] || fail "missing mJust Management API file: ${file}"
done

grep -Fq 'authSourceLocalRoot authSource = "local-root"' "${identity}" || fail 'local-root auth source is missing'
grep -Fq 'func localRootAdministrator' "${authorization}" || fail 'local-root principal helper is missing'
grep -Fq 'uid != 0' "${authorization}" || fail 'local-root principal is not restricted to uid 0'
grep -Fq 'if sess, ok := localRootAdministrator(r); ok {' "${main}" || fail 'root-peer authorization is not wired into the Agent'

grep -Fq 'EUID != 0' "${api_client}" || fail 'mJust API client does not require root'
grep -Fq -- '--unix-socket' "${api_client}" || fail 'mJust API client does not use the Unix socket'
grep -Fq 'JV_MANAGEMENT_SOCKET' "${api_client}" || fail 'mJust API client socket contract is missing'
grep -Fq -- '--data-binary @-' "${api_client}" || fail 'mJust API request bodies must come from stdin'
if grep -Fq 'Authorization:' "${api_client}"; then
    fail 'mJust API client must not introduce a persistent bearer token'
fi

grep -Fq '"${api_client}" GET /v1/players' "${players}" || fail 'mJust Players does not use the Management API'
grep -Fq 'players:' "${justfile}" || fail 'mJust Players recipe is missing'
grep -Fq 'sudo /usr/libexec/justvoxel/mjust/players' "${justfile}" || fail 'mJust Players must enter the API path through sudo/root'
for forbidden in 'systemctl ' 'podman ' 'rcon-cli'; do
    if grep -Fq "${forbidden}" "${players}"; then
        fail "mJust Players still performs direct system access: ${forbidden}"
    fi
done

grep -Fq 'start|stop|restart)' "${service}" || fail 'mJust service action allowlist is missing'
grep -Fq 'POST "/v1/minecraft/${action}"' "${service}" || fail 'mJust service does not use the Minecraft Management API'
for action in start stop restart; do
    grep -Fq "${action}:" "${justfile}" || fail "mJust ${action} recipe is missing"
    grep -Fq "sudo /usr/libexec/justvoxel/mjust/service ${action}" "${justfile}" || fail "mJust ${action} must enter the API path through sudo/root"
done
grep -Fq '.confirmation_required // false' "${service}" || fail 'mJust service frontend does not handle player confirmation responses'
grep -Fq '"confirm_players":%s' "${service}" || fail 'mJust service frontend does not send explicit player confirmation'
for forbidden in 'systemctl ' 'podman ' 'rcon-cli' 'jv_player_check_before_interrupt'; do
    if grep -Fq "${forbidden}" "${service}"; then
        fail "mJust service frontend still performs direct system/safety access: ${forbidden}"
    fi
done

echo 'mJust Management API foundation, Players, and Minecraft control migration checks passed.'
