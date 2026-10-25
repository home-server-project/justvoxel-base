#!/usr/bin/env python3
"""Bounded network configuration recovery tests; no host commands or containers.

This file is intentionally added without executing it during implementation.
"""
import base64
import io
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

    def test_image_rebuild_does_not_discard_valid_six_artifact_evidence(self):
        self.migrate()
        manifest = dict(self.manifest)
        self.writes.clear()
        self.baked['limboapi.jar'] = b'another verified stable image release'
        runtime['prepare']()
        self.assertEqual(json.loads(self.files[self.root+'/artifacts.json']),manifest)
        self.assertEqual(self.writes,[])

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


class LobbySourceContract(unittest.TestCase):
    def test_build_and_plugin_contract(self):
        root = SOURCE.parents[2]
        java = (root/'velocity-lobby/src/main/java/org/homeserverproject/justvoxel/lobby/JustVoxelLobby.java').read_text()
        gradle = (root/'velocity-lobby/build.gradle.kts').read_text()
        build = (root/'build_files/build-minecraft-network-plugins.sh').read_text()
        release_resolver = (root/'build_files/resolve-limboapi-release.py').read_text()
        acquisition = build + release_resolver
        for text in ['id = "justvoxel-lobby"', '@Dependency(id = "limboapi")',
                     'LoginLimboRegisterEvent', 'addOnJoinCallback', 'passLoginLimbo',
                     'PlayerChooseInitialServerEvent', 'PostOrder.FIRST',
                     'ClickEvent.runCommand("/servers " + id)',
                     'SimpleForm.builder().title("JustVoxel Servers")', 'form.button(name)',
                     'serverIds.add(id)', 'form.validResultHandler(response ->',
                     'int index = response.clickedButtonId()',
                     'index >= 0 && index < serverIds.size()', 'select(serverIds.get(index))',
                     'FloodgateApi.getInstance()', 'isFloodgatePlayer(player.getUniqueId())',
                     'sendForm(player.getUniqueId(), form.build())',
                     'import com.velocitypowered.proxy.protocol.packet.PluginMessagePacket;',
                     'void onGeneric(Object packet)', 'packet instanceof PluginMessagePacket message',
                     'message.getChannel().equals("floodgate:form")',
                     'new PluginMessageEvent(player, player,',
                     'MinecraftChannelIdentifier.from("floodgate:form")',
                     'ByteBufUtil.getBytes(message.content())',
                     'event.setResult(PluginMessageEvent.ForwardResult.handled())',
                     'proxy.getEventManager().fireAndForget(event)',
                     'config.servers().containsKey(id)', 'proxy.getServer(id)',
                     'limboPlayer.disconnect(destination.get())',
                     'No servers are currently available.', 'Server is currently unavailable.']:
            self.assertIn(text, java)
        self.assertLess(java.index('config.servers().containsKey(id)'),java.index('proxy.getServer(id)'))
        self.assertNotIn('new LoginEvent',java)
        for forbidden in ['PacketMapping', 'registerPacket(', 'InetSocketAddress', 'LimboHub',
                          'Class.forName', 'java.lang.reflect', 'implements MinecraftPacket']:
            self.assertNotIn(forbidden,java)
        for forbidden in ['shadowJar', 'implementation("net.elytrium']:
            self.assertNotIn(forbidden,gradle)
        self.assertIn('compileOnly(files(limboApiJar))',gradle)
        self.assertNotIn('com.velocitypowered:velocity-proxy', gradle)
        self.assertIn('providers.gradleProperty("velocityVersion")', gradle)
        self.assertIn('providers.gradleProperty("velocityServerJar")', gradle)
        self.assertIn('Missing -PvelocityVersion', gradle)
        self.assertIn('Missing -PvelocityServerJar', gradle)
        self.assertIn('compileOnly("com.velocitypowered:velocity-api:$velocityVersion")', gradle)
        self.assertIn('annotationProcessor("com.velocitypowered:velocity-api:$velocityVersion")', gradle)
        self.assertIn('compileOnly(files(velocityServerJar))', gradle)
        self.assertEqual([line.strip() for line in gradle.splitlines()
                          if 'velocityServerJar' in line and 'files(' in line],
                         ['compileOnly(files(velocityServerJar))'])
        for forbidden in ['implementation(', 'runtimeOnly(', 'from(', 'zipTree(', '4.3.0']:
            self.assertNotIn(forbidden, gradle)
        self.assertIn('compileOnly("org.geysermc.floodgate:api:2.2.5-SNAPSHOT")',gradle)
        self.assertIn('java.specification.version = 25',build)
        self.assertIn('resolve-limboapi-release.py', build)
        self.assertIn('"-PlimboApiJar=${limbo_jar}"', build)
        self.assertIn('limbo_jar="${output}/limboapi.jar"', build)
        self.assertIn('resolve-velocity-release.py" "${velocity_dependencies}"', build)
        self.assertIn('velocity_dependencies="${work}/velocity"', build)
        self.assertIn('work="$(mktemp -d)"', build)
        self.assertIn('--project-cache-dir "${work}/gradle-project-cache"', build)
        self.assertIn('"-PbuildOutputDir=${work}/gradle-build"', build)
        self.assertIn('providers.gradleProperty("buildOutputDir").orNull?.let {\n'
                      '    layout.buildDirectory.set(file(it))\n}', gradle)
        self.assertIn('lobby_jar="$(one_jar "${work}/gradle-build/libs")"', build)
        self.assertNotIn('velocity-lobby/build/libs', build)
        self.assertNotIn('velocity-lobby/.gradle', build)
        self.assertIn("trap 'rm -rf -- \"${work}\"' EXIT", build)
        self.assertIn("print(release['version'])", build)
        self.assertIn('"-PvelocityVersion=${velocity_version}"', build)
        self.assertIn('"-PvelocityServerJar=${velocity_server_jar}"', build)
        self.assertIn('Velocity compile API: %s build %s STABLE', build)
        self.assertIn('com/velocitypowered/', build)
        for line in build.splitlines():
            if 'install ' in line or 'sha256sum limboapi.jar' in line:
                self.assertNotIn('velocity', line.lower())
        for runtime_source in [SOURCE, root/'build_files/build-common.sh']:
            for forbidden in ['velocity-release.json', 'velocity-server.jar', 'velocityServerJar']:
                self.assertNotIn(forbidden, runtime_source.read_text())
        self.assertIn('sha256sum limboapi.jar justvoxel-lobby.jar > SHA256SUMS', build)
        self.assertIn('org/geysermc/', build)
        for text in ['Elytrium/LimboAPI', '390180560', '568567016', 'bfef579',
                     'limboapi-1.1.27-SNAPSHOT.jar', '3339167',
                     '5d541984b90ca7a88eb49ca5f8cc3a803f5ecdfa6d7ed30b605b72ff94156e40',
                     'https://api.github.com/repos/Elytrium/LimboAPI/releases/assets/568567016',
                     'application/octet-stream', 'hashlib.sha256()', 'limboapi-release.json']:
            self.assertIn(text, acquisition)
        for forbidden in ['modrinth', 'version_type', 'select_release', 'sha512',
                          '/releases/latest', '/releases/tags/', '1.1.26']:
            self.assertNotIn(forbidden, release_resolver)
        self.assertNotIn('MINIMUM_VERSION', acquisition)
        self.assertNotIn('check_compatibility', acquisition)
        self.assertLess(build.index('command -v java'), build.index('java -XshowSettings'))
        self.assertIn('if ! java_properties=', build)
        for forbidden in ['git clone', 'checkout --detach', ':plugin:shadowJar', ':api:jar',
                          'maven.elytrium.net', 'FastPrepareAPI', 'java-commons']:
            self.assertNotIn(forbidden, acquisition + gradle)
        import re
        implementation_imports = []
        for path in (root/'velocity-lobby/src/main/java').rglob('*.java'):
            source = path.read_text()
            implementation_imports.extend(re.findall(r'^import (com\.velocitypowered\.proxy\.[^;]+);',
                                                      source, re.MULTILINE))
            for forbidden in ['package com.velocitypowered', 'implements MinecraftPacket',
                              'PacketMapping', 'registerPacket(']:
                self.assertNotIn(forbidden, source)
        self.assertEqual(implementation_imports,
                         ['com.velocitypowered.proxy.protocol.packet.PluginMessagePacket'])
        for path in [root/'velocity-lobby/build.gradle.kts',
                     root/'build_files/build-minecraft-network-plugins.sh',
                     SOURCE,
                     root/'.github/workflows/build-testing.yml', root/'.github/workflows/build.yml']:
            text = path.read_text()
            self.assertNotRegex(text, r'(?i)limbo[^\n]*(?:[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})')
        for jar in ['limboapi.jar','justvoxel-lobby.jar']:
            self.assertIn(jar,build)
            self.assertIn(jar,SOURCE.read_text())
        for name in ['build-testing.yml','build.yml']:
            workflow = (root/'.github/workflows'/name).read_text()
            self.assertLess(workflow.index('uses: actions/setup-java@'),
                            workflow.index('./build_files/build-minecraft-network-plugins.sh'))
            self.assertIn("java-version: '25'", workflow)


