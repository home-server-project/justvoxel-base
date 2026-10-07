package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// These tests exercise the registered Workspace handlers with administrator sessions.
func migrationSafetyApp(t *testing.T, client API) *App {
	t.Helper()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func migrationSafetyResponse(t *testing.T, response *httptest.ResponseRecorder, status int, wants ...string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d, want %d: %s", response.Code, status, response.Body.String())
	}
	for _, want := range wants {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("response missing %q: %s", want, response.Body.String())
		}
	}
}

func migrationSafetyStarted(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	migrationSafetyResponse(t, response, http.StatusSeeOther)
	if got := response.Header().Get("Location"); got != "/workspace/migration/progress/"+serverMigrationOperationID {
		t.Fatalf("progress redirect=%q", got)
	}
}

func migrationSafetyOperation(kind string) api.AdminMigrationApplyResponse {
	return api.AdminMigrationApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: kind,
		PlanFingerprint: serverMigrationFingerprint, State: "queued", Stage: "queued",
	}}
}

func migrationSafetyExportValues(kind string) url.Values {
	values := url.Values{"csrf": {"csrf-token"}, "kind": {kind}, "filename": {"justvoxel-migration-test.tar.gz"},
		"plan_fingerprint": {serverMigrationFingerprint}, "export_confirmation": {"EXPORT"}}
	if kind == "smb" {
		values.Set("source", "//nas/migrations")
		values.Set("username", "voxel")
		values.Set("domain", "HOME")
	} else {
		values.Set("path", "/srv/migrations")
	}
	return values
}

func migrationSafetyImportValues(kind string) url.Values {
	values := configuredImportRequestValues(kind)
	values.Set("plan_fingerprint", serverMigrationFingerprint)
	values.Set("import_confirmation", "IMPORT")
	for _, field := range []string{"players_confirmed", "eula_accepted", "vanilla_confirmed", "plugins_confirmed", "online_mode_confirmed"} {
		values.Set(field, "yes")
	}
	return values
}

func migrationSafetyImportClient(plan api.AdminMigrationImportPlanResponse) *fakeServerMigrationImportAPI {
	return &fakeServerMigrationImportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: importMediaFixture(), importDiscovery: importDiscoveryFixture(true)},
		plan:                   plan,
	}
}

func migrationSafetyRecoveryClient() *fakeServerMigrationRecoveryAPI {
	discovery := recoveryDiscoveryFixture()
	summary := discovery.Transactions[0]
	return &fakeServerMigrationRecoveryAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{recovery: discovery},
		plan: api.AdminMigrationRecoveryPlanResponse{
			OK: true, SchemaVersion: "v1", PlanFingerprint: serverMigrationFingerprint, Normalized: &summary,
			Requirements: &api.AdminMigrationRecoveryRequirements{FinalizeConfirmationRequired: true, RuntimeValidationRequired: true},
		},
	}
}

func migrationSafetyRecoveryValues(client *fakeServerMigrationRecoveryAPI) url.Values {
	return url.Values{"csrf": {"csrf-token"}, "transaction": {client.plan.Normalized.Transaction},
		"plan_fingerprint": {client.plan.PlanFingerprint}, "cleanup_confirmed": {"yes"}}
}

func TestMigrationWorkspaceMutationRoutesRequireCSRF(t *testing.T) {
	// Include Apply and Resolve: none had surviving request-level CSRF coverage.
	for _, kind := range []string{"export", "import", "recovery"} {
		routes := []string{"review", "apply"}
		if kind == "recovery" {
			routes = append(routes, "resolve")
		}
		for _, route := range routes {
			t.Run(kind+"/"+route, func(t *testing.T) {
				export := &fakeServerMigrationExportAPI{}
				imp := &fakeServerMigrationImportAPI{}
				recovery := &fakeServerMigrationRecoveryAPI{}
				var client API = export
				if kind == "import" {
					client = imp
				}
				if kind == "recovery" {
					client = recovery
				}
				request := exportWebRequest(http.MethodPost, "http://example/workspace/migration/"+kind+"/"+route, nil)
				response := httptestResponse(migrationSafetyApp(t, client), request)
				migrationSafetyResponse(t, response, http.StatusForbidden, "CSRF")
				if export.planCalls+imp.planCalls+recovery.planCalls != 0 ||
					export.applyCalls+imp.applyCalls+recovery.applyCalls+recovery.resolveCalls+recovery.recoveryResolveCalls != 0 {
					t.Fatal("request without CSRF reached a mutation API")
				}
			})
		}
	}
}

