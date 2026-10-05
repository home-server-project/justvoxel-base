package server

import (
	"net/http"
	"net/url"
	"strconv"
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
	for _, want := range []string{"Migration Recovery needs attention", "No retained recovery files remain", "Keep current server and continue", `data-migration-recovery-available="true"`, `action="/workspace/migration/recovery/resolve"`} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("recovery page missing %q: %d %s", want, page.Code, page.Body.String())
		}
	}

	if strings.Contains(page.Body.String(), "Back to Server Migration") || strings.Contains(page.Body.String(), `href="/?workspace=migration"`) {
		t.Fatal("unresolved Recovery exposes looping Server Migration navigation")
	}

	values := url.Values{"csrf": {"csrf-token"}, "operation_id": {current.OperationID}, "keep_current_state": {"yes"}}
	resolved := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/resolve", values))
	if resolved.Code != http.StatusSeeOther || resolved.Header().Get("Location") != "/workspace/migration" {
		t.Fatalf("resolve response = %d %q: %s", resolved.Code, resolved.Header().Get("Location"), resolved.Body.String())
	}
	if client.recoveryResolveCalls != 1 || client.resolveCalls != 0 || client.recoveryResolveReq.OperationID != current.OperationID || !client.recoveryResolveReq.KeepCurrentState {
		t.Fatalf("wrong resolve API: recovery=%d import=%d request=%#v", client.recoveryResolveCalls, client.resolveCalls, client.recoveryResolveReq)
	}

	// Model the Management API releasing current ownership after successful resolution.
	client.current = nil
	migrationRequest := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration", "")
	migrationRequest.Header.Set(migrationWorkspaceFragmentHeader, "1")
	migrationPage := httptestResponse(app, migrationRequest)
	for _, want := range []string{"Open Server Export", "Open Server Import", "Open Migration Recovery"} {
		if migrationPage.Code != http.StatusOK || !strings.Contains(migrationPage.Body.String(), want) {
			t.Fatalf("released operation still blocks Migration: %d %s", migrationPage.Code, migrationPage.Body.String())
		}
	}
	recoveryRequest := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/recovery", "")
	recoveryRequest.Header.Set(migrationWorkspaceFragmentHeader, "1")
	recoveryPage := httptestResponse(app, recoveryRequest)
	if recoveryPage.Code != http.StatusOK || !strings.Contains(recoveryPage.Body.String(), `href="/?workspace=migration">Back to Server Migration`) {
		t.Fatalf("resolved Recovery lost normal navigation: %d %s", recoveryPage.Code, recoveryPage.Body.String())
	}
	client.current = current

	client.recoveryResolveErr = &api.ResponseError{StatusCode: http.StatusConflict, Message: "Current appliance validation requires review."}
	rejected := httptestResponse(app, recoveryWebRequest(http.MethodPost, "http://example/workspace/migration/recovery/resolve", values))
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "Current appliance validation requires review.") || !strings.Contains(rejected.Body.String(), "Keep current server and continue") || strings.Contains(rejected.Body.String(), "Back to Server Migration") {
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

func TestMigrationWorkspaceRecoveryAttentionNavigation(t *testing.T) {
	for _, operationType := range []string{"migration_import", "migration_recovery"} {
		for _, retained := range []bool{false, true} {
			t.Run(operationType+"/retained="+strconv.FormatBool(retained), func(t *testing.T) {
				discovery := api.AdminMigrationRecoveryDiscoveryResponse{OK: true, SchemaVersion: "v1", Configured: true}
				if retained {
					discovery = recoveryDiscoveryFixture()
				}
				client := &fakeServerMigrationRecoveryAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{
					current:  &api.PersistentOperation{OperationID: serverMigrationOperationID, OperationType: operationType, State: "needs_attention"},
					recovery: discovery,
				}}
				app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
				if err != nil {
					t.Fatal(err)
				}
				request := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/recovery", "")
				request.Header.Set(migrationWorkspaceFragmentHeader, "1")
				page := httptestResponse(app, request)
				body := page.Body.String()
				if page.Code != http.StatusOK || strings.Contains(body, "Back to Server Migration") || strings.Contains(body, `href="/?workspace=migration"`) {
					t.Fatalf("unresolved Recovery navigation loops: %d %s", page.Code, body)
				}
				attentionNotice := "Server Import needs attention."
				if operationType == "migration_recovery" {
					attentionNotice = "Migration Recovery needs attention."
				}
				if !strings.Contains(body, attentionNotice) {
					t.Fatalf("current operation attention notice missing: %s", body)
				}
				if retained {
					if !strings.Contains(body, `action="/workspace/migration/recovery/review"`) || !strings.Contains(body, "Clean Up Recovery Files") {
						t.Fatalf("Recovery cleanup action missing: %s", body)
					}
				} else if !strings.Contains(body, `action="/workspace/migration/recovery/resolve"`) || !strings.Contains(body, "Keep current server and continue") {
					t.Fatalf("Recovery resolve action missing: %s", body)
				}
				if !retained && !strings.Contains(body, `name="operation_id" value="`+serverMigrationOperationID+`"`) {
					t.Fatalf("Recovery resolve lost current operation ID: %s", body)
				}

				// Released ownership restores normal navigation even with retained files.
				client.current = nil
				releasedRequest := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/recovery", "")
				releasedRequest.Header.Set(migrationWorkspaceFragmentHeader, "1")
				releasedPage := httptestResponse(app, releasedRequest)
				releasedBody := releasedPage.Body.String()
				if releasedPage.Code != http.StatusOK || !strings.Contains(releasedBody, `href="/?workspace=migration">Back to Server Migration`) {
					t.Fatalf("released operation lost normal navigation: %d %s", releasedPage.Code, releasedBody)
				}
				if strings.Contains(releasedBody, "Server Import needs attention.") || strings.Contains(releasedBody, "Migration Recovery needs attention.") || strings.Contains(releasedBody, `action="/workspace/migration/recovery/resolve"`) {
					t.Fatalf("released operation still requires attention: %s", releasedBody)
				}
			})
		}
	}
}
