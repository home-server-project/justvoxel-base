# Plugin Workspace

## Cycle 1 — read-only discovery and inventory

Administrators can open **Control Center → Minecraft → Plugins**. The movable,
resizable window has Discover, Installed and Updates tabs. The original Cycle 1 capability is read only:
it cannot install, download, update, remove or hot load plugins, change server
configuration, or interrupt Minecraft.

Discover has one compact desktop toolbar: a bounded search field and Search
button, a fixed ON Modrinth source switch, an OFF disabled Hangar placeholder,
and Compatible only / Release only switches. Hangar makes no requests and is not
an available source. The toolbar may wrap in a genuinely narrow workspace.

Plugin results use compact cards of uniform height, with a fixed-size logo,
clamped name and description, release/platform/Minecraft badges and a short
status or dependency summary. The entire card opens its overlay by click, Enter
or Space. Keyboard focus is visible; selection uses the green Server Software
card language and stays visible while the overlay is open. There are no separate
Info or Install card buttons. Closing the overlay returns focus to the card;
details never expand inside the results grid. Installed and Updates use the same
whole-card selection and information/status overlays, without update or removal
actions. Installable Discover cards open one combined information/install
overlay; JustVoxel-managed and unavailable selections remain read only.
Supported Minecraft versions appear as a wrapping list in the compatibility
section. Dependencies are grouped by type, with identities
first and IDs as secondary metadata. Installed information has separate sections for JAR,
Metadata, Management and Update status. Installed and Updates cards show ownership
and a compact status; update and removal actions remain unavailable.
Discover uses three columns in wide windows, two in medium windows and one in
narrow windows. Buttons stay distinct from badges. Authoritative Modrinth project
IDs use real publisher logos through the Administrator-only same-origin
`GET /api/plugins/workspace/icon?project_id=<id>` route and the Agent's
`GET /v1/admin/plugins/icon?project_id=<id>` route. Only the Agent resolves the
fixed Modrinth project API and fetches the verified HTTPS project CDN image;
upstream URLs never reach the browser. PNG, JPEG and WebP are bounded to 512 KiB,
with no redirects or persistent image cache. Missing or rejected images leave
the existing fixed-size deterministic initials tile without affecting any
catalog or installation operation. Information and installation reuse the card identity tile.
Progress and terminal results retain the selected identity in browser memory,
bound to the returned operation ID; after reload they use fallback. Inventory
exposes the existing manifest project/release IDs only after exact
filename, SHA-512 and descriptor ownership verification. Installed and Updates
reuse the same-origin logo helper for that authoritative project ID; unidentified
JARs retain fallback without identity searches or changes to ownership records.
The overlay scrolls within its own bounds on desktop and mobile.

Paper and Purpur support plugins. Vanilla displays an unsupported state.
The workspace reuses appliance configuration discovery and runtime status for
server software, Minecraft version and the configured data location.

Discover searches Modrinth server plugins. Compatible only and Release only
start enabled. Compatibility means **Compatible according to publisher metadata**;
JustVoxel has not verified third-party runtime compatibility. Required, optional,
incompatible and embedded dependencies are publisher metadata only. If the active
Minecraft version cannot be determined, the compatible search stays unavailable;
turning off Compatible only permits browsing without claiming compatibility.
Discover measures the actual grid columns and available result height to choose
1–12 projects per page. Workspace resizing recalculates capacity after a brief
debounce and realigns the previous first-result offset to a page under the new
limit, preserving the approximate catalog position. The Agent accepts only
integer limits from 1 to 12 and defaults to 8 when omitted. Pagination stays at
the bottom-right of Discover, in the order **Page X of Y**, **Previous**, **Next**.
The page count uses Modrinth's authoritative total; Previous is disabled on the
first page and Next on the final page. Installed and Updates have no catalog
pagination. Release filtering can leave a page empty; Next continues to the next
catalog page. Upstream failures are shown as
unavailable metadata, never as proven incompatibility.

Installed reads regular JARs under the configured data directory's `plugins`
folder. It refuses a symlinked plugin folder and skips symlinked JARs and unrelated
files. Metadata reads inspect simple, bounded top-level name/version scalars in
`paper-plugin.yml` and `plugin.yml`; unreadable, malformed, unusually large or
complex metadata may leave the version or name unknown. This does not prevent
other JARs from appearing. No ownership database is created.

Geyser, Floodgate and ViaVersion are displayed as JustVoxel managed. Their
existing server update mechanism remains authoritative. All other JARs are
Manual / unmanaged. Updates reports **Update status unknown - manually managed**
for those JARs. Plugin names are never used to guess a Modrinth project identity.

