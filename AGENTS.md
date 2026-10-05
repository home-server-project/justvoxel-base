# JustVoxel Base Agent Instructions

This document provides repository-specific instructions for coding agents working on JustVoxel Base.

Read this file before modifying the repository.

## Project

JustVoxel is an immutable bootc-based Minecraft server appliance.

This repository owns the shared VM-ready JustVoxel Base implementation:

- WebUI
- privileged Management Agent
- Management API
- mjust
- Minecraft runtime integration
- storage, backup, restore, migration, and reset workflows
- system integration
- common image validation

Base image:

ghcr.io/home-server-project/justvoxel-base

Upstream platform:

Home Server Base 10
→ AlmaLinux 10
→ bootc

## Development branch

Plus development happens only on:

testing-plus

Do not modify `main` unless the user explicitly approves a stable promotion or main-specific task.

Before starting work, verify:

git status -sb
git branch --show-current
git log -1 --oneline --decorate

Required Plus development branch:

testing-plus

Do not inspect, modify, merge into, cherry-pick into, or use the normal testing branch.
Do not silently switch branches.

## Golden references

When architecture or implementation patterns require external comparison, prefer these Universal Blue projects:

- https://github.com/ublue-os/bluefin
- https://github.com/ublue-os/ucore
- https://github.com/ublue-os/bazzite
- https://github.com/ublue-os

Use them as references, not as code to copy blindly.

Inspect current upstream state before making claims about their implementation.

## Working model

The normal development workflow is:

Research / reproduce
→ define exact scope
→ user approval
→ implement
→ validate
→ review diff
→ user decides whether to commit/push

Do not expand scope without explicit approval.

If an additional problem is discovered while implementing:

1. report it;
2. explain whether it blocks the approved work;
3. do not fix it unless it is required for the approved scope or the user expands scope.

## Git and GitHub safety

Never perform any of the following unless the user explicitly approves it:

- commit
- push
- force push
- create or delete branches
- create or modify pull requests
- merge
- tag
- release
- modify GitHub Actions state
- modify repository settings

Local source edits inside the approved scope are allowed after the user has approved implementation.

Never discard existing user changes.

Never use destructive Git commands such as:

git reset --hard
git clean -fd
git checkout -- .
git restore .

unless the user explicitly asks for that exact action.

At the end of implementation, report:

- files changed
- tests run
- test results
- remaining concerns
- git diff/stat
- whether anything was not tested

## Immutable OS rules

JustVoxel is an image-built immutable appliance.

Do not design fixes around runtime RPM layering.

Do not use `rpm-ostree install` as a product solution.

Required OS packages belong in the image build.

Prefer normal image composition, systemd, Podman, NetworkManager, firewalld, SELinux, and bootc-native patterns.

Home Server Packages are consumed by their channel tag.

Stable packages use:

:stable

Do not introduce a custom digest-resolution layer for Home Server Packages unless explicitly approved.

## Architecture boundaries

Preserve the runtime security model:

Browser
→ unprivileged WebUI
→ Management API over local Unix socket
→ privileged Management Agent
→ explicit appliance operation

Do not make the WebUI:

- run arbitrary shell commands;
- run arbitrary `mjust` commands;
- control generic systemd units;
- control generic Podman containers;
- bypass the Management Agent for privileged operations.

For appliance functionality shared by WebUI and mjust, prefer one authoritative backend implementation rather than duplicating orchestration.

`mjust` should remain a valid console/SSH management and recovery interface even when equivalent WebUI functionality exists.

## Repository layout

Important areas:

- `management/`
  - privileged Go Management Agent and Management API

- `webui/`
  - unprivileged Go WebUI

- `mjust/`
  - terminal appliance interface and helper commands

- `runtime/`
  - Minecraft runtime helpers and service integration

- `system_files/`
  - files installed into the appliance image

- `templates/`
  - generated/runtime configuration templates

- `build_files/`
  - image composition and build logic

- `tests/`
  - source-level integration and regression tests

- `docs/`
  - architecture and user/developer documentation

- `Containerfile`
  - bootc image definition

Before creating a new implementation path, inspect whether equivalent logic already exists in another frontend or helper.

## Coding rules

Keep changes narrowly scoped.

Prefer simple, explicit implementations over additional abstraction unless the abstraction removes real duplication.

Do not weaken validation merely to make a failing input pass.

Do not remove strict JSON decoding or safety checks globally to work around one contract mismatch.

Do not silently ignore errors that can identify a real runtime failure.

Do not expose passwords, credentials, tokens, session secrets, SMB passwords, or other secrets through:

- logs
- command arguments
- audit output
- URLs/query strings
- browser localStorage
- returned API payloads

Preserve SELinux enforcement and existing permission boundaries.

## Destructive appliance operations

Storage provisioning, restore, migration, reset, and similar operations require special care.

Preserve:

- explicit planning/review before destructive Apply;
- bounded operations;
- player-aware Minecraft shutdown where applicable;
- transaction/recovery state where applicable;
- validation before activation;
- clear failure reporting.

Never add an automatic retry/re-plan loop around a destructive operation unless explicitly designed and approved.

External or remote storage must not be erased merely because JustVoxel configuration references it.

## WebUI

The WebUI is an appliance interface, not a generic Linux administration panel.

Normal users should see JustVoxel concepts rather than implementation internals wherever practical.

Preserve role boundaries:

- Administrator
- Operator
- Viewer

Do not expand a role's authority as a side effect of a UI change.

Desktop and phone behavior must both remain usable for WebUI changes.

## Validation

Run the narrowest relevant tests first.

For Go changes:

cd management && go test ./...

and/or:

cd webui && go test ./...

depending on the changed component.

For shell/helper changes, run the directly relevant tests under `tests/`.

Run broader regression tests when the changed behavior crosses Management Agent, mjust, runtime, or WebUI boundaries.

Do not claim a test passed unless it was actually executed.

Do not claim runtime/VM validation from source tests alone.

Full image builds are separate from source-level validation and should only be run when appropriate for the task.

## Documentation

Update documentation when behavior, architecture, user-visible workflow, supported configuration, or security boundaries change.

Do not update documentation merely to describe an implementation detail that users or future maintainers do not need.

When documentation and implementation disagree, investigate which one represents the intended current behavior instead of silently choosing one.

## Final report

After an implementation task, provide a concise report containing:

1. What changed.
2. Why.
3. Exact files changed.
4. Tests/validation executed.
5. Results.
6. Anything not verified.
7. Current `git status -sb`.

Do not commit or push merely because implementation and tests succeeded.
