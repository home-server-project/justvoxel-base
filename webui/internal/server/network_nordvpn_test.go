package server

import (
	"context"
	"encoding/json"
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
		{"administrator", "token", "complete-login", "https://nordaccount.com/product/nordvpn/login/success?exchange_token=never_log_me", "", 200},
		{"administrator", "token", "complete-login", "https://nordaccount.com/product/nordvpn/login/success?exchange_token=never_log_me&extra=value", "", 400},
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
	for _, want := range []string{"NordVPN account required", "NordVPN Meshnet requires a Nord Account.", "Already have an account?", "Log in to NordVPN", "Create NordVPN account", "https://refer-nordvpn.com/pZGMpGTrBrO", "Complete NordVPN login", "After NordVPN shows “Great – you’re in!”, if your browser asks to open NordVPN or xdg-open, choose Cancel. Copy the URL from the browser address bar and paste it below. Do not select Continue.", "NordVPN success URL", "https://nordaccount.com/product/nordvpn/login/success?exchange_token=...", "Paste the NordVPN success URL from your browser address bar.", "Open NordVPN login", "Complete login", "noopener noreferrer", "Open NordVPN account", "https://my.nordaccount.com/", `input.maxLength = 2048`, `input.autocomplete = "off"`, `input.value = ""`, `if (result?.login_required) showNordAccountDialog()`} {
		if !strings.Contains(script, want) {
			t.Fatal("approved UI missing", want)
		}
	}
	if strings.Contains(script, "On the Continue button, copy the link address and paste it below.") || strings.Contains(script, `"Continue link"`) {
		t.Fatal("old Continue-copy instruction remains")
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

func TestNordBrowserCompletionValidation(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "  const validNordCallback =")
	end := strings.Index(script, "  let nordDialog")
	if start < 0 || end <= start {
		t.Fatal("missing browser validator")
	}
	program := script[start:end] + `
const assert = require("node:assert/strict");
for (const good of [
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=YWJj%2B%2F%3D",
  "nordvpn://login?exchange_token=abc",
  "nordvpn://login?action=login&exchange_token=abc&status=done"
]) assert.equal(validNordCallback(good), true);
for (const bad of [
  "http://nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "https://www.nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "https://my.nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "https://api.nordvpn.com/product/nordvpn/login/success?exchange_token=abc",
  "https://nordaccount.com.evil.test/product/nordvpn/login/success?exchange_token=abc",
  "https://evil.nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "https://nordaccount.com:443/product/nordvpn/login/success?exchange_token=abc",
  "https://user:password@nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "https://nordaccount.com/wrong?exchange_token=abc",
  "https://nordaccount.com/product/nordvpn/login/%73uccess?exchange_token=abc",
  "https://nordaccount.com/product/nordvpn/login/success/?exchange_token=abc",
  "https://nordaccount.com/product/nordvpn/login/other/../success?exchange_token=abc",
  "https://nordaccount.com/product/nordvpn/login%2fsuccess?exchange_token=abc",
  "https://nordaccount.com/product/nordvpn/login/success",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc&exchange_token=def",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc&extra=value",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc&action=login",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc#fragment",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc#",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%0a",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%20",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%3B",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%ZZ",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc+def",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc\r\n",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc def",
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc\u0000",
  "HTTPS://nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "//nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
  "nordvpn:login?exchange_token=abc",
  "nordvpn://login:443?exchange_token=abc",
  "nordvpn://login?exchange_token=abc&status=bad",
  "nordvpn://login?exchange_token=abc&action=login&action=login",
  "nordvpn://login?exchange_token=abc&extra=value",
  "nordvpn://login/path?exchange_token=abc",
  "nordvpn://user@login?exchange_token=abc",
  "nordvpn://login?exchange_token=abc#fragment",
  "nordvpn://login?exchange_token=abc&exchange_token=def",
  "nordvpn://login?exchange_token=abc%0a"
].concat([
  "https://nordaccount.com/product/nordvpn/login/success?exchange_token=" + "a".repeat(1025),
  "nordvpn://login?exchange_token=" + "a".repeat(1025),
  "a".repeat(2049)
])) assert.equal(validNordCallback(bad), false);
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser completion validation: %v: %s", err, output)
	}
}

func TestNordBrowserCompletionPrivacy(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "  const validNordLoginURL =")
	end := strings.Index(script, "  const renderRemoteAccess =")
	if start < 0 || end <= start {
		t.Fatal("missing Nord login dialog")
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
const window = { open: () => null };
const secret = "never_reflect_this_exchange";
const completion = "https://nordaccount.com/product/nordvpn/login/success?exchange_token=" + secret;
let submitted;
const postForm = async (_, request) => {
  if (request.action === "login") return { login_url: "https://api.nordvpn.com/v1/users/oauth/login-redirect?attempt=example" };
  submitted = request.callback;
  throw new Error(completion); // Even sensitive backend errors must stay private.
};
const refreshRemoteAccess = async () => {};
const flatten = node => [node, ...node.children.flatMap(flatten)];
` + script[start:end] + `
(async () => {
  showNordAccountDialog();
  const popup = document.body.children[0];
  const button = label => flatten(popup).find(node => node.tag === "button" && node.textContent === label);
  await button("Log in to NordVPN").listeners.click();
  const input = flatten(popup).find(node => node.tag === "input");
  const complete = button("Complete login");
  input.value = completion + "&extra=invalid";
  await complete.listeners.click();
  assert.equal(submitted, undefined);
  assert(flatten(popup).some(node => node.textContent === "Paste the NordVPN success URL from your browser address bar."));
  input.value = completion;
  await complete.listeners.click();
  assert.equal(submitted, completion);
  assert.equal(input.value, "");
  assert(flatten(popup).some(node => node.textContent === "NordVPN login could not be completed. Try logging in again."));
  for (const node of flatten(popup)) {
    for (const field of ["textContent", "innerHTML", "href", "placeholder"]) {
      assert(!String(node[field] || "").includes(secret));
    }
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser completion privacy: %v: %s", err, output)
	}
}
