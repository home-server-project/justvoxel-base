#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
cat > "${fixture_dir}/common.sh" <<'STUB'
require_root() { :; }
require_config() { :; }
resolve_latest_stable_paper_version() { printf '%s' "${PAPER_STABLE:-26.3}"; }
resolve_latest_available_paper_version() { printf '%s' "${PAPER_AVAILABLE:-26.3}"; }
resolve_geyser_supported_java_version() { [[ ${GEYSER_SUPPORT:-26.2} != unavailable ]] && printf '%s' "${GEYSER_SUPPORT:-26.2}"; }
paper_version_has_stable_build() { [[ $1 == 26.2 || $1 == 26.3 ]]; }
paper_version_build_channel() { [[ $1 == 26.2 || $1 == 26.3 ]] && printf STABLE; }
bedrock_crossplay_supports_version() { [[ -n $1 && $1 == "$2" ]]; }
systemctl() { return 1; }
minecraft_image_ref() { printf 'docker.io/itzg/minecraft-server:%s' "${MINECRAFT_IMAGE_TAG}"; }
validate_minecraft_image_tag() { [[ $1 != missing && $1 != deprecated ]]; }
jv_remote_platform_digest() { [[ ${IMAGE_STATE:-current} != unavailable ]] && printf 'sha256:%064d' 1; }
podman() {
    if [[ $* == *'.Digest'* ]]; then printf 'sha256:%064d' "${LOCAL_DIGEST_NUMBER:-1}";
    else printf '%s' "${LOCAL_ID:-image-id}"; fi
}
jv_crossplay_metadata() {
    [[ ${COMPONENT_STATE:-up_to_date} != unavailable ]] || return 1
    printf '{"project":"%s","version":"2.99.0","build":19,"artifact":"fixture.jar","sha256":"%064d"}' "$1" 1
}
jv_crossplay_artifact_state() { printf '%s' "${COMPONENT_STATE:-up_to_date}"; }
STUB
sed -n '/^# Three fixed implementations/,/^render_runtime_files() {/p' "${repo_root}/mjust/libexec/common.sh" | sed '$d' >> "${fixture_dir}/common.sh"
sed -e "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    -e '\|^source /usr/libexec/justvoxel/mjust/managed-crossplay.sh$|d' \
    -e '\|^source /usr/libexec/justvoxel/mjust/update-policy.sh$|d' \
    "${repo_root}/mjust/libexec/admin-version-status-json" > "${fixture_dir}/status"
export DATA_PATH="${fixture_dir}/data" MINECRAFT_VERSION_MODE=recommended MINECRAFT_VERSION=26.2 MINECRAFT_IMAGE_TAG=stable BEDROCK_ENABLED=yes
mkdir -p "${DATA_PATH}/logs"
printf 'Starting minecraft server version 26.2\n' > "${DATA_PATH}/logs/latest.log"
recommended="$(bash "${fixture_dir}/status" recommended)"
jq -e '.installed == "26.2" and .available == "26.3" and .recommended == "26.2" and
    .selected_candidate == "26.2" and .configured_version == "26.2" and .policy == "recommended" and
    .update_available == false and .stack_state == "waiting_for_compatibility" and
    .minecraft_state == "up_to_date" and .paper_supported and .crossplay_compatible and
    .viaversion_state == "managed_automatically" and
    (.reason | contains("Minecraft 26.3 is available") and contains("released cross-play support") and contains("No action is needed"))' <<< "${recommended}" >/dev/null
# Released Java support advances dynamically, with no source version switch.
advanced="$(GEYSER_SUPPORT=26.3 bash "${fixture_dir}/status" recommended)"
jq -e '.recommended == "26.3" and .selected_candidate == "26.3" and .stack_state == "updates_available" and .update_available' <<< "${advanced}" >/dev/null
# Component-only maintenance leaves the game version unchanged.
components="$(COMPONENT_STATE=updates_available bash "${fixture_dir}/status" recommended)"
jq -e '.installed == .selected_candidate and .minecraft_state == "up_to_date" and .stack_state == "updates_available" and .update_available and .geyser_state == "updates_available" and (.reason | contains("No action") | not)' <<< "${components}" >/dev/null
current="$(PAPER_AVAILABLE=26.2 PAPER_STABLE=26.2 bash "${fixture_dir}/status" recommended)"
jq -e '.stack_state == "up_to_date" and (.update_available | not)' <<< "${current}" >/dev/null
for setting in 'COMPONENT_STATE=unavailable' 'GEYSER_SUPPORT=unavailable' 'IMAGE_STATE=unavailable'; do
    unknown="$(env "${setting}" bash "${fixture_dir}/status" recommended)"
    jq -e '.stack_state == "unavailable" and (.update_available | not) and (.reason | contains("No action") | not)' <<< "${unknown}" >/dev/null
