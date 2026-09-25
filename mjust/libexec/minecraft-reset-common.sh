#!/usr/bin/bash

jv_reset_data_scope() {
    local data_path="$1" source fstype transport parent

    if [[ -z ${data_path} || ${data_path} == / || ${data_path} == /var || ${data_path} == /var/lib ]]; then
        printf 'unsafe\n'
        return
    fi
    if [[ -L ${data_path} ]]; then
        printf 'unknown\n'
        return
    fi
    case "${data_path}" in
        /var/lib/justvoxel/*)
            printf 'internal\n'
            return
            ;;
    esac

    source="$(findmnt -n -o SOURCE --target "${data_path}" 2>/dev/null || true)"
    fstype="$(findmnt -n -o FSTYPE --target "${data_path}" 2>/dev/null || true)"
    case "${fstype,,}" in
        nfs|nfs4|cifs|smb3)
            printf 'network\n'
            return
            ;;
    esac

    if [[ ${source} == /dev/* ]]; then
        transport="$(lsblk -ndo TRAN "${source}" 2>/dev/null | awk 'NF {print $1; exit}')"
        if [[ -z ${transport} ]]; then
            parent="$(lsblk -ndo PKNAME "${source}" 2>/dev/null | awk 'NF {print $1; exit}')"
            if [[ -n ${parent} ]]; then
                transport="$(lsblk -ndo TRAN "/dev/${parent}" 2>/dev/null | awk 'NF {print $1; exit}')"
            fi
        fi
        if [[ ${transport,,} == usb ]]; then
            printf 'external\n'
            return
        fi
        if [[ -n ${transport} || ${data_path} == /var/mnt/justvoxel-data/* ]]; then
            printf 'internal\n'
            return
        fi
    fi

    printf 'unknown\n'
}

jv_reset_backup_nested_in_data() {
    local data_path="$1" backup_path="$2"
    [[ -n ${data_path} && -n ${backup_path} ]] || return 1
    case "${backup_path}/" in
        "${data_path%/}/"*) return 0 ;;
        *) return 1 ;;
    esac
}

jv_reset_delete_internal_data() {
    local data_path="$1" backup_path="$2"

    [[ -n ${data_path} && ${data_path} != / && ${data_path} != /var && ${data_path} != /var/lib ]] || {
        echo 'ERROR: refusing unsafe Minecraft data path.' >&2
        return 1
    }
    [[ ! -L ${data_path} ]] || {
        echo 'ERROR: refusing to delete Minecraft data through a symbolic link.' >&2
        return 1
    }
    if jv_reset_backup_nested_in_data "${data_path}" "${backup_path}"; then
        echo 'ERROR: backup path is inside the Minecraft data path; move backups before resetting Minecraft.' >&2
        return 1
    fi
    [[ -d ${data_path} ]] || return 0

    find "${data_path}" -xdev -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
}

jv_reset_remove_active_configuration() {
    local java_port="$1" bedrock_enabled="$2" bedrock_port="$3"

    systemctl disable --now minecraft-backup.timer >/dev/null 2>&1 || true

    firewall-cmd --permanent --remove-port="${java_port}/tcp" >/dev/null 2>&1 || true
    if [[ ${bedrock_enabled} == yes ]]; then
        firewall-cmd --permanent --remove-port="${bedrock_port}/udp" >/dev/null 2>&1 || true
    fi
    firewall-cmd --reload >/dev/null 2>&1 || true

    rm -f -- "${JV_QUADLET}" "${JV_BACKUP_SERVICE}" "${JV_BACKUP_TIMER}"
    rm -rf -- "${JV_CONFIG_DIR}"
    rm -f -- "${JV_PREVIOUS_IMAGE_STATE}"
    systemctl daemon-reload
    systemctl reset-failed minecraft.service minecraft-backup.service minecraft-backup.timer >/dev/null 2>&1 || true
}
