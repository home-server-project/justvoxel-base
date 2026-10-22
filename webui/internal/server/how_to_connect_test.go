package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type connectionGuideAPI struct {
	fakeAPI
	remote      api.RemoteAccessStatus
	identity    api.SessionInfo
	identityErr error
	statusErr   error
	remoteErr   error
	remoteCalls int
}

func (f *connectionGuideAPI) Session(ctx context.Context, session string) (api.SessionInfo, error) {
	if f.identityErr != nil {
		return api.SessionInfo{}, f.identityErr
	}
	if f.identity.Role != "" {
		return f.identity, nil
	}
	return f.fakeAPI.Session(ctx, session)
}
func (f *connectionGuideAPI) Status(ctx context.Context, session string) (api.Status, error) {
	if f.statusErr != nil {
		return api.Status{}, f.statusErr
	}
	return f.fakeAPI.Status(ctx, session)
}
func (f *connectionGuideAPI) RemoteAccessStatus(context.Context, string) (api.RemoteAccessStatus, error) {
	f.remoteCalls++
	return f.remote, f.remoteErr
}
func guideFixture() api.Status {
	status := defaultStatus()
	status.Minecraft.ServerSoftware = "Paper"
	status.Minecraft.JavaPort = 25571
	status.Minecraft.BedrockPort = 19139
	return status
}
func guidePage(t *testing.T, client *connectionGuideAPI, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/minecraft/how-to-connect", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response
}
func TestConnectionGuideAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name     string
		client   connectionGuideAPI
		cookies  []*http.Cookie
		location string
		code     int
	}{
		{name: "unauthenticated", location: "/login", code: 303},
		{name: "expired", cookies: []*http.Cookie{{Name: sessionCookie, Value: "expired"}}, location: "/login", code: 303},
		{name: "password cookie", cookies: []*http.Cookie{{Name: sessionCookie, Value: "session-token"}, {Name: mustChangeCookie, Value: "1"}}, location: "/password", code: 303},
		{name: "authoritative password", client: connectionGuideAPI{identity: api.SessionInfo{Role: "viewer", MustChange: true}}, cookies: []*http.Cookie{{Name: sessionCookie, Value: "session-token"}}, location: "/password", code: 303},
		{name: "session password error", client: connectionGuideAPI{identityErr: api.ErrPasswordChangeRequired}, cookies: []*http.Cookie{{Name: sessionCookie, Value: "session-token"}}, location: "/password", code: 303},
		{name: "status password error", client: connectionGuideAPI{statusErr: api.ErrPasswordChangeRequired}, cookies: []*http.Cookie{{Name: sessionCookie, Value: "session-token"}}, location: "/password", code: 303},
		{name: "unknown role", client: connectionGuideAPI{identity: api.SessionInfo{Role: "unknown"}}, cookies: []*http.Cookie{{Name: sessionCookie, Value: "session-token"}}, code: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := guidePage(t, &tc.client, tc.cookies...)
			if response.Code != tc.code || response.Header().Get("Location") != tc.location {
				t.Fatalf("status %d location %q", response.Code, response.Header().Get("Location"))
			}
			if tc.client.remoteCalls != 0 {
				t.Fatal("unauthorized discovery")
			}
		})
	}
}

