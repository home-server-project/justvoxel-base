package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

const adminDiscoveryHelper = "/usr/libexec/justvoxel/mjust/admin-discovery-json"

var runAdminDiscoveryHelper = func(ctx context.Context, action string) ([]byte, error) {
	return exec.CommandContext(ctx, adminDiscoveryHelper, action).CombinedOutput()
}

type adminMinecraftConfiguration struct {
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

type adminBackupConfiguration struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	MountPoint     string `json:"mount_point"`
	ExpectedUUID   string `json:"expected_uuid"`
	ExpectedSource string `json:"expected_source"`
	Keep           int    `json:"keep"`
	Schedule       string `json:"schedule"`
	TimerEnabled   bool   `json:"timer_enabled"`
}

type adminConfigurationDiscovery struct {
	Configured bool                        `json:"configured"`
	Minecraft  adminMinecraftConfiguration `json:"minecraft"`
	Backup     adminBackupConfiguration    `json:"backup"`
}

type adminStorageDevice struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Parent      string   `json:"parent"`
	Type        string   `json:"type"`
	SizeBytes   uint64   `json:"size_bytes"`
	Filesystem  string   `json:"filesystem"`
	Label       string   `json:"label"`
	UUID        string   `json:"uuid"`
	Mountpoints []string `json:"mountpoints"`
	Model       string   `json:"model"`
	Transport   string   `json:"transport"`
	ReadOnly    bool     `json:"read_only"`
	System      bool     `json:"system"`
}

type adminStorageDiscovery struct {
	SystemDisks []string             `json:"system_disks"`
	Devices     []adminStorageDevice `json:"devices"`
}

type adminSetupDefaults struct {
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

func registerAdminDiscoveryRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/configuration", s.adminConfiguration)
	mux.HandleFunc("GET /v1/admin/storage", s.adminStorage)
	mux.HandleFunc("GET /v1/admin/setup-defaults", s.adminSetupDefaults)
	registerAdminBackupStorageRoutes(mux, s)
	registerAdminStorageProvisionRoutes(mux, s)
	registerAdminStorageActionRoutes(mux, s)
	registerAdminStorageMountRoutes(mux, s)
	registerAdminDataMigrationRoutes(mux, s)
	registerAdminSetupPlanRoutes(mux, s)
	registerAdminSetupApplyRoutes(mux, s)
	registerAdminSetupDiagnosticRoutes(mux, s)
	registerAdminOperationRoutes(mux, s)
}

func (s *server) adminConfiguration(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var out adminConfigurationDiscovery
	if !s.collectAdminDiscovery(w, r, "configuration", &out) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) adminStorage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var out adminStorageDiscovery
	if !s.collectAdminDiscovery(w, r, "storage", &out) {
		return
	}
	if out.SystemDisks == nil {
		out.SystemDisks = []string{}
	}
	if out.Devices == nil {
		out.Devices = []adminStorageDevice{}
	}
	for i := range out.Devices {
		if out.Devices[i].Mountpoints == nil {
			out.Devices[i].Mountpoints = []string{}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) adminSetupDefaults(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var out adminSetupDefaults
	if !s.collectAdminDiscovery(w, r, "defaults", &out) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) collectAdminDiscovery(w http.ResponseWriter, r *http.Request, action string, target any) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, err := runAdminDiscoveryHelper(ctx, action)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "appliance discovery is unavailable")
		return false
	}
	if err := json.Unmarshal(output, target); err != nil {
		writeError(w, http.StatusInternalServerError, "appliance discovery returned invalid data")
		return false
	}
	return true
}
