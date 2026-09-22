#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
common="${repo_root}/mjust/libexec/admin-backup-delete-common.sh"
apply="${repo_root}/mjust/libexec/admin-backup-delete-apply.sh"
entry="${repo_root}/mjust/libexec/admin-backup-delete-json"

for script in "${common}" "${apply}" "${entry}"; do
    bash -n "${script}"
done

grep -Fq 'minecraft-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}\.tar\.gz' "${common}" || {
    echo 'ERROR: backup deletion lost strict JustVoxel archive naming.' >&2
    exit 1
}
grep -Fq '! -L ${archive}' "${common}" || {
    echo 'ERROR: backup deletion lost archive symlink rejection.' >&2
    exit 1
}
grep -Fq '${real} == "${root}/${id}"' "${common}" || {
    echo 'ERROR: backup deletion lost backup-directory path confinement.' >&2
    exit 1
}
grep -Fq 'confirmation="DELETE ' "${common}" || {
    echo 'ERROR: backup deletion lost exact typed confirmation.' >&2
    exit 1
}
grep -Fq 'BACKUP_DELETE_PLAN_JSON' "${common}" || {
    echo 'ERROR: backup deletion lost reviewed plan generation.' >&2
    exit 1
}
grep -Fq 'flock -n 9' "${apply}" || {
    echo 'ERROR: backup deletion lost the JustVoxel maintenance lock.' >&2
    exit 1
}
grep -Fq 'submitted_fingerprint' "${apply}" || {
    echo 'ERROR: backup deletion lost reviewed fingerprint verification.' >&2
    exit 1
}
grep -Fq 'changed after Review. Nothing was deleted' "${apply}" || {
    echo 'ERROR: backup deletion lost bounded changed-selection failure.' >&2
    exit 1
}
grep -Fq 'rm -f -- "${metadata}"' "${apply}" || {
    echo 'ERROR: backup deletion no longer removes matching metadata sidecars.' >&2
    exit 1
}

echo 'New Backups delete safety source checks passed.'
