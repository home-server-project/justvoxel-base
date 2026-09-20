#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/admin-discovery-json"

[[ -f ${helper} ]] || { echo "missing configuration helper: ${helper}" >&2; exit 1; }
bash -n "${helper}"

grep -Fq 'source /usr/libexec/justvoxel/mjust/interrupt-safety.sh' "${helper}"
grep -Fq 'remaining_mib < 1024' "${helper}"
grep -Fq 'memory_restart_required=true' "${helper}"
grep -Fq "jv_player_check_before_interrupt 'restart Minecraft for the new memory settings'" "${helper}"
grep -Fq 'confirmation_required:true' "${helper}"
grep -Fq 'No settings were changed and Minecraft was not restarted.' "${helper}"
grep -Fq 'systemctl restart minecraft.service' "${helper}"
grep -Fq 'Previous settings were restored.' "${helper}"
grep -Fq "Minecraft is stopped, so the new limits will be used on its next start." "${helper}"

check_line="$(grep -n "jv_player_check_before_interrupt 'restart Minecraft for the new memory settings'" "${helper}" | head -n1 | cut -d: -f1)"
write_line="$(grep -n 'if ! write_main_config || ! render_runtime' "${helper}" | head -n1 | cut -d: -f1)"
restart_line="$(grep -n 'if ! systemctl restart minecraft.service' "${helper}" | head -n1 | cut -d: -f1)"

[[ -n ${check_line} && -n ${write_line} && -n ${restart_line} ]] || {
    echo 'memory restart safety sequence is incomplete' >&2
    exit 1
}
(( check_line < write_line && write_line < restart_line )) || {
    echo 'player safety check must happen before configuration mutation and restart' >&2
    exit 1
}

echo 'JustVoxel runtime memory restart policy checks passed.'
