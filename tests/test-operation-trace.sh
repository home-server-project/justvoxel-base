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
[[ ${helper} != *'printenv'* && ${helper} != *'env '* && ${helper} != *'$(cat)'* && ${helper} != *'/dev/fd/'* ]] || fail 'environment, stdin, or destination path exposed'
# Redaction is exclusively Agent-owned; this raw record must travel through FD 3.
exec 3>"${tmp}/raw"
JV_OPERATION_TRACE_FD=3
jv_trace_output 'password=test-secret'
exec 3>&-
[[ $(cat "${tmp}/raw") == 'password=test-secret' ]] || fail 'Agent transport was bypassed'
echo 'operation trace helper tests passed.'
