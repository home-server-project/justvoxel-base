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
			if rr.Code != 200 || client.reads != 1 || client.ensures != 0 || client.saves != 0 {
				t.Fatalf("read side effects or denied: %d", rr.Code)
			}
			for _, action := range []string{"ensure", "manual"} {
				rr = httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "/api/minecraft/workspace/identity/"+action, "csrf=csrf-token&suffix=ABC123"))
				want := 403
				if role == "administrator" {
					want = 200
				}
				if rr.Code != want {
					t.Fatalf("%s %s: %d %s", role, action, rr.Code, rr.Body.String())
				}
			}
			if role == "administrator" {
				if client.ensures != 1 || client.saves != 1 || client.suffix != "ABC123" {
					t.Fatal("incorrect proxy request")
				}
			} else if client.ensures != 0 || client.saves != 0 {
				t.Fatal("role bypass")
			}
			before := client.ensures + client.saves
			for _, action := range []string{"ensure", "manual"} {
				rr = httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "/api/minecraft/workspace/identity/"+action, "csrf=wrong&suffix=ABC123"))
				if rr.Code != 403 || client.ensures+client.saves != before {
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
	if rr.Code != 502 {
		t.Fatalf("identity failure=%d", rr.Code)
	}
	for _, route := range []string{"/api/minecraft/workspace/settings", "/api/minecraft/workspace/logs", "/api/dashboard-status", "/"} {
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, route, ""))
		if rr.Code != 200 {
			t.Fatalf("identity failure broke %s: %d %s", route, rr.Code, rr.Body.String())
		}
	}
	if client.reads != 2 || client.ensures != 1 || client.saves != 0 {
		t.Fatal("administrator dashboard did not attempt legacy registration once")
	}
	for _, failure := range []struct {
		status  int
		message string
	}{{400, "Use 6-12 letters and numbers after jv-."}, {409, "Instance ID is already used. Choose another value."}} {
		client.identityError = &api.ResponseError{StatusCode: failure.status, Message: failure.message}
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "/api/minecraft/workspace/identity/manual", "csrf=csrf-token&suffix=bad"))
		if rr.Code != failure.status || !strings.Contains(rr.Body.String(), failure.message) {
			t.Fatalf("lost validation error: %d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestInstalledServerIdentityAttemptOncePerAdministratorSession(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			client := &fakeMinecraftInstanceAPI{fakeMinecraftWorkspaceAPI: configuredMinecraftWorkspaceAPI(), identityError: errors.New("generation unavailable")}
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
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "/", ""))
				if rr.Code != 200 {
					t.Fatalf("dashboard: %d", rr.Code)
				}
				required := strings.Contains(rr.Body.String(), `data-instance-required="true"`)
				if required != (role == "administrator") {
					t.Fatal("manual popup not aligned with administrator role")
				}
			}
			want := 0
			if role == "administrator" {
				want = 1
			}
			if client.ensures != want {
				t.Fatalf("automatic attempts=%d want=%d", client.ensures, want)
			}
			client.identityError = nil
			client.identity = api.MinecraftIdentity{ID: "jv-local123", Status: "available"}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "/", ""))
			if strings.Contains(rr.Body.String(), `data-instance-required="true"`) || client.ensures != want {
				t.Fatal("existing valid identity prompted or was replaced")
			}
		})
	}
}

func TestInstalledServerAutomaticIdentitySuccessIsSilent(t *testing.T) {
	client := &fakeMinecraftInstanceAPI{fakeMinecraftWorkspaceAPI: configuredMinecraftWorkspaceAPI(), identity: api.MinecraftIdentity{Status: "unavailable"}}
	client.statusResult = defaultStatus()
	client.statusResult.Minecraft.Configured = true
	client.playersResult = defaultPlayers()
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "/", ""))
		if rr.Code != 200 || strings.Contains(rr.Body.String(), `data-instance-required="true"`) {
			t.Fatal("automatic success required interaction")
		}
	}
	if client.ensures != 1 || client.identity.ID != "jv-auto123" {
		t.Fatal("automatic registration was repeated or did not persist")
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
