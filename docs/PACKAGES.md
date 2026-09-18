# JustVoxel Base packages

JustVoxel Base includes the common operating-system and appliance packages used by every JustVoxel installation.

This document groups those packages by purpose so the package set is understandable without reading the build scripts. The exact current package declaration remains the source of truth in [`build_files/packages.env`](../build_files/packages.env).

## Core networking and remote access

JustVoxel includes the normal server networking tools needed for configuration, troubleshooting, and remote administration:

- NetworkManager and `nmtui`
- systemd-resolved
- firewalld
- OpenSSH
- common IP, DNS, routing, connectivity, packet-capture, and throughput tools

This group includes tools such as `iproute`, `iputils`, `bind-utils`, `traceroute`, `nmap-ncat`, `tcpdump`, and `iperf3`.

## Tailscale and NetBird

Tailscale and NetBird are included in the Base image as optional remote-access tools.

They are **not configured automatically** and their services are **disabled by default**. Installing them in the image means an administrator can enable and configure either option without adding packages to the immutable host.

Their external package repositories are used only during image composition and are disabled in the finished image.

## Container and appliance runtime

The Base includes the tools required for the containerized Minecraft workload and JustVoxel management layer:

- Podman
- Skopeo
- container SELinux policy
- `just`
- `fzf`
- `gum`
- `btop`
- Python 3
- common JSON, TLS, archive, file, and process utilities

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

## Exact package list

For the exact current package names used by the image build, see:

- [`build_files/packages.env`](../build_files/packages.env)

The build logic that installs the Base packages, Tailscale, and NetBird is in:

- [`build_files/build-common.sh`](../build_files/build-common.sh)
