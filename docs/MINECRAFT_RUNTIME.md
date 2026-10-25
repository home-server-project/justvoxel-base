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

Java uses TCP 25565 by default and managed Bedrock cross-play uses TCP and UDP 19132 by default, but setup can render administrator-selected host ports. RCON is enabled inside the container for local administration and is never published as a host port.

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

The Agent explicitly assigns the store ownership when creating the optional names directory. At startup, under the identity-directory lock, it can repair the empty wrong-group directory left by the C3 acceptance release: the existing directory must be opened without following symlinks, already have the expected UID, have exactly `0700` permissions with no special bits, and contain no entries. Only a differing GID is corrected to the expected root:root ownership, synced, and verified again. Startup never creates missing name metadata. Other ownership or mode failures, populated wrong-group directories, symlinks, and non-directories remain untouched and fail registry validation.

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
| Data | `<appliance Minecraft storage root>/minecraft-instances/jv-a1b2c3/data` |

The default appliance storage root `/var/lib/justvoxel` yields `/var/lib/justvoxel/minecraft-instances/jv-a1b2c3/data`; another configured storage root yields the same relative instance paths. The appliance storage policy currently comes from the parent of `DATA_PATH` and its mount identity fields in `/etc/justvoxel/justvoxel.conf`, when present; absence uses the existing system-storage default. No fixed server, environment, Quadlet, service, or identity is required. Backup storage remains appliance-wide. Data never enters the backup root or Phase 1 identity directory; overlapping paths and mounts that cannot safely hold the derived instance paths are rejected. It does not create, move, delete, repair, or relabel worlds. All existing path components must be directories without symlinks. Future activation must explicitly provision and verify the data directory before startup.

`minecraftInstanceRuntimeConfig` holds per-instance ports, Java and container memory, software, selected version, Paper channel, image tag, startup settings, explicit EULA acceptance, non-root container UID/GID, private RCON password, and Start on Boot. RCON passwords follow the existing generated hexadecimal convention (32–128 hex characters). The renderer reuses `templates/config/minecraft.env.in` and the established Paper/Purpur/Vanilla capability rules. The new Quadlet template uses `docker.io/itzg/minecraft-server:<selected tag>` with the existing moving or pinned tag convention; rendering does not resolve tags, fetch metadata, or resolve digests. Version inputs are a selected pin or `LATEST`; upstream availability checks remain a caller responsibility. Latest Paper requires its existing experimental channel convention.

The trusted internal caller must supply **every configured instance, including stopped instances**, plus a fresh appliance physical RAM/configured-storage snapshot. There is no fixed-server port or memory reservation. This phase does not yet provide the authoritative persistent multi-instance configuration store or activation lock. Validation reserves Java TCP and managed Geyser TCP/UDP ports, rejects collisions across all configured instances, and checks the 1–65535 range. A managed Geyser reservation cannot share its numeric port with Java or another Geyser instance. Unrelated TCP and UDP listeners may share a numeric port when neither conflicts with managed Geyser. Disabled Bedrock requires a zero port. Phase 2 originally rejected enabled instance Bedrock after validation. Phase 3B adds the independent startup helper described below; the legacy global `managed-crossplay-start` is never attached to independent units.

Memory uses positive `M`/`G` values (case insensitive), between 512 MiB and 1 TiB per quantity. Each container must allow at least 512 MiB above its Java allocation. Combined configured container limits, including stopped instances, must leave at least 1 GiB of physical RAM for the appliance. Free memory, listening sockets, swap, and zram do not determine reservations. Startup values and immutable IDs are validated before any output is returned; malformed JSON and unknown fields are rejected by the internal decoder. There are no arbitrary environment, image repository, unit, or data-path overrides.

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
helpers and obtains container UID/GID from the existing appliance-level
`resolve_minecraft_ids` helper shared with Setup planning: valid invoking sudo
account IDs, otherwise the `voxel` account, otherwise its established `1000:1000`
default. It never derives ownership from another server. Creation is self-sufficient
on an appliance with zero configured servers. Every server-specific value comes
from the selected immutable `jv-*` ID and its authoritative registry, configuration,
and committed runtime evidence; friendly names remain presentation only.
First-run Setup Apply is intentionally unchanged in convergence slice C1.
Phase 3B supports independent Paper/Purpur cross-play through the instance-specific startup helper.

