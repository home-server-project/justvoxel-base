package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestValidationNewBackupsRestoreErrorAndAuthorizationGuards(t *testing.T) {
	t.Run("planning service error renders without a plan", func(t *testing.T) {
		client := &fakeNewBackupsAPI{restorePlanErr: errors.New("planner unavailable")}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}

		body := "csrf=csrf-token&backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world"
		page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/restore/plan", body))
		if page.Code != http.StatusOK {
			t.Fatalf("planning error page returned %d: %s", page.Code, page.Body.String())
		}
		if !strings.Contains(page.Body.String(), "Restore cannot continue as submitted.") {
			t.Fatalf("planning error was not rendered in New Backups: %s", page.Body.String())
		}
		if client.restorePlanCalls != 1 || client.restoreApplyCalls != 0 {
			t.Fatalf("unexpected Restore calls: plan=%d apply=%d", client.restorePlanCalls, client.restoreApplyCalls)
		}
	})

	t.Run("csrf and role guard privileged Restore routes", func(t *testing.T) {
		client := &fakeNewBackupsAPI{}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}

		body := "csrf=wrong&backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world"
		page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/restore/plan", body))
		if page.Code != http.StatusForbidden {
			t.Fatalf("bad CSRF Restore plan status=%d, want 403", page.Code)
		}
		if client.restorePlanCalls != 0 {
			t.Fatal("Restore plan reached API after CSRF rejection")
		}

		client.role = "operator"
		body = "csrf=csrf-token&backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world"
		page = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/restore/plan", body))
		if page.Code != http.StatusForbidden {
			t.Fatalf("operator Restore plan status=%d, want 403", page.Code)
		}
		if client.restorePlanCalls != 0 {
			t.Fatal("Restore plan reached API for Operator")
		}
	})
}
