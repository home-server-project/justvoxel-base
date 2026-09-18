# JustVoxel architecture

JustVoxel is a purpose-built immutable server appliance for deploying and operating a Minecraft server workload without requiring the user to assemble and maintain the underlying Linux stack by hand.

The project deliberately separates the appliance operating system from the Minecraft workload itself.

JustVoxel does **not** ship Minecraft server binaries, Mojang server software, a pre-created world, or pre-accepted Minecraft EULA state inside the bootc image. The image provides the operating system, management logic, runtime templates, safety mechanisms, and container-management environment. After setup, the administrator accepts the EULA and JustVoxel creates the active runtime configuration for a separate containerized Paper-based server workload.

## Why an appliance instead of a setup script

A conventional setup script or package can install software onto an existing Linux distribution, but the result still depends heavily on the host that was already there: distribution version, package state, firewall configuration, SELinux state, container runtime, system services, storage layout, and previous manual changes.

JustVoxel takes a different approach.

The operating system and the JustVoxel management layer are built, tested, versioned, and updated together. That gives the project a known starting point and allows the normal user experience to focus on appliance tasks such as first setup, Minecraft configuration, backups, storage, updates, and recovery instead of requiring the user to assemble a server from individual Linux components.

This does not make JustVoxel a closed platform. Advanced administrators still have normal access to the underlying Home Server Base 10 / AlmaLinux 10 host and its standard administration tools.

## Why Home Server Base 10 and AlmaLinux 10

JustVoxel consumes Home Server Base 10 as its direct operating-system parent. Home Server Base 10 provides the Home Server Project's shared AlmaLinux 10 Minimal Plus bootc foundation, while AlmaLinux 10 remains the upstream Enterprise Linux source for the kernel and core packages.

The project needs a conservative server-oriented base with systemd, NetworkManager, firewalld, SELinux, Podman, and the normal Linux administration model expected on a long-lived home server. Home Server Base 10 centralizes that generic EL10 foundation so JustVoxel can focus on the Minecraft-appliance layer instead of maintaining its own duplicate rootfs composition.

JustVoxel Base adds the complete shared appliance layer and remains VM-ready. Final VM release promotion and the HWE physical-hardware delta are owned outside this Base repository.

For exact image composition and build details, see [BUILD.md](BUILD.md).

## Why bootc and an immutable system image

JustVoxel uses bootc so the operating system is delivered as a versioned container image.

The important user-facing result is predictability: the operating-system content is produced together rather than being gradually changed through an arbitrary history of package installs and removals on each individual machine.

A normal JustVoxel OS update stages a new bootc deployment. The currently running system continues operating until the next reboot. The previous deployment can remain available at the bootc level, while JustVoxel keeps appliance configuration and persistent data outside the immutable system content where appropriate.

The supported host-management model is bootc. JustVoxel does not use host-side rpm-ostree package layering as its normal administration model.

## Immutable system, persistent configuration, persistent data

JustVoxel separates three kinds of state.

The immutable image owns implementation and templates under locations such as:

- `/usr/share/justvoxel/`
- `/usr/libexec/justvoxel/`
- `/usr/lib/justvoxel/`

Administrator-owned active configuration lives under `/etc`, including JustVoxel configuration and generated runtime/service files.

Persistent application and management state lives under `/var`, including container state and Minecraft persistent data when that data is stored on the system filesystem.

A bootc image update can replace the immutable implementation without silently replacing the administrator's active `/etc` configuration.

## Minecraft is a separate workload

Minecraft is not baked into the JustVoxel operating-system image.

The JustVoxel image contains the management logic and templates needed to create and operate a separate containerized server workload. During first setup, the administrator chooses the supported Minecraft settings and explicitly accepts the Minecraft EULA. JustVoxel then renders the local runtime configuration and manages the separate container workload through Podman and systemd Quadlets.

This separation is intentional for both maintenance and licensing clarity:

- JustVoxel OS images do not contain Minecraft server binaries or Mojang server software.
- Minecraft/Paper runtime updates are separate from JustVoxel operating-system updates.
- Minecraft persistent data remains outside the immutable OS deployment.
- The administrator retains control of the runtime policy and data location.

