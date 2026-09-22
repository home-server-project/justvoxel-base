package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNewBackupsAPI struct {
	fakeAPI
	role                    string
	backups                 api.AdminRestoreBackupsResponse
	backupResult            api.ManualBackupResponse
	backupErr               error
	backupCalls             int
	discoveryCalls          int
	configuration           api.AdminConfigurationDiscovery
	configurationPlan       api.AdminConfigurationChangeResponse
	configurationApply      api.AdminConfigurationChangeResponse
	configurationPlanErr    error
	configurationApplyErr   error
	plannedConfiguration    api.AdminConfigurationChangeRequest
	appliedConfiguration    api.AdminConfigurationChangeRequest
	configurationPlanCalls  int
	configurationApplyCalls int
	storage                 api.AdminStorageDiscovery
	destinationStatus       api.AdminBackupStorageResponse
	destinationPlan         api.AdminBackupStorageResponse
	destinationApply        api.AdminBackupStorageResponse
	plannedDestination      api.AdminBackupStorageRequest
	appliedDestination      api.AdminBackupStorageRequest
	destinationStatusCalls  int
	destinationPlanCalls    int
	destinationApplyCalls   int
	restorePlanResult       api.AdminRestorePlanResponse
	restorePlanErr          error
	restoreApplyResult      api.AdminRestoreApplyResponse
	restoreApplyErr         error
	restorePlanRequest      api.AdminRestorePlanRequest
	restoreApplyRequest     api.AdminRestoreApplyRequest
	restorePlanCalls        int
	restoreApplyCalls       int
	currentRestoreOperation *api.PersistentOperation
	restoreOperation        *api.PersistentOperation
}

func defaultNewBackupsConfiguration() api.AdminConfigurationDiscovery {
	return api.AdminConfigurationDiscovery{
		Configured: true,
		Minecraft: api.AdminMinecraftConfiguration{
			JavaMemory: "4G", ContainerMemory: "6G", JavaPort: 25565,
			BedrockEnabled: false, BedrockPort: 19132, Timezone: "UTC",
			MaxPlayers: 10, MOTD: "JustVoxel", ImageTag: "stable",
			VersionMode: "pinned", Version: "26.3",
		},
		Backup: api.AdminBackupConfiguration{
			Keep: 7, Schedule: "*-*-* 04:30:00", TimerEnabled: true,
		},
	}
}

func (f *fakeNewBackupsAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeNewBackupsAPI) ManualBackup(_ context.Context, session string) (api.ManualBackupResponse, error) {
	if session != "session-token" {
		return api.ManualBackupResponse{}, api.ErrUnauthorized
	}
	f.backupCalls++
	if f.backupResult.Message == "" && f.backupErr == nil {
		return api.ManualBackupResponse{OK: true, Message: "Manual backup requested."}, nil
	}
	return f.backupResult, f.backupErr
}

