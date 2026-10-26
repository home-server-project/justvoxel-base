# mjust user and administrator guide

`mjust` is JustVoxel's built-in administration interface.

It is designed for people who are comfortable installing an operating system and following normal computer instructions, but who should not need deep Linux, container, systemd, bootc, SELinux, firewall, storage, or Minecraft-server administration knowledge just to run a family Minecraft appliance.

Run `mjust` with no arguments to open the interactive terminal interface. Choose the task you want, read the explanation, and follow the prompts. The same operations are also available as direct `mjust` commands for experienced users, documentation, automation, and troubleshooting.

The interaction model is inspired by Universal Blue's `ujust` / `ugum` work in `ublue-os/packages`, but JustVoxel uses its own server-focused implementation.

JustVoxel remains a normal immutable AlmaLinux server underneath. Advanced administrators can still use normal Linux tools directly when they want deeper control.

> JustVoxel is still under active development and remains under active validation before stable promotion. The unified Management API / mJust architecture and WebUI parity work are developed and validated on `testing`.

## How mjust is organized

The normal interactive interface groups appliance tasks by what the user wants to accomplish rather than by the Linux commands underneath.

The main areas are:

- first setup and per-instance Minecraft configuration
- appliance status and health
- Minecraft start, stop, and restart
- players and whitelist management
- backups and restore
- storage and Minecraft data migration
- Minecraft software, version, and container updates
- operating-system maintenance
- live system resources
- terminal file management
- friendly and advanced NetworkManager configuration
- validation and logs
- advanced/reset tools

Safety checks live in the command/workflow layer, not only in the menu. Using a direct command does not bypass player checks, backup validation, storage protection, typed confirmations, SELinux handling, or other JustVoxel safeguards.

## First setup

`mjust setup` is the normal first-run wizard and creates the first registered
Minecraft instance. Additional servers are created through the WebUI's **Add
Minecraft Server** workflow.

It collects the settings needed to bring the appliance online, including server
software, Minecraft data location, applicable memory and ports, maximum players,
server message, container-image and game-version policy, backup target and
schedule, retention, and Minecraft EULA acceptance. The current choices are Paper,
Purpur, Spigot, Fabric, NeoForge, Forge, Quilt, Vanilla Java, and native Bedrock;
their available controls differ by capability.

The normal flow tries to keep Linux-specific details out of the way. Memory values are suggested from installed physical RAM, but the user can change them.

Setup refuses to silently overwrite an already-configured JustVoxel installation.

## Configure an installed server

`mjust configure [--instance jv-...] [backup]` configures a registered Minecraft server. `mjust configure-max-players [--instance jv-...]` opens the focused player/RAM guidance screen. Omit the ID only when exactly one server is registered; zero or multiple servers require resolving that selection first. Friendly names are never identities. The selected immutable ID stays the same through submenus and refreshes.

Ordinary settings (memory, ports, Bedrock cross-play, MOTD and maximum players) use that server's complete public runtime configuration. Stop the selected server through normal lifecycle controls before applying settings, then start it normally when ready. Configure does not stop or restart it automatically.

Automatic backup settings use the same selected server's backup API: daily time (HH:MM), enabled state and retention. `backup` opens this screen directly. These settings are separate from runtime configuration.

The Version/image entry delegates to the existing `update-minecraft --instance jv-...` workflow for the selected server's configured policy; Configure no longer edits image/version policy directly. The storage entry delegates to `storage-migrate`'s existing data-migration frontend. Physical data migration is appliance-wide, covers all managed servers, and does not take a selected instance ID.

## Status and health

`mjust status` is the normal user-facing appliance dashboard.

It presents host health, network and system storage, then summarizes every registered Minecraft server by display name and immutable ID. Each server has its own runtime, players, software/version, addresses, configured memory, start-on-boot, image tag and backup summary. Zero servers do not make the appliance unhealthy; missing or inconsistent instance evidence needs attention.

`mjust status --details` shows the same friendly summary first, then bounded host failed-service, system storage and network detail, plus additional public per-server configuration and backup information. Status uses the Management API throughout and does not select a first, sole or default server.

`mjust validate` is different from status. Validation is the stricter correctness check used to confirm that the deployed appliance matches its recorded configuration and expected runtime state.

See `STATUS.md` for the full status model.

## Minecraft service control

The normal commands are:

- `mjust start [instance_id]`
- `mjust stop [instance_id]`
- `mjust restart [instance_id]`

When exactly one server is registered, the ID may be omitted. With multiple
servers, choose or provide the immutable `jv-…` ID; a friendly name is never used
as the backend target.

