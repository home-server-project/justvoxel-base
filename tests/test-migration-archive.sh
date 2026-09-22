#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
archive="${repo_root}/mjust/libexec/migration-archive"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/migration-common.sh"
# shellcheck disable=SC1091

fail(){ echo "FAIL: $*" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

make_paper() {
    local root="$1" type="${2:-paper}" online="${3:-true}" version="${4:-1.21.8}"
    mkdir -p "${root}/world" "${root}/plugins" "${root}/config" "${root}/logs"
    cat > "${root}/server.properties" <<EOF2
level-name=world
online-mode=${online}
gamemode=survival
difficulty=hard
white-list=true
enforce-whitelist=true
max-players=8
motd=Migration Test
server-port=25565
EOF2
    printf 'level\n' > "${root}/world/level.dat"
    printf 'paper\n' > "${root}/config/paper-global.yml"
    printf 'plugin\n' > "${root}/plugins/ExamplePlugin.jar"
    printf 'This server is running Paper version test (MC: %s)\n' "${version}" > "${root}/logs/latest.log"
    if [[ ${type} == itzg ]]; then
        printf 'PAPER_VERSION=%s\n' "${version}" > "${root}/.paper.env"
    fi
}

paper="${tmp}/paper"
make_paper "${paper}"
info="$("${archive}" inspect "${paper}")"
[[ $(jq -r '.format' <<< "${info}") == directory ]] || fail 'directory inspect format wrong'
detected="$("${archive}" detect "${paper}")"
[[ $(jq -r '.candidates | length' <<< "${detected}") == 1 ]] || fail 'Paper root not detected'
[[ $(jq -r '.candidates[0].sourceType' <<< "${detected}") == paper ]] || fail 'generic Paper classification wrong'
[[ $(jq -r '.candidates[0].minecraftVersion' <<< "${detected}") == 1.21.8 ]] || fail 'Paper source version detection wrong'

itzg="${tmp}/itzg"
make_paper "${itzg}" itzg
detected="$("${archive}" detect "${itzg}")"
[[ $(jq -r '.candidates[0].sourceType' <<< "${detected}") == itzg-paper ]] || fail 'itzg/Paper classification wrong'

wrapper="${tmp}/wrapper/export/data"
mkdir -p "$(dirname "${wrapper}")"
cp -a "${paper}" "${wrapper}"
detected="$("${archive}" detect "${tmp}/wrapper")"
[[ $(jq -r '.candidates | length' <<< "${detected}") == 1 ]] || fail 'wrapper directory Paper root not detected'
[[ $(jq -r '.candidates[0].root' <<< "${detected}") == "$(realpath "${wrapper}")" ]] || fail 'wrapper root resolution wrong'

mkdir -p "${tmp}/ambiguous/a" "${tmp}/ambiguous/b"
cp -a "${paper}/." "${tmp}/ambiguous/a/"
cp -a "${paper}/." "${tmp}/ambiguous/b/"
detected="$("${archive}" detect "${tmp}/ambiguous")"
[[ $(jq -r '.candidates | length' <<< "${detected}") == 2 ]] || fail 'ambiguous roots were not preserved for administrator choice'

for mod in fabric forge neoforge; do
    root="${tmp}/${mod}"
    make_paper "${root}"
    rm -f "${root}/config/paper-global.yml"
    case "${mod}" in
        fabric) touch "${root}/fabric-server-launch.jar" ;;
        forge) mkdir -p "${root}/libraries/net/minecraftforge" ;;
        neoforge) mkdir -p "${root}/libraries/net/neoforged" ;;
    esac
    detected="$("${archive}" detect "${root}")"
    [[ $(jq -r '.candidates[0].sourceType' <<< "${detected}") == "${mod}" ]] || fail "${mod} refusal detection wrong"
    [[ $(jq -r '.candidates[0].supported' <<< "${detected}") == false ]] || fail "${mod} must be unsupported"
done

# Native bundle integrity.
manifest="${tmp}/manifest.json"
cat > "${manifest}" <<'JSON'
{"schema":"org.justvoxel.migration","schemaVersion":1,"createdAt":"2026-09-15T19:00:00Z","minecraft":{"version":"1.21.8","onlineMode":true},"integrity":{"algorithm":"sha256","index":"integrity/files.json"}}
JSON
native="${tmp}/native.tar.gz"
"${archive}" create-native "${paper}" "${manifest}" "${native}"
"${archive}" verify-native "${native}" >/dev/null || fail 'native bundle verification failed'
[[ $(jq -r '.nativeBundle' <<< "$("${archive}" inspect "${native}")") == true ]] || fail 'native bundle not identified'

mkdir "${tmp}/corrupt-native"
tar -C "${tmp}/corrupt-native" -xzf "${native}"
printf 'corrupted\n' >> "${tmp}/corrupt-native/justvoxel-migration/server/world/level.dat"
tar -C "${tmp}/corrupt-native" -czf "${tmp}/corrupt-checksum.tar.gz" justvoxel-migration
if "${archive}" verify-native "${tmp}/corrupt-checksum.tar.gz" >/dev/null 2>&1; then
    fail 'corrupted native checksum was accepted'
fi

# Normal tar and ZIP import support.
tar -C "${tmp}" -cf "${tmp}/paper.tar" paper
tar -C "${tmp}" -czf "${tmp}/paper.tgz" paper
python3 - "${paper}" "${tmp}/paper.zip" <<'PY'
import os, sys, zipfile
source, output = sys.argv[1:]
with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as zf:
    for root, dirs, files in os.walk(source):
        for name in files:
            path=os.path.join(root,name)
            zf.write(path, os.path.join('paper', os.path.relpath(path, source)))
