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
setup="${repo_root}/mjust/libexec/setup"
setup_api="${repo_root}/mjust/libexec/setup-api.sh"
restore="${repo_root}/mjust/libexec/restore"
restore_api="${repo_root}/mjust/libexec/restore-api.sh"
admin_restore="${repo_root}/management/cmd/justvoxel-management-agent/admin_restore.go"
admin_restore_apply="${repo_root}/management/cmd/justvoxel-management-agent/admin_restore_apply.go"
restore_worker="${repo_root}/management/cmd/justvoxel-management-agent/restore_worker.go"
validate="${repo_root}/mjust/libexec/validate"
validate_backend="${repo_root}/mjust/libexec/validate-backend"
storage_provision="${repo_root}/mjust/libexec/storage-provision"
storage_plan="${repo_root}/mjust/libexec/storage-plan"
storage_api="${repo_root}/mjust/libexec/storage-api.sh"
data_migration="${repo_root}/mjust/libexec/data-migration"
data_migration_api="${repo_root}/mjust/libexec/data-migration-api.sh"
data_migration_plan_backend="${repo_root}/mjust/libexec/admin-data-migration-plan-json"
data_migration_transaction_backend="${repo_root}/mjust/libexec/admin-data-migration-transaction-json"
admin_data_migration_plan="${repo_root}/management/cmd/justvoxel-management-agent/admin_data_migration_plan.go"
admin_data_migration_apply="${repo_root}/management/cmd/justvoxel-management-agent/admin_data_migration_apply.go"
data_migration_worker="${repo_root}/management/cmd/justvoxel-management-agent/data_migration_worker.go"
migration_export="${repo_root}/mjust/libexec/migration-export"
migration_api="${repo_root}/mjust/libexec/migration-api.sh"
migration_export_backend="${repo_root}/mjust/libexec/migration-export-backend"
migration_export_plan_backend="${repo_root}/mjust/libexec/admin-migration-export-plan-json"
migration_export_transaction_backend="${repo_root}/mjust/libexec/admin-migration-export-transaction-json"
admin_migration_export_plan="${repo_root}/management/cmd/justvoxel-management-agent/admin_migration_export_plan.go"
admin_migration_export_apply="${repo_root}/management/cmd/justvoxel-management-agent/admin_migration_export_apply.go"
migration_export_worker="${repo_root}/management/cmd/justvoxel-management-agent/migration_export_worker.go"
admin_backup_storage="${repo_root}/management/cmd/justvoxel-management-agent/admin_backup_storage.go"
admin_storage_provision="${repo_root}/management/cmd/justvoxel-management-agent/admin_storage_provision.go"
admin_setup_plan="${repo_root}/management/cmd/justvoxel-management-agent/admin_setup_plan.go"
admin_setup_apply="${repo_root}/management/cmd/justvoxel-management-agent/admin_setup_apply.go"
admin_operations="${repo_root}/management/cmd/justvoxel-management-agent/admin_operations.go"
admin_validation="${repo_root}/management/cmd/justvoxel-management-agent/admin_validation.go"
admin_configuration="${repo_root}/management/cmd/justvoxel-management-agent/admin_configuration.go"
admin_discovery="${repo_root}/management/cmd/justvoxel-management-agent/admin_discovery.go"
operator_surfaces="${repo_root}/management/cmd/justvoxel-management-agent/operator_surfaces.go"
justfile="${repo_root}/mjust/justfile"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

for file in "${api_client}" "${authorization}" "${identity}" "${main}" "${players}" "${service}" "${status}" "${whitelist}" "${whitelist_backend}" "${backup}" "${logs}" "${configure}" "${configure_max}" "${configuration_api}" "${backup_storage}" "${backup_storage_api}" "${setup}" "${setup_api}" "${restore}" "${restore_api}" "${admin_restore}" "${admin_restore_apply}" "${restore_worker}" "${validate}" "${validate_backend}" "${storage_provision}" "${storage_plan}" "${storage_api}" "${data_migration}" "${data_migration_api}" "${data_migration_plan_backend}" "${data_migration_transaction_backend}" "${admin_data_migration_plan}" "${admin_data_migration_apply}" "${data_migration_worker}" "${migration_export}" "${migration_api}" "${migration_export_backend}" "${migration_export_plan_backend}" "${migration_export_transaction_backend}" "${admin_migration_export_plan}" "${admin_migration_export_apply}" "${migration_export_worker}" "${admin_backup_storage}" "${admin_storage_provision}" "${admin_setup_plan}" "${admin_setup_apply}" "${admin_operations}" "${admin_validation}" "${admin_configuration}" "${admin_discovery}" "${operator_surfaces}" "${justfile}"; do
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

