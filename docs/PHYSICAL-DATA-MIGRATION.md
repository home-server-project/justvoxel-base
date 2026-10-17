# Physical data migration convergence

Physical migration is appliance-wide. It moves the physical data/storage root;
selected-server Restore and portable server export/import are separate operations.

## Liveness and retained boundaries

| Area | Classification | Physical migration behavior |
| --- | --- | --- |
| `admin-data-migration-plan-json`, `admin-data-migration-transaction-json` | Production live | Target discovery/planning, storage preparation, tree copy/verification, switch, progress, rollback orchestration |
| Management `admin_data_migration_*`, `data_migration_worker.go`, operation store | Production live | Administrator authorization, strict decoding, reviewed fingerprint, durable operation claim, progress/status/reconnect |
| `data_migration_runtime.go` | Production live | Existing registry/runtime inventory and renderer; per-ID player checks, lifecycle, cold backups, relocation evidence and recovery |
| WebUI `storage_minecraft_migration.go`, data migration orchestration, Storage browser template/JS | Production live | Appliance-wide thin frontend; review, confirmations, progress, reconnect, dashboard attention remain unchanged |
| `mjust storage-migrate`, `data-migration`, API adapter, storage-provision/menu entries | Production live | Same appliance-wide API; confirmed modern recovery retry |
| `minecraft.json` reserved identity | Compatibility only | Registry validation retains it; authoritative renderer excludes it from independent runtimes. Shared backup workers still inspect it to reject legacy identity adoption |
| Fixed `minecraft.service` / `minecraft` inspection in the new runtime protocol | Safety inspection only | Refuses active fixed service or a fixed container. Never starts, stops or selects it |
| Old physical migration journals without participants | Read-only recovery compatibility | Retained operation/status/attention and private evidence; never infer a current owner |
| `backup-common.sh`, `backup-instance-storage.sh`, `instance-backup`, independent cold-backup helper | Shared live helpers | Reused per immutable ID; legacy defaults in shared files are not physical migration lifecycle fallbacks |
| `restore-common.sh`, `restore-archive` | Shared live helpers | Import/layout/archive callers remain; unrelated Restore behavior is unchanged |
| `restore-runtime-validate` | Legacy compatibility code | No physical migration caller remains; fixed-runtime implementation retained, not repurposed as an appliance validator |
| `interrupt-safety.sh`, global `web-status-json players` | Shared/legacy code outside this slice | No physical migration caller remains; instance-aware Go player shutdown handles migration |
| Fixed `render_runtime`, `apply_data_selinux`, fixed `minecraft-backup --leave-stopped` | Retired physical migration execution paths | Replaced only in physical migration. Shared definitions and unrelated callers remain untouched |
| Server export/import/recovery workspace and helpers | Separate live operation | Selected-instance behavior, logs/downloads, staging and recovery are outside this physical migration change |

The audit included repository-wide callers, tests, documentation, operation-store
restart behavior, storage policy derivation, instance records, runtime commitments,
lock paths, and prior physical migration history (`d704f5c`, `94db6eb`, `8845f23`).
Historical fixed-runtime references in `MINECRAFT-CONVERGENCE-C2.md` describe that
older audit, not the converged execution path.

## Durable evidence and interruption

The root-private `management/data-migration-<operation-id>` directory holds:

- `justvoxel.conf.before`: original appliance configuration;
- `state.json` v2: exact operation ID, participant set and old/new storage policy;
- `runtime.json` schema 1: immutable participants and original runtime state,
  preserved committed records/environment/Quadlet bytes, and exact target bytes
  before publication.

Runtime evidence contains credentials and is never returned in public API payloads.
Publication uses the existing private atomic write/fsync implementation. Participant
startup gates and live PID/start-time permits use a separate migration schema;
they do not reinterpret activation or Restore journals. A reboot loses the live
permit and leaves incomplete publication inhibited.

Review/Apply compare every participant ID, unit/container derivation, state and
configuration hash. A sibling added, removed, reconfigured, or moved cannot silently
escape the operation. Registry sharing prevents membership publication during the
transaction. The maintenance and Restore exclusion locks prevent competing writes;
setup/Restore/server-migration claims remain held while maintenance is temporarily
released for generated-unit startup prerequisites.

Rollback accepts each live runtime file only when it matches its exact preserved
old or target bytes. This bounded mixed-publication compatibility permits recovery
after interruption between file renames without adopting unrelated state. Original
configuration must also match the preserved version or the exact storage-only
switch. Recovery restarts only journal-recorded running IDs and verifies siblings
individually. Failed validation retains evidence for another supported retry.

A historical v1 `state.json` does not record instance ownership or original states.
It cannot safely become a new participant journal. No current selection, first
server, global container, or reconstructed identity is used to bridge this boundary.

## Verification boundary

Source/model tests exercise zero, one and two participants, running/stopped state
combinations, stale identities and siblings, fixed-runtime refusal, partial runtime
publication rollback, operation-bound recovery, and legacy read-only refusal.
Source contracts cover appliance-wide API scope, copying the complete instance
tree, lock/exclusion use, durable state and retirement of fixed execution callers.
They do not prove real mount, disk provisioning, boot, Podman, player countdown,
SELinux, RCON readiness or rendered WebUI acceptance. Those require human VM
validation; the WebUI assets and templates are unchanged in this slice.
