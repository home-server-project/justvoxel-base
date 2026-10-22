# Plugin Workspace

## Cycle 1 — read-only discovery and inventory

Administrators can open **Control Center → Minecraft → Plugins**. The movable,
resizable window has Discover, Installed and Add-ons tabs. The original Cycle 1 capability is read only:
it cannot install, download, update, remove or hot load plugins, change server
configuration, or interrupt Minecraft.

Discover has one compact desktop toolbar: a growing search field
with independent Modrinth and Hangar source switches. Modrinth defaults ON and
Hangar defaults OFF. Either source can be used alone, or both can populate the
same grid. With both OFF, Discover makes no upstream catalog request and shows
“Enable Modrinth or Hangar to browse plugins.” Source changes reset pagination.
The toolbar may wrap in a genuinely narrow workspace.

Modrinth and Hangar share the information/install overlay. Hangar retains native
Channel terminology; Modrinth uses Release type. Install appears only when the
Agent proves compatibility and complete direct download metadata for the selected
release. Source identities stay explicit: similarly named projects from different
catalogs are separate records. All catalog and icon reads pass through the
same-origin WebUI, the Management Agent Unix API and fixed HTTPS sources; the
browser never contacts an upstream catalog or image server directly. Icons use
initials when metadata, validation or retrieval fails. Passive information footer Close actions
use the normal green button styling shared by Previous and Next.

Discover marks provider-aware `(Source, ProjectID)` ownership from the existing
Plugins v1 manifest as **Installed**; source-less v1 entries mean Modrinth.
Verified locations show Enabled or Disabled; uncertain local files show Needs
attention while remaining Installed. These selections open read-only information
with **Managed by Plugins** and **Updates are not available yet**. Install and
install-plan are suppressed even if the catalog release differs. Manual JARs
are not matched by name, and JustVoxel-managed projects retain their special treatment.

Pagination displays **Page X of Y** with **Previous** and **Next** navigation
using authoritative composite Modrinth/Hangar cursors. Next uses the returned
next cursor; Previous uses an already-known cursor and freshly fetches that page.
Only cursors are kept temporarily in memory; catalog payloads and cards are never
cached. Refresh, search changes/submission, source/filter changes, authoritative
server-context changes, capacity changes and workspace close/reopen clear cursor
history and return to page 1.

Plugin identity/status badges use restrained semantic colors in dark and light
mode: blue for version, violet for server software, teal for Minecraft version,
green for Installed/Enabled, amber for Disabled, and red/orange for Needs attention.
Every authoritative server software label uses the same server badge treatment.
Plugins-managed installed projects show a non-clickable green **Installed** status
in the information overlay's top-right action position, with no Install action.
Manual/unmanaged plugins and JustVoxel-managed components retain their presentation.

Hangar's exact validated `https://hangarcdn.papermc.io/avatars/default/default.webp?v=0`
uses the JustVoxel initials tile. The Agent rechecks authoritative metadata and
returns no custom icon without contacting the CDN. Real project and user fallback
WebP avatars remain strictly validated and proxied.

