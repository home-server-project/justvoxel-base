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
            printf '%s' '{"latest":{"release":"26.3"},"versions":[{"id":"26.3","type":"release"},{"id":"1.21.10","type":"release"},{"id":"1.21.11","type":"release"},{"id":"1.21.8","type":"release"},{"id":"1.21.8-pre1","type":"snapshot"}]}' ;;
        'https://ci.pufferfish.host/api/json?tree=jobs%5Bname%5D')
            # Only published jobs may be queried; no invented 26.x job probes.
            if [[ ${fixture} == no-jobs ]]; then
                printf '%s' '{"jobs":[]}'
            else
                printf '%s' '{"jobs":[{"name":"Pufferfish-1.21"},{"name":"Pufferfish-1.20"},{"name":"Pufferfish-26.3/unsafe"},{"name":"Other"}]}'
            fi ;;
        https://ci.pufferfish.host/job/Pufferfish-1.21/lastSuccessfulBuild/api/json)
            case "${fixture}" in
                reobf|both|ambiguous|nonrelease|malformed|zero|fractional|absolute|backslash)
                    jq -cn --arg fixture "${fixture}" '
                        {result:"SUCCESS",number:39,artifacts:[{fileName:"pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar",relativePath:"build/libs/pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar"}]} |
                        if $fixture == "reobf" then .artifacts |= map(.fileName |= sub("mojmap";"reobf") | .relativePath |= sub("mojmap";"reobf"))
                        elif $fixture == "both" then .artifacts += [(.artifacts[0] | .fileName |= sub("mojmap";"reobf") | .relativePath |= sub("mojmap";"reobf"))]
                        elif $fixture == "ambiguous" then .artifacts += [{fileName:"pufferfish-paperclip-1.21.8-R0.1-SNAPSHOT-reobf.jar",relativePath:"pufferfish-paperclip-1.21.8-R0.1-SNAPSHOT-reobf.jar"}]
                        elif $fixture == "nonrelease" then .artifacts |= map(.fileName |= sub("1.21.10";"1.21.9") | .relativePath |= sub("1.21.10";"1.21.9"))
                        elif $fixture == "malformed" then .artifacts[0].fileName="pufferfish-paperclip-latest.jar"
                        elif $fixture == "zero" then .number=0
                        elif $fixture == "fractional" then .number=39.5
                        elif $fixture == "absolute" then .artifacts[0].relativePath="/" + .artifacts[0].relativePath
                        elif $fixture == "backslash" then .artifacts[0].relativePath="..\\" + .artifacts[0].fileName else . end' ;;
                empty) printf '%s' '{"result":"SUCCESS","number":39,"artifacts":[]}' ;;
                failed) printf '%s' '{"result":"FAILURE","number":39,"artifacts":[{"fileName":"pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar","relativePath":"build/libs/pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar"}]}' ;;
                unsafe) printf '%s' '{"result":"SUCCESS","number":39,"artifacts":[{"fileName":"pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar","relativePath":"../pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar"}]}' ;;
                *) printf '%s' '{"result":"SUCCESS","number":39,"artifacts":[{"fileName":"pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar","relativePath":"build/libs/pufferfish-paperclip-1.21.10-R0.1-SNAPSHOT-mojmap.jar"}]}' ;;
            esac ;;
        https://api.leafmc.one/v2/projects/leaf)
            printf '%s' '{"project_id":"leaf","versions":["1.21.11","26.3"]}' ;;
        https://api.leafmc.one/v2/projects/leaf/versions/26.3|https://api.leafmc.one/v2/projects/leaf/versions/1.21.11)
            local version="${*: -1}" build=13
            version="${version##*/}"
            [[ ${version} != 1.21.11 ]] || build=179
            jq -cn --arg version "${version}" --argjson build "${build}" --arg fixture "${fixture}" '
                {project_id:"leaf",version:$version,builds:[$build]} |
                if $fixture == "empty" then .builds=[]
                elif $fixture == "mismatch" then .version="wrong"
                elif $fixture == "string-build" then .builds=["13"]
                elif $fixture == "fractional-build" then .builds=[13.5]
                elif $fixture == "zero-build" then .builds=[0] else . end' ;;
        https://api.leafmc.one/v2/projects/leaf/versions/26.3/builds/13|https://api.leafmc.one/v2/projects/leaf/versions/1.21.11/builds/179)
            local path="${*: -1}" version build channel=experimental
            build="${path##*/}"
            version="${path%/builds/*}"; version="${version##*/}"
            [[ ${version} != 1.21.11 ]] || channel=default
            jq -cn --arg version "${version}" --argjson build "${build}" --arg channel "${channel}" --arg fixture "${fixture}" '
                {project_id:"leaf",version:$version,build:$build,channel:$channel,
                 downloads:{application:{name:("leaf-" + $version + "-" + ($build|tostring) + ".jar"),sha256:("a" * 64)}}} |
                if $fixture == "failed" then .version="wrong"
                elif $fixture == "unsafe" then .downloads.application.sha256="invalid"
                elif $fixture == "wrong-artifact" then .downloads.application.name="leaf-wrong.jar"
                elif $fixture == "keyed-download" then .downloads={server:.downloads.application}
                elif $fixture == "duplicate-download" then .downloads.server=.downloads.application
                elif $fixture == "wrong-build" then .build=999
                elif $fixture == "wrong-project" then .project_id="other"
                elif $fixture == "missing-hash" then del(.downloads.application.sha256)
                elif $fixture == "unknown-channel" then .channel="unknown" else . end' ;;
        *) return 22 ;; # No unlisted upstream/version may acquire invented builds.
    esac
}
for software in pufferfish leaf; do
    version=26.3 image=java25
    [[ ${software} != pufferfish ]] || { version=1.21.10; image=java21; }
    validate_server_type "${software}"
    server_supports_bukkit_plugins "${software}"
    server_supports_viaversion "${software}"
    server_supports_managed_crossplay "${software}"
    reject server_is_mod_loader "${software}"
    validate_java_server_selection "${software}" pinned "${version}" '' "${image}"
    reject validate_java_server_selection "${software}" pinned "${version}" 0.1.0 "${image}"
    java_server_version_available "${software}" "${version}"
    reject java_server_version_available "${software}" "${version}" 0.1.0
    reject java_server_version_available "${software}" 26.4
    reject java_server_version_available "${software}" 1.21.8-pre1
    reject java_server_version_available "${software}" 1.21.9
    for fixture in empty failed unsafe offline; do
        reject java_server_version_available "${software}" "${version}"
        reject resolve_setup_java_runtime "${software}" recommended ''
    done
    fixture=valid
    selected="$(resolve_setup_java_runtime "${software}" recommended '')"
    jq -e --arg version "${version}" --arg image "${image}" '.game == $version and .loader == "" and .image == $image' <<< "${selected}" >/dev/null
    reject resolve_setup_java_runtime "${software}" pinned 1.21.9
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
    reject server_crossplay_version_compatible "${version}" 26.4
