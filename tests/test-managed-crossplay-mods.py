#!/usr/bin/env python3
"""Offline publisher/artifact fixtures; no real downloads or appliance commands."""
import hashlib
import io
import json
import pathlib
import sys
import tempfile
import unittest
import zipfile
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[1]


def jar(descriptor, content):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, 'w') as archive:
        archive.writestr(descriptor, content)
    return stream.getvalue()


class ManagedModPreparation(unittest.TestCase):
    def setUp(self):
        source = (ROOT / 'mjust/libexec/managed-crossplay-mods').read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        definitions = source.split('try:\n    prepare()', 1)[0]
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.ns = {}
        with patch.object(sys, 'argv', ['helper', 'fabric', '26.3', '0.19.5', self.temp.name]):
            exec(compile(definitions, 'managed-crossplay-mods', 'exec'), self.ns)
        self.artifacts = {}
        for project, identity in [('geyser', 'geyser-fabric'), ('floodgate', 'floodgate'), ('fabric-api', 'fabric-api')]:
            self.artifacts[project] = jar('fabric.mod.json', json.dumps({'id':identity, 'depends':{'minecraft':'~26.3', 'fabricloader':'>=0.19.5'}}))
        self.releases = [self.api_release('26.3', 'exact'), self.api_release('26.2', 'wrong-but-newer')]
        self.ns['metadata'] = self.metadata
        self.ns['fetch'] = self.fetch

    def api_release(self, game, identity):
        return {'project_id':'P7dR8mSH', 'id':identity, 'version_type':'release', 'game_versions':[game], 'loaders':['fabric'], 'date_published':'2026-10-22' if game == '26.3' else '2026-10-23', 'files':[{'primary':True, 'url':f'https://cdn.modrinth.com/data/P7dR8mSH/versions/{identity}/fabric-api.jar', 'hashes':{'sha512':hashlib.sha512(self.artifacts['fabric-api']).hexdigest()}}]}

    def metadata(self, url):
        if url == self.ns['VERSION_URL']:
            return {'java':{'supported':'26.3'}}
        if '/P7dR8mSH/version?' in url:
            self.assertIn('26.3', url)
            return self.releases
        for project in ['geyser', 'floodgate']:
            if f'/projects/{project}/' in url:
                return {'project_id':project, 'version':'2.12.0', 'build':123, 'channel':'default', 'downloads':{self.ns['platform']:{'sha256':hashlib.sha256(self.artifacts[project]).hexdigest()}}}
        self.fail('Unexpected metadata endpoint: ' + url)

    def fetch(self, url, limit):
        for project in ['geyser', 'floodgate']:
            if f'/projects/{project}/' in url:
                self.assertEqual(url, self.ns['BASE'] + project + '/versions/2.12.0/builds/123/downloads/' + self.ns['platform'])
                return self.artifacts[project]
        self.assertIn('/versions/exact/', url)
        return self.artifacts['fabric-api']

    def test_fabric_current_artifacts_and_exact_api(self):
        self.ns['prepare']()
        for project in ['geyser', 'floodgate', 'fabric-api']:
            self.assertEqual((pathlib.Path(self.temp.name) / (project + '.jar')).read_bytes(), self.artifacts[project])
        metadata = json.loads((pathlib.Path(self.temp.name) / 'fabric-api.json').read_text())
        self.assertEqual(metadata['version'], 'exact')

    def test_fabric_api_never_guesses_other_game_release(self):
        self.releases = [self.api_release('26.2', 'wrong-but-newer')]
        with self.assertRaises(ValueError):
            self.ns['prepare']()
        self.assertFalse((pathlib.Path(self.temp.name) / 'fabric-api.jar').exists())

    def test_unproven_game_and_loader_fail_closed(self):
        self.ns['game'] = '26.2'
        with self.assertRaises(ValueError):
            self.ns['prepare']()
        self.ns['game'], self.ns['loader'] = '26.3', '0.16.14'
        with self.assertRaises(ValueError):
            self.ns['prepare']()

    def test_hash_failure_never_creates_replacement(self):
        original = self.metadata
        def wrong_hash(url):
            result = original(url)
            if '/projects/geyser/' in url:
                result['downloads']['fabric']['sha256'] = '0' * 64
            return result
        self.ns['metadata'] = wrong_hash
        with self.assertRaises(ValueError):
            self.ns['prepare']()
        self.assertFalse((pathlib.Path(self.temp.name) / 'geyser.jar').exists())

    def test_missing_exact_platform_never_guesses_an_artifact(self):
        original = self.metadata
        def wrong_platform(url):
            result = original(url)
            if '/projects/geyser/' in url:
                result['downloads'] = {'spigot': result['downloads']['fabric']}
            return result
        self.ns['metadata'] = wrong_platform
        with self.assertRaises(ValueError):
            self.ns['prepare']()
        self.assertFalse((pathlib.Path(self.temp.name) / 'geyser.jar').exists())

    def test_neoforge_game_loader_and_platform_fail_closed(self):
        self.ns['platform'], self.ns['loader'] = 'neoforge', '26.3.0.5'
        for project, identity in [('geyser', 'geyser_neoforge'), ('floodgate', 'floodgate')]:
            for game, loader, descriptor in [
                ('[26.2,26.3)', '[26.3.0.0,26.4.0.0)', 'META-INF/neoforge.mods.toml'),
                ('[26.3,26.4)', '[26.2.0.0,26.3.0.0)', 'META-INF/neoforge.mods.toml'),
                ('[26.3,26.4)', '[26.3.0.0,26.4.0.0)', 'fabric.mod.json'),
            ]:
                with self.subTest(project=project, game=game, loader=loader, descriptor=descriptor):
                    path = pathlib.Path(self.temp.name) / 'incompatible.jar'
                    path.write_bytes(jar(descriptor, f'[[mods]]\nmodId="{identity}"\n[[dependencies.{identity}]]\nmodId="minecraft"\nversionRange="{game}"\n[[dependencies.{identity}]]\nmodId="neoforge"\nversionRange="{loader}"\n'))
                    with self.assertRaises(ValueError):
                        self.ns['verify_mod'](path, project)

    def test_neoforge_platform_and_dependencies(self):
        self.ns['platform'], self.ns['loader'] = 'neoforge', '26.3.0.5'
        for project, identity in [('geyser', 'geyser_neoforge'), ('floodgate', 'floodgate')]:
            self.artifacts[project] = jar('META-INF/neoforge.mods.toml', f'[[mods]]\nmodId="{identity}"\n[[dependencies.{identity}]]\nmodId="minecraft"\nversionRange="[26.3,26.4)"\n[[dependencies.{identity}]]\nmodId="neoforge"\nversionRange="[26.3.0.0,26.4.0.0)"\n')
        self.ns['prepare']()
        self.assertFalse((pathlib.Path(self.temp.name) / 'fabric-api.jar').exists())
        for project in ['geyser', 'floodgate']:
            result = json.loads((pathlib.Path(self.temp.name) / (project + '.json')).read_text())
            self.assertEqual(result['platform'], 'neoforge')

    def test_official_neoforge_open_and_prerelease_ranges(self):
        self.assertTrue(self.ns['accepts']('[26.2.0.0-beta,)', '26.3.0.5'))
        self.assertFalse(self.ns['accepts']('[26.3.0.0,26.4.0.0)', '26.2.0.5'))
        self.assertTrue(self.ns['accepts']('>=25', '25.0'))

    def test_fabric_api_hash_failure_preserves_replacement_boundary(self):
        self.releases[0]['files'][0]['hashes']['sha512'] = '0' * 128
        with self.assertRaises(ValueError):
            self.ns['prepare']()
        self.assertFalse((pathlib.Path(self.temp.name) / 'fabric-api.jar').exists())

    def test_unknown_dependency_language_fails_closed(self):
        for rule in ['*', 'latest', '>=0.1 || anything', None, {'version':'26.3'}]:
            self.assertFalse(self.ns['accepts'](rule, '26.3'))


if __name__ == '__main__':
    unittest.main()
