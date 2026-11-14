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
        for software in ('forge', 'neoforge',
                         'spigot', 'vanilla', 'bedrock', '', 'unknown'):
            with self.subTest(software=software), self.assertRaises(ValueError):
                runtime['backend_profile'](software)

    def test_fabric_family_mapping(self):
        for software in ('fabric','quilt'):
            self.assertEqual(runtime['backend_profile'](software),'fabric-family')
        self.assertEqual(runtime['backend_paths']('fabric-family')['files'],
                         (runtime['FPL_FILE'],runtime['API_FILE'],runtime['FPL_CONFIG']))

    def test_exact_ordered_ownership(self):
        self.assertEqual(set(runtime['BACKEND_PROFILES']), {'paper-family','fabric-family'})
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
                    self.record['runtime']['software'] = 'forge'
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

    def test_unsupported_or_mismatched_software_cannot_publish_as_member(self):
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


# Tiny synthetic jars carry only descriptors; no large fixtures or downloads.
def fabric_jar(identity='fabricproxy-lite', dependencies=None, nested=None, version='1.0.0'):
    import io
    import zipfile
    desc = {'schemaVersion':1,'id':identity,'version':version,
            'depends':dependencies if dependencies is not None else {
                'minecraft':'~1.20.6','fabricloader':'>=0.15.0'}}
    if nested:
        desc['jars'] = [{'file':'META-INF/jars/'+name} for name in nested]
    output = io.BytesIO()
    with zipfile.ZipFile(output,'w') as jar:
        jar.writestr('fabric.mod.json',json.dumps(desc))
        for name,data in (nested or {}).items():
            jar.writestr('META-INF/jars/'+name,data)
    return output.getvalue()


def fabric_release(project, data, identity='release1'):
    return {'project_id':project,'id':identity,'version_type':'release','status':'listed',
            'date_published':'2026-01-01T00:00:00Z','version_number':'1.0.0',
            'game_versions':['1.20.6'],'loaders':['fabric'],'dependencies':[],
            'files':[{'primary':True,'filename':'mod.jar','size':len(data),
                      'url':'https://cdn.modrinth.com/data/'+project+'/versions/'+identity+'/mod.jar',
                      'hashes':{'sha512':hashlib.sha512(data).hexdigest()}}]}


class FabricResolution(unittest.TestCase):
    def test_exact_release_and_artifact(self):
        data = fabric_jar()
        release = fabric_release(runtime['FPL_PROJECT'],data)
        with mock.patch.dict(runtime,{'modrinth_metadata':mock.Mock(return_value=[release]),
                                     'fetch_modrinth_artifact':mock.Mock(return_value=data)}):
            self.assertEqual(runtime['modrinth_release'](runtime['FPL_PROJECT'],'1.20.6'),[release])
            blob,mods,entry = runtime['release_artifact'](release,runtime['FPL_PROJECT'],'1.20.6','fabricproxy-lite')
        self.assertEqual(blob,data)
        self.assertEqual(entry['release_id'],'release1')
        runtime['verify_dependencies'](mods,'1.20.6','0.16.14','21.0')
        with self.assertRaises(ValueError):
            runtime['verify_dependencies'](mods,'1.20.6','0.14.0','21.0')

    def test_publisher_and_artifact_mismatches(self):
        for bad in ('project','game','primary','cdn','path','hash','id','dependency'):
            with self.subTest(bad=bad):
                data = fabric_jar('wrong-id' if bad=='id' else 'fabricproxy-lite',
                                  {'minecraft':'~1.20.6','fabricloader':'>=0.15.0','unknown':'*'} if bad=='dependency' else None)
                release = fabric_release(runtime['FPL_PROJECT'],data)
                if bad=='project': release['project_id']='wrong'
                if bad=='game': release['game_versions']=['1.21']
                if bad=='primary': release['files'][0]['primary']=False
                if bad=='cdn': release['files'][0]['url']=release['files'][0]['url'].replace('cdn.modrinth.com','example.com')
                if bad=='path': release['files'][0]['url']=release['files'][0]['url'].replace('release1','other')
                if bad=='hash': release['files'][0]['hashes']['sha512']='0'*128
                with mock.patch.dict(runtime,{'fetch_modrinth_artifact':mock.Mock(return_value=data)}), self.assertRaises(ValueError):
                    _,mods,_ = runtime['release_artifact'](release,runtime['FPL_PROJECT'],'1.20.6','fabricproxy-lite')
                    runtime['verify_dependencies'](mods,'1.20.6','0.16.14','21.0')

    def test_unknown_ranges_fail_closed(self):
        self.assertFalse(runtime['accepts']('^0.1 || arbitrary','0.16.14'))
        self.assertFalse(runtime['accepts']('>=0.17','0.16.14'))
        self.assertTrue(runtime['accepts']('>=0.15 <0.17','0.16.14'))

    def test_quilt_requires_authoritative_selected_loader_provides(self):
        import io
        import zipfile
        record = {'runtime':{'software':'quilt','version':'1.20.6','loader_version':'0.26.4',
                             'image_tag':'java21'},'storage':{'data_path':'/unused'}}
        for valid in (True,False):
            output = io.BytesIO()
            with zipfile.ZipFile(output,'w') as jar:
                jar.writestr('quilt.mod.json',json.dumps({'quilt_loader':{'id':'quilt_loader',
                    'version':'0.26.4','provides':[{'id':'fabricloader','version':'0.16.14'}] if valid else []}}))
            data = output.getvalue()
            def download(url,limit=runtime['MAX_JAR']):
                return hashlib.sha512(data).hexdigest().encode() if url.endswith('.sha512') else data
            with mock.patch('subprocess.run') as verifier, mock.patch.dict(runtime,{'fetch_modrinth_artifact':download}):
                if valid:
                    self.assertEqual(runtime['fabric_loader'](record),'0.16.14')
                else:
                    with self.assertRaises(ValueError): runtime['fabric_loader'](record)
                self.assertEqual(verifier.call_args.args[0][2:4],['quilt','1.20.6'])


