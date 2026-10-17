#!/usr/bin/bash
# Source contract only. Does not contact the appliance.
set -euo pipefail
frontend=mjust/libexec/start-over
agent=management/cmd/justvoxel-management-agent/admin_start_over.go
archive=management/cmd/justvoxel-management-agent/admin_start_over_archive.go
retirement=management/cmd/justvoxel-management-agent/minecraft_instance_delete.go
fail() { echo "ERROR: $*" >&2; exit 1; }
for route in 'GET /v1/admin/start-over/plan' 'POST /v1/admin/start-over/apply'; do
    grep -Fq "${route}" "${frontend}" || fail "frontend route missing: ${route}"
    grep -Fq "${route}" "${agent}" || fail "Agent route missing: ${route}"
done
for forbidden in 'systemctl' 'podman ' 'firewall-cmd' 'service stop' 'minecraft-reset-common.sh' 'require_config' 'rm -' 'minecraft.service' 'minecraft-preserved-'; do
    if grep -Fq "${forbidden}" "${frontend}"; then fail "direct runtime frontend operation: ${forbidden}"; fi
done
for required in 'api-client' 'START OVER' 'confirm_players' 'save_configuration_backup' 'total_players_online' 'configuration_backup_path' 'ALL configured Minecraft servers'; do
    grep -Fq "${required}" "${frontend}" || fail "missing frontend contract: ${required}"
done
for required in 'localRootAdministrator(r)' 's.claimMinecraftMutation()' 'planStartOver(ctx)' 'PlanFingerprint != req.PlanFingerprint' 'plan.TotalPlayersOnline > 0 && !req.ConfirmPlayers' 'actionWithWarnings(ctx, j.ID, "stop", req.ConfirmPlayers)' 'retireSelectedInstance(ctx, j.ID, j.Name, "start_over")' '"minecraft_instances"' 'checkStartOverRetirement' 'instanceStorageFor(snapshot, id)' 'b.verifyLive(r)' 'b.factoryResetCoverage(ctx, coverage)'; do
    grep -Fq "${required}" "${agent}" || fail "missing Agent contract: ${required}"
done
for required in 'j.ResetPolicy == "start_over"' 'identity != j.DataIdentity' 'return b.removeInstanceData(ctx, j)'; do
    grep -Fq "${required}" "${retirement}" || fail "missing data policy: ${required}"
done
for required in '.partial' 'partial.Chmod(0600)' 'partial.Chown(0, 0)' 'gzip.NewReader(partial)' 'os.Rename(path, final)' 'dir.Sync()' 'instanceOpen(path,'; do
    grep -Fq "${required}" "${archive}" || fail "missing private archive contract: ${required}"
done
# No auth/user/image/storage-layout writer is allowed in this operation.
if grep -E 'resetAuthentication|invalidate.*[Ss]ession|DELETE FROM|podman.*prune|os.RemoveAll|jv_reset_remove_active_configuration|umount|wipefs|mkfs\.|fstab.*Write' "${agent}" "${archive}"; then
    fail 'Start Over crossed its preservation boundary'
fi
echo 'Start Over instance source contract OK'