Plugin results use compact cards of uniform height, with a fixed-size logo,
clamped name and description, release/platform/Minecraft badges and a short
status or dependency summary. The entire card opens its overlay by click, Enter
or Space. Keyboard focus is visible; selection uses the green Server Software
card language and stays visible while the overlay is open. There are no separate
Info or Install card buttons. Closing the overlay returns focus to the card;
details never expand inside the results grid. Installed uses the same
whole-card selection and information/status overlays. Managed lifecycle actions
are described in the current cycle below; updates remain unavailable. Installable Discover cards open one combined information/install
overlay; JustVoxel-managed and unavailable selections remain read only.
Supported versions appear as compact slash-separated text that wraps when
needed. Supported platforms use the same compact presentation. Publisher
dependencies are grouped into compact rows, including cross-loader required
metadata that will not be installed. Actual install dependencies remain explicit.
Installed information has separate sections for JAR,
Metadata, Management and Update status. Installed cards show ownership
and a compact status; updates remain unavailable.
Discover uses three columns in wide windows, two in medium windows and one in
narrow windows. Buttons stay distinct from badges. Authoritative provider/project
identities use real publisher logos through the Administrator-only same-origin
`GET /api/plugins/workspace/icon?source=<Modrinth|Hangar>&project_id=<id>` route and the Agent's
`GET /v1/admin/plugins/icon?source=<Modrinth|Hangar>&project_id=<id>` route. Modrinth Discover first passes a validated
search `icon_url` with `project_id` to the
same-origin proxy. If that image fails, it retries exactly once using only
`project_id`, allowing the Agent to look up fresh project metadata. Missing search
icons begin with that metadata lookup. Installed uses project ID only. Initials
stay visible until a logo loads and are the final fallback if metadata or image
retrieval fails. The browser never uses a CDN URL as `img.src`.
All documented Modrinth extensions are accepted: `png`, `jpg`, `jpeg`, `bmp`,
`gif`, `webp`, `svg`, `svgz` and `rgb` (case-sensitive). Project-bound URL
validation remains strict: HTTPS on exactly `cdn.modrinth.com`, the matching
`/data/<project_id>/<filename>` path, a bounded URL length, and no credentials,
ports, queries, fragments, encoded alternate paths or traversal. Filename
extensions remain a path safety rule; neither they nor CDN response headers prove
the delivered raster representation. Actual PNG/JPEG/BMP/GIF/WebP byte signatures
are authoritative, and the Agent derives canonical browser MIME from those bytes
without transcoding. Upstream Content-Type is advisory, including when missing or
malformed, and is never forwarded. The Agent requests no specific CDN image format.
SVG/SVGZ and SGI RGB use bounded PNG conversion only when the CDN delivers source
representations rather than an already verified supported raster. Conversion uses
fixed image-composition tooling, private bounded buffers and a short timeout.
SVGZ decompression is bounded; RGB headers and dimensions are checked before
conversion. Browsers never receive raw SVG, SVGZ or RGB. Source and final image
bytes are limited to 512 KiB, with no redirects or persistent image cache. This
improves compatibility with Modrinth CDN transforms while preserving the
same-origin proxy and security boundary.
Hangar icon requests send `source=Hangar` and the numeric project ID only. The
Agent re-fetches official project metadata and takes `avatarUrl` from that exact
project. Only HTTPS `hangarcdn.papermc.io` WebP avatar paths are approved: project avatars
(`/avatars/project/<positive-bounded-decimal-id>.webp`), owner/user fallback avatars
(`/avatars/user/<canonical-UUID>.webp`), or the Hangar default avatar
(`/avatars/default/default.webp`). Each requires exactly one bounded decimal `v`
query value; the default requires exactly `v=0`. No caller URL is
accepted, redirects are rejected, and the same 512 KiB byte/signature bounds apply.
Project and user fallback avatars remain Agent-proxied fixed-CDN images. The
exact default avatar returns HTTP 404 without a CDN fetch and uses initials;
metadata/image validation or retrieval failures also use initials. Hangar SVG/source
conversions are not supported. Modrinth icon behavior is unchanged.
Information and installation reuse the card identity tile.
On hover-capable pointers, the existing green card frame/background follows the
pointer. Cards begin unselected; selection can remain while their overlay is
open. Closing clears every selected card and resets `aria-pressed` to false,
so the green selection does not remain stale. Keyboard focus-visible styling
remains available.
Progress and terminal results retain the selected identity in browser memory,
bound to the returned operation ID; after reload they use fallback. Inventory
exposes the existing manifest project/release IDs only after exact
filename, SHA-512 and descriptor ownership verification. Installed
reuses the same-origin logo helper, preferring the verified `project_id`, then the
display-only `IconProjectID` (`icon_project_id` on the wire), then initials.
JustVoxel-managed components receive explicit official Modrinth display identities
from the existing exact trusted filename/main-class classification: Geyser
`wKkoqHrH`, Floodgate `bWrNNfkb`, and ViaVersion `P1OZGk5p`. Floodgate's identity
is visual only and does not change its source or management. `IconProjectID`
does not alter ownership, adoption, update ownership, install eligibility,
manifest verification or artifact verification. Manual/unmanaged plugins receive
no display identity and are never searched by name or filename, or guessed, to
obtain a logo. Unidentified JARs retain initials without ownership changes.
The combined overlay keeps one compact column, with metadata pairs sharing
lines where they fit and each install file showing its filename and size in one
row. Successful review places Install at the top-right of the identity header,
on the same horizontal row as the logo and plugin name. The action keeps a fixed
flex size while the identity uses the remaining width; description and version
badges stay below. There is no separate Install lane or Cancel/Install footer.
Info and terminal Done/Close actions align to the right. The compact layout is designed to fit the normal FHD workspace
without an internal scrollbar. Overflow remains auto as a safety fallback so
content is never clipped. Normal-FHD visual acceptance remains a human real-VM
check.

Passive Info, read-only rejected information, pre-install Review and review
preparation close by clicking directly on the dark backdrop or pressing Esc.
Clicks inside the content never dismiss the overlay. Submitting an installation
and operation rendering, including backup, install, restart, verification,
rollback and needs-attention, are not backdrop-dismissible. Existing explicit
Close/Done controls and Esc behavior remain, with keyboard focus restored.
Plugins tabs use the same shared workspace geometry as System, Minecraft,
Migration and Version: minimum height 34px, padding `.38rem .7rem`, radius 8px
and font size `.86rem`. Header Refresh/Close and normal actions retain their
shared workspace button styling. Source/filter switches remain switches;
whole-card article selections retain their card geometry.

