# Start over / reset Minecraft

`mjust start-over` returns an installed JustVoxel Minecraft configuration to first-setup state without deleting Minecraft world data or existing backup archives.

The interactive `mjust` menu exposes this under **Advanced → Start over / reset Minecraft**.

Before changing anything, the flow can save a root-only configuration archive under `/var/lib/justvoxel/config-backups/`. The archive contains the active JustVoxel configuration and generated Minecraft/backup unit files. Minecraft world data is not copied into this configuration archive.

If the Minecraft container exists, the administrator chooses whether to remove it or preserve it as a stopped container with a timestamped `minecraft-preserved-*` name. Podman images are not pruned.

If Minecraft is running, Start over uses the normal player-aware shutdown path. Unknown player state fails closed; online players require explicit administrator approval before interruption.

The operation then requires the exact phrase `START OVER`.

Start over removes the active JustVoxel configuration, generated Minecraft Quadlet, generated backup service/timer, JustVoxel-managed Java/Bedrock firewall openings, and local JustVoxel secrets, except the root-private SMB credentials required by the current preserved backup mount. That mount and its existing credentials survive together; unsafe or inconsistent credential configuration blocks Start over. It does not delete the Minecraft data directory, backup archives, storage partitions, filesystems, `/etc/fstab` storage mounts, network-share contents, Podman images, unrelated containers, or bootc deployments.

After completion, running `mjust` returns to the first-setup interface.
