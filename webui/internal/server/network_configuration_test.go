package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNetworkConfigurationAPI struct {
	*fakeNetworkAPI
	calls            int
	ethernet         api.EthernetSettings
	provider, action string
}

func (f *fakeNetworkConfigurationAPI) RemoteAccessStatus(context.Context, string) (api.RemoteAccessStatus, error) {
	return api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "playit", DisplayName: "Playit.gg", Installed: true, ServiceActive: true, ServiceEnabled: true}}}, nil
}
func (f *fakeNetworkConfigurationAPI) ChangeRemoteAccess(_ context.Context, _, provider, action string) (api.RemoteAccessChange, error) {
	f.calls++
	f.provider, f.action = provider, action
	if provider == "netbird" && action == "activate" {
		return api.RemoteAccessChange{OK: true, LoginURL: "https://app.netbird.io/login?test=1"}, nil
	}
	return api.RemoteAccessChange{OK: true}, nil
}
func (f *fakeNetworkConfigurationAPI) ConfigureEthernet(_ context.Context, _, _ string, settings api.EthernetSettings) (api.NetworkWiFiMutation, error) {
	f.calls++
	f.ethernet = settings
	return api.NetworkWiFiMutation{OK: true}, nil
}
func (f *fakeNetworkConfigurationAPI) CheckNetworkConnectivity(context.Context, string) error {
	f.calls++
	return nil
}
func (f *fakeNetworkConfigurationAPI) ReconnectNetwork(context.Context, string, string, string, string) (api.NetworkWiFiMutation, error) {
	f.calls++
	return api.NetworkWiFiMutation{OK: true}, nil
}

func TestNetworkWorkspaceTabsFollowAuthenticatedRole(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		fake := &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: role}
		app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/network", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		var status api.NetworkStatus
		if rr.Code != http.StatusOK {
			t.Fatalf("role %s: %d", role, rr.Code)
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		want := []string{"Overview"}
		if role == "administrator" {
			want = []string{"Overview", "Ethernet", "Wi-Fi", "Troubleshoot", "Remote Access"}
		}
		if !reflect.DeepEqual(status.WorkspaceTabs, want) {
			t.Fatalf("role %s tabs %v", role, status.WorkspaceTabs)
		}
	}
}

func TestNetworkConfigurationProxyRequiresAdministratorAndCSRF(t *testing.T) {
	paths := []string{"/api/network/ethernet/enp1s0", "/api/network/reconnect/enp1s0", "/api/network/connectivity-check", "/api/network/remote-access/playit"}
	for _, role := range []string{"administrator", "operator", "viewer"} {
		for _, csrf := range []string{"", "token"} {
			fake := &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: role}}
			app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("csrf="+csrf+"&method=auto&prefix=0&mtu=0&autoconnect=true&profile_uuid=12345678-1234-1234-1234-123456789abc&checkpoint_id=opaque&action=activate"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
				req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
				rr := httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, req)
				want := http.StatusForbidden
				if role == "administrator" && csrf == "token" {
					want = http.StatusOK
				}
				if rr.Code != want {
					t.Fatalf("%s %s csrf=%q: %d body=%s", role, path, csrf, rr.Code, rr.Body.String())
				}
			}
			if role != "administrator" || csrf == "" {
				if fake.calls != 0 {
					t.Fatal("rejected mutation reached Management API")
				}
			} else {
				if fake.ethernet.CheckpointID != "opaque" || fake.provider != "playit" || fake.action != "activate" {
					t.Fatal("proxy lost checkpoint or fixed provider action")
				}
			}
		}
	}
}

func TestNetworkWiFiControlsRemainInsideWiFiTab(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, `} else if (selectedTab === "Wi-Fi") {`)
	end := strings.Index(script, `} else if (selectedTab === "Troubleshoot") {`)
	if start < 0 || end <= start {
		t.Fatal("Wi-Fi tab missing")
	}
	wifiTab := script[start:end]
	for _, want := range []string{"renderWiFiNetworks", "renderSavedWiFi", "renderConnectPanel"} {
		if !strings.Contains(wifiTab, want) {
			t.Fatalf("Wi-Fi tab lost %s", want)
		}
	}
	for _, want := range []string{"renderWiFiNetworks(device, currentNetworks.get(device.interface))", "renderSavedWiFi(currentSnapshot)", "renderConnectPanel()", "networkWifiRadio", "networkWifiSavedConnect", "networkWifiJoin", "networkWifiOther", "networkWifiDisconnect", "networkWifiForget", "runCheckpointedMutation", "rollback_timeout_seconds: 90"} {
		if !strings.Contains(script, want) {
			t.Fatalf("Wi-Fi behavior missing %s", want)
		}
	}
	if !strings.Contains(script, `effectiveTabs(currentSnapshot)`) || !strings.Contains(script, "if (!isAdministrator()) return;") {
		t.Fatal("read-only tab/action guard missing")
	}
}

func (f *fakeNetworkConfigurationAPI) PlayitSetup(_ context.Context, _ string, start bool) (api.PlayitSetupState, error) {
	f.calls++
	return api.PlayitSetupState{State: "waiting", ClaimURL: "https://playit.gg/claim/0123abcdef"}, nil
}

func TestPlayitSetupProxyAdministratorAndCSRF(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, csrf := range []string{"", "token"} {
				fake := &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: role}}
				app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(method, "/api/network/remote-access/playit/setup", strings.NewReader("csrf="+csrf))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
				req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
				rr := httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, req)
				allowed := role == "administrator" && (method == http.MethodGet || csrf == "token")
				if allowed {
					if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "https://playit.gg/claim/0123abcdef") || fake.calls != 1 {
						t.Fatal(rr.Body.String())
					}
				} else if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), "0123abcdef") || fake.calls != 0 {
					t.Fatalf("%s %s csrf=%q: %d %s", role, method, csrf, rr.Code, rr.Body.String())
				}
			}
		}
	}
}

