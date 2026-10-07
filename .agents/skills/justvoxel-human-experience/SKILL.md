---
name: justvoxel-human-experience
description: Design or review JustVoxel user-facing language and workflows so the Minecraft appliance stays understandable to people with little or no Linux knowledge. Use for labels, setup flows, confirmations, warnings, errors, help text, user documentation, and other human-facing behavior.
---

# JustVoxel Human Experience

Design for a normal Minecraft-server user, including a young or non-technical user.

## Core Rules

- Describe the human goal, not the Linux mechanism.
- Prefer terms such as Start Server, Create Backup, Restore Backup, Move Server Storage, or Update JustVoxel.
- Do not require users to understand Podman, systemd, mount units, Unix sockets, internal APIs, or filesystem internals for normal appliance use.
- Keep technical transparency optional. A console or detailed status view may expose internals, but normal use must not depend on understanding them.
- Keep warnings plain and specific: what will happen, what may be lost, and what the user must decide.
- Reuse JustVoxel's existing deliberate confirmation patterns. Do not create chains of repeated dialogs or typed confirmation phrases unless a genuinely new risk requires it.
- Avoid adding low-level controls merely because the backend can expose them.
- Keep developer documentation and internal comments technical when that improves maintainability.

## Decision Test

Before adding a user-facing control or message, ask:

1. What is the user trying to accomplish?
2. Can the interface describe that goal without exposing implementation detail?
3. Is the warning/action understandable without Linux knowledge?
4. Does this make JustVoxel feel more like an appliance and less like a generic server panel?

Use the relevant domain skill as well when the change is primarily WebUI, networking, transactional, or management work.
