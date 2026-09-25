#!/usr/bin/bash
set -euo pipefail

common=mjust/libexec/minecraft-reset-common.sh
helper=mjust/libexec/admin-minecraft-reset-json
start_over=mjust/libexec/start-over
agent=management/cmd/justvoxel-management-agent/admin_minecraft_reset.go
operations=management/cmd/justvoxel-management-agent/operation_store.go

for path in "${common}" "${helper}" "${start_over}" "${agent}" "${operations}"; do
    [[ -f "${path}" ]] || {
        echo "ERROR: missing Minecraft reset source: ${path}" >&2
        exit 1
    }
done

grep -Fq 'network|external|unknown)' "${helper}"
grep -Fq 'data_action=preserve' "${helper}"
grep -Fq 'data_action=delete' "${helper}"
grep -Fq 'backup_action:"preserve"' "${helper}"
grep -Fq 'storage_layout_action:"preserve"' "${helper}"
grep -Fq 'authentication_action:"preserve"' "${helper}"
grep -Fq 'webui_users_action:"preserve"' "${helper}"
grep -Fq 'jv_reset_delete_internal_data "${DATA_PATH}" "${BACKUP_PATH}"' "${helper}"
grep -Fq 'jv_reset_remove_active_configuration "${JAVA_PORT}" "${BEDROCK_ENABLED}" "${BEDROCK_PORT}"' "${helper}"

grep -Fq 'nfs|nfs4|cifs|smb3)' "${common}"
grep -Fq 'if [[ ${transport,,} == usb ]]' "${common}"
grep -Fq 'jv_reset_backup_nested_in_data' "${common}"
grep -Fq 'find "${data_path}" -xdev -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +' "${common}"

if grep -Eq 'rm[[:space:]]+-rf[[:space:]]+--?[[:space:]]*"?\$\{?BACKUP_PATH' "${helper}" "${common}"; then
    echo 'ERROR: Minecraft reset must not delete backup storage.' >&2
    exit 1
fi
if grep -Eq 'umount|wipefs|parted|sgdisk|mkfs\.' "${helper}" "${common}"; then
    echo 'ERROR: Minecraft reset must not alter storage layout or filesystems.' >&2
    exit 1
fi

grep -Fq 'source /usr/libexec/justvoxel/mjust/minecraft-reset-common.sh' "${start_over}"
grep -Fq 'operationTypeMinecraftReset = "minecraft_reset"' "${operations}"
grep -Fq 'POST /v1/admin/reset/minecraft/plan' "${agent}"
grep -Fq 'POST /v1/admin/reset/minecraft/apply' "${agent}"

echo 'Minecraft reset source contract OK'
