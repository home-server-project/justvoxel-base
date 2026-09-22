package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validRestoreDiscovery = `{"backups":[{"id":"minecraft-2026-09-20-043000.tar.gz","created_at":"2026-09-20T08:30:00Z","size_bytes":12345,"metadata_status":"valid","metadata":{"created_at":"2026-09-20T08:30:00Z","minecraft":{"version_mode":"pinned","configured_version":"26.3","server_reported_version":"Paper 26.3"},"bedrock":{"enabled":true,"geyser_reported_version":"2.9.0","floodgate_configured":true},"justvoxel":{"variant":"justvoxel-vm"}}},{"id":"minecraft-2026-09-19-043000.tar.gz","created_at":"2026-09-19T08:30:00Z","size_bytes":6789,"metadata_status":"missing"}]}`

func TestAdminRestoreBackupsAllowsAdministratorAndLocalRoot(t *testing.T) {
	old := runAdminRestoreDiscoveryHelper
	defer func() { runAdminRestoreDiscoveryHelper = old }()
	runAdminRestoreDiscoveryHelper = func(_ context.Context) ([]byte, error) {
		return []byte(validRestoreDiscovery), nil
	}

	for _, request := range []*http.Request{
		surfaceRequest(http.MethodGet, "/v1/admin/restore/backups", ""),
		requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/restore/backups", 0),
	} {
		s := surfaceTestServer(t, roleAdministrator)
		if request.Context().Value(peerUIDKey{}) != nil {
			s = surfaceTestServer(t, roleViewer)
		}
		rr := httptest.NewRecorder()
		s.adminRestoreBackups(rr, request)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		for _, want := range []string{"minecraft-2026-09-20-043000.tar.gz", `"metadata_status":"valid"`, `"configured_version":"26.3"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("response missing %q: %s", want, body)
			}
		}
		if strings.Contains(body, "/var/") || strings.Contains(body, "backup_path") {
			t.Fatalf("restore discovery exposed a filesystem path: %s", body)
		}
	}
}

func TestAdminRestoreBackupsRequiresAdministrator(t *testing.T) {
	old := runAdminRestoreDiscoveryHelper
	defer func() { runAdminRestoreDiscoveryHelper = old }()
	called := false
	runAdminRestoreDiscoveryHelper = func(_ context.Context) ([]byte, error) {
		called = true
		return []byte(validRestoreDiscovery), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminRestoreBackups(rr, surfaceRequest(http.MethodGet, "/v1/admin/restore/backups", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("restore discovery helper ran for a non-Administrator")
	}
}

func TestAdminRestoreBackupsNormalizesEmptyList(t *testing.T) {
	old := runAdminRestoreDiscoveryHelper
	defer func() { runAdminRestoreDiscoveryHelper = old }()
	runAdminRestoreDiscoveryHelper = func(_ context.Context) ([]byte, error) {
		return []byte(`{"backups":null}`), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminRestoreBackups(rr, surfaceRequest(http.MethodGet, "/v1/admin/restore/backups", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"backups":[]`) {
		t.Fatalf("empty restore discovery = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminRestoreBackupsRejectsUnsafeOrUnexpectedHelperData(t *testing.T) {
	old := runAdminRestoreDiscoveryHelper
	defer func() { runAdminRestoreDiscoveryHelper = old }()

	cases := []string{
		`{"backups":[{"id":"../../etc/shadow","created_at":"2026-09-20T08:30:00Z","size_bytes":1,"metadata_status":"missing"}]}`,
		`{"backups":[{"id":"minecraft-2026-09-20-043000.tar.gz","created_at":"not-a-time","size_bytes":1,"metadata_status":"missing"}]}`,
		`{"backups":[{"id":"minecraft-2026-09-20-043000.tar.gz","created_at":"2026-09-20T08:30:00Z","size_bytes":1,"metadata_status":"other"}]}`,
		`{"backups":[],"path":"/var/lib/justvoxel/backups","password":"secret"}`,
	}
	for _, payload := range cases {
		runAdminRestoreDiscoveryHelper = func(_ context.Context) ([]byte, error) {
			return []byte(payload), nil
		}
		s := surfaceTestServer(t, roleAdministrator)
		rr := httptest.NewRecorder()
		s.adminRestoreBackups(rr, surfaceRequest(http.MethodGet, "/v1/admin/restore/backups", ""))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("payload %s returned %d, want 500", payload, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "shadow") || strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "/var/") {
			t.Fatalf("invalid helper payload leaked into response: %s", rr.Body.String())
		}
	}
}

func TestAdminRestoreBackupsSanitizesHelperFailure(t *testing.T) {
	old := runAdminRestoreDiscoveryHelper
	defer func() { runAdminRestoreDiscoveryHelper = old }()
	runAdminRestoreDiscoveryHelper = func(_ context.Context) ([]byte, error) {
		return []byte("password=top-secret"), errors.New("exit status 1")
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminRestoreBackups(rr, surfaceRequest(http.MethodGet, "/v1/admin/restore/backups", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "top-secret") {
		t.Fatalf("helper failure leaked raw output: %s", rr.Body.String())
	}
}
