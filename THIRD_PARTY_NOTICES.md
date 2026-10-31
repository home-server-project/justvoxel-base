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
plugins. The two baked JARs, LimboAPI and JustVoxel Lobby, come from the Home
Server Packages stable artifact
`ghcr.io/home-server-project/minecraft-network-plugins:stable`. Home Server
Packages owns their build, upstream tracking and provenance; JustVoxel consumes
the runtime artifacts. Geyser, Floodgate, and
Global Whitelist are downloaded and verified by the appliance's managed artifact
workflow. The Velocity server JAR used to compile JustVoxel Lobby is temporary and
is not bundled as a second runtime.

- itzg/mc-proxy: https://github.com/itzg/docker-mc-proxy
- Velocity: https://papermc.io/software/velocity
- LimboAPI: https://github.com/Elytrium/LimboAPI
- Global Whitelist: https://github.com/UrAvgCode/global-whitelist

LimboAPI compatibility and build provenance are maintained by Home Server
Packages in `packages/minecraft-network-plugins/`. JustVoxel package consumption
and runtime artifact verification are described in
[`docs/minecraft-network.md`](docs/minecraft-network.md).

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

## AI & API gateway Go dependencies

- Official MCP Go SDK `github.com/modelcontextprotocol/go-sdk` **v1.8.0**,
  used only by the unprivileged WebUI for Streamable HTTP and RFC 9728 resource
  metadata: https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0
  Its release license records the transition to Apache-2.0 with earlier
  un-relicensed contributions retaining MIT terms. Retain the upstream license:
  https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/LICENSE
- `github.com/golang-jwt/jwt/v5` **v5.3.1** (MIT), used by the Management Agent
  for audited JWT signature/claim verification. This small library avoids
  implementing cryptographic JWT verification in JustVoxel; JWKS sources and
  connection bindings remain explicitly administrator-configured. It is also
  used transitively by the MCP SDK:
  https://github.com/golang-jwt/jwt/tree/v5.3.1

JWT copyright and permission notice:

```text
Copyright (c) 2012 Dave Grijalva
Copyright (c) 2021 golang-jwt maintainers

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

```

MCP SDK transitive modules are pinned in `webui/go.mod`/`webui/go.sum`, including
Google jsonschema-go, Segmentio encoding/asm, yosida95 uritemplate, and Go
OAuth2/time/sync/sys modules. Their upstream licenses remain applicable; no
additional operating-system packages are introduced.

MCP SDK retained MIT notice:

```text
MIT License

Copyright (c) 2024-2025 Model Context Protocol a Series of LF Projects, LLC.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

```
