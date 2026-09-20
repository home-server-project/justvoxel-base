#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/admin-setup-plan-json"

[[ -f ${helper} ]] || { echo "missing A4.4.1 helper: ${helper}" >&2; exit 1; }
bash -n "${helper}"

# A4.4.1 is planning only. It may inspect storage and upstream metadata but it
# must not mutate filesystems, mounts, configuration, services, firewall, or
# runtime state.
for forbidden in \
    'mkfs\.' \
    'wipefs' \
    'parted ' \
    'storage_mount_local' \
    'storage_write_local_fstab' \
    'storage_write_network_fstab' \
    'write_main_config' \
    'render_runtime' \
    'configure_firewall' \
    'systemctl (start|stop|restart|enable|disable)' \
    'podman (run|rm|stop|start|pull)' \
    'mount --' \
    'umount'; do
    if grep -Eq "${forbidden}" "${helper}"; then
        echo "A4.4.1 setup planner contains forbidden mutating operation: ${forbidden}" >&2
        exit 1
    fi
done

# The helper owns the authoritative full-draft schema and rejects any key whose
# name contains password. SMB credentials are execution-time input in A5, not
# planning data.
grep -Fq 'SETUP_PLAN_SCHEMA_VERSION=v1' "${helper}"
grep -Fq 'contains("password")' "${helper}"
grep -Fq 'smb_password_required' "${helper}"
grep -Fq 'network_backup_validation_on_apply' "${helper}"

# Important cross-field safety boundaries must remain server-side in the helper.
grep -Fq 'Maximum Minecraft memory must be larger than Minecraft game memory.' "${helper}"
grep -Fq 'must leave at least 1 GiB' "${helper}"
grep -Fq 'Root, boot, EFI, and /var filesystems cannot be selected' "${helper}"
grep -Fq 'Minecraft data and backups cannot use the same directory' "${helper}"
grep -Fq 'same_physical_disk' "${helper}"
grep -Fq 'resolve_latest_stable_paper_version' "${helper}"
grep -Fq 'PaperMC did not confirm a stable build' "${helper}"
grep -Fq 'JustVoxel is already configured' "${helper}"

# Network-backed first-run plans delegate mount-point safety to the shared
# storage validator. Safe non-critical mount points must return success while
# critical and malformed paths remain rejected.
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/common.sh"

geyser_fixture='{"java":{"supported":"26.2"}}'
[[ $(geyser_supported_java_version_from_json "${geyser_fixture}") == 26.2 ]] || {
    echo 'Geyser Java support metadata was not parsed correctly.' >&2
    exit 1
}
if geyser_supported_java_version_from_json '{"java":{"supported":"26.2-26.3"}}' >/dev/null 2>&1; then
    echo 'Ambiguous Geyser Java support metadata was accepted.' >&2
    exit 1
fi
bedrock_crossplay_supports_version 26.2 26.2 || {
    echo 'Matching Minecraft/Geyser versions were rejected.' >&2
    exit 1
}
if bedrock_crossplay_supports_version 26.3 26.2; then
    echo 'Unsupported Minecraft/Geyser version mismatch was accepted.' >&2
    exit 1
fi

grep -Fq 'resolve_geyser_supported_java_version' "${helper}"
grep -Fq 'version="${geyser_supported_version}"' "${helper}"
grep -Fq 'bedrock_enabled=false' "${helper}"
grep -Fq 'bedrock_version_unsupported' "${helper}"
grep -Fq 'Come back later and enable Bedrock cross-play' "${helper}"

# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/storage-common-base.sh"

for safe_mount in /var/mnt/justvoxel-backup /var/mnt/justvoxel-data; do
    storage_validate_mountpoint_path "${safe_mount}" >/dev/null 2>&1 || {
        echo "safe JustVoxel mount point was rejected: ${safe_mount}" >&2
        exit 1
    }
done

for unsafe_mount in / /boot /boot/efi /var 'relative/path' '/var/mnt/bad path' '/var/mnt/bad;path'; do
    if storage_validate_mountpoint_path "${unsafe_mount}" >/dev/null 2>&1; then
        echo "unsafe JustVoxel mount point was accepted: ${unsafe_mount}" >&2
        exit 1
    fi
done

grep -Fq 'storage_validate_mountpoint_path "${mountpoint}" >/dev/null 2>&1 || return 20' "${helper}"
grep -Fq "20) printf 'Choose a safe absolute mount point for the network backup destination.'" "${helper}"

# A4.4.1 produces a normalized plan only; Apply is intentionally absent.
if grep -Eq '(^|[[:space:]])apply([[:space:]:]|$)' "${helper}"; then
    echo 'A4.4.1 setup planner must not expose an Apply action.' >&2
    exit 1
fi

echo 'WebUI first-run setup planner A4.4.1 safety checks passed.'
