#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/admin-storage-mounts-json"
agent="${repo_root}/management/cmd/justvoxel-management-agent/admin_storage_mounts.go"

bash -n "${helper}"

for required in \
    'storage_action_validate_target' \
    'storage_action_mountable_filesystem' \
    'storage_mount_fingerprint' \
    'changed after Review. Nothing was changed.' \
    'storage_action_mount_type' \
    'storage_action_mount_options' \
    'nofail,x-systemd.device-timeout=10s' \
    '/etc/justvoxel/storage-mounts' \
    'configured outside JustVoxel. JustVoxel will not replace it.' \
    'was not created by JustVoxel, so JustVoxel will not remove it.' \
    'mounted filesystem did not match the reviewed filesystem' \
    'The previous configuration was restored.' \
    'rmdir -- "${mountpoint}"'; do
    grep -Fq -- "${required}" "${helper}" || {
        echo "missing permanent-mount safety invariant: ${required}" >&2
        exit 1
    }
done

grep -Fq 'requireAdministrator' "${agent}" || {
    echo 'permanent mount API must remain Administrator-only' >&2
    exit 1
}
grep -Fq '/v1/admin/storage-mounts/plan' "${agent}" || {
    echo 'permanent mount plan endpoint is missing' >&2
    exit 1
}
grep -Fq '/v1/admin/storage-mounts/apply' "${agent}" || {
    echo 'permanent mount apply endpoint is missing' >&2
    exit 1
}

echo 'Generic permanent-mount safety source checks passed.'