The browser calls Administrator-only `/api/plugins/workspace/{discover,installed,updates}`
WebUI routes. The WebUI calls `/v1/admin/plugins/{discover,installed,updates}` over
the existing Management API Unix socket. The Management Agent alone reads local
JARs and contacts Modrinth's fixed HTTPS API. Requests have bounded timeouts and
responses, send a JustVoxel User-Agent, require no API key and reject redirects.
There are no mutation endpoints in this cycle.

## Stage 2 — third-party installation

Stage 2 permits installation of compatible third-party Modrinth releases
on Paper and Purpur when the current Minecraft version is known and publisher
file metadata is complete. Clicking an installable Discover card fetches the
authoritative plan and opens a single-column combined information/install
overlay. Turning off Compatible only or Release only still permits browsing;
incompatible, beta, alpha and JustVoxel-managed selections have read-only
information overlays with no generic Install action.

The combined overlay shows About this version, Compatibility, Dependencies,
Files to install, Installation and Restart as normal restrained information
sections. Required dependencies show their exact planned name/version and
**Already satisfied** or **Will be installed** status. Optional/incompatible
publisher entries remain informational without duplicating required entries.
Restart explains the verified cold backup, Minecraft start/restart for plugin
verification and return to an original stopped state; it does not promise
continuous uptime. Players appears only when the plan requires player-aware
confirmation, with online count and available names. The footer offers Cancel
and Install. This source implementation still requires human real-VM visual
acceptance.

Installation uses an authoritative Management Agent plan. The browser supplies
only Modrinth project/version IDs; it cannot choose download URLs, filenames,
hashes, paths or dependency lists. Review shows the selected release, publisher
compatibility, active server/Minecraft version, required dependencies, exact
files and sizes, and the configured `<DataPath>/plugins` location. Publisher
compatibility is a prerequisite, not a promise that the plugin will run.

Only **required** dependencies are resolved recursively and automatically
installed. Exact dependency versions must belong to the declared project and be
compatible releases. Project-only dependencies select the newest compatible
release. Optional, incompatible and embedded entries remain display-only Info
metadata and are not automatically downloaded. Cycles, conflicting versions,
filename-only requirements and unresolved identities block installation. Bounds
are six dependency levels, sixteen artifacts, 128 MiB per JAR and 256 MiB combined.

A deterministic SHA-256 fingerprint binds server type, Minecraft version, data
path, selected files, exact SHA-512 hashes and authoritative CDN identities, plus
dependency relationships. Apply re-resolves the plan and requires that exact
fingerprint. Player snapshots are excluded from the fingerprint and checked
separately. Artifact URLs and private transaction evidence are never returned to
the browser. Modrinth's fixed HTTPS API and `cdn.modrinth.com` are the only sources;
redirects are rejected, and response sizes and timeouts are bounded.

All needed JARs are downloaded, size/hash checked and inspected in private Agent
staging **before Minecraft downtime**. Staged Paper/Bukkit descriptors must provide
an unambiguous usable plugin name. The installer rejects manual filename or
plugin-identity collisions and never adopts, overwrites or deletes a manual JAR.
A previously managed dependency is reused only for the exact required version,
filename and verified hash. A different installed version blocks installation;
Stage 2 does not upgrade dependencies.

Geyser, Floodgate and ViaVersion remain **JustVoxel managed**, owned by their
existing server mechanisms. Their canonical catalog slugs suppress generic
Install. Independently, the backend checks canonical filenames and actual JAR
main-class identities. A managed root JAR is rejected even when renamed. A
required managed component is satisfied only by an existing valid component;
if unavailable, installation fails instead of adding a generic copy. Built-in
components never enter the generic ownership manifest. Existing reserved paths
`geyser-spigot.jar`, `floodgate-spigot.jar` and `viaversion.jar` (case-insensitive)
are excluded from generic identity-collision parsing after regular-file and
no-symlink safety checks. Complex metadata in those appliance JARs does not block
an unrelated install. All other existing/manual JARs still require safe identity
inspection. Reserved JARs are never overwritten, deleted or adopted, and staged
managed identities remain subject to the independent strict checks above.

After staging, the installer revalidates the reviewed plan and authoritative
context. It shares persistent Minecraft maintenance exclusion with reset/update
and acquires `/run/justvoxel-minecraft-maintenance.lock`, coordinating with CLI
maintenance. A busy lock leaves Minecraft and plugin files untouched.
The final player check runs under that lock immediately before backup. Unknown
player status fails closed. Review shows online player count/names and warns
about interruption; its final Install action confirms the existing player-aware
shutdown only when that review required confirmation. If players join after an
unconfirmed zero-player review, installation cancels safely for another review.

