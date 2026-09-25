# JustVoxel roadmap

This roadmap is priority-based rather than date-based. Work moves forward when the current layer is tested and stable enough to support the next one.

JustVoxel remains a dedicated Minecraft server appliance. Normal operation should use guided, validated workflows rather than requiring Linux administration knowledge.

## P0 - Current testing and stabilization

The immediate priority is real-system validation of already-implemented appliance workflows, completion of the remaining Management API migrations, and stabilization of the recent WebUI workspace changes before adding more product surface.

### Real-system storage, backup, migration, and setup validation

The implementation exists. Remaining work is to prove it on real systems and failure paths:

- storage discovery and configuration
- removable and USB storage
- local, NFS, and SMB/CIFS backup storage
- destructive-storage safety and re-validation
- Minecraft data migration
- Server Migration Export, Import, and Recovery
- migration interruption/reconnect behavior
- rollback and needs-attention recovery
- fresh-unconfigured Recovery
- first-run Minecraft data and backup-destination selection
- transactional setup/apply behavior, including execution-time SMB credentials
- reboot persistence and recovery behavior
- backup and restore correctness

### Recent WebUI workspace validation

Validate the new workspace-window UI on the real appliance before old page cleanup:

- Minecraft workspace
- System workspace
- Storage workspace
- Backups workspace
- System Monitor
- System Update
- move, resize, reopen, close, and browser-state persistence
- desktop and small-screen behavior
- Administrator / Operator / Viewer visibility and authorization
- old pages remain available until the replacement UI is accepted and a separate cleanup pass is approved

### Unified Management API and mJust rebuild

Keep the JustVoxel Management Agent / Management API as the single appliance-management engine.

- treat WebUI and mJust as two frontends over the same engine
- keep mJust intentionally thin: terminal navigation, user input, API calls, and presentation
- do not recreate validation, safety policy, storage logic, Minecraft policy, or other business logic independently in mJust
- use local console or SSH authentication plus `sudo` as the CLI administrative trust boundary
- allow an explicitly authorized local UID 0 client to act as Management API administrator without creating a second CLI login/session system
- keep the WebUI Bearer-session and Administrator / Operator / Viewer authorization model unchanged
- do not grant the ordinary `voxel` account unrestricted direct access to the privileged management socket
- migrate remaining capabilities in small, independently verified steps
- develop and validate the unified Management API / mJust architecture on `testing` before stable promotion
- keep shared helpers where they are until direct CLI callers have safely disappeared, then progressively move backend helpers out of the misleading `mjust` namespace

Management API gaps that still need shared implementations before the corresponding direct CLI paths can disappear:

- Minecraft update policy and execution
- Start Over / reset workflows
- WebUI lifecycle and recovery behavior

### Cross-interface validation

Verify that CLI and WebUI remain consistent through shared Management API behavior rather than duplicated frontend business logic:

- configuration made through one interface is correctly understood by the other
- storage and backup state survives reboot
- failed operations fail safely
- recovery paths behave as documented
- installer and first-boot behavior remains correct
- backup and restore remain correct
- Minecraft/Paper updates remain correct
- bootc operating-system updates remain correct
- documentation is reconciled only after behavior is proven

## P1 - Small, high-value appliance additions

These are relatively contained improvements once P0 testing is complete.

### Documentation source and local documentation

Create a dedicated documentation repository as the authoritative user-documentation source.

- use `main` for approved, published documentation
- use `draft` for active documentation work and incomplete updates
- promote documentation from `draft` to `main` through a clean reviewed PR
- keep retired or historical material in an excluded directory such as `archive/` or `retired/` rather than adding another permanent publication branch
- have stable/public JustVoxel builds consume only approved documentation from `main`
- resolve and record a specific documentation commit for each image build so installed documentation is reproducible
- exclude draft, retired, contributor-only, and other non-user-facing material from the appliance documentation payload

For the CLI:

- use Glow to render the bundled Markdown snapshot
- provide an easy `mjust docs` entry point

For the WebUI:

- use Material for MkDocs to present the same bundled snapshot as a local readable site
- expose it through Help / Documentation
- keep documentation matched to the installed bootc generation and available offline

The future public website should present or link to the approved documentation from the same source repository so online and bundled documentation share one publication path.

### Minecraft server software choice

Keep the initial implementation within the existing single-server appliance model.

- allow first-run setup to choose Paper, Purpur, or Vanilla
- keep Paper as the default server software
- keep one Minecraft container and one active server
- support Bedrock cross-play for Paper and Purpur through the existing managed Geyser/Floodgate path
- make Vanilla Java-only in the initial implementation
- support safe in-place switching between Paper and Purpur
- treat the Paper/Purpur switch as a managed operation with stop, backup, type change, restart, and validation
- keep Minecraft version changes separate from server-software switching
- expose the same capability rules through both mJust and WebUI so the two interfaces do not drift
- do not support in-place migration to or from Vanilla in the initial implementation

## P2 - Appliance workflow improvements

### Public release and ISO distribution

Create a dedicated public-release ISO repository separate from the customizable `justvoxel-iso` builder.

- keep the existing customizable ISO repository for user-specific builds such as SSH key, timezone, keyboard, and partition choices
- use a separate public ISO repository only for official release media
- build official media from stable/release JustVoxel images, not development/testing channels
- publish two official x86-64 installers:
  - JustVoxel VM for virtual machines and hypervisors
  - JustVoxel HWS for physical hardware
