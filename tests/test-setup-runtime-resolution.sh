#!/usr/bin/bash
# Setup discovery fixtures; exact artifact verification has separate coverage in
# test-java-server-runtime.sh. No network or appliance access.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
source "${repo_root}/mjust/libexec/common.sh"

reject() { if "$@"; then echo 'Unsafe setup resolution accepted' >&2; exit 1; fi; }
curl() {
    [[ ${offline:-no} != yes ]] || return 22
    case "${*: -1}" in
        https://meta.fabricmc.net/v2/versions/loader/1.21.8|https://meta.quiltmc.org/v3/versions/loader/1.21.8)
            printf '%s' '[{"loader":{"version":"0.99.0-beta","stable":false}},{"loader":{"version":"0.18.0","stable":true}}]' ;;
        https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json)
            printf '%s' '{"promos":{"1.21.8-recommended":"58.0.1","1.21.8-latest":"58.0.2"}}' ;;
        https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml)
            printf '%s\n' '<metadata><versioning><versions>' '<version>21.8.1</version>' '<version>21.8.2-beta</version>' '<version>21.9.9</version>' '</versions></versioning></metadata>' ;;
        *) echo 'Unexpected metadata URL' >&2; return 1 ;;
    esac
}
vanilla_metadata() { printf '%s' '{"versions":[{"id":"26.3-rc-1","type":"snapshot"},{"id":"1.21.8","type":"release"}]}'; }
java_server_version_available() {
    [[ ${missing_pair:-no} != yes && $1 == "${software}" && $2 == 1.21.8 && ${3:-} == "${expected_loader}" ]]
}
for software in spigot fabric quilt forge neoforge; do
    case "${software}" in
        spigot) expected_loader='' expected_image=java21-jdk ;;
        fabric|quilt) expected_loader=0.18.0 expected_image=java21 ;;
        forge) expected_loader=58.0.1 expected_image=java21 ;;
        neoforge) expected_loader=21.8.1 expected_image=java21 ;;
    esac
    for policy in recommended pinned; do
        resolved="$(resolve_setup_java_runtime "${software}" "${policy}" 1.21.8)"
        jq -e --arg loader "${expected_loader}" --arg image "${expected_image}" '.game == "1.21.8" and .loader == $loader and .image == $image' <<< "${resolved}" >/dev/null
    done
    missing_pair=yes
    reject resolve_setup_java_runtime "${software}" recommended ''
    missing_pair=no
    reject resolve_setup_java_runtime "${software}" latest ''
    reject resolve_setup_java_runtime "${software}" pinned 26.3-rc-1
    if [[ ${software} != spigot ]]; then
        offline=yes
        reject resolve_setup_java_runtime "${software}" recommended ''
        offline=no
    fi
done
