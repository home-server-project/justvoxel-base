#!/usr/bin/env python3
"""Acquire the verified LimboAPI compatibility pin; used only during image builds."""
import argparse
import hashlib
import json
from pathlib import Path
import sys
import urllib.parse
import urllib.request

REPOSITORY = 'Elytrium/LimboAPI'
RELEASE_ID = 390180560
RELEASE_TAG = 'dev-build'
RELEASE_NAME = 'Dev-build bfef579'
SOURCE_REVISION = 'bfef579'
PUBLISHED_AT = '2026-09-16T18:44:41Z'
ASSET_ID = 568567016
UPSTREAM_FILENAME = 'limboapi-1.1.27-SNAPSHOT.jar'
EXPECTED_SIZE = 3339167
EXPECTED_SHA256 = '5d541984b90ca7a88eb49ca5f8cc3a803f5ecdfa6d7ed30b605b72ff94156e40'
ASSET_URL = 'https://api.github.com/repos/Elytrium/LimboAPI/releases/assets/568567016'
ARTIFACT_HOSTS = {'api.github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'}
COMPATIBILITY = 'Minecraft 26.3 / Velocity protocol 777'


def open_asset():
    request = urllib.request.Request(ASSET_URL, headers={
        'User-Agent': 'JustVoxel-image-build (home-server-project/justvoxel-base)',
        'Accept': 'application/octet-stream',
        'Cache-Control': 'no-cache',
    })
    return urllib.request.urlopen(request, timeout=60)


def acquire(output):
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    # Invalidate a previous successful build's composition record before downloading.
    (output / 'SHA256SUMS').unlink(missing_ok=True)
    temporary = output / 'limboapi.jar.part'
    try:
        digest = hashlib.sha256()
        size = 0
        with open_asset() as response:
            parsed = urllib.parse.urlsplit(response.geturl())
            if (parsed.scheme != 'https' or parsed.hostname not in ARTIFACT_HOSTS
                    or parsed.netloc != parsed.hostname):
                raise ValueError('LimboAPI asset must use an official GitHub HTTPS host')
            with temporary.open('wb') as destination:
                while True:
                    chunk = response.read(1024 * 1024)
                    if not chunk:
                        break
                    size += len(chunk)
                    if size > EXPECTED_SIZE:
                        raise ValueError('LimboAPI upstream file size mismatch')
                    digest.update(chunk)
                    destination.write(chunk)
        if size != EXPECTED_SIZE:
            raise ValueError('LimboAPI upstream file size mismatch')
        if digest.hexdigest() != EXPECTED_SHA256:
            raise ValueError('LimboAPI upstream SHA-256 mismatch')
        temporary.chmod(0o644)
        temporary.replace(output / 'limboapi.jar')
    finally:
        temporary.unlink(missing_ok=True)
    provenance = {
        'source': 'github-release-asset',
        'repository': REPOSITORY,
        'release_id': RELEASE_ID,
        'release_tag': RELEASE_TAG,
        'release_name': RELEASE_NAME,
        'source_revision': SOURCE_REVISION,
        'prerelease': True,
        'published_at': PUBLISHED_AT,
        'asset_id': ASSET_ID,
        'filename': UPSTREAM_FILENAME,
        'size': size,
        'sha256': EXPECTED_SHA256,
        'compatibility': COMPATIBILITY,
    }
    (output / 'limboapi-release.json').write_text(json.dumps(provenance, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output_directory', metavar='OUTPUT_DIRECTORY')
    arguments = parser.parse_args()
    try:
        acquire(arguments.output_directory)
    except (ValueError, OSError) as error:
        sys.exit(f'ERROR: {error}')