The selected `pluginContext()` / `pluginServerContext` is authoritative for both
providers: ServerType, ServerSoftware and MinecraftVersion. Today Paper and Purpur
are supported; the workspace displays that same context, including Purpur identity.
Neither provider parses visible header text. Modrinth maps Paper to
paper/spigot/bukkit and Purpur to purpur/paper/spigot/bukkit. Hangar maps both to
PAPER only at its provider boundary, because Hangar exposes PAPER for these
compatible servers and no distinct PURPUR platform. Future server types require an
explicit provider mapping and otherwise fail closed. The future instance/server
selector will supply this same authoritative context. Vanilla is unsupported.
The workspace reuses appliance configuration discovery and runtime status for
server software, Minecraft version and the configured data location.

Discover qualifies provider hits in order, scanning at most eight times the
requested capacity per provider (up to 96 projects each). Partial or empty pages
can retain a Next cursor. Combined pages alternate sources, preserving each
provider's order and carrying the next turn across pages, including capacity 1.
Unconsumed hits are revisited, never skipped to the end of a fetched batch.
`total` is the sum of enabled providers' raw totals (`total_hits` for Modrinth,
`pagination.count` for Hangar), not a count of eligible projects. The UI shows
**Page X of Y**, **Previous**, **Next**, with Y calculated from that raw catalog
total and the current page capacity rather than an eligible-result count.

The bounded Discover query accepts `q` (up to 200 bytes), `compatible_only`,
`release_only`, `modrinth`, `hangar`, `hangar_first` (literal `true`/`false`),
`modrinth_offset` and `hangar_offset` (0–10000), and `limit` (1–24, default 8).
Omitted filters default ON; omitted sources default Modrinth ON/Hangar OFF;
omitted offsets and turn default zero/Modrinth first. Unknown/repeated fields,
legacy scalar `offset` or `source`, and malformed values are rejected.
Responses contain enabled-source booleans, `cursor` and nullable `next_cursor`,
each cursor carrying both offsets and `hangar_first`, plus projects and raw total.
Previous/Next use the actual in-memory composite history. Search/filter/source
changes, Refresh, authoritative server-context changes and workspace close/reopen
reset it. Capacity changes also discard history and restart both provider cursors
at page 1.

Live search retains its 250 ms debounce, stale-request cancellation and immediate
Enter submission. Compatible only and Release only start enabled and apply to
each enabled provider. Modrinth discovery uses the fixed HTTPS API
`https://api.modrinth.com/v2` and the search facet `all_project_types:plugin`.
Compatible only adds one OR group of the selected JustVoxel server's compatible
loader facets and the exact `versions:<MinecraftVersion>` facet from the
authoritative context above. With Compatible only off, the loader OR group covers
paper/purpur/spigot/bukkit and no active-version facet is sent. Release only selects
`version_type == release` from project versions; it is a version-level filter,
so the provider Total remains the raw search candidate count, not an exact
post-release-filter count. Installability is intentionally narrower than
discoverability: a compatible release, supported server, valid direct artifact,
non-JustVoxel-managed project, and all installer safety checks are required.
Hangar reads
`GET https://hangar.papermc.io/api/v1/projects` with bounded `q`, `offset`, `limit`
and `platform=PAPER`. Compatible-only project search also sends
`version=<active-version>` when the authoritative server version is known; it
omits `version` when Compatible only is off. Exact release inspection remains
authoritative afterward: `/projects/<numeric-id>/versions` uses `platform=PAPER`
and, when compatible-only, `platformVersion=<active-version>`. Discovery attempts
to fill the workspace page while inspecting at most `8 × limit` upstream projects
per request, stopping when full, exhausted, or at the absolute cursor bound.
Rejected projects advance the cursor; eligible unconsumed projects remain for the
next page. Budget exhaustion may return a short page. It inspects up to
75 versions per project in bounded pages. Exact `platformDependencies.PAPER`
membership determines publisher compatibility; other platforms never qualify.
Release only requires an explicit channel flags list without `UNSTABLE` or unknown
flags. Native channel names are displayed; no release/beta/alpha equivalence is
claimed. Missing stability evidence fails the release filter. Direct download file metadata controls install availability. Required internal
Hangar project dependencies enter its narrow planner; optional dependencies are
displayed only. External downloads and external-only required dependencies are not
followed. Numeric project/release IDs remain separate from Modrinth identities. Hangar uses the existing no-redirect,
bounded-timeout client with a 2 MiB JSON body cap and needs no API key.

Compatibility means **Compatible according to publisher metadata**, never verified
third-party runtime compatibility. With Compatible only OFF, other Paper versions
can be browsed without claiming compatibility. Unknown active Minecraft versions
require that filter OFF. Upstream failures are reported as unavailable, not as
proven incompatibility. Discover measures grid columns and available height for
1–12 cards per page; a capacity change resets both provider cursors and history
to page 1. Installed has no catalog pagination.

