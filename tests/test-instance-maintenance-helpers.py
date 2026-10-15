#!/usr/bin/env python3
"""Isolated helper fixtures; no actual Podman, systemd, network or firewalld."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import hashlib

REPO = Path(__file__).resolve().parents[1]
LIB = REPO / 'mjust/libexec'


class InstanceMaintenanceHelpers(unittest.TestCase):
    def setUp(self):
        if os.geteuid() != 0:
            self.skipTest('Production helper ownership checks require root fixture ownership')
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.instances = self.root / 'instances'
        self.registry = self.root / 'registry'
        self.quadlets = self.root / 'quadlets'
        self.bin = self.root / 'bin'
        self.helpers = self.root / 'helpers'
        for directory in (self.instances, self.registry, self.quadlets, self.bin, self.helpers):
            directory.mkdir(mode=0o700)
        self.calls = self.root / 'calls'
        self.players = self.root / 'players.json'
        self.running = self.root / 'running.json'
        self.players.write_text('{}')
        self.running.write_text('{}')
        self.environment = dict(os.environ, PATH=str(self.bin) + ':' + os.environ['PATH'],
                                FIXTURE_CALLS=str(self.calls), FIXTURE_PLAYERS=str(self.players),
                                FIXTURE_RUNNING=str(self.running))
        self.tool('podman', '''#!/usr/bin/env python3
import json, os, sys
with open(os.environ['FIXTURE_CALLS'], 'a') as f: f.write(' '.join(sys.argv[1:])+'\\n')
a=sys.argv[1:]
if a[0]=='inspect':
    print('true' if json.load(open(os.environ['FIXTURE_RUNNING'])).get(a[-1], False) else 'false')
elif a[0]=='exec':
    value=json.load(open(os.environ['FIXTURE_PLAYERS'])).get(a[1])
    if value is None: raise SystemExit(1)
    print(value)
elif a[0]=='kill':
    if a[1:3] != ['--signal', 'SIGUSR1']: raise SystemExit(2)
else: raise SystemExit(2)
''')

    def tool(self, name, text):
        path = self.bin / name
        path.write_text(text)
        path.chmod(0o700)
        return path

    def private(self, path, data):
        path.write_text(data)
        path.chmod(0o600)

    def instance(self, instance, enabled=True, software='paper'):
        directory = self.instances / instance
        directory.mkdir(mode=0o700)
        data = self.root / 'storage/minecraft-instances' / instance / 'data'
        data.mkdir(parents=True)
        env = 'RCON_PASSWORD=never-print-this-secret\n'
        quadlet = '[Container]\nContainerName=minecraft-' + instance + '\n'
        record = dict(schema_version=1,
                      runtime=dict(id=instance, uid=os.getuid() or 1000, gid=os.getgid() or 1000,
                                   bedrock_enabled=enabled, software=software),
                      storage=dict(data_path=str(data)),
                      environment_sha256=hashlib.sha256(env.encode()).hexdigest(),
                      quadlet_sha256=hashlib.sha256(quadlet.encode()).hexdigest())
        self.private(directory / 'config.json', json.dumps(record))
        self.private(directory / 'minecraft.env', env)
        self.private(self.quadlets / ('minecraft-' + instance + '.container'), quadlet)
        self.private(self.registry / (instance + '.json'), json.dumps(dict(id=instance)))
        return data

    def shutdown(self):
        source = (LIB / 'instance-host-shutdown-prepare').read_text()
        source = source.replace('/etc/justvoxel/instances', str(self.instances))
        source = source.replace('/var/lib/justvoxel/instances', str(self.registry))
        source = source.replace('/etc/containers/systemd', str(self.quadlets))
        result = subprocess.run(['bash', '-c', source], env=self.environment,
                                capture_output=True, text=True, timeout=12)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('never-print-this-secret', result.stdout + result.stderr)
        return self.calls.read_text() if self.calls.exists() else ''

    def test_shutdown_zero_online_unknown_and_stopped(self):
        for instance in ('jv-aaaaaa', 'jv-bbbbbb', 'jv-cccccc', 'jv-dddddd'):
            self.instance(instance)
        self.running.write_text(json.dumps({'minecraft-jv-aaaaaa': True, 'minecraft-jv-bbbbbb': True,
                                            'minecraft-jv-cccccc': True}))
        self.players.write_text(json.dumps({'minecraft-jv-aaaaaa': 'There are 0 of a max of 20 players online:',
                                            'minecraft-jv-bbbbbb': 'There are 1 of a max of 20 players online: Alex'}))
        calls = self.shutdown()
        self.assertIn('kill --signal SIGUSR1 minecraft-jv-aaaaaa', calls)
        self.assertNotIn('kill --signal SIGUSR1 minecraft-jv-bbbbbb', calls)
        self.assertNotIn('kill --signal SIGUSR1 minecraft-jv-cccccc', calls)
        self.assertNotIn('exec minecraft-jv-dddddd', calls)
        self.assertNotIn('start', calls)
        self.assertNotIn('exec minecraft rcon', calls)

    def test_shutdown_malformed_and_uncommitted_never_bypass(self):
        self.instance('jv-aaaaaa')
        self.running.write_text('{"minecraft-jv-aaaaaa":true}')
        for response in ('garbage', 'There are 0 of a max of 20 players online: Alex',
                         'There are 0 of a max of 0 players online:',
                         'There are 0 of a max of 20 players online:\nsecret'):
            self.players.write_text(json.dumps({'minecraft-jv-aaaaaa': response}))
            self.assertNotIn('kill', self.shutdown())
        self.private(self.instances / 'jv-aaaaaa/minecraft.env', 'unexpected contents')
        self.players.write_text('{"minecraft-jv-aaaaaa":"There are 0 of a max of 20 players online:"}')
        self.assertNotIn('kill', self.shutdown())

    def crossplay(self, instance):
        source = (LIB / 'instance-crossplay-start').read_text()
        source = source.replace('/etc/justvoxel/instances', str(self.instances))
        source = source.replace('/usr/libexec/justvoxel/mjust/', str(self.helpers) + '/')
        source = source.replace('/run/justvoxel-minecraft-maintenance.lock', str(self.root / 'maintenance.lock'))
        return subprocess.run(['bash', '-c', source, 'fixture', instance], env=self.environment,
                              capture_output=True, text=True, timeout=12)

    def prepare_crossplay_helpers(self):
        for helper in ('managed-plugin-files', 'managed-crossplay.sh'):
            (self.helpers / helper).write_text((LIB / helper).read_text())
            (self.helpers / helper).chmod(0o700)
        (self.helpers / 'validate-instance-runtime-commit').write_text('#!/usr/bin/bash\nexit 0\n')
        (self.helpers / 'validate-instance-runtime-commit').chmod(0o700)
        # Deterministic private-record discovery, without requiring host jq.
        self.tool('jq', '''#!/usr/bin/env python3
import json, sys
r=json.load(open(sys.argv[-1])); c=r['runtime']
for v in (r['storage']['data_path'],c['uid'],c['gid'],str(c['bedrock_enabled']).lower(),c['software']): print(v)
''')

    def test_crossplay_isolated_existing_plugins_and_disable(self):
        self.prepare_crossplay_helpers()
        paths = [self.instance('jv-aaaaaa'), self.instance('jv-bbbbbb')]
        for path in paths:
            plugins = path / 'plugins'
            plugins.mkdir()
            for name in ('Geyser-Spigot.jar', 'floodgate-spigot.jar', 'custom.jar'):
                (plugins / name).write_bytes(b'existing working binary')
        # Any network call would fail. Existing working jars must still start.
        self.tool('curl', '#!/usr/bin/bash\nexit 1\n')
        result = self.crossplay('jv-aaaaaa')
        self.assertEqual(result.returncode, 0, result.stderr)
        config = (paths[0] / 'plugins/Geyser-Spigot/config.yml').read_text()
        self.assertIn('auth-type: floodgate', config)
        self.assertIn('port: 19132', config)
        self.assertIn('port: 25565', config)
        self.assertFalse((paths[1] / 'plugins/Geyser-Spigot').exists())
        for path in paths:
            self.assertEqual((path / 'plugins/Geyser-Spigot.jar').read_bytes(), b'existing working binary')
        record_path = self.instances / 'jv-aaaaaa/config.json'
        record = json.loads(record_path.read_text())
        record['runtime']['bedrock_enabled'] = False
        self.private(record_path, json.dumps(record))
        result = self.crossplay('jv-aaaaaa')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((paths[0] / 'plugins/Geyser-Spigot.jar').exists())
        self.assertTrue((paths[0] / 'plugins/custom.jar').exists())
        self.assertTrue((paths[0] / 'plugins/Geyser-Spigot/config.yml').exists())
        self.assertTrue((paths[1] / 'plugins/Geyser-Spigot.jar').exists())

    def test_crossplay_preparation_failure_preserves_plugins(self):
        self.prepare_crossplay_helpers()
        data = self.instance('jv-aaaaaa')
        (data / 'plugins').mkdir()
        jar = data / 'plugins/Geyser-Spigot.jar'
        jar.write_bytes(b'working geyser')
        self.tool('curl', '#!/usr/bin/bash\nexit 1\n')
        result = self.crossplay('jv-aaaaaa')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(jar.read_bytes(), b'working geyser')
        self.assertNotIn('never-print-this-secret', result.stdout + result.stderr)

    def test_crossplay_failed_replacement_rolls_back_selected_pair(self):
        self.prepare_crossplay_helpers()
        data = self.instance('jv-aaaaaa')
        other = self.instance('jv-bbbbbb')
        for path in (data, other):
            (path / 'plugins').mkdir()
            (path / 'plugins/Geyser-Spigot.jar').write_bytes(b'previous geyser')
        helper = self.helpers / 'managed-crossplay.sh'
        helper.write_text(helper.read_text() + r"""
