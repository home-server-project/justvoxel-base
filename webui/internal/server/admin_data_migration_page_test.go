package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const migrationPageFingerprint = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
const migrationPageOperationID = "87654321-4321-4abc-8def-abcdefabcdef"

type fakeDataMigrationAPI struct {
	fakeAPI
	role           string
	discovery      api.AdminDataMigrationDiscoveryResponse
	plan           api.AdminDataMigrationPlanResponse
	planErr        error
	planCalls      int
	apply          api.AdminDataMigrationApplyResponse
	applyErr       error
	applyRequest   api.AdminDataMigrationApplyRequest
	applyCalls     int
	discoveryCalls int
	current        *api.PersistentOperation
	operation      *api.PersistentOperation
}

func (f *fakeDataMigrationAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeDataMigrationAPI) AdminDataMigrationDiscovery(_ context.Context, session string) (api.AdminDataMigrationDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.discovery, nil
}

func (f *fakeDataMigrationAPI) AdminDataMigrationPlan(_ context.Context, session string, request api.AdminDataMigrationPlanRequest) (api.AdminDataMigrationPlanResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	if request.Operation == "" || request.Device == "" {
		return api.AdminDataMigrationPlanResponse{}, &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "invalid request"}
	}
	return f.plan, f.planErr
}

func (f *fakeDataMigrationAPI) AdminDataMigrationApply(_ context.Context, session string, request api.AdminDataMigrationApplyRequest) (api.AdminDataMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyRequest = request
	return f.apply, f.applyErr
}

func (f *fakeDataMigrationAPI) AdminCurrentDataMigrationOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.current == nil {
		return api.PersistentOperationResponse{}, nil
	}
	copy := *f.current
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func (f *fakeDataMigrationAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.operation == nil || f.operation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	copy := *f.operation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func dataMigrationPagePlan(players, destructive bool) api.AdminDataMigrationPlanResponse {
	requirements := &api.AdminDataMigrationRequirements{
		MigrationConfirmationRequired: true,
		DestructiveConfirmationRequired: destructive,
		PlayersConfirmationRequired: players,
		MinecraftState: "running",
		Online: 0,
		Players: []string{},
		ExactSpaceValidationOnApply: true,
		ColdBackupRequired: true,
		CopyVerificationRequired: true,
		RuntimeValidationRequired: true,
	}
	if destructive {
		requirements.ConfirmationPhrase = "ERASE /dev/vdb"
	}
	if players {
		requirements.Online = 1
		requirements.Players = []string{"PlayerOne"}
	}
	return api.AdminDataMigrationPlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: migrationPageFingerprint,
		Normalized: &api.AdminDataMigrationPlanNormalized{
			Operation: "erase_disk", Device: "/dev/vdb", Model: "Virtual Disk", Transport: "virtio",
			Filesystem: "xfs", MountPoint: "/var/mnt/justvoxel-data", Path: "/var/mnt/justvoxel-data/minecraft",
			SizeGiB: "all", SizeBytes: 10737418240, DataBytes: 2147483648,
			TargetCapacityBytes: 10737418240, CurrentDataPath: "/var/lib/justvoxel/minecraft",
		},
		Warnings: []api.AdminDataMigrationWarning{{Code: "old_data_retained", Message: "The old Minecraft data directory will be retained."}},
		Requirements: requirements,
	}
}

func migrationRequest(method, target, form string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(form))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
	return req
}

func migrationResponse(app *App, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	return rr
}

func TestDataMigrationPageListsAgentCandidatesAndIsAdministratorOnly(t *testing.T) {
	client := &fakeDataMigrationAPI{discovery: api.AdminDataMigrationDiscoveryResponse{
		OK: true, SchemaVersion: "v1", Warnings: []api.AdminDataMigrationWarning{},
		WholeDisks: []api.AdminDataMigrationCandidate{{Path: "/dev/vdb", Model: "Virtual Disk", Transport: "virtio", SizeBytes: 10737418240}},
		Partitions: []api.AdminDataMigrationCandidate{{Path: "/dev/vdc1", Filesystem: "xfs", Mountpoint: "/mnt/data", SizeBytes: 8589934592}},
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := migrationResponse(app, migrationRequest(http.MethodGet, "http://example/settings/data-migration", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("migration page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Minecraft data storage", "/dev/vdb", "Virtual Disk", "/dev/vdc1", "/mnt/data", "Dedicated disk or USB drive", "Existing filesystem"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("migration page missing %q: %s", want, page.Body.String())
		}
	}

	client.role = "operator"
	denied := migrationResponse(app, migrationRequest(http.MethodGet, "http://example/settings/data-migration", ""))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("operator migration page status = %d, want 403", denied.Code)
	}
	if client.discoveryCalls != 1 {
		t.Fatalf("migration discovery ran for non-administrator; calls=%d", client.discoveryCalls)
	}
}

func TestDataMigrationReviewShowsAuthoritativeWarningsAndConfirmations(t *testing.T) {
	client := &fakeDataMigrationAPI{plan: dataMigrationPagePlan(true, true)}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "operation": {"erase_disk"}, "device": {"/dev/vdb"},
		"mount_point": {"/var/mnt/justvoxel-data"}, "path": {"/var/mnt/justvoxel-data/minecraft"}, "size_gib": {"all"},
	}
	page := migrationResponse(app, migrationRequest(http.MethodPost, "http://example/settings/data-migration/review", values.Encode()))
	if page.Code != http.StatusOK {
		t.Fatalf("migration review returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Review Minecraft data migration", "The old Minecraft data directory will be retained.",
		"PlayerOne", "ERASE /dev/vdb", "Type MIGRATE", "Agent-owned safety sequence",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("migration review missing %q: %s", want, page.Body.String())
		}
	}
}

func TestDataMigrationApplyRequiresMigrateBeforeAgentApply(t *testing.T) {
	client := &fakeDataMigrationAPI{plan: dataMigrationPagePlan(false, true)}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "operation": {"erase_disk"}, "device": {"/dev/vdb"},
		"mount_point": {"/var/mnt/justvoxel-data"}, "path": {"/var/mnt/justvoxel-data/minecraft"}, "size_gib": {"all"},
		"plan_fingerprint": {migrationPageFingerprint}, "migration_confirmation": {"migrate"},
		"destructive_confirmation": {"ERASE /dev/vdb"},
	}
	page := migrationResponse(app, migrationRequest(http.MethodPost, "http://example/settings/data-migration/apply", values.Encode()))
	if page.Code != http.StatusBadRequest || !strings.Contains(page.Body.String(), "Type MIGRATE exactly") {
		t.Fatalf("wrong migration confirmation returned %d: %s", page.Code, page.Body.String())
	}
	if client.applyCalls != 0 {
		t.Fatalf("migration apply API called %d times before exact confirmation", client.applyCalls)
	}
}

