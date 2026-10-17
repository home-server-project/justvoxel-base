#!/usr/bin/bash

jv_status_format_duration() {
    local total="${1:-0}" days hours minutes
    [[ ${total} =~ ^[0-9]+$ ]] || total=0
    days=$((total / 86400))
    hours=$(((total % 86400) / 3600))
    minutes=$(((total % 3600) / 60))
    if (( days > 0 )); then
        printf '%dd %dh %dm' "${days}" "${hours}" "${minutes}"
    elif (( hours > 0 )); then
        printf '%dh %dm' "${hours}" "${minutes}"
    else
        printf '%dm' "${minutes}"
    fi
}

jv_status_format_kib() {
    local kib="${1:-0}"
    awk -v kib="${kib}" 'BEGIN {
        if (kib >= 1048576) printf "%.1f GiB", kib / 1048576;
        else if (kib >= 1024) printf "%.1f MiB", kib / 1024;
        else printf "%d KiB", kib;
    }'
}

jv_status_overall() {
    local failed_count="$1" system_state="$2"
    local network_state="$3" storage_state="$4"

    if (( failed_count > 0 )) \
        || [[ ${system_state} == degraded || ${system_state} == maintenance || ${system_state} == stopping || ${system_state} == offline ]] \
        || [[ ${network_state} == problem || ${storage_state} == problem ]]; then
        printf 'Attention needed'
        return 0
    fi

    if [[ ${system_state} == running ]]; then
        printf 'Healthy'
    else
        printf 'Unknown'
    fi
}
