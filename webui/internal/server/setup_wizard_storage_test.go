package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func setupWizardStorageClient() *fakeDiscoveryAPI {
	client := setupWizardClient()
	client.defaults.DataPath = "/var/lib/justvoxel/minecraft"
	client.defaults.BackupPath = "/var/lib/justvoxel/backups"
	client.defaults.BackupKeep = 7
	client.defaults.BackupDailyTime = "04:30"
	client.storage.SystemDisks = []string{"/dev/vda"}
	client.storage.Devices = []api.AdminStorageDevice{
		{Name: "vda", Path: "/dev/vda", Type: "disk", SizeBytes: 128 * 1024 * 1024 * 1024, Model: "System Disk", Transport: "virtio", System: true},
		{
			Name: "vda1", Path: "/dev/vda1", Parent: "vda", Type: "part", SizeBytes: 32 * 1024 * 1024 * 1024,
			Filesystem: "xfs", UUID: "root-uuid", Mountpoints: []string{"/"}, Model: "System Disk", System: true,
		},
		{
			Name: "vda4", Path: "/dev/vda4", Parent: "vda", Type: "part", SizeBytes: 90 * 1024 * 1024 * 1024,
			Filesystem: "xfs", Label: "LOCAL", UUID: "local-uuid", Model: "System Disk", System: true,
		},
		{Name: "vdb", Path: "/dev/vdb", Type: "disk", SizeBytes: 700 * 1024 * 1024 * 1024, Model: "Samsung SSD", Transport: "sata"},
		{
			Name: "vdb1", Path: "/dev/vdb1", Parent: "vdb", Type: "part", SizeBytes: 500 * 1024 * 1024 * 1024,
			Filesystem: "ext4", Label: "DATA", UUID: "data-uuid", Mountpoints: []string{"/srv/data"}, Model: "Samsung SSD", Transport: "sata",
		},
		{Name: "vdb2", Path: "/dev/vdb2", Parent: "vdb", Type: "part", SizeBytes: 50 * 1024 * 1024 * 1024, Model: "Samsung SSD", Transport: "sata"},
		{Name: "vdc", Path: "/dev/vdc", Type: "disk", SizeBytes: 100 * 1024 * 1024 * 1024, Model: "Old Disk", Transport: "sata"},
		{
			Name: "vdc1", Path: "/dev/vdc1", Parent: "vdc", Type: "part", SizeBytes: 100 * 1024 * 1024 * 1024,
			Filesystem: "ntfs", Label: "UNSUPPORTED", UUID: "ntfs-uuid", Model: "Old Disk", Transport: "sata",
		},
		{Name: "vdd", Path: "/dev/vdd", Type: "disk", SizeBytes: 256 * 1024 * 1024 * 1024, Model: "USB SSD", Transport: "usb"},
		{
			Name: "vdd1", Path: "/dev/vdd1", Parent: "vdd", Type: "part", SizeBytes: 256 * 1024 * 1024 * 1024,
			Filesystem: "xfs", Label: "USB", UUID: "usb-uuid", Model: "USB SSD",
		},
	}
	client.storage.FreeSpaces = []api.AdminStorageFreeSpace{{
		Device: "/dev/vdb", Start: "563201MiB", End: "716800MiB", SizeBytes: 150 * 1024 * 1024 * 1024,
	}}
	return client
}