Installed reads regular JARs under the configured data directory's `plugins`
folder. It refuses a symlinked plugin folder and skips symlinked JARs and unrelated
files. Metadata reads inspect simple, bounded top-level name/version scalars in
`paper-plugin.yml` and `plugin.yml`; unreadable, malformed, unusually large or
complex metadata may leave the version or name unknown. This does not prevent
other JARs from appearing. No ownership database is created.

Geyser, Floodgate and ViaVersion are displayed as JustVoxel managed. Their
existing server update mechanism remains authoritative. All other JARs are
Manual / unmanaged. Installed shows their unknown update status. Future
**Update available** status belongs on Installed cards; no update action exists
yet. Plugin names are never used to guess a Modrinth project identity.

The visible workspace calls Administrator-only `/api/plugins/workspace/{discover,installed}`
WebUI routes. The existing `updates` routes/contracts remain for compatibility;
there is no visible Updates view. The WebUI calls `/v1/admin/plugins/{discover,installed,updates}` over
the existing Management API Unix socket. The Management Agent alone reads local
JARs and contacts Modrinth's fixed HTTPS API. Requests have bounded timeouts and
responses, send a JustVoxel User-Agent, require no API key and reject redirects.
There are no mutation endpoints in this cycle.

## Stage 2 — third-party installation

Managed installation permits compatible third-party Modrinth and Hangar releases
on Paper and Purpur when the current Minecraft version is known and publisher
file metadata is complete. Clicking an installable Discover card fetches the
authoritative plan and opens a single-column combined information/install
overlay. Turning off Compatible only or Release only still permits browsing;
incompatible and JustVoxel-managed selections have read-only
information overlays with no generic Install action. Modrinth beta/alpha releases
remain read only; Hangar keeps native channels and requires exact compatible
direct artifact metadata for installation.

The combined overlay shows About this version, Compatibility, Dependencies,
Files to install, Installation and Restart as normal restrained information
sections. Required dependencies show their exact planned name/version and
**Already satisfied** or **Will be installed** status. Optional/incompatible
publisher entries remain informational without duplicating required entries.
Restart explains the verified cold backup, Minecraft start/restart for plugin
verification and return to an original stopped state; it does not promise
continuous uptime. Players appears only when the plan requires player-aware
confirmation, with online count and available names. Install appears at the
top-right of the identity header; read-only information has no Install button.
This source implementation still requires human real-VM visual acceptance.

Installation uses an authoritative Management Agent plan. The browser supplies
an explicit canonical source (`Modrinth` or `Hangar`) and exact project/version IDs; it cannot choose download URLs, filenames,
hashes, paths or dependency lists. Review shows the selected release, publisher
compatibility, active server/Minecraft version, required dependencies, exact
files and sizes, and the configured `<DataPath>/plugins` location. Publisher
compatibility is a prerequisite, not a promise that the plugin will run.

Only **required** dependencies are resolved recursively and automatically
installed. Exact dependency versions must belong to the declared project. Required
Modrinth metadata is scoped to the active Paper-family loaders: exact versions
with no loader overlap, and project-only dependencies whose authoritative project
loaders have no overlap, remain publisher information and are not installed.
For example, a multi-loader Paper plugin does not cause Fabric API to be installed
on Paper. Required dependencies with target-loader support still need a compatible
release for the active Minecraft version; missing releases fail closed with the
dependency and compatibility problem identified. Project-only dependencies select
the newest compatible release. Optional, incompatible and embedded entries remain
display-only Info metadata and are not automatically downloaded. Cycles, conflicting versions,
filename-only requirements and unresolved identities block installation. Bounds
are six dependency levels, sixteen artifacts, 128 MiB per JAR and 256 MiB combined.

A deterministic SHA-256 fingerprint binds server type, Minecraft version, data
path, provider, selected files, authoritative provider hashes/download identities, plus
dependency relationships. Apply re-resolves the plan and requires that exact
fingerprint. Player snapshots are excluded from the fingerprint and checked
separately. Artifact URLs and private transaction evidence are never returned to
the browser. Modrinth's catalog API remains fixed HTTPS; Hangar's catalog API is
fixed at `hangar.papermc.io` through the Management Agent. Modrinth icons remain on `cdn.modrinth.com`; Hangar icons use the approved project and user fallback
avatar sources; the exact generic default uses initials as described above. Redirects are rejected, and response sizes and
timeouts are bounded.