jv_crossplay_prepare() { :; }
jv_crossplay_install() {
    jv_crossplay_preserve "$1" || return 1
    printf damaged > "${DATA_PATH}/plugins/Geyser-Spigot.jar"
    printf partial > "${DATA_PATH}/plugins/floodgate-spigot.jar"
    return 1
}
""")
        result = self.crossplay('jv-aaaaaa')
        self.assertNotEqual(result.returncode, 0)
        for path in (data, other):
            self.assertEqual((path / 'plugins/Geyser-Spigot.jar').read_bytes(), b'previous geyser')
            self.assertFalse((path / 'plugins/floodgate-spigot.jar').exists())
        self.assertFalse((self.instances / 'jv-aaaaaa/crossplay-staging').exists())

    def test_host_guard_keeps_legacy_and_independent_paths(self):
        self.tool('systemctl', '#!/usr/bin/bash\nprintf stopping\n')
        legacy = self.helpers / 'interrupt-safety.sh'
        legacy.write_text('jv_prepare_minecraft_for_host_shutdown() { echo legacy >> "$FIXTURE_CALLS"; }\n')
        independent = self.helpers / 'instance-host-shutdown-prepare'
        independent.write_text('#!/usr/bin/bash\necho independent >> "$FIXTURE_CALLS"\n')
        independent.chmod(0o700)
        source = (LIB / 'host-shutdown-guard').read_text()
        source = source.replace('/usr/libexec/justvoxel/mjust/', str(self.helpers) + '/')
        result = subprocess.run(['bash', '-c', source], env=self.environment,
                                capture_output=True, text=True, timeout=12)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(set(self.calls.read_text().splitlines()), {'legacy', 'independent'})

    def test_shutdown_does_not_wait_for_maintenance_lock(self):
        import fcntl
        self.instance('jv-aaaaaa')
        self.running.write_text('{"minecraft-jv-aaaaaa":true}')
        self.players.write_text('{"minecraft-jv-aaaaaa":"There are 0 of a max of 20 players online:"}')
        with open(self.root / 'maintenance.lock', 'w') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.assertIn('kill --signal SIGUSR1 minecraft-jv-aaaaaa', self.shutdown())

    def test_crossplay_waits_for_maintenance_lock(self):
        import fcntl
        self.prepare_crossplay_helpers()
        data = self.instance('jv-aaaaaa', enabled=False)
        validator = self.helpers / 'validate-instance-runtime-commit'
        validator.write_text('#!/usr/bin/bash\necho validated >> "$FIXTURE_CALLS"\n')
        with open(self.root / 'maintenance.lock', 'w') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.crossplay('jv-aaaaaa')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((data / 'plugins').exists())
        self.assertFalse(self.calls.exists(), 'Preparation entered while independent maintenance held the lock')
        # Direct invocation models boot/systemd retry without an Agent parent.
        # Once independent maintenance finishes, the same helper can proceed.
        result = self.crossplay('jv-aaaaaa')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls.read_text(), 'validated\n')

    def test_crossplay_rejects_vanilla(self):
        self.prepare_crossplay_helpers()
        data = self.instance('jv-aaaaaa', software='vanilla')
        result = self.crossplay('jv-aaaaaa')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((data / 'plugins').exists())


if __name__ == '__main__':
    unittest.main()
