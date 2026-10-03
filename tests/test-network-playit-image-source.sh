#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

python3 - <<'PY'
from pathlib import Path
container = Path('Containerfile').read_text()
build = Path('build_files/build-common.sh').read_text()
validation = Path('build_files/validate/common.sh').read_text()
menu = Path('mjust/libexec/network').read_text()
assert 'ARG PLAYIT_PACKAGE_IMAGE=ghcr.io/home-server-project/playit:stable' in container
assert 'FROM ${PLAYIT_PACKAGE_IMAGE} AS playit-package' in container
assert 'COPY --from=playit-package /rpms /playit-rpms' in container
assert "-name 'playit-*.x86_64.rpm'" in build
assert 'dnf install -y "${playit_rpm}"' in build
assert 'systemctl enable playit.service' in build
assert 'systemctl start playit' not in build and 'enable --now playit' not in build
assert 'playit setup' not in build
for item in ('rpm -q playit', 'test -x /usr/bin/playit\n', 'test -x /usr/bin/playitd',
             'test -f /usr/lib/systemd/system/playit.service',
             'test "$(systemctl is-enabled playit.service)" = "enabled"',
             'test ! -e /etc/playit/playit.toml'):
    assert item in validation, item
assert 'systemctl start playit' not in validation
assert "'Friendly network manager (nm-hsp)' 'Classic NetworkManager interface (nmtui)' 'Playit.gg public game access' 'Back'" in menu
assert 'sudo /usr/bin/playit setup' in menu
assert 'sudo /usr/bin/playit status' in menu
assert 'sudo systemctl enable --now playit.service' in menu
assert 'sudo systemctl disable --now playit.service' in menu
assert 'https://playit.gg/account/' in menu
assert 'playit.toml' not in build
assert 'curl' not in menu and 'wget' not in menu
# A versioned consumer or upstream download must never supplement the HSP tag.
import re
assert not re.search(r'PLAYIT_VERSION|playit:[0-9]|playit-[0-9]+\.[0-9]+', container + build)
assert 'github.com/playit' not in container + build
print('Network Playit appliance source checks passed.')
PY
