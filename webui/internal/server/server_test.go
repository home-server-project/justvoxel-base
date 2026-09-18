package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeAPI struct {
	authenticated bool
	mustChange    bool
	statusResult  api.Status
	playersResult api.Players
	playersErr    error
	actionResult  api.MinecraftActionResponse
	actionErr     error
	action        string
	confirmed     bool
}

func (f *fakeAPI) Login(_ context.Context, username, password string) (api.LoginResponse, error) {
	if username == "admin" && password == "secret" {
		f.authenticated = true
		return api.LoginResponse{Session: "session-token", ManagementAPI: "v1", MustChange: f.mustChange}, nil
	}
	return api.LoginResponse{}, api.ErrUnauthorized
}

func (f *fakeAPI) Logout(_ context.Context, _ string) error { f.authenticated = false; return nil }

func (f *fakeAPI) ChangePassword(_ context.Context, session, currentPassword, newPassword string) error {
	if session != "session-token" || currentPassword != "secret" || len(newPassword) < 12 {
		return api.ErrUnauthorized
	}
	f.mustChange = false
	return nil
}

func defaultStatus() api.Status {
	var s api.Status
	s.System.Variant = "VM"
	s.System.Health = "Healthy"
	s.System.IPv4 = "192.0.2.10"
	s.Minecraft.Configured = true
	s.Minecraft.State = "Running"
	s.Minecraft.Players = 2
	s.Minecraft.MaxPlayers = 10
	s.Minecraft.Version = "1.21.8"
	s.Backup.Enabled = true
	s.JustVoxel.Version = "10-test"
	return s
}

func defaultPlayers() api.Players {
	return api.Players{Configured: true, State: "running", Online: 2, Max: 10, Names: []string{"Alex", "Steve"}}
}

func (f *fakeAPI) Status(_ context.Context, session string) (api.Status, error) {
	if session != "session-token" {
		return api.Status{}, api.ErrUnauthorized
	}
	if f.statusResult.Minecraft.State != "" {
		return f.statusResult, nil
	}
	return defaultStatus(), nil
}

func (f *fakeAPI) Players(_ context.Context, session string) (api.Players, error) {
	if session != "session-token" {
		return api.Players{}, api.ErrUnauthorized
	}
	if f.playersErr != nil {
		return api.Players{}, f.playersErr
	}
	if f.playersResult.State != "" {
		return f.playersResult, nil
	}
	return defaultPlayers(), nil
}

func (f *fakeAPI) MinecraftAction(_ context.Context, session, action string, confirmPlayers bool) (api.MinecraftActionResponse, error) {
	if session != "session-token" {
		return api.MinecraftActionResponse{}, api.ErrUnauthorized
	}
	f.action = action
	f.confirmed = confirmPlayers
	if f.actionResult.Action != "" || f.actionResult.ConfirmationRequired || f.actionResult.Message != "" {
		return f.actionResult, f.actionErr
	}
	return api.MinecraftActionResponse{OK: true, Action: action}, f.actionErr
}

func TestUnauthenticatedDashboardRedirects(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "1.0.0", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Fatalf("unexpected redirect %q", rr.Header().Get("Location"))
	}
}

func TestLoginSetsLocalHTTPSession(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	form := strings.NewReader("username=admin&password=secret")
	req := httptest.NewRequest(http.MethodPost, "http://example/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rr.Code)
	}
	found := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie {
			found = true
			if c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("local HTTP session cookie has unexpected security attributes: %#v", c)
			}
		}
	}
	if !found {
		t.Fatal("session cookie not set")
	}
}

func TestCSRFAcceptsSameHTTPOrigin(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/logout", strings.NewReader("csrf=token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://example")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	if !app.validCSRF(req) {
		t.Fatal("same-origin HTTP CSRF token was rejected")
	}
}

func TestCSRFAcceptsNullOriginWithValidToken(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/logout", strings.NewReader("csrf=token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	if !app.validCSRF(req) {
		t.Fatal("null Origin with valid CSRF token was rejected")
	}
}

func TestCSRFRejectsForeignOrigin(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"https://example", "http://other.example"} {
		req := httptest.NewRequest(http.MethodPost, "http://example/logout", strings.NewReader("csrf=token"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", origin)
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
		if app.validCSRF(req) {
			t.Fatalf("foreign Origin %q was accepted", origin)
		}
	}
}

func TestCSRFRejectsMissingOrWrongToken(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		form        string
		cookieValue string
	}{
		{name: "missing form token", form: "", cookieValue: "token"},
		{name: "wrong form token", form: "csrf=wrong", cookieValue: "token"},
		{name: "missing cookie", form: "csrf=token", cookieValue: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://example/logout", strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", "null")
			if tc.cookieValue != "" {
				req.AddCookie(&http.Cookie{Name: csrfCookie, Value: tc.cookieValue})
			}
			if app.validCSRF(req) {
				t.Fatal("invalid CSRF request was accepted")
			}
		})
	}
}

