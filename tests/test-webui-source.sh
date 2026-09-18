#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_commit="${1:-${GITHUB_SHA:-}}"

if [[ ! ${source_commit} =~ ^[0-9a-f]{40}$ ]]; then
    source_commit="$(git -C "${repo_root}" rev-parse HEAD 2>/dev/null || true)"
fi
[[ ${source_commit} =~ ^[0-9a-f]{40}$ ]] || {
    echo 'ERROR: could not determine a valid Base source commit for WebUI validation.' >&2
    exit 1
}

version="$(tr -d '[:space:]' < "${repo_root}/webui/VERSION")"
[[ ${version} =~ ^[0-9]+\.[0-9]+\.[0-9]+(-(beta\.[0-9]+|rc\.[0-9]+))?$ ]] || {
    echo "ERROR: invalid WebUI VERSION: ${version}" >&2
    exit 1
}

echo "Checking JustVoxel WebUI ${version}"

cd "${repo_root}/webui"
unformatted="$(gofmt -l .)"
if [[ -n ${unformatted} ]]; then
    echo 'ERROR: WebUI Go source is not gofmt-clean:' >&2
    printf '%s\n' "${unformatted}" >&2
    exit 1
fi

go vet ./...
go test ./...

artifact_root="$(mktemp -d)"
trap 'rm -rf -- "${artifact_root}"' EXIT
"${repo_root}/build_files/build-webui.sh" "${source_commit}" "${artifact_root}/webui"

jq -e \
    --arg version "${version}" \
    --arg commit "${source_commit}" \
    '.version == $version and .source_commit == $commit and .management_api == "v1" and (.artifact_sha256 | test("^[0-9a-f]{64}$")) and (.build_date | type == "string") and (.go_version | type == "string")' \
    "${artifact_root}/webui/webui-release.json" >/dev/null

echo 'JustVoxel WebUI source and local artifact checks passed.'
