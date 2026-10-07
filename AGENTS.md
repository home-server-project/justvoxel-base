# JustVoxel Base Agent Instructions

Read this file before modifying the repository.

## Project

JustVoxel Base is the development/source repository for the VM-ready JustVoxel bootc image.

It owns:

- WebUI
- privileged Management Agent and Management API
- mjust
- Minecraft runtime integration
- storage, backup, restore, migration, import, and reset workflows
- networking integration used by the appliance
- system/image integration
- source and image validation

Base image:

ghcr.io/home-server-project/justvoxel-base

Upstream platform:

Home Server Base 10 -> AlmaLinux 10 -> bootc

This repository does not own the JustVoxel ISO installer or the future public-facing JustVoxel release repository.

## Product boundary

JustVoxel is a focused Minecraft server appliance, not a general-purpose server distribution.

The supported product manages one Minecraft server container.

Knowledgeable users may run additional Podman containers manually, but generic multi-container management is outside the supported JustVoxel product.

Prefer simple, safe, stable, secure solutions. Do not add machinery merely because it is technically possible.

## Branch and release model

Active integration development happens on `testing`.

Short-lived development branches are allowed when the human explicitly selects or creates them for a task. Do not silently switch branches or create a branch on your own.

Before editing, inspect:

- `git status -sb`
- `git branch --show-current`
- `git log -1 --oneline --decorate`

Normal Codex scope is `testing` or an explicitly selected development branch.

`main` and any future `stable` branch are outside normal Codex modification scope unless the human explicitly approves a branch-specific task.

When a future `stable` branch exists, promotion is human-controlled. The intended release flow is development branch -> PR to testing -> testing image -> human runtime/VM acceptance -> PR from testing to stable -> stable image.

Codex must not merge, promote, tag, publish a release, or modify a stable branch unless the human explicitly authorizes that exact action.

For a major/global change, flag when a human-created backup checkpoint such as `testing-backup` would be sensible. Do not create it automatically.

## Working model

Use this loop:

Research/reproduce -> define exact scope -> human approval -> implement -> validate -> independent review -> human decides commit/push/promotion.

Do not edit files before implementation is approved.

Do not expand scope without explicit approval.

If the human expands scope during the task, absorb the new approved scope into the same work cycle.

If another problem is discovered:

1. decide whether it blocks the approved task;
2. report it clearly;
3. do not fix it unless it is required by the approved scope or the human expands scope.

If a relevant test fails because of an unrelated area, do not chase the failure through other subsystems. Report the failure, likely cause, and boundary.

Protect known-working behavior. Do not replace, restructure, or clean up working code unless the approved task requires it.

Avoid speculative cleanup and endless test-fix-test loops.

## Skills

Repository-local skills live under `.agents/skills/`.

Always read this AGENTS file first. Then load only the skill or skills that materially apply to the task.

Normally use one primary skill and, when needed, one supporting skill. Three skills are reasonable only for a genuinely cross-cutting task. Do not load the whole skill directory.

Available skills:

- `justvoxel-human-experience`: user-facing language, flows, warnings, confirmations, and appliance usability.
- `justvoxel-management-api`: privileged operations, Management Agent/API boundaries, authorization, and shared backend capabilities.
- `justvoxel-transactional-ops`: backup, restore, migration, storage movement, import, reset, and other multi-stage state-changing operations.
- `justvoxel-webui`: WebUI handlers, templates, JavaScript, CSS, workspaces, roles, dialogs, progress, and responsive behavior.
- `justvoxel-mjust`: mjust menus, direct commands, recovery CLI, and terminal behavior.
- `justvoxel-image`: Containerfile, build files, system files, packages, systemd, SELinux, bootc image composition, and persistence boundaries.
- `justvoxel-ci`: GitHub Actions, source checks, image builds, publishing/signing, workflow triggers, and CI failure investigation.
- `justvoxel-networking`: appliance networking, HSP Network Manager CLI integration, WebUI networking, LAN/remote-access behavior, and Tailscale-related flows.
- `justvoxel-review`: independent human-directed verification/review of a completed change.

Typical routing examples:

- WebUI layout or copy: `justvoxel-webui` + `justvoxel-human-experience`.
- New privileged WebUI operation: `justvoxel-webui` + `justvoxel-management-api`; add human-experience only when user-facing behavior changes.
- Restore/migration/storage change: `justvoxel-transactional-ops` + `justvoxel-management-api`.
- Networking WebUI change: `justvoxel-networking` + `justvoxel-webui`.
- mjust networking change: `justvoxel-mjust` + `justvoxel-networking`.
- Image/system integration change: `justvoxel-image`.
- Workflow/build failure: `justvoxel-ci`.
- Independent review: `justvoxel-review` + the relevant domain skill.

