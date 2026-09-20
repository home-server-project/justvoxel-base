#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
api_client="${repo_root}/mjust/libexec/api-client"
authorization="${repo_root}/management/cmd/justvoxel-management-agent/authorization.go"
identity="${repo_root}/management/cmd/justvoxel-management-agent/identity.go"
main="${repo_root}/management/cmd/justvoxel-management-agent/main.go"
players="${repo_root}/mjust/libexec/players"
service="${repo_root}/mjust/libexec/service"
status="${repo_root}/mjust/libexec/status"
whitelist="${repo_root}/mjust/libexec/whitelist"
whitelist_backend="${repo_root}/mjust/libexec/whitelist-backend"
backup="${repo_root}/mjust/libexec/backup"
logs="${repo_root}/mjust/libexec/logs"
configure="${repo_root}/mjust/libexec/configure"
configure_max="${repo_root}/mjust/libexec/configure-max-players"
configuration_api="${repo_root}/mjust/libexec/configuration-api.sh"
backup_storage="${repo_root}/mjust/libexec/backup-storage"
backup_storage_api="${repo_root}/mjust/libexec/backup-storage-api.sh"
storage_provision="${repo_root}/mjust/libexec/storage-provision"
admin_backup_storage="${repo_root}/management/cmd/justvoxel-management-agent/admin_backup_storage.go"
admin_storage_provision="${repo_root}/management/cmd/justvoxel-management-agent/admin_storage_provision.go"
admin_configuration="${repo_root}/management/cmd/justvoxel-management-agent/admin_configuration.go"
admin_discovery="${repo_root}/management/cmd/justvoxel-management-agent/admin_discovery.go"
operator_surfaces="${repo_root}/management/cmd/justvoxel-management-agent/operator_surfaces.go"
justfile="${repo_root}/mjust/justfile"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

for file in "${api_client}" "${authorization}" "${identity}" "${main}" "${players}" "${service}" "${status}" "${whitelist}" "${whitelist_backend}" "${backup}" "${logs}" "${configure}" "${configure_max}" "${configuration_api}" "${backup_storage}" "${backup_storage_api}" "${storage_provision}" "${admin_backup_storage}" "${admin_storage_provision}" "${admin_configuration}" "${admin_discovery}" "${operator_surfaces}" "${justfile}"; do
    [[ -f ${file} ]] || fail "missing mJust Management API file: ${file}"
done

grep -Fq 'authSourceLocalRoot authSource = "local-root"' "${identity}" || fail 'local-root auth source is missing'
grep -Fq 'func localRootAdministrator' "${authorization}" || fail 'local-root principal helper is missing'
grep -Fq 'uid != 0' "${authorization}" || fail 'local-root principal is not restricted to uid 0'
grep -Fq 'if sess, ok := localRootAdministrator(r); ok {' "${main}" || fail 'root-peer authorization is not wired into the Agent'

grep -Fq 'EUID != 0' "${api_client}" || fail 'mJust API client does not require root'
grep -Fq -- '--unix-socket' "${api_client}" || fail 'mJust API client does not use the Unix socket'
grep -Fq 'JV_MANAGEMENT_SOCKET' "${api_client}" || fail 'mJust API client socket contract is missing'
grep -Fq -- '--data-binary @-' "${api_client}" || fail 'mJust API request bodies must come from stdin'
if grep -Fq 'Authorization:' "${api_client}"; then
    fail 'mJust API client must not introduce a persistent bearer token'
fi

grep -Fq '"${api_client}" GET /v1/players' "${players}" || fail 'mJust Players does not use the Management API'
grep -Fq 'players:' "${justfile}" || fail 'mJust Players recipe is missing'
grep -Fq 'sudo /usr/libexec/justvoxel/mjust/players' "${justfile}" || fail 'mJust Players must enter the API path through sudo/root'
for forbidden in 'systemctl ' 'podman ' 'rcon-cli'; do
    if grep -Fq "${forbidden}" "${players}"; then
        fail "mJust Players still performs direct system access: ${forbidden}"
    fi
done

grep -Fq 'start|stop|restart)' "${service}" || fail 'mJust service action allowlist is missing'
grep -Fq 'POST "/v1/minecraft/${action}"' "${service}" || fail 'mJust service does not use the Minecraft Management API'
for action in start stop restart; do
    grep -Fq "${action}:" "${justfile}" || fail "mJust ${action} recipe is missing"
    grep -Fq "sudo /usr/libexec/justvoxel/mjust/service ${action}" "${justfile}" || fail "mJust ${action} must enter the API path through sudo/root"
