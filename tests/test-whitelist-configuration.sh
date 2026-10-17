#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/configuration-api.sh"
# The complete public runtime object preserves whitelist and every untouched field.
current='{"id":"jv-family1","runtime":{"whitelist":true,"enforce_whitelist":true,"max_players":10,"software":"paper","java_memory":"4G","container_memory":"6G"}}'
payload="$(jv_config_payload <<<"${current}" | jq -c '.runtime.max_players=12')"
jq -e --argjson original "${current}" '.runtime.whitelist and .runtime.enforce_whitelist and (.runtime | del(.max_players)) == ($original.runtime | del(.max_players)) and .runtime.max_players == 12' <<<"${payload}" >/dev/null
printf 'PASS: selected configuration preserves untouched runtime and whitelist settings\n'
