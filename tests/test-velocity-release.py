#!/usr/bin/env python3
"""Build-only Velocity acquisition contracts; no network or Java required."""
import copy
import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SOURCE = Path(__file__).resolve().parents[1] / 'build_files/resolve-velocity-release.py'
resolver = {'__name__': 'velocity_release_source_test'}
exec(compile(SOURCE.read_text(), str(SOURCE), 'exec'), resolver)


class VelocityReleaseAcquisition(unittest.TestCase):
    def setUp(self):
        self.data = b'verified Velocity server artifact'
        self.digest = hashlib.sha256(self.data).hexdigest()
        self.artifact = {
            'name': 'velocity-4.2.0-20.jar', 'size': len(self.data),
            'checksums': {'sha256': self.digest},
            'url': f'https://fill-data.papermc.io/v1/objects/{self.digest}/velocity-4.2.0-20.jar',
        }
        self.build = {'id': 20, 'channel': 'STABLE',
                      'downloads': {'server:default': self.artifact}}
        self.project = {'project': {'id': 'velocity'},
                        'versions': {'4.0.0': ['4.2.1-SNAPSHOT', '4.2.0'],
                                     '3.0.0': ['3.5.1']}}
        self.calls = []

    def metadata(self, url):
        self.calls.append(url)
        if url == resolver['PROJECT_URL']:
            return self.project
        if url.endswith('/4.2.1-SNAPSHOT/builds'):
            return [{'id': 30, 'channel': 'EXPERIMENTAL'}]
        if url.endswith('/4.2.0/builds'):
            return [{'id': 22, 'channel': 'BETA'}, self.build,
                    dict(self.build, id=19)]
        self.fail(f'Unexpected metadata request: {url}')

    def test_official_order_skips_unstable_and_uses_first_stable_build(self):
        with mock.patch.dict(resolver, read_json=self.metadata):
            metadata, artifact = resolver['select_release']()
        self.assertEqual(metadata, dict(project='velocity', version='4.2.0', build=20,
                                        channel='STABLE', sha256=self.digest,
                                        filename=self.artifact['name']))
        self.assertEqual(artifact, self.artifact)
        self.assertEqual(len(self.calls), 3)

    def test_no_stable_build_fails_without_fallback(self):
        def metadata(url):
            return self.project if url == resolver['PROJECT_URL'] else []
        with mock.patch.dict(resolver, read_json=metadata):
            with self.assertRaisesRegex(ValueError, 'No stable'):
                resolver['select_release']()

    def test_invalid_artifact_metadata_fails_closed(self):
        for field, value in [
                ('url', self.artifact['url'].replace('https:', 'http:')),
                ('url', self.artifact['url'].replace('fill-data.papermc.io', 'example.com')),
                ('url', self.artifact['url'].replace('fill-data.papermc.io', 'user@fill-data.papermc.io')),
                ('name', '../velocity.jar'), ('checksums', {}),
                ('checksums', {'sha256': 'x' * 64}), ('size', 0)]:
            with self.subTest(field=field, value=value):
                broken = copy.deepcopy(self.build)
                broken['downloads']['server:default'][field] = value
                with mock.patch.object(self, 'build', broken), \
                        mock.patch.dict(resolver, read_json=self.metadata):
                    with self.assertRaises(ValueError):
                        resolver['select_release']()

    def test_verified_jar_and_provenance_only(self):
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.dict(resolver, read_json=self.metadata,
                                open_url=lambda url: io.BytesIO(self.data)):
            resolver['acquire'](directory)
            output = Path(directory)
            self.assertEqual((output/self.artifact['name']).read_bytes(), self.data)
            metadata = json.loads((output/'velocity-release.json').read_text())
            self.assertEqual(metadata['sha256'], self.digest)
            self.assertEqual(metadata['version'], '4.2.0')
            self.assertEqual(set(p.name for p in output.iterdir()),
                             {self.artifact['name'], 'velocity-release.json'})

    def test_bad_hash_or_size_never_publishes(self):
        for data in [b'x' * len(self.data), self.data[:-1], self.data + b'x']:
            with self.subTest(data=data), tempfile.TemporaryDirectory() as directory, \
                    mock.patch.dict(resolver, read_json=self.metadata,
                                    open_url=lambda url: io.BytesIO(data)):
                with self.assertRaises(ValueError):
                    resolver['acquire'](directory)
                self.assertEqual(list(Path(directory).iterdir()), [])

    def test_download_headers_and_redirect_boundary(self):
        with mock.patch('urllib.request.build_opener') as opener:
            resolver['open_url'](resolver['PROJECT_URL'])
        request = opener.return_value.open.call_args.args[0]
        self.assertIn('JustVoxel', request.get_header('User-agent'))
        self.assertIn('https://github.com/home-server-project/justvoxel-base',
                      request.get_header('User-agent'))
        with self.assertRaisesRegex(ValueError, 'Redirects'):
            resolver['NoRedirect']().redirect_request(None, None, 302, '', {}, 'https://example.com')

    def test_missing_output_directory_reports_usage_without_download_or_traceback(self):
        stderr = io.StringIO()
        with mock.patch('sys.argv', [str(SOURCE)]), mock.patch('sys.stderr', stderr), \
                mock.patch('urllib.request.build_opener') as opener:
            with self.assertRaises(SystemExit) as failure:
                exec(compile(SOURCE.read_text(), str(SOURCE), 'exec'), {'__name__': '__main__'})
        self.assertNotEqual(failure.exception.code, 0)
        self.assertIn('OUTPUT_DIRECTORY', stderr.getvalue())
        self.assertNotIn('Traceback', stderr.getvalue())
        opener.assert_not_called()


if __name__ == '__main__':
    unittest.main()
