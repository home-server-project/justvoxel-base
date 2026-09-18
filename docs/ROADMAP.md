# JustVoxel roadmap

This roadmap tracks product direction and future appliance capabilities. It is priority-based rather than date-based: items move forward when the current layer is validated and stable enough to support the next one.

JustVoxel is intentionally a dedicated Minecraft server appliance. The goal is not to become a general-purpose game-hosting panel or generic container platform.

## Product principles

JustVoxel should remain understandable to someone who can install an operating system, create a VM, write an ISO to a USB drive, and follow normal computer instructions without requiring deep Linux administration knowledge.

The appliance is designed to run either in an isolated VM or on dedicated hardware. Minecraft itself remains a separate containerized workload rather than being baked into the JustVoxel operating-system image.

Normal operations should prefer guided, validated workflows over exposing raw Linux commands. Advanced administrators still retain access to the underlying AlmaLinux system when they need it.

Safety remains more important than automation. If JustVoxel cannot reliably determine the state of players, storage, backups, configuration, or another destructive boundary, the normal behavior should be to fail closed rather than guess.

## Current priority: stabilization and validation

The immediate priority is to finish and harden what already exists before expanding the appliance surface.

Current Base work includes:

- real-world WebUI testing and bug fixing
- VM-ready Base validation
- installer and first-boot validation
- backup and restore validation
- import/export and migration validation
- destructive and failure-path storage testing
- network-storage failure testing
- Minecraft/Paper update testing
- operating-system update testing
- documentation reconciliation after behavior is proven

Existing capabilities such as System-account PAM authentication, the optional Separate WebUI password provider, player-aware interruption checks, scheduled backups, manual backups, restore, import/export, migration recovery, storage provisioning and migration, Minecraft updates, bootc OS update staging, status, validation, resources, reboot, poweroff, and firmware handling are implemented features rather than future roadmap items.

## Near-term: player-aware maintenance improvements

The shared player-awareness and fail-closed interruption checks are already implemented. Minecraft stop/restart, update, restore, migration, reboot, poweroff, and related disruptive workflows can check whether players are online before interruption.

The remaining usability improvement is to make the shutdown experience smarter:

- if Minecraft is already stopped, continue immediately;
- if zero players are online, avoid an unnecessary long player-warning delay;
- if players are online, provide useful in-game maintenance countdown notices such as 60, 30, 10, and 5 seconds where the runtime supports it cleanly;
- avoid duplicated waiting between JustVoxel's maintenance flow and the underlying Minecraft container shutdown behavior.

This work should improve convenience without weakening the current fail-closed player-state policy.

## Next: notifications and event history

JustVoxel should provide useful appliance-level notifications without becoming a full monitoring platform.

Examples include:

- backup failure
- low storage space
- repeated Minecraft service failures
- OS update availability
- Minecraft/Paper update availability
- import, export, or restore completion/failure

Initial notification targets should stay simple, such as Discord and a generic webhook mechanism.

Event history should complement notifications by keeping short appliance-relevant records such as repeated Minecraft restarts, the last failure time, whether automatic recovery succeeded, and concise diagnostic context.

Systemd should remain the normal service-recovery mechanism rather than adding a second custom watchdog solely for Minecraft restarts.

## Later: UPS monitoring

JustVoxel Base should eventually expose native UPS monitoring through the common JustVoxel WebUI and Management API.

UPS support is part of the appliance capability model owned by Base. The same WebUI, Management Agent, API, mjust layer, and validation model should handle UPS functionality when the running system provides the required NUT/hardware capability. On systems where that capability is unavailable or inappropriate, the UPS controls should remain hidden or disabled.

The implementation should therefore be feature-gated by detected capability rather than split into a separate management stack.

The preferred design is not to add a second Cockpit administration interface or embed the Cockpit UPSide plugin. JustVoxel should use Network UPS Tools (NUT) as the backend and expose a narrow UPS API through the existing privileged management agent:

```text
JustVoxel WebUI
    -> local Unix socket
    -> JustVoxel Management Agent
    -> NUT / upsd
```

The browser-facing WebUI must remain unprivileged and must not receive arbitrary shell access. The management agent may use the normal NUT client interfaces such as `upsc -l` and `upsc <ups-name>` and return normalized UPS data to the WebUI.

The initial UPS page should be deliberately small and read-only. Useful fields include:

- UPS manufacturer/model and friendly name where available
- online / on-battery / low-battery / communication state
- battery charge percentage
- estimated runtime remaining
- UPS load percentage
- input voltage
- output voltage
- battery voltage where reported
- current NUT/driver communication health

