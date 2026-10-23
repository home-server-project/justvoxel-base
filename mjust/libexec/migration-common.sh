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

jv_migration_running_minecraft_owns_port() {
    local proto="$1" port="$2" container_port="${3:-}" running mappings mapping
    case "${proto}" in
        tcp) container_port="${container_port:-25565}" ;;
        udp) container_port="${container_port:-19132}" ;;
        *) return 1 ;;
    esac
    [[ ${port} =~ ^[0-9]+$ ]] || return 1
    running="$(podman inspect --type container --format '{{.State.Running}}' "${JV_INSTANCE_CONTAINER:?}" 2>/dev/null)" || return 1
    [[ ${running} == true ]] || return 1
    mappings="$(podman port "${JV_INSTANCE_CONTAINER:?}" "${container_port}/${proto}" 2>/dev/null)" || return 1
    while IFS= read -r mapping; do
        [[ ${mapping} =~ ^.+:([0-9]+)$ && ${BASH_REMATCH[1]} == "${port}" ]] && return 0
    done <<< "${mappings}"
    return 1
}

jv_migration_check_import_bedrock_port() {
    local port="$1" old_enabled="$2" old_port="$3" proto
    for proto in tcp udp; do
        # Existing Geyser mappings may still be running after disabling cross-play.
        # Prove each protocol's actual 19132 mapping; an enabled config alone
        # does not prove the newly required TCP port belongs to Geyser.
        if jv_migration_running_minecraft_owns_port "${proto}" "${port}" 19132; then
            continue
        fi
        jv_migration_check_candidate_port "${proto}" "${port}" || return 1
    done
}

