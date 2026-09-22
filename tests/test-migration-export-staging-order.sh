#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
txn="${repo_root}/mjust/libexec/admin-migration-export-transaction-json"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

line_of() {
    local needle="$1"
    grep -nF -- "${needle}" "${txn}" | head -n1 | cut -d: -f1
}

local_preflight="$(line_of 'local_available="$(jv_migration_available_bytes "$data_parent")"')"
downtime_start="$(line_of '# COLD EXPORT DOWNTIME STARTS HERE.')"
backend_local="$(line_of 'migration-export-backend "$staged_bundle"')"
downtime_end="$(line_of '# COLD EXPORT DOWNTIME ENDS HERE.')"
final_transfer="$(line_of '# FINAL TARGET TRANSFER STARTS HERE.')"
copy_line="$(line_of 'cp --reflink=auto --sparse=always -- "$staged_bundle" "$destination.partial"')"
final_verify="$(line_of 'migration-archive verify-native "$destination.partial"')"

for value in "${local_preflight}" "${downtime_start}" "${backend_local}" "${downtime_end}" "${final_transfer}" "${copy_line}" "${final_verify}"; do
    [[ ${value} =~ ^[0-9]+$ ]] || fail 'Export staging order marker is missing'
done

(( local_preflight < downtime_start )) || fail 'Local staging-space preflight must happen before Minecraft downtime'
(( downtime_start < backend_local && backend_local < downtime_end )) || fail 'Cold bundle creation must stay inside the downtime window'
(( downtime_end < final_transfer && final_transfer < copy_line && copy_line < final_verify )) || fail 'Final target copy/verification must happen after Minecraft downtime ends'

if grep -Fq 'migration-export-backend "$destination"' "${txn}"; then
    fail 'Agent export backend still writes the cold bundle directly to the final target'
fi
grep -Fq 'systemctl start minecraft.service' "${txn}" || fail 'Export transaction no longer restarts Minecraft after cold capture'
grep -Fq 'restore-runtime-validate' "${txn}" || fail 'Export transaction no longer validates Minecraft before final transfer'
grep -Fq 'staged_size=' "${txn}" || fail 'Export transaction does not measure the completed local bundle before transfer'
grep -Fq 'jv_migration_available_bytes "$target_root"' "${txn}" || fail 'Export target free space is not rechecked before final transfer'
grep -Fq 'rm -f -- "$staged_bundle"' "${txn}" || fail 'Successful export does not clean local staging'

echo 'migration export local-staging order checks passed.'