Creation takes the existing setup/restore/migration/reset operation locks and
the shared Minecraft maintenance lock. It refreshes appliance storage policy,
physical RAM, and every persisted instance configuration, including stopped
instances. Invalid records and orphan instance Quadlets fail closed; an outstanding activation journal blocks
new applies and starts. Port and memory checks use configured reservations,
across `jv-*` instances, rather than free memory or listening sockets. Pending
activation/recovery evidence continues to block unsafe publication.
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
ownership model to enabled managed Geyser TCP/UDP ports. Omitted ownership protocols in
Phase 3A records continue to mean TCP. Legacy fixed-runtime helpers record newly created Geyser TCP rules under `/etc/justvoxel/geyser-tcp-firewall/`, with the exact port and zone. Disable, port changes and reset remove only TCP rules with that evidence; pre-existing TCP rules and older deployments without TCP ownership evidence are preserved.

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
| `POST /v1/minecraft/instances/{id}/console` | Administrator; `{"command":"list"}`, one command up to 1024 bytes; canonical `stop` and `minecraft:stop` (with optional slash) are refused. `{"discover":true}` requests bounded help discovery |
| `GET /v1/minecraft/instances/{id}/version` | Any authenticated role; configured software/version and explicit absence of independent backups |
| `POST /v1/minecraft/instances/{id}/version/verify` | Administrator; `{"version":"1.21.5"}` checks existing software-specific upstream availability without changing configuration |

Console returns `{ok,id,output}` with sanitized text; command text and output are omitted from audit events. Commands remain a single argument to the existing in-container RCON tool, never a host shell command. Discovery returns server-exposed `{name,usage,description}` entries in `commands` plus `partial` when a bound or later-page failure prevents completion. It reads at most ten help pages and 32 KiB total within a shared 20-second deadline, retaining the normal per-call limits.

The Administrator-only Minecraft Settings RCON tab uses `POST /api/minecraft/workspace/console?server=<id>` with CSRF and matching `server_id` form fields. Both execution and discovery bind to that immutable selected ID. Browser selection changes discard stale results and reset the transcript, command history and catalog; these are page-memory state only.
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

Paper/Purpur/Spigot cross-play is configured through the stopped-only runtime settings
apply, with `bedrock_enabled` and `bedrock_port`. Vanilla is rejected. Every
configured instance, including stopped instances, reserves its Java TCP and
enabled managed Geyser TCP/UDP ports. Quadlets publish the
selected host ports to container-local Java 25565 and Bedrock 19132. Only owned
runtime/permanent TCP/UDP firewall rules are removed or rolled back; already-open
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

First-run Setup also submits normal asynchronous Start, then waits for the
operation-bound instance to report Running before checking Minecraft readiness.
Its existing 20-minute Verify deadline covers both waits. Starting is expected;
a stopped service has only a two-second submission grace before activation.
Setup rollback cancels an in-flight start with a bounded synchronous stop and
confirms a safe inactive state before activation recovery or instance deletion.
Explicit Setup recovery uses the same rollback path if that instance has since
become Running; persistent evidence and the setup marker remain until recovery
is verified. Successful Setup configures instance backups and clears the marker
only at normal commit.

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

Manual requests return acceptance, not backup success. Operators use a
shared SQL allowance and global/personal cooldown across all instance Backup Now
requests, under the same process mutex. Switching instance does not reset those
limits. Systemd rejection does not consume the allowance; an accepted job
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
runtime environment. Restore recomputes the checksum and validates complete
archive contents and compatibility before staging and again on the staged copy.

Retention runs only after verified publication and any required restart succeed,
then reacquires maintenance protection and selects recognized completed
pairs for that ID, and leaves legacy/other-instance/unrecognized/incomplete files
and recovery evidence alone. The existing shared restore/maintenance protection
excludes active Restore selection/use. Interrupted Restore journals also pin the
selected archive against retention until completion or verified rollback.
Metadata is retired with its archive. Retention errors or a conflict while
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
shared Operator quota, authentication, strict inputs, read-only endpoints,
30-minute/midnight/legacy spacing, persistent timers, disabled schedules, failed
apply rollback and interrupted schedule recovery. `tests/test-instance-backups.sh`
uses temporary directories and mocked mount/runtime tools for archive contents,
verification/publication failure, capacity/mount mismatch, interrupted cleanup,
independent retention, symlinks, partial metadata, and legacy isolation. These
tests were added but not run. Only gofmt and `bash -n` were run; compilation,
source-test, image, native-systemd, network-filesystem, SELinux and VM acceptance
remain unverified.