Hangar installation re-fetches exact project and version metadata under
`https://hangar.papermc.io/api/v1`, verifies bounded positive decimal numeric IDs,
and requires the selected Minecraft version in the mapped platform's
`platformDependencies`. Exact releases are re-fetched by numeric ID; dependencies use bounded compatible
release metadata and then exact numeric release lookups. Direct
`downloads.PAPER.fileInfo` supplies the safe filename, exact size and SHA-256.
The Agent constructs the fixed HTTPS `hangarcdn.papermc.io/plugins/` endpoint from
the trusted project namespace, release name, mapped platform and filename, and
requires `downloadUrl` to match exactly. The API download route redirects to this
CDN, so the Agent requests the validated CDN endpoint directly. ExternalUrl-only
releases are rejected. Redirects are never followed. Download
before downtime verifies the exact size and upstream SHA-256 while calculating
SHA-512 locally. That local SHA-512 is the ownership/runtime evidence. Staged JAR
inspection, player checks, durable evidence, verified cold backup, atomic placement,
Minecraft start, stack/startup verification, original stopped-state restoration and
rollback are the existing shared installer flow.

Required Hangar dependencies with an internal numeric project ID resolve through
Hangar using the same selected server type and Minecraft version. Required
external-only dependencies block review with an explanation; optional dependencies
are displayed without automatic installation. Provider identity participates in
parents and duplicate/reuse checks. Cycle, depth, artifact-count, combined-download
and filename-collision limits are preserved.

Before returning an install review, the Agent downloads only the exact selected
root artifact into private temporary staging, using the existing bounded CDN,
size, SHA-512 and timeout checks, then applies the existing strict Paper/Bukkit
descriptor inspection. Temporary review material is removed immediately; there is
no persistent artifact cache or catalog-wide download. A zero-byte descriptor
provides no usable plugin identity. A rejected direct-plugin review opens the
existing read-only project Info with the rejection explanation and no Install
action. Review starts no operation, backup or Minecraft mutation. Publisher loader
tags alone do not authorize an Install action. Modrinth platform metadata listing
Paper support is not proof that the artifact is a direct Paper plugin. Direct
artifact inspection remains authoritative for plugins-directory installs.
When no valid direct descriptor exists, exact known JAR entries
`fabric.mod.json`, `quilt.mod.json`, `META-INF/mods.toml`,
`META-INF/neoforge.mods.toml` and `META-INF/sponge_plugins.json` identify a
loader-based server mod. Their presence only improves classification and the
read-only rejection explanation; these files are never parsed or executed.
A valid direct Paper/Bukkit descriptor remains authoritative, and normal strict
descriptor validation still applies. Unknown JARs retain the generic rejection.
Loader-based server mods remain outside current Plugin Install scope and require
a separate loader/runtime installation path. No
Ignite or other mod-loader support or mods directory is added, and releases such
as NoDim or Slumber are not made installable by publisher metadata. The Agent and
WebUI use the same rejection:
“Loader-based server mod - not supported by the current version of JustVoxel Plugin installer. Should be available in future updates.”
Install-time inspection remains independent and authoritative.

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
directory, hash checked and fsynced. Its already-open destination descriptor is
explicitly set to mode `0644` before closing and atomic rename without replacement;
a mode-setting error fails placement. This makes the published JAR readable to
Minecraft despite the Agent's intentional service-wide `UMask=0007`, which remains
unchanged. Ownership, parent permissions and SELinux labeling remain unchanged.
The plugin directory is fsynced after publication. Every potential
placement has private durable transaction evidence under
`/var/lib/justvoxel/management/plugin-install/<operation-id>/`.

Minecraft starts for bounded RCON `rcon-cli list` readiness and the existing
`verify-minecraft-stack` checks. `rcon-cli plugins` is not used for plugin identity.
Every newly installed plugin/dependency must appear by exact descriptor name
in the current Paper startup log, `<DataPath>/logs/latest.log`: both a Paper or
Bukkit PluginInitializerManager inventory entry and `[PluginName] Enabling ...`
evidence are required. The logger identity is authoritative even if the enable
message spells the name differently. Only evidence after the last
`[PluginInitializerManager] Initializing plugins...` marker counts. Reads are
bounded and confined to a regular file without symlinks; malformed, incomplete,
duplicate or detected load/enable failure evidence fails closed and runs the
existing rollback.
File existence or publisher metadata alone is insufficient. If Minecraft was
running before maintenance, successful installation leaves it running. If it
was intentionally stopped, it starts temporarily for verification and returns
to stopped **before** ownership commit; completion reports the actual final state.

After runtime/plugin verification and restoration of the original service state,
the Agent atomically commits `<DataPath>/.justvoxel/plugins.json`. This explicit
`v1` manifest records provider project/version, filename, SHA-512, parsed plugin
name/main class, requested/dependency status and relationships. The directory and
manifest cannot be symlinks; unsafe/malformed evidence blocks installation.
Writes are private, atomic and fsynced. The existing full cold backup naturally
includes the manifest with Minecraft data; backup code is unchanged.

