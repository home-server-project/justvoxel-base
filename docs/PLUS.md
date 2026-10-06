# JustVoxel Plus

Development and publication belong only to testing-plus. The image package is justvoxel-plus-base, with the testing-plus channel, based on Home Server Base 10 stable-docker for x86-64-v3.

JustVoxel Plus manages the host. Drydock manages the infrastructure containers. Pterodactyl manages the game servers.

## Phase two: host interface

The existing desktop, wallpapers and host workspaces remain. System Monitor, Network, System Update, users, authentication, date/time, disk and mount management, host power controls and existing UPS capability detection are retained. Administrator, Operator and Viewer permissions remain unchanged.

Minecraft workspaces, game software/version controls, backups, restore, migration and game reset are not exposed. The WebUI and privileged Management API both reject the inherited game routes. Their implementation remains in source for now. Game-specific storage assignment is unavailable; ordinary disk/partition/mount operations keep their existing review and confirmation steps.

Host power and update reboots do not use Minecraft player queries or game backups. Docker/systemd handle host shutdown normally. Glances remains the RPM supplied by Home Server Packages. The Plus image installs the AlmaLinux 10 python3-docker RPM from the Home Server Packages stable artifact with DNF so Glances can use Docker rather than requesting the Podman API socket. The inherited Minecraft shutdown guard is not enabled.

The Plus console reports Docker and infrastructure setup status, using the Plus deployment marker rather than a Minecraft Quadlet. Host validation checks Docker, containerd, the Agent, WebUI and Glances services, plus bounded Docker and Glances API probes. Game templates are retained in source but no longer define Plus image readiness.

The Logs screen provides the current boot's last 200 journal lines for the Management Agent, WebUI, Docker and containerd. Only Administrators may read these logs. Service selection is fixed, and lines matching the existing sensitive diagnostic pattern are redacted.

Setup and Look Around are retained. Setup opens the Plus component, address, storage and account wizard; it does not run the inherited Minecraft wizard. The wizard deploys the selected upstream applications and shows host-side progress. Factory Reset reviews and removes the Plus installation and owned internal data while preserving second-drive data. Pterodactyl and Drydock launchers appear in the Control Center’s Applications section after successful deployment.

Tailscale, NetBird and playit.gg setup, installation and activation are unchanged.

## Phase three: infrastructure templates

Upstream Panel, MariaDB, Redis, Wings and Drydock Compose definitions and runtime configuration templates are installed with the image. Every component has its own profile for the wizard switches. Drydock watches only explicitly labelled infrastructure containers and uses its upstream Compose action for administrator-requested updates. The stack service remains disabled until setup deploys and connects the applications. See PLUS_STACK.md for the configuration and storage contract.

## Remaining phases

Verify the working appliance at runtime.

The password step explains: These are separate accounts. Changing your password in one does not change the other.

## Phase four: one setup wizard

The Plus desktop's Setup invitation now opens one five-step flow: components,
address and optional TLS, storage, application account, and review. There is no
recommended/advanced mode split. All five infrastructure components start enabled;
the Pterodactyl group switch and individual switches remain editable. Partial
selections are allowed with a warning that external services or configuration may
be needed.

A domain is optional. The administrator supplies the reachable hostname or IP,
and may upload or paste a PEM certificate chain and matching private key. The
Agent checks certificate validity, key matching and hostname coverage. DNS and
certificate issuance are not performed by the wizard. Without supplied TLS,
applications use HTTP; the wizard recommends a trusted local network.

Storage defaults to `/var/lib/justvoxel-plus`. A mounted local ext4, XFS or Btrfs
filesystem under `/mnt/`, `/srv/`, `/media/` or canonical `/var/mnt/` and
`/var/srv/` paths can instead hold the application
data in a `justvoxel-plus` subdirectory. Docker image storage stays on the system
disk. Read-only, network and memory filesystems are not offered. The disk must
remain mounted across reboot; use the existing Storage interface to prepare it.
The wizard does not format disks, move Docker storage or create backups.

The signed-in host username is prefilled. The administrator may choose a different
application username and enters the desired password once, plus confirmation.
Panel needs an email address. Selected Panel and Drydock accounts use these
initial credentials but remain independent of the host and of each other.
Changing one account does not synchronize the others.

Preparation is the first part of deployment. An administrator-only Agent API
stores the choices, account password and optional TLS material in
`/var/lib/justvoxel-plus-setup/setup.json` (0600, parent 0700). The WebUI forwards
input over the existing local Unix socket, never executes commands, and never
stores credentials in browser storage or returns them in API responses. Existing
choices can be replaced before deployment. The deployment worker consumes this preparation, creates the upstream accounts,
and removes the entire temporary preparation file after successful setup.


## Phase five: application deployment

The final wizard step deploys the selected upstream images. A fixed systemd
oneshot runs the Management Agent's first-run backend independently of the browser.
It reports secret-free progress and errors through the administrator-only API.
There are no automatic deployment retries, database backups, rollback transactions
or infrastructure update manager. An explicit retry keeps the original database
credentials and administrator-edited runtime Compose file. After deployment starts,
use Retry for those saved choices rather than replacing them through the wizard.

