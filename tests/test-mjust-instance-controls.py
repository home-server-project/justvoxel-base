#!/usr/bin/env python3
"""Focused frontend tests using a temporary Management API fixture."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class InstanceControls(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.env = dict(os.environ, FIXTURE_DIR=str(self.directory))
        (self.directory / 'inventory').write_text(json.dumps({'instances': [{'id': 'jv-a1b2c3'}, {'id': 'jv-b1b2c3'}]}))
        api = self.directory / 'api-client'
        api.write_text('''#!/usr/bin/bash
printf '%s\\n' "$*" >>"${FIXTURE_DIR}/calls"
if [[ $2 == /v1/minecraft/instances/runtime ]]; then cat "${FIXTURE_DIR}/inventory"
elif [[ $1 == GET ]]; then echo '{"lines":["selected log"]}'
else
 body=$(cat)
 if [[ -f ${FIXTURE_DIR}/confirm && $body != *true* ]]; then
  echo '{"confirmation_required":true,"online":1,"players":["Alex"]}'
 else echo '{"ok":true,"message":"Minecraft action requested."}'; fi
fi
''')
        api.chmod(0o700)
        (self.directory / 'ui.sh').write_text('jui_has_tty() { return 1; }\n')
        for name in ('logs', 'service', 'instance-selection.sh'):
            source = (ROOT / 'mjust/libexec' / name).read_text()
            source = source.replace('/usr/libexec/justvoxel/mjust/', str(self.directory) + '/')
            # Test copies exercise caller behavior without host privilege or actions.
            if name in ('logs', 'service'):
                self.assertIn('if (( EUID != 0 )); then', source)
                source = source.replace('if (( EUID != 0 )); then', 'if false; then')
            (self.directory / name).write_text(source)

    def run_command(self, name, *args, input=''):
        result = subprocess.run(['bash', str(self.directory / name), *args], input=input, text=True, capture_output=True, env=self.env)
        calls = (self.directory / 'calls').read_text().splitlines() if (self.directory / 'calls').exists() else []
        return result, calls

    def test_selected_logs(self):
        result, calls = self.run_command('logs', '--instance', 'jv-a1b2c3')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, 'selected log\n')
        self.assertEqual(calls[-1], 'GET /v1/minecraft/instances/jv-a1b2c3/logs?limit=100&format=cat')

    def test_menu_style_single_server_logs(self):
        (self.directory / 'inventory').write_text('{"instances":[{"id":"jv-a1b2c3"}]}')
        result, calls = self.run_command('logs')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('/jv-a1b2c3/logs?', calls[-1])

    def test_ambiguous_stale_malformed_and_empty_selection(self):
        for command, prefix in [('logs', []), ('service', ['restart'])]:
            for selector in [[], ['--instance', 'jv-absent1'], ['--instance', 'friendly-name'], ['--instance', ''], ['']]:
                result, calls = self.run_command(command, *prefix, *selector)
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(all(call.endswith('/runtime') for call in calls), calls)
        (self.directory / 'inventory').write_text('{"instances":[]}')
        result, _ = self.run_command('logs')
        self.assertNotEqual(result.returncode, 0)

    def test_lifecycle_paths_and_confirmation_reuses_identity(self):
        for action in ('start', 'stop', 'restart'):
            (self.directory / 'calls').write_text('')
            (self.directory / 'confirm').touch()
            result, calls = self.run_command('service', action, '--instance', 'jv-a1b2c3', input='yes\n')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(calls, ['GET /v1/minecraft/instances/runtime'] + [f'POST /v1/minecraft/instances/jv-a1b2c3/{action} --data'] * 2)
            self.assertNotIn('jv-b1b2c3', '\n'.join(calls))


if __name__ == '__main__':
    unittest.main()
