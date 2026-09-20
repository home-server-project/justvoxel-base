#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
agent="${repo_root}/management/cmd/justvoxel-management-agent/admin_setup_plan.go"
agent_test="${repo_root}/management/cmd/justvoxel-management-agent/admin_setup_plan_test.go"
helper="${repo_root}/mjust/libexec/admin-setup-plan-json"
justfile="${repo_root}/mjust/justfile"

for file in "${agent}" "${agent_test}" "${helper}" "${justfile}"; do
    [[ -f ${file} ]] || { echo "missing A4.4 integration file: ${file}" >&2; exit 1; }
done

# The browser-to-agent boundary is deliberately one planning endpoint backed by
# one fixed helper. Setup execution belongs to A5 and must not appear in A4.4.
grep -Fq 'const adminSetupPlanHelper = "/usr/libexec/justvoxel/mjust/admin-setup-plan-json"' "${agent}"
grep -Fq 'mux.HandleFunc("POST /v1/admin/setup/plan", s.adminSetupPlan)' "${agent}"
if grep -Fq '/v1/admin/setup/apply' "${agent}"; then
    echo 'A4.4 must not register a setup Apply endpoint.' >&2
    exit 1
fi
grep -Fq '/v1/admin/setup/apply' "${agent_test}"
grep -Fq 'http.StatusNotFound' "${agent_test}"

# Planning request structs and the helper schema must remain secret-free. The
# response may legitimately expose the boolean execution requirement named
# smb_password_required, so inspect only the request-schema section here.
request_schema="$(sed -n '/^type adminSetupPlanServerRequest struct {/,/^type adminSetupPlanWarning struct {/p' "${agent}")"
if grep -Eq 'json:"[^" ]*password[^" ]*"' <<< "${request_schema}"; then
    echo 'A4.4 Agent planning request schema unexpectedly accepts a password field.' >&2
    exit 1
fi
grep -Fq 'contains("password")' "${helper}"
grep -Fq '"username","domain"' "${helper}"

# The Agent, not the browser or helper, binds the normalized execution-relevant
# plan to a deterministic SHA-256 identity.
grep -Fq 'PlanFingerprint string' "${agent}"
grep -Fq 'adminSetupPlanFingerprint' "${agent}"
grep -Fq 'sha256.Sum256(payload)' "${agent}"
grep -Fq 'out.PlanFingerprint = fingerprint' "${agent}"
grep -Fq 'out.SchemaVersion != "v1"' "${agent}"

# Authorization must happen before request decoding/helper execution.
auth_line="$(grep -n 's.requireAdministrator(w, r)' "${agent}" | head -n1 | cut -d: -f1)"
decode_line="$(grep -n 'decodeAdminSetupPlanRequest' "${agent}" | grep -v '^.*func ' | head -n1 | cut -d: -f1)"
[[ -n ${auth_line} && -n ${decode_line} && ${auth_line} -lt ${decode_line} ]] || {
    echo 'Administrator authorization must precede setup request processing.' >&2
    exit 1
}

# Direct terminal administration remains a supported fallback independent of
# the WebUI first-run flow.
grep -A1 '^setup:$' "${justfile}" | grep -Fq '/usr/libexec/justvoxel/mjust/setup'
if grep -Fq 'setup-advanced:' "${justfile}" || grep -Fq 'setup --advanced' "${justfile}"; then
    echo 'Legacy Advanced Setup must not be exposed as an mJust recipe.' >&2
    exit 1
fi

echo 'WebUI A4.4 first-run planning integration checks passed.'
