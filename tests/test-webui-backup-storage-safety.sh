#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper_main="${repo_root}/mjust/libexec/admin-backup-storage-json"
helper_common="${repo_root}/mjust/libexec/admin-backup-storage-common.sh"
helper_apply="${repo_root}/mjust/libexec/admin-backup-storage-apply.sh"
agent="${repo_root}/management/cmd/justvoxel-management-agent/admin_backup_storage.go"

for file in "${helper_main}" "${helper_common}" "${helper_apply}" "${agent}"; do
    [[ -f ${file} ]] || { echo "missing A3.1 file: ${file}" >&2; exit 1; }
done

if grep -Eq '(^|[^[:alnum:]_])(mkfs(\.[[:alnum:]]+)?|wipefs|parted)([^[:alnum:]_]|$)' "${helper_main}" "${helper_common}" "${helper_apply}"; then
    echo 'A3.1 backup storage helpers must not format, erase, or partition disks.' >&2
    exit 1
fi

grep -q 'admin-backup-storage-json' "${agent}"
grep -q 'GET /v1/admin/backup-storage' "${agent}"
grep -q 'POST /v1/admin/backup-storage/plan' "${agent}"
grep -q 'POST /v1/admin/backup-storage/apply' "${agent}"
grep -Fq 'credentials_new="$(mktemp /etc/justvoxel/.smb-backup.credentials.new.XXXXXX 2>/dev/null)"' "${helper_apply}"
grep -Fq 'chmod 0600 "${credentials_new}"' "${helper_apply}"
grep -Fq 'mv -fT -- "${credentials_new}" "${A31_SMB_CREDENTIALS}"' "${helper_apply}"
grep -Fq "Existing SMB credential path is not a safe regular file." "${helper_apply}"
grep -q "password is required when applying a new SMB mount" "${helper_apply}"

# SMB mount diagnostics must preserve useful mount.cifs text without leaking
# credential values or unbounded/multiline output.
# shellcheck disable=SC1090
source "${helper_apply}"
diag="$(bounded_smb_mount_error "$(printf 'mount error(13): Permission denied\npassword=super-secret credentials=/etc/justvoxel/smb-backup.credentials')")"
[[ ${diag} == *'mount error(13): Permission denied'* ]] || { echo "SMB mount diagnostic lost the real error: ${diag}" >&2; exit 1; }
[[ ${diag} != *'super-secret'* ]] || { echo 'SMB mount diagnostic leaked a password.' >&2; exit 1; }
[[ ${diag} != *'/etc/justvoxel/smb-backup.credentials'* ]] || { echo 'SMB mount diagnostic leaked the credentials-file path.' >&2; exit 1; }
[[ $(printf '%s' "${diag}" | wc -l) -eq 0 && ${#diag} -le 512 ]] || { echo 'SMB mount diagnostic is not bounded to one line.' >&2; exit 1; }

grep -Fq 'smb_mount_error="$(mount "${TARGET_MOUNT}" 2>&1)"' "${helper_apply}" || { echo 'A3.1 SMB mount stderr is still discarded.' >&2; exit 1; }
grep -Fq 'bounded_smb_mount_error "${smb_mount_error}"' "${helper_apply}" || { echo 'A3.1 SMB mount failure does not use bounded diagnostics.' >&2; exit 1; }
if grep -Fq "SMB share could not be mounted. Check the server, share, credentials, network, and permissions." "${helper_apply}"; then
    echo 'A3.1 SMB mount failure still uses the generic discarded-diagnostic path.' >&2
    exit 1
fi
grep -Fq 'vers=3.0' "${helper_apply}" || { echo 'Step 7 must not silently change the SMB dialect.' >&2; exit 1; }

echo 'WebUI backup storage A3.1 safety checks passed.'
