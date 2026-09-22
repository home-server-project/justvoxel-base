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
		OK: true,
		Changes: []api.AdminConfigurationChange{},
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
