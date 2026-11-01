#!/usr/bin/bash
# Offline setup discovery and exact authoritative runtime verification fixtures.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
source "${repo_root}/mjust/libexec/common.sh"
reject() { if "$@"; then echo 'Unsafe setup resolution accepted' >&2; exit 1; fi; }
curl() {
    [[ ${offline:-no} != yes ]] || return 22
    case "${*: -1}" in
        */version_manifest_v2.json)
            printf '%s' '{"latest":{"release":"26.3"},"versions":[{"id":"26.4-rc-1","type":"snapshot"},{"id":"26.3","type":"release"},{"id":"1.21.8","type":"release"}]}' ;;
        https://meta.fabricmc.net/v2/versions/loader/26.3)
            printf '%s' '[{"loader":{"version":"0.99.0","stable":false}},{"loader":{"version":"0.20.0"}},{"loader":{"version":"0.19.5","stable":true}}]' ;;
        https://meta.quiltmc.org/v3/versions/loader/1.21.8)
            printf '%s' '[{"loader":{"version":"0.99.0-beta","stable":false}},{"loader":{"version":"0.18.0","stable":true}}]' ;;
        https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json)
            printf '%s' '{"promos":{"1.21.8-recommended":"58.0.1","1.21.8-latest":"58.0.2"}}' ;;
        https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml)
            # Deliberately one line, with newer betas and a different game patch.
            printf '%s' '<metadata><versioning><versions><version>21.8.1</version><version>26.3.0.99</version><version>26.3.0.100-beta</version><version>26.3.1.999</version></versions></versioning></metadata>' ;;
        https://meta.fabricmc.net/v2/versions/loader/26.3/0.19.5/server/json)
            [[ ${missing_pair:-no} != yes ]] || return 22
            printf '%s' '{"inheritsFrom":"26.3","mainClass":"net.fabricmc.loader.impl.launch.knot.KnotServer","libraries":[{"name":"net.fabricmc:fabric-loader:0.19.5"}]}' ;;
        https://meta.quiltmc.org/v3/versions/loader/1.21.8/0.18.0/server/json)
            [[ ${missing_pair:-no} != yes ]] || return 22
            printf '%s' '{"inheritsFrom":"1.21.8","mainClass":"org.quiltmc.loader.impl.launch.knot.KnotServer","libraries":[{"name":"org.quiltmc:quilt-loader:0.18.0"}]}' ;;
        https://hub.spigotmc.org/versions/1.21.8.json)
            [[ ${missing_pair:-no} != yes ]] || return 22
            printf '%s' '{"javaVersions":[65],"refs":{"BuildData":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Bukkit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","CraftBukkit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Spigot":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}' ;;
        https://maven.neoforged.net/releases/net/neoforged/neoforge/26.3.0.99/neoforge-26.3.0.99-installer.jar.sha1|https://maven.minecraftforge.net/net/minecraftforge/forge/1.21.8-58.0.1/forge-1.21.8-58.0.1-installer.jar.sha1)
            [[ ${missing_pair:-no} != yes ]] || return 22
            printf '%s' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ;;
        *) return 22 ;; # Never delegate unlisted URLs to the network.
    esac
}
for software in spigot fabric quilt forge neoforge; do
    expected_game=1.21.8
    case "${software}" in
        spigot) expected_loader='' expected_image=java21-jdk ;;
        fabric) expected_game=26.3 expected_loader=0.19.5 expected_image=java25 ;;
        quilt) expected_loader=0.18.0 expected_image=java21 ;;
        forge) expected_loader=58.0.1 expected_image=java21 ;;
        neoforge) expected_game=26.3 expected_loader=26.3.0.99 expected_image=java25 ;;
    esac
    for policy in recommended pinned; do
        resolved="$(resolve_setup_java_runtime "${software}" "${policy}" "${expected_game}")"
        jq -e --arg game "${expected_game}" --arg loader "${expected_loader}" --arg image "${expected_image}" '.game == $game and .loader == $loader and .image == $image' <<< "${resolved}" >/dev/null
    done
    missing_pair=yes
    reject resolve_setup_java_runtime "${software}" recommended ''
    missing_pair=no
    reject resolve_setup_java_runtime "${software}" latest ''
    reject resolve_setup_java_runtime "${software}" pinned 26.4-rc-1
    offline=yes
    reject resolve_setup_java_runtime "${software}" recommended ''
    offline=no
done
reject java_server_version_available neoforge 26.3 26.3.1.999
reject java_server_version_available fabric 26.3 0.99.0
