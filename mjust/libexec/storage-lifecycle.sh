# shellcheck shell=bash
# Shared, bounded ownership and identity checks. These helpers never delete data.
jv_storage_credentials_safe() {
    local file="$1"
    [[ ! -L ${file} && -f ${file} && $(realpath -- "${file}") == "${file}" ]] || return 1
    [[ $(stat -c '%u:%g:%a:%h' -- "${file}") == 0:0:600:1 ]] || return 1
    awk '
      index($0,"\r") || index($0,sprintf("%c",0)) {bad=1; next}
      /^username=.+$/ {u++; next}
      /^password=.+$/ {p++; next}
      /^domain=[^\r]*$/ {d++; next}
      {bad=1}
      END {exit (bad || u != 1 || p != 1 || d > 1)}
    ' "${file}"
}

# Exactly one entry; reserved paths or the private JustVoxel credential reference
# establish ownership. An adopted administrator mount elsewhere is left alone.
jv_storage_managed_entry() {
    local fstab="$1" point="$2" credentials="$3" ownership="${4:-reserved}" row source type options rest
    [[ -f ${fstab} && ! -L ${fstab} ]] || return 1
    [[ ${point} == /* && ${point} != *[[:space:]]* && $(realpath -m -- "${point}") == "${point}" ]] || return 1
    case "${point}" in
        /|/boot|/boot/*|/var|/var/tmp|/var/lib/containers|/var/lib/containers/*|/etc|/etc/*|/sysroot|/sysroot/*) return 1 ;;
    esac
    row="$(awk -v mp="${point}" '$0 !~ /^[[:space:]]*#/ && NF >= 2 && $2 == mp {print}' "${fstab}")"
    [[ -n ${row} && ${row} != *$'\n'* ]] || return 1
    read -r source rest type options rest <<< "${row}"
    case "${point}" in
        /var/mnt/justvoxel-data|/var/mnt/justvoxel-backup) ;;
        *)
            if [[ ${type} == cifs && ,${options}, == *,credentials="${credentials}",* ]]; then
                :
            elif [[ ${ownership} == configured ]]; then
                # Saved configuration plus the exact options written by JustVoxel
                # distinguish its custom local/NFS mounts from adopted admin mounts.
                case "${type}:${options}" in
                    xfs:noatime,nofail,x-systemd.device-timeout=10s|ext4:noatime,nofail,x-systemd.device-timeout=10s|btrfs:noatime,nofail,x-systemd.device-timeout=10s|nfs:rw,_netdev,nofail,x-systemd.mount-timeout=20s) ;;
                    *) return 1 ;;
                esac
            else
                return 1
            fi
            ;;
    esac
    case "${type}" in
        xfs|ext4|btrfs) [[ ${source} == UUID=?* ]] || return 1 ;;
        nfs|nfs4) [[ ${source} == *:/* ]] || return 1 ;;
        cifs) [[ ${source} == //*/* && ,${options}, == *,credentials="${credentials}",* ]] || return 1 ;;
        *) return 1 ;;
    esac
    printf '%s\n' "${source}"
}

jv_storage_identity_matches() {
    local point="$1" source="$2" uuid="${3:-}" live
    if [[ -n ${uuid} || ${source} == UUID=* ]]; then
        live="$(jv_exact_mount_identity UUID "${point}")" || return 1
        [[ ${live} == "${uuid:-${source#UUID=}}" ]] || return 1
    fi
    if [[ ${source} != UUID=* ]]; then
        live="$(jv_exact_mount_identity SOURCE "${point}")" || return 1
        [[ -n ${source} && ${live} == "${source}" ]] || return 1
    fi
}

jv_storage_remove_entry() {
    local fstab="$1" point="$2" tmp
    [[ -f ${fstab} && ! -L ${fstab} ]] || return 1
    tmp="$(mktemp "$(dirname -- "${fstab}")/.fstab.lifecycle.XXXXXX")" || return 1
    # Prepare contents and original metadata without touching the live fstab.
    if ! awk -v mp="${point}" '/^[[:space:]]*#/ || NF < 2 || $2 != mp {print}' "${fstab}" > "${tmp}" ||
       ! cp --attributes-only --preserve=all -- "${fstab}" "${tmp}" ||
       ! sync -f -- "${tmp}" ||
       ! mv -fT -- "${tmp}" "${fstab}"; then
        rm -f -- "${tmp}"
        return 1
    fi
}