PY
for item in "${tmp}/paper.tar" "${tmp}/paper.tgz" "${tmp}/paper.zip"; do
    "${archive}" inspect "${item}" >/dev/null || fail "supported archive rejected: ${item}"
done

# Realistic itzg/minecraft-server Paper export: the server root is /data inside
# the archive, matching the common container volume layout used for migration.
mkdir -p "${tmp}/itzg-container"
make_paper "${tmp}/itzg-container/data" itzg true 1.21.8
tar -C "${tmp}/itzg-container" -czf "${tmp}/itzg-data.tar.gz" data
"${archive}" inspect "${tmp}/itzg-data.tar.gz" >/dev/null || fail 'itzg /data tar.gz inspection failed'
rm -rf "${tmp}/itzg-stage"
"${archive}" extract "${tmp}/itzg-data.tar.gz" "${tmp}/itzg-stage"
detected="$("${archive}" detect "${tmp}/itzg-stage")"
[[ $(jq -r '.candidates | length' <<< "${detected}") == 1 ]] || fail 'itzg /data tar.gz did not produce exactly one Paper candidate'
[[ $(jq -r '.candidates[0].root' <<< "${detected}") == "${tmp}/itzg-stage/data" ]] || fail 'itzg /data tar.gz candidate root is wrong'
[[ $(jq -r '.candidates[0].sourceType' <<< "${detected}") == itzg-paper ]] || fail 'itzg /data tar.gz classification is wrong'
[[ $(jq -r '.candidates[0].minecraftVersion' <<< "${detected}") == 1.21.8 ]] || fail 'itzg /data tar.gz Minecraft version detection is wrong'
rm -rf "${tmp}/zip-stage"
"${archive}" extract "${tmp}/paper.zip" "${tmp}/zip-stage"
[[ -f ${tmp}/zip-stage/paper/world/level.dat ]] || fail 'ZIP extraction missed world data'

# Truncated/incomplete .partial files are never valid import selections or bundles.
head -c 256 "${native}" > "${tmp}/migration.tar.gz.partial"
if jv_migration_validate_source_name "${tmp}/migration.tar.gz.partial" >/dev/null 2>&1; then
    fail '.partial source was accepted'
fi
if "${archive}" verify-native "${tmp}/migration.tar.gz.partial" >/dev/null 2>&1; then
    fail 'truncated native .partial bundle was accepted by integrity verification'
fi

# Path traversal, unsafe symlink and duplicate tar paths.
python3 - "${tmp}" <<'PY'
import io, os, sys, tarfile, zipfile, warnings
warnings.simplefilter('ignore', UserWarning)
base=sys.argv[1]
with tarfile.open(os.path.join(base,'traversal.tar.gz'),'w:gz') as tf:
    data=b'x'; i=tarfile.TarInfo('../escape'); i.size=1; tf.addfile(i,io.BytesIO(data))
with tarfile.open(os.path.join(base,'unsafe-link.tar.gz'),'w:gz') as tf:
    d=tarfile.TarInfo('server'); d.type=tarfile.DIRTYPE; tf.addfile(d)
    l=tarfile.TarInfo('server/link'); l.type=tarfile.SYMTYPE; l.linkname='../../etc/passwd'; tf.addfile(l)
with tarfile.open(os.path.join(base,'duplicate.tar.gz'),'w:gz') as tf:
    for data in (b'a',b'b'):
        i=tarfile.TarInfo('server/file'); i.size=1; tf.addfile(i,io.BytesIO(data))
with zipfile.ZipFile(os.path.join(base,'traversal.zip'),'w') as zf:
    zf.writestr('../escape','x')
with zipfile.ZipFile(os.path.join(base,'duplicate.zip'),'w') as zf:
    zf.writestr('server/file','a'); zf.writestr('server/file','b')
with zipfile.ZipFile(os.path.join(base,'symlink.zip'),'w') as zf:
    i=zipfile.ZipInfo('server/link'); i.create_system=3; i.external_attr=(0o120777 << 16); zf.writestr(i,'../../etc/passwd')
PY
for item in traversal.tar.gz unsafe-link.tar.gz duplicate.tar.gz traversal.zip duplicate.zip symlink.zip; do
    if "${archive}" inspect "${tmp}/${item}" >/dev/null 2>&1; then
        fail "unsafe archive was accepted: ${item}"
    fi
done

# Deterministic expanded-byte and member-count limits.
if JV_MIGRATION_MAX_EXPANDED_BYTES=4 "${archive}" inspect "${tmp}/paper.tgz" >/dev/null 2>&1; then
    fail 'excessive expanded size limit was not enforced'
fi
if JV_MIGRATION_MAX_MEMBERS=2 "${archive}" inspect "${tmp}/paper.tgz" >/dev/null 2>&1; then
    fail 'excessive member count limit was not enforced'
fi

# Identity and version safety helpers.
[[ $(jv_migration_online_mode_state true true) == online ]] || fail 'online-mode true should be supported'
[[ $(jv_migration_online_mode_state unknown unknown) == unknown ]] || fail 'unknown online-mode should require resolution'
[[ $(jv_migration_online_mode_state unknown false) == offline ]] || fail 'offline-mode must be refused'
[[ $(jv_migration_online_mode_state true false) == conflict ]] || fail 'manifest/server.properties identity conflict not detected'
[[ $(jv_migration_version_resolution 1.21.8 1.21.8) == 1.21.8 ]] || fail 'matching source version failed'
if jv_migration_version_resolution 1.21.8 1.21.7 >/dev/null 2>&1; then fail 'source-version conflict accepted'; fi

echo 'migration archive, detection and compatibility tests passed.'
