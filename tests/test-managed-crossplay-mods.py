#!/usr/bin/env python3
"""Offline publisher/artifact fixtures; no real downloads or appliance commands."""
import copy
import hashlib
import io
import json
import pathlib
import sys
import tempfile
import unittest
import zipfile
import urllib.parse
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
        with patch.object(sys, 'argv', ['helper', 'fabric', '26.3', '0.20.1', self.temp.name]):
            exec(compile(definitions, 'managed-crossplay-mods', 'exec'), self.ns)
        self.builds = {'geyser':123}
        self.floodgate_override = None
        self.artifacts = {}
        for project, identity in [('geyser', 'geyser-fabric'), ('floodgate', 'floodgate'), ('fabric-api', 'fabric-api')]:
            self.artifacts[project] = jar('fabric.mod.json', json.dumps({'id':identity, 'depends':{'minecraft':'~26.3', 'fabricloader':'>=0.19.5'}}))
        self.releases = [self.api_release('26.3', 'exact'), self.api_release('26.2', 'wrong-but-newer')]
        self.ns['metadata'] = self.metadata
        self.ns['fetch'] = self.fetch

    def api_release(self, game, identity):
        return {'project_id':'P7dR8mSH', 'id':identity, 'version_type':'release', 'game_versions':[game], 'loaders':['fabric'], 'date_published':'2026-10-22' if game == '26.3' else '2026-10-23', 'files':[{'primary':True, 'url':f'https://cdn.modrinth.com/data/P7dR8mSH/versions/{identity}/fabric-api.jar', 'hashes':{'sha512':hashlib.sha512(self.artifacts['fabric-api']).hexdigest()}}]}

    def floodgate_release(self, identity='AbCd1234', date='2026-10-22'):
        platform = self.ns['platform']
        name = 'Fabric' if platform == 'fabric' else 'Neoforge'
        filename = f'Floodgate-{name}-2.2.7-b70.jar'
        return {'project_id':'bWrNNfkb', 'id':identity, 'version_type':'release', 'status':'listed',
                'version_number':'2.2.7-b70', 'game_versions':['26.3'], 'loaders':[platform],
                'date_published':date, 'files':[{'primary':True, 'filename':filename,
                'url':f'https://cdn.modrinth.com/data/bWrNNfkb/versions/{identity}/{filename}',
                'hashes':{'sha512':hashlib.sha512(self.artifacts['floodgate']).hexdigest()}}]}

    def metadata(self, url):
        if url == self.ns['VERSION_URL']:
            return {'java':{'supported':'26.3'}}
        if '/bWrNNfkb/version?' in url:
            query = urllib.parse.parse_qs(urllib.parse.urlsplit(url).query)
            self.assertEqual(query, {'loaders':[json.dumps([self.ns['platform']])], 'game_versions':['["26.3"]'], 'include_changelog':['false']})
            return self.floodgate_override if self.floodgate_override is not None else [self.floodgate_release()]
        if '/P7dR8mSH/version?' in url:
            self.assertIn('26.3', url)
            return self.releases
        for project in ['geyser']:
            if f'/projects/{project}/' in url:
                return {'project_id':project, 'version':'2.12.0', 'build':self.builds[project], 'channel':'default', 'downloads':{self.ns['platform']:{'sha256':hashlib.sha256(self.artifacts[project]).hexdigest()}}}
        self.fail('Unexpected metadata endpoint: ' + url)

    def fetch(self, url, limit):
        for project in ['geyser']:
            if f'/projects/{project}/' in url:
                self.assertEqual(url, self.ns['BASE'] + project + '/versions/2.12.0/builds/' + str(self.builds[project]) + '/downloads/' + self.ns['platform'])
                return self.artifacts[project]
        if '/data/bWrNNfkb/' in url:
            releases = self.floodgate_override if self.floodgate_override is not None else [self.floodgate_release()]
            self.assertIn(url, [f['url'] for r in releases for f in r['files']])
            self.assertEqual(limit, self.ns['MAX_JAR'])
            return self.artifacts['floodgate']
        self.assertIn('/versions/exact/', url)
        return self.artifacts['fabric-api']

    def test_fabric_current_artifacts_and_exact_api(self):
        self.ns['prepare']()
        for project in ['geyser', 'floodgate', 'fabric-api']:
            self.assertEqual((pathlib.Path(self.temp.name) / (project + '.jar')).read_bytes(), self.artifacts[project])
        metadata = json.loads((pathlib.Path(self.temp.name) / 'fabric-api.json').read_text())
        self.assertEqual(metadata['version'], 'exact')

    def use_neoforge(self):
        self.ns['platform'], self.ns['loader'] = 'neoforge', '26.3.0.101'
        for project, identity in [('geyser', 'geyser_neoforge'), ('floodgate', 'floodgate')]:
            self.artifacts[project] = jar('META-INF/neoforge.mods.toml', f'[[mods]]\nmodId="{identity}"\n[[dependencies.{identity}]]\nmodId="minecraft"\nversionRange="[26.3,26.4)"\n[[dependencies.{identity}]]\nmodId="neoforge"\nversionRange="[26.3.0.0,26.4.0.0)"\n')

    def test_geyser_requires_positive_integer_build(self):
        for build in [0, -1, '1', 1.0, None, True, False]:
            with self.subTest(build=build):
                self.builds['geyser'] = build
                with self.assertRaisesRegex(ValueError, 'Invalid official build identity'):
                    self.ns['prepare']()

    def test_floodgate_verified_metadata_and_primary_fallback(self):
        for platform in ['fabric', 'neoforge']:
            if platform == 'neoforge':
                self.use_neoforge()
            for primary in [True, False, None]:
                with self.subTest(platform=platform, primary=primary), tempfile.TemporaryDirectory() as staging:
                    self.ns['staging'] = staging
                    release = self.floodgate_release()
                    if primary is None:
                        del release['files'][0]['primary']
                    else:
                        release['files'][0]['primary'] = primary
                    self.floodgate_override = [release]
                    self.ns['prepare']()
                    result = json.loads((pathlib.Path(staging) / 'floodgate.json').read_text())
                    self.assertEqual(result, {'project':'floodgate', 'publisher':'modrinth', 'project_id':'bWrNNfkb',
                        'version':release['version_number'], 'version_id':release['id'],
                        'sha256':hashlib.sha256(self.artifacts['floodgate']).hexdigest(),
                        'sha512':release['files'][0]['hashes']['sha512'], 'platform':platform,
                        'game':'26.3', 'loader':self.ns['loader']})
                    self.assertEqual((pathlib.Path(staging) / 'floodgate.jar').read_bytes(), self.artifacts['floodgate'])
                    self.assertEqual((pathlib.Path(staging) / 'fabric-api.jar').exists(), platform == 'fabric')

    def test_floodgate_metadata_and_file_rejections(self):
        for platform in ['fabric', 'neoforge']:
            if platform == 'neoforge':
                self.use_neoforge()
            valid = self.floodgate_release()
            cases = []
            for key, value in [('project_id','other'), ('version_type','beta'), ('version_type','alpha'),
                    ('game_versions',['26.2']), ('loaders',['forge']), ('game_versions','26.3'),
                    ('loaders',platform), ('date_published',None), ('id','../bad'), ('id','A'*65),
                    ('version_number',''), ('version_number',' '*3), ('version_number','a'*129)]:
                bad = copy.deepcopy(valid)
                bad[key] = value
                cases.append((key + repr(value), [bad]))
            for status in ['draft', 'scheduled', 'unknown', 'unlisted', 'archived', None]:
                bad = copy.deepcopy(valid)
                bad['status'] = status
                cases.append(('status ' + str(status), [bad]))
            address = valid['files'][0]['url']
            for url in [address.replace('cdn.modrinth.com', 'evil.example'),
                    address.replace('/bWrNNfkb/', '/Other123/'), address.replace('/AbCd1234/', '/Wrong123/'),
                    address + '?download=1', address + '#fragment', address + '?', address + '#',
                    address.replace('https:', 'http:'), address + '.jar']:
                bad = copy.deepcopy(valid)
                bad['files'][0]['url'] = url
                cases.append(('url ' + url, [bad]))
            for digest in [None, '', 'a'*127, 'A'*128, 'g'*128, '0'*128, 123]:
                bad = copy.deepcopy(valid)
                bad['files'][0]['hashes'] = {} if digest is None else {'sha512':digest}
                cases.append(('hash ' + str(digest), [bad]))
            for filename in ['../Floodgate-Fabric-b70.jar', 'Floodgate-Fabric-../b70.jar',
                    'Floodgate-Fabric-..b70.jar', 'Floodgate-Fabric-' + 'a'*256 + '.jar',
                    'Floodgate-' + ('Neoforge' if platform == 'fabric' else 'Fabric') + '-b70.jar']:
                bad = copy.deepcopy(valid)
                bad['files'][0]['filename'] = filename
                cases.append(('filename ' + filename, [bad]))
            for primary in [True, False]:
                bad = copy.deepcopy(valid)
                bad['files'][0]['primary'] = primary
                bad['files'].append(copy.deepcopy(bad['files'][0]))
                cases.append(('ambiguous primary ' + str(primary), [bad]))
            cases.extend([('not list', {}), ('too many', [valid]*1001), ('not object', [None])])
            for label, response in cases:
                with self.subTest(platform=platform, case=label), tempfile.TemporaryDirectory() as staging:
                    self.ns['staging'] = staging
                    self.floodgate_override = response
                    with self.assertRaises(ValueError):
                        self.ns['prepare']()
                    self.assertFalse((pathlib.Path(staging) / 'floodgate.json').exists())
                    self.assertFalse((pathlib.Path(staging) / 'floodgate.jar').exists())

    def test_floodgate_newest_exact_release_is_deterministic(self):
        older = self.floodgate_release('ZzYy1234', '2026-10-21')
        first = self.floodgate_release('AaBb1234')
        newest = self.floodgate_release('ZzYy1234')
        wrong = self.floodgate_release('Wrong123', '2026-10-23')
        wrong['game_versions'] = ['26.2']
        beta = self.floodgate_release('Beta1234', '2026-10-24')
        beta['version_type'] = 'beta'
        for releases in [[older, newest, first, wrong, beta], [beta, wrong, first, newest, older]]:
            with tempfile.TemporaryDirectory() as staging:
                self.ns['staging'] = staging
                self.floodgate_override = releases
                self.ns['prepare']()
                result = json.loads((pathlib.Path(staging) / 'floodgate.json').read_text())
                self.assertEqual(result['version_id'], newest['id'])

    def test_floodgate_fabric_descriptor_rejections(self):
        for identity, game, loader in [('other','26.3','>=0.19.5'), ('floodgate','26.2','>=0.19.5'), ('floodgate','26.3','>=0.21')]:
            with self.subTest(identity=identity, game=game, loader=loader), tempfile.TemporaryDirectory() as staging:
                self.ns['staging'] = staging
                self.artifacts['floodgate'] = jar('fabric.mod.json', json.dumps({'id':identity, 'depends':{'minecraft':game, 'fabricloader':loader}}))
                with self.assertRaises(ValueError):
                    self.ns['prepare']()
                self.assertFalse((pathlib.Path(staging) / 'floodgate.json').exists())

    def test_fabric_trailing_prerelease_bound_descriptors(self):
        for project, identity in [('geyser', 'geyser-fabric'), ('floodgate', 'floodgate'), ('fabric-api', 'fabric-api')]:
            self.artifacts[project] = jar('fabric.mod.json', json.dumps({'id':identity, 'depends':{'minecraft':'~26.3-', 'fabricloader':'>=0.19.5', 'fabric-api':'*', 'java':'>=25'}}))
        self.releases = [self.api_release('26.3', 'exact')]
        self.ns['prepare']()
        for project in self.artifacts:
            path = pathlib.Path(self.temp.name) / (project + '.jar')
            for game in ['26.2', '26.4']:
                with self.subTest(project=project, game=game):
                    self.ns['game'] = game
                    with self.assertRaises(ValueError):
                        self.ns['verify_mod'](path, project)

    def test_fabric_narrow_trailing_prerelease_bound(self):
        for value in ['26.3', '26.3-alpha', '26.3-alpha.1', '26.3-beta-2', '26.3-rc.1', '26.3.1']:
            with self.subTest(value=value):
                self.assertTrue(self.ns['accepts']('~26.3-', value))
        for value in ['26.2', '26.2-rc.1', '26.4', '26.4-alpha']:
            self.assertFalse(self.ns['accepts']('~26.3-', value))
        for rule in ['~26-', '~26.3--', '~26.3-*', '~26.3- || 26.4']:
            self.assertFalse(self.ns['accepts'](rule, '26.3'))
        for rule in ['26.3', '=26.3', '~26.3', '[26.3,26.4)', '>=26.3 <26.4']:
            self.assertTrue(self.ns['accepts'](rule, '26.3'))

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
        self.ns['platform'], self.ns['loader'] = 'neoforge', '26.3.0.101'
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
        self.ns['platform'], self.ns['loader'] = 'neoforge', '26.3.0.101'
        for project, identity in [('geyser', 'geyser_neoforge'), ('floodgate', 'floodgate')]:
            self.artifacts[project] = jar('META-INF/neoforge.mods.toml', f'[[mods]]\nmodId="{identity}"\n[[dependencies.{identity}]]\nmodId="minecraft"\nversionRange="[26.3,26.4)"\n[[dependencies.{identity}]]\nmodId="neoforge"\nversionRange="[26.3.0.0,26.4.0.0)"\n')
        self.ns['prepare']()
        self.assertFalse((pathlib.Path(self.temp.name) / 'fabric-api.jar').exists())
        for project in ['geyser', 'floodgate']:
            result = json.loads((pathlib.Path(self.temp.name) / (project + '.json')).read_text())
            self.assertEqual(result['platform'], 'neoforge')

    def test_official_neoforge_open_and_prerelease_ranges(self):
        self.assertTrue(self.ns['accepts']('[26.2.0.0-beta,)', '26.3.0.101'))
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