func TestConnectionGuideRemoteStatusPasswordBoundary(t *testing.T) {
	client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: guideFixture()}, remoteErr: api.ErrPasswordChangeRequired}
	response := guidePage(t, client, &http.Cookie{Name: sessionCookie, Value: "session-token"})
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/password" || strings.Contains(response.Body.String(), "JustVoxel Connection Guide") {
		t.Fatal("remote status bypassed the password-change boundary")
	}
}
func TestConnectionGuidePageEditionsAndRoles(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		for _, bedrock := range []bool{false, true} {
			status := guideFixture()
			status.Minecraft.Bedrock = bedrock
			client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: status}, identity: api.SessionInfo{Role: role}, remote: api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "tailscale", Connected: true, IP: "100.75.0.1", DashboardURL: "https://private-auth.example/token"}}}}
			response := guidePage(t, client, &http.Cookie{Name: sessionCookie, Value: "session-token"})
			body := response.Body.String()
			if response.Code != 200 {
				t.Fatalf("%s: %d %s", role, response.Code, body)
			}
			for _, want := range []string{"JustVoxel Connection Guide", "Paper 1.21.8", "192.0.2.10", "25571", "100.75.0.1", `role="switch"`, `Save to PDF`} {
				if !strings.Contains(body, want) {
					t.Fatalf("missing %q", want)
				}
			}
			for _, marker := range []string{"Join with Bedrock Edition", "PS5 · Method 1", "PS5 · Method 2", "19139", "45.55.68.52", "<h4>Bedrock</h4>", "Java Edition + Bedrock Edition"} {
				if strings.Contains(body, marker) != bedrock {
					t.Fatalf("bedrock=%v marker %q", bedrock, marker)
				}
			}
			if !bedrock && strings.Contains(body, "Bedrock") {
				t.Fatal("Java-only guide contains Bedrock text")
			}
			if strings.Contains(body, "private-auth") || strings.Contains(body, "25565") {
				t.Fatal("auth URL or default port leaked")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("connection page must not be cached")
			}
		}
	}
}
func TestConnectionGuideUnconfigured(t *testing.T) {
	status := guideFixture()
	status.Minecraft.Configured = false
	client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: status}}
	body := guidePage(t, client, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
	if !strings.Contains(body, "Minecraft is not configured yet") {
		t.Fatal(body)
	}
	for _, absent := range []string{"Save to PDF", "data-connect-method", "192.0.2.10", "25571", "JustVoxel Connection Guide"} {
		if strings.Contains(body, absent) {
			t.Fatalf("unconfigured rendered %q", absent)
		}
	}
	if client.remoteCalls != 0 {
		t.Fatal("unnecessary remote discovery")
	}
}
func TestConnectionGuideAvailability(t *testing.T) {
	for _, id := range []string{"local", "tailscale", "netbird"} {
		for _, value := range []string{"192.168.1.5", "100.80.0.8", "", "0.0.0.0", "127.0.0.1", "169.254.1.2", "224.0.0.1", "https://auth.example"} {
			for _, connected := range []bool{false, true} {
				status := guideFixture()
				status.System.IPv4 = value
				remote := api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: id, Connected: connected, IP: value, Summary: "Not connected"}}}
				guide := newConnectionGuide(status, remote, "")
				for _, method := range guide.Methods {
					if method.ID == id {
						want := usableConnectionIP(value, id == "local") && (id == "local" || connected)
						if method.Available != want || method.Default != (id == "local" && want) {
							t.Fatalf("%s %q connected=%v: %+v", id, value, connected, method)
						}
					}
				}
			}
		}
	}
	// Provider IDs are exact; system legacy fields do not substitute for status.
	status := guideFixture()
	status.System.Tailscale = "100.0.0.1"
	guide := newConnectionGuide(status, api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "Tailscale", Connected: true, IP: "100.0.0.1"}}}, "")
	if guide.Methods[2].Available {
		t.Fatal("matched wrong provider")
	}
}

