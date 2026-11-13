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
from unittest import mock
from types import SimpleNamespace
import stat

SOURCE = Path(__file__).resolve().parents[1] / 'mjust/libexec/minecraft-network-runtime'
payload = SOURCE.read_text().split("<<'PYTHON'\n", 1)[1].rsplit('\nPYTHON', 1)[0]
runtime = {'__name__': 'network_runtime_source_test'}
exec(compile(payload, str(SOURCE), 'exec'), runtime)


class PortableStandaloneRecovery(unittest.TestCase):
    def evidence(self, original):
        configs = {name: None for name in runtime['backend_paths']('paper-family')['portable_configs']}
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
            runtime['restore_portable'](str(root), os.getuid(), os.getgid(), 'paper-family')
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
                    runtime['restore_portable'](str(root), os.getuid(), os.getgid(), 'paper-family')
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
                runtime['restore_portable'](str(root), os.getuid(), os.getgid(), 'paper-family')
            self.assertEqual(outside.read_bytes(), b'preserved')


class BackendProfiles(unittest.TestCase):
    files = ('config/paper-global.yml', 'paper.yml', 'plugins/floodgate/key.pem',
             'plugins/floodgate/config.yml', 'plugins/floodgate-spigot.jar',
             'plugins/Geyser-Spigot.jar', '.justvoxel-network-patches.json')
    portable = ('config/paper-global.yml', 'paper.yml', 'plugins/floodgate/key.pem',
                'plugins/floodgate/config.yml')

    def test_supported_paper_family_software_resolves(self):
        for software in ('paper', 'purpur', 'pufferfish', 'leaf'):
            self.assertEqual(runtime['backend_profile'](software), 'paper-family')
        for software in ('fabric', 'quilt', 'forge', 'neoforge',
                         'spigot', 'vanilla', 'bedrock', '', 'unknown'):
            with self.subTest(software=software), self.assertRaises(ValueError):
                runtime['backend_profile'](software)

    def test_exact_ordered_ownership(self):
        self.assertEqual(set(runtime['BACKEND_PROFILES']), {'paper-family'})
        paths = runtime['backend_paths']('paper-family')
        self.assertEqual(paths['files'], self.files)
        self.assertEqual(paths['portable_configs'], self.portable)

    def test_unknown_profiles_fail_before_accessing_data(self):
        with mock.patch.dict(runtime, {'safe': mock.Mock(side_effect=AssertionError('no data access'))}):
            for operation, args in (
                    ('backend_paths', ('unknown',)),
                    ('restore_portable', ('/unused', 1000, 1000, 'unknown')),
                    ('prepare_backend_profile', ('unknown', '/unused')),
                    ('publish_backend_profile', ('unknown', '/unused', 1000, 1000, {}, {}))):
                with self.subTest(operation=operation), self.assertRaises(ValueError):
                    runtime[operation](*args)


