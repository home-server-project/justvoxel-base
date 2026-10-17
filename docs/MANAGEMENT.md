# JustVoxel management model

JustVoxel is designed to be easy to operate as an appliance without hiding the underlying Linux system from administrators who want direct control.

The management interfaces are layers over the same AlmaLinux 10 bootc host and Minecraft runtime. None of them turns JustVoxel into a closed control plane.

## Management surfaces

JustVoxel can be administered in four ways:

1. **WebUI** — browser-based management for users who prefer a graphical interface.
2. **Interactive `mjust` menu** — terminal UI with descriptions, selectors, and guarded workflows.
3. **Direct `mjust` commands** — the same operations can be called directly without opening the menu.
4. **Native Linux administration** — advanced users can use standard tools such as `systemctl`, `journalctl`, `podman`, `bootc`, `nmcli`, `firewall-cmd`, `findmnt`, and `lsblk` directly over SSH or the local console.

A typical supported appliance relationship is:

```text
WebUI --------\
               -> JustVoxel Management API -> Management Agent -> shared appliance implementation
mJust --------/
                                                         |
                              systemd / Podman / bootc / NetworkManager / firewalld
                                                         |
                                              AlmaLinux 10 bootc host
```

The Management API and Agent are the authoritative appliance-management layer
for migrated capabilities. WebUI and mJust are separate user interfaces over
that same implementation rather than separate management engines.

This diagram describes the normal supported appliance flow, not a restriction.
An administrator may still use appropriate native Linux tools directly for
inspection, troubleshooting, or deliberate advanced administration.

## `mjust` is a convenience and safety layer

Running `mjust` with no arguments opens the interactive JustVoxel terminal interface. The menu explains operations and dispatches the same direct recipes that are available from the shell.

Examples:

```text
mjust status
mjust players
mjust backup
mjust restore
mjust update-minecraft
mjust os-status
mjust os-update
mjust resources
mjust validate
```

The interactive menu and direct `mjust` commands are not separate appliance
implementations. For capabilities migrated to the Management API, direct
`mjust` commands are thin terminal frontends over the same Management Agent
used by WebUI. They remain suitable entry points for normal SSH administration,
documentation, automation, and troubleshooting, but they do not own duplicate
business logic or safety policy.

Administrator-only `GET /v1/admin/validation` combines the default host-only
`validate-backend` result with independent checks of every registered modern
immutable instance. Registry and committed runtime coverage must agree; retained
`minecraft.json` compatibility identity is excluded. Zero modern instances is
valid. Stopped servers are structurally checked without starting them; running
servers receive `verify-minecraft-stack --instance <id> <version>`. Storage,
SELinux contexts, owned firewall rules and backup settings/status/timer/destination
checks are read-only. The appliance-wide deadline is ten minutes. Expected
problems return HTTP 200 with `ok=false` and `exit_code=1`; infrastructure failures
return sanitized HTTP 503. Historical `validate-backend --first-run` recovery
keeps its fixed-runtime contract and is never part of normal validation.

## Native Linux administration remains available

JustVoxel is still a normal immutable Linux server.

Advanced administrators may inspect or operate the machine directly with normal system tools. Examples include:

```text
systemctl status minecraft.service
journalctl -u minecraft.service
podman ps
podman image ls
bootc status
nmcli
firewall-cmd --list-all
findmnt
lsblk
```

JustVoxel does not require administrators to enter a proprietary shell and does not disable ordinary host administration.

The supported host-management model is bootc. `rpm-ostree` may exist in build plumbing, but JustVoxel does not use host-side rpm-ostree layering as its administration model.

## Configuration ownership

The bootc image owns immutable implementation and templates under locations such as:

- `/usr/share/justvoxel/`
- `/usr/libexec/justvoxel/`
- `/usr/lib/justvoxel/`

Administrator/runtime configuration lives under `/etc`, including:

- `/etc/justvoxel/justvoxel.conf`
- `/etc/justvoxel/minecraft.env`
- `/etc/justvoxel/minecraft-backup.env`
- `/etc/containers/systemd/minecraft.container`
- `/etc/systemd/system/minecraft-backup.service`
- `/etc/systemd/system/minecraft-backup.timer`

Persistent application and management state lives under `/var`.

Management journals and human-readable diagnostic logs use one persistent hierarchy:

```text
/var/lib/justvoxel/management/
├── logs/
│   ├── operations/<operation-id>.json
│   ├── setup-logs/<operation-id>.log
│   ├── migration-logs/<operation-id>.log
│   ├── restore-logs/<operation-id>.log
│   ├── reset-logs/<operation-id>.log
│   └── support/
├── setup.lock
├── restore.lock
├── data-migration.lock
└── server-migration.lock
```

