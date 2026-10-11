# JustVoxel storage model

JustVoxel keeps Minecraft data and backups outside the immutable bootc deployment. The administrator chooses the active paths; the image ships only the management logic and templates.

## Storage goals

Minecraft data and backup storage are independent. A typical physical appliance may use a SATA system disk, NVMe for Minecraft data, and a USB disk or NAS share for backups. A typical VM uses its primary virtual disk for the OS and Minecraft data and a second virtual disk for backups.

Podman's global image store is not relocated by mjust.

## Read-only storage overview

`mjust storage-plan` is a thin Management API frontend. Device discovery, system-disk identification, filesystem metadata, mountpoints, model/transport information, and read-only/system flags come from `GET /v1/admin/storage`; the terminal only formats that authoritative discovery for a human-readable overview. It does not run its own `lsblk` or system-disk discovery path.

## VM design

The recommended VM layout is:

- primary virtual disk: JustVoxel OS and Minecraft data
- second virtual disk: backup storage

If JustVoxel VM sees no safe secondary disk, the storage wizard tells the administrator to attach another virtual disk in the hypervisor. NFS and SMB/CIFS are also normal supported backup targets. Both client stacks are installed in the VM image.

A virtual disk is treated exactly like local block storage. The hypervisor can snapshot, replicate, or back up that virtual disk using its own policy independently of JustVoxel.

## Physical-hardware design

Physical-hardware deployments support:

- dedicated internal disk
- external USB disk or USB stick
- existing local partition/filesystem
- a new partition created only from already-unallocated disk space
- NFS share
- SMB/CIFS share
- a directory on the system filesystem

An external USB device is not a special storage backend. It is discovered as a normal block disk, identified by model/size/transport, and can be provisioned as a dedicated backup disk.

## Whole-disk provisioning

`mjust storage-disk` can initialize a dedicated disk with a GPT partition table, one XFS filesystem, and a persistent UUID mount.

Before the operation, JustVoxel identifies the disks that back `/`, `/boot`, `/boot/efi`, and `/var`. Those system disks are excluded from whole-disk erase candidates. Any disk with mounted child filesystems is also excluded.

The administrator sees the exact device, size, model, transport, filesystems, and mount state and must type an exact phrase such as `ERASE /dev/sdb`. A simple yes/no confirmation is not accepted for destructive disk erase.

## Existing partitions

`mjust storage-partition` can adopt an existing XFS, ext4, or Btrfs partition. If the partition is already mounted at a non-critical mount point, mjust records and validates it without rewriting the administrator's existing mount configuration. If it is unmounted, mjust asks for a mount point and creates a persistent UUID mount.

If an existing partition has no filesystem, mjust can format only that partition as XFS after a separate exact `FORMAT /dev/...` confirmation. It never silently reformats a filesystem it recognizes.

## Unallocated space

`mjust storage-free-space` can create a new XFS partition inside the largest already-unallocated segment on a disk. This is the supported way to use spare space left beside an existing OS installation.

JustVoxel does not shrink or resize an existing filesystem. If the disk has no usable unallocated segment, mjust refuses the operation instead of attempting partition surgery.

The system disk is allowed for this operation because only already-free space is used, but the exact disk and start/end range are displayed and the administrator must type `CREATE PARTITION /dev/...`.

## Network backups

NFS and SMB/CIFS are supported for both VM and physical-hardware backup targets.

Network mounts created by mjust are written persistently to `/etc/fstab` with `_netdev` and `nofail`. If the requested NFS/SMB source is already mounted at the selected path, mjust adopts that mount without rewriting its existing mount configuration. A network outage therefore does not block the appliance from booting. The backup job still fails closed: if the expected share is not mounted or the reported source does not match the stored source, Minecraft is not stopped and no archive is written to the local root filesystem by mistake.

Start over and Minecraft Reset preserve the current managed SMB backup mount together with its existing root-owned, mode `0600` credential file. They never recreate missing credentials. Factory Reset removes managed mount configuration and credentials according to its existing policy; it does not use this preservation mode.

First-run setup retires obsolete JustVoxel-managed persistent mounts not selected by the reviewed storage plan. Changing the backup destination also retires a superseded managed backup mount unless Minecraft data still uses it. Retirement never deletes old filesystem/share data or formats storage, and leaves unrelated administrator fstab entries alone. Active mounts must match their persisted identity before unmounting. The existing storage transaction snapshots the complete fstab and any credentials it changes, and restores previously active mounts during rollback. Only the retired mount unit's failed state is cleared; ordinary failed-mount validation remains an error. Obsolete credentials are removed only when the new destination is verified and no final fstab entry still requires them.

