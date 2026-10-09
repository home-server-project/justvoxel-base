#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
common="${repo_root}/mjust/libexec/admin-backup-delete-common.sh"
apply="${repo_root}/mjust/libexec/admin-backup-delete-apply.sh"
entry="${repo_root}/mjust/libexec/admin-backup-delete-json"

for script in "${common}" "${apply}" "${entry}"; do
    bash -n "${script}"
done

grep -Fq 'jv_backup_valid_archive_id "$1"' "${common}" || {
    echo 'ERROR: backup deletion lost strict JustVoxel archive naming.' >&2
    exit 1
}
source "${repo_root}/mjust/libexec/backup-common.sh"
for id in minecraft-2026-09-15-043000.tar.gz minecraft-paper-2026-10-09-1506.tar.gz minecraft-kids-purpur-2026-10-09-1506-2.tar.gz; do
    jv_backup_valid_archive_id "${id}" || { echo "ERROR: valid archive name rejected: ${id}" >&2; exit 1; }
done
for id in ../minecraft-paper-2026-10-09-1506.tar.gz minecraft-paper-2026-10-09-1506.tar.gz.partial minecraft-paper-2026-10-09-1506.tar.gz/../other minecraft-bad.tar.gz; do
    if jv_backup_valid_archive_id "${id}"; then echo "ERROR: unsafe archive name accepted: ${id}" >&2; exit 1; fi
done
[[ "$(MINECRAFT_SERVER_TYPE=purpur jv_archive_server_label)" == minecraft-purpur ]] || { echo 'ERROR: configured Minecraft type missing from archive label' >&2; exit 1; }

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
