# Plugin Workspace (Cycle 1)

Administrators can open **Control Center → Minecraft → Plugins**. The movable,
resizable window has Discover, Installed and Updates tabs. Cycle 1 is read only:
it cannot install, download, update, remove or hot load plugins, change server
configuration, or interrupt Minecraft.

Paper and Purpur support plugins. Vanilla displays an unsupported state.
The workspace reuses appliance configuration discovery and runtime status for
server software, Minecraft version and the configured data location.

Discover searches Modrinth server plugins. Compatible only and Release only
start enabled. Compatibility means **Compatible according to publisher metadata**;
JustVoxel has not verified third-party runtime compatibility. Required, optional,
incompatible and embedded dependencies are publisher metadata only. If the active
Minecraft version cannot be determined, the compatible search stays unavailable;
turning off Compatible only permits browsing without claiming compatibility.
Catalog pages contain up to eight projects. Release filtering can leave a page
empty; Next continues to the next catalog page. Upstream failures are shown as
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
