#!/usr/bin/env python3
"""Resolve and verify the latest stable LimboAPI; used only during image builds."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import re
import sys
import urllib.parse
import urllib.request

PROJECT_ID = 'TZOteSf2'
VERSIONS_URL = f'https://api.modrinth.com/v2/project/{PROJECT_ID}/version'
MAX_JAR_SIZE = 128 * 1024 * 1024
ARTIFACT_HOSTS = {'cdn.modrinth.com'}


def version_tuple(value):
    if not isinstance(value, str) or not re.fullmatch(r'(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})', value):
        raise ValueError('LimboAPI release must have a stable semantic version')
    return tuple(map(int, value.split('.')))


def published(version):
    value = version.get('date_published')
    if not isinstance(value, str) or not re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z', value):
        raise ValueError('Invalid LimboAPI release publication date')
    return datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))


def select_release(versions):
    if not isinstance(versions, list) or any(not isinstance(v, dict) for v in versions):
        raise ValueError('Invalid Modrinth versions response')
    stable = [v for v in versions if v.get('version_type') == 'release'
              and v.get('status') == 'listed'
              and isinstance(v.get('loaders'), list) and 'velocity' in v['loaders']]
    if not stable:
        raise ValueError('No listed stable Velocity LimboAPI release exists on Modrinth')
    stable.sort(key=published, reverse=True)
    if len(stable) > 1 and published(stable[0]) == published(stable[1]):
        raise ValueError('Ambiguous newest stable LimboAPI release')
    release = stable[0]
    if release.get('project_id') != PROJECT_ID:
        raise ValueError('Unexpected LimboAPI Modrinth project ID')
    if not isinstance(release.get('id'), str) or not re.fullmatch(r'[A-Za-z0-9]{8}', release['id']):
        raise ValueError('Invalid Modrinth version ID')
    version_tuple(release.get('version_number'))
    files = release.get('files')
    if not isinstance(files, list) or not files or any(not isinstance(f, dict) for f in files):
        raise ValueError('Invalid LimboAPI file metadata')
    if any(type(f.get('primary', False)) is not bool for f in files):
        raise ValueError('Invalid LimboAPI primary flag')
    jars = [f for f in files if isinstance(f.get('filename'), str) and f['filename'].endswith('.jar')]
    primaries = [f for f in files if f.get('primary', False)]
    if primaries:
        if len(primaries) != 1 or primaries[0] not in jars:
            raise ValueError('Ambiguous primary LimboAPI JAR')
        artifact = primaries[0]
    elif len(jars) == 1:
        artifact = jars[0]
    else:
        raise ValueError('Missing or ambiguous LimboAPI JAR')
    url = artifact.get('url')
    if not isinstance(url, str) or any(ord(c) <= 32 or ord(c) >= 127 for c in url):
        raise ValueError('Invalid LimboAPI artifact URL')
    parsed = urllib.parse.urlsplit(url)
    if (parsed.scheme != 'https' or parsed.hostname not in ARTIFACT_HOSTS
            or parsed.netloc != parsed.hostname or parsed.query or parsed.fragment
            or not parsed.path.endswith('.jar')):
        raise ValueError('LimboAPI artifact URL must use the trusted HTTPS Modrinth CDN')
    size = artifact.get('size')
    if type(size) is not int or not 0 < size <= MAX_JAR_SIZE:
        raise ValueError('Invalid LimboAPI artifact size')
    hashes = artifact.get('hashes')
    digest = hashes.get('sha512') if isinstance(hashes, dict) else None
    if not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{128}', digest):
        raise ValueError('LimboAPI requires an upstream SHA-512 digest')
    return release, artifact


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('Redirects are not allowed for LimboAPI acquisition')


def open_url(url):
    request = urllib.request.Request(url, headers={
        'User-Agent': 'JustVoxel-image-build (home-server-project/justvoxel-base)',
        'Cache-Control': 'no-cache',
    })
    return urllib.request.build_opener(NoRedirect()).open(request, timeout=60)


def acquire(output):
    # Always resolve live metadata, even when artifacts from a previous build exist.
    with open_url(VERSIONS_URL) as response:
        data = response.read(16 * 1024 * 1024 + 1)
    if len(data) > 16 * 1024 * 1024:
        raise ValueError('Modrinth versions response exceeds size limit')
    release, artifact = select_release(json.loads(data))
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    # Invalidate a previous successful build's composition record before downloading.
    (output / 'SHA256SUMS').unlink(missing_ok=True)
    temporary = output / 'limboapi.jar.part'
    try:
        digest = hashlib.sha512()
        size = 0
        with open_url(artifact['url']) as response, temporary.open('wb') as destination:
            while True:
                chunk = response.read(1024 * 1024)
                if not chunk:
                    break
                size += len(chunk)
                if size > artifact['size']:
                    raise ValueError('LimboAPI upstream file size mismatch')
                digest.update(chunk)
                destination.write(chunk)
        if size != artifact['size']:
            raise ValueError('LimboAPI upstream file size mismatch')
        if digest.hexdigest() != artifact['hashes']['sha512']:
            raise ValueError('LimboAPI upstream SHA-512 mismatch')
        temporary.chmod(0o644)
        temporary.replace(output / 'limboapi.jar')
    finally:
        temporary.unlink(missing_ok=True)
    provenance = {key: release[key] for key in ('project_id', 'version_number', 'date_published')}
    provenance.update(version_id=release['id'], sha512=artifact['hashes']['sha512'], size=size)
    (output / 'limboapi-release.json').write_text(json.dumps(provenance, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output_directory', metavar='OUTPUT_DIRECTORY')
    arguments = parser.parse_args()
    try:
        acquire(arguments.output_directory)
    except (ValueError, OSError) as error:
        sys.exit(f'ERROR: {error}')
