package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type authFlowAPI struct {
	*fakeAPI
	authStatus  api.AuthStatus
	change      api.AuthModeChange
	changeResp  api.AuthModeChangeResponse
	changeErr   error
	passwordErr error
}

func newAuthFlowAPI() *authFlowAPI {
	return &authFlowAPI{
		fakeAPI: &fakeAPI{},
		authStatus: api.AuthStatus{
			Mode:               "system",
			Username:           "voxel",
			MinimumPasswordLen: 8,
		},
	}
}

func (f *authFlowAPI) AuthStatus(_ context.Context, session string) (api.AuthStatus, error) {
	if session != "session-token" {
		return api.AuthStatus{}, api.ErrUnauthorized
	}
	return f.authStatus, nil
}

func (f *authFlowAPI) ChangeAuthMode(_ context.Context, session string, change api.AuthModeChange) (api.AuthModeChangeResponse, error) {
	if session != "session-token" {
		return api.AuthModeChangeResponse{}, api.ErrUnauthorized
	}
	f.change = change
	if f.changeErr != nil {
		return api.AuthModeChangeResponse{}, f.changeErr
	}
	if f.changeResp.AuthMode == "" {
		return api.AuthModeChangeResponse{OK: true, AuthMode: change.Mode, Reauthenticate: true}, nil
	}
	return f.changeResp, nil
}

func (f *authFlowAPI) ChangePassword(ctx context.Context, session, currentPassword, newPassword string) error {
	if f.passwordErr != nil {
		return f.passwordErr
	}
	return f.fakeAPI.ChangePassword(ctx, session, currentPassword, newPassword)
}

func TestLoginPageUsesVoxelAdministrator(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/login", nil)
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `value="voxel"`) || strings.Contains(body, `value="admin"`) {
		t.Fatalf("login page does not expose voxel administrator: %s", body)
	}
}

func TestPasswordPageShowsPlainLanguageRequirements(t *testing.T) {
	fake := newAuthFlowAPI()
	fake.authStatus.MinimumPasswordLen = 8
	app, err := New(fake, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/password", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Use at least 8 characters") || !strings.Contains(body, `minlength="8"`) {
		t.Fatalf("platform password minimum missing: %s", body)
	}
	for _, requirement := range []string{
		"mix letters, numbers, and symbols",
		"simple, repeated, or predictable patterns",
		"too similar to your current password",
		"JustVoxel will show the reason",
	} {
		if !strings.Contains(body, requirement) {
			t.Fatalf("plain-language password requirement %q missing: %s", requirement, body)
		}
	}
	if !strings.Contains(body, "local console") || !strings.Contains(body, "SSH password login") {
		t.Fatalf("system-account explanation missing: %s", body)
	}
}

func TestAuthenticationSettingsShowsSystemMode(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/settings/authentication", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "System account") || !strings.Contains(body, "Separate WebUI password") {
		t.Fatalf("authentication mode choices missing: %s", body)
	}
	if !strings.Contains(body, "Password requirements:") || !strings.Contains(body, "mix letters, numbers, and symbols") {
		t.Fatalf("plain-language password requirements missing: %s", body)
	}
}

func TestSystemToSeparateModeRequiresCSRFAndReauthentication(t *testing.T) {
	fake := newAuthFlowAPI()
	app, err := New(fake, Config{Version: "test", ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}

	form := "csrf=token&mode=separate&system_password=system-secret&new_web_password=web-secret&confirm_web_password=web-secret"
	req := httptest.NewRequest(http.MethodPost, "http://example/settings/authentication", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login?message=auth-mode-changed" {
		t.Fatalf("unexpected result %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	if fake.change.Mode != "separate" || fake.change.SystemPassword != "system-secret" || fake.change.NewWebPassword != "web-secret" {
		t.Fatalf("mode-change request not forwarded correctly: %#v", fake.change)
	}
}

func TestAuthenticationModeChangeShowsBackendPolicyReason(t *testing.T) {
	fake := newAuthFlowAPI()
	fake.changeErr = &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "Password is too simple"}
	app, err := New(fake, Config{Version: "test", ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	form := "csrf=token&mode=separate&system_password=system-secret&new_web_password=web-secret&confirm_web_password=web-secret"
	req := httptest.NewRequest(http.MethodPost, "http://example/settings/authentication", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Password is too simple") {
		t.Fatalf("backend policy reason not shown: %d %s", rr.Code, rr.Body.String())
	}
}

func TestPasswordChangeShowsBackendPolicyReason(t *testing.T) {
	fake := newAuthFlowAPI()
	fake.passwordErr = &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "Password is too simple"}
	app, err := New(fake, Config{Version: "test", ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	form := "csrf=token&current_password=secret&new_password=strong-enough-123&confirm_password=strong-enough-123"
	req := httptest.NewRequest(http.MethodPost, "http://example/password", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Password is too simple") {
		t.Fatalf("backend password rejection reason not shown: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAuthenticationModeChangeRejectsMissingCSRF(t *testing.T) {
	fake := newAuthFlowAPI()
	app, err := New(fake, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/settings/authentication", strings.NewReader("mode=system"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if fake.change.Mode != "" {
		t.Fatal("mode change reached API without CSRF")
	}
}

func TestSystemWorkspaceAPIsRespectForcedPasswordChangeBoundary(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/api/system/workspace/about", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "1"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forced password-change boundary, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Password change required") {
		t.Fatalf("forced password-change boundary is not explicit: %s", rr.Body.String())
	}
}

func TestForcedPasswordPageRemainsStandaloneRoute(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/password", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "1"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("forced password page status=%d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Change administrator password") || !strings.Contains(body, "action=\"/password\"") {
		t.Fatalf("forced password page no longer renders the existing standalone form: %s", body)
	}
}