The main dashboard may surface concise appliance warnings such as **On battery**, **Low battery**, or **UPS communication lost**. Dangerous UPS controls, load-off commands, UPS shutdown commands, writable NUT variables, and automated power sequencing are not part of the first monitoring implementation.

### AlmaLinux 10 / bootc lessons already proven in Pasiv Black Box

The implementation should treat the existing `highwaytoit/pasiv-black-box` main and testing branches as a practical AlmaLinux 10 reference. That project uncovered several NUT/UPSide issues that are easy to miss in a normal mutable distro install and should be checked before any JustVoxel UPS work is considered complete.

#### NUT USB discovery

`nut-scanner` on the AlmaLinux 10 image required the libusb development/runtime linkage to be present. Pasiv carries `libusb1-devel` and validates that `/usr/lib64/libusb-1.0.so` exists. JustVoxel should verify the exact requirement on its current AlmaLinux base rather than assuming that the presence of `nut` and `nut-client` alone makes USB scanning functional.

#### NUT service-account supplementary groups

EL10 bootc can keep vendor group data in `/usr/lib/group` while `systemd-sysusers` writes supplementary membership state under `/etc/gshadow`. Pasiv found that the NUT service account needed its package-declared `tty` and `dialout` memberships preserved in the image-managed group database so NUT could access its runtime/device paths correctly after deployment.

Any JustVoxel implementation that uses local USB UPS hardware must validate after image composition and after a real boot that the `nut` account has the required supplementary groups. The fix should follow the active AlmaLinux/bootc account model rather than blindly copying a mutable-host command sequence.

#### Secure NUT configuration ownership

Pasiv explicitly preserves the package-intended ownership and mode for:

- `/etc/ups/upsd.conf` -> `root:nut` mode `0640`
- `/etc/ups/upsd.users` -> `root:nut` mode `0640`

JustVoxel should validate those or the current package-equivalent permissions after bootc composition so `upsd` can read its configuration without making credential-bearing files broadly readable.

#### Do not enable an unconfigured UPS stack globally

A generic image cannot assume that every physical machine has a directly attached UPS. NUT hardware identifiers, UPS name, driver, credentials, shutdown thresholds, listener addresses, and site-specific power policy remain deployment-specific.

The image should therefore ship the capability without pretending it is configured. Once a local UPS has been configured, the setup path should ensure the appropriate NUT top-level target/service set is enabled and survives reboot.

Pasiv's working model uses `nut.target` as the reboot-safe top-level NUT activation point, with server, monitor, and driver units following the NUT target relationships rather than enabling unrelated units ad hoc.

#### NUT network listener ordering

If `upsd.conf` binds `LISTEN` to a specific LAN address, `nut-server.service` can start before that address exists and fail with listener errors during boot. Pasiv testing fixed this with a systemd drop-in that adds:

```ini
[Unit]
Wants=network-online.target
After=network-online.target
```

JustVoxel should include equivalent ordering only where the NUT server mode needs it and should validate a full host reboot, not just a manual service restart.

#### Optional historical trends require the complete PCP path

If JustVoxel later adds UPS history/trend charts, the AlmaLinux reference is more than simply installing `pcp`.

Pasiv required the focused package set:

- `pcp`
- `pcp-pmda-openmetrics`
- `pcp-system-tools`

`pcp-system-tools` provides `pmrep`, which UPSide uses to read local PCP archives. Pasiv also enables `pmcd.service` and `pmlogger.service`, validates the OpenMetrics PMDA installer, and notes that the `pmcd` binary on this EL10 layout is under `/usr/libexec/pcp/bin/pmcd` rather than necessarily being available as a normal shell command.

The OpenMetrics PMDA must be registered before an OpenMetrics-based UPS history collector can be configured. On the tested AlmaLinux system the registration path is:

```text
/var/lib/pcp/pmdas/openmetrics/Install
```

Historical charts should remain optional. Live NUT monitoring must not depend on PCP being configured.

#### Use Pasiv's NUT Exporter work as telemetry/validation reference, not as a mandatory dependency

Pasiv testing also validated a NUT Exporter path against a local NUT server on `127.0.0.1:3493`, including battery charge, runtime, battery/input/output voltage, load, and UPS status metrics. It deliberately disables hardware-identity metadata and keeps the exporter on a private monitoring path rather than exposing it generally on the LAN.

JustVoxel does not need to add Prometheus, VictoriaMetrics, or NUT Exporter merely to render a local UPS page. However, that validated field set and reboot test are useful acceptance references. A future optional metrics/exporter integration may reuse that model if it fits JustVoxel without turning the appliance into a monitoring platform.

