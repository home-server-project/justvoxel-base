#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf -- "${fixture}"' EXIT
export PLUS_TEST_CALLS="${fixture}/calls"
mkdir -p "${fixture}/bin"
cat > "${fixture}/bin/systemctl" <<'STUB'
#!/usr/bin/bash
printf '%s\n' "$*" >> "${PLUS_TEST_CALLS}"
# An inherited Minecraft unit can exist: Plus must never query or stop it.
case "$1" in
 is-active|is-enabled) exit 0 ;;
 is-system-running) printf 'running\n' ;;
 *) exit 0 ;;
esac
STUB
cat > "${fixture}/bin/systemd-run" <<'STUB'
#!/usr/bin/bash
printf 'queue %s\n' "$*" >> "${PLUS_TEST_CALLS}"
STUB
chmod +x "${fixture}/bin/"*
export PATH="${fixture}/bin:${PATH}"
python3 - "${repo_root}" "${fixture}" <<'PY'
from pathlib import Path
import sys
repo, fixture = map(Path,sys.argv[1:])
for name in ('admin-system-actions-json','validate-backend'):
 text=(repo/'mjust/libexec'/name).read_text()
 text=text.replace('source /usr/libexec/justvoxel/mjust/common.sh', f'source "{repo}/mjust/libexec/common.sh"\njv_variant_raw() {{ printf "justvoxel-plus-base\\n"; }}\nrequire_root() {{ :; }}')
 text=text.replace('source /usr/libexec/justvoxel/mjust/os-common.sh','jv_bootc_status_json() { printf "{}"; }\njv_bootc_validate_status_json() { return 1; }')
 text=text.replace('source /usr/libexec/justvoxel/mjust/interrupt-safety.sh','jv_stop_minecraft_adaptive() { echo "unexpected game shutdown" >&2; exit 99; }')
 (fixture/name).write_text(text)
PY
bash "${fixture}/admin-system-actions-json" status | jq -e '.ok == true and .minecraft_state == "stopped" and .variant == "vm"' >/dev/null
for action in reboot poweroff; do
 bash "${fixture}/admin-system-actions-json" apply "${action}" | jq -e '.ok == true and .accepted == true' >/dev/null
done
bash "${fixture}/validate-backend" > "${fixture}/health"
grep -Fq 'OK: docker.service is running' "${fixture}/health"
if rg -q 'minecraft|rcon|backup' "${PLUS_TEST_CALLS}"; then
 echo 'FAIL: Plus host controls reached the game layer' >&2
 exit 1
fi
printf 'Plus host power and health isolation tests passed.\n'
