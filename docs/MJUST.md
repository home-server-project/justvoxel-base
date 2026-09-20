# mjust user and administrator guide

`mjust` is JustVoxel's built-in administration interface.

It is designed for people who are comfortable installing an operating system and following normal computer instructions, but who should not need deep Linux, container, systemd, bootc, SELinux, firewall, storage, or Minecraft-server administration knowledge just to run a family Minecraft appliance.

Run `mjust` with no arguments to open the interactive terminal interface. Choose the task you want, read the explanation, and follow the prompts. The same operations are also available as direct `mjust` commands for experienced users, documentation, automation, and troubleshooting.

The interaction model is inspired by Universal Blue's `ujust` / `ugum` work in `ublue-os/packages`, but JustVoxel uses its own server-focused implementation.

JustVoxel remains a normal immutable AlmaLinux server underneath. Advanced administrators can still use normal Linux tools directly when they want deeper control.

> JustVoxel is still under active development on the `testing` branch and remains under active validation before stable promotion.

## How mjust is organized

The normal interactive interface groups appliance tasks by what the user wants to accomplish rather than by the Linux commands underneath.

The main areas are:

- first setup and Minecraft configuration
- appliance status and health
- Minecraft start, stop, and restart
- players and whitelist management
- backups and restore
- storage and Minecraft data migration
- Minecraft/Paper updates
- operating-system maintenance
- live system resources
- validation and logs
- advanced/reset tools

Safety checks live in the command/workflow layer, not only in the menu. Using a direct command does not bypass player checks, backup validation, storage protection, typed confirmations, SELinux handling, or other JustVoxel safeguards.

## First setup

`mjust setup` is the normal first-run wizard.

It collects the settings needed to bring the appliance online, including Minecraft data location, Java and container memory, Java and optional Bedrock ports, timezone, maximum players, server message, Minecraft container-image policy, Minecraft/Paper version policy, backup target and schedule, backup retention, and Minecraft EULA acceptance.

The normal flow tries to keep Linux-specific details out of the way. Memory values are suggested from installed physical RAM, but the user can change them.

Setup refuses to silently overwrite an already-configured JustVoxel installation.

## Configure an installed server

`mjust configure` changes supported Minecraft and backup settings after first setup.

The current configuration flow can change memory limits, Java/Bedrock ports, MOTD, backup schedule and timer state, storage/migration settings, Minecraft container-image policy, and Minecraft/Paper version policy.

`mjust configure-max-players` provides a focused guided screen for changing the maximum player count.

Configuration changes that require Minecraft to restart are not applied through an automatic disruptive restart. JustVoxel tells the user when a restart is needed.

## Status and health

`mjust status` is the normal user-facing appliance dashboard.

It presents short, readable information about the host, Minecraft state, players, Paper version, Bedrock support, network state, storage, backups, memory, service health, and the configured Minecraft container image.

`mjust status --details` shows the same friendly summary first and then adds administrator-oriented systemd, Podman, mount, and network detail.

`mjust validate` is different from status. Validation is the stricter correctness check used to confirm that the deployed appliance matches its recorded configuration and expected runtime state.

See `STATUS.md` for the full status model.

## Minecraft service control

The normal commands are:

- `mjust start`
- `mjust stop`
- `mjust restart`

Stop and restart are player-aware. When Minecraft is running, JustVoxel checks players through internal RCON before interruption. If player state cannot be confirmed, the operation fails closed instead of guessing.

When no players are online, JustVoxel skips the container's fixed 60-second announcement delay and completes the graceful stop immediately. When players are online and the interruption is approved, JustVoxel keeps the 60-second grace period and sends in-game countdown notices at 60, 30, 15, 10, 5, 4, 3, 2, and 1 seconds before shutdown.

The same adaptive shutdown path is reused by host reboot/poweroff and disruptive Minecraft maintenance so those workflows do not independently implement player timing.

## Players and whitelist

