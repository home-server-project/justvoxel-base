# Plus infrastructure stack

Phase three provides image-owned templates under `/usr/share/justvoxel/templates/plus` and a disabled `justvoxel-plus-stack.service`. It does not deploy containers, create accounts, enable services, open firewall ports or generate image-build secrets. Phases four and five now prepare and deploy them at runtime; image composition itself performs no deployment.

Setup copies the templates into `/etc/justvoxel/plus`. That runtime directory belongs to the administrator; image updates must not overwrite its files. Compose uses `stack.env` explicitly. Initial defaults are editable channel/version references, not enforced versions or image digests:

| Component | Initial image | Compose profile |
| --- | --- | --- |
| Panel | ghcr.io/pterodactyl/panel:v1.15.1 | panel |
| MariaDB | mariadb:11.4 | database |
| Redis | redis:7.4-alpine | cache |
| Wings | ghcr.io/pterodactyl/wings:v1.13.3 | wings |
| Drydock | ghcr.io/codeswhat/drydock:1.6.1 | drydock |

These are upstream-informed initial defaults, pending runtime verification together. They are not yet a VM-tested compatibility claim. Drydock can replace the image scalar in the writable runtime Compose file. Neither host updates nor service restarts restore old image selections. The stack service invokes ordinary Compose start/stop; it does not implement container updates.

Each component is independently selectable through `COMPOSE_PROFILES`. The complete stack is the default. Selecting an incomplete stack can leave Panel without its database/cache or Wings without its Panel configuration; the wizard will explain this without forcibly reinstating disabled components. Panel's upstream entrypoint waits for the database and performs its own standard migration/seed. JustVoxel does not wrap it in database backup, rollback or recovery machinery.

## Runtime files and secrets

| File | Setup responsibility |
| --- | --- |
| compose.yaml | Copy template; keep writable for Drydock updates |
| stack.env | Reachable Panel URL, storage root, timezone, ports, socket group, profiles and initial image references |
| database.env | Random database/root passwords; use upstream MariaDB variables |
| panel.env | Matching database password, random Laravel APP_KEY and HASHIDS_SALT |
| panel/var/.env in the data directory | Same APP_KEY and HASHIDS_SALT before first Panel start |
| drydock.env | Administrator username and upstream-supported Argon2id password hash |
| wings/config.yml | Actual node credentials and configuration obtained from Panel in phase five |
| deployed | Write only after setup has completed application configuration |

No template contains a usable credential. The setup backend generates and writes secrets at runtime, with a restrictive umask and mode 0600, and must not log passwords or keys. Precreating Panel's persistent `.env` also avoids the upstream entrypoint's key-generation log output. Drydock uses Compose's raw env-file format so dollar signs in its Argon2id hash remain literal. Anonymous authentication and insecure root mode are not enabled.

Drydock runs as upstream's node UID/GID 1000, with the host Docker socket group added. Setup must obtain that actual socket GID. Give Drydock traversal/write access to the runtime configuration directory through that group: directory mode 2770, runtime Compose file mode 0660. Secret env files remain root-owned 0600; Compose reads them on the host. Do not recursively chown the secret files to Drydock. Prepare its persistent store with UID/GID 1000 and mode 0700. These permissions are applied by first-run setup, not during image composition.

## Storage and networking

The default persistent data root is `/var/lib/justvoxel-plus`. A mounted secondary disk can supply another absolute root. The selected root contains MariaDB data, Redis data, Panel persistent configuration/logs/nginx files, certificates, Wings game data/logs and Drydock state. This is application storage, not a JustVoxel backup system. The global Docker image/layer store remains the host default.

Wings mounts its game-data directory at the **same absolute path inside and outside the container**. Its generated `system.data` must match that path. Docker on the host resolves game-container bind sources; translating the Wings path would break server creation. Docker allocates the initial separate game bridge without a fixed subnet. Wings uses that bridge and owns the game-container lifecycle; Compose only defines the infrastructure bridge.

MariaDB and Redis publish no host ports. Initial launcher endpoints use Panel HTTP 8081 or HTTPS 8443, Wings API 8080/SFTP 2022 and Drydock 3000; the existing host WebUI stays on 8099. Setup provides actual addresses, optional domain/certificate configuration and firewall activation. Panel's persistent nginx directory supports upstream-style administrator-supplied TLS configuration. No certificate is acquired or account enrolled during image build.

Private infrastructure bind mounts use SELinux labels. Wings and Drydock individually disable container label isolation because they intentionally access the host Docker API/state; host SELinux enforcement is unchanged. Do not relabel the Docker socket, Docker state or host CA certificate tree. Socket access is a privileged host capability and belongs only to the intended upstream management applications.

## Drydock defaults

All five infrastructure services have `dd.watch=true`. Drydock's local watcher has `WATCHBYDEFAULT=false` and `WATCHALL=true`, so it sees explicitly included infrastructure even when stopped, while ordinary Wings-created game containers are excluded. Administrators may change this configuration later.

The upstream Docker Compose action is configured in batch/manual mode. The runtime configuration directory is mounted at the same absolute path inside Drydock and is writable for image-reference updates. Drydock performs those actions itself, including its supported self-update behavior. JustVoxel adds no infrastructure updater, database-version framework or game-server manager.

## Source validation and runtime boundary

Source checks parse the Compose templates and verify mounts, independent profiles, secret-free image defaults and monitoring boundaries. CI additionally runs Docker Compose configuration resolution for the complete stack, a secondary-drive path and each individual component, and checks that raw Drydock authentication hashes survive unchanged. This needs no running Docker daemon or VM.

CI now checks actual image pulls, Panel startup, database initialization, Drydock authentication/default visibility and the Wings connection on an Ubuntu Docker runner. SELinux access, immutable-appliance startup/reboots and administrator-driven Drydock updates remain appliance runtime checks. The stack service is enabled only after selected applications and the Panel/Wings connection are verified.

Upstream references used: Pterodactyl Panel v1.15.1 Docker example/entrypoint, Wings v1.13.3 Docker example, MariaDB official container healthcheck and Drydock v1.6.1 authentication/Compose-action/watcher documentation.


Setup persists Wings' entire root directory at the same host/container path, so
its database, archives and upstream-managed backup files survive recreation.
This does not add a JustVoxel backup workflow. Panel's persistent environment is
0600 owned by upstream's Alpine nginx UID 82, allowing PHP-FPM to read it.
Drydock TLS key copies are 0400 owned by upstream UID 1000. Root-owned
configuration secrets remain 0600. A stack-only CA bundle and PHP CA configuration
support administrator-supplied certificates without disabling verification.
The setup unit is bounded to 35 minutes and runs no timer or automatic retry.