func TestRemoteAccessCardBehavior(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	controlsStart := strings.Index(script, "  const providerTransitional =")
	controlsEnd := strings.Index(script, "  const isAdministrator =")
	renderStart := strings.Index(script, "  const renderRemoteAccess =")
	renderEnd := strings.Index(script, "  const renderNetwork =")
	if controlsStart < 0 || controlsEnd <= controlsStart || renderStart < 0 || renderEnd <= renderStart {
		t.Fatal("remote card functions missing")
	}
	program := `
const assert = require("node:assert/strict");
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.dataset = {}; this.disabled = false; this.textContent = ""; }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); return node; }
}
const document = { createElement: (tag) => new Element(tag) };
const row = (name, value) => { const e = new Element("row"); e.textContent = name + ": " + value; return e; };
const sectionHeading = () => new Element("heading");
const actionButton = (text, key, value, kind = "secondary") => { const e = new Element("button"); e.textContent = text; e.dataset[key] = value; e.className = kind + " nav-admin-only network-action-button"; return e; };
const label = (value) => String(value || "unknown");
let remoteBusy = false, remoteError = "", remoteProviders = [], playitSetup = { state: "idle" };
const providerProgress = new Map();
let playitPopup = null, playitClaimOpened = false, netbirdLoginURL = "", tailscaleLoginURL = "";
const validTailscaleLoginURL = (value) => typeof value === "string" && value.startsWith("https://login.tailscale.com/a/");
const validNetbirdLoginURL = (value) => typeof value === "string" && value.startsWith("https://");
` + script[controlsStart:controlsEnd] + script[renderStart:renderEnd] + `
const flatten = (node) => [node, ...node.children.flatMap(flatten)];
const render = (provider) => { remoteProviders = [provider]; return flatten(renderRemoteAccess()); };
const base = { id: "playit", display_name: "Playit.gg", installed: true, service_active: true, service_enabled: true, service_state: "active", configured: false, dashboard_url: "https://evil.test/" };
let nodes = render(base);
assert(!nodes.some((n) => n.textContent === "Set up Playit"));
assert(nodes.some((n) => n.textContent === "Activate" && n.disabled));
const fresh = render({ ...base, service_active: false, service_enabled: false, service_state: "inactive" });
assert(fresh.some((n) => n.textContent === "Activate" && !n.disabled));
assert(fresh.some((n) => n.textContent === "Deactivate" && n.disabled));
assert(nodes.some((n) => n.textContent === "Deactivate" && !n.disabled));
assert(!nodes.some((n) => /mjust net|use CLI/.test(n.textContent)));
for (const state of ["activating", "deactivating", "reloading", "refreshing"]) {
  nodes = render({ ...base, configured: true, service_active: false, service_state: state });
  assert(nodes.some((n) => n.textContent === "Activate" && n.disabled));
  assert(nodes.some((n) => n.textContent === "Deactivate" && n.disabled === (state === "deactivating")));
  const netbird = render({ ...base, id: "netbird", configured: false, service_active: false, service_state: state });
  assert(netbird.some((n) => n.textContent === "Activate" && n.disabled === (state !== "activating")));
  assert(netbird.some((n) => n.textContent === "Deactivate" && n.disabled === (state === "deactivating")));
  assert(nodes.some((n) => n.textContent === "Service: " + state[0].toUpperCase() + state.slice(1) + "…"));
}
for (const id of ["tailscale", "netbird", "playit"]) {
  nodes = render({ ...base, id, configured: true });
  assert(nodes.some((n) => n.textContent === "Activate" && n.disabled === (id === "playit")));
  assert(nodes.some((n) => n.textContent === "Deactivate" && !n.disabled));
  assert(!nodes.some((n) => n.textContent === "Set up Playit"));
  const link = nodes.find((n) => n.textContent === "Open provider dashboard");
  assert.equal(link.className, "button-link primary network-action-button");
  assert.equal(nodes.find((n) => n.textContent === "Activate").className, "warning nav-admin-only network-action-button");
  assert.equal(nodes.find((n) => n.textContent === "Deactivate").className, "secondary nav-admin-only network-action-button");
  assert.equal(link.target, "_blank"); assert.equal(link.rel, "noopener noreferrer");
  assert.equal(link.href, { tailscale: "https://console.tailscale.com/admin/", netbird: "https://app.netbird.io/", playit: "https://playit.gg/account/" }[id]);
}
netbirdLoginURL = "https://app.netbird.io/login?code=abcd";
nodes = render({ ...base, id: "netbird", configured: false, service_active: false, service_enabled: false, service_state: "inactive" });
assert(nodes.some((n) => n.textContent === "Complete NetBird login" && n.href === netbirdLoginURL));
netbirdLoginURL = "";
nodes = render({ ...base, id: "netbird", configured: true, connected: true, ip: "100.80.12.5/16" });
assert(nodes.some((n) => n.textContent === "NetBird IP: 100.80.12.5/16"));
assert(nodes.some((n) => n.textContent === "Activate" && n.disabled));
tailscaleLoginURL = "https://login.tailscale.com/a/0123456789abcdef";
nodes = render({ ...base, id: "tailscale", configured: false, connected: false, service_active: true });
assert(nodes.some((n) => n.textContent === "Activate" && !n.disabled));
assert(nodes.some((n) => n.textContent === "Complete Tailscale login" && n.href === tailscaleLoginURL));
tailscaleLoginURL = "";
nodes = render({ ...base, id: "tailscale", configured: true, connected: true, ip: "100.101.102.103", summary: "Connected" });
assert(nodes.some((n) => n.textContent === "Tailscale IP: 100.101.102.103"));
assert(nodes.some((n) => n.textContent === "Connection: Connected"));
assert(nodes.some((n) => n.textContent === "Activate" && n.disabled));
nodes = render({ ...base, configured: true, service_active: false, service_enabled: false, service_state: "inactive" });
assert(nodes.some((n) => n.textContent === "Activate" && !n.disabled));
assert(nodes.some((n) => n.textContent === "Deactivate" && n.disabled));
for (const state of ["inactive", "failed"]) {
  nodes = render({ ...base, configured: true, service_active: false, service_state: state });
  assert(nodes.some((n) => n.textContent === "Activate" && !n.disabled));
  assert(nodes.some((n) => n.textContent === "Deactivate" && !n.disabled));
}
for (const state of ["starting", "waiting"]) {
  playitSetup = { state, claim_url: "https://playit.gg/claim/0123abcdef" };
  nodes = render(base);
  assert(nodes.filter((n) => ["Activate", "Set up Playit"].includes(n.textContent)).every((n) => n.disabled));
  assert(nodes.some((n) => n.textContent === "Deactivate" && !n.disabled));
  assert(nodes.some((n) => n.textContent === "Open provider dashboard"));
  assert(render({ ...base, service_active: false, service_enabled: false, service_state: "inactive" }).some((n) => n.textContent === "Deactivate" && !n.disabled));
  if (state === "waiting") {
    const claim = nodes.find((n) => n.textContent === "Open Playit setup");
    assert.equal(claim.href, playitSetup.claim_url);
    assert.equal(claim.className, "button-link primary network-action-button");
    assert(!nodes.some((n) => n.textContent === "Activate"));
    playitClaimOpened = true;
    assert(render(base).some((n) => n.textContent === "Activate" && n.disabled));
    assert(!render(base).some((n) => n.textContent === "Open Playit setup"));
    playitClaimOpened = false;
  }
}
for (const claim_url of ["https://playit.gg/claim/0123abcdef", "https://playit.gg/claim/A1B2C3D4E5"]) {
  playitSetup = { state: "waiting", claim_url };
  assert.equal(render(base).find((n) => n.textContent === "Open Playit setup").href, claim_url);
}
for (const claim_url of [
  "https://playit.gg/claim/0123abcde",
  "https://playit.gg/claim/0123abcdef0",
  "https://playit.gg/claim/0123abcdeg",
  "https://playit.gg/claim/0123abcdef?secret=x",
  "https://playit.gg/claim/0123abcdef/path",
  "http://playit.gg/claim/0123abcdef",
  "https://evil.test/claim/0123abcdef",
  "https://playit.gg/claim/",
  "prefixhttps://playit.gg/claim/0123abcdef",
  "https://playit.gg/claim/0123abcdefsuffix",
  "https://playit.gg/claim/0123abcdef\n",
]) {
  playitSetup = { state: "waiting", claim_url };
  assert(!render(base).some((n) => n.textContent === "Open Playit setup"));
}
playitSetup = { state: "failed" };
assert(!render(base).some((n) => n.textContent === "Set up Playit"));
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("card behavior: %v\n%s", err, output)
	}
	css, err := assets.ReadFile("static/network-workspace.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".network-provider-card .button-link{display:inline-flex;") || !strings.Contains(string(css), "button.warning{background:var(--jv-warning)") {
		t.Fatal("provider anchor lost normal button styling")
	}
}

func TestRemoteAccessPollingIsBoundedAndStopsWhenStable(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "  const pollRemoteAccess =")
	end := strings.Index(script, "  const runRemoteAction =")
	if start < 0 || end <= start {
		t.Fatal("bounded remote polling missing")
	}
	program := `
const assert = require("node:assert/strict");
let now = 0, polls = 0, remotePollSequence = 0;
const pendingDashboards = new Set(), providerProgress = new Map();
let playitSetup = { state: "idle" }, remoteProviders = [{ service_state: "activating" }];
const dialog = { open: true }, state = {};
const isAdministrator = () => true;
const providerTransitional = (p) => ["activating", "deactivating", "reloading"].includes(p.service_state);
const setupRunning = () => ["starting", "waiting"].includes(playitSetup.state);
Date.now = () => now;
const setTimeout = (callback, interval) => { now += interval; callback(); };
let remoteError = "";
const failPlayitPopup = () => {}, renderNetwork = () => {};
let refreshRemoteAccess = async () => { polls++; if (polls === 2) remoteProviders = [{ service_state: "active" }]; };
` + script[start:end] + `
(async () => {
  await pollRemoteAccess(); assert.equal(polls, 2);
  polls = 0; now = 0; remoteProviders = [{ service_state: "deactivating" }];
  refreshRemoteAccess = async () => { polls++; };
  await pollRemoteAccess(); assert(polls > 0 && polls <= 27); assert(now >= 20000);
  polls = 0; now = 0; playitSetup = { state: "waiting" };
  await pollRemoteAccess(); assert(polls > 0 && polls <= 305); assert(now >= 610000);
  polls = 0; dialog.open = false;
  await pollRemoteAccess(); assert.equal(polls, 0);
  dialog.open = true; now = 0; playitSetup = { state: "waiting" };
  refreshRemoteAccess = async () => { polls++; playitSetup = { state: "complete" }; remoteProviders = [{ service_state: "active", configured: true }]; };
  await pollRemoteAccess(); assert.equal(polls, 1); assert(remoteProviders[0].configured);
})().catch((error) => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("polling behavior: %v\n%s", err, output)
	}
}

