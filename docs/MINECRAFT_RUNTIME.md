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

## Independent runtime rendering (Phase 2)

The Management Agent now has an internal renderer for a complete set of independently configured instances. It has no public creation, preview, apply, or lifecycle API. It reads existing immutable identities without registering them, loading names, or changing legacy configuration. Friendly-name changes cannot affect the rendered runtime.

The existing installation still uses `/etc/containers/systemd/minecraft.container`, `/etc/justvoxel/minecraft.env`, `minecraft.service`, and `ContainerName=minecraft`. The renderer rejects the legacy identity as a new instance; legacy setup, rendering, worlds, backups, restore, migration, and the shutdown guard remain unchanged.

For a registered `jv-a1b2c3`, the runtime layout prepared for a future explicit activation is:

| Component | Path or name |
| --- | --- |
| Quadlet | `/etc/containers/systemd/minecraft-jv-a1b2c3.container` |
| Generated service | `minecraft-jv-a1b2c3.service` |
| Container | `minecraft-jv-a1b2c3` |
| Private environment | `/etc/justvoxel/instances/jv-a1b2c3/minecraft.env` |
| Data | `<configured legacy data parent>/minecraft-instances/jv-a1b2c3/data` |

For example, legacy `/var/lib/justvoxel/minecraft` yields `/var/lib/justvoxel/minecraft-instances/jv-a1b2c3/data`; another configured storage parent yields corresponding sibling paths. Data never enters the legacy world, backup root, or Phase 1 identity directory. The renderer rejects layouts where those paths overlap, including configurations whose mount contains only the legacy world and cannot safely hold siblings. It does not create, move, delete, repair, or relabel worlds. All existing path components must be directories without symlinks. Future activation must explicitly provision and verify the data directory before startup.

`minecraftInstanceRuntimeConfig` holds per-instance ports, Java and container memory, software, selected version, Paper channel, image tag, startup settings, explicit EULA acceptance, non-root container UID/GID, private RCON password, and Start on Boot. RCON passwords follow the existing generated hexadecimal convention (32–128 hex characters). The renderer reuses `templates/config/minecraft.env.in` and the established Paper/Purpur/Vanilla capability rules. The new Quadlet template uses `docker.io/itzg/minecraft-server:<selected tag>` with the existing moving or pinned tag convention; rendering does not resolve tags, fetch metadata, or resolve digests. Version inputs are a selected pin or `LATEST`; upstream availability checks remain a caller responsibility. Latest Paper requires its existing experimental channel convention.

The trusted internal caller must supply **every configured instance, including stopped instances**, plus a fresh legacy reservation and physical RAM/configured-storage snapshot. This phase does not yet provide the authoritative persistent multi-instance configuration store or activation lock. Validation reserves Java TCP and enabled Bedrock UDP ports separately, rejects duplicates within each protocol and collisions with legacy reservations, and checks the 1–65535 range. Equal numeric TCP and UDP ports are allowed. Disabled Bedrock requires a zero port. Enabled instance Bedrock is validated but rendering then rejects it explicitly: the global `managed-crossplay-start` helper is never attached to new instances, and Phase 2 does not provide instance cross-play.

Memory uses positive `M`/`G` values (case insensitive), between 512 MiB and 1 TiB per quantity. Each container must allow at least 512 MiB above its Java allocation. Combined configured container limits, including the legacy container and stopped instances, must leave at least 1 GiB of physical RAM for the appliance. Free memory, listening sockets, swap, and zram do not determine reservations. Startup values and immutable IDs are validated before any output is returned; malformed JSON and unknown fields are rejected by the internal decoder. There are no arbitrary environment, image repository, unit, or data-path overrides.

Start on Boot ON renders `[Install]` with `WantedBy=multi-user.target`. OFF omits boot installation while retaining the complete Quadlet for manual service startup after explicit activation. It is independent of running state and never calls `systemctl enable` on generated services. The runtime preserves `:Z`, journald, `Restart=always`, `RestartSec=10`, `StopTimeout=180`, `TimeoutStartSec=900`, and `TimeoutStopSec=240`.

`validate-instance-data-mount` receives the immutable ID, storage root, derived data path, and configured mount identity directly from the Quadlet. It does not load legacy configuration. It requires existing canonical directories, rejects symlinks and another instance's path, and checks the exact configured mount's UUID/source with the established identity function. Configured mounts require identity evidence. `RequiresMountsFor` remains present. The helper performs no world creation, permission repair, or SELinux changes.

Rendering returns deterministic in-memory files, including private environment bytes that must never be logged or exposed in an API response. Optional internal staging writes a new root:root `0700` bundle under an already existing private staging parent outside active configuration, Minecraft storage, and identity state. A bundle contains `<id>.env` and `minecraft-<id>.container`, all root:root `0600`. Files and the temporary directory are synced before atomic, non-replacing directory publication. A rendering/write failure leaves no published partial bundle and preserves older bundles; a directory-sync failure after publication reports that the complete bundle exists but durability is uncertain. Staged Quadlets reference their eventual environment paths; staging is not activation and the bundle must stay outside Quadlet search paths. It creates no `/etc` files and invokes no systemd, firewall, or container operations.

Focused Go tests cover independent output, boot settings, identity/name independence, legacy contracts and read purity, stopped-instance reservations, ports, resources, malformed inputs, path overlap, symlinks, and atomic inactive staging. `tests/test-instance-runtime-mount.sh` uses temporary fixtures and stubbed mount identity checks. The optional Go generator test requires explicit `JUSTVOXEL_QUADLET_GENERATOR` selection and runs only the generator's dry-run mode; it starts no containers. These tests have been added but not executed for Phase 2. Formatting and shell syntax checks are separate from test or runtime acceptance.

Phase 3 still needs the authoritative private configuration store and complete reservation discovery, activation-time locking/revalidation and recoverable live-file publication, explicit lifecycle operations, instance-aware cross-play helpers, shutdown coordination, and VM runtime acceptance. No first-run multi-instance startup, WebUI creation flow, firewall changes, or scheduled backups are introduced here.
