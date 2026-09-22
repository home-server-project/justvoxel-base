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

echo "Checking web-status-json syntax"
bash -n "${repo_root}/mjust/libexec/web-status-json"
if grep -Fq "rcon-cli 'version'" "${repo_root}/mjust/libexec/web-status-json"; then
    echo 'ERROR: dashboard status helper still overwrites Minecraft version from raw RCON output.' >&2
    exit 1
fi

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
    '.version == $version and .source_commit == $commit and .management_api == "v1" and (.build_date | type == "string" and length > 0) and (.go_version | type == "string" and length > 0) and (has("artifact_sha256") | not) and (has("release_tag") | not)' \
    "${artifact_root}/webui/webui-release.json" >/dev/null

for workflow in \
    "${repo_root}/.github/workflows/build-testing.yml" \
    "${repo_root}/.github/workflows/build.yml"; do
    ! grep -Fq 'resolve-webui-release.sh' "${workflow}"
    ! grep -Fq 'home-server-project/justvoxel-webui' "${workflow}"
    ! grep -Fq 'Fetch and verify exact WebUI release' "${workflow}"
done

echo 'JustVoxel WebUI source and local artifact checks passed.'