func TestNetworkOverviewCompactCanonicalRows(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "  const renderOverview =")
	end := strings.Index(script, "  const renderCheckpoint =")
	if start < 0 || end <= start {
		t.Fatal("overview function missing")
	}
	program := `
const assert = require("node:assert/strict");
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.textContent = ""; }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); return node; }
}
const document = { createElement: (tag) => new Element(tag) };
const row = (name, value) => { const e = new Element("row"); e.textContent = name + ": " + value; return e; };
const sectionHeading = (name, value) => { const e = new Element("heading"); e.textContent = name + ": " + value; return e; };
const remoteProviders = [
  { id: "tailscale", summary: "Awaiting approval" },
  { id: "netbird", summary: "Not configured" },
  { id: "playit", summary: "Setup pending", claim_url: "https://playit.gg/claim/0123abcdef" },
];
` + script[strings.Index(script, "  const connectivitySummary ="):strings.Index(script, "  const validPlayitClaim =")] + script[start:end] + `
const snapshot = { connectivity: "full", devices: [
  { kind: "ethernet", interface: "enp1s0", state: "activated", carrier: true, ipv4: { gateway: "192.168.0.1", addresses: [{ address: "192.168.0.28" }] } },
] };
const panel = renderOverview(snapshot);
assert.equal(panel.tag, "section"); assert.equal(panel.children.length, 6);
assert.deepEqual(panel.children.map((node) => node.textContent), [
  "Connectivity: Internet connected", "IPv4: 192.168.0.28", "Ethernet: enp1s0 · Connected",
  "Tailscale: Awaiting approval", "NetBird: Not configured", "Playit: Setup pending",
]);
assert(!JSON.stringify(panel).includes("claim"));
const missing = renderOverview({ devices: [] });
assert.equal(missing.children[1].textContent, "IPv4: Unavailable");
assert.equal(missing.children[2].textContent, "Ethernet: Unavailable");
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("overview behavior: %v\n%s", err, output)
	}
	start = strings.Index(script, `if (selectedTab === "Overview") {`)
	if start < 0 {
		t.Fatal("overview tab missing")
	}
	end = strings.Index(script[start:], `} else if (selectedTab === "Ethernet")`)
	if end < 0 || strings.Count(script[start:start+end], "panel.appendChild(") != 1 || strings.Contains(script[start:start+end], "Interfaces") {
		t.Fatal("overview contains a second panel")
	}
	if strings.Contains(script[strings.Index(script, "  const renderOverview ="):strings.Index(script, "  const renderCheckpoint =")], "claim_url") {
		t.Fatal("overview reads administrator setup data")
	}
	if !strings.Contains(script, `if (isAdministrator() && selectedTab === "Remote Access")`) || !strings.Contains(script, `if (selectedTab === "Remote Access") playitSetup = await requestJSON`) {
		t.Fatal("overview setup fetch guard missing")
	}
}

func TestDrawerRetainsNetworkBlockAndAddsCanonicalPlayitRow(t *testing.T) {
	source, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, `    const overlays = quickLook.querySelector("[data-quick-look-overlays]");`)
	if start < 0 {
		t.Fatal("existing drawer status block missing")
	}
	end := strings.Index(script[start:], `    setValue("[data-quick-look-backup]"`)
	if end < 0 {
		t.Fatal("existing drawer status block missing")
	}
	program := `
const assert = require("node:assert/strict");
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.textContent = ""; }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); }
  replaceChildren() { this.children = []; }
}
const document = { createElement: (tag) => new Element(tag) };
const overlays = new Element("div");
const quickLook = { querySelector: () => overlays };
const render = (providers) => {
` + strings.Replace(script[start:start+end], `    const overlays = quickLook.querySelector("[data-quick-look-overlays]");`, "", 1) + `
  return overlays.children.map((line) => line.children.map((cell) => cell.textContent));
};
assert.deepEqual(render([]), [["Tailscale", "Unavailable"], ["NetBird", "Unavailable"], ["Playit", "Unavailable"]]);
assert.deepEqual(render(["tailscale", "netbird", "playit"].map((id) => ({ id, summary: "Not configured", connected: true, configured: true, service_active: true }))),
  [["Tailscale", "Not configured"], ["NetBird", "Not configured"], ["Playit", "Not configured"]]);
