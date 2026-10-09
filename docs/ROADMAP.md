# JustVoxel roadmap

This roadmap lists features that JustVoxel still plans to build. Working features belong in their existing documentation. Small fixes and WebUI touch-ups are handled as needed, not as separate roadmap projects.

## Active development

### Multiple Minecraft server instances

- Run and manage multiple independent Minecraft containers, each with its own configuration, data, ports, backups, lifecycle, and status.
- Allow cloning an existing Minecraft server into a separate instance.
- Support changing a cloned Paper instance to Purpur, or Purpur to Paper, where compatible.
- Prevent port conflicts and unsafe overcommit of host CPU and memory.
- Keep existing Paper/Purpur switching working while multi-instance support is developed.
- Keep Vanilla as a separate supported server type. No Vanilla-to-Paper/Purpur conversion or migration project.

## Planned features

### Plugin management

- Provide guided Paper/Purpur plugin installation, updates, disabling, and removal through the WebUI.
- Show relevant compatibility information and protect server data before risky changes.

### Notifications

- Notify administrators about useful events such as backup failures, low storage, repeated Minecraft failures, and available updates.
- Start with simple delivery options such as Discord and generic webhooks.

## Nice to have

### Scheduling

- Optionally schedule Minecraft restarts and appliance maintenance operations.

### Optional support reporting

- Keep existing local logs and log viewing unchanged.
- Let an administrator review a privacy-sanitized support report and explicitly choose whether to send it.
- Do not send reports automatically.

### JustVoxel-aware bootc rollback

- Consider a simple appliance-managed operating-system rollback option that respects JustVoxel configuration, Minecraft data, and backups.

## Roadmap rule

Only unfinished or planned features belong here. Completed workflows do not remain on the roadmap as open testing or rebuild projects.
