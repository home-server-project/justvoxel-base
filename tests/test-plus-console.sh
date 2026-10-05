#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf -- "${fixture}"' EXIT
mkdir -p "${fixture}/bin"
for command in nmcli tailscale netbird playit; do
 printf '#!/usr/bin/bash\nexit 1\n' > "${fixture}/bin/${command}"
done
cat > "${fixture}/bin/systemctl" <<'STUB'
#!/usr/bin/bash
case "$*" in
 'is-active --quiet docker.service') exit 0 ;;
 *) exit 1 ;;
esac
STUB
chmod +x "${fixture}/bin/"*
python3 - "${repo_root}" "${fixture}" <<'PY'
from pathlib import Path
import sys
repo, fixture = map(Path, sys.argv[1:])
s = (repo/'runtime/justvoxel-motd').read_text()
s = s.replace('variant_id="$(cat /usr/lib/justvoxel/variant 2>/dev/null || true)"', 'variant_id=justvoxel-plus-base')
s = s.replace('/etc/justvoxel/plus/deployed', str(fixture/'deployed'))
(fixture/'motd').write_text(s)
PY
export PATH="${fixture}/bin:${PATH}"
for mode in --issue shell; do
 args=()
 [[ ${mode} == --issue ]] && args+=(--issue)
 bash "${fixture}/motd" "${args[@]}" > "${fixture}/banner"
 grep -Fq 'JustVoxel Plus Base' "${fixture}/banner"
 grep -Eq 'Docker:.*Running' "${fixture}/banner"
 grep -Eq 'Infrastructure:.*Not configured' "${fixture}/banner"
 if grep -Eq 'Minecraft:|Minecraft Server Appliance|mjust setup|Server status:' "${fixture}/banner"; then
  echo 'FAIL: Plus console exposed legacy game setup/status' >&2
  exit 1
 fi
 touch "${fixture}/deployed"
 bash "${fixture}/motd" "${args[@]}" > "${fixture}/banner"
 grep -Eq 'Infrastructure:.*Configured' "${fixture}/banner"
 rm "${fixture}/deployed"
done
printf 'Plus console identity and setup checks passed.\n'
