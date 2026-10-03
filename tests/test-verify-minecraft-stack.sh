#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
export DATA_PATH="${fixture_dir}/data" MINECRAFT_VERSION_MODE=recommended MINECRAFT_VERSION=26.2 BEDROCK_ENABLED=yes
mkdir -p "${DATA_PATH}/plugins/Geyser-Spigot"
printf 'auth-type: floodgate\n' > "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
cat > "${fixture_dir}/common.sh" <<'STUB'
require_root() { :; }
require_config() { :; }
JV_MAINTENANCE_LOCK="${DATA_PATH}/lock"
systemctl() { [[ ${ACTIVE:-yes} == yes ]]; }
wait_for_rcon() { [[ ${READY:-yes} == yes ]]; }
podman() {
    case "${*: -1}" in
        version) printf '%s' "${VERSION_RESPONSE:-This server is running Paper version 26.2-19-main (Implementing API version 26.2-R0.1-SNAPSHOT)}" ;;
        plugins) printf '%s' "${PLUGIN_RESPONSE:-Plugins (3): Geyser-Spigot, floodgate, ViaVersion}" ;;
        'geyser version') printf '%s' "${GEYSER_RESPONSE:-This server is running Geyser version fixture}" ;;
        *) return 1 ;;
    esac
}
STUB
sed "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    "${repo_root}/mjust/libexec/verify-minecraft-stack" > "${fixture_dir}/verify"
bash "${fixture_dir}/verify" 26.2
VERSION_RESPONSE='This server is running Paper (MC: 26.2)' bash "${fixture_dir}/verify" 26.2
for failure in 'ACTIVE=no' 'READY=no' 'VERSION_RESPONSE=Unknown command' 'VERSION_RESPONSE=This server is running Paper version 26.3-19-main' 'PLUGIN_RESPONSE=Plugins (1): OtherPlugin' 'GEYSER_RESPONSE=Unknown command'; do
    if env "${failure}" bash "${fixture_dir}/verify" 26.2; then echo "Invalid runtime accepted: ${failure}" >&2; exit 1; fi
done
printf 'auth-type: online\n' > "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
if bash "${fixture_dir}/verify" 26.2; then echo 'Unverified authentication accepted' >&2; exit 1; fi