func TestConnectionGuideNetBirdCleanIP(t *testing.T) {
	for _, tc := range []struct {
		ip      string
		address string
	}{
		{"100.90.1.4", "100.90.1.4"},
		{"127.0.0.1", ""},
		{"0.0.0.0", ""},
	} {
		status := guideFixture()
		status.Minecraft.Bedrock = true
		remote := api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "netbird", Connected: true, IP: tc.ip, Summary: "Connected"}}}
		method := newConnectionGuide(status, remote, "").Methods[3]
		if method.Available != (tc.address != "") {
			t.Fatalf("reported IP %q: %+v", tc.ip, method)
		}
		if tc.address == "" {
			continue
		}
		if len(method.Endpoints) != 2 || method.Endpoints[0].Address != tc.address || method.Endpoints[0].Port != "25571" || method.Endpoints[1].Address != tc.address || method.Endpoints[1].Port != "19139" {
			t.Fatalf("reported IP %q: %+v", tc.ip, method.Endpoints)
		}
		body := guidePage(t, &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: status}, remote: remote}, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
		if !strings.Contains(body, `data-connect-method="netbird">`) || !strings.Contains(body, tc.address) || strings.Contains(body, tc.address+"/") {
			t.Fatalf("NetBird host presentation for %q: %s", tc.ip, body)
		}
		remote.Providers[0].Connected = false
		if newConnectionGuide(status, remote, "").Methods[3].Available {
			t.Fatal("disconnected NetBird provider is selectable")
		}
	}
}

func TestConnectionGuideCustomDomainIsClientPresentationOnly(t *testing.T) {
	client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: guideFixture()}}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/minecraft/how-to-connect?domain=injected.example&method=custom", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "injected.example") {
		t.Fatal("server accepted custom-domain presentation state")
	}
	request = httptest.NewRequest(http.MethodPost, "/minecraft/how-to-connect", strings.NewReader("domain=injected.example"))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	response = httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("connection guide accepted a mutation: %d", response.Code)
	}
}
func TestConnectionGuidePlayitMapping(t *testing.T) {
	status := guideFixture()
	status.Minecraft.Bedrock = true
	provider := api.RemoteAccessProvider{ID: "playit", Connected: true, ServiceActive: true, Summary: "2 tunnels", DashboardURL: "secret", Tunnels: []api.PlayitTunnel{
		{DisplayAddress: "java.playit.gg:40123", Destination: "127.0.0.1:25571"},
		{DisplayAddress: "bedrock.playit.gg:50123", Destination: "127.0.0.1:19139"},
		{DisplayAddress: "disabled.playit.gg:44444", Destination: "127.0.0.1:25571", Disabled: true},
		{DisplayAddress: "wrong-port.playit.gg", Destination: "127.0.0.1:25565"},
		{DisplayAddress: "remote.playit.gg", Destination: "192.0.2.99:25571"},
		{DisplayAddress: "unknown.playit.gg", Destination: "udp:19139"},
		{DisplayAddress: "https://claim.example/token", Destination: "127.0.0.1:25571"},
	}}
	remote := api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{provider}}
	method := newConnectionGuide(status, remote, "").Methods[4]
	if !method.Available || len(method.Endpoints) != 2 {
		t.Fatalf("%+v", method)
	}
	if method.Endpoints[0].JoinAddress != "java.playit.gg:40123" || method.Endpoints[0].Port != "40123" || method.Endpoints[1].Edition != "Bedrock" || method.Endpoints[1].Port != "50123" {
		t.Fatalf("%+v", method.Endpoints)
	}
	status.Minecraft.Bedrock = false
	if endpoints := newConnectionGuide(status, remote, "").Methods[4].Endpoints; len(endpoints) != 1 || endpoints[0].Edition != "Java" {
		t.Fatalf("Java-only: %+v", endpoints)
	}
	status.Minecraft.Bedrock = true
	status.Minecraft.BedrockPort = status.Minecraft.JavaPort
	if newConnectionGuide(status, remote, "").Methods[4].Available {
		t.Fatal("ambiguous destination guessed")
	}
	status = guideFixture()
	for _, active := range []bool{false, true} {
		for _, connected := range []bool{false, true} {
			provider.ServiceActive = active
			provider.Connected = connected
			remote.Providers[0] = provider
			if newConnectionGuide(status, remote, "").Methods[4].Available != (active && connected) {
				t.Fatal("inactive tunnels selectable")
			}
		}
	}
	for _, edition := range []string{"Java", "Bedrock"} {
		endpoint, ok := playitConnectionEndpoint("bare.playit.gg", edition)
		if !ok || endpoint.Port != "" || endpoint.JoinAddress != "bare.playit.gg" {
			t.Fatal("bare display address fabricated a port")
		}
	}
}
func TestConnectionGuideControlCenterAndSafety(t *testing.T) {
	header, _ := assets.ReadFile("templates/header.html")
	if !strings.Contains(string(header), `class="control-tile" href="/minecraft/how-to-connect{{if .SelectedMinecraftID}}?server={{.SelectedMinecraftID}}{{end}}" target="_blank" rel="noopener"`) {
		t.Fatal("safe read-only launcher missing")
	}
	js, _ := assets.ReadFile("static/how-to-connect.js")
	for _, forbidden := range []string{"innerHTML", "fetch(", "localStorage", "sessionStorage"} {
		if strings.Contains(string(js), forbidden) {
			t.Fatalf("unsafe presentation behavior %s", forbidden)
		}
	}
	status := guideFixture()
	status.Minecraft.Version = `<script>alert('x')</script>`
	body := guidePage(t, &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: status}}, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
	if strings.Contains(body, status.Minecraft.Version) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("unescaped server label")
	}
}

