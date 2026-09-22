#!/usr/bin/bash

readonly JV_MIGRATION_SCHEMA='org.justvoxel.migration'
readonly JV_MIGRATION_SCHEMA_VERSION=1
readonly JV_MIGRATION_RUN_ROOT=/run/justvoxel-migration
readonly JV_MIGRATION_RECOVERY_ROOT=/var/lib/justvoxel/management/migration-recovery

jv_migration_recovery_key() {
    printf '%s' "$1" | sha256sum | awk '{print $1}'
}

jv_migration_register_recovery() {
    local transaction="$1" canonical key tmp
    canonical="$(realpath -m -- "${transaction}")"
    [[ -d ${canonical} && ! -L ${canonical} && $(basename -- "${canonical}") == .justvoxel-import-* && -r ${canonical}/state ]] || return 1
    install -d -m0700 -o root -g root "${JV_MIGRATION_RECOVERY_ROOT}"
    key="$(jv_migration_recovery_key "${canonical}")"
    tmp="$(mktemp "${JV_MIGRATION_RECOVERY_ROOT}/.recovery.XXXXXX")"
    chmod 0600 "${tmp}"
    printf '%s\n' "${canonical}" > "${tmp}"
    mv -f -- "${tmp}" "${JV_MIGRATION_RECOVERY_ROOT}/${key}"
}

jv_migration_clear_recovery() {
    local transaction="$1" canonical key
    canonical="$(realpath -m -- "${transaction}")"
    key="$(jv_migration_recovery_key "${canonical}")"
    rm -f -- "${JV_MIGRATION_RECOVERY_ROOT}/${key}"
}

