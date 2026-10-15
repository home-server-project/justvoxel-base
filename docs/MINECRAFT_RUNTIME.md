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

The trusted internal caller must supply **every configured instance, including stopped instances**, plus a fresh legacy reservation and physical RAM/configured-storage snapshot. This phase does not yet provide the authoritative persistent multi-instance configuration store or activation lock. Validation reserves Java TCP and enabled Bedrock UDP ports separately, rejects duplicates within each protocol and collisions with legacy reservations, and checks the 1–65535 range. Equal numeric TCP and UDP ports are allowed. Disabled Bedrock requires a zero port. Phase 2 originally rejected enabled instance Bedrock after validation. Phase 3B adds the independent startup helper described below; the legacy global `managed-crossplay-start` is never attached to independent units.

Memory uses positive `M`/`G` values (case insensitive), between 512 MiB and 1 TiB per quantity. Each container must allow at least 512 MiB above its Java allocation. Combined configured container limits, including the legacy container and stopped instances, must leave at least 1 GiB of physical RAM for the appliance. Free memory, listening sockets, swap, and zram do not determine reservations. Startup values and immutable IDs are validated before any output is returned; malformed JSON and unknown fields are rejected by the internal decoder. There are no arbitrary environment, image repository, unit, or data-path overrides.

Start on Boot ON renders `[Install]` with `WantedBy=multi-user.target`. OFF omits boot installation while retaining the complete Quadlet for manual service startup after explicit activation. It is independent of running state and never calls `systemctl enable` on generated services. The runtime preserves `:Z`, journald, `Restart=always`, `RestartSec=10`, `StopTimeout=180`, `TimeoutStartSec=900`, and `TimeoutStopSec=240`.

`validate-instance-data-mount` receives the immutable ID, storage root, derived data path, and configured mount identity directly from the Quadlet. It does not load legacy configuration. It requires existing canonical directories, rejects symlinks and another instance's path, and checks the exact configured mount's UUID/source with the established identity function. Configured mounts require identity evidence. `RequiresMountsFor` remains present. The helper performs no world creation, permission repair, or SELinux changes.

Rendering returns deterministic in-memory files, including private environment bytes that must never be logged or exposed in an API response. Optional internal staging writes a new root:root `0700` bundle under an already existing private staging parent outside active configuration, Minecraft storage, and identity state. A bundle contains `<id>.env` and `minecraft-<id>.container`, all root:root `0600`. Files and the temporary directory are synced before atomic, non-replacing directory publication. A rendering/write failure leaves no published partial bundle and preserves older bundles; a directory-sync failure after publication reports that the complete bundle exists but durability is uncertain. Staged Quadlets reference their eventual environment paths; staging is not activation and the bundle must stay outside Quadlet search paths. It creates no `/etc` files and invokes no systemd, firewall, or container operations.

Focused Go tests cover independent output, boot settings, identity/name independence, legacy contracts and read purity, stopped-instance reservations, ports, resources, malformed inputs, path overlap, symlinks, and atomic inactive staging. `tests/test-instance-runtime-mount.sh` uses temporary fixtures and stubbed mount identity checks. The optional Go generator test requires explicit `JUSTVOXEL_QUADLET_GENERATOR` selection and runs only the generator's dry-run mode; it starts no containers. These tests have been added but not executed for Phase 2. Formatting and shell syntax checks are separate from test or runtime acceptance.

Phase 2 introduced no first-run multi-instance startup, WebUI creation flow, firewall changes, or scheduled backups. Phase 3A adds the backend activation and lifecycle behavior described below; Phase 3B adds instance-aware cross-play, player-aware maintenance and host shutdown coordination below.


## Independent activation and lifecycle (Phase 3A)

The administrator-only Management API can now create and activate independent
Java instances. This backend phase does not add a WebUI wizard, backups, or
multi-instance host shutdown coordination. The legacy service, environment,
Quadlet template, world, identity, backup timer, and reset/import/restore routes
keep their existing contracts. Independent instance routes reject the legacy
identity rather than silently targeting `minecraft.service`.

Each registered new instance has one authoritative private record at
`/etc/justvoxel/instances/<id>/config.json`. It stores schema version 1, the
Phase 2 runtime settings (including the generated private RCON password),
trusted storage identity and derived data location, commitments for the live
environment and Quadlet, and firewall rules this instance owns. Configuration
is separate from the identity and friendly-name records. Runtime directories
are root:root `0700`; records, journals, environments, and published Quadlets
are root:root `0600`. Files are synced and atomically published, and runtime
reads reject symlinks throughout the path. Read APIs return a dedicated public
settings structure that excludes credentials, account IDs, and host paths.