grep -Fq "'/v1/logs/minecraft?limit=100&format=cat'" "${logs}" || fail 'mJust logs do not use the Management API'
if grep -Fq -- '--advanced' "${logs}" || grep -Fq 'journalctl ' "${logs}"; then
    fail 'mJust logs still expose the removed direct advanced journal path'
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

grep -Fq 'storage-api.sh' "${storage_plan}" || fail 'mJust storage-plan does not use the storage API helper'
grep -Fq 'jv_storage_discovery' "${storage_plan}" || fail 'mJust storage-plan does not obtain storage discovery from the Agent'
grep -Fq 'jv_storage_status' "${storage_plan}" || fail 'mJust storage-plan does not obtain the variant through the Management API'
grep -Fq 'jv_storage_get /v1/admin/storage' "${storage_api}" || fail 'mJust storage discovery route is missing from the API helper'
grep -Fq 'jv_storage_get /v1/status' "${storage_api}" || fail 'mJust storage-plan variant status route is missing from the API helper'
grep -Fq 'GET /v1/admin/storage' "${admin_discovery}" || fail 'Management Agent storage discovery route is missing'
for forbidden in 'lsblk ' 'findmnt ' 'storage-common.sh' 'storage-common-base.sh' 'storage_system_disks' 'storage_show_devices' 'storage_is_vm' 'storage_is_hwe'; do
    if grep -Fq "${forbidden}" "${storage_plan}"; then
        fail "mJust storage-plan still performs direct host storage discovery: ${forbidden}"
    fi
done
if grep -Fq 'Authorization:' "${storage_api}"; then
    fail 'mJust storage API helper must not introduce a bearer token'
fi

grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage backup' "${storage_provision}" || fail 'configured backup storage menu does not delegate to the API frontend'
grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage "${mode}"' "${storage_provision}" || fail 'configured local backup provisioning does not delegate to the API frontend'
grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage network' "${storage_provision}" || fail 'configured network backup storage does not delegate to the API frontend'
grep -Fq '/usr/libexec/justvoxel/mjust/backup-storage system' "${storage_provision}" || fail 'configured system backup storage does not delegate to the API frontend'
grep -Fq '/usr/libexec/justvoxel/mjust/data-migration' "${storage_provision}" || fail 'storage menu does not delegate Minecraft data migration to the API frontend'
if grep -Fq 'migrate_data()' "${storage_provision}"; then
    fail 'legacy direct Minecraft data migration backend still exists in storage-provision'
