# Minecraft runtime

JustVoxel ships immutable runtime templates based on the hardware-verified Home Server Project Paper deployment. The bootc image itself does not create a world, accept the Minecraft EULA, or activate a Minecraft service.

`mjust setup` copies and renders those templates into administrator-owned files under `/etc`. Minecraft runs only from the local rendered Quadlet in `/etc/containers/systemd/minecraft.container`; it never runs directly from `/usr/share/justvoxel/templates`.

The runtime supports **Paper (default), Purpur, and Vanilla**, using the same `docker.io/itzg/minecraft-server` image and Quadlet. `MINECRAFT_SERVER_TYPE` in administrator configuration is lowercase `paper`, `purpur`, or `vanilla`; old configurations without it default to Paper. Invalid values fail validation.

Paper and Purpur support plugins, managed ViaVersion, and optional managed Bedrock cross-play through the existing Geyser-Spigot/Floodgate-Spigot pair. Vanilla has no managed plugins, ViaVersion, or Bedrock cross-play in this implementation. Both setup paths allow all three implementations; Recommended enables Bedrock for Paper/Purpur and disables it for Vanilla. Backend validation rejects enabling Bedrock on Vanilla.

The Version workspace offers reviewed Paper ↔ Purpur changes through the existing safe maintenance transaction: player checks, cold backup, graceful stop, configuration/render, start and complete stack verification. A failure restores the previous server type, version and runtime and verifies the restarted previous state; incomplete recovery requires attention. Worlds, plugins, player data and server configuration files are preserved, including `purpur.yml` when returning to Paper. Changing to or from Vanilla requires **Reset Minecraft** and setup again.

Purpur Recommended uses the Downloads API `metadata.current`, or the exact released Geyser-supported Java version when Bedrock is enabled and that version exists in Purpur `versions[]`. Latest uses the newest available Purpur Minecraft version; Pinned requires an exact listed version. The image resolves the latest Purpur build; no Paper build channels are passed to Purpur. Vanilla Recommended and Latest use Mojang's current release, never snapshots; Pinned requires an exact Mojang release. Unavailable upstream metadata fails planning/status safely. Vanilla stack verification uses `mc-monitor status --json` plus RCON, without plugin commands.

Migration v1 remains a Paper import format/workflow. Exports record the authoritative software in the existing `minecraft.implementation` manifest field. Import rejects non-Paper native bundles and non-Paper destinations rather than converting them silently. Legacy bundles without an implementation continue to use Paper. Cross-software import support is a separate compatibility project.

The image tag is administrator-configurable: JustVoxel offers upstream `stable`, upstream `latest`, or a validated custom/exact tag. The Minecraft game version is a separate setting: Recommended uses an exact stable version, Latest follows newer versions, and Specific uses an exact version available for the selected software. Setup keeps the chosen policy and the resolved version separately. For Paper, a non-stable build uses `PAPER_CHANNEL=experimental`. At runtime, Latest renders `VERSION=LATEST`.

Changing or restarting a service does not itself refresh a moving container tag from the registry. `mjust update-minecraft` explicitly checks and pulls the configured tag when its remote digest changes. If the game version uses `VERSION=LATEST`, however, the container can download a newer Minecraft release during startup; mjust warns about that behavior when the policy is selected.

Java uses TCP 25565 by default and Bedrock uses UDP 19132 by default, but setup can render administrator-selected host ports. RCON is enabled inside the container for local administration and is never published as a host port.

The template preserves the proven graceful-stop behavior: the itzg shutdown announcement delay is 60 seconds, Podman has 180 seconds to stop, and systemd allows 240 seconds around the stop operation. If an administrator chooses to continue maintenance while players are online, that normal shutdown warning remains the mechanism that gives players time to finish.

Persistent Minecraft data is a configurable host path mounted only at `/data` with a private SELinux `:Z` label. No Mojang server JAR, Paper server JAR, world, EULA acceptance, RCON password, or Floodgate private key is baked into the bootc image.

## Memory behavior

Minecraft memory usage may stay high even when no players are online.

That does not necessarily mean the server actively needs all of that memory. Java can grow its heap while the server is running and keep memory reserved for reuse instead of immediately returning it to the operating system. High idle memory usage by itself is therefore not automatically a problem.