class BackendStandaloneRecovery(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        base = Path(temporary.name)
        self.instance = 'jv-a1b2c3'
        self.root = base/'minecraft-instances'/self.instance/'data'
        self.root.mkdir(parents=True)
        self.instances = base/'instances'
        self.private = self.instances/self.instance
        self.private.mkdir(parents=True)
        self.snapshot = self.private/'network-before.json'
        self.blobs = self.private/'network-before'
        self.network = base/'network'
        (self.network/'artifacts').mkdir(parents=True)
        self.record = {'runtime': {'software':'paper', 'version':'1.20.6',
                                  'uid':os.getuid(), 'gid':os.getgid()},
                       'storage': {'data_path':str(self.root)}, 'network_member':True}
        self.state = {'config':{'enabled':True, 'members':[self.instance]},
                      'forwarding_secret':'test-forwarding-secret',
                      'floodgate_key':base64.b64encode(b'0123456789abcdef').decode()}
        self.jar = b'verified floodgate backend jar'
        artifacts = {}
        for name in runtime['EXTERNAL_ARTIFACTS'] | runtime['BAKED_ARTIFACTS']:
            data = self.jar if name == 'floodgate-spigot.jar' else ('verified '+name).encode()
            (self.network/'artifacts'/name).write_bytes(data)
            artifacts[name] = hashlib.sha256(data).hexdigest()
        (self.network/'artifacts.json').write_text(json.dumps(artifacts))
        (self.network/'state.json').write_text(json.dumps(self.state))
        self.save_record()
        original_stat = os.stat

        def snapshot_stat(path, *args, **kwargs):
            info = original_stat(path, *args, **kwargs)
            # Exercise private blob cleanup without requiring a root test runner.
            if str(path).startswith(str(self.blobs)+'/'):
                return SimpleNamespace(st_mode=info.st_mode, st_uid=0)
            return info

        patches = [
            mock.patch.dict(runtime, {
                'ROOT':str(self.network), 'INSTANCE_ROOT':str(self.instances),
                'PINNED_WHITELIST_SHA256':artifacts['global-whitelist.jar'],
                'private_json':lambda path:json.loads(runtime['read'](path)),
            }),
            mock.patch.object(os, 'chown'), mock.patch.object(os, 'fchown'),
            mock.patch.object(os, 'stat', side_effect=snapshot_stat),
            mock.patch('subprocess.run'),
        ]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)
        self.original = {}
        for name in BackendProfiles.files:
            # Exercise both saved contents and originally absent managed files.
            if name in ('paper.yml', '.justvoxel-network-patches.json'):
                self.original[name] = None
            else:
                self.original[name] = ('standalone '+name).encode()
                self.write(name, self.original[name])
        self.unowned = {'whitelist.json':b'backend whitelist',
                        'server.properties':b'original properties',
                        'world/level.dat':b'world data',
                        'plugins/user.jar':b'user plugin', 'mods/user.jar':b'user mod'}
        for name, data in self.unowned.items():
            self.write(name, data)

    def write(self, name, data):
        path = self.root/name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def save_record(self):
        (self.private/'config.json').write_text(json.dumps(self.record))

    def enroll(self):
        runtime['backend'](self.instance, publishing=True)

    def leave(self):
        self.record['network_member'] = False
        self.save_record()
        runtime['backend'](self.instance, publishing=True)

    def assert_unowned(self):
        for name, data in self.unowned.items():
            self.assertEqual((self.root/name).read_bytes(), data)

    def test_snapshot_publication_and_standalone_roundtrip_for_paper_family(self):
        for software in ('paper', 'purpur', 'pufferfish', 'leaf'):
            with self.subTest(software=software):
                self.record['runtime']['software'] = software
                self.record['network_member'] = True
                self.save_record()
                self.enroll()
                evidence_bytes = self.snapshot.read_bytes()
                evidence = json.loads(evidence_bytes)
                self.assertEqual(set(evidence), {'id', 'profile', 'files'})
                self.assertEqual(evidence['profile'], 'paper-family')
                self.assertEqual(tuple(evidence['files']), BackendProfiles.files)
                portable = json.loads((self.root/runtime['PORTABLE_NAME']).read_bytes())
                self.assertEqual(set(portable), {'schema', 'configs'})
                self.assertEqual(portable['schema'], 1)
                self.assertEqual(tuple(portable['configs']), BackendProfiles.portable)
                for index, name in enumerate(BackendProfiles.files):
                    original = self.original[name]
                    self.assertEqual(evidence['files'][name], None if original is None else hashlib.sha256(original).hexdigest())
                    if original is not None:
                        self.assertEqual((self.blobs/str(index)).read_bytes(), original)
                self.assertFalse((self.root/'plugins/Geyser-Spigot.jar').exists())
                self.assertEqual((self.root/'plugins/floodgate-spigot.jar').read_bytes(), self.jar)
                self.assertEqual((self.root/'plugins/floodgate/key.pem').read_bytes(), b'0123456789abcdef')
                self.assertEqual((self.root/'plugins/floodgate/config.yml').read_bytes(),
                                 b'key-file-name: key.pem\nusername-prefix: "."\nreplace-spaces: true\nconfig-version: 3\n')
                # A second publication reads deployed evidence without rewriting it.
                self.enroll()
                self.assertEqual(self.snapshot.read_bytes(), evidence_bytes)
                self.assert_unowned()
                self.leave()
                for name, original in self.original.items():
                    if original is None:
                        self.assertFalse((self.root/name).exists())
                    else:
                        self.assertEqual((self.root/name).read_bytes(), original)
                self.assert_unowned()
                self.assertFalse(self.snapshot.exists())
                self.assertFalse(self.blobs.exists())
                self.assertFalse((self.root/runtime['PORTABLE_NAME']).exists())

    def test_existing_legacy_snapshot_needs_no_profile_metadata(self):
        self.blobs.mkdir()
        files = {}
        for index, (name, data) in enumerate(self.original.items()):
            files[name] = None if data is None else hashlib.sha256(data).hexdigest()
            if data is not None:
                blob = self.blobs/str(index)
                blob.write_bytes(data)
                blob.chmod(0o600)
        legacy_bytes = json.dumps({'id':self.instance, 'files':files}).encode()
        self.snapshot.write_bytes(legacy_bytes)
        original_atomic = runtime['atomic']
        def no_evidence_rewrite(path, *args, **kwargs):
            self.assertNotEqual(path, str(self.snapshot))
            self.assertEqual(self.snapshot.read_bytes(), legacy_bytes)
            return original_atomic(path, *args, **kwargs)
        with mock.patch.dict(runtime, {'atomic': no_evidence_rewrite}):
            runtime['backend_preflight'](self.instance)
            self.enroll()
        self.assertEqual(self.snapshot.read_bytes(), legacy_bytes)
        # Restoration ownership comes from evidence even after a software change.
        self.record['runtime']['software'] = 'fabric'
        self.write('config/paper-global.yml', b'network configuration')
        with mock.patch.dict(runtime, {'atomic': no_evidence_rewrite}):
            self.leave()
        self.assertEqual((self.root/'config/paper-global.yml').read_bytes(), self.original['config/paper-global.yml'])
        self.assert_unowned()

    def backend_bytes(self):
        return {str(path.relative_to(self.root)): path.read_bytes()
                for path in self.root.rglob('*') if path.is_file()}

    def test_preflight_is_read_only_and_requires_verified_cache(self):
        artifact = self.network/'artifacts/floodgate-spigot.jar'
        for software in ('paper', 'purpur', 'pufferfish', 'leaf'):
            with self.subTest(software=software):
                self.record['runtime']['software'] = software
                self.save_record()
                artifact.write_bytes(self.jar)
                before = self.backend_bytes()
                private_before = {str(path):path.read_bytes() for path in self.private.rglob('*') if path.is_file()}
                verify = mock.Mock(wraps=runtime['validate_artifacts'])
                forbidden = mock.Mock(side_effect=AssertionError('preflight must not publish or resolve'))
                with mock.patch.dict(runtime, {'validate_artifacts': verify, 'atomic': forbidden,
                                              'prepare': forbidden, 'fetch': forbidden}):
                    runtime['backend_preflight'](self.instance)
                verify.assert_called_once_with()
                self.assertEqual(self.backend_bytes(), before)
                self.assertFalse(self.snapshot.exists())
                self.assertFalse(self.blobs.exists())
                self.assertEqual({str(path):path.read_bytes() for path in self.private.rglob('*') if path.is_file()}, private_before)
                artifact.write_bytes(b'corrupt prepared artifact')
                with self.assertRaisesRegex(ValueError, 'installed artifact checksum mismatch'):
                    runtime['backend_preflight'](self.instance)
                artifact.unlink()
                with self.assertRaises(FileNotFoundError):
                    runtime['backend_preflight'](self.instance)
                self.assertEqual(self.backend_bytes(), before)
                self.assertFalse(self.snapshot.exists())
                self.assertFalse(self.blobs.exists())

    def test_preflight_rejects_wrong_storage_and_unsupported_software(self):
        before = self.backend_bytes()
        for change in ('storage', 'software'):
            with self.subTest(change=change):
                original_path = self.record['storage']['data_path']
                if change == 'storage':
                    self.record['storage']['data_path'] = str(self.root.parent)
                else:
                    self.record['runtime']['software'] = 'fabric'
                self.save_record()
                with self.assertRaises(ValueError):
                    runtime['backend_preflight'](self.instance)
                self.record['storage']['data_path'] = original_path
                self.record['runtime']['software'] = 'paper'
                self.assertEqual(self.backend_bytes(), before)
                self.assertFalse(self.snapshot.exists())
                self.assertFalse(self.blobs.exists())

    def test_preflight_rejects_duplicate_plugins_without_mutation(self):
        import subprocess
        before = self.backend_bytes()
        with mock.patch('subprocess.run', side_effect=subprocess.CalledProcessError(1, 'managed-plugin-files')):
            with self.assertRaises(subprocess.CalledProcessError):
                runtime['backend_preflight'](self.instance)
        self.assertEqual(self.backend_bytes(), before)
        self.assertFalse(self.snapshot.exists())
        self.assertFalse(self.blobs.exists())

    def test_evidence_profile_and_exact_file_set_fail_closed(self):
        self.enroll()
        original = self.snapshot.read_bytes()
        before = self.backend_bytes()
        for bad in ('unknown', 'null', 'missing-file', 'extra-file', 'legacy-extra', 'legacy-missing'):
            with self.subTest(bad=bad):
                evidence = json.loads(original)
                if bad == 'unknown':
                    evidence['profile'] = 'future-profile'
                elif bad == 'null':
                    evidence['profile'] = None
                elif bad in ('missing-file', 'legacy-missing'):
                    del evidence['files']['paper.yml']
                else:
                    evidence['files']['plugins/unowned.jar'] = None
                if bad.startswith('legacy-'):
                    del evidence['profile']
                self.snapshot.write_text(json.dumps(evidence))
                saved = self.snapshot.read_bytes()
                for operation in (lambda: runtime['backend_preflight'](self.instance), self.enroll, self.leave):
                    self.record['network_member'] = True
                    self.save_record()
                    with self.assertRaises(ValueError):
                        operation()
                    self.assertEqual(self.snapshot.read_bytes(), saved)
                    self.assertEqual(self.backend_bytes(), before)
        self.snapshot.write_bytes(original)

    def test_active_member_requires_runtime_profile_agreement(self):
        self.enroll()
        self.record['runtime']['software'] = 'fabric'
        self.save_record()
        with self.assertRaises(ValueError):
            runtime['backend_preflight'](self.instance)
        with self.assertRaises(ValueError):
            self.enroll()

    def test_profile_bound_restore_uses_snapshot_not_current_software(self):
        self.enroll()
        self.record['runtime']['software'] = 'fabric'
        self.leave()
        for name, original in self.original.items():
            if original is not None:
                self.assertEqual((self.root/name).read_bytes(), original)
        self.assert_unowned()

    def test_backend_publication_never_resolves_artifacts(self):
        forbidden = mock.Mock(side_effect=AssertionError('publication must consume prepared cache'))
        with mock.patch.dict(runtime, {'prepare': forbidden, 'prepare_backend_profile': forbidden, 'fetch': forbidden}):
            for software in ('paper', 'purpur', 'pufferfish', 'leaf'):
                with self.subTest(software=software):
                    self.record['runtime']['software'] = software
                    self.record['network_member'] = True
                    self.save_record()
                    self.enroll()
                    self.assertEqual((self.root/'plugins/floodgate-spigot.jar').read_bytes(), self.jar)
                    self.leave()
        forbidden.assert_not_called()

    def test_bad_snapshot_identity_paths_checksum_and_symlinks_preserve_evidence(self):
        self.enroll()
        original = self.snapshot.read_bytes()
        for bad in ('identity', 'path', 'checksum', 'symlink'):
            with self.subTest(bad=bad):
                evidence = json.loads(original)
                target = self.root/'config/paper-global.yml'
                if bad == 'identity':
                    evidence['id'] = 'jv-xxxxxx'
                elif bad == 'path':
                    evidence['files']['world/level.dat'] = None
                elif bad == 'checksum':
                    evidence['files']['config/paper-global.yml'] = '0'*64
                else:
                    target.unlink()
                    target.symlink_to(self.root/'world/level.dat')
                self.snapshot.write_text(json.dumps(evidence))
                with self.assertRaises(ValueError):
                    self.leave()
                self.assertTrue(self.snapshot.exists())
                self.assert_unowned()

    def test_unsupported_software_cannot_publish_as_member(self):
        self.enroll()
        before = self.snapshot.read_bytes()
        for software in ('fabric', 'quilt', 'forge', 'neoforge', 'spigot',
                         'vanilla', 'bedrock', '', 'unknown'):
            with self.subTest(software=software):
                self.record['runtime']['software'] = software
                self.save_record()
                with self.assertRaises(ValueError):
                    runtime['backend_preflight'](self.instance)
                with self.assertRaises(ValueError):
                    self.enroll()
                self.assertEqual(self.snapshot.read_bytes(), before)
                self.assert_unowned()

    def test_managed_plugin_checks_precede_snapshot_and_publication(self):
        import subprocess
        with mock.patch('subprocess.run') as command:
            command.side_effect = [None, subprocess.CalledProcessError(1, 'managed-plugin-files')]
            with self.assertRaises(subprocess.CalledProcessError):
                self.enroll()
            self.assertEqual(command.call_args_list, [
                mock.call(['/usr/libexec/justvoxel/mjust/managed-plugin-files',
                           str(self.root/'plugins'), component, keep, '--check'],
                          check=True, stdout=subprocess.DEVNULL)
                for component, keep in [('geyser', 'Geyser-Spigot.jar'),
                                        ('floodgate', 'floodgate-spigot.jar')]])
        self.assertFalse(self.snapshot.exists())
        self.assertFalse((self.root/runtime['PORTABLE_NAME']).exists())
        for name, original in self.original.items():
            if original is not None:
                self.assertEqual((self.root/name).read_bytes(), original)
        self.assert_unowned()

    def test_paper_version_specific_forwarding_patches(self):
        for software in ('paper', 'purpur', 'pufferfish', 'leaf'):
            for version, filename, parent, field in (
                    ('1.18.2', '/data/paper.yml', '$.settings', 'velocity-support'),
                    ('1.19', '/data/config/paper-global.yml', '$.proxies', 'velocity'),
                    ('LATEST', '/data/config/paper-global.yml', '$.proxies', 'velocity')):
                with self.subTest(software=software, version=version):
                    self.record['runtime']['software'] = software
                    self.record['runtime']['version'] = version
                    self.save_record()
                    self.enroll()
                    patches = json.loads((self.root/'.justvoxel-network-patches.json').read_bytes())
                    self.assertEqual(patches, {'patches':[{'file':filename, 'ops':[{'$put':{
                        'path':parent, 'key':field, 'value':{'enabled':True, 'online-mode':True,
                                                           'secret':self.state['forwarding_secret']}}}]}]})


