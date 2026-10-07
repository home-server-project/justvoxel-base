package server

import (
	"context"
	"net/http"
	"os"
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
		Status:   "Minecraft data migration operation queued.",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeStorageMinecraftMigrationAPI{
		plan:  storageMigrationPlan(),
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
	assertStorageMinecraftMigrationSource(t)
}

func assertStorageMinecraftMigrationSource(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"templates/storage_browser.html",
		"static/storage-browser.js",
	} {
		content, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"/settings/data-migration", "/api/data-migration/progress", "data-migration-operation.js"} {
			if strings.Contains(string(content), forbidden) {
				t.Fatalf("Storage workspace still references %q in %s", forbidden, name)
			}
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
		"void showCurrentMigrationIfAny();",
		"if (migrationDialog && !migrationDialog.open) migrationDialog.showModal();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("native Storage Minecraft migration behavior missing %q", want)
		}
	}
}

func TestStorageMinecraftMigrationCurrentReconnectsPersistentOperation(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: storageMigrationOperationID, OperationType: "data_migration",
		PlanFingerprint: storageMigrationFingerprint, State: "running", Stage: "copying",
		Status:   "Copying Minecraft data to the reviewed storage target.",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeStorageMinecraftMigrationAPI{current: operation}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/new-storage/minecraft-data/current", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("current Storage migration status=%d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{storageMigrationOperationID, "copying", "Copying Minecraft data"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("current Storage migration response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestStorageMinecraftMigrationPlanReconnectsInsteadOfStartingSecondOperation(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: storageMigrationOperationID, OperationType: "data_migration",
		State: "running", Stage: "copying", Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeStorageMinecraftMigrationAPI{current: operation, plan: storageMigrationPlan()}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=use_partition&device=%2Fdev%2Fvdb1&mount_point=%2Fvar%2Fmnt%2Fjustvoxel-data&path=%2Fvar%2Fmnt%2Fjustvoxel-data%2Fminecraft&size_gib=all"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/minecraft-data/plan", body))
	if rr.Code != http.StatusConflict {
		t.Fatalf("active Storage migration plan status=%d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 0 || client.applyCalls != 0 {
		t.Fatalf("new migration planning ran while persistent operation was active: plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
	if !strings.Contains(rr.Body.String(), storageMigrationOperationID) {
		t.Fatalf("active persistent operation was not returned for reconnect: %s", rr.Body.String())
	}
}

func TestStorageMinecraftMigrationProgressUsesPersistentOperationJournal(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: storageMigrationOperationID, OperationType: "data_migration",
		State: "verifying", Stage: "minecraft_runtime", Status: "Validating migrated Minecraft runtime.",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeStorageMinecraftMigrationAPI{operation: operation}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/new-storage/minecraft-data/progress/"+storageMigrationOperationID, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("Storage migration progress status=%d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{storageMigrationOperationID, "minecraft_runtime", "Validating migrated Minecraft runtime."} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("Storage migration progress missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestStandaloneDataMigrationUIRetired(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/settings/data-migration", http.StatusNotFound},
		{http.MethodGet, "/settings/data-migration/progress/operation-id", http.StatusNotFound},
		{http.MethodGet, "/api/data-migration/progress/operation-id", http.StatusNotFound},
		{http.MethodPost, "/settings/data-migration/review", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/data-migration/apply", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rr := httptestResponse(app, authenticatedAdminRequest(tc.method, "http://example"+tc.path, ""))
			if rr.Code != tc.status {
				t.Fatalf("status=%d, want %d: %s", rr.Code, tc.status, rr.Body.String())
			}
		})
	}
	for _, name := range []string{
		"templates/data_migration.html",
		"templates/data_migration_review.html",
		"templates/data_migration_progress.html",
		"static/data-migration-operation.js",
	} {
		if _, err := assets.ReadFile(name); !os.IsNotExist(err) {
			t.Fatalf("retired asset %s: expected not found, got %v", name, err)
		}
	}
	// The Storage workspace and its native migration endpoints remain available.
	assertStorageMinecraftMigrationSource(t)
}
