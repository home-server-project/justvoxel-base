#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_plan="${repo_root}/mjust/libexec/migration-import-plan-source.sh"
activate="${repo_root}/mjust/libexec/migration-import-activate.sh"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

marker='# ORIGINAL SOURCE FILESYSTEM ACCESS ENDS HERE.'
boundary="$(grep -nF "${marker}" "${source_plan}" | cut -d: -f1)"
[[ ${boundary} =~ ^[0-9]+$ ]] || fail 'Import staging authority boundary is missing'

pre_stage="$(head -n "${boundary}" "${source_plan}")"
post_stage="$(tail -n "+$((boundary + 1))" "${source_plan}")"

grep -Fq 'migration-archive extract "${JV_MIGRATION_SOURCE}" "${staging_source}"' <<< "${pre_stage}" || fail 'Import no longer stages the reviewed source locally'
if grep -Fq 'jv_migration_source_identity "${JV_MIGRATION_SOURCE}"' <<< "${pre_stage}"; then fail 'Import still re-checks user-owned source contents after staging'; fi
grep -Fq 'source_backup_meta_mode=' <<< "${pre_stage}" || fail 'JustVoxel backup metadata is not captured before staging authority changes'
grep -Fq 'source_backup_meta_version=' <<< "${pre_stage}" || fail 'JustVoxel backup version metadata is not captured before staging authority changes'
grep -Fq 'jv_migration_transport_cleanup' <<< "${post_stage}" || fail 'JustVoxel-managed source transport is not released after staging'
grep -Fq 'transport_started=no' <<< "${post_stage}" || fail 'Import transport state is not cleared after staged handoff'

if grep -Fq 'JV_MIGRATION_SOURCE' <<< "${post_stage}"; then
    fail 'Import still reads or depends on the original source after local staging becomes authoritative'
fi
if grep -Fq 'jv_migration_source_identity "${JV_MIGRATION_SOURCE}"' "${activate}"; then
    fail 'Import activation re-reads the original source after staging'
fi

grep -Fq 'source_is_file' <<< "${post_stage}" || fail 'staged Import lost captured source classification'
grep -Fq 'source_backup_meta_mode' <<< "${post_stage}" || fail 'staged Import lost captured backup metadata'
grep -Fq 'staging_source' <<< "${post_stage}" || fail 'post-staging Import no longer operates from local staging'

echo 'migration staging authority checks passed.'