On Management Agent startup, configured appliances also reconcile obsolete persistent entries at the historical reserved mountpoints `/var/mnt/justvoxel-data` and `/var/mnt/justvoxel-backup`. Current storage selections and paths are preserved. Safely identified obsolete mounts are retired without deleting data; active local mounts must match both UUID and source, and active network mounts must match their source. Ambiguous state is preserved and logged without preventing the Management API from starting. Normal validation continues to report unresolved failures. Unused SMB credentials are removed only if the existing credential safety checks pass and neither fstab nor the current backup selection requires them.

SMB credentials are stored only in `/etc/justvoxel/smb-backup.credentials` with root-only permissions and are referenced by the fstab entry rather than placed directly in `/etc/fstab`.

If the saved SMB destination is unavailable and credentials need to be re-entered, open **Backups → Change destination**, keep the saved source, mount point and backup directory, and enter the username and optional domain. Choose **Review destination**, then enter the password during **Apply destination**. Review remains actionable even when the location matches the saved configuration. Apply verifies the mounted source before saving the configuration; passwords are never prefilled or saved in the review page.

Network shares can use server-side ownership rules or NFS root-squash. The backup helper therefore verifies writability rather than requiring local `chown` semantics on NFS/SMB targets.

## Persistent mount identity

Local filesystems are mounted by filesystem UUID, never by a transient name such as `/dev/sdb1`. This matters especially for USB storage, where Linux device names can change across boots.

The runtime records the expected UUID/source and validates it before Minecraft or backup work proceeds.

## Minecraft data migration

`mjust storage-migrate` and **WebUI -> Storage & Backups -> Minecraft data storage** are thin Administrator frontends over the shared Minecraft data migration Management API. Both use Agent-discovered safe targets, display the authoritative plan and warnings, collect the exact destructive phrase when storage preparation requires one, confirm player interruption when required, and monitor/reconnect to the same persistent migration operation.

The Management Agent owns the migration transaction:

1. revalidate the exact reviewed target, configuration identity, capacity, and player state
2. prepare or adopt the reviewed local destination
3. refuse a non-empty destination data directory
4. hold the shared Minecraft maintenance lock
5. recheck players immediately before interruption
6. create a verified pre-migration cold backup and leave Minecraft stopped
7. copy the complete persistent data tree with rsync
8. run a dry-run rsync verification before switching configuration
9. update the administrator-owned data path and persistent mount identity
10. restore SELinux labels and regenerate runtime configuration
11. start and validate Minecraft when it was running before migration
12. roll back to the old data-path configuration/runtime if migrated runtime validation fails

The operation is journaled persistently. Re-running `mjust storage-migrate` or reopening the WebUI migration entry point reconnects to an active operation instead of starting a second migration. If the Agent or appliance is interrupted before a safe terminal result can be proven, the operation becomes `needs_attention` and preserves migration recovery state for administrator review.

The old Minecraft data directory is never deleted automatically. After a successful migration the administrator removes it only after verifying normal gameplay and backups. Storage preparation already completed on a reviewed target may remain after a safe rollback; the original Minecraft configuration/runtime remains authoritative.

## Backup retention and failure behavior

Manual backups, timer backups, pre-update backups, and pre-migration backups use the same verified cold-backup helper and the same retention count. If retention is seven, publishing the eighth verified archive removes the oldest archive.

A backup on another partition of the same physical disk can protect against an OS reinstall, but not against failure of that physical disk. A separate internal disk, USB device, or NAS share provides a stronger failure boundary.

## Local block storage in the WebUI

The Storage workspace protects the exact JustVoxel system partitions. Additional
non-system partitions and unallocated space on the system disk remain manageable,
as do internal, virtual, and USB disks. Network storage is unchanged.

Existing XFS, ext4, Btrfs, NTFS, VFAT/FAT32, and exFAT filesystems can be mounted
with the existing filesystem-specific mount stack. Non-system partitions can be
explicitly reformatted to XFS or deleted after destructive review and confirmation.
Deletion removes only the selected partition, leaving its space unallocated and
neighboring partitions unchanged. Read-only and unavailable targets remain blocked.

For a new partition, enter a whole-number size in GiB (for example, 200), or choose
**Use all available space**. Any remainder stays unallocated for later partitions.
JustVoxel creates only XFS filesystems.

