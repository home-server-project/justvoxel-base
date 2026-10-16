package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeMinecraftInstanceAPI struct {
	*fakeMinecraftWorkspaceAPI
	identity              api.MinecraftIdentity
	identityError         error
	reads, ensures, saves int
	suffix                string
}

func (f *fakeMinecraftInstanceAPI) MinecraftIdentity(context.Context, string) (api.MinecraftIdentity, error) {
	f.reads++
	return f.identity, f.identityError
}
func (f *fakeMinecraftInstanceAPI) EnsureMinecraftIdentity(context.Context, string) (api.MinecraftIdentity, error) {
	f.ensures++
	if f.identityError == nil && f.identity.Status != "available" {
		f.identity = api.MinecraftIdentity{Status: "available", ID: "jv-auto123"}
	}
	return f.identity, f.identityError
}
func (f *fakeMinecraftInstanceAPI) SaveMinecraftIdentity(_ context.Context, _, suffix string) (api.MinecraftIdentity, error) {
	f.saves++
	f.suffix = suffix
	return f.identity, f.identityError
}

func TestMinecraftInstanceProxyPermissionsAndCSRF(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			client := &fakeMinecraftInstanceAPI{fakeMinecraftWorkspaceAPI: configuredMinecraftWorkspaceAPI(), identity: api.MinecraftIdentity{Status: "available", ID: "jv-abc123"}}
			client.role = role
			app, err := New(client, Config{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "/api/minecraft/workspace/identity", ""))
			if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"id":"jv-family1"`) || client.reads != 0 || client.ensures != 0 || client.saves != 0 || len(client.instanceWrites) != 0 {
				t.Fatalf("read side effects or denied: %d", rr.Code)
			}
			for _, action := range []string{"ensure", "manual"} {
				rr = httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "/api/minecraft/workspace/identity/"+action, "csrf=csrf-token&suffix=ABC123"))
				want := 403
				if role == "administrator" {
					want = http.StatusConflict
				}
				if rr.Code != want {
					t.Fatalf("%s %s: %d %s", role, action, rr.Code, rr.Body.String())
				}
			}
			if client.reads != 0 || client.ensures != 0 || client.saves != 0 || len(client.instanceWrites) != 0 {
				t.Fatal("immutable identity route accessed fixed identity or mutated an instance")
			}
			before := client.ensures + client.saves
			for _, action := range []string{"ensure", "manual"} {
				rr = httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "/api/minecraft/workspace/identity/"+action, "csrf=wrong&suffix=ABC123"))
				if rr.Code != 403 || client.ensures+client.saves != before || client.reads != 0 || len(client.instanceWrites) != 0 {
					t.Fatal("CSRF bypass")
				}
			}
		})
	}
}

func TestMinecraftInstanceFailureLeavesWorkspaceAndDashboardUsable(t *testing.T) {
	client := &fakeMinecraftInstanceAPI{fakeMinecraftWorkspaceAPI: configuredMinecraftWorkspaceAPI(), identityError: errors.New("identity socket operation failed")}
	client.statusResult = defaultStatus()
	client.playersResult = defaultPlayers()
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "/api/minecraft/workspace/identity", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"id":"jv-family1"`) {
		t.Fatalf("registered identity unavailable: %d %s", rr.Code, rr.Body.String())
	}
	for _, route := range []string{"/api/minecraft/workspace/settings", "/api/minecraft/workspace/logs", "/api/dashboard-status", "/"} {
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, route, ""))
		if rr.Code != 200 {
			t.Fatalf("identity failure broke %s: %d %s", route, rr.Code, rr.Body.String())
		}
	}
	if client.reads != 0 || client.ensures != 0 || client.saves != 0 || len(client.instanceWrites) != 0 {
		t.Fatal("read-only workspace or dashboard accessed fixed identity or mutated an instance")
	}
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "/api/minecraft/workspace/selection", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"selected":{"id":"jv-family1"`) || strings.Contains(rr.Body.String(), "jv-auto123") {
		t.Fatalf("registered instance is not authoritative: %d %s", rr.Code, rr.Body.String())
	}
}

func TestDashboardNeverSynthesizesFixedServerIdentity(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		client := &fakeMinecraftInstanceAPI{fakeMinecraftWorkspaceAPI: configuredMinecraftWorkspaceAPI(), identityError: errors.New("unavailable")}
		client.role = role
		client.statusResult = defaultStatus()
		client.statusResult.Minecraft.Configured = true
		client.playersResult = defaultPlayers()
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest("GET", "/", ""))
			if rr.Code != 200 || strings.Contains(rr.Body.String(), `data-instance-required="true"`) {
				t.Fatal(rr.Code, rr.Body.String())
			}
		}
		if client.ensures != 0 || client.reads != 0 || client.saves != 0 {
			t.Fatal("dashboard accessed fixed identity")
		}
	}
}

func TestDashboardKeepsZeroServerIdentityAbsent(t *testing.T) {
	client := &fakeMinecraftInstanceAPI{fakeMinecraftWorkspaceAPI: configuredMinecraftWorkspaceAPI(), identity: api.MinecraftIdentity{Status: "unavailable"}}
	client.selectedIDs = nil
	client.statusResult = defaultStatus()
	client.playersResult = defaultPlayers()
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest("GET", "/", ""))
	if rr.Code != 200 || client.ensures != 0 || client.identity.ID != "" {
		t.Fatal("zero-server dashboard recreated fixed identity", rr.Code)
	}
}

func TestReviewedSetupIdentityRequiredResponsePreservesSubmission(t *testing.T) {
	client := setupExecutionClient()
	client.plan.Requirements.SMBPasswordRequired = true
	client.applyResponse = api.AdminSetupApplyResponse{Code: "identity_required", Error: "Instance ID could not be generated."}
	client.applyErr = &api.ResponseError{StatusCode: http.StatusConflict, Message: client.applyResponse.Error}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	acceptSetupEULA(t, app)
	values := url.Values{"csrf": {"csrf-token"}, "plan_fingerprint": {setupReviewFingerprint}, "smb_password": {"reviewed-secret"}}
	request := authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/apply", values.Encode())
	request.Header.Set("Accept", "application/json")
	response := httptestResponse(app, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"identity_required"`) || strings.Contains(response.Body.String(), "reviewed-secret") {
		t.Fatalf("identity response: %d %s", response.Code, response.Body.String())
	}
	first := client.applyRequest
	client.applyErr = nil
	client.applyResponse = api.AdminSetupApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{OperationID: setupExecutionOperationID}}
	response = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/apply", values.Encode()))
	if response.Code != http.StatusSeeOther || client.applyCalls != 2 || client.applyRequest != first {
		t.Fatal("same reviewed setup was not retained for manual resumption")
	}
}

func TestReviewedImportIdentityRequiredResponsePreservesSubmission(t *testing.T) {
	client := migrationSafetyImportClient(successfulImportPlan("replace"))
	client.apply = api.AdminMigrationApplyResponse{Code: "identity_required", Error: "Instance ID could not be generated."}
	client.applyErr = &api.ResponseError{StatusCode: http.StatusConflict, Message: client.apply.Error}
	app := migrationSafetyApp(t, client)
	values := migrationSafetyImportValues("local")
	request := importWebRequest(http.MethodPost, "http://example/workspace/migration/import/apply", values)
	request.Header.Set("Accept", "application/json")
	response := httptestResponse(app, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"identity_required"`) {
		t.Fatalf("identity response: %d %s", response.Code, response.Body.String())
	}
	first := client.applyReq
	client.applyErr = nil
	client.apply = migrationSafetyOperation("migration_import")
	response = httptestResponse(app, importWebRequest(http.MethodPost, "http://example/workspace/migration/import/apply", values))
	migrationSafetyStarted(t, response)
	if client.applyCalls != 2 || client.applyReq != first {
		t.Fatal("same reviewed Import was not retained for manual resumption")
	}
}
