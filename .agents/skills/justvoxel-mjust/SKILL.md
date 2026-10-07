---
name: justvoxel-mjust
description: Change or review the JustVoxel mjust terminal interface, menus, direct commands, helpers, recovery commands, and CLI behavior for local or SSH administration.
---

# JustVoxel mjust

Keep mjust a simple local administration and recovery interface for users comfortable with a terminal.

## Core Rules

- Keep mjust thin. Do not turn it into a second appliance orchestration engine.
- When mjust exposes the same product capability already owned by the Management Agent, normally use the shared backend instead of duplicating safety/orchestration logic.
- Menu and direct-command entry points for the same function should reach the same behavior and safety checks.
- Direct commands should work sensibly without requiring navigation through the menu.
- If an operation requires an interactive TTY, detect that and fail clearly rather than hanging.
- Keep recovery functions useful even when the WebUI is unavailable.
- CLI wording may be more technical than WebUI wording, but should still be understandable without memorizing internal unit/container/path names.

## Intentional CLI-Only Capabilities

Do not force WebUI parity.

Examples:

- Networking CLI uses HSP Network Manager.
- Advanced filesystem access may launch Superfile.
- Other approved local tools may remain terminal-only.

Run relevant shell syntax/regression tests for changed mjust paths. Do not use a passing menu test as proof of VM/runtime behavior.