func TestFirstLoginRequiresPasswordChange(t *testing.T) {
	app, err := New(&fakeAPI{mustChange: true}, Config{Version: "1.0.0", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	form := strings.NewReader("username=admin&password=secret")
	req := httptest.NewRequest(http.MethodPost, "http://example/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/password" {
		t.Fatalf("expected password redirect, got %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestDashboardRendersStatusPlayersAndControls(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "1.0.0", Commit: "abc123", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Running", "2 / 10", "1.21.8", "Alex", "Steve", "10-test", "1.0.0", "/minecraft/start", "/minecraft/stop", "/minecraft/restart", `data-dashboard-status="/api/dashboard-status"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in response", want)
		}
	}
}

func TestDashboardStatusRequiresAuthentication(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/api/dashboard-status", nil)
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestDashboardStatusReturnsLiveJSON(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/api/dashboard-status", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("unexpected content type %q", got)
	}
	body := rr.Body.String()
	for _, want := range []string{`"state":"Running"`, `"online":2`, `"names":["Alex","Steve"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in status response: %s", want, body)
		}
	}
}

func TestDashboardResultMarksPendingAction(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/?result=restart", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-pending-action="restart"`) {
		t.Fatalf("missing pending restart state: %s", body)
	}
	if !strings.Contains(body, "Minecraft restart requested.") {
		t.Fatalf("missing restart request message: %s", body)
	}
}

func TestDashboardShowsNotConfiguredExplicitly(t *testing.T) {
	var status api.Status
	status.System.Variant = "VM"
	status.System.Health = "Healthy"
	status.Minecraft.State = "Not configured"
	status.Minecraft.Version = "Not configured"
	players := api.Players{State: "not_configured"}
	app, err := New(&fakeAPI{statusResult: status, playersResult: players}, Config{Version: "1.0.0", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Count(body, "Not configured") < 2 {
		t.Fatalf("expected explicit not-configured states, got: %s", body)
	}
	if !strings.Contains(body, "mjust setup") {
		t.Fatal("missing setup guidance for unconfigured appliance")
	}
}

func TestMinecraftActionRequiresCSRF(t *testing.T) {
	fake := &fakeAPI{}
	app, err := New(fake, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/minecraft/restart", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if fake.action != "" {
		t.Fatal("Minecraft action reached API without CSRF")
	}
}

func TestMinecraftRestartRendersPlayerConfirmation(t *testing.T) {
	fake := &fakeAPI{actionResult: api.MinecraftActionResponse{
		Action: "restart", ConfirmationRequired: true, Reason: "players_online", Online: 2, Players: []string{"Alex", "Steve"},
	}}
	app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := actionRequest("http://example/minecraft/restart", "csrf=token")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Confirmation required", "Restart anyway", "Alex", "Steve", `name="confirm_players" value="yes"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in confirmation page", want)
		}
	}
}

func TestMinecraftConfirmedRestartRedirectsAfterSuccess(t *testing.T) {
	fake := &fakeAPI{actionResult: api.MinecraftActionResponse{OK: true, Action: "restart", Message: "Minecraft restart requested."}}
	app, err := New(fake, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := actionRequest("http://example/minecraft/restart", "csrf=token&confirm_players=yes")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/?result=restart" {
		t.Fatalf("unexpected result %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if fake.action != "restart" || !fake.confirmed {
		t.Fatalf("confirmation not forwarded correctly: action=%q confirmed=%v", fake.action, fake.confirmed)
	}
}

func TestMinecraftActionErrorRendersFriendlyMessage(t *testing.T) {
	fake := &fakeAPI{
		actionResult: api.MinecraftActionResponse{Action: "stop", Message: "Could not confirm player status through RCON. Minecraft was not interrupted."},
		actionErr:    errors.New("rejected"),
	}
	app, err := New(fake, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := actionRequest("http://example/minecraft/stop", "csrf=token")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Minecraft was not interrupted") {
		t.Fatalf("friendly failure message missing: %s", rr.Body.String())
	}
}

func actionRequest(target, form string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	return req
}
