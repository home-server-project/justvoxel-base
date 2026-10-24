#!/usr/bin/env python3
"""Bounded network configuration recovery tests; no host commands or containers.

This file is intentionally added without executing it during implementation.
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1] / 'mjust/libexec/minecraft-network-runtime'
payload = SOURCE.read_text().split("<<'PYTHON'\n", 1)[1].rsplit('\nPYTHON', 1)[0]
runtime = {'__name__': 'network_runtime_source_test'}
exec(compile(payload, str(SOURCE), 'exec'), runtime)


class PortableStandaloneRecovery(unittest.TestCase):
    def evidence(self, original):
        configs = {name: None for name in runtime['PORTABLE_CONFIGS']}
        configs['config/paper-global.yml'] = {
            'data': base64.b64encode(original).decode(),
            'sha256': hashlib.sha256(original).hexdigest(),
        }
        return {'schema': 1, 'configs': configs}

    def test_import_restores_standalone_without_replacing_whitelist(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root/'config').mkdir()
            (root/'config/paper-global.yml').write_bytes(b'network forwarding')
            (root/'whitelist.json').write_bytes(b'backend whitelist')
            (root/'.justvoxel-network-patches.json').write_bytes(b'network secret')
            original = b'original standalone Paper configuration\n'
            (root/runtime['PORTABLE_NAME']).write_text(json.dumps(self.evidence(original)))
            runtime['restore_portable'](str(root), os.getuid(), os.getgid())
            self.assertEqual((root/'config/paper-global.yml').read_bytes(), original)
            self.assertEqual((root/'whitelist.json').read_bytes(), b'backend whitelist')
            self.assertFalse((root/'.justvoxel-network-patches.json').exists())
            self.assertFalse((root/runtime['PORTABLE_NAME']).exists())

    def test_unknown_paths_and_bad_checksums_preserve_evidence(self):
        for malicious in ('path', 'checksum'):
            with self.subTest(malicious=malicious), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root/'config').mkdir()
                current = root/'config/paper-global.yml'
                current.write_bytes(b'current')
                evidence = self.evidence(b'original')
                if malicious == 'path':
                    evidence['configs']['../../outside'] = None
                else:
                    evidence['configs']['config/paper-global.yml']['sha256'] = '0'*64
                journal = root/runtime['PORTABLE_NAME']
                journal.write_text(json.dumps(evidence))
                with self.assertRaises(ValueError):
                    runtime['restore_portable'](str(root), os.getuid(), os.getgid())
                self.assertEqual(current.read_bytes(), b'current')
                self.assertTrue(journal.exists())

    def test_symlink_target_is_not_followed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root/'config').mkdir()
            outside = root/'outside'
            outside.write_bytes(b'preserved')
            (root/'config/paper-global.yml').symlink_to(outside)
            (root/runtime['PORTABLE_NAME']).write_text(json.dumps(self.evidence(b'original')))
            with self.assertRaises(ValueError):
                runtime['restore_portable'](str(root), os.getuid(), os.getgid())
            self.assertEqual(outside.read_bytes(), b'preserved')


if __name__ == '__main__':
    unittest.main()
