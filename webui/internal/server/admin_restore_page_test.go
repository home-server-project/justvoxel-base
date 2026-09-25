package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const restorePageFingerprint = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const restorePageOperationID = "87654321-4321-4abc-8def-123456789abc"

type fakeRestoreAPI struct {
	fakeAPI
	role           string
	backups        api.AdminRestoreBackupsResponse
	plan           api.AdminRestorePlanResponse
	planErr        error
	apply          api.AdminRestoreApplyResponse
	applyErr       error
	applyRequest   api.AdminRestoreApplyRequest
	applyCalls     int
	discoveryCalls int
	current        *api.PersistentOperation
	operation      *api.PersistentOperation
}

func (f *fakeRestoreAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeRestoreAPI) AdminRestoreBackups(_ context.Context, session string) (api.AdminRestoreBackupsResponse, error) {
	if session != "session-token" {
		return api.AdminRestoreBackupsResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.backups, nil
}

func (f *fakeRestoreAPI) AdminRestorePlan(_ context.Context, session string, request api.AdminRestorePlanRequest) (api.AdminRestorePlanResponse, error) {
	if session != "session-token" {
		return api.AdminRestorePlanResponse{}, api.ErrUnauthorized
	}
	if request.BackupID == "" || request.Mode == "" {
		return api.AdminRestorePlanResponse{}, &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "invalid request"}
	}
	return f.plan, f.planErr
}

func (f *fakeRestoreAPI) AdminRestoreApply(_ context.Context, session string, request api.AdminRestoreApplyRequest) (api.AdminRestoreApplyResponse, error) {
	if session != "session-token" {
		return api.AdminRestoreApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyRequest = request
	return f.apply, f.applyErr
}

func (f *fakeRestoreAPI) AdminCurrentRestoreOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.current == nil {
		return api.PersistentOperationResponse{}, nil
	}
	copy := *f.current
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func (f *fakeRestoreAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.operation == nil || f.operation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	copy := *f.operation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func restorePlan(players bool) api.AdminRestorePlanResponse {
	requirements := &api.AdminRestorePlanRequirements{
		DestructiveConfirmationRequired: true, PlayersConfirmationRequired: players,
		MinecraftState: "running", Online: 0, Players: []string{},
		ArchiveIntegrityValidationOnApply: true, ArchiveSafetyValidationOnApply: true, StagingSpaceValidationOnApply: true,
	}
	if players {
		requirements.Online = 1
		requirements.Players = []string{"PlayerOne"}
	}
	return api.AdminRestorePlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: restorePageFingerprint,
		Normalized: &api.AdminRestorePlanNormalized{
			BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world", CreatedAt: "2026-09-20T08:30:00Z",
			SizeBytes: 12345, MetadataStatus: "valid",
			Metadata:        &api.AdminRestoreBackupMetadata{Minecraft: api.AdminRestoreMetadataMinecraft{VersionMode: "pinned", ConfiguredVersion: "26.2"}},
			Current:         api.AdminRestorePlanCurrent{VersionMode: "pinned", Version: "26.3"},
			VersionRelation: "backup_older",
			Validation:      api.AdminRestorePlanValidation{ArchiveIntegrity: "pending_apply", ArchiveSafety: "pending_apply", StagingSpace: "pending_apply"},
		},
		Warnings:     []api.AdminRestorePlanWarning{{Code: "backup_older", Message: "This backup is older than the configured Minecraft version."}},
		Requirements: requirements,
	}
}

func TestRestorePageListsBackupsAndIsAdministratorOnly(t *testing.T) {
	client := &fakeRestoreAPI{backups: api.AdminRestoreBackupsResponse{Backups: []api.AdminRestoreBackup{{
		ID: "minecraft-2026-09-20-043000.tar.gz", CreatedAt: "2026-09-20T08:30:00Z", SizeBytes: 12345, MetadataStatus: "missing",
	}}}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/restore", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("restore page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Restore Minecraft", "minecraft-2026-09-20-043000.tar.gz", "metadata unavailable", "Restore world", "Restore full Minecraft data"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("restore page missing %q: %s", want, page.Body.String())
		}
	}

	client.role = "operator"
	denied := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/restore", ""))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("operator restore page status = %d, want 403", denied.Code)
	}
	if client.discoveryCalls != 1 {
		t.Fatalf("restore discovery ran for non-administrator; calls=%d", client.discoveryCalls)
	}
}

