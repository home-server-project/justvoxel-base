#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf -- "${fixture}"' EXIT
mkdir -p "${fixture}/helpers" "${fixture}/bin"
printf 'justvoxel-plus-base\n' > "${fixture}/variant"
export PLUS_CLI_CALLS="${fixture}/calls"
cat > "${fixture}/bin/sudo" <<'SH'
#!/usr/bin/bash
exec "$@"
SH
cat > "${fixture}/helpers/api-client" <<'SH'
#!/usr/bin/bash
printf '%s\n' "$*" >> "${PLUS_CLI_CALLS}"
case "$2" in
 /v1/status*) printf '{"system":{"health":"Healthy"},"plus":{"docker":"Running","configured":true},"extended":{"system":{"hostname":"plus"}},"minecraft":{"secret":"must-not-appear"},"backup":{"secret":"must-not-appear"}}' ;;
 /v1/plus/applications) printf '{"configured":true,"panel_url":"http://plus:8081","drydock_url":"http://plus:3000"}' ;;
 /v1/admin/plus/host-logs*) printf '{"output":"bounded host journal"}' ;;
 *) exit 99 ;;
esac
SH
for command in files network resources os-status os-update; do
 printf '#!/usr/bin/bash\nprintf "helper %s\\n" >> "${PLUS_CLI_CALLS}"\n' "${command}" > "${fixture}/helpers/${command}"
done
python3 - "${repo_root}" "${fixture}" <<'PY'
from pathlib import Path
import sys
repo,fixture=map(Path,sys.argv[1:])
for source,destination in [('mjust/bin/mjust',fixture/'mjust'),('mjust/libexec/plus',fixture/'helpers/plus'),('mjust/libexec/plus-host',fixture/'helpers/plus-host')]:
 text=(repo/source).read_text().replace('/usr/lib/justvoxel/variant',str(fixture/'variant')).replace('/usr/libexec/justvoxel/mjust/',str(fixture/'helpers')+'/')
 text=text.replace('source '+str(fixture/'helpers/ui.sh'), 'jui_is_interactive() { return 1; }')
 text=text.replace("(( EUID == 0 )) || { echo 'Run this operation through sudo.' >&2; exit 1; }", ': # fixture root boundary')
 destination.write_text(text);destination.chmod(0o755)
PY
chmod +x "${fixture}/helpers/"* "${fixture}/bin/"*
export PATH="${fixture}/bin:${PATH}"
"${fixture}/mjust" --list > "${fixture}/list"
grep -Fq 'JustVoxel Plus Base host commands' "${fixture}/list"
python3 - "${repo_root}/build_files/validate/common.sh" "${fixture}/list" <<'PY'
from pathlib import Path
import re,sys
validator,listing=map(Path,sys.argv[1:])
expected=re.findall(r"^grep -Fq '([^']+)' <<<\"\$\{mjust_list\}\"",validator.read_text(),re.MULTILINE)
assert expected, 'No image command-list validation contract found'
for command in expected:
    assert command in listing.read_text(), 'Plus CLI help misses image validation entry: '+command
PY
if grep -Eq 'mjust (players|backup|restore|configure|start|stop|update-minecraft)' "${fixture}/list"; then exit 1; fi
for command in players backup restore configure start stop update-minecraft storage-migrate start-over; do
 if "${fixture}/mjust" "${command}" > /dev/null 2>&1; then echo "Legacy command accepted: ${command}" >&2; exit 1; fi
done
"${fixture}/mjust" status --details > "${fixture}/status"
grep -Fq 'Docker: Running' "${fixture}/status"
! grep -Fq 'must-not-appear' "${fixture}/status"
"${fixture}/mjust" applications | grep -Fq 'http://plus:8081'
"${fixture}/mjust" logs docker | grep -Fq 'bounded host journal'
if "${fixture}/mjust" logs ../../other-unit; then exit 1; fi
if "${fixture}/mjust" factory-reset < /dev/null; then exit 1; fi
for command in net files resources os-status; do "${fixture}/mjust" "${command}"; done
! grep -Eq '/v1/(minecraft|players|backups)' "${PLUS_CLI_CALLS}"
printf 'Plus CLI host dispatch, status, logs and command boundaries passed.\n'
