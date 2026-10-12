#!/usr/bin/bash

# Generic permanent mounts: shared ownership evidence and exact-row operations.
STORAGE_MOUNT_FSTAB="${STORAGE_MOUNT_FSTAB:-/etc/fstab}"
STORAGE_MOUNT_STATE_DIR="${STORAGE_MOUNT_STATE_DIR:-/etc/justvoxel/storage-mounts}"

storage_mount_passno() {
    [[ $1 == ext4 ]] && printf '2\n' || printf '0\n'
}

storage_mount_fstab_line() {
    local uuid="$1" mountpoint="$2" filesystem="$3" mount_type mount_options
    mount_type="$(storage_action_mount_type "${filesystem}")" || return 1
    mount_options="$(storage_action_mount_options "${filesystem}")" || return 1
    printf 'UUID=%s %s %s %s,nofail,x-systemd.device-timeout=10s 0 %s\n' \
        "${uuid}" "${mountpoint}" "${mount_type}" "${mount_options}" "$(storage_mount_passno "${filesystem}")"
}

storage_mount_state_path() {
    [[ $1 =~ ^[A-Za-z0-9._-]+$ ]] || return 1
    printf '%s/%s.conf\n' "${STORAGE_MOUNT_STATE_DIR}" "$1"
}

storage_mount_load_state() {
    local uuid="$1" state
    state="$(storage_mount_state_path "${uuid}")" || return 1
    [[ -f ${state} && -r ${state} && ! -L ${state} && ! -L ${STORAGE_MOUNT_STATE_DIR} ]] || return 1
    awk -F= '
        NF < 2 { bad=1; next }
        { if (++seen[$1] != 1) bad=1 }
        $1 != "schema" && $1 != "uuid" && $1 != "mount_point" && $1 != "filesystem" && $1 != "created_dir" { bad=1 }
        END { exit (bad || length(seen) != 5) ? 1 : 0 }
    ' "${state}" || return 1
    MOUNT_STATE_SCHEMA="$(awk -F= '$1=="schema"{print substr($0,index($0,"=")+1)}' "${state}")"
    MOUNT_STATE_UUID="$(awk -F= '$1=="uuid"{print substr($0,index($0,"=")+1)}' "${state}")"
    MOUNT_STATE_MOUNT="$(awk -F= '$1=="mount_point"{print substr($0,index($0,"=")+1)}' "${state}")"
    MOUNT_STATE_FILESYSTEM="$(awk -F= '$1=="filesystem"{print substr($0,index($0,"=")+1)}' "${state}")"
    MOUNT_STATE_CREATED="$(awk -F= '$1=="created_dir"{print substr($0,index($0,"=")+1)}' "${state}")"
    [[ ${MOUNT_STATE_SCHEMA} == v1 && ${MOUNT_STATE_UUID} == "${uuid}" ]] || return 1
    storage_validate_mountpoint_path "${MOUNT_STATE_MOUNT}" >/dev/null 2>&1 || return 1
    [[ $(realpath -m -- "${MOUNT_STATE_MOUNT}") == "${MOUNT_STATE_MOUNT}" ]] || return 1
    storage_action_mountable_filesystem "${MOUNT_STATE_FILESYSTEM}" || return 1
    [[ ${MOUNT_STATE_CREATED} == true || ${MOUNT_STATE_CREATED} == false ]] || return 1
}

storage_mount_fstab_matches() {
    local uuid="$1" mountpoint="$2"
    [[ -r ${STORAGE_MOUNT_FSTAB} ]] || return 0
    awk -v source="UUID=${uuid}" -v mount="${mountpoint}" '
        /^[[:space:]]*#/ || NF < 2 { next }
        $1 == source || (mount != "" && $2 == mount) { print }
    ' "${STORAGE_MOUNT_FSTAB}"
}

storage_mount_first_fstab_target() {
    local uuid="$1"
    [[ -r ${STORAGE_MOUNT_FSTAB} ]] || return 0
    awk -v source="UUID=${uuid}" '
        /^[[:space:]]*#/ || NF < 2 { next }
        $1 == source { print $2; exit }
    ' "${STORAGE_MOUNT_FSTAB}"
}