func TestRestoreReviewShowsAuthoritativeWarningsAndConfirmations(t *testing.T) {
	client := &fakeRestoreAPI{plan: restorePlan(true)}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/restore/review?backup_id=minecraft-2026-09-20-043000.tar.gz&mode=world", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("restore review returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Review Restore", "This backup is older than the configured Minecraft version.", "PlayerOne",
		"data-destructive-confirmation", `data-confirm-value="RESTORE"`, "data-destructive-submit", "name=\"players_confirmed\"", "Current configured version", "26.3",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("restore review missing %q: %s", want, page.Body.String())
		}
	}
}

func TestRestoreApplyRequiresExactConfirmationBeforeAgentApply(t *testing.T) {
	client := &fakeRestoreAPI{plan: restorePlan(false)}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "backup_id": {"minecraft-2026-09-20-043000.tar.gz"},
		"mode": {"world"}, "plan_fingerprint": {restorePageFingerprint}, "confirmation": {"restore"},
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/restore/apply", values.Encode()))
	if page.Code != http.StatusBadRequest || !strings.Contains(page.Body.String(), "Type RESTORE exactly") {
		t.Fatalf("wrong confirmation returned %d: %s", page.Code, page.Body.String())
	}
	if client.applyCalls != 0 {
		t.Fatalf("Restore apply API called %d times before exact confirmation", client.applyCalls)
	}
}

func TestRestoreApplyReplansAndStartsPersistentOperation(t *testing.T) {
	client := &fakeRestoreAPI{plan: restorePlan(true)}
	client.apply = api.AdminRestoreApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: restorePageOperationID, OperationType: "restore",
		PlanFingerprint: restorePageFingerprint, State: "queued", Stage: "queued", Status: "Restore operation queued.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:00:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "backup_id": {"minecraft-2026-09-20-043000.tar.gz"},
		"mode": {"world"}, "plan_fingerprint": {restorePageFingerprint}, "confirmation": {"RESTORE"}, "players_confirmed": {"yes"},
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/restore/apply", values.Encode()))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/restore/progress/"+restorePageOperationID {
		t.Fatalf("Restore apply returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.applyCalls != 1 || !client.applyRequest.DestructiveConfirmed || !client.applyRequest.PlayersConfirmed {
		t.Fatalf("unexpected Restore apply request: %#v calls=%d", client.applyRequest, client.applyCalls)
	}
}

func TestRestoreEntryReconnectsToCurrentOperationAndProgressUsesSameJournal(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: restorePageOperationID, OperationType: "restore",
		PlanFingerprint: restorePageFingerprint, State: "running", Stage: "staging",
		Status:    "Preparing verified Restore data on the Minecraft data filesystem.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:01:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeRestoreAPI{current: operation, operation: operation}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	entry := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/restore", ""))
	if entry.Code != http.StatusSeeOther || entry.Header().Get("Location") != "/settings/restore/progress/"+restorePageOperationID {
		t.Fatalf("Restore entry did not reconnect: %d %q", entry.Code, entry.Header().Get("Location"))
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/restore/progress/"+restorePageOperationID, ""))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Preparing Restore data") || !strings.Contains(page.Body.String(), "/api/restore/progress/"+restorePageOperationID) {
		t.Fatalf("Restore progress page unexpected: %d %s", page.Code, page.Body.String())
	}
	status := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/restore/progress/"+restorePageOperationID, ""))
	if status.Code != http.StatusOK {
		t.Fatalf("Restore progress API returned %d: %s", status.Code, status.Body.String())
	}
	var response api.PersistentOperationResponse
	if err := json.Unmarshal(status.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.OperationID != restorePageOperationID || response.Operation.Stage != "staging" {
		t.Fatalf("unexpected Restore progress response: %#v", response)
	}
}

func TestRestoreProgressLabelsRollbackAndNeedsAttention(t *testing.T) {
	if restoreOperationStateLabel("rolling_back") != "Rolling back" || restoreOperationStageLabel("rollback") != "Restoring previous Minecraft data" {
		t.Fatal("Restore rollback labels drifted")
	}
	if restoreOperationStateLabel("needs_attention") != "Needs attention" || restoreOperationStageLabel("restore_needs_attention") != "Administrator attention required" {
		t.Fatal("Restore needs-attention labels drifted")
	}
}
