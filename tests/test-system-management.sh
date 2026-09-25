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
system_update_api="${repo_root}/mjust/libexec/system-update-api.sh"
system_actions_backend="${repo_root}/mjust/libexec/admin-system-actions-json"
system_actions_agent="${repo_root}/management/cmd/justvoxel-management-agent/admin_system_actions.go"
system_update_agent="${repo_root}/management/cmd/justvoxel-management-agent/admin_system_updates.go"
system_update_reboot_agent="${repo_root}/management/cmd/justvoxel-management-agent/admin_system_update_reboot.go"
system_update_reboot_helper="${repo_root}/mjust/libexec/admin-system-update-reboot-json"
system_update_reboot_worker="${repo_root}/runtime/system-update-reboot-worker"
interrupt_safety="${repo_root}/mjust/libexec/interrupt-safety.sh"
build_common="${repo_root}/build_files/build-common.sh"
management_main="${repo_root}/management/cmd/justvoxel-management-agent/main.go"
menu="${repo_root}/mjust/libexec/menu"
justfile="${repo_root}/mjust/justfile"
service="${repo_root}/mjust/libexec/service"
management_unit="${repo_root}/system_files/usr/lib/systemd/system/justvoxel-management.service"

grep -Fq 'system-update-api.sh' "${os_status}" || fail 'os-status must use the System Update API helper'
grep -Fq 'jv_system_update_get' "${os_status}" || fail 'os-status must read status through the Management API'
grep -Fq 'system-update-api.sh' "${os_update}" || fail 'os-update must use the System Update API helper'
grep -Fq 'jv_system_update_apply' "${os_update}" || fail 'os-update must update through the Management API'
for frontend in "${os_status}" "${os_update}"; do
    if grep -Eq 'bootc[[:space:]]+(status|upgrade)' "${frontend}"; then fail 'OS update frontend must not execute bootc directly'; fi
    if grep -Fq 'os-common.sh' "${frontend}"; then fail 'OS update frontend must not parse bootc state directly'; fi
done
grep -Fq '"${JV_SYSTEM_UPDATE_API_CLIENT}" GET /v1/admin/system/updates' "${system_update_api}" || fail 'System Update frontend status endpoint missing'
grep -Fq '"${JV_SYSTEM_UPDATE_API_CLIENT}" POST /v1/admin/system/updates' "${system_update_api}" || fail 'System Update frontend update endpoint missing'
grep -Fq 'GET /v1/admin/system/updates' "${system_update_agent}" || fail 'System Update status route missing'
grep -Fq 'POST /v1/admin/system/updates/check' "${system_update_agent}" || fail 'System Update fresh-check route missing'
grep -Fq 'POST /v1/admin/system/updates' "${system_update_agent}" || fail 'System Update apply route missing'
grep -Fq 'exec.CommandContext(ctx, "bootc", "status", "--json", "--format-version=1")' "${system_update_agent}" || fail 'System Update backend bootc status command missing'
grep -Fq 'exec.CommandContext(ctx, "bootc", "upgrade", "--check")' "${system_update_agent}" || fail 'System Update backend fresh registry check missing'
grep -Fq 'exec.CommandContext(ctx, "bootc", "upgrade")' "${system_update_agent}" || fail 'System Update backend ordinary bootc upgrade command missing'
if grep -Eq -- '--download-only|--from-downloaded|--apply' "${system_update_agent}"; then fail 'System Update backend contains an unwanted bootc download/apply mode'; fi
grep -Fq 'JustVoxel operating system' "${os_status}" || fail 'OS status summary missing'

grep -Fq 'ProtectSystem=true' "${management_unit}" || fail 'management service must keep /etc writable for PAM system password changes'
if grep -Fqx 'ProtectSystem=full' "${management_unit}" || grep -Fqx 'ProtectSystem=strict' "${management_unit}"; then
    fail 'management service must not make /etc read-only while PAM system password changes are supported'
fi
grep -Fqx 'RestrictSUIDSGID=no' "${management_unit}" || fail 'management service must allow bootc openat2 authfile lookup'
if grep -Fqx 'RestrictSUIDSGID=yes' "${management_unit}"; then
    fail 'RestrictSUIDSGID=yes breaks bootc openat2 authfile lookup with ENOSYS'
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
grep -Fq 'registerAdminSystemUpdateRoutes(mux, s)' "${management_main}" || fail 'System Update routes are not registered'
grep -Fq 'registerAdminSystemUpdateRebootRoutes(mux, s)' "${management_main}" || fail 'System Update reboot routes are not registered'
grep -Fq 'GET /v1/admin/system/update-reboot' "${system_update_reboot_agent}" || fail 'System Update reboot status route missing'
grep -Fq 'POST /v1/admin/system/update-reboot' "${system_update_reboot_agent}" || fail 'System Update reboot request route missing'
grep -Fq 'WriteTimeout:      20 * time.Minute' "${management_main}" || fail 'Management API write timeout is too short for system update pulls'

