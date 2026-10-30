# Optional Minecraft Network

Minecraft Network connects selected existing Minecraft servers through Velocity
and a virtual lobby inside Velocity. It is disabled by default. Multiple standalone
servers do not require a proxy.

Open **Control Center → Minecraft → Network** or **Minecraft Servers → Network**
as an administrator. Both open the Minecraft Network workspace on the dashboard.
Choose Paper or Purpur servers, set the public Java and Bedrock ports and a lobby
welcome message, and
turn on Enabled. Modern forwarding requires Minecraft 1.13 or later. Fabric,
NeoForge, Spigot, Vanilla, Forge, Quilt and native Bedrock stay standalone in this
implementation. Velocity is infrastructure, not an Add Server choice.

Stop selected servers with their normal Stop controls before changing network
membership, ports or lobby settings. Apply saves the complete network definition
and starts the infrastructure. Start the backend servers again afterwards. Their
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

Publication uses the existing maintenance locks and atomic private-file writes.
A pending network journal blocks startup until recovery restores the previous
configuration. Use **Network → Retry recovery** or `mjust minecraft-network recover`;
do not edit journals or use a reset to clear a failed publication. Recovery also
handles first-enable interruptions before infrastructure services are generated.
Disabling the network removes its generated units and clears only stale failed
systemd state for units that are now absent; failures for still-loaded units remain
visible for diagnosis.

Standalone configuration and managed artifact snapshots are private to each
instance. Network-member data also carries bounded original standalone
configuration evidence so backups and exports can restore original forwarding and
Floodgate settings when imported as standalone servers. These records contain no
routing identity or destination host paths. Worlds and per-server whitelists are
preserved independently.

There is exactly one private infrastructure bind mount:

- Velocity: `/etc/justvoxel/minecraft-network/velocity:/server:Z`

It is generated by `networkQuadlets` in
`management/cmd/justvoxel-management-agent/minecraft_network_transaction.go`.
Existing instance mounts and data labels retain their established model.

## Lobby and server selection

Velocity is the only Minecraft Network infrastructure container and the only
public Java entrance. Selected Paper/Purpur members remain private. LimboAPI
provides one lightweight virtual lobby inside Velocity; the small JustVoxel Lobby
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
