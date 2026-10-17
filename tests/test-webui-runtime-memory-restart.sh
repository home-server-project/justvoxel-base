#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/configuration-api.sh"
selected="${repo_root}/webui/internal/server/selected_server.go"
grep -Fq '.id == $id and .state == "stopped"' "${helper}"
grep -Fq 'state.State != "stopped"' "${selected}"
grep -Fq 'SetMinecraftInstanceConfiguration' "${selected}"
if grep -Eq 'systemctl|podman|confirm_players|confirmation_required' "${helper}"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
echo 'Selected-server stopped configuration policy checks passed.'
