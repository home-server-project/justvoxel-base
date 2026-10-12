#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf -- "${tmp}"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

export DIAGNOSTIC_TEST_DIR="${tmp}"
JV_SETUP_API_CLIENT="${tmp}/api-client"
cat > "${JV_SETUP_API_CLIENT}" <<'EOF'
#!/usr/bin/bash
set -euo pipefail
printf '%s\n' "$@" > "${DIAGNOSTIC_TEST_DIR}/args"
cat > "${DIAGNOSTIC_TEST_DIR}/payload"
EOF
chmod 0755 "${JV_SETUP_API_CLIENT}"

# Load the actual function unchanged, keeping the API client isolated in the fixture.
source <(sed -n '/^jv_setup_diagnostic_event() {/,/^}/p' "${repo_root}/mjust/libexec/setup-api.sh")
declare -F jv_setup_diagnostic_event >/dev/null || fail 'diagnostic helper was not loaded'

session_id='01234567-89ab-cdef-0123-456789abcdef'
run_event() {
    rm -f -- "${tmp}/args" "${tmp}/payload"
    jv_setup_diagnostic_event "$@" 2>"${tmp}/stderr" || fail 'diagnostic helper must remain best-effort'
    [[ ! -s ${tmp}/stderr ]] || fail 'diagnostic helper leaked stderr'
}

assert_post() {
    local event="$1" values="$2" expected_args
    expected_args="$(printf '%s\n' POST "/v1/admin/setup/diagnostics/${session_id}/event" --data)"
    [[ -f ${tmp}/args && $(cat "${tmp}/args") == "${expected_args}" ]] || fail 'diagnostic API request changed or was lost'
    jq -se --arg event "${event}" --argjson values "${values}" \
        'length == 1 and .[0] == {event:$event,values:$values}' \
        "${tmp}/payload" >/dev/null || fail 'diagnostic payload is invalid or changed'
}

run_event "${session_id}" 'Minecraft EULA accepted' '{}'
assert_post 'Minecraft EULA accepted' '{}'

run_event "${session_id}" 'backup settings collected' '{"backup_automatic":"true","backup_keep":"7"}'
assert_post 'backup settings collected' '{"backup_automatic":"true","backup_keep":"7"}'

run_event "${session_id}" 'mjust setup started'
assert_post 'mjust setup started' '{}'

run_event "${session_id}" 'mjust setup started' ''
assert_post 'mjust setup started' '{}'

run_event "${session_id}" 'malformed diagnostic values' '{invalid'
[[ ! -e ${tmp}/args && ! -e ${tmp}/payload ]] || fail 'malformed diagnostic values caused an API POST'

run_event 'invalid-session-id' 'mjust setup started' '{}'
[[ ! -e ${tmp}/args && ! -e ${tmp}/payload ]] || fail 'invalid diagnostic session ID caused an API POST'

echo 'mjust setup diagnostic event tests passed.'
