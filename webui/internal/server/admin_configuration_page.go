package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func configurationRequestFromDiscovery(configuration api.AdminConfigurationDiscovery) api.AdminConfigurationChangeRequest {
	if !configuration.Configured {
		return api.AdminConfigurationChangeRequest{}
	}
	return api.AdminConfigurationChangeRequest{
		WhitelistEnabled: &configuration.Minecraft.WhitelistEnabled,
		JavaMemory:       configuration.Minecraft.JavaMemory, ContainerMemory: configuration.Minecraft.ContainerMemory,
		JavaPort: configuration.Minecraft.JavaPort, BedrockEnabled: configuration.Minecraft.BedrockEnabled,
		BedrockPort: configuration.Minecraft.BedrockPort, Timezone: configuration.Minecraft.Timezone,
		MaxPlayers: configuration.Minecraft.MaxPlayers, MOTD: configuration.Minecraft.MOTD,
		ImageTag: configuration.Minecraft.ImageTag, VersionPolicy: configuration.Minecraft.VersionMode,
		Version: configuration.Minecraft.Version, GameMode: configuration.Minecraft.GameMode, BackupKeep: configuration.Backup.Keep,
		BackupSchedule: configuration.Backup.Schedule, BackupTimerEnabled: configuration.Backup.TimerEnabled,
	}
}

func parsePositiveFormInt(value, label string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive number.", label)
	}
	return parsed, nil
}

func formatMemoryMiB(value int) string {
	if value <= 0 {
		return "Unknown"
	}
	return humanBytes(uint64(value) * 1024 * 1024)
}