func TestConnectionGuideRenderedUnavailableSwitches(t *testing.T) {
	status := guideFixture()
	status.System.IPv4 = "0.0.0.0"
	client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: status}, remote: api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{
		{ID: "tailscale", Connected: false, IP: "100.72.0.1", Summary: "Stopped"},
		{ID: "netbird", Connected: true, IP: "", Summary: "Connected"},
		{ID: "playit", Connected: true, ServiceActive: true, Tunnels: []api.PlayitTunnel{{DisplayAddress: "disabled.playit.gg", Destination: "127.0.0.1:25571", Disabled: true}}},
	}}}
	body := guidePage(t, client, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
	for _, id := range []string{"local", "tailscale", "netbird", "playit"} {
		if !strings.Contains(body, `data-connect-method="`+id+`" disabled`) {
			t.Fatalf("%s switch is selectable", id)
		}
		if strings.Contains(body, `data-connect-card="`+id+`"`) {
			t.Fatalf("%s unavailable card rendered", id)
		}
	}
	if strings.Contains(body, "Stopped") {
		t.Fatal("provider status subtitle rendered")
	}
	if !strings.Contains(body, `id="connect-domain" type="text"`) || !strings.Contains(body, `aria-describedby="connect-domain-help" disabled`) {
		t.Fatal("custom domain must start inactive")
	}
}

func TestConnectionGuidePlayitRenderedPublicAddresses(t *testing.T) {
	status := guideFixture()
	status.Minecraft.Bedrock = true
	client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: status}, remote: api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{
		ID: "playit", Connected: true, ServiceActive: true, DashboardURL: "https://secret.example", Summary: "https://claim.example/token", Tunnels: []api.PlayitTunnel{
			{DisplayAddress: "java.playit.gg:40500", Destination: "127.0.0.1:25571"},
			{DisplayAddress: "bedrock.playit.gg:40501", Destination: "127.0.0.1:19139"},
			{DisplayAddress: "disabled.playit.gg:40502", Destination: "127.0.0.1:19139", Disabled: true},
		},
	}}}}
	body := guidePage(t, client, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
	for _, want := range []string{"java.playit.gg:40500", "bedrock.playit.gg", "40501"} {
		if !strings.Contains(body, want) {
			t.Fatalf("public connection %q missing", want)
		}
	}
	for _, absent := range []string{"disabled.playit.gg", "java.playit.gg:25571", "bedrock.playit.gg:19139", "secret.example", "claim.example", "127.0.0.1"} {
		if strings.Contains(body, absent) {
			t.Fatalf("invalid or private value %q exposed", absent)
		}
	}
	// An identified Java tunnel does not invent a Bedrock counterpart.
	client.remote.Providers[0].Tunnels = client.remote.Providers[0].Tunnels[:1]
	guide := newConnectionGuide(status, client.remote, "")
	if len(guide.Methods[4].Endpoints) != 1 || guide.Methods[4].Endpoints[0].Edition != "Java" {
		t.Fatal("invented Bedrock tunnel")
	}
}

