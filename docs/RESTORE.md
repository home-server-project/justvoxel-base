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
Restore Recovery, or confirm recovery in the terminal command. Progress polling
does not automatically retry Apply or re-plan the operation.

Legacy fixed-server Restore helpers remain in the repository for later
convergence and deployed-state compatibility. They are not the active normal
instance Restore path.
