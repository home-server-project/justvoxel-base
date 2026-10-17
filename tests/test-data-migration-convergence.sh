#!/usr/bin/bash
set -euo pipefail
cd "$(dirname -- "$0")/.."
fail() { echo "FAIL: $*" >&2; exit 1; }
plan=mjust/libexec/admin-data-migration-plan-json
transaction=mjust/libexec/admin-data-migration-transaction-json
runtime=management/cmd/justvoxel-management-agent/data_migration_runtime.go
for file in "$plan" "$transaction" mjust/libexec/data-migration mjust/libexec/data-migration-api.sh mjust/libexec/validate-instance-runtime-commit; do bash -n "$file"; done
for obsolete in minecraft.service web-status-json jv_stop_minecraft_adaptive 'systemctl ' 'render_runtime' 'apply_data_selinux' 'minecraft-backup --leave-stopped' restore-runtime-validate; do
    if rg -F "$obsolete" "$plan" "$transaction"; then fail "physical migration retains fixed-runtime execution: $obsolete"; fi
done
for required in 'data-migration-runtime plan' 'minecraft-instances' 'insufficient_backup_space' 'migration_busy' 'participants:$participants'; do
    rg -Fq "$required" "$plan" || fail "missing whole-appliance Plan contract: $required"
done
for required in 'runtime capture' 'runtime quiesce' 'runtime backup' 'runtime relocate' 'runtime rollback' 'runtime finish' 'restore_participant_states' 'sync -f' 'rsync -aHAX' 'schema_version:"v2"' 'restore-exclusion.lock' 'flock -s -n 7' 'configuration changed outside migration'; do
    rg -Fq "$required" "$transaction" || fail "missing physical transaction safety: $required"
done
for required in 'migrationRegisteredInstances' 'listInstances' 'readRecord' 'b.render' 'b.observe' 'b.players' 'b.actionWithWarnings' 'migrationRecordHash' 'b.writeJSON' 'reflect.DeepEqual(live, reviewed)' 'e.Participant.State == "stopped"'; do
    rg -Fq "$required" "$runtime" || fail "missing authoritative participants/lifecycle: $required"
done
if rg 'server_id|selected_server|/v1/minecraft/instances/.*data-migration' management/cmd/justvoxel-management-agent/*data_migration*.go "$transaction" "$plan"; then fail 'physical migration became selected-server scoped'; fi
rg -Fq 'POST /v1/admin/data-migration/recover' management/cmd/justvoxel-management-agent/admin_data_migration_apply.go
rg -Fq 'jv_data_migration_recover' mjust/libexec/data-migration
rg -Fq 'data-migration-start-${id}.json' mjust/libexec/validate-instance-runtime-commit
rg -Fq 'marker.OperationID != j.OperationID' "$runtime"
echo 'Physical data migration appliance-wide source contracts passed.'

# Execute the actual copy/verification block on isolated user-owned files. Only
# directory ownership installation is stubbed; rsync and its verification are real.
python3 - <<'PY'
import os, subprocess, tempfile
from pathlib import Path
source=Path('mjust/libexec/admin-data-migration-transaction-json').read_text()
copy=source[source.index('old_root="$(dirname -- "${old_data}")"'):source.index('[[ "$(sha256sum "${JV_CONFIG}"')]
with tempfile.TemporaryDirectory(prefix='jv-physical-migration-') as root:
    root=Path(root); old=root/'old'; new=root/'new'
    for parent in (old,new): (parent/'minecraft').mkdir(parents=True)
    (old/'minecraft/legacy.txt').write_text('retained historical data')
    for identity,content in [('jv-aaaaaa','first world'),('jv-bbbbbb','second world')]:
        directory=old/'minecraft-instances'/identity
        (directory/'data').mkdir(parents=True)
        (directory/'data/level.dat').write_text(content)
        (directory/'.provision.json').write_text(identity)
    env=dict(os.environ,old_data=str(old/'minecraft'),new_data=str(new/'minecraft'))
    result=subprocess.run(['bash','-euo','pipefail','-c',
        'emit_progress() { :; }; install() { mkdir -p -m0700 -- "${@: -1}"; }; '+copy],
        env=env,capture_output=True,text=True)
    if result.returncode: raise AssertionError(result.stdout+result.stderr)
    for identity,content in [('jv-aaaaaa','first world'),('jv-bbbbbb','second world')]:
        for parent in (old,new):
            assert (parent/'minecraft-instances'/identity/'data/level.dat').read_text()==content
            assert (parent/'minecraft-instances'/identity/'.provision.json').read_text()==identity
    assert (new/'minecraft/legacy.txt').read_text()=='retained historical data'
print('Physical migration real rsync two-instance copy/verification and source retention passed.')
PY
