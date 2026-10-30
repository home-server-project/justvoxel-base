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
