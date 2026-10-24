package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNordAPI struct {
	*fakeNetworkConfigurationAPI
	callback, loginAction string
	actionError           error
}

func (f *fakeNordAPI) NordVPNLogin(_ context.Context, _, action, callback string) (api.RemoteAccessChange, error) {
	f.calls++
	f.callback, f.loginAction = callback, action
	return api.RemoteAccessChange{OK: true, LoginURL: "https://api.nordvpn.com/v1/users/oauth/login-redirect?attempt=example"}, nil
}
func (f *fakeNordAPI) RemoteAccessStatus(context.Context, string) (api.RemoteAccessStatus, error) {
	return api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "nordvpn", DisplayName: "NordVPN Meshnet", Account: "Logged in", Meshnet: "Enabled", NordName: "local-everest.nord", IP: "100.64.0.1"}}}, nil
}
func TestNordWebUIProxyAdministratorCSRFAndCallback(t *testing.T) {
	const callback = "nordvpn://login?action=login&exchange_token=never_log_me&status=done"
	for _, tc := range []struct {
		role, csrf, action, callback, extra string
		want                                int
	}{
		{"viewer", "token", "login", "", "", 403},
		{"operator", "token", "login", "", "", 403},
		{"administrator", "wrong", "login", "", "", 403},
		{"administrator", "token", "login", "", "", 200},
		{"administrator", "token", "complete-login", callback, "", 200},
		{"administrator", "token", "complete-login", "https://evil.test", "", 400},
		{"administrator", "token", "login", "", "&referral=https://evil.test", 400},
		{"administrator", "token", "login", "", "&dashboard=https://evil.test", 400},
	} {
		fake := &fakeNordAPI{fakeNetworkConfigurationAPI: &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: tc.role}}}
		app, err := New(fake, Config{ManagementAPI: "v1", ExternalScheme: "http"})
		if err != nil {
			t.Fatal(err)
		}
		body := "csrf=" + tc.csrf + "&action=" + tc.action + "&callback=" + url.QueryEscape(tc.callback) + tc.extra
		req := httptest.NewRequest(http.MethodPost, "/api/network/remote-access/nordvpn/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != tc.want || strings.Contains(rr.Body.String(), "never_log_me") {
			t.Fatal(tc.want, rr.Code)
		}
		if tc.want == 200 && (fake.calls != 1 || fake.callback != tc.callback || fake.loginAction != tc.action) {
			t.Fatal("lost login request")
		}
		if tc.want != 200 && fake.calls != 0 {
			t.Fatal("rejected request reached agent")
		}
		req = httptest.NewRequest(http.MethodGet, "/api/network/remote-access", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"meshnet":"Enabled"`) {
			t.Fatal("Nord status proxy", rr.Code)
		}
	}
}

func TestNordApprovedDialogsAndResponsiveProviderLayout(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	for _, want := range []string{"NordVPN account required", "NordVPN Meshnet requires a Nord Account.", "Already have an account?", "Log in to NordVPN", "Create NordVPN account", "https://refer-nordvpn.com/pZGMpGTrBrO", "Complete NordVPN login", "On the Continue button, copy the link address and paste it below.", "Continue link", "Complete login", "noopener noreferrer", "Open NordVPN account", "https://my.nordaccount.com/", `input.maxLength = 2048`, `input.autocomplete = "off"`, `input.value = ""`, `if (result?.login_required) showNordAccountDialog()`} {
		if !strings.Contains(script, want) {
			t.Fatal("approved UI missing", want)
		}
	}
	if strings.Contains(script, `"Open Nord account"`) {
		t.Fatal("old Nord account button label remains")
	}
	if strings.Contains(script, "provider.dashboard_url") || strings.Contains(script, "result.referral") {
		t.Fatal("caller controlled destination")
	}
	css, err := assets.ReadFile("static/network-workspace.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".network-provider-grid{grid-template-columns:minmax(0,1fr)}", "@container(min-width:580px){.network-provider-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}", "@container(min-width:860px){.network-provider-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}", "@container(min-width:1140px){.network-provider-grid{grid-template-columns:repeat(4,minmax(0,1fr))}}", "width:min(1180px,calc(100vw - 1rem))", "height:min(760px,var(--jv-workspace-height))"} {
		if !strings.Contains(string(css), want) {
			t.Fatal("responsive layout or geometry", want)
		}
	}
	app, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(app), `["Setup", "Migration", "Restore", "Plugins", "Reset", "Network", "Support", "Failed units"]`) {
		t.Fatal("log ordering")
	}
}

func (f *fakeNordAPI) ChangeRemoteAccess(_ context.Context, _, provider, action string) (api.RemoteAccessChange, error) {
	f.calls++
	f.provider, f.action = provider, action
	if f.actionError != nil {
		return api.RemoteAccessChange{}, f.actionError
	}
	return api.RemoteAccessChange{OK: true, LoginRequired: provider == "nordvpn" && action == "activate"}, nil
}

func TestNordActivationLoginRequiredResponse(t *testing.T) {
	fake := &fakeNordAPI{fakeNetworkConfigurationAPI: &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: "administrator"}}}
	app, err := New(fake, Config{ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/network/remote-access/nordvpn", strings.NewReader("csrf=token&action=activate"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"login_required":true`) || fake.provider != "nordvpn" {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestNordLifecycleErrorsPassThroughSafely(t *testing.T) {
	for _, tc := range []struct{ action, message string }{
		{"activate", "NordVPN could not be activated. Open Troubleshoot and generate a Network report before retrying."},
		{"deactivate", "NordVPN could not be deactivated. Open Troubleshoot and generate a Network report before retrying."},
	} {
		t.Run(tc.action, func(t *testing.T) {
			fake := &fakeNordAPI{
				fakeNetworkConfigurationAPI: &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: "administrator"}},
				actionError:                 &api.ResponseError{StatusCode: http.StatusServiceUnavailable, Message: tc.message},
			}
			app, err := New(fake, Config{ManagementAPI: "v1", ExternalScheme: "http"})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/network/remote-access/nordvpn", strings.NewReader("csrf=token&action="+tc.action))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
			req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			var response struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rr.Code != http.StatusServiceUnavailable || response.Error != tc.message || fake.calls != 1 {
				t.Fatal(rr.Code, rr.Body.String())
			}
		})
	}
}
