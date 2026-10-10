#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

python3 - <<'PY'
from pathlib import Path
import os
import re
import subprocess
import tempfile
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
assert 'Set up Playit account/agent' not in menu
assert 'Playit CLI status' not in menu
assert "'Playit status'" in menu
assert 'sudo test -s /etc/playit/playit.toml' in menu
assert 'playit.toml' not in build
assert 'curl' not in menu and 'wget' not in menu
assert not re.search(r'https?://[^\s\'\"]+', menu.replace('https://playit.gg/account/', ''))
# A versioned consumer or upstream download must never supplement the HSP tag.
assert not re.search(r'PLAYIT_VERSION|playit:[0-9]|playit-[0-9]+\.[0-9]+', container + build)
assert 'github.com/playit' not in container + build

# Run only the extracted submenu with mocked appliance commands and selectors.
function = menu[menu.index('playit_menu() {'):menu.index('\n}\n', menu.index('playit_menu() {')) + 3]
harness = r'''
set -euo pipefail
sudo() {
    if [[ $1 == test ]]; then
        [[ $* == 'test -s /etc/playit/playit.toml' ]] || exit 99
        test -s "$FIXTURE/config"
        return
    fi
    printf 'sudo %s\n' "$*"
    case "$*" in
        'systemctl enable --now playit.service') return "$ACTIVATION_RC" ;;
        'systemctl disable --now playit.service'|'/usr/bin/playit setup'|'/usr/bin/playit status') return "$ACTION_RC" ;;
        '/usr/libexec/justvoxel/remote-access-status playit --details') return "$ACTION_RC" ;;
        *) exit 99 ;;
    esac
}
systemctl() {
    if [[ $* == 'is-active --quiet playit.service' ]]; then
        [[ $ACTIVE == yes ]]
    elif [[ $* == '--no-pager --lines=0 status playit.service' ]]; then
        printf 'systemctl %s\n' "$*"
        return "$ACTION_RC"
    else
        exit 99
    fi
}
jui_choose() {
    printf '%s\n' "$@" >&2
    if [[ -e $FIXTURE/selected ]]; then
        printf 'Back\n'
    else
        : > "$FIXTURE/selected"
        printf '%s\n' "$CHOICE"
    fi
}
jui_pause() { echo PAUSE; }
'''

def exercise(config, active, choice='Back', activation_rc=0, action_rc=0):
    with tempfile.TemporaryDirectory() as fixture:
        if config is not None:
            Path(fixture, 'config').write_text(config)
        env = dict(os.environ, FIXTURE=fixture, ACTIVE=active, CHOICE=choice,
                   ACTIVATION_RC=str(activation_rc), ACTION_RC=str(action_rc))
        result = subprocess.run(['bash', '-c', harness + function + '\nplayit_menu'],
                                env=env, text=True, capture_output=True, check=True)
        return result.stdout.splitlines(), result.stderr.splitlines()

for config, active, state, options in (
    (None, 'no', 'Not configured', ['Activate Playit']),
    ('', 'no', 'Not configured', ['Activate Playit']),
    (None, 'yes', 'Not configured', ['Activate Playit', 'Deactivate Playit']),
    ('configured', 'yes', 'Running', ['Deactivate Playit']),
    ('configured', 'no', 'Stopped', ['Activate Playit']),
):
    output, selector = exercise(config, active)
    assert not output, output  # No automatic detailed status dump.
    assert selector == [f'Playit.gg public game access · {state}',
                        *options, 'Playit status', 'Back'], selector

activate = 'sudo systemctl enable --now playit.service'
setup = 'sudo /usr/bin/playit setup'
account = 'Further tunnel and account management: https://playit.gg/account/'
for config in (None, ''):
    for active in ('no', 'yes'):
        for action_rc in (0, 1):
            output, _ = exercise(config, active, 'Activate Playit', action_rc=action_rc)
            assert output == [activate, setup, account, 'PAUSE'], output
output, _ = exercise('configured', 'no', 'Activate Playit')
assert output == [activate, 'PAUSE'], output
output, _ = exercise(None, 'no', 'Activate Playit', activation_rc=1)
assert output == [activate, 'PAUSE'], output
for action_rc in (0, 1):
    output, _ = exercise('configured', 'yes', 'Deactivate Playit', action_rc=action_rc)
    assert output == ['sudo systemctl disable --now playit.service', 'PAUSE'], output
    output, _ = exercise(None, 'no', 'Playit status', action_rc=action_rc)
    assert output == ['systemctl --no-pager --lines=0 status playit.service',
                      'sudo /usr/bin/playit status',
                      'sudo /usr/libexec/justvoxel/remote-access-status playit --details',
                      'PAUSE'], output
print('Network Playit appliance source checks passed.')
PY