Setup starts MariaDB and Redis when selected, lets Panel perform its standard
migration/seed, and calls upstream Artisan commands to create the administrator,
location and local node. Account input travels through stdin, never command
arguments. The tiny first-run PHP adapter uses upstream commands and models;
it adds no ongoing application or game-server management API. Wings configuration
comes from Panel and is persisted before Wings starts. A final check asks Panel's
upstream Wings repository to authenticate to the daemon.

Panel, Wings and Drydock can use the uploaded certificate. Panel's external HTTP
binding becomes loopback-only when TLS is selected. The supplied certificate is
trusted only inside the stack, together with the host CA bundle; TLS verification
is not disabled. Initial direct addressing supports IPv4 or a hostname.

A separate game bridge is allocated by Docker to avoid Wings' default subnet
colliding with the infrastructure bridge. Wings owns the game containers and their
lifecycle. No games, eggs, plugins, backups or port allocations are created by
JustVoxel. Allocate ports and create servers in Pterodactyl itself.

Successful setup opens only the selected application TCP ports, records the
secret-free application addresses, enables the stack service and removes temporary
credentials. Storage dependencies are added to the stack service; a secondary
mount must be mounted before startup, so an absent disk cannot silently become
new application storage. Existing host authentication and remote-access tools
are unchanged.

The Control Center’s Applications section launches Pterodactyl and Drydock in separate tabs. The desktop has no application launcher panel. Their own logins
remain independent. Host Logs includes Plus setup and stack journals.
CI has a bounded Ubuntu/Docker smoke check that pulls the declared upstream
images, starts the complete stack, verifies Panel/Wings and Drydock authentication,
and checks that an unlabelled container is excluded from Drydock's default view.
This does not replace immutable-image, SELinux or appliance reboot testing.


## Phase six: host CLI and factory reset

The Plus mjust entry point uses a compact host menu and explicit host command
list. Status, application URLs, bounded host logs, networking, file browsing,
system resources, OS updates, power, existing UPS/firmware commands, WebUI
controls and password reset remain available. Setup directs the administrator
to the WebUI wizard. Legacy game, backup, restore, migration and game-storage
commands are unavailable through mjust in Plus. Existing remote-access helpers
are reused unchanged.

Full Factory Reset uses the existing administrator boundary, reviewed plan,
confirmation, persistent operation tracking, explicit retry and first-login
credential reset. The reviewed fingerprint includes the runtime configuration;
a changed configuration requires a fresh review. It stops the Plus stack,
removes its Compose containers, and removes only Pterodactyl-labelled game
containers whose UUID data bind belongs to this installation. Unrelated Docker
containers and downloaded images are preserved; no Docker prune is used.

Only the default appliance-owned internal data directory is erased. A selected
second drive, custom data path or mounted data directory is preserved. Nested
filesystems must be unmounted before reset, and symlink paths are refused.
Partitions, filesystem formats, mounts, network, SSH and remote-access settings
are preserved. The Plus runtime configuration, temporary setup state and stack
storage dependency are removed. WebUI users/history/sessions are reset using the
existing identity backend; the voxel password returns to voxel / voxel and must
be changed immediately after sign-in. There is no automatic backup or retry.
If second-drive data is preserved, fresh setup requires an empty application
directory; the administrator decides what to do with the old data.

The CLI factory-reset command requires an interactive terminal and an exact
FACTORY RESET confirmation after displaying the plan. It queues the same Agent
operation as the WebUI and directs progress/recovery to the WebUI.

CI's bounded upstream container smoke test additionally exercises Plus runtime
cleanup and preservation of an unrelated container. Host identity, systemd,
SELinux, boot and reboot behavior still require appliance runtime validation.

## First appliance runtime check (when available)

Boot ghcr.io/home-server-project/justvoxel-plus-base:testing-plus on an x86-64-v3
VM. Sign in, change the initial password, open the host WebUI on port 8099 and
check Look Around plus the Setup invitation. Complete the five-step setup with
all components enabled, the VM's reachable IPv4 address, default storage and
TLS off for a local-network first check.

Setup should pull and start MariaDB, Redis, Panel, Wings and Drydock, create the
initial accounts, connect Panel to Wings and show both launchers. Open Panel
on port 8081 and Drydock on port 3000. Check that Panel's node is connected and
Drydock shows the five infrastructure containers. In the console, check mjust,
mjust status, mjust applications and mjust logs plus-setup. Reboot and confirm
the host WebUI and all selected applications return with the same accounts.

Report the failing screen/stage, mjust status --details, the relevant bounded
host log (plus-setup, plus-stack, docker or management-agent), and any SELinux
AVC denials. Do not send credentials, full environment files or Wings tokens.
Optional checks after the basic path: supplied domain/TLS, second-drive reboot
and missing-disk startup guard, and factory reset on a disposable VM containing
only test data. These are deferred runtime checks, not claimed source results.
