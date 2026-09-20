#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/status-lib.sh"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

[[ $(jv_status_format_duration 90) == '1m' ]] || fail 'duration formatter should use minutes below one hour'
[[ $(jv_status_format_duration 3660) == '1h 1m' ]] || fail 'duration formatter should include hours and minutes'
[[ $(jv_status_format_duration 90061) == '1d 1h 1m' ]] || fail 'duration formatter should include days, hours and minutes'

[[ $(jv_status_overall Running 0 running healthy healthy healthy) == 'Healthy' ]] || fail 'healthy appliance state should report Healthy'
[[ $(jv_status_overall Stopped 0 running healthy healthy healthy) == 'Healthy' ]] || fail 'deliberately stopped Minecraft must not make appliance unhealthy'
[[ $(jv_status_overall Failed 0 running healthy healthy healthy) == 'Attention needed' ]] || fail 'failed Minecraft service must need attention'
[[ $(jv_status_overall Running 2 degraded healthy healthy healthy) == 'Attention needed' ]] || fail 'failed system services must need attention'
[[ $(jv_status_overall Running 0 running problem healthy healthy) == 'Attention needed' ]] || fail 'missing basic network connectivity must need attention'
[[ $(jv_status_overall Running 0 running healthy problem healthy) == 'Attention needed' ]] || fail 'unavailable configured storage must need attention'
[[ $(jv_status_overall Running 0 running healthy healthy problem) == 'Attention needed' ]] || fail 'configured backup timer failure must need attention'
[[ $(jv_status_overall Running 0 unknown healthy healthy healthy) == 'Unknown' ]] || fail 'unknown system state should remain Unknown'

status="${repo_root}/mjust/libexec/status"
collector="${repo_root}/mjust/libexec/web-status-json"
common="${repo_root}/mjust/libexec/common.sh"
for section in System Minecraft Network Storage Backups Container; do
    grep -Fq "section '${section}'" "${status}" || fail "status ${section} section missing"
done

grep -Fq '"${api_client}" GET "${path}"' "${status}" || fail 'mJust status must use the Management API'
grep -Fq "path='/v1/status?details=1'" "${status}" || fail 'mJust detailed status must use the Management API'
grep -Fq 'extended.system.hostname' "${status}" || fail 'mJust status does not consume extended system status'
grep -Fq 'extended.storage.system' "${status}" || fail 'mJust status does not consume extended storage status'

for forbidden in 'systemctl ' 'podman ' 'rcon-cli' 'findmnt ' 'df -' 'ip -4 ' 'resolvectl '; do
    if grep -Fq "${forbidden}" "${status}"; then
        fail "mJust status still performs direct system inspection: ${forbidden}"
    fi
done

grep -Fq 'df -Pk -- /var' "${collector}" || fail 'Agent status collector is missing portable filesystem usage'
if grep -Fq 'df -Pk --output=' "${collector}"; then
    fail 'invalid GNU df -P/--output combination returned'
fi

grep -Fq 'jv_variant_name' "${collector}" || fail 'Agent status collector must use canonical variant normalization'
grep -Fq 'justvoxel-hwe' "${common}" || fail 'JustVoxel HWE variant normalization missing'
grep -Fq 'justvoxel-baremetal' "${common}" || fail 'legacy Bare Metal compatibility missing'
grep -Fq 'c_good=' "${status}" || fail 'healthy status color missing'
grep -Fq 'c_warn=' "${status}" || fail 'warning status color missing'
grep -Fq 'c_bad=' "${status}" || fail 'failure status color missing'
grep -Fq "section 'Advanced details'" "${status}" || fail 'advanced status detail section missing'
grep -Fq 'details_failed=' "${collector}" || fail 'Agent detailed failed-service status is missing'
grep -Fq 'details_systemd=' "${collector}" || fail 'Agent detailed Minecraft service status is missing'

echo 'status dashboard API migration regression tests passed.'
