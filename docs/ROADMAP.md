# JustVoxel roadmap

This roadmap is priority-based rather than date-based. Work moves forward when the current layer is tested and stable enough to support the next one.

JustVoxel remains a dedicated Minecraft server appliance. Normal operation should use guided, validated workflows rather than requiring Linux administration knowledge.

## P0 - Current testing and stabilization

The immediate priority is to validate the storage, backup, migration, and recent WebUI work already implemented before adding more appliance surface.

### CLI / mjust storage

Test and validate:

- storage discovery and configuration
- removable and USB storage
- backup storage
- import and export
- migration and recovery paths
- destructive-storage safety
- NFS and SMB failure handling
- reboot persistence and recovery behavior

### WebUI storage

Recent WebUI storage work is implemented and now needs real-system validation:

- Backup Storage page
  - system storage
  - existing local filesystems
  - NFS
  - SMB/CIFS
- Advanced Storage
  - provision a dedicated disk or USB device
  - format a blank partition
  - create a partition from existing unallocated space
  - destructive review, confirmation, and re-validation
- Minecraft data storage migration
  - Agent-discovered local migration targets
  - authoritative review, destructive/player confirmations, and plan re-validation
  - persistent migration progress, reconnect, rollback, and needs-attention recovery state
- Server Migration
  - Administrator-only Export, Import, and Recovery
  - local/configured-backup/device/USB/NFS/SMB transport choices through Agent planning
  - fresh-destination storage, ports, memory, timezone, backup schedule and retention review
  - persistent progress/reconnect across Export, Import, and Recovery
  - rollback, needs-attention handoff, and fresh-unconfigured Recovery
  - no browser-side migration/storage/Minecraft execution engine and no browser file upload
- First-run setup
  - Minecraft data-storage selection
  - backup-destination selection
  - local, NFS, and SMB storage choices
- transactional setup/apply path
  - including execution-time SMB credential handling

### Unified Management API and mJust rebuild

Rebuild mJust around the same authoritative management layer already used by the WebUI.

- keep the JustVoxel Management Agent / Management API as the single appliance-management engine
- treat WebUI and mJust as two frontends over that same engine
- keep the new mJust intentionally thin: terminal navigation, user input, API calls, and presentation of results
- do not recreate validation, safety policy, storage logic, Minecraft policy, or other business logic independently in mJust
- use local console or SSH authentication plus `sudo` as the CLI administrative trust boundary
- allow an explicitly authorized local UID 0 client to act as the Management API administrator without creating a second CLI login/session system
- keep the existing WebUI Bearer-session and Administrator / Operator / Viewer authorization model unchanged
- do not grant the ordinary `voxel` account unrestricted direct access to the privileged management socket
- migrate capabilities in small, independently verified steps instead of a single large rewrite
- develop and validate the unified Management API / mJust architecture on `testing` before stable promotion
- do not delete helpers merely because they currently live under `mjust/libexec`; helpers used by the Management Agent remain shared implementation until they are safely moved or rewritten
- progressively move shared backend helpers out of the misleading mJust namespace after direct CLI callers have been removed

The server-migration terminal rebuild and WebUI parity are complete: Export, Import, and Recovery share one persistent Management API operation family, and both mJust and the Administrator WebUI are thin frontends over the same authoritative backend. Remaining P0 work for migration is real-system validation of transports, interruption/reconnect behavior, rollback, needs-attention Recovery, and fresh-unconfigured Recovery rather than another migration implementation.

Management API gaps that still need shared implementations before the corresponding direct CLI paths can disappear include:

- Minecraft update policy and execution
- bootc status and operating-system updates
- Start Over / reset workflows
- WebUI lifecycle and recovery behavior

Administrator validation is now unified: both mJust and WebUI consume the same Administrator Validation endpoint in the Management Agent, which runs the shared authoritative validation backend.

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

These are intentionally early because they are relatively contained improvements once P0 testing is complete.

### System Resources - Glances

- package Glances through `home-server-packages`
- consume it from JustVoxel Base so VM and HWS inherit the same feature
- use the existing Glances WebUI initially
- ship a JustVoxel-owned default configuration
- keep system-level configuration image-controlled rather than user-managed
- expose only the supported monitoring feature set
- keep Podman/container access behind the security review already in progress
- consider a custom JustVoxel frontend later only if the upstream WebUI becomes limiting

