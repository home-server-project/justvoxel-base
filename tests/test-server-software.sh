#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
# Actual config loader/writer/rendering, with every filesystem path in the fixture.
sed -e "s|/etc|${fixture_dir}/etc|g" \
    -e "s|/usr/share/justvoxel/templates|${repo_root}/templates|g" \
    "${repo_root}/mjust/libexec/common.sh" > "${fixture_dir}/common.sh"
mkdir -p "${fixture_dir}/etc/systemd/system"
source "${fixture_dir}/common.sh"
systemctl() { :; }
chown() { :; }
install() {
    local -a args=()
    while (( $# )); do
        case "$1" in -o|-g) shift 2 ;; *) args+=("$1"); shift ;; esac
    done
    command install "${args[@]}"
}
# Authoritative upstream fixtures, no network or appliance commands.
curl() {
    case "${*: -1}" in
        https://api.purpurmc.org/v2/purpur) printf '%s' "${PURPUR_METADATA}" ;;
        https://piston-meta.mojang.com/mc/game/version_manifest_v2.json) printf '%s' "${VANILLA_METADATA}" ;;
        *) echo 'Unexpected resolver URL' >&2; return 1 ;;
    esac
}
PURPUR_METADATA='{"metadata":{"current":"1.21.8"},"versions":["1.21.8","1.21.10","1.21.9"]}'
VANILLA_METADATA='{"latest":{"release":"1.21.8","snapshot":"26.3-snapshot"},"versions":[{"id":"1.21.8","type":"release"},{"id":"1.20.1","type":"release"},{"id":"26.3-snapshot","type":"snapshot"}]}'
paper_version_build_channel() { [[ $1 == 1.21.8 ]] && printf STABLE; }
paper_version_has_stable_build() { [[ $1 == 1.21.8 ]]; }
resolve_latest_stable_paper_version() { printf 1.21.8; }
resolve_latest_available_paper_version() { printf 1.21.8; }
DATA_PATH="${fixture_dir}/data" DATA_MOUNT_POINT='' DATA_EXPECTED_UUID='' DATA_EXPECTED_SOURCE=''
BACKUP_TYPE=system BACKUP_PATH="${fixture_dir}/backup" BACKUP_MOUNT_POINT='' BACKUP_EXPECTED_UUID='' BACKUP_EXPECTED_SOURCE=''
BACKUP_KEEP=7 BACKUP_SCHEDULE='*-*-* 04:30:00' BACKUP_TIMER_ENABLED=no
JAVA_PORT=25565 BEDROCK_PORT=19132 BEDROCK_ENABLED=no JAVA_MEMORY=2G CONTAINER_MEMORY=3G
MINECRAFT_IMAGE_TAG=stable MINECRAFT_VERSION_MODE=pinned MINECRAFT_VERSION=1.21.8
MINECRAFT_UID=1000 MINECRAFT_GID=1000 TIMEZONE=UTC MAX_PLAYERS=10 MOTD=Fixture
write_main_config
sed -i '/^MINECRAFT_SERVER_TYPE=/d' "${JV_CONFIG}"
MINECRAFT_SERVER_TYPE=purpur
require_config
[[ ${MINECRAFT_SERVER_TYPE} == paper ]]
for invalid in '' PAPER fabric purpur-invalid; do
    MINECRAFT_SERVER_TYPE="${invalid}"
    if write_main_config; then echo 'Invalid server type persisted' >&2; exit 1; fi
    printf 'MINECRAFT_SERVER_TYPE=%q\n' "${invalid}" >> "${JV_CONFIG}"
    if require_config; then echo 'Invalid config loaded' >&2; exit 1; fi
    sed -i '/^MINECRAFT_SERVER_TYPE=/d' "${JV_CONFIG}"
done
printf 'RCON_PASSWORD=fixture\n' > "${JV_MC_ENV}"
for software in paper purpur vanilla; do
    MINECRAFT_SERVER_TYPE="${software}" BEDROCK_ENABLED=no
    write_main_config
    render_runtime_files
    grep -Fxq "TYPE=${software^^}" "${JV_MC_ENV}"
    [[ $(resolve_recommended_server_version) == 1.21.8 ]]
    server_version_available 1.21.8
    if server_version_available 9.99; then echo 'Unknown release accepted' >&2; exit 1; fi
    if [[ ${software} == vanilla ]]; then
        if grep -Eq 'PAPER_CHANNEL|MODRINTH_PROJECTS|PLUGINS=' "${JV_MC_ENV}"; then exit 1; fi
        if grep -Eq 'managed-crossplay-start|19132/udp' "${JV_QUADLET}"; then exit 1; fi
        [[ $(resolve_latest_server_version) == 1.21.8 ]]
        if server_version_available 26.3-snapshot; then echo 'Snapshot accepted' >&2; exit 1; fi
        BEDROCK_ENABLED=yes
        if write_main_config; then echo 'Vanilla Bedrock persisted' >&2; exit 1; fi
    else
        grep -Fxq MODRINTH_PROJECTS=viaversion "${JV_MC_ENV}"
        grep -Fq managed-crossplay-start "${JV_QUADLET}"
        BEDROCK_ENABLED=yes
        write_main_config
        render_runtime_files
        grep -Fxq 'PublishPort=19132:19132/udp' "${JV_QUADLET}"
        if [[ ${software} == purpur ]]; then
            [[ $(resolve_latest_server_version) == 1.21.10 ]]
            if grep -q '^PAPER_CHANNEL=' "${JV_MC_ENV}"; then exit 1; fi
        fi
    fi
done
for from in paper purpur vanilla; do
    for to in paper purpur vanilla; do
        if [[ ${from} == "${to}" || ( ${from} != vanilla && ${to} != vanilla ) ]]; then
            server_allows_in_place_switch "${from}" "${to}"
        elif server_allows_in_place_switch "${from}" "${to}"; then
            echo 'Unsafe transition accepted' >&2; exit 1
        fi
    done
done
MINECRAFT_SERVER_TYPE=purpur
PURPUR_METADATA='{"metadata":{"current":"9.9"},"versions":["1.21.8"]}'
if resolve_recommended_server_version; then echo 'Invalid Purpur metadata accepted' >&2; exit 1; fi
MINECRAFT_SERVER_TYPE=vanilla
VANILLA_METADATA='{}'
if resolve_latest_server_version; then echo 'Missing Mojang release guessed' >&2; exit 1; fi
echo 'Server software config, resolver, capability and render tests passed.'
# Cold backups load the same authoritative type, rather than inheriting a caller value.
MINECRAFT_SERVER_TYPE=vanilla BEDROCK_ENABLED=no
write_main_config
source "${repo_root}/mjust/libexec/backup-common.sh"
MINECRAFT_SERVER_TYPE=purpur
jv_backup_load_config "${JV_BACKUP_ENV}"
[[ ${MINECRAFT_SERVER_TYPE} == vanilla ]]
printf 'MINECRAFT_SERVER_TYPE=invalid\n' >> "${JV_CONFIG}"
if jv_backup_load_config "${JV_BACKUP_ENV}"; then echo 'Backup accepted invalid software config' >&2; exit 1; fi
