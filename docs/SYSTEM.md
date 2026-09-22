# JustVoxel system management

JustVoxel keeps operating-system maintenance separate from Minecraft/container maintenance.

Reboot, poweroff, and firmware/UEFI reboot use the same Management API architecture as other migrated appliance controls:

`mJust / WebUI -> Management API -> Management Agent -> system action backend`

The terminal commands and WebUI power control are presentation/confirmation frontends. Host power control, firmware capability checks, player-safety decisions, and final systemd actions are owned behind the Management API.

## Commands

- `mjust os-status` — friendly bootc deployment status
- `mjust os-update` — check for a newer OS image and optionally download/stage it
- `mjust resources` — open the live btop system resource monitor
- `mjust reboot` — player-aware graceful reboot
- `mjust poweroff` — player-aware graceful power off
- `mjust firmware` — reboot into firmware/UEFI setup when supported on physical hardware

All commands are also reachable from `mjust` -> **System**.

## OS status

`mjust os-status` reads `bootc status --json --format-version=1` and translates bootc deployment state into Running image, Staged update, and Rollback image.

The command is read-only.

## OS update

`mjust os-update` first runs `bootc upgrade --check` so the available update metadata is refreshed even when another deployment is already staged.

When a newer image is available, the administrator is asked whether to download and stage it. Approval runs ordinary `bootc upgrade`.

Downloading/staging an OS image does not reboot the host, stop Minecraft, query players, or create a Minecraft backup. The running system continues unchanged. The staged deployment is used after the next normal reboot.

There is intentionally no separate `mjust os-apply` command. Reboot is the normal bootc apply boundary.

## rpm-ostree package layering

JustVoxel disables rpm-ostree package layering by default through `/etc/rpm-ostreed.conf` with `LockLayering=true`. This keeps deployed systems aligned with the tested appliance image instead of allowing local package overlays, package overrides, or other mutations of the base OSTree deployment.

This does not disable JustVoxel system updates. `mjust os-update` and `bootc upgrade` continue to check, download, and stage newer JustVoxel images normally.

An administrator with root access can deliberately override the policy when local layering is required by changing `LockLayering=false` in `/etc/rpm-ostreed.conf` and running `sudo rpm-ostree reload`. A system with local layering enabled is a locally customized deployment rather than the default JustVoxel appliance state.

## System resources

`mjust resources` opens `btop` as a live resource view for CPU, memory and swap, disks, network activity, and running processes.

It requires an interactive terminal. Over SSH, allocate a terminal, for example:

```text
ssh -t <server> mjust resources
```

Inside btop:

- `q` quits directly back to the JustVoxel System menu or the calling shell
- `Esc` opens the btop menu, which includes Quit
- `Ctrl-C` also exits btop

The JustVoxel menu does not add an extra pause after btop exits.

## Reboot and power off

`mjust reboot` and `mjust poweroff` are thin Management API frontends.

The Agent exposes system-action capability/status data and requires explicit confirmation before accepting either action. It applies the same player-aware interruption policy as Minecraft service control:

1. if Minecraft is stopped, no RCON query is needed
2. if Minecraft is running, query players through internal RCON
3. fail closed if player state cannot be determined
4. require explicit approval when players are online
5. stop Minecraft through the shared adaptive graceful-shutdown path
6. queue the requested host action only after Minecraft has stopped safely

Accepted host actions are queued with a short delay so the API can return a final accepted response before the machine goes down.

An ordinary reboot/poweroff does not force a Minecraft backup. If a bootc update is staged, the next boot uses it normally.

## Firmware / UEFI

`mjust firmware` is a thin frontend over the same System Actions API and is intended only for supported physical-hardware deployments. VM or otherwise unsupported environments refuse the operation and direct the administrator to the platform/hypervisor controls.

The Agent owns the HWS/VM decision, EFI/systemd firmware-reboot capability check, best-effort DRM display state, player-safe Minecraft shutdown, and final firmware reboot request. The terminal only presents those results and asks for confirmation.

The WebUI uses the same capability/status endpoint. Its browser-facing label is **Restart to UEFI/BIOS**. The control is shown only when the Agent reports supported HWS firmware capability, so VM deployments never offer the UEFI/BIOS action.

Display detection is advisory because KVM switches, EDID behavior, firmware and hardware can make Linux connector state imperfect.

## Rollback

JustVoxel-aware bootc rollback remains deliberately out of scope. It needs a separate design for `/etc` configuration preservation/reconciliation and post-rollback recovery behavior.
