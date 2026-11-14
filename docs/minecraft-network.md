# Optional Minecraft Network

Minecraft Network connects selected existing Minecraft servers through Velocity
and a virtual lobby inside Velocity. It is disabled by default. Multiple standalone
servers do not require a proxy.

Open **Control Center → Minecraft → Network** or **Minecraft Servers → Network**
as an administrator. Both open the Minecraft Network workspace on the dashboard.
Choose supported servers, set the public Java and Bedrock
ports and a lobby welcome message, and
turn on Enabled. Modern forwarding requires Minecraft 1.13 or later.
Velocity is infrastructure, not an Add Server choice.

Supported Network backends:

- Native paper-family: Paper, Purpur, Pufferfish and Leaf.
- Managed fabric-family: Fabric and Quilt, using automatically managed
  FabricProxy-Lite compatibility and the existing exact game/loader selections.
- Managed forge-family: Forge and NeoForge, using automatically managed
  [Proxy Compatible Forge (PCF)](https://modrinth.com/mod/proxy-compatible-forge).

Managed backend availability is checked against each server's exact current game,
loader, Java context and installed mods. Fabric/Quilt require verified
FabricProxy-Lite compatibility; Forge/NeoForge require verified Proxy Compatible
Forge compatibility. Unavailable combinations remain standalone, with a short
reason under **Standalone / unsupported servers** in Network. Availability can
change when upstream publishes a newly compatible release that passes the same
strict artifact and packaged contract verification. Refresh Network to check
again; verified availability is cached in the Management Agent for up to five
minutes, with a shorter retry period for check failures. Provider/check failures
never force-enable a backend. Status checks do not stage or install components.
Apply always repeats authoritative pre-downtime verification and does not trust
the status cache.

Server state badges refresh every five seconds while the Network workspace is
open. These observations preserve unsaved settings and operation notices; use
Refresh for a full authoritative configuration and compatibility refresh.

Spigot, Vanilla and native Bedrock remain standalone.
[FabricProxy-Lite](https://modrinth.com/mod/fabricproxy-lite) is prepared
automatically; users do not need to install it in Mods. Fabric API is installed only when the selected release needs an external
API and no safely compatible user-owned API is present. Existing user mods are
preserved; conflicting or ambiguous compatibility mods block Apply.
CrossStitch is not installed by default. Backend Geyser-Fabric and Floodgate-Fabric
are not installed for Network compatibility; Geyser and Floodgate run on Velocity.

Apply checks the exact selected game and loader compatibility and prepares
compatibility components before any player warning or shutdown. Forge and
NeoForge keep the existing JustVoxel pinned game, exact loader and Java image
policy; PCF's broader upstream version support does not expand that policy.
PCF is automatically prepared for Velocity modern forwarding. Ambassador is not
installed because JustVoxel's supported Forge range is newer than the legacy
Forge 1.13–1.20.1 range that needs it. PCF includes its own CrossStitch support;
no separate CrossStitch mod is installed or configured.
If no verified compatible artifact exists, Apply fails before downtime and leaves
the server unchanged. Apply saves the complete network definition, starts the
infrastructure, and restarts backends that were running before the change. Their
Start on Boot choices are retained. Removing a member or disabling the network
restores its saved standalone publications and managed cross-play configuration.
A member must leave the network before it can be deleted or reset.

The defaults are Java TCP 25565 and Bedrock TCP/UDP 19132, matching JustVoxel's
managed Geyser transport. Enrolled backends publish neither their Java game port
nor their individual managed Geyser ports. Unselected servers keep their own
publications and whitelists. How to Connect and invitations for enrolled members
use the network endpoint. Existing remote-access mappings must target those
public network ports.

## Whitelists

**Enforce Network whitelist** controls whether the network player list is used as
a Velocity login gate. It defaults on for existing and new configurations. When
off, players may enter the lobby without a network-list entry, but every backend's
own whitelist remains independent and still applies. Turning enforcement off does
not delete saved Java or Bedrock entries; turning it back on reuses them.

When enforcement is on, the network whitelist is checked at Velocity login before
a player reaches the lobby or a backend. Java and Bedrock player operations are
separate. Player identities are resolved through the corresponding Mojang/Geyser
identity services; a lookup failure does not add an unverified identity.

On first enrollment, each selected backend's existing whitelist is imported into
the union of network players. A later member is imported once. Re-enrollment,
backend whitelist edits and backend removal do not re-import, synchronize or
subtract network players. Each backend's existing whitelist API remains
independent and authoritative for that backend.

With enforcement on, adding a player to the network whitelist grants entry to the
lobby. They also need permission from each backend's own whitelist to join that
server. Removing a network player blocks the entire network regardless of backend
entries. Changing the network whitelist restarts Velocity to enforce removal for
existing sessions; the backend servers and their whitelists are preserved.

## Terminal administration

Use the administrator's normal sudo authentication:

```text
mjust minecraft-network status
mjust minecraft-network members
mjust minecraft-network join jv-abcdef
mjust minecraft-network enable
mjust minecraft-network disable
mjust minecraft-network leave jv-abcdef
mjust minecraft-network whitelist list
mjust minecraft-network whitelist add java PlayerName
mjust minecraft-network whitelist add bedrock "Player Name"
mjust minecraft-network whitelist remove java PlayerName
mjust minecraft-network recover
```

The frontend calls the same Management API as the WebUI. It does not implement
privileged networking or publication in shell. General appliance networking
commands retain their existing meaning.

## State and recovery

Private root-owned JSON under `/etc/justvoxel/minecraft-network` owns configuration,
import-once evidence, the network whitelist, the forwarding secret, a 128-bit
Floodgate key, verified artifact hashes, firewall ownership and pending publication
evidence. Secrets are excluded from public API responses and diagnostics output.
The existing per-instance runtime records retain standalone port settings and
record whether the active definition is a network member.

Apply resolves and verifies infrastructure artifacts into the private cache and
runs backend profile preflight without changing live server data before any player-aware server shutdown.
Publication consumes those prepared artifacts without resolving or downloading
them again. A preparation failure leaves running Minecraft servers running.

Publication uses the existing maintenance locks and atomic private-file writes.
A pending network journal blocks startup until recovery restores the previous
configuration. Use **Network → Retry recovery** or `mjust minecraft-network recover`;
do not edit journals or use a reset to clear a failed publication. Recovery also
handles first-enable interruptions before infrastructure services are generated.
Disabling the network removes its generated units and clears only stale failed
systemd state for units that are now absent; failures for still-loaded units remain
visible for diagnosis.

Standalone configuration and managed artifact snapshots are private to each
instance. New snapshots bind their exact owned file set to the backend profile
(`paper-family` for Paper, Purpur, Pufferfish and Leaf). This native profile needs
no additional proxy plugin or mod. Fabric and Quilt share `fabric-family`, which
owns only `mods/justvoxel-network-fabricproxy-lite.jar`, the optional
`mods/justvoxel-network-fabric-api.jar`, and `config/FabricProxy-Lite.toml`.
[The official setup](https://github.com/OKTW-Network/FabricProxy-Lite#setup) uses
the same modern forwarding secret as Velocity; only its
unambiguous top-level TOML secret changes, preserving other settings. Leaving
restores the exact original configuration and removes only verified Network-owned
canonical artifacts. User-owned Fabric API remains untouched.

Fabric-family preflight verifies exact Minecraft publisher metadata, the primary
Fabric artifact, fixed Modrinth CDN identity, publisher SHA-512 and jar descriptors,
including required dependencies and conservative exact-loader compatibility.
Quilt reuses the existing selected loader validation and requires explicit Fabric
Loader compatibility evidence from the exact official Quilt loader jar. Missing or
unrecognized evidence fails closed. Quilt uses the Fabric artifact, never an
invented Quilt-specific release.

Forge and NeoForge share `forge-family`, owning only
`mods/justvoxel-network-proxy-compatible-forge.jar` and
`config/proxy-compatible-forge.toml`. PCF is resolved through its fixed Modrinth
slug, then bound to the returned immutable project ID, exact release, platform,
game and publisher SHA-512. The selected JAR must identify as `pcf`, prove exact
game and Forge/NeoForge loader compatibility, and require no unbundled external
mods. Existing user PCF installations conflict with enrollment and are preserved.

Only PCF's forwarding `enabled`, `mode` and `secret` entries change: modern
forwarding is enabled with the same secret as Velocity. Other settings and
comments, including approved proxy hosts, CrossStitch, debug and advanced
settings, are preserved. Ambiguous TOML fails closed. An absent config receives
only the minimal forwarding table. Leaving Network restores the exact original
PCF config, or removes it if Network created it, and removes only the verified
Network-owned PCF JAR. Changed or unowned artifacts are preserved.

PCF's publisher version must agree with the packaged mod version. Its packaged
Constants and EarlyConfig classes must prove `proxy-compatible-forge.toml` and
`forwarding.enabled`, `forwarding.mode`, `forwarding.secret`; the forwarding Mode
must prove `MODERN`. There is no version-number allowlist: a future release with
the same verified contract can become available without a JustVoxel update.
Changed or missing contract evidence, historical `pcf-common.toml` configurations,
and incompatible game/platform/loader ranges fail closed. If no verified PCF
release matches, Apply fails safely before downtime.

Verified jars and bounded context/hash metadata live in each instance's private
`network-staging` directory, outside `/data`. Publication and startup consume that
exact staging without provider requests or downloads. Modified staging, changed
runtime selections, conflicting identities and symlinks fail closed.
Historical snapshots without a profile remain
readable only with the selected instance ID and exact historical Paper file set;
reading them does not rewrite them. Restoration uses the snapshot's ownership,
while active members must also match the runtime software profile.
Network-member data also carries bounded original standalone
configuration evidence so backups and exports can restore original forwarding and
Floodgate settings when imported as standalone servers. These records contain no
routing identity or destination host paths. Worlds and per-server whitelists are
preserved independently. Paper portable schema 1 remains unchanged. Fabric-family
uses bounded schema 2: the original configuration and hashes/absent states for the
two fixed Network artifact paths, without embedding jars or user mod files.
Standalone restoration verifies identity and hash before removing a Network-owned
artifact; changed or ambiguous files are preserved and restoration fails closed.
Forge-family uses bounded portable schema 3 with only the original PCF config
content/hash or absent state and the fixed Network PCF ownership hash. It embeds
no JAR bytes. Standalone restore verifies PCF identity and ownership before
removing that artifact. Paper schema 1 and Fabric schema 2 are unchanged.

There is exactly one private infrastructure bind mount:

- Velocity: `/etc/justvoxel/minecraft-network/velocity:/server:Z`

It is generated by `networkQuadlets` in
`management/cmd/justvoxel-management-agent/minecraft_network_transaction.go`.
Existing instance mounts and data labels retain their established model.

## Lobby and server selection

Velocity is the only Minecraft Network infrastructure container and the only
public Java entrance. Selected Paper, Purpur, Pufferfish, Leaf, Fabric, Quilt, Forge and NeoForge members remain
private. LimboAPI provides one lightweight virtual lobby inside Velocity; the small JustVoxel Lobby
Velocity plugin supplies the server selector. No separate full Minecraft lobby
server or second lobby container exists. Geyser and Floodgate remain on Velocity
for Bedrock players.

Java players see the welcome message and an interactive clickable list of friendly
server names. Bedrock players automatically receive a native Floodgate/Cumulus
form titled **JustVoxel Servers** with one button per member. Use `/servers` to
return to the lobby and reopen either selector. Players do not need to type
backend IDs.
An empty network displays **No servers are currently available.** A missing
registered member leaves the player in the lobby. If form delivery is temporarily
unavailable, the player can retry with `/servers`.

Selection routes only to configured immutable `jv-…` IDs registered by Velocity.
Friendly names are presentation only. The generated `lobby.conf` in
`velocity/plugins/justvoxel-lobby/` uses schema 1 with Base64-encoded UTF-8 welcome
and display-name fields. It contains no secrets, whitelist identities or host
paths. Velocity has an explicitly empty automatic fallback list; it never sends
new players to an arbitrary backend.

## Component provenance and acceptance

Velocity uses `docker.io/itzg/mc-proxy:java25` with `TYPE=VELOCITY`. Java 25 is
required by the lobby plugins and Global Whitelist 1.2.1.

JustVoxel consumes
`ghcr.io/home-server-project/minecraft-network-plugins:stable` through a
Containerfile package stage. Home Server Packages owns the JustVoxel Lobby
source, LimboAPI compatibility tracking and acquisition, Velocity/Floodgate
compile dependencies, Java/Gradle build, validation, upstream tracking and
provenance. JustVoxel does not build these artifacts itself; the stable channel
tag is the package contract.

Image composition copies `/artifacts/limboapi.jar` and
`/artifacts/justvoxel-lobby.jar` from the package and installs non-empty,
root-owned mode-0644 copies under `/usr/lib/justvoxel/minecraft-network/`.
Appliances copy these baked JARs into the
private artifact cache and verify all managed JAR hashes before Velocity startup.
On a bootc image update, the baked LimboAPI and JustVoxel Lobby artifacts refresh
from the newly deployed image after the existing private artifact set has been
verified; externally fetched Geyser, Floodgate, and Global Whitelist evidence is
preserved. A corrupt cache fails closed before replacement. Appliances do not
download LimboAPI at runtime.

The old PicoLobby implementation has been removed. Apply migrates existing testing
systems by stopping Velocity and the legacy lobby, removing the legacy Quadlet
and generated configuration, and reloading systemd. A valid old four-artifact
manifest is verified and extended atomically with the two baked plugins, keeping
all existing external hashes. A six-artifact manifest must validate completely;
unknown or partial sets fail closed. Pre-commit recovery can temporarily restore
the previous legacy lobby, using private hash-bound rollback evidence. Apply
finishes the migration before further Network whitelist edits. There is no lobby
engine selector. Successful publication starts only Velocity, after releasing the
maintenance lock; readiness is Velocity active plus the local Java listener,
bounded to 30 seconds. Previously running backends restart after that phase.

Global Whitelist is pinned to [Modrinth project aKrMZ5cC, version 1.2.1](https://modrinth.com/plugin/global-whitelist).
Its primary Velocity JAR must match Modrinth's SHA-512 and size and the
[upstream release](https://github.com/UrAvgCode/global-whitelist/releases/tag/1.2.1)
SHA-256 `35c059c0913f2fa3cb75265edda4d84df84465c1ebebaefef3918e02be8bfbf5`.
Geyser and Floodgate use the existing fixed upstream project/build metadata and
SHA-256 verification model for their exact platform artifacts. Prepared artifacts
are verified as a complete set and reused with their private hash evidence; an
ordinary configuration change does not silently update them. Download failures
and checksum mismatches fail closed.

Floodgate uses shared private identity material on the proxy and backend plugins.
Backend Geyser binaries are suspended while enrolled. Container-written configuration is
regenerated from authoritative state before infrastructure startup, because
upstream configuration loaders may normalize their own files.

Source tests are included but were not run for this implementation, as requested.
Compilation, image builds, Java/Bedrock login enforcement, selector interaction,
SELinux behavior, firewall transitions, backup/restore/import, crash recovery and
visual acceptance still require human-directed validation before release.
