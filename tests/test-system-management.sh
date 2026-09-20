#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/os-common.sh"

fail(){ echo "FAIL: $*" >&2; exit 1; }

fixture='{"apiVersion":"org.containers.bootc/v1","status":{"booted":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"10","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"staged":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"11","imageDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},"rollback":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"9","imageDigest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},"readOnly":false}}'
jv_bootc_validate_status_json "${fixture}" || fail 'valid bootc v1 fixture rejected'
[[ $(jv_bootc_entry_version "${fixture}" booted) == 10 ]] || fail 'booted version parse failed'
[[ $(jv_bootc_entry_version "${fixture}" staged) == 11 ]] || fail 'staged version parse failed'
jv_bootc_staged_exists "${fixture}" || fail 'staged deployment not detected'
jv_bootc_rollback_exists "${fixture}" || fail 'rollback deployment not detected'
[[ $(jv_bootc_short_state "${fixture}") == 'Update staged - reboot to use it' ]] || fail 'short staged state wrong'

os_status="${repo_root}/mjust/libexec/os-status"
os_update="${repo_root}/mjust/libexec/os-update"
power="${repo_root}/mjust/libexec/system-power"
firmware="${repo_root}/mjust/libexec/firmware"
menu="${repo_root}/mjust/libexec/menu"
justfile="${repo_root}/mjust/justfile"
service="${repo_root}/mjust/libexec/service"
management_unit="${repo_root}/system_files/usr/lib/systemd/system/justvoxel-management.service"

grep -Fq 'bootc upgrade --check' "${os_update}" || fail 'os-update must check first'
grep -Eq '^[[:space:]]*bootc upgrade[[:space:]]*$' "${os_update}" || fail 'os-update must stage with ordinary bootc upgrade'
if grep -Eq -- '--download-only|--apply' "${os_update}"; then fail 'os-update must not use download-only/apply'; fi
grep -Fq 'does not reboot the appliance' "${os_update}" || fail 'os-update must state non-disruptive behavior'
grep -Fq 'JustVoxel operating system' "${os_status}" || fail 'OS status summary missing'

grep -Fq 'ProtectSystem=true' "${management_unit}" || fail 'management service must keep /etc writable for PAM system password changes'
if grep -Eq '^ProtectSystem=(full|strict)$' "${management_unit}"; then
    fail 'management service must not make /etc read-only while PAM system password changes are supported'
fi

grep -Fq 'jv_player_check_before_interrupt' "${service}" || fail 'Minecraft service control does not share interruption safety'
grep -Fq 'systemctl reboot' "${power}" || fail 'reboot action missing'
grep -Fq 'systemctl poweroff' "${power}" || fail 'poweroff action missing'
grep -Fq 'systemctl reboot --firmware-setup' "${firmware}" || fail 'firmware reboot action missing'
grep -Fq 'Firmware setup is available on JustVoxel HWE only.' "${firmware}" || fail 'VM firmware refusal missing'
grep -Fq 'jv_variant_is_hwe' "${firmware}" || fail 'firmware must use canonical HWE detection'
grep -Fq 'jv_variant_is_hwe' "${menu}" || fail 'System menu must use canonical HWE detection'

# Direct recipes remain authoritative even though the normal menu presents one
# combined user workflow for OS status and updates.
for recipe in os-status os-update reboot poweroff firmware password-reset; do
    grep -Fq "${recipe}:" "${justfile}" || fail "missing recipe: ${recipe}"
done

grep -Fq 'system)' "${menu}" || fail 'System menu dispatch missing'
grep -Fq "'System status & updates'" "${menu}" || fail 'combined System status/update entry missing'
grep -Fq "jui_choose 'System status & updates' 'Check for updates' 'Back'" "${menu}" || fail 'combined System status/update submenu missing'
grep -Fq '/usr/bin/mjust os-status' "${menu}" || fail 'combined System view does not show OS status'
grep -Fq '/usr/bin/mjust os-update' "${menu}" || fail 'combined System view does not expose update check'
if grep -Fq "'Operating system status'" "${menu}" || grep -Fq "'Check / download OS update'" "${menu}"; then
    fail 'obsolete duplicate System entries remain in the normal menu'
fi

echo 'system-management regression tests passed.'
