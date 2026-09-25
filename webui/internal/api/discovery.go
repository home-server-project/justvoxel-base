package api

import (
	"context"
	"net/http"
)

type AdminMinecraftConfiguration struct {
	DataPath           string `json:"data_path"`
	DataMountPoint     string `json:"data_mount_point"`
	DataExpectedUUID   string `json:"data_expected_uuid"`
	DataExpectedSource string `json:"data_expected_source"`
	JavaMemory         string `json:"java_memory"`
	ContainerMemory    string `json:"container_memory"`
	JavaPort           int    `json:"java_port"`
	BedrockEnabled     bool   `json:"bedrock_enabled"`
	BedrockPort        int    `json:"bedrock_port"`
	Timezone           string `json:"timezone"`
	MaxPlayers         int    `json:"max_players"`
	MOTD               string `json:"motd"`
	ImageTag           string `json:"image_tag"`
	VersionMode        string `json:"version_mode"`
	Version            string `json:"version"`
	GameMode           string `json:"game_mode"`
	Difficulty         string `json:"difficulty"`
	WhitelistEnabled   bool   `json:"whitelist_enabled"`
	EnforceWhitelist   bool   `json:"enforce_whitelist"`
}

type AdminBackupConfiguration struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	MountPoint     string `json:"mount_point"`
	ExpectedUUID   string `json:"expected_uuid"`
	ExpectedSource string `json:"expected_source"`
	Keep           int    `json:"keep"`
	Schedule       string `json:"schedule"`
	TimerEnabled   bool   `json:"timer_enabled"`
}

type AdminConfigurationDiscovery struct {
	Configured bool                        `json:"configured"`
	Minecraft  AdminMinecraftConfiguration `json:"minecraft"`
	Backup     AdminBackupConfiguration    `json:"backup"`
}

type AdminStorageDevice struct {
	Name                 string   `json:"name"`
	Path                 string   `json:"path"`
	Parent               string   `json:"parent"`
	Type                 string   `json:"type"`
	SizeBytes            uint64   `json:"size_bytes"`
	Filesystem           string   `json:"filesystem"`
	Label                string   `json:"label"`
	UUID                 string   `json:"uuid"`
	Mountpoints          []string `json:"mountpoints"`
	Model                string   `json:"model"`
	Transport            string   `json:"transport"`
	ReadOnly             bool     `json:"read_only"`
	System               bool     `json:"system"`
	FilesystemSizeBytes  uint64   `json:"filesystem_size_bytes,omitempty"`
	FilesystemUsedBytes  uint64   `json:"filesystem_used_bytes,omitempty"`
	FilesystemFreeBytes  uint64   `json:"filesystem_free_bytes,omitempty"`
	FilesystemUsageKnown bool     `json:"filesystem_usage_known,omitempty"`
}

type AdminStorageFreeSpace struct {
	Device    string `json:"device"`
	Start     string `json:"start"`
	End       string `json:"end"`
	SizeBytes uint64 `json:"size_bytes"`
}

type AdminStorageDiscovery struct {
	SystemDisks []string                `json:"system_disks"`
	Devices     []AdminStorageDevice    `json:"devices"`
	FreeSpaces  []AdminStorageFreeSpace `json:"free_spaces"`
}

type AdminSetupDefaults struct {
	DataPath                    string `json:"data_path"`
	BackupPath                  string `json:"backup_path"`
	JavaMemory                  string `json:"java_memory"`
	ContainerMemory             string `json:"container_memory"`
	JavaPort                    int    `json:"java_port"`
	BedrockEnabled              bool   `json:"bedrock_enabled"`
	BedrockPort                 int    `json:"bedrock_port"`
	Timezone                    string `json:"timezone"`
	MaxPlayers                  int    `json:"max_players"`
	MOTD                        string `json:"motd"`
	ImageTag                    string `json:"image_tag"`
	VersionMode                 string `json:"version_mode"`
	BackupKeep                  int    `json:"backup_keep"`
	BackupDailyTime             string `json:"backup_daily_time"`
	SystemMemoryMiB             int    `json:"system_memory_mib"`
	SystemReserveMinimumMiB     int    `json:"system_reserve_minimum_mib"`
	SystemReserveRecommendedMiB int    `json:"system_reserve_recommended_mib"`
}

func (c *Client) AdminConfiguration(ctx context.Context, session string) (AdminConfigurationDiscovery, error) {
	var out AdminConfigurationDiscovery
	err := c.do(ctx, http.MethodGet, "/v1/admin/configuration", session, nil, &out)
	return out, err
}

func (c *Client) AdminStorage(ctx context.Context, session string) (AdminStorageDiscovery, error) {
	var out AdminStorageDiscovery
	err := c.do(ctx, http.MethodGet, "/v1/admin/storage", session, nil, &out)
	return out, err
}

func (c *Client) AdminSetupDefaults(ctx context.Context, session string) (AdminSetupDefaults, error) {
	var out AdminSetupDefaults
	err := c.do(ctx, http.MethodGet, "/v1/admin/setup-defaults", session, nil, &out)
	return out, err
}
