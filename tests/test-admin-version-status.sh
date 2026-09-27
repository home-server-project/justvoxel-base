#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "${fixture_dir}"' EXIT

cat > "${fixture_dir}/common.sh" <<'EOF'
require_root() { :; }
require_config() { :; }
resolve_latest_stable_paper_version() { printf '26.3'; }
resolve_latest_available_paper_version() { printf '26.3'; }
resolve_geyser_supported_java_version() { printf '26.2'; }
paper_version_has_stable_build() { [[ $1 == 26.2 || $1 == 26.3 ]]; }
paper_version_build_channel() { [[ $1 == 26.2 || $1 == 26.3 ]] && printf STABLE; }
bedrock_crossplay_supports_version() { [[ -n $1 && $1 == "$2" ]]; }
EOF
sed "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    "${repo_root}/mjust/libexec/admin-version-status-json" > "${fixture_dir}/status"
export DATA_PATH="${fixture_dir}/data" MINECRAFT_VERSION_MODE=recommended MINECRAFT_VERSION=26.2 MINECRAFT_IMAGE_TAG=stable BEDROCK_ENABLED=yes
mkdir -p "${DATA_PATH}/logs"
printf 'Starting minecraft server version 26.2\n' > "${DATA_PATH}/logs/latest.log"

recommended="$(bash "${fixture_dir}/status" recommended)"
jq -e '.installed == "26.2" and .available == "26.3" and .recommended == "26.2" and
    .selected_candidate == "26.2" and .configured_version == "26.2" and .policy == "recommended" and
    .update_available == true and .paper_supported == true and .crossplay_compatible == true and
    (.reason | contains("Minecraft 26.3 is available") and contains("recommends 26.2") and contains("turn off cross-play"))' \
    <<< "${recommended}" >/dev/null

latest="$(bash "${fixture_dir}/status" latest)"
jq -e '.available == "26.3" and .recommended == "26.2" and .selected_candidate == "26.3" and
    .paper_supported == true and .crossplay_compatible == false and
    (.reason | contains("Geyser/Floodgate currently supports 26.2") and contains("turn off cross-play"))' \
    <<< "${latest}" >/dev/null

pinned="$(bash "${fixture_dir}/status" pinned 26.2)"
jq -e '.policy == "pinned" and .selected_candidate == "26.2" and .available == "26.3" and
    .paper_supported == true and .crossplay_compatible == true' <<< "${pinned}" >/dev/null

pinned_incompatible="$(bash "${fixture_dir}/status" pinned 26.3)"
jq -e '.policy == "pinned" and .selected_candidate == "26.3" and .paper_supported == true and
    .crossplay_compatible == false and (.reason | contains("turn off cross-play before selecting 26.3"))' \
    <<< "${pinned_incompatible}" >/dev/null

rm "${DATA_PATH}/logs/latest.log"
unknown="$(bash "${fixture_dir}/status" recommended)"
jq -e '.installed == "" and .configured_version == "26.2" and .update_available == false' <<< "${unknown}" >/dev/null

export BEDROCK_ENABLED=no
without_crossplay="$(bash "${fixture_dir}/status" recommended)"
jq -e '.available == "26.3" and .recommended == "26.3" and .selected_candidate == "26.3"' \
    <<< "${without_crossplay}" >/dev/null