Ownership is `(Source, ProjectID)`, not project ID alone. The optional `source`
field uses canonical `Modrinth` or `Hangar`. Existing v1 entries without source
mean Modrinth in memory and are not rewritten or migrated by reads or inventory.
New entries explicitly store their source. Hangar project/release IDs are bounded
positive decimal identities; Modrinth keeps its existing ID rules. Legacy bare
`required_by` parents mean Modrinth, and Hangar parents use `Hangar:<project-id>`.
Install reuse, lifecycle actions/recovery, manifest commits, transaction evidence,
and preserved-data Reset Minecraft cleanup use these provider-aware identities.

Installed distinguishes **JustVoxel managed**, **Managed by Plugins**, and
**Manual / unmanaged**. Generic ownership is trusted only for a valid manifest
entry, exact filename and current matching JAR hash. Changed or unavailable files
show ownership attention instead of silently being adopted. Generic update and
removal are not available yet.

A transaction JAR can temporarily exist before runtime verification and ownership
manifest commit. Installed checks the current installation operation
and intentionally hide inventory during unverified or unresolved operations,
including `needs_attention`, clearing stale cards and showing a verification/recovery
message. This prevents transaction-created artifacts from being misrepresented as
manual/unmanaged. Refresh and tabs remain usable, and Discover remains available.
No current operation, or `succeeded`, `rolled_back` and `resolved` states, expose
stable inventory again. A lightweight re-check runs only while Installed
is blocked, and automatically loads that requested inventory once the operation
is settled. Switching to Discover or closing Plugins clears this timer.
Rollback semantics are unchanged.

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

Administrators can use **Re-check recovery** for a matching `needs_attention`
plugin operation. This strictly decoded acknowledgement re-checks private
transaction evidence, files, ownership and the original service state. It accepts
only all expected installed files with exact hashes and ownership, or all
transaction-created files absent with no manifest claim on those artifacts.
Running Minecraft must pass the existing stack verifier; an installed state must
also prove the expected active plugin identities from the current startup log
using the same inventory-plus-enable checks. Originally stopped
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
the workspace automatically resumes only a non-settled installation, including
`needs_attention`; prior `succeeded`, `rolled_back` or `resolved` operations do
not reopen automatically. Review and final Install both check for an existing
non-settled operation and resume it instead of starting another installation.
Progress updates retain the same operation identity header and logo.
Success offers View in
Installed as a secondary/dark action and Done as the primary/green action.
View in Installed closes the overlay and selects Installed; Done closes it and
refreshes the current workspace state. Failure reports restored prior state
or administrator attention; it never displays fake success. Terminal results use
the existing audit event mechanism with `plugin_install` and the requested
selected project ID as target; failed/rolled-back installs are not successes.

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
Only the privileged Management Agent accesses provider APIs, artifacts or the filesystem.

Stage 2 adds no plugin update/removal, user batching, other marketplaces, Version
Workspace integration, or multi-instance support. It makes **no SELinux policy,
label, mount-label or Quadlet changes**. If the existing environment cannot consume
new JARs, verification fails and rollback runs; labeling changes require a separate
human-approved task.


## Managed Enable, Disable and Remove

Only entries whose ownership is verified as **Managed by Plugins** receive
lifecycle actions. Enabled entries show **Enabled** and one yellow **Disable**
action; disabled entries show **Disabled** and one yellow **Enable** action.
Both offer red **Remove**. Disabled is a normal state. Manual/unmanaged plugins
and JustVoxel-managed Geyser, Floodgate and ViaVersion stay read only.

The existing `<DataPath>/.justvoxel/plugins.json` **v1 schema remains backward compatible**.
Enabled/disabled state is never persisted in that JSON: an enabled JAR is in
`<DataPath>/plugins/<filename>`, and a disabled JAR is in the root-private,
mode-0700 `<DataPath>/.justvoxel/disabled-plugins/<filename>` directory. Both
locations use pinned directory access, no symlink following, the same filename
rules and exact manifest SHA-512/name/main-class verification. A duplicate,
missing, changed or unsafe JAR becomes Attention and cannot receive lifecycle
mutations. Arbitrary files in the disabled store are never adopted.

Enable returns the same verified stored JAR to `/plugins`; Disable moves that
exact JAR out of the active set. Neither changes the ownership manifest. Remove
stages the exact JAR privately on the data filesystem and commits only removal
of that project's manifest entry after runtime verification. The staged rollback
copy survives until the success journal is durable. **All plugin configuration
and data directories remain untouched**, and dependencies are never removed
automatically. An enabled managed parent recorded in `required_by` blocks Disable
and Remove of its dependency. A verified disabled parent does not block; unsafe
or ambiguous parent state fails closed. Orphan dependencies remain installed.