Operation journals remain machine-readable metadata in `operations/`; System → Logs does not list their raw JSON. Setup logs use `setup-logs/`. Data Migration, Server Migration Export, Import, and Recovery use `migration-logs/`. Restore uses `restore-logs/`. Minecraft and factory resets use `reset-logs/`. Administrators can generate on-demand local support reports through `POST /v1/admin/logs/support`. Reports remain in the private `logs/support/<uuid>.log` directory until normal appliance log/state cleanup removes them, and use the existing diagnostic list/read/download API without an operation journal. Each report is capped at 256 KiB and includes release metadata, fixed Management/WebUI service properties, socket and appliance path metadata, up to 300 Management and 150 WebUI current-boot journal lines, and a metadata-only management inventory (depth 2, at most 200 entries, no symlink traversal). Command sections are capped at 64 KiB, lines at 2048 bytes, and commands at four seconds each. Output is sanitized line by line using Setup hostname/IP redaction and sensitive-family redaction; configuration and environment contents are excluded. Reports are root-owned regular files with mode 0600 in the existing private directory. Nothing is automatically uploaded. Logs and journals remain root-owned, with private directories and files. Server Import records bounded, redacted backend output alongside its lifecycle and rollback evidence.

Administrators can select Setup, Migration, Restore, Reset, or Support in System → Logs, then read and download a file through the Management API's allowlisted `/v1/admin/logs` endpoints. The WebUI does not accept filesystem paths or send reports externally.

First-run Setup (Recommended and Advanced) and both Factory Reset progress views offer **Live Console**, a movable, non-modal, read-only redacted execution trace of supported operations (`setup`, `minecraft_reset`, and `factory_reset`). While open, it refreshes about every 2.5 seconds. Closing it stops reads; reopening reads a fresh copy. Scrolling up pauses automatic following until you return near the bottom. Temporary read failures preserve the displayed transcript and show **Reconnecting…**. The console starts closed after a page reload. Setup completion leaves it open for inspection; Open Dashboard and successful resets close it. The Administrator-only trace is separate from System → Logs, which retains persistent forensic diagnostics and Setup Log downloads. Trace commands come only from authoritative Agent/helper execution. The Agent sanitizes and bounds inherited-FD trace records before storing them privately; secrets, stdin/request JSON, and environment contents are never exposed. Live Console has no command input and provides no shell.

System → Logs → Failed units is a live diagnostic view, separate from persistent operation logs. Refresh re-queries the current failed units. Selecting a unit reads its last 60 journal lines from the current boot; Download log retrieves the same bounded view. Only administrators can use this view, and the Management Agent verifies that the unit is still failed before reading its journal. Enumeration failures appear as unavailable rather than an empty list.

The System → Date & Time tab changes the host timezone, automatic time synchronization, and (when automatic time is off) the system clock. Its administrator-only WebUI route uses CSRF protection and calls the Management Agent over the local Unix socket. The agent validates the timezone against the host database and passes fixed arguments to `timedatectl`. The city search uses the same local timezone list as First Setup. The same Administrator-only change route saves the appliance-wide 12/24-hour and date-order display profile in the existing WebUI database. An additive singleton-profile migration preserves existing installations and defaults to 12-hour time and Month first. Every authenticated WebUI role receives only the two display format values through the shared session response; changing display format does not change the host clock.

Minecraft Settings contains Minecraft memory, players, whitelist, and cross-play controls. The separate Version workspace shows the current Paper implementation and container channel under Server Software, and installed/available Minecraft versions, policy, and Paper/Geyser compatibility under Minecraft Version. Purpur and Vanilla remain disabled placeholders. Version changes use the existing configuration Review → Apply path; incompatible cross-play remains enabled and blocks the proposed version change until the administrator changes the configuration explicitly.

On Management Agent startup, legacy `management/operations/` and `management/setup-logs/` directories are migrated into the `logs/` hierarchy without overwriting existing data. Existing `logs/operation-logs/<operation-type>/` files are moved into the matching category directory after bounded preflight checks. Conflicting non-empty directories or file names stop startup rather than overwrite logs.

Bootc image updates may update the immutable implementation, but JustVoxel does not silently replace the active administrator configuration in `/etc` as part of an ordinary image update.

## Manual edits and generated files

Advanced users are free to inspect and manually edit the host, including the rendered Minecraft Quadlet. However, some active files are generated by JustVoxel from its recorded configuration.

For example:

```text
/etc/containers/systemd/minecraft.container
```

may be regenerated by operations such as `mjust configure`, storage migration, or another workflow that intentionally renders the runtime again.

Therefore:

- direct Linux administration is supported;
- manual inspection and troubleshooting are supported;
- manual customization is possible;
- arbitrary edits to files owned by a JustVoxel render workflow are not guaranteed to survive a later re-render.

Administrators who intentionally diverge from the supported generated configuration should treat those changes as their own advanced configuration management responsibility.

## Safety lives below the frontends

