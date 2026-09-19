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
- First-run setup
  - Minecraft data-storage selection
  - backup-destination selection
  - local, NFS, and SMB storage choices
- transactional setup/apply path
  - including execution-time SMB credential handling

### Cross-interface validation

Verify that CLI and WebUI remain consistent:

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

### Local documentation - CLI

- use Glow to render the bundled Markdown documentation
- provide an easy `mjust docs` entry point
- keep the bundled Markdown files as the authoritative documentation source

### Local documentation - WebUI

- use Material for MkDocs to present the same bundled documentation as a local readable site
- expose it through Help / Documentation in the JustVoxel WebUI
- keep documentation matched to the installed bootc generation and available offline

## P2 - Appliance workflow improvements

### Player-aware maintenance

Improve the existing player-aware interruption flow:

- skip unnecessary delay when Minecraft is already stopped
- avoid long warnings when no players are online
- provide useful in-game countdown notices when players are online
- avoid duplicated waiting between JustVoxel and the Minecraft container shutdown path

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

## Roadmap rule

Implemented and validated behavior belongs in the operational documentation. Future or not-yet-validated behavior belongs here.

When a roadmap item is completed, move its detailed behavior to the appropriate operational document instead of leaving stale duplicate descriptions in this file.
