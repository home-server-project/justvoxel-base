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

echo 'migration source ownership checks passed.'
