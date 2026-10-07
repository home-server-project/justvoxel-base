---
name: justvoxel-management-api
description: Change or review JustVoxel privileged operations, Management Agent/API contracts, authorization, validation, and shared backend capabilities used by WebUI or mjust.
---

# JustVoxel Management API

Keep privileged system changes behind explicit appliance operations.

## Architecture

Preserve:

Browser -> unprivileged WebUI -> local Unix-socket Management API -> privileged Management Agent -> explicit operation.

## Core Rules

- Reuse an existing Management Agent operation when it already owns the capability.
- When a genuinely shared WebUI/mjust product capability needs new backend support, add the smallest explicit operation that represents the appliance goal.
- Model operations as actions such as restore backup, start server, move storage, or configure a supported network function.
- Do not expose generic shell execution, arbitrary systemd control, arbitrary Podman arguments, or arbitrary mjust execution.
- Validate inputs strictly at the privileged boundary.
- Keep authorization in the backend. Hidden buttons are not security.
- Keep secrets on the privileged side where practical and never return or log them unnecessarily.
- Preserve clear errors rather than silently converting failures into success.

## Shared Backend Does Not Mean Forced Parity

Use one authoritative backend for genuine shared product capabilities, but do not force interfaces to become identical.

Intentional examples:

- CLI networking may use HSP Network Manager while WebUI networking uses controlled Management Agent operations.
- CLI users may launch Superfile while WebUI intentionally has no general file manager.

Do not create a duplicate orchestration engine merely to make one frontend easier to implement.

Run relevant Management Agent Go checks for changed contracts and callers. Do not claim runtime acceptance from source tests alone.