done
grep -Fq '.confirmation_required // false' "${service}" || fail 'mJust service frontend does not handle player confirmation responses'
grep -Fq '"confirm_players":%s' "${service}" || fail 'mJust service frontend does not send explicit player confirmation'
for forbidden in 'systemctl ' 'podman ' 'rcon-cli' 'jv_player_check_before_interrupt'; do
    if grep -Fq "${forbidden}" "${service}"; then
        fail "mJust service frontend still performs direct system/safety access: ${forbidden}"
    fi
done

grep -Fq 'path=/v1/status' "${status}" || fail 'mJust status does not use the Management API'
grep -Fq "path='/v1/status?details=1'" "${status}" || fail 'mJust detailed status does not use the Management API'
grep -Fq 'sudo /usr/libexec/justvoxel/mjust/status' "${justfile}" || fail 'mJust status must enter the API path through sudo/root'
grep -Fq 'r.URL.Query().Get("details") == "1"' "${main}" || fail 'Management API does not expose bounded detailed status'
for forbidden in 'systemctl ' 'podman exec' 'podman stats' 'podman ps' 'podman container' 'rcon-cli' 'findmnt ' 'df -' 'ip -4 ' 'resolvectl '; do
    if grep -Fq "${forbidden}" "${status}"; then
        fail "mJust status still performs direct system inspection: ${forbidden}"
    fi
done

grep -Fq '"${api_client}" GET "${path}"' "${whitelist}" || fail 'mJust whitelist GET helper does not use the Management API client'
grep -Fq 'api_get /v1/whitelist' "${whitelist}" || fail 'mJust whitelist list does not use the Management API'
grep -Fq 'POST /v1/whitelist --data' "${whitelist}" || fail 'mJust whitelist changes do not use the Management API'
grep -Fq 'api_get /v1/status' "${whitelist}" || fail 'mJust Bedrock availability check does not use the Management API'
grep -Fq 'whitelist-backend' "${operator_surfaces}" || fail 'Management Agent does not use the whitelist backend helper'
for forbidden in 'systemctl ' 'podman ' 'rcon-cli' 'source "${JV_LIBEXEC_DIR}/common.sh"' 'require_config'; do
    if grep -Fq "${forbidden}" "${whitelist}"; then
        fail "mJust whitelist frontend still performs direct backend work: ${forbidden}"
    fi
done
grep -Fq 'podman exec minecraft rcon-cli' "${whitelist_backend}" || fail 'whitelist backend lost authoritative RCON implementation'

grep -Fq '"${api_client}" POST /v1/backups/manual' "${backup}" || fail 'mJust manual backup does not use the Management API'
grep -Fq 'sudo /usr/libexec/justvoxel/mjust/backup' "${justfile}" || fail 'mJust backup recipe does not use the API frontend'
for forbidden in 'systemctl ' '/usr/libexec/justvoxel/minecraft-backup' 'flock ' 'tar '; do
    if grep -Fq "${forbidden}" "${backup}"; then
        fail "mJust backup frontend still performs direct backup work: ${forbidden}"
    fi
done

grep -Fq "'/v1/logs/minecraft?limit=100&format=cat'" "${logs}" || fail 'normal mJust logs do not use the Management API'
grep -Fq 'if [[ ${mode} == advanced ]]' "${logs}" || fail 'advanced logs branch is missing'
grep -Fq 'journalctl "${journal_args[@]}" -f' "${logs}" || fail 'advanced logs no longer provide the direct systemd follow view'
if grep -Fq -- '-o cat' "${logs}"; then
    fail 'normal mJust logs still contain the old direct journalctl -o cat path'
fi
grep -Fq 'outputMode := "short-iso"' "${operator_surfaces}" || fail 'WebUI/default Minecraft log format changed'
grep -Fq 'case "cat":' "${operator_surfaces}" || fail 'Management API message-only log format is missing'
grep -Fq 'unsupported Minecraft log format' "${operator_surfaces}" || fail 'Management API log format allowlist is missing'

grep -Fq 'GET /v1/admin/configuration' "${configuration_api}" || fail 'mJust configuration does not read current state through the Management API'
grep -Fq 'jv_config_post /v1/admin/configuration/plan' "${configuration_api}" || fail 'mJust configuration does not validate changes through the Management API'
grep -Fq 'jv_config_post /v1/admin/configuration/apply' "${configuration_api}" || fail 'mJust configuration does not apply changes through the Management API'
grep -Fq '"${JV_CONFIG_API_CLIENT}" POST "${path}" --data' "${configuration_api}" || fail 'mJust configuration POST helper does not use the Management API client'
grep -Fq 'GET /v1/status' "${configuration_api}" || fail 'mJust configuration guidance does not use API status'
grep -Fq '.confirmation_required // false' "${configuration_api}" || fail 'mJust configuration does not handle player restart confirmation'
grep -Fq 'configuration-api.sh' "${configure}" || fail 'main mJust configure frontend does not use the configuration API helper'
grep -Fq 'configuration-api.sh' "${configure_max}" || fail 'maximum-player frontend does not use the configuration API helper'
grep -Fq 'jv_config_apply_payload' "${configure}" || fail 'main mJust configure frontend does not apply through the Agent'
grep -Fq 'jv_config_apply_payload' "${configure_max}" || fail 'maximum-player frontend does not apply through the Agent'
grep -Fq 'registerAdminConfigurationRoutes' "${admin_configuration}" || fail 'Management Agent configuration routes are missing'
grep -Fq 'GET /v1/admin/configuration' "${admin_discovery}" || fail 'Management Agent configuration discovery route is missing'

