#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
cat > "${fixture_dir}/common.sh" <<'STUB'
require_root() { :; }
require_config() { echo "Legacy config loaded" >&2; exit 91; }
resolve_latest_stable_paper_version() { printf '%s' "${PAPER_STABLE:-26.3}"; }
resolve_latest_available_paper_version() { printf '%s' "${PAPER_AVAILABLE:-26.3}"; }
resolve_geyser_supported_java_version() { [[ ${GEYSER_SUPPORT:-26.2} != unavailable ]] && printf '%s' "${GEYSER_SUPPORT:-26.2}"; }
paper_version_has_stable_build() { [[ $1 == 26.2 || $1 == 26.3 ]]; }
paper_version_build_channel() { [[ $1 == 26.2 || $1 == 26.3 ]] && printf STABLE; }
bedrock_crossplay_supports_version() { [[ -n $1 && $1 == "$2" ]]; }
systemctl() { printf 'systemctl %s\n' "$*" >> "${RUNTIME_CALLS}"; return "${SERVICE_STATUS:-1}"; }
minecraft_image_ref() { printf 'docker.io/itzg/minecraft-server:%s' "${MINECRAFT_IMAGE_TAG}"; }
validate_minecraft_image_tag() { [[ $1 != missing && $1 != deprecated ]]; }
jv_remote_platform_digest() { [[ ${IMAGE_STATE:-current} != unavailable ]] && printf 'sha256:%064d' 1; }
podman() {
    printf 'podman %s\n' "$*" >> "${RUNTIME_CALLS}"
    if [[ $* == *'.Digest'* ]]; then printf 'sha256:%064d' "${LOCAL_DIGEST_NUMBER:-1}";
    else printf '%s' "${LOCAL_ID:-image-id}"; fi
}
jv_crossplay_metadata() {
    [[ ${COMPONENT_STATE:-up_to_date} != unavailable ]] || return 1
    printf '{"project":"%s","version":"2.99.0","build":19,"artifact":"fixture.jar","sha256":"%064d"}' "$1" 1
}
jv_crossplay_artifact_state() { printf '%s' "${COMPONENT_STATE:-up_to_date}"; }
STUB
# Copy the real compatibility wrapper; its Paper metadata dependency is stubbed above.
sed -n '/^server_crossplay_version_compatible() {/,/^}/p' "${repo_root}/mjust/libexec/common.sh" >> "${fixture_dir}/common.sh"
sed -n '/^validate_server_type() {/,/^render_runtime_files() {/p' "${repo_root}/mjust/libexec/common.sh" | sed '$d' >> "${fixture_dir}/common.sh"
sed -e "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    -e '\|^source /usr/libexec/justvoxel/mjust/managed-crossplay.sh$|d' \
    -e '\|^source /usr/libexec/justvoxel/mjust/update-policy.sh$|d' \
    -e "s|/usr/libexec/justvoxel/mjust/instance-context.sh|${fixture_dir}/instance-context.sh|g" \
    -e "s|/etc/justvoxel/instances|${fixture_dir}/instances|g" \
    "${repo_root}/mjust/libexec/admin-version-status-json" > "${fixture_dir}/status"
