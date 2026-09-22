#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/whitelist-backend"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
mkdir -p "${tmp}/bin" "${tmp}/libexec"

cat > "${tmp}/libexec/common.sh" <<'EOF'
#!/usr/bin/bash
require_root() { :; }
require_config() { BEDROCK_ENABLED=yes; }
EOF

cat > "${tmp}/bin/systemctl" <<'EOF'
#!/usr/bin/bash
if [[ $1 == is-active ]]; then
    echo active
    exit 0
fi
exit 1
EOF

cat > "${tmp}/bin/podman" <<'EOF'
#!/usr/bin/bash
set -euo pipefail
state="${JV_WHITELIST_TEST_STATE:?}"
log="${JV_WHITELIST_TEST_LOG:?}"
[[ $1 == exec && $2 == minecraft && $3 == rcon-cli ]]
cmd="$4"
printf '%s\n' "${cmd}" >> "${log}"

read_state() {
    if [[ -s ${state} ]]; then cat "${state}"; fi
}
write_state() {
    if [[ -n $1 ]]; then
        printf '%s\n' "$1" > "${state}"
    else
        : > "${state}"
    fi
}
list_state() {
    local value
    value="$(read_state)"
    if [[ -z ${value} ]]; then
        echo 'There are no whitelisted players'
    else
        echo "There are 1 whitelisted player(s): ${value}"
    fi
}

case "${cmd}" in
    'whitelist list')
        list_state
        ;;
    'whitelist remove .CatchaLlama')
        write_state ''
        echo 'Removed .CatchaLlama from the whitelist'
        ;;
    'fwhitelist add CatchaLlama')
        echo 'Got an error from requesting the xuid of a Bedrock player: Unable to find user in our cache. Please try specifying their Floodgate UUID instead' >&2
        exit 1
        ;;
    'whitelist add .CatchaLlama')
        write_state '.CatchaLlama'
        echo 'Added .CatchaLlama to the whitelist'
        ;;
    *)
        echo "unexpected RCON command: ${cmd}" >&2
        exit 2
        ;;
esac
EOF
chmod +x "${tmp}/bin/systemctl" "${tmp}/bin/podman"

export PATH="${tmp}/bin:${PATH}"
export JV_LIBEXEC_DIR="${tmp}/libexec"
export JV_WHITELIST_TEST_STATE="${tmp}/state"
export JV_WHITELIST_TEST_LOG="${tmp}/rcon.log"
printf '%s\n' '.CatchaLlama' > "${JV_WHITELIST_TEST_STATE}"
: > "${JV_WHITELIST_TEST_LOG}"

remove_output="$(bash "${helper}" remove-bedrock .CatchaLlama)"
grep -Fq 'Removed CatchaLlama from the whitelist.' <<< "${remove_output}" || {
    echo "FAIL: Bedrock remove did not report success: ${remove_output}" >&2
    exit 1
}
[[ ! -s ${JV_WHITELIST_TEST_STATE} ]] || {
    echo 'FAIL: Bedrock entry remained after remove' >&2
    exit 1
}

add_output="$(bash "${helper}" add-bedrock CatchaLlama)"
grep -Fq 'Added CatchaLlama to the whitelist using its known local Bedrock profile.' <<< "${add_output}" || {
    echo "FAIL: local-profile fallback was not reported: ${add_output}" >&2
    exit 1
}
grep -Fxq '.CatchaLlama' "${JV_WHITELIST_TEST_STATE}" || {
    echo 'FAIL: Bedrock entry was not restored with Floodgate prefix' >&2
    exit 1
}

grep -Fq 'fwhitelist add CatchaLlama' "${JV_WHITELIST_TEST_LOG}" || {
    echo 'FAIL: Floodgate gamertag lookup was not attempted first' >&2
    exit 1
}
grep -Fq 'whitelist add .CatchaLlama' "${JV_WHITELIST_TEST_LOG}" || {
    echo 'FAIL: local Floodgate-prefixed fallback was not attempted' >&2
    exit 1
}

before="$(wc -l < "${JV_WHITELIST_TEST_LOG}")"
idempotent_output="$(bash "${helper}" add-bedrock .CatchaLlama)"
after="$(wc -l < "${JV_WHITELIST_TEST_LOG}")"
grep -Fq 'already on the whitelist' <<< "${idempotent_output}" || {
    echo "FAIL: repeated Bedrock add was not idempotent: ${idempotent_output}" >&2
    exit 1
}
if (( after != before + 1 )); then
    echo 'FAIL: repeated Bedrock add performed a mutation instead of list-only preflight' >&2
    exit 1
fi

echo 'Bedrock whitelist backend lifecycle tests passed.'