storage_mount_ownership() {
    local uuid="$1" requested_mount="$2" filesystem="$3" matches expected count state
    [[ ! -L ${STORAGE_MOUNT_FSTAB} ]] || { printf 'conflict\n'; return 0; }
    state="$(storage_mount_state_path "${uuid}")" || { printf 'conflict\n'; return 0; }
    if [[ -e ${state} || -L ${state} ]]; then
        if ! storage_mount_load_state "${uuid}"; then
            printf 'conflict\n'
            return 0
        fi
        storage_validate_mountpoint_path "${MOUNT_STATE_MOUNT}" >/dev/null 2>&1 || { printf 'conflict\n'; return 0; }
        [[ ${MOUNT_STATE_FILESYSTEM} == "${filesystem}" ]] || { printf 'conflict\n'; return 0; }
        expected="$(storage_mount_fstab_line "${uuid}" "${MOUNT_STATE_MOUNT}" "${filesystem}")"
        matches="$(storage_mount_fstab_matches "${uuid}" "${MOUNT_STATE_MOUNT}")"
        count="$(sed '/^$/d' <<< "${matches}" | wc -l)"
        [[ ${count} -eq 1 && ${matches} == "${expected}" ]] || { printf 'conflict\n'; return 0; }
        if [[ -n ${requested_mount} && ${requested_mount} != "${MOUNT_STATE_MOUNT}" ]]; then
            printf 'other_mount\n'
        else
            printf 'justvoxel\n'
        fi
        return 0
    fi
    if [[ -n $(storage_mount_fstab_matches "${uuid}" "${requested_mount}") ]]; then
        printf 'external\n'
    else
        printf 'none\n'
    fi
}

storage_mount_write_state() {
    local uuid="$1" mountpoint="$2" filesystem="$3" created="$4" state tmp
    state="$(storage_mount_state_path "${uuid}")" || return 1
    install -d -m0700 -o root -g root "${STORAGE_MOUNT_STATE_DIR}" || return 1
    tmp="$(mktemp "${STORAGE_MOUNT_STATE_DIR}/.mount.XXXXXX")" || return 1
    printf 'schema=v1\nuuid=%s\nmount_point=%s\nfilesystem=%s\ncreated_dir=%s\n' \
        "${uuid}" "${mountpoint}" "${filesystem}" "${created}" > "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    install -o root -g root -m0600 "${tmp}" "${state}" || { rm -f -- "${tmp}"; return 1; }
    rm -f -- "${tmp}"
}

storage_mount_append_fstab() {
    local line="$1" tmp
    [[ -e ${STORAGE_MOUNT_FSTAB} ]] || : > "${STORAGE_MOUNT_FSTAB}"
    tmp="$(mktemp "$(dirname -- "${STORAGE_MOUNT_FSTAB}")/.fstab.justvoxel-mount.XXXXXX")" || return 1
    cat -- "${STORAGE_MOUNT_FSTAB}" > "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    [[ ! -s ${tmp} ]] || printf '\n' >> "${tmp}"
    printf '%s\n' "${line}" >> "${tmp}" || { rm -f -- "${tmp}"; return 1; }
    install -o root -g root -m0644 "${tmp}" "${STORAGE_MOUNT_FSTAB}" || { rm -f -- "${tmp}"; return 1; }
    rm -f -- "${tmp}"
}

storage_mount_remove_fstab_line() {
    local line="$1" tmp removed
    tmp="$(mktemp "$(dirname -- "${STORAGE_MOUNT_FSTAB}")/.fstab.justvoxel-mount.XXXXXX")" || return 1
    removed="$(mktemp /tmp/justvoxel-mount-removed.XXXXXX)" || { rm -f -- "${tmp}"; return 1; }
    awk -v expected="${line}" -v removed="${removed}" '
        $0 == expected && !done { print "yes" > removed; done=1; next }
        { print }
    ' "${STORAGE_MOUNT_FSTAB}" > "${tmp}" || { rm -f -- "${tmp}" "${removed}"; return 1; }
    grep -Fqx yes "${removed}" || { rm -f -- "${tmp}" "${removed}"; return 1; }
    rm -f -- "${removed}"
    install -o root -g root -m0644 "${tmp}" "${STORAGE_MOUNT_FSTAB}" || { rm -f -- "${tmp}"; return 1; }
    rm -f -- "${tmp}"
}