func TestMigrationWorkspaceExportReviewUsesAuthoritativePlan(t *testing.T) {
	client := &fakeServerMigrationExportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()}, plan: exportPlanFixture("local", true),
	}
	// Backend normalization must win over the submitted destination and filename.
	client.plan.Normalized.TargetDisplay = "/srv/authoritative/backend-bundle.tar.gz"
	client.plan.Normalized.Filename = "backend-bundle.tar.gz"
	client.plan.Normalized.DataPath = "/srv/authoritative/minecraft"
	response := httptestResponse(migrationSafetyApp(t, client), exportWebRequest(http.MethodPost,
		"http://example/workspace/migration/export/review", migrationSafetyExportValues("local")))
	migrationSafetyResponse(t, response, http.StatusOK, client.plan.Normalized.TargetDisplay,
		client.plan.Normalized.Filename, client.plan.Normalized.DataPath, "2.0 GiB", "10.0 GiB", "PlayerOne",
		`name="players_confirmed"`, `name="export_confirmation"`, `data-confirm-value="EXPORT"`, "data-destructive-slider", "data-destructive-toggle")
	if client.planCalls != 1 || client.applyCalls != 0 || client.planReq.Path != "/srv/migrations" {
		t.Fatalf("review calls: plan=%d apply=%d request=%+v", client.planCalls, client.applyCalls, client.planReq)
	}
}

func TestMigrationWorkspaceExportSMBPasswordIsApplyOnly(t *testing.T) {
	for _, players := range []bool{false, true} {
		name := "no players"
		if players {
			name = "players online"
		}
		t.Run(name, func(t *testing.T) {
			client := &fakeServerMigrationExportAPI{
				fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()},
				plan:                   exportPlanFixture("smb", players), apply: migrationSafetyOperation("migration_export"),
			}
			app := migrationSafetyApp(t, client)
			values := migrationSafetyExportValues("smb")
			const reviewSecret = "export-review-secret-10b"
			values.Set("smb_password", reviewSecret)
			review := httptestResponse(app, exportWebRequest(http.MethodPost, "http://example/workspace/migration/export/review", values))
			migrationSafetyResponse(t, review, http.StatusOK)
			publicPlanRequest, err := json.Marshal(client.planReq)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(review.Body.String(), reviewSecret) || strings.Contains(review.Body.String(), `name="smb_password"`) ||
				strings.Contains(string(publicPlanRequest), reviewSecret) || strings.Contains(string(publicPlanRequest), "password") {
				t.Fatal("Export review retained an apply-only credential")
			}
			if client.planCalls != 1 || client.applyCalls != 0 {
				t.Fatal("review must plan once without Apply")
			}
			client.planCalls = 0
			client.mutationCalls = nil
			const applySecret = "export-apply-secret-10b"
			values.Set("smb_password", applySecret)
			if players {
				values.Set("players_confirmed", "yes")
			}
			applied := httptestResponse(app, exportWebRequest(http.MethodPost, "http://example/workspace/migration/export/apply", values))
			migrationSafetyStarted(t, applied)
			if client.planCalls != 1 || client.applyCalls != 1 || strings.Join(client.mutationCalls, ",") != "export plan,export apply" {
				t.Fatalf("Apply must replan first: %v", client.mutationCalls)
			}
			if client.applyReq.SMBPassword != applySecret || !client.applyReq.ExportConfirmed || client.applyReq.PlayersConfirmed != players ||
				client.applyReq.PlanFingerprint != client.plan.PlanFingerprint {
				t.Fatal("Export Apply lost reviewed confirmations or apply-only credential")
			}
			replanned, err := json.Marshal(client.planReq)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(replanned), applySecret) || strings.Contains(applied.Body.String(), applySecret) {
				t.Fatal("Export Apply credential leaked into planning or response")
			}
		})
	}
}

