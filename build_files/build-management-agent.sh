#!/usr/bin/bash
# Run inside the Home Server Base 10 validation container, with /repo and /out mounted.
set -euo pipefail

# Local reusable builders already contain these dependencies; CI installs them by default.
if [[ ${JUSTVOXEL_MANAGEMENT_SKIP_DNF:-0} != 1 ]]; then
    dnf install -y gcc golang pam-devel libpwquality-devel diffutils authselect authselect-libs shadow-utils
fi
mkdir -p /work /tmp/go-home /tmp/go-build-cache /tmp/go-path
cp -a /repo/management /repo/templates /repo/mjust /repo/system_files /repo/build_files /repo/runtime /work/
cd /work/management
export HOME=/tmp/go-home GOCACHE=/tmp/go-build-cache GOPATH=/tmp/go-path GOENV=off

toolchain_file="/repo/.go-version"
[[ -f ${toolchain_file} && -r ${toolchain_file} ]] || {
    echo 'ERROR: missing readable .go-version file.' >&2
    exit 1
}
repo_go="$(tr -d '[:space:]' < "${toolchain_file}")"
[[ ${repo_go} =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
    echo 'ERROR: .go-version must contain an exact stable Go version (major.minor.patch).' >&2
    exit 1
}
export GOTOOLCHAIN="go${repo_go}"
go_version="$(go env GOVERSION)" || {
    echo "ERROR: cannot use Go toolchain ${GOTOOLCHAIN}; a modern bootstrap Go command and access to its toolchain cache/download are required." >&2
    exit 1
}
[[ ${go_version} == "${GOTOOLCHAIN}" ]] || {
    echo "ERROR: Go selected ${go_version}; expected ${GOTOOLCHAIN}." >&2
    exit 1
}
printf 'Go toolchain: %s (.go-version)\n' "${go_version}"

go mod tidy
dirty=0
diff -u /repo/management/go.mod /work/management/go.mod || dirty=1
diff -u /repo/management/go.sum /work/management/go.sum || dirty=1
if (( dirty != 0 )); then
    echo "ERROR: Repository Go toolchain requires module metadata changes shown above." >&2
    exit 1
fi

CGO_ENABLED=1 go vet -mod=readonly ./...
CGO_ENABLED=1 go test -mod=readonly ./...
# Adapt the runner working directory without changing the source script.
bash <(sed "s|^cd /work$|cd /work/management|" /run-pam-integration-alma.sh)
CGO_ENABLED=1 go build -mod=readonly -trimpath -o /out/management-agent ./cmd/justvoxel-management-agent
/out/management-agent version