grep -Fq '"${JV_SYSTEM_ACTIONS_API_CLIENT}" GET /v1/admin/system/actions' "${system_actions_api}" || fail 'System Actions frontend status endpoint missing'
grep -Fq 'POST "/v1/admin/system/${action}" --data' "${system_actions_api}" || fail 'System Actions frontend apply endpoint missing'
grep -Fq 'reboot|poweroff|firmware-reboot)' "${system_actions_api}" || fail 'System Actions frontend action allowlist missing'
grep -Fq 'action_confirmed:true' "${system_actions_api}" || fail 'System Actions frontend explicit action confirmation missing'
grep -Fq 'confirm_players:$players_confirmed' "${system_actions_api}" || fail 'System Actions player confirmation replay missing'

grep -Fq 'jv_stop_minecraft_adaptive' "${system_actions_backend}" || fail 'System Actions backend does not use adaptive Minecraft shutdown'
grep -Fq 'systemctl reboot --firmware-setup --dry-run' "${system_actions_backend}" || fail 'System Actions backend lost firmware capability validation'
grep -Fq 'systemd-run --quiet --collect --unit=justvoxel-reboot --on-active=2s /usr/bin/systemctl reboot' "${system_actions_backend}" || fail 'System reboot backend action missing'
grep -Fq 'systemd-run --quiet --collect --unit=justvoxel-poweroff --on-active=2s /usr/bin/systemctl poweroff' "${system_actions_backend}" || fail 'System poweroff backend action missing'
grep -Fq 'systemd-run --quiet --collect --unit=justvoxel-firmware-reboot --on-active=2s /usr/bin/systemctl reboot --firmware-setup' "${system_actions_backend}" || fail 'Firmware reboot backend action missing'
grep -Fq 'case "${action}" in' "${system_actions_backend}" || fail 'System Actions backend action allowlist missing'
if grep -Eq -- '--backup-minecraft|--warning-seconds' "${system_actions_backend}"; then
    fail 'Existing Control Center power backend must remain on the normal reboot behavior'
fi

grep -Fq 'JV_INTERRUPT_WARNING_SECONDS:-60' "${interrupt_safety}" || fail 'Normal player warning must remain 60 seconds by default'
grep -Fq '[[ ${warning_seconds} == 10 ]]' "${interrupt_safety}" || fail 'Quick reboot 10-second warning path missing'
grep -Fq -- '--backup-minecraft' "${system_update_reboot_helper}" || fail 'Update reboot helper backup option missing'
grep -Fq -- '--warning-seconds=10' "${system_update_reboot_helper}" || fail 'Update reboot helper quick-warning option missing'
grep -Fq 'systemd-run --quiet --collect --unit="${unit_name}"' "${system_update_reboot_helper}" || fail 'Update reboot helper must queue the detached worker'
grep -Fq 'state:"failed",accepted:false,confirmation_required:false,reason:"action_failed"' "${system_update_reboot_helper}" || fail 'Update reboot helper must persist worker-start failure state'
grep -Fq 'minecraft-backup --leave-stopped' "${system_update_reboot_worker}" || fail 'Update reboot worker must use the existing verified cold backup'
grep -Fq 'backup_failed' "${system_update_reboot_worker}" || fail 'Update reboot worker must report backup failure'
grep -Fq 'restart_minecraft_if_needed' "${system_update_reboot_worker}" || fail 'Update reboot worker must recover Minecraft after failure'
grep -Fq 'systemctl reboot' "${system_update_reboot_worker}" || fail 'Update reboot worker reboot action missing'
grep -Fq 'system-update-reboot-worker /usr/libexec/justvoxel/system-update-reboot-worker' "${build_common}" || fail 'Update reboot worker is not installed in the Base image'

grep -Fq 'Firmware setup is available on JustVoxel HWS only.' "${firmware}" || fail 'VM firmware refusal missing'
grep -Fq 'jv_variant_is_hws' "${menu}" || fail 'System menu must use canonical HWS detection'

# Direct recipes remain supported entry points even though the normal menu
# presents one combined user workflow for OS status and updates.
for recipe in os-status os-update reboot poweroff firmware password-reset; do
    grep -Fq "${recipe}:" "${justfile}" || fail "missing recipe: ${recipe}"
done

grep -Fq 'system)' "${menu}" || fail 'System menu dispatch missing'
grep -Fq "'System status & updates'" "${menu}" || fail 'combined System status/update entry missing'
grep -Fq "jui_choose 'System status & updates' 'Update system' 'Back'" "${menu}" || fail 'combined System status/update submenu missing'
grep -Fq '/usr/bin/mjust os-status' "${menu}" || fail 'combined System view does not show OS status'
grep -Fq '/usr/bin/mjust os-update' "${menu}" || fail 'combined System view does not expose update check'
if grep -Fq "'Operating system status'" "${menu}" || grep -Fq "'Check / download OS update'" "${menu}"; then
    fail 'obsolete duplicate System entries remain in the normal menu'
fi

echo 'system-management regression tests passed.'
