# JustVoxel networking architecture

JustVoxel provides network management through two independent user interfaces:

- `nm-hsp` is the friendly local terminal network manager shipped as a Home Server Package.
- The JustVoxel WebUI uses the privileged JustVoxel Management Agent.

These interfaces may provide similar user-facing capabilities, but they are deliberately not dependencies of one another.

## Independence rule

The WebUI networking implementation must not:

- import `nm-hsp` packages
- execute the `nm-hsp` binary
- parse `nm-hsp` terminal output or `--snapshot` output
- require a particular `nm-hsp` version to be installed

Likewise, `nm-hsp` must not require the JustVoxel WebUI or Management Agent.

This allows `nm-hsp` to be updated, redesigned, or replaced independently without changing the WebUI networking backend. The two implementations share NetworkManager as the system authority, not an application-level protocol between each other.

## Runtime boundary

The supported WebUI path is:

Browser → unprivileged JustVoxel WebUI → Management API over the local Unix socket → privileged JustVoxel Management Agent → NetworkManager system D-Bus

The browser never talks to D-Bus directly and never receives raw D-Bus object paths as identifiers.

The Management Agent owns the JustVoxel networking backend under `management/internal/networking`. That package talks directly to `org.freedesktop.NetworkManager` on the system D-Bus using `github.com/godbus/dbus/v5`.

The networking work is split into independent checkpoints.

### Backend foundation

The Management Agent owns the direct NetworkManager D-Bus implementation and normalized appliance model. This layer remains independent from both the browser UI and `nm-hsp`.

### Read-only WebUI workspace

The Management API and WebUI now expose the read-only network experience, with Wi-Fi scan as the only operation that asks NetworkManager to refresh runtime state. It provides:

- NetworkManager version, overall state, and connectivity state
- networking and Wi-Fi radio state
- Ethernet and Wi-Fi device status
- active runtime IPv4 and IPv6 addresses, gateway, and DNS
- saved non-secret connection-profile metadata
- Ethernet carrier and link speed
- active Wi-Fi SSID, signal, and bitrate
- nearby Wi-Fi networks and security classification
- explicit Wi-Fi scan requests

This checkpoint does not connect or disconnect networks, toggle radios, change IP/DNS/gateway/MTU settings, modify autoconnect, forget profiles, or run repairs. Those configuration-changing actions require later checkpoints and the safety model described below.

### Checkpoint safety layer

The Management Agent now implements the NetworkManager checkpoint transaction layer that future disruptive network mutations must use.

The Management API exposes administrator-only checkpoint lifecycle endpoints for:

- creating a checkpoint for explicit interface names
- reading active checkpoint metadata
- confirming the new state by destroying the checkpoint
- rolling back immediately and reporting per-interface rollback results

The unprivileged WebUI has matching proxy endpoints, but Step 3 intentionally adds no visible checkpoint controls. Future mutation screens will use this transaction layer behind the normal user experience.

Checkpoint behavior is deliberately conservative:

- the default automatic rollback timeout is 90 seconds
- accepted timeouts are 30 through 300 seconds
- a zero timeout from the WebUI/API means use the 90-second JustVoxel default, never an infinite checkpoint
- at least one explicit interface is required; JustVoxel does not create an all-device checkpoint
- JustVoxel uses NetworkManager checkpoint flags value 0, so it does not destroy external checkpoints or opt into overlapping checkpoints
- overlapping JustVoxel transactions on the same interface are rejected
- confirm and rollback terminal actions are serialized so concurrent requests cannot race
- NetworkManager D-Bus checkpoint object paths remain inside the Management Agent; browser/API clients receive an opaque random transaction ID
- transaction creation, confirmation, and rollback are written to the existing audit log

The in-memory JustVoxel transaction map is intentionally not authoritative for rollback safety. NetworkManager owns the actual checkpoint and its automatic rollback timer. If the Management Agent restarts while a checkpoint is pending, the opaque JustVoxel transaction ID is lost and cannot be confirmed afterward, but NetworkManager still retains the checkpoint and automatically rolls it back when its timeout expires. This fails toward restoring connectivity rather than keeping an unconfirmed network change.

### Wi-Fi management

Step 4 adds administrator-only Wi-Fi mutations through the same independent JustVoxel backend:

- turn the NetworkManager Wi-Fi radio on
- turn the Wi-Fi radio off behind a checkpoint that covers all Wi-Fi interfaces
- connect an existing saved Wi-Fi profile by NetworkManager UUID
- create and activate a new Open, OWE, WPA/WPA2 Personal, or WPA3 Personal profile
- join a hidden Open, OWE, WPA/WPA2 Personal, or WPA3 Personal network
- disconnect a Wi-Fi interface
- forget an inactive saved Wi-Fi profile