jv_migration_registered_recoveries() {
    local record transaction canonical
    [[ -d ${JV_MIGRATION_RECOVERY_ROOT} && ! -L ${JV_MIGRATION_RECOVERY_ROOT} ]] || return 0
    while IFS= read -r -d '' record; do
        [[ -f ${record} && ! -L ${record} ]] || continue
        IFS= read -r transaction < "${record}" || continue
        [[ ${transaction} == /* ]] || continue
        canonical="$(realpath -m -- "${transaction}")"
        [[ ${canonical} == "${transaction}" && -d ${canonical} && ! -L ${canonical} && $(basename -- "${canonical}") == .justvoxel-import-* && -r ${canonical}/state ]] || continue
        printf '%s\n' "${canonical}"
    done < <(find "${JV_MIGRATION_RECOVERY_ROOT}" -maxdepth 1 -type f -print0 2>/dev/null)
}

jv_migration_human_bytes() {
    numfmt --to=iec-i --suffix=B "$1" 2>/dev/null || printf '%s bytes' "$1"
}

jv_migration_bool_to_yesno() {
    case "${1,,}" in
        true|yes|1|on) printf 'yes' ;;
        false|no|0|off) printf 'no' ;;
        *) return 1 ;;
    esac
}

jv_migration_bool_to_env() {
    case "${1,,}" in
        true|yes|1|on) printf 'TRUE' ;;
        false|no|0|off) printf 'FALSE' ;;
        *) return 1 ;;
    esac
}

jv_migration_source_identity() {
    local path="$1"
    if [[ -d ${path} ]]; then
        python3 - "${path}" <<'PYID'
import hashlib, os, stat, sys
base=os.path.realpath(sys.argv[1])
h=hashlib.sha256()
for root, dirs, files in os.walk(base, topdown=True, followlinks=False):
    dirs.sort(); files.sort()
    for name in dirs + files:
        full=os.path.join(root,name)
        rel=os.path.relpath(full,base).replace(os.sep,'/')
        st=os.lstat(full)
        target=os.readlink(full) if stat.S_ISLNK(st.st_mode) else ''
        row=f"{rel}\0{st.st_mode}\0{st.st_size}\0{st.st_mtime_ns}\0{target}\n"
        h.update(row.encode('utf-8','surrogateescape'))
print('dirsha256:'+h.hexdigest())
PYID
    else
        stat -Lc 'file:%d:%i:%s:%Y:%Z' -- "${path}"
    fi
}

jv_migration_available_bytes() {
    local path="$1" available
    available="$(df -B1 --output=avail -- "${path}" 2>/dev/null | tail -n1 | tr -d ' ')"
    [[ ${available} =~ ^[0-9]+$ ]] || return 1
    printf '%s' "${available}"
}

jv_migration_require_staging_space() {
    local path="$1" expanded="$2" available required
    [[ ${expanded} =~ ^[0-9]+$ ]] || {
        echo 'ERROR: invalid expanded source size.' >&2
        return 1
    }
    available="$(jv_migration_available_bytes "${path}")" || {
        echo "ERROR: could not determine free space for ${path}." >&2
        return 1
    }
    required=$(( expanded + expanded / 20 + 64 * 1024 * 1024 ))
    if (( available < required )); then
        echo 'ERROR: not enough free space to stage the migration safely.' >&2
        echo "Required:  $(jv_migration_human_bytes "${required}")" >&2
        echo "Available: $(jv_migration_human_bytes "${available}")" >&2
        return 1
    fi
}

jv_migration_port_in_use() {
    local proto="$1" port="$2"
    case "${proto}" in
        tcp)
            ss -H -ltn 2>/dev/null | awk -v p=":${port}" '$4 ~ p"$" {found=1} END {exit !found}'
            ;;
        udp)
            ss -H -lun 2>/dev/null | awk -v p=":${port}" '$4 ~ p"$" {found=1} END {exit !found}'
            ;;
        *) return 2 ;;
    esac
}

jv_migration_check_candidate_port() {
    local proto="$1" port="$2" old_port="${3:-}"
    [[ ${port} == "${old_port}" ]] && return 0
    if jv_migration_port_in_use "${proto}" "${port}"; then
        echo "ERROR: destination ${proto^^} port ${port} is already in use." >&2
        return 1
    fi
}

jv_migration_write_state() {
    local transaction="$1" phase="$2" source="$3" source_type="${4:-unknown}" tmp
    tmp="${transaction}/.state.tmp"
    {
        printf 'phase=%q\n' "${phase}"
        printf 'source=%q\n' "${source}"
        printf 'source_type=%q\n' "${source_type}"
        printf 'updated_at=%q\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    } > "${tmp}"
    chmod 0600 "${tmp}"
    mv -f -- "${tmp}" "${transaction}/state"
}

jv_migration_runtime_paths() {
    cat <<'PATHS'
/etc/justvoxel/justvoxel.conf
/etc/justvoxel/minecraft.env
/etc/justvoxel/minecraft-backup.env
/etc/containers/systemd/minecraft.container
/etc/systemd/system/minecraft-backup.service
/etc/systemd/system/minecraft-backup.timer
PATHS
}

jv_migration_snapshot_runtime() {
    local transaction="$1" old_java="$2" old_bedrock_enabled="$3" old_bedrock="$4" candidate_java="$5" candidate_bedrock_enabled="$6" candidate_bedrock="$7"
    local state_dir path rel enabled active proto port exists
    state_dir="${transaction}/destination-state"
    install -d -m0700 -o root -g root "${state_dir}/files"
    : > "${state_dir}/files.tsv"

    while IFS= read -r path; do
        [[ -n ${path} ]] || continue
        rel="${path#/}"
        if [[ -e ${path} || -L ${path} ]]; then
            printf 'present\t%s\n' "${path}" >> "${state_dir}/files.tsv"
            install -d -m0700 -o root -g root "${state_dir}/files/$(dirname -- "${rel}")"
            cp -a -- "${path}" "${state_dir}/files/${rel}"
        else
            printf 'absent\t%s\n' "${path}" >> "${state_dir}/files.tsv"
        fi
    done < <(jv_migration_runtime_paths)

    enabled="$(systemctl is-enabled minecraft-backup.timer 2>/dev/null || true)"
    active="$(systemctl is-active minecraft-backup.timer 2>/dev/null || true)"
    printf '%s\n' "${enabled}" > "${state_dir}/backup-timer.enabled"
    printf '%s\n' "${active}" > "${state_dir}/backup-timer.active"

    : > "${state_dir}/firewall.tsv"
    declare -A seen=()
    for spec in "tcp:${old_java}" "udp:${old_bedrock}" "tcp:${candidate_java}" "udp:${candidate_bedrock}"; do
        proto="${spec%%:*}"
        port="${spec#*:}"
        [[ ${port} =~ ^[0-9]+$ ]] || continue
        if [[ ${proto} == udp ]]; then
            if [[ ${port} == "${old_bedrock}" && ${old_bedrock_enabled} != yes && ${port} != "${candidate_bedrock}" ]]; then continue; fi
            if [[ ${port} == "${candidate_bedrock}" && ${candidate_bedrock_enabled} != yes && ${port} != "${old_bedrock}" ]]; then continue; fi
            if [[ ${port} == "${old_bedrock}" && ${port} == "${candidate_bedrock}" && ${old_bedrock_enabled} != yes && ${candidate_bedrock_enabled} != yes ]]; then continue; fi
        fi
        [[ -n ${seen[${proto}:${port}]:-} ]] && continue
        seen["${proto}:${port}"]=1
        if firewall-cmd --permanent --query-port="${port}/${proto}" >/dev/null 2>&1; then exists=1; else exists=0; fi
        printf '%s\t%s\t%s\n' "${proto}" "${port}" "${exists}" >> "${state_dir}/firewall.tsv"
    done
}

jv_migration_restore_firewall() {
    local transaction="$1" proto port expected
    local state_dir="${transaction}/destination-state"
    [[ -r ${state_dir}/firewall.tsv ]] || return 0
    while IFS=$'\t' read -r proto port expected; do
        [[ -n ${proto} && ${port} =~ ^[0-9]+$ ]] || continue
        if [[ ${expected} == 1 ]]; then
            firewall-cmd --permanent --add-port="${port}/${proto}" >/dev/null 2>&1 || return 1
        else
            firewall-cmd --permanent --remove-port="${port}/${proto}" >/dev/null 2>&1 || true
        fi
    done < "${state_dir}/firewall.tsv"
    firewall-cmd --reload >/dev/null 2>&1
}

jv_migration_restore_runtime() {
    local transaction="$1" state_dir status path rel timer_enabled timer_active
    state_dir="${transaction}/destination-state"
    [[ -r ${state_dir}/files.tsv ]] || {
        echo 'ERROR: destination runtime snapshot is missing.' >&2
        return 1
    }

    systemctl stop minecraft.service >/dev/null 2>&1 || true
    systemctl disable --now minecraft-backup.timer >/dev/null 2>&1 || true

    while IFS=$'\t' read -r status path; do
        [[ -n ${path} ]] || continue
        rel="${path#/}"
        if [[ ${status} == present ]]; then
            install -d -m0755 -o root -g root "$(dirname -- "${path}")"
            rm -rf -- "${path}"
            cp -a -- "${state_dir}/files/${rel}" "${path}"
        else
            rm -rf -- "${path}"
        fi
    done < "${state_dir}/files.tsv"

    systemctl daemon-reload
    timer_enabled="$(cat "${state_dir}/backup-timer.enabled" 2>/dev/null || true)"
    timer_active="$(cat "${state_dir}/backup-timer.active" 2>/dev/null || true)"
    if [[ ${timer_enabled} == enabled ]]; then
        systemctl enable minecraft-backup.timer >/dev/null 2>&1 || return 1
    else
        systemctl disable minecraft-backup.timer >/dev/null 2>&1 || true
    fi
    if [[ ${timer_active} == active ]]; then
        systemctl start minecraft-backup.timer >/dev/null 2>&1 || return 1
    else
        systemctl stop minecraft-backup.timer >/dev/null 2>&1 || true
    fi

    jv_migration_restore_firewall "${transaction}"
}

jv_migration_remove_fresh_container() {
    systemctl stop minecraft.service >/dev/null 2>&1 || true
    podman rm -f minecraft >/dev/null 2>&1 || true
}

jv_migration_selinux_pattern() {
    local path="$1" escaped
    escaped="$(printf '%s' "${path}" | sed 's/[][\\.^$*+?(){}|]/\\&/g')"
    printf '%s(/.*)?' "${escaped}"
}

jv_migration_assert_fresh_selinux_path() {
    local path="$1" pattern
    pattern="$(jv_migration_selinux_pattern "${path}")"
    if semanage fcontext -l -C 2>/dev/null | awk -v p="${pattern}" '$1 == p {found=1} END {exit !found}'; then
        echo "ERROR: fresh import destination already has a local SELinux fcontext rule: ${pattern}" >&2
        echo 'Choose another Minecraft data path or remove/reconcile that administrator rule manually before import.' >&2
        return 1
    fi
}

jv_migration_remove_fresh_selinux_rule() {
    local path="$1" pattern
    pattern="$(jv_migration_selinux_pattern "${path}")"
    semanage fcontext -d "${pattern}" >/dev/null 2>&1 || true
}

jv_migration_apply_candidate_firewall() {
    local old_java="$1" old_bedrock_enabled="$2" old_bedrock="$3"
    update_firewall_ports "${old_java}" "${old_bedrock_enabled}" "${old_bedrock}"
}

jv_migration_manifest_field() {
    local manifest="$1" expression="$2"
    jq -r "${expression} // empty" <<< "${manifest}"
}

jv_migration_validate_setting_values() {
    case "${GAME_MODE}" in
        survival|creative|adventure|spectator) ;;
        *) echo "ERROR: unsupported imported game mode: ${GAME_MODE}" >&2; return 1 ;;
    esac
    case "${DIFFICULTY}" in
        peaceful|easy|normal|hard|0|1|2|3) ;;
        *) echo "ERROR: unsupported imported difficulty: ${DIFFICULTY}" >&2; return 1 ;;
    esac
    [[ ${WHITELIST_ENABLED} == yes || ${WHITELIST_ENABLED} == no ]] || return 1
    [[ ${ENFORCE_WHITELIST} == yes || ${ENFORCE_WHITELIST} == no ]] || return 1
}

jv_migration_normalize_staged_runtime_files() {
    local root="$1" bedrock_enabled="$2"
    python3 - "${root}" "${bedrock_enabled}" <<'PY'
import os
import re
import sys

root, bedrock = sys.argv[1:3]
props_path = os.path.join(root, "server.properties")

if not os.path.isfile(props_path):
    print("ERROR: staged server.properties is missing", file=sys.stderr)
    raise SystemExit(1)

with open(props_path, "r", encoding="iso-8859-1", errors="replace") as f:
    lines = f.read().splitlines()

replacements = {
    "server-ip": "",
    "server-port": "25565",
    "enable-rcon": "true",
    "rcon.port": "25575",
    "rcon.password": "",
}
seen = set()
out = []
for line in lines:
    stripped = line.strip()
    if not stripped or stripped.startswith(("#", "!")) or "=" not in line:
        out.append(line)
        continue
    key = line.split("=", 1)[0].strip()
    if key in replacements:
        out.append(f"{key}={replacements[key]}")
        seen.add(key)
    else:
        out.append(line)
for key, value in replacements.items():
    if key not in seen:
        out.append(f"{key}={value}")
with open(props_path, "w", encoding="iso-8859-1", newline="\n") as f:
    f.write("\n".join(out) + "\n")

if bedrock == "yes":
    geyser = os.path.join(root, "plugins", "Geyser-Spigot", "config.yml")
    if os.path.isfile(geyser):
        with open(geyser, "r", encoding="utf-8", errors="replace") as f:
            glines = f.read().splitlines()
        in_bedrock = False
        bedrock_indent = 0
        replaced = False
        for i, line in enumerate(glines):
            stripped = line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            indent = len(line) - len(line.lstrip(" "))
            if stripped == "bedrock:":
                in_bedrock = True
                bedrock_indent = indent
                continue
            if in_bedrock and indent <= bedrock_indent:
                in_bedrock = False
            if in_bedrock and re.match(r"port:\s*[^ ]+", stripped):
                prefix = line[: len(line) - len(line.lstrip(" "))]
                comment = ""
                if "#" in line:
                    comment = " #" + line.split("#", 1)[1]
                glines[i] = f"{prefix}port: 19132{comment}"
                replaced = True
                break
        if not replaced:
            print("ERROR: Geyser is enabled but its bedrock.port could not be normalized safely", file=sys.stderr)
            raise SystemExit(1)
        with open(geyser, "w", encoding="utf-8", newline="\n") as f:
            f.write("\n".join(glines) + "\n")
PY
}

jv_migration_activate_data() {
    local transaction="$1" data_path="$2" staged_server="$3" had_existing="$4"
    [[ -d ${staged_server} ]] || { echo 'ERROR: staged server data is missing.' >&2; return 1; }
    if [[ ${had_existing} == yes ]]; then
        [[ -d ${data_path} ]] || { echo 'ERROR: existing destination data is missing.' >&2; return 1; }
        [[ ! -e ${transaction}/pre-import ]] || { echo 'ERROR: pre-import safety path already exists.' >&2; return 1; }
        mv -- "${data_path}" "${transaction}/pre-import" || return 1
    else
        if [[ -d ${data_path} ]]; then
            if find "${data_path}" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
                echo "ERROR: fresh destination is not empty: ${data_path}" >&2
                return 1
            fi
            rmdir -- "${data_path}" || return 1
        elif [[ -e ${data_path} ]]; then
            echo "ERROR: fresh destination path exists and is not a directory: ${data_path}" >&2
            return 1
        fi
    fi
    mv -- "${staged_server}" "${data_path}"
}

jv_migration_rollback_data() {
    local transaction="$1" data_path="$2" had_existing="$3"
    if [[ -e ${data_path} ]]; then
        [[ ! -e ${transaction}/failed-import ]] || {
            echo 'ERROR: failed-import recovery path already exists.' >&2
            return 1
        }
        mv -- "${data_path}" "${transaction}/failed-import" || return 1
    fi
    if [[ ${had_existing} == yes ]]; then
        [[ -d ${transaction}/pre-import ]] || {
            echo 'ERROR: pre-import safety copy is missing.' >&2
            return 1
        }
        [[ ! -e ${data_path} ]] || return 1
        mv -- "${transaction}/pre-import" "${data_path}" || return 1
    fi
}

jv_migration_validate_source_name() {
    if [[ $(basename -- "$1") == *.partial ]]; then
        echo 'ERROR: .partial files are incomplete migration/export artifacts and cannot be imported.' >&2
        return 1
    fi
}

jv_migration_online_mode_state() {
    local manifest="${1:-unknown}" properties="${2:-unknown}"
    if [[ ${manifest} != unknown && ${properties} != unknown && ${manifest} != "${properties}" ]]; then
        printf 'conflict'
    elif [[ ${manifest} == false || ${properties} == false ]]; then
        printf 'offline'
    elif [[ ${manifest} == true || ${properties} == true ]]; then
        printf 'online'
    else
        printf 'unknown'
    fi
}

jv_migration_version_resolution() {
    local manifest="${1:-}" detected="${2:-}"
    if [[ -n ${manifest} && -n ${detected} && ${manifest} != "${detected}" ]]; then
        printf 'conflict'
        return 1
    fi
    if [[ -n ${manifest} ]]; then
        printf '%s' "${manifest}"
        return 0
    fi
    if [[ -n ${detected} ]]; then
        printf '%s' "${detected}"
        return 0
    fi
    printf 'unknown'
    return 2
}
