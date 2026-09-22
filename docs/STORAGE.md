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

SMB credentials are stored only in `/etc/justvoxel/smb-backup.credentials` with root-only permissions and are referenced by the fstab entry rather than placed directly in `/etc/fstab`.

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
