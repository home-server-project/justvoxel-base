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
sleep() { printf '%s\n' "$1" >> "${DATA_PATH}/sleeps"; }
podman() {
    case "${*: -1}" in
        --json) printf 'probe\n' >> "${DATA_PATH}/monitor-probes"; [[ ${MONITOR_FAIL:-no} != yes ]] || return 1; printf '%s' "${MONITOR_RESPONSE}" ;;
        list) printf 'There are 0 of a max of 10 players online' ;;
        version) [[ ${VERSION_FAIL:-no} != yes ]] || return 1; [[ ${MINECRAFT_SERVER_TYPE:-paper} != vanilla ]] || { echo 'Unexpected Vanilla version command' >&2; return 1; }; if [[ ${TRANSIENT:-no} == yes ]]; then
            count=$(cat "${DATA_PATH}/probes" 2>/dev/null || printf 0)
            printf '%s' "$((count + 1))" > "${DATA_PATH}/probes"
            if (( count < ${TRANSIENT_COUNT:-1} )); then printf '%s' "${TRANSIENT_RESPONSE:-Checking version, please wait...}"; return 0; fi
        fi; printf '%s' "${VERSION_RESPONSE:-This server is running Paper version 26.2-19-main (Implementing API version 26.2-R0.1-SNAPSHOT)}" ;;
        plugins) [[ ${MINECRAFT_SERVER_TYPE:-paper} != vanilla ]] || { echo 'Unexpected Vanilla plugins command' >&2; return 1; }; printf '%s' "${PLUGIN_RESPONSE:-Plugins (3): Geyser-Spigot, floodgate, ViaVersion}" ;;
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

MINECRAFT_SERVER_TYPE=purpur BEDROCK_ENABLED=no VERSION_RESPONSE='This server is running Purpur version 26.2-19-main' bash "${fixture_dir}/verify" 26.2
MINECRAFT_SERVER_TYPE=vanilla BEDROCK_ENABLED=no MONITOR_RESPONSE='{"server_info":{"version":{"name":"26.2"}}}' bash "${fixture_dir}/verify" 26.2
for response in '{"server_info":{"version":{"name":"26.3"}}}' '{}' 'not JSON'; do
    if MINECRAFT_SERVER_TYPE=vanilla BEDROCK_ENABLED=no MONITOR_RESPONSE="${response}" bash "${fixture_dir}/verify" 26.2; then
        echo 'Invalid Vanilla status accepted' >&2; exit 1
    fi
done

# Probe state is stored in files because version runs in a command substitution.
for software in paper purpur; do
    rm -f "${DATA_PATH}/probes" "${DATA_PATH}/sleeps"
    TRANSIENT=yes MINECRAFT_SERVER_TYPE="${software}" BEDROCK_ENABLED=no \
        VERSION_RESPONSE="This server is running ${software^} version 26.2-19-main" bash "${fixture_dir}/verify" 26.2
    [[ $(cat "${DATA_PATH}/probes") == 2 && $(cat "${DATA_PATH}/sleeps") == 3 ]]
