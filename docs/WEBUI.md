# Web management

The WebUI source and the privileged JustVoxel Management Agent source are maintained together in `justvoxel-base` and are built/tested from the same appliance source commit.

This source consolidation does not change the runtime security model. The browser-facing WebUI remains unprivileged and communicates through Management API v1 over the local Unix socket with the privileged Management Agent.

JustVoxel WebUI is designed for simple administration from a trusted local home network.

By default, Web management uses plain HTTP on TCP port `8099`. This is intentional: the appliance can show both a friendly local address such as `http://justvoxel.local:8099` and a direct address such as `http://192.168.1.50:8099` without requiring a private certificate or a self-signed certificate exception.

Because default local WebUI traffic is not protected by TLS, administrator credentials and sessions should only be used on a network you trust. Do not forward TCP port `8099` directly to the public Internet.

If remote access is required, place JustVoxel behind a properly secured solution that provides trusted HTTPS or private-network access, such as an appropriately configured reverse proxy, VPN, Tailscale, or similar protected access. Those advanced access methods are separate from the default JustVoxel local WebUI.

In the Minecraft Servers dashboard workspace, **Add Minecraft Server** opens the maintained first-run Setup flow (`/setup`) until appliance configuration is complete. After configuration it opens Add Server (`/minecraft/servers/add`). A bookmarked Add Server GET also redirects to Setup on an unconfigured appliance; stale Add Server POST requests are refused without starting first-run mutations.

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

## First-run setup

The Storage and Backups setup steps open the same disk and partition browser as Control Center Storage. Partition details offer the existing eligible format, delete, temporary mount, and permanent mount actions. Unallocated-space details offer partition creation with a chosen size. Destructive changes require a reviewed target, the confirmation slider, and the final confirmation switch; choosing a setup destination alone performs no disk operation. After changes, discovery refreshes without leaving the wizard or clearing entered values. Use **Choose for setup** on an eligible filesystem, or select it in the refreshed wizard list. Existing setup validation and the final setup transaction still apply. Live Minecraft migration and backup destination configuration remain Control Center operations; the wizard records those destinations in its setup draft.

The dashboard shows live Minecraft controls and players after configuration. Before configuration, it presents a centered setup invitation. Quick Look in the header holds compact status values. Control Center launches the Minecraft, System, Network, System Monitor, System Update, Storage, Backups, Migration, and Files workspaces. Older page URLs with workspace replacements open the matching dashboard workspace. In-progress operation and recovery pages remain available, and Operators can still start a manual backup from Quick Look.

The floating top panel shows date and time in the appliance timezone. Timezone, automatic time (NTP), and manual Date/Time are host system settings. The 12-hour or 24-hour time format and Day first or Month first date order are appliance-wide JustVoxel WebUI settings, persisted by the Management Agent and shared by all WebUI clients across logins and reboots. Topbar dates use abbreviated text months. Display formats do not change the host clock, schedules, or stored timestamps. System contains Health, History, Users, Security, Logs, Date & Time, Wallpaper, Factory Reset, and About; UPS remains conditional on availability. Only Administrators can change Date & Time settings; every authenticated role receives the saved display format. Wallpaper settings remain local to this browser, and the footer Wallpaper link opens the same System tab.

Network keeps loading and errors below its navigation. Troubleshoot groups each interface with its connection, gateway, DNS, and reconnect action. Tailscale and NetBird activation shows service progress and opens their dashboard after the service is running; use the dashboard link if the browser blocks opening. Account setup remains separate from service activation. Playit retains its dark waiting page and validated claim-link flow. Storage refresh keeps the selected physical drive when it is still available.


The Advanced wizard uses six equal Server, Cross-play, Memory, Storage, Backups and Review navigation cards in one row below its title on desktop. Storage and local backups share the Control Center disk and partition browser and reviewed management actions: choose a physical drive to show only its partitions. Preparation opens a focused dialog with the existing destructive slider and confirmation switch, then refreshes the inventory and returns to that drive when available. Server software cards share local artwork from `webui/internal/server/static/server-software/`; its README records sources, licenses and the pending official Paper asset.

The Administrator can choose Recommended setup or follow the six-step Advanced wizard: Server, Cross-play (including version and updates), Memory, Storage, Backups and Review. Progress cards only navigate backward, even after a later step has been visited; Continue validates each form before advancing. Drafts retain their choices when navigating back. Storage and local backup selection reuse the maintained Control Center Storage presentation, dialogs and reviewed actions. Storage and local backup destinations require an explicit partition selection; disk preparation remains a separate reviewed action. When data and backups use the same filesystem, JustVoxel mounts it once and uses separate directories. Review shows the four configuration summary cards and authoritative warnings. The Download configuration action provides a technical snapshot.

