#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "${fixture_dir}"' EXIT

cat > "${fixture_dir}/common.sh" <<'EOF'
require_root() { :; }
require_config() { :; }
resolve_latest_stable_paper_version() { printf '%s' "${STABLE_VERSION}"; }
resolve_latest_available_paper_version() { printf '%s' "${NEWEST_VERSION}"; }
resolve_geyser_supported_java_version() { printf '%s' "${STABLE_VERSION}"; }
paper_version_has_stable_build() { [[ $1 == "${STABLE_VERSION}" ]]; }
paper_version_build_channel() {
    case "$1" in
        "${STABLE_VERSION}") printf STABLE ;;
        "${NEWEST_VERSION}") printf ALPHA ;;
        *) return 1 ;;
    esac
}
bedrock_crossplay_supports_version() { [[ $1 == "$2" ]]; }
systemctl() { echo runtime-check >> "${DATA_PATH}.checks"; return 1; }
minecraft_image_ref() { echo runtime-check >> "${DATA_PATH}.checks"; printf 'docker.io/itzg/minecraft-server:stable'; }
jv_remote_platform_digest() { return 1; }
podman() { echo runtime-check >> "${DATA_PATH}.checks"; return 1; }
jv_crossplay_metadata() { echo runtime-check >> "${DATA_PATH}.checks"; return 1; }
jv_crossplay_artifact_state() { printf unavailable; }
EOF
sed -e "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    -e '\|^source /usr/libexec/justvoxel/mjust/managed-crossplay.sh$|d' \
    -e '\|^source /usr/libexec/justvoxel/mjust/update-policy.sh$|d' \
    "${repo_root}/mjust/libexec/admin-version-status-json" > "${fixture_dir}/status"
export STABLE_VERSION=26.2 NEWEST_VERSION=26.3 DATA_PATH="${fixture_dir}/data"
export MINECRAFT_VERSION_MODE=recommended MINECRAFT_VERSION="${STABLE_VERSION}" MINECRAFT_IMAGE_TAG=stable

recommended="$(bash "${fixture_dir}/status" recommended '' no)"
jq -e --arg stable "${STABLE_VERSION}" --arg newest "${NEWEST_VERSION}" \
    '.selected_candidate == $stable and .available == $newest and .available_channel == "ALPHA"' <<< "${recommended}" >/dev/null

bedrock="$(bash "${fixture_dir}/status" recommended '' yes)"
jq -e --arg stable "${STABLE_VERSION}" '.selected_candidate == $stable and .crossplay_compatible == true' <<< "${bedrock}" >/dev/null

latest="$(bash "${fixture_dir}/status" latest '' no)"
jq -e --arg newest "${NEWEST_VERSION}" '.selected_candidate == $newest and .candidate_channel == "ALPHA"' <<< "${latest}" >/dev/null

latest_bedrock="$(bash "${fixture_dir}/status" latest '' yes)"
jq -e --arg newest "${NEWEST_VERSION}" --arg stable "${STABLE_VERSION}" \
    '.selected_candidate == $newest and .geyser_supported_version == $stable and .crossplay_enabled == true and .crossplay_compatible == false' <<< "${latest_bedrock}" >/dev/null

specific_bedrock="$(bash "${fixture_dir}/status" pinned "${NEWEST_VERSION}" yes)"
jq -e --arg newest "${NEWEST_VERSION}" --arg stable "${STABLE_VERSION}" \
    '.selected_candidate == $newest and .geyser_supported_version == $stable and .crossplay_enabled == true and .crossplay_compatible == false' <<< "${specific_bedrock}" >/dev/null

stable="$(bash "${fixture_dir}/status" pinned "${STABLE_VERSION}" no)"
jq -e --arg stable "${STABLE_VERSION}" '.selected_candidate == $stable and .paper_supported == true' <<< "${stable}" >/dev/null

alpha="$(bash "${fixture_dir}/status" pinned "${NEWEST_VERSION}" no)"
jq -e --arg newest "${NEWEST_VERSION}" '.selected_candidate == $newest and .candidate_channel == "ALPHA" and .paper_supported == true' <<< "${alpha}" >/dev/null

missing="$(bash "${fixture_dir}/status" pinned 9.9 no)"
jq -e '.selected_candidate == "" and .paper_supported == false' <<< "${missing}" >/dev/null