func (f *fakeNewBackupsAPI) AdminRestoreBackups(_ context.Context, session string) (api.AdminRestoreBackupsResponse, error) {
	if session != "session-token" {
		return api.AdminRestoreBackupsResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.backups, nil
}

func (f *fakeNewBackupsAPI) AdminConfiguration(_ context.Context, session string) (api.AdminConfigurationDiscovery, error) {
	if session != "session-token" {
		return api.AdminConfigurationDiscovery{}, api.ErrUnauthorized
	}
	if f.configuration.Configured {
		return f.configuration, nil
	}
	return defaultNewBackupsConfiguration(), nil
}

func (f *fakeNewBackupsAPI) AdminConfigurationPlan(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.configurationPlanCalls++
	f.plannedConfiguration = request
	if f.configurationPlan.OK || f.configurationPlanErr != nil {
		return f.configurationPlan, f.configurationPlanErr
	}
	return api.AdminConfigurationChangeResponse{
		OK:       true,
		Changes:  []api.AdminConfigurationChange{},
		Proposed: defaultNewBackupsConfiguration(),
	}, nil
}

func (f *fakeNewBackupsAPI) AdminConfigurationApply(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.configurationApplyCalls++
	f.appliedConfiguration = request
	if f.configurationApply.OK || f.configurationApplyErr != nil {
		return f.configurationApply, f.configurationApplyErr
	}
	return api.AdminConfigurationChangeResponse{OK: true, Applied: true}, nil
}

func (f *fakeNewBackupsAPI) AdminStorage(_ context.Context, session string) (api.AdminStorageDiscovery, error) {
	if session != "session-token" {
		return api.AdminStorageDiscovery{}, api.ErrUnauthorized
	}
	if len(f.storage.Devices) > 0 || len(f.storage.SystemDisks) > 0 {
		return f.storage, nil
	}
	return api.AdminStorageDiscovery{
		SystemDisks: []string{"/dev/vda"},
		Devices: []api.AdminStorageDevice{
			{Name: "vda1", Path: "/dev/vda1", Parent: "vda", Type: "part", SizeBytes: 20 * 1024 * 1024 * 1024, Filesystem: "xfs", Mountpoints: []string{"/"}, System: true},
			{Name: "vdb1", Path: "/dev/vdb1", Parent: "vdb", Type: "part", SizeBytes: 100 * 1024 * 1024 * 1024, Filesystem: "xfs", Label: "BACKUP", UUID: "backup-uuid", Mountpoints: []string{"/var/mnt/backup"}, Model: "Virtual Disk"},
			{Name: "vdc1", Path: "/dev/vdc1", Parent: "vdc", Type: "part", SizeBytes: 50 * 1024 * 1024 * 1024, Filesystem: "", Model: "Blank Disk"},
		},
	}, nil
}

func (f *fakeNewBackupsAPI) AdminBackupStorageStatus(_ context.Context, session string) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.destinationStatusCalls++
	if f.destinationStatus.OK {
		return f.destinationStatus, nil
	}
	return api.AdminBackupStorageResponse{OK: true, Current: api.AdminBackupStorageTarget{
		Status: "ready", StatusDetail: "Backup destination is ready.", Type: "system", Path: "/var/lib/justvoxel/backups",
		AvailableBytes: 40 * 1024 * 1024 * 1024, FilesystemBytes: 80 * 1024 * 1024 * 1024, SamePhysicalDisk: true,
	}}, nil
}

func (f *fakeNewBackupsAPI) AdminBackupStoragePlan(_ context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.destinationPlanCalls++
	f.plannedDestination = request
	if f.destinationPlan.OK {
		return f.destinationPlan, nil
	}
	return api.AdminBackupStorageResponse{OK: true, Changed: true, Proposed: api.AdminBackupStorageTarget{
		Type: request.Type, Path: request.Path, MountPoint: request.MountPoint, ExpectedSource: request.Source,
	}}, nil
}

func (f *fakeNewBackupsAPI) AdminBackupStorageApply(_ context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.destinationApplyCalls++
	f.appliedDestination = request
	if f.destinationApply.OK {
		return f.destinationApply, nil
	}
	return api.AdminBackupStorageResponse{OK: true, Changed: true, Applied: true, Proposed: api.AdminBackupStorageTarget{Type: request.Type, Path: request.Path}}, nil
}

func (f *fakeNewBackupsAPI) AdminRestorePlan(_ context.Context, session string, request api.AdminRestorePlanRequest) (api.AdminRestorePlanResponse, error) {
	if session != "session-token" {
		return api.AdminRestorePlanResponse{}, api.ErrUnauthorized
	}
	f.restorePlanCalls++
	f.restorePlanRequest = request
	if f.restorePlanResult.SchemaVersion != "" || f.restorePlanErr != nil {
		return f.restorePlanResult, f.restorePlanErr
	}
	return restorePlan(false), nil
}

func (f *fakeNewBackupsAPI) AdminRestoreApply(_ context.Context, session string, request api.AdminRestoreApplyRequest) (api.AdminRestoreApplyResponse, error) {
	if session != "session-token" {
		return api.AdminRestoreApplyResponse{}, api.ErrUnauthorized
	}
	f.restoreApplyCalls++
	f.restoreApplyRequest = request
	return f.restoreApplyResult, f.restoreApplyErr
}

