package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminBackupDeleteRequiresAdministrator(t *testing.T) {
	old := runAdminBackupDeleteHelper
	defer func() { runAdminBackupDeleteHelper = old }()
	called := false
	runAdminBackupDeleteHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(`{"ok":true,"warnings":[],"proposed":{"backups":[],"count":1,"total_size_bytes":1,"confirmation":"DELETE one","fingerprint":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"applied":false}`), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminBackupDeletePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/backups/delete/plan", `{"backup_ids":["minecraft-2026-09-22-120000.tar.gz"]}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("backup delete helper ran for non-Administrator")
	}
}

func TestAdminBackupDeletePlanReturnsReviewedEvidence(t *testing.T) {
	old := runAdminBackupDeleteHelper
	defer func() { runAdminBackupDeleteHelper = old }()
	runAdminBackupDeleteHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" {
			t.Fatalf("action = %q, want plan", action)
		}
		if !strings.Contains(string(request), "minecraft-2026-09-22-120000.tar.gz") {
			t.Fatalf("request lost backup id: %s", request)
		}
		return []byte(`{"ok":true,"warnings":["Deleted backups cannot be restored by JustVoxel."],"proposed":{"backups":[{"id":"minecraft-2026-09-22-120000.tar.gz","size_bytes":12345}],"count":1,"total_size_bytes":12345,"confirmation":"DELETE minecraft-2026-09-22-120000.tar.gz","fingerprint":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"applied":false}`), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminBackupDeletePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/backups/delete/plan", `{"backup_ids":["minecraft-2026-09-22-120000.tar.gz"]}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"DELETE minecraft-2026-09-22-120000.tar.gz", "sha256:aaaaaaaa", "12345"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("plan response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminBackupDeleteApplyPreservesConfirmationAndFingerprint(t *testing.T) {
	old := runAdminBackupDeleteHelper
	defer func() { runAdminBackupDeleteHelper = old }()
	runAdminBackupDeleteHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "apply" {
			t.Fatalf("action = %q, want apply", action)
		}
		body := string(request)
		for _, want := range []string{"DELETE 2 BACKUPS", "sha256:bbbbbbbb", "minecraft-2026-09-22-120000.tar.gz", "minecraft-2026-09-21-120000.tar.gz"} {
			if !strings.Contains(body, want) {
				t.Fatalf("apply request missing %q: %s", want, body)
			}
		}
		return []byte(`{"ok":true,"warnings":[],"proposed":{"backups":[{"id":"minecraft-2026-09-22-120000.tar.gz","size_bytes":10},{"id":"minecraft-2026-09-21-120000.tar.gz","size_bytes":20}],"count":2,"total_size_bytes":30,"confirmation":"DELETE 2 BACKUPS","fingerprint":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"deleted":2,"applied":true}`), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminBackupDeleteApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/backups/delete/apply", `{"backup_ids":["minecraft-2026-09-22-120000.tar.gz","minecraft-2026-09-21-120000.tar.gz"],"confirmation":"DELETE 2 BACKUPS","fingerprint":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"deleted":2`) {
		t.Fatalf("apply status = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminBackupDeleteApplyReturnsBoundedFailure(t *testing.T) {
	old := runAdminBackupDeleteHelper
	defer func() { runAdminBackupDeleteHelper = old }()
	runAdminBackupDeleteHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte(`{"ok":false,"error":"The selected backups changed after Review. Nothing was deleted. Review the selection again.","warnings":[],"applied":false}`), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminBackupDeleteApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/backups/delete/apply", `{"backup_ids":["minecraft-2026-09-22-120000.tar.gz"],"confirmation":"DELETE minecraft-2026-09-22-120000.tar.gz","fingerprint":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}`))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "changed after Review") {
		t.Fatalf("bounded failure status = %d: %s", rr.Code, rr.Body.String())
	}
}
