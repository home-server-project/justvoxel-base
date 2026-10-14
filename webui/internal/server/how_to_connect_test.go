package server

import (
	"context"
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
			for _, want := range []string{"JustVoxel Connection Guide", "Paper 1.21.8", "192.0.2.10", "25571", "100.75.0.1", `role="switch"`, `Save as PDF`} {
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
	for _, absent := range []string{"Save as PDF", "data-connect-method", "192.0.2.10", "25571", "JustVoxel Connection Guide"} {
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
				guide := newConnectionGuide(status, remote)
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
	guide := newConnectionGuide(status, api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "Tailscale", Connected: true, IP: "100.0.0.1"}}})
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
		method := newConnectionGuide(status, remote).Methods[3]
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
		if newConnectionGuide(status, remote).Methods[3].Available {
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
	method := newConnectionGuide(status, remote).Methods[4]
	if !method.Available || len(method.Endpoints) != 2 {
		t.Fatalf("%+v", method)
	}
	if method.Endpoints[0].JoinAddress != "java.playit.gg:40123" || method.Endpoints[0].Port != "40123" || method.Endpoints[1].Edition != "Bedrock" || method.Endpoints[1].Port != "50123" {
		t.Fatalf("%+v", method.Endpoints)
	}
	status.Minecraft.Bedrock = false
	if endpoints := newConnectionGuide(status, remote).Methods[4].Endpoints; len(endpoints) != 1 || endpoints[0].Edition != "Java" {
		t.Fatalf("Java-only: %+v", endpoints)
	}
	status.Minecraft.Bedrock = true
	status.Minecraft.BedrockPort = status.Minecraft.JavaPort
	if newConnectionGuide(status, remote).Methods[4].Available {
		t.Fatal("ambiguous destination guessed")
	}
	status = guideFixture()
	for _, active := range []bool{false, true} {
		for _, connected := range []bool{false, true} {
			provider.ServiceActive = active
			provider.Connected = connected
			remote.Providers[0] = provider
			if newConnectionGuide(status, remote).Methods[4].Available != (active && connected) {
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
	if !strings.Contains(string(header), `class="control-tile" href="/minecraft/how-to-connect" target="_blank" rel="noopener"`) {
		t.Fatal("safe read-only launcher missing")
	}
	js, _ := assets.ReadFile("static/how-to-connect.js")
	for _, forbidden := range []string{"innerHTML", "fetch(", "localStorage", "sessionStorage"} {
		if strings.Contains(string(js), forbidden) {
			t.Fatalf("unsafe presentation behavior %s", forbidden)
		}
	}
	status := guideFixture()
	status.Minecraft.ServerSoftware = `<script>alert('x')</script>`
	body := guidePage(t, &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: status}}, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
	if strings.Contains(body, status.Minecraft.ServerSoftware) || !strings.Contains(body, "&lt;script&gt;") {
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
	guide := newConnectionGuide(status, client.remote)
	if len(guide.Methods[4].Endpoints) != 1 || guide.Methods[4].Endpoints[0].Edition != "Java" {
		t.Fatal("invented Bedrock tunnel")
	}
}

func TestConnectionGuideApprovedVisualContract(t *testing.T) {
	client := &connectionGuideAPI{fakeAPI: fakeAPI{statusResult: guideFixture()}}
	body := guidePage(t, client, &http.Cookie{Name: sessionCookie, Value: "session-token"}).Body.String()
	for _, absent := range []string{"connect-navigation", "/static/justvoxel-logo.png", "Minecraft Friends Connection Guide", "LET’S PLAY TOGETHER", ">Server<", "Enter your hostname", "Hostname / domain", "connect-switch", "data-connect-message", "<small>Available", "<small>Not connected", "<small>Stopped", "<small>Connected"} {
		if strings.Contains(body, absent) {
			t.Fatalf("unapproved presentation %q", absent)
		}
	}
	for _, want := range []string{`class="connect-controls panel"`, `class="connect-title-row title-row"`, `class="connect-server" aria-label="Server"`, `class="connect-print-logo" src="/static/justvoxel-login-logo.png"`, "JustVoxel Connection Guide", "Current server — Paper 1.21.8", "Hostname only, for example minecraft.example.com. Do not include https:// or a port."} {
		if !strings.Contains(body, want) {
			t.Fatalf("approved presentation missing %q", want)
		}
	}
	guideStart := strings.Index(body, `<article class="connect-guide"`)
	if guideStart < 0 || strings.Contains(body[:guideStart], "<img") || strings.Count(body, "<img") != 1 {
		t.Fatal("logo must exist only in the print guide")
	}
	controlsStart := strings.Index(body, `<div class="connect-methods">`)
	if controlsStart < 0 {
		t.Fatal("method grid missing")
	}
	controls := body[controlsStart:guideStart]
	previous := -1
	for _, id := range []string{"local", "playit", "tailscale", "netbird"} {
		position := strings.Index(controls, `data-connect-method="`+id+`"`)
		if position <= previous {
			t.Fatalf("method %s is missing or out of row order", id)
		}
		previous = position
	}
	if strings.Count(controls, `class="connect-method"`) != 4 || strings.Contains(controls, "<small") || strings.Contains(controls, "<span") {
		t.Fatal("method cells must contain only their label and shared switch")
	}
	label := strings.Index(body, `<label for="connect-domain">Custom domain</label>`)
	input := strings.Index(body, `id="connect-domain" type="text"`)
	switchInput := strings.Index(body, `id="connect-custom" type="checkbox" role="switch"`)
	if label < 0 || input <= label || switchInput <= input || switchInput >= controlsStart {
		t.Fatal("custom domain row must contain label, input, switch in that order")
	}
	if !strings.Contains(body, `<div class="connect-actions"><button type="button" data-connect-print disabled>Save as PDF</button></div>`) {
		t.Fatal("Save as PDF action must have no adjacent status sentence")
	}
}