func TestSetupSameDiskWarningUsesKnownPhysicalDisk(t *testing.T) {
	client := setupWizardStorageClient()
	draft := setupDraft{Inventory: client.storage}
	for index := range draft.Inventory.Devices {
		if draft.Inventory.Devices[index].Path == "/dev/vdb2" {
			draft.Inventory.Devices[index].Filesystem = "xfs"
			draft.Inventory.Devices[index].UUID = "second-data-uuid"
		}
	}
	draft.Inventory.Devices = append(draft.Inventory.Devices, api.AdminStorageDevice{
		Name: "vdb3", Path: "/dev/vdb3", Type: "part", Filesystem: "xfs", UUID: "unknown-parent-uuid",
	})
	draft.Inventory.Devices = append(draft.Inventory.Devices, api.AdminStorageDevice{
		Name: "vda5", Path: "/dev/vda5", Parent: "vda", Type: "part", Filesystem: "xfs", UUID: "second-system-uuid", System: true,
	})
	for _, tc := range []struct {
		name, storageType, storageDevice, backupType, backupDevice string
		warn                                                       bool
	}{
		{"system and system", "system", "", "system", "", true},
		{"system and system partition", "system", "", "partition", "/dev/vda4", true},
		{"system and unrelated disk", "system", "", "partition", "/dev/vdb1", false},
		{"system partition and system", "partition", "/dev/vda4", "system", "", true},
		{"system disk partitions", "partition", "/dev/vda4", "partition", "/dev/vda5", true},
		{"non-system partitions on same disk", "partition", "/dev/vdb1", "partition", "/dev/vdb2", true},
		{"partitions on different disks", "partition", "/dev/vdb1", "partition", "/dev/vdd1", false},
		{"NFS backup", "partition", "/dev/vdb1", "nfs", "", false},
		{"SMB backup", "partition", "/dev/vdb1", "smb", "", false},
		{"unknown parent", "partition", "/dev/vdb1", "partition", "/dev/vdb3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft.Storage.Type, draft.Storage.Device = tc.storageType, tc.storageDevice
			draft.Backups.Type, draft.Backups.Device = tc.backupType, tc.backupDevice
			warning := setupSameDiskWarning(draft)
			if (warning != "") != tc.warn {
				t.Fatalf("same-disk warning = %q, want warning %t", warning, tc.warn)
			}
		})
	}
}

func TestSetupSameDiskWarningAppearsOnlyOnBackupsStep(t *testing.T) {
	markup, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(markup)
	backupsStart := strings.Index(source, "{{else if eq .CurrentStep 6}}")
	if backupsStart < 0 {
		t.Fatal("Backups step start is missing")
	}
	relativeEnd := strings.Index(source[backupsStart:], "{{else}}\n    <div class=\"setup-placeholder\"")
	if relativeEnd < 0 {
		t.Fatal("Backups step boundaries are missing")
	}
	backupsEnd := backupsStart + relativeEnd
	if strings.Count(source, "{{if .SameDiskWarning}}") != 1 || !strings.Contains(source[backupsStart:backupsEnd], `data-setup-same-disk-warning`) {
		t.Fatal("draft same-disk warning must appear only on Backups")
	}
}

