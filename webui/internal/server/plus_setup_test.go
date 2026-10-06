package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type plusSetupTestAPI struct {
	plusTestAPI
	prepareCalls int
	deployCalls  int
	request      api.PlusSetupRequest
}

func (f *plusSetupTestAPI) PlusSetupState(context.Context, string) (api.PlusSetupState, error) {
	return api.PlusSetupState{Mounts: []api.PlusSetupMount{{Target: "/mnt/data", FSType: "xfs"}}}, nil
}
func (f *plusSetupTestAPI) PlusSetupPrepare(_ context.Context, _ string, request api.PlusSetupRequest) (api.PlusSetupResponse, error) {
	f.prepareCalls++
	f.request = request
	return api.PlusSetupResponse{OK: true, Prepared: true, Message: "Saved"}, nil
}
func plusSetupWebRequest(method, path, body string, csrf bool) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if csrf {
		r.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	}
	return r
}
func TestPlusSetupWebRoleCSRFAndSecretBoundary(t *testing.T) {
	payload := `{"components":{"panel":true,"drydock":true},"host":"panel.example.com","email":"admin@example.com","password":"secret-once-only"}`
	body := url.Values{"csrf": {"token"}, "setup": {payload}}.Encode()
	for _, role := range []string{"administrator", "operator", "viewer"} {
		client := &plusSetupTestAPI{plusTestAPI: plusTestAPI{role: role}}
		app, err := New(client, Config{Plus: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/plus/setup/state", "/api/plus/setup/prepare"} {
			method := http.MethodGet
			if strings.HasSuffix(path, "prepare") {
				method = http.MethodPost
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, plusSetupWebRequest(method, path, body, true))
			want := http.StatusForbidden
			if role == "administrator" {
				want = http.StatusOK
			}
			if rr.Code != want {
				t.Fatalf("%s %s: %d %s", role, path, rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "secret-once-only") {
				t.Fatal("password returned")
			}
		}
		if role != "administrator" && client.prepareCalls != 0 {
			t.Fatal("unauthorized preparation")
		}
		if role == "administrator" {
			if client.prepareCalls != 1 || client.request.Password != "secret-once-only" {
				t.Fatal("password was not forwarded through the agent API")
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, plusSetupWebRequest(http.MethodPost, "/api/plus/setup/prepare", body, false))
			if rr.Code != http.StatusForbidden || client.prepareCalls != 1 {
				t.Fatal("CSRF boundary bypassed")
			}
		}
	}
}
func TestPlusSetupWebStrictPayloadAndWizard(t *testing.T) {
	client := &plusSetupTestAPI{plusTestAPI: plusTestAPI{role: "administrator"}}
	app, err := New(client, Config{Plus: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"unknown":true}`, `{} {}`, `not-json`} {
		body := url.Values{"csrf": {"token"}, "setup": {payload}}.Encode()
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, plusSetupWebRequest(http.MethodPost, "/api/plus/setup/prepare", body, true))
		if rr.Code != http.StatusBadRequest || client.prepareCalls != 0 {
			t.Fatal("invalid setup accepted")
		}
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, plusSetupWebRequest(http.MethodGet, "/setup", "", true))
	if rr.Code != http.StatusOK {
		t.Fatalf("setup page: %d %s", rr.Code, rr.Body.String())
	}
	for _, text := range []string{`data-setup-step="0"`, `data-setup-step="4"`, `role="switch"`, `data-username="admin"`, `Look around`, `These are separate accounts.`, `/static/plus-setup.js`} {
		if !strings.Contains(rr.Body.String(), text) {
			t.Fatalf("wizard missing %s", text)
		}
	}
	if strings.Contains(rr.Body.String(), "Recommended setup") || strings.Contains(rr.Body.String(), "Advanced setup") {
		t.Fatal("wizard split into separate modes")
	}
}

func (f *plusSetupTestAPI) PlusSetupDeploy(context.Context, string) (api.PlusDeploymentResponse, error) {
	f.deployCalls++
	return api.PlusDeploymentResponse{OK: true, Deployment: api.PlusDeploymentStatus{State: "running"}}, nil
}
func (f *plusSetupTestAPI) PlusApplications(context.Context, string) (api.PlusApplications, error) {
	return api.PlusApplications{Configured: true, PanelURL: "https://panel.example.com:8443", DrydockURL: "https://panel.example.com:3000"}, nil
}
func TestPlusDeploymentCSRFAndLaunchers(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		client := &plusSetupTestAPI{plusTestAPI: plusTestAPI{role: role}}
		app, err := New(client, Config{Plus: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, csrf := range []bool{false, true} {
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, plusSetupWebRequest(http.MethodPost, "/api/plus/setup/deploy", "csrf=token", csrf))
			want := http.StatusForbidden
			if csrf && role == "administrator" {
				want = http.StatusAccepted
			}
			if rr.Code != want {
				t.Fatalf("deployment %s csrf=%t: %d", role, csrf, rr.Code)
			}
		}
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, plusSetupWebRequest(http.MethodGet, "/applications/pterodactyl", "", false))
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "https://panel.example.com:8443" {
			t.Fatal("Panel launcher not available to authenticated host role")
		}
		if strings.Contains(rr.Header().Get("Location"), "session") {
			t.Fatal("host session forwarded to external application")
		}
	}
}