A supported filesystem directly on a whole non-system disk can be mounted for now.
To repurpose a disk without partitions, choose **Reinitialize disk as GPT**. This
separately reviewed action erases the entire disk, creates a GPT table, and leaves
space available for partitions of your chosen sizes. The system disk cannot be
reinitialized. Whole-disk Minecraft and backup preparation remains a convenience.

Formatting, deletion, and reinitialization warn when Minecraft or Backups use the
target: that data is destroyed and its configured location becomes unavailable
until replaced. Destructive confirmation also acknowledges player interruption
for Minecraft storage; the existing player-aware graceful stop runs before erasure
and Minecraft remains stopped. Saved mount settings may also need replacement.

### First-run runtime recovery

Failed first-run setup must retain a user-accessible recovery path whenever safe
recovery can be proven. This is a product invariant across transaction formats.

New setup transactions retain the non-secret runtime request in the root-owned,
private storage transaction manifest before runtime changes begin. It includes the
operation identity, reviewed plan fingerprint, ports, data and backup paths, and
mount expectations. Recovery validates those values against the original storage
transaction; it never reconstructs missing evidence from current configuration.

For a setup stopped at runtime or storage rollback, **Retry recovery** requires
confirmation that Minecraft data generated by the failed installation may be
removed. Runtime recovery runs first and must be verified before storage recovery
continues. Generated data is eligible for removal only when the recorded empty
pre-runtime directory still has the same canonical path, directory and filesystem
identity, with no nested mounts or overlapping backup path. Existing data and
unrelated storage remain protected.

After both rollback stages are verified, the operation becomes `rolled_back`, the
setup lock is released, and **Start setup over** is available. Failure or restart
during recovery retains the transaction and setup lock, reports the reason, and
allows retry at the preserved recovery stage.

For transactions from older images that have no `runtime_request`, **Retry recovery**
must support bounded compatibility verification when sufficient preserved
transaction evidence and current appliance state positively prove safe recovery.
This is an implementation requirement, not a claim of verified legacy runtime
behavior. Evidence may include the private operation identity, plan fingerprint,
runtime initial-state evidence, transaction-owned mount identities, created
directories, optional recorded data-directory identity, and fstab snapshot/hash
evidence. Historical manifests need neither `runtime_request` nor
`storage_request`. Verification of an already-clean runtime must establish that
managed runtime files and the setup marker are absent, Minecraft and backup units are inactive with no remaining unit
processes, the Minecraft container is absent, the maintenance lock is available,
and storage identity and mount safety still match the transaction.

Compatibility recovery must never fabricate missing evidence, reconstruct runtime
settings from live configuration, or bypass storage/runtime identity or ownership
checks. Unverifiable recovery must retain the transaction and lock and report the
concrete failure. `needs_attention` must not become a permanent dead end merely
because internal transaction formats changed when safe recovery can be proven.

The protected product workflow remains **Retry recovery -> verified rollback ->
`rolled_back` -> setup lock releases -> Start setup over**. Factory Reset and
Minecraft Reset remain separate operations, never substitutes for Setup Recovery.

Compatibility recovery must preserve remaining data and unrelated firewall rules.
Historical rollback attempted firewall cleanup using the original in-memory ports;
preserved boolean firewall evidence does not identify those ports. Compatibility
recovery must therefore preserve firewall rules, including any residual setup
rules, rather than risk deleting unrelated rules. New transactions continue using
their durable `runtime_request` for guarded runtime cleanup.

## Generic permanent-mount ownership lifecycle

Generic permanent mounts use a filesystem UUID row in `/etc/fstab` and a matching
v1 ownership record under `/etc/justvoxel/storage-mounts`. Format, Delete partition,
and Reinitialize disk retire only verified generic ownership for the exact device
before destroying its filesystem identity. Reinitialize disk continues to require
an unpartitioned, non-system disk. Full Factory Reset validates and retires these
records before removing JustVoxel configuration; it preserves external/network
data and partition layouts. Administrator mount entries are not adopted by this
cleanup. Ambiguous ownership or a mounted identity mismatch prevents retirement.

An explicit permanent-mount request can also recover the historical orphan left
by filesystem recreation: one exact historical JustVoxel native-filesystem row at
the requested, unused non-critical location, whose old UUID no longer exists,
with no conflicting ownership or setup/data/backup selection evidence. This is a
bounded compatibility check during the normal operation, not boot-time cleanup
or a general classification of Administrator entries as JustVoxel-owned. Apply
revalidates the evidence, snapshots persisted state, replaces only that row,
creates normal ownership, reloads systemd, mounts, and verifies the current UUID.
Failure restores prior persisted state; rollback failures retain their snapshots
and report failure.
