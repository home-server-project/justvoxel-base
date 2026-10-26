# JustVoxel management model

JustVoxel is designed to be easy to operate as an appliance without hiding the underlying Linux system from administrators who want direct control.

The management interfaces are layers over the same AlmaLinux 10 bootc host and Minecraft runtime. None of them turns JustVoxel into a closed control plane.

## Management surfaces

JustVoxel can be administered in four ways:

1. **WebUI** — browser-based management for users who prefer a graphical interface.
2. **Interactive `mjust` menu** — terminal UI with descriptions, selectors, and guarded workflows.
3. **Direct `mjust` commands** — the same operations can be called directly without opening the menu.
4. **Native Linux administration** — advanced users can use standard tools such as `systemctl`, `journalctl`, `podman`, `bootc`, `nmcli`, `firewall-cmd`, `findmnt`, and `lsblk` directly over SSH or the local console.

A typical supported appliance relationship is:

```text
WebUI --------\
               -> JustVoxel Management API -> Management Agent -> shared appliance implementation
mJust --------/
                                                         |
                              systemd / Podman / bootc / NetworkManager / firewalld
                                                         |
                                              AlmaLinux 10 bootc host
```

The Management API and Agent are the authoritative appliance-management layer
for migrated capabilities. WebUI and mJust are separate user interfaces over
that same implementation rather than separate management engines.

This diagram describes the normal supported appliance flow, not a restriction.
An administrator may still use appropriate native Linux tools directly for
inspection, troubleshooting, or deliberate advanced administration.

## `mjust` is a convenience and safety layer

Running `mjust` with no arguments opens the interactive JustVoxel terminal interface. The menu explains operations and dispatches the same direct recipes that are available from the shell.

Examples:

```text
mjust status
mjust players
mjust backup
mjust restore
mjust update-minecraft
mjust os-status
mjust os-update
mjust resources
mjust validate
```

The interactive menu and direct `mjust` commands are not separate appliance
implementations. For capabilities migrated to the Management API, direct
`mjust` commands are thin terminal frontends over the same Management Agent
used by WebUI. They remain suitable entry points for normal SSH administration,
documentation, automation, and troubleshooting, but they do not own duplicate
business logic or safety policy.

Administrator-only `GET /v1/admin/validation` combines the default host-only
`validate-backend` result with independent checks of every registered modern
immutable instance. Registry and committed runtime coverage must agree; retained
`minecraft.json` compatibility identity is excluded. Zero modern instances is
valid. Stopped servers are structurally checked without starting them; running
servers receive `verify-minecraft-stack --instance <id> <version>`. Storage,
SELinux contexts, owned firewall rules and backup settings/status/timer/destination
checks are read-only. The appliance-wide deadline is ten minutes. Expected
problems return HTTP 200 with `ok=false` and `exit_code=1`; infrastructure failures
return sanitized HTTP 503. Historical `validate-backend --first-run` recovery
keeps its fixed-runtime contract and is never part of normal validation.

## Native Linux administration remains available

JustVoxel is still a normal immutable Linux server.

Advanced administrators may inspect or operate the machine directly with normal system tools. Examples include:

```text
systemctl status minecraft-jv-a1b2c3.service
journalctl -u minecraft-jv-a1b2c3.service
podman ps
podman image ls
bootc status
nmcli
firewall-cmd --list-all
findmnt
lsblk
```

JustVoxel does not require administrators to enter a proprietary shell and does not disable ordinary host administration.

The supported host-management model is bootc. `rpm-ostree` may exist in build plumbing, but JustVoxel does not use host-side rpm-ostree layering as its administration model.

## Configuration ownership

The bootc image owns immutable implementation and templates under locations such as:

- `/usr/share/justvoxel/`
- `/usr/libexec/justvoxel/`
- `/usr/lib/justvoxel/`

Administrator/runtime configuration lives under `/etc`, including appliance-wide
policy and per-instance records such as:

- `/etc/justvoxel/justvoxel.conf`
- `/etc/justvoxel/instances/<id>/config.json`
- `/etc/justvoxel/instances/<id>/minecraft.env`
- `/etc/containers/systemd/minecraft-<id>.container`
- per-instance backup service and timer units

