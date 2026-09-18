# JustVoxel Base

JustVoxel Base is the shared, VM-ready immutable appliance image used to develop and release the JustVoxel Minecraft server platform.

It is designed for people who are comfortable installing an operating system and following normal computer instructions, but who do not want to become Linux, container, systemd, firewall, SELinux, or Minecraft-server administrators just to run a reliable family or small-community server.

> **Development status:** active Base development is on the `testing` branch. Stable Base publication remains manual-only until the development line is deliberately promoted.

## What JustVoxel is

JustVoxel is a complete server appliance, not an RPM, a shell script, or a container bundle that is installed on top of an arbitrary existing Linux system.

The operating system, management layer, update model, storage safety rules, backup/recovery logic, and container runtime integration are built and versioned together. The goal is to give users a predictable system that can be installed, configured, operated, updated, and recovered without requiring deep knowledge of the technologies underneath it.

Under the hood, JustVoxel is built on [Home Server Base 10](https://github.com/home-server-project/home-server-base-10), which provides the shared AlmaLinux 10 Minimal Plus bootc foundation. AlmaLinux 10 remains the upstream Enterprise Linux source for the kernel and core operating-system packages. JustVoxel uses standard Linux components such as Podman, systemd, NetworkManager, firewalld, and SELinux, but normal users are not expected to manage those pieces directly.

For the technical design and the reasons behind it, see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Minecraft is a separate workload

JustVoxel does **not** ship Minecraft server binaries, Mojang server software, a pre-created world, or pre-accepted Minecraft EULA state inside the operating-system image.

The JustVoxel image provides the appliance operating system, management tools, runtime templates, and safety mechanisms. During first setup, the administrator accepts the Minecraft EULA and JustVoxel creates the active configuration for a separate containerized Paper-based server workload.

Minecraft/Paper maintenance is intentionally separate from JustVoxel operating-system maintenance.

For the runtime boundary and implementation details, see [`docs/MINECRAFT_RUNTIME.md`](docs/MINECRAFT_RUNTIME.md).

## Who JustVoxel is for

The main audience is an advanced home user rather than a professional Linux administrator.

If you can install Windows or Linux yourself, create a VM, write an ISO to a USB drive, follow installation instructions, and understand basic ideas such as an IP address and a disk, JustVoxel is intended to handle the deeper appliance work for you.

Experienced administrators are not locked out. JustVoxel remains a normal immutable EL10 server built on Home Server Base 10 / AlmaLinux 10, and standard Linux administration tools remain available when deeper control or troubleshooting is wanted.

## Install JustVoxel

For a fresh installation, use the dedicated [JustVoxel ISO Builder](https://github.com/home-server-project/justvoxel-iso).

The ISO project owns the installation side of JustVoxel, including:

- VM versus physical/HWE installation
- CPU, memory, and disk guidance
- installer-media creation
- installation-disk safety
- SSH and first-access guidance
- installer options
- first boot

The normal user path is to install JustVoxel from its installer media rather than manually rebasing another operating system to the JustVoxel image.

Advanced bootc users can still work directly with the published images, but that is not the primary installation path documented for normal users.

## Base, VM release, and HWE

This repository builds one shared appliance image: **JustVoxel Base**.

The Base is already VM-ready. After validation, the final JustVoxel product repository can promote the approved `justvoxel-base:stable` image into the VM release without rebuilding the same appliance content.

**JustVoxel HWE** is the physical-machine product. It derives from the approved Base and adds only the hardware-specific administration delta.

For the product layering, see [`docs/VARIANTS.md`](docs/VARIANTS.md).

## Operate the appliance with mjust

`mjust` is JustVoxel's built-in administration interface.

Run `mjust` with no arguments to open the interactive terminal interface and choose what you want to do. It is designed so common appliance operations can be completed without knowing the Linux commands underneath them.

The interface covers first setup, Minecraft configuration and service control, players and whitelist management, backups and restore, storage, Minecraft updates, appliance health/status, operating-system maintenance, system resources, validation, and other appliance tasks.

The underlying safety checks remain active whether an operation is started from the menu or from a direct `mjust` command.

The interaction model is inspired by Universal Blue's `ujust` / `ugum` work in [ublue-os/packages](https://github.com/ublue-os/packages), while JustVoxel uses its own server-focused implementation.

For the complete interface and command reference, see [`docs/MJUST.md`](docs/MJUST.md).

## Web management

JustVoxel WebUI is intended for administration from a trusted local network. By default, Web management uses plain HTTP on TCP port `8099`, allowing direct access from the appliance LAN address without a self-signed certificate warning.

The default administrator is `voxel`. In the recommended/default **System account** authentication mode, the same real Linux `voxel` password is used by the WebUI, local console, and SSH password login when SSH password authentication is enabled. Authentication is performed through the AlmaLinux/RHEL PAM stack by the privileged JustVoxel Management Agent; the unprivileged WebUI does not keep a synchronized copy of that password.

An optional **Separate WebUI password** mode is available for administrators who intentionally want browser authentication to differ from the Linux/SSH password.

Because the default local WebUI does not use TLS, administrator credentials and sessions should only be used on a network you trust. Do not forward TCP port `8099` directly to the public Internet.

For the local-access, authentication, and security model, see [`docs/WEBUI.md`](docs/WEBUI.md).

## Storage, backups, and recovery

JustVoxel separates Minecraft data from backup storage so users can choose a layout that fits their machine or hypervisor.

The appliance can use supported local storage or network storage for backups, and Minecraft data can remain on the system filesystem or be migrated to supported local storage later.

Storage operations are guarded with device validation and exact typed confirmations for destructive actions. Backup and restore workflows use their own validation and safety checks rather than relying on the user to assemble commands manually.

See:

- [`docs/STORAGE.md`](docs/STORAGE.md) for storage and migration
- [`docs/RESTORE.md`](docs/RESTORE.md) for world and full Minecraft-data recovery

## Updates

JustVoxel keeps operating-system updates and Minecraft workload updates separate.

The appliance operating system is updated through bootc and can be managed through `mjust` system-management commands.

The separate Minecraft/Paper workload has its own update flow, backup safeguards, version policy, and rollback handling.

See [`docs/SYSTEM.md`](docs/SYSTEM.md) for operating-system maintenance and [`docs/MJUST.md`](docs/MJUST.md) for the Minecraft update workflow.

## Documentation

Start with the document that matches what you are trying to do:

- [JustVoxel ISO Builder](https://github.com/home-server-project/justvoxel-iso) — install JustVoxel on a VM or physical machine
- [`docs/MJUST.md`](docs/MJUST.md) — operate the appliance
- [`docs/STATUS.md`](docs/STATUS.md) — understand the appliance health dashboard
- [`docs/SYSTEM.md`](docs/SYSTEM.md) — OS status, updates, resources, reboot, poweroff, and firmware controls
- [`docs/STORAGE.md`](docs/STORAGE.md) — storage choices, provisioning, mounts, and migration
- [`docs/RESTORE.md`](docs/RESTORE.md) — world and full Minecraft-data recovery
- [`docs/MANAGEMENT.md`](docs/MANAGEMENT.md) — management layers and native Linux administration
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — why JustVoxel is built as an immutable appliance
- [`docs/VARIANTS.md`](docs/VARIANTS.md) — Base, VM release, and HWE product layering
- [`docs/BUILD.md`](docs/BUILD.md) — image composition, signing, CI, branches, and release mechanics
- [`docs/ROADMAP.md`](docs/ROADMAP.md) — current priorities, future features, and project non-goals

## Branch and release model

- `testing` — active Base development
- `main` — validated Base promotions

Testing builds run on pushes to `testing` and manual **Run workflow** invocations. They publish:

```text
ghcr.io/home-server-project/justvoxel-base:testing
testing-YYYYMMDD-<git-sha>
```

There is no scheduled testing build.

The stable Base workflow is currently manual-only. When deliberately run from validated `main`, it publishes:

```text
ghcr.io/home-server-project/justvoxel-base:stable
stable-YYYYMMDD-<git-sha>
```

Final JustVoxel VM/HWE release images are owned by the separate final product repository and consume the approved `justvoxel-base:stable` channel.

## License

Apache-2.0. See [LICENSE](LICENSE).

Minecraft, Mojang software, Paper, plugins, and other third-party components retain their own licenses and distribution terms. JustVoxel does not embed Minecraft server binaries or Mojang server software in its bootc images.