The public creation request is `{"display_name":"Family Survival","runtime":{…}}`.
The `runtime` object accepts the following fields; it accepts no paths, image
repositories, container names, unit names, environment overrides, account IDs,
or private credentials:

```json
{
  "java_port": 25566,
  "bedrock_enabled": false,
  "bedrock_port": 0,
  "java_memory": "1G",
  "container_memory": "2G",
  "software": "paper",
  "version": "1.21.4",
  "paper_channel": "default",
  "image_tag": "stable",
  "start_on_boot": false,
  "eula_accepted": true,
  "timezone": "UTC",
  "game_mode": "survival",
  "difficulty": "normal",
  "max_players": 20,
  "whitelist": true,
  "enforce_whitelist": true,
  "motd": "Family Survival"
}
```

This is a request-shape example, not a recommended current Minecraft version.
Creation verifies software/version availability with the existing metadata
helpers and derives container UID/GID from the configured legacy installation.
A complete configured legacy server and its immutable identity are required.
Phase 3B supports independent Paper/Purpur cross-play through the instance-specific startup helper.

Creation takes the existing setup/restore/migration/reset operation locks and
the shared Minecraft maintenance lock. It refreshes legacy configuration,
physical RAM, and every persisted independent configuration, including stopped
instances. Invalid records and orphan instance Quadlets fail closed; an outstanding activation journal blocks
new applies and starts. Port and memory checks use configured reservations,
including the legacy server, rather than free memory or listening sockets.
Additional IPv4/IPv6 TCP bind checks detect occupied host ports. During apply,
new and retired ports remain held until publication and rollback finish.

Before registration, creation validates the full proposed configuration and
checks the exact existing storage mount identity, canonical storage paths,
root-owned storage parents, at least 1 GiB of available storage, and absence of
an existing candidate instance data tree. It registers a generated immutable
ID, persists the configuration, stages the complete set using Phase 2 under
`/var/lib/justvoxel-instance-runtime`, and validates generated units with the
installed Quadlet generator's dry-run mode. Staging stays outside Quadlet
search paths and Minecraft storage. The staged files are then published only
for the selected instance. Creation provisions only its derived data directory;
it never starts a container or deletes, moves, adopts, or relabels existing
world data. A mount-namespace helper checks storage against the host mount
namespace and never repairs existing world ownership or labels. A small private
`.provision.json` marker in a newly created instance parent allows completing
ownership only on its still-empty, root-owned new data directory after an
interrupted mkdir/chown; missing or invalid evidence fails closed.

| Route | Permission and behavior |
| --- | --- |
| `POST /v1/minecraft/instances` | Administrator; register, configure, provision, validate, activate; return the immutable ID |
| `GET /v1/minecraft/instances/runtime` | Authenticated read; list new logical instances and their independent runtime states |
| `GET /v1/minecraft/instances/{id}/configuration` | Authenticated read; public runtime settings only |
| `PUT /v1/minecraft/instances/{id}/configuration` | Administrator; full `{"runtime":{…}}` settings apply |
| `POST /v1/minecraft/instances/{id}/activate` | Administrator; retry activation of the persisted configuration; body `{}` |
| `POST /v1/minecraft/instances/{id}/recover` | Administrator; retry verified rollback from private activation evidence; body `{}` |
| `GET /v1/minecraft/instances/{id}/status` | Authenticated read; actual unit/container evidence and storage availability |
| `POST /v1/minecraft/instances/{id}/start` | Administrator or operator; request startup of the selected committed runtime; body `{}` |
| `POST /v1/minecraft/instances/{id}/stop` | Administrator; player-aware warning/countdown and ordinary graceful shutdown; body `{}` |
| `POST /v1/minecraft/instances/{id}/restart` | Administrator or operator; verified graceful stop followed by startup; body `{}` |

Operators retain the existing shared restart allowance and cooldown; independent
instance restarts consume that allowance and audit the internally derived
service. All mutation requests use the existing authenticated API and strict
JSON decoder. Selected-instance Stop and Restart refuse failed/unparseable
RCON responses or uncertain service/container evidence. Phase 3B administrators
may interrupt online players with a mandatory warning/countdown; operators
continue to require an empty server. There is no force stop. The ordinary
itzg shutdown delay, Podman stop timeout, and systemd stop timeout remain intact.