func TestConnectionGuideApprovedVisualContract(t *testing.T) {
	client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: guideFixture()}}
	body := guidePage(t, client, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
	for _, absent := range []string{"connect-navigation", "/static/justvoxel-logo.png", "Minecraft Friends Connection Guide", "LET’S PLAY TOGETHER", ">Server<", "Enter your hostname", "Hostname / domain", "connect-switch", "data-connect-message", "<small>Available", "<small>Not connected", "<small>Stopped", "<small>Connected", "Save as PDF", "Preview the guide below"} {
		if strings.Contains(body, absent) {
			t.Fatalf("unapproved presentation %q", absent)
		}
	}
	for _, want := range []string{`class="connect-controls panel"`, `class="connect-title-row title-row"`, `class="connect-server" aria-label="Server"`, `class="connect-print-logo" src="/static/justvoxel-login-logo.png"`, "JustVoxel Connection Guide", "Current server — Paper 1.21.8", "Choose how your friends will join, then save the connection guide to share.", "Hostname only — no https:// or port."} {
		if !strings.Contains(body, want) {
			t.Fatalf("approved presentation missing %q", want)
		}
	}
	guideStart := strings.Index(body, `<article class="connect-guide"`)
	if guideStart < 0 || strings.Contains(body[:guideStart], "<img") || strings.Count(body, "<img") != 1 {
		t.Fatal("logo must exist only in the shared preview/print guide")
	}
	if strings.Count(body, `data-connect-guide`) != 1 || !strings.Contains(body, `<article class="connect-guide" data-connect-guide hidden>`) {
		t.Fatal("preview and print must share one guide with visibility controlled by readiness")
	}
	controlsStart := strings.Index(body, `<div class="connect-methods">`)
	if controlsStart < 0 {
		t.Fatal("method grid missing")
	}
	actionsStart := strings.Index(body, `<div class="connect-actions connect-domain">`)
	if actionsStart <= controlsStart || actionsStart >= guideStart {
		t.Fatal("bottom action lane must follow the method grid")
	}
	controls := body[controlsStart:actionsStart]
	previous := -1
	for _, id := range []string{"local", "playit", "tailscale", "netbird"} {
		position := strings.Index(controls, `data-connect-method="`+id+`"`)
		if position <= previous {
			t.Fatalf("method %s is missing or out of row order", id)
		}
		previous = position
	}
	if strings.Count(controls, `class="connect-method"`) != 4 || strings.Count(controls, `role="switch"`) != 4 || strings.Contains(controls, "<small") || strings.Contains(controls, "<span") {
		t.Fatal("method cells must contain only their label and shared switch")
	}
	label := strings.Index(body, `<label for="connect-domain">Custom domain</label>`)
	input := strings.Index(body, `id="connect-domain" type="text"`)
	switchInput := strings.Index(body, `id="connect-custom" type="checkbox" role="switch"`)
	if label < 0 || input <= label || switchInput <= input || label <= actionsStart || switchInput >= guideStart {
		t.Fatal("bottom action lane must contain label, input, switch in that order")
	}
	actionsEnd := strings.Index(body[actionsStart:guideStart], "</div>")
	if actionsEnd < 0 {
		t.Fatal("bottom action lane is not closed")
	}
	actionsEnd += actionsStart
	button := strings.Index(body, `<button type="button" data-connect-print disabled>Save to PDF</button>`)
	if button <= switchInput || button >= actionsEnd || strings.Contains(body[actionsStart:actionsEnd], "<p") {
		t.Fatal("Save to PDF must follow the custom switch in the same lane without a status sentence")
	}
	buttonEnd := button + len(`<button type="button" data-connect-print disabled>Save to PDF</button>`)
	if strings.TrimSpace(body[buttonEnd:actionsEnd]) != `<small class="muted" id="connect-domain-help">Hostname only — no https:// or port.</small>` {
		t.Fatal("Save to PDF must end the primary row, followed only by the hostname helper on its own row")
	}
	login, err := assets.ReadFile("templates/login.html")
	if err != nil || !strings.Contains(string(login), `src="/static/justvoxel-login-logo.png"`) {
		t.Fatal("print guide must use the exact login logo")
	}
	if !strings.Contains(body, `height="724"><p>Current server — Paper 1.21.8</p></div><h1>JustVoxel Connection Guide</h1>`) {
		t.Fatal("print header must retain the logo, current server and exact guide title")
	}
}