fi
grep -Fq 'sudo /usr/libexec/justvoxel/mjust/data-migration' "${justfile}" || fail 'mJust storage-migrate recipe does not use the API frontend'
grep -Fq 'data-migration-api.sh' "${data_migration}" || fail 'mJust data migration frontend does not use the API helper'
grep -Fq 'jv_data_migration_current_operation' "${data_migration}" || fail 'mJust data migration cannot reconnect to an active operation'
grep -Fq 'jv_data_migration_discovery' "${data_migration}" || fail 'mJust data migration does not discover targets through the Agent'
grep -Fq 'jv_data_migration_plan' "${data_migration}" || fail 'mJust data migration does not plan through the Agent'
grep -Fq 'jv_data_migration_apply' "${data_migration}" || fail 'mJust data migration does not apply through the Agent'
grep -Fq 'jv_data_migration_monitor_operation' "${data_migration}" || fail 'mJust data migration does not monitor the persistent Agent operation'
grep -Fq 'jv_data_migration_get /v1/admin/data-migration' "${data_migration_api}" || fail 'data migration discovery API route is missing from the frontend helper'
grep -Fq 'jv_data_migration_get /v1/admin/data-migration/current-operation' "${data_migration_api}" || fail 'data migration current-operation API route is missing from the frontend helper'
grep -Fq 'jv_data_migration_post /v1/admin/data-migration/plan' "${data_migration_api}" || fail 'data migration planning API route is missing from the frontend helper'
grep -Fq 'jv_data_migration_post /v1/admin/data-migration/apply' "${data_migration_api}" || fail 'data migration apply API route is missing from the frontend helper'
grep -Fq 'registerAdminDataMigrationRoutes' "${admin_data_migration_apply}" || fail 'Management Agent data migration routes are missing'
grep -Fq 'authoritativeAdminDataMigrationPlan' "${admin_data_migration_apply}" || fail 'data migration apply does not re-run authoritative planning'
grep -Fq 'adminDataMigrationTransactionHelper' "${data_migration_worker}" || fail 'data migration worker does not use the authoritative transaction backend'
grep -Fq 'admin-data-migration-plan-json plan' "${data_migration_transaction_backend}" || fail 'data migration transaction does not revalidate the reviewed plan'
grep -Fq 'JV_MAINTENANCE_LOCK' "${data_migration_transaction_backend}" || fail 'data migration transaction lost the shared Minecraft maintenance lock'
grep -Fq 'minecraft-backup --leave-stopped' "${data_migration_transaction_backend}" || fail 'data migration transaction lost the verified pre-migration cold backup'
grep -Fq 'rsync -aHAX' "${data_migration_transaction_backend}" || fail 'data migration transaction lost copy/verification semantics'
grep -Fq 'restore-runtime-validate' "${data_migration_transaction_backend}" || fail 'data migration transaction lost runtime validation'
for forbidden in 'systemctl ' 'podman ' 'rcon-cli' 'rsync ' 'flock ' 'mkfs' 'parted ' 'wipefs ' 'write_main_config' 'render_runtime' 'apply_data_selinux' 'storage-common.sh' 'storage_prepare_' 'lsblk ' 'findmnt ' '/etc/fstab' 'JV_MAINTENANCE_LOCK'; do
    if grep -Fq "${forbidden}" "${data_migration}"; then
        fail "mJust data migration frontend still performs direct backend/safety work: ${forbidden}"
    fi
done
if grep -Eq '^[[:space:]]*(if[[:space:]]+!)?[[:space:]]*mount[[:space:]]' "${data_migration}"; then
    fail 'mJust data migration frontend still performs a direct mount command'
fi
if grep -Fq 'Authorization:' "${data_migration_api}"; then
    fail 'mJust data migration API helper must not introduce a bearer token'
