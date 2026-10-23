#!/usr/bin/bash
# Isolated metadata fixtures. No appliance commands, containers, or network.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
source "${repo_root}/mjust/libexec/common.sh"

reject() {
    if "$@"; then echo 'Unsupported Java server selection was accepted' >&2; exit 1; fi
}

for software in spigot forge neoforge fabric quilt; do
    validate_server_type "${software}"
    reject server_supports_plugins "${software}"
    if [[ ${software} == spigot ]]; then server_supports_bukkit_plugins "${software}"; server_supports_viaversion "${software}"; else reject server_supports_bukkit_plugins "${software}"; reject server_supports_viaversion "${software}"; fi
    reject server_allows_in_place_switch paper "${software}"
    reject server_allows_in_place_switch "${software}" purpur
    server_allows_in_place_switch "${software}" "${software}"
    MINECRAFT_SERVER_TYPE=${software} BEDROCK_ENABLED=yes
    if [[ ${software} == forge || ${software} == quilt ]]; then reject validate_server_capabilities; else validate_server_capabilities; fi
    BEDROCK_ENABLED=no
    validate_server_capabilities
    loader=0.16.10 image=java21
    if [[ ${software} == spigot ]]; then loader='' image=java21-jdk; fi
    for version in 1.20.5 1.20.6 1.21 1.21.11 26.1 26.1.1 26.2; do
        selected_image=${image}
        [[ ${version} != 26.* ]] || selected_image=${image/21/25}
        validate_java_server_selection "${software}" pinned "${version}" "${loader}" "${selected_image}"
    done
    for version in 1.20.4 1.20.7 1.21.12 26 26.0 26.01 26.1.01 26.1.1.1 26.1-snapshot-1 26.1-pre-1 26.1-rc-1 26w01a 27.1; do
        reject validate_java_server_selection "${software}" pinned "${version}" "${loader}" "${image/21/25}"
    done
    reject validate_java_server_selection "${software}" pinned 1.21.1 "${loader}" "${image/21/25}"
    reject validate_java_server_selection "${software}" recommended 26.1 "${loader}" "${image/21/25}"
    reject validate_java_server_selection "${software}" latest 26.1 "${loader}" "${image/21/25}"
    wrong_image=java25-jdk
    [[ ${software} != spigot ]] || wrong_image=java25
    reject validate_java_server_selection "${software}" pinned 26.1 "${loader}" "${wrong_image}"
    reject validate_java_server_selection "${software}" recommended 1.21.1 "${loader}" "${image}"
    reject validate_java_server_selection "${software}" latest LATEST "${loader}" "${image}"
    reject validate_java_server_selection "${software}" pinned 1.20.1 "${loader}" "${image}"
    reject validate_java_server_selection "${software}" pinned 26.1 "${loader}" "${image}"
    reject validate_java_server_selection "${software}" pinned 1.21.1 "${loader}" stable
    reject resolve_latest_server_version
    reject resolve_recommended_server_version
done
server_supports_bukkit_plugins spigot
reject server_is_mod_loader spigot
for software in forge neoforge fabric quilt; do
    server_is_mod_loader "${software}"
    reject server_supports_bukkit_plugins "${software}"
    for loader in '' latest recommended beta $'0.16.10\nTYPE=PAPER' https://example.org/loader.jar; do
        reject validate_java_server_selection "${software}" pinned 1.21.1 "${loader}" java21
        reject validate_java_server_selection "${software}" pinned 26.1 "${loader}" java25
    done
done
for software in paper purpur vanilla; do
    validate_java_server_selection "${software}" latest LATEST '' stable
done
server_allows_in_place_switch paper purpur
server_allows_in_place_switch purpur paper
reject server_allows_in_place_switch paper vanilla

