package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestMigrationWorkspaceRecoveryAttentionWithoutTransactions(t *testing.T) {
	current := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_recovery",
		PlanFingerprint: serverMigrationFingerprint, State: "needs_attention", Stage: "recovery_backend_invalid",
		Status: "Migration recovery backend returned invalid progress data.", StartedAt: "x", UpdatedAt: "x",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeServerMigrationRecoveryAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			current:  current,
			recovery: api.AdminMigrationRecoveryDiscoveryResponse{OK: true, SchemaVersion: "v1", Configured: true, Transactions: []api.AdminMigrationRecoverySummary{}},
		},
		recoveryResolve: api.AdminMigrationRecoveryResolveResponse{Operation: &api.PersistentOperation{
			OperationID: current.OperationID, OperationType: "migration_recovery", State: "resolved",
		}},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	request := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/recovery", "")
	request.Header.Set(migrationWorkspaceFragmentHeader, "1")
	page := httptestResponse(app, request)
	for _, want := range []string{"Migration Recovery needs attention", "No retained recovery files remain", "Keep current server and continue", `data-migration-recovery-available="true"`} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("recovery page missing %q: %d %s", want, page.Code, page.Body.String())
		}
	}

	values := url.Values{"csrf": {"csrf-token"}, "operation_id": {current.OperationID}, "keep_current_state": {"yes"}}
	resolved := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/resolve", values))
	if resolved.Code != http.StatusSeeOther || resolved.Header().Get("Location") != "/workspace/migration" {
		t.Fatalf("resolve response = %d %q: %s", resolved.Code, resolved.Header().Get("Location"), resolved.Body.String())
	}
	if client.recoveryResolveCalls != 1 || client.resolveCalls != 0 || client.recoveryResolveReq.OperationID != current.OperationID || !client.recoveryResolveReq.KeepCurrentState {
		t.Fatalf("wrong resolve API: recovery=%d import=%d request=%#v", client.recoveryResolveCalls, client.resolveCalls, client.recoveryResolveReq)
	}

	client.recoveryResolveErr = &api.ResponseError{StatusCode: http.StatusConflict, Message: "Current appliance validation requires review."}
	rejected := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/resolve", values))
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "Current appliance validation requires review.") || !strings.Contains(rejected.Body.String(), "Keep current server and continue") {
		t.Fatalf("rejected resolve = %d: %s", rejected.Code, rejected.Body.String())
	}
}

func TestMigrationRecoveryTabVisibilityContract(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`syncMigrationRecoveryAvailability(root);`,
		`if (!Object.hasOwn(root.dataset, "migrationRecoveryAvailable")) return;`,
		`recoveryTab.hidden = !available && currentTab !== "recovery";`,
		`if (requestedMigrationTab === "recovery") {`,
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("Recovery tab script missing %q", want)
		}
	}
}