### Independent Restore (Phase 4B)

Administrator-only routes beneath `/v1/minecraft/instances/{id}/backups/restore`:

| Method | Route | Request / result |
| --- | --- | --- |
| POST | `/plan` | `archive`; returns reviewed `plan_id`, immutable ID, integrity, compatibility, capacity, running intent and player interruption requirements |
| POST | `/apply` | `archive`, reviewed `plan_id`, `confirm_destructive: true`, and `confirm_players: true` when running; returns 202 acceptance |
| GET | `/status` | Current stage, selected archive, running intent and `recovery_required` |
| POST | `/recover` | `confirm_players: true`; returns 202 for retryable rollback and runtime verification |

Only an archive belonging to the original immutable Instance ID is accepted.
Friendly-name changes do not change identity. Apply recomputes the plan and
refuses stale evidence or missing confirmation. Software family and exact pinned
Minecraft version must match. LATEST, snapshots, software conversion and version
changes are refused. Each archived world must have readable bounded gzip/NBT
`level.dat` metadata whose release name matches the pinned version and whose
DataVersion agrees with its embedded Version ID. Worlds without verifiable
release metadata are refused. Populated-world version changes remain restricted
by the existing Phase 3B rules.

The private helper validates committed runtime/configuration, registry ownership,
source/destination mount identity, root-owned private archive/metadata, size and
SHA-256, full gzip CRC/trailer, tar headers/end markers, paths, duplicates,
ancestor conflicts, links, special files, permissions and bounded expansion.
Planning requires staging capacity for the compressed copy, expanded replacement,
entry overhead and 64 MiB headroom. Rollback uses renames, preserving the original
without requiring a second world-sized copy. A disconnected archive source cannot
prevent recovery after staging.

All copying, extraction, compatibility and integrity checks finish in a private
`.restore-<transaction>` directory on the destination filesystem before downtime.
Phase 3B's player-aware shutdown must verify the selected container is stopped.
A root-private `restore-journal.json` is synced before creating transaction state
and before each destructive transition. Linux atomic directory exchange activates
the replacement while preserving the original, then moves the original into the
transaction directory. Device/inode identities and the committed configuration
hash resolve interruption around each rename without guessing ownership.
Filesystem support for atomic exchange and durable fsync is required; failures
retain evidence and use the same recovery path.

Activation verifies committed runtime, storage and the original running intent;
a running instance must answer RCON. Failure attempts rollback under the same
protection. Recovery stops only the selected running instance through the graceful
path, restores the original by inode, preserves a failed replacement, and verifies
the original running/stopped intent. Incomplete rollback retains both worlds and
its journal, reports recovery required, and allows administrator retry. A
nonterminal or invalid journal blocks further independent backup/configuration
mutations. Recovery never requires editing journals, deleting locks or resetting
the appliance. Startup rejects interrupted journals except for a transaction-bound
live Agent permit during runtime or rollback verification; stale permits cannot
survive reboot or Agent PID reuse.

Lock acquisition is nonblocking throughout:

- Restore/Recover: Agent active claim, operation-store mutex, setup, restore,
  data-migration and migration claims, shared restore-exclusion flock, maintenance.
- Legacy backup: restore-exclusion, then its existing maintenance protection.
  Existing maintenance-owning callers still acquire exclusion nonblockingly.
- Independent backup/lifecycle: existing operation claims and maintenance;
  the Restore active claim or durable journal refuses admission.

The fixed exclusion identity is
`/run/justvoxel-minecraft-restore-exclusion.lock`. Restore keeps its descriptor and
operation claims through staging, activation, startup, rollback and recovery.
Only maintenance is released for synchronous ExecStartPre, which can then acquire
it normally. Legacy backup must first acquire exclusion and therefore cannot
enter this handoff. No new environment-variable lock bypass is used. Failed
acquisition unwinds acquired claims; there is no circular waiting. Completion or
safe abandonment releases descriptors, while recovery evidence stays durable.
Process termination releases flocks naturally. Lock files are never unlinked.
The legacy worker changes only by adding this exclusion; its service, timer,
archive format, storage, retention, quotas and shutdown/restart behavior stay as
before.

Completed transactions retain the original world (or failed replacement), staged
archive and terminal journal for inspection. Automatic deletion of this recovery
data is outside this phase; repeated restores therefore require additional free
space. Legacy Restore APIs/helpers remain separate.

