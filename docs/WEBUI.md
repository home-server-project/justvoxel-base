# Web management

The WebUI source and the privileged JustVoxel Management Agent source are maintained together in `justvoxel-base` and are built/tested from the same appliance source commit.

This source consolidation does not change the runtime security model. The browser-facing WebUI remains unprivileged and communicates through Management API v1 over the local Unix socket with the privileged Management Agent.

JustVoxel WebUI is designed for simple administration from a trusted local home network.

By default, Web management uses plain HTTP on TCP port `8099`. This is intentional: the appliance can show both a friendly local address such as `http://justvoxel.local:8099` and a direct address such as `http://192.168.1.50:8099` without requiring a private certificate or a self-signed certificate exception.

Because default local WebUI traffic is not protected by TLS, administrator credentials and sessions should only be used on a network you trust. Do not forward TCP port `8099` directly to the public Internet.

If remote access is required, place JustVoxel behind a properly secured solution that provides trusted HTTPS or private-network access, such as an appropriately configured reverse proxy, VPN, Tailscale, or similar protected access. Those advanced access methods are separate from the default JustVoxel local WebUI.

## Administrator account

The default authentication mode is **System account**.

The administrator username is:

`voxel`

In System account mode, the WebUI authenticates the real local `voxel` account through the AlmaLinux/RHEL PAM stack. JustVoxel does not copy `/etc/shadow`, expose password hashes to the WebUI, or maintain a synchronized second password database.

The same `voxel` password is therefore used for:

- JustVoxel WebUI
- local console login
- SSH password login when SSH password authentication is enabled

The browser-facing WebUI remains unprivileged. It sends the submitted login credential over the local Unix socket to the privileged JustVoxel Management Agent. The agent performs PAM authentication and returns a WebUI session token. The browser does not repeatedly send the Linux password for normal management operations.

## First login from the JustVoxel ISO

The JustVoxel ISO may install the initial development/bootstrap account as `voxel` with the known bootstrap password. The ISO installer expires that password before first boot.

On first use:

1. Sign in as `voxel` with the bootstrap password.
2. JustVoxel reports that the administrator password must be changed.
3. The WebUI allows only the password-change flow.
4. PAM changes the real Linux `voxel` password using the host's normal password policy.
5. Existing WebUI sessions are invalidated and the user signs in again with the new password.

This expiration is performed by the JustVoxel ISO installation path only. A bootc update/rebase or custom installation does not blindly expire or replace an existing `voxel` password.

## Password policy

JustVoxel does not impose its own uppercase/lowercase/digit/symbol formula or a separate hard-coded password length.

The Management Agent reads the host's current libpwquality/PAM policy and the WebUI displays the effective minimum length. PAM remains authoritative when the password is actually changed.

The privileged Management Agent intentionally uses systemd `ProtectSystem=true` rather than `full` or `strict`. System-account password changes are handled by PAM/pam_unix, which must create lock and temporary files and atomically update `/etc/shadow`. Making `/etc` read-only prevents that normal password-update transaction. The other management-service hardening controls remain enabled.

## Optional Separate WebUI password

An administrator can deliberately switch to **Separate WebUI password** mode from WebUI Authentication settings.

In this mode:

- the username remains `voxel`;
- browser authentication uses a WebUI-local Argon2 credential;
- the Linux `voxel` password is unchanged;
- console and SSH continue using the Linux password;
- the system password no longer authenticates the WebUI while Separate mode is active.

Switching from System account to Separate WebUI password requires confirmation with the current real system password and a new WebUI password entered twice. Switching back to System account requires the real system password. Both transitions invalidate existing WebUI sessions and require sign-in again.

The local credential store is structured so future WebUI-only Operator/Viewer identities can be added without automatically creating Linux system users. Those roles are not implemented yet.

## Recovery

`mjust web password-reset` is mode-aware.

In **System account** mode, recovery changes the real local `voxel` password using the normal system `passwd` mechanism and then invalidates WebUI sessions.

In **Separate WebUI password** mode, recovery resets only the WebUI-local credential. The Linux `voxel` password is not modified.

## System validation

Administrator users can open **Administration -> Validation** to run the same authoritative appliance validation used by `mjust validate`.

The WebUI calls `GET /v1/admin/validation` through the local Management API. It does not run systemd, Podman, RCON, firewall, storage, or other validation checks directly. The Management Agent executes the shared validation backend and returns its result.

The page deliberately distinguishes three outcomes:

- validation passed;
- validation completed and the backend detected one or more problems;
- the validation service/API was unavailable and no validation result was produced.

For troubleshooting, the backend validation output is displayed without the WebUI independently reclassifying individual checks. The Validation page remains Administrator-only.

## Minecraft Restore

Administrator users can open **Storage & Backups -> Restore** for the same two Restore modes exposed by mJust: world Restore and full Minecraft-data Restore.

The WebUI uses the existing Management API for completed-backup discovery, authoritative Restore planning, plan-fingerprint revalidation, apply, persistent operation tracking, runtime validation, and rollback. Compatibility and safety policy remain in the Management Agent/shared backend.

The browser presents Agent warnings and confirmation requirements, requires the explicit destructive confirmation `RESTORE`, and requires a separate online-player interruption confirmation when the Agent says it is needed. Browser refresh or reconnect resumes the same persistent Restore operation.

## Local behavior

When Web management is enabled and healthy, the console/SSH welcome message reports `Web interface: Ready` and shows the friendly `hostname.local:8099` address plus the direct IPv4 address. The live health check takes precedence over bootstrap marker timing so a healthy listener is not reported as merely starting.

When Web management is disabled, the WebUI service is stopped and its firewalld service is removed, so the management port is no longer exposed.

The WebUI keeps session expiration, login throttling, logout, CSRF protection, the Unix-socket trust boundary, and the unprivileged browser-facing service regardless of authentication provider.
