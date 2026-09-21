# JustVoxel server migration

## Shared Management API status

Server **Export** now has an authoritative Management API/backend foundation. The Agent owns Export discovery, reviewed planning and fingerprints, player interruption requirements, persistent operation tracking, target revalidation, temporary local/NFS/SMB/device transport handling, the cold portable-bundle transaction, SHA-256 verification, Minecraft restart validation, and conservative restart/interruption recovery.

During this 5A.1 stage, `mjust export` remains behavior-compatible through the extracted shared Export backend; it is **not yet the thin API frontend**. Import and migration recovery also remain on their existing direct mjust workflows. Step 5A.2 will move Import/Recovery behind the same migration operation family and then convert all three terminal commands to thin Management API frontends.

JustVoxel migration moves the **complete persistent Minecraft server state** to another JustVoxel installation. It is separate from normal JustVoxel Backup/Restore.

- **Backup / Restore** protects the current appliance and uses its configured backup destination.
- **Export / Import** is for reinstall, VM-to-VM, VM-to-physical-hardware, physical-hardware-to-VM, another machine, or an existing external Paper server.

The normal commands are:

```text
mjust export
mjust import
```

A native JustVoxel export is one portable `justvoxel-migration-*.tar.gz` file. It contains the full persistent Minecraft data tree, a versioned source manifest, and a SHA-256 integrity index. SHA-256 detects corruption; it does **not** prove who created the file or make an untrusted bundle trusted.

## Supported sources in migration v1

Supported:

- JustVoxel native migration bundle
- existing JustVoxel `minecraft-*.tar.gz` backup
- Paper server directory/archive
- `itzg/minecraft-server` using `TYPE=PAPER`
- vanilla server -> Paper, with an explicit conversion warning

Generic import formats:

- directory
- `.tar.gz` / `.tgz`
- normal `.tar`
- `.zip`

ZIP uses Python's built-in ZIP support already present in JustVoxel; no additional archive package is installed. 7z and RAR are not supported.

Not supported automatically in v1:

- Fabric
- Forge
- NeoForge
- modpacks and arbitrary modded launchers
- arbitrary custom Minecraft launchers

If one of those is detected, JustVoxel stops before live data is changed.

## What migration preserves

Migration copies the complete persistent server directory rather than rebuilding a world from selected pieces. That includes, when present:

- Overworld, Nether and End
- player data, inventories and positions
- advancements and statistics
- whitelist, bans and ops
- plugins and plugin configuration
- Geyser/Floodgate configuration
- Floodgate keys
- `server.properties`
- other persistent Minecraft/Paper data

Destination-machine state is deliberately recreated rather than blindly copied. Disk UUIDs, old mount paths, old backup paths, system accounts, SSH configuration, WebUI credentials, host identity, bootc state, firewall machine state and the old RCON password are not imported as destination configuration.

The source Java/Bedrock ports are hints. Destination ports are checked locally. Memory and UID/GID belong to the destination machine. RCON credentials are regenerated.

## Minecraft version safety

Migration is **not** a Minecraft upgrade.

JustVoxel determines the exact source Minecraft version from native metadata or recognizable Paper/itzg server evidence. The first imported start is pinned to that version. It never switches an imported world to `LATEST` automatically.

If the source version cannot be determined, the administrator must provide the exact source version. If JustVoxel cannot confirm a compatible stable Paper build for that exact version, import stops before activation.

After a migration succeeds and gameplay has been checked, a later version change is a separate operation:

```text
mjust update-minecraft
```

Opening a world with a newer Minecraft version can modify it irreversibly, which is why migration and upgrade remain separate.

## Java online-mode / player identity safety

Complete migration v1 requires Java `online-mode=true`.

If the source reports `online-mode=false`, automatic complete migration is refused. Online-mode and offline-mode can assign different UUID identities, which can disconnect the correct player from inventory, position, advancement, whitelist, ops and plugin data.

If source identity mode cannot be determined, JustVoxel requires the administrator to explicitly confirm that the source used `online-mode=true` before activation. If a native manifest and `server.properties` disagree, the import is refused.

Geyser/Floodgate Bedrock identity is handled separately and does not make an offline-mode Java migration safe.

## Imported plugins are executable code

For an external Paper/itzg import, JustVoxel warns when plugin JARs are present. Minecraft plugins are executable server code and will run inside the Minecraft container.

JustVoxel preserves them; it does not silently remove or disable them. Only import plugins from a source you trust.

Native JustVoxel-to-JustVoxel migration does not repeat the external-code warning as an alarm on every move.

## EULA

A source server may contain `eula.txt`. That file is server data, not destination administrator consent.

A **fresh/unconfigured JustVoxel import always requires explicit Minecraft EULA acceptance** before the imported server can be started. JustVoxel never infers destination acceptance from an imported `eula.txt`.

## Transport choices