Persistent application and management state lives under `/var`.

Management journals and human-readable diagnostic logs use one persistent hierarchy:

```text
/var/lib/justvoxel/management/
├── logs/
│   ├── operations/<operation-id>.json
│   ├── setup-logs/<operation-id>.log
│   ├── migration-logs/<operation-id>.log
│   ├── restore-logs/<operation-id>.log
│   ├── reset-logs/<operation-id>.log
│   └── support/
├── setup.lock
├── restore.lock
├── data-migration.lock
└── server-migration.lock
```

Operation journals remain machine-readable metadata in `operations/`; System → Logs does not list their raw JSON. Setup logs use `setup-logs/`. Data Migration, Server Migration Export, Import, and Recovery use `migration-logs/`. Restore uses `restore-logs/`. Minecraft and factory resets use `reset-logs/`. Administrators can generate on-demand local support reports through `POST /v1/admin/logs/support`. Reports remain in the private `logs/support/<uuid>.log` directory until normal appliance log/state cleanup removes them, and use the existing diagnostic list/read/download API without an operation journal. Each report is capped at 256 KiB and includes release metadata, fixed Management/WebUI service properties, socket and appliance path metadata, up to 300 Management and 150 WebUI current-boot journal lines, and a metadata-only management inventory (depth 2, at most 200 entries, no symlink traversal). Command sections are capped at 64 KiB, lines at 2048 bytes, and commands at four seconds each. Output is sanitized line by line using Setup hostname/IP redaction and sensitive-family redaction; configuration and environment contents are excluded. Reports are root-owned regular files with mode 0600 in the existing private directory. Nothing is automatically uploaded. Logs and journals remain root-owned, with private directories and files. Server Import records bounded, redacted backend output alongside its lifecycle and rollback evidence.

Administrators can select Setup, Migration, Restore, Reset, or Support in System → Logs, then read and download a file through the Management API's allowlisted `/v1/admin/logs` endpoints. The WebUI does not accept filesystem paths or send reports externally.

First-run Setup (Recommended and Advanced) and both Factory Reset progress views offer **Live Console**, a movable, non-modal, read-only redacted execution trace of supported operations (`setup`, `minecraft_reset`, and `factory_reset`). While open, it refreshes about every 2.5 seconds. Closing it stops reads; reopening reads a fresh copy. Scrolling up pauses automatic following until you return near the bottom. Temporary read failures preserve the displayed transcript and show **Reconnecting…**. The console starts closed after a page reload. Setup completion leaves it open for inspection; Open Dashboard and successful resets close it. The Administrator-only trace is separate from System → Logs, which retains persistent forensic diagnostics and Setup Log downloads. Trace commands come only from authoritative Agent/helper execution. The Agent sanitizes and bounds inherited-FD trace records before storing them privately; secrets, stdin/request JSON, and environment contents are never exposed. Live Console has no command input and provides no shell.

System → Logs → Failed units is a live diagnostic view, separate from persistent operation logs. Refresh re-queries the current failed units. Selecting a unit reads its last 60 journal lines from the current boot; Download log retrieves the same bounded view. Only administrators can use this view, and the Management Agent verifies that the unit is still failed before reading its journal. Enumeration failures appear as unavailable rather than an empty list.

The System → Date & Time tab changes the host timezone, automatic time synchronization, and (when automatic time is off) the system clock. Its administrator-only WebUI route uses CSRF protection and calls the Management Agent over the local Unix socket. The agent validates the timezone against the host database and passes fixed arguments to `timedatectl`. The city search uses the same local timezone list as First Setup. The same Administrator-only change route saves the appliance-wide 12/24-hour and date-order display profile in the existing WebUI database. An additive singleton-profile migration preserves existing installations and defaults to 12-hour time and Month first. Every authenticated WebUI role receives only the two display format values through the shared session response; changing display format does not change the host clock.

Minecraft Settings contains the selected instance's memory, players, whitelist,
gameplay, and applicable cross-play controls. The separate Version workspace shows
the installed software, container channel, version policy, available versions, and
software-specific compatibility information. Current software availability and
capability boundaries are documented in [server-software.md](server-software.md).
Version changes use the reviewed maintenance path; an incompatible cross-play
choice blocks the proposal until the administrator changes it explicitly.

