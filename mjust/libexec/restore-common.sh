#!/usr/bin/bash

jv_restore_list_archives() {
    local path="$1"
    find "${path}" -maxdepth 1 -type f -name 'minecraft-*.tar.gz' \
        -printf '%T@\t%s\t%p\n' 2>/dev/null \
        | sort -t $'\t' -k1,1nr
}

jv_restore_human_bytes() {
    local bytes="$1"
    numfmt --to=iec-i --suffix=B "${bytes}" 2>/dev/null || printf '%s bytes' "${bytes}"
}

jv_restore_archive_identity() {
    stat -Lc '%d:%i:%s:%Y' -- "$1"
}

jv_restore_version_relation() {
    local backup_mode="$1" backup_version="$2" current_mode="$3" current_version="$4" first

    if [[ ${backup_mode} != pinned || ${current_mode} != pinned ]]; then
        printf 'unknown'
        return 0
    fi
    if [[ ! ${backup_version} =~ ^[0-9][0-9A-Za-z._+-]*$ || ! ${current_version} =~ ^[0-9][0-9A-Za-z._+-]*$ ]]; then
        printf 'unknown'
        return 0
    fi
    if [[ ${backup_version} == "${current_version}" ]]; then
        printf 'same'
        return 0
    fi

    first="$(printf '%s\n%s\n' "${backup_version}" "${current_version}" | sort -V | head -n1)"
    if [[ ${first} == "${backup_version}" ]]; then
        printf 'backup_older'
    else
        printf 'backup_newer'
    fi
}

jv_restore_validate_data_layout() {
    local data_path="$1" target

    if [[ ! -d ${data_path} ]]; then
        echo "ERROR: Minecraft data directory does not exist: ${data_path}" >&2
        return 1
    fi
    if [[ -L ${data_path} ]]; then
        echo 'ERROR: restore requires DATA_PATH to be a real directory, not a symlink.' >&2
        return 1
    fi
    if mountpoint -q -- "${data_path}"; then
        echo 'ERROR: restore requires DATA_PATH to be a directory inside its filesystem, not the filesystem mount point itself.' >&2
        echo 'Migrate Minecraft data to a normal subdirectory first, then retry restore.' >&2
        return 1
    fi

    while IFS= read -r target; do
        [[ -n ${target} ]] || continue
        if [[ ${target} == "${data_path}/"* ]]; then
            echo "ERROR: restore refuses DATA_PATH with a nested mount: ${target}" >&2
            return 1
        fi
    done < <(findmnt -rn -o TARGET 2>/dev/null || true)
}

jv_restore_require_space() {
    local path="$1" content_bytes="$2" available required
    available="$(df -B1 --output=avail -- "${path}" 2>/dev/null | tail -n1 | tr -d ' ')"
    [[ ${available} =~ ^[0-9]+$ ]] || {
        echo 'ERROR: could not determine free space for restore staging.' >&2
        return 1
    }

    required=$((content_bytes + content_bytes / 20 + 64 * 1024 * 1024))
    if (( available < required )); then
        echo "ERROR: not enough free space to prepare the selected restore safely." >&2
        echo "Required staging space: $(jv_restore_human_bytes "${required}")" >&2
        echo "Available space:        $(jv_restore_human_bytes "${available}")" >&2
        return 1
    fi
}

jv_restore_write_state() {
    local transaction="$1" phase="$2" mode="$3" archive="$4"
    local tmp="${transaction}/.state.tmp"
    {
        printf 'phase=%q\n' "${phase}"
        printf 'mode=%q\n' "${mode}"
        printf 'archive=%q\n' "${archive}"
        printf 'data_path=%q\n' "${DATA_PATH:-}"
        printf 'updated_at=%q\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    } > "${tmp}"
    chmod 0600 "${tmp}"
    mv -f -- "${tmp}" "${transaction}/state"
}
