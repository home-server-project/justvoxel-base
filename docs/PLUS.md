# JustVoxel Plus

Development and publication belong only to testing-plus. The image package is justvoxel-plus-base, with the testing-plus channel, based on Home Server Base 10 stable-docker for x86-64-v3.

JustVoxel Plus manages the host. Drydock manages the infrastructure containers. Pterodactyl manages the game servers.

## Phase two: host interface

The existing desktop, wallpapers and host workspaces remain. System Monitor, Network, System Update, users, authentication, date/time, disk and mount management, host power controls and existing UPS capability detection are retained. Administrator, Operator and Viewer permissions remain unchanged.

Minecraft workspaces, game software/version controls, backups, restore, migration and game reset are not exposed. The WebUI and privileged Management API both reject the inherited game routes. Their implementation remains in source for now. Game-specific storage assignment is unavailable; ordinary disk/partition/mount operations keep their existing review and confirmation steps.

Host power and update reboots do not use Minecraft player queries or game backups. Docker/systemd handle host shutdown normally. Glances remains the RPM supplied by Home Server Packages. The Plus image installs the AlmaLinux 10 python3-docker RPM from the Home Server Packages stable artifact with DNF so Glances can use Docker rather than requesting the Podman API socket. The inherited Minecraft shutdown guard is not enabled.

The Plus console reports Docker and infrastructure setup status, using the Plus deployment marker rather than a Minecraft Quadlet. Host validation checks Docker, containerd, the Agent, WebUI and Glances services, plus bounded Docker and Glances API probes. Game templates are retained in source but no longer define Plus image readiness.

The Logs screen provides the current boot's last 200 journal lines for the Management Agent, WebUI, Docker and containerd. Only Administrators may read these logs. Service selection is fixed, and lines matching the existing sensitive diagnostic pattern are redacted.

Setup and Look Around are retained. Setup opens the Plus component, address, storage and account wizard; it does not run the inherited Minecraft wizard. The wizard deploys the selected upstream applications and shows host-side progress. Factory Reset stays visible but cannot execute until its Plus scope is implemented. Pterodactyl and Drydock launchers appear after successful deployment.

Tailscale, NetBird and playit.gg setup, installation and activation are unchanged.

## Phase three: infrastructure templates

Upstream Panel, MariaDB, Redis, Wings and Drydock Compose definitions and runtime configuration templates are installed with the image. Every component has its own profile for the wizard switches. Drydock watches only explicitly labelled infrastructure containers and uses its upstream Compose action for administrator-requested updates. The stack service remains disabled until setup deploys and connects the applications. See PLUS_STACK.md for the configuration and storage contract.

## Remaining phases

Adapt CLI and factory reset, then verify the working appliance at runtime.

The password step must explain: These are separate accounts. Changing your password in one does not change the other.

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

The desktop launches Pterodactyl and Drydock in separate tabs. Their own logins
remain independent. Host Logs includes Plus setup and stack journals.
CI has a bounded Ubuntu/Docker smoke check that pulls the declared upstream
images, starts the complete stack, verifies Panel/Wings and Drydock authentication,
and checks that an unlabelled container is excluded from Drydock's default view.
This does not replace immutable-image, SELinux or appliance reboot testing.