class NetworkArtifacts(unittest.TestCase):
    def setUp(self):
        self.root = '/network-test'
        self.files = {}
        self.baked = {'limboapi.jar': b'verified stable limbo release', 'justvoxel-lobby.jar': b'local lobby output'}
        self.manifest = {}
        for name in runtime['EXTERNAL_ARTIFACTS']:
            data = ('verified external '+name).encode()
            self.files[self.root+'/artifacts/'+name] = data
            self.manifest[name] = hashlib.sha256(data).hexdigest()
        self.whitelist_hash = self.manifest['global-whitelist.jar']
        self.save_manifest()
        self.writes = []
        def atomic(path, data, *args):
            self.writes.append(path)
            self.files[path] = data
        self.patch = mock.patch.dict(runtime, {
            'ROOT': self.root,
            'PINNED_WHITELIST_SHA256': self.whitelist_hash,
            'read': lambda path, limit=1024*1024: self.files[path],
            'private_json': lambda path: json.loads(self.files[path]),
            'directory': lambda *args: None,
            'atomic': atomic,
            'baked_artifacts': lambda: dict(self.baked),
            'fetch': mock.Mock(side_effect=AssertionError('must not redownload external artifacts')),
        })
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.exists = mock.patch.object(runtime['os'].path, 'exists', side_effect=lambda path: path in self.files)
        self.exists.start()
        self.addCleanup(self.exists.stop)

    def save_manifest(self):
        self.files[self.root+'/artifacts.json'] = json.dumps(self.manifest).encode()

    def migrate(self):
        runtime['prepare']()
        self.manifest = json.loads(self.files[self.root+'/artifacts.json'])

    def test_old_four_artifact_evidence_is_validated_and_extended_atomically(self):
        old = dict(self.manifest)
        original_bytes = {name:self.files[self.root+'/artifacts/'+name] for name in old}
        self.migrate()
        self.assertEqual(set(self.manifest), runtime['EXTERNAL_ARTIFACTS'] | runtime['BAKED_ARTIFACTS'])
        for name, digest in old.items():
            self.assertEqual(self.manifest[name], digest)
            self.assertEqual(self.files[self.root+'/artifacts/'+name], original_bytes[name])
        for name, data in self.baked.items():
            self.assertEqual(self.manifest[name], hashlib.sha256(data).hexdigest())
        self.assertEqual(self.writes[-1], self.root+'/artifacts.json')
        self.assertEqual(len(self.writes), 3)

    def test_six_artifacts_validate_without_rewriting_evidence(self):
        self.migrate()
        self.writes.clear()
        runtime['prepare']()
        runtime['validate_artifacts']()
        self.assertEqual(self.writes, [])

    def test_unknown_partial_or_malformed_manifests_fail_closed(self):
        original = dict(self.manifest)
        for bad in ({**original, 'unknown.jar':'a'*64},
                    {**original, 'limboapi.jar':'a'*64},
                    {name:digest for name,digest in original.items() if name != 'floodgate-spigot.jar'},
                    {**original, 'global-whitelist.jar':'a'*64},
                    {**original, 'floodgate-spigot.jar': []}, []):
            with self.subTest(bad=bad):
                self.files[self.root+'/artifacts.json'] = json.dumps(bad).encode()
                self.writes.clear()
                with self.assertRaises(ValueError):
                    runtime['prepare']()
                self.assertEqual(self.writes, [])

    def test_each_cached_jar_is_hash_verified(self):
        self.migrate()
        for name in sorted(self.manifest):
            with self.subTest(name=name):
                path = self.root+'/artifacts/'+name
                old = self.files[path]
                self.files[path] = b'changed jar'
                with self.assertRaises(ValueError):
                    runtime['validate_artifacts']()
                self.files[path] = old

    def test_both_baked_copies_are_hash_verified_at_startup(self):
        self.migrate()
        self.files[self.root+'/state.json'] = json.dumps({'schema':1,'config':{'enabled':True}}).encode()
        with mock.patch.object(runtime['os'].path, 'lexists', return_value=False):
            runtime['check']()
            for name in sorted(self.baked):
                path = self.root+'/artifacts/'+name
                old = self.files[path]
                self.files[path] = b'changed baked artifact copy'
                with self.assertRaises(ValueError):
                    runtime['check']()
                self.files[path] = old

    def test_image_rebuild_refreshes_baked_artifacts_and_preserves_external_evidence(self):
        self.migrate()
        manifest = dict(self.manifest)
        external_bytes = {name:self.files[self.root+'/artifacts/'+name] for name in runtime['EXTERNAL_ARTIFACTS']}
        self.writes.clear()
        self.baked['limboapi.jar'] = b'another verified stable image release'
        runtime['prepare']()
        refreshed = runtime['validate_artifacts']()
        for name, data in self.baked.items():
            self.assertEqual(self.files[self.root+'/artifacts/'+name], data)
            self.assertEqual(refreshed[name], hashlib.sha256(data).hexdigest())
        for name, data in external_bytes.items():
            self.assertEqual(self.files[self.root+'/artifacts/'+name], data)
            self.assertEqual(refreshed[name], manifest[name])
        self.assertEqual(set(self.writes), {self.root+'/artifacts/'+name for name in self.baked} | {self.root+'/artifacts.json'})
        runtime['fetch'].assert_not_called()

    def test_image_refresh_rejects_corrupt_cached_baked_artifact_before_writing(self):
        self.migrate()
        self.files[self.root+'/artifacts/limboapi.jar'] = b'corrupt cached jar'
        self.baked['limboapi.jar'] = b'another verified stable image release'
        original_files = dict(self.files)
        self.writes.clear()
        with mock.patch.dict(runtime, {'baked_artifacts': mock.Mock(side_effect=AssertionError('must validate existing evidence first'))}):
            with self.assertRaisesRegex(ValueError, 'installed artifact checksum mismatch'):
                runtime['prepare']()
        self.assertEqual(self.writes, [])
        self.assertEqual(self.files, original_files)
        runtime['fetch'].assert_not_called()

    def test_velocity_start_publishes_both_baked_jars_and_generated_config(self):
        self.migrate()
        self.files[self.root+'/state.json'] = json.dumps({'schema':1,'config':{'enabled':True}}).encode()
        generated = {name:base64.b64encode(b'generated config').decode() for name in [
            'velocity/velocity.toml','velocity/forwarding.secret',
            'velocity/plugins/global-whitelist/config.properties','velocity/plugins/global-whitelist/whitelist.json',
            'velocity/plugins/floodgate/key.pem','velocity/plugins/floodgate/config.yml',
            'velocity/plugins/Geyser-Velocity/config.yml','velocity/plugins/justvoxel-lobby/lobby.conf']}
        self.files[self.root+'/generated.json'] = json.dumps(generated).encode()
        with mock.patch.object(runtime['os'].path, 'lexists', return_value=False), \
             mock.patch.object(runtime['os'], 'geteuid', return_value=0), \
             mock.patch.object(runtime['sys'], 'argv', ['helper','start-velocity']):
            runtime['main']()
        runtime['fetch'].assert_not_called()
        for name,data in self.baked.items():
            self.assertEqual(self.files[self.root+'/velocity/plugins/'+name],data)
        self.assertIn(self.root+'/velocity/plugins/justvoxel-lobby/lobby.conf',self.writes)
        self.assertNotIn('start-picolobby', SOURCE.read_text())


