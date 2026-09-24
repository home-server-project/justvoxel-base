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

type fakeStorageBrowserMigrationAPI struct {
	fakeDiscoveryAPI
	migration      api.AdminDataMigrationDiscoveryResponse
	migrationCalls int
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

func (f *fakeStorageBrowserMigrationAPI) AdminDataMigrationDiscovery(_ context.Context, session string) (api.AdminDataMigrationDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.migrationCalls++
	return f.migration, nil
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
	for _, path := range []string{"/settings/server", "/settings/storage", "/settings/new-storage", "/workspace/storage"} {
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

func TestNewStorageBrowserGroupsDisksAndPartitions(t *testing.T) {
	client := &fakeDiscoveryAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.DataPath = "/var/mnt/data/minecraft"
	client.configuration.Minecraft.DataMountPoint = "/var/mnt/data"
	client.configuration.Minecraft.DataExpectedUUID = "minecraft-uuid"
	client.configuration.Backup.Path = "/var/mnt/backups/justvoxel"
	client.configuration.Backup.MountPoint = "/var/mnt/backups"
	client.configuration.Backup.ExpectedUUID = "backup-uuid"
	client.storage.SystemDisks = []string{"/dev/vda"}
	client.storage.Devices = []api.AdminStorageDevice{
		{Name: "zram0", Path: "/dev/zram0", Type: "disk", SizeBytes: 4 * 1024 * 1024 * 1024},
		{Name: "vda", Path: "/dev/vda", Type: "disk", SizeBytes: 100 * 1024 * 1024 * 1024, Model: "System Disk", Transport: "virtio"},
		{Name: "vda1", Path: "/dev/vda1", Parent: "vda", Type: "part", SizeBytes: 1024 * 1024 * 1024, Filesystem: "xfs", UUID: "system-uuid", Mountpoints: []string{"/var/lib/containers/storage/overlay", "/var", "/sysroot/ostree/deploy/default/var"}},
		{Name: "vda2", Path: "/dev/vda2", Parent: "vda", Type: "part", SizeBytes: 8 * 1024 * 1024 * 1024, Filesystem: "swap", UUID: "swap-uuid"},
		{Name: "vdb", Path: "/dev/vdb", Type: "disk", SizeBytes: 500 * 1024 * 1024 * 1024, Model: "Data Disk", Transport: "virtio"},
		{Name: "vdb1", Path: "/dev/vdb1", Parent: "vdb", Type: "part", SizeBytes: 300 * 1024 * 1024 * 1024, Filesystem: "xfs", UUID: "minecraft-uuid", Mountpoints: []string{"/var/mnt/data"}},
		{Name: "vdb2", Path: "/dev/vdb2", Parent: "vdb", Type: "part", SizeBytes: 200 * 1024 * 1024 * 1024},
		{Name: "sdc", Path: "/dev/sdc", Type: "disk", SizeBytes: 64 * 1024 * 1024 * 1024, Model: "USB Backup", Transport: "usb"},
		{Name: "sdc1", Path: "/dev/sdc1", Parent: "sdc", Type: "part", SizeBytes: 64 * 1024 * 1024 * 1024, Filesystem: "exfat", UUID: "usb-uuid"},
	}

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-storage", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("new storage returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Storage", "Internal drives", "USB drives", "System Disk", "Data Disk", "USB Backup", "data-storage-disk=\"vda\"", "data-storage-disk=\"sdc\"", "data-storage-partitions=\"vdb\"", "data-storage-partitions=\"sdc\"", "/dev/vdb1", "Minecraft", "/dev/vdb2", "Not formatted", "storage-partition-card storage-partition-swap", "data-storage-detail-dialog", "data-storage-detail-close", "/static/storage-browser.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("new storage browser missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "data-path=\"/dev/vda2\"") {
		t.Fatal("swap partition is interactive")
	}
	if strings.Contains(body, "/dev/zram0") {
		t.Fatal("zram was exposed as physical storage")
	}
	if strings.Contains(body, "/sysroot/ostree") || strings.Contains(body, "/var/lib/containers/storage/overlay") {
		t.Fatal("system partition leaked internal bootc/OSTree mount paths")
	}
	start := strings.Index(body, `data-path="/dev/vda1"`)
	if start < 0 {
		t.Fatal("system partition /dev/vda1 missing")
	}
	end := strings.Index(body[start:], "</button>")
	if end < 0 {
		t.Fatal("system partition button is incomplete")
	}
	systemPartition := body[start : start+end]
	if !strings.Contains(systemPartition, `data-system="Yes"`) || !strings.Contains(systemPartition, "System partition") {
		t.Fatalf("partition did not inherit system protection from parent disk: %s", systemPartition)
	}
}

func TestNewStorageUsesAgentApprovedMinecraftMigrationCandidates(t *testing.T) {
	client := &fakeStorageBrowserMigrationAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.DataPath = "/var/mnt/minecraft/minecraft"
	client.configuration.Minecraft.DataMountPoint = "/var/mnt/minecraft"
	client.configuration.Minecraft.DataExpectedUUID = "minecraft-uuid"
	client.configuration.Backup.Path = "/var/mnt/backups/justvoxel"
	client.configuration.Backup.MountPoint = "/var/mnt/backups"
	client.configuration.Backup.ExpectedUUID = "backup-uuid"
	client.storage.Devices = []api.AdminStorageDevice{
		{Name: "vdb", Path: "/dev/vdb", Type: "disk", SizeBytes: 800 * 1024 * 1024 * 1024, Model: "Data Disk", Transport: "virtio"},
		{Name: "vdb1", Path: "/dev/vdb1", Parent: "vdb", Type: "part", SizeBytes: 200 * 1024 * 1024 * 1024, Filesystem: "xfs", UUID: "minecraft-uuid", Mountpoints: []string{"/var/mnt/minecraft"}},
		{Name: "vdb2", Path: "/dev/vdb2", Parent: "vdb", Type: "part", SizeBytes: 200 * 1024 * 1024 * 1024, Filesystem: "xfs", UUID: "target-uuid"},
		{Name: "vdb3", Path: "/dev/vdb3", Parent: "vdb", Type: "part", SizeBytes: 200 * 1024 * 1024 * 1024, Filesystem: "xfs", UUID: "backup-uuid", Mountpoints: []string{"/var/mnt/backups"}},
		{Name: "vdb4", Path: "/dev/vdb4", Parent: "vdb", Type: "part", SizeBytes: 100 * 1024 * 1024 * 1024, Filesystem: "xfs", UUID: "not-approved"},
		{Name: "vdb5", Path: "/dev/vdb5", Parent: "vdb", Type: "part", SizeBytes: 100 * 1024 * 1024 * 1024, Filesystem: "ext4", UUID: "mounted-target", Mountpoints: []string{"/var/mnt/fast"}},
		{Name: "vdc", Path: "/dev/vdc", Type: "disk", SizeBytes: 10 * 1024 * 1024 * 1024, Model: "Empty Disk", Transport: "virtio"},
	}
	client.migration = api.AdminDataMigrationDiscoveryResponse{
		OK: true, SchemaVersion: "v1",
		WholeDisks: []api.AdminDataMigrationCandidate{
			{Path: "/dev/vdc", SizeBytes: 10 * 1024 * 1024 * 1024, Model: "Empty Disk", Transport: "virtio"},
		},
		Partitions: []api.AdminDataMigrationCandidate{
			{Path: "/dev/vdb1", Filesystem: "xfs", Mountpoint: "/var/mnt/minecraft", SizeBytes: 200 * 1024 * 1024 * 1024},
			{Path: "/dev/vdb2", Filesystem: "xfs", SizeBytes: 200 * 1024 * 1024 * 1024},
			{Path: "/dev/vdb3", Filesystem: "xfs", Mountpoint: "/var/mnt/backups", SizeBytes: 200 * 1024 * 1024 * 1024},
			{Path: "/dev/vdb5", Filesystem: "ext4", Mountpoint: "/var/mnt/fast", SizeBytes: 100 * 1024 * 1024 * 1024},
		},
	}

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-storage", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("new storage returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.migrationCalls != 1 {
		t.Fatalf("migration discovery calls = %d, want 1", client.migrationCalls)
	}

	body := rr.Body.String()
	partitionMarkup := func(path string) string {
		marker := `data-path="` + path + `"`
		start := strings.Index(body, marker)
		if start < 0 {
			t.Fatalf("partition %s missing from New Storage: %s", path, body)
		}
		end := strings.Index(body[start:], "</button>")
		if end < 0 {
			t.Fatalf("partition %s button is incomplete", path)
		}
		return body[start : start+end]
	}

	if !strings.Contains(partitionMarkup("/dev/vdb2"), `data-minecraft-candidate="Yes"`) {
		t.Fatal("Agent-approved unmounted filesystem was not offered for Minecraft migration")
	}
	mounted := partitionMarkup("/dev/vdb5")
	if !strings.Contains(mounted, `data-minecraft-candidate="Yes"`) || !strings.Contains(mounted, `data-minecraft-mount-point="/var/mnt/fast"`) {
		t.Fatalf("Agent-approved mounted filesystem lost migration mount identity: %s", mounted)
	}
	for _, path := range []string{"/dev/vdb1", "/dev/vdb3", "/dev/vdb4"} {
		if !strings.Contains(partitionMarkup(path), `data-minecraft-candidate="No"`) {
			t.Fatalf("%s should not be offered by the New Storage Minecraft shortcut", path)
		}
	}
	for _, want := range []string{
		"Use for Minecraft data",
		`action="/settings/data-migration/review"`,
		`name="operation" value="use_partition"`,
		`data-storage-whole-purpose="minecraft"`,
		`data-storage-whole-purpose="backups"`,
		`data-storage-whole-device="/dev/vdc"`,
		"Prepare for backups",
		"Nothing is erased until Review and the exact confirmation step.",
		"Review migration",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("New Storage Minecraft migration handoff missing %q: %s", want, body)
		}
	}
}

func TestStorageWorkspaceFragmentUsesSameBrowserAndUSBGrouping(t *testing.T) {
	client := &fakeDiscoveryAPI{}
	client.storage.Devices = []api.AdminStorageDevice{
		{Name: "vda", Path: "/dev/vda", Type: "disk", SizeBytes: 100 * 1024 * 1024 * 1024, Model: "Internal", Transport: "virtio"},
		{Name: "sdb", Path: "/dev/sdb", Type: "disk", SizeBytes: 32 * 1024 * 1024 * 1024, Model: "Thumb Drive", Transport: "usb"},
		{Name: "sdb1", Path: "/dev/sdb1", Parent: "sdb", Type: "part", SizeBytes: 32 * 1024 * 1024 * 1024, Filesystem: "exfat", UUID: "thumb-uuid"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/storage", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("storage workspace returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"data-storage-browser-root", "Internal drives", "USB drives", "Thumb Drive", "data-storage-disk=\"sdb\"", "data-storage-partitions=\"sdb\""} {
		if !strings.Contains(body, want) {
			t.Fatalf("storage workspace missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "data-control-center") {
		t.Fatal("storage workspace endpoint returned a full page instead of the reusable browser fragment")
	}
}

func TestStorageBrowserRoleClassification(t *testing.T) {
	configuration := api.AdminConfigurationDiscovery{Configured: true}
	configuration.Minecraft.DataExpectedUUID = "minecraft-uuid"
	configuration.Backup.ExpectedUUID = "backup-uuid"
	cases := []struct {
		name   string
		device api.AdminStorageDevice
		want   string
	}{
		{name: "swap", device: api.AdminStorageDevice{Filesystem: "swap"}, want: "Swap"},
		{name: "system", device: api.AdminStorageDevice{System: true, Filesystem: "xfs"}, want: "System partition"},
		{name: "minecraft", device: api.AdminStorageDevice{Filesystem: "xfs", UUID: "minecraft-uuid"}, want: "Minecraft"},
		{name: "backup", device: api.AdminStorageDevice{Filesystem: "xfs", UUID: "backup-uuid"}, want: "Backups"},
		{name: "blank", device: api.AdminStorageDevice{}, want: "Not formatted"},
		{name: "mounted", device: api.AdminStorageDevice{Filesystem: "xfs", Mountpoints: []string{"/var/mnt/data"}}, want: "Mounted"},
		{name: "available", device: api.AdminStorageDevice{Filesystem: "xfs"}, want: "Available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storageBrowserRole(tc.device, configuration); got != tc.want {
				t.Fatalf("storageBrowserRole() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStorageBrowserFilesystemDisplayNames(t *testing.T) {
	cases := map[string]string{
		"xfs":     "XFS",
		"ext4":    "ext4",
		"btrfs":   "Btrfs",
		"ntfs":    "NTFS",
		"ntfs-3g": "NTFS",
		"vfat":    "FAT / FAT32",
		"fat":     "FAT / FAT32",
		"fat32":   "FAT / FAT32",
		"exfat":   "exFAT",
		"":        "Not formatted",
	}
	for filesystem, want := range cases {
		if got := storageBrowserFilesystemDisplay(filesystem); got != want {
			t.Fatalf("storageBrowserFilesystemDisplay(%q) = %q, want %q", filesystem, got, want)
		}
	}
}
