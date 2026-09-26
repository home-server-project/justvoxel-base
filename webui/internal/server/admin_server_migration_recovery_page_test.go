package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeServerMigrationRecoveryAPI struct {
	fakeServerMigrationAPI
	plan       api.AdminMigrationRecoveryPlanResponse
	planErr    error
	planCalls  int
	planReq    api.AdminMigrationRecoveryPlanRequest
	apply      api.AdminMigrationApplyResponse
	applyErr   error
	applyCalls int
	applyReq   api.AdminMigrationRecoveryApplyRequest
	resolve      api.AdminMigrationImportResolveResponse
	resolveErr   error
	resolveCalls int
	resolveReq   api.AdminMigrationImportResolveRequest
}

func (f *fakeServerMigrationRecoveryAPI) AdminMigrationRecoveryPlan(_ context.Context, session string, request api.AdminMigrationRecoveryPlanRequest) (api.AdminMigrationRecoveryPlanResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationRecoveryPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planReq = request
	return f.plan, f.planErr
}

func (f *fakeServerMigrationRecoveryAPI) AdminMigrationRecoveryApply(_ context.Context, session string, request api.AdminMigrationRecoveryApplyRequest) (api.AdminMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyReq = request
	return f.apply, f.applyErr
}

func (f *fakeServerMigrationRecoveryAPI) AdminMigrationImportResolve(_ context.Context, session string, request api.AdminMigrationImportResolveRequest) (api.AdminMigrationImportResolveResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationImportResolveResponse{}, api.ErrUnauthorized
	}
	f.resolveCalls++
	f.resolveReq = request
	return f.resolve, f.resolveErr
}

func recoveryDiscoveryFixture() api.AdminMigrationRecoveryDiscoveryResponse {
	return api.AdminMigrationRecoveryDiscoveryResponse{
		OK: true, SchemaVersion: "v1", Configured: true,
		Transactions: []api.AdminMigrationRecoverySummary{
			{Transaction: "/var/lib/justvoxel/.justvoxel-import-safe", Phase: "rolled-back", SourceType: "paper", UpdatedAt: "2026-09-21T14:00:00Z", Mode: "configured", Finalizable: true},
			{Transaction: "/var/lib/justvoxel/.justvoxel-import-critical", Phase: "critical-rollback", SourceType: "vanilla", UpdatedAt: "2026-09-21T14:10:00Z", Mode: "configured-attention", Finalizable: true},
			{Transaction: "/var/lib/justvoxel/.justvoxel-import-manual", Phase: "prepared", SourceType: "paper", UpdatedAt: "2026-09-21T14:20:00Z", Mode: "manual", Finalizable: false},
		},
	}
}

func recoveryPlanFixture(transaction, phase, mode string) api.AdminMigrationRecoveryPlanResponse {
	return api.AdminMigrationRecoveryPlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: serverMigrationFingerprint,
		Normalized: &api.AdminMigrationRecoverySummary{
			Transaction: transaction, Phase: phase, SourceType: "paper", UpdatedAt: "2026-09-21T14:00:00Z", Mode: mode, Finalizable: true,
		},
		Warnings: []api.AdminMigrationWarning{},
		Requirements: &api.AdminMigrationRecoveryRequirements{
			FinalizeConfirmationRequired:   true,
			RuntimeValidationRequired:      phase == "rolled-back" || phase == "critical-rollback",
			UnconfiguredValidationRequired: phase == "rolled-back-fresh",
		},
	}
}

func recoveryWebRequest(method, target string, values url.Values) *http.Request {
	return exportWebRequest(method, target, values)
}

func TestMigrationRecoveryPageShowsOnlyAgentFinalizableActions(t *testing.T) {
	client := &fakeServerMigrationRecoveryAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{recovery: recoveryDiscoveryFixture()}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, recoveryWebRequest(http.MethodGet, "http://example/settings/server-migration/recovery", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("Migration Recovery page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Migration Recovery", "Configured server rollback", "Configured rollback requiring attention",
		"Critical rollback retained", "/var/lib/justvoxel/.justvoxel-import-safe",
		"not safely finalizable in the current appliance state",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Migration Recovery page missing %q: %s", want, page.Body.String())
		}
	}
	if got := strings.Count(page.Body.String(), "Review finalization"); got != 2 {
		t.Fatalf("finalizable Recovery actions = %d, want 2", got)
	}
}

