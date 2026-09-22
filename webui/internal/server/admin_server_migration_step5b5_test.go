package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestServerMigrationAuthenticationAndPasswordChangeHandling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		location string
	}{
		{name: "expired session", err: api.ErrUnauthorized, location: "/login"},
		{name: "password change required", err: api.ErrPasswordChangeRequired, location: "/password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeServerMigrationAPI{sessionErr: tc.err}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server-migration", ""))
			if page.Code != http.StatusSeeOther || page.Header().Get("Location") != tc.location {
				t.Fatalf("migration auth response = %d %q, want redirect to %q", page.Code, page.Header().Get("Location"), tc.location)
			}
		})
	}
}

func serverMigrationPOSTWithoutCSRF(target string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	return req
}

func TestServerMigrationMutationRoutesRequireCSRF(t *testing.T) {
	t.Run("export review", func(t *testing.T) {
		client := &fakeServerMigrationExportAPI{}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		page := httptestResponse(app, serverMigrationPOSTWithoutCSRF("http://example/settings/server-migration/export/review"))
		if page.Code != http.StatusForbidden || client.planCalls != 0 {
			t.Fatalf("Export CSRF response = %d, plan calls = %d", page.Code, client.planCalls)
		}
	})

	t.Run("import review", func(t *testing.T) {
		client := &fakeServerMigrationImportAPI{}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		page := httptestResponse(app, serverMigrationPOSTWithoutCSRF("http://example/settings/server-migration/import/review"))
		if page.Code != http.StatusForbidden || client.planCalls != 0 {
			t.Fatalf("Import CSRF response = %d, plan calls = %d", page.Code, client.planCalls)
		}
	})

	t.Run("recovery review", func(t *testing.T) {
		client := &fakeServerMigrationRecoveryAPI{}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		page := httptestResponse(app, serverMigrationPOSTWithoutCSRF("http://example/settings/server-migration/recovery/review"))
		if page.Code != http.StatusForbidden || client.planCalls != 0 {
			t.Fatalf("Recovery CSRF response = %d, plan calls = %d", page.Code, client.planCalls)
		}
	})
}

func TestServerImportRequiredConfirmationsBlockApply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		want  string
	}{
		{name: "players", field: "players_confirmed", want: "online players may be interrupted"},
		{name: "eula", field: "eula_accepted", want: "Accept the Minecraft EULA"},
		{name: "vanilla conversion", field: "vanilla_confirmed", want: "Vanilla-to-Paper conversion"},
		{name: "external plugins", field: "plugins_confirmed", want: "external plugin JARs"},
		{name: "online mode identity", field: "online_mode_confirmed", want: "online-mode=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := successfulImportPlan("replace")
			plan.Requirements.EULAAcceptanceRequired = true
			plan.Requirements.VanillaConfirmationRequired = true
			client := &fakeServerMigrationImportAPI{
				fakeServerMigrationAPI: fakeServerMigrationAPI{
					exportDiscovery: importMediaFixture(),
					importDiscovery: importDiscoveryFixture(true),
				},
				plan: plan,
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}

			values := configuredImportRequestValues("local")
			values.Set("plan_fingerprint", serverMigrationFingerprint)
			values.Set("import_confirmation", "IMPORT")
			values.Set("players_confirmed", "yes")
			values.Set("eula_accepted", "yes")
			values.Set("vanilla_confirmed", "yes")
			values.Set("plugins_confirmed", "yes")
			values.Set("online_mode_confirmed", "yes")
			values.Del(tc.field)

			page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/apply", values))
			if page.Code != http.StatusBadRequest || !strings.Contains(page.Body.String(), tc.want) {
				t.Fatalf("%s confirmation response = %d: %s", tc.name, page.Code, page.Body.String())
			}
			if client.applyCalls != 0 {
				t.Fatalf("%s confirmation omission reached Agent Apply: %d", tc.name, client.applyCalls)
			}
		})
	}
}

func TestServerImportRejectsStaleReviewedPlanBeforeApply(t *testing.T) {
	client := &fakeServerMigrationImportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			exportDiscovery: importMediaFixture(),
			importDiscovery: importDiscoveryFixture(true),
		},
		plan: successfulImportPlan("replace"),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := configuredImportRequestValues("local")
	values.Set("plan_fingerprint", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/apply", values))
	if page.Code != http.StatusConflict || !strings.Contains(page.Body.String(), "changed since it was reviewed") {
		t.Fatalf("stale Import response = %d: %s", page.Code, page.Body.String())
	}
	if client.applyCalls != 0 {
		t.Fatalf("stale Import reached Agent Apply: %d", client.applyCalls)
	}
}

