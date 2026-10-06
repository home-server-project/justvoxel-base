#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
python3 - "${repo_root}" <<'PY'
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import yaml

repo = Path(sys.argv[1])
templates = repo / 'templates/plus'
stack = yaml.safe_load((templates / 'compose.yaml').read_text())
services = stack['services']
expected = {'database', 'cache', 'panel', 'wings', 'drydock'}
assert set(services) == expected
for name, service in services.items():
    assert service['profiles'] == [name]
    assert service['labels']['dd.watch'] == 'true'
    assert service['labels']['io.home-server-project.justvoxel.layer'] == 'infrastructure'
    assert 'build' not in service and 'privileged' not in service
    # Profiles must remain independent; no disabled component gets pulled in.
    assert not service.get('depends_on')
assert not services['database'].get('ports') and not services['cache'].get('ports')
assert services['database']['healthcheck']['test'][1] == 'healthcheck.sh'
assert services['cache']['healthcheck']['test'] == ['CMD', 'redis-cli', 'ping']
assert services['drydock']['environment']['DD_WATCHER_LOCAL_WATCHBYDEFAULT'] == 'false'
assert services['drydock']['environment']['DD_WATCHER_LOCAL_WATCHALL'] == 'true'
assert services['drydock']['environment']['DD_ACTION_DOCKERCOMPOSE_INFRA_AUTO'] == 'false'
assert services['drydock']['environment']['DD_ACTION_DOCKERCOMPOSE_INFRA_MODE'] == 'batch'
assert services['drydock']['env_file'][0]['format'] == 'raw'
assert not any('ANONYMOUS' in key or 'INSECURE_ROOT' in key for key in services['drydock']['environment'])
assert services['drydock']['user'] == '1000:1000'
assert '/etc/justvoxel/plus:/etc/justvoxel/plus:z' in services['drydock']['volumes']
assert '${PLUS_DATA_ROOT}/wings:${PLUS_DATA_ROOT}/wings:z' in services['wings']['volumes']
for service in services.values():
    for mount in service.get('volumes', []):
        if mount.startswith(('/var/run/docker.sock:', '/var/lib/docker/containers:', '/etc/ssl/certs:')):
            assert not mount.endswith((':z', ':Z')), 'Do not relabel host Docker or CA trees'
assert not stack['networks']['infrastructure'].get('ipam')
unit = (repo / 'system_files/usr/lib/systemd/system/justvoxel-plus-stack.service').read_text()
assert 'ConditionPathExists=/etc/justvoxel/plus/deployed' in unit
assert 'Requires=docker.service' in unit
assert 'compose --env-file stack.env --file compose.yaml up --detach' in unit
assert 'compose --env-file stack.env --file compose.yaml stop' in unit
assert 'down' not in unit and 'pull' not in unit
assert 'systemctl enable justvoxel-plus-stack' not in (repo / 'build_files/build-common.sh').read_text()
for filename in ('database.env.in', 'panel.env.in', 'panel-persistent.env.in', 'drydock.env.in'):
    lines = (templates / filename).read_text().splitlines()
    for line in lines:
        if any(key in line for key in ('PASSWORD=', 'APP_KEY=', 'HASHIDS_SALT=', 'HASH=')):
            assert '=@@' in line and line.endswith('@@'), 'No usable secret in image template'
print('Plus stack source contracts passed.')

compose = None
if shutil.which('docker') and subprocess.run(['docker', 'compose', 'version'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
    compose = ['docker', 'compose']
elif shutil.which('docker-compose'):
    compose = ['docker-compose']
if compose is None:
    if os.environ.get('CI') == 'true':
        raise SystemExit('Docker Compose is required for CI stack validation')
    print('Docker Compose unavailable locally; native resolution will run in CI.')
    raise SystemExit(0)

with tempfile.TemporaryDirectory() as directory:
    work = Path(directory)
    shutil.copyfile(templates / 'compose.yaml', work / 'compose.yaml')
    raw_hash = '$argon2id$v=19$m=65536,t=3,p=4$testSalt$testHash'
    replacements = {
        'PANEL_URL': 'http://192.0.2.10:8081', 'DOCKER_GID': '987',
        'DATABASE_PASSWORD': 'fixtureDatabasePassword', 'DATABASE_ROOT_PASSWORD': 'fixtureRootPassword',
        'PANEL_APP_KEY': 'base64:fixtureOnly', 'PANEL_HASHIDS_SALT': 'fixtureSalt',
        'DRYDOCK_USER': 'fixtureAdmin', 'DRYDOCK_PASSWORD_HASH': raw_hash,
        'DATA_ROOT': '/var/lib/justvoxel-plus', 'PROFILES': ','.join(sorted(expected)),
        'PLUS_HOST': '192.0.2.10', 'PANEL_HTTP_BIND_ADDRESS': '0.0.0.0', 'TLS_ENABLED': 'false',
    }
    for filename in ('stack.env', 'database.env', 'panel.env', 'drydock.env'):
        text = (templates / (filename + '.in')).read_text()
        for key, value in replacements.items():
            text = text.replace('@@' + key + '@@', value)
        assert '@@' not in text
        (work / filename).write_text(text)
    clean_env = {key: value for key, value in os.environ.items() if key not in ('COMPOSE_PROFILES', 'PLUS_DATA_ROOT', 'PANEL_URL', 'DOCKER_GID') and not key.endswith('_IMAGE')}
    def config(profiles, root):
        env = {**clean_env, 'COMPOSE_PROFILES': ','.join(profiles), 'PLUS_DATA_ROOT': root}
        result = subprocess.run(compose + ['--env-file', 'stack.env', '--file', 'compose.yaml', 'config', '--format', 'json'], cwd=work, env=env, capture_output=True, text=True)
        if result.returncode:
            raise AssertionError(result.stderr)
        return json.loads(result.stdout)
    for root in ('/var/lib/justvoxel-plus', '/mnt/second-drive/plus'):
        resolved = config(sorted(expected), root)
        assert set(resolved['services']) == expected
        wings_mounts = resolved['services']['wings']['volumes']
        assert any(v['source'] == root + '/wings' and v['target'] == root + '/wings' for v in wings_mounts)
        # Compose escapes dollar signs in exported JSON as well as YAML.
        # Compare the full export representation, not an interpolated hash.
        assert resolved['services']['drydock']['environment']['DD_AUTH_BASIC_ADMIN_HASH'] == raw_hash.replace('$', '$$'), 'Compose export changed the authentication hash beyond dollar escaping'
        for name in expected:
            assert set(config([name], root)['services']) == {name}
    env = {**clean_env, 'COMPOSE_PROFILES': ','.join(expected), 'PLUS_DATA_ROOT': '/var/lib/justvoxel-plus', 'PANEL_IMAGE': 'ghcr.io/pterodactyl/panel:administrator-choice'}
    changed = subprocess.run(compose + ['--env-file', 'stack.env', '--file', 'compose.yaml', 'config', '--format', 'json'], cwd=work, env=env, capture_output=True, text=True, check=True)
    assert json.loads(changed.stdout)['services']['panel']['image'].endswith(':administrator-choice')
print('Native Docker Compose full/partial stack, storage, authentication and editable-image checks passed.')
PY