`mjust players` shows the current player list through the server's internal RCON connection.

Whitelist management has separate Java and Bedrock/Floodgate operations:

- `mjust whitelist-list`
- `mjust whitelist-add-java <name>`
- `mjust whitelist-remove-java <name>`
- `mjust whitelist-add-bedrock <gamertag>`
- `mjust whitelist-remove-bedrock <gamertag>`

RCON remains internal to the Minecraft container and is not published as a host port.

## Backups

`mjust backup` creates a verified cold backup of the complete persistent Minecraft data directory.

The backup flow validates the configured destination before stopping Minecraft, acquires the shared maintenance lock, gracefully stops the server when required, creates the archive as a temporary partial file, verifies it, publishes it atomically, applies retention, and restarts Minecraft only when appropriate.

A missing or incorrect external/network backup mount fails closed before Minecraft is stopped. This prevents a failed network mount from silently writing backups to the root filesystem.

Manual backups, scheduled backups, pre-update backups, and pre-migration backups use the same retention policy.

## Restore

JustVoxel provides two restore levels.

`mjust restore` restores the main Minecraft world and its normal Nether/End world directories while keeping current plugins and Minecraft/JustVoxel configuration.

`mjust restore-full` restores the complete backed-up Minecraft persistent-data directory, including plugins, plugin data, and Minecraft-side configuration.

The Restore command is a thin Management API frontend. Backup discovery,
compatibility decisions, archive validation, staging, player-safety policy,
shutdown, data switching, SELinux handling, runtime validation and rollback are
owned by the Management Agent and shared Restore backend. The terminal keeps the
human-facing selection and exact `RESTORE` confirmation.

If the terminal disconnects during a Restore, running `mjust restore` again
reconnects to the persistent Restore operation and resumes progress display.

See `RESTORE.md` for the detailed recovery model and boundaries.

## Import, export, and migration

`mjust export` creates a portable JustVoxel migration bundle containing the complete persistent Minecraft server state plus versioned migration metadata and integrity information.

`mjust import` can adopt a native JustVoxel migration bundle, an existing JustVoxel backup, or supported external Paper server data after staging and validation. Import does not treat migration as a Minecraft version upgrade.

`mjust migration-recover` is the recovery path for an incomplete or failed migration transaction that requires administrator review.

Migration uses the same appliance safety principles as restore and storage workflows: staging before activation, player-aware interruption, destination-specific configuration, transaction preservation, SELinux/ownership normalization, runtime validation, and rollback of the previous live state when possible.

See `MIGRATION.md` for supported sources, transport options, version/identity safety, transaction behavior, and recovery boundaries.

## Minecraft/Paper updates

`mjust update-minecraft` handles Minecraft container-image maintenance and the configured Minecraft/Paper version policy.

The container-image tag and Minecraft/Paper game version are independent settings.

Before disruptive maintenance, JustVoxel checks players, creates a verified cold backup, performs the selected update work, starts Minecraft once, and validates the resulting server.

Moving container tags are refreshed only by an explicit image pull. A pinned Minecraft/Paper version is not silently changed just because a newer stable Paper-supported version exists.

JustVoxel retains one previous Minecraft image generation for rollback and does not run a broad Podman image prune that could affect unrelated containers.

## Storage

Run `mjust storage` to open the storage-management menu.

The current direct storage operations are:

- `mjust storage-plan` — read-only storage/device overview
- `mjust storage-disk` — provision a dedicated whole disk or USB device
- `mjust storage-partition` — adopt/use an existing partition
- `mjust storage-free-space` — create a partition only in already-unallocated space
- `mjust storage-network` — configure NFS or SMB/CIFS backup storage
- `mjust storage-system` — use a normal directory on the system filesystem
- `mjust storage-migrate` — move active Minecraft data to provisioned local storage

Destructive storage actions require exact typed confirmations such as `ERASE /dev/...`, `FORMAT /dev/...`, or `CREATE PARTITION /dev/...`. A simple yes/no confirmation is not enough.