Full settings applies require the selected instance to be stopped, or failed
with actual evidence that no live container remains. Changing
only Start on Boot may be applied while running, while preserving the committed
environment and non-install Quadlet content. ON uses the Phase 2
`[Install]`/`WantedBy=multi-user.target` configuration; OFF omits it. Neither
calls `systemctl enable` or changes current running state. Software changes on
an already activated instance are outside Phase 3A; world conversion requires
separate reviewed behavior.

Firewall activation manages only the selected Java TCP port, in the current
default firewalld zone, separately for runtime and permanent configuration.
Already-open rules are preserved and are not claimed as owned. Port changes
remove only recorded instance-owned rules after confirming the previous port
is not occupied. Apply and rollback use explicit port actions and queries;
they never reload firewalld or change unrelated services/rules. Failed applies
reverse only the planned instance-owned changes. Phase 3B extends the same
ownership model to enabled Bedrock UDP ports. Omitted ownership protocols in
Phase 3A records continue to mean TCP.

Before any provisioning or live mutation, the Agent durably publishes
`pending.json` beside `config.json`. It contains the previous record, target
record, exact previous private runtime bytes, and planned firewall changes.
The new Quadlet's `validate-instance-runtime-commit` startup guard requires
matching immutable registration, committed hashes, and absence of this journal. A crash at any stage
therefore cannot boot an incomplete runtime. Successful publication syncs both
live files and the authoritative record, reloads systemd, verifies generated
service/container evidence, and clears the journal durably. It starts no unit.

Failure restores the exact previous configuration and private files, reverses
owned firewall changes, reloads systemd, and verifies restoration before
clearing evidence. It preserves all instance data. Incomplete rollback reports
`recovery_required`, retains evidence, and blocks uncertain startup. The
administrator can retry the recovery route, then activate the same ID; a failed
creation response includes its ID when registration already succeeded. If
registration succeeded but the initial configuration write did not, a full
configuration PUT can complete it only after proving no active files or data
tree exist. Recovery never starts a service. A boot-only rollback can preserve
a currently running instance; other rollback attempts refuse live containers.

Status distinguishes `configured` (not activated), `running`, `stopped`,
`starting`, `stopping`, `failed`, `recovery_required`, `invalid_configuration`,
and `unavailable`. It checks systemd and exact container evidence; a failed or
exited container is not counted as running. Reads never create directories,
repair files, query upstream versions, or start services. Messages are bounded
and exclude private runtime bytes and command diagnostics.

Focused Go tests and `tests/test-instance-runtime-activation.sh` cover
independent activation/creation, persistent settings, boot preferences,
instance lifecycle and player refusal, host/configured port conflicts, memory
reservations, mount failures, private files, authorization, rollback/retry,
concurrent-operation protection, secret boundaries, and data preservation.
They use temporary filesystem fixtures and mocked systemd, Podman, firewalld,
and storage operations. Tests have been added but **not executed** for Phase 3A.
Only Go formatting and shell syntax checks were performed. No image build,
container execution, deployment, or runtime acceptance is claimed.

Phase 3B adds instance-aware maintenance, cross-play and host shutdown
coordination below. The creation wizard, independent backup workflows and
VM runtime acceptance remain separately scoped work.


## Independent maintenance (Phase 3B)

All routes below require the existing authenticated session. They derive only
`minecraft-<validated ID>` from authoritative independent runtime/identity
records, and never fall back to `minecraft` or read legacy environment data.

| Route | Permission and behavior |
| --- | --- |
| `GET /v1/minecraft/instances/{id}/players` | Any authenticated role; offline, online count/maximum, or explicit unknown state |
| `GET /v1/minecraft/instances/{id}/logs` | Any authenticated role; selected unit's last 100 journal entries, at most 16 KiB of sanitized text |
| `POST /v1/minecraft/instances/{id}/console` | Administrator; `{"command":"say …"}`, up to 256 bytes; no other console commands |
| `GET /v1/minecraft/instances/{id}/version` | Any authenticated role; configured software/version and explicit absence of independent backups |
| `POST /v1/minecraft/instances/{id}/version/verify` | Administrator; `{"version":"1.21.5"}` checks existing software-specific upstream availability without changing configuration |

