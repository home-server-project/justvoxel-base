# JustVoxel status dashboard

`mjust status` is the normal user-facing health dashboard for a JustVoxel appliance.

JustVoxel intentionally does not provide a second `mjust health` dashboard. Health information belongs in `mjust status`, while `mjust validate` remains the stricter correctness check used to prove that the appliance matches its expected configuration and runtime state.

## Normal status

Normal `mjust status` uses short appliance-oriented sections instead of raw Linux command output:

- overall health
- host identity, variant, uptime, physical RAM, and failed-service summary
- every registered Minecraft server, identified by display name and immutable ID
- per-server runtime, players, software/version, Java and Bedrock addresses, configured memory, start-on-boot, and image tag
- per-server backup schedule, daily time, last success and current backup state
- primary network interface, IPv4 address, Tailscale, NetBird, gateway and DNS
- host system/container filesystem and available capacity

`GET /v1/status` supplies host/appliance health and information only. Minecraft and backup information comes from the authoritative registry/runtime inventories and each registered immutable ID's configuration, players and backup APIs. Names are labels, never correlation keys. Status does not choose a first, sole or default server.

With zero servers, status says `No Minecraft servers configured.` An unavailable individual read is shown as unavailable while independent server information remains visible. Registered servers with missing runtime/configuration evidence remain visible as unavailable. Unregistered runtime identities produce an explicit attention condition; status never guesses their ownership.

## Overall health

`Overall: Healthy` is a lightweight operational summary. It does not run `mjust validate` internally.

Host health depends on failed system services, system state, basic network/default-route availability and system storage availability. mJust starts from that host health and reports `Attention needed` for unhealthy or unavailable instance evidence, mismatched inventories, failed/interrupted backups, or a required backup restart. A running or deliberately stopped server does not by itself make the appliance unhealthy. Zero servers are healthy when the host is healthy. Unknown host state remains `Unknown` unless instance evidence requires attention.

## Storage presentation

Storage shows the host system/container filesystem reported by `/v1/status`. It does not infer global Minecraft data or backup locations from one server.

## Administrator detail

`mjust status --details` prints the same friendly dashboard first, then bounded host details for failed services, system storage, interfaces, routes and resolver state. Additional per-server details include timezone, backup retention, last backup attempt, backup stage and restart requirement, using the public responses already obtained. It does not inspect a fixed Minecraft service or container or expose private instance paths, credentials or image repositories.

`mjust logs` remains the dedicated Minecraft log viewer. `mjust validate` remains a separate correctness check.

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