func TestMigrationRecoveryReviewUsesAuthoritativeValidationRequirements(t *testing.T) {
	transaction := "/var/lib/justvoxel/.justvoxel-import-safe"
	client := &fakeServerMigrationRecoveryAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{recovery: recoveryDiscoveryFixture()},
		plan:                   recoveryPlanFixture(transaction, "rolled-back", "configured"),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"csrf": {"csrf-token"}, "transaction": {transaction}}
	page := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/settings/server-migration/recovery/review", values))
	if page.Code != http.StatusOK {
		t.Fatalf("Migration Recovery review returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Review Migration Recovery", "Configured server rollback", "Rolled back", transaction,
		"validate the current JustVoxel runtime", "does not rerun the failed Import", "Type FINALIZE",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Migration Recovery review missing %q: %s", want, page.Body.String())
		}
	}
	if client.planCalls != 1 || client.planReq.Transaction != transaction {
		t.Fatalf("unexpected Recovery plan request: %#v calls=%d", client.planReq, client.planCalls)
	}
}

func TestMigrationRecoveryRejectsNonFinalizableDiscoveryStateBeforePlanning(t *testing.T) {
	client := &fakeServerMigrationRecoveryAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{recovery: recoveryDiscoveryFixture()}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"csrf": {"csrf-token"}, "transaction": {"/var/lib/justvoxel/.justvoxel-import-manual"}}
	page := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/settings/server-migration/recovery/review", values))
	if page.Code != http.StatusBadRequest || !strings.Contains(page.Body.String(), "not safely finalizable") {
		t.Fatalf("non-finalizable Recovery returned %d: %s", page.Code, page.Body.String())
	}
	if client.planCalls != 0 {
		t.Fatalf("Recovery planner called for non-finalizable discovery state: %d", client.planCalls)
	}
}

func TestMigrationRecoveryApplyRejectsStaleReviewBeforeAgentApply(t *testing.T) {
	transaction := "/var/lib/justvoxel/.justvoxel-import-safe"
	client := &fakeServerMigrationRecoveryAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{recovery: recoveryDiscoveryFixture()},
		plan:                   recoveryPlanFixture(transaction, "rolled-back", "configured"),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "transaction": {transaction},
		"plan_fingerprint":      {"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"finalize_confirmation": {"FINALIZE"},
	}
	page := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/settings/server-migration/recovery/apply", values))
	if page.Code != http.StatusConflict || !strings.Contains(page.Body.String(), "changed since it was reviewed") {
		t.Fatalf("stale Migration Recovery returned %d: %s", page.Code, page.Body.String())
	}
	if client.applyCalls != 0 {
		t.Fatalf("Recovery Apply called for stale plan: %d", client.applyCalls)
	}
}

func TestMigrationRecoveryCanTakeOverImportNeedsAttentionAndStartPersistentOperation(t *testing.T) {
	transaction := "/var/lib/justvoxel/.justvoxel-import-critical"
	current := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: "34567891-1234-4abc-8def-123456789abc", OperationType: "migration_import",
		PlanFingerprint: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		State:           "needs_attention", Stage: "import_needs_attention", Status: "Retained Import recovery state requires review.",
		StartedAt: "2026-09-21T14:00:00Z", UpdatedAt: "2026-09-21T14:05:00Z", Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeServerMigrationRecoveryAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{recovery: recoveryDiscoveryFixture(), current: current},
		plan:                   recoveryPlanFixture(transaction, "critical-rollback", "configured-attention"),
	}
	client.apply = api.AdminMigrationApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_recovery",
		PlanFingerprint: serverMigrationFingerprint, State: "queued", Stage: "queued", Status: "Server migration recovery operation queued.",
		StartedAt: "2026-09-21T15:00:00Z", UpdatedAt: "2026-09-21T15:00:00Z", Rollback: api.PersistentOperationRollback{State: "not_started"},
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	entry := httptestResponse(app, recoveryWebRequest(http.MethodGet, "http://example/settings/server-migration/recovery", nil))
	if entry.Code != http.StatusOK || !strings.Contains(entry.Body.String(), "previous Server Import still needs attention") {
		t.Fatalf("Recovery entry from needs-attention Import returned %d: %s", entry.Code, entry.Body.String())
	}

	values := url.Values{
		"csrf": {"csrf-token"}, "transaction": {transaction}, "plan_fingerprint": {serverMigrationFingerprint},
		"finalize_confirmation": {"FINALIZE"},
	}
	page := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/settings/server-migration/recovery/apply", values))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/server-migration/progress/"+serverMigrationOperationID {
		t.Fatalf("Migration Recovery apply returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 1 || !client.applyReq.FinalizeConfirmed || client.applyReq.Transaction != transaction {
		t.Fatalf("unexpected Recovery calls plan=%d apply=%d request=%#v", client.planCalls, client.applyCalls, client.applyReq)
	}
}

