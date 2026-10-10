#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/mjust/libexec/admin-backup-storage-common.sh"

probe="$(mktemp -d)"
trap 'rmdir -- "${probe}"' EXIT

# Use the real readable filesystem and GNU df, not canned metrics. Available
# space may legitimately be zero; filesystem size must be positive.
available="$(available_bytes_for_path "${probe}")"
total="$(filesystem_bytes_for_path "${probe}")"
[[ ${available} =~ ^[0-9]+$ ]] || { echo 'Available capacity is not an integer.' >&2; exit 1; }
[[ ${total} =~ ^[0-9]+$ && ${total} -gt 0 ]] || { echo 'Filesystem capacity is not a positive integer.' >&2; exit 1; }
[[ ${available} -le ${total} ]] || { echo 'Available capacity exceeds filesystem size.' >&2; exit 1; }
expected_total="$(df -B1 --output=size "${probe}" | awk 'NR == 2 {print $1}')"
[[ ${total} == "${expected_total}" ]] || { echo 'Filesystem capacity differs from GNU df bytes.' >&2; exit 1; }

# Failed measurement must not turn into a fabricated zero or other number.
df() { return 1; }
[[ -z $(available_bytes_for_path "${probe}" || true) ]]
[[ -z $(filesystem_bytes_for_path "${probe}" || true) ]]

echo 'Backup storage capacity helpers passed.'