JustVoxel protects detected system disks from whole-disk erase and does not automatically shrink existing filesystems or partitions.

New local mounts created by mjust use filesystem UUIDs rather than temporary device names such as `/dev/sdb1`.

See `STORAGE.md` for supported layouts, network storage, migration behavior, and storage safety rules.

## Operating-system maintenance

JustVoxel keeps operating-system maintenance separate from Minecraft container maintenance.

The current system-management commands are:

- `mjust os-status` — friendly bootc deployment status
- `mjust os-update` — check for a newer JustVoxel OS image and optionally download/stage it
- `mjust resources` — open the live btop resource monitor
- `mjust reboot` — player-aware graceful reboot
- `mjust poweroff` — player-aware graceful power off
- `mjust firmware` — reboot into firmware/UEFI setup when supported on physical hardware

Checking or downloading a bootc OS update does not stop Minecraft, create a backup, or reboot the appliance. The staged deployment is used on the next normal reboot.

Reboot and poweroff use the same player-awareness policy as other disruptive Minecraft operations.

`mjust firmware` is available only when the running system supports the physical-hardware firmware workflow and refuses unsupported/VM use.

A JustVoxel-aware bootc rollback workflow is not implemented. It remains a future roadmap item; see `ROADMAP.md`.

See `SYSTEM.md` for the complete system-management behavior.

## Live system resources

`mjust resources` opens `btop` for a live view of CPU, memory and swap, disks, network activity, and processes.

It requires an interactive terminal. Inside btop, `q` exits directly back to the JustVoxel menu or calling shell.

## Logs

`mjust logs` follows the Minecraft systemd journal for troubleshooting.

It is separate from the friendly status dashboard and the stricter validation workflow.

## Validation

`mjust validate` checks the active JustVoxel deployment, including important runtime files and permissions, SELinux labels, firewall state, storage identity, backup target availability, generated service state, RCON response, Minecraft version, Bedrock/Geyser state when enabled, zram, and failed systemd units.

The goal is to fail visibly when the appliance does not match its recorded configuration rather than continuing with an unexpected mount or incomplete runtime.

## Start over / reset Minecraft

`mjust start-over` returns an installed JustVoxel Minecraft configuration to first-setup state without deleting Minecraft world data or existing backup archives.

The operation can save a root-only configuration archive first, handles a running Minecraft server through the normal player-aware shutdown path, and requires the exact phrase `START OVER` before removing active JustVoxel configuration.

It does not erase storage filesystems, Minecraft world data, backup archives, Podman images, unrelated containers, or bootc deployments.

See `START_OVER.md` for the full reset boundary.

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
- automatically change a pinned Minecraft/Paper version

These are intentional safety boundaries rather than missing automatic steps.

## Advanced Linux administration

mjust is the normal appliance interface, but it is not a proprietary shell and it does not lock experienced administrators out of the host.

Standard tools such as `systemctl`, `journalctl`, `podman`, `bootc`, `nmcli`, `firewall-cmd`, `findmnt`, and `lsblk` remain available over SSH or the local console.

Some active files are generated from JustVoxel's recorded configuration. Manual edits are possible, but files owned by a JustVoxel render workflow may be regenerated later by operations such as configuration changes or storage migration.

See `MANAGEMENT.md` for how interactive mjust, direct commands, Web management, and native Linux administration fit together.

## Current development status

The management, backup/restore, storage, system-status/update, resource-monitoring, and power-control flows are implemented on `testing`.

The priority remains validation and hardening before stable promotion. Validation covers the shared appliance behavior, including VM-ready operation, destructive/failure-path storage testing, backup and restore, migration, network-storage failure handling, Minecraft updates, installation/first-boot behavior, and hardware-dependent paths when the required capability is available.

Future feature priorities, including JustVoxel-aware system rollback, are tracked in `ROADMAP.md`.