The current runtime design uses Paper with optional cross-play components managed by the JustVoxel runtime workflow. See [MINECRAFT_RUNTIME.md](MINECRAFT_RUNTIME.md) for the runtime boundary.

## Podman and Quadlets

Podman runs the Minecraft server workload as a container.

Systemd Quadlets describe the container as a normal systemd-managed service. This lets JustVoxel use standard service ordering, restart behavior, logging, and host integration without introducing a separate proprietary daemon for the Minecraft process.

The active rendered Quadlet is administrator-visible under `/etc`. Advanced users can inspect it directly, while normal users operate the appliance through `mjust`.

## mjust is the normal appliance interface

The normal user should not need to know which `podman`, `systemctl`, `bootc`, `firewall-cmd`, storage, or SELinux command is required for a common JustVoxel task.

`mjust` provides the user-facing administration layer for first setup, Minecraft service control, players, whitelist management, backups, restore, storage, updates, status, validation, operating-system maintenance, and power controls.

The safety checks live below the interactive menu, so direct `mjust` commands retain the same safeguards.

Advanced administrators can still use the normal Linux tools directly when deeper control or troubleshooting is required.

See [MJUST.md](MJUST.md) and [MANAGEMENT.md](MANAGEMENT.md).

## Security model

JustVoxel uses the normal Linux security boundaries rather than replacing them with a custom all-powerful management process.

The appliance relies on:

- SELinux enforcing mode
- firewalld
- least-necessary service exposure
- internal-only RCON access
- explicit storage identity validation
- exact confirmations for destructive storage actions
- player-aware interruption handling
- verified backup and restore workflows
- fail-closed behavior when critical state cannot be confirmed

The goal is for the convenient interface to preserve the same safety expectations as the underlying system operations.

## Separate update paths

JustVoxel has two important update layers.

### Operating-system updates

The JustVoxel OS is updated through bootc. `mjust os-status` and `mjust os-update` provide the appliance-facing workflow for checking and staging operating-system updates.

### Minecraft workload updates

The Minecraft/Paper container workload is maintained separately. `mjust update-minecraft` handles the configured container-image policy and Minecraft/Paper version policy without treating a Minecraft update as an operating-system update.

Keeping these paths separate reduces unnecessary coupling between the appliance OS and the server workload.

## Base and final product variants

This repository builds one shared JustVoxel Base image.

The Base is already VM-ready and contains the complete appliance core plus VM validation. After validation, the final product repository can promote the approved `justvoxel-base:stable` image into the JustVoxel VM release without rebuilding the same content.

JustVoxel HWE is the physical-machine product. It derives from the approved Base and adds the physical-hardware administration delta such as UPS, storage-health, sensor, firmware, and hardware-diagnostic tooling.

See [VARIANTS.md](VARIANTS.md) for the product layering.

## Storage is independent from the immutable image

Minecraft persistent data and backup storage are not part of the immutable bootc deployment.

JustVoxel can keep Minecraft data on the system filesystem or migrate it to supported local storage. Backup storage is selected independently and can use local disks, supported existing filesystems, or network storage.

This allows the operating system to be replaced or updated without treating the Minecraft world and backup strategy as part of the image itself.

See [STORAGE.md](STORAGE.md) and [RESTORE.md](RESTORE.md).

## Documentation boundaries

The documentation is intentionally split by purpose:

- [README](../README.md) — project overview and the normal starting point
- [MJUST.md](MJUST.md) — operating the appliance
- [MANAGEMENT.md](MANAGEMENT.md) — management layers and native Linux access
- [STATUS.md](STATUS.md) — appliance health/status model
- [SYSTEM.md](SYSTEM.md) — OS maintenance and power controls
- [STORAGE.md](STORAGE.md) — storage and migration
- [RESTORE.md](RESTORE.md) — Minecraft-data recovery
- [VARIANTS.md](VARIANTS.md) — Base, VM release, and HWE product layering
- [BUILD.md](BUILD.md) — image composition, CI, signing, and release mechanics
- [JustVoxel ISO Builder](https://github.com/home-server-project/justvoxel-iso) — fresh installation, hardware/disk guidance, installation access, and installer-media creation

The design goal is straightforward: a normal user should be able to install and operate JustVoxel without deep Linux administration knowledge, while the full underlying system remains available to administrators who want it.
