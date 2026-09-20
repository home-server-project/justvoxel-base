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
common="${repo_root}/mjust/libexec/common.sh"
for section in System Minecraft Network Storage Backups Container; do
    grep -Fq "section '${section}'" "${status}" || fail "status ${section} section missing"
done

grep -Fq 'df -Pk -- "$1"' "${status}" || fail 'portable filesystem usage query missing'
if grep -Fq 'df -Pk --output=' "${status}"; then
    fail 'invalid GNU df -P/--output combination returned'
fi

grep -Fq 'jv_variant_name' "${status}" || fail 'status must use canonical variant normalization'
grep -Fq 'justvoxel-hwe' "${common}" || fail 'JustVoxel HWE variant normalization missing'
grep -Fq 'justvoxel-baremetal' "${common}" || fail 'legacy Bare Metal compatibility missing'
grep -Fq 'c_good=' "${status}" || fail 'healthy status color missing'
grep -Fq 'c_warn=' "${status}" || fail 'warning status color missing'
grep -Fq 'c_bad=' "${status}" || fail 'failure status color missing'
grep -Fq "section 'Advanced details'" "${status}" || fail 'advanced status detail section missing'
grep -Fq 'systemctl --failed --type=service' "${status}" || fail 'failed service details missing'

echo 'status dashboard regression tests passed.'
