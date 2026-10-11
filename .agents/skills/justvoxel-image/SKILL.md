---
name: justvoxel-image
description: Change or review JustVoxel bootc image composition, Containerfile/build logic, system files, packages, systemd units, SELinux integration, persistent-state boundaries, health checks, and other components baked into the Base image.
---

# JustVoxel Image

Keep the Base image simple, immutable, reproducible, and appliance-focused.

## Core Rules

- Honor the immutable OS and product boundaries in AGENTS.md.
- Put required host packages, binaries, services, and policy into the image rather than runtime layering.
- Keep immutable image content separate from persistent Minecraft/JustVoxel state.
- Ensure worlds, configuration, backups, and appliance state that must survive updates are not accidentally hidden in image-only locations.
- Consume Home Server Packages through their established channel tags, especially `:stable` for stable packages.
- Never introduce a private digest-resolution layer for those packages.
- Inspect existing build/system patterns before inventing another mechanism.
- Prefer declarative systemd/image configuration over fragile boot-time scripting where practical.
- Preserve SELinux Enforcing and all known-working labels, types, policy, labeling behavior, and the established Podman `:Z` data-labeling model.
- Never use SELinux as speculative troubleshooting: do not switch to permissive mode, weaken policy, add `chcon` hacks, broad `restorecon` or relabel-at-boot fixes, or `semanage fcontext` changes merely to make an unexplained failure pass.
- Investigate AVC evidence first and prove SELinux is actually involved before proposing an SELinux change.
- Any deliberate SELinux policy or labeling change requires explicit approved SELinux scope and human approval.
- If SELinux is Enforcing and the existing labeling works, LEAVE IT ALONE.
- Avoid broad privileges that are not required by the actual appliance function.

## Validation Levels

Source checks do not prove an image builds.

A successful image build and configured health checks do not prove the appliance boots or behaves correctly in a VM.

Use the normal GitHub Actions image pipeline for full builds when the task reaches that stage. Do not create a local nested-VM build system merely to test every edit.