First-run WebUI setup generates the welcome message as `JustVoxel <software> Minecraft <resolved-version> Server` using the validated plan’s Paper, Purpur, or Vanilla version. Advanced setup allows a custom message; entering one disables the automatic default and preserves the message through software and version changes. Review shows the final message. After setup, Minecraft settings can still edit it; updates and software changes do not regenerate it, and existing servers are unchanged.

Advanced setup lets the Administrator choose Survival, Creative, Adventure, or Spectator as the game mode. Recommended setup uses Survival. The choice appears in Review and the downloadable configuration. After setup, the Administrator can change Game mode in Minecraft settings through the normal review and apply flow. Minecraft must be restarted for the change to take effect; changes other than memory settings are saved for a later restart.

In Advanced setup, Step 2 Cross-play includes Server update channel and the Minecraft version policy as separate controls. An administrator sees the exact server version selected by Recommended or Latest before continuing. Paper Recommended selects the newest stable Paper build, or the stable version supported by Geyser/Floodgate when Bedrock is enabled. Purpur uses its current API version, or the exact Geyser-supported version when available. Vanilla uses Mojang releases and has no managed Bedrock cross-play. Latest shows the version available now and follows newer versions at future starts. Specific version validates availability for the selected software; Paper previews identify pre-release builds. When Bedrock is enabled, an incompatible Latest or Specific version blocks Continue and setup planning; JustVoxel keeps the selected version and Bedrock choice intact. Review shows the resolved version and the policy chosen.

Selecting **Configure JustVoxel** requests an SMB password in a modal when the backup destination requires one, then opens the Minecraft EULA dialog if acceptance has not yet been recorded for the exact validated plan. **Accept and continue** records acceptance for that plan and starts the existing setup operation. **Decline** and **Cancel** return to Review without starting setup. A changed plan requires fresh acceptance. The SMB password remains outside the draft and operation journal.

The progress page follows the persistent setup operation through Storage, Configuration, Minecraft, Verification, and Complete. During Minecraft startup it explains that downloads and initialization can take several minutes. Refreshing or reopening the page reconnects to the same operation.

First-run startup keeps its 15-minute allowance for downloads and initialization, but three automatic Minecraft restarts during that start fail verification and enter rollback. Paper/Purpur version checks retry the temporary “Checking version, please wait...” response every three seconds for up to one minute; software, game version, and plugin verification remain required before backup timer activation.

A failed first-run rollback uses the Reset Minecraft cleanup helpers to remove generated data only when the transaction recorded an empty appliance-owned internal data directory before runtime configuration. It rechecks directory and mount identity before cleanup. Pre-existing data, external/network/unknown storage, and backups are preserved. Symlinks, nested mounts, unsafe paths, or changed storage identity prevent deletion; an eligible cleanup that cannot complete safely requires attention.

If setup needs attention during storage rollback, the Administrator can select **Retry recovery** on that operation's progress page. The Management Agent checks the preserved storage transaction before restoring any remaining changes. **Start setup over** stays disabled until recovery succeeds and the operation reaches **Rolled back**. Other attention stages do not offer this retry.

## Server software and container updates

In Version → Server Software, **Current** always identifies the installed software. Selecting Paper or Purpur moves the green frame to the review target; Cancel restores it to the installed server. Vanilla is informational: selecting it explains that switching requires Reset Minecraft and setup again.

Software & container updates reviews Paper/Purpur and the Stable, Latest, or Custom container channel together. Custom input is enabled only for Custom. **Check for updates** is read-only; **Review changes** resolves both proposed selections without saving configuration. One confirmation switch and **Apply changes** submit one persistent server update bound to the reviewed selections and image identity. Runtime changes use the existing cold backup, player-aware shutdown, pull and digest checks, runtime verification, and rollback of both selections on failure. Administrator-configured backup retention and one-generation rollback-image cleanup remain unchanged. A channel-only change using the verified identical image preserves Minecraft's running or stopped state without downtime. Incomplete recovery requires attention through the existing operation controls. Changes and failed updates are never automatically retried.

Minecraft memory settings retain a hard minimum of 1 GiB (1024 MiB) outside Minecraft. The recommended headroom warning appears from 1024 through 1535 MiB; at least 1.5 GiB (1536 MiB) clears that warning.

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

WebUI-only Operator and Viewer identities do not create Linux system users. In **System → Users**, the Administrator opens an account’s actions menu to change its role or password, reset its restart or backup allowance, enable or disable it, or delete it. Each dialog names the selected account. Disabling or deleting an account requires sliding to the end and enabling the red confirmation toggle.

## Recovery

`mjust web password-reset` is mode-aware.

In **System account** mode, recovery changes the real local `voxel` password using the normal system `passwd` mechanism and then invalidates WebUI sessions.

In **Separate WebUI password** mode, recovery resets only the WebUI-local credential. The Linux `voxel` password is not modified.

## System validation