# Use the real instance loader and commitment validator in temporary paths.
# Match the ownership-only fixture shim used by test-instance-runtime-activation.
mkdir -p "${fixture_dir}/instances" "${fixture_dir}/registry" "${fixture_dir}/quadlets" "${fixture_dir}/bin"
chmod 0700 "${fixture_dir}/instances" "${fixture_dir}/registry"
real_stat="$(command -v stat)"
cat > "${fixture_dir}/bin/stat" <<STUB
#!/usr/bin/bash
if [[ \$2 == '%u:%g:%a' ]]; then
    mode="\$("${real_stat}" -c '%a' -- "\${!#}")" || exit 1
    printf '0:0:%s\n' "\${mode}"
else
    exec "${real_stat}" "\$@"
fi
STUB
sed -e "s|/etc/justvoxel/instances|${fixture_dir}/instances|g" \
    -e "s|/var/lib/justvoxel/instances|${fixture_dir}/registry|g" \
    -e "s|/etc/containers/systemd|${fixture_dir}/quadlets|g" \
    -e "s|/var/lib/justvoxel-instance-runtime|${fixture_dir}/transactions|g" \
    -e 's/\$EUID == 0 \&\& //' \
    "${repo_root}/mjust/libexec/validate-instance-runtime-commit" > "${fixture_dir}/guard"
sed -e "s|/etc/justvoxel/instances|${fixture_dir}/instances|g" \
    -e "s|/usr/libexec/justvoxel/mjust/validate-instance-runtime-commit|${fixture_dir}/guard|g" \
    "${repo_root}/mjust/libexec/instance-context.sh" > "${fixture_dir}/instance-context.sh"
chmod 0755 "${fixture_dir}/bin/stat" "${fixture_dir}/guard"
export PATH="${fixture_dir}/bin:${PATH}" RUNTIME_CALLS="${fixture_dir}/runtime-calls"
export DATA_PATH="${fixture_dir}/storage/minecraft-instances/jv-aaaaaa/data"
export MINECRAFT_VERSION_MODE=recommended MINECRAFT_VERSION=26.2 MINECRAFT_IMAGE_TAG=stable BEDROCK_ENABLED=yes
for id in jv-aaaaaa jv-bbbbbb; do
    dir="${fixture_dir}/instances/${id}"
    data="${fixture_dir}/storage/minecraft-instances/${id}/data"
    mkdir -p "${dir}" "${data}/logs"
    chmod 0700 "${dir}"
    printf 'Starting minecraft server version 26.2\n' > "${data}/logs/latest.log"
    printf 'fixture environment\n' > "${dir}/minecraft.env"
    printf '[Container]\nContainerName=minecraft-%s\n' "${id}" > "${fixture_dir}/quadlets/minecraft-${id}.container"
    env_hash="$(sha256sum "${dir}/minecraft.env")"
    quadlet_hash="$(sha256sum "${fixture_dir}/quadlets/minecraft-${id}.container")"
    jq -n --arg id "${id}" --arg root "${fixture_dir}/storage" --arg data "${data}" \
        --arg env "${env_hash%% *}" --arg quadlet "${quadlet_hash%% *}" \
        '{schema_version:1,runtime:{id:$id,software:"paper",image_tag:"stable",version:"26.2",version_policy:"recommended",uid:1000,gid:1000,bedrock_enabled:true},
          storage:{root:$root,data_path:$data},environment_sha256:$env,quadlet_sha256:$quadlet}' > "${dir}/config.json"
    jq -n --arg id "${id}" '{id:$id}' > "${fixture_dir}/registry/${id}.json"
    chmod 0600 "${dir}/config.json" "${dir}/minecraft.env" "${fixture_dir}/quadlets/minecraft-${id}.container" "${fixture_dir}/registry/${id}.json"
done
printf 'Starting minecraft server version 9.9\n' > "${fixture_dir}/storage/minecraft-instances/jv-bbbbbb/data/logs/latest.log"
# A readable legacy configuration must never select a runtime or affect preview.
export JV_CONFIG="${fixture_dir}/legacy.conf"
printf 'exit 92\n' > "${JV_CONFIG}"
reject_status() {
    : > "${RUNTIME_CALLS}"
    if bash "${fixture_dir}/status" "$@" > "${fixture_dir}/rejected" 2> "${fixture_dir}/error"; then
        echo 'Unsafe runtime status accepted' >&2; exit 1
    fi
    [[ ! -s ${RUNTIME_CALLS} && ! -s ${fixture_dir}/rejected ]]
}
for invalid in minecraft friendly-name '../jv-aaaaaa' jv-a jv-AAAAAA; do
    reject_status --instance "${invalid}" recommended
