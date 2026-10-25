package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNordAPI struct {
	*fakeNetworkConfigurationAPI
	token       string
	loginError  error
	actionError error
}

func (f *fakeNordAPI) NordVPNTokenLogin(_ context.Context, _, token string) (api.RemoteAccessChange, error) {
	f.calls++
	f.token = token
	return api.RemoteAccessChange{OK: true}, f.loginError
}
func (f *fakeNordAPI) RemoteAccessStatus(context.Context, string) (api.RemoteAccessStatus, error) {
	return api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "nordvpn", DisplayName: "NordVPN Meshnet", Account: "Logged in", Meshnet: "Enabled", NordName: "local-everest.nord", IP: "100.64.0.1"}}}, nil
}
func TestNordWebUIProxyAdministratorCSRFAndToken(t *testing.T) {
	const token = "never_reflect_me+/_-.~=opaque"
	for _, tc := range []struct {
		name, role, csrf, token, extra string
		want                           int
		fail                           bool
	}{
		{name: "viewer", role: "viewer", csrf: "token", token: token, want: 403},
		{name: "operator", role: "operator", csrf: "token", token: token, want: 403},
		{name: "csrf", role: "administrator", csrf: "wrong", token: token, want: 403},
		{name: "success", role: "administrator", csrf: "token", token: token, want: 200},
		{name: "maximum encoded", role: "administrator", csrf: "token", token: strings.Repeat("+", 4096), want: 200},
		{name: "missing", role: "administrator", csrf: "token", want: 400},
		{name: "whitespace", role: "administrator", csrf: "token", token: "a b", want: 400},
		{name: "control", role: "administrator", csrf: "token", token: "a\x00b", want: 400},
		{name: "oversized", role: "administrator", csrf: "token", token: strings.Repeat("a", 4097), want: 400},
		{name: "duplicate", role: "administrator", csrf: "token", token: token, extra: "&token=other", want: 400},
		{name: "duplicate csrf", role: "administrator", csrf: "token", token: token, extra: "&csrf=token", want: 400},
		{name: "action", role: "administrator", csrf: "token", token: token, extra: "&action=token-login", want: 400},
		{name: "callback", role: "administrator", csrf: "token", token: token, extra: "&callback=secret", want: 400},
		{name: "provider", role: "administrator", csrf: "token", token: token, extra: "&provider=nordvpn", want: 400},
		{name: "url", role: "administrator", csrf: "token", token: token, extra: "&URL=secret", want: 400},
		{name: "vendor failure", role: "administrator", csrf: "token", token: token, want: 503, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNordAPI{fakeNetworkConfigurationAPI: &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: tc.role}}}
			if tc.fail {
				fake.loginError = errors.New("private vendor output " + token)
			}
			app, err := New(fake, Config{ManagementAPI: "v1", ExternalScheme: "http"})
			if err != nil {
				t.Fatal(err)
			}
			form := url.Values{"csrf": {tc.csrf}}
			if tc.token != "" {
				form.Set("token", tc.token)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/network/remote-access/nordvpn/login", strings.NewReader(form.Encode()+tc.extra))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
			req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			if rr.Code != tc.want || strings.Contains(rr.Body.String(), "never_reflect_me") || strings.Contains(rr.Body.String(), "private vendor") {
				t.Fatal("status or response privacy", tc.want, rr.Code)
			}
			reachesAgent := tc.want == 200 || tc.fail
			if reachesAgent && (fake.calls != 1 || fake.token != tc.token) {
				t.Fatal("lost opaque token")
			}
			if !reachesAgent && fake.calls != 0 {
				t.Fatal("rejected request reached agent")
			}
			req = httptest.NewRequest(http.MethodGet, "/api/network/remote-access", nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
			rr = httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"meshnet":"Enabled"`) || strings.Contains(rr.Body.String(), "never_reflect_me") {
				t.Fatal("Nord status proxy/privacy", rr.Code)
			}
		})
	}
}

func TestNordTokenLoginAuthenticationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, session string
		mustChange    bool
		want          int
	}{
		{name: "unauthenticated", want: http.StatusUnauthorized},
		{name: "password change required", session: "session-token", mustChange: true, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNordAPI{fakeNetworkConfigurationAPI: &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: "administrator"}}}
			app, err := New(fake, Config{ManagementAPI: "v1", ExternalScheme: "http"})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/network/remote-access/nordvpn/login", strings.NewReader("csrf=token&token=never_reflect_me"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
			if tc.session != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.session})
			}
			if tc.mustChange {
				req.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "1"})
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			if rr.Code != tc.want || fake.calls != 0 || strings.Contains(rr.Body.String(), "never_reflect_me") {
				t.Fatal("authentication boundary or privacy changed", rr.Code)
			}
		})
	}
}

func TestNordTokenLoginAllowsOnlySafeManagementErrors(t *testing.T) {
	const token = "never_reflect_me+/_-.~=opaque"
	const loginFailure = "NordVPN login could not be completed. Check the access token and retry."
	const meshnetFailure = "NordVPN login succeeded, but Meshnet could not be activated. Retry Activate or open Troubleshoot."
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"login stage", &api.ResponseError{StatusCode: 503, Message: loginFailure}, loginFailure},
		{"meshnet stage", &api.ResponseError{StatusCode: 503, Message: meshnetFailure}, meshnetFailure},
		{"vendor response", &api.ResponseError{StatusCode: 503, Message: "secret vendor failure 123"}, loginFailure},
		{"token response", &api.ResponseError{StatusCode: 503, Message: token}, loginFailure},
		{"near match", &api.ResponseError{StatusCode: 503, Message: meshnetFailure + " "}, loginFailure},
		{"transport", errors.New("private transport error " + token), loginFailure},
		{"non ResponseError", errors.New(meshnetFailure), loginFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNordAPI{
				fakeNetworkConfigurationAPI: &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: "administrator"}},
				loginError:                  tc.err,
			}
			app, err := New(fake, Config{ManagementAPI: "v1", ExternalScheme: "http"})
			if err != nil {
				t.Fatal(err)
			}
			form := url.Values{"csrf": {"token"}, "token": {token}}
			req := httptest.NewRequest(http.MethodPost, "/api/network/remote-access/nordvpn/login", strings.NewReader(form.Encode()))
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
			if rr.Code != http.StatusServiceUnavailable || response.Error != tc.want || fake.calls != 1 {
				t.Fatal("unsafe or incorrect login message", rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), token) || strings.Contains(rr.Body.String(), "secret vendor") {
				t.Fatal("token/vendor output reflected")
			}
		})
	}
}

func TestNordApprovedDialogsAndResponsiveProviderLayout(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	for _, want := range []string{
		"NordVPN account required", "NordVPN Meshnet needs a NordVPN account and access token.",
		"Already have a NordVPN account?", "Open NordVPN dashboard", "Create NordVPN account",
		"https://my.nordaccount.com/dashboard/nordvpn/", "https://refer-nordvpn.com/pZGMpGTrBrO",
		"Get your NordVPN access token", "Select NordVPN from the left menu.", "Scroll down to Advanced settings.",
		"Under Access token, select Get Access token.", "Select Generate new token.", "Copy the token and paste it below.",
		"NordVPN may ask you to verify your email before generating the token.",
		"Access token", "Log in & activate", "noopener noreferrer", "Open NordVPN account",
		`input.type = "password"`, `input.maxLength = 4096`, `input.autocomplete = "off"`,
		`input.spellcheck = false`, `input.autocapitalize = "off"`,
		`if (result?.login_required) showNordAccountDialog()`,
	} {
		if !strings.Contains(script, want) {
			t.Fatal("approved UI missing", want)
		}
	}
	for _, old := range []string{"Complete NordVPN login", "NordVPN success URL", "Open NordVPN login", "xdg-open", "exchange_token", "nordvpn://", "validNordCallback", "validNordLoginURL", "complete-login"} {
		if strings.Contains(script, old) {
			t.Fatal("obsolete Nord browser flow remains", old)
		}
	}
	start := strings.Index(script, "  const validNordAccessToken")
	end := strings.Index(script, "  const renderRemoteAccess")
	if start < 0 || end <= start || strings.Contains(script[start:end], "window.open") {
		t.Fatal("automatic Nord tab opening")
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

func TestNordBrowserTokenValidationAndPrivacy(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "  const validNordAccessToken")
	end := strings.Index(script, "  const renderRemoteAccess")
	if start < 0 || end <= start {
		t.Fatal("missing Nord token dialog")
	}
	if !strings.Contains(script[start:end], "} finally {\n        token = \"\";\n        input.value = \"\";") {
		t.Fatal("token local and input must clear in finally")
	}
	program := `
const assert = require("node:assert/strict");
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.textContent = ""; this.listeners = {}; this.value = ""; }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); return node; }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute() {}
  addEventListener(name, handler) { this.listeners[name] = handler; }
  showModal() { this.open = true; }
  close() { this.open = false; this.listeners.close?.(); }
  remove() {}
}
const document = { createElement: tag => new Element(tag), body: new Element("body") };
const window = { open: () => { throw new Error("automatic Nord tab"); } };
const flatten = node => [node, ...node.children.flatMap(flatten)];
const secret = "never_reflect_this+/_-.~=opaque";
let submitted, input, fail = true, failure = new Error(secret), refreshed = 0;
const postForm = async (path, request) => {
  assert.equal(path, "/api/network/remote-access/nordvpn/login");
  assert.equal(input.value, "", "clear field before async POST");
  assert.deepEqual(request, { token: secret });
  submitted = request.token;
  if (fail) throw failure;
};
const refreshRemoteAccess = async () => { refreshed++; };
` + script[start:end] + `
(async () => {
  let visible = "";
  for (let b = 0x21; b <= 0x7e; b++) visible += String.fromCharCode(b);
  for (const good of [visible, secret, "a".repeat(4096)]) assert(validNordAccessToken(good));
  for (const bad of ["", "a".repeat(4097), "a b", "a\tb", "a\rb", "a\nb", "a\0b", "a\x1fb", "a\x7fb", "é", "\ud800"]) {
    assert.equal(validNordAccessToken(bad), false);
  }
  showNordAccountDialog();
  let popup = document.body.children.at(-1);
  const button = label => flatten(popup).find(node => node.tag === "button" && node.textContent === label);
  input = flatten(popup).find(node => node.tag === "input");
  assert.equal(input.type, "password"); assert.equal(input.maxLength, 4096);
  assert.equal(input.autocomplete, "off"); assert.equal(input.spellcheck, false);
  assert.equal(input.autocapitalize, "off");
  for (const [label, href] of [
    ["Open NordVPN dashboard", "https://my.nordaccount.com/dashboard/nordvpn/"],
    ["Create NordVPN account", "https://refer-nordvpn.com/pZGMpGTrBrO"]
  ]) {
    const link = flatten(popup).find(node => node.tag === "a" && node.textContent === label);
    assert.equal(link.href, href); assert.equal(link.target, "_blank"); assert.equal(link.rel, "noopener noreferrer");
  }
  input.value = "bad token";
  await button("Log in & activate").listeners.click();
  assert.equal(submitted, undefined);
  assert.equal(input.value, "");
  const loginFailure = "NordVPN login could not be completed. Check the access token and retry.";
  const meshnetFailure = "NordVPN login succeeded, but Meshnet could not be activated. Retry Activate or open Troubleshoot.";
  for (const [error, expected] of [
    [new Error(meshnetFailure), meshnetFailure],
    [new Error(loginFailure), loginFailure],
    [new Error("secret vendor failure 123"), loginFailure],
    [new Error(secret), loginFailure],
    [new Error(meshnetFailure + " "), loginFailure],
    [{ message: "<img src=x onerror=alert(1)>" }, loginFailure],
    [{}, loginFailure], [null, loginFailure], ["arbitrary failure", loginFailure]
  ]) {
    failure = error;
    input.value = secret;
    await button("Log in & activate").listeners.click();
    assert.equal(submitted, secret);
    assert.equal(input.value, "");
    assert.equal(popup.open, true);
    assert(flatten(popup).some(node => node.textContent === expected));
    if (error?.message && error.message !== expected) {
      assert(!flatten(popup).some(node => node.textContent === error.message));
    }
    for (const node of flatten(popup)) {
      for (const field of ["value", "textContent", "innerHTML", "href", "placeholder"]) {
        assert(!String(node[field] || "").includes(secret));
      }
    }
  }
  fail = false;
  input.value = secret;
  await button("Log in & activate").listeners.click();
  assert.equal(popup.open, false); assert.equal(input.value, ""); assert.equal(refreshed, 1);
  showNordAccountDialog();
  popup = document.body.children.at(-1);
  input = flatten(popup).find(node => node.tag === "input");
  input.value = secret;
  button("Cancel").listeners.click();
  assert.equal(input.value, ""); assert.equal(popup.open, false);
  showNordAccountDialog();
  popup = document.body.children.at(-1);
  input = flatten(popup).find(node => node.tag === "input");
  input.value = secret;
  popup.close(); // Native Escape close event also clears the secret.
  assert.equal(input.value, "");
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser token validation/privacy: %v: %s", err, output)
	}
}