func TestMigrationWorkspaceExportApplyRejectsStaleReviewedPlan(t *testing.T) {
	client := &fakeServerMigrationExportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()}, plan: exportPlanFixture("local", false),
	}
	values := migrationSafetyExportValues("local")
	values.Set("plan_fingerprint", "sha256:"+strings.Repeat("a", 64))
	response := httptestResponse(migrationSafetyApp(t, client), exportWebRequest(http.MethodPost, "http://example/workspace/migration/export/apply", values))
	migrationSafetyResponse(t, response, http.StatusConflict, "changed since", client.plan.PlanFingerprint)
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("stale plan: plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
}

func TestMigrationWorkspaceImportRequiredConfirmationsBlockApply(t *testing.T) {
	for _, tc := range []struct{ field, message string }{
		{"players_confirmed", "online players"}, {"eula_accepted", "Minecraft EULA"},
		{"vanilla_confirmed", "Vanilla-to-Paper"}, {"plugins_confirmed", "external plugin JARs"},
		{"online_mode_confirmed", "online-mode=true"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			plan := successfulImportPlan("replace")
			plan.Requirements.EULAAcceptanceRequired = true
			plan.Requirements.VanillaConfirmationRequired = true
			client := migrationSafetyImportClient(plan)
			values := migrationSafetyImportValues("local")
			values.Del(tc.field)
			response := httptestResponse(migrationSafetyApp(t, client), importWebRequest(http.MethodPost, "http://example/workspace/migration/import/apply", values))
			migrationSafetyResponse(t, response, http.StatusBadRequest, tc.message)
			if client.planCalls != 1 || client.applyCalls != 0 {
				t.Fatalf("unconfirmed import: plan=%d apply=%d", client.planCalls, client.applyCalls)
			}
		})
	}
}

func TestMigrationWorkspaceImportApplyRejectsStaleReviewedPlan(t *testing.T) {
	client := migrationSafetyImportClient(successfulImportPlan("replace"))
	values := migrationSafetyImportValues("local")
	values.Set("plan_fingerprint", "sha256:"+strings.Repeat("a", 64))
	response := httptestResponse(migrationSafetyApp(t, client), importWebRequest(http.MethodPost, "http://example/workspace/migration/import/apply", values))
	migrationSafetyResponse(t, response, http.StatusConflict, "changed since", client.plan.PlanFingerprint)
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("stale import: plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
}

func TestMigrationWorkspaceImportSMBSourcePasswordIsNotEchoed(t *testing.T) {
	client := migrationSafetyImportClient(successfulImportPlan("replace"))
	const secret = "import-source-secret-10b"
	values := configuredImportRequestValues("smb")
	values.Set("source_smb_password", secret)
	response := httptestResponse(migrationSafetyApp(t, client), importWebRequest(http.MethodPost, "http://example/workspace/migration/import/review", values))
	migrationSafetyResponse(t, response, http.StatusOK, `action="/workspace/migration/import/apply"`)
	if client.planCalls != 1 || client.applyCalls != 0 || client.planReq.Source.SMBPassword != secret {
		t.Fatal("SMB planning must receive the ephemeral source credential without Apply")
	}
	if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), `name="source_smb_password"`) {
		t.Fatal("reviewed public request persisted the SMB source credential")
	}
}