`mjust import` and `mjust export` can use:

- a local path
- an already-mounted filesystem
- an attached disk/partition
- USB/removable media
- the currently configured JustVoxel backup location
- a one-time NFS share
- a one-time SMB/CIFS share

Temporary NFS/SMB migration mounts do not add `/etc/fstab` entries. Temporary SMB credentials are created only below `/run/justvoxel-migration`, mode `0600`, and are deleted during cleanup.

### Temporary removable-media filesystems

Migration v1 allows temporary mounts of these existing filesystems:

- ext4
- XFS
- Btrfs
- exFAT
- FAT32/vfat

The actual mount is attempted by the running JustVoxel kernel. If a filesystem is recognized on the media but is not mountable by the running image/kernel, migration fails clearly without formatting or modifying the media. This is important for Btrfs/exFAT because kernel capability can vary by appliance/kernel build.

NTFS is intentionally unsupported in migration v1. JustVoxel does not add an NTFS package or use a fallback userspace driver automatically.

Normal migration **never formats removable media**.

For media JustVoxel mounts itself:

- import uses read-only mounting where possible, with `nodev,nosuid,noexec`;
- export uses a writable mount with `nodev,nosuid,noexec`;
- the media is unmounted after completion;
- JustVoxel reports when it is safe to remove a USB device.

If the administrator mounted the filesystem before starting migration, JustVoxel uses that existing mount and does **not** unmount it afterward.

FAT32/vfat has a single-file size limit of 4 GiB minus 1 byte. Export performs a conservative preflight and refuses a migration that cannot safely be guaranteed to fit as one file.

## JustVoxel -> JustVoxel

On the source JustVoxel:

```text
mjust export
```

Choose a destination such as local storage, USB, configured backup storage, temporary NFS or temporary SMB. Export acquires the normal Minecraft maintenance lock, checks players, gracefully stops a running server, captures the full persistent data tree, creates the manifest/integrity index, verifies the completed `.partial` archive, atomically renames it to the final `.tar.gz`, then restarts Minecraft only if export stopped it.

Move the resulting file to the destination JustVoxel.

On the destination:

```text
mjust import
```

Select the bundle. JustVoxel verifies SHA-256 integrity, source compatibility and exact Minecraft version before activation.

## Existing JustVoxel backup

An existing completed JustVoxel backup such as:

```text
minecraft-2026-09-15-043000.tar.gz
```

can also be selected by `mjust import`.

If its `.meta.json` sidecar is present, migration uses useful metadata. Older valid JustVoxel backups without the sidecar remain usable when the Minecraft data itself can be identified safely.

This does not rename Backup/Restore into Migration. Normal `mjust restore` and `mjust restore-full` remain same-appliance recovery operations.

## Existing itzg Docker Paper server

These instructions assume `TYPE=PAPER` and persistent `/data`.

1. Find the container and confirm that the persistent server data is mounted at `/data`:

```text
docker inspect <container-name>
```

Look in `Mounts` for the entry whose destination is `/data`. Identify its bind-mount source or Docker volume.

2. Stop the Minecraft container cleanly:

```text
docker stop <container-name>
```

3. Archive the **complete `/data` contents**, not only the world directory. If `/srv/minecraft/data` is the host bind mount, for example:

```text
tar -C /srv/minecraft/data -czf paper-server.tar.gz .
```

A normal ZIP archive of the same complete server directory is also accepted.

4. Move the archive to JustVoxel by local copy, attached disk, USB, NFS or SMB.

5. Run:

```text
mjust import
```

JustVoxel should identify the server as `itzg/minecraft-server + Paper` when its Paper data markers are present. It does not require a JustVoxel manifest.

Do not restart the original server and continue playing after taking the migration archive unless you intentionally plan to discard those later changes.

## Existing itzg Podman Paper server

The process is the same, using Podman:

```text
podman inspect <container-name>
podman stop <container-name>
```

Find the persistent mount whose container destination is `/data`. Archive the complete host bind-mount directory or volume contents:

```text
tar -C /path/to/persistent/data -czf paper-server.tar.gz .
```

Then transfer the archive and run `mjust import` on JustVoxel.

For a Quadlet deployment, the persistent host path is also normally visible in the Quadlet `Volume=...:/data...` entry.

## Native / normal Paper server

Stop Paper cleanly using the normal console/service method for that server.

Archive the **server working directory** containing `server.properties`, the world directories, `plugins/`, whitelist/ops/bans and Paper configuration. Example:

```text
tar -C /srv/paper -czf paper-server.tar.gz .
```

Move that archive to JustVoxel and select it with `mjust import`.

Do not archive only `world/`; that would not be a complete server migration.

## Crafty Controller

Stop the individual Minecraft server in Crafty before exporting it.

Use the server's normal Crafty export/download when it contains the complete Minecraft server working directory. If Crafty provides a normal `.zip`, JustVoxel can import that ZIP directly; no ZIP -> tar.gz conversion is required.