JustVoxel enables zram only as a memory-pressure safety buffer. Zram is compressed swap in RAM; it is not extra physical memory and is not counted when `mjust setup` recommends Minecraft memory values. The recommendation logic uses physical `MemTotal` only.

JustVoxel does not configure disk swap by default.

The single managed Minecraft server has one persistent, immutable destination Instance ID, independent of its name, software, container, version, and data path. The Management Agent stores only that ID in `/var/lib/justvoxel/instances/minecraft.json` (root:root, `0600`), in a root-only `0700` directory. Restarts, container recreation, and image updates preserve the record.

Reviewed first-run setup and Fresh Import persist the destination identity before creating an operation or starting a worker. Registration success is silent. If automatic registration fails, the active Review workflow asks for a manual `jv-` suffix of 6–12 letters and numbers, then resumes the same reviewed submission with all safety checks intact. Uppercase is normalized to lowercase; automatic IDs have 12 lowercase alphanumeric characters. Existing valid IDs cannot be changed, and collisions are rejected.

For previously configured servers, the administrator dashboard silently attempts missing identity registration once per authenticated WebUI session. Failure immediately opens the same manual popup. Registration does not change server data or stop/restart Minecraft. Instance ID is internal metadata and is absent from Minecraft Overview and Settings.

Identity belongs to the local destination, independently of archive metadata. Replace Import and restore preserve the destination ID. Old backups and external archives need no ID; archive layouts and unattended backup timers are unchanged. The authenticated Management API provides `GET /v1/minecraft/identity` and administrator-only `POST /v1/minecraft/identity/ensure` and `POST /v1/minecraft/identity/manual` (JSON `suffix`), including before first-run creation. WebUI writes require CSRF protection. This identity foundation manages only the existing single server.

## Instance registry foundation (Phase 1)

Runtime remains single-instance: this foundation does not create another container, activate services, or change setup, reset, import, restore, ports, data paths, or backup behavior. The existing `minecraft.json` identity record, permissions, and identity API contracts remain unchanged and work without name metadata.

The same Management Agent identity store can internally register future logical instances, with callers supplying the selected software for both the new instance and legacy default-name reservation, as top-level `<Instance ID>.json` records containing only `id`. Optional presentation metadata lives separately in `/var/lib/justvoxel/instances/names/<Instance ID>.json`, containing `id`, `display_name`, and the software used for default naming. The names directory is root:root `0700`; its records are root:root `0600`. It is outside the legacy top-level identity collision scan. Names contain no runtime secrets and never enter portable backup archives; backup grouping remains by Instance ID.

Registration and rename use the existing cross-process identity-directory lock. Records are synced and atomically published; identity publication cannot replace an existing record. Internal logical registration publishes the name before its identity. Interrupted publication may retain name metadata without an active identity; internal registration reserves such IDs instead of reusing them. Retained metadata is excluded from active enumeration and can support historical backup names in a later deletion phase. No deletion API is provided here, and current retirement semantics remain unchanged.

Authenticated administrators, operators, and viewers can use `GET /v1/minecraft/instances`, returning `{"instances":[{"id":"jv-…","display_name":"JV Paper"}]}`. Administrators can use `POST /v1/minecraft/instances/{id}/name` with `{"display_name":"Family Survival"}`; the response contains the selected ID and resulting name. No creation or lifecycle API and no WebUI endpoints are added.

Names are trimmed, limited to 64 Unicode characters, and restricted to letters, numbers, combining marks, spaces, and `. _ - ' ( )`. Controls, invisible formatting characters, path separators, and markup are rejected. Active display names are unique without regard to case; explicit duplicates return a conflict. An omitted or empty name generates `JV Paper`, `JV Purpur`, or `JV Vanilla`, adding ` 2`, ` 3`, and so on when needed. An unnamed legacy server uses its selected software from read-only configuration discovery, or `JV Minecraft` when unavailable. Listing never creates or rewrites registry records. Stored names survive Agent restarts. Rename changes presentation metadata only and never uses a name as a privileged filesystem identifier.

Registry operations reject malformed records, duplicate identities or active names, symlinks, and unsafe ownership or permissions. An unavailable or corrupt optional name record makes registry operations unavailable, but does not affect legacy identity reads, existing identity registration, or Minecraft runtime startup.