func TestMigrationWorkspaceImportApplyReplansAndStartsPersistentOperation(t *testing.T) {
	client := migrationSafetyImportClient(successfulImportPlan("fresh"))
	client.importDiscovery = importDiscoveryFixture(false)
	client.storage = importStorageFixture()
	client.apply = migrationSafetyOperation("migration_import")
	values := migrationSafetyImportValues("smb")
	for field, value := range map[string]string{
		"java_port": "25565", "bedrock_port": "19132", "java_memory": "4G", "container_memory": "6G",
		"timezone": "UTC", "backup_keep": "7", "backup_daily_time": "04:30", "backup_automatic": "on",
		"storage_type": "system", "backup_type": "smb", "backup_path": "/var/mnt/backups/minecraft",
		"backup_source": "//nas/backups", "backup_username": "voxel", "backup_mount_point": "/var/mnt/backups",
	} {
		values.Set(field, value)
	}
	const backupSecret = "import-backup-apply-secret-10b"
	const sourceSecret = "import-source-apply-secret-10b"
	values.Set("backup_smb_password", backupSecret)
	values.Set("source_smb_password", sourceSecret)
	response := httptestResponse(migrationSafetyApp(t, client), importWebRequest(http.MethodPost, "http://example/workspace/migration/import/apply", values))
	migrationSafetyStarted(t, response)
	if client.planCalls != 1 || client.applyCalls != 1 || strings.Join(client.mutationCalls, ",") != "import plan,import apply" {
		t.Fatalf("Import must replan before Apply: %v", client.mutationCalls)
	}
	req := client.applyReq
	if !req.ImportConfirmed || !req.PlayersConfirmed || !req.EULAAccepted || !req.VanillaConfirmed || !req.PluginsConfirmed || !req.OnlineModeConfirmed ||
		req.BackupSMBPassword != backupSecret || req.PlanFingerprint != client.plan.PlanFingerprint || req.Request.Source.SMBPassword != sourceSecret ||
		client.planReq.Source.SMBPassword != sourceSecret {
		t.Fatal("Import Apply lost confirmations or ephemeral credentials")
	}
	planned, err := json.Marshal(client.planReq)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(planned), backupSecret) || strings.Contains(response.Body.String(), backupSecret) || strings.Contains(response.Body.String(), sourceSecret) {
		t.Fatal("Import credential leaked into planning or HTML")
	}
}