Before JAR placement the installer calls the existing verified cold backup,
`minecraft-backup --leave-stopped`, using its caller-held-lock contract. It does
not change backup format or retention. A zero helper exit status proves backup
creation and archive verification succeeded; non-zero blocks plugin placement.
Human-readable helper output is not a success contract. Minecraft must be stopped
before placement.
Each JAR is copied to a unique temporary file within the pinned, no-follow plugin
directory, fsynced, and atomically renamed without replacement. Every potential
placement has private durable transaction evidence under
`/var/lib/justvoxel/management/plugin-install/<operation-id>/`.

Minecraft starts for bounded RCON readiness and the existing
`verify-minecraft-stack` checks. Every newly installed plugin/dependency must
also appear as an exact, non-failed plugin-list entry in RCON `plugins` output.
File existence or publisher metadata alone is insufficient. If Minecraft was
running before maintenance, successful installation leaves it running. If it
was intentionally stopped, it starts temporarily for verification and returns
to stopped **before** ownership commit; completion reports the actual final state.

After runtime/plugin verification and restoration of the original service state,
the Agent atomically commits `<DataPath>/.justvoxel/plugins.json`. This explicit
`v1` manifest records Modrinth project/version, filename, SHA-512, parsed plugin
name/main class, requested/dependency status and relationships. The directory and
manifest cannot be symlinks; unsafe/malformed evidence blocks installation.
Writes are private, atomic and fsynced. The existing full cold backup naturally
includes the manifest with Minecraft data; backup code is unchanged.

Installed distinguishes **JustVoxel managed**, **Managed by Plugins**, and
**Manual / unmanaged**. Generic ownership is trusted only for a valid manifest
entry, exact filename and current matching JAR hash. Changed or unavailable files
show ownership attention instead of silently being adopted. Generic update and
removal are not available yet.

A failure after placement stops the failed runtime and removes only JARs created
by that transaction whose current SHA-512 still matches. Changed files are never
deleted. Normal rollback keeps the transaction cold backup; it does not restore
an archive automatically. It restores the original running/stopped state and,
when originally running, verifies the previous runtime with the existing stack
verifier. Proven recovery ends `rolled_back`; unproven recovery ends
`needs_attention`, with clear administrator guidance and preserved evidence.
An Agent restart/reboot during installation also becomes `needs_attention` with
plugin-specific interruption status. It never claims success or deletes unknown
transaction artifacts automatically.

Administrators can use **Re-check after repair** for a matching `needs_attention`
plugin operation. This strictly decoded acknowledgement re-checks private
transaction evidence, files, ownership and the original service state. It accepts
only all expected installed files with exact hashes and ownership, or all
transaction-created files absent with no manifest claim on those artifacts.
Running Minecraft must pass the existing stack verifier; an installed state must
also prove the expected active plugin identities through RCON. Originally stopped
Minecraft must remain stopped; re-check does not start it. Unproven states return
conflict and retain maintenance exclusion and evidence. Re-check never changes
plugin files, retries installation or restores backups. Proven states become
`resolved`, release the normal maintenance exclusion and record an audit event.

Private transaction staging is removed after persisted success, verified clean
rollback, or a safe untouched terminal failure. It is preserved for
`needs_attention`, including interrupted operations, until a successful re-check.
Cleanup failure adds a terminal status warning and audit event; it does not undo
verified plugin/runtime state or retain maintenance exclusion. Cold backups are
not touched.

Review, real persistent progress, completion and failure share one workspace
panel. Progress uses recorded stages/status, without invented timing. Reopening
the workspace discovers the current plugin installation. Success offers View in
Installed, Install another plugin and Done. Failure reports restored prior state
or administrator attention; it never displays fake success. Terminal results use
the existing audit event mechanism with `plugin_install` and the requested
Modrinth project as target; failed/rolled-back installs are not successes.

Administrator-only WebUI routes are
`POST /api/plugins/workspace/install/plan`,
`POST /api/plugins/workspace/install`,
`POST /api/plugins/workspace/install/acknowledge`, and
`GET /api/plugins/workspace/install-operation` (optionally `?id=<operation-id>`).
POSTs reuse existing CSRF/session protection. The existing Unix-socket client
mirrors `POST /v1/admin/plugins/install/plan`,
`POST /v1/admin/plugins/install`,
`POST /v1/admin/plugins/install/acknowledge` (JSON `operation_id` only), and
`GET /v1/admin/plugins/install-operation`; polling by ID reuses generic AdminOperation.
Only the privileged Management Agent accesses Modrinth, artifacts or the filesystem.

Stage 2 adds no plugin update/removal, user batching, other marketplaces, Version
Workspace integration, or multi-instance support. It makes **no SELinux policy,
label, mount-label or Quadlet changes**. If the existing environment cannot consume
new JARs, verification fails and rollback runs; labeling changes require a separate
human-approved task.