- refresh public installation media approximately monthly
- allow on-demand public ISO builds for important installer fixes, security changes, or significant user-facing functionality
- do not rebuild public ISOs for every routine bootc image rebuild
- use SourceForge as the planned public ISO hosting/mirror target and automate publication from GitHub Actions
- publish checksums and release metadata alongside the ISOs
- provide a small public website with product information, screenshots, clear VM/HWS download choices, documentation, and source links

### USB backup workflow in WebUI

Build on the storage support already available through mJust and WebUI:

- detect supported removable storage
- export/copy backups to USB
- import backups from USB
- verify transfers
- handle mounting and safe unmounting through the appliance workflow

### Diagnostics, log viewer, and privacy-controlled support reporting

Build on the existing Setup Diagnostic Log foundation rather than creating a second logging system.

Local diagnostics should remain Management-Agent owned and usable even when remote reporting is disabled. WebUI and mJust should act only as frontends for viewing, downloading, retention, privacy settings, review, and submission.

Provide four administrator-selectable diagnostic modes:

- **Local only** - default. Collect supported diagnostics locally and never transmit them.
- **Disabled** - do not retain supported diagnostic history.
- **Review before sending** - retain diagnostics locally and require review of the exact sanitized support report before each submission.
- **Automatic reporting** - opt-in only, with explicit confirmation after showing a representative sanitized report.

Add a normal Diagnostics / Logs experience:

- browse retained setup and operation diagnostics
- download human-readable logs
- show operation, component, stage, result, and timestamps
- allow administrator-controlled retention and cleanup
- preserve useful helper/runtime failure output
- keep privacy redaction centralized in the Management Agent

Do not send ordinary raw local logs directly to GitHub. Build a normalized Support Report from relevant local evidence.

A Support Report may include:

- JustVoxel version/build metadata
- booted bootc image reference and immutable digest/ID
- intended JustVoxel variant
- detected runtime environment class and hypervisor family when reliably available
- kernel/base operating-system version
- affected component and operation
- failure stage
- normalized error/signature
- bounded sanitized representative failure output
- first/last relevant timestamps

Diagnostic reporting must not include firmware serial numbers, motherboard UUIDs, MAC addresses, hostnames, IP addresses, or other stable hardware identifiers.

If remote reporting needs installation-level grouping, create a random resettable diagnostics installation identifier only when remote reporting is enabled. It must be independent of hardware identity.

Use a dedicated diagnostics intake service between appliances and GitHub:

- accept only versioned Support Report schemas
- validate and re-sanitize submissions server-side
- rate-limit abuse and malformed clients
- store individual reports outside Git
- fingerprint and aggregate equivalent failures
- track first/last seen, affected image digests/tags, and recurrence across releases
- identify likely regressions without automatically assigning blame

Do not embed GitHub credentials in the appliance. Use a narrowly permissioned GitHub App from the diagnostics service to a dedicated diagnostics/triage repository.

The GitHub-facing layer should create or update aggregated incidents rather than one Issue per appliance report.

Keep AI optional and later. Privacy boundaries, redaction, schema validation, fingerprinting, aggregation, and promotion rules must remain deterministic.

### Notifications

Add simple appliance notifications for useful failures or changes:

- backup failure
- low storage
- repeated Minecraft service failure
- OS update availability
- Minecraft/Paper update availability
- import, export, or restore completion/failure

Initial targets should stay simple, such as Discord and generic webhooks.

## P3 - Later appliance capabilities

### UPS monitoring

Add native UPS monitoring through the JustVoxel WebUI and Management API.

- use NUT as the backend
- feature-gate the UI when UPS capability is unavailable
- keep the first implementation read-only
- validate real USB UPS hardware, reboot recovery, permissions, and network ordering
- use the proven Pasiv Black Box NUT work as implementation/testing reference
- do not require a second Cockpit administration surface

### Safe scheduling improvements

Scheduled backups already exist. Future scheduling should be limited to appliance-defined operations where useful:

- scheduled Minecraft restart
- optional maintenance reboot
- update checks
- defined maintenance windows

### Plugin management

Potentially provide guided Paper plugin install/update/disable/remove workflows with compatibility and backup safety checks.

## P4 - Nice to have

### JustVoxel-aware rollback

Consider an appliance-aware bootc rollback workflow only after its interaction with `/etc`, JustVoxel configuration, Minecraft data, and backup/recovery boundaries is fully defined.

### Vanilla migration

Consider supported migration between Vanilla and Paper/Purpur only after the conversion paths are explicitly designed, backed up, tested, and recoverable.

- do not treat Vanilla migration as a simple server-type toggle
- preserve the current single-server model while evaluating this
- require clear compatibility and rollback behavior before exposing it in mJust or WebUI

### Multiple Minecraft server instances

Very long-term option only after the single-server appliance is mature and stable.

- allow multiple independent Minecraft containers only as a separately designed feature
- give each instance its own configuration, storage, ports, backups, lifecycle, and status
- add host resource budgeting before allowing additional running instances
- account for reserved RAM, configured container memory limits, CPU capacity, and port conflicts
- prevent users from starting more instances than the appliance can safely support
- keep multi-instance management primarily WebUI-focused, with mJust retaining the necessary recovery and administration paths
- consider developing this on a dedicated future branch because it changes core single-server assumptions

## Roadmap rule

Implemented and validated behavior belongs in operational documentation. Future or not-yet-validated behavior belongs here.

When a roadmap item is completed, move its detailed behavior to the appropriate operational document instead of leaving stale duplicate descriptions in this file.
