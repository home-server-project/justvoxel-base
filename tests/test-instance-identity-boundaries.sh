#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Destination identity preflight stays outside runtime and archive helpers.
# Backup, restore, and import archives never require identity metadata. Existing archive contracts and all single-server paths stay valid.
if grep -REn 'minecraftInstances|registerSetupMinecraftInstance|/var/lib/justvoxel/instances|/v1/minecraft/identity' \
    "${repo_root}/mjust/libexec/common.sh" \
    "${repo_root}/mjust/libexec/backup-common.sh" \
    "${repo_root}/mjust/libexec/restore-common.sh" \
    "${repo_root}/mjust/libexec/restore-archive" \
    "${repo_root}/mjust/libexec/admin-restore-transaction-json" \
    "${repo_root}/mjust/libexec/migration-import-activate.sh" \
    "${repo_root}/mjust/libexec/migration-import-common.sh" \
    "${repo_root}/runtime" "${repo_root}/templates"; then
    echo 'FAIL: destination identity entered runtime or archive formats.' >&2
    exit 1
fi
printf '%s\n' 'PASS: destination identity stays outside runtime and archive formats.'
