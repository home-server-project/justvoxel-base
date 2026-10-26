# JustVoxel roadmap

This roadmap lists features that JustVoxel still plans to build. Working features belong in their existing documentation. Small fixes and WebUI touch-ups are handled as needed, not as separate roadmap projects.

## Active development

### Server cloning and controlled software conversion

Multiple independent Minecraft instances are implemented and can run concurrently.
Each current instance has its own immutable identity, configuration, container,
ports, storage, backups, lifecycle, and status.

The remaining project is safe cloning and broader software conversion:

- Clone an existing server into a separate instance while leaving the source unchanged.
- Discover the source software, Minecraft version, plugins, mods, and loader requirements before offering a conversion.
- Allow a reviewed conversion only when the destination software and retained content are compatible.
- When compatibility is incomplete or unknown, offer a normal clone without conversion instead of risking the source.
- Identify incompatible plugins or mods clearly so the administrator can remove them and review the conversion again.
- Keep existing supported Paper/Purpur changes working while the broader compatibility design is developed.

## Planned features

### Notifications

- Notify administrators about useful events such as backup failures, low storage, repeated Minecraft failures, and available updates.
- Start with simple delivery options such as Discord and generic webhooks.

## Nice to have

### Scheduling

- Optionally schedule Minecraft restarts and appliance maintenance operations.

### Optional support-report sharing

- Local bounded, privacy-sanitized support-report generation, viewing, and download are implemented.
- A future sharing workflow may let an administrator review the report and explicitly choose whether and where to send it.
- Do not send reports automatically.

### JustVoxel-aware bootc rollback

- Consider a simple appliance-managed operating-system rollback option that respects JustVoxel configuration, Minecraft data, and backups.

## Roadmap rule

Only unfinished or planned features belong here. Completed workflows do not remain on the roadmap as open testing or rebuild projects.
