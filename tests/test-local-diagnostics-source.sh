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
grep -Fq 'class="setup-review-summary"' "$review"
for label in 'Maximum players' Timezone 'Bedrock cross-play' 'Minecraft version' 'Version policy' 'Minecraft game memory' 'Backup destination' 'Automatic backups' Retention; do
    grep -Fq "<dt>${label}</dt>" "$review"
done
grep -Fq 'Download configuration' "$review"
! grep -Eq '<details|overflow:auto|data-technical-toggle' "$review"
grep -Fq '.setup-review-summary{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))' "$style"
grep -Fq '@media(max-width:700px){.setup-review-summary{grid-template-columns:1fr}' "$style"
grep -Fq '@media(max-width:560px)' "$style"
grep -Fq 'JustVoxel-managed mounts and /etc/fstab entries are reset' "$app"
grep -Fq 'unrelated administrator /etc/fstab entries' "$app"
grep -Fq 'class="panel migration-destination-helper"' "$import"
grep -Fq '<strong>Choose a source. JustVoxel discovers the server' "$import"
grep -Fq 'data-system-tab="logs"' "$header"
grep -Fq 'currentTab === "logs"' "$app"
grep -Fq 'documentText.textContent = data' "$app"
grep -Fq 'filepath.Join(s.logsDir, diagnosticCategoryForOperationType(operationType)+"-logs")' "$agent"
grep -Fq 'filepath.Join(s.logsDir, category+"-logs")' "$agent"
grep -Fq 'validOperationID(id)' "$agent"
grep -Fq 'O_NOFOLLOW' "$agent"
grep -Fq 'operationDiagnosticLimit' "$agent"
grep -Fq 'recordOutput(diagnostic)' "$worker"
grep -Fq 'migration-import-backend "$source_path" >&2' "$wrapper"
