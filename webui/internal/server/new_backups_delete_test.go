package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNewBackupsDeleteAPI struct {
	fakeNewBackupsAPI
	planResult  api.AdminBackupDeleteResponse
	applyResult api.AdminBackupDeleteResponse
	planned     api.AdminBackupDeleteRequest
	applied     api.AdminBackupDeleteRequest
	planCalls   int
	applyCalls  int
}

func (f *fakeNewBackupsDeleteAPI) AdminBackupDeletePlan(_ context.Context, session string, request api.AdminBackupDeleteRequest) (api.AdminBackupDeleteResponse, error) {
	if session != "session-token" {
		return api.AdminBackupDeleteResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planned = request
	return f.planResult, nil
}

func (f *fakeNewBackupsDeleteAPI) AdminBackupDeleteApply(_ context.Context, session string, request api.AdminBackupDeleteRequest) (api.AdminBackupDeleteResponse, error) {
	if session != "session-token" {
		return api.AdminBackupDeleteResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applied = request
	return f.applyResult, nil
}

func TestNewBackupsDeletePlanCarriesMultipleSelections(t *testing.T) {
	client := &fakeNewBackupsDeleteAPI{}
	client.planResult = api.AdminBackupDeleteResponse{
		OK: true,
		Proposed: api.AdminBackupDeletePlan{
			Backups: []api.AdminBackupDeleteItem{
				{ID: "minecraft-2026-09-22-120000.tar.gz", SizeBytes: 10},
				{ID: "minecraft-2026-09-21-120000.tar.gz", SizeBytes: 20},
			},
			Count: 2, TotalSizeBytes: 30, Confirmation: "DELETE 2 BACKUPS",
			Fingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Warnings: []string{"Deleted backups cannot be restored by JustVoxel."},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&backup_id=minecraft-2026-09-22-120000.tar.gz&backup_id=minecraft-2026-09-21-120000.tar.gz"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-backups/delete/plan", body))
	if page.Code != http.StatusOK {
		t.Fatalf("delete plan returned %d: %s", page.Code, page.Body.String())
	}
	if client.planCalls != 1 || len(client.planned.BackupIDs) != 2 {
		t.Fatalf("plan calls=%d request=%#v", client.planCalls, client.planned)
	}
	for _, want := range []string{"DELETE 2 BACKUPS", "sha256:aaaaaaaa", "minecraft-2026-09-21-120000.tar.gz"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("delete plan response missing %q: %s", want, page.Body.String())
		}
	}
}

func TestNewBackupsDeleteApplyCarriesReviewedEvidence(t *testing.T) {
	client := &fakeNewBackupsDeleteAPI{}
	client.applyResult = api.AdminBackupDeleteResponse{
		OK: true, Applied: true, Deleted: 2,
		Proposed: api.AdminBackupDeletePlan{
			Count: 2, Confirmation: "DELETE 2 BACKUPS",
			Fingerprint: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&backup_id=minecraft-2026-09-22-120000.tar.gz&backup_id=minecraft-2026-09-21-120000.tar.gz&confirmation=DELETE+2+BACKUPS&fingerprint=sha256%3Abbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-backups/delete/apply", body))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"deleted":2`) {
		t.Fatalf("delete apply returned %d: %s", page.Code, page.Body.String())
	}
	if client.applyCalls != 1 || client.applied.Confirmation != "DELETE 2 BACKUPS" || !strings.HasPrefix(client.applied.Fingerprint, "sha256:bbbb") {
		t.Fatalf("reviewed evidence lost: %#v calls=%d", client.applied, client.applyCalls)
	}
}

func TestNewBackupsDeleteRejectsBadCSRFAndOperator(t *testing.T) {
	client := &fakeNewBackupsDeleteAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=wrong&backup_id=minecraft-2026-09-22-120000.tar.gz"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-backups/delete/plan", body))
	if page.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF delete status=%d, want 403", page.Code)
	}
	if client.planCalls != 0 {
		t.Fatal("delete plan reached privileged API after CSRF rejection")
	}

	client.role = "operator"
	body = "csrf=csrf-token&backup_id=minecraft-2026-09-22-120000.tar.gz"
	page = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-backups/delete/plan", body))
	if page.Code != http.StatusForbidden {
		t.Fatalf("operator delete status=%d, want 403", page.Code)
	}
	if client.planCalls != 0 {
		t.Fatal("delete plan reached privileged API for Operator")
	}
}
