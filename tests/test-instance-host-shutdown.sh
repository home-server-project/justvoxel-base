#!/usr/bin/bash
set -euo pipefail
# Only temporary fixtures and mocked commands; never touches the host runtime.
python3 - "$(cd -- "$(dirname -- "$0")/.." && pwd)" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

REPO = Path(sys.argv.pop())
IDS = ['jv-aaaaaa', 'jv-bbbbbb', 'jv-cccccc']

class Shutdown(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        for name in ('instances', 'registry', 'quadlets', 'bin', 'helpers', 'run'):
            (self.root / name).mkdir(mode=0o700)
        self.env = dict(os.environ, PATH=str(self.root / 'bin') + ':' + os.environ['PATH'],
                        FIXTURE=str(self.root), JV_UPDATE_REBOOT_WARNING_SECONDS='10', JV_UPDATE_REBOOT_DIGEST='sha256:'+'b'*64)
        self.data = dict(running=[], players={}, queue_fail=False, reboot_fail=False, backup_fail='')
        self.save()
        tool = '''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
r=Path(os.environ['FIXTURE']); tool=Path(sys.argv[0]).name; a=sys.argv[1:]
with (r/'calls').open('a') as f: f.write(json.dumps([tool]+a)+'\\n')
p=r/'state'; d=json.loads(p.read_text())
if tool=='systemctl':
    if a==['is-system-running']: print('stopping')
    elif a[0]=='show': print('active' if a[-1][10:-8] in d['running'] else 'inactive')
    elif a[0]=='is-active': sys.exit(3)
    elif a[:2]==['--no-block','stop']:
        identity=a[-1][10:-8]
        if d.get('stop_fail')==identity: sys.exit(1)
        d['running'].remove(identity); p.write_text(json.dumps(d))
    elif a[0]=='start':
        identity=a[-1][10:-8]
        if identity not in d['running']: d['running'].append(identity)
        p.write_text(json.dumps(d))
    elif a==['reboot']: sys.exit(1 if d['reboot_fail'] else 0)
    else: sys.exit(2)
elif tool=='podman':
    if a[0]=='ps':
        identity=a[2][16:-1]
        if identity in d['running']: print('minecraft-'+identity)
    elif a[0]=='inspect': print('true' if a[-1][10:] in d['running'] else 'false')
    elif a[0]=='exec':
        if a[3]=='list':
            value=d['players'].get(a[1][10:], 'There are 0 of a max of 20 players online:')
            if value is None: sys.exit(1)
            print(value)
    elif a[:3]!=['kill','--signal','SIGUSR1']: sys.exit(2)
elif tool=='systemd-run': sys.exit(1 if d['queue_fail'] else 0)
elif tool=='management-agent':
    if a[0]=='system-update-activate':
        assert a[1]=='sha256:'+'b'*64
        sys.exit(1 if d.get('activation_fail') else 0)
    assert a[0]=='instance-version-backup'
    assert not d['running'], 'backup must be cold across the appliance'
    sys.exit(1 if a[1]==d['backup_fail'] else 0)
else: sys.exit(2)
'''
        for name in ('systemctl', 'podman', 'systemd-run', 'management-agent'):
            self.write(self.root / 'bin' / name, tool, 0o700)
        common = 'require_root() { :; }; jv_variant_kind() { echo hws; }; jv_bootc_status_json() { :; }\n'
        for name in ('common.sh', 'os-common.sh'):
            self.write(self.root / 'helpers' / name, common, 0o600)
        self.write(self.root / 'helpers/system-update-api.sh', 'jv_system_update_get(){ printf \'%s\\n\' \'{"ok":true,"reboot_required":true,"staged":{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}\'; }\n', 0o600)
        for relative in ('mjust/libexec/instance-host-shutdown-prepare',
                         'mjust/libexec/host-shutdown-guard',
                         'mjust/libexec/admin-system-actions-json',
                         'mjust/libexec/admin-system-update-reboot-json',
                         'runtime/system-update-reboot-worker'):
            source = (REPO / relative).read_text()
            source = source.replace('/usr/libexec/justvoxel/mjust/', str(self.root / 'helpers') + '/')
            source = source.replace('/usr/libexec/justvoxel/management-agent', str(self.root / 'bin/management-agent'))
            source = source.replace('/run/justvoxel', str(self.root / 'run'))
            source = source.replace('/etc/justvoxel/instances', str(self.root / 'instances'))
            source = source.replace('/var/lib/justvoxel/instances', str(self.root / 'registry'))
            source = source.replace('/etc/containers/systemd', str(self.root / 'quadlets'))
            # Exercise real validation under unprivileged fixture ownership.
            source = source.replace('info.st_uid != 0 or info.st_gid != 0',
                                    'info.st_uid != os.getuid() or info.st_gid != os.getgid()')
            source = source.replace('time.sleep(', 'fixture_sleep(')
            source = source.replace("root = '", "def fixture_sleep(seconds):\n    with open(os.environ['FIXTURE'] + '/sleeps', 'a') as output:\n        output.write(str(seconds) + '\\n')\n\nroot = '", 1)
            source = source.replace('[[ -d /sys/firmware/efi ]]', '[[ -d ' + str(self.root) + ' ]]')
            source = source.replace('busctl call org.freedesktop.login1 /org/freedesktop/login1 org.freedesktop.login1.Manager CanRebootToFirmwareSetup', "printf 's \\\"yes\\\"'")
            self.write(self.root / 'helpers' / Path(relative).name, source, 0o700)

    def write(self, path, text, mode):
        path.write_text(text)
        path.chmod(mode)

    def save(self):
        (self.root / 'state').write_text(json.dumps(self.data))

    def instances(self, running, players=None):
        for identity in IDS:
            directory = self.root / 'instances' / identity
            directory.mkdir(mode=0o700)
            env, quadlet = 'PRIVATE=secret\n', '[Container]\n'
            record = dict(schema_version=1, runtime=dict(id=identity),
                          environment_sha256=hashlib.sha256(env.encode()).hexdigest(),
                          quadlet_sha256=hashlib.sha256(quadlet.encode()).hexdigest())
            for path, text in ((directory / 'config.json', json.dumps(record)),
                               (directory / 'minecraft.env', env),
                               (self.root / 'quadlets' / ('minecraft-' + identity + '.container'), quadlet),
                               (self.root / 'registry' / (identity + '.json'), json.dumps(dict(id=identity)))):
                self.write(path, text, 0o600)
        self.data.update(running=running, players=players or {})
        self.save()

    def run_helper(self, name, *args, rc=0, **env):
        result = subprocess.run(['bash', str(self.root / 'helpers' / name), *args],
                                env=dict(self.env, **env), capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, rc, result.stdout + result.stderr)
        self.assertNotIn('secret', result.stdout + result.stderr)
        for call in self.calls():
            self.assertNotIn('minecraft.service', call)
            self.assertNotIn('minecraft', call)
        return json.loads(result.stdout) if result.stdout.strip().startswith('{') else result.stdout

    def calls(self):
        path = self.root / 'calls'
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def changed(self, verb):
        return [c[-1] for c in self.calls() if c[0]=='systemctl' and verb in c]

    def assert_running(self, expected):
        self.assertEqual(set(json.loads((self.root / 'state').read_text())['running']), set(expected))

    def test_zero_instances_actions_and_guard(self):
        for action in ('reboot', 'poweroff', 'firmware-reboot'):
            self.assertTrue(self.run_helper('admin-system-actions-json', 'apply', action)['accepted'])
        self.run_helper('host-shutdown-guard')
        self.assertEqual(self.changed('stop'), [])

    def test_zero_instances_update_preflight_and_worker(self):
        self.run_helper('admin-system-update-reboot-json', 'request', '--backup-minecraft')
        self.run_helper('system-update-reboot-worker', JV_UPDATE_REBOOT_BACKUP='yes')
        self.assertEqual(self.changed('stop'), [])
        self.assertEqual(self.changed('start'), [])
        self.assertEqual([c for c in self.calls() if c[0]=='management-agent'], [
            ['management-agent', 'system-update-activate',
             'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'],
        ])
        self.assertEqual([c for c in self.calls() if c[:2]==['management-agent', 'instance-version-backup']], [])
        self.assertIn(['systemctl', 'reboot'], self.calls())

    def test_status_and_only_running_prepared(self):
        self.instances([IDS[0]])
        self.assertEqual(self.run_helper('admin-system-actions-json', 'status')['minecraft_state'], 'running')
        self.run_helper('admin-system-actions-json', 'apply', 'reboot')
        self.assertEqual(self.changed('stop'), ['minecraft-'+IDS[0]+'.service'])
        self.assertFalse(any(c[:3]==['podman', 'exec', 'minecraft-'+IDS[1]] for c in self.calls()))
        self.assert_running([])
        self.assertEqual(self.run_helper('admin-system-actions-json', 'status')['minecraft_state'], 'stopped')

    def test_all_running_prepared_and_queue_failure_restores_exact_set(self):
        self.instances(IDS[:2])
        self.data['queue_fail'] = True
        self.save()
        result = self.run_helper('admin-system-actions-json', 'apply', 'poweroff', rc=4)
        self.assertEqual(result['reason'], 'action_failed')
        units = ['minecraft-'+i+'.service' for i in IDS[:2]]
        self.assertEqual(self.changed('stop'), units)
        self.assertEqual(self.changed('start'), units)
        self.assert_running(IDS[:2])

    def test_players_on_either_instance_aggregate_before_stop(self):
        self.instances(IDS[:2], {IDS[0]: 'There are 1 of a max of 20 players online: Alex',
                                IDS[1]: 'There are 2 of a max of 20 players online: Sam, Steve'})
        for helper, args in (('admin-system-actions-json', ('apply', 'reboot')),
                             ('admin-system-update-reboot-json', ('request',))):
            result = self.run_helper(helper, *args, rc=10)
            self.assertTrue(result['confirmation_required'])
            self.assertEqual(result['online'], 3)
            self.assertEqual(result['players'], ['Alex', 'Sam', 'Steve'])
        self.assertEqual(self.changed('stop'), [])
        self.assertFalse(any(c[0]=='systemd-run' for c in self.calls()))
        self.run_helper('admin-system-actions-json', 'apply', 'reboot', '--confirm-players')
        self.assertEqual(len(self.changed('stop')), 2)
        sleeps = [int(value) for value in (self.root / 'sleeps').read_text().splitlines()]
        self.assertEqual(sum(sleeps), 60)
        self.assertEqual(sleeps, [30, 15, 5, 5, 1, 1, 1, 1, 1])

    def test_second_server_players_confirm_update(self):
        self.instances(IDS[:2], {IDS[1]: 'There are 1 of a max of 20 players online: Steve'})
        result = self.run_helper('admin-system-actions-json', 'apply', 'reboot', rc=10)
        self.assertEqual(result['players'], ['Steve'])
        self.run_helper('admin-system-update-reboot-json', 'request', rc=10)
        self.run_helper('admin-system-update-reboot-json', 'request', '--confirm-players', '--backup-minecraft', '--warning-seconds=10')
        self.assertEqual(self.changed('stop'), [])
        self.assertTrue(any(c[0]=='systemd-run' for c in self.calls()))

    def test_player_query_failure_never_stops_or_queues(self):
        self.instances(IDS[:2], {IDS[1]: None})
        for helper, args in (('admin-system-actions-json', ('apply', 'reboot', '--confirm-players')),
                             ('admin-system-update-reboot-json', ('request', '--confirm-players'))):
            self.run_helper(helper, *args, rc=3)
        self.assertEqual(self.changed('stop'), [])
        self.assertFalse(any(c[0]=='systemd-run' for c in self.calls()))

    def test_untrusted_discovery_fails_closed(self):
        self.instances([IDS[0]])
        (self.root / 'registry' / (IDS[0]+'.json')).unlink()
        self.run_helper('admin-system-actions-json', 'status', rc=3)
        self.run_helper('admin-system-actions-json', 'apply', 'reboot', rc=3)
        self.assertEqual(self.changed('stop'), [])

    def test_missing_runtime_directory_cannot_mean_zero_instances(self):
        self.write(self.root / 'registry' / (IDS[0]+'.json'), json.dumps(dict(id=IDS[0])), 0o600)
        self.run_helper('admin-system-actions-json', 'apply', 'reboot', rc=3)
        self.assertEqual(self.changed('stop'), [])

    def test_malformed_player_response_never_stops(self):
        self.instances(IDS[:2], {IDS[1]: 'There are 0 of a max of 20 players online: Steve'})
        self.run_helper('admin-system-actions-json', 'apply', 'reboot', rc=3)
        self.run_helper('admin-system-update-reboot-json', 'request', rc=3)
        self.assertEqual(self.changed('stop'), [])

    def test_worker_backs_up_every_instance_cold(self):
        self.instances(IDS[:2])
        self.run_helper('system-update-reboot-worker', JV_UPDATE_REBOOT_BACKUP='yes')
        self.assertEqual(self.changed('stop'), ['minecraft-'+i+'.service' for i in IDS[:2]])
        self.assertEqual([c[2] for c in self.calls() if c[:2]==['management-agent','instance-version-backup']], IDS)
        self.assertEqual(self.changed('start'), [])
        self.assert_running([])

    def test_backup_failure_cancels_reboot_restores_only_original_running(self):
        self.instances([IDS[0]])
        self.data['backup_fail'] = IDS[1]
        self.save()
        self.run_helper('system-update-reboot-worker', rc=5, JV_UPDATE_REBOOT_BACKUP='yes')
        self.assertEqual(self.changed('start'), ['minecraft-'+IDS[0]+'.service'])
        self.assertNotIn(['systemctl', 'reboot'], self.calls())
        self.assertEqual(json.loads((self.root / 'run/system-update-reboot.json').read_text())['state'], 'backup_failed')
        self.assert_running([IDS[0]])

    def test_reboot_failure_restores_worker_current_running_set(self):
        self.instances(IDS[:2])
        self.data['reboot_fail'] = True
        self.save()
        self.run_helper('system-update-reboot-worker', rc=6, JV_UPDATE_REBOOT_WAS_ACTIVE='no')
        self.assertEqual(self.changed('start'), ['minecraft-'+i+'.service' for i in IDS[:2]])
        self.assert_running(IDS[:2])

    def test_worker_quick_countdown_preserves_stopped_sibling(self):
        self.instances([IDS[0]], {IDS[0]: 'There are 1 of a max of 20 players online: Alex'})
        self.run_helper('system-update-reboot-worker')
        sleeps = [int(value) for value in (self.root / 'sleeps').read_text().splitlines()]
        self.assertEqual(sum(sleeps), 10)
        self.assertIn(['podman', 'kill', '--signal', 'SIGUSR1', 'minecraft-'+IDS[0]], self.calls())
        self.assertEqual(self.changed('stop'), ['minecraft-'+IDS[0]+'.service'])
        self.assert_running([])

    def test_partial_stop_failure_restores_running_set(self):
        self.instances(IDS[:2])
        self.data['stop_fail'] = IDS[1]
        self.save()
        self.run_helper('system-update-reboot-worker', rc=4)
        self.assert_running(IDS[:2])
        self.assertNotIn(['systemctl', 'reboot'], self.calls())

    def test_activation_failure_preserves_running_servers_and_prevents_reboot(self):
        self.instances(IDS[:2])
        self.data['activation_fail'] = True
        self.save()
        self.run_helper('system-update-reboot-worker', rc=3)
        self.assertEqual(self.changed('stop'), [])
        self.assertEqual(self.changed('start'), [])
        self.assertNotIn(['systemctl', 'reboot'], self.calls())
        self.assert_running(IDS[:2])
        self.assertEqual(json.loads((self.root / 'run/system-update-reboot.json').read_text())['reason'], 'activation_failed')

    def test_activation_precedes_graceful_stop_backup_and_reboot(self):
        self.instances(IDS[:2])
        self.run_helper('system-update-reboot-worker', JV_UPDATE_REBOOT_BACKUP='yes')
        calls = self.calls()
        activate = next(i for i,c in enumerate(calls) if c[:2]==['management-agent','system-update-activate'])
        stop = next(i for i,c in enumerate(calls) if c[:3]==['systemctl','--no-block','stop'])
        backup = next(i for i,c in enumerate(calls) if c[:2]==['management-agent','instance-version-backup'])
        self.assertLess(activate, stop)
        self.assertLess(stop, backup)
        self.assertLess(backup, calls.index(['systemctl','reboot']))

    def test_player_confirmation_does_not_activate_or_queue(self):
        self.instances(IDS[:2], {IDS[1]: 'There are 1 of a max of 20 players online: Steve'})
        self.run_helper('admin-system-update-reboot-json', 'request', rc=10)
        self.assertFalse(any(c[0] in ('management-agent','systemd-run') for c in self.calls()))
        self.assert_running(IDS[:2])

    def test_reviewed_stage_mismatch_does_not_queue(self):
        self.instances(IDS[:2])
        result=self.run_helper('admin-system-update-reboot-json','request','--staged-digest=sha256:'+'c'*64,rc=3)
        self.assertEqual(result['reason'],'stage_changed')
        self.assertFalse(any(c[0]=='systemd-run' for c in self.calls()))
        self.assertEqual(self.changed('stop'),[])

    def test_guard_only_instance_helper_and_preserves_delay(self):
        self.instances(IDS[:2], {IDS[1]: None})
        self.run_helper('host-shutdown-guard')
        self.assertIn(['podman', 'kill', '--signal', 'SIGUSR1', 'minecraft-'+IDS[0]], self.calls())
        self.assertNotIn(['podman', 'kill', '--signal', 'SIGUSR1', 'minecraft-'+IDS[1]], self.calls())
        self.assertEqual(self.changed('stop'), [])
        guard = (REPO / 'mjust/libexec/host-shutdown-guard').read_text()
        self.assertNotIn('interrupt-safety.sh', guard)
        self.assertNotIn('jv_prepare_minecraft_for_host_shutdown', guard)

unittest.main()
PY