### UPSide and Home Server Packages relationship

`home-server-project/home-server-packages` already builds and validates the upstream `deviationist/cockpit-upside` package on AlmaLinux 10 / EL10 and Fedora. For JustVoxel, that package should primarily be treated as an upstream/reference implementation unless the architecture changes deliberately.

UPSide is tightly coupled to Cockpit's runtime and `cockpit.spawn()` API. Installing Cockpit solely to host UPSide would create a second Web administration surface, second authentication/session model, and additional network exposure that does not fit the normal JustVoxel appliance design.

If actual UPSide source code is reused rather than only its public NUT behavior and UI ideas, its `LGPL-2.1-or-later` licensing and required notices must be handled explicitly. A native JustVoxel implementation using standard NUT interfaces avoids that coupling.

### UPS acceptance criteria

Before the native UPS feature is considered complete, test it on real physical hardware with a supported USB UPS and include at least:

- USB UPS detection/scanning
- NUT driver start and stable communication
- battery charge, runtime, load, voltage, and status reads
- `nut` account/group and configuration-file permission validation
- WebUI rendering through the management API without direct browser shell access
- power-event state changes such as online -> on battery -> online where practical
- service restart recovery
- complete host reboot recovery
- network-bound `upsd` startup where that mode is supported
- zero unexpected failed systemd units after reboot
- no UPS serial numbers, credentials, or deployment-specific private addresses exposed in reusable defaults or logs

## Later: local documentation in CLI and WebUI

JustVoxel documentation should remain available on the appliance even when the Internet is unavailable.

The JustVoxel Base Markdown documentation should remain the authoritative appliance source of truth and continue to ship with the matching Base system image. A future `mjust docs` experience should make the local documentation easy to browse or search from the terminal.

The WebUI should provide a Help or Documentation area that renders those same bundled Markdown documents as a readable local documentation site with normal navigation, headings, links, tables, notes, and warnings rather than exposing raw Markdown text.

Documentation should follow the installed bootc generation so an operating-system update brings the matching documentation and an operating-system rollback returns to the matching documentation. A separate PDF documentation set is not required unless a later use case justifies one.

## Later: operator access

A later WebUI capability should allow limited non-administrator access without turning JustVoxel into a hosting platform.

A simple model is preferred:

- **Administrator** — full appliance control
- **Operator** — safe Minecraft-level operations such as status, players, whitelist, start/restart, and backups

Operator access should not allow storage reconfiguration, OS administration, appliance reset, firmware operations, or host power controls unless explicitly expanded in a future design.

## Later: safe scheduling improvements

Scheduled backups already exist and are not a future feature.

Future scheduling work should focus only on safe appliance-defined maintenance operations where they add clear value, for example:

- scheduled Minecraft restart
- optional maintenance reboot
- update checks
- defined maintenance windows

The normal user interface should stay focused on predefined appliance operations rather than becoming a general-purpose task scheduler.

## Later: plugin management

Plugin management may eventually provide a guided way to discover, install, update, disable, or remove compatible Paper plugins from trusted sources.

Any implementation should preserve the appliance model by checking compatibility where possible, creating appropriate safety backups before risky changes, and avoiding unrelated host-level software installation behavior.

This is intentionally later work because plugin compatibility creates a much larger support and validation surface than the core appliance.

## Nice to have: JustVoxel-aware system rollback

JustVoxel already provides bootc operating-system status and update staging through `mjust`.

A future `mjust` rollback workflow may provide a safer appliance-aware way to return to the previous JustVoxel bootc deployment instead of requiring the user to work directly with low-level bootc rollback operations.

This is deliberately not a near-term priority. A correct rollback design must consider more than selecting an older OS deployment. It must also define how deployment-specific `/etc` state, active JustVoxel configuration, Minecraft data, and recovery/backup boundaries interact so that rolling the operating system backward does not create an inconsistent appliance state.

Until that design exists, bootc-level rollback remains an advanced native-system operation rather than an automated JustVoxel workflow.

## Non-goals

JustVoxel is not intended to become:

- a multi-game hosting panel
- a generic Podman or Docker manager
- a billing or commercial hosting platform
- a multi-node game-server orchestration system
- a general Linux management panel
- a network infrastructure management system

Supporting more features is not automatically an improvement. New capabilities should be added only when they make a dedicated Minecraft appliance safer, easier to operate, easier to migrate, or easier to recover.

## Roadmap rule

Implemented behavior belongs in the operational documentation. Future behavior belongs here.

When a roadmap item becomes implemented and validated, its detailed behavior should move to the appropriate operational document rather than leaving duplicate or stale future descriptions in this file.