Console output is discarded; announcements are audited without their text.
RCON runs inside the selected container using its private configuration; no
password is put in arguments, API responses, or logs. Calls have a five-second
limit. Player lists must match the complete expected response and consistent
counts; unavailable, malformed or oversized responses mean unknown. Logs use
fixed journal arguments, bounded capture, credential redaction and omission of
private diagnostic lines. No arbitrary shell, systemctl or Podman arguments
are accepted.

Administrator Stop/Restart holds the Phase 3A appliance locks and shared
Minecraft maintenance lock across player checking, graceful stop, verification
and any subsequent start request. Occupied instances receive a 60-second announcement,
then the existing 30/15/10/5/4/3/2/1-second warning sequence while systemd uses
the ordinary itzg graceful shutdown. The countdown and stop confirmation share
a 150-second safety deadline. Failed announcements or timeout return failure
without a force stop or restart fallback. Empty instances still use the normal
container stop delay, protecting players who join after the check. Operators
retain Phase 3A's empty-server restriction, shared allowance, cooldown and
audit accounting. Other instances are never stopped or restarted.

Paper/Purpur cross-play is configured through the stopped-only runtime settings
apply, with `bedrock_enabled` and `bedrock_port`. Vanilla is rejected. Every
configured instance, including stopped instances, reserves its Java TCP and
enabled Bedrock UDP ports alongside legacy reservations. Quadlets publish the
selected host ports to container-local Java 25565 and Bedrock 19132. Only owned
runtime/permanent UDP firewall rules are removed or rolled back; already-open
rules remain unowned. Recovery accepts old TCP ownership records and validates
both protocols against the previous/target runtime evidence.

`instance-crossplay-start` validates the selected committed runtime, takes the
same maintenance lock with a bounded wait, and prepares only that instance's
plugins. It reuses managed Geyser/Floodgate metadata, checksum validation,
prepare-before-replace and rollback functions. Existing canonical binaries
survive upstream outages. Private durable staging retains failed rollback
artifacts; the next start retries rollback before proceeding. Disabling removes
only managed Geyser/Floodgate binaries at the next start, preserving their
configuration and identity material. Geyser uses the instance's internal ports
and Floodgate authentication. No legacy plugin folder is loaded; Podman `:Z`
continues to own independent volume labels, without explicit relabeling.

Administrator and operator Start, and the startup portion of Restart, submit
`systemctl --no-block start -- minecraft-<ID>.service` while the Agent holds
the maintenance lock. They return after job submission, without waiting for
`ExecStartPre` or server readiness. The Agent releases its lock after the
action and audit/quota accounting; the startup helper then acquires that same
lock independently. A synchronous start here would wait on its own locked
prerequisite and fail its five-second lock timeout. No lock bypass or inherited
lock flag is used. Independent maintenance can win the lock before the helper;
the helper then waits up to five seconds and refuses startup if still blocked,
without preparing plugins. Boot-time startup and systemd automatic retries use
the same helper and serialization. Independent activation recovery only rolls
back configuration/firewall evidence and reloads systemd; it never starts a
server. Legacy recovery startup remains on the separate `minecraft.service`
path.

The focused startup lock regression uses the real lifecycle API, a temporary
flock and a controlled prerequisite process for administrator/operator Start
and Restart. It requires the prerequisite to encounter the Agent's held lock,
acquire it after the request releases it, and exclude independent maintenance
until preparation completes. The helper fixture also checks that independent
maintenance blocks preparation and that direct startup succeeds after release.
These regression tests have been added but not executed.

Version changes use the existing administrator configuration apply and existing
software/version metadata verification. They require a stopped instance and
an explicitly pinned target. Until Phase 4, an already activated instance may
change versions only when its host-namespace data directory is verified empty.
Any existing data, unknown storage evidence or a moving target is refused with
an independent backup/migration explanation. Software-family conversion remains
refused. Verification of availability alone does not authorize a world upgrade
or imply that an independent backup exists.

