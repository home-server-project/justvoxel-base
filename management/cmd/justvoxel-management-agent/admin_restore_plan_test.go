package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validRestorePlanHelper = `{"ok":true,"schema_version":"v1","normalized":{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"world","created_at":"2026-09-20T08:30:00Z","size_bytes":12345,"metadata_status":"valid","metadata":{"created_at":"2026-09-20T08:30:00Z","minecraft":{"version_mode":"pinned","configured_version":"26.2","server_reported_version":"Paper 26.2"},"bedrock":{"enabled":false,"floodgate_configured":false},"justvoxel":{"variant":"justvoxel-vm"}},"current":{"version_mode":"pinned","version":"26.3","bedrock_enabled":false},"version_relation":"backup_older","validation":{"archive_integrity":"pending_apply","archive_safety":"pending_apply","staging_space":"pending_apply"}},"warnings":[{"code":"backup_older","message":"Older backup."}],"requirements":{"destructive_confirmation_required":true,"players_confirmation_required":false,"minecraft_state":"running","online":0,"players":[],"archive_integrity_validation_on_apply":true,"archive_safety_validation_on_apply":true,"staging_space_validation_on_apply":true},"context":{"archive_identity":"8:123:12345:1789900000","data_path":"/var/lib/justvoxel/minecraft","backup_path":"/var/lib/justvoxel/backups","minecraft_uid":"1001","minecraft_gid":"1001","backup_type":"system","backup_mount_point":"","backup_expected_uuid":"","backup_expected_source":""}}`

func TestAdminRestorePlanSuccessComputesFingerprintAndHidesContext(t *testing.T) {
	old := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = old }()
	runAdminRestorePlanHelper = func(_ context.Context, request []byte) ([]byte, error) {
		if !strings.Contains(string(request), `"backup_id":"minecraft-2026-09-20-043000.tar.gz"`) || !strings.Contains(string(request), `"mode":"world"`) {
			t.Fatalf("unexpected request: %s", request)
		}
		return []byte(validRestorePlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	body := `{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"world"}`
	s.adminRestorePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	response := rr.Body.String()
	if !strings.Contains(response, `"plan_fingerprint":"sha256:`) || !strings.Contains(response, `"version_relation":"backup_older"`) {
		t.Fatalf("restore plan response missing reviewed identity: %s", response)
	}
	for _, forbidden := range []string{"/var/lib/justvoxel", "archive_identity", "backup_path", "data_path", "minecraft_uid", "minecraft_gid"} {
		if strings.Contains(response, forbidden) {
			t.Fatalf("restore plan leaked internal context %q: %s", forbidden, response)
		}
	}
}

func TestAdminRestorePlanLocalRootAllowedAndRolesDenied(t *testing.T) {
	old := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = old }()
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validRestorePlanHelper), nil
	}
	body := `{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"world"}`

	s := surfaceTestServer(t, roleViewer)
	rr := httptest.NewRecorder()
	localRootRequest := httptest.NewRequest(http.MethodPost, "http://unix/v1/admin/restore/plan", strings.NewReader(body))
	localRootRequest = localRootRequest.WithContext(context.WithValue(localRootRequest.Context(), peerUIDKey{}, uint32(0)))
	s.adminRestorePlan(rr, localRootRequest)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root restore plan returned %d: %s", rr.Code, rr.Body.String())
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s = surfaceTestServer(t, role)
		rr = httptest.NewRecorder()
		s.adminRestorePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/plan", body))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}
}

func TestAdminRestorePlanRejectsInvalidRequestBeforeHelper(t *testing.T) {
	old := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = old }()
	called := false
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte(validRestorePlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	for _, body := range []string{
		`{"backup_id":"../../etc/shadow","mode":"world"}`,
		`{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"everything"}`,
		`{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"world","path":"/tmp/x"}`,
	} {
		rr := httptest.NewRecorder()
		s.adminRestorePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/plan", body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s returned %d, want 400", body, rr.Code)
		}
	}
	if called {
		t.Fatal("restore plan helper ran for invalid request")
	}
}

func TestAdminRestorePlanReturnsCompatibilityBlockWithoutFingerprint(t *testing.T) {
	old := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = old }()
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(`{"ok":false,"schema_version":"v1","code":"backup_newer","error":"This backup was created for a newer pinned Minecraft version.","normalized":{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"full","created_at":"2026-09-20T08:30:00Z","size_bytes":12345,"metadata_status":"valid","metadata":{"minecraft":{"version_mode":"pinned","configured_version":"26.4"},"bedrock":{"enabled":false,"floodgate_configured":false},"justvoxel":{}},"current":{"version_mode":"pinned","version":"26.3","bedrock_enabled":false},"version_relation":"backup_newer","validation":{"archive_integrity":"pending_apply","archive_safety":"pending_apply","staging_space":"pending_apply"}},"warnings":[],"requirements":{"destructive_confirmation_required":true,"players_confirmation_required":false,"minecraft_state":"stopped","online":0,"players":[],"archive_integrity_validation_on_apply":true,"archive_safety_validation_on_apply":true,"staging_space_validation_on_apply":true}}`), nil
	}
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminRestorePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/plan", `{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"full"}`))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"backup_newer"`) {
		t.Fatalf("compatibility rejection = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "plan_fingerprint") {
		t.Fatalf("blocked restore unexpectedly received fingerprint: %s", rr.Body.String())
	}
}

func TestAdminRestorePlanSanitizesHelperFailures(t *testing.T) {
	old := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = old }()
	s := surfaceTestServer(t, roleAdministrator)
	body := `{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"world"}`

	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("password=top-secret"), errors.New("exit status 1")
	}
	rr := httptest.NewRecorder()
	s.adminRestorePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/plan", body))
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "top-secret") {
		t.Fatalf("helper failure was not sanitized: %d %s", rr.Code, rr.Body.String())
	}

	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(`{"ok":true,"schema_version":"v1","context":{"data_path":"/secret"}}`), nil
	}
	rr = httptest.NewRecorder()
	s.adminRestorePlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/plan", body))
	if rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), "/secret") {
		t.Fatalf("malformed helper payload leaked: %d %s", rr.Code, rr.Body.String())
	}
}

func TestRestorePlanFingerprintChangesWithArchiveIdentity(t *testing.T) {
	var helper adminRestorePlanHelperResponse
	if err := decodeAdminRestorePlanHelperResponse([]byte(validRestorePlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	first, err := adminRestorePlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	helper.Context.ArchiveIdentity = "8:999:12345:1789900000"
	second, err := adminRestorePlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("restore plan fingerprint did not change when archive identity changed")
	}
}
