package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validMigrationExportPlanHelper = `{"ok":true,"schema_version":"v1","normalized":{"kind":"local","path":"/var/lib/justvoxel/exports","device":"","removable":false,"source":"","username":"","domain":"","filename":"justvoxel-migration-test.tar.gz","target_display":"/var/lib/justvoxel/exports/justvoxel-migration-test.tar.gz","target_filesystem":"xfs","data_path":"/var/lib/justvoxel/minecraft","data_bytes":2147483648,"target_available_bytes":8589934592},"warnings":[{"code":"cold_export","message":"Cold export."}],"requirements":{"export_confirmation_required":true,"players_confirmation_required":false,"minecraft_state":"running","online":0,"players":[],"smb_password_required":false,"target_validation_on_apply":true,"integrity_validation_required":true,"runtime_validation_required":true},"context":{"config_identity":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","data_identity":"1:2","target_identity":"local:5:6"}}`

func testMigrationExportRequest() adminMigrationExportTargetRequest {
	return adminMigrationExportTargetRequest{
		Kind:     "local",
		Path:     "/var/lib/justvoxel/exports",
		Filename: "justvoxel-migration-test.tar.gz",
	}
}

func exactMigrationExportFingerprint(t *testing.T) string {
	t.Helper()
	var helper adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func TestAdminMigrationExportPlanComputesFingerprintAndHidesContext(t *testing.T) {
	old := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = old }()
	runAdminMigrationExportPlanHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" ||
			!strings.Contains(string(request), `"kind":"local"`) ||
			!strings.Contains(string(request), `"filename":"justvoxel-migration-test.tar.gz"`) {
			t.Fatalf("unexpected export planner call: action=%s request=%s", action, request)
		}
		return []byte(validMigrationExportPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	body := `{"kind":"local","path":"/var/lib/justvoxel/exports","filename":"justvoxel-migration-test.tar.gz"}`
	s.adminMigrationExportPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/export/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	response := rr.Body.String()
	if !strings.Contains(response, `"plan_fingerprint":"sha256:`) ||
		!strings.Contains(response, `"target_display":"/var/lib/justvoxel/exports/justvoxel-migration-test.tar.gz"`) {
		t.Fatalf("export plan response missing reviewed identity: %s", response)
	}
	for _, forbidden := range []string{"config_identity", "data_identity", "target_identity"} {
		if strings.Contains(response, forbidden) {
			t.Fatalf("export plan leaked internal context %q: %s", forbidden, response)
		}
	}
}

func TestAdminMigrationExportPlanRejectsInvalidRequestBeforeHelper(t *testing.T) {
	old := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = old }()
	called := false
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(validMigrationExportPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	for _, body := range []string{
		`{"kind":"shell","filename":"justvoxel-migration-test.tar.gz"}`,
		`{"kind":"local","path":"../../tmp","filename":"justvoxel-migration-test.tar.gz"}`,
		`{"kind":"local","path":"/var/lib/justvoxel/exports","filename":"bad.tar.gz"}`,
		`{"kind":"local","path":"/var/lib/justvoxel/exports","filename":"justvoxel-migration-test.tar.gz","extra":true}`,
	} {
		rr := httptest.NewRecorder()
		s.adminMigrationExportPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/export/plan", body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s returned %d, want 400", body, rr.Code)
		}
	}
	if called {
		t.Fatal("export planner ran for invalid request")
	}
}

func TestAdminMigrationExportDiscoveryRequiresAdministratorAndSanitizesFailure(t *testing.T) {
	old := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = old }()

	called := false
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(`{"ok":true,"schema_version":"v1","suggested_filename":"justvoxel-migration-test.tar.gz","configured_backup_available":false,"devices":[],"target_kinds":["local","backup","device","nfs","smb"]}`), nil
	}
	s := surfaceTestServer(t, roleOperator)
	rr := httptest.NewRecorder()
	s.adminMigrationExportDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/migration/export", ""))
	if rr.Code != http.StatusForbidden || called {
		t.Fatalf("operator discovery status=%d helper_called=%t", rr.Code, called)
	}

	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte("secret=//private/share"), errors.New("exit status 1")
	}
	s = surfaceTestServer(t, roleAdministrator)
	rr = httptest.NewRecorder()
	s.adminMigrationExportDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/migration/export", ""))
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "//private/share") {
		t.Fatalf("export discovery helper failure was not sanitized: %d %s", rr.Code, rr.Body.String())
	}
}

func TestMigrationExportFingerprintChangesWithTargetIdentity(t *testing.T) {
	var helper adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	first, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	helper.Context.TargetIdentity = "local:changed"
	second, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("export plan fingerprint did not change when target identity changed")
	}
}

func TestMigrationExportFingerprintIgnoresLiveMeasurements(t *testing.T) {
	var helper adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	first, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}

	helper.Normalized.DataBytes += 4096
	helper.Normalized.TargetAvailableBytes -= 4096
	helper.Normalized.TargetFilesystem = "btrfs"
	helper.Requirements.MinecraftState = "stopped"
	helper.Requirements.PlayersConfirmationRequired = true
	helper.Requirements.Online = 2
	helper.Requirements.Players = []string{"PlayerOne", "PlayerTwo"}

	second, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("live export measurements changed reviewed fingerprint: %s != %s", first, second)
	}
}

func TestMigrationExportFingerprintChangesWithStableSourceIdentity(t *testing.T) {
	var helper adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	first, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	helper.Context.DataIdentity = "1:999"
	second, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("export plan fingerprint did not change when stable Minecraft data identity changed")
	}
}