# Scan all surviving generic evidence; malformed records cannot establish safety.
storage_mount_records() (
    local record uuid
    shopt -s dotglob nullglob
    [[ ! -L ${STORAGE_MOUNT_STATE_DIR} ]] || return 1
    [[ -e ${STORAGE_MOUNT_STATE_DIR} ]] || return 0
    [[ -d ${STORAGE_MOUNT_STATE_DIR} ]] || return 1
    for record in "${STORAGE_MOUNT_STATE_DIR}"/*; do
        [[ -e ${record} || -L ${record} ]] || continue
        [[ ${record} == *.conf ]] || return 1
        uuid="${record##*/}"; uuid="${uuid%.conf}"
        storage_mount_load_state "${uuid}" || return 1
        printf '%s\n' "${uuid}"
    done
)

# Reading the host mount table must succeed before absence is trusted. Boolean
# mountpoint probes alone cannot distinguish an unused path from a probe failure.
storage_mount_point_state() {
    local table
    table="$(nsenter --mount=/proc/1/ns/mnt -- cat /proc/self/mountinfo)" || return 1
    [[ -n ${table} ]] || return 1
    awk -v point="$1" '$5 == point {found=1} END {print found ? "mounted" : "unmounted"}' <<< "${table}"
}

# blkid exit 2 proves absence; other errors are not absence. Bypass its cache.
storage_mount_uuid_absent() {
    local output rc
    if output="$(blkid -c /dev/null -t "UUID=$1" -o device 2>/dev/null)"; then
        return 1
    else
        rc=$?
        [[ ${rc} == 2 && -z ${output} ]]
    fi
}

storage_mount_unique_device() {
    local devices
    devices="$(blkid -c /dev/null -t "UUID=$1" -o device 2>/dev/null)" || return 1
    [[ -n ${devices} && ${devices} != *$'\n'* ]] || return 1
    [[ $(storage_action_real_device "${devices}") == "$2" ]]
}

# This is compatibility evidence for one explicit persist request, never ownership
# classification or boot-time cleanup. Only the exact native historical form is
# accepted, with a missing UUID and no contradictory surviving selection evidence.
storage_mount_prove_orphan() {
    local device="$1" uuid="$2" point="$3" filesystem="$4"
    local matches source old expected records record mounts
    STORAGE_MOUNT_ORPHAN_LINE=''
    [[ -f ${STORAGE_MOUNT_FSTAB} && ! -L ${STORAGE_MOUNT_FSTAB} ]] || return 1
    [[ ! -e ${JV_SETUP_IN_PROGRESS} && ! -L ${JV_SETUP_IN_PROGRESS} ]] || return 1
    # If configuration exists, its separate storage ownership must have loaded.
    [[ ! -e ${JV_CONFIG} && ! -L ${JV_CONFIG} || ${STORAGE_ACTION_CONFIGURED} == true ]] || return 1
    storage_action_validate_target "${device}" >/dev/null 2>&1 || return 1
    [[ $(storage_action_uuid "${device}") == "${uuid}" && $(storage_action_filesystem "${device}") == "${filesystem}" ]] || return 1
    storage_action_managed_filesystem "${filesystem}" || return 1
    storage_validate_mountpoint_path "${point}" >/dev/null 2>&1 || return 1
    [[ $(realpath -m -- "${point}") == "${point}" ]] || return 1
    case "${point}" in /var/mnt/justvoxel-data|/var/mnt/justvoxel-backup) return 1 ;; esac
    [[ $(storage_mount_point_state "${point}") == unmounted ]] || return 1
    mounts="$(storage_action_host_mountpoints "${device}")" || return 1
    [[ -z ${mounts} ]] || return 1
    storage_mount_unique_device "${uuid}" "${device}" || return 1
    matches="$(storage_mount_fstab_matches "${uuid}" "${point}")" || return 1
    [[ -n ${matches} && ${matches} != *$'\n'* ]] || return 1
    read -r source _ <<< "${matches}"
    [[ ${source} == UUID=* ]] || return 1
    old="${source#UUID=}"
    [[ ${old} != "${uuid}" && ${old} =~ ^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}$ ]] || return 1
    expected="$(storage_mount_fstab_line "${old}" "${point}" "${filesystem}")" || return 1
    [[ ${matches} == "${expected}" && $(storage_mount_fstab_matches "${old}" "${point}") == "${expected}" ]] || return 1
    storage_mount_uuid_absent "${old}" || return 1
    records="$(storage_mount_records)" || return 1
    while IFS= read -r record; do
        [[ -n ${record} ]] || continue
        storage_mount_load_state "${record}" || return 1
        [[ ${record} != "${old}" && ${record} != "${uuid}" && ${MOUNT_STATE_MOUNT} != "${point}" ]] || return 1
    done <<< "${records}"
    # Setup/data/backup selections are separate ownership mechanisms.
    [[ ${DATA_EXPECTED_UUID:-} != "${old}" && ${BACKUP_EXPECTED_UUID:-} != "${old}" &&
       ${DATA_EXPECTED_SOURCE:-} != "${source}" && ${BACKUP_EXPECTED_SOURCE:-} != "${source}" &&
       ${DATA_MOUNT_POINT:-} != "${point}" && ${BACKUP_MOUNT_POINT:-} != "${point}" ]] || return 1
    STORAGE_MOUNT_ORPHAN_LINE="${expected}"
}

