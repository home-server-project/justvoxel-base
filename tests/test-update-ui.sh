#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

ui="${repo_root}/mjust/libexec/update-minecraft"
backend="${repo_root}/mjust/libexec/update-minecraft-backend"

grep -Fq "echo 'Minecraft update'" "${ui}" || fail 'friendly update heading missing'
grep -Fq 'Game version:' "${ui}" || fail 'friendly game-version state missing'
grep -Fq 'Container image:' "${ui}" || fail 'friendly container state missing'
grep -Fq 'Running container:' "${ui}" || fail 'friendly running-container state missing'
grep -Fq 'No update is needed.' "${ui}" || fail 'current-state summary missing'
if grep -Fq 'Running image ID:' "${ui}"; then fail 'raw running image ID leaked into normal update UI'; fi
grep -Fq 'Running image ID:' "${backend}" || fail 'advanced maintenance backend lost image identity detail'
# The selected immutable ID owns planning, execution, and operation polling.
for contract in \
    'jv_load_instance "${instance_id}"' \
    '/v1/minecraft/instances/${instance_id}/version/status' \
    '.instance_id == $id and .update_available' \
    'api-client POST "/v1/minecraft/instances/${instance_id}/version/update" --data' \
    '.operation | select(.instance_id == $id) | .operation_id' \
    '/v1/minecraft/instances/${instance_id}/version/update/current?operation_id=${operation_id}' \
    '.operation.instance_id == $id and .operation.operation_id == $op'; do
    grep -Fq "${contract}" "${ui}" || fail "selected-instance update handoff missing: ${contract}"
done
if grep -Fq 'exec /usr/libexec/justvoxel/mjust/update-minecraft-backend' "${ui}"; then fail 'update UI bypasses the Management API'; fi

echo 'Minecraft update UI regression tests passed.'
