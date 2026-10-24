#!/usr/bin/bash
# Source assertions only; this file does not build or install anything.
set -euo pipefail
python3 - <<'PY'
from pathlib import Path
build = Path('build_files/build-common.sh').read_text()
validation = Path('build_files/validate/common.sh').read_text()
assert 'dnf install -y https://repo.nordvpn.com/yum/nordvpn/centos/noarch/Packages/n/nordvpn-release-1.0.0-1.noarch.rpm' in build
assert 'dnf install -y --setopt=install_weak_deps=False nordvpn\n' in build
assert 'nordvpn-gui' not in build
assert "repo.set(section, 'enabled', '0')" in build
assert 'done < <(rpm -ql nordvpn-release)' in build
assert 'systemctl stop nordvpnd.socket nordvpnd.service' in build
assert 'systemctl --root=/ disable nordvpnd.socket nordvpnd.service' in build
for item in ('rpm -q nordvpn nordvpn-release', 'test -x /usr/bin/nordvpn',
             'test -x /usr/sbin/nordvpnd', 'nordvpnd.service nordvpnd.socket',
             'test -f "/usr/lib/systemd/system/${nord_unit}"',
             "repo.getboolean(section, 'enabled', fallback=True)"):
    assert item in validation, item
assert '''if rpm -q nordvpn-gui >/dev/null 2>&1; then
    echo "ERROR: nordvpn-gui must not be installed in the headless image." >&2
    exit 1
fi''' in validation
assert 'test -f /usr/lib/sysusers.d/justvoxel-nordvpn.conf' in validation
assert """test "$(grep -Ev '^[[:space:]]*(#|$)' /usr/lib/sysusers.d/justvoxel-nordvpn.conf)" = 'g nordvpn -'""" in validation
sysusers = Path('system_files/usr/lib/sysusers.d/justvoxel-nordvpn.conf')
assert sysusers.is_file()
declarations = [line for line in sysusers.read_text().splitlines()
                if line.strip() and not line.lstrip().startswith('#')]
assert declarations == ['g nordvpn -']
assert not any(line.split()[2].isdigit() for line in declarations), 'No fixed numeric GID'
assert 'nordvpn login' not in build and 'nordvpn set meshnet' not in build
assert 'NORDVPN_VERSION' not in build and 'nordvpn@sha256' not in build
assert 'nordvpn' not in Path('Containerfile').read_text().lower() # No Nord container stage.
PY
