#!/usr/bin/bash
jv_migration_mount_nfs_noninteractive() {
    local source="$1" mode="$2" options actual
    jv_migration_transport_begin
    [[ ${source} == *:* && ${source} != *' '* && ${source} != *$'\n'* && ${source} != *$'\r'* ]] || { echo 'ERROR: invalid NFS source.' >&2; return 1; }
    JV_MIGRATION_MEDIA_MOUNT="${JV_MIGRATION_TRANSPORT_ROOT}/nfs"
    install -d -m0700 -o root -g root "${JV_MIGRATION_MEDIA_MOUNT}"
    [[ ${mode} == import ]] && options='ro,nodev,nosuid,noexec' || options='rw,nodev,nosuid,noexec'
    if ! mount -t nfs -o "${options}" "${source}" "${JV_MIGRATION_MEDIA_MOUNT}"; then
        echo 'ERROR: temporary NFS mount failed. No persistent fstab entry was created.' >&2
        return 1
    fi
    actual="$(findmnt -n -o SOURCE --target "${JV_MIGRATION_MEDIA_MOUNT}" 2>/dev/null || true)"
    [[ -n ${actual} ]] || { umount "${JV_MIGRATION_MEDIA_MOUNT}" || true; echo 'ERROR: NFS source identity could not be verified.' >&2; return 1; }
    JV_MIGRATION_OWNED_MOUNT="${JV_MIGRATION_MEDIA_MOUNT}"
}

jv_migration_mount_nfs() {
    local mode="$1" source
    source="$(jui_input 'NFS source (server:/export)')" || return 2
    jv_migration_mount_nfs_noninteractive "${source}" "${mode}"
}

jv_migration_mount_smb_noninteractive() {
    local source="$1" username="$2" password="$3" domain="$4" mode="$5" options actual
    jv_migration_transport_begin
    [[ ${source} == //*/* && ${source} != *' '* && ${source} != *$'\n'* && ${source} != *$'\r'* ]] || { echo 'ERROR: invalid SMB source.' >&2; return 1; }
    [[ -n ${username} && ${username} != *$'\n'* && ${username} != *$'\r'*         && ${password} != *$'\n'* && ${password} != *$'\r'*         && ${domain} != *$'\n'* && ${domain} != *$'\r'* ]] || {
        echo 'ERROR: SMB credentials are invalid.' >&2
        return 1
    }

    JV_MIGRATION_SMB_CREDENTIALS="${JV_MIGRATION_TRANSPORT_ROOT}/smb.credentials"
    umask 077
    {
        printf 'username=%s\n' "${username}"
        printf 'password=%s\n' "${password}"
        [[ -n ${domain} ]] && printf 'domain=%s\n' "${domain}"
    } > "${JV_MIGRATION_SMB_CREDENTIALS}"
    chmod 0600 "${JV_MIGRATION_SMB_CREDENTIALS}"
    unset password

    JV_MIGRATION_MEDIA_MOUNT="${JV_MIGRATION_TRANSPORT_ROOT}/smb"
    install -d -m0700 -o root -g root "${JV_MIGRATION_MEDIA_MOUNT}"
    [[ ${mode} == import ]] && options='ro' || options='rw'
    options="credentials=${JV_MIGRATION_SMB_CREDENTIALS},vers=3.0,${options},nodev,nosuid,noexec,uid=0,gid=0,file_mode=0600,dir_mode=0700"
    if ! mount -t cifs -o "${options}" "${source}" "${JV_MIGRATION_MEDIA_MOUNT}"; then
        echo 'ERROR: temporary SMB/CIFS mount failed. No persistent fstab entry was created.' >&2
        return 1
    fi
    actual="$(findmnt -n -o SOURCE --target "${JV_MIGRATION_MEDIA_MOUNT}" 2>/dev/null || true)"
    [[ -n ${actual} ]] || { umount "${JV_MIGRATION_MEDIA_MOUNT}" || true; echo 'ERROR: SMB source identity could not be verified.' >&2; return 1; }
    JV_MIGRATION_OWNED_MOUNT="${JV_MIGRATION_MEDIA_MOUNT}"
}

jv_migration_mount_smb() {
    local mode="$1" source username password domain
    source="$(jui_input 'SMB source (//server/share)')" || return 2
    username="$(jui_input 'SMB username')" || return 2
    printf 'SMB password: ' >/dev/tty
    IFS= read -r -s password </dev/tty
    echo >/dev/tty
    domain="$(jui_input 'SMB domain/workgroup (optional)')" || return 2
    jv_migration_mount_smb_noninteractive "${source}" "${username}" "${password}" "${domain}" "${mode}"
}
