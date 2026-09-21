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
- keep the normal `testing` branch as a read-only reference for the legacy mJust behavior while the new architecture is developed on `mjust-testing`
- do not delete helpers merely because they currently live under `mjust/libexec`; helpers used by the Management Agent remain shared implementation until they are safely moved or rewritten
- progressively move shared backend helpers out of the misleading mJust namespace after direct CLI callers have been removed

Management API gaps that still need shared implementations before the corresponding direct CLI paths can disappear include:

- import and migration recovery; Export now has the shared persistent Management API/backend foundation, while terminal frontend conversion remains part of the same migration rebuild
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
- consume it from JustVoxel Base so VM and HWE inherit the same feature
- use the existing Glances WebUI initially
- ship a JustVoxel-owned default configuration
- keep system-level configuration image-controlled rather than user-managed
- expose only the supported monitoring feature set
- keep Podman/container access behind the security review already in progress
- consider a custom JustVoxel frontend later only if the upstream WebUI becomes limiting

### Superfile

- ship Superfile as the friendly CLI file manager
- consume the existing verified `home-server-packages` package
- make it easy to launch from `mjust`
- keep it as a user convenience tool rather than a privileged WebUI file browser

### Micro

- ship Micro as the recommended friendly terminal text editor
- preserve existing administrator/editor tools
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
  - JustVoxel HWE for physical hardware
- refresh public installation media approximately monthly so the installer does not become unnecessarily stale as the underlying AlmaLinux/base image evolves
- allow an additional on-demand public ISO build for important installer fixes, security changes, or significant user-facing functionality
- do not rebuild public ISOs for every routine bootc image rebuild
- use SourceForge as the planned public ISO hosting/mirror target and automate publication from GitHub Actions
- make the current VM/HWE pair the primary public downloads
- optionally retain one previous VM/HWE pair when storage availability makes it useful, without cluttering the main download experience
- publish checksums and release metadata alongside the ISOs
- provide a small public website, likely through GitHub Pages, with product information, screenshots, clear VM/HWE download choices, documentation, and source links

### USB backup workflow in WebUI

Build on the storage support already available through `mjust` and the current WebUI storage work:

- detect supported removable storage
- export/copy backups to USB
- import backups from USB
- verify transfers
- handle mounting and safe unmounting through the appliance workflow

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
