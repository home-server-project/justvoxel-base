#!/usr/bin/env python3
"""Resolve and verify stable Velocity for the lobby's compile classpath only."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
import urllib.parse
import urllib.request

PROJECT_URL = 'https://fill.papermc.io/v3/projects/velocity'
MAX_JAR_SIZE = 128 * 1024 * 1024


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('Redirects are not allowed for Velocity acquisition')


def open_url(url):
    request = urllib.request.Request(url, headers={
        'User-Agent': 'JustVoxel-image-build (https://github.com/home-server-project/justvoxel-base)',
        'Cache-Control': 'no-cache',
    })
    return urllib.request.build_opener(NoRedirect()).open(request, timeout=60)


def read_json(url):
    with open_url(url) as response:
        data = response.read(16 * 1024 * 1024 + 1)
    if len(data) > 16 * 1024 * 1024:
        raise ValueError('PaperMC metadata exceeds size limit')
    return json.loads(data)


def select_release():
    project = read_json(PROJECT_URL)
    if (not isinstance(project, dict) or not isinstance(project.get('project'), dict)
            or project['project'].get('id') != 'velocity'):
        raise ValueError('Unexpected PaperMC project metadata')
    groups = project.get('versions')
    if not isinstance(groups, dict) or not groups:
        raise ValueError('Missing PaperMC version groups')
    # Fill supplies newest-first groups and versions; preserve its ordering.
    # https://docs.papermc.io/misc/downloads-service/
    for versions in groups.values():
        if not isinstance(versions, list) or not versions:
            raise ValueError('Invalid PaperMC version group')
        for version in versions:
            if not isinstance(version, str) or not re.fullmatch(r'[0-9][A-Za-z0-9.+-]{0,79}', version):
                raise ValueError('Invalid Velocity version')
            builds = read_json(f'{PROJECT_URL}/versions/{urllib.parse.quote(version, safe="")}/builds')
            if not isinstance(builds, list) or any(not isinstance(b, dict) for b in builds):
                raise ValueError('Invalid PaperMC builds response')
            # Fill returns builds newest-first, including non-stable channels.
            release = next((b for b in builds if b.get('channel') == 'STABLE'), None)
            if release is None:
                continue
            build = release.get('id')
            if type(build) is not int or build <= 0:
                raise ValueError('Invalid Velocity build ID')
            downloads = release.get('downloads')
            artifact = downloads.get('server:default') if isinstance(downloads, dict) else None
            if not isinstance(artifact, dict):
                raise ValueError('Missing Velocity server:default download')
            filename = artifact.get('name')
            if not isinstance(filename, str) or not re.fullmatch(r'velocity-[A-Za-z0-9.+-]+\.jar', filename):
                raise ValueError('Invalid Velocity JAR filename')
            checksums = artifact.get('checksums')
            digest = checksums.get('sha256') if isinstance(checksums, dict) else None
            if not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest):
                raise ValueError('Velocity requires an upstream SHA-256 digest')
            url = artifact.get('url')
            if not isinstance(url, str) or any(ord(c) <= 32 or ord(c) >= 127 for c in url):
                raise ValueError('Invalid Velocity download URL')
            parsed = urllib.parse.urlsplit(url)
            if (parsed.scheme != 'https' or parsed.netloc != 'fill-data.papermc.io'
                    or parsed.query or parsed.fragment
                    or parsed.path != f'/v1/objects/{digest}/{filename}'):
                raise ValueError('Velocity download must use the official HTTPS PaperMC download host')
            size = artifact.get('size')
            if type(size) is not int or not 0 < size <= MAX_JAR_SIZE:
                raise ValueError('Invalid Velocity download size')
            metadata = dict(project='velocity', version=version, build=build,
                            channel='STABLE', sha256=digest, filename=filename)
            return metadata, artifact
    raise ValueError('No stable Velocity build exists on PaperMC')


def acquire(output):
    metadata, artifact = select_release()
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    # Never leave a previous success record after a failed acquisition.
    (output / 'velocity-release.json').unlink(missing_ok=True)
    temporary = output / 'velocity-server.jar.part'
    try:
        digest = hashlib.sha256()
        size = 0
        with open_url(artifact['url']) as response, temporary.open('wb') as destination:
            while True:
                chunk = response.read(1024 * 1024)
                if not chunk:
                    break
                size += len(chunk)
                if size > artifact['size']:
                    raise ValueError('Velocity upstream file size mismatch')
                digest.update(chunk)
                destination.write(chunk)
        if size != artifact['size']:
            raise ValueError('Velocity upstream file size mismatch')
        if digest.hexdigest() != metadata['sha256']:
            raise ValueError('Velocity upstream SHA-256 mismatch')
        temporary.chmod(0o644)
        temporary.replace(output / metadata['filename'])
    finally:
        temporary.unlink(missing_ok=True)
    (output / 'velocity-release.json').write_text(json.dumps(metadata, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output_directory', metavar='OUTPUT_DIRECTORY')
    arguments = parser.parse_args()
    try:
        acquire(arguments.output_directory)
    except (ValueError, OSError) as error:
        sys.exit(f'ERROR: {error}')