Administrator users can open **System -> Validation** to run the same authoritative appliance validation used by `mjust validate`.

The WebUI calls `GET /v1/admin/validation` through the local Management API. It does not run systemd, Podman, RCON, firewall, storage, or other validation checks directly. The Management Agent checks the host once and every modern immutable Minecraft instance independently. Zero servers is valid if host checks pass. Stopped servers receive structural validation without being started; running servers also receive live instance-aware stack verification. The report includes each server’s display name and immutable ID. The operation has a ten-minute appliance-wide deadline, with a slightly longer WebUI API timeout.

The default host helper does not inspect fixed Minecraft runtime files. `validate-backend --first-run` remains only for historical fixed-runtime Setup recovery, outside normal validation.

The page deliberately distinguishes three outcomes:

- validation passed;
- validation completed and the backend detected one or more problems;
- the validation service/API was unavailable and no validation result was produced.

For troubleshooting, the backend validation output is displayed without the WebUI independently reclassifying individual checks. The Validation page remains Administrator-only.

## Minecraft data storage migration

Administrator users can open **Storage & Backups -> Minecraft data storage** to move the active Minecraft persistent-data directory to another supported local disk or partition.

The WebUI is a thin frontend over the same persistent Minecraft data migration Management API used by `mjust storage-migrate`. Candidate discovery, target safety, authoritative planning, plan fingerprinting, destructive confirmation requirements, player state, pre-migration cold backup, copy and verification, configuration switching, SELinux/runtime regeneration, validation, rollback, and recovery state remain owned by the Management Agent/shared backend.

The browser shows Agent-discovered choices for a dedicated disk/USB device, an existing XFS/ext4/Btrfs filesystem, a blank partition, or already-unallocated disk space. Review displays the Agent warnings and requirements. A migration requires the explicit `MIGRATE` frontend confirmation; destructive target preparation additionally requires the exact phrase supplied by the Agent, and online-player interruption requires a separate confirmation when the Agent says it is needed.

Migration runs as a persistent operation. Refreshing or reopening the migration entry point reconnects to the same operation. Successful migration retains the old Minecraft data for administrator verification; a validated rollback keeps the original configuration/runtime active; `needs_attention` preserves recovery state and prevents treating the migration as safely complete.

## Minecraft Restore

Administrator users can open **Storage & Backups -> Restore** for the same two Restore modes exposed by mJust: world Restore and full Minecraft-data Restore.

The WebUI uses the existing Management API for completed-backup discovery, authoritative Restore planning, plan-fingerprint revalidation, apply, persistent operation tracking, runtime validation, and rollback. Compatibility and safety policy remain in the Management Agent/shared backend.

The browser presents Agent warnings and confirmation requirements, requires the explicit destructive confirmation `RESTORE`, and requires a separate online-player interruption confirmation when the Agent says it is needed. Browser refresh or reconnect resumes the same persistent Restore operation.

## System power controls

Administrator users have a power control in the top-right WebUI header. It opens appliance-level actions for **Restart** and **Power off**. On supported physical HWS systems, the same menu also exposes **Restart to UEFI/BIOS**. VM deployments do not show that action.

Selecting a power action first opens a centered confirmation dialog. The WebUI then submits the action through the existing System Actions API; it does not call systemd or firmware tools directly. If Minecraft players are online, the Management Agent returns its player-confirmation requirement and the dialog asks again before the existing graceful shutdown path is allowed to continue.

The Agent remains authoritative for HWS/VM capability detection, player state, graceful Minecraft shutdown, firmware/UEFI availability, and final host action acceptance.

## Workspace controls

Desktop workspaces can be dragged by their headers and resized from the browser’s bottom corner. Window geometry is saved separately for each signed-in user. In **System → History**, drag the divider between Open notifications and Detailed history to adjust their widths. **System → Logs** has a horizontal category selector above Files and Viewer. Its divider adjusts those two columns. Both dividers can also be focused with Tab and moved with the Left and Right arrow keys.

## Plugins lifecycle and diagnostics

**Minecraft → Plugins → Installed** shows Enabled or Disabled for verified
Plugins-managed entries. Their information overlay offers one yellow state
action (**Disable** when enabled, **Enable** when disabled) and red **Remove**.
Manual/unmanaged plugins and JustVoxel-managed Geyser, Floodgate and ViaVersion
remain read only. Enable/Disable review has Cancel and one yellow final action.
Remove requires the destructive slider fully armed and the final confirmation
switch, with Cancel and red Remove. Cancel returns to Installed. Configuration
and data folders are preserved; dependencies are not removed automatically.
Enabled managed parents can block disabling/removing a required dependency.
Actions use cold backup and verified Minecraft restart, with no hot toggling.
Successful operations refresh Installed; Done is green and View in Installed is
dark secondary. Failures point to **System → Logs → Plugins**, with an in-workspace
shortcut where available. Re-check recovery retains maintenance exclusion until
an exact target or restored original state can be verified.

