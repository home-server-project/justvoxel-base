#!/usr/bin/bash
set -euo pipefail

header=webui/internal/server/templates/header.html
script=webui/internal/server/static/app.js
styles=webui/internal/server/static/app.css
server=webui/internal/server/system_workspace.go
client=webui/internal/api/reset.go
operations=webui/internal/api/persistent_operations.go

for path in "${header}" "${script}" "${styles}" "${server}" "${client}" "${operations}"; do
    [[ -f "${path}" ]] || {
        echo "ERROR: missing System reset workspace source: ${path}" >&2
        exit 1
    }
done

reset_line="$(grep -n 'data-system-tab="reset"' "${header}" | head -n1 | cut -d: -f1)"
about_line="$(grep -n 'data-system-tab="about"' "${header}" | head -n1 | cut -d: -f1)"
[[ -n "${reset_line}" && -n "${about_line}" && ${reset_line} -lt ${about_line} ]] || {
    echo 'ERROR: Factory Reset tab must appear before About.' >&2
    exit 1
}

grep -Fq 'const administratorTabs = new Set(["health", "users", "security", "reset"]);' "${script}"
grep -Fq 'currentTab === "reset"' "${script}"
grep -Fq '/api/system/workspace/reset/minecraft/plan' "${script}"
grep -Fq '/api/system/workspace/reset/factory/plan' "${script}"
grep -Fq '/api/system/workspace/reset/minecraft/apply' "${script}"
grep -Fq '/api/system/workspace/reset/factory/apply' "${script}"
grep -Fq '/api/system/workspace/reset/factory/resolve' "${script}"
grep -Fq 'Keep current server' "${script}"
grep -Fq '/api/system/workspace/reset/factory/current' "${script}"
grep -Fq '/api/system/workspace/reset/minecraft/current' "${script}"
grep -Fq '/api/system/workspace/reset/operations/' "${script}"
grep -Fq 'slider.type = "range"' "${script}"
grep -Fq 'slider.max = "100"' "${script}"
grep -Fq 'finalConfirm.disabled = true' "${script}"
grep -Fq 'Current voxel system password' "${script}"
grep -Fq 'External, USB, NFS, and SMB storage are never erased' "${script}"
grep -Fq 'backup_action === "preserve"' "${script}"
grep -Fq 'plan_fingerprint: plan.plan_fingerprint' "${script}"

grep -Fq 'POST /api/system/workspace/reset/minecraft/plan' "${server}"
grep -Fq 'POST /api/system/workspace/reset/factory/plan' "${server}"
grep -Fq 'POST /api/system/workspace/reset/factory/resolve' "${server}"
grep -Fq 'GET /api/system/workspace/reset/operations/{id}' "${server}"
grep -Fq 'type systemWorkspaceResetAPI interface' "${server}"
grep -Fq 'SystemPassword:  password' "${server}"

grep -Fq 'adminFactoryResetPlanPath' "${client}"
grep -Fq 'adminFactoryResetResolvePath' "${client}"
grep -Fq 'adminMinecraftResetApplyPath' "${client}"
grep -Fq 'AdminCurrentFactoryResetOperation' "${operations}"
grep -Fq 'AdminCurrentMinecraftResetOperation' "${operations}"

grep -Fq '.system-reset-choices' "${styles}"
grep -Fq '.system-reset-impact-grid' "${styles}"
grep -Fq '.system-reset-arm.is-armed' "${styles}"

echo 'System Factory Reset workspace source contract OK'