func (f *fakeNewBackupsAPI) AdminCurrentRestoreOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.currentRestoreOperation == nil {
		return api.PersistentOperationResponse{}, nil
	}
	copy := *f.currentRestoreOperation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func (f *fakeNewBackupsAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.restoreOperation == nil || f.restoreOperation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	copy := *f.restoreOperation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func TestNewBackupsPageListsExistingBackups(t *testing.T) {
	client := &fakeNewBackupsAPI{backups: api.AdminRestoreBackupsResponse{Backups: []api.AdminRestoreBackup{
		{
			ID: "minecraft-2026-09-22-120000.tar.gz", CreatedAt: "2026-09-22T16:00:00Z",
			SizeBytes: 1024 * 1024 * 512, MetadataStatus: "valid",
			Metadata: &api.AdminRestoreBackupMetadata{
				Minecraft: api.AdminRestoreMetadataMinecraft{ConfiguredVersion: "26.3"},
				Bedrock:   api.AdminRestoreMetadataBedrock{Enabled: true},
				JustVoxel: api.AdminRestoreMetadataJustVoxel{Variant: "VM"},
			},
		},
		{
			ID: "minecraft-2026-09-21-120000.tar.gz", CreatedAt: "2026-09-21T16:00:00Z",
			SizeBytes: 1024 * 1024 * 256, MetadataStatus: "missing",
		},
	}}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("new backups page returned %d: %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"New Backups",
		"Backup Now",
		"minecraft-2026-09-22-120000.tar.gz",
		"minecraft-2026-09-21-120000.tar.gz",
		"Minecraft 26.3",
		"Bedrock",
		"VM",
		"Metadata missing",
		"768.0 MiB",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("new backups page missing %q: %s", want, body)
		}
	}
	if client.discoveryCalls != 1 {
		t.Fatalf("backup discovery calls = %d, want 1", client.discoveryCalls)
	}
}

func TestNewBackupsPageIsAdministratorOnly(t *testing.T) {
	client := &fakeNewBackupsAPI{role: "operator"}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups", ""))
	if page.Code != http.StatusForbidden {
		t.Fatalf("operator new backups page status = %d, want 403", page.Code)
	}
	if client.discoveryCalls != 0 || client.backupCalls != 0 {
		t.Fatalf("backup API used for non-admin: discovery=%d backup=%d", client.discoveryCalls, client.backupCalls)
	}
}

func TestNewBackupsBackupNowUsesExistingManualBackupAPI(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/backup", "csrf=csrf-token"))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/new-backups?result=backup" {
		t.Fatalf("backup now returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.backupCalls != 1 {
		t.Fatalf("manual backup calls = %d, want 1", client.backupCalls)
	}

	resultPage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups?result=backup", ""))
	if resultPage.Code != http.StatusOK || !strings.Contains(resultPage.Body.String(), "Backup started. It will appear in the library when the backup service finishes.") {
		t.Fatalf("backup result page returned %d: %s", resultPage.Code, resultPage.Body.String())
	}
}

func TestNewBackupsBackupNowRejectsBadCSRF(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/backup", "csrf=wrong"))
	if page.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF backup status = %d, want 403", page.Code)
	}
	if client.backupCalls != 0 {
		t.Fatalf("manual backup API ran after CSRF rejection: %d", client.backupCalls)
	}
}

func TestNewBackupsPageShowsAutomaticPolicy(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("new backups page returned %d: %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{"Schedule & retention", "Enable automatic backups", "value=\"04:30\"", "value=\"7\"", "UTC"} {
		if !strings.Contains(body, want) {
			t.Fatalf("automatic backup panel missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "*-*-* 04:30:00") {
		t.Fatal("New Backups exposed the raw systemd calendar string")
	}
}

func TestBackupDailyTimeFromSchedule(t *testing.T) {
	if got, ok := backupDailyTimeFromSchedule("*-*-* 04:30:00"); !ok || got != "04:30" {
		t.Fatalf("daily schedule parsed as %q, %v", got, ok)
	}
	for _, schedule := range []string{"hourly", "*-*-* 04:30:15", "Mon *-*-* 04:30:00"} {
		if got, ok := backupDailyTimeFromSchedule(schedule); ok || got != "" {
			t.Fatalf("unsupported schedule %q parsed as %q, %v", schedule, got, ok)
		}
	}
}

func TestNewBackupsAutomaticPlanPreservesMinecraftConfiguration(t *testing.T) {
	client := &fakeNewBackupsAPI{
		configurationPlan: api.AdminConfigurationChangeResponse{
			OK: true,
			Changes: []api.AdminConfigurationChange{
				{Field: "backup_schedule", Label: "Automatic backup schedule", Before: "*-*-* 04:30:00", After: "*-*-* 05:15:00"},
				{Field: "backup_keep", Label: "Backups retained", Before: "7", After: "12"},
			},
		},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&automatic_enabled=on&daily_time=05%3A15&backup_keep=12"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/automatic/plan", body))
	if page.Code != http.StatusOK {
		t.Fatalf("automatic plan returned %d: %s", page.Code, page.Body.String())
	}
	if client.configurationPlanCalls != 1 {
		t.Fatalf("configuration plan calls = %d, want 1", client.configurationPlanCalls)
	}
	request := client.plannedConfiguration
	if request.BackupKeep != 12 || request.BackupSchedule != "*-*-* 05:15:00" || !request.BackupTimerEnabled {
		t.Fatalf("unexpected automatic backup request: %#v", request)
	}
	if request.JavaMemory != "4G" || request.ContainerMemory != "6G" || request.JavaPort != 25565 ||
		request.Timezone != "UTC" || request.MaxPlayers != 10 || request.Version != "26.3" {
		t.Fatalf("automatic backup plan did not preserve Minecraft configuration: %#v", request)
	}
	for _, want := range []string{"Automatic backup changes", "05:15", "12 backups"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("automatic review missing %q: %s", want, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), "*-*-* 05:15:00") {
		t.Fatal("automatic review exposed the raw systemd calendar string")
	}
}

func TestNewBackupsAutomaticApplyRevalidatesAndApplies(t *testing.T) {
	client := &fakeNewBackupsAPI{
		configurationPlan: api.AdminConfigurationChangeResponse{
			OK: true,
			Changes: []api.AdminConfigurationChange{
				{Field: "backup_timer_enabled", Label: "Automatic backups", Before: "yes", After: "no"},
				{Field: "backup_keep", Label: "Backups retained", Before: "7", After: "5"},
			},
		},
		configurationApply: api.AdminConfigurationChangeResponse{OK: true, Applied: true},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&daily_time=03%3A45&backup_keep=5"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/automatic/apply", body))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/new-backups?result=automatic" {
		t.Fatalf("automatic apply returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.configurationPlanCalls != 1 || client.configurationApplyCalls != 1 {
		t.Fatalf("plan calls=%d apply calls=%d", client.configurationPlanCalls, client.configurationApplyCalls)
	}
	if client.appliedConfiguration.BackupTimerEnabled || client.appliedConfiguration.BackupKeep != 5 ||
		client.appliedConfiguration.BackupSchedule != "*-*-* 03:45:00" {
		t.Fatalf("unexpected applied automatic backup request: %#v", client.appliedConfiguration)
	}
}

func TestNewBackupsPageShowsCompactBackupDestination(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("new backups destination page returned %d: %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"Backup destination", "Backup destination is ready.", "/var/lib/justvoxel/backups", "40.0 GiB", "80.0 GiB",
		"Same physical disk", "Change destination", "Directory on this JustVoxel system",
		"Existing local partition / filesystem", "NFS share", "SMB / CIFS share", "/dev/vdb1", "Virtual Disk",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("backup destination panel missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "/dev/vda1") || strings.Contains(body, "/dev/vdc1") {
		t.Fatalf("unsafe or unformatted partition offered by New Backups: %s", body)
	}
	if client.destinationStatusCalls != 1 {
		t.Fatalf("destination status calls = %d, want 1", client.destinationStatusCalls)
	}
}

func TestNewBackupsDestinationPlanKeepsSMBPasswordOutOfReview(t *testing.T) {
	client := &fakeNewBackupsAPI{
		destinationPlan: api.AdminBackupStorageResponse{OK: true, Changed: true, Proposed: api.AdminBackupStorageTarget{
			Type: "smb", Path: "/var/mnt/justvoxel-backup/backups", MountPoint: "/var/mnt/justvoxel-backup",
			ExpectedSource: "//nas/backups", AvailableBytes: 60 * 1024 * 1024 * 1024,
			FilesystemBytes: 100 * 1024 * 1024 * 1024, CredentialsNeeded: true,
		}},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&type=smb&path=%2Fvar%2Fmnt%2Fjustvoxel-backup%2Fbackups&mount_point=%2Fvar%2Fmnt%2Fjustvoxel-backup&source=%2F%2Fnas%2Fbackups&username=backup-user&domain=HOME&password=do-not-review"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/destination/plan", body))
	if page.Code != http.StatusOK {
		t.Fatalf("destination plan returned %d: %s", page.Code, page.Body.String())
	}
	if client.destinationPlanCalls != 1 || client.destinationApplyCalls != 0 {
		t.Fatalf("destination plan calls=%d apply calls=%d", client.destinationPlanCalls, client.destinationApplyCalls)
	}
	if client.plannedDestination.Password != "" {
		t.Fatalf("SMB password reached destination Review: %q", client.plannedDestination.Password)
	}
	for _, want := range []string{"Proposed destination", "//nas/backups", "60.0 GiB", "SMB password", "Apply destination"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("destination review missing %q: %s", want, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), "do-not-review") {
		t.Fatal("SMB password leaked into New Backups destination review")
	}
}

func TestNewBackupsDestinationApplyPassesPasswordOnlyAtApply(t *testing.T) {
	client := &fakeNewBackupsAPI{
		destinationApply: api.AdminBackupStorageResponse{OK: true, Changed: true, Applied: true, Proposed: api.AdminBackupStorageTarget{
			Type: "smb", Path: "/var/mnt/justvoxel-backup/backups",
		}},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&type=smb&path=%2Fvar%2Fmnt%2Fjustvoxel-backup%2Fbackups&mount_point=%2Fvar%2Fmnt%2Fjustvoxel-backup&source=%2F%2Fnas%2Fbackups&username=backup-user&domain=HOME&password=secret-at-apply"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/destination/apply", body))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/new-backups?result=destination" {
		t.Fatalf("destination apply returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.destinationApplyCalls != 1 || client.appliedDestination.Password != "secret-at-apply" {
		t.Fatalf("destination apply request=%#v calls=%d", client.appliedDestination, client.destinationApplyCalls)
	}
}