done
fixture=valid
reject java_server_version_available pufferfish 1.21.8 # artifact proves 1.21.10 only
for fixture in mismatch wrong-artifact duplicate-download wrong-build wrong-project missing-hash unknown-channel string-build fractional-build zero-build; do
    reject java_server_version_available leaf 26.3
    reject resolve_setup_java_runtime leaf recommended ''
done
fixture=valid
selected="$(resolve_setup_java_runtime leaf recommended '')"
jq -e '.game == "26.3" and (.reason | contains("Experimental"))' <<< "${selected}" >/dev/null
specific="$(resolve_setup_java_runtime leaf pinned 1.21.11)"
jq -e '.game == "1.21.11" and .reason == ""' <<< "${specific}" >/dev/null
specific="$(resolve_setup_java_runtime pufferfish pinned 1.21.10)"
jq -e '.game == "1.21.10"' <<< "${specific}" >/dev/null
reject resolve_setup_java_runtime pufferfish pinned 26.3
# A single usable identity, official release, safe path and positive integer
# build are mandatory even when another archived JAR looks plausible.
for fixture in ambiguous nonrelease malformed zero fractional absolute backslash; do
    reject resolve_setup_java_runtime pufferfish recommended ''
    reject java_server_version_available pufferfish 1.21.10
done

for fixture in reobf both; do
    java_server_version_available pufferfish 1.21.10
    selected="$(resolve_setup_java_runtime pufferfish recommended '')"
    jq -e '.game == "1.21.10"' <<< "${selected}" >/dev/null
done

# Leaf v2 download records retain exact identity and SHA-256 independently of
# their map key. Experimental remains visible and uses the supported Java image.
fixture=keyed-download
selected="$(resolve_setup_java_runtime leaf recommended '')"
jq -e '.game == "26.3" and .image == "java25" and (.reason | contains("Leaf labels this build Experimental. It is available for setup."))' <<< "${selected}" >/dev/null

fixture=no-jobs
reject resolve_setup_java_runtime pufferfish recommended ''
