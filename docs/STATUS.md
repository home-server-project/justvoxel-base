# JustVoxel status dashboard

`mjust status` is the normal user-facing health dashboard for a JustVoxel appliance.

JustVoxel intentionally does not provide a second `mjust health` dashboard. Health information belongs in `mjust status`, while `mjust validate` remains the stricter correctness check used to prove that the appliance matches its expected configuration and runtime state.

## Normal status

Normal `mjust status` uses short appliance-oriented sections instead of raw Linux command output:

- overall health
- host identity, variant, uptime, physical RAM, and failed-service summary
- Minecraft state, players, Paper version, Bedrock cross-play, addresses, memory and uptime
- primary network interface, IPv4 address, gateway and DNS
- system/container storage, Minecraft data storage, backup storage and available capacity
- automatic backup state, most recent completed backup and next scheduled backup
- configured Minecraft container channel and image

Unavailable optional information is shown as `Unknown` instead of dumping command errors.

A deliberately stopped Minecraft server is reported as `Stopped` and does not by itself make the appliance unhealthy.

## Overall health

`Overall: Healthy` is a lightweight operational summary. It does not run `mjust validate` internally.

The normal status view uses cheap local observations such as:

- failed system services
- failed Minecraft service state
- basic network/default-route availability
- configured data/backup storage availability
- configured backup timer state

Problems are summarized as `Attention needed`. When the system state cannot be determined reliably, status uses `Unknown`.

## Failed services

Failed services are part of the normal status dashboard rather than a separate normal-user command.

When no service has failed, status reports:

`System services: Healthy - no failed services`

When failures exist, status shows the number of failed services and a short summary for up to three units: unit name, description and result. Additional failures are left to `mjust status --details` so the normal dashboard stays concise.

## Storage presentation

Status is purpose-aware rather than a raw `df` listing.

It identifies system/container storage, Minecraft data and backup storage. If Minecraft data shares the system/container filesystem, that relationship is stated instead of repeating capacity rows.

If backups share the Minecraft-data filesystem, capacity is not printed twice. Status explains that same-filesystem backups help with world/configuration recovery but do not protect against physical disk failure.

A separate disk, NFS or SMB filesystem is shown as its own backup target with its own available capacity.

## Administrator detail

`mjust status --details` prints the same friendly dashboard first and then adds administrator-oriented information including:

- complete failed-service output
- Minecraft systemd status
- Podman container and memory detail
- raw mount/filesystem detail
- interface, route and resolver detail

`mjust logs` remains the dedicated Minecraft log viewer.

`mjust validate` remains separate from both status modes. Validation is allowed to be stricter, perform more checks, return failure, and explain configuration/runtime mismatches.

## System management is separate from status

Operating-system maintenance and power controls are implemented, but they intentionally remain separate from the normal health dashboard.

Use:

- `mjust os-status` for the running, staged, and rollback bootc deployment view
- `mjust os-update` to check for and optionally download/stage a newer JustVoxel OS image
- `mjust resources` for the live btop resource monitor
- `mjust reboot` for a player-aware graceful reboot
- `mjust poweroff` for a player-aware graceful shutdown
- `mjust firmware` for reboot into firmware/UEFI setup when supported on physical hardware

This keeps `mjust status` focused on answering one question quickly: is the appliance healthy and what needs attention?

See `SYSTEM.md` for the detailed operating-system and power-management behavior.

## Rollback boundary

JustVoxel-aware bootc rollback remains intentionally separate and is not currently implemented as an mjust workflow. A safe appliance-level rollback needs deliberate handling of deployment-specific `/etc` state together with Minecraft backup and recovery behavior.
