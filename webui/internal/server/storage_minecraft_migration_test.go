package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const storageMigrationFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const storageMigrationOperationID = "12345678-1234-4abc-8def-123456789abc"

type fakeStorageMinecraftMigrationAPI struct {
	fakeAPI
	plan         api.AdminDataMigrationPlanResponse
	apply        api.AdminDataMigrationApplyResponse
	current      *api.PersistentOperation
	operation    *api.PersistentOperation
	planCalls    int
	applyCalls   int
	planned      api.AdminDataMigrationPlanRequest
	applyRequest api.AdminDataMigrationApplyRequest
}

func (f *fakeStorageMinecraftMigrationAPI) AdminDataMigrationPlan(_ context.Context, session string, request api.AdminDataMigrationPlanRequest) (api.AdminDataMigrationPlanResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planned = request
	return f.plan, nil
}

func (f *fakeStorageMinecraftMigrationAPI) AdminDataMigrationApply(_ context.Context, session string, request api.AdminDataMigrationApplyRequest) (api.AdminDataMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyRequest = request
	return f.apply, nil
}

func (f *fakeStorageMinecraftMigrationAPI) AdminCurrentDataMigrationOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	return api.PersistentOperationResponse{Operation: f.current}, nil
}

func (f *fakeStorageMinecraftMigrationAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.operation == nil || f.operation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	return api.PersistentOperationResponse{Operation: f.operation}, nil
}

func storageMigrationPlan() api.AdminDataMigrationPlanResponse {
	return api.AdminDataMigrationPlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: storageMigrationFingerprint,
		Normalized: &api.AdminDataMigrationPlanNormalized{
			Operation: "use_partition", Device: "/dev/vdb1", Filesystem: "xfs",
			MountPoint: "/var/mnt/justvoxel-data", Path: "/var/mnt/justvoxel-data/minecraft",
			SizeGiB: "all", DataBytes: 2147483648, TargetCapacityBytes: 10737418240,
			CurrentDataPath: "/var/lib/justvoxel/minecraft",
		},
		Warnings: []api.AdminDataMigrationWarning{{Code: "old_data_retained", Message: "Old data is retained."}},
		Requirements: &api.AdminDataMigrationRequirements{
			MigrationConfirmationRequired: true, ExactSpaceValidationOnApply: true,
			ColdBackupRequired: true, CopyVerificationRequired: true, RuntimeValidationRequired: true,
			Players: []string{},
		},
	}
}

func TestStorageMinecraftMigrationPlanUsesNativeStorageAPI(t *testing.T) {
	client := &fakeStorageMinecraftMigrationAPI{plan: storageMigrationPlan()}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=use_partition&device=%2Fdev%2Fvdb1&mount_point=%2Fvar%2Fmnt%2Fjustvoxel-data&path=%2Fvar%2Fmnt%2Fjustvoxel-data%2Fminecraft&size_gib=all"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/minecraft-data/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("native Storage migration plan status=%d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 || client.planned.Operation != "use_partition" || client.planned.Device != "/dev/vdb1" {
		t.Fatalf("unexpected Storage migration plan request: %#v", client.planned)
	}
	for _, want := range []string{storageMigrationFingerprint, "/dev/vdb1", "Old data is retained."} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("native Storage migration plan missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestStorageMinecraftMigrationApplyReplansAndReturnsPersistentOperation(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: storageMigrationOperationID, OperationType: "data_migration",
		PlanFingerprint: storageMigrationFingerprint, State: "queued", Stage: "queued",
		Status: "Minecraft data migration operation queued.",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeStorageMinecraftMigrationAPI{
		plan: storageMigrationPlan(),
		apply: api.AdminDataMigrationApplyResponse{OK: true, Created: true, Operation: operation},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=use_partition&device=%2Fdev%2Fvdb1&mount_point=%2Fvar%2Fmnt%2Fjustvoxel-data&path=%2Fvar%2Fmnt%2Fjustvoxel-data%2Fminecraft&size_gib=all&plan_fingerprint=" + storageMigrationFingerprint + "&migration_confirmation=MIGRATE"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/minecraft-data/apply", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("native Storage migration apply status=%d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 1 {
		t.Fatalf("apply must re-plan exactly once; plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
	if client.applyRequest.PlanFingerprint != storageMigrationFingerprint || !client.applyRequest.MigrationConfirmed {
		t.Fatalf("review evidence lost at native Storage migration apply: %#v", client.applyRequest)
	}
	if !strings.Contains(rr.Body.String(), storageMigrationOperationID) {
		t.Fatalf("persistent operation missing from native Storage apply response: %s", rr.Body.String())
	}
}

func TestStorageMinecraftMigrationSourceHasNoLegacyPageDependency(t *testing.T) {
	for _, name := range []string{
		"templates/storage_browser.html",
		"static/storage-browser.js",
	} {
		content, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "/settings/data-migration") {
			t.Fatalf("Storage workspace still references legacy Data Migration page in %s", name)
		}
	}

	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, want := range []string{
		"/api/new-storage/minecraft-data/current",
		"/api/new-storage/minecraft-data/plan",
		"/api/new-storage/minecraft-data/apply",
		"/api/new-storage/minecraft-data/progress/",
		"showMigrationProgress(payload.operation)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("native Storage Minecraft migration behavior missing %q", want)
		}
	}
}