There is no hot plugin load/unload. Actions share Minecraft maintenance exclusion
with install, reset and updates, re-resolve the exact reviewed SHA-256 plan,
recheck players under the runtime maintenance lock, persist private synced
transaction evidence before downtime, and use the existing verified cold backup
with `--leave-stopped`. Minecraft starts for normal stack verification and the
existing active-plugin startup proof for Enable. Disable/Remove require absence
from the final initialization inventory. This also temporarily starts an
originally stopped server for verification and returns it to stopped afterward.
Failures stop the failed runtime, restore the exact original JAR location and
ownership, and verify the original runtime. Unproven recovery retains
`needs_attention`, evidence and maintenance exclusion after restart.

**Re-check recovery** for lifecycle operations uses durable evidence, never
browser memory. It verifies a complete target state or performs a bounded exact
rollback and verifies the original state. It cannot resolve ambiguous copies,
changed hashes/descriptors or unexplained manifest changes. No terminal cleanup
is part of the WebUI recovery workflow.

The administrator-only Agent endpoints are `POST /v1/admin/plugins/manage/plan`,
`POST /v1/admin/plugins/manage`, `POST /v1/admin/plugins/manage/acknowledge`, and
`GET /v1/admin/plugins/operation`. Requests are bounded and reject unknown fields;
only `enable`, `disable` and `remove` are supported. Matching CSRF-protected WebUI
routes live under `/api/plugins/workspace/`. The workspace uses the generic
current Plugin operation contract for install and all lifecycle actions; prior
install endpoints remain available for compatibility. Installed inventory stays
blocked until a mutation has a verified terminal state. Lifecycle progress uses
Prepare, Backup, Change, Restart and Verify from the persisted operation journal.
Administrator audit events use `plugin_enable`, `plugin_disable` and
`plugin_remove`, the project ID, and the actual success/failure outcome.

### Exact BlueMap addon classification

Only Modrinth project `99IreA6y` **and** descriptor main class
`de.bluecolored.bluemap.entities.PaperEntrypoint` together receive the dedicated
root-artifact review rejection:

> BlueMap native add-on belongs in Plugins -> Add-ons -> BlueMap Add-ons. Select its local file there.

This add-on is not installed into `/plugins`. Administrators select its local JAR
in Add-ons → BlueMap Add-ons; no automatic provider download or handoff is performed. Either identity alone continues through normal strict
validation. Names, descriptions and loader tags do not trigger this special
case. PlayerDataSync has no special rejection, and Purpur compatibility and
active-plugin verification remain unchanged.

### Plugins diagnostics

**System → Logs → Plugins** lists, reads and downloads the private Agent log for
`plugin_install`, `plugin_enable`, `plugin_disable`, `plugin_remove` and
`plugin_addon_install`.
`plugins-logs` uses the same private protections as other operation logs.
Operation journals provide real stage/status history. Failed final startup
verification adds only a small sanitized excerpt from the bounded final
PluginInitializerManager initialization segment, limited to the expected plugin
identities. Standard load/enable/disable/failure messages and safely identified
plugin logger explanations can explain storage/configuration failures or a
plugin disabling itself. Evidence is capped at 64 lines and 16 KiB, including
its diagnostic framing; control corruption is rejected, sensitive lines are
redacted, and URLs, connection strings and stack frames are excluded. Host
identity uses existing diagnostic sanitation. The full server log is never
exposed and raw Minecraft logs remain untouched. Read-only catalog compatibility
rejections do not create persistent operations or logs.

### Reset Minecraft cleanup

The authoritative reset plan includes fingerprinted `plugins_action`:
`delete_with_internal_data` keeps existing whole internal-data deletion;
`remove_managed_jars` removes exact Plugins-managed JAR ownership while external,
network or unknown data is preserved. Preserved-data resets verify every manifest
entry in exactly one active/disabled location, persist private cleanup evidence,
and atomically stage those JARs before the destructive helper. Unsafe evidence
blocks reset. Helper failure restores and verifies the staged JARs. Successful
completion removes the manifest and empty disabled ownership store before
discarding staging. Retry recognizes existing staged files and durable helper
completion rather than duplicating or losing JARs. Manual plugins and plugin
configuration/data folders remain when the data path is preserved. Internal
reset verifies that manifest/disabled ownership state did not survive deletion.
Full Factory Reset policy is unchanged.


## Managed Add-ons — local content uploads

**Plugins → Add-ons** supports exactly five hosted content kinds. All destinations
are relative to the configured Minecraft data root and are chosen by the Agent:

| Kind | File | Authoritative destination |
| --- | --- | --- |
| PlaceholderAPI expansion | `.jar` | `plugins/PlaceholderAPI/expansions/` |
| Geyser extension | `.jar` | `plugins/Geyser-Spigot/extensions/` |
| BlueMap native add-on | `.jar` | `plugins/BlueMap/packs/` |
| Skript script | `.sk` | `plugins/Skript/scripts/` |
| WorldEdit schematic | `.schem`, `.schematic` | `plugins/WorldEdit/<saveDir>/` |

An administrator chooses a **local file** and selects **Review / Install** in
that host's section. There is no Add-ons catalog, arbitrary URL download or
editable destination. The host must be identified by the existing plugin
inventory/metadata and have a safe real data directory. Geyser uses the existing
JustVoxel Geyser-Spigot classification. A directory name alone does not establish
an installed host. Disabled Plugins-managed hosts are unavailable until enabled.
Installed host metadata does not claim the host is currently runtime-active.

Review and Install are separate bounded multipart uploads; both send the selected
file. The browser keeps it only in memory. The unprivileged WebUI checks the
Administrator role, CSRF cookie/origin and fixed field/type contract, then streams
to the Agent over the Unix socket without a staging copy. The Agent creates a
private temporary file, validates and hashes it, and deletes review staging before
returning. Apply creates a fresh upload, repeats all validation and environment
checks, and compares the fingerprint. Filename, SHA-512, size, authoritative
relative target, host identity/hash/status, safe parent identity, WorldEdit saveDir,
server context and current ownership evidence are bound to the plan. Player
confirmation uses the existing live player checks rather than freezing player
counts in the fingerprint. Uploads are limited to 128 MiB plus bounded multipart
framing; scripts are limited to 4 MiB and descriptors to 32 KiB.

JARs require a bounded structurally valid ZIP with readable entries. PlaceholderAPI
expansions require normal class content and reject root plugin or other hosted
runtime descriptors. This is a **format check**, not static proof of a
PlaceholderExpansion implementation; PlaceholderAPI performs runtime loading.
Geyser requires root `extension.yml` with plausible non-empty `id`, `name`, `main`,
`api` and `version`. BlueMap requires root `bluemap.addon.json` with non-empty `id`
and `entrypoint`. Skript scripts must be non-empty UTF-8 without NUL bytes; no
Skript parser is implemented. Schematics must have a supported extension and be
bounded and non-empty; validation does not claim full clipboard format parsing.
Host software performs its own parsing/loading.

Ownership is separate from Plugins JAR ownership:
`<DataPath>/.justvoxel/plugin-addons.json`, schema **v1**, records only kind,
filename, target relative path, SHA-512 and available display/descriptor identity.
It never stores an absolute data path. Inventory verifies each exact owned file
and hash. Missing or changed owned content shows **Needs attention** and cannot
be silently reinstalled. Manual files are never adopted. Existing targets fail
closed, including already-owned files; overwrite, updates and removal are not
available in this version. Cards show filename/type, Installed, Managed by Add-ons,
the destination and **Updates are not available yet**.

Executable expansions, extensions, native add-ons and scripts share the existing
maintenance exclusion and runtime lock. They recheck players and confirmation,
persist synced transaction evidence, take the existing verified cold backup with
`--leave-stopped`, atomically publish without replacement using Minecraft data
ownership/mode, restart Minecraft and verify health. Originally stopped Minecraft
is temporarily started for verification and returned to stopped. Success proves
the exact file was installed and Minecraft returned healthy; it does not claim
that the hosted add-on itself loaded. Failures remove only the exact new
transaction-owned inode/hash and restore the original service state. Durable
journal/log evidence remains available, including across Agent restart.
**Retry recovery** uses that evidence; uncertain state retains maintenance
exclusion and never deletes unknown content.

WorldEdit schematics share the operation gate and atomic non-overwriting placement,
but require **no restart, no player confirmation and no cold backup** solely for
this additive passive content. Final placement verifies the exact hash; rollback
removes only the newly created exact file and does not change Minecraft's state.

WorldEdit reads only the root `saveDir` scalar from
`plugins/WorldEdit/config.yml`, defaulting to `schematics` when absent. A safe
custom relative value such as `custom/schematics` is resolved beneath
`plugins/WorldEdit`; simple quoted values are accepted. Explicit empty values,
absolute paths, traversal, control garbage, duplicate settings and unsafe/symlinked
parents are rejected. The browser never supplies this setting or a target path.

Administrator-only Agent routes are `GET /v1/admin/plugins/addons`,
`POST /v1/admin/plugins/addons/plan`, `POST /v1/admin/plugins/addons/install` and
`POST /v1/admin/plugins/addons/recover`. Corresponding WebUI routes live under
`/api/plugins/workspace/addons`. Uploads use `X-CSRF-Token` with the established
cookie/origin policy before streaming. Polling reuses the existing Plugins operation
surface with operation type `plugin_addon_install`. There are no changes to the
existing Plugins ownership schema, runtime targets or SELinux policy.
