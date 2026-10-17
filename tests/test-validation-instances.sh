#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Source and mocked execution regression: never call appliance runtime commands.
python3 - "${repo_root}" <<'PY'
from pathlib import Path
import subprocess
import sys

root = Path(sys.argv[1])
source = (root / 'mjust/libexec/validate-backend').read_text()
management = (root / 'management/cmd/justvoxel-management-agent/admin_validation.go').read_text()
frontend = (root / 'mjust/libexec/validate').read_text()
legacy = source.split('validate_legacy_first_run_runtime() {', 1)[1].split('\n}', 1)[0]
host = source.split('validate_host() {', 1)[1].split('\n}', 1)[0]
for forbidden in ('require_config', 'JV_MC_ENV', 'JV_QUADLET', 'DATA_PATH', 'BACKUP_PATH',
                  'minecraft.service', 'podman', 'rcon-cli', 'firewall-cmd',
                  'verify-minecraft-stack', 'storage-summary', 'minecraft-backup'):
    assert forbidden not in host, forbidden
for retained in ('check_file "${JV_MC_ENV}" 600', 'systemctl cat minecraft.service',
                 'podman exec minecraft rcon-cli', 'verify-minecraft-stack',
                 'minecraft-backup.timer', 'storage-summary'):
    assert retained in legacy, retained
assert 'validate_failed_units "${validation_mode}"' in host
assert 'validation_mode=first-run' in source
assert 'first-run validation requires an active setup transaction' in source
assert 'if [[ ${validation_mode} == first-run ]]; then\n    validate_legacy_first_run_runtime\nfi' in source
assert '--first-run' not in management
assert 'minecraft.service' not in management
assert 'GET /v1/admin/validation' in frontend
for forbidden in ('systemctl', 'podman', 'rcon-cli', 'firewall-cmd', 'mountpoint', '--instance'):
    assert forbidden not in frontend, forbidden

# Execute actual default entrypoint with no configuration or server files.
# Any attempt to load configuration or inspect a fixed runtime fails this test.
prelude = r'''
require_root() { :; }
require_config() { echo 'FORBIDDEN global config' >&2; exit 99; }
systemctl() {
    [[ $* == '--failed --no-legend --plain' ]] || { echo 'FORBIDDEN systemctl target' >&2; return 99; }
    return 0
}
swapon() { return 0; }
podman() { echo 'FORBIDDEN fixed container' >&2; exit 99; }
firewall-cmd() { echo 'FORBIDDEN global firewall' >&2; exit 99; }
'''
script = source.replace('source /usr/libexec/justvoxel/mjust/common.sh', prelude)
result = subprocess.run(['bash', '-c', script], capture_output=True, text=True)
assert result.returncode == 0, (result.returncode, result.stdout, result.stderr)
assert 'JustVoxel validation passed' in result.stdout
assert 'Failed system services: none' in result.stdout
assert 'FORBIDDEN' not in result.stdout + result.stderr
# Failed-unit enumeration failure must fail the host, without fixed-runtime work.
script = script.replace('    return 0\n}\nswapon', '    return 1\n}\nswapon')
result = subprocess.run(['bash', '-c', script], capture_output=True, text=True)
assert result.returncode == 1
assert 'could not enumerate failed systemd units' in result.stderr
assert 'FORBIDDEN' not in result.stdout + result.stderr
print('Appliance validation source/default-mode regression checks passed.')
PY
# Historical classifier/recovery behavior remains in its dedicated existing test:
# tests/test-setup-first-run-validation.sh
