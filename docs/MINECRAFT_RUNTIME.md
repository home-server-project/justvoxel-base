# Minecraft runtime

JustVoxel ships immutable runtime templates based on the hardware-verified Home Server Project Paper deployment. The bootc image itself does not create a world, accept the Minecraft EULA, or activate a Minecraft service.

`mjust setup` copies and renders those templates into administrator-owned files under `/etc`. Minecraft runs only from the local rendered Quadlet in `/etc/containers/systemd/minecraft.container`; it never runs directly from `/usr/share/justvoxel/templates`.

The runtime uses `docker.io/itzg/minecraft-server` with Paper and optional Geyser/Floodgate cross-play, plus ViaVersion. The image tag is administrator-configurable: JustVoxel offers upstream `stable`, upstream `latest`, or a validated custom/exact tag. The Minecraft game version is a separate setting: Recommended uses an exact stable version, Latest follows newer versions, and Specific uses an exact version with a usable Paper build. Setup keeps the chosen policy and the resolved version separately. At runtime, Latest renders `VERSION=LATEST`; a non-stable Paper build uses `PAPER_CHANNEL=experimental`.

Changing or restarting a service does not itself refresh a moving container tag from the registry. `mjust update-minecraft` explicitly checks and pulls the configured tag when its remote digest changes. If the game version uses `VERSION=LATEST`, however, the container can download a newer Minecraft release during startup; mjust warns about that behavior when the policy is selected.

Java uses TCP 25565 by default and Bedrock uses UDP 19132 by default, but setup can render administrator-selected host ports. RCON is enabled inside the container for local administration and is never published as a host port.

The template preserves the proven graceful-stop behavior: the itzg shutdown announcement delay is 60 seconds, Podman has 180 seconds to stop, and systemd allows 240 seconds around the stop operation. If an administrator chooses to continue maintenance while players are online, that normal shutdown warning remains the mechanism that gives players time to finish.

Persistent Minecraft data is a configurable host path mounted only at `/data` with a private SELinux `:Z` label. No Mojang server JAR, Paper server JAR, world, EULA acceptance, RCON password, or Floodgate private key is baked into the bootc image.

## Memory behavior

Minecraft memory usage may stay high even when no players are online.

That does not necessarily mean the server actively needs all of that memory. Java can grow its heap while the server is running and keep memory reserved for reuse instead of immediately returning it to the operating system. High idle memory usage by itself is therefore not automatically a problem.

JustVoxel enables zram only as a memory-pressure safety buffer. Zram is compressed swap in RAM; it is not extra physical memory and is not counted when `mjust setup` recommends Minecraft memory values. The recommendation logic uses physical `MemTotal` only.

JustVoxel does not configure disk swap by default.
