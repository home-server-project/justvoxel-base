# JustVoxel Plus

Development and publication belong only to testing-plus. The image package is justvoxel-plus-base, with the testing-plus channel, based on Home Server Base 10 stable-docker for x86-64-v3.

JustVoxel Plus manages the host. Drydock manages the infrastructure containers. Pterodactyl manages the game servers.

## Phase two: host interface

The existing desktop, wallpapers and host workspaces remain. System Monitor, Network, System Update, users, authentication, date/time, disk and mount management, host power controls and existing UPS capability detection are retained. Administrator, Operator and Viewer permissions remain unchanged.

Minecraft workspaces, game software/version controls, backups, restore, migration and game reset are not exposed. The WebUI and privileged Management API both reject the inherited game routes. Their implementation remains in source for now. Game-specific storage assignment is unavailable; ordinary disk/partition/mount operations keep their existing review and confirmation steps.

Host power and update reboots do not use Minecraft player queries or game backups. Docker/systemd handle host shutdown normally. Glances remains the RPM supplied by Home Server Packages. The Plus image installs the AlmaLinux 10 python3-docker RPM from the Home Server Packages stable artifact with DNF so Glances can use Docker rather than requesting the Podman API socket. The inherited Minecraft shutdown guard is not enabled.

The Logs screen provides the current boot's last 200 journal lines for the Management Agent, WebUI, Docker and containerd. Only Administrators may read these logs. Service selection is fixed, and lines matching the existing sensitive diagnostic pattern are redacted.

Setup and Look Around are retained. The setup page currently explains that application deployment is still being developed; it does not run the inherited Minecraft wizard. Factory Reset stays visible but cannot execute until its Plus scope is implemented. Pterodactyl and Drydock launchers follow when actual deployment URLs exist.

Tailscale, NetBird and playit.gg setup, installation and activation are unchanged.

## Remaining phases

Provide upstream container definitions and persistent configuration; implement the compact setup wizard; create and connect Panel/Wings and application launchers; then adapt CLI and factory reset and verify the working appliance at runtime.

The password step must explain: These are separate accounts. Changing your password in one does not change the other.