jv_migration_write_state() {
    local transaction="$1" phase="$2" source="$3" source_type="${4:-unknown}" tmp
    tmp="${transaction}/.state.tmp"
    {
        printf 'instance_id=%q\n' "${JV_INSTANCE_ID:?}"
        printf 'phase=%q\n' "${phase}"
        printf 'source=%q\n' "${source}"
        printf 'source_type=%q\n' "${source_type}"
        printf 'updated_at=%q\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    } > "${tmp}"
    chmod 0600 "${tmp}"
    mv -f -- "${tmp}" "${transaction}/state"
    sync -f "${transaction}/state"
}

# Portable import never changes appliance storage, backups or firewall ownership.
jv_migration_runtime_paths() {
    printf '%s\n' "/etc/justvoxel/instances/${JV_INSTANCE_ID:?}/config.json" "/etc/justvoxel/instances/${JV_INSTANCE_ID}/minecraft.env" "/etc/containers/systemd/minecraft-${JV_INSTANCE_ID}.container"
}
jv_migration_snapshot_runtime() {
    local transaction="$1" path
    install -d -m0700 "${transaction}/destination-state"
    while IFS= read -r path; do
        [[ -f ${path} && ! -L ${path} ]] || return 1
        cp -a -- "${path}" "${transaction}/destination-state/$(basename -- "${path}")" || return 1
    done < <(jv_migration_runtime_paths)
    jq -cn --arg id "${JV_INSTANCE_ID}" --arg data "${DATA_PATH}" --arg tx "${transaction}" \
        --arg config "$(sha256sum "${JV_MIGRATION_INSTANCE_CONFIG}" | awk '{print $1}')" \
        --arg data_identity "$(stat -Lc '%d:%i' "${DATA_PATH}")" \
        --arg staged "$(if [[ -d ${transaction}/staged-server ]]; then jv_migration_source_identity "${transaction}/staged-server"; elif [[ $(basename -- "${transaction}") == .justvoxel-export-* ]]; then printf export; else printf pending; fi)" \
        --arg source "${source_identity:-}" --argjson running "$([[ ${minecraft_was_active} == yes ]] && echo true || echo false)" \
        '{schema:1,instance_id:$id,data_path:$data,transaction:$tx,config_identity:$config,data_identity:$data_identity,staged_identity:$staged,source_identity:$source,was_running:$running}' > "${transaction}/.evidence.tmp"
    chmod 0600 "${transaction}/.evidence.tmp"
    sync -f "${transaction}/.evidence.tmp"
    mv -f -- "${transaction}/.evidence.tmp" "${transaction}/evidence.json"
    sync -f "${transaction}/evidence.json"
}
jv_migration_validate_evidence() {
    local transaction="$1" id parent
    parent="$(dirname -- "${DATA_PATH}")"
    [[ ${JV_INSTANCE_ID:-} =~ ^jv-[a-z0-9]{6,12}$ && $(dirname -- "${transaction}") == "${parent}" ]] || return 1
    case $(basename -- "${transaction}") in .justvoxel-import-*|.justvoxel-export-*) ;; *) return 1 ;; esac
    [[ -d ${transaction} && ! -L ${transaction} && $(realpath -e -- "${transaction}") == "${transaction}" ]] || return 1
    [[ -f ${transaction}/evidence.json && ! -L ${transaction}/evidence.json && $(stat -c '%u:%g:%a' "${transaction}/evidence.json") == 0:0:600 ]] || return 1
    jq -e --arg id "${JV_INSTANCE_ID}" --arg data "${DATA_PATH}" --arg tx "${transaction}" \
        '.schema==1 and .instance_id==$id and .data_path==$data and .transaction==$tx and (.was_running|type=="boolean") and (.data_identity|type=="string") and (.config_identity|test("^[a-f0-9]{64}$")) and (.staged_identity|type=="string")' "${transaction}/evidence.json" >/dev/null || return 1
    [[ $(sed -n 's/^instance_id=//p' "${transaction}/state") == "${JV_INSTANCE_ID}" ]] || return 1
    # Validate the complete selected snapshot before restoring any file.
    local path saved
    while IFS= read -r path; do
        saved="${transaction}/destination-state/$(basename -- "${path}")"
        [[ -f ${saved} && ! -L ${saved} && $(stat -c '%u:%g:%a' "${saved}") == 0:0:600 ]] || return 1
    done < <(jv_migration_runtime_paths)
    [[ $(sha256sum "${transaction}/destination-state/config.json" | awk '{print $1}') == $(jq -r '.config_identity' "${transaction}/evidence.json") ]] || return 1
    [[ $(sha256sum "${transaction}/destination-state/minecraft.env" | awk '{print $1}') == $(jq -r '.environment_sha256' "${transaction}/destination-state/config.json") ]] || return 1
    [[ $(sha256sum "${transaction}/destination-state/minecraft-${JV_INSTANCE_ID}.container" | awk '{print $1}') == $(jq -r '.quadlet_sha256' "${transaction}/destination-state/config.json") ]] || return 1
    jq -e --arg id "${JV_INSTANCE_ID}" --arg path "${DATA_PATH}" '.runtime.id==$id and .storage.data_path==$path' "${transaction}/destination-state/config.json" >/dev/null
}
jv_migration_restore_runtime() {
    local transaction="$1" path tmp
    jv_migration_validate_evidence "${transaction}" || return 1
    while IFS= read -r path; do
        tmp="$(mktemp "$(dirname -- "${path}")/.migration-recovery.XXXXXX")" || return 1
        cp -a -- "${transaction}/destination-state/$(basename -- "${path}")" "${tmp}" || return 1
        sync -f "${tmp}" || return 1
        mv -f -- "${tmp}" "${path}" || return 1
        sync -f "${path}" || return 1
    done < <(jv_migration_runtime_paths)
    systemctl daemon-reload || return 1
    jv_migration_load_instance "${JV_INSTANCE_ID}"
}
# Recovery is idempotent across interruption between either rename and state write.
jv_migration_recover_selected() {
    local transaction="$1" phase running current_identity
    jv_migration_validate_evidence "${transaction}" || return 1
    phase="$(sed -n 's/^phase=//p' "${transaction}/state")"
    running="$(jq -r '.was_running' "${transaction}/evidence.json")"
    if [[ $(basename -- "${transaction}") == .justvoxel-export-* ]]; then
        jv_migration_validate_data || return 1
        [[ $(stat -Lc '%d:%i' "${DATA_PATH}") == $(jq -r '.data_identity' "${transaction}/evidence.json") ]] || return 1
        if [[ ${running} == true ]]; then systemctl start "${JV_INSTANCE_UNIT}" || return 1; jv_migration_validate_runtime || return 1
        elif systemctl is-active --quiet "${JV_INSTANCE_UNIT}"; then return 1; fi
        return 0
    fi
    case "${phase}" in
        validated) return 0 ;;
        staging|prepared|ready-to-switch|switching|imported-active|validating|rollback|critical-rollback|rolled-back) ;;
        *) return 1 ;;
    esac
    systemctl stop "${JV_INSTANCE_UNIT}" || return 1
    if [[ -d ${transaction}/pre-import && ! -L ${transaction}/pre-import ]]; then
        [[ $(stat -Lc '%d:%i' "${transaction}/pre-import") == $(jq -r '.data_identity' "${transaction}/evidence.json") ]] || return 1
        if [[ -e ${DATA_PATH} ]]; then
            [[ -d ${DATA_PATH} && ! -L ${DATA_PATH} && ! -e ${transaction}/failed-import ]] || return 1
            mv -- "${DATA_PATH}" "${transaction}/failed-import" || return 1
        fi
        mv -- "${transaction}/pre-import" "${DATA_PATH}" || return 1
    else
        [[ -d ${DATA_PATH} && ! -L ${DATA_PATH} && $(stat -Lc '%d:%i' "${DATA_PATH}") == $(jq -r '.data_identity' "${transaction}/evidence.json") ]] || return 1
    fi
    jv_migration_restore_runtime "${transaction}" || return 1
    if ! systemctl start "${JV_INSTANCE_UNIT}"; then
        if [[ ${running} != true ]]; then systemctl stop "${JV_INSTANCE_UNIT}" || true; fi
        return 1
    fi
    local valid=yes
    jv_migration_validate_runtime || valid=no
    if [[ ${running} != true ]]; then systemctl stop "${JV_INSTANCE_UNIT}" || valid=no; fi
    [[ ${valid} == yes ]] || return 1
    jv_migration_write_state "${transaction}" rolled-back recovery recovery
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