func TestServerMigrationProgressPresentsTerminalStates(t *testing.T) {
	for _, tc := range []struct {
		name          string
		operationType string
		state         string
		stage         string
		status        string
		wants         []string
	}{
		{
			name: "success", operationType: "migration_export", state: "succeeded", stage: "completed",
			status: "Migration export completed successfully.",
			wants:  []string{"Succeeded", "completed successfully"},
		},
		{
			name: "rolled back", operationType: "migration_export", state: "rolled_back", stage: "export_rolled_back",
			status: "Migration export rolled back.",
			wants:  []string{"Rolled back", "rolled back safely"},
		},
		{
			name: "needs attention", operationType: "migration_import", state: "needs_attention", stage: "import_needs_attention",
			status: "Retained Import recovery state requires review.",
			wants:  []string{"Needs attention", "needs administrator attention", "Review Migration Recovery"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation := &api.PersistentOperation{
				SchemaVersion:   "v1",
				OperationID:     serverMigrationOperationID,
				OperationType:   tc.operationType,
				PlanFingerprint: serverMigrationFingerprint,
				State:           tc.state,
				Stage:           tc.stage,
				Status:          tc.status,
				StartedAt:       "x",
				UpdatedAt:       "x",
				Rollback:        api.PersistentOperationRollback{State: "not_started"},
			}
			client := &fakeServerMigrationAPI{operation: operation}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server-migration/progress/"+serverMigrationOperationID, ""))
			if page.Code != http.StatusOK {
				t.Fatalf("%s progress returned %d: %s", tc.name, page.Code, page.Body.String())
			}
			for _, want := range tc.wants {
				if !strings.Contains(page.Body.String(), want) {
					t.Fatalf("%s progress missing %q: %s", tc.name, want, page.Body.String())
				}
			}
		})
	}
}

func TestFreshUnconfiguredMigrationRecoveryReviewAndApply(t *testing.T) {
	transaction := "/var/lib/justvoxel/.justvoxel-import-fresh"
	discovery := api.AdminMigrationRecoveryDiscoveryResponse{
		OK: true, SchemaVersion: "v1", Configured: false,
		Transactions: []api.AdminMigrationRecoverySummary{{
			Transaction: transaction, Phase: "rolled-back-fresh", SourceType: "paper",
			UpdatedAt: "2026-09-21T16:00:00Z", Mode: "fresh-unconfigured", Finalizable: true,
		}},
	}
	client := &fakeServerMigrationRecoveryAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{recovery: discovery},
		plan:                   recoveryPlanFixture(transaction, "rolled-back-fresh", "fresh-unconfigured"),
	}
	client.apply = api.AdminMigrationApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_recovery",
		PlanFingerprint: serverMigrationFingerprint, State: "queued", Stage: "queued",
		Status: "Server migration recovery operation queued.", StartedAt: "x", UpdatedAt: "x",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	reviewValues := url.Values{"csrf": {"csrf-token"}, "transaction": {transaction}}
	review := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/settings/server-migration/recovery/review", reviewValues))
	if review.Code != http.StatusOK {
		t.Fatalf("fresh Recovery review returned %d: %s", review.Code, review.Body.String())
	}
	for _, want := range []string{"Fresh appliance rollback", "Fresh Import rolled back", "fresh-Import runtime files remain absent"} {
		if !strings.Contains(review.Body.String(), want) {
			t.Fatalf("fresh Recovery review missing %q: %s", want, review.Body.String())
		}
	}

	applyValues := url.Values{
		"csrf": {"csrf-token"}, "transaction": {transaction},
		"plan_fingerprint": {serverMigrationFingerprint}, "finalize_confirmation": {"FINALIZE"},
	}
	apply := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/settings/server-migration/recovery/apply", applyValues))
	if apply.Code != http.StatusSeeOther || apply.Header().Get("Location") != "/settings/server-migration/progress/"+serverMigrationOperationID {
		t.Fatalf("fresh Recovery apply returned %d %q: %s", apply.Code, apply.Header().Get("Location"), apply.Body.String())
	}
	if client.applyCalls != 1 || !client.applyReq.FinalizeConfirmed || client.applyReq.Transaction != transaction {
		t.Fatalf("unexpected fresh Recovery Apply request: %#v calls=%d", client.applyReq, client.applyCalls)
	}
}