# Recognition must not silently enable the existing portable migration format,
# which has no loader-version contract. Reject before reading any host paths.
source <(sed -n '/^jv_migration_load_instance() {/,/^}/p' "${repo_root}/mjust/libexec/migration-instance.sh")
jv_load_instance() { :; }
for software in spigot forge neoforge fabric quilt; do
    MINECRAFT_SERVER_TYPE=${software}
    if output="$(jv_migration_load_instance jv-aaaaaa 2>&1)"; then
        echo 'New server software entered migration' >&2; exit 1
    fi
    [[ ${output} == *'migration for this server software requires separate compatibility support'* ]]
done

# Curl can only return a fixture for the exact reviewed upstream URL. Any
# fallback request fails. The actual parsers and compatibility checks run here.
curl() {
    if [[ ${*: -1} == https://piston-meta.mojang.com/mc/game/version_manifest_v2.json ]]; then
        [[ ${offline:-no} != yes ]] || return 22
        printf '%s' '{"versions":[{"id":"1.21.1","type":"release"},{"id":"1.21.2","type":"release"},{"id":"26.1","type":"release"},{"id":"26.1.1","type":"release"},{"id":"26.2","type":"release"},{"id":"26.3","type":"snapshot"}]}'
        return
    fi
    [[ ${*: -1} == "${expected_url}" ]] || { echo 'Unexpected upstream URL' >&2; return 1; }
    [[ ${offline:-no} != yes && ${missing_pair:-no} != yes ]] || return 22
    printf '%s' "${metadata}"
}
hash=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
for version in 1.21.1 26.1; do
    expected_url="https://hub.spigotmc.org/versions/${version}.json"
    java_class=65 wrong_class=69
    [[ ${version} != 26.* ]] || { java_class=69 wrong_class=65; }
    metadata="{\"javaVersions\":[${java_class}],\"refs\":{\"BuildData\":\"${hash}\",\"Bukkit\":\"${hash}\",\"CraftBukkit\":\"${hash}\",\"Spigot\":\"${hash}\"}}"
    java_server_version_available spigot "${version}" ''
    missing_pair=yes
    reject java_server_version_available spigot "${version}" ''
    missing_pair=no
    metadata=${metadata/"[${java_class}]"/"[${wrong_class}]"}
    reject java_server_version_available spigot "${version}" ''
    metadata='{"refs":{"Spigot":"unverified"}}'
    reject java_server_version_available spigot "${version}" ''
done

for version in 1.21.1 26.1; do
    loader=52.0.28
    [[ ${version} != 26.* ]] || loader=62.0.1
    expected_url="https://maven.minecraftforge.net/net/minecraftforge/forge/${version}-${loader}/forge-${version}-${loader}-installer.jar.sha1"
    metadata=${hash}
    java_server_version_available forge "${version}" "${loader}"
    reject java_server_version_available forge "${version}" "${loader}.999"
    reject java_server_version_available forge 26.2 "${loader}"
    missing_pair=yes
    reject java_server_version_available forge "${version}" "${loader}"
    missing_pair=no
done
for version in 1.21.1 26.1 26.1.1; do
    case ${version} in
        1.21.1) loader=21.1.172 ;;
        26.1) loader=26.1.0.5-beta ;;
        26.1.1) loader=26.1.1.5-beta ;;
    esac
    expected_url="https://maven.neoforged.net/releases/net/neoforged/neoforge/${loader}/neoforge-${loader}-installer.jar.sha1"
    metadata=${hash}
    java_server_version_available neoforge "${version}" "${loader}"
    missing_pair=yes
    reject java_server_version_available neoforge "${version}" "${loader}"
    missing_pair=no
    reject java_server_version_available neoforge 1.21.2 "${loader}"
    reject java_server_version_available neoforge 26.2 "${loader}"
    reject java_server_version_available neoforge "${version}" "${loader}.999"
    metadata=$'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\ninvalid'
    reject java_server_version_available neoforge "${version}" "${loader}"