On Management Agent startup, legacy `management/operations/` and `management/setup-logs/` directories are migrated into the `logs/` hierarchy without overwriting existing data. Existing `logs/operation-logs/<operation-type>/` files are moved into the matching category directory after bounded preflight checks. Conflicting non-empty directories or file names stop startup rather than overwrite logs.

Bootc image updates may update the immutable implementation, but JustVoxel does not silently replace the active administrator configuration in `/etc` as part of an ordinary image update.

## Manual edits and generated files

Advanced users are free to inspect and manually edit the host, including the rendered Minecraft Quadlet. However, some active files are generated by JustVoxel from its recorded configuration.

For example:

```text
/etc/containers/systemd/minecraft-<id>.container
```

may be regenerated by operations such as `mjust configure --instance <id>`,
storage migration, or another workflow that intentionally renders the runtime
again. The instance's `config.json` is authoritative; a friendly name is never a
filesystem or service identity.

Therefore:

- direct Linux administration is supported;
- manual inspection and troubleshooting are supported;
- manual customization is possible;
- arbitrary edits to files owned by a JustVoxel render workflow are not guaranteed to survive a later re-render.

Administrators who intentionally diverge from the supported generated configuration should treat those changes as their own advanced configuration management responsibility.

## Safety lives below the frontends

Important appliance safety checks belong behind the Management API in the
Management Agent and shared backend implementation rather than only in the
interactive menu, direct mJust command, or WebUI. Calling a direct mJust command
does not bypass the intended protections.

Examples include:

- player-aware interruption checks;
- graceful Minecraft shutdown;
- the shared Minecraft maintenance lock;
- backup mount/source identity validation;
- exact typed confirmations for destructive storage actions;
- exact `RESTORE` confirmation for restore operations;
- restore staging and pre-restore safety copies;
- SELinux relabeling and runtime validation;
- fail-closed behavior when critical state cannot be confirmed.

WebUI and mJust should call the same management capabilities rather than create
independent definitions of appliance behavior.

## Web management architecture

The browser-facing WebUI is intentionally unprivileged. It communicates with the privileged JustVoxel Management Agent over a local Unix socket. The WebUI process does not read `/etc/shadow` and does not call PAM directly.

The Management API is versioned as `v1`. Its default authentication provider is the AlmaLinux/RHEL system account. The administrator identity is `voxel`.

In default **System account** mode, the trust path is:

```text
Browser
  |
Unprivileged JustVoxel WebUI
  |
Unix socket
  |
Privileged JustVoxel Management Agent
  |
PAM / authselect-managed AlmaLinux system stack
  |
local voxel account
```

The Management Agent authenticates the submitted credential once through PAM, detects an expired password through PAM account management, and then issues the normal JustVoxel WebUI session token. Normal WebUI requests use the session token rather than repeatedly transmitting the system password.

System password changes are performed through PAM and change the real Linux `voxel` credential. A successful password change invalidates active WebUI sessions.

The optional **Separate WebUI password** provider uses a WebUI-local Argon2 credential for the `voxel` browser identity. It is a separate provider, not password synchronization. Switching providers requires confirmation with the real system password and invalidates all WebUI sessions.

Persistent WebUI authentication state is stored under `/var/lib/justvoxel/webui`. A missing authentication-mode state means System account mode, which keeps ordinary bootc updates/rebases safe for already-configured machines.

Full Factory Reset is appliance-wide: it retires every authoritative managed Minecraft instance, including stopped instances, regardless of the selected server. Review binds the complete instance set; a changed scope requires another review. Running instances use player-aware graceful shutdown. Internal managed worlds and internal configured backups are deleted; external/network data and unrelated storage remain preserved. Interrupted instance cleanup retains private identity evidence for Review and retry; a partially erased runtime cannot be dismissed as usable current state. Legacy interrupted resets without ownership evidence do not guess a server for destructive retry. Start Over is a separate terminal workflow and is not this operation. See [the Factory Reset convergence audit](MINECRAFT-CONVERGENCE-C3-FACTORY-RESET.md) for its exact destructive and recovery boundaries.