Important appliance safety checks belong behind the Management API in the
Management Agent and shared backend implementation rather than only in the
interactive menu, direct mJust command, or WebUI. Calling a direct mJust command
does not bypass the intended protections.

Examples include:

- player-aware interruption checks;
- graceful Minecraft shutdown;
- the shared Minecraft maintenance lock;
- backup mount/source identity validation;
- exact typed confirmations for destructive storage actions;
- exact `RESTORE` confirmation for restore operations;
- restore staging and pre-restore safety copies;
- SELinux relabeling and runtime validation;
- fail-closed behavior when critical state cannot be confirmed.

WebUI and mJust should call the same management capabilities rather than create
independent definitions of appliance behavior.

## Web management architecture

The browser-facing WebUI is intentionally unprivileged. It communicates with the privileged JustVoxel Management Agent over a local Unix socket. The WebUI process does not read `/etc/shadow` and does not call PAM directly.

The Management API is versioned as `v1`. Its default authentication provider is the AlmaLinux/RHEL system account. The administrator identity is `voxel`.

In default **System account** mode, the trust path is:

```text
Browser
  |
Unprivileged JustVoxel WebUI
  |
Unix socket
  |
Privileged JustVoxel Management Agent
  |
PAM / authselect-managed AlmaLinux system stack
  |
local voxel account
```

The Management Agent authenticates the submitted credential once through PAM, detects an expired password through PAM account management, and then issues the normal JustVoxel WebUI session token. Normal WebUI requests use the session token rather than repeatedly transmitting the system password.

System password changes are performed through PAM and change the real Linux `voxel` credential. A successful password change invalidates active WebUI sessions.

The optional **Separate WebUI password** provider uses a WebUI-local Argon2 credential for the `voxel` browser identity. It is a separate provider, not password synchronization. Switching providers requires confirmation with the real system password and invalidates all WebUI sessions.

Persistent WebUI authentication state is stored under `/var/lib/justvoxel/webui`. A missing authentication-mode state means System account mode, which keeps ordinary bootc updates/rebases safe for already-configured machines.

Full Factory Reset is appliance-wide: it retires every authoritative managed Minecraft instance, including stopped instances, regardless of the selected server. Review binds the complete instance set; a changed scope requires another review. Running instances use player-aware graceful shutdown. Internal managed worlds and internal configured backups are deleted; external/network data and unrelated storage remain preserved. Interrupted instance cleanup retains private identity evidence for Review and retry; a partially erased runtime cannot be dismissed as usable current state. Legacy interrupted resets without ownership evidence do not guess a server for destructive retry. Start Over is a separate terminal workflow and is not this operation. See [the Factory Reset convergence audit](MINECRAFT-CONVERGENCE-C3-FACTORY-RESET.md) for its exact destructive and recovery boundaries.

Full Factory Reset removes appliance-owned state and restores System account authentication. It resets the real Linux `voxel` account to the same `voxel` / `voxel` bootstrap credential used by the installer and expires that password immediately. A successful reset invalidates existing WebUI sessions and clears authentication failure and lock state. The next authentication with `voxel` / `voxel` requires the administrator to replace the bootstrap password through the existing forced password-change flow. The replacement becomes the real system-wide `voxel` password for WebUI, console, and SSH password authentication when enabled.

Web management can be enabled, disabled, inspected, and recovered through:

```text
mjust web status
mjust web enable
mjust web disable
mjust web password-reset
```

`mjust web password-reset` is provider-aware: System account mode uses the normal Linux `voxel` password recovery path; Separate mode resets only the local WebUI credential.

The default WebUI is intended for trusted local-network administration. See [WEBUI.md](WEBUI.md) for the current access and security model.

## Choosing an interface

For a family/home user, the WebUI or interactive `mjust` menu should usually be the simplest path.

For an administrator who knows the command they want, direct `mjust` commands avoid the menu while keeping JustVoxel's safety model.

For Linux troubleshooting or deliberate customization, normal host tools remain available.

The design goal is simple: **JustVoxel behaves like an appliance when that is useful, and like a normal immutable Linux server when an administrator needs deeper control.**

### Appliance-wide physical data migration

Physical migration stays under `/v1/admin/data-migration` (discover, plan, apply,
current-operation) and the existing persistent-operation status endpoints. It has
no selected-instance route or browser-supplied unit. The Plan fingerprint commits
the complete authoritative participant set, its runtime configuration identities,
and original runtime states. Root-only `data-migration-runtime` Agent commands
reuse the instance registry/runtime implementation inside the storage transaction.

`POST /v1/admin/data-migration/recover` accepts only `operation_id` and explicit
`confirm_recovery: true`. It retries rollback for the current `needs_attention`
operation using private immutable participant evidence. Legacy identity-less
journals remain read-only. [Storage](STORAGE.md#minecraft-data-migration) describes
state restoration, startup protection, and recovery behavior.
