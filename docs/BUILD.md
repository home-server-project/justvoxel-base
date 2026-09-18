# JustVoxel Base build model

JustVoxel Base follows the Home Server Project's shared Home Server Base 10 bootc composition model.

## Parent image

JustVoxel Base consumes [Home Server Base 10](https://github.com/home-server-project/home-server-base-10) directly from the signed moving channel:

```text
ghcr.io/home-server-project/home-server-base-10:stable
```

The workflow verifies the current `:stable` signature with Cosign and builds from that tag directly. The parent image is not maintained as a manually pinned digest in this repository.

The supported host-management model is bootc. No host-side rpm-ostree layering workflow is supported.

## Base image

This repository builds one image:

```text
ghcr.io/home-server-project/justvoxel-base
```

The Base contains the complete shared JustVoxel appliance layer and is already VM-ready. It includes the management agent, WebUI integration, `mjust`, Minecraft runtime templates and helpers, common networking/storage tooling, and both `health/common` and `health/vm` validation.

Physical-hardware-only packages and validation are intentionally not part of this repository. That delta belongs to the final JustVoxel HWE product.

## Channels

### Testing

The `testing` branch builds on:

- push to `testing`
- manual `workflow_dispatch`

It publishes:

```text
ghcr.io/home-server-project/justvoxel-base:testing
testing-YYYYMMDD-<git-sha>
```

There is no scheduled testing build.

### Stable

The stable workflow is currently manual-only:

```yaml
on:
  workflow_dispatch:
```

When deliberately run from validated `main`, it publishes:

```text
ghcr.io/home-server-project/justvoxel-base:stable
stable-YYYYMMDD-<git-sha>
```

No automatic stable schedule or main-push build is enabled during active development.

## Final products

The final product repository consumes `justvoxel-base:stable` by tag.

The intended product split is:

```text
justvoxel-base:stable
        |
        +--> JustVoxel VM   (promotion/copy of the approved Base)
        |
        +--> JustVoxel HWE  (physical-hardware delta built on the approved Base)
```

The Base image installs signed-image trust for the Base repository and the future VM and HWE repositories so a promoted VM image does not require rebuilding solely to change repository identity.

## Rechunk limits

CI rechunks the completed Base image with:

- RPM chunk target: `127`
- OCI layer hard limit: `128`

## Signing

Published Base images are signed with the Home Server Project Cosign key. CI verifies the signature after publication.