System Logs categories appear exactly in this order: **Setup, Migration,
Restore, Plugins, Reset, Network, Support, Failed units**. Plugins supports the same
list/select/read/download flow as other operation groups. Deep links accept
`workspace=system&tab=logs&log_group=plugins&log_id=<operation UUID>` with existing
identifier validation. Plugin startup failure evidence is bounded and sanitized;
it does not expose the full Minecraft server log.

In **System -> Logs -> Network**, **Generate network report** creates a private, bounded and sanitized local report of NetworkManager, interfaces, routes, DNS, failed units and the four Remote Access providers. Reports open in the same Viewer and download through **Download log**. There is no automatic upload, arbitrary command/unit selection or credential/configuration dump.

Network -> Remote Access includes **NordVPN Meshnet** as its fourth provider. Its native RPM/daemon controls are Meshnet-only. Activate either enables verified Meshnet or opens the account-required dialog: log in, create an account through the fixed [JustVoxel creator’s referral link](https://refer-nordvpn.com/pZGMpGTrBrO), or cancel. After browser login, paste the Continue link into **Complete NordVPN login**; the callback is transient and never logged. Deactivate preserves the Nord account. **Open NordVPN account** opens the fixed official dashboard. Unknown account/Meshnet output stays Unknown; local Nord name/IP rows require CLI evidence.

Provider cards reflow with container width from one to two (580px), three (860px), and four (1140px) columns, preserving workspace geometry and card appearance. The multiple-VPN message is informational: concurrent use is possible but routing, DNS or firewall rules may conflict. It neither blocks activation nor deactivates other providers; Playit remains independent.

In **System -> Logs -> Support**, administrators can select **Generate support report** to create a bounded, redacted local appliance snapshot. The new report opens in the existing Viewer; **Download log** saves it locally. Reports remain in private appliance storage until normal log/state cleanup removes them. There is no external transmission or automatic upload.

**System → Factory Reset → Reset Minecraft** review states that Plugins-managed
JARs installed through JustVoxel will be removed. When Minecraft data outside
internal storage is preserved, manual plugins and plugin configuration/data
folders are preserved too. Reset stages exact verified managed JARs before its
helper and retains recovery evidence through failure/retry. Internal-data reset
continues to delete the whole Minecraft data tree. Full Factory Reset policy is
unchanged.

## Local behavior

When Web management is enabled and healthy, the console/SSH welcome message reports `Web interface: Ready` and shows the friendly `hostname.local:8099` address plus the direct IPv4 address. The live health check takes precedence over bootstrap marker timing so a healthy listener is not reported as merely starting.

When Web management is disabled, the WebUI service is stopped and its firewalld service is removed, so the management port is no longer exposed.

The WebUI keeps session expiration, login throttling, logout, CSRF protection, the Unix-socket trust boundary, and the unprivileged browser-facing service regardless of authentication provider.


## Files

Only the primary `voxel` account with the `administrator` role can open **Control Center → Storage & Data → Files** to manage Minecraft server data and available configured backups. FileBrowser is embedded at `/files/` through the authenticated JustVoxel proxy, with no second login or FileBrowser administration screen. Sources are private, exclude symlink traversal, and permit viewing, downloading, creating, modifying, and deleting files only within those data roots.

**Settings → File Browser → Availability** selects one mode. **On demand** is the fresh-install default: opening Files starts FileBrowser, waits for `/files/health`, and offers Retry if startup fails; closing the window stops it. **Always available** enables boot startup and starts it immediately; closing Files leaves it running. Switching to On demand leaves an open Files window running until it closes. Refresh reloads the embedded browser without restarting the service.


## Terminal

The primary `voxel` administrator opens **Control Center → System → Terminal**. Other accounts, including other administrators, cannot see or access Terminal or Files. Terminal opens the real `voxel` login shell, with no second login. Opening the tab starts the native ttyd service on demand and waits for readiness; startup errors offer Retry.

Switching System tabs keeps the terminal connection alive. System Refresh reconnects Terminal by closing the current shell and stopping its service before starting it again. Closing the System workspace closes the connection and requests a managed stop. If the browser disappears, ttyd exits after its last WebSocket client disconnects. Terminal has no availability setting or boot startup.

Terminal HTTP and WebSocket traffic passes through the existing authenticated Management Agent Unix socket. Both WebUI and Agent enforce the primary `voxel` administrator requirement; the JustVoxel session remains the real authentication boundary. The Management Agent is the only ttyd proxy and alone injects `X-JustVoxel-Terminal: 1`. ttyd exposes no TCP listener: it uses `/run/justvoxel-terminal/ttyd.sock` inside a private `0700` runtime directory, which the WebUI cannot access directly.