RESOLVER_SOURCE = SOURCE.parents[2] / 'build_files/resolve-limboapi-release.py'
resolver = {'__name__': 'limbo_release_source_test'}
exec(compile(RESOLVER_SOURCE.read_text(), str(RESOLVER_SOURCE), 'exec'), resolver)


class LimboReleaseAcquisition(unittest.TestCase):
    def test_missing_output_directory_reports_usage_without_download_or_traceback(self):
        namespace = {'__name__': '__main__'}
        stderr = io.StringIO()
        with mock.patch('sys.argv', [str(RESOLVER_SOURCE)]), \
                mock.patch('sys.stderr', stderr), \
                mock.patch('urllib.request.urlopen') as opener:
            with self.assertRaises(SystemExit) as failure:
                exec(compile(RESOLVER_SOURCE.read_text(), str(RESOLVER_SOURCE), 'exec'), namespace)
        self.assertNotEqual(failure.exception.code, 0)
        self.assertIn('usage:', stderr.getvalue())
        self.assertIn('OUTPUT_DIRECTORY', stderr.getvalue())
        self.assertNotIn('Traceback', stderr.getvalue())
        self.assertLessEqual(len(stderr.getvalue().splitlines()), 3)
        opener.assert_not_called()

    PIN = {
        'source': 'github-release-asset',
        'repository': 'Elytrium/LimboAPI',
        'release_id': 390180560,
        'release_tag': 'dev-build',
        'release_name': 'Dev-build bfef579',
        'source_revision': 'bfef579',
        'prerelease': True,
        'published_at': '2026-09-16T18:44:41Z',
        'asset_id': 568567016,
        'filename': 'limboapi-1.1.27-SNAPSHOT.jar',
        'size': 3339167,
        'sha256': '5d541984b90ca7a88eb49ca5f8cc3a803f5ecdfa6d7ed30b605b72ff94156e40',
        'compatibility': 'Minecraft 26.3 / Velocity protocol 777',
    }
    ASSET_URL = 'https://api.github.com/repos/Elytrium/LimboAPI/releases/assets/568567016'

    def test_exact_official_compatibility_pin(self):
        for constant, field in [('REPOSITORY', 'repository'), ('RELEASE_ID', 'release_id'),
                                ('RELEASE_TAG', 'release_tag'), ('RELEASE_NAME', 'release_name'),
                                ('SOURCE_REVISION', 'source_revision'), ('PUBLISHED_AT', 'published_at'),
                                ('ASSET_ID', 'asset_id'), ('UPSTREAM_FILENAME', 'filename'),
                                ('EXPECTED_SIZE', 'size'), ('EXPECTED_SHA256', 'sha256'),
                                ('COMPATIBILITY', 'compatibility')]:
            self.assertEqual(resolver[constant], self.PIN[field])
        self.assertEqual(resolver['ASSET_URL'], self.ASSET_URL)

    def response(self, data, url=None):
        response = io.BytesIO(data)
        response.geturl = mock.Mock(return_value=url or self.ASSET_URL)
        return response

    def assert_request(self, opener):
        opener.assert_called_once()
        args, kwargs = opener.call_args
        self.assertEqual(len(args), 1)
        request = args[0]
        self.assertEqual(request.full_url, self.ASSET_URL)
        self.assertEqual(request.get_method(), 'GET')
        self.assertEqual(request.get_header('Accept'), 'application/octet-stream')
        self.assertIn('JustVoxel', request.get_header('User-agent'))
        self.assertEqual(kwargs, {'timeout': 60})

    def test_verified_output_and_exact_provenance_acquired_each_time(self):
        # Synthetic bytes cannot match the upstream digest; mock only its result.
        # Keep the real pin and exact size, and check every byte fed to the hash.
        data = b'J' * self.PIN['size']
        for host in ['api.github.com', 'release-assets.githubusercontent.com',
                     'objects.githubusercontent.com']:
            with self.subTest(host=host), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                for _ in range(2):
                    digest = mock.Mock()
                    digest.hexdigest.return_value = self.PIN['sha256']
                    with mock.patch('urllib.request.urlopen', return_value=self.response(
                            data, f'https://{host}/asset?signature=example')) as opener, \
                            mock.patch.object(resolver['hashlib'], 'sha256', return_value=digest):
                        resolver['acquire'](root)
                    self.assert_request(opener)
                    self.assertEqual(b''.join(call.args[0] for call in digest.update.call_args_list), data)
                    self.assertGreater(digest.update.call_count, 1)
                    self.assertEqual((root/'limboapi.jar').read_bytes(), data)
                    self.assertEqual(json.loads((root/'limboapi-release.json').read_text()), self.PIN)
                    self.assertFalse((root/'limboapi.jar.part').exists())

    def test_untrusted_final_urls_fail_before_reading_bytes(self):
        for url in ['http://release-assets.githubusercontent.com/asset',
                    'https://evil.example/asset',
                    'https://release-assets.githubusercontent.com.evil.example/asset',
                    'https://api.github.com@evil.example/asset',
                    'https://user@api.github.com/asset',
                    'https://api.github.com:443/asset']:
            with self.subTest(url=url), tempfile.TemporaryDirectory() as directory:
                response = self.response(b'untrusted', url)
                response.read = mock.Mock(side_effect=AssertionError('must validate URL first'))
                with mock.patch('urllib.request.urlopen', return_value=response) as opener:
                    with self.assertRaisesRegex(ValueError, 'official GitHub HTTPS host'):
                        resolver['acquire'](directory)
                self.assert_request(opener)
                response.read.assert_not_called()
                for name in ['limboapi.jar', 'limboapi.jar.part', 'limboapi-release.json']:
                    self.assertFalse((Path(directory)/name).exists())

    def test_integrity_failure_never_replaces_verified_artifact_or_provenance(self):
        for data, error in [(b'short', 'size mismatch'),
                            (b'J' * (self.PIN['size'] + 1), 'size mismatch'),
                            (b'J' * self.PIN['size'], 'SHA-256 mismatch')]:
            with self.subTest(error=error), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root/'limboapi.jar').write_bytes(b'previous verified jar')
                (root/'limboapi-release.json').write_text('previous verified provenance')
                (root/'SHA256SUMS').write_text('previous build')
                def open_response(request, **kwargs):
                    self.assertFalse((root/'SHA256SUMS').exists())
                    return self.response(data)
                with mock.patch('urllib.request.urlopen', side_effect=open_response) as opener:
                    with self.assertRaisesRegex(ValueError, error):
                        resolver['acquire'](root)
                self.assert_request(opener)
                self.assertEqual((root/'limboapi.jar').read_bytes(), b'previous verified jar')
                self.assertEqual((root/'limboapi-release.json').read_text(), 'previous verified provenance')
                self.assertFalse((root/'SHA256SUMS').exists())
                self.assertFalse((root/'limboapi.jar.part').exists())

    def test_network_failure_invalidates_composition_without_fallback(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'SHA256SUMS').write_text('previous build')
            def fail(request, **kwargs):
                self.assertFalse((root/'SHA256SUMS').exists())
                raise OSError('asset unavailable')
            with mock.patch('urllib.request.urlopen', side_effect=fail) as opener:
                with self.assertRaisesRegex(OSError, 'asset unavailable'):
                    resolver['acquire'](root)
            self.assert_request(opener)
            for name in ['SHA256SUMS', 'limboapi.jar', 'limboapi.jar.part', 'limboapi-release.json']:
                self.assertFalse((root/name).exists())

    def test_no_dynamic_release_selection_or_old_stable_fallback(self):
        source = RESOLVER_SOURCE.read_text()
        for forbidden in ['modrinth', 'select_release', 'version_type', '1.1.26',
                          '/releases/latest', '/releases/tags/', 'VERSIONS_URL']:
            self.assertNotIn(forbidden, source)
        self.assertEqual(source.count('urllib.request.urlopen('), 1)
        self.assertIn('urllib.request.Request(ASSET_URL', source)


if __name__ == '__main__':
    unittest.main()
