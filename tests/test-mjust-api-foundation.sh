#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
api_client="${repo_root}/mjust/libexec/api-client"
authorization="${repo_root}/management/cmd/justvoxel-management-agent/authorization.go"
identity="${repo_root}/management/cmd/justvoxel-management-agent/identity.go"
main="${repo_root}/management/cmd/justvoxel-management-agent/main.go"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

for file in "${api_client}" "${authorization}" "${identity}" "${main}"; do
    [[ -f ${file} ]] || fail "missing Phase 0B file: ${file}"
done

grep -Fq 'authSourceLocalRoot authSource = "local-root"' "${identity}" || fail 'local-root auth source is missing'
grep -Fq 'func localRootAdministrator' "${authorization}" || fail 'local-root principal helper is missing'
grep -Fq 'uid != 0' "${authorization}" || fail 'local-root principal is not restricted to uid 0'
grep -Fq 'if sess, ok := localRootAdministrator(r); ok {' "${main}" || fail 'root-peer authorization is not wired into the Agent'

grep -Fq 'EUID != 0' "${api_client}" || fail 'mJust API client does not require root'
grep -Fq -- '--unix-socket' "${api_client}" || fail 'mJust API client does not use the Unix socket'
grep -Fq 'JV_MANAGEMENT_SOCKET' "${api_client}" || fail 'mJust API client socket contract is missing'
grep -Fq -- '--data-binary @-' "${api_client}" || fail 'mJust API request bodies must come from stdin'
if grep -Fq 'Authorization:' "${api_client}"; then
    fail 'mJust API client must not introduce a persistent bearer token'
fi

echo 'mJust Management API foundation checks passed.'
