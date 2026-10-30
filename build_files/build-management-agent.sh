#!/usr/bin/bash
# Run inside the Home Server Base 10 validation container, with /repo and /out mounted.
set -euo pipefail

dnf install -y gcc golang pam-devel libpwquality-devel diffutils authselect authselect-libs shadow-utils
mkdir -p /work /tmp/go-home /tmp/go-build-cache /tmp/go-path
cp -a /repo/management /repo/templates /repo/mjust /repo/system_files /repo/build_files /work/
cd /work/management
export HOME=/tmp/go-home GOCACHE=/tmp/go-build-cache GOPATH=/tmp/go-path GOENV=off

go mod tidy
dirty=0
diff -u /repo/management/go.mod /work/management/go.mod || dirty=1
diff -u /repo/management/go.sum /work/management/go.sum || dirty=1
if (( dirty != 0 )); then
    echo "ERROR: AlmaLinux Go requires module metadata changes shown above." >&2
    exit 1
fi

CGO_ENABLED=1 go vet -mod=readonly ./...
CGO_ENABLED=1 go test -mod=readonly ./...
# Adapt the runner working directory without changing the source script.
bash <(sed "s|^cd /work$|cd /work/management|" /run-pam-integration-alma.sh)
CGO_ENABLED=1 go build -mod=readonly -trimpath -o /out/management-agent ./cmd/justvoxel-management-agent
/out/management-agent version
