---
name: justvoxel-networking
description: Change or review JustVoxel appliance networking, LAN configuration, remote access, Tailscale-related flows, HSP Network Manager CLI integration, and controlled WebUI networking behavior.
---

# JustVoxel Networking

Keep networking focused on getting the Minecraft appliance connected and reachable, not on turning JustVoxel into a router or general network-management platform.

## Core Rules

- Present user goals such as connect the server, show its address, configure supported network access, or enable remote access.
- Do not require normal users to understand NetworkManager profiles, interface internals, routing tables, systemd units, or Podman networking.
- Preserve known-working access paths unless the approved task requires changing them.
- Treat changes that can cut off management access as high regression risk and review dependencies before broad edits.

## Intentional Interface Split

CLI networking uses HSP Network Manager.

WebUI networking may use separate controlled Management Agent/backend operations.

That difference is intentional. Do not refactor the two interfaces into artificial implementation parity merely for architectural neatness.

Use `justvoxel-mjust` for CLI/menu changes, `justvoxel-webui` for UI behavior, and `justvoxel-management-api` when privileged networking operations themselves change.
