# Third-party notices

## Home Server Base 10 / AlmaLinux / bootc foundation

JustVoxel Base inherits its shared AlmaLinux 10 Minimal Plus bootc foundation from the Home Server Project's Home Server Base 10 image. Home Server Base 10 owns the generic operating-system composition; JustVoxel Base adds the Minecraft-appliance layer on top.

- Home Server Base 10: https://github.com/home-server-project/home-server-base-10
- AlmaLinux: https://almalinux.org/
- bootc: https://github.com/bootc-dev/bootc

## Passive Black Box / Home Server Rose build references

The repository/build layout and immutable-template-to-local-configuration model are adapted from proven Home Server Project / Highway to IT AlmaLinux bootc patterns:

- https://github.com/highwaytoit/pasiv-black-box
- https://github.com/home-server-project/home-server-rose

JustVoxel reuses only patterns useful to this purpose-built Minecraft appliance rather than the broader Rose/Cockpit/HCI stack.

## Minecraft runtime projects

JustVoxel ships configuration and management code for upstream server projects.
The bootc image does not contain Mojang, Paper, Purpur, Spigot, mod-loader, or
Bedrock server binaries. The selected workload image and its verified upstream
download paths acquire the applicable runtime when an administrator creates or
starts an instance.

- itzg/minecraft-server: https://github.com/itzg/docker-minecraft-server
- itzg/minecraft-bedrock-server: https://github.com/itzg/docker-minecraft-bedrock-server
- Paper: https://papermc.io/
- Purpur: https://purpurmc.org/
- Spigot: https://www.spigotmc.org/
- Fabric: https://fabricmc.net/
- NeoForge: https://neoforged.net/
- Minecraft Forge: https://minecraftforge.net/
- Quilt: https://quiltmc.org/
- Minecraft Java and Bedrock: https://www.minecraft.net/
- Geyser: https://geysermc.org/
- Floodgate: https://geysermc.org/wiki/floodgate/
- ViaVersion: https://viaversion.com/

## Optional Minecraft Network

The optional network uses the itzg Velocity proxy image and verified proxy
plugins. LimboAPI and the repository-built JustVoxel Lobby plugin are compiled
and baked into the bootc image with pinned hash evidence. Geyser, Floodgate, and
Global Whitelist are downloaded and verified by the appliance's managed artifact
workflow. The Velocity server JAR used to compile JustVoxel Lobby is temporary and
is not bundled as a second runtime.

- itzg/mc-proxy: https://github.com/itzg/docker-mc-proxy
- Velocity: https://papermc.io/software/velocity
- LimboAPI: https://github.com/Elytrium/LimboAPI
- Global Whitelist: https://github.com/UrAvgCode/global-whitelist

Exact versions, hashes, and the temporary LimboAPI compatibility pin are recorded
in [`docs/minecraft-network.md`](docs/minecraft-network.md) and the image build
metadata.

## Appliance tools and remote access

The image also consumes these maintained external tools or package artifacts.
Their upstream projects retain their own licenses and notices:

- Tailscale: https://tailscale.com/
- NetBird: https://netbird.io/
- Playit: https://playit.gg/
- NordVPN: https://nordvpn.com/
- Superfile: https://github.com/yorukot/superfile
- Glances: https://github.com/nicolargo/glances
- ttyd: https://github.com/tsl0922/ttyd

## Universal Blue `ujust` / `ugum`

The `mjust` interaction model is inspired by Universal Blue's `ujust` / `ugum` implementation:

- https://github.com/ublue-os/packages/tree/main/packages/ublue-os-just

JustVoxel currently uses its own wrapper, justfile, menus, and administration scripts and does not copy `ujust` or `ugum` source code. If future JustVoxel work directly reuses or adapts Universal Blue source, the applicable Apache-2.0 license and attribution will be retained with those files.