for frontend in "${configure}" "${configure_max}"; do
    for forbidden in 'write_main_config' 'render_runtime' 'update_firewall_ports' 'systemctl ' 'require_config' '/etc/justvoxel' '/proc/'; do
        if grep -Fq "${forbidden}" "${frontend}"; then
            fail "mJust configuration frontend still performs direct backend work: ${forbidden}"
        fi
    done
done
if grep -Fq 'Authorization:' "${configuration_api}"; then
    fail 'mJust configuration API helper must not introduce a bearer token'
fi

grep -Fq 'jv_backup_storage_get /v1/admin/backup-storage' "${backup_storage_api}" || fail 'mJust backup storage status does not use the Management API'
grep -Fq 'jv_backup_storage_post /v1/admin/backup-storage/plan' "${backup_storage_api}" || fail 'mJust backup storage validation does not use the Management API'
grep -Fq 'jv_backup_storage_post /v1/admin/backup-storage/apply' "${backup_storage_api}" || fail 'mJust backup storage apply does not use the Management API'
grep -Fq 'jv_backup_storage_get /v1/admin/storage-provision' "${backup_storage_api}" || fail 'mJust advanced backup storage discovery does not use the Management API'
grep -Fq 'jv_backup_storage_post /v1/admin/storage-provision/plan' "${backup_storage_api}" || fail 'mJust advanced storage validation does not use the Management API'
grep -Fq 'jv_backup_storage_post /v1/admin/storage-provision/apply' "${backup_storage_api}" || fail 'mJust advanced storage apply does not use the Management API'
grep -Fq '"${JV_BACKUP_STORAGE_API_CLIENT}" POST "${path}" --data' "${backup_storage_api}" || fail 'mJust backup storage POST helper does not use the Management API client'
grep -Fq 'backup-storage-api.sh' "${backup_storage}" || fail 'mJust backup storage frontend does not use the API helper'
grep -Fq 'jv_backup_storage_apply_target' "${backup_storage}" || fail 'normal backup targets do not apply through the Agent'
grep -Fq 'jv_backup_storage_apply_provision' "${backup_storage}" || fail 'destructive backup provisioning does not apply through the Agent'
grep -Fq 'Type exactly:' "${backup_storage_api}" || fail 'destructive backup provisioning exact confirmation is missing'
grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage backup' "${storage_provision}" || fail 'configured backup storage menu does not delegate to the API frontend'
grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage "${mode}"' "${storage_provision}" || fail 'configured local backup provisioning does not delegate to the API frontend'
grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage network' "${storage_provision}" || fail 'configured network backup storage does not delegate to the API frontend'
grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage system' "${storage_provision}" || fail 'configured system backup storage does not delegate to the API frontend'
grep -Fq 'migrate_data()' "${storage_provision}" || fail 'Minecraft data migration path was unexpectedly removed'
grep -Fq 'registerAdminBackupStorageRoutes' "${admin_backup_storage}" || fail 'Management Agent backup storage routes are missing'
grep -Fq 'registerAdminStorageProvisionRoutes' "${admin_storage_provision}" || fail 'Management Agent storage provisioning routes are missing'

for forbidden in 'lsblk ' 'findmnt ' 'umount ' 'mkfs' 'parted ' 'wipefs ' 'write_main_config' 'render_runtime' 'systemctl ' '/etc/fstab' '/proc/'; do
    if grep -Fq "${forbidden}" "${backup_storage}"; then
        fail "mJust backup storage frontend still performs direct backend work: ${forbidden}"
    fi
done
if grep -Eq '^[[:space:]]*(if[[:space:]]+!)?[[:space:]]*mount[[:space:]]' "${backup_storage}"; then
    fail 'mJust backup storage frontend still performs a direct mount command'
fi
if grep -Fq 'Authorization:' "${backup_storage_api}"; then
    fail 'mJust backup storage API helper must not introduce a bearer token'
fi

echo 'mJust Management API foundation, Players, Minecraft control, Status, Whitelist, Manual Backup, Logs, Configuration, and Backup Storage migration checks passed.'
