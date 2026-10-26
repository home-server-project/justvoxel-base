# C3: appliance-wide Factory Reset

> Historical convergence record. This document preserves the C3 implementation
> and audit boundary; its “remaining references” section is not the current
> roadmap. Current architecture and user-visible reset behavior are described in
> [ARCHITECTURE.md](ARCHITECTURE.md), [WEBUI.md](WEBUI.md), and
> [ROADMAP.md](ROADMAP.md).

This slice changes Factory Reset's Minecraft runtime ownership only. The routes
remain `/v1/admin/reset/factory/{plan,apply,resolve}` and
`/v1/admin/reset/factory/current-operation`. No selected-instance Factory Reset
route exists. No image build or appliance runtime acceptance is claimed.

## Existing destructive boundary

The pre-change `admin_factory_reset.go` and `admin-factory-reset-json`, including
history through `7bafd7f`, `2438d3a`, `014a351`, and `d24f015`, implement:

- Graceful, player-aware interruption before runtime cleanup.
- Removal of Minecraft runtime/configuration, previous-image state, and the
  historical fixed Minecraft identity after verification.
- Deletion of the contents of the configured Minecraft data directory and backup
  destination **only when classified as internal storage**. The common remover
  rejects symlink roots and nested mounts and uses a single-filesystem traversal.
- Preservation of external, USB, network, and unknown-scope data/backup contents.
- Deletion of local JustVoxel configuration-backup contents.
- Retirement of JustVoxel-owned storage mount records and matching fstab entries,
  with source/UUID checks; partitions and filesystems are preserved. Unrelated
  administrator fstab entries are preserved.
- Removal of `/etc/justvoxel`, the old Quadlet/backup units and active Minecraft
  firewall openings. Host NetworkManager/Tailscale, SSH and OS configuration are
  not reset.
- Deletion of WebUI users, notifications, audit history and operator usage;
  reset of operator allowance timestamps and system-monitor preferences.
- Return to System authentication, restoration and immediate expiration of the
  factory `voxel` credential, then session/failure/lock-state invalidation after
  successful verification.

It does not erase whole disks, prune Podman images or unrelated containers, wipe
arbitrary storage roots, or erase migration import/export/transport state. An
internal configured backup destination is already destructive scope: that scope
is retained, rather than changing Factory Reset into backup preservation.

## Liveness audit and ownership classification

| Exact source | Classification and behavior |
| --- | --- |
| `management/.../admin_factory_reset.go` | Production live: administrator/local-root authorization, separate-password proof, Review/Apply fingerprint, persistent worker, identity reset, verification, completion and keep-current-state resolution. |
| `management/.../admin_operations.go`, `operation_store.go` | Production live/shared operation framework: Factory Reset current-operation/status, mutually exclusive Setup/Restore/data-migration/server-migration/reset claims, retry and interrupted `needs_attention` journals. Other operation implementations are unchanged. |
| `mjust/libexec/admin-factory-reset-json` | Production live Agent subprocess. Shared appliance storage cleanup and compatibility cleanup of historical Minecraft artifacts. It is not a terminal Factory Reset frontend. |
| `webui/internal/api/reset.go`, `persistent_operations.go` | Production live: appliance-wide client contracts; selected Minecraft Reset has separate routing. |
| `webui/internal/server/system_workspace.go` | Production live: Factory Reset Review/Apply/current/progress/resolve; does not resolve a selected Minecraft instance for these actions. |
| `webui/internal/server/static/app.js`, `templates/header.html`, `factory_reset_complete.html`, `factory_reset_complete.go` | Production live, unchanged presentation: Review, destructive slider/confirmation, warnings, player confirmation, progress/Live Console, retry, keep-current-state, factory bootstrap completion. Navigation selection does not change participants. |
| `webui/internal/server/server.go:dashboardCurrentAttention` | Production live: Factory Reset `needs_attention` is appliance-wide and survives selected-server context. |
| `mjust/justfile`, `mjust/libexec/menu` | Production live: invoke Start Over, not Factory Reset. No mjust Factory Reset frontend was found. |
| `mjust/libexec/start-over` | Separate live feature: preserves worlds/backups and optionally archives configuration or preserves/renames the stopped legacy container. Still uses fixed runtime lifecycle and shared cleanup; out of this slice. |
| `mjust/libexec/minecraft-reset-common.sh` | Shared with Start Over and legacy Setup recovery. Its fixed container remover remains used by `admin-setup-runtime-transaction-json`; Factory Reset no longer calls either fixed runtime-removal function. Path classification/safe historical storage deletion remain shared. |
| `minecraft_instance.go`, `minecraft_instance_registry.go` | Authoritative immutable registry under `/var/lib/justvoxel/instances`; duplicate/conflicting identity validation. Historical `minecraft.json` reserves the fixed identity; it is not adopted as an active participant. |
| `minecraft_instance_activation.go`, `minecraft_instance_lifecycle.go`, `minecraft_instance_delete.go` | Authoritative committed configuration under `/etc/justvoxel/instances`, per-ID Quadlets, lifecycle observations/player warnings, firewall ownership, backup-unit retirement, inode-pinned data removal and identity-last retirement. Shared primitives are reused without changing selected Delete/Reset behavior. |
| `instance-runtime-prerequisites`, `instance-delete` | Shared safe host-mount/storage primitives. Per-instance data is `<storage-root>/minecraft-instances/<id>/data`. Provisioning markers are transient and removed by successful provisioning. Unrelated parent-directory contents are not recursively deleted. |
| `minecraft_instance_backups.go`, `minecraft_instance_restore.go` | Shared backup settings/status, native backup jobs and Restore journals; active/pending jobs and nonterminal Restore evidence prevent destructive reset. Terminal Restore evidence is retired with its instance. |
| `data_migration_runtime.go` | Shared authoritative registry inventory and legacy inactivity inspection. `data-migration.json` in an instance directory prevents reset; persistent operation locks also exclude unresolved physical/server migration and Restore. |
| `validate-instance-runtime-commit` | Shared startup and native backup validation. Added only the Factory Reset marker gate. Setup and its recovery semantics are unchanged. |