The existing host shutdown guard keeps the legacy path and checks independent
instances concurrently in a shared six-second window, with an eight-second
outer timeout. It reads private authoritative runtime/identity records and
committed hashes, confirms actually running containers, and sends itzg's
SIGUSR1 delay bypass only after reliably parsing zero players. Online players,
missing RCON, malformed responses and unhealthy records retain normal graceful
container shutdown. No server is started or forcibly killed. Warnings are
aggregate and bounded, and shutdown takes no maintenance lock. New Quadlets
order before the guard at startup, so the guard runs before their shutdown;
no generated service is enabled manually. Older committed bundles remain
usable; stop and reapply their runtime to install the new Quadlet ordering.
A running Start on Boot-only change preserves its existing committed bundle.

Focused Go and temporary-filesystem helper tests cover player protocols,
container targeting, warnings and failure isolation, roles, bounded diagnostics,
UDP reservations/ownership, empty-data version safety, independent plugin
preservation/rollback, and coordinated shutdown. Phase 3A restart quota/audit
and legacy adaptive-shutdown tests remain applicable. These tests have not been
run for this phase. Formatting and Bash syntax checks do not establish runtime,
firewall, upstream plugin compatibility, generated-unit or VM acceptance.

Phase 4 still needs independent backups and their recovery/maintenance contracts
before safe version upgrades of populated worlds can be offered. No independent
backup implementation, scheduling, WebUI workflow or deployment is added here.

## Independent cold backups and scheduling (Phase 4A)

Phase 4A adds backend backups for independently activated instances. The legacy
`minecraft-backup.service`, `minecraft-backup.timer`,
`/etc/justvoxel/minecraft-backup.env`, worker, operator allowances, archive
locations, restore and migration behavior remain unchanged. There is no new
WebUI, restore operation, version-upgrade permission, daemon, or automatic retry.

The existing configured backup-storage root is reused. Each independent instance
writes directly into `<backup-root>/<immutable-id>/`. New archives are named
`instance-backup-<software>-YYYY-MM-DD-HHMM[-collision].tar.gz`, with an adjacent
`.meta.json`. The distinct prefix is intentional: legacy restore discovery scans
all ID folders for `minecraft-*.tar.gz`. Independent backups must stay outside
that legacy discovery/restore contract until Phase 4B can check their original
instance. Existing archives are neither renamed nor moved. Friendly names never
form paths. The archive contains `data/` and its Minecraft files, without adding
an Instance ID or private host runtime configuration to the world.

Private settings and status are separate from Quadlets in the existing private
`/etc/justvoxel/instances/<id>/` runtime directory:

- `backup-settings.json`: `enabled`, daily local `time` (`HH:MM`), and `retention`
  (1–365 successful backups). Defaults are OFF, `04:00`, and 7.
- `backup-status.json`: last attempt, last successful archive publication, stage,
  bounded failure message, archive publication result, original running state,
  and durable restart-required intent. These use the existing atomic, synced,
  root:root `0600` private-file publisher.
- `backup-schedule-pending.json`: previous settings, unit bytes, and timer state
  while a settings transaction is applying. A failed apply rolls back. If apply
  and rollback are interrupted or fail, a settings PUT retries rollback before
  applying the requested configuration. Backups refuse pending schedule changes.

Authenticated routes beneath `/v1/minecraft/instances/{id}/backups` are:

| Method and suffix | Access | Behavior |
| --- | --- | --- |
| `GET` (no suffix) | Administrator, Operator, Viewer | Completed archive/metadata inventory |
| `GET /status` | Administrator, Operator, Viewer | Last attempt/publication and restart state |
| `GET /settings` | Administrator, Operator, Viewer | Independent schedule and retention preference |
| `PUT /settings` | Administrator | Strict JSON `{enabled,time,retention}`; transactionally apply units/settings |
| `POST` (no suffix) | Administrator, Operator | `{}` requests Backup Now through the native service |
| `POST /run` | Local root Unix peer only | Internal synchronous systemd worker entry; WebUI sessions cannot call it |

Manual requests return acceptance, not backup success. Operators use the same
SQL allowance and global/personal cooldown as legacy manual backups, under the
same process mutex. Switching instance or using the legacy route does not reset
those limits. Systemd rejection does not consume the allowance; an accepted job
consumes it even if the subsequent backup fails. Reads never create settings,
probe files or folders, apply configuration, start Minecraft, or start a backup.
The inventory only exposes a strict metadata allowlist, with bounded capture;
unknown/incomplete files are excluded. It is not a restore-eligibility decision.

