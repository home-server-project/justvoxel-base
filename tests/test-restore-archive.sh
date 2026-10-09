#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${repo_root}/mjust/libexec/restore-archive"

fail(){ echo "FAIL: $*" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

mkdir -p \
    "${tmp}/source/minecraft/world/playerdata" \
    "${tmp}/source/minecraft/world_nether" \
    "${tmp}/source/minecraft/world_the_end" \
    "${tmp}/source/minecraft/plugins/ExamplePlugin"
printf 'level-name=world\n' > "${tmp}/source/minecraft/server.properties"
printf 'overworld\n' > "${tmp}/source/minecraft/world/level.dat"
printf 'player\n' > "${tmp}/source/minecraft/world/playerdata/player.dat"
printf 'nether\n' > "${tmp}/source/minecraft/world_nether/level.dat"
printf 'end\n' > "${tmp}/source/minecraft/world_the_end/level.dat"
printf 'plugin-data\n' > "${tmp}/source/minecraft/plugins/ExamplePlugin/config.yml"
ln -s level.dat "${tmp}/source/minecraft/world/safe-link"

# Existing backups have no ID; the local destination identity lives outside data.
mkdir -p "${tmp}/instances"
printf '{"id":"jv-local123"}\n' > "${tmp}/instances/minecraft.json"
chmod 0700 "${tmp}/instances"
chmod 0600 "${tmp}/instances/minecraft.json"
local_identity="$(cat "${tmp}/instances/minecraft.json")"
tar -C "${tmp}/source" -czf "${tmp}/minecraft-2026-09-15-043000.tar.gz" minecraft

info="$(bash "${helper}" inspect "${tmp}/minecraft-2026-09-15-043000.tar.gz")"
[[ $(jq -r '.root' <<< "${info}") == minecraft ]] || fail 'archive root detection failed'
[[ $(jq -r '.levelName' <<< "${info}") == world ]] || fail 'level-name detection failed'
[[ $(jq -r '.primaryWorldPresent' <<< "${info}") == true ]] || fail 'primary world detection failed'

bash "${helper}" extract-world "${tmp}/minecraft-2026-09-15-043000.tar.gz" "${tmp}/world-stage" >/dev/null
test -f "${tmp}/world-stage/minecraft/world/level.dat" || fail 'world restore staging missed overworld'
test -f "${tmp}/world-stage/minecraft/world_nether/level.dat" || fail 'world restore staging missed Nether'
test -f "${tmp}/world-stage/minecraft/world_the_end/level.dat" || fail 'world restore staging missed End'
test ! -e "${tmp}/world-stage/minecraft/plugins" || fail 'world-only staging extracted plugin data'
test ! -e "${tmp}/world-stage/minecraft/server.properties" || fail 'world-only staging extracted server.properties'
[[ -L "${tmp}/world-stage/minecraft/world/safe-link" ]] || fail 'safe in-root symlink was not restored'

bash "${helper}" extract-full "${tmp}/minecraft-2026-09-15-043000.tar.gz" "${tmp}/full-stage" >/dev/null
test -f "${tmp}/full-stage/minecraft/plugins/ExamplePlugin/config.yml" || fail 'full restore staging missed plugin data'
test -f "${tmp}/full-stage/minecraft/server.properties" || fail 'full restore staging missed server.properties'

[[ $(cat "${tmp}/instances/minecraft.json") == "${local_identity}" ]] || fail 'legacy restore changed local identity'

# An external data archive can contain a similarly named file; it remains data
# within staging and cannot replace the appliance-owned identity outside data.
mkdir -p "${tmp}/source/minecraft/instances"
printf '{"id":"jv-archive123"}\n' > "${tmp}/source/minecraft/instances/minecraft.json"
tar -C "${tmp}/source" -czf "${tmp}/external.tar.gz" minecraft
bash "${helper}" extract-full "${tmp}/external.tar.gz" "${tmp}/external-stage" >/dev/null
[[ $(cat "${tmp}/instances/minecraft.json") == "${local_identity}" ]] || fail 'external archive replaced local identity'
test -f "${tmp}/external-stage/minecraft/world/level.dat" || fail 'external archive no longer restores'

printf 'not a gzip archive\n' > "${tmp}/corrupt.tar.gz"
if bash "${helper}" inspect "${tmp}/corrupt.tar.gz" >/dev/null 2>&1; then
    fail 'corrupt archive was accepted'
fi

python3 - "${tmp}/malicious.tar.gz" <<'PY'
import io, sys, tarfile
with tarfile.open(sys.argv[1], 'w:gz') as tf:
    data=b'escape'
    info=tarfile.TarInfo('../escape')
    info.size=len(data)
    tf.addfile(info, io.BytesIO(data))
PY
if bash "${helper}" inspect "${tmp}/malicious.tar.gz" >/dev/null 2>&1; then
    fail 'path-traversal archive was accepted'
fi

rm -f "${tmp}/source/minecraft/world/safe-link"
ln -s /etc/passwd "${tmp}/source/minecraft/world/unsafe-link"
tar -C "${tmp}/source" -czf "${tmp}/link.tar.gz" minecraft
if bash "${helper}" inspect "${tmp}/link.tar.gz" >/dev/null 2>&1; then
    fail 'archive containing an unsafe symlink was accepted'
fi

echo 'restore archive safety tests passed.'