assert.deepEqual(render([{ id: "tailscale", summary: "Awaiting approval" }, { id: "netbird", summary: "Stopped" }, { id: "playit", summary: "Running" }]),
  [["Tailscale", "Awaiting approval"], ["NetBird", "Stopped"], ["Playit", "Running"]]);
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("drawer behavior: %v\n%s", err, output)
	}
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(header), `quick-look-tile quick-look-network-tile`) != 1 || !strings.Contains(string(header), `<span>IPv4</span><strong data-quick-look-ipv4>`) || !strings.Contains(string(header), `data-quick-look-overlays`) {
		t.Fatal("existing drawer block changed")
	}
	if !strings.Contains(script, `fetch("/api/network/remote-access"`) {
		t.Fatal("drawer does not consume canonical status")
	}
}

func TestReadOnlyRemoteAccessProxyUsesStatusWithoutSetup(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		fake := &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: role}}
		app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/network/remote-access", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || fake.calls != 0 || strings.Contains(rr.Body.String(), "claim") {
			t.Fatalf("role %s status=%d setup calls=%d body=%s", role, rr.Code, fake.calls, rr.Body.String())
		}
	}
}

func TestNetworkVisibleTabsAndConnectivityPresentation(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "  const effectiveTabs =")
	end := strings.Index(script, "  const validPlayitClaim =")
	validation := `    const tabs = effectiveTabs(currentSnapshot);
    if (!tabs.includes(selectedTab)) selectedTab = "Overview";`
	if start < 0 || end <= start || !strings.Contains(script, validation) || strings.Count(script, "const tabs = effectiveTabs(currentSnapshot);") != 2 {
		t.Fatal("rendering, selection and keyboard navigation must share visible tabs")
	}
	keyboardStart := strings.Index(script, `  content.addEventListener("keydown", (event) => {`)
	keyboardEnd := strings.Index(script[keyboardStart:], `  content.addEventListener("submit"`)
	program := `
const assert = require("node:assert/strict");
` + script[start:end] + `
const all = ["Overview", "Ethernet", "Wi-Fi", "Troubleshoot", "Remote Access"];
let currentSnapshot = { workspace_tabs: all, devices: [{ kind: "ethernet" }], wireless_enabled: true };
assert.deepEqual(effectiveTabs(currentSnapshot), ["Overview", "Ethernet", "Troubleshoot", "Remote Access"]);
for (const state of ["disconnected", "unavailable"]) {
  assert.deepEqual(effectiveTabs({ workspace_tabs: all, devices: [{ kind: "wifi", state }], wireless_enabled: false }), all);
}
assert.deepEqual(effectiveTabs({ workspace_tabs: ["Overview", "Troubleshoot"], devices: [{ kind: "wifi" }] }), ["Overview", "Troubleshoot"]);
let selectedTab = "Wi-Fi", connectionDraft = null;
const renderNetwork = () => {
` + validation + `
};
renderNetwork(); assert.equal(selectedTab, "Overview");
let onKey;
const content = { addEventListener: (_, callback) => { onKey = callback; }, querySelector: () => null };
const refreshRemoteAccess = async () => {}, pollRemoteAccess = () => {}, state = {};
` + script[keyboardStart:keyboardStart+keyboardEnd] + `
const key = (key) => onKey({ key, target: { closest: () => ({}) }, preventDefault() {} });
selectedTab = "Ethernet"; key("ArrowRight"); assert.equal(selectedTab, "Troubleshoot");
key("ArrowLeft"); assert.equal(selectedTab, "Ethernet");
key("End"); assert.equal(selectedTab, "Remote Access");
key("ArrowRight"); assert.equal(selectedTab, "Overview");
key("ArrowLeft"); assert.equal(selectedTab, "Remote Access");
key("Home"); assert.equal(selectedTab, "Overview");
for (const [value, expected] of Object.entries({ full: "Internet connected", limited: "Limited connectivity", portal: "Sign-in network detected", none: "No internet connection", unknown: "Network status unknown" })) {
  assert.equal(connectivitySummary(value), expected);
}
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("visible tabs: %v\n%s", err, output)
	}
	if !strings.Contains(script, `row("NetworkManager connectivity", connectivitySummary(snapshot.connectivity))`) {
		t.Fatal("troubleshoot must use friendly connectivity wording")
	}
}

func TestPlayitActivationOpensPopupBeforeSetup(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	stylesheet, err := assets.ReadFile("static/playit-setup.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	for _, token := range []string{"color-scheme:dark", "max-width:580px", "border-radius:18px", "radial-gradient", "background:#0d1822", ".pulse-dot", "@keyframes setup-pulse", "prefers-reduced-motion", ".failure", "@media (prefers-reduced-motion: reduce){.pulse-dot{animation:none}}"} {
		if !strings.Contains(css, token) {
			t.Fatalf("missing popup stylesheet design token: %s", token)
		}
	}
	for _, forbidden := range []string{"@import", "url("} {
		if strings.Contains(strings.ToLower(css), forbidden) {
			t.Fatalf("popup stylesheet must not contain %s", forbidden)
		}
	}
	script := string(source)
	start := strings.Index(script, "  const validPlayitClaim =")
	end := strings.Index(script, "  const isAdministrator =")
	clickStart := strings.Index(script, `    const provider = event.target.closest("[data-network-provider]");`)
	clickEnd := strings.Index(script[clickStart:], "    const check =")
	actionStart := strings.Index(script, "  const runRemoteAction =")
	actionEnd := strings.Index(script, "  const submitEthernet =")
	// Both popup documents must remain complete static literals, with no interpolation.
	popupWrites := strings.Split(script[start:end], "playitPopup.document.write(`")
	if len(popupWrites) != 3 || strings.Count(script[start:end], "playitPopup.document.write(") != 2 {
		t.Fatal("popup pages must be written as two fixed HTML literals")
	}
	for _, write := range popupWrites[1:] {
		html, _, ok := strings.Cut(write, "`);")
		if !ok || strings.Contains(html, "${") || strings.Contains(html, "`") {
			t.Fatal("popup HTML must not interpolate or concatenate values")
		}
		if !strings.HasPrefix(html, "<!doctype html>\n<html lang=\"en\">\n<head>") || !strings.HasSuffix(html, "</body>\n</html>") {
			t.Fatal("popup HTML must be a complete document")
		}
	}
	program := `
const assert = require("node:assert/strict");
let playitPopup = null, playitClaimOpened = false, remoteError = "", playitSetup = { state: "idle" }, remoteBusy = false;
` + script[start:end] + `
let events = [], popup, blocked = false, pending, resolveSetup, rejectSetup;
const writes = [], navigations = [];
const window = { open(url, target) {
  events.push("open");
  assert.equal(url, "about:blank"); assert.equal(target, "_blank");
  if (blocked) return null;
  popup = {
    closed: false, closeCalls: 0, html: "", opener: {},
    close() { this.closeCalls++; this.closed = true; },
    location: { replace(url) { navigations.push({ popup, url }); } },
    document: {
      open() { events.push("document.open"); popup.html = ""; },
      write(html) { events.push("document.write"); popup.html += html; writes.push(html); },
      close() { events.push("document.close"); }
    }
  };
  return popup;
} };
let remotePollSequence = 0;
const state = { textContent: "" };
const renderNetwork = () => {}, refreshRemoteAccess = async () => {}, pollRemoteAccess = () => {};
` + strings.Replace(script[actionStart:actionEnd], "const runRemoteAction =", "const executeRemoteAction =", 1) + `
const runRemoteAction = (action) => { pending = executeRemoteAction(action); };
const postForm = (url) => {
  events.push("request"); assert.equal(url, "/api/network/remote-access/playit/setup");
  if (!blocked) {
    assert.match(popup.html, /<h1 id="setup-title">Preparing Playit setup…<\/h1>/);
    assert.equal(popup.opener, null);
  }
  return new Promise((resolve, reject) => { resolveSetup = resolve; rejectSetup = reject; });
};
const remoteProviders = [{ id: "playit", configured: false }];
const providerButton = { disabled: false, dataset: { networkProvider: "playit", action: "activate" } };
const click = () => {
  const event = { target: { closest: () => providerButton } };
` + script[clickStart:clickStart+clickEnd] + `
};
(async () => {
  click();
  const initialPopup = popup;
  assert.deepEqual(events, ["open", "document.open", "document.write", "document.close", "request"]);
  assert.equal(playitPopup, initialPopup); assert.equal(popup.opener, null);
  assert.match(popup.html, /Starting Playit and generating your secure setup link\./);
  assert.match(popup.html, /This page will open Playit automatically when ready\./);
  assert.equal(navigations.length, 0);
  resolveSetup({ state: "starting" });
  await pending;
  const loadingHTML = popup.html;
  assert.match(loadingHTML, /<section class="setup-card" aria-labelledby="setup-title">/);
  assert.match(loadingHTML, /<div class="claim-panel" role="status">/);
  assert.match(loadingHTML, /Claim link/);
  assert.match(loadingHTML, /Waiting for Playit to generate the secure claim link\./);
  assert.match(loadingHTML, /class="pulse-dot" aria-hidden="true"/);
  for (const claim_url of [
    "https://evil.test/claim/0123abcdef", "javascript:alert(1)",
    "https://playit.gg/claim/0123abcde", "https://playit.gg/claim/0123abcdef0",
    "https://playit.gg/claim/0123abcdeg", "https://playit.gg/claim/0123abcdef?secret=x",
    "https://playit.gg/claim/0123abcdef/path", "https://playit.gg/claim/0123abcdef\n",
    "<script>untrusted</script>"
  ]) {
    playitSetup = { state: "waiting", claim_url };
    updatePlayitPopup();
    assert.equal(navigations.length, 0); assert.equal(popup.html, loadingHTML);
  }
  playitSetup.claim_url = "https://playit.gg/claim/0123abcdef";
  updatePlayitPopup();
  assert.deepEqual(navigations, [{ popup: initialPopup, url: "https://playit.gg/claim/0123abcdef" }]);
  assert(playitClaimOpened); assert.equal(playitPopup, null);
  assert.equal(popup.html, loadingHTML);

  events = []; blocked = true; click();
  assert.deepEqual(events, ["open", "request"]);
  resolveSetup({ state: "waiting", claim_url: "https://playit.gg/claim/0123abcdef" });
  await pending; assert.equal(playitPopup, null); assert(!playitClaimOpened);
  assert.equal(navigations.length, 1);
  click(); rejectSetup(Error("<script>backend-secret</script> https://evil.test/claim/0123abcdef"));
  await pending; assert.equal(playitPopup, null);

  blocked = false; click();
  rejectSetup(Error("<script>backend-secret</script> https://evil.test/claim/0123abcdef"));
  await pending;
  const failureHTML = popup.html;
  assert.match(failureHTML, /<h1 id="setup-title">Playit setup could not be started\.<\/h1>/);
  assert.match(failureHTML, /Return to JustVoxel and try Activate again\./);
  assert(!failureHTML.includes("Preparing Playit setup"));
  assert.match(failureHTML, /<section class="setup-card failure" aria-labelledby="setup-title">/);
  for (const html of [loadingHTML, failureHTML]) {
    for (const token of ["PLAYIT SETUP", "JUSTVOXEL", "PLAYIT"]) {
      assert(html.includes(token), "missing popup design token: " + token);
    }
    assert(!html.includes("Login"));
    assert(!html.includes("Create Account"));
    assert(!html.includes("${"));
    assert(!/<(?:button|a|input|script|img)\b/i.test(html));
    assert.match(html, /<style>\s*:root\{color-scheme:dark/);
    assert(!/\b(?:src|style)\s*=|url\(/i.test(html));
    const stylesheetLink = '<link rel="stylesheet" href="/static/playit-setup.css">';
    assert.equal(html.split(stylesheetLink).length - 1, 1);
    assert(html.indexOf("<style>") < html.indexOf(stylesheetLink));
    assert(!/\bhref\s*=/i.test(html.replace(stylesheetLink, "")));
    assert.match(html, /<head>\s*<meta charset="utf-8">\s*<meta name="viewport" content="width=device-width, initial-scale=1">\s*<title>Playit setup<\/title>\s*<style>[\s\S]*?<\/style>\s*<link rel="stylesheet" href="\/static\/playit-setup\.css">\s*<\/head>/);
  }
  assert.equal(playitPopup, null); assert.equal(popup.opener, null);
  closePlayitPopup();
  assert.equal(popup.closeCalls, 0); assert.equal(popup.closed, false);

  click(); resolveSetup({ state: "starting" }); await pending;
  playitSetup = { state: "failed", error: "backend-secret", claim_url: "https://evil.test/claim/0123abcdef" };
  updatePlayitPopup(); closePlayitPopup();
  assert.equal(popup.html, failureHTML); assert.equal(popup.closeCalls, 0);
  assert.equal(playitPopup, null);

  click(); const closedPopup = popup; closedPopup.close();
  const writesBeforeFailure = writes.length;
  rejectSetup(Error("backend-secret")); await pending;
  assert.equal(playitPopup, null); assert.equal(closedPopup.closeCalls, 1);
  assert.equal(writes.length, writesBeforeFailure);
  assert(writes.every((html) => html === loadingHTML || html === failureHTML));
  assert(writes.every((html) => !html.includes("backend-secret") && !html.includes("https://") && !html.includes("<script>")));
})().catch((error) => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Playit activation: %v\n%s", err, output)
	}
}

func TestNetBirdActivationOpensCorrectLoginAndRejectsTailscale(t *testing.T) {
	data, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	validStart := strings.Index(script, "  const validNetbirdLoginURL =")
	validEnd := strings.Index(script, "  const openActivatedDashboards =")
	clickStart := strings.Index(script, `    const provider = event.target.closest("[data-network-provider]");`)
	clickEnd := strings.Index(script[clickStart:], "    const check =")
	if validStart < 0 || validEnd <= validStart || clickStart < 0 || clickEnd < 0 {
		t.Fatal("NetBird login code missing")
	}
	program := `
