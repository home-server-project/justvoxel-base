# Minecraft server software

Add Server Simple Setup uses three states in the existing card: family, software,
then server name and game mode. It leads to the shared Review page. Advanced Setup
retains Server, Cross-play, Memory, Storage, Backups and Review, with software
grouped by capability. All eight Java server types and native Bedrock are selectable.

Setup resolves an exact stable Minecraft release. Spigot derives its JDK image
from that release: java21-jdk for 1.20.5–1.21.11 and java25-jdk for 26.x. Mod loaders
resolve a stable exact loader/build from their publisher metadata and verify the
exact game/loader pair using the existing upstream artifact verifier. The draft
carries these pins through Review/Create. No manual loader control is needed.
New server types use a pinned runtime; this does not enable ongoing game updates.

Simple Setup enables managed cross-play only when compatibility is proven for its
exact runtime. Missing cross-play evidence leaves Java setup available with
cross-play disabled. Advanced Setup validates an enabled cross-play choice
separately; disabling it permits a valid Java server to continue. Fabric and
NeoForge require exact platform compatibility; Forge, Quilt and Vanilla are Java
only. Existing storage, backup and port safety remain authoritative.

| Software | Bukkit Plugins workspace | Managed Bedrock cross-play |
| --- | --- | --- |
| Paper, Purpur | Modrinth and Hangar | Existing Spigot-family implementation |
| Spigot | Modrinth: explicit Spigot/Bukkit metadata | Spigot-family Geyser/Floodgate with managed ViaVersion |
| Fabric | Unavailable | Only a proven current Geyser-Fabric game/loader pair, Floodgate-Fabric and exact-version Fabric API |
| NeoForge | Unavailable | Only a proven current Geyser-NeoForge game/loader pair and Floodgate-NeoForge |
| Vanilla, Forge, Quilt | Unavailable | Unavailable |
| Native Bedrock | Unavailable | Native Bedrock clients; no Geyser/Floodgate |

Spigot's Hangar provider stays unavailable: the current Hangar platform mapping
only proves Paper compatibility. Modrinth releases must explicitly include
`spigot` or `bukkit` and the exact Minecraft version; installation also requires
a usable Bukkit `plugin.yml` descriptor.

Fabric and NeoForge platform downloads come only from the fixed official
GeyserMC download API. Their SHA-256 hashes are checked before replacement.
Compatibility requires agreement between Geyser's published current Java release
and dependency metadata inside the verified platform artifacts, including the
selected loader pin. Missing, unsupported or contradictory evidence blocks
cross-play. Older releases never gain a Bukkit ViaVersion plugin or another proxy.

Fabric API uses fixed Modrinth project `P7dR8mSH`, explicit Fabric loader metadata,
and the exact game release. Only a publisher release with one primary artifact
and its SHA-512 hash can be selected. Geyser, Floodgate and Fabric API are staged
as one set before replacement in `mods`; no mod is placed in `plugins`.

Managed mods have root-private per-instance ownership evidence. Unowned canonical
files, modified owned files and duplicate managed mod identities block replacement.
Interrupted replacement retains rollback evidence; disabling cross-play removes
only verified owned Geyser/Floodgate binaries and preserves configuration, identity
material and Fabric API, which other mods may also need.
The existing Podman data-mount labeling model remains unchanged.

The configured Geyser numeric port is reserved and published over **both TCP and
UDP**, with firewall ownership tracked for both protocols. It cannot share a TCP
port with a Java server. The primary Geyser transport is NetherNet; this change
does not allocate another WebRTC port or install a signaling service.

Source checks do not establish runtime compatibility or visual acceptance. Verify
these changes in the development VM before release.

Publisher references used by this implementation:

- [Geyser supported versions](https://geysermc.org/wiki/geyser/supported-versions/)
- [Geyser setup, platform configuration and TCP/UDP requirements](https://geysermc.org/wiki/geyser/setup/)
- [Official Geyser download API](https://geysermc.org/wiki/api/downloads.geysermc.org/build-latest/)
- [Fabric API publisher project](https://modrinth.com/mod/fabric-api)

## Native Bedrock

Bedrock is a separate server runtime using
`docker.io/itzg/minecraft-bedrock-server:stable`, with per-instance persistent
storage mounted at `/data`. It has no Java image, Java heap, plugins, mod loaders,
or managed Geyser/Floodgate cross-play. Survival, Creative and Adventure are
available as initial game modes. Servers remain open to authenticated Bedrock
players; JustVoxel does not currently provide a Bedrock allow-list workflow.

Setup verifies the official Minecraft Services `serverBedrockLinux` release URL
and pins its exact numeric version. Preview releases and moving `LATEST` versions
are refused. An unavailable or changed release requires reviewing setup again.
Restarting a Bedrock container keeps the selected game version.

NetherNet uses one unique TCP signaling port (first candidate 19132) and a unique
contiguous UDP gameplay range (first candidate 19140), with at least one UDP port
per maximum player. Both active listeners and stopped instance reservations are
checked. Native containers use host networking so Bedrock advertises appliance
addresses without a guessed NAT address. LAN broadcast discovery is disabled;
connect using the appliance address and the server's TCP port. Routers and
external firewalls must allow the TCP port and complete UDP range as well.
JustVoxel tracks ownership of each firewall port, preserves pre-existing rules,
and removes only operation/instance-owned rules during rollback or deletion.

Simple Setup selects Bedrock through family, software, then name/game mode and
uses the shared Review. Advanced Setup retains six steps; its Cross-play step
shows the native connection and exact release, and Memory offers only a container
limit. The existing identity, lifecycle, boot preference, backup inventory and
cold-backup/transactional restore paths apply. Restore requires the same instance,
Bedrock software and exact version, and validates Bedrock world headers without
using Java NBT metadata. Java/Bedrock conversion is unsupported.

## Appliance timezone

Minecraft setup and Review have no independent timezone setting. New instance
configuration does not store one. Java and Bedrock containers inherit appliance
time through a read-only `/etc/localtime` mount; change the authoritative timezone
under System → Date & Time. Legacy timezone fields remain readable for deployed
records and recovery journals but are not used to generate new runtimes.

Upstream contracts:
[Bedrock image configuration](https://github.com/itzg/docker-minecraft-bedrock-server#nethernet),
[official release metadata](https://net.web.minecraft-services.net/api/v1.0/download/links).