# Setup compatibility never depends on a local appliance runtime or JARs.
[[ ! -e ${DATA_PATH} && ! -e ${DATA_PATH}.checks ]]
for preview in "${recommended}" "${bedrock}" "${latest}" "${latest_bedrock}" "${specific_bedrock}"; do
    jq -e '.stack_state == "not_applicable" and .image_state == "not_applicable" and .installed == "" and .plan_fingerprint == "" and (.update_available | not) and (.reason | contains("Server update information could not be verified") | not)' <<< "${preview}" >/dev/null
done
jq -e '.recommended == "26.2" and .crossplay_compatible and .paper_supported' <<< "${bedrock}" >/dev/null
jq -e '.reason | contains("selected Minecraft 26.3 is not compatible")' <<< "${specific_bedrock}" >/dev/null

# Exercise the shared Paper metadata resolver with stable, pre-release, and
# unusable build records. A build without a server download is not installable.
source "${repo_root}/mjust/libexec/common.sh"
curl() {
    case "${*: -1}" in
        */versions/26.2/builds) printf '[{"channel":"ALPHA","downloads":{"server:default":{"url":"https://example.invalid/alpha.jar"}}},{"channel":"STABLE","downloads":{"server:default":{"url":"https://example.invalid/stable.jar"}}},{"channel":"BETA","downloads":{"server:default":{"url":"https://example.invalid/beta.jar"}}}]' ;;
        */versions/26.3/builds) printf '[{"channel":"ALPHA","downloads":{}},{"channel":"ALPHA","downloads":{"server:default":{"url":"https://example.invalid/alpha.jar"}}},{"channel":"BETA","downloads":{"server:default":{"url":"https://example.invalid/beta.jar"}}}]' ;;
        */versions/26.3-rc-3/builds) printf '[{"channel":"ALPHA","downloads":{"server:default":{"url":"https://example.invalid/rc.jar"}}}]' ;;
        */versions/26.1/builds) printf '[{"channel":"RECOMMENDED","downloads":{"server:default":{"url":"https://example.invalid/recommended.jar"}}}]' ;;
        */versions/9.9/builds) printf '[{"channel":"ALPHA","downloads":{}}]' ;;
        */projects/paper) printf '{"versions":{"26":["26.3","26.3-rc-3","26.2"]}}' ;;
        *) return 1 ;;
    esac
}
[[ $(resolve_latest_stable_paper_version) == "${STABLE_VERSION}" ]]
[[ $(resolve_latest_available_paper_version) == "${NEWEST_VERSION}" ]]
[[ $(paper_version_build_channel "${NEWEST_VERSION}") == ALPHA ]]
[[ $(paper_version_build_channel "${STABLE_VERSION}") == STABLE ]]
if paper_version_has_stable_build 26.1; then exit 1; fi
if paper_version_build_channel 9.9 >/dev/null; then exit 1; fi

grep -Fq 'resolve_latest_available_paper_version' "${repo_root}/mjust/libexec/admin-setup-plan-json"
grep -Fq 'paper_version_build_channel "${version}"' "${repo_root}/mjust/libexec/admin-setup-plan-json"
grep -Fq 'AdminSetupVersionPreview' "${repo_root}/webui/internal/server/setup_wizard.go"
grep -Fq '/setup/version-preview?' "${repo_root}/webui/internal/server/static/settings.js"
grep -Fq 'status.selected_candidate' "${repo_root}/webui/internal/server/static/settings.js"
grep -Fq 'next.disabled = !status.selected_candidate || (status.crossplay_enabled && !status.crossplay_compatible)' "${repo_root}/webui/internal/server/static/settings.js"
grep -Fq '<dt>Minecraft version</dt><dd>{{.Plan.Normalized.Minecraft.Version}}</dd>' "${repo_root}/webui/internal/server/templates/setup_review.html"
grep -Fq '<dt>Version policy</dt><dd>{{.VersionPolicyLabel}}</dd>' "${repo_root}/webui/internal/server/templates/setup_review.html"
if grep -Fq 'Version: LATEST' "${repo_root}/webui/internal/server/templates/setup_review.html"; then exit 1; fi

