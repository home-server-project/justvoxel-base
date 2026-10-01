#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ups="${repo_root}/mjust/libexec/ups"
menu="${repo_root}/mjust/libexec/menu"
justfile="${repo_root}/mjust/justfile"

fail() { echo "FAIL: $*" >&2; exit 1; }

[[ -f ${ups} ]] || fail 'UPS command is missing'
grep -Fq 'ups:' "${justfile}" || fail 'direct mjust ups command is missing'
grep -Fq "'UPS') /usr/bin/mjust ups || true ;;" "${menu}" || fail 'System UPS entry is missing'
grep -Fq '/usr/libexec/justvoxel/mjust/api-client' "${ups}" || fail 'UPS must use the shared Management API client'

for route in '/v1/ups' '/v1/admin/ups/source' '/v1/admin/ups/shutdown' '/v1/admin/ups/sharing' '/v1/admin/ups/source/forget'; do
    grep -Fq "${route}" "${ups}" || fail "UPS API route is missing: ${route}"
done

for field in 'source.mode' 'source.host' 'source.port' 'source.ups_name' 'nut_mode' 'protection_enabled' 'shutdown_delay_seconds' 'sharing_enabled' 'monitor_credentials_configured' 'sharing_credentials_configured' 'monitor_service_active' 'server_service_active' 'driver_service_active' 'battery_charge' 'battery_runtime_seconds' 'load_percent'; do
    grep -Fq "${field}" "${ups}" || fail "UPS status field is missing: ${field}"
done

grep -Fq "jui_confirm 'Forget the saved UPS source" "${ups}" || fail 'forget source must require confirmation'
grep -Fq 'read -r -s value </dev/tty' "${ups}" || fail 'UPS passwords must be entered without echo'
grep -Fq 'password}" | jq -Rs' "${ups}" || fail 'UPS passwords must enter JSON through stdin'
if grep -Eq -- 'systemctl|/etc/ups|--arg(password|json)[[:space:]]+("?\$\{?password)' "${ups}"; then
    fail 'UPS client must not manage NUT or place a password in command arguments'
fi

echo 'mjust UPS source checks passed.'
