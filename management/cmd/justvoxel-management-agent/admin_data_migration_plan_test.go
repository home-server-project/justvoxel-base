package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validDataMigrationPlanHelper = `{"ok":true,"schema_version":"v1","normalized":{"operation":"use_partition","device":"/dev/vdb1","model":"Virtual Disk","transport":"virtio","filesystem":"xfs","mount_point":"/var/mnt/justvoxel-data","path":"/var/mnt/justvoxel-data/minecraft","size_gib":"all","size_bytes":10737418240,"data_bytes":2147483648,"target_capacity_bytes":8589934592,"current_data_path":"/var/lib/justvoxel/minecraft","free_start":"","free_end":"","planned_end":"","free_mib":0,"planned_mib":0},"warnings":[{"code":"old_data_retained","message":"Old data retained."}],"requirements":{"migration_confirmation_required":true,"destructive_confirmation_required":false,"confirmation_phrase":"","players_confirmation_required":false,"minecraft_state":"running","online":0,"players":[],"exact_space_validation_on_apply":true,"cold_backup_required":true,"copy_verification_required":true,"runtime_validation_required":true},"context":{"config_identity":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","target_identity":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","current_data_identity":"1:2:3:4","current_data_path":"/var/lib/justvoxel/minecraft","current_mount_point":"","current_expected_uuid":"","current_expected_source":"","minecraft_uid":"1000","minecraft_gid":"1000","backup_path":"/var/lib/justvoxel/backups","backup_mount_point":"","backup_expected_uuid":"","backup_expected_source":""}}`

func TestAdminDataMigrationPlanComputesFingerprintAndHidesContext(t *testing.T) {
	old := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = old }()
	runAdminDataMigrationPlanHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" ||
			!strings.Contains(string(request), `"operation":"use_partition"`) ||
			!strings.Contains(string(request), `"device":"/dev/vdb1"`) {
			t.Fatalf("unexpected planner call: action=%s request=%s", action, request)
		}
		return []byte(validDataMigrationPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	body := `{"operation":"use_partition","device":"/dev/vdb1","mount_point":"/var/mnt/justvoxel-data","path":"/var/mnt/justvoxel-data/minecraft","size_gib":"all"}`
	s.adminDataMigrationPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/data-migration/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	response := rr.Body.String()
	if !strings.Contains(response, `"plan_fingerprint":"sha256:`) ||
		!strings.Contains(response, `"operation":"use_partition"`) {
		t.Fatalf("migration plan response missing reviewed identity: %s", response)
	}
	for _, forbidden := range []string{
		"config_identity", "target_identity", "current_data_identity",
		"minecraft_uid", "minecraft_gid", "backup_path",
	} {
		if strings.Contains(response, forbidden) {
			t.Fatalf("migration plan leaked internal context %q: %s", forbidden, response)
		}
	}
}

func TestAdminDataMigrationPlanRejectsInvalidRequestBeforeHelper(t *testing.T) {
	old := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = old }()
	called := false
	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(validDataMigrationPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	for _, body := range []string{
		`{"operation":"shell","device":"/dev/vdb1","mount_point":"/var/mnt/data","path":"/var/mnt/data/minecraft","size_gib":"all"}`,
		`{"operation":"use_partition","device":"../../dev/vdb1","mount_point":"/var/mnt/data","path":"/var/mnt/data/minecraft","size_gib":"all"}`,
		`{"operation":"use_partition","device":"/dev/vdb1","mount_point":"/var/mnt/data","path":"/var/mnt/data/minecraft","size_gib":"all","extra":true}`,
	} {
		rr := httptest.NewRecorder()
		s.adminDataMigrationPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/data-migration/plan", body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s returned %d, want 400", body, rr.Code)
		}
	}
	if called {
		t.Fatal("migration planner ran for invalid request")
	}
}

func TestAdminDataMigrationDiscoveryRequiresAdministratorAndSanitizesFailure(t *testing.T) {
	old := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = old }()

	called := false
	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(`{"ok":true,"schema_version":"v1","warnings":[],"whole_disks":[],"partitions":[],"blank_partitions":[],"free_space_disks":[]}`), nil
	}
	s := surfaceTestServer(t, roleOperator)
	rr := httptest.NewRecorder()
	s.adminDataMigrationDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/data-migration", ""))
	if rr.Code != http.StatusForbidden || called {
		t.Fatalf("operator discovery status=%d helper_called=%t", rr.Code, called)
	}

	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte("secret=/dev/private"), errors.New("exit status 1")
	}
	s = surfaceTestServer(t, roleAdministrator)
	rr = httptest.NewRecorder()
	s.adminDataMigrationDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/data-migration", ""))
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "/dev/private") {
		t.Fatalf("helper failure was not sanitized: %d %s", rr.Code, rr.Body.String())
	}
}

func TestDataMigrationFingerprintChangesWithTargetIdentity(t *testing.T) {
	var helper adminDataMigrationHelperResponse
	if err := decodeAdminDataMigrationJSON([]byte(validDataMigrationPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	first, err := adminDataMigrationPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	helper.Context.TargetIdentity = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	second, err := adminDataMigrationPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("migration plan fingerprint did not change when target identity changed")
	}
}