jv_storage_clear_retired_failure() {
    local unit load state
    systemctl daemon-reload || return 1
    unit="$(systemd-escape --path --suffix=mount "$1")" || return 1
    # reset-failed may report that a successfully removed unit is no longer loaded.
    systemctl reset-failed "${unit}" >/dev/null 2>&1 || true
    load="$(systemctl show --property=LoadState --value -- "${unit}" 2>/dev/null)" || return 1
    [[ ${load} != not-found ]] || return 0
    state="$(systemctl show --property=ActiveState --value -- "${unit}" 2>/dev/null)" || return 1
    [[ -n ${state} && ${state} != failed ]]
}

jv_storage_credentials_referenced() {
    awk -v credential="$2" '
      $0 !~ /^[[:space:]]*#/ && NF >= 4 {
        n=split($4,opts,","); for (i=1;i<=n;i++) if(opts[i]=="credentials=" credential) found=1
      }
      END {exit !found}
    ' "$1"
}

# Non-destructive inspection of configured appliances.
# Only the two historical reserved paths provide ownership without old config.
jv_storage_reconcile_configured() {
    local fstab="$1" credentials="$2" point source status=0 value
    [[ ! -e ${JV_SETUP_IN_PROGRESS} ]] || return 0
    [[ -e ${JV_CONFIG} || -L ${JV_CONFIG} ]] || return 0
    if [[ ! -f ${JV_CONFIG} || -L ${JV_CONFIG} ]] || ! require_config; then
        echo 'Storage reconciliation skipped: current configuration could not be read safely.' >&2
        return 1
    fi
    # Missing/invalid selection evidence must not be interpreted as obsolete storage.
    case "${BACKUP_TYPE:-}" in system|disk|partition|smb|nfs) ;;
        *) echo 'Storage reconciliation skipped: backup selection is ambiguous.' >&2; return 1 ;;
    esac
    [[ -n ${DATA_PATH:-} && -n ${BACKUP_PATH:-} && ${DATA_MOUNT_POINT+x} && ${BACKUP_MOUNT_POINT+x} ]] || {
        echo 'Storage reconciliation skipped: storage selection is incomplete.' >&2; return 1;
    }
    for value in "${DATA_PATH}" "${BACKUP_PATH}" "${DATA_MOUNT_POINT}" "${BACKUP_MOUNT_POINT}"; do
        [[ -n ${value} ]] || continue
        [[ ${value} == /* && ${value} != *[[:space:]]* && $(realpath -m -- "${value}") == "${value}" ]] || {
            echo 'Storage reconciliation skipped: storage selection is not canonical.' >&2; return 1;
        }
    done
    [[ -f ${fstab} && ! -L ${fstab} ]] || {
        echo 'Storage reconciliation skipped: fstab is unsafe or unavailable.' >&2; return 1;
    }
    for point in /var/mnt/justvoxel-data /var/mnt/justvoxel-backup; do
        # Path containment also protects shared data/backup filesystems and older
        # configurations without a dedicated backup mount selection.
        if [[ ${point} == "${DATA_MOUNT_POINT}" || ${point} == "${BACKUP_MOUNT_POINT}" ||
              ${DATA_PATH} == "${point}" || ${DATA_PATH} == "${point}/"* ||
              ${BACKUP_PATH} == "${point}" || ${BACKUP_PATH} == "${point}/"* ]]; then
            continue
        fi
        if ! awk -v mp="${point}" '$0 !~ /^[[:space:]]*#/ && $2==mp {found=1} END {exit !found}' "${fstab}"; then
            continue
        fi
        source="$(jv_storage_managed_entry "${fstab}" "${point}" "${credentials}")" || {
            echo "Storage reconciliation skipped for safety at ${point}: ambiguous ownership." >&2
            status=1; continue;
        }
        # Configuration is only a default. Other instances may own this mount.
        echo "Storage reconciliation preserved ${point}: not referenced by appliance defaults." >&2
    done
    return "${status}"
}
