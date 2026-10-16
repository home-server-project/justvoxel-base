#!/usr/bin/bash
# Independent verifier only:
#   podman unshare bash tests/test-instance-delete.sh
# Exercises the production remover with runtime/mount discovery stubbed. There
# is no production environment-variable bypass and no host runtime invocation.
set -euo pipefail
[[ $EUID == 0 ]] || { echo 'Run this regression in the root user namespace.' >&2; exit 1; }
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -m0700 "$fixture/storage" "$fixture/storage/minecraft-instances"
mkdir -m0700 "$fixture/storage/minecraft-instances/jv-family1" "$fixture/storage/minecraft-instances/jv-sibling"
selected="$fixture/storage/minecraft-instances/jv-family1/data"
sibling="$fixture/storage/minecraft-instances/jv-sibling/data"
mkdir "$selected" "$sibling" "$fixture/storage/minecraft" "$fixture/archives" "$fixture/names" "$fixture/journals"
# A non-root container owner must remain distinct from root-private parents.
chown 1000:1000 "$selected" "$sibling"
mkdir "$selected/world"
printf world > "$selected/world/level.dat"
printf sibling > "$sibling/level.dat"
ln -s "$sibling" "$selected/world/sibling-link"
printf legacy > "$fixture/storage/minecraft/level.dat"
printf archive > "$fixture/archives/completed.tar.gz"
printf metadata > "$fixture/archives/completed.json"
printf historical > "$fixture/names/jv-family1.json"
cat > "$fixture/prerequisites" <<'STUB'
#!/usr/bin/bash
[[ $1 == restore-validate && $2 == jv-family1 && $# == 9 ]]
STUB
chmod +x "$fixture/prerequisites"
# Replace ONLY the fixed external runtime discovery dependency in a fixture
# copy. The production path checks, inode evidence and real remover are intact.
sed "s|/usr/libexec/justvoxel/mjust/instance-runtime-prerequisites|$fixture/prerequisites|" "$repo/mjust/libexec/instance-delete" > "$fixture/delete"
chmod +x "$fixture/delete"
call_delete() { "$fixture/delete" "$1" jv-family1 "$fixture/storage" "$selected" '' '' '' 1000 1000 "$2"; }
evidence=$(call_delete inspect - | python3 -c 'import json,sys; print(json.load(sys.stdin)["identity"])')
printf '%s\n' "$evidence" > "$fixture/journals/delete-jv-family1"
# Interruption before removal leaves both world and durable retry evidence.
[[ -f "$selected/world/level.dat" && -s "$fixture/journals/delete-jv-family1" ]]
# A substituted target must fail without touching either real tree.
mv "$selected" "$fixture/storage/minecraft-instances/jv-family1/held-data"
ln -s "$sibling" "$selected"
if call_delete remove "$evidence"; then echo 'symlink substitution accepted' >&2; exit 1; fi
[[ -f "$sibling/level.dat" && -f "$fixture/storage/minecraft-instances/jv-family1/held-data/world/level.dat" ]]
rm "$selected"
mv "$fixture/storage/minecraft-instances/jv-family1/held-data" "$selected"
# A different real directory at the exact same path is also foreign evidence.
mv "$selected" "$fixture/storage/minecraft-instances/jv-family1/held-data"
mkdir "$selected"
chown 1000:1000 "$selected"
printf replacement > "$selected/level.dat"
if call_delete remove "$evidence"; then echo 'directory substitution accepted' >&2; exit 1; fi
[[ $(cat "$selected/level.dat") == replacement ]]
rm "$selected/level.dat"
rmdir "$selected"
mv "$fixture/storage/minecraft-instances/jv-family1/held-data" "$selected"
# Reject symlinks in parents too, even when they resolve back to the same data.
mv "$fixture/storage/minecraft-instances/jv-family1" "$fixture/storage/minecraft-instances/held-parent"
ln -s "$fixture/storage/minecraft-instances/held-parent" "$fixture/storage/minecraft-instances/jv-family1"
if call_delete remove "$evidence"; then echo 'parent symlink accepted' >&2; exit 1; fi
[[ -f "$fixture/storage/minecraft-instances/held-parent/data/world/level.dat" ]]
rm "$fixture/storage/minecraft-instances/jv-family1"
mv "$fixture/storage/minecraft-instances/held-parent" "$fixture/storage/minecraft-instances/jv-family1"
# Wrong transaction/inode evidence cannot remove this world.
if call_delete remove '0:1'; then echo 'foreign evidence accepted' >&2; exit 1; fi
[[ -f "$selected/world/level.dat" ]]
call_delete remove "$evidence"
[[ ! -e "$selected" && ! -L "$selected" ]]
# Retry after bytes were erased, before journal finalization, is idempotent.
[[ -s "$fixture/journals/delete-jv-family1" ]]
call_delete remove "$evidence"
[[ $(cat "$sibling/level.dat") == sibling ]]
[[ $(cat "$fixture/storage/minecraft/level.dat") == legacy ]]
[[ $(cat "$fixture/archives/completed.tar.gz") == archive ]]
[[ $(cat "$fixture/archives/completed.json") == metadata ]]
[[ $(cat "$fixture/names/jv-family1.json") == historical ]]
[[ -d "$fixture/storage" && -d "$fixture/storage/minecraft-instances/jv-family1" ]]
rm "$fixture/journals/delete-jv-family1"
echo 'PASS: exact selected tree removal, preservation, substitution refusal, and retry'
