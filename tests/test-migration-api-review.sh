#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf -- "${tmp}"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

fake_client="${tmp}/api-client"
cat > "${fake_client}" <<'EOF'
#!/usr/bin/bash
case "${FAKE_API_MODE:-}" in
    source_selection_required|multiple_roots|source_version_required)
        printf '{"ok":false,"code":"%s","error":"Review input required."}' "${FAKE_API_MODE}"
        echo 'curl: (22) The requested URL returned error: 400' >&2
        exit 22
        ;;
    unexpected)
        printf '%s' '{"ok":false,"code":"invalid_source","error":"Bad source."}'
        echo 'curl: (22) The requested URL returned error: 400' >&2
        exit 22
        ;;
    transport)
        echo 'curl: (7) Failed to connect' >&2
        exit 7
        ;;
    success)
        printf '%s' '{"ok":true}'
        exit 0
        ;;
    *)
        exit 99
        ;;
esac
EOF
chmod 0755 "${fake_client}"

JV_MIGRATION_API_CLIENT="${fake_client}"
# shellcheck disable=SC1090
source "${repo_root}/mjust/libexec/migration-api.sh"

run_review() {
    local mode="$1" out_file="$2" err_file="$3" rc_file="$4" rc response
    set +e
    response="$(FAKE_API_MODE="${mode}" jv_migration_post_review /v1/admin/migration/import/plan '{}' 2>"${err_file}")"
    rc=$?
    set -e
    printf '%s' "${response}" > "${out_file}"
    printf '%s' "${rc}" > "${rc_file}"
}

for code in source_selection_required multiple_roots source_version_required; do
    run_review "${code}" "${tmp}/expected.out" "${tmp}/expected.err" "${tmp}/expected.rc"
    [[ $(cat "${tmp}/expected.rc") == 22 ]] || fail "${code} review response lost curl status"
    jq -e --arg code "${code}" '.code==$code' "${tmp}/expected.out" >/dev/null || fail "${code} review response body was lost"
    [[ ! -s "${tmp}/expected.err" ]] || fail "${code} interactive 400 leaked curl stderr"
done

run_review unexpected "${tmp}/unexpected.out" "${tmp}/unexpected.err" "${tmp}/unexpected.rc"
[[ $(cat "${tmp}/unexpected.rc") == 22 ]] || fail 'unexpected HTTP failure status changed'
grep -Fq 'curl: (22)' "${tmp}/unexpected.err" || fail 'real HTTP failure stderr was hidden'
jq -e '.code=="invalid_source"' "${tmp}/unexpected.out" >/dev/null || fail 'unexpected HTTP failure body was lost'

run_review transport "${tmp}/transport.out" "${tmp}/transport.err" "${tmp}/transport.rc"
[[ $(cat "${tmp}/transport.rc") == 7 ]] || fail 'transport failure status changed'
grep -Fq 'curl: (7)' "${tmp}/transport.err" || fail 'transport failure stderr was hidden'
[[ ! -s "${tmp}/transport.out" ]] || fail 'transport failure produced unexpected response body'

run_review success "${tmp}/success.out" "${tmp}/success.err" "${tmp}/success.rc"
[[ $(cat "${tmp}/success.rc") == 0 ]] || fail 'successful review changed status'
jq -e '.ok==true' "${tmp}/success.out" >/dev/null || fail 'successful review body was lost'
[[ ! -s "${tmp}/success.err" ]] || fail 'successful review produced stderr'

echo 'migration API review HTTP handling tests passed.'
