#!/usr/bin/bash
set -euo pipefail

review=webui/internal/server/templates/setup_review.html
progress=webui/internal/server/templates/setup_progress.html
style=webui/internal/server/static/setup-review.css
app=webui/internal/server/static/app.js
app_style=webui/internal/server/static/app.css
header=webui/internal/server/templates/header.html
import=webui/internal/server/templates/migration_workspace_import.html
login=webui/internal/server/templates/login.html
agent=management/cmd/justvoxel-management-agent/operation_diagnostics.go
worker=management/cmd/justvoxel-management-agent/migration_import_worker.go
wrapper=mjust/libexec/admin-migration-import-transaction-json

for path in "$review" "$progress" "$style" "$app" "$app_style" "$header" "$import" "$login" "$agent" "$worker" "$wrapper"; do
    [[ -f "$path" ]]
done

grep -Fq 'class="setup-recovery-action"' "$progress"
grep -Fq '.setup-recovery-action{background:#a96713' "$style"
grep -Fq '<script src="/static/app.js" defer></script>' "$login"
grep -Fq 'input.autocomplete === "new-password"' "$app"
for state in unacceptable acceptable strong; do grep -Fq "password-strength.is-$state" "$app_style"; done
! grep -Fq 'fetch(' <<< "$(sed -n '/const enhancePasswordFields/,/^};/p' "$app")"
grep -Fq 'class="setup-review-header"' "$review"
grep -Fq 'class="setup-review-summary' "$review"
for label in 'Maximum players' Timezone 'Bedrock cross-play' 'Minecraft version' 'Version policy' 'Minecraft game memory' 'Backup destination' 'Automatic backups' Retention; do
    grep -Fq "<dt>${label}</dt>" "$review"
done
grep -Fq 'Download configuration' "$review"
! grep -Eq '<details|overflow:auto|data-technical-toggle' "$review"
grep -Fq '.setup-review-summary{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))' "$style"
grep -Fq '@media(max-width:700px){.setup-review-summary{grid-template-columns:1fr}' "$style"
grep -Fq '@media(max-width:560px)' "$style"
grep -Eq 'remove[.]push\(.*JustVoxel-managed mounts.*fstab.*storage data is not erased' "$app"
grep -Eq 'keep[.]push\(.*Storage partitions, filesystems,.*unrelated administrator.*fstab' "$app"
grep -Fq 'class="panel migration-destination-helper"' "$import"
grep -Fq '<strong>Choose a source. JustVoxel discovers the server' "$import"
grep -Fq 'data-system-tab="logs"' "$header"
grep -Eq 'currentTab === "logs".*await renderLogs\(sequence\)' "$app"
grep -Fq 'const data = await response.text()' "$app"
grep -Eq 'documentText[.]textContent[[:space:]]*=[[:space:]]*data;' "$app"
grep -Fq 'encodeURIComponent(entry.category) + "/" + encodeURIComponent(entry.log_id)' "$app"
grep -Fq 'filepath.Join(s.logsDir, diagnosticCategoryForOperationType(operationType)+"-logs")' "$agent"
grep -Fq 'filepath.Join(s.logsDir, category+"-logs")' "$agent"
grep -Fq 'filepath.Join(s.logsDir, "support")' "$agent"
grep -Fq 'POST /v1/admin/logs/support' "$agent"
grep -Fq 'Generate support report' "$app"
grep -Fq 'generateSupport.hidden = selectedGroup !== "Support"' "$app"
grep -Fq 'requestedEntry = result.log' "$app"
grep -Fq 'logs.unshift(result.log)' "$app"
grep -Fq '["Setup", "Migration", "Restore", "Plugins", "Reset", "Support", "Failed units"]' "$app"
grep -Fq 'validOperationID(id)' "$agent"
grep -Fq 'O_NOFOLLOW' "$agent"
grep -Fq 'operationDiagnosticLimit' "$agent"
grep -Fq 'recordOutput(diagnostic)' "$worker"
grep -Fq 'migration-import-backend --instance "${JV_INSTANCE_ID}" "$source_path" >&2' "$wrapper"
grep -Fq 'ownership.Uid != uint32(os.Geteuid())' "$agent"
grep -Fq 'info.Mode().Perm() != 0o600' "$agent"
grep -Fq 'validatePrivateDiagnosticDirectory(dir)' "$agent"