The added Go tests cover authorization, strict input, reviewed-plan confirmation,
staging order, player-aware stop, running intent, rollback/retry and actual
separate-process flock acquisition during startup and rollback. Temporary-filesystem
fixtures in `tests/test-instance-restore.sh` cover identity, integrity, tar safety,
world compatibility, capacity, mount refusal, interruption, rename recovery,
retention pins and legacy/other-instance isolation. Dedicated tests in
`tests/test-legacy-backup-restore-exclusion.sh` run the real legacy worker with
mocked appliance commands and separate flock holders, covering normal backup,
bidirectional exclusion, handoff, nonblocking failure, completion and termination.
Root-owned fixtures require root or an authorized rootless user namespace. Tests
are added but intentionally unrun in this task; gofmt and shell syntax checks do
not establish compilation, runtime, filesystem, SELinux or VM acceptance.

## Add Server wizard (Phase 5)

The administrator's Minecraft Servers entry opens the existing Setup Wizard in an
independent creation context. Recommended and Advanced share the maintained
wizard and Review templates; Advanced retains Server, Cross-play, Memory,
Storage, Backups, and Review. Add Server drafts and reviews are separate from
legacy first-run state. Friendly server name and Minecraft MOTD remain separate.
Storage is fixed to the appliance's configured Minecraft storage; the agent
chooses the immutable identity and derives the data location. Backups reuse the
appliance destination with per-instance schedule and retention settings.

Creation calls the existing independent instance API and leaves the server
stopped. Backup settings are applied afterward. If that second step fails, the
wizard reports that the server exists and allows changing/retrying only its
backup settings without creating another server. An uncertain creation result
cannot be blindly resubmitted. Legacy Setup Plan/Apply, destination provisioning,
and recovery retain their existing contracts. Full independent management cards
remain Phase 6; the single existing How to Connect implementation remains
unchanged for Phase 8.

## Server inventory and overview (Phase 6)

The administrator-only `/minecraft/servers` workspace keeps the compact Add
Minecraft Server entry first, followed by the original server and independent
servers sorted by friendly name without regard to case. Independent inventory
joins the registry with the existing runtime inventory, which excludes the
legacy identity and includes registered servers whose runtime is unavailable.
Cards and selected overviews use public configuration and status reads. Failed
reads retain unavailable or attention states rather than implying a stopped
server. Player counts are omitted.

`/minecraft/servers/view/{id}` provides a read-only overview using the existing
Minecraft workspace styles. Its authenticated, CSRF-protected lifecycle forms
call the selected instance's existing Start/Stop/Restart APIs with no override
arguments or automatic retry. The `original` route uses legacy discovery,
status, and lifecycle contracts only. The legacy discovery contract does not
provide Start on Boot, so that summary is shown only for independent servers.
Settings editing, rename, deletion, and instance-aware How to Connect remain
later phases. Opening inventory or overview never changes runtime state.

### Phase 7: selected server settings and deletion

The Minecraft and Backups workspaces share explicit selected-server context.
`original` uses only the existing legacy contracts. Independent selection is a
validated active immutable `jv-*` identity, never a name or inventory position.
Changing selection reloads the document and discards pending settings reviews.
Invalid or stale selections fail closed. How to Connect remains unchanged.

Independent Memory, Gameplay and Cross-play reuse the existing controls.
Start on Boot is editable from Overview. Review commits the selected identity,
current public configuration and proposed allowed fields. Apply reads current
configuration again, refuses a stale review, overlays only those allowed fields,
and PUTs the full public runtime configuration. Software, version, channel,
image, timezone, EULA and private runtime details cannot be edited here. These
changes require a stopped server and never request a stop or restart.
Independent Players reads selected player status; independent whitelist mutation
is unavailable. Original Players and Version retain their existing behavior.

Independent Backups uses only the selected identity's Phase 4 inventory, status,
settings, Backup Now and Restore plan/apply/status/recover routes. It reuses the
Backups template, schedule controls, archive cards and Restore review. Independent
Restore replaces complete server data and remains **same immutable ID only**.
No cross-instance Restore or independent backup deletion is introduced.

Administrator rename calls the existing name route. Name metadata changes do not
change identity, data location, units or archive ownership. Deletion is
`POST /v1/minecraft/instances/{id}/delete` with strict JSON
`{"confirm_name":"Current friendly name"}`. Both the WebUI and Agent require
Administrator authority; WebUI requests also require CSRF. Original cannot be
deleted by this contract. The name is checked under the registry lock.