New WEP and Enterprise credential entry are intentionally unsupported. A previously saved Enterprise profile can still be activated because no new credential is collected by JustVoxel.

Potentially disruptive Wi-Fi actions use this sequence:

1. The browser asks the Management Agent to create the checkpoint first.
2. The browser stores the opaque JustVoxel transaction ID in session storage.
3. Only after the checkpoint is armed does the browser request the Wi-Fi mutation.
4. The workspace shows a "Keep settings" / "Revert now" banner with the rollback countdown.
5. The pending transaction is recovered after a page reload while the browser session remains available.
6. "Keep settings" destroys the checkpoint.
7. "Revert now" rolls back immediately.
8. If the browser loses connectivity and cannot confirm, NetworkManager rolls back automatically at the timeout.

For a newly created Wi-Fi profile, JustVoxel records the new profile UUID in the pending transaction. An explicit JustVoxel rollback first asks NetworkManager to restore connectivity and then deletes that newly created profile. The conservative NetworkManager checkpoint flags remain value 0. Therefore an unattended NetworkManager timeout can restore connectivity while leaving the newly created saved profile behind; JustVoxel deliberately accepts that non-disruptive residue rather than enabling a broad checkpoint flag that could delete unrelated profiles created by another tool during the same window.

An active profile cannot be forgotten directly. The administrator must disconnect it, confirm the disconnected state, and then forget it. This prevents a "forget" action from deleting the profile that NetworkManager would need for rollback.

## Stable identifiers

D-Bus object paths are runtime implementation details and can change.

The WebUI/API boundary should use stable appliance identifiers instead:

- network devices: interface name, for example `enp1s0` or `wlp2s0`
- saved connection profiles: NetworkManager UUID

The backend resolves those identifiers to current NetworkManager objects immediately before an operation.

## Secret handling

Read-only status uses NetworkManager's normal settings and runtime interfaces and does not call `GetSecrets`.

A submitted Wi-Fi password is treated as short-lived request data:

- never place it in command-line arguments
- never log it
- never return it in API responses
- never store an application-side copy
- pass it directly to NetworkManager over D-Bus
- clear the mutable JustVoxel secret buffer after the D-Bus call and remove the PSK from the temporary settings map

NetworkManager requires a Go string at the D-Bus boundary, so JustVoxel does not claim that every runtime string copy can be cryptographically erased.

The existing WebUI transport is plain HTTP on the trusted LAN by default. That is a separate transport-security decision: keeping secrets out of the Management Agent does not encrypt the browser-to-WebUI hop.

## Authorization

The existing Management Agent remains the security boundary.

Read-only networking endpoints should use the same read-access policy as other status surfaces. Configuration-changing actions should initially require the administrator role unless a narrower permission is deliberately approved later.

No browser Polkit agent, sudo bridge, shell wrapper, or additional privileged daemon is required.

## Safe configuration changes

Potentially disruptive network changes must use the implemented NetworkManager checkpoint layer before they are exposed through the WebUI.

The intended mutation transaction is:

1. Create a NetworkManager checkpoint for the affected device or devices.
2. Apply the requested configuration.
3. Let the browser reconnect using the new configuration.
4. Require an explicit confirmation that connectivity is healthy.
5. Destroy the checkpoint after confirmation.
6. If confirmation does not arrive before the timeout, allow NetworkManager to roll back automatically.

This is the safety mechanism for changes such as static IP, gateway, DNS, or other settings that could otherwise lock a remote user out of the appliance.

## Functional target

The WebUI should eventually cover the same normal-user networking jobs that JustVoxel currently exposes through its friendly CLI experience, while remaining an independent implementation:

- status and connection overview
- Ethernet automatic/manual configuration
- DNS, gateway, MTU, and autoconnect
- Wi-Fi radio, scan, connect, disconnect, saved networks, and forget
- safe troubleshooting and guarded repairs
- Tailscale and NetBird status/normal controls when those providers are installed

Tailscale and NetBird are not part of NetworkManager's core D-Bus backend. Their WebUI support should use separate JustVoxel provider adapters behind the Management Agent rather than creating a dependency on `nm-hsp`.

## Development rule

Behavior may be compared with `nm-hsp` as a user-experience reference, but source-level or release-level coupling is not allowed. Each implementation must remain independently buildable, testable, releasable, and replaceable.