const assert = require("node:assert/strict");
let remoteBusy = false, netbirdLoginURL = "", pending, loginTab, responseURL, responseError;
let blocked = false, events = [];
const remoteProviders = [{ id: "netbird", service_active: false, connected: false, configured: false }];
const providerProgress = new Map();
const window = { open(url, target) {
  events.push("open");
  assert.equal(url, "about:blank");
  assert.equal(target, "_blank");
  if (blocked) return null;
  loginTab = { closed: false, opener: {}, navigations: [], html: "",
    location: { replace(url) { loginTab.navigations.push(url); } },
    close() { this.closed = true; },
    document: {
      open() { events.push("document.open"); loginTab.html = ""; },
      write(html) { events.push("document.write"); loginTab.html += html; },
      close() { events.push("document.close"); }
    }
  };
  return loginTab;
} };
const postForm = async (path, params) => {
  events.push("request");
  assert.equal(path, "/api/network/remote-access/netbird");
  assert.deepEqual(params, { action: "activate" });
  if (!blocked) {
    assert.match(loginTab.html, /Preparing NetBird login/);
    assert.match(loginTab.html, /Waiting for the authentication link/);
    assert.equal(loginTab.opener, null);
  }
  if (responseError) throw Error(responseError);
  return { login_url: responseURL };
};
const runRemoteAction = (action) => { pending = action(); };
const button = { disabled: false, dataset: { networkProvider: "netbird", action: "activate" } };
` + script[validStart:validEnd] + `
assert(validNetbirdLoginURL("https://app.netbird.io/verify?user_code=abc"));
assert(validNetbirdLoginURL("https://idp.example.org/authorize?state=123"));
assert(!validNetbirdLoginURL("https://login.tailscale.com/a/123"));
assert(!validNetbirdLoginURL("https://console.tailscale.com/admin/"));
assert(!validNetbirdLoginURL("javascript:alert(1)"));
const click = () => {
  const event = { target: { closest: (selector) => selector === "[data-network-provider]" ? button : null } };
` + script[clickStart:clickStart+clickEnd] + `
};
(async () => {
  responseURL = "https://app.netbird.io/verify?user_code=abc";
  click();
  assert.deepEqual(events.slice(0, 5), ["open", "document.open", "document.write", "document.close", "request"]);
  assert.match(loginTab.html, /<h1 id="setup-title">Preparing NetBird login…<\\/h1>/);
  assert.match(loginTab.html, /class="pulse-dot"/);
  assert.match(loginTab.html, /JUSTVOXEL/);
  assert.match(loginTab.html, /static\\/playit-setup.css/);
  assert(!loginTab.html.includes(responseURL));
  await pending;
  assert.deepEqual(loginTab.navigations, [responseURL]);
  assert.equal(loginTab.opener, null);
  assert.equal(netbirdLoginURL, responseURL);

  responseURL = "https://login.tailscale.com/a/123";
  click();
  await assert.rejects(pending, /invalid login link/);
  assert.deepEqual(loginTab.navigations, []);
  assert.equal(loginTab.closed, false);
  assert.match(loginTab.html, /Remote access setup could not continue/);
  assert(!loginTab.html.includes(responseURL));

  responseURL = "https://app.netbird.io/verify?user_code=abc";
  responseError = "private backend message <script>alert(1)</script>";
  click();
  await assert.rejects(pending, /private backend message/);
  assert.match(loginTab.html, /Remote access setup could not continue/);
  assert(!loginTab.html.includes("private backend message"));
  assert(!loginTab.html.includes("<script>"));
  responseError = "";

  responseURL = "";
  click();
  await pending;
  assert.equal(loginTab.closed, true);

  blocked = true;
  responseURL = "https://app.netbird.io/verify?user_code=abc";
  click();
  await pending;
  assert.equal(netbirdLoginURL, responseURL);
})().catch((error) => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("NetBird browser redirect: %v\n%s", err, output)
	}
}