The selected archive/directory must ultimately contain the server's `server.properties`, world data and other persistent Minecraft files. Do not export Crafty's entire application/database and assume that is a Minecraft server directory.

## PufferPanel

Stop the Minecraft instance in PufferPanel.

Identify that instance's Minecraft server data/working directory—the directory containing `server.properties`, worlds and plugins—and archive or copy that directory. Transfer it to JustVoxel and run `mjust import`.

PufferPanel application configuration itself is not the migration source; the Minecraft instance data is.

## AMP

Stop the Minecraft instance in AMP.

Export/copy the instance's Minecraft working directory containing `server.properties`, worlds and plugins. Transfer that directory/archive to JustVoxel and run `mjust import`.

AMP host identity, users and AMP service configuration are not imported into JustVoxel.

## VM migration examples

Useful transfer choices for a JustVoxel VM include:

- attach a second temporary virtual disk containing the migration archive;
- copy the archive to the VM and choose Local file;
- temporary NFS;
- temporary SMB/CIFS.

A disk used once as a migration source is mounted temporarily; selecting it for migration does not permanently adopt or reconfigure it as JustVoxel storage.

## Physical-hardware migration examples

Useful choices include:

- USB drive/stick;
- attached second disk/partition;
- local copied archive;
- temporary NFS;
- temporary SMB/CIFS.

For USB import, JustVoxel discovers devices, displays block-device details and requires an explicit selection. It does not pick a removable device automatically.

## Temporary NFS

Choose `Temporary NFS share` from Import or Export and enter a source such as:

```text
server:/srv/migration
```

The mount exists only for the operation. Import is mounted read-only; export is mounted writable. JustVoxel verifies the mounted source and unmounts it during cleanup. No permanent `/etc/fstab` entry is created.

## Temporary SMB/CIFS

Choose `Temporary SMB/CIFS share` and enter a source such as:

```text
//server/migration
```

Enter the temporary username/password when prompted. The password is not placed in `/etc/fstab` or on the command line. The temporary credential file lives below `/run/justvoxel-migration` with root-only permissions and is removed during success, cancellation or failure cleanup.

This is a useful migration path between an existing Linux/Windows/NAS host and a disposable JustVoxel VM.

## Import into a fresh JustVoxel appliance

A fresh appliance menu includes `Import existing Minecraft server`.

Import asks only for destination-specific state that cannot safely come from the source, such as:

- destination data storage;
- destination memory;
- Java/Bedrock host ports;
- backup destination and schedule;
- destination UID/GID resolution;
- explicit EULA acceptance.

The imported source Minecraft version is pinned for the first start. Old disk UUIDs, paths, RCON secrets and host state are not adopted.

## Replacing an existing JustVoxel server

On an already configured appliance, `mjust import` means **replace the current persistent Minecraft server state**.

JustVoxel stages and validates the candidate before asking for the final exact confirmation:

```text
Type IMPORT to continue:
```

The existing destination server is protected until the imported server passes runtime and full JustVoxel validation.

## Transaction and automatic rollback

Import never extracts directly over the live Minecraft data directory.

The sequence is:

1. identify and validate source;
2. check archive/directory safety and available space;
3. copy/extract to staging;
4. identify server type, online-mode and exact Minecraft version;
5. determine destination-specific configuration;
6. acquire the shared Minecraft maintenance lock;
7. perform a final player check;
8. gracefully stop the current Minecraft server if required;
9. preserve the old live data and destination runtime/firewall state;
10. activate the staged server by same-filesystem rename;
11. apply destination ownership and SELinux `container_file_t` labels;
12. render destination runtime with a new RCON secret;
13. start Minecraft;
14. validate RCON, Paper/version, plugins and Geyser/Floodgate when enabled;
15. run full `mjust validate`;
16. remove transaction/safety data only after success.

If imported runtime validation fails on an existing JustVoxel server, JustVoxel attempts to restore the old data, configuration, rendered environment, Quadlet/runtime files, backup unit/timer state and the Java/Bedrock firewall state that existed before import.

A successful rollback reports:

```text
Import failed.
Original server restored and validated.
```

If rollback itself cannot be validated, JustVoxel reports a **CRITICAL** state and retains all recovery evidence under the `.justvoxel-import-*` transaction directory. Do not delete that recovery directory until the appliance is recovered.

For a fresh import failure, generated Minecraft runtime/firewall state is removed and the appliance returns to its previous unconfigured runtime state. Storage that the administrator explicitly provisioned is not reformatted or destroyed merely because the Minecraft import failed.

## After migration

After a successful import:

```text
mjust status
mjust validate
mjust backup
```

Also test the real clients you use, including Java and Bedrock when enabled, and reboot once to confirm normal persistence before decommissioning the source server.
