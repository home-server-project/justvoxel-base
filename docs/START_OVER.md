# Start Over

`mjust start-over` is appliance-wide: it retires **all configured Minecraft servers** from JustVoxel and returns Minecraft to first-setup state. The interactive menu exposes it under **Advanced → Start over / reset Minecraft**.

All worlds/data trees and all existing backup archives and archive metadata remain on disk. Start Over preserves storage partitions, filesystems, mounted storage, `/etc/fstab`, storage ownership records and SMB credentials. Authentication mode, WebUI users, accounting/history, audit records, unrelated host configuration and bootc deployments are preserved.

Modern service-managed Minecraft containers are disposable runtime objects and are retired along with their exact instance runtime, backup units/timers and owned firewall openings. There is no keep-container option. Podman images and unrelated containers remain untouched. Preserved data and backups are not automatically adopted by fresh Setup.

The root-only Unix Management API plans the complete immutable instance set. The terminal shows each participant and requires the exact phrase `START OVER`. For every running server it checks players through that exact instance ID. Unknown player presence blocks planning. Online players require a separate yes/no interruption confirmation; each running instance then uses its normal graceful shutdown, including player warnings/countdown. Stopped servers remain stopped. Failed servers require proof that no live container remains before safe stop normalization. Start Over never starts or restarts a server.

The optional configuration backup is saved **before any server stop or configuration mutation** under `/var/lib/justvoxel/config-backups/`. It includes JustVoxel policy, private participant configuration, Quadlets and native backup service/timer files. It can contain secrets and is root-only (0600). Publication uses a verified `.partial` archive and atomic rename. It contains no Minecraft data trees, backup archives, WebUI database or Podman storage. Existing configuration archives remain preserved. An archive failure aborts before Minecraft changes.

Retirement uses the existing durable per-instance journal with the distinct `start_over` preservation policy. It pins data identity and verifies that the same data directory survives retirement before removing its journal. Interrupted retirement is resumed only from that exact immutable ID and evidence, including when its registry identity has already been retired. Completed instances remain retired; retry reviews the remaining set. Unrelated Delete, Reset or incomplete Restore evidence blocks Start Over. New instance creation is blocked while Start Over retirement evidence is unresolved.

Only after all participants retire and empty runtime ownership is verified are `/etc/justvoxel/justvoxel.conf` and `/etc/justvoxel/minecraft-backup.env` removed. The configuration directory is never recursively deleted. Run `mjust` after success to begin Setup.

This implementation has no legacy execution branch. Fixed-runtime artifacts or activity fail closed; they never become authority for modern instances. Legacy Setup recovery remains a separate supported operation.