Stop and restart are player-aware. When Minecraft is running, JustVoxel checks players through internal RCON before interruption. If player state cannot be confirmed, the operation fails closed instead of guessing.

When no players are online, JustVoxel skips the container's fixed 60-second announcement delay and completes the graceful stop immediately. When players are online and the interruption is approved, JustVoxel keeps the 60-second grace period and sends in-game countdown notices at 60, 30, 15, 10, 5, 4, 3, 2, and 1 seconds before shutdown.

The same adaptive shutdown path is reused by host reboot/poweroff and disruptive Minecraft maintenance so those workflows do not independently implement player timing.

## Players and whitelist

`mjust players [instance_id]` shows the selected server’s player counts through the Management API. Player names are shown only when supplied by that instance API.

Whitelist management has separate Java and Bedrock/Floodgate operations:

- `mjust whitelist-list`
- `mjust whitelist-add-java <name>`
- `mjust whitelist-remove-java <name>`
- `mjust whitelist-add-bedrock <gamertag>`
- `mjust whitelist-remove-bedrock <gamertag>`

These commands accept an optional immutable instance ID after their other arguments, for example `mjust whitelist-add-java Alex jv-a1b2c3`. An omitted ID resolves only when exactly one registered server exists. With zero or multiple servers, the command requires an explicit server choice. Names never select a backend target.

RCON remains internal to the Minecraft container and is not published as a host port.

## Backups

`mjust backup [instance_id]` creates a verified cold backup of the complete persistent Minecraft data directory.

The backup flow validates the configured destination before stopping Minecraft, acquires the shared maintenance lock, gracefully stops the server when required, creates the archive as a temporary partial file, verifies it, publishes it atomically, applies retention, and restarts Minecraft only when appropriate.

A missing or incorrect external/network backup mount fails closed before Minecraft is stopped. This prevents a failed network mount from silently writing backups to the root filesystem.

Manual and scheduled instance backups use the selected instance’s retention policy. Version maintenance creates a same-instance cold backup before disruptive changes. Import/Export convergence remains separate.

## Restore

`mjust restore [instance_id]` restores the selected server’s complete backed-up persistent data, including worlds, plugins, player data, whitelist/ops and Minecraft-side configuration. `mjust restore-full [instance_id]` is an alias for the same full-data operation.

Archives belong to the immutable ID. A renamed server retains its backups; another server cannot restore them. World-only legacy Restore is not a mode of the instance Restore API. Omitted selection resolves only for exactly one registered server.

The Restore command is a thin Management API frontend. Backup discovery,
compatibility decisions, archive validation, staging, player-safety policy,
shutdown, data switching, SELinux handling, runtime validation and rollback are
owned by the Management Agent and shared Restore backend. The terminal keeps the
human-facing selection and exact `RESTORE` confirmation.

If the terminal disconnects during a Restore, running `mjust restore <same-instance-id>` again
reconnects to the persistent Restore operation and resumes progress display.

See `RESTORE.md` for the detailed recovery model and boundaries.

## Import, export, and migration

`mjust export` selects one registered instance and creates a portable JustVoxel
migration bundle containing its complete persistent Minecraft server state plus
versioned migration metadata and integrity information.

`mjust import` selects an existing destination instance and can adopt a native
JustVoxel migration bundle, an existing JustVoxel backup, or supported external
Paper server data after staging and validation. Import does not create an
unconfigured destination and does not treat migration as a Minecraft version
upgrade.

`mjust migration-recover` selects the destination instance and is the recovery
path for an incomplete or failed migration transaction that requires
administrator review.

Migration uses the same appliance safety principles as restore and storage workflows: staging before activation, player-aware interruption, destination-specific configuration, transaction preservation, SELinux/ownership normalization, runtime validation, and rollback of the previous live state when possible.

See `MIGRATION.md` for supported sources, transport options, version/identity safety, transaction behavior, and recovery boundaries.

## Minecraft updates

`mjust update-minecraft [instance_id]` handles container-image maintenance and
the configured software/version policy for the selected server. Omit the ID only
when exactly one instance is registered.

The container-image tag and Minecraft game/software version are independent
settings where that server type supports both controls.

Before disruptive maintenance, JustVoxel checks players, creates a verified cold backup, performs the selected update work, starts Minecraft once, and validates the resulting server.

Moving container tags are refreshed only by an explicit image pull. A pinned game
version is not silently changed because a newer supported release exists.

JustVoxel retains one previous Minecraft image generation for rollback and does not run a broad Podman image prune that could affect unrelated containers.

## Storage

Run `mjust storage` to open the storage-management menu.

The current direct storage operations are:

