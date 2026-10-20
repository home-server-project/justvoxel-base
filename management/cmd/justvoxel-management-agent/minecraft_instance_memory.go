package main

import (
	"context"
	"strings"
)

const minecraftMemoryReserveMiB int64 = 1024

func instanceMemoryCapacity(total, used, required int64) bool {
	return total >= 2048 && total <= 16777216 && used >= 0 && required >= 512 && required <= total-minecraftMemoryReserveMiB && used <= total-minecraftMemoryReserveMiB-required
}

// Only the selected replacement participates once; stopped installed servers
// have no running reservation. Unknown runtime evidence refuses a new start.
func (b *instanceRuntimeBackend) admitMemory(ctx context.Context, total int64, records []instanceRuntimeRecord, candidate minecraftInstanceRuntimeConfig, boot bool) error {
	required, err := instanceRuntimeMemoryMiB(candidate.ContainerMemory)
	if err != nil {
		return instanceRuntimeFailure("Minecraft memory capacity could not be verified.")
	}
	var used int64
	seen := map[string]bool{}
	for _, record := range records {
		c := record.Runtime
		if seen[c.ID] || !instanceIDPattern.MatchString(c.ID) {
			return errInstanceRecovery
		}
		seen[c.ID] = true
		if c.ID == candidate.ID {
			continue
		}
		count := c.StartOnBoot
		if !boot {
			var err error
			count, err = b.memoryInUse(ctx, c.ID)
			if err != nil {
				return err
			}
		}
		if count {
			limit, err := instanceRuntimeMemoryMiB(c.ContainerMemory)
			if err != nil {
				return instanceRuntimeFailure("Minecraft memory capacity could not be verified.")
			}
			used += limit
		}
	}
	if !instanceMemoryCapacity(total, used, required) {
		if boot {
			return instanceRuntimeFailure("Start on Boot is unavailable because the boot-enabled Minecraft servers would exceed available memory. Reduce a memory limit or disable Start on Boot, then try again.")
		}
		return instanceRuntimeFailure("Not enough Minecraft memory capacity to start this server. Stop another server or reduce a memory limit, then try again.")
	}
	return nil
}

// Active systemd units reserve memory even when container observation would
// classify their unusual state as failed. Inactive/failed services additionally
// need the existing container check before their reservation can be released.
func (b *instanceRuntimeBackend) memoryInUse(ctx context.Context, id string) (bool, error) {
	unavailable := instanceRuntimeFailure("Minecraft memory capacity could not be verified. Try again after other servers have stopped.")
	output, err := b.command(ctx, "systemctl", "show", "--property=LoadState,ActiveState", "--", "minecraft-"+id+".service")
	if err != nil {
		return false, unavailable
	}
	properties := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return false, unavailable
		}
		if _, exists := properties[key]; exists {
			return false, unavailable
		}
		properties[key] = value
	}
	load, active := properties["LoadState"], properties["ActiveState"]
	if load != "loaded" && load != "not-found" {
		return false, unavailable
	}
	switch active {
	case "active", "activating", "reloading":
		if load != "loaded" {
			return false, unavailable
		}
		return true, nil
	case "inactive", "failed":
		state, err := b.observeRaw(ctx, id)
		if err != nil {
			return false, unavailable
		}
		switch state.State {
		case "running", "starting":
			return true, nil
		case "stopped", "configured", "failed":
			return false, nil
		}
	}
	return false, unavailable
}
