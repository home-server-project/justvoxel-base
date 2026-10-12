#!/usr/bin/bash
# Only the Agent's inherited pipe. No destination path, stdin, or environment dump.
jv_trace_output() {
    [[ ${JV_OPERATION_TRACE_FD:-} == 3 ]] || return 0
    (trap '' PIPE; printf '%s\n' "$1" >&3) 2>/dev/null || true
}
jv_trace_stage() { jv_trace_output "STAGE $1"; }
jv_trace_command() {
    local command='$' argument
    for argument in "$@"; do
        printf -v argument '%q' "${argument}"
        command+=" ${argument}"
    done
    jv_trace_output "${command}"
}
jv_trace_result() { jv_trace_output "[exit $1]"; }
# Use only at explicit, non-secret external command sites. Redirections and
# stdout remain with the caller; do not capture JSON responses or stdin here.
jv_trace_run() {
    local rc
    jv_trace_command "$@"
    if "$@"; then rc=0; else rc=$?; fi
    jv_trace_result "${rc}"
    return "${rc}"
}