## New authority and persistent behavior

`factory_reset_instances.go` builds a sorted, complete participant set from the
immutable registry and committed runtime records. Exact set equality, duplicate
checks, committed file hashes, storage/mount identity, directory inode evidence,
backup-idle checks and runtime inspection are required. Orphan managed Quadlets,
containers, active services and backup artifacts cause refusal. Zero participants
is explicitly valid; it performs no invented Minecraft lifecycle operation.
Loaded per-instance backup timers are inventoried independently of unit files,
including during empty-runtime proof, so an orphan timer cannot escape coverage
when its on-disk unit has disappeared.

Review hashes all participant records, policies, data identities and original
states into the appliance fingerprint. Neither private records nor credentials,
data identities or per-instance private paths are returned in that response.
Apply recomputes Review; a changed sibling invalidates it. Shared storage cleanup
policy is also bound through the original helper-plan fingerprint,
even when the public data scope/action is summarized from managed instances.
Shared configured storage paths cannot erase the registry, recovery state or other protected
configuration trees.

The existing persistent operation claims and the maintenance/Restore-exclusion
flocks cover the worker. Before any Minecraft mutation, a private
`factory-reset-<operation-id>.json` captures every participant. Every participant
gets an operation/ID-specific `factory-reset.json` startup/backup gate before any
shutdown. Running instances use `actionWithWarnings(..., "stop", true)`; stopped
siblings are never started. All are quiesced before per-instance destruction.

For each pinned participant, the shared retirement stages disable its own backup
timer, remove only owned firewall rules, remove its exact stopped container,
remove its environment/Quadlet/backup units, reload systemd, apply its internal
`delete` or external/network/unknown `preserve` data policy, then retire runtime
state and immutable registration. Identity is last. Unknown directory entries,
changed identities/bytes, or uninspectable state refuse cleanup. Historical name
records and unrelated storage children remain untouched, as in the existing
identity/retirement model.

Stages are durable and idempotent across missing files caused by that exact
retirement. Retry validates surviving evidence against the original complete
set, including already-retired siblings; no fixed server is recreated or guessed.
The shell refuses Apply without the Agent handoff and independently proves an
empty managed runtime before and after appliance cleanup. Success additionally
requires first-use/authentication/user verification and an empty managed set.

Failure remains `needs_attention`. Review-and-retry is supported for new
identity-bearing evidence. Legacy interrupted journals without that evidence
remain fail-closed for destructive retry and retain informational/keep-current-
state resolution. For new operations, keep-current-state releases gates only
when every original runtime is intact; it cannot discard the sole recovery path
for partially erased Minecraft state. No automatic rollback or world recreation
is introduced.

## Remaining fixed-runtime references

| Reference | Classification |
| --- | --- |
| `data_migration_runtime.go:migrationFixedRuntimeInactive`: `minecraft.service` and `name=^minecraft$` | Compatibility cleanup preconditions, inspection only. A live/unknown fixed runtime refuses reset; neither is stopped, started, removed or used as a fallback. Also shared with physical migration. |
| `factory_reset_instances.go:factoryResetCoverage`: fixed `minecraft-backup.service`/`.timer` | Compatibility cleanup preconditions: both must already be inactive. Native per-instance backup jobs remain the active authority. |
| `admin-factory-reset-json`: `systemctl disable -- minecraft-backup.timer` | Optional historical enablement cleanup only, after verified inactivity; no `--now`, stop or start. |
| `admin-factory-reset-json`: `JV_QUADLET`, `JV_BACKUP_SERVICE`, `JV_BACKUP_TIMER`, `JV_PREVIOUS_IMAGE_STATE`, configuration directory | Compatibility file cleanup only. Constants in `common.sh` map to the historical fixed Quadlet, backup units and global environment files. No fixed environment is loaded as a server runtime. |
| `admin-factory-reset-json`: historical `DATA_PATH` and shared `BACKUP_PATH` from `justvoxel.conf` | Historical-data cleanup and shared appliance storage policy, respectively. Historical port openings are also removed as compatibility cleanup. The global server type/ports, environment and Quadlet do not select participants or control lifecycle. |
| `factory_reset_instances.go`: `minecraft.env` | Per-instance committed file, under that immutable ID's runtime directory. Not a global environment fallback. |
| `minecraft-reset-common.sh`, `start-over`, legacy Setup recovery, global status/service/backup helpers | Unrelated live subsystems/shared compatibility code; no longer Factory Reset execution dependencies for fixed lifecycle. Retained deliberately. |
| Tests and `docs/MINECRAFT-CONVERGENCE-C2.md` scan snapshots | Tests/docs, including historical audit snapshots. |

No remaining fixed Minecraft lifecycle target was found in the Factory Reset
execution path. Start Over still needs a separate approved convergence of its
fixed service/container, keep-container override, configuration archive paths and
shared fixed-runtime cleanup. It is not a Factory Reset caller, and no Start Over
implementation was changed here.

## Verification boundary

Source tests exercise mocked systemd/Podman commands and real temporary-file
ownership boundaries. They do not prove boot, real mount transitions, native
systemd job behavior, player countdown timing, runtime recovery or visual
acceptance. Those remain human development-VM checks. No build, deployment,
commit, push or GitHub operation is part of this work.
