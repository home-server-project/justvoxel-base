#!/usr/bin/bash
set -euo pipefail

if ! source mjust/libexec/common.sh; then
    echo 'ERROR: could not load the mount identity helper from common.sh' >&2
    exit 1
fi

require_source_text() {
    local file="$1" required="$2"
    if ! grep -Fq -- "${required}" "${file}"; then
        printf 'ERROR: missing required source text in %s: %s\n' "${file}" "${required}" >&2
        exit 1
    fi
}

findmnt() {
    local field=''
    while (( $# )); do
        case "$1" in
            -o) field="$2"; shift 2 ;;
            *) shift ;;
        esac
    done
    case "${field}" in
        TARGET) printf '%s\n' /var/mnt/justvoxel-data /var/mnt/justvoxel-data ;;
        UUID) printf '%s\n' "${TEST_UUID}" "${TEST_SECOND_UUID}" ;;
        SOURCE) printf '%s\n' "${TEST_SOURCE}" "${TEST_SECOND_SOURCE}" ;;
    esac
}

TEST_UUID=3cff6565-2602-4ad4-a107-1a39ff886f2e
TEST_SECOND_UUID="${TEST_UUID}"
TEST_SOURCE=/dev/vdb1
TEST_SECOND_SOURCE="${TEST_SOURCE}"
if [[ $(jv_exact_mount_identity UUID /var/mnt/justvoxel-data) != "${TEST_UUID}" ]]; then
    echo 'ERROR: canonical mount UUID was not returned' >&2
    exit 1
fi
if [[ $(jv_exact_mount_identity SOURCE /var/mnt/justvoxel-data) != "${TEST_SOURCE}" ]]; then
    echo 'ERROR: canonical mount source was not returned' >&2
    exit 1
fi
if jv_exact_mount_identity UUID /var/mnt/justvoxel-backup >/dev/null; then
    echo 'ERROR: identity from a different mountpoint was accepted' >&2
    exit 1
fi

TEST_SECOND_UUID=00000000-0000-0000-0000-000000000000
if jv_exact_mount_identity UUID /var/mnt/justvoxel-data >/dev/null; then
    echo 'ERROR: conflicting mount UUID was accepted' >&2
    exit 1
fi
TEST_SECOND_UUID="${TEST_UUID}"
TEST_SECOND_SOURCE=/dev/vdc1
if jv_exact_mount_identity SOURCE /var/mnt/justvoxel-data >/dev/null; then
    echo 'ERROR: conflicting mount source was accepted' >&2
    exit 1
fi

for file in mjust/libexec/validate-data-mount mjust/libexec/validate-backend mjust/libexec/admin-setup-storage-transaction-common.sh mjust/libexec/admin-setup-storage-transaction-apply.sh; do
    require_source_text "${file}" 'jv_exact_mount_identity'
done
require_source_text mjust/libexec/admin-setup-storage-transaction-apply.sh 'if ! current_source="$(jv_exact_mount_identity SOURCE'
require_source_text mjust/libexec/storage-summary 'findmnt -n --first-only -o SOURCE --target "$1" 2>/dev/null || printf '\''Unknown'\'''
require_source_text mjust/libexec/storage-summary 'findmnt -n --first-only -o TARGET --target "$1" 2>/dev/null || printf '\''Unknown'\'''

require_source_text runtime/minecraft-backup 'source /usr/libexec/justvoxel/mjust/backup-common.sh'
require_source_text runtime/minecraft-backup 'jv_backup_prepare_write_target'
require_source_text mjust/libexec/backup-common.sh 'if ! declare -F jv_exact_mount_identity >/dev/null; then'
require_source_text mjust/libexec/backup-common.sh 'source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"'
require_source_text mjust/libexec/backup-common.sh 'jv_exact_mount_identity UUID'
require_source_text mjust/libexec/common.sh 'jv_exact_mount_identity() {'

echo 'Canonical mount identity source checks passed.'
