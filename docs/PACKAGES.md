# JustVoxel Base packages

JustVoxel Base includes the common operating-system and appliance packages used by every JustVoxel installation.

This document groups those packages by purpose so the package set is understandable
without reading the build scripts. `build_files/packages.env` declares inherited
requirements and the common DNF package list. The complete image composition also
includes package-stage RPMs and vendor repositories declared by `Containerfile`
and `build_files/build-common.sh`; no single package file is the entire inventory.

## Core networking and remote access

JustVoxel includes the normal server networking tools needed for configuration, troubleshooting, and remote administration:

- NetworkManager and `nmtui`
- NetworkManager-HSP (`nm-hsp`) as the friendly Home Server Project terminal interface
- systemd-resolved
- firewalld
- OpenSSH
- common IP, DNS, routing, connectivity, packet-capture, and throughput tools

The JustVoxel System → Network menu presents `nm-hsp` for normal Ethernet, Wi-Fi, and easy network troubleshooting, with `nmtui` retained as the classic advanced NetworkManager interface.

This group includes tools such as `iproute`, `iputils`, `bind-utils`, `traceroute`, `nmap-ncat`, `tcpdump`, and `iperf3`.

## Tailscale and NetBird

Tailscale and NetBird are included in the Base image as optional remote-access tools.

They are **not configured automatically** and their services are **disabled by default**. Installing them in the image means an administrator can enable and configure either option without adding packages to the immutable host.

Their external package repositories are used only during image composition and are disabled in the finished image.

## NordVPN Meshnet

The testing image installs the native `nordvpn` RPM from NordVPN’s official YUM repository using its [repository bootstrap RPM](https://repo.nordvpn.com/yum/nordvpn/centos/noarch/Packages/n/nordvpn-release-1.0.0-1.noarch.rpm). The repository resolves the current package at composition time; there is no package version/digest resolver, container, or runtime layering.

All repository sections supplied by `nordvpn-release` are explicitly disabled after installation. The bootstrap package remains installed with disabled repository definitions. Composition normalizes vendor service enablement by stopping the daemon/socket if running and disabling both `nordvpnd.service` and `nordvpnd.socket` in the finished image. Fresh boot is dormant: no Nord login, account, Meshnet identity or Meshnet configuration is created during composition. Account state is established only by an administrator at runtime and survives normal Meshnet off/on operations.

Image validation checks the RPMs, `/usr/bin/nordvpn`, `/usr/sbin/nordvpnd`, both native units, disabled service/socket state and disabled repository sections. JustVoxel’s WebUI controls Meshnet only.

## Playit.gg public game access

Playit is consumed exclusively from the Home Server Packages artifact `ghcr.io/home-server-project/playit:stable`. The Containerfile copies its `/rpms` payload through a package stage, and image composition installs the verified `playit-*.x86_64.rpm`. JustVoxel has no Playit software version pin or custom digest resolution. HSP owns upstream tracking, build and provenance.

JustVoxel owns appliance service policy: composition enables `playit.service` without starting it. The fresh image intentionally contains no `/etc/playit/playit.toml`, account secret or agent identity. On deployed boot the enabled service starts in its supported waiting-for-secret state. After setup it reads the persistent `/etc/playit/playit.toml` normally on reboot. WebUI Activate/Deactivate persistently enable/start or stop/disable the service.

Appliance image validation checks the `playit` RPM, `/usr/bin/playit`, `/usr/bin/playitd`, the packaged service unit, enabled state and absence of fresh-image configuration. It does not start Playit, require configuration or test tunnels.

`mjust net` option three provides Playit status, activation/deactivation and initial setup through the packaged `playit setup` CLI. Account and tunnel management remains at https://playit.gg/account/.

## Minecraft Network plugins

JustVoxel consumes `ghcr.io/home-server-project/minecraft-network-plugins:stable`
through a Containerfile package stage. Home Server Packages owns the build,
upstream tracking and provenance. JustVoxel installs its `/artifacts/limboapi.jar`
and `/artifacts/justvoxel-lobby.jar` runtime artifacts into
`/usr/lib/justvoxel/minecraft-network/`.

## Container and appliance runtime

The Base includes the tools required for the containerized Minecraft workload and JustVoxel management layer:

- Podman
- Skopeo
- container SELinux policy
- `just`
- `fzf`
- `gum`
- `btop`
- `ttyd` for the authenticated on-demand WebUI terminal
- Superfile (`spf`) for the administrator file-manager backend
- Glances for the local system-monitor backend
- Python 3
- common JSON, TLS, archive, file, and process utilities

## Plugin icon conversion

`librsvg2-tools` is included only for bounded SVG/SVGZ plugin icon rendering.
`netpbm-progs` is included only for bounded SGI RGB plugin icon conversion.
These are image-composition dependencies, not runtime package installation.
Composition checks require `rsvg-convert`, `sgitopnm` and `pnmtopng`.

## SELinux and system security

JustVoxel keeps SELinux enforcing and includes the administration/policy tools required for container workloads and appliance-managed files:

- `container-selinux`
- `policycoreutils-python-utils`
- `selinux-policy-extra`
- CA certificates and OpenSSL
- PAM, authselect, and password-quality support

## Storage, backup, and migration

The Base includes the common tools used by JustVoxel storage, backup, restore, and migration workflows:

- XFS utilities
- partitioning and block-device utilities
- rsync
- tar and gzip
- NFS client support
- SMB/CIFS client support
- standard mount/filesystem utilities

These packages support local storage as well as supported network backup and migration targets.

## Administration and troubleshooting

Common server-administration tools are included so both JustVoxel workflows and advanced administrators can inspect and troubleshoot the appliance when necessary.

Examples include `curl`, `jq`, `lsof`, `file`, `less`, `nano`, `util-linux`, and related command-line utilities.

## VM guest integration

The Base is VM-ready and includes guest integration for the major supported hypervisor families:

- QEMU/KVM guest agent
- VMware guest tools
- Hyper-V daemons

These packages can remain installed even when a particular hypervisor does not use them.

## Exact package sources

For inherited requirements and the common DNF package names, see:

- [`build_files/packages.env`](../build_files/packages.env)

The build logic that installs those packages plus the verified Superfile, Glances,
and Playit package-stage RPMs and the native NordVPN RPM is in:

- [`build_files/build-common.sh`](../build_files/build-common.sh)

The package-stage image references are declared in:

- [`Containerfile`](../Containerfile)