- `mjust storage-plan` — read-only storage/device overview from the shared Management API discovery
- `mjust storage-disk` — provision a dedicated whole disk or USB device
- `mjust storage-partition` — adopt/use an existing partition
- `mjust storage-free-space` — create a partition only in already-unallocated space
- `mjust storage-network` — configure NFS or SMB/CIFS backup storage
- `mjust storage-system` — use a normal directory on the system filesystem
- `mjust storage-migrate` — migrate active Minecraft data through the shared persistent Management API transaction

Destructive storage actions require exact typed confirmations such as `ERASE /dev/...`, `FORMAT /dev/...`, or `CREATE PARTITION /dev/...`. A simple yes/no confirmation is not enough.

JustVoxel protects detected system disks from whole-disk erase and does not automatically shrink existing filesystems or partitions.

New local mounts created by JustVoxel use filesystem UUIDs rather than temporary device names such as `/dev/sdb1`.

Minecraft data migration is now API-owned: mjust only selects the Agent-discovered target, presents the reviewed plan, collects required confirmations, and monitors/reconnects to the persistent operation. Target provisioning, player safety, the cold backup, copy/verification, configuration switching, SELinux/runtime work, validation, and rollback are owned by the Management Agent and shared backend.

See `STORAGE.md` for supported layouts, network storage, migration behavior, and storage safety rules.

## Operating-system maintenance

JustVoxel keeps operating-system maintenance separate from Minecraft container maintenance.

The current system-management commands are:

- `mjust os-status` — friendly bootc deployment status
- `mjust os-update` — run the JustVoxel OS update through the Management API; a newer image is pulled/staged when available
- `mjust resources` — open the live btop resource monitor
- `mjust net` — open the System → Network chooser for nm-hsp or nmtui
- `mjust reboot` — player-aware graceful reboot
- `mjust poweroff` — player-aware graceful power off
- `mjust firmware` — reboot into firmware/UEFI setup when supported on physical hardware

`mjust os-status` and `mjust os-update` are thin Management API frontends. The Management Agent owns `bootc status` and ordinary `bootc upgrade`; mJust does not execute bootc directly for these workflows.

Running a bootc OS update does not stop Minecraft, create a backup, or reboot the appliance. A staged deployment is used on the next normal reboot.

`mjust net` keeps networking inside the System area. It presents **nm-hsp** as the friendly Home Server Project interface for normal Ethernet, Wi-Fi, and easy network troubleshooting, while retaining **nmtui** as the classic advanced NetworkManager interface. No separate `mjust net-hsp` or `mjust nmtui` commands are exposed.

Reboot, poweroff, and firmware reboot are thin frontends over the Management API.
The Management Agent owns host capability checks, player-aware shutdown of all
running instances, and the final system action. The WebUI uses the same
implementation.

## Minecraft Network

`mjust minecraft-network` manages the optional Velocity front door for selected
Paper and Purpur instances. Status, membership, enable/disable, whitelist state,
and recovery use the same Management API as the WebUI. Standalone instances do
not require the network. See [minecraft-network.md](minecraft-network.md).

## UPS

`mjust ups` shows UPS status and, on a hardware-capable deployment, configures a
local or remote NUT source, automatic shutdown protection, and optional network
sharing. The WebUI exposes the same backend. Automatic hardware/driver discovery
is not implemented; local setup requires explicit NUT values.

`mjust firmware` is available only when the Agent reports that the running HWS system supports the physical-hardware firmware workflow and refuses unsupported/VM use.

A JustVoxel-aware bootc rollback workflow is not implemented. It remains a future roadmap item; see `ROADMAP.md`.

See `SYSTEM.md` for the complete system-management behavior.

## Live system resources

`mjust resources` opens `btop` for a live view of CPU, memory and swap, disks, network activity, and processes.

It requires an interactive terminal. Inside btop, `q` exits directly back to the JustVoxel menu or calling shell.

## Terminal file management

`mjust files` opens the packaged Superfile (`spf`) terminal file manager in the
current local or SSH terminal. It is an advanced administrator tool and follows
the permissions of the account that launched it. It does not provision or mount
storage; use `mjust storage` for those operations. This command is separate from
the authenticated WebUI Files workspace and its constrained FileBrowser sources.

## Logs

`mjust logs` shows a bounded recent Minecraft log view through the JustVoxel Management API.

The old advanced/full-system journal mode is intentionally not exposed through mJust. Administrators who need lower-level journal inspection can use standard Linux tools such as `journalctl` directly.

Logs remain separate from the friendly status dashboard and the stricter validation workflow.

## Validation

`mjust validate` is appliance-wide: Management runs host checks once (failed systemd units and swap/zram policy), then validates every modern immutable Minecraft instance independently. Zero servers is valid when the host checks pass. Display names label the report; immutable `jv-*` IDs identify every server check.

