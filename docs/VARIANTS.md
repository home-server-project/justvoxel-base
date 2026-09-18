# JustVoxel product layering

JustVoxel now separates the shared appliance Base from final product-specific delivery.

## JustVoxel Base

`justvoxel-base` contains the complete shared Minecraft appliance and is already VM-ready.

It includes:

- Home Server Base 10 / AlmaLinux 10 bootc foundation
- Podman and Quadlet support
- NetworkManager + `nmtui`
- OpenSSH
- firewalld
- SELinux container policy and administration tools
- JustVoxel management agent
- JustVoxel WebUI integration
- `mjust`
- Minecraft runtime templates and helpers
- backup, storage, migration, restore, and system-management logic
- VM guest tooling
- `health/common`
- `health/vm`

It intentionally excludes the physical-hardware-only administration delta.

## JustVoxel VM

The final VM product is intended to be promoted from an approved `justvoxel-base:stable` image rather than rebuilt as a second copy of the same appliance content.

The final product repository owns the VM release tag and release mechanics.

## JustVoxel HWE

JustVoxel HWE is the physical-machine product.

It derives from the approved `justvoxel-base:stable` channel and adds only the physical-hardware administration delta, such as UPS, storage-health, sensor, firmware, and hardware-diagnostic support.

The HWE package list, HWE build logic, and HWE validation belong in the final JustVoxel product repository, not in `justvoxel-base`.

Hardware-specific runtime configuration remains deployment-specific. For example, UPS hardware identifiers, NUT driver selection, credentials, and shutdown policy are not baked into the generic Base image.