func TestMigrationWorkspaceRecoveryReviewUsesAuthoritativePlan(t *testing.T) {
	client := migrationSafetyRecoveryClient()
	// Discovery says rolled-back; the newer backend plan requires attention.
	client.plan.Normalized.Phase = "critical-rollback"
	client.plan.Normalized.Mode = "configured-attention"
	client.plan.Warnings = []api.AdminMigrationWarning{{Code: "validation", Message: "Backend requires current-server validation before cleanup."}}
	values := migrationSafetyRecoveryValues(client)
	response := httptestResponse(migrationSafetyApp(t, client), recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/review", values))
	migrationSafetyResponse(t, response, http.StatusOK, "Server Import needs attention", client.plan.Warnings[0].Message,
		`name="transaction" value="`+client.plan.Normalized.Transaction+`"`, client.plan.PlanFingerprint,
		`name="cleanup_confirmed"`, "data-recovery-confirmation-dialog")
	if strings.Contains(response.Body.String(), "Your server data is safe") {
		t.Fatal("review trusted stale discovery instead of backend plan")
	}
	if client.planCalls != 1 || client.applyCalls != 0 || client.planReq.Transaction != client.plan.Normalized.Transaction {
		t.Fatal("Recovery review must plan the selected transaction without Apply")
	}
}

func TestMigrationWorkspaceRecoveryApplyRejectsStaleReviewedPlan(t *testing.T) {
	client := migrationSafetyRecoveryClient()
	values := migrationSafetyRecoveryValues(client)
	values.Set("plan_fingerprint", "sha256:"+strings.Repeat("a", 64))
	response := httptestResponse(migrationSafetyApp(t, client), recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/apply", values))
	migrationSafetyResponse(t, response, http.StatusConflict, "state changed", "Review the current state", client.plan.PlanFingerprint)
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("stale recovery: plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
}

func TestMigrationWorkspaceRecoveryRequiresExplicitCleanupConfirmation(t *testing.T) {
	client := migrationSafetyRecoveryClient()
	values := migrationSafetyRecoveryValues(client)
	values.Del("cleanup_confirmed")
	response := httptestResponse(migrationSafetyApp(t, client), recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/apply", values))
	migrationSafetyResponse(t, response, http.StatusBadRequest, "Confirm cleanup")
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("unconfirmed recovery: plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
}

func TestMigrationWorkspaceRecoveryApplyStartsPersistentOperation(t *testing.T) {
	for _, mode := range []string{"configured", "configured-attention", "fresh-unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			client := migrationSafetyRecoveryClient()
			summary := &client.recovery.Transactions[0]
			summary.Mode = mode
			if mode == "configured-attention" {
				summary.Phase = "critical-rollback"
				client.current = &api.PersistentOperation{OperationID: "previous-import", OperationType: "migration_import", State: "needs_attention"}
			}
			if mode == "fresh-unconfigured" {
				summary.Phase = "rolled-back-fresh"
				client.recovery.Configured = false
			}
			normalized := *summary
			client.plan.Normalized = &normalized
			client.plan.Requirements.RuntimeValidationRequired = mode != "fresh-unconfigured"
			client.plan.Requirements.UnconfiguredValidationRequired = mode == "fresh-unconfigured"
			client.apply = migrationSafetyOperation("migration_recovery")
			response := httptestResponse(migrationSafetyApp(t, client), recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/apply", migrationSafetyRecoveryValues(client)))
			migrationSafetyStarted(t, response)
			if client.planCalls != 1 || client.applyCalls != 1 || strings.Join(client.mutationCalls, ",") != "recovery plan,recovery apply" {
				t.Fatalf("Recovery must replan before Apply: %v", client.mutationCalls)
			}
			if client.applyReq.Transaction != summary.Transaction || client.applyReq.PlanFingerprint != client.plan.PlanFingerprint || !client.applyReq.FinalizeConfirmed {
				t.Fatalf("Recovery Apply request=%+v", client.applyReq)
			}
		})
	}
}

func TestMigrationWorkspaceAuthenticationAndPasswordChangeHandling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		location string
	}{
		{"expired session", api.ErrUnauthorized, "/login"},
		{"password change required", api.ErrPasswordChangeRequired, "/password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeServerMigrationAPI{sessionErr: tc.err}
			response := httptestResponse(migrationSafetyApp(t, client), authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration", ""))
			migrationSafetyResponse(t, response, http.StatusSeeOther)
			if response.Header().Get("Location") != tc.location || client.discoveryCalls != 0 {
				t.Fatal("authentication failure did not stop Migration discovery")
			}
		})
	}
}

func TestMigrationWorkspaceRecoveryRejectsNonFinalizableTransactionBeforePlanning(t *testing.T) {
	for _, route := range []string{"review", "apply"} {
		t.Run(route, func(t *testing.T) {
			client := migrationSafetyRecoveryClient()
			values := migrationSafetyRecoveryValues(client)
			values.Set("transaction", "/var/lib/justvoxel/.justvoxel-import-manual")
			response := httptestResponse(migrationSafetyApp(t, client), recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/"+route, values))
			migrationSafetyResponse(t, response, http.StatusBadRequest, "cannot safely clean up")
			if client.planCalls != 0 || client.applyCalls != 0 {
				t.Fatal("non-finalizable transaction reached planning or Apply")
			}
		})
	}
}

func TestMigrationWorkspaceProgressAPIReconnectsToPersistentJournal(t *testing.T) {
	for _, kind := range []string{"migration_export", "migration_import", "migration_recovery"} {
		t.Run(kind, func(t *testing.T) {
			operation := &api.PersistentOperation{
				OperationID: serverMigrationOperationID, OperationType: kind, State: "running", Stage: "journal-stage",
				Status: "Authoritative persistent operation status.", PlanFingerprint: serverMigrationFingerprint,
			}
			client := &fakeServerMigrationExportAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{operation: operation}}
			response := httptestResponse(migrationSafetyApp(t, client), authenticatedAdminRequest(http.MethodGet,
				"http://example/api/workspace/migration/progress/"+serverMigrationOperationID, ""))
			migrationSafetyResponse(t, response, http.StatusOK)
			var result api.PersistentOperationResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Operation == nil {
				t.Fatal("persistent operation missing")
			}
			if result.Operation.OperationID != operation.OperationID || result.Operation.OperationType != kind ||
				result.Operation.Stage != operation.Stage || result.Operation.Status != operation.Status || result.Operation.State != operation.State {
				t.Fatalf("progress did not reconnect to journal: %+v", result.Operation)
			}
			if client.planCalls != 0 || client.applyCalls != 0 {
				t.Fatal("progress started a new operation")
			}
		})
	}
}