func TestTailscaleActivationOpensMachineLink(t *testing.T) {
	data, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	validStart := strings.Index(script, "  const validTailscaleLoginURL =")
	validEnd := strings.Index(script, "  const providerDashboards =")
	popupStart := strings.Index(script, "  const renderRemoteLoginPopup =")
	popupEnd := strings.Index(script, "  const openActivatedDashboards =")
	clickStart := strings.Index(script, `    const provider = event.target.closest("[data-network-provider]");`)
	clickEnd := strings.Index(script[clickStart:], "    const check =")
	if validStart < 0 || validEnd <= validStart || popupStart < 0 || popupEnd <= popupStart || clickStart < 0 || clickEnd < 0 {
		t.Fatal("Tailscale login UI path missing")
	}
	program := `
const assert = require("node:assert/strict");
let remoteBusy = false, tailscaleLoginURL = "", pending;
let popup, events = [], responseURL = "https://login.tailscale.com/a/0123456789abcdef", responseError = "";
const remoteProviders = [{ id: "tailscale", service_active: true, connected: false }];
const providerProgress = new Map();
const providerDashboards = { tailscale: "https://console.tailscale.com/admin/" };
const window = { open(url, target) {
  events.push(["open", url]);
  assert.equal(target, "_blank");
  popup = { closed: false, opener: {}, html: "", location: { replace(url) { events.push(["navigate", url]); } },
    close() { this.closed = true; },
    document: {
      open() { events.push(["document.open"]); popup.html = ""; },
      write(html) { events.push(["document.write"]); popup.html += html; },
      close() { events.push(["document.close"]); }
    }
  };
  return popup;
} };
const postForm = async (path, params) => {
  events.push(["request", path]);
  assert.deepEqual(params, { action: "activate" });
  assert.match(popup.html, /Preparing Tailscale login/);
  assert.equal(popup.opener, null);
  if (responseError) throw Error(responseError);
  return { login_url: responseURL };
};
const runRemoteAction = (action) => { pending = action(); };
const button = { disabled: false, dataset: { networkProvider: "tailscale", action: "activate" } };
` + script[validStart:validEnd] + script[popupStart:popupEnd] + `
const click = () => {
  const event = { target: { closest: (selector) => selector === "[data-network-provider]" ? button : null } };
` + script[clickStart:clickStart+clickEnd] + `
};
(async () => {
  click();
  assert.deepEqual(events.slice(0, 5), [
    ["open", "about:blank"], ["document.open"], ["document.write"], ["document.close"],
    ["request", "/api/network/remote-access/tailscale"]
  ]);
  assert.match(popup.html, /Preparing Tailscale login/);
  assert.match(popup.html, /Waiting for the authentication link/);
  assert.match(popup.html, /class="pulse-dot"/);
  await pending;
  assert.deepEqual(events[5], ["navigate", responseURL]);
  assert.equal(popup.opener, null);
  assert.equal(tailscaleLoginURL, responseURL);
  assert(!events.some((event) => event.includes("https://console.tailscale.com/admin/")));

  responseError = "secret activation failure";
  click();
  await assert.rejects(pending, /secret activation failure/);
  assert.match(popup.html, /Remote access setup could not continue/);
  assert(!popup.html.includes("secret activation failure"));
  assert.equal(popup.closed, false);
  responseError = "";

  responseURL = "";
  click();
  await pending;
  assert.equal(popup.closed, true);
})().catch((error) => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Tailscale browser login: %v\n%s", err, output)
	}
}

func TestProviderDashboardWaitsForSuccessfulServiceActivation(t *testing.T) {
	data, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	start := strings.Index(script, "  const providerDashboards =")
	end := strings.Index(script, "  let remotePollSequence =")
	if start < 0 || end <= start {
		t.Fatal("Provider dashboard flow missing")
	}
	program := `
const assert = require("node:assert/strict");
let remoteProviders = [];
const providerTransitional = (provider) => ["activating", "deactivating", "reloading", "refreshing"].includes(provider.service_state);
let blocked = false, opens = [], popup;
const window = { open(url, target) {
  assert.equal(target, "_blank");
  assert.notEqual(url, "about:blank");
  opens.push(url);
  popup = blocked ? null : { opener: {} };
  return popup;
} };
` + script[start:end] + `
for (const id of ["tailscale", "netbird"]) {
  opens = [];
  const provider = { id, service_state: "activating", service_active: false, configured: false, connected: false };
  remoteProviders = [provider]; pendingDashboards.add(id);
  openActivatedDashboards(); assert.equal(opens.length, 0);
  assert(pendingDashboards.has(id));
  provider.service_state = "active"; provider.service_active = true;
  openActivatedDashboards();
  assert.deepEqual(opens, [providerDashboards[id]]);
  assert.equal(popup.opener, null);
  assert.equal(provider.configured, false); assert.equal(provider.connected, false);
  assert(!pendingDashboards.has(id));
  openActivatedDashboards(); assert.equal(opens.length, 1);

  blocked = true; pendingDashboards.add(id); openActivatedDashboards();
  assert.match(providerProgress.get(id), /Use Open provider dashboard/);
  assert.equal(provider.configured, false); assert.equal(provider.connected, false);
  blocked = false;
  provider.service_active = false; provider.service_state = "failed";
  const previous = opens.length;
  pendingDashboards.add(id); openActivatedDashboards();
  assert.equal(opens.length, previous);
  assert.match(providerProgress.get(id), /has not completed/);
}
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Provider dashboard flow: %v\n%s", err, output)
	}
}
