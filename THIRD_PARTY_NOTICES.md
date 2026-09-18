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

JustVoxel ships configuration templates that reference upstream runtime projects. Their Minecraft/Paper/Geyser/Floodgate/ViaVersion artifacts are fetched from their proper upstream sources at runtime; those server binaries are not redistributed in the bootc image.

- itzg/minecraft-server: https://github.com/itzg/docker-minecraft-server
- Paper: https://papermc.io/
- Geyser: https://geysermc.org/
- Floodgate: https://geysermc.org/wiki/floodgate/
- ViaVersion: https://viaversion.com/

## Universal Blue `ujust` / `ugum`

The `mjust` interaction model is inspired by Universal Blue's `ujust` / `ugum` implementation:

- https://github.com/ublue-os/packages/tree/main/packages/ublue-os-just

JustVoxel currently uses its own wrapper, justfile, menus, and administration scripts and does not copy `ujust` or `ugum` source code. If future JustVoxel work directly reuses or adapts Universal Blue source, the applicable Apache-2.0 license and attribution will be retained with those files.