Full Factory Reset removes appliance-owned state and restores System account authentication. It resets the real Linux `voxel` account to the same `voxel` / `voxel` bootstrap credential used by the installer and expires that password immediately. A successful reset invalidates existing WebUI sessions and clears authentication failure and lock state. The next authentication with `voxel` / `voxel` requires the administrator to replace the bootstrap password through the existing forced password-change flow. The replacement becomes the real system-wide `voxel` password for WebUI, console, and SSH password authentication when enabled.

Web management can be enabled, disabled, inspected, and recovered through:

```text
mjust web status
mjust web enable
mjust web disable
mjust web password-reset
```

`mjust web password-reset` is provider-aware: System account mode uses the normal Linux `voxel` password recovery path; Separate mode resets only the local WebUI credential.

The default WebUI is intended for trusted local-network administration. See [WEBUI.md](WEBUI.md) for the current access and security model.

## Choosing an interface

For a family/home user, the WebUI or interactive `mjust` menu should usually be the simplest path.

For an administrator who knows the command they want, direct `mjust` commands avoid the menu while keeping JustVoxel's safety model.

For Linux troubleshooting or deliberate customization, normal host tools remain available.

The design goal is simple: **JustVoxel behaves like an appliance when that is useful, and like a normal immutable Linux server when an administrator needs deeper control.**

### Appliance-wide physical data migration

Physical migration stays under `/v1/admin/data-migration` (discover, plan, apply,
current-operation) and the existing persistent-operation status endpoints. It has
no selected-instance route or browser-supplied unit. The Plan fingerprint commits
the complete authoritative participant set, its runtime configuration identities,
and original runtime states. Root-only `data-migration-runtime` Agent commands
reuse the instance registry/runtime implementation inside the storage transaction.