done
# A matching artifact checksum cannot override a mismatched game prefix.
metadata=${hash}
for pair in '26.1 26.1.1.5-beta' '26.1.1 26.1.0.5-beta' '26.1 26.1.5-beta' '26.1 21.1.172'; do
    read -r version loader <<< "${pair}"
    expected_url="https://maven.neoforged.net/releases/net/neoforged/neoforge/${loader}/neoforge-${loader}-installer.jar.sha1"
    reject java_server_version_available neoforge "${version}" "${loader}"
done

for software in fabric quilt; do
    for version in 1.21.1 26.1; do
        if [[ ${software} == fabric ]]; then
            expected_url=https://meta.fabricmc.net/v2/versions/loader/${version}/0.16.10/server/json
            coordinate=net.fabricmc:fabric-loader:0.16.10
            main_class=net.fabricmc.loader.impl.launch.knot.KnotServer
        else
            expected_url=https://meta.quiltmc.org/v3/versions/loader/${version}/0.26.0/server/json
            coordinate=org.quiltmc:quilt-loader:0.26.0
            main_class=org.quiltmc.loader.impl.launch.knot.KnotServer
        fi
        loader=${coordinate##*:}
        metadata="{\"inheritsFrom\":\"${version}\",\"mainClass\":\"${main_class}\",\"libraries\":[{\"name\":\"${coordinate}\"}]}"
        java_server_version_available "${software}" "${version}" "${loader}"
        missing_pair=yes
        reject java_server_version_available "${software}" "${version}" "${loader}"
        missing_pair=no
        reject java_server_version_available "${software}" "${version}" "${loader}.999"
        offline=yes
        reject java_server_version_available "${software}" "${version}" "${loader}"
        offline=no
        metadata="{\"inheritsFrom\":\"1.21.2\",\"mainClass\":\"${main_class}\",\"libraries\":[{\"name\":\"${coordinate}\"}]}"
        reject java_server_version_available "${software}" "${version}" "${loader}"
        metadata="{\"inheritsFrom\":\"${version}\",\"mainClass\":\"${main_class}\",\"libraries\":[{\"name\":\"another:loader:${loader}\"}]}"
        reject java_server_version_available "${software}" "${version}" "${loader}"
        metadata='{}'
        reject java_server_version_available "${software}" "${version}" "${loader}"
    done
done
# Even valid project metadata cannot authorize a missing/non-release game.
for version in 26.3 26.99; do
    for software in spigot forge neoforge fabric quilt; do
        loader=0.16.10 metadata=${hash}
        case ${software} in
            spigot)
                loader=''
                expected_url="https://hub.spigotmc.org/versions/${version}.json"
                metadata="{\"javaVersions\":[69],\"refs\":{\"BuildData\":\"${hash}\",\"Bukkit\":\"${hash}\",\"CraftBukkit\":\"${hash}\",\"Spigot\":\"${hash}\"}}"
                ;;
            forge)
                expected_url="https://maven.minecraftforge.net/net/minecraftforge/forge/${version}-${loader}/forge-${version}-${loader}-installer.jar.sha1"
                ;;
            neoforge)
                loader=${version}.0.5-beta
                expected_url="https://maven.neoforged.net/releases/net/neoforged/neoforge/${loader}/neoforge-${loader}-installer.jar.sha1"
                ;;
            fabric|quilt)
                if [[ ${software} == fabric ]]; then
                    expected_url="https://meta.fabricmc.net/v2/versions/loader/${version}/${loader}/server/json"
                    coordinate=net.fabricmc:fabric-loader:${loader}
                else
                    expected_url="https://meta.quiltmc.org/v3/versions/loader/${version}/${loader}/server/json"
                    coordinate=org.quiltmc:quilt-loader:${loader}
                fi
                metadata="{\"inheritsFrom\":\"${version}\",\"mainClass\":\"example.KnotServer\",\"libraries\":[{\"name\":\"${coordinate}\"}]}"
                ;;
        esac
        reject java_server_version_available "${software}" "${version}" "${loader}"
    done
done
echo 'Java server capability and exact-release fixture checks passed.'