fi
grep -Fq 'migration-api.sh' "${migration_export}" || fail 'mJust Export frontend does not use the server migration API helper'
grep -Fq 'jv_migration_export_plan' "${migration_export}" || fail 'mJust Export does not plan through the Management API'
grep -Fq 'jv_migration_export_apply' "${migration_export}" || fail 'mJust Export does not apply through the Management API'
grep -Fq 'jv_migration_monitor_operation' "${migration_export}" || fail 'mJust Export does not monitor the persistent Agent operation'
for forbidden in 'systemctl ' 'podman ' 'rcon-cli' 'migration-export-backend' 'JV_MAINTENANCE_LOCK' 'flock '; do if grep -Fq "${forbidden}" "${migration_export}"; then fail "mJust Export frontend still performs direct backend work: ${forbidden}"; fi; done
if grep -Fq 'Authorization:' "${migration_api}"; then fail 'server migration API helper must not introduce a bearer token'; fi
grep -Fq 'GET /v1/admin/migration/export' "${admin_migration_export_apply}" || fail 'server migration export discovery route is missing'
grep -Fq 'POST /v1/admin/migration/export/plan' "${admin_migration_export_apply}" || fail 'server migration export plan route is missing'
grep -Fq 'POST /v1/admin/migration/export/apply' "${admin_migration_export_apply}" || fail 'server migration export apply route is missing'
grep -Fq 'authoritativeAdminMigrationExportPlan' "${admin_migration_export_apply}" || fail 'server migration export apply does not re-run authoritative planning'
grep -Fq 'adminMigrationExportTransactionHelper' "${migration_export_worker}" || fail 'server migration export worker does not use the authoritative transaction backend'
grep -Fq 'admin-migration-export-plan-json plan' "${migration_export_transaction_backend}" || fail 'server migration export transaction does not revalidate the reviewed plan'
grep -Fq 'JV_MAINTENANCE_LOCK' "${migration_export_transaction_backend}" || fail 'server migration export transaction lost the shared Minecraft maintenance lock'
grep -Fq 'jv_stop_minecraft_adaptive' "${migration_export_transaction_backend}" || fail 'server migration export transaction lost player-aware shutdown'
grep -Fq 'migration-export-backend' "${migration_export_transaction_backend}" || fail 'server migration export transaction lost the shared export backend'
grep -Fq 'restore-runtime-validate' "${migration_export_transaction_backend}" || fail 'server migration export transaction lost runtime validation'
grep -Fq 'JV_MIGRATION_EXPORT_SERVER_REPORTED' "${migration_export_transaction_backend}" || fail 'server migration export transaction does not preserve server-reported metadata'
grep -Fq 'JV_MIGRATION_EXPORT_GEYSER_REPORTED' "${migration_export_transaction_backend}" || fail 'server migration export transaction does not preserve Geyser metadata'
grep -Fq 'migration-archive create-native' "${migration_export_backend}" || fail 'shared export backend lost native bundle creation'
grep -Fq 'migration-archive verify-native' "${migration_export_backend}" || fail 'shared export backend lost SHA-256 integrity verification'
grep -Fq 'JV_MIGRATION_EXPORT_MAINTENANCE_LOCK_HELD' "${migration_export_backend}" || fail 'shared export backend cannot run under the Agent-held maintenance lock'
grep -Fq 'GET /v1/admin/migration/current-operation' "${admin_operations}" || fail 'server migration current-operation route is missing'
bash -n "${migration_export}" "${migration_export_backend}" "${migration_export_plan_backend}" "${migration_export_transaction_backend}" || fail 'server migration export shell source failed bash syntax validation'

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

grep -Fq 'setup-api.sh' "${setup}" || fail 'normal mJust setup does not use the setup API helper'
if grep -Fq 'setup-legacy' "${setup}" || grep -Fq -- '--advanced' "${setup}"; then
    fail 'normal mJust setup must not expose the legacy Advanced Setup path'
fi
grep -Fq 'jv_setup_current_operation' "${setup}" || fail 'normal mJust setup cannot reconnect to an active setup operation'
grep -Fq 'jv_setup_defaults' "${setup}" || fail 'normal mJust setup does not obtain Agent defaults'
grep -Fq 'jv_setup_storage' "${setup}" || fail 'normal mJust setup does not obtain Agent storage discovery'
grep -Fq 'jv_setup_plan' "${setup}" || fail 'normal mJust setup does not plan through the Agent'
grep -Fq 'jv_setup_apply' "${setup}" || fail 'normal mJust setup does not apply through the Agent'
grep -Fq 'jv_setup_monitor_operation' "${setup}" || fail 'normal mJust setup does not monitor the persistent Agent operation'
grep -Fq 'jv_setup_get /v1/admin/setup-defaults' "${setup_api}" || fail 'setup defaults are not read through the Management API'
grep -Fq 'jv_setup_get /v1/admin/storage' "${setup_api}" || fail 'setup storage discovery is not read through the Management API'
grep -Fq 'jv_setup_post /v1/admin/setup/plan' "${setup_api}" || fail 'setup planning does not use the Management API'
grep -Fq 'jv_setup_post /v1/admin/setup/apply' "${setup_api}" || fail 'setup apply does not use the Management API'
grep -Fq 'jv_setup_get "/v1/admin/operations/${id}"' "${setup_api}" || fail 'setup operation status does not use the Management API'
grep -Fq 'registerAdminSetupPlanRoutes' "${admin_setup_plan}" || fail 'Management Agent setup plan route is missing'
grep -Fq 'registerAdminSetupApplyRoutes' "${admin_setup_apply}" || fail 'Management Agent setup apply route is missing'
grep -Fq 'registerAdminOperationRoutes' "${admin_operations}" || fail 'Management Agent setup operation routes are missing'

for forbidden in 'write_main_config' 'render_runtime' 'configure_firewall_initial' 'apply_data_selinux' 'systemctl ' 'mountpoint ' 'findmnt ' 'install -d' 'storage_prepare_' '/etc/justvoxel' '/proc/'; do
    if grep -Fq "${forbidden}" "${setup}"; then
        fail "normal mJust setup still performs direct backend work: ${forbidden}"
    fi