func TestConnectionGuideSelectedFriendlyNameAndInvitation(t *testing.T) {
	app, client := newServerInventoryApp(t)
	for _, name := range []string{"Family Survival", "Renamed Family", "", "   ", "<script>bad</script>"} {
		client.instances[2].DisplayName = name
		client.reads = nil
		response := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "/minecraft/how-to-connect?server=jv-family1&invite=1", ""))
		body := response.Body.String()
		if response.Code != http.StatusOK {
			t.Fatalf("status %d: %s", response.Code, body)
		}
		label := strings.TrimSpace(name)
		if label == "" {
			label = "Current server"
		}
		if name == "<script>bad</script>" {
			label = "&lt;script&gt;bad&lt;/script&gt;"
		}
		for _, want := range []string{
			label + " — Paper 1.21.8",
			`data-connect-server-name="` + label + `"`,
			`<dialog class="connect-invitation"`,
			`data-connect-invitation-text readonly`,
			`data-connect-copy>Copy invitation</button>`,
			`data-connect-invitation-close>Close</button>`,
			`data-connect-join="192.0.2.10:25567"`,
			`data-connect-endpoint="Bedrock" data-connect-address="192.0.2.10" data-connect-port="19134"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("name %q missing %q", name, want)
			}
		}
		guideStart := strings.Index(body, `<article class="connect-guide"`)
		if guideStart < 0 {
			t.Fatal("selected guide missing")
		}
		guide := body[guideStart:]
		if strings.Contains(guide, "jv-") || strings.Contains(guide, "Kids Creative") || strings.Contains(body, "<script>bad</script>") {
			t.Fatal("invitation presentation exposed identity, another server, or unescaped name")
		}
		if strings.Join(client.reads, ",") != "configuration:jv-family1" {
			t.Fatalf("friendly name changed selection: %v", client.reads)
		}
	}
	for _, tc := range []struct {
		query string
		code  int
	}{
		{"?invite=1", http.StatusOK},
		{"?server=Family+Survival&invite=1", http.StatusBadRequest},
		{"?server=", http.StatusBadRequest},
		{"?server=jv-missing&invite=1", http.StatusNotFound},
		{"?server=jv-family1&server=jv-kids001&invite=1", http.StatusBadRequest},
	} {
		client.reads = nil
		response := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "/minecraft/how-to-connect"+tc.query, ""))
		if response.Code != tc.code || len(client.reads) != 0 {
			t.Fatalf("invalid selection %q: status %d reads %v", tc.query, response.Code, client.reads)
		}
	}
}

func TestConnectionGuideOwnServerSelection(t *testing.T) {
	app, client := newServerInventoryApp(t)
	response := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "/minecraft/how-to-connect", ""))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("selection page: %d %s", response.Code, body)
	}
	for _, want := range []string{`<option value="">Choose server</option>`, `value="jv-family1"`, `>family Survival</option>`, `value="jv-kids001"`, `>Kids Creative</option>`, "Choose a server to see its connection guide."} {
		if !strings.Contains(body, want) {
			t.Errorf("selection page missing %q", want)
		}
	}
	for _, absent := range []string{"Choose a Minecraft server.", "Minecraft is not configured yet", "data-connect-guide", "jv-legacy1"} {
		if strings.Contains(body, absent) {
			t.Errorf("selection page contains %q", absent)
		}
	}
	if len(client.reads) != 0 {
		t.Fatalf("unselected page read server data: %v", client.reads)
	}
	response = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "/minecraft/how-to-connect?server=jv-kids001", ""))
	body = response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `value="jv-kids001" selected>Kids Creative</option>`) || !strings.Contains(body, "Kids Creative — Purpur 1.21.8") || !strings.Contains(body, `value="jv-family1"`) {
		t.Fatalf("explicit selection: %d %s", response.Code, body)
	}
	delete(client.runtimes, "jv-family1")
	client.reads = nil
	response = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "/minecraft/how-to-connect", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Kids Creative — Purpur 1.21.8") || strings.Join(client.reads, ",") != "configuration:jv-kids001" {
		t.Fatalf("single automatic selection: %d %v", response.Code, client.reads)
	}
}

func TestDirectServerInvitationUsesOnlyGuideDefaults(t *testing.T) {
	status := guideFixture()
	status.System.IPv4 = "192.0.2.10"
	guide := newConnectionGuide(status, api.RemoteAccessStatus{}, "Family")
	text := defaultConnectionInvitation(guide)
	if !strings.Contains(text, "Join Family on Minecraft\n\nLocal connection IPv4\nJava: 192.0.2.10:25571") {
		t.Fatal(text)
	}
	status.System.IPv4 = ""
	remote := api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "tailscale", Connected: true, IP: "100.80.0.8"}}}
	if text := defaultConnectionInvitation(newConnectionGuide(status, remote, "Family")); text != "" {
		t.Fatal("invented non-default selection", text)
	}
}

func TestDirectServerInvitationIdentityRolesAndPrivacy(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		app, client := newServerInventoryApp(t)
		client.role = role
		response := httptestResponse(app, authenticatedAdminRequest("GET", "/api/minecraft/servers/invitation?server=jv-family1", ""))
		if response.Code != 200 {
			t.Fatal(role, response.Code, response.Body.String())
		}
		var payload map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 2 || payload["name"] != "family Survival" || !strings.Contains(payload["invitation"], "192.0.2.10:25567") || strings.Contains(payload["invitation"], "25568") {
			t.Fatal(payload)
		}
		requireNoPrivateServerContent(t, response.Body.String())
		if len(client.actions) != 0 {
			t.Fatal("invitation mutated runtime")
		}
	}
	app, client := newServerInventoryApp(t)
	for _, query := range []string{"", "?server=family+Survival", "?server=jv-missing1", "?server=jv-family1&server=jv-kids001"} {
		response := httptestResponse(app, authenticatedAdminRequest("GET", "/api/minecraft/servers/invitation"+query, ""))
		if response.Code < 400 {
			t.Fatal("implicit/invalid selection accepted", query)
		}
	}
	client.configurationErrors["jv-family1"] = true
	response := httptestResponse(app, authenticatedAdminRequest("GET", "/api/minecraft/servers/invitation?server=jv-family1", ""))
	if response.Code < 400 {
		t.Fatal("unknown configuration invited")
	}
	requireNoPrivateServerContent(t, response.Body.String())
	response = httptestResponse(app, httptest.NewRequest("GET", "/api/minecraft/servers/invitation?server=jv-family1", nil))
	if response.Code != 401 {
		t.Fatal("anonymous invitation", response.Code)
	}
}

func TestDirectServerInvitationUnavailableDefault(t *testing.T) {
	app, client := newServerInventoryApp(t)
	status := defaultStatus()
	status.System.IPv4 = ""
	client.statusResult = status
	response := httptestResponse(app, authenticatedAdminRequest("GET", "/api/minecraft/servers/invitation?server=jv-family1", ""))
	if response.Code != 409 || !strings.Contains(response.Body.String(), "No default invitation") || strings.Contains(response.Body.String(), `"invitation":`) {
		t.Fatal("invented address", response.Body.String())
	}
}