done
for software in paper purpur; do
    for response in "This server is running Spigot (MC: 26.2)" "This server is running ${software^} (MC: 26.3)"; do
        rm -f "${DATA_PATH}/probes"
        if TRANSIENT=yes MINECRAFT_SERVER_TYPE="${software}" BEDROCK_ENABLED=no \
            VERSION_RESPONSE="${response}" bash "${fixture_dir}/verify" 26.2; then
            echo 'Invalid response after transient accepted' >&2; exit 1
        fi
    done
    # Exhaust the same bounded wait, then verify the running version locally.
    printf 'auth-type: floodgate\n' > "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
    for expected in 26.2 26.3 LATEST; do
        reported_version="${expected}"
        [[ ${expected} != LATEST ]] || reported_version=26.3
        rm -f "${DATA_PATH}/probes" "${DATA_PATH}/sleeps"
        TRANSIENT=yes TRANSIENT_COUNT=100 MINECRAFT_SERVER_TYPE="${software}" \
            MONITOR_RESPONSE="{\"server_info\":{\"version\":{\"name\":\"${software^} ${reported_version}\",\"protocol\":776}}}" bash "${fixture_dir}/verify" "${expected}"
        [[ $(cat "${DATA_PATH}/probes") == 21 && $(wc -l < "${DATA_PATH}/sleeps") == 20 ]]
    done
    # Reproduce the real ANSI-styled transient response, including terminal whitespace.
    for response in $'\033[37m\033[3mChecking version, please wait...\033[0m' $' \r\n\033[37m\033[3mChecking version, please wait...\r\n\033[0m\t '; do
        rm -f "${DATA_PATH}/probes" "${DATA_PATH}/sleeps"
        TRANSIENT=yes TRANSIENT_COUNT=100 TRANSIENT_RESPONSE="${response}" MINECRAFT_SERVER_TYPE="${software}" \
            MONITOR_RESPONSE="{\"server_info\":{\"version\":{\"name\":\"${software^} 26.2\"}}}" bash "${fixture_dir}/verify" 26.2
        [[ $(cat "${DATA_PATH}/probes") == 21 && $(wc -l < "${DATA_PATH}/sleeps") == 20 ]]
    done
    # Formatting removal must not turn extra response content into the exact transient.
    for response in $'\033[37m\033[3mChecking version, please wait... unexpected\033[0m' $'\033[37m\033[3mWarning: Checking version, please wait...\033[0m'; do
        rm -f "${DATA_PATH}/probes" "${DATA_PATH}/sleeps" "${DATA_PATH}/monitor-probes"
        if TRANSIENT=yes TRANSIENT_COUNT=100 TRANSIENT_RESPONSE="${response}" MINECRAFT_SERVER_TYPE="${software}" \
            MONITOR_RESPONSE="{\"server_info\":{\"version\":{\"name\":\"${software^} 26.2\"}}}" bash "${fixture_dir}/verify" 26.2; then
            echo 'ANSI-formatted non-transient response accepted' >&2; exit 1
        fi
        [[ $(cat "${DATA_PATH}/probes") == 1 && ! -e ${DATA_PATH}/sleeps && ! -e ${DATA_PATH}/monitor-probes ]]
    done
    wrong_software=Paper
    [[ ${software} != paper ]] || wrong_software=Purpur
    for response in "{\"server_info\":{\"version\":{\"name\":\"${software^} 26.2\"}}}" \
        "{\"server_info\":{\"version\":{\"name\":\"${wrong_software} 26.3\"}}}" \
        '{"server_info":{"version":{"name":"Spigot 26.3"}}}' \
        '{"server_info":{"version":{"name":"26.3"}}}' \
        "{\"server_info\":{\"version\":{\"name\":\"${software^} 26.3 extra\"}}}" \
        "{\"server_info\":{\"version\":{\"name\":\"${software^}  26.3\"}}}" \
        "{\"server_info\":{\"version\":{\"name\":\"${software^} 26.3\\n\"}}}" \
        'not JSON' '{}' '{"server_info":{"version":{}}}' \
        '{"server_info":{"version":{"name":""}}}' '{"server_info":{"version":{"name":26.3}}}'; do
        rm -f "${DATA_PATH}/probes" "${DATA_PATH}/sleeps"
        if TRANSIENT=yes TRANSIENT_COUNT=100 MINECRAFT_SERVER_TYPE="${software}" \
            MONITOR_RESPONSE="${response}" bash "${fixture_dir}/verify" 26.3; then
            echo 'Invalid fallback status accepted' >&2; exit 1
        fi
        [[ $(cat "${DATA_PATH}/probes") == 21 && $(wc -l < "${DATA_PATH}/sleeps") == 20 ]]
    done
    for failure in 'MONITOR_FAIL=yes' 'PLUGIN_RESPONSE=Plugins (1): OtherPlugin' 'PLUGIN_RESPONSE=Plugins (1): ViaVersion' 'GEYSER_RESPONSE=Unknown command' 'VERSION_FAIL=yes' 'ACTIVE=no' 'READY=no'; do
        rm -f "${DATA_PATH}/probes"
        if env "${failure}" TRANSIENT=yes TRANSIENT_COUNT=100 MINECRAFT_SERVER_TYPE="${software}" \
            MONITOR_RESPONSE="{\"server_info\":{\"version\":{\"name\":\"${software^} 26.2\"}}}" bash "${fixture_dir}/verify" 26.2; then
            echo "Invalid fallback runtime accepted: ${failure}" >&2; exit 1
        fi
    done
    for response in 'Unknown command' 'This server is running Spigot (MC: 26.2)' "This server is running ${software^} (MC: 26.3)" 'Checking version, please wait... unexpected'; do
        if MINECRAFT_SERVER_TYPE="${software}" VERSION_RESPONSE="${response}" \
            MONITOR_RESPONSE="{\"server_info\":{\"version\":{\"name\":\"${software^} 26.2\"}}}" bash "${fixture_dir}/verify" 26.2; then
            echo 'Completed invalid response bypassed by fallback' >&2; exit 1
        fi
    done
    for response in '{}' 'not JSON' '{"server_info":{"version":{"name":""}}}' \
        '{"server_info":{"version":{"name":26.3}}}' '{"server_info":{"version":{"name":"Spigot 26.3"}}}' \
        "{\"server_info\":{\"version\":{\"name\":\"${wrong_software} 26.3\"}}}"; do
        rm -f "${DATA_PATH}/probes"
        if TRANSIENT=yes TRANSIENT_COUNT=100 MINECRAFT_SERVER_TYPE="${software}" \
            MONITOR_RESPONSE="${response}" bash "${fixture_dir}/verify" LATEST; then
            echo 'Invalid LATEST fallback accepted' >&2; exit 1
        fi
    done
done
echo 'Minecraft stack readiness regression checks passed.'