done
latest="$(bash "${fixture_dir}/status" latest)"
jq -e '.policy == "latest" and .selected_candidate == "26.3" and .paper_supported and (.crossplay_compatible | not) and (.reason | contains("not compatible"))' <<< "${latest}" >/dev/null
pinned="$(bash "${fixture_dir}/status" pinned 26.2)"
jq -e '.policy == "pinned" and .selected_candidate == "26.2" and .paper_supported and .crossplay_compatible' <<< "${pinned}" >/dev/null
pinned_incompatible="$(bash "${fixture_dir}/status" pinned 26.3)"
jq -e '.policy == "pinned" and .selected_candidate == "26.3" and (.crossplay_compatible | not) and .stack_state == "unavailable"' <<< "${pinned_incompatible}" >/dev/null
rm "${DATA_PATH}/logs/latest.log"
unknown="$(bash "${fixture_dir}/status" recommended)"
jq -e '.installed == "" and .configured_version == "26.2" and (.update_available | not) and .stack_state == "unavailable"' <<< "${unknown}" >/dev/null
without_crossplay="$(bash "${fixture_dir}/status" recommended '' no)"
jq -e '.recommended == "26.3" and .selected_candidate == "26.3" and .geyser_state == "not_applicable" and .stack_state == "not_applicable"' <<< "${without_crossplay}" >/dev/null

# First-run preview remains usable with no installed game or managed binaries,
# and with local image/component checks deliberately unavailable.
preview="$(COMPONENT_STATE=unavailable IMAGE_STATE=unavailable bash "${fixture_dir}/status" recommended '' yes)"
jq -e '.installed == "" and .recommended == "26.2" and .selected_candidate == "26.2" and .crossplay_compatible and .paper_supported and .stack_state == "not_applicable" and (.reason | contains("Server update information could not be verified") | not)' <<< "${preview}" >/dev/null
preview_incompatible="$(COMPONENT_STATE=unavailable IMAGE_STATE=unavailable bash "${fixture_dir}/status" pinned 26.3 yes)"
jq -e '.selected_candidate == "26.3" and (.crossplay_compatible | not) and .stack_state == "not_applicable" and (.reason | contains("not compatible"))' <<< "${preview_incompatible}" >/dev/null

cat >> "${fixture_dir}/common.sh" <<'STUB'
curl() {
    case "${*: -1}" in
        */v2/purpur) printf '{"metadata":{"current":"26.2"},"versions":["26.2","26.3"]}' ;;
        */version_manifest_v2.json) printf '{"latest":{"release":"26.3"},"versions":[{"id":"26.3","type":"release"},{"id":"26.2","type":"release"},{"id":"26.4-snapshot","type":"snapshot"}]}' ;;
        *) return 1 ;;
    esac
}
STUB
purpur="$(MINECRAFT_SERVER_TYPE=purpur bash "${fixture_dir}/status" recommended '' yes)"
jq -e '.server_type == "purpur" and .server_software == "Purpur" and .recommended == "26.2" and .available == "26.3" and .server_supported and .crossplay_compatible and (.paper_supported | not)' <<< "${purpur}" >/dev/null
vanilla="$(MINECRAFT_SERVER_TYPE=vanilla BEDROCK_ENABLED=no bash "${fixture_dir}/status" latest '' no)"
jq -e '.server_software == "Vanilla" and .selected_candidate == "26.3" and .server_supported and .viaversion_state == "not_applicable" and (.crossplay_enabled | not)' <<< "${vanilla}" >/dev/null
if MINECRAFT_SERVER_TYPE=vanilla BEDROCK_ENABLED=no bash "${fixture_dir}/status" recommended '' yes; then echo 'Vanilla Bedrock preview accepted' >&2; exit 1; fi

