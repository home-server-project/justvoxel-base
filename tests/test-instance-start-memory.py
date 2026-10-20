#!/usr/bin/env python3
"""Source fixtures for the final guard; never runs systemctl or a container."""
import json
import os
from pathlib import Path
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
SOURCE = (REPO / 'mjust/libexec/validate-instance-start-memory').read_text()
# Load only the guard definitions. Adapt expected ownership to the test runner;
# production accepts root:root only and has no environment/path override.
DEFINITIONS = SOURCE.split("<<'PY'\n", 1)[1].split('\ntry:\n', 1)[0]
DEFINITIONS = DEFINITIONS.replace('info.st_uid != 0', 'info.st_uid != os.geteuid()').replace('info.st_gid != 0', 'info.st_gid != os.getegid()')


class StartupMemory(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        base = Path(self.temporary.name)
        self.root = base / 'instances'
        self.quadlets = base / 'quadlets'
        self.root.mkdir()
        self.quadlets.mkdir()
        meminfo = base / 'meminfo'
        meminfo.write_text('MemTotal: 8055808 kB\nMemAvailable: 999999999 kB\nSwapTotal: 999999999 kB\n')  # 7867 MiB
        self.ns = {}
        exec(compile(DEFINITIONS, str(REPO / 'mjust/libexec/validate-instance-start-memory'), 'exec'), self.ns)
        self.ns.update(ROOT=self.root, QUADLETS=self.quadlets, MEMINFO=meminfo, command=self.command)
        self.states = {}
        self.config('jv-aaaaaa', '6G')
        self.config('jv-bbbbbb', '6G')

    def config(self, identifier, limit):
        folder = self.root / identifier
        folder.mkdir(exist_ok=True)
        path = folder / 'config.json'
        path.write_text(json.dumps({'schema_version': 1, 'runtime': {'id': identifier, 'container_memory': limit}}))
        path.chmod(0o600)
        self.states[identifier] = 'inactive'

    def command(self, *args):
        if args[0] == 'list-units':
            return ''.join(f'minecraft-{identifier}.service loaded {state} mock\n' for identifier, state in self.states.items())
        self.assertEqual(args[:3], ('show', '--property=LoadState,ActiveState', '--'))
        identifier = args[-1][len('minecraft-'):-len('.service')]
        return f'LoadState=loaded\nActiveState={self.states[identifier]}\n'

    def admit(self):
        self.ns['admit']('jv-bbbbbb')

    def test_stopped_configs_and_many_installed_are_ignored(self):
        self.config('jv-cccccc', '6G')
        self.config('jv-dddddd', '6G')
        self.admit()

    def test_active_and_activating_limits_count(self):
        for state in ('active', 'activating', 'reloading'):
            self.states['jv-aaaaaa'] = state
            with self.assertRaises(ValueError):
                self.admit()

    def test_candidate_exactly_once_even_activating(self):
        self.states['jv-bbbbbb'] = 'activating'
        self.admit()

    def test_safe_simultaneous_4g_2g(self):
        self.config('jv-aaaaaa', '4G')
        self.config('jv-bbbbbb', '2G')
        self.states['jv-aaaaaa'] = 'active'
        self.admit()

    def test_malformed_relevant_config_fails_closed(self):
        self.states['jv-aaaaaa'] = 'active'
        (self.root / 'jv-aaaaaa/config.json').write_text('{')
        with self.assertRaises(ValueError):
            self.admit()

    def test_candidate_identity_must_match(self):
        path = self.root / 'jv-bbbbbb/config.json'
        record = json.loads(path.read_text())
        record['runtime']['id'] = 'jv-aaaaaa'
        path.write_text(json.dumps(record))
        with self.assertRaises(ValueError):
            self.admit()

    def test_missing_active_configuration_is_not_ignored(self):
        self.states['jv-cccccc'] = 'active'
        with self.assertRaises(FileNotFoundError):
            self.admit()

    def test_unknown_runtime_state_fails_closed(self):
        self.states['jv-aaaaaa'] = 'deactivating'
        with self.assertRaises(ValueError):
            self.admit()

    def test_provisional_candidate_requires_no_published_identity(self):
        # Runtime-commit validates the existing operation-bound permit first;
        # the capacity guard can inspect that exact unpublished candidate.
        self.admit()
        crossplay = (REPO / 'mjust/libexec/instance-crossplay-start').read_text()
        lock = crossplay.index('exec 9>/run/justvoxel-minecraft-maintenance.lock')
        held = crossplay.index('flock -w 5 9')
        commit = crossplay.index('/validate-instance-runtime-commit')
        guard = crossplay.index('/validate-instance-start-memory')
        preparation = crossplay.index('readarray -t values')
        self.assertLess(lock, held)
        self.assertLess(held, commit)
        self.assertLess(commit, guard)
        self.assertLess(guard, preparation)
        quadlet = (REPO / 'templates/quadlets/minecraft-instance.container.in').read_text()
        self.assertIn('ExecStartPre=/usr/libexec/justvoxel/mjust/instance-crossplay-start @@INSTANCE_ID@@', quadlet)


if __name__ == '__main__':
    unittest.main()