### Superfile - implemented

- shipped Superfile as the friendly CLI file manager
- consume the verified `home-server-packages` RPM artifact in JustVoxel Base
- expose it as `mjust files` and System -> File browser
- keep it as a user convenience tool rather than a privileged WebUI file browser

### Micro - implemented

- shipped Micro from the native AlmaLinux package in JustVoxel Base
- preserve Nano and existing administrator/editor tools
- update Micro with the JustVoxel image rather than through an independent user-managed lifecycle

### Documentation source and local documentation

Create a dedicated documentation repository as the authoritative user-documentation source.

- use `main` for approved, published documentation
- use `draft` for active documentation work and incomplete updates
- promote documentation from `draft` to `main` through a clean reviewed PR
- keep retired or historical material in an excluded directory such as `archive/` or `retired/` rather than adding another permanent publication branch
- have stable/public JustVoxel builds consume only approved documentation from `main`
- resolve and record a specific documentation commit for each image build so the installed documentation is reproducible
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
- expose the same capability rules through both mjust and WebUI so the two interfaces do not drift
- do not support in-place migration to or from Vanilla in the initial implementation

## P2 - Appliance workflow improvements

### Public release and ISO distribution

Create a dedicated public-release ISO repository separate from the customizable `justvoxel-iso` builder.

- keep the existing customizable ISO repository for user-specific builds such as SSH-key, timezone, keyboard, and partition choices
- use a separate public ISO repository only for official release media
- build official media from stable/release JustVoxel images, not development/testing channels
- publish two official x86-64 installers:
  - JustVoxel VM for virtual machines and hypervisors
  - JustVoxel HWS for physical hardware
- refresh public installation media approximately monthly so the installer does not become unnecessarily stale as the underlying AlmaLinux/base image evolves
- allow an additional on-demand public ISO build for important installer fixes, security changes, or significant user-facing functionality
- do not rebuild public ISOs for every routine bootc image rebuild
- use SourceForge as the planned public ISO hosting/mirror target and automate publication from GitHub Actions
- make the current VM/HWS pair the primary public downloads
- optionally retain one previous VM/HWS pair when storage availability makes it useful, without cluttering the main download experience
- publish checksums and release metadata alongside the ISOs
- provide a small public website, likely through GitHub Pages, with product information, screenshots, clear VM/HWS download choices, documentation, and source links

### USB backup workflow in WebUI

Build on the storage support already available through `mjust` and the current WebUI storage work:

- detect supported removable storage
- export/copy backups to USB
- import backups from USB
- verify transfers
- handle mounting and safe unmounting through the appliance workflow

### Diagnostics, log viewer, and privacy-controlled support reporting

Build on the Setup Diagnostic Log foundation rather than creating a second logging system.

Local diagnostics should remain Management-Agent owned and usable even when remote reporting is disabled. The WebUI and mJust should act only as frontends for viewing, downloading, retention, privacy settings, review, and submission.

Provide four administrator-selectable diagnostic modes:

- **Local only** - default. Collect supported diagnostics locally and never transmit them.
- **Disabled** - do not retain supported diagnostic history. Clearly warn that future troubleshooting may require the user to reproduce the problem and collect information manually.
- **Review before sending** - retain diagnostics locally and require the administrator to review the exact sanitized support report before each submission.
- **Automatic reporting** - opt-in only. Before enabling it, show the administrator a representative sanitized report and require explicit confirmation. Allow switching back to any other mode at any time.

Expose the same policy through WebUI and mJust, with the Management Agent as the single authority.

Add a normal Diagnostics / Logs experience later:

- browse retained Setup and operation diagnostics;
- download human-readable logs;
- show operation, component, stage, result, and timestamps;
- allow administrator-controlled retention/cleanup;
- preserve useful helper/runtime failure output;
- keep privacy redaction centralized in the Management Agent.

Do not send ordinary raw local logs directly to GitHub. Build a normalized **Support Report** from relevant local evidence.

A remote Support Report should contain only information useful for reproducibility and regression analysis, such as:

- JustVoxel version/build metadata;
- booted bootc image reference including release/development channel or tag;
- immutable booted image digest/ID;
- intended JustVoxel image variant such as VM or HWS when that identity is available;
- detected runtime environment class: physical hardware, virtual machine, container/nested environment, or unknown;
- hypervisor/virtualization family when it can be determined reliably;
- kernel/base operating-system version;
- affected component and operation;
- failure stage;
- normalized error/signature;
- bounded sanitized representative failure output;
- first/last event timestamps relevant to the report.