class BakedArtifactBoundary(unittest.TestCase):
    def test_baked_files_are_nonempty_root_owned_and_mode_0644(self):
        original = runtime['baked_artifacts']
        for uid, mode, data, valid in [(0,0o644,b'jar',True),(1,0o644,b'jar',False),
                                       (0,0o666,b'jar',False),(0,0o644,b'',False)]:
            with self.subTest(uid=uid,mode=mode,data=data), \
                 mock.patch.dict(runtime, {'read':lambda *args:data}), \
                 mock.patch.object(runtime['os'],'stat',return_value=SimpleNamespace(st_uid=uid,st_mode=stat.S_IFREG | mode)):
                if valid:
                    self.assertEqual(set(original()), runtime['BAKED_ARTIFACTS'])
                else:
                    with self.assertRaises(ValueError): original()


class NetworkPackageContract(unittest.TestCase):
    def test_stable_package_consumption(self):
        root = SOURCE.parents[2]
        containerfile = (root/'Containerfile').read_text()
        build = (root/'build_files/build-common.sh').read_text()
        self.assertIn(
            'ARG MINECRAFT_NETWORK_PACKAGE_IMAGE=ghcr.io/home-server-project/minecraft-network-plugins:stable',
            containerfile.splitlines())
        self.assertIn('FROM ${MINECRAFT_NETWORK_PACKAGE_IMAGE} AS minecraft-network-package',
                      containerfile.splitlines())
        for jar in ['limboapi.jar', 'justvoxel-lobby.jar']:
            self.assertIn(
                f'COPY --from=minecraft-network-package /artifacts/{jar} /minecraft-network-artifacts/{jar}',
                containerfile.splitlines())
        self.assertIn('install -d -m0755 /usr/lib/justvoxel/minecraft-network', build)
        self.assertIn('for network_plugin in limboapi justvoxel-lobby; do', build)
        self.assertIn('[[ -s "/ctx/minecraft-network-artifacts/${network_plugin}.jar" ]]', build)
        self.assertIn(
            'install -o0 -g0 -m0644 "/ctx/minecraft-network-artifacts/${network_plugin}.jar" '
            '"/usr/lib/justvoxel/minecraft-network/${network_plugin}.jar"', build)
        for path in ['build_files/build-minecraft-network-plugins.sh',
                     'build_files/resolve-limboapi-release.py',
                     'build_files/resolve-velocity-release.py', 'velocity-lobby']:
            self.assertFalse((root/path).exists(), path)
        for name in ['build-testing.yml', 'build.yml']:
            workflow = (root/'.github/workflows'/name).read_text()
            self.assertNotIn('actions/setup-java', workflow)
            self.assertNotIn('build-minecraft-network-plugins.sh', workflow)


if __name__ == '__main__':
    unittest.main()
