#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
webui_root="${repo_root}/webui"
source_commit="${1:-}"
output_dir="${2:-${repo_root}/build_artifacts/webui}"

if [[ ! ${source_commit} =~ ^[0-9a-f]{40}$ ]]; then
    echo 'ERROR: build-webui.sh requires a 40-character lowercase Git source commit.' >&2
    exit 2
fi

version_file="${webui_root}/VERSION"
[[ -r ${version_file} ]] || { echo 'ERROR: webui/VERSION is missing.' >&2; exit 1; }
version="$(tr -d '[:space:]' < "${version_file}")"
[[ ${version} =~ ^[0-9]+\.[0-9]+\.[0-9]+(-(beta\.[0-9]+|rc\.[0-9]+))?$ ]] || {
    echo "ERROR: invalid WebUI version: ${version}" >&2
    exit 1
}

build_date="${SOURCE_DATE_EPOCH:-}"
if [[ -n ${build_date} ]]; then
    [[ ${build_date} =~ ^[0-9]+$ ]] || { echo 'ERROR: SOURCE_DATE_EPOCH must be numeric.' >&2; exit 1; }
    build_date="$(date -u -d "@${build_date}" +%Y-%m-%dT%H:%M:%SZ)"
else
    build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi

go_version="$(go version | awk '{print $3}')"
[[ -n ${go_version} ]] || { echo 'ERROR: Go version could not be determined.' >&2; exit 1; }

rm -rf -- "${output_dir}"
install -d -m0755 "${output_dir}"

(
    cd "${webui_root}"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
        -ldflags "-s -w -X github.com/home-server-project/justvoxel-webui/internal/version.Version=${version} -X github.com/home-server-project/justvoxel-webui/internal/version.Commit=${source_commit} -X github.com/home-server-project/justvoxel-webui/internal/version.ManagementAPI=v1 -X github.com/home-server-project/justvoxel-webui/internal/version.BuildDate=${build_date}" \
        -o "${output_dir}/justvoxel-webui" ./cmd/justvoxel-webui
)
chmod 0755 "${output_dir}/justvoxel-webui"

artifact_sha256="$(sha256sum "${output_dir}/justvoxel-webui" | awk '{print $1}')"
printf '{"version":"%s","source_commit":"%s","management_api":"v1","build_date":"%s","go_version":"%s","artifact_sha256":"%s"}\n' \
    "${version}" "${source_commit}" "${build_date}" "${go_version}" "${artifact_sha256}" \
    > "${output_dir}/webui-release.json"
chmod 0644 "${output_dir}/webui-release.json"

jq -e \
    --arg version "${version}" \
    --arg commit "${source_commit}" \
    --arg sha "${artifact_sha256}" \
    '.version == $version and .source_commit == $commit and .management_api == "v1" and .artifact_sha256 == $sha and (.build_date | type == "string") and (.go_version | type == "string")' \
    "${output_dir}/webui-release.json" >/dev/null

version_output="$("${output_dir}/justvoxel-webui" -version)"
grep -Fqx "JustVoxel WebUI ${version}" <<<"${version_output}"
grep -Fqx "source=${source_commit}" <<<"${version_output}"
grep -Fqx 'management-api=v1' <<<"${version_output}"
[[ "$(sha256sum "${output_dir}/justvoxel-webui" | awk '{print $1}')" == "${artifact_sha256}" ]]

printf 'Built JustVoxel WebUI %s from %s\n' "${version}" "${source_commit}"