Deletion takes the existing nonblocking backup worker gate, then reuses Restore's
nonblocking claim order: operation mutex, existing
setup/restore/data-migration/migration claims, restore exclusion, then runtime
maintenance lock; the registry lock is acquired last without waiting. The Agent
refuses unsettled runtime state, queued lifecycle work, active Backup/Restore,
activation or backup recovery, and unverified storage/configuration evidence.
It never stops Minecraft automatically.

Original configuration Apply (`POST /v1/admin/configuration/apply`) uses the
same Agent mutation claim as Delete, including the backup worker gate. The
operation-store mutex and active-operation checks stay held with the persistent
claims, so concurrent requests in the same Agent cannot reuse process-tracked
locks as if they had acquired independent ownership. A conflict returns HTTP 409
before the configuration helper runs and is never audited as success. The
existing helper still owns all validation, player confirmation, configuration
backup/rollback, rendering, firewall updates and memory restart behavior. Its
response and successful-Apply audit contract are unchanged. Release occurs after
the helper exits on success, rejection, failure or invalid output. Configuration
Plan stays read-only and does not acquire this mutation claim.

The private journal is `/var/lib/justvoxel-instance-runtime/delete-{id}.json`,
outside the world and runtime directories. Intent records committed runtime and
storage evidence plus the exact data directory device/inode before retiring any
material. Startup validation refuses a pending deletion. An explicit repeated
Delete Server request resumes the same ID, same confirmed name and same evidence;
there is no background cleanup or rollback claim after world bytes are erased.
The selected backup timer is disabled, owned firewall entries and selected
runtime/native backup units are retired, and the exact data tree is removed in
the host mount namespace. The helper rejects symlink substitution, changed inode,
and nested mounts and uses descriptor-relative symlink-safe removal. Removal and
runtime cleanup are verified and synced before active identity retirement.
Historical names remain reserved, so Add Server cannot deliberately reuse the ID.
Only then is transaction evidence cleared.

`GET /v1/minecraft/instances/deletions` provides a separate safe recovery inventory
of incomplete deletions. These entries remain reachable in Minecraft Servers,
including when runtime configuration or active identity has already been retired.
The selected overview retains the typed-name Delete retry action when ordinary
settings are unavailable. Successful deletion redirects to Minecraft Servers.

Completed archives and metadata, retained historical names, sibling servers,
legacy runtime/data, shared storage roots and unrelated import/migration/export
material are preserved. Preserved archives remain historical archives; current
Restore remains tied to the deleted server identity. Creating a new server does
not make those archives restorable into its new identity.

The root-filesystem regression is `tests/test-instance-delete.sh`, intended for
independent verification with `podman unshare bash tests/test-instance-delete.sh`.
It stubs runtime discovery while executing the production filesystem remover on
real temporary directories. Implementation-pass validation is restricted to
formatting and syntax checks; application and root-namespace execution is left
to independent verification.

### Appliance-wide Start Over

`mjust start-over` calls `GET /v1/admin/start-over/plan` and
`POST /v1/admin/start-over/apply`. Both require local root Unix-peer identity;
bearer/WebUI administrator access does not authorize this operation. Planning
binds every authoritative modern immutable ID, committed runtime ownership,
storage/data identity, recovery evidence and configuration into a fingerprint.
Apply re-plans under the existing appliance Minecraft mutation exclusion.

Every running instance uses exact-ID player inspection and graceful shutdown
with the existing warnings/countdown. Unknown presence fails closed and online
players require explicit interruption confirmation. Failed instances require
proof of no live container and safe stop normalization. No instance is started.

Start Over reuses the durable deletion retirement stages with the explicit
`start_over` policy. This policy preserves data and verifies its pinned identity;
ordinary Delete continues deleting data and selected Reset is unchanged. Start
Over journals are excluded from ordinary Delete recovery inventory. Retries
resume the exact journal even after registry retirement; completed participants
remain retired. Unresolved evidence prevents new instance creation.

After all managed runtime/container/backup-unit/firewall and active identities
are retired, only the active setup policy pair is removed. Worlds/data, backup
archives/metadata, storage layout/mounts/ownership, SMB credentials, `/etc/fstab`,
authentication/users/history, images and unrelated containers remain preserved.
An optional verified root-only configuration archive is published atomically
before any runtime mutation. There is no fixed-runtime execution fallback; fixed
artifacts never authorize a modern participant. See `START_OVER.md` for the
complete boundary and operator flow.
