#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/migration-import-source.sh"
plan="${repo_root}/mjust/libexec/migration-import-plan-source.sh"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

if grep -Fq 'sha256sum -- "${path}"' "${helper}"; then
    fail 'Import still hashes the entire user-owned remote archive for source identity'
fi
grep -Fq 'transport_identity' "${helper}" || fail 'Import lost reviewed source-location identity'
grep -Fq 'The original archive belongs to the user.' "${plan}" || fail 'Import source ownership boundary is not explicit'
if grep -Fq 'migration source changed while it was being staged' "${plan}"; then
    fail 'Import still treats post-read changes to the original user file as JustVoxel state'
fi
if grep -Fq 'jv_migration_import_source_content_identity' "${repo_root}/mjust/libexec/admin-migration-import-plan-json"; then
    fail 'Import planner still calls the removed content-identity helper'
fi
grep -Fq 'printf '\''%s\n%s\n'\'' "$current_transport_identity" "$requested_source_path"' "${repo_root}/mjust/libexec/admin-migration-import-plan-json" || fail 'Import planner no longer rebuilds the reviewed source-location identity consistently'

transaction="${repo_root}/mjust/libexec/admin-migration-import-transaction-json"
backend="${repo_root}/mjust/libexec/migration-import-backend"
grep -Fq 'transport_started=no' "${transaction}" || fail 'Agent transaction does not initialize source transport ownership'
grep -Fq 'if [[ -n ${JV_MIGRATION_OWNED_MOUNT:-} ]]; then' "${transaction}" || fail 'Agent transaction does not derive source transport ownership from the owned mount'
grep -Fq 'transport_started=yes' "${transaction}" || fail 'Agent transaction does not mark temporary source mounts as owned'
grep -Fq 'JV_MIGRATION_API_SOURCE_TRANSPORT_STARTED="$transport_started"' "${transaction}" || fail 'Agent transaction does not hand owned source transport to the staging backend'
grep -Fq 'JV_MIGRATION_API_SOURCE_OWNED_MOUNT=' "${transaction}" || fail 'Agent transaction does not hand the owned mount to the staging backend'
grep -Fq 'JV_MIGRATION_API_SOURCE_TRANSPORT_STARTED:-no' "${backend}" || fail 'staging backend does not restore owned transport state'
grep -Fq 'JV_MIGRATION_OWNED_MOUNT="${JV_MIGRATION_API_SOURCE_OWNED_MOUNT:-}"' "${backend}" || fail 'staging backend cannot release the JustVoxel-owned source mount'

echo 'migration source ownership checks passed.'
