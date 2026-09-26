#!/usr/bin/bash
set -euo pipefail

common=mjust/libexec/minecraft-reset-common.sh
helper=mjust/libexec/admin-factory-reset-json
agent=management/cmd/justvoxel-management-agent/admin_factory_reset.go
operations=management/cmd/justvoxel-management-agent/operation_store.go
auth=management/cmd/justvoxel-management-agent/auth_provider.go
store=management/cmd/justvoxel-management-agent/webui_store.go

for path in "${common}" "${helper}" "${agent}" "${operations}" "${auth}" "${store}"; do
    [[ -f "${path}" ]] || {
        echo "ERROR: missing Factory Reset source: ${path}" >&2
        exit 1
    }
done

grep -Fq 'network|external|unknown)' "${helper}"
grep -Fq 'internal) backup_action=delete' "${helper}"
grep -Fq 'backup_action=preserve' "${helper}"
grep -Fq 'external_storage_action:"preserve"' "${helper}"
grep -Fq 'network_storage_action:"preserve"' "${helper}"
grep -Fq 'storage_layout_action:"preserve"' "${helper}"
grep -Fq 'config_backups_action:"delete"' "${helper}"
grep -Fq 'jv_reset_delete_tree_same_filesystem "${backup_path}"' "${helper}"
grep -Fq 'jv_reset_delete_tree_same_filesystem "${config_backup_dir}"' "${helper}"
grep -Fq 'podman rm --force --ignore minecraft' "${helper}"
grep -Fq 'container_cleanup_failed' "${helper}"
grep -Fq 'data_cleanup_failed' "${helper}"
grep -Fq 'backup_cleanup_failed' "${helper}"
grep -Fq 'configuration_cleanup_failed' "${helper}"
grep -Fq 'runtime_cleanup_incomplete' "${helper}"

(
    source "${common}"
    findmnt() {
        printf '%s\n' '/var/mnt/justvoxel-data' '/var/mnt/justvoxel-data/nested' '/var/mnt/other'
    }
    nested="$(jv_reset_nested_mounts /var/mnt/justvoxel-data)"
    [[ "${nested}" == '/var/mnt/justvoxel-data/nested' ]] || {
        echo "ERROR: reset root mountpoint was incorrectly treated as a nested mount: ${nested}" >&2
        exit 1
    }
)

if grep -Eq 'umount|wipefs|parted|sgdisk|mkfs\.' "${helper}" "${common}"; then
    echo 'ERROR: Full Factory Reset must not alter mounts, partitions, or filesystems.' >&2
    exit 1
fi
if grep -Eq 'rm[[:space:]]+-rf.*(nfs|smb|cifs|network|external|usb)' "${helper}" "${common}"; then
    echo 'ERROR: Full Factory Reset contains an external/network recursive delete path.' >&2
    exit 1
fi

grep -Fq 'SystemPassword  string' "${agent}"
grep -Fq 'systemAuthenticate(systemAdminUsername, request.SystemPassword)' "${agent}"
grep -Fq 'request.SystemPassword = ""' "${agent}"
grep -Fq '"/usr/bin/chage", "-d", "0", systemAdminUsername' "${agent}"
grep -Fq 'resetFactoryAuthenticationState()' "${agent}"
grep -Fq 's.store.resetFactoryState()' "${agent}"
grep -Fq 's.invalidateSessions()' "${agent}"
grep -Fq 'operationTypeFactoryReset   = "factory_reset"' "${operations}"
grep -Fq 'POST /v1/admin/reset/factory/plan' "${agent}"
grep -Fq 'POST /v1/admin/reset/factory/apply' "${agent}"
grep -Fq 'POST /v1/admin/reset/factory/resolve' "${agent}"
grep -Fq 'operationResolved       operationState = "resolved"' "${operations}"
grep -Fq 'return state != operationSucceeded && state != operationRolledBack && state != operationResolved' "${operations}"

grep -Fq 'setAuthMode(authModeSystem)' "${auth}"
grep -Fq 'os.Remove(localAuthPath)' "${auth}"
grep -Fq 'DELETE FROM web_users' "${store}"
grep -Fq 'DELETE FROM audit_events' "${store}"
grep -Fq 'DELETE FROM notifications' "${store}"

echo 'Full Factory Reset source contract OK'