func TestNewBackupsPageOffersRestoreForSingleSelection(t *testing.T) {
	client := &fakeNewBackupsAPI{backups: api.AdminRestoreBackupsResponse{Backups: []api.AdminRestoreBackup{{
		ID: "minecraft-2026-09-22-120000.tar.gz", CreatedAt: "2026-09-22T16:00:00Z", SizeBytes: 1024, MetadataStatus: "missing",
	}}}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("new backups Restore page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Restore selected", "Choose Restore scope", "Restore world", "Restore full Minecraft data",
		"action=\"/settings/new-backups/restore/plan\"", "Nothing changes during Review",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("New Backups Restore UI missing %q: %s", want, page.Body.String())
		}
	}
}

func TestNewBackupsRestorePlanShowsAuthoritativeReview(t *testing.T) {
	client := &fakeNewBackupsAPI{restorePlanResult: restorePlan(true)}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/restore/plan", body))
	if page.Code != http.StatusOK {
		t.Fatalf("New Backups Restore plan returned %d: %s", page.Code, page.Body.String())
	}
	if client.restorePlanCalls != 1 || client.restoreApplyCalls != 0 {
		t.Fatalf("Restore plan calls=%d apply calls=%d", client.restorePlanCalls, client.restoreApplyCalls)
	}
	for _, want := range []string{
		"Restore review", "Restore world", "This backup is older than the configured Minecraft version.",
		"PlayerOne", "Type RESTORE to continue", "name=\"players_confirmed\"", "Safety checks remain Agent-owned.",
		"action=\"/settings/new-backups/restore/apply\"",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("New Backups Restore review missing %q: %s", want, page.Body.String())
		}
	}
}