Each instance receives runtime commitment/file permissions, storage identity, read-only SELinux context, owned firewall rules, service/container state, and backup destination/settings/status/timer checks. Deliberately stopped servers receive structural checks and are never started merely to validate. Running servers also receive live instance-aware stack verification, including RCON, software/version, ViaVersion where applicable, and enabled Geyser/Floodgate integration.

`mjust validate` remains a thin frontend to `GET /v1/admin/validation`. The default `validate-backend` helper checks only the host. Its `--first-run` mode retains fixed-runtime semantics solely for historical Setup recovery evidence; normal validation and new Setup do not use that mode.

The goal is to fail visibly when the appliance does not match its recorded configuration rather than continuing with an unexpected mount or incomplete runtime.

## Start over / reset Minecraft

`mjust start-over` uses the local root Management API to retire **all configured Minecraft servers** and return the appliance to Setup. It preserves every world/data tree, backup archive and metadata, storage layout/mounts, storage ownership and SMB credentials, authentication, WebUI users and history.

It reviews exact immutable instance IDs, offers a verified root-only configuration archive before mutation, and requires `START OVER`. Online players require separate interruption approval; every running instance uses its normal graceful shutdown and player countdown. Modern containers and owned runtime/backup units/firewall rules are retired; images and unrelated containers remain.

Interrupted work resumes from exact durable Start Over retirement evidence. Unrelated recovery transactions and fixed-runtime artifacts block the operation; this frontend has no legacy execution fallback. See `START_OVER.md` for the preservation and recovery boundary.

## Login welcome controls

The advanced menu also provides controls for the JustVoxel login welcome display:

- `mjust welcome`
- `mjust welcome-off`
- `mjust welcome-on`

These affect the login/welcome presentation only and do not change Minecraft runtime behavior.

## Interactive terminal behavior

On a normal local or SSH terminal with enough space, JustVoxel uses `fzf` for an arrow-key two-pane browser. The selected action includes a short explanation and the equivalent direct command where useful.

On smaller or more limited interactive terminals, JustVoxel falls back to a compact selector. The preference is `gum`, then compact `fzf`, then a plain Bash numbered menu.

When stdin/stdout is not attached to an interactive TTY, or `TERM=dumb` is used, `mjust` does not wait for menu input. It prints concise direct-command help instead.

For an interactive SSH menu, allocate a TTY, for example with `ssh -t`.

## Configuration ownership

The bootc image owns the immutable implementation and templates under locations such as:

- `/usr/share/justvoxel/`
- `/usr/libexec/justvoxel/`

Administrator-owned active configuration lives under `/etc`, including the JustVoxel configuration, Minecraft environment, backup environment, generated Minecraft Quadlet, and generated backup service/timer.

Bootc image updates may update the immutable mjust implementation and templates, but they do not silently overwrite an installed machine's active administrator configuration in `/etc`.

Persistent application/runtime state under `/var` remains outside the immutable deployment and is created/reconstructed through the appliance's declarative system mechanisms where required.

## Operations intentionally not automated

The current implementation deliberately does not:

- shrink an existing partition or filesystem
- resize a filesystem to create free space
- erase the detected system disk as a whole-disk storage target
- silently reformat a recognized filesystem
- silently overwrite active `/etc` configuration during a bootc update
- automatically delete the old Minecraft data directory after migration
- expose RCON publicly
- relocate Podman's global image store
- run a broad Podman image prune
- automatically change a pinned Minecraft version

These are intentional safety boundaries rather than missing automatic steps.

## Advanced Linux administration

mjust is the normal appliance interface, but it is not a proprietary shell and it does not lock experienced administrators out of the host.

Standard tools such as `systemctl`, `journalctl`, `podman`, `bootc`, `nmcli`, `firewall-cmd`, `findmnt`, and `lsblk` remain available over SSH or the local console.

Some active files are generated from JustVoxel's recorded configuration. Manual edits are possible, but files owned by a JustVoxel render workflow may be regenerated later by operations such as configuration changes or storage migration.

See `MANAGEMENT.md` for how interactive mjust, direct commands, Web management, and native Linux administration fit together.

## Current development status

The current unified Management API / mJust rebuild and recent WebUI parity work are implemented on `testing`.

The priority remains validation and hardening before stable promotion. Validation covers the shared appliance behavior, including VM-ready operation, destructive/failure-path storage testing, backup and restore, migration, network-storage failure handling, Minecraft updates, installation/first-boot behavior, and hardware-dependent paths when the required capability is available.

Future feature priorities, including JustVoxel-aware system rollback, are tracked in `ROADMAP.md`.
