---
name: justvoxel-transactional-ops
description: Design or review multi-stage JustVoxel operations where failure can damage or strand state, including backup, restore, migration, server import, storage movement, provisioning, reset, and similar destructive workflows.
---

# JustVoxel Transactional Operations

Make state-changing operations boring, bounded, recoverable, and hard to misuse.

## Core Process

- Validate prerequisites before disruption.
- Fail early for invalid inputs, unavailable storage, incompatibility, or insufficient space.
- Stage preparatory and expensive work before downtime when possible.
- Stop Minecraft only when the operation reaches a stage that actually requires downtime.
- Track clear stages so completed, current, remaining, and failed work are understandable.
- Use bounded retries only. Never add infinite retry or re-planning loops.
- Protect known-good data until the replacement is sufficiently validated.
- Preserve useful recovery state and failure information.
- Use player-aware Minecraft shutdown where appropriate.

## Data Safety

- Do not erase an entire external disk, mount, share, or remote location merely because JustVoxel references it.
- Do not delete the old working state until the new state has reached the operation's defined safety point.
- Treat import, migration, restore, reset, and storage moves as explicit appliance operations, not ad-hoc file copying.

## Confirmation UX

Reuse existing JustVoxel confirmation patterns for destructive operations: clear warning, deliberate slider/toggle or equivalent confirmation, then Apply/OK.

Do not add repeated modal chains or typed phrases just to increase friction.

Use `justvoxel-management-api` when the operation crosses the privileged API boundary and `justvoxel-human-experience` when user-facing flow or wording changes.
