package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeDiscoveryAPI struct {
	fakeAPI
	role             string
	configuration    api.AdminConfigurationDiscovery
	storage          api.AdminStorageDiscovery
	defaults         api.AdminSetupDefaults
	configurationHit int
	storageHit       int
	defaultsHit      int
}

func (f *fakeDiscoveryAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeDiscoveryAPI) AdminConfiguration(_ context.Context, session string) (api.AdminConfigurationDiscovery, error) {
	if session != "session-token" {
		return api.AdminConfigurationDiscovery{}, api.ErrUnauthorized
	}
	f.configurationHit++
	return f.configuration, nil
}

func (f *fakeDiscoveryAPI) AdminStorage(_ context.Context, session string) (api.AdminStorageDiscovery, error) {
	if session != "session-token" {
		return api.AdminStorageDiscovery{}, api.ErrUnauthorized
	}
	f.storageHit++
	return f.storage, nil
}

func (f *fakeDiscoveryAPI) AdminSetupDefaults(_ context.Context, session string) (api.AdminSetupDefaults, error) {
	if session != "session-token" {
		return api.AdminSetupDefaults{}, api.ErrUnauthorized
	}
	f.defaultsHit++
	return f.defaults, nil
}

func TestServerSettingsShowsCurrentConfiguration(t *testing.T) {
	client := &fakeDiscoveryAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.DataPath = "/var/lib/justvoxel/minecraft"
	client.configuration.Minecraft.JavaMemory = "4G"
	client.configuration.Minecraft.ContainerMemory = "6G"
	client.configuration.Minecraft.JavaPort = 25565
	client.configuration.Minecraft.Version = "1.21.8"
	client.configuration.Minecraft.VersionMode = "pinned"
	client.configuration.Minecraft.MaxPlayers = 10
	client.configuration.Backup.Type = "system"
	client.configuration.Backup.Path = "/var/lib/justvoxel/backups"
	client.configuration.Backup.Keep = 7
	client.configuration.Backup.Schedule = "*-*-* 04:30:00"
	client.defaults.JavaMemory = "4G"
	client.defaults.ContainerMemory = "6G"
	client.defaults.DataPath = "/var/lib/justvoxel/minecraft"
	client.defaults.BackupPath = "/var/lib/justvoxel/backups"
	client.defaults.JavaPort = 25565
	client.defaults.BedrockPort = 19132
	client.defaults.MaxPlayers = 10
	client.defaults.BackupKeep = 7
	client.defaults.BackupDailyTime = "04:30"
	client.defaults.SystemMemoryMiB = 8192
	client.defaults.SystemReserveMinimumMiB = 1024
	client.defaults.SystemReserveRecommendedMiB = 2048

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("server settings returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Minecraft settings", "/var/lib/justvoxel/minecraft", "4G", "6G", "1.21.8", "/var/lib/justvoxel/backups", "Review changes"} {
		if !strings.Contains(body, want) {
			t.Fatalf("server settings missing %q", want)
		}
	}
	if client.configurationHit != 1 || client.defaultsHit != 1 {
		t.Fatalf("discovery calls configuration=%d defaults=%d", client.configurationHit, client.defaultsHit)
	}
}

func TestServerSettingsSupportsUnconfiguredAppliance(t *testing.T) {
	client := &fakeDiscoveryAPI{}
	client.defaults.DataPath = "/var/lib/justvoxel/minecraft"
	client.defaults.BackupPath = "/var/lib/justvoxel/backups"
	client.defaults.JavaMemory = "4G"
	client.defaults.ContainerMemory = "6G"
	client.defaults.SystemMemoryMiB = 8192
	client.defaults.SystemReserveMinimumMiB = 1024
	client.defaults.SystemReserveRecommendedMiB = 2048

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("unconfigured settings returned %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Minecraft is not configured yet", "Suggested defaults", "8.0 GiB", "2.0 GiB", "1.0 GiB"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("missing unconfigured state %q: %s", want, rr.Body.String())
		}
	}
}

func TestStorageSettingsShowsHumanReadableInventory(t *testing.T) {
	client := &fakeDiscoveryAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.DataPath = "/var/lib/justvoxel/minecraft"
	client.configuration.Backup.Path = "/var/mnt/backup/justvoxel"
	client.storage.SystemDisks = []string{"/dev/vda"}
	client.storage.Devices = []api.AdminStorageDevice{{
		Name: "vdb1", Path: "/dev/vdb1", Type: "part", SizeBytes: 1073741824,
		Filesystem: "xfs", Label: "BACKUP", UUID: "uuid-123", Mountpoints: []string{"/var/mnt/backup"},
		Model: "Virtual Disk", Transport: "virtio",
	}}

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/storage", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("storage settings returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Storage overview", "/dev/vda", "Virtual Disk", "/dev/vdb1", "1.0 GiB", "xfs", "/var/mnt/backup"} {
		if !strings.Contains(body, want) {
			t.Fatalf("storage settings missing %q", want)
		}
	}
}

func TestDiscoveryPagesRejectOperatorBeforePrivilegedDiscovery(t *testing.T) {
	client := &fakeDiscoveryAPI{role: "operator"}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/settings/server", "/settings/storage"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example"+path, ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("operator %s status = %d, want 403", path, rr.Code)
		}
	}
	if client.configurationHit != 0 || client.storageHit != 0 || client.defaultsHit != 0 {
		t.Fatalf("privileged discovery ran for operator: configuration=%d storage=%d defaults=%d", client.configurationHit, client.storageHit, client.defaultsHit)
	}
}