func TestDataMigrationApplyReplansAndStartsPersistentOperation(t *testing.T) {
	client := &fakeDataMigrationAPI{plan: dataMigrationPagePlan(true, true)}
	client.apply = api.AdminDataMigrationApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: migrationPageOperationID, OperationType: "data_migration",
		PlanFingerprint: migrationPageFingerprint, State: "queued", Stage: "queued", Status: "Minecraft data migration operation queued.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:00:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "operation": {"erase_disk"}, "device": {"/dev/vdb"},
		"mount_point": {"/var/mnt/justvoxel-data"}, "path": {"/var/mnt/justvoxel-data/minecraft"}, "size_gib": {"all"},
		"plan_fingerprint": {migrationPageFingerprint}, "migration_confirmation": {"MIGRATE"},
		"destructive_confirmation": {"ERASE /dev/vdb"}, "players_confirmed": {"yes"},
	}
	page := migrationResponse(app, migrationRequest(http.MethodPost, "http://example/settings/data-migration/apply", values.Encode()))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/data-migration/progress/"+migrationPageOperationID {
		t.Fatalf("migration apply returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 1 ||
		!client.applyRequest.MigrationConfirmed || !client.applyRequest.PlayersConfirmed ||
		client.applyRequest.Confirmation != "ERASE /dev/vdb" {
		t.Fatalf("unexpected migration apply request: %#v plan_calls=%d apply_calls=%d", client.applyRequest, client.planCalls, client.applyCalls)
	}
}

func TestDataMigrationEntryReconnectsAndProgressUsesSameJournal(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: migrationPageOperationID, OperationType: "data_migration",
		PlanFingerprint: migrationPageFingerprint, State: "running", Stage: "copying",
		Status: "Copying Minecraft data to the reviewed storage target.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:01:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeDataMigrationAPI{current: operation, operation: operation}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	entry := migrationResponse(app, migrationRequest(http.MethodGet, "http://example/settings/data-migration", ""))
	if entry.Code != http.StatusSeeOther || entry.Header().Get("Location") != "/settings/data-migration/progress/"+migrationPageOperationID {
		t.Fatalf("migration entry did not reconnect: %d %q", entry.Code, entry.Header().Get("Location"))
	}

	page := migrationResponse(app, migrationRequest(http.MethodGet, "http://example/settings/data-migration/progress/"+migrationPageOperationID, ""))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Copying Minecraft data") ||
		!strings.Contains(page.Body.String(), "/api/data-migration/progress/"+migrationPageOperationID) {
		t.Fatalf("migration progress page unexpected: %d %s", page.Code, page.Body.String())
	}
	status := migrationResponse(app, migrationRequest(http.MethodGet, "http://example/api/data-migration/progress/"+migrationPageOperationID, ""))
	if status.Code != http.StatusOK {
		t.Fatalf("migration progress API returned %d: %s", status.Code, status.Body.String())
	}
	var response api.PersistentOperationResponse
	if err := json.Unmarshal(status.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.OperationID != migrationPageOperationID || response.Operation.Stage != "copying" {
		t.Fatalf("unexpected migration progress response: %#v", response)
	}
}

func TestDataMigrationProgressLabelsRollbackAndNeedsAttention(t *testing.T) {
	if dataMigrationOperationStateLabel("rolling_back") != "Rolling back" ||
		dataMigrationOperationStageLabel("rollback") != "Restoring previous configuration" {
		t.Fatal("migration rollback labels drifted")
	}
	if dataMigrationOperationStateLabel("needs_attention") != "Needs attention" ||
		dataMigrationOperationStageLabel("migration_backend_interrupted") != "Administrator attention required" {
		t.Fatal("migration needs-attention labels drifted")
	}
}
