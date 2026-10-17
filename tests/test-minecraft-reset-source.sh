#!/usr/bin/bash
set -euo pipefail

common=mjust/libexec/minecraft-reset-common.sh
retirement=management/cmd/justvoxel-management-agent/minecraft_instance_delete.go
start_over=mjust/libexec/start-over
agent=management/cmd/justvoxel-management-agent/admin_minecraft_reset.go
operations=management/cmd/justvoxel-management-agent/operation_store.go

for path in "${common}" "${retirement}" "${start_over}" "${agent}" "${operations}"; do
    [[ -f "${path}" ]] || {
        echo "ERROR: missing Minecraft reset source: ${path}" >&2
        exit 1
    }
done

# Selected Reset plans from authoritative instance storage and inspects selected players.
grep -Fq 'instanceRuntimes.readRecord(id)' "${agent}"
grep -Fq 'record.Storage != instanceStorageFor(snapshot, id)' "${agent}"
grep -Fq 'instanceRuntimes.storage(parent, "validate", record)' "${agent}"
grep -Fq '"nsenter", "--mount=/proc/1/ns/mnt"' "${agent}"
grep -Fq 'minecraft-reset-common.sh; jv_reset_data_scope "$1"' "${agent}"
grep -Fq 'scope != "internal" && scope != "external" && scope != "network" && scope != "unknown"' "${agent}"
grep -Fq 'action := "preserve"' "${agent}"
grep -Fq 'if scope == "internal" {' "${agent}"
grep -Fq 'action = "delete"' "${agent}"
grep -Fq 'BackupAction: "preserve", StorageLayoutAction: "preserve", AuthenticationAction: "preserve", WebUIUsersAction: "preserve"' "${agent}"
grep -Fq 'pluginInstallPlayers(parent, id)' "${agent}"
grep -Fq 'plan.InstanceID != journal.InstanceID' "${agent}"
grep -Fq 's.prepareResetPluginTransaction(ctx, operationID, plan)' "${agent}"
grep -Fq 'instanceRuntimes.retireSelectedInstance(ctx, plan.InstanceID, tx.InstanceName, plan.DataAction)' "${agent}"
grep -Fq 'j.Record.Storage != instanceStorageFor(snapshot, id)' "${retirement}"
grep -Fq 'retry && j.ResetPolicy != resetPolicy' "${retirement}"
grep -Fq 'if j.ResetPolicy == "preserve" {' "${retirement}"
grep -Fq 'return b.removeInstanceData(ctx, j)' "${retirement}"
grep -Fq '"podman", "rm", "--ignore", "--", "minecraft-"+id' "${retirement}"

# Fixed container removal remains shared with legacy Setup recovery; Factory Reset no longer calls it.
grep -Fq 'podman rm --force --ignore minecraft' "${common}"
grep -Fq 'podman container exists minecraft' "${common}"
if grep -Fq 'podman rm minecraft' "${agent}" "${retirement}" "${common}"; then
    echo 'ERROR: Minecraft reset must not use race-prone plain podman rm.' >&2
    exit 1
fi

grep -Fq 'nfs|nfs4|cifs|smb3)' "${common}"
grep -Fq 'if [[ ${transport,,} == usb ]]' "${common}"
grep -Fq 'jv_reset_backup_nested_in_data' "${common}"
grep -Fq 'jv_reset_nested_mounts' "${common}"
grep -Fq 'find "${root}" -xdev -mindepth 1 -delete' "${common}"

if grep -Eq 'rm[[:space:]]+-rf[[:space:]]+--?[[:space:]]*"?\$\{?BACKUP_PATH' "${agent}" "${retirement}" "${common}"; then
    echo 'ERROR: Minecraft reset must not delete backup storage.' >&2
    exit 1
fi
if grep -Eq 'umount|wipefs|parted|sgdisk|mkfs\.' "${agent}" "${retirement}" "${common}"; then
    echo 'ERROR: Minecraft reset must not alter storage layout or filesystems.' >&2
    exit 1
fi

grep -Fq 'POST /v1/admin/start-over/apply' "${start_over}"
grep -Eq '^[[:space:]]*operationTypeMinecraftReset[[:space:]]*=[[:space:]]*"minecraft_reset"[[:space:]]*$' "${operations}"
for route in \
    'POST /v1/minecraft/instances/{id}/reset/plan' \
    'POST /v1/minecraft/instances/{id}/reset/apply' \
    'POST /v1/minecraft/instances/{id}/reset/recover' \
    'GET /v1/minecraft/instances/{id}/reset/current-operation' \
    'GET /v1/minecraft/instances/{id}/reset/operations/{operation}'; do
    grep -Fq "${route}" "${agent}"
done
if [[ -e mjust/libexec/admin-minecraft-reset-json ]] || grep -Eq 'admin-minecraft-reset-json|adminMinecraftResetHelper|runAdminMinecraftResetHelper|adminMinecraftResetHelperApplyResponse|decodeAdminMinecraftResetApplyResponse|/v1/admin/reset/minecraft/' "${agent}"; then
    echo 'ERROR: selected Minecraft reset must not depend on the obsolete fixed-server backend.' >&2
    exit 1
fi
grep -Fq 'retryMinecraftReset' "${agent}"
grep -Fq 'func (s *operationStore) retryMinecraftReset' "${operations}"

echo 'Minecraft reset source contract OK'