func advanceToStorage(t *testing.T, app *App) {
	t.Helper()
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("connections save returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr := saveResourcesStep(t, app, validResourceValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("resources save returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr := saveMinecraftStep(t, app, validMinecraftValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("Minecraft save returned %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSetupWizardStorageStepShowsInternalDiskBrowserAndExcludesUSB(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("storage page returned %d: %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"<h2>Storage</h2>", "Use system storage", "Use another internal disk", "/dev/vda4", "/dev/vdb1", "/dev/vdb2",
		"Samsung SSD", "ext4", "Unallocated", "Create one XFS partition using this free space",
		"External drives are not offered for Minecraft data", "/static/setup-storage.js",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("storage page missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"/dev/vda1", "/dev/vdc1", "UNSUPPORTED", "/dev/vdd1", "USB SSD"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unsafe or external filesystem %q was offered: %s", forbidden, body)
		}
	}
}

func TestSetupWizardStorageRejectsUSBFilesystemEvenWhenPostedDirectly(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)

	values := url.Values{
		"csrf": {"csrf-token"}, "storage_type": {"partition"}, "storage_device": {"/dev/vdd1"},
		"storage_mount_point": {"/var/mnt/justvoxel-data"}, "storage_path": {"/var/mnt/justvoxel-data/minecraft"}, "direction": {"next"},
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", values.Encode()))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "safe internal XFS, ext4, or Btrfs") {
		t.Fatalf("USB Minecraft storage was not rejected: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSetupWizardStorageMissingPartitionKeepsInternalMode(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)
	values := url.Values{"csrf": {"csrf-token"}, "storage_type": {"partition"}, "direction": {"next"}}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", values.Encode()))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Choose a partition before continuing.") || !strings.Contains(rr.Body.String(), `data-setup-storage-internal-panel`) {
		t.Fatalf("missing storage partition did not keep chooser: %d %s", rr.Code, rr.Body.String())
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.Storage.Type != "partition" || draft.Storage.Device != "" || draft.CurrentStep != 5 {
		t.Fatalf("storage mode changed after validation error: %#v", draft.Storage)
	}
}

func TestSetupWizardBackupMissingPartitionKeepsLocalMode(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)
	storage := url.Values{"csrf": {"csrf-token"}, "storage_type": {"system"}, "direction": {"next"}}
	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode())); rr.Code != http.StatusSeeOther {
		t.Fatalf("storage save: %d", rr.Code)
	}
	values := url.Values{"csrf": {"csrf-token"}, "backup_type": {"partition"}, "backup_keep": {"7"}, "backup_automatic": {"on"}, "backup_daily_time": {"04:30"}, "direction": {"next"}}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", values.Encode()))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Choose a partition before continuing.") || !strings.Contains(rr.Body.String(), `data-setup-backup-panel="partition"`) {
		t.Fatalf("missing backup partition did not keep chooser: %d %s", rr.Code, rr.Body.String())
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.Backups.Type != "partition" || draft.Backups.Device != "" || draft.CurrentStep != 6 {
		t.Fatalf("backup mode changed after validation error: %#v", draft.Backups)
	}
}
func TestSetupWizardStorageUsesSharedReviewedStorageActions(t *testing.T) {
	template, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(template)
	for _, want := range []string{
		"data-setup-storage-prepare=\"format\"",
		"data-setup-storage-prepare=\"create_partition\"",
		"data-setup-storage-confirm-slider",
		"data-setup-storage-confirm-toggle",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("setup Storage shared action UI missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/setup-storage.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, want := range []string{
		"fetch('/api/new-storage/actions/' + phase",
		"body.set('fingerprint', reviewed?.proposed?.fingerprint || '')",
		"body.set('confirmation', reviewed?.proposed?.confirmation || '')",
		"operation === 'create_partition' ? 'all'",
		"window.location.reload()",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("setup Storage shared action behavior missing %q", want)
		}
	}
}
func TestSetupWizardStorageStepValidatesAndAdvances(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)

	bad := url.Values{
		"storage_type":        {"partition"},
		"storage_device":      {"/dev/vdb1"},
		"storage_mount_point": {"/wrong"},
		"storage_path":        {"/wrong/minecraft"},
		"direction":           {"next"},
		"csrf":                {"csrf-token"},
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", bad.Encode()))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "already mounted at /srv/data") {
		t.Fatalf("mounted filesystem mismatch was not rejected: %d %s", rr.Code, rr.Body.String())
	}

	good := url.Values{
		"csrf":                {"csrf-token"},
		"storage_type":        {"partition"},
		"storage_device":      {"/dev/vdb1"},
		"storage_mount_point": {"/srv/data"},
		"storage_path":        {"/srv/data/minecraft"},
		"direction":           {"next"},
	}
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", good.Encode()))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("valid storage step returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<h2>Backups</h2>") || !strings.Contains(page.Body.String(), "Internal or USB drive") {
		t.Fatalf("storage step did not advance to backups: %d %s", page.Code, page.Body.String())
	}
}

