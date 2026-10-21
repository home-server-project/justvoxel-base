#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf -- "${tmp}"' EXIT
source "${repo_root}/mjust/libexec/operation-trace.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
# No services or product helpers are executed: only the reusable trace library.
unset JV_OPERATION_TRACE_FD
jv_trace_stage 'ignored' < /dev/null
jv_trace_command printf '%s' 'no destination'
jv_trace_output 'ignored'
jv_trace_result 0
[[ ! -e ${tmp}/trace ]] || fail 'no-FD trace created a file'
# The test owns the isolated descriptor; the helper only inherits it.
exec 3>"${tmp}/trace"
JV_OPERATION_TRACE_FD=3
jv_trace_stage 'STORAGE'
jv_trace_command /fixed/helper 'two words' '' 'literal;$(false)'
jv_trace_output 'ordinary output'
jv_trace_result 7
exec 3>&-
expected="$(cat <<'RECORDS'
STAGE STORAGE
$ /fixed/helper two\ words '' literal\;\$\(false\)
ordinary output
[exit 7]
RECORDS
)"
[[ $(cat "${tmp}/trace") == "${expected}" ]] || fail 'trace records or separate arguments changed'
jv_trace_output 'closed descriptor' || fail 'closed pipe is not safe'
# A path masquerading as an FD cannot create or open a destination.
JV_OPERATION_TRACE_FD="${tmp}/arbitrary"
jv_trace_output 'must not write'
[[ ! -e ${tmp}/arbitrary ]] || fail 'arbitrary trace destination opened'
helper="$(cat "${repo_root}/mjust/libexec/operation-trace.sh")"
[[ ${helper} != *'set -x'* && ${helper} != *BASH_XTRACEFD* && ${helper} != *'read '* && ${helper} != *'eval '* ]] || fail 'interactive or generic tracing introduced'
[[ ${helper} != *'printenv'* && ${helper} != *'env '* && ${helper} != *'$(cat)'* ]] || fail 'environment, stdin, or destination path exposed'
[[ ${helper} == *'tee /dev/fd/3'* ]] || fail 'pull output must use only inherited FD 3'
# Redaction is exclusively Agent-owned; this raw record must travel through FD 3.
exec 3>"${tmp}/raw"
JV_OPERATION_TRACE_FD=3
jv_trace_output 'password=test-secret'
exec 3>&-
[[ $(cat "${tmp}/raw") == 'password=test-secret' ]] || fail 'Agent transport was bypassed'
# Exercise the fixed pull command without invoking real Podman or the network.
mkdir "${tmp}/bin"
cat >"${tmp}/bin/podman" <<'PODMAN'
#!/usr/bin/bash
printf '%s\n' "$@" >"${FAKE_PODMAN_ARGS}"
if [[ ${FAKE_PODMAN_EXIT} == 0 ]]; then
    printf '%s\n' 'Copying blob fixture' 'Copying config fixture'
    printf '%s\n' 'Writing manifest fixture' >&2
else
    printf '%s\n' 'Pull failed fixture' >&2
fi
exit "${FAKE_PODMAN_EXIT}"
PODMAN
chmod +x "${tmp}/bin/podman"
(
    export PATH="${tmp}/bin:${PATH}"
    export FAKE_PODMAN_ARGS="${tmp}/podman-args"
    export FAKE_PODMAN_EXIT=0
    JV_OPERATION_TRACE_FD=3
    image='example.invalid/fixture/minecraft:trace-test'
    exec 3>"${tmp}/pull-success"
    if jv_trace_pull "${image}"; then rc=0; else rc=$?; fi
    exec 3>&-
    [[ ${rc} == 0 ]] || fail 'successful pull returned nonzero'
    for line in 'Copying blob fixture' 'Copying config fixture' 'Writing manifest fixture'; do
        grep -Fxq -- "${line}" "${tmp}/pull-success" || fail "pull trace missing: ${line}"
    done
    [[ $(cat "${tmp}/podman-args") == "$(printf '%s\n' pull "${image}")" ]] || fail 'pull arguments changed'

    export FAKE_PODMAN_EXIT=17
    exec 3>"${tmp}/pull-failure"
    if jv_trace_pull "${image}"; then rc=0; else rc=$?; fi
    exec 3>&-
    grep -Fxq -- 'Pull failed fixture' "${tmp}/pull-failure" || fail 'failed pull output missing from trace'
    [[ ${rc} == 17 ]] || fail 'pull failure status replaced by tee status'
)
echo 'operation trace helper tests passed.'