`POST /v1/admin/data-migration/recover` accepts only `operation_id` and explicit
`confirm_recovery: true`. It retries rollback for the current `needs_attention`
operation using private immutable participant evidence. Legacy identity-less
journals remain read-only. [Storage](STORAGE.md#minecraft-data-migration) describes
state restoration, startup protection, and recovery behavior.


## File Browser service

The primary-administrator-only `/v1/admin/filebrowser` API reads availability and runtime readiness. POST actions `/availability`, `/start`, `/stop`, and `/refresh` use the existing privileged Unix-socket boundary. Availability is one SQLite enum (`always` or `on-demand`) in the existing Management Agent WebUI store. Saves are idempotent; retrying a failed save reapplies service enable/start or disable. Stop rereads the setting and leaves Always available running.

`justvoxel-filebrowser.service` runs the application container `ghcr.io/gtsteffaniak/filebrowser:stable-slim` directly by its moving channel tag. Its port is published only at `127.0.0.1:61209`; no firewall rule is added. Every `/files/` request and FileBrowser Management API operation requires an authoritative session with username `voxel` (`systemAdminUsername`) and role `administrator`; administrator role alone is insufficient. The reverse proxy replaces trusted identity/forwarding headers and injects the authenticated username as `X-JustVoxel-User`. FileBrowser uses supported proxy authentication, with password login disabled. Current Quantum configuration places login method and settings restrictions under `userDefaults.account`, equivalent to the former frontend options. Authenticated `/files/` and `/terminal/` responses permit same-origin framing; ordinary routes retain their framing denial.

Packaged configuration lives under `/usr/share/justvoxel/filebrowser/`; the service generates its sources into `/run/justvoxel-filebrowser/config.yaml`. Files automatically exposes actual mounted non-system local filesystems using Storage's authoritative device discovery, and mounted NFS/NFS4 and SMB/CIFS filesystems using the host mount table. It does not guess mount locations or expose system disk filesystems as general sources. Local mounts must match Storage's authoritative major:minor device number and filesystem type. Each local filesystem has one canonical mountpoint exposing its complete filesystem root (shortest path depth, then lexical order); devices with only subtree mounts are skipped. Stacked mountpoints are skipped without hiding other valid filesystems, and the host root mountpoint is never a general source. Network exports may expose an exported subtree. Configured Minecraft data and available backup roots remain exact dedicated sources only when they are not already reachable through an exposed filesystem; their existing safe-root validation remains in force.

Every filesystem bind uses its exact mountpoint and is nonrecursive, without propagation. The agent rechecks mount identity and rejects missing mounts, replacements, or symlink substitution immediately before starting Podman. Host read-only mounts and local devices marked read-only are read-only in both the container and Quantum permissions. All sources are private and ignore symlinks. Files Refresh performs a managed restart of only `justvoxel-filebrowser.service`, regenerates sources, and waits for readiness in either availability mode. There is no background mount scanner. Mount/unmount and filesystem capacity/usage remain Storage's responsibility; Quantum's source usage bars are disabled while source information remains available. The named Podman volume `justvoxel-filebrowser-state` preserves FileBrowser's database and cache across stop/start and container recreation, separately from packaged configuration.

The image's non-root account (1000:1000) runs with no-new-privileges and all capabilities dropped except `DAC_OVERRIDE`: JustVoxel backup directories and archives are root-owned with modes 0700 and 0600. SELinux label separation is disabled only for this FileBrowser container to access approved data already privately labeled for Minecraft. No host labels, contexts, policy, ownership, or modes are changed.

Schema reference: [Quantum stable configuration](https://github.com/gtsteffaniak/filebrowser/blob/v2.0.0-stable/frontend/public/config.generated.yaml), [proxy authentication](https://filebrowserquantum.com/en/docs/configuration/authentication/proxy/).


## Native Web Terminal

The standalone **Control Center → System → Terminal** workspace uses the native EPEL 10 `ttyd` package, installed through `JUSTVOXEL_COMMON_PACKAGES` during image composition. The existing finalization policy disables EPEL on deployed hosts; no runtime package installation is used. `justvoxel-terminal.service` runs `/usr/bin/ttyd` as `voxel:voxel` in `/home/voxel` with a private umask and the fixed `/bin/bash --login` command. It uses `/run/justvoxel-terminal/ttyd.sock` in a private systemd runtime directory owned by `voxel:voxel` with mode `0700`, exposes no TCP listener, and runs with base path `/terminal`, writable input, origin checks, and exit after the last WebSocket disconnect. It is never enabled at boot.

The privileged Unix-socket API exposes `GET /v1/admin/terminal` and `POST /v1/admin/terminal/start` and `/stop`. All require username `systemAdminUsername` (`voxel`) and role `administrator`. Start/stop accept no operation parameters and control only `justvoxel-terminal.service`; there are no command, user, unit, environment, path, or port options. Status returns the systemd state plus readiness from one bounded HTTP probe of `/terminal/` through the fixed private Unix socket with a fixed Host and the trusted proxy header. No Terminal settings are persisted.

The unprivileged WebUI calls these operations through `/api/system/workspace/terminal` and its `/start` and `/stop` routes; mutations require normal CSRF validation. Every `/terminal/` request, including WebSocket upgrades, validates the real JustVoxel session and rejects password-change-required sessions and secondary administrators. That session is the authentication boundary. The WebUI cannot traverse the private runtime directory or connect directly to ttyd. It forwards Terminal HTTP and WebSocket traffic through its existing authenticated Management Agent Unix-socket transport to `/v1/admin/terminal/proxy/`. The root Management Agent is the only ttyd proxy; both WebUI and Agent enforce the primary `voxel` administrator boundary. Only the Agent injects `X-JustVoxel-Terminal: 1`, after removing Management bearer credentials and browser-supplied headers. The header is defense-in-depth: [ttyd 1.7.7 proxy authentication checks header presence](https://github.com/tsl0922/ttyd/blob/1.7.7/src/http.c), not a password. Browser-supplied identity/forwarding headers and cookies are stripped, and the original public Host is preserved for ttyd origin validation. There is no second login.

Only authenticated `/terminal/` responses receive same-origin framing and the inline script/style allowance required by ttyd's bundled page. Ordinary routes retain `X-Frame-Options: DENY` and `frame-ancestors 'none'`. Terminal is always on demand: closing the Terminal workspace ends the on-demand session, removes its frame and requests a stop; last-client disconnect is the fallback if the browser disappears. Startup polling is bounded to the explicit startup wait. Source coverage does not establish live image, runtime, or visual acceptance.