func TestSetupWizardBackupStepSupportsLocalNFSAndSMBWithoutPasswordDraft(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)

	storage := url.Values{
		"csrf":         {"csrf-token"},
		"storage_type": {"system"},
		"storage_path": {"/var/lib/justvoxel/minecraft"},
		"direction":    {"next"},
	}
	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode())); rr.Code != http.StatusSeeOther {
		t.Fatalf("storage save returned %d: %s", rr.Code, rr.Body.String())
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := page.Body.String()
	for _, want := range []string{"Automatic backups", "04:30", "System storage", "Internal or USB drive", "NFS share", "SMB share", "Password will be requested when setup starts.", `data-setup-same-disk-warning`, "Backups are on the same disk", "If this disk fails, both Minecraft and its backups could be lost.", "USB SSD", " · USB</span>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("backup page missing %q: %s", want, body)
		}
	}
	usbCardStart := strings.Index(body, `data-setup-disk="/dev/vdd"`)
	if usbCardStart < 0 {
		t.Fatal("backup page must offer the USB physical disk for selection")
	}
	usbCard, _, found := strings.Cut(body[usbCardStart:], "</button>")
	if !found || !strings.Contains(usbCard, "<strong>USB SSD</strong>") || !strings.Contains(usbCard, " · USB</span>") || !strings.Contains(usbCard, "<code>/dev/vdd</code>") {
		t.Fatal("USB backup disk card must retain its model, USB label and device identity")
	}
	if strings.Contains(body, `name="backup_password"`) || strings.Contains(body, `type="password"`) {
		t.Fatal("SMB password field must not be part of the A4.3 setup draft")
	}

	smb := url.Values{
		"csrf":               {"csrf-token"},
		"backup_automatic":   {"on"},
		"backup_daily_time":  {"04:30"},
		"backup_keep":        {"7"},
		"backup_type":        {"smb"},
		"backup_source":      {"//nas/backups"},
		"backup_username":    {"minecraft"},
		"backup_domain":      {"HOME"},
		"backup_mount_point": {"/var/mnt/justvoxel-backup"},
		"backup_path":        {"/var/mnt/justvoxel-backup/backups"},
		"direction":          {"next"},
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", smb.Encode()))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/review" {
		t.Fatalf("SMB backup draft returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	draft, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok || draft.CurrentStep != 7 || draft.Backups.Type != "smb" || draft.Backups.Source != "//nas/backups" || draft.Backups.Username != "minecraft" {
		t.Fatalf("SMB backup draft was not preserved: %#v", draft)
	}
	if strings.Contains(strings.ToLower(fmt.Sprintf("%#v", draft.Backups)), "password") {
		t.Fatal("backup draft unexpectedly contains a password field")
	}
}

func TestSetupWizardBackupAllowsExternalUSBFilesystem(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)

	storage := url.Values{"csrf": {"csrf-token"}, "storage_type": {"system"}, "storage_path": {"/var/lib/justvoxel/minecraft"}, "direction": {"next"}}
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode()))

	usb := url.Values{
		"csrf": {"csrf-token"}, "backup_automatic": {"on"}, "backup_daily_time": {"04:30"}, "backup_keep": {"7"},
		"backup_type": {"partition"}, "backup_device": {"/dev/vdd1"}, "backup_mount_point": {"/var/mnt/justvoxel-backup"},
		"backup_path": {"/var/mnt/justvoxel-backup/backups"}, "direction": {"next"},
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", usb.Encode()))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/review" {
		t.Fatalf("USB backup filesystem was not accepted: %d %q %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	draft, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok || draft.Backups.Type != "partition" || draft.Backups.Device != "/dev/vdd1" {
		t.Fatalf("USB backup draft was not preserved: %#v", draft)
	}
}

func TestSetupWizardBackupUsesSharedReviewedStorageActions(t *testing.T) {
	template, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(template)
	for _, want := range []string{
		"data-setup-backup-prepare=\"format\"",
		"data-setup-backup-prepare=\"create_partition\"",
		"data-setup-backup-confirm-slider",
		"data-setup-backup-confirm-toggle",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("setup Backup shared action UI missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/setup-storage.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, want := range []string{
		"fetch('/api/new-storage/actions/' + phase",
		"body.set('fingerprint', reviewed?.proposed?.fingerprint || '')",
		"body.set('confirmation', reviewed?.proposed?.confirmation || '')",
		"justvoxel-setup-backup-disk",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("setup Backup shared action behavior missing %q", want)
		}
	}
}

func TestSetupWizardBackupStepRejectsBadNetworkTarget(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)

	storage := url.Values{"csrf": {"csrf-token"}, "storage_type": {"system"}, "storage_path": {"/var/lib/justvoxel/minecraft"}, "direction": {"next"}}
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode()))

	badNFS := url.Values{
		"csrf": {"csrf-token"}, "backup_automatic": {"on"}, "backup_daily_time": {"04:30"}, "backup_keep": {"7"},
		"backup_type": {"nfs"}, "backup_source": {"not-a-share"}, "backup_mount_point": {"/var/mnt/justvoxel-backup"}, "backup_path": {"/var/mnt/justvoxel-backup/backups"}, "direction": {"next"},
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", badNFS.Encode()))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "server:/export") {
		t.Fatalf("invalid NFS source was not rejected: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSetupWizardStorageAndBackupBackPreserveDraft(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)

	storage := url.Values{
		"csrf": {"csrf-token"}, "storage_type": {"partition"}, "storage_device": {"/dev/vda4"},
		"storage_mount_point": {"/var/mnt/justvoxel-data"}, "storage_path": {"/var/mnt/justvoxel-data/minecraft"}, "direction": {"next"},
	}
	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode())); rr.Code != http.StatusSeeOther {
		t.Fatalf("storage save returned %d: %s", rr.Code, rr.Body.String())
	}

	backups := url.Values{
		"csrf": {"csrf-token"}, "backup_automatic": {"on"}, "backup_daily_time": {"05:15"}, "backup_keep": {"9"},
		"backup_type": {"nfs"}, "backup_source": {"nas:/backups"}, "backup_mount_point": {"/var/mnt/nas-backup"}, "backup_path": {"/var/mnt/nas-backup/justvoxel"}, "direction": {"back"},
	}
	back := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", backups.Encode()))
	if back.Code != http.StatusSeeOther {
		t.Fatalf("backup back returned %d: %s", back.Code, back.Body.String())
	}
	storagePage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	for _, want := range []string{"<h2>Storage</h2>", "/dev/vda4", "/var/mnt/justvoxel-data/minecraft"} {
		if !strings.Contains(storagePage.Body.String(), want) {
			t.Fatalf("storage draft lost %q: %s", want, storagePage.Body.String())
		}
	}

	storage.Set("direction", "next")
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode()))
	backupPage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	for _, want := range []string{"05:15", "nas:/backups", "/var/mnt/nas-backup/justvoxel"} {
		if !strings.Contains(backupPage.Body.String(), want) {
			t.Fatalf("backup draft lost %q: %s", want, backupPage.Body.String())
		}
	}
}