done
reject_status --instance
reject_status --instance jv-cccccc recommended
# A retained config with a retired registry identity also fails closed.
mv "${fixture_dir}/registry/jv-bbbbbb.json" "${fixture_dir}/retired.json"
reject_status --instance jv-bbbbbb recommended
mv "${fixture_dir}/retired.json" "${fixture_dir}/registry/jv-bbbbbb.json"
mkdir "${fixture_dir}/transactions"
printf '{}' > "${fixture_dir}/transactions/delete-jv-bbbbbb.json"
reject_status --instance jv-bbbbbb recommended
rm "${fixture_dir}/transactions/delete-jv-bbbbbb.json"
reject_status
reject_status recommended
reject_status pinned 26.2
reject_status --server-type purpur recommended
reject_status --image-tag latest recommended

: > "${RUNTIME_CALLS}"
selected="$(SERVICE_STATUS=0 DATA_PATH="${fixture_dir}/storage/minecraft-instances/jv-bbbbbb/data" MINECRAFT_SERVER_TYPE=vanilla MINECRAFT_IMAGE_TAG=wrong bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
jq -e '.installed == "26.2" and .configured_server_type == "paper" and .configured_image_tag == "stable"' <<< "${selected}" >/dev/null
grep -Fxq 'systemctl is-active --quiet minecraft-jv-aaaaaa.service' "${RUNTIME_CALLS}"
grep -Fxq "podman inspect minecraft-jv-aaaaaa --format {{.Image}}" "${RUNTIME_CALLS}"
grep -Fxq "podman image inspect docker.io/itzg/minecraft-server:stable --format {{.Id}}" "${RUNTIME_CALLS}"
! grep -Eq 'jv-bbbbbb|minecraft\.service|podman inspect minecraft ' "${RUNTIME_CALLS}"
[[ $(wc -l < "${RUNTIME_CALLS}") == 4 ]]
: > "${RUNTIME_CALLS}"
preview="$(bash "${fixture_dir}/status" recommended '' yes)"
jq -e '.installed == "" and .selected_candidate == "26.2" and .stack_state == "not_applicable"' <<< "${preview}" >/dev/null
[[ ! -s ${RUNTIME_CALLS} ]]

recommended="$(bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
jq -e '.installed == "26.2" and .available == "26.3" and .recommended == "26.2" and
    .selected_candidate == "26.2" and .configured_version == "26.2" and .policy == "recommended" and
    .update_available == false and .stack_state == "waiting_for_compatibility" and
    .minecraft_state == "up_to_date" and .paper_supported and .crossplay_compatible and
    .viaversion_state == "managed_automatically" and
    (.reason | contains("Minecraft 26.3 is available") and contains("released cross-play support") and contains("No action is needed"))' <<< "${recommended}" >/dev/null
# Released Java support advances dynamically, with no source version switch.
advanced="$(GEYSER_SUPPORT=26.3 bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
jq -e '.recommended == "26.3" and .selected_candidate == "26.3" and .stack_state == "updates_available" and .update_available' <<< "${advanced}" >/dev/null
# Component-only maintenance leaves the game version unchanged.
components="$(COMPONENT_STATE=updates_available bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
jq -e '.installed == .selected_candidate and .minecraft_state == "up_to_date" and .stack_state == "updates_available" and .update_available and .geyser_state == "updates_available" and (.reason | contains("No action") | not)' <<< "${components}" >/dev/null
current="$(PAPER_AVAILABLE=26.2 PAPER_STABLE=26.2 bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
jq -e '.stack_state == "up_to_date" and (.update_available | not)' <<< "${current}" >/dev/null
for setting in 'COMPONENT_STATE=unavailable' 'GEYSER_SUPPORT=unavailable' 'IMAGE_STATE=unavailable'; do
    unknown="$(env "${setting}" bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
    jq -e '.stack_state == "unavailable" and (.update_available | not) and (.reason | contains("No action") | not)' <<< "${unknown}" >/dev/null