# Exercise the planner separately from the browser preview. The selected policy
# and Bedrock choice must survive a compatible plan; mismatches must be errors.
cat > "${fixture_dir}/planner-common.sh" <<'EOF'
require_root() { :; }
resolve_minecraft_ids() { printf '1000 1000 fixture\n'; }
validate_nonroot_id() { [[ $1 -ge 1000 ]]; }
validate_simple_text() { [[ $1 != *$'\n'* ]]; }
validate_positive_int() { [[ $1 =~ ^[1-9][0-9]*$ ]]; }
validate_game_mode() { [[ $1 == survival ]]; }
validate_memory() { [[ $1 =~ ^[1-9][0-9]*G$ ]]; }
memory_to_mib() { printf '%s' "$((${1%G} * 1024))"; }
validate_port() { [[ $1 -ge 1 && $1 -le 65535 ]]; }
validate_minecraft_image_tag() { :; }
resolve_geyser_supported_java_version() { printf '26.2'; }
resolve_latest_stable_paper_version() { printf '26.2'; }
resolve_latest_available_paper_version() { printf '%s' "${PLANNER_LATEST_VERSION:-26.3}"; }
paper_version_has_stable_build() { [[ $1 == 26.2 ]]; }
paper_version_build_channel() { [[ $1 == 26.2 || $1 == 26.3 ]] && printf STABLE; }
bedrock_crossplay_supports_version() { [[ $1 == "$2" ]]; }
normalize_daily_backup_time() { printf '%s' "$1"; }
daily_backup_schedule_from_time() { printf '*-*-* %s:00' "$1"; }
EOF
touch "${fixture_dir}/planner-storage.sh"
sed -e "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/planner-common.sh|" \
    -e "s|^source /usr/libexec/justvoxel/mjust/storage-common-base.sh$|source ${fixture_dir}/planner-storage.sh|" \
    -e '/^request="$(cat)"$/i system_memory_mib() { printf 8192; }\nport_in_use() { return 1; }' \
    "${repo_root}/mjust/libexec/admin-setup-plan-json" > "${fixture_dir}/planner"
planner_request='{"server":{"motd":"Family","max_players":10,"bedrock_enabled":true,"timezone":"America/Toronto"},"minecraft":{"java_memory":"2G","container_memory":"3G","java_port":49165,"bedrock_port":49166,"image_tag":"stable","version_policy":"recommended","version":"","game_mode":"survival"},"storage":{"type":"system","path":"/var/lib/justvoxel/minecraft","device":"","mount_point":""},"backups":{"automatic":true,"daily_time":"04:30","keep":7,"type":"system","path":"/var/lib/justvoxel/backups","device":"","mount_point":"","source":"","username":"","domain":""}}'
recommended_plan="$(env JV_CONFIG="${fixture_dir}/absent-config" JV_QUADLET="${fixture_dir}/absent-quadlet" bash "${fixture_dir}/planner" <<< "${planner_request}")"
jq -e '.ok == true and .normalized.server.bedrock_enabled == true and .normalized.minecraft.version_policy == "recommended" and .normalized.minecraft.version == "26.2"' <<< "${recommended_plan}" >/dev/null
compatible_latest_request="$(jq -c '.minecraft.version_policy="latest"' <<< "${planner_request}")"
compatible_latest_plan="$(env JV_CONFIG="${fixture_dir}/absent-config" JV_QUADLET="${fixture_dir}/absent-quadlet" PLANNER_LATEST_VERSION=26.2 bash "${fixture_dir}/planner" <<< "${compatible_latest_request}")"
jq -e '.ok == true and .normalized.server.bedrock_enabled == true and .normalized.minecraft.version_policy == "latest" and .normalized.minecraft.version == "26.2" and ([.warnings[].code] | index("bedrock_moving_version") == null)' <<< "${compatible_latest_plan}" >/dev/null
for policy in latest pinned; do
    incompatible_request="$(jq -c --arg policy "${policy}" '.minecraft.version_policy=$policy | .minecraft.version="26.3"' <<< "${planner_request}")"
    incompatible_plan="$(env JV_CONFIG="${fixture_dir}/absent-config" JV_QUADLET="${fixture_dir}/absent-quadlet" bash "${fixture_dir}/planner" <<< "${incompatible_request}")"
    jq -e '.ok == false and .code == "bedrock_version_unsupported" and (.error | contains("26.3") and contains("26.2"))' <<< "${incompatible_plan}" >/dev/null
done
