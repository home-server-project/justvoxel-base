# JustVoxel Plus build model

All Plus development stays in testing-plus. The normal testing branch is not a development target and must not be inspected or modified for Plus work.

## Parent and output

The signed parent is ghcr.io/home-server-project/home-server-base-10:stable-docker, for x86-64-v3. Docker Engine belongs to that base; retained Podman is a bootc dependency, not the Plus application runtime. Toolbox is not included.

Pushes to testing-plus and manual builds selected on testing-plus publish ghcr.io/home-server-project/justvoxel-plus-base:testing-plus and testing-plus-YYYYMMDD-<git-sha>. Manual builds from other refs are skipped. Checkouts use the triggering commit.

The copied stable workflow is disabled in this branch. Automatic package cleanup is disabled during Plus development to avoid touching shared package history. Cosign signing and verification and the existing 127-target/128-hard-limit rechunk checks remain in place.

## Product boundary and milestone

JustVoxel Plus manages the host. Drydock manages infrastructure containers. Pterodactyl manages game servers. Phase one isolates publication and selects the Docker parent. Phase two enables the host-only Plus desktop and blocks inherited game workflows. The infrastructure stack and deployment wizard are not implemented yet.

The later wizard account step must explain: These are separate accounts. Changing your password in one does not change the other.

Full image CI and boot/runtime validation are separate from local source checks. No stable Plus release is defined yet.
