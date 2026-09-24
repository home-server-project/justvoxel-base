#!/usr/bin/bash

jv_backup_load_config() {
    local backup_config_file="${1:-/etc/justvoxel/minecraft-backup.env}"

    if [[ ! -r ${backup_config_file} ]]; then
        echo "ERROR: backup configuration is missing: ${backup_config_file}" >&2
        return 1
    fi

    # shellcheck disable=SC1090
    source "${backup_config_file}"

    : "${MINECRAFT_DATA_PATH:?MINECRAFT_DATA_PATH must be configured}"
    : "${MINECRAFT_BACKUP_PATH:?MINECRAFT_BACKUP_PATH must be configured}"

    MINECRAFT_SERVICE="${MINECRAFT_SERVICE:-minecraft.service}"
    BACKUP_TYPE="${BACKUP_TYPE:-}"
    if [[ -z ${BACKUP_TYPE} ]]; then
        if [[ -n ${BACKUP_EXPECTED_UUID:-} ]]; then
            BACKUP_TYPE=local
        elif [[ ${BACKUP_EXPECTED_SOURCE:-} == //* ]]; then
            BACKUP_TYPE=smb
        elif [[ -n ${BACKUP_EXPECTED_SOURCE:-} ]]; then
            BACKUP_TYPE=nfs
        else
            BACKUP_TYPE=system
        fi
    fi
    BACKUP_KEEP="${BACKUP_KEEP:-7}"
    BACKUP_MOUNT_POINT="${BACKUP_MOUNT_POINT:-}"
    BACKUP_EXPECTED_UUID="${BACKUP_EXPECTED_UUID:-}"
    BACKUP_EXPECTED_SOURCE="${BACKUP_EXPECTED_SOURCE:-}"

    if [[ ${MINECRAFT_DATA_PATH} != /* || ${MINECRAFT_BACKUP_PATH} != /* ]]; then
        echo 'ERROR: Minecraft data and backup paths must be absolute.' >&2
        return 1
    fi
    if [[ ${MINECRAFT_DATA_PATH} == / || ${MINECRAFT_BACKUP_PATH} == / ]]; then
        echo 'ERROR: refusing to use / as a Minecraft data or backup path.' >&2
        return 1
    fi
    if [[ ! ${BACKUP_KEEP} =~ ^[1-9][0-9]*$ ]]; then
        echo 'ERROR: BACKUP_KEEP must be a positive integer.' >&2
        return 1
    fi

    minecraft_data_path="$(realpath -m -- "${MINECRAFT_DATA_PATH}")"
    minecraft_backup_path="$(realpath -m -- "${MINECRAFT_BACKUP_PATH}")"

    if [[ ${minecraft_backup_path} == "${minecraft_data_path}" || ${minecraft_backup_path} == "${minecraft_data_path}/"* ]]; then
        echo 'ERROR: backup path must not be the Minecraft data directory or a child of it.' >&2
        return 1
    fi
}

jv_backup_validate_mount_identity() {
    [[ -n ${BACKUP_MOUNT_POINT:-} ]] || return 0

    if [[ ${BACKUP_MOUNT_POINT} != /* ]]; then
        echo 'ERROR: BACKUP_MOUNT_POINT must be absolute.' >&2
        return 1
    fi

    backup_mount_point="$(realpath -m -- "${BACKUP_MOUNT_POINT}")"
    if ! mountpoint -q -- "${backup_mount_point}"; then
        echo "ERROR: configured backup mount is not mounted: ${backup_mount_point}" >&2
        return 1
    fi

    case "${minecraft_backup_path}/" in
        "${backup_mount_point}/"*) ;;
        *)
            echo "ERROR: backup path is outside configured backup mount: ${backup_mount_point}" >&2
            return 1
            ;;
    esac

    if [[ -n ${BACKUP_EXPECTED_UUID:-} ]]; then
        local actual_uuid
        actual_uuid="$(findmnt -n -o UUID --target "${backup_mount_point}" 2>/dev/null || true)"
        if [[ ${actual_uuid} != "${BACKUP_EXPECTED_UUID}" ]]; then
            echo "ERROR: backup mount UUID mismatch; expected ${BACKUP_EXPECTED_UUID}, got ${actual_uuid:-unknown}." >&2
            return 1
        fi
    fi

    if [[ -n ${BACKUP_EXPECTED_SOURCE:-} ]]; then
        local actual_source
        actual_source="$(findmnt -n -o SOURCE --target "${backup_mount_point}" 2>/dev/null || true)"
        if [[ ${actual_source} != "${BACKUP_EXPECTED_SOURCE}" ]]; then
            echo "ERROR: backup mount source mismatch; expected ${BACKUP_EXPECTED_SOURCE}, got ${actual_source:-unknown}." >&2
            return 1
        fi
    fi
}

jv_backup_require_read_target() {
    jv_backup_validate_mount_identity || return 1
    if [[ ! -d ${minecraft_backup_path} || ! -r ${minecraft_backup_path} || ! -x ${minecraft_backup_path} ]]; then
        echo "ERROR: configured backup path is not readable: ${minecraft_backup_path}" >&2
        return 1
    fi
}

jv_backup_prepare_write_target() {
    jv_backup_validate_mount_identity || return 1

    if [[ ${BACKUP_TYPE} == nfs || ${BACKUP_TYPE} == smb ]]; then
        # Network shares may use server-side ownership rules or root-squash.
        mkdir -p -- "${minecraft_backup_path}"
    else
        install -d -m0700 -o root -g root "${minecraft_backup_path}"
    fi

    if [[ ! -w ${minecraft_backup_path} ]]; then
        echo "ERROR: backup path is not writable: ${minecraft_backup_path}" >&2
        return 1
    fi

    local write_probe
    write_probe="$(mktemp "${minecraft_backup_path}/.justvoxel-write-test.XXXXXX" 2>/dev/null)" || {
        echo "ERROR: backup path failed an actual write test: ${minecraft_backup_path}" >&2
        return 1
    }
    rm -f -- "${write_probe}"
}

jv_backup_write_metadata() {
    local archive="$1" server_reported_version="${2:-}" geyser_reported_version="${3:-}" floodgate_configured="${4:-false}"
    local metadata="${archive}.meta.json" tmp

    tmp="$(mktemp "${metadata}.partial.XXXXXX")" || return 1
    if ! (
        set -euo pipefail
        local_config=/etc/justvoxel/justvoxel.conf
        [[ -r ${local_config} ]]
        # shellcheck disable=SC1090
        source "${local_config}"

        image_repo='docker.io/itzg/minecraft-server'
        image_ref="${image_repo}:${MINECRAFT_IMAGE_TAG:-latest}"
        image_digest="$(podman image inspect "${image_ref}" --format '{{.Digest}}' 2>/dev/null || true)"

        variant="$(cat /usr/lib/justvoxel/variant 2>/dev/null || true)"
        os_pretty=''
        if [[ -r /usr/lib/os-release ]]; then
            # shellcheck disable=SC1091
            source /usr/lib/os-release
            os_pretty="${PRETTY_NAME:-}"
        fi

        bootc_json="$(bootc status --json --format-version=1 2>/dev/null || true)"
        bootc_ref=''
        bootc_digest=''
        if jq -e '.apiVersion == "org.containers.bootc/v1"' >/dev/null 2>&1 <<< "${bootc_json}"; then
            bootc_ref="$(jq -r '.status.booted.image.image.image // empty' <<< "${bootc_json}")"
            bootc_digest="$(jq -r '.status.booted.image.imageDigest // empty' <<< "${bootc_json}")"
        fi

        archive_size="$(stat -c '%s' "${archive}")"
        created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

        jq -n \
            --argjson schemaVersion 1 \
            --arg createdAt "${created_at}" \
            --arg archive "$(basename -- "${archive}")" \
            --argjson archiveSizeBytes "${archive_size}" \
            --arg dataDirectoryName "$(basename -- "${MINECRAFT_DATA_PATH}")" \
            --arg versionMode "${MINECRAFT_VERSION_MODE:-unknown}" \
            --arg configuredVersion "${MINECRAFT_VERSION:-unknown}" \
            --arg serverReportedVersion "${server_reported_version}" \
            --arg containerImage "${image_ref}" \
            --arg containerDigest "${image_digest}" \
            --arg uid "${MINECRAFT_UID:-}" \
            --arg gid "${MINECRAFT_GID:-}" \
            --arg bedrockEnabled "${BEDROCK_ENABLED:-no}" \
            --arg geyserReportedVersion "${geyser_reported_version}" \
            --argjson floodgateConfigured "${floodgate_configured}" \
            --arg justvoxelVariant "${variant}" \
            --arg justvoxelOS "${os_pretty}" \
            --arg bootcImage "${bootc_ref}" \
            --arg bootcDigest "${bootc_digest}" \
            '{
                schemaVersion: $schemaVersion,
                createdAt: $createdAt,
                archive: $archive,
                archiveSizeBytes: $archiveSizeBytes,
                minecraft: {
                    dataDirectoryName: $dataDirectoryName,
                    versionMode: $versionMode,
                    configuredVersion: $configuredVersion,
                    serverReportedVersion: (if $serverReportedVersion == "" then null else $serverReportedVersion end),
                    dataUID: (if $uid == "" then null else ($uid | tonumber) end),
                    dataGID: (if $gid == "" then null else ($gid | tonumber) end)
                },
                container: {
                    image: $containerImage,
                    digest: (if $containerDigest == "" then null else $containerDigest end)
                },
                bedrock: {
                    enabled: ($bedrockEnabled == "yes"),
                    geyserReportedVersion: (if $geyserReportedVersion == "" then null else $geyserReportedVersion end),
                    floodgateConfigured: $floodgateConfigured
                },
                justvoxel: {
                    variant: (if $justvoxelVariant == "" then null else $justvoxelVariant end),
                    os: (if $justvoxelOS == "" then null else $justvoxelOS end),
                    bootcImage: (if $bootcImage == "" then null else $bootcImage end),
                    bootcDigest: (if $bootcDigest == "" then null else $bootcDigest end)
                }
            }'
    ) > "${tmp}"; then
        rm -f -- "${tmp}"
        return 1
    fi

    if ! jq -e '.schemaVersion == 1 and (.archive | type == "string")' "${tmp}" >/dev/null 2>&1; then
        rm -f -- "${tmp}"
        return 1
    fi

    chmod 0600 "${tmp}" 2>/dev/null || true
    mv -f -- "${tmp}" "${metadata}"
}