# A software-only change is actionable, and its review cannot authorize a different target.
printf 'Starting minecraft server version 26.2\n' > "${DATA_PATH}/logs/latest.log"
paper="$(PAPER_AVAILABLE=26.2 PAPER_STABLE=26.2 bash "${fixture_dir}/status" recommended)"
switch="$(bash "${fixture_dir}/status" --server-type purpur recommended)"
jq -e '.server_type == "purpur" and .server_software == "Purpur" and .stack_state == "updates_available" and .crossplay_compatible' <<< "${switch}" >/dev/null
[[ $(jq -r '.plan_fingerprint' <<< "${switch}") != "$(jq -r '.plan_fingerprint' <<< "${paper}")" ]]
preview="$(bash "${fixture_dir}/status" --server-type vanilla recommended '' no)"
jq -e '.server_type == "vanilla" and .server_supported and (.crossplay_enabled | not)' <<< "${preview}" >/dev/null
if bash "${fixture_dir}/status" --server-type vanilla recommended; then echo 'In-place Vanilla status accepted' >&2; exit 1; fi

# Combined previews bind both selections and image identity without writing state.
export JV_CONFIG="${fixture_dir}/config"
printf 'fixture administrator configuration\n' > "${JV_CONFIG}"
before="$(sha256sum "${JV_CONFIG}" "${DATA_PATH}/logs/latest.log")"
combined="$(bash "${fixture_dir}/status" --server-type purpur --image-tag latest recommended)"
jq -e '.configured_server_type == "paper" and .configured_image_tag == "stable" and
    .server_type == "purpur" and .image_tag == "latest" and .runtime_update_required and .update_available' <<< "${combined}" >/dev/null
channel="$(bash "${fixture_dir}/status" --image-tag latest recommended)"
jq -e '.update_available and (.runtime_update_required | not) and .image_state == "up_to_date"' <<< "${channel}" >/dev/null
custom="$(bash "${fixture_dir}/status" --image-tag release_1.2-3 recommended)"
for other in "${paper}" "${switch}" "${channel}" "${custom}"; do
    [[ $(jq -r '.plan_fingerprint' <<< "${combined}") != "$(jq -r '.plan_fingerprint' <<< "${other}")" ]]
done
repeat="$(bash "${fixture_dir}/status" --server-type purpur --image-tag latest recommended)"
[[ $(jq -r '.plan_fingerprint' <<< "${repeat}") == "$(jq -r '.plan_fingerprint' <<< "${combined}")" ]]
identity="$(LOCAL_ID=other-image bash "${fixture_dir}/status" --server-type purpur --image-tag latest recommended)"
[[ $(jq -r '.plan_fingerprint' <<< "${identity}") != "$(jq -r '.plan_fingerprint' <<< "${combined}")" ]]
changed_digest="$(LOCAL_DIGEST_NUMBER=2 bash "${fixture_dir}/status" --server-type purpur --image-tag latest recommended)"
[[ $(jq -r '.plan_fingerprint' <<< "${changed_digest}") != "$(jq -r '.plan_fingerprint' <<< "${combined}")" ]]
for invalid in '../bad' '-bad' 'tag:other' missing deprecated "$(printf 'a%.0s' {1..129})"; do
    if bash "${fixture_dir}/status" --image-tag "${invalid}" recommended; then echo 'Unsafe custom preview accepted' >&2; exit 1; fi
done
[[ $(sha256sum "${JV_CONFIG}" "${DATA_PATH}/logs/latest.log") == "${before}" ]]

# Exercise the actual fixed-repository registry/deprecation validation too.
sed -n '/^validate_minecraft_image_tag() {/,/^resolve_latest_itzg_release() {/p' "${repo_root}/mjust/libexec/common.sh" | sed '$d' > "${fixture_dir}/tag-validation"
(
    source "${fixture_dir}/tag-validation"
    JV_MINECRAFT_IMAGE_REPO=docker.io/itzg/minecraft-server
    JV_ITZG_IMAGES_URL=https://fixture.invalid/catalog
    skopeo() { [[ $1 == inspect && $2 == docker://docker.io/itzg/minecraft-server:* && $2 != *:missing ]]; }
    curl() { printf '[{"tag":"deprecated","deprecated":true},{"tag":"stable","deprecated":false}]'; }
    for valid in stable latest release_1.2-3; do validate_minecraft_image_tag "${valid}"; done
    for invalid in missing deprecated '../bad' 'repo:tag'; do
        if validate_minecraft_image_tag "${invalid}"; then echo 'Registry/deprecation validation bypassed' >&2; exit 1; fi
    done
)