Runtime classification should be derived from normal operating-system/firmware virtualization signals rather than permanent hardware identity. It may use facilities such as virtualization detection and DMI/firmware hints, but diagnostic reporting must not include firmware serial numbers, motherboard UUIDs, MAC addresses, hostnames, IP addresses, or other stable hardware identifiers.

Compare the **declared image variant** with the **detected runtime class**. A report should be able to identify cases such as a VM-oriented image running directly on physical hardware or an HWS image running in a VM. Treat that as a diagnostic mismatch/advisory, not an automatic root-cause conclusion.

If remote reporting needs to distinguish many reports from one installation from the same failure across many installations, generate a random diagnostics installation identifier only when remote reporting is enabled. It must be independent of hardware identifiers, replaceable/resettable by the administrator, and used only for diagnostics aggregation.

Use a dedicated diagnostics intake service between appliances and GitHub:

- accept only versioned Support Report schemas;
- validate and re-sanitize every submission server-side;
- rate-limit abuse and malformed clients;
- store individual reports outside Git;
- fingerprint and aggregate equivalent failures;
- count distinct reporting installations without exposing real user identity;
- track first seen, last seen, affected image digests/tags, and recurrence across releases;
- identify likely regressions such as a failure signature first appearing after a specific image/base transition;
- avoid automatically blaming AlmaLinux, Podman, Minecraft, or JustVoxel until the evidence supports that conclusion.

Do not embed GitHub credentials in the appliance.

Use a narrowly-permissioned GitHub App as the bridge from the diagnostics service to GitHub. Prefer a separate diagnostics/triage repository rather than flooding the main product repository with raw reports.

The GitHub-facing layer should create or update **aggregated incidents**, not one Issue per appliance report. One incident may represent many equivalent reports and should summarize information such as:

- normalized failure signature;
- affected component/stage;
- number of reports;
- number of distinct reporting installations;
- first/last seen;
- affected image references/digests;
- image/runtime-class mismatches;
- representative sanitized error;
- whether the signature persists across multiple consecutive image builds.

Define promotion rules so a single report can remain only in the collector while repeated or severe failures become actionable GitHub incidents.

Keep AI optional and later. Initial redaction, schema validation, fingerprinting, aggregation, regression detection, counting, and GitHub issue maintenance should be deterministic. AI may later help summarize related incidents or suggest common root-cause areas, but it must not decide privacy boundaries or whether raw private data is safe to transmit.

### Notifications

Add simple appliance notifications for useful failures or changes, such as:

- backup failure
- low storage
- repeated Minecraft service failure
- OS update availability
- Minecraft/Paper update availability
- import, export, or restore completion/failure

Initial targets should stay simple, such as Discord and generic webhooks.

## P3 - Later appliance capabilities

### UPS monitoring

Add native UPS monitoring through the existing JustVoxel WebUI and Management API.

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

### Future System Resources presentation

If the Glances WebUI eventually proves limiting, a later JustVoxel-native System Resources page may consume the Glances API while keeping Glances as the metrics backend.

### Vanilla migration

Consider supported migration between Vanilla and Paper/Purpur only after the conversion paths are explicitly designed, backed up, tested, and recoverable.

- do not treat Vanilla migration as a simple server-type toggle
- preserve the current single-server model while evaluating this
- require clear compatibility and rollback behavior before exposing it in mjust or WebUI

### Multiple Minecraft server instances

Very long-term option only after the single-server appliance is mature and stable.

- allow multiple independent Minecraft containers only as a separately designed feature
- give each instance its own configuration, storage, ports, backups, lifecycle, and status
- add host resource budgeting before allowing additional running instances
- account for reserved RAM, configured container memory limits, CPU capacity, and port conflicts
- prevent users from starting more instances than the appliance can safely support
- keep multi-instance management primarily WebUI-focused, with mjust retaining the necessary recovery and administration paths
- consider developing this on a dedicated future branch because it changes core single-server assumptions

## Roadmap rule

Implemented and validated behavior belongs in the operational documentation. Future or not-yet-validated behavior belongs here.

When a roadmap item is completed, move its detailed behavior to the appropriate operational document instead of leaving stale duplicate descriptions in this file.