func TestMigrationRecoveryProgressUsesRecoveryStageLabels(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_recovery",
		PlanFingerprint: serverMigrationFingerprint, State: "running", Stage: "recovery_finalize",
		Status: "Removing retained migration recovery transaction.", StartedAt: "x", UpdatedAt: "x",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeServerMigrationRecoveryAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{operation: operation}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, recoveryWebRequest(http.MethodGet, "http://example/settings/server-migration/progress/"+serverMigrationOperationID, nil))
	if page.Code != http.StatusOK {
		t.Fatalf("Migration Recovery progress returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Migration Recovery", "Finalizing retained recovery state", "Removing retained migration recovery transaction."} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Migration Recovery progress missing %q: %s", want, page.Body.String())
		}
	}
}

func TestImportNeedsAttentionProgressLinksDirectlyToMigrationRecovery(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_import",
		PlanFingerprint: serverMigrationFingerprint, State: "needs_attention", Stage: "import_needs_attention",
		Status: "Retained Import recovery state requires review.", StartedAt: "x", UpdatedAt: "x",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeServerMigrationRecoveryAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{current: operation, operation: operation}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, recoveryWebRequest(http.MethodGet, "http://example/settings/server-migration/progress/"+serverMigrationOperationID, nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "href=\"/settings/server-migration/recovery\"") || !strings.Contains(page.Body.String(), "Review Migration Recovery") {
		t.Fatalf("needs-attention Import progress did not expose Recovery handoff: %d %s", page.Code, page.Body.String())
	}
}


func TestMigrationRecoveryOffersKeepCurrentForOrphanedImport(t *testing.T) {
	current := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: "34567891-1234-4abc-8def-123456789abc", OperationType: "migration_import",
		PlanFingerprint: serverMigrationFingerprint, State: "needs_attention", Stage: "import_backend_interrupted",
		Status: "Import backend stopped before a retained transaction was created.", StartedAt: "x", UpdatedAt: "x",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeServerMigrationRecoveryAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			recovery: api.AdminMigrationRecoveryDiscoveryResponse{OK: true, SchemaVersion: "v1", Configured: true, Transactions: []api.AdminMigrationRecoverySummary{}},
			current: current,
		},
		resolve: api.AdminMigrationImportResolveResponse{Operation: &api.PersistentOperation{
			SchemaVersion: "v1", OperationID: current.OperationID, OperationType: "migration_import",
			PlanFingerprint: serverMigrationFingerprint, State: "resolved", Stage: "resolved", Status: "Current server kept.",
			StartedAt: "x", UpdatedAt: "x", FinishedAt: "x", Rollback: api.PersistentOperationRollback{State: "not_started"},
		}},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	entry := httptestResponse(app, recoveryWebRequest(http.MethodGet, "http://example/settings/server-migration/recovery", nil))
	if entry.Code != http.StatusOK {
		t.Fatalf("orphaned Import Recovery page returned %d: %s", entry.Code, entry.Body.String())
	}
	for _, want := range []string{"No retained migration recovery transaction", "Keep current server and continue", "/settings/server-migration/recovery/resolve"} {
		if !strings.Contains(entry.Body.String(), want) {
			t.Fatalf("orphaned Import Recovery page missing %q: %s", want, entry.Body.String())
		}
	}

	values := url.Values{
		"csrf": {"csrf-token"},
		"operation_id": {current.OperationID},
		"keep_current_state": {"yes"},
	}
	result := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/settings/server-migration/recovery/resolve", values))
	if result.Code != http.StatusSeeOther || result.Header().Get("Location") != "/settings/server-migration" {
		t.Fatalf("orphaned Import resolve returned %d %q: %s", result.Code, result.Header().Get("Location"), result.Body.String())
	}
	if client.resolveCalls != 1 || client.resolveReq.OperationID != current.OperationID || !client.resolveReq.KeepCurrentState {
		t.Fatalf("unexpected Import resolve request: calls=%d request=%#v", client.resolveCalls, client.resolveReq)
	}
}