done
if grep -Fq 'Authorization:' "${setup_api}"; then
    fail 'mJust setup API helper must not introduce a bearer token'
fi

grep -Fq 'restore-api.sh' "${restore}" || fail 'mJust Restore does not use the Restore API helper'
grep -Fq 'jv_restore_current_operation' "${restore}" || fail 'mJust Restore cannot reconnect to an active Restore operation'
grep -Fq 'jv_restore_backups' "${restore}" || fail 'mJust Restore does not discover backups through the Agent'
grep -Fq 'jv_restore_plan' "${restore}" || fail 'mJust Restore does not plan through the Agent'
grep -Fq 'jv_restore_apply' "${restore}" || fail 'mJust Restore does not apply through the Agent'
grep -Fq 'jv_restore_monitor_operation' "${restore}" || fail 'mJust Restore does not monitor the persistent Agent operation'
grep -Fq 'jv_restore_get /v1/admin/restore/backups' "${restore_api}" || fail 'Restore backup discovery API route is missing from the frontend helper'
grep -Fq 'jv_restore_get /v1/admin/restore/current-operation' "${restore_api}" || fail 'Restore current-operation API route is missing from the frontend helper'
grep -Fq 'jv_restore_post /v1/admin/restore/plan' "${restore_api}" || fail 'Restore planning API route is missing from the frontend helper'
grep -Fq 'jv_restore_post /v1/admin/restore/apply' "${restore_api}" || fail 'Restore apply API route is missing from the frontend helper'
grep -Fq 'POST /v1/admin/restore/apply' "${admin_restore}" || fail 'Management Agent Restore apply route is not registered'
grep -Fq 'authoritativeAdminRestorePlan' "${admin_restore_apply}" || fail 'Restore apply does not re-run authoritative planning'
grep -Fq 'adminRestoreTransactionHelper' "${restore_worker}" || fail 'Restore worker does not use the authoritative transaction backend'
for forbidden in 'JV_MAINTENANCE_LOCK' 'jv_backup_require_read_target' 'gzip -t' 'restore-archive' 'restore-runtime-validate' 'systemctl ' 'podman ' 'rcon-cli' 'chown ' 'restorecon '; do
    if grep -Fq "${forbidden}" "${restore}"; then
        fail "mJust Restore frontend still performs direct backend/safety work: ${forbidden}"
    fi
done
if grep -Fq 'Authorization:' "${restore_api}"; then
    fail 'mJust Restore API helper must not introduce a bearer token'
fi

grep -Fq '"${api_client}" GET /v1/admin/validation' "${validate}" || fail 'mJust validate does not use the Management API'
grep -Fq 'adminValidationHelper = "/usr/libexec/justvoxel/mjust/validate-backend"' "${admin_validation}" || fail 'Management Agent validation helper is not the preserved backend'
grep -Fq 'GET /v1/admin/validation' "${admin_validation}" || fail 'Management Agent validation route is missing'
grep -Fq 'registerAdminValidationRoutes(mux, s)' "${main}" || fail 'Management Agent validation route is not registered'
grep -Fq 'systemctl --failed' "${validate_backend}" || fail 'validation backend lost systemd health checks'
grep -Fq 'podman exec minecraft rcon-cli' "${validate_backend}" || fail 'validation backend lost Minecraft RCON checks'

for forbidden in 'systemctl ' 'podman ' 'rcon-cli' 'firewall-cmd ' 'findmnt ' 'mountpoint ' 'swapon ' '/proc/' '/sys/' 'ls -Z' 'storage-summary'; do
    if grep -Fq "${forbidden}" "${validate}"; then
        fail "mJust validate frontend still performs direct system inspection: ${forbidden}"
    fi
done
if grep -Fq 'Authorization:' "${validate}"; then
    fail 'mJust validate frontend must not introduce a bearer token'
fi

echo 'mJust Management API foundation, Players, Minecraft control, Status, Whitelist, Manual Backup, Logs, Configuration, Backup Storage, Minecraft Data Migration, Server Migration Export API, First-run Setup, Restore, and Validation migration checks passed.'
