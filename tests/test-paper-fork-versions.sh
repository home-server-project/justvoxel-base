#!/usr/bin/bash
# Offline fixtures for exact official metadata and bounded recommendation search.
# This script is written for separate verification; it does not access an appliance.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
source "${repo_root}/mjust/libexec/common.sh"
reject() { if "$@"; then echo 'Unsupported fork version accepted' >&2; exit 1; fi; }
fixture=valid
curl() {
    [[ ${fixture} != offline ]] || return 22
    case "${*: -1}" in
        https://piston-meta.mojang.com/mc/game/version_manifest_v2.json)
            printf '%s' '{"latest":{"release":"26.3"},"versions":[{"id":"26.3","type":"release"},{"id":"1.21.10","type":"release"},{"id":"1.21.8","type":"release"},{"id":"1.21.8-pre1","type":"snapshot"}]}' ;;
        https://ci.pufferfish.host/job/Pufferfish-1.21/lastSuccessfulBuild/api/json)
            case "${fixture}" in
                empty) printf '%s' '{"result":"SUCCESS","number":39,"artifacts":[]}' ;;
                failed) printf '%s' '{"result":"FAILURE","number":39,"artifacts":[{"fileName":"pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar","relativePath":"build/libs/pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar"}]}' ;;
                unsafe) printf '%s' '{"result":"SUCCESS","number":39,"artifacts":[{"fileName":"pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar","relativePath":"../pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar"}]}' ;;
                *) printf '%s' '{"result":"SUCCESS","number":39,"artifacts":[{"fileName":"pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar","relativePath":"build/libs/pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar"}]}' ;;
            esac ;;
        https://api.leafmc.one/v2/projects/leaf/versions/1.21.8)
            case "${fixture}" in
                empty) printf '%s' '{"project_id":"leaf","version":"1.21.8","builds":[]}' ;;
                mismatch) printf '%s' '{"project_id":"leaf","version":"1.21.7","builds":[53]}' ;;
                *) printf '%s' '{"project_id":"leaf","version":"1.21.8","builds":[51,53]}' ;;
            esac ;;
        https://api.leafmc.one/v2/projects/leaf/versions/1.21.8/builds/53)
            case "${fixture}" in
                failed) printf '%s' '{"project_id":"leaf","version":"1.21.7","build":53,"downloads":{}}' ;;
                unsafe) printf '%s' '{"project_id":"leaf","version":"1.21.8","build":53,"downloads":{"application":{"name":"leaf-1.21.8-53.jar","sha256":"invalid"}}}' ;;
                wrong-artifact) printf '%s' '{"project_id":"leaf","version":"1.21.8","build":53,"downloads":{"application":{"name":"leaf-1.21.7-53.jar","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}' ;;
                *) printf '%s' '{"project_id":"leaf","version":"1.21.8","build":53,"downloads":{"application":{"name":"leaf-1.21.8-53.jar","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}' ;;
            esac ;;
        *) return 22 ;; # No unlisted upstream/version may acquire invented builds.
    esac
}
for software in pufferfish leaf; do
    version=1.21.8
    [[ ${software} != pufferfish ]] || version=1.21.10
    validate_server_type "${software}"
    server_supports_bukkit_plugins "${software}"
    server_supports_viaversion "${software}"
    server_supports_managed_crossplay "${software}"
    reject server_is_mod_loader "${software}"
    validate_java_server_selection "${software}" pinned "${version}" '' java21
    reject validate_java_server_selection "${software}" pinned "${version}" 0.1.0 java21
    java_server_version_available "${software}" "${version}"
    reject java_server_version_available "${software}" "${version}" 0.1.0
    reject java_server_version_available "${software}" 26.3
    reject java_server_version_available "${software}" 1.21.8-pre1
    reject java_server_version_available "${software}" 1.21.9
    for fixture in empty failed unsafe offline; do
        reject java_server_version_available "${software}" "${version}"
        reject resolve_setup_java_runtime "${software}" recommended ''
    done
    fixture=valid
    selected="$(resolve_setup_java_runtime "${software}" recommended '')"
    jq -e --arg version "${version}" '.game == $version and .loader == "" and .image == "java21"' <<< "${selected}" >/dev/null
    reject resolve_setup_java_runtime "${software}" pinned 26.3
    MINECRAFT_SERVER_TYPE=${software}
    [[ $(resolve_recommended_server_version) == "${version}" ]]
    [[ $(resolve_latest_server_version) == "${version}" ]]
    [[ $(server_version_channel "${version}") == RELEASE ]]
    for other in paper purpur pufferfish leaf; do
        server_allows_in_place_switch "${software}" "${other}"
        server_allows_in_place_switch "${other}" "${software}"
    done
    for other in spigot vanilla fabric forge neoforge quilt bedrock; do
        reject server_allows_in_place_switch "${software}" "${other}"
        reject server_allows_in_place_switch "${other}" "${software}"
        server_allows_in_place_switch "${other}" "${other}"
    done
    # Paper forks retain Paper's exact Geyser version compatibility check.
    server_crossplay_version_compatible "${version}" "${version}"
    reject server_crossplay_version_compatible "${version}" 26.3
done
fixture=valid
reject java_server_version_available pufferfish 1.21.8 # artifact proves 1.21.10 only
for fixture in mismatch wrong-artifact; do reject java_server_version_available leaf 1.21.8; done