storage_mount_verify_record() {
    local uuid="$1" point records record
    storage_mount_load_state "${uuid}" || return 1
    point="${MOUNT_STATE_MOUNT}"
    [[ $(storage_mount_ownership "${uuid}" "${point}" "${MOUNT_STATE_FILESYSTEM}") == justvoxel ]] || return 1
    records="$(storage_mount_records)" || return 1
    while IFS= read -r record; do
        [[ -n ${record} && ${record} != "${uuid}" ]] || continue
        storage_mount_load_state "${record}" || return 1
        [[ ${MOUNT_STATE_MOUNT} != "${point}" ]] || return 1
    done <<< "${records}"
    storage_mount_load_state "${uuid}"
}

storage_mount_verify_live() {
    local uuid="$1" point="$2" device="$3" live
    [[ $(storage_action_host_mount_identity UUID "${point}") == "${uuid}" ]] || return 1
    live="$(storage_action_host_mount_identity SOURCE "${point}")" || return 1
    [[ $(storage_action_real_device "${live}") == "${device}" ]]
}

# Retire a proven record before filesystem destruction (or reset). Roll back
# persistence and the live mount if retirement fails; never destroy on failure.
storage_mount_retire_record() {
    local uuid="$1" device="$2" point filesystem state line snapshot occupancy was_mounted=false ok=true
    storage_mount_verify_record "${uuid}" || return 1
    point="${MOUNT_STATE_MOUNT}"; filesystem="${MOUNT_STATE_FILESYSTEM}"
    if [[ -n ${device} ]]; then
        storage_mount_unique_device "${uuid}" "${device}" || return 1
        [[ $(storage_action_filesystem "${device}") == "${filesystem}" ]] || return 1
    else
        storage_mount_uuid_absent "${uuid}" || return 1
        [[ $(storage_mount_point_state "${point}") == unmounted ]] || return 1
    fi
    state="$(storage_mount_state_path "${uuid}")" || return 1
    line="$(storage_mount_fstab_line "${uuid}" "${point}" "${filesystem}")" || return 1
    [[ -f ${STORAGE_MOUNT_FSTAB} && ! -L ${STORAGE_MOUNT_FSTAB} ]] || return 1
    snapshot="$(mktemp -d "$(dirname -- "${STORAGE_MOUNT_FSTAB}")/.mount-retire.XXXXXX")" || return 1
    if ! cp -a -- "${STORAGE_MOUNT_FSTAB}" "${snapshot}/fstab" || ! cp -a -- "${state}" "${snapshot}/state"; then
        rm -rf -- "${snapshot}"; return 1
    fi
    occupancy="$(storage_mount_point_state "${point}")" || { rm -rf -- "${snapshot}"; return 1; }
    if [[ ${occupancy} == mounted ]]; then
        if ! storage_mount_verify_live "${uuid}" "${point}" "${device}" || ! storage_action_host_unmount "${point}"; then
            rm -rf -- "${snapshot}"; return 1
        fi
        was_mounted=true
    fi
    if [[ $(storage_mount_point_state "${point}") == unmounted ]] && storage_mount_verify_record "${uuid}" &&
       { if [[ -n ${device} ]]; then storage_mount_unique_device "${uuid}" "${device}"; else storage_mount_uuid_absent "${uuid}"; fi; } &&
       storage_mount_remove_fstab_line "${line}" && rm -f -- "${state}" && { if declare -F jv_trace_run >/dev/null; then jv_trace_run systemctl daemon-reload; else systemctl daemon-reload; fi; }; then
        rm -rf -- "${snapshot}"; return 0
    fi
    cp -a -- "${snapshot}/fstab" "${STORAGE_MOUNT_FSTAB}" || ok=false
    cp -a -- "${snapshot}/state" "${state}" || ok=false
    if declare -F jv_trace_run >/dev/null; then jv_trace_run systemctl daemon-reload || ok=false; else systemctl daemon-reload || ok=false; fi
    [[ ${was_mounted} == false ]] || storage_action_host_mount "${point}" || ok=false
    if [[ ${ok} == true ]]; then rm -rf -- "${snapshot}"; else
        echo "Permanent-mount rollback incomplete; evidence preserved at ${snapshot}." >&2
    fi
    return 1
}