func TestNewBackupsRestoreApplyRejectsStalePlanAndWrongConfirmation(t *testing.T) {
	t.Run("stale plan", func(t *testing.T) {
		client := &fakeNewBackupsAPI{restorePlanResult: restorePlan(false)}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		body := "csrf=csrf-token&backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world&plan_fingerprint=sha256%3Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&confirmation=RESTORE"
		page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/restore/apply", body))
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "changed since it was reviewed") {
			t.Fatalf("stale Restore response=%d: %s", page.Code, page.Body.String())
		}
		if client.restoreApplyCalls != 0 {
			t.Fatal("stale Restore reached Apply API")
		}
	})

	t.Run("wrong confirmation", func(t *testing.T) {
		client := &fakeNewBackupsAPI{restorePlanResult: restorePlan(false)}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		body := "csrf=csrf-token&backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world&plan_fingerprint=" + restorePageFingerprint + "&confirmation=restore"
		page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/restore/apply", body))
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Type RESTORE exactly") {
			t.Fatalf("wrong Restore confirmation response=%d: %s", page.Code, page.Body.String())
		}
		if client.restoreApplyCalls != 0 {
			t.Fatal("wrong Restore confirmation reached Apply API")
		}
	})
}

func TestNewBackupsRestoreApplyStartsAndShowsPersistentOperation(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: restorePageOperationID, OperationType: "restore",
		PlanFingerprint: restorePageFingerprint, State: "queued", Stage: "queued", Status: "Restore operation queued.",
		StartedAt: "2026-09-22T12:00:00Z", UpdatedAt: "2026-09-22T12:00:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeNewBackupsAPI{
		restorePlanResult:  restorePlan(true),
		restoreApplyResult: api.AdminRestoreApplyResponse{OK: true, Created: true, Operation: operation},
		restoreOperation:   operation,
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world&plan_fingerprint=" + restorePageFingerprint + "&confirmation=RESTORE&players_confirmed=yes"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/restore/apply", body))
	if page.Code != http.StatusSeeOther {
		t.Fatalf("New Backups Restore apply returned %d: %s", page.Code, page.Body.String())
	}
	wantLocation := "/settings/new-backups?result=restore&restore_operation=" + restorePageOperationID
	if page.Header().Get("Location") != wantLocation {
		t.Fatalf("Restore redirect=%q want %q", page.Header().Get("Location"), wantLocation)
	}
	if client.restoreApplyCalls != 1 || !client.restoreApplyRequest.DestructiveConfirmed || !client.restoreApplyRequest.PlayersConfirmed {
		t.Fatalf("unexpected Restore apply request: %#v calls=%d", client.restoreApplyRequest, client.restoreApplyCalls)
	}

	progress := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example"+wantLocation, ""))
	if progress.Code != http.StatusOK {
		t.Fatalf("New Backups Restore progress returned %d: %s", progress.Code, progress.Body.String())
	}
	for _, want := range []string{"Restore progress", "Queued", "Waiting to start", "/api/restore/progress/" + restorePageOperationID} {
		if !strings.Contains(progress.Body.String(), want) {
			t.Fatalf("New Backups Restore progress missing %q: %s", want, progress.Body.String())
		}
	}
}