A skill supplements this file. It never overrides the global scope, Git/GitHub, or approval rules here.

## Git and GitHub safety

Read-only Git inspection is allowed when relevant.

Never perform any of the following unless the human explicitly approves that exact action:

- commit
- push
- force push
- create or delete branches
- create or modify pull requests
- merge
- tag
- release
- manually trigger or cancel GitHub Actions
- modify repository settings

Local source edits inside the approved scope are allowed after implementation is approved.

Never discard existing user changes.

Never use destructive Git commands such as:

- `git reset --hard`
- `git clean -fd`
- `git checkout -- .`
- `git restore .`

unless the human explicitly asks for that exact action.

## Immutable OS rules

JustVoxel is an image-built immutable bootc appliance.

Do not design product fixes around runtime RPM layering.

Do not use or re-enable `rpm-ostree` as a product solution.

Required host packages, binaries, services, and policy belong in the image.

Prefer image composition, systemd, Podman, NetworkManager, firewalld, SELinux, and bootc-native patterns.

Home Server Packages are consumed by their channel tags. Stable packages use `:stable`.

Do not introduce a custom digest-resolution layer for Home Server Packages.

Do not instruct the human to paste `set -euo pipefail` into an interactive shell. Existing non-interactive scripts/CI using it are not automatically a problem.

## Architecture boundary

Preserve the privileged-operation model:

Browser -> unprivileged WebUI -> Management API over local Unix socket -> privileged Management Agent -> explicit appliance operation.

Do not make the WebUI:

- run arbitrary shell commands;
- run arbitrary mjust commands;
- control generic systemd units;
- control generic Podman containers;
- bypass the Management Agent for privileged operations.

For genuine product capabilities shared by WebUI and mjust, prefer one authoritative backend implementation.

Do not force artificial parity between interfaces when the design intentionally differs.

## Repository layout

Important areas:

- `management/`: privileged Go Management Agent and API
- `webui/`: unprivileged Go WebUI
- `mjust/`: terminal appliance interface and helper commands
- `runtime/`: Minecraft runtime helpers and service integration
- `system_files/`: files installed into the appliance image
- `templates/`: generated/runtime configuration templates
- `build_files/`: image composition and build logic
- `tests/`: source-level integration and regression tests
- `docs/`: architecture and user/developer documentation
- `Containerfile`: bootc image definition

Before creating a new implementation path, inspect whether equivalent logic already exists.

## Coding and security rules

Keep changes narrowly scoped.

Prefer simple, explicit implementations over abstraction unless the abstraction removes real duplication.

Do not weaken validation merely to make a failing input pass.

Do not remove strict decoding or safety checks globally to work around one contract mismatch.

Do not silently ignore errors that can identify a real runtime failure.

Do not expose passwords, credentials, tokens, session secrets, SMB passwords, or other secrets through logs, command arguments, audit output, URLs, browser storage, or returned API payloads.

Preserve SELinux enforcement and existing permission boundaries.

## Validation

During implementation, run reasonable checks for the changed area.

Use the narrowest relevant tests first.

Examples:

- Management Go changes: relevant `go test` / `go vet`.
- WebUI Go changes: relevant WebUI tests.
- Shell/helpers: syntax checks and directly relevant tests under `tests/`.
- Cross-layer changes: verify the affected layers together when practical.

Do not run every possible check for every tiny change.

Do not repeatedly prove facts already guaranteed by Home Server Base unless JustVoxel has a specific integration assumption to validate.

Do not claim a check passed unless it was actually executed.

Keep validation levels separate:

- local/source checks prove source behavior only;
- CI image build proves the image built and passed configured image checks;
- human development-VM testing proves real boot/runtime/visual behavior.

Do not claim runtime or visual acceptance from source tests or CI alone.

## Documentation

Update documentation when behavior, architecture, supported configuration, user workflow, or security boundaries change.

Do not update documentation merely to narrate implementation details that users or future maintainers do not need.

When documentation and implementation disagree, investigate rather than silently choosing one.

## Final report

After an implementation task, keep the report short:

1. what was implemented;
2. main files/areas changed;
3. tests/checks run and results;
4. blocker or important unverified item, if any.

Codex's report is not proof of correctness. Independent human-directed review follows before release decisions.
