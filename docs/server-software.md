# Minecraft server software

Advanced Setup keeps the existing server-card grid and adds Spigot, Bedrock,
Fabric, Forge, NeoForge and Quilt. Paper, Purpur, Spigot and Vanilla can be selected.
Fast Path retains Paper, Purpur and Vanilla.

Spigot requires **Specific version** and a matching **Custom server update tag**:
`java21-jdk` for Minecraft 1.20.5–1.21.11, or `java25-jdk` for 26.x. These are
existing controls. Incomplete combinations cannot proceed to setup review.

The mod-loader cards remain unavailable in the wizard: their runtimes require an
explicit loader version and this UI does not yet collect one. No loader version
is guessed. Native Bedrock also remains unavailable; its separate runtime has
not been implemented. The Bedrock card cannot enter Java setup.

| Software | Bukkit Plugins workspace | Managed Bedrock cross-play |
| --- | --- | --- |
| Paper, Purpur | Modrinth and Hangar | Existing Spigot-family implementation |
| Spigot | Modrinth: explicit Spigot/Bukkit metadata | Spigot-family Geyser/Floodgate with managed ViaVersion |
| Fabric | Unavailable | Only a proven current Geyser-Fabric game/loader pair, Floodgate-Fabric and exact-version Fabric API |
| NeoForge | Unavailable | Only a proven current Geyser-NeoForge game/loader pair and Floodgate-NeoForge |
| Vanilla, Forge, Quilt | Unavailable | Unavailable |
| Native Bedrock | Unavailable | No runtime in this step |

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