func TestNewBackupsReconnectsToCurrentRestoreOperation(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: restorePageOperationID, OperationType: "restore",
		PlanFingerprint: restorePageFingerprint, State: "running", Stage: "staging",
		Status: "Preparing verified Restore data on the Minecraft data filesystem.",
		StartedAt: "2026-09-22T12:00:00Z", UpdatedAt: "2026-09-22T12:01:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeNewBackupsAPI{currentRestoreOperation: operation}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("New Backups current Restore returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Restore progress", "Restoring", "Preparing Restore data", "data-restore-busy=\"1\""} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("current Restore panel missing %q: %s", want, page.Body.String())
		}
	}
}

func TestNewBackupsAutomaticRejectsInvalidOrUnrelatedChanges(t *testing.T) {
	t.Run("invalid daily time", func(t *testing.T) {
		client := &fakeNewBackupsAPI{}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		body := "csrf=csrf-token&automatic_enabled=on&daily_time=25%3A99&backup_keep=7"
		page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/automatic/plan", body))
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "valid 24-hour time") {
			t.Fatalf("invalid daily time response = %d: %s", page.Code, page.Body.String())
		}
		if client.configurationPlanCalls != 0 {
			t.Fatal("invalid daily time reached configuration plan API")
		}
	})

	t.Run("unexpected Minecraft change", func(t *testing.T) {
		client := &fakeNewBackupsAPI{
			configurationPlan: api.AdminConfigurationChangeResponse{
				OK: true,
				Changes: []api.AdminConfigurationChange{
					{Field: "motd", Label: "Server welcome message", Before: "Old", After: "New", RestartRequired: true},
				},
				RestartRequired: true,
			},
		}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		body := "csrf=csrf-token&automatic_enabled=on&daily_time=04%3A30&backup_keep=7"
		page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/automatic/apply", body))
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Configuration changed unexpectedly") {
			t.Fatalf("unexpected-change response = %d: %s", page.Code, page.Body.String())
		}
		if client.configurationApplyCalls != 0 {
			t.Fatal("unexpected non-backup change reached configuration apply API")
		}
	})
}
