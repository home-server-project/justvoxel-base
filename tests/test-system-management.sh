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
system_actions_api="${repo_root}/mjust/libexec/system-actions-api.sh"
system_actions_backend="${repo_root}/mjust/libexec/admin-system-actions-json"
system_actions_agent="${repo_root}/management/cmd/justvoxel-management-agent/admin_system_actions.go"
management_main="${repo_root}/management/cmd/justvoxel-management-agent/main.go"
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

grep -Fq '/v1/minecraft/' "${service}" || fail 'Minecraft service control must use the Management API'
if grep -Eq 'systemctl |podman |rcon-cli|jv_player_check_before_interrupt' "${service}"; then
    fail 'Minecraft service frontend must not perform direct system or player-safety operations'
fi
grep -Fq 'jv_stop_minecraft_adaptive' "${repo_root}/mjust/libexec/web-status-json" || fail 'Minecraft API backend does not use adaptive shutdown'
grep -Fq 'jv_stop_minecraft_adaptive' "${repo_root}/runtime/minecraft-backup" || fail 'cold backup does not use adaptive shutdown'
grep -Fq '/usr/libexec/justvoxel/mjust/service stop' "${repo_root}/mjust/libexec/start-over" || fail 'Start Over does not use the shared Minecraft stop path'
grep -Fq 'system-actions-api.sh' "${power}" || fail 'system power frontend does not use the System Actions API helper'
grep -Fq 'system-actions-api.sh' "${firmware}" || fail 'firmware frontend does not use the System Actions API helper'
grep -Fq 'jv_system_actions_get' "${power}" || fail 'system power frontend does not read Agent capabilities'
grep -Fq 'jv_system_actions_apply' "${power}" || fail 'system power frontend does not apply through the Agent'
grep -Fq 'jv_system_actions_get' "${firmware}" || fail 'firmware frontend does not read Agent capabilities'
grep -Fq 'jv_system_actions_apply firmware-reboot' "${firmware}" || fail 'firmware frontend does not apply through the Agent'
for frontend in "${power}" "${firmware}"; do
    for forbidden in 'systemctl ' 'systemd-run ' 'podman ' 'rcon-cli' 'bootc ' 'jv_stop_minecraft_' '/sys/firmware' '/sys/class/drm'; do
        if grep -Fq "${forbidden}" "${frontend}"; then
            fail "System action frontend still performs direct host/safety work: ${forbidden}"
        fi
    done
done

grep -Fq 'GET /v1/admin/system/actions' "${system_actions_agent}" || fail 'System Actions status route missing'
grep -Fq 'POST /v1/admin/system/reboot' "${system_actions_agent}" || fail 'System reboot API route missing'
grep -Fq 'POST /v1/admin/system/poweroff' "${system_actions_agent}" || fail 'System poweroff API route missing'
grep -Fq 'POST /v1/admin/system/firmware-reboot' "${system_actions_agent}" || fail 'Firmware reboot API route missing'
grep -Fq 'registerAdminSystemActionRoutes(mux, s)' "${management_main}" || fail 'System Actions routes are not registered'
grep -Fq 'WriteTimeout:      210 * time.Second' "${management_main}" || fail 'Management API write timeout is too short for player countdown actions'

grep -Fq 'jv_system_actions_get /v1/admin/system/actions' "${system_actions_api}" || fail 'System Actions frontend status endpoint missing'
grep -Fq 'POST "/v1/admin/system/${action}" --data' "${system_actions_api}" || fail 'System Actions frontend apply endpoint missing'
grep -Fq 'action_confirmed:true' "${system_actions_api}" || fail 'System Actions frontend explicit action confirmation missing'
grep -Fq 'confirm_players:$players_confirmed' "${system_actions_api}" || fail 'System Actions player confirmation replay missing'

grep -Fq 'jv_stop_minecraft_adaptive' "${system_actions_backend}" || fail 'System Actions backend does not use adaptive Minecraft shutdown'
grep -Fq 'systemctl reboot --firmware-setup --dry-run' "${system_actions_backend}" || fail 'System Actions backend lost firmware capability validation'
grep -Fq 'systemd-run --quiet --collect --unit=justvoxel-reboot --on-active=2s /usr/bin/systemctl reboot' "${system_actions_backend}" || fail 'System reboot backend action missing'
grep -Fq 'systemd-run --quiet --collect --unit=justvoxel-poweroff --on-active=2s /usr/bin/systemctl poweroff' "${system_actions_backend}" || fail 'System poweroff backend action missing'
grep -Fq 'systemd-run --quiet --collect --unit=justvoxel-firmware-reboot --on-active=2s /usr/bin/systemctl reboot --firmware-setup' "${system_actions_backend}" || fail 'Firmware reboot backend action missing'
grep -Fq 'case "${action}" in' "${system_actions_backend}" || fail 'System Actions backend action allowlist missing'
grep -Fq 'Firmware setup is available on JustVoxel HWE only.' "${firmware}" || fail 'VM firmware refusal missing'
grep -Fq 'jv_variant_is_hwe' "${menu}" || fail 'System menu must use canonical HWE detection'

# Direct recipes remain supported entry points even though the normal menu
# presents one combined user workflow for OS status and updates.
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
