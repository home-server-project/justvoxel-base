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
	role           string
	backups        api.AdminRestoreBackupsResponse
	backupResult   api.ManualBackupResponse
	backupErr      error
	backupCalls    int
	discoveryCalls int
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