Units are authored in `templates/systemd/minecraft-instance-backup.*.in`, rendered
as ordinary `/etc/systemd/system/minecraft-backup-<id>.service` and `.timer`.
They are installed on the first explicit settings update or manual request.
Disabling stops/disables the timer while retaining its manually usable service.
Enabling uses native systemd timer enable/start, `Persistent=true`, one-second
accuracy, and no randomized delay. Missed scheduled runs may execute when an
enabled timer starts after reboot. Viewing settings does not enable a timer.
Generated Minecraft Quadlet services are never enabled by this backup code.

Enabled instance schedules must be at least 30 minutes apart on the 24-hour
clock, including across midnight and from the enabled or active legacy daily
backup. The legacy timer is read from systemd; unknown timer state, non-daily
expressions, multiple calendars, timer overrides, or unrecognized timer triggers
refuse enabling/updating an enabled schedule. This check cannot prevent a later
legacy schedule edit or a manually edited native unit. Shared maintenance locks
still prevent simultaneous cold backups; conflicts fail without retrying. These
are local wall-clock start preferences, not a guarantee of backup duration or
elapsed separation around clock/DST changes.

The Agent takes the existing setup, restore, migration and shared maintenance
protection without waiting behind an active operation. It verifies committed
instance/runtime/source storage, exact backup mount identity and canonical
non-symlink destinations, and conservatively checks destination space before
shutdown. Missing RCON or malformed player status refuses interruption. Online
players receive Phase 3B's graceful warnings; shutdown must be confirmed and is
never forced. An initially stopped instance stays stopped. The helper rechecks
storage and capacity before archiving, uses an exclusive temporary filename,
reads gzip to EOF for CRC/trailer validation, verifies tar entries, calculates
SHA-256, and publishes the archive atomically without replacing an existing file.
Metadata is staged and published separately; inventory requires both completed
files with matching instance, archive and size. Filesystem sync or publication
failure is reported, including on targets lacking required filesystem semantics.

Metadata is bounded and contains the immutable ID, descriptive friendly name,
software/version, UTC creation time, size, committed source configuration hash
and archive SHA-256. It contains no RCON password, credentials, token or private
runtime environment. The checksum records creation-time integrity; Phase 4B must
recompute it and validate archive contents/compatibility before accepting restore.

Retention runs only after verified publication and any required restart succeed,
then reacquires maintenance protection and selects recognized completed
pairs for that ID, and leaves legacy/other-instance/unrecognized/incomplete files
and recovery evidence alone. The existing shared restore/maintenance protection
excludes active restore selection/use. Phase 4B must hold those same locks while
selecting and using an archive. Metadata is retired with its archive. Retention errors or a conflict while
reacquiring maintenance are reported as warnings on the already successful
backup; no retention occurs before a failed archive operation or restart.

On ordinary failure the helper cleans only its own temporary files. SIGKILL or
power loss may leave hidden `.instance-backup-*` files or an archive without
completed metadata; these remain unadvertised, are never counted for retention,
and are not glob-deleted. No failure deletes a completed backup or world data.
The Agent releases maintenance protection before submitting a required start,
so the Phase 3B ExecStartPre helper can acquire it. Restart is synchronous and
observed; failure retains restart-required intent and reports failure even if the
archive was published successfully. An interrupted persisted running attempt is
shown as interrupted once its native worker is inactive. An administrator can
retry Backup Now to reuse the preserved running intent; no terminal journal
editing or reset is required. Status/metadata write failure is never success.

Added Go fixtures cover independent lifecycle, occupied/unknown players,
timeouts, release-before-start, restart failure/intent, maintenance collisions,
shared legacy/operator quota, authentication, strict inputs, read-only endpoints,
30-minute/midnight/legacy spacing, persistent timers, disabled schedules, failed
apply rollback and interrupted schedule recovery. `tests/test-instance-backups.sh`
uses temporary directories and mocked mount/runtime tools for archive contents,
verification/publication failure, capacity/mount mismatch, interrupted cleanup,
independent retention, symlinks, partial metadata, and legacy isolation. These
tests were added but not run. Only gofmt and `bash -n` were run; compilation,
source-test, image, native-systemd, network-filesystem, SELinux and VM acceptance
remain unverified.

Phase 4B still needs instance-aware restore planning, original-instance and
configuration compatibility checks, checksum/content validation, selected-archive
protection through restore completion/recovery, and transactional cold restore
with rollback. Legacy restore must remain separate. Populated-world version
changes remain restricted by the existing Phase 3B rules.