storage_mount_retire_device() {
    local device="$1" uuid filesystem current records record found=''
    uuid="$(storage_action_uuid "${device}")"
    filesystem="$(storage_action_filesystem "${device}")"
    current="$(storage_action_host_mountpoints "${device}")" || return 1
    records="$(storage_mount_records)" || return 1
    while IFS= read -r record; do
        [[ -n ${record} ]] || continue
        storage_mount_load_state "${record}" || return 1
        if [[ ${record} == "${uuid}" ]]; then
            storage_mount_verify_record "${record}" || return 1
            [[ ${MOUNT_STATE_FILESYSTEM} == "${filesystem}" ]] || return 1
            [[ -z ${current} || ${current} == "${MOUNT_STATE_MOUNT}" ]] || return 1
            found="${record}"
        elif grep -Fxq -- "${MOUNT_STATE_MOUNT}" <<< "${current}"; then
            return 1
        fi
    done <<< "${records}"
    [[ -n ${found} ]] || return 0
    storage_mount_retire_record "${found}" "${device}"
}

storage_mount_retire_all() {
    local records uuid device occupancy
    records="$(storage_mount_records)" || return 1
    # Validate every record before retiring any mount during Factory Reset.
    while IFS= read -r uuid; do
        [[ -n ${uuid} ]] || continue
        storage_mount_verify_record "${uuid}" || return 1
        if storage_mount_uuid_absent "${uuid}"; then
            [[ $(storage_mount_point_state "${MOUNT_STATE_MOUNT}") == unmounted ]] || return 1
            continue
        fi
        device="$(blkid -c /dev/null -t "UUID=${uuid}" -o device)" || return 1
        [[ -n ${device} && ${device} != *$'\n'* ]] || return 1
        device="$(storage_action_real_device "${device}")" || return 1
        storage_mount_unique_device "${uuid}" "${device}" || return 1
        [[ $(storage_action_filesystem "${device}") == "${MOUNT_STATE_FILESYSTEM}" ]] || return 1
        occupancy="$(storage_mount_point_state "${MOUNT_STATE_MOUNT}")" || return 1
        if [[ ${occupancy} == mounted ]]; then
            storage_mount_verify_live "${uuid}" "${MOUNT_STATE_MOUNT}" "${device}" || return 1
        fi
    done <<< "${records}"
    while IFS= read -r uuid; do
        [[ -n ${uuid} ]] || continue
        if storage_mount_uuid_absent "${uuid}"; then device=''; else
            device="$(blkid -c /dev/null -t "UUID=${uuid}" -o device)" || return 1
            device="$(storage_action_real_device "${device}")" || return 1
        fi
        storage_mount_retire_record "${uuid}" "${device}" || return 1
    done <<< "${records}"
}