done
latest="$(bash "${fixture_dir}/status" --instance jv-aaaaaa latest)"
jq -e '.policy == "latest" and .selected_candidate == "26.3" and .paper_supported and (.crossplay_compatible | not) and (.reason | contains("not compatible"))' <<< "${latest}" >/dev/null
pinned="$(bash "${fixture_dir}/status" --instance jv-aaaaaa pinned 26.2)"
jq -e '.policy == "pinned" and .selected_candidate == "26.2" and .paper_supported and .crossplay_compatible' <<< "${pinned}" >/dev/null
pinned_incompatible="$(bash "${fixture_dir}/status" --instance jv-aaaaaa pinned 26.3)"
jq -e '.policy == "pinned" and .selected_candidate == "26.3" and (.crossplay_compatible | not) and .stack_state == "unavailable"' <<< "${pinned_incompatible}" >/dev/null
rm "${DATA_PATH}/logs/latest.log"
unknown="$(bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
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
paper="$(PAPER_AVAILABLE=26.2 PAPER_STABLE=26.2 bash "${fixture_dir}/status" --instance jv-aaaaaa recommended)"
switch="$(bash "${fixture_dir}/status" --instance jv-aaaaaa --server-type purpur recommended)"
jq -e '.server_type == "purpur" and .server_software == "Purpur" and .stack_state == "updates_available" and .crossplay_compatible' <<< "${switch}" >/dev/null
[[ $(jq -r '.plan_fingerprint' <<< "${switch}") != "$(jq -r '.plan_fingerprint' <<< "${paper}")" ]]
preview="$(bash "${fixture_dir}/status" --server-type vanilla recommended '' no)"
jq -e '.server_type == "vanilla" and .server_supported and (.crossplay_enabled | not)' <<< "${preview}" >/dev/null
if bash "${fixture_dir}/status" --instance jv-aaaaaa --server-type vanilla recommended; then echo 'In-place Vanilla status accepted' >&2; exit 1; fi

# Combined previews bind both selections and image identity without writing state.
export JV_CONFIG="${fixture_dir}/config"
printf 'fixture administrator configuration\n' > "${JV_CONFIG}"
before="$(sha256sum "${JV_CONFIG}" "${DATA_PATH}/logs/latest.log")"
combined="$(bash "${fixture_dir}/status" --instance jv-aaaaaa --server-type purpur --image-tag latest recommended)"
jq -e '.configured_server_type == "paper" and .configured_image_tag == "stable" and
    .server_type == "purpur" and .image_tag == "latest" and .runtime_update_required and .update_available' <<< "${combined}" >/dev/null
channel="$(bash "${fixture_dir}/status" --instance jv-aaaaaa --image-tag latest recommended)"
jq -e '.update_available and (.runtime_update_required | not) and .image_state == "up_to_date"' <<< "${channel}" >/dev/null
custom="$(bash "${fixture_dir}/status" --instance jv-aaaaaa --image-tag release_1.2-3 recommended)"
for other in "${paper}" "${switch}" "${channel}" "${custom}"; do
    [[ $(jq -r '.plan_fingerprint' <<< "${combined}") != "$(jq -r '.plan_fingerprint' <<< "${other}")" ]]
done
repeat="$(bash "${fixture_dir}/status" --instance jv-aaaaaa --server-type purpur --image-tag latest recommended)"
[[ $(jq -r '.plan_fingerprint' <<< "${repeat}") == "$(jq -r '.plan_fingerprint' <<< "${combined}")" ]]
identity="$(LOCAL_ID=other-image bash "${fixture_dir}/status" --instance jv-aaaaaa --server-type purpur --image-tag latest recommended)"
[[ $(jq -r '.plan_fingerprint' <<< "${identity}") != "$(jq -r '.plan_fingerprint' <<< "${combined}")" ]]
changed_digest="$(LOCAL_DIGEST_NUMBER=2 bash "${fixture_dir}/status" --instance jv-aaaaaa --server-type purpur --image-tag latest recommended)"
[[ $(jq -r '.plan_fingerprint' <<< "${changed_digest}") != "$(jq -r '.plan_fingerprint' <<< "${combined}")" ]]
for invalid in '../bad' '-bad' 'tag:other' missing deprecated "$(printf 'a%.0s' {1..129})"; do
    if bash "${fixture_dir}/status" --instance jv-aaaaaa --image-tag "${invalid}" recommended; then echo 'Unsafe custom preview accepted' >&2; exit 1; fi
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
