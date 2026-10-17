# Minecraft Restore

Restore replaces the complete persistent data of one selected Minecraft server:
worlds, player data, plugins, plugin data, server.properties and whitelist/ops.
It preserves the instance identity, appliance storage policy, runtime definition,
container channel, Minecraft version policy and operating-system deployment.

Select the server in the WebUI Backups workspace, or use:

```text
mjust restore jv-a1b2c3
```

`mjust restore-full` is an alias. An omitted ID is accepted only when exactly
one actual registered server exists. Zero or multiple servers require a choice.
Friendly names are presentation metadata and cannot select a backend target.
Legacy world-only Restore is not an instance Restore mode.

The Management Agent owns planning, archive verification, player safety, staging,
data switching, runtime verification and recovery. Both frontends use
`/v1/minecraft/instances/{id}/backups/restore/...`. Archives and metadata must
belong to that exact immutable ID. Renaming a server preserves its backup
identity; cross-instance Restore is refused.

Review the selected archive and current/backup versions before applying. Restore
requires deliberate destructive confirmation and, when necessary, confirmation
of online-player interruption. Planning and Apply bind the same reviewed plan;
a changed or stale plan requires a new explicit review.

The existing instance transaction validates mount identity, metadata, integrity,
archive layout, compatibility, staging space and stopped/runtime safety. It
protects the previous data until the restored runtime passes verification.
Failure retains durable recovery evidence; it never switches to another server
or uses Factory Reset or Minecraft Reset as a substitute for Restore Recovery.

Reopening Backups or rerunning `mjust restore <same-instance-id>` reconnects to
that instance’s persistent Restore state. If recovery is required, use Retry
Restore Recovery, or confirm recovery in the terminal command. After Apply or
Recovery is accepted, Backups shows live progress automatically.
The browser polls `/workspace/backups/restore/status?server=<immutable-id>`;
the WebUI verifies the selected identity against the returned instance ID.
Progress shows completion, verified rollback, or administrator attention without
exposing transaction evidence. Polling does not retry Apply or re-plan.
Administrators also receive a selected-server Review Restore dashboard link
when that instance requires recovery. Legacy global notices remain informational.

The legacy global execution endpoints (`GET /v1/admin/restore/backups`,
`POST /v1/admin/restore/plan`, `POST /v1/admin/restore/apply`) and their
fixed-server workers are retired without translation to an instance.

Previously deployed global operation journals, Restore locks, diagnostic logs,
and the read-only `/v1/admin/restore/current-operation` endpoint remain for
upgrade compatibility. Interrupted legacy journals still block conflicting
operations and remain visible as dashboard attention. They cannot safely be
translated to selected-instance recovery because they lack an immutable target
ID. TODO: provide legacy recovery only when original ownership can be proven;
this retirement does not invent identity, discard evidence, or release locks.
Selected Restore recovery continues through the same instance ID.

Shared `backup-common.sh`, `restore-common.sh`, `restore-archive`, and
`restore-runtime-validate` remain. Physical data migration still invokes the
runtime validator; migration import still uses Restore layout validation.