class FabricNetworkLifecycle(unittest.TestCase):
    write = BackendStandaloneRecovery.write
    save_record = BackendStandaloneRecovery.save_record
    leave = BackendStandaloneRecovery.leave
    assert_unowned = BackendStandaloneRecovery.assert_unowned

    def setUp(self):
        BackendStandaloneRecovery.setUp(self)
        # Reuse paths and private-file mocks; replace just the Paper-owned fixture.
        for name in BackendProfiles.files:
            target = self.root/name
            if target.exists(): target.unlink()
        self.original = {}
        self.record['runtime'].update(software='fabric',version='1.20.6',loader_version='0.16.14',image_tag='java21')
        self.save_record()
        self.unowned['mods/user.jar'] = fabric_jar('unrelated-mod')
        self.write('mods/user.jar',self.unowned['mods/user.jar'])
        self.state['schema']=1
        self.state['forwarding_secret']='a'*64
        (self.network/'state.json').write_text(json.dumps(self.state))
        real_stat = os.stat
        def stage_stat(path,*args,**kwargs):
            info = real_stat(path,*args,**kwargs)
            if str(path).startswith(str(self.private/'network-staging')):
                return SimpleNamespace(st_mode=info.st_mode,st_uid=0)
            return info
        patch = mock.patch.object(os,'stat',side_effect=stage_stat)
        patch.start()
        self.addCleanup(patch.stop)
        self.config = b'# preserve comments\nsecret = "standalone" # keep\nhackEarlySend = true\n'
        self.write(runtime['FPL_CONFIG'],self.config)
        self.api = fabric_jar('fabric-api',version='0.100.0+1.20.6')
        self.fpl = fabric_jar(dependencies={'minecraft':'~1.20.6','fabricloader':'>=0.15.0','fabric-api':'>=0.100.0'})
        self.releases = {runtime['FPL_PROJECT']:fabric_release(runtime['FPL_PROJECT'],self.fpl),
                         runtime['API_PROJECT']:fabric_release(runtime['API_PROJECT'],self.api,'api1')}
        def metadata(url,limit):
            import urllib.parse
            query = urllib.parse.parse_qs(urllib.parse.urlsplit(url).query)
            self.assertEqual(json.loads(query['loaders'][0]),['fabric'])
            self.assertEqual(json.loads(query['game_versions'][0]),['1.20.6'])
            for project,release in self.releases.items():
                if '/project/'+project+'/' in url: return json.dumps([release]).encode()
            raise AssertionError('unexpected publisher request')
        def download(url,limit=runtime['MAX_JAR']):
            return self.api if '/'+runtime['API_PROJECT']+'/' in url else self.fpl
        patch = mock.patch.dict(runtime,{'modrinth_metadata':lambda url:json.loads(metadata(url,2*1024*1024)),'fetch_modrinth_artifact':download,
                                        'fabric_loader':lambda record:'0.16.14'})
        patch.start()
        self.addCleanup(patch.stop)

    def data_bytes(self):
        return {str(path.relative_to(self.root)):path.read_bytes() for path in self.root.rglob('*') if path.is_file()}

    def prepare(self):
        before = self.data_bytes()
        runtime['backend_preflight'](self.instance)
        self.assertEqual(self.data_bytes(),before)

    def publish(self):
        forbidden = mock.Mock(side_effect=AssertionError('publication made a provider request'))
        with mock.patch.dict(runtime,{'fetch':forbidden,'fetch_modrinth_artifact':forbidden,'fabric_loader':forbidden}):
            runtime['backend'](self.instance,publishing=True)
        forbidden.assert_not_called()

    def test_fabric_and_quilt_roundtrip_uses_exact_staging(self):
        for software in ('fabric','quilt'):
            with self.subTest(software=software):
                self.record['runtime']['software']=software
                self.record['network_member']=True
                self.save_record()
                self.prepare()
                manifest = json.loads((self.private/'network-staging/manifest.json').read_text())
                self.assertEqual(manifest['context']['software'],software)
                self.assertEqual(manifest['context']['profile'],'fabric-family')
                self.assertEqual(manifest['context']['game'],'1.20.6')
                self.assertEqual(manifest['context']['loader'],'0.16.14')
                self.assertEqual(manifest['artifacts'][runtime['FPL_FILE']]['project_id'],runtime['FPL_PROJECT'])
                self.assertEqual(manifest['artifacts'][runtime['FPL_FILE']]['release_id'],'release1')
                self.publish()
                evidence = json.loads(self.snapshot.read_text())
                self.assertEqual(evidence['profile'],'fabric-family')
                self.assertEqual(set(evidence['files']),set(runtime['backend_paths']('fabric-family')['files']))
                self.assertEqual((self.root/runtime['FPL_FILE']).read_bytes(),self.fpl)
                self.assertEqual((self.root/runtime['API_FILE']).read_bytes(),self.api)
                self.assertIn(b'hackEarlySend = true', (self.root/runtime['FPL_CONFIG']).read_bytes())
                self.publish() # ordinary startup remains offline and idempotent
                self.leave()
                self.assertEqual((self.root/runtime['FPL_CONFIG']).read_bytes(),self.config)
                self.assertFalse((self.root/runtime['FPL_FILE']).exists())
                self.assertFalse((self.root/runtime['API_FILE']).exists())
                self.assert_unowned()

    def test_user_api_is_preserved_and_not_installed_again(self):
        self.write('mods/user-fabric-api.jar',self.api)
        self.prepare()
        self.publish()
        self.assertFalse((self.root/runtime['API_FILE']).exists())
        self.leave()
        self.assertEqual((self.root/'mods/user-fabric-api.jar').read_bytes(),self.api)

    def test_no_external_api_when_bundled(self):
        self.fpl = fabric_jar(nested={'api.jar':self.api}, dependencies={
            'minecraft':'~1.20.6','fabricloader':'>=0.15.0','fabric-api':'>=0.100.0'})
        self.releases[runtime['FPL_PROJECT']] = fabric_release(runtime['FPL_PROJECT'],self.fpl)
        self.prepare()
        self.publish()
        self.assertFalse((self.root/runtime['API_FILE']).exists())

    def test_conflicts_duplicates_and_symlinks_fail_before_data_changes(self):
        cases = ('fpl','duplicate-fpl','api','canonical','symlink','unknown-dependency','config')
        for bad in cases:
            with self.subTest(bad=bad):
                # Each case resets only the files created by the preceding case.
                for name in ('mods/user-fpl.jar','mods/other-fpl.jar','mods/api1.jar','mods/api2.jar',runtime['FPL_FILE']):
                    target = self.root/name
                    if target.is_symlink() or target.exists(): target.unlink()
                self.write(runtime['FPL_CONFIG'],self.config)
                if bad in ('fpl','duplicate-fpl'): self.write('mods/user-fpl.jar',self.fpl)
                if bad=='duplicate-fpl': self.write('mods/other-fpl.jar',self.fpl)
                if bad=='api':
                    self.write('mods/api1.jar',self.api)
                    self.write('mods/api2.jar',self.api)
                if bad=='canonical': self.write(runtime['FPL_FILE'],self.fpl)
                if bad=='symlink': (self.root/runtime['FPL_FILE']).symlink_to(self.root/'mods/user.jar')
                if bad=='config': self.write(runtime['FPL_CONFIG'],b'secret = """ambiguous\nvalue"""\n')
                if bad=='unknown-dependency':
                    self.releases[runtime['FPL_PROJECT']]['dependencies']=[{'dependency_type':'required','project_id':'unknown'}]
                else: self.releases[runtime['FPL_PROJECT']]['dependencies']=[]
                before = self.data_bytes()
                with self.assertRaises(ValueError): runtime['backend_preflight'](self.instance)
                self.assertEqual(self.data_bytes(),before)
                self.assertFalse(self.snapshot.exists())

    def test_modified_staging_and_context_are_rejected_without_publication(self):
        self.prepare()
        stage = self.private/'network-staging'
        manifest = stage/'manifest.json'
        saved = manifest.read_bytes()
        before = self.data_bytes()
        for field in ('profile','software','game','loader','data_path'):
            changed = json.loads(saved)
            changed['context'][field]='changed'
            manifest.write_text(json.dumps(changed))
            with self.assertRaises(ValueError): self.publish()
            self.assertEqual(self.data_bytes(),before)
            self.assertFalse(self.snapshot.exists())
        manifest.write_bytes(saved)
        artifact = stage/Path(runtime['FPL_FILE']).name
        artifact.write_bytes(b'tampered')
        with self.assertRaises(ValueError): self.publish()
        self.assertEqual(self.data_bytes(),before)

    def test_portable_standalone_removes_only_verified_network_artifacts(self):
        self.write('mods/user-fabric-api.jar',self.api)
        self.prepare()
        self.publish()
        evidence = json.loads((self.root/runtime['PORTABLE_NAME']).read_text())
        self.assertEqual(evidence['schema'],2)
        self.assertEqual(evidence['artifacts'][runtime['API_FILE']],None)
        runtime['restore_portable'](str(self.root),os.getuid(),os.getgid(),'fabric-family')
        self.assertEqual((self.root/runtime['FPL_CONFIG']).read_bytes(),self.config)
        self.assertFalse((self.root/runtime['FPL_FILE']).exists())
        self.assertEqual((self.root/'mods/user-fabric-api.jar').read_bytes(),self.api)
        self.assert_unowned()

    def test_failed_partial_publication_restores_original(self):
        self.prepare()
        original = runtime['atomic']
        def fail(path,*args,**kwargs):
            if path==str(self.root/runtime['API_FILE']): raise OSError('interrupted copy')
            return original(path,*args,**kwargs)
        with mock.patch.dict(runtime,{'atomic':fail}), self.assertRaises(OSError): self.publish()
        self.leave()
        self.assertEqual((self.root/runtime['FPL_CONFIG']).read_bytes(),self.config)
        self.assertFalse((self.root/runtime['FPL_FILE']).exists())
        self.assert_unowned()

    def test_changed_mod_inventory_fails_apply_but_normal_start_allows_new_unrelated_mod(self):
        self.prepare()
        self.write('mods/new-user.jar',fabric_jar('another-mod'))
        before = self.data_bytes()
        with self.assertRaises(ValueError): self.publish()
        self.assertEqual(self.data_bytes(),before)
        # New preflight binds this Apply to the new inventory.
        self.prepare()
        self.publish()
        self.write('mods/after-enrollment.jar',fabric_jar('later-mod'))
        forbidden = mock.Mock(side_effect=AssertionError('startup must stay offline'))
        with mock.patch.dict(runtime,{'fetch':forbidden,'fetch_modrinth_artifact':forbidden}):
            runtime['backend'](self.instance)
        self.assertIn(b'later-mod',(self.root/'mods/after-enrollment.jar').read_bytes())

    def test_changed_network_artifact_is_preserved_on_leave_and_portable_restore(self):
        self.prepare()
        self.publish()
        target = self.root/runtime['FPL_FILE']
        target.write_bytes(fabric_jar('unrelated-mod'))
        changed = target.read_bytes()
        with self.assertRaises(ValueError): self.leave()
        self.assertEqual(target.read_bytes(),changed)
        with self.assertRaises(ValueError):
            runtime['restore_portable'](str(self.root),os.getuid(),os.getgid(),'fabric-family')
        self.assertEqual(target.read_bytes(),changed)

    def test_api_is_not_installed_when_not_required(self):
        self.fpl = fabric_jar()
        self.releases[runtime['FPL_PROJECT']] = fabric_release(runtime['FPL_PROJECT'],self.fpl)
        self.prepare()
        self.publish()
        self.assertFalse((self.root/runtime['API_FILE']).exists())

    def test_network_startup_exits_before_standalone_crossplay(self):
        source = (SOURCE.parent/'instance-crossplay-start').read_text()
        network = source.index('minecraft-network-runtime backend')
        standalone = source.index('source /usr/libexec/justvoxel/mjust/managed-crossplay.sh')
        self.assertLess(network,standalone)
        self.assertIn('then exit 0; fi',source[network:standalone])

    def test_originally_absent_config_is_removed_on_standalone_restore(self):
        (self.root/runtime['FPL_CONFIG']).unlink()
        self.prepare()
        self.publish()
        self.assertTrue((self.root/runtime['FPL_CONFIG']).exists())
        runtime['restore_portable'](str(self.root),os.getuid(),os.getgid(),'fabric-family')
        self.assertFalse((self.root/runtime['FPL_CONFIG']).exists())
        self.assertFalse((self.root/runtime['FPL_FILE']).exists())
        self.assertFalse((self.root/runtime['API_FILE']).exists())
        self.assert_unowned()


if __name__ == '__main__':
    unittest.main()