func TestSetupBackupSharedAndIndependentPartitionMounts(t *testing.T) {
	for _, tc := range []struct{ name, device, mount, wantMount, wantPath string }{
		{"shared", "/dev/vdb1", "/var/mnt/justvoxel-backup", "/srv/data", "/srv/data/backups"},
		{"independent", "/dev/vda4", "/var/mnt/justvoxel-backup", "/var/mnt/justvoxel-backup", "/var/mnt/justvoxel-backup/backups"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := New(setupWizardStorageClient(), Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			advanceToStorage(t, app)
			storage := url.Values{"csrf": {"csrf-token"}, "storage_type": {"partition"}, "storage_device": {"/dev/vdb1"}, "storage_mount_point": {"/srv/data"}, "storage_path": {"/srv/data/minecraft"}, "direction": {"next"}}
			if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode())); rr.Code != http.StatusSeeOther {
				t.Fatalf("storage: %d %s", rr.Code, rr.Body.String())
			}
			backup := url.Values{"csrf": {"csrf-token"}, "backup_type": {"partition"}, "backup_device": {tc.device}, "backup_mount_point": {tc.mount}, "backup_path": {tc.mount + "/backups"}, "backup_keep": {"7"}, "backup_daily_time": {"04:30"}, "direction": {"next"}}
			if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", backup.Encode())); rr.Code != http.StatusSeeOther {
				t.Fatalf("backup: %d %s", rr.Code, rr.Body.String())
			}
			draft, _ := firstRunSetupDrafts.get(app, "session-token")
			if draft.Backups.MountPoint != tc.wantMount || draft.Backups.Path != tc.wantPath || setupPathsOverlap(draft.Storage.Path, draft.Backups.Path) {
				t.Fatalf("invalid mount layout: %#v %#v", draft.Storage, draft.Backups)
			}
		})
	}
}
