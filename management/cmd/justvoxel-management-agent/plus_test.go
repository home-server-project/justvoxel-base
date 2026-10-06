package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlusManagementBlocksGameRoutesAndKeepsHostRoutes(t *testing.T) {
	s := &server{plus: true}
	handler := s.plusRoutes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, path := range []string{"/v1/minecraft/start", "/v1/players", "/v1/backups/manual", "/v1/admin/setup/apply", "/v1/admin/reset/minecraft/apply", "/v1/admin/version/update", "/v1/admin/data-migration/apply", "/v1/admin/migration/import/apply", "/v1/admin/storage-provision/apply", "/v1/admin/users/1/backup-allowance/reset"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("exposed %s: %d", path, rr.Code)
		}
	}
	for _, path := range []string{"/v1/admin/reset/factory/apply", "/v1/admin/reset/factory/current-operation", "/v1/status", "/v1/session", "/v1/admin/users", "/v1/admin/storage", "/v1/admin/storage-actions/apply", "/v1/admin/storage-mounts/apply", "/v1/admin/system/reboot", "/v1/admin/system/updates", "/v1/network", "/v1/admin/network/remote-access/playit/setup", "/v1/admin/network/remote-access/tailscale", "/v1/admin/network/remote-access/netbird", "/v1/ups"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusNoContent {
			t.Fatalf("blocked host route %s: %d", path, rr.Code)
		}
	}
}

func TestPlusHostLogsRoleFixedUnitsAndRedaction(t *testing.T) {
	old := runPlusHostJournal
	defer func() { runPlusHostJournal = old }()
	called := ""
	runPlusHostJournal = func(_ context.Context, unit string) ([]byte, error) {
		called = unit
		return []byte("Docker started\npassword=do-not-show\n"), nil
	}
	for _, role := range []principalRole{roleViewer, roleOperator, roleAdministrator} {
		s := surfaceTestServer(t, role)
		s.plus = true
		called = ""
		rr := httptest.NewRecorder()
		s.plusHostLogs(rr, surfaceRequest(http.MethodGet, "/v1/admin/plus/host-logs?service=docker", ""))
		if role != roleAdministrator {
			if rr.Code != http.StatusForbidden || called != "" {
				t.Fatalf("%s obtained host logs: %d", role, rr.Code)
			}
		} else {
			if rr.Code != http.StatusOK || called != "docker.service" || strings.Contains(rr.Body.String(), "do-not-show") || !strings.Contains(rr.Body.String(), "Docker started") {
				t.Fatalf("invalid host logs %d %s", rr.Code, rr.Body.String())
			}
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	s.plus = true
	called = ""
	rr := httptest.NewRecorder()
	s.plusHostLogs(rr, surfaceRequest(http.MethodGet, "/v1/admin/plus/host-logs?service=../../secret", ""))
	if rr.Code != http.StatusBadRequest || called != "" {
		t.Fatal("arbitrary host journal unit accepted")
	}
}
