package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type networkTestAPI struct {
	role string
	*serverInventoryTestAPI
	network       api.MinecraftNetworkStatus
	writes        int
	reads         int
	whitelistArgs []string
	connection    api.MinecraftConnection
	connectionErr error
}

func (f *networkTestAPI) MinecraftNetwork(context.Context, string) (api.MinecraftNetworkStatus, error) {
	f.reads++
	return f.network, nil
}
func (f *networkTestAPI) ConfigureMinecraftNetwork(_ context.Context, _ string, c api.MinecraftNetworkConfig) error {
	f.network.Config = c
	f.writes++
	return nil
}
func (f *networkTestAPI) RecoverMinecraftNetwork(context.Context, string) error {
	f.writes++
	return nil
}
func (f *networkTestAPI) MinecraftNetworkWhitelist(_ context.Context, _ string, platform, action, name string) error {
	f.whitelistArgs = []string{platform, action, name}
	f.writes++
	return nil
}
func (f *networkTestAPI) MinecraftInstanceConnection(context.Context, string, string) (api.MinecraftConnection, error) {
	return f.connection, f.connectionErr
}
func newNetworkTestApp(t *testing.T) (*App, *networkTestAPI) {
	t.Helper()
	_, base := newServerInventoryApp(t)
	f := &networkTestAPI{serverInventoryTestAPI: base, network: api.MinecraftNetworkStatus{Config: api.MinecraftNetworkConfig{WhitelistEnforced: true, JavaPort: 25565, BedrockPort: 19132, WelcomeMessage: "Welcome"}, WhitelistEnforced: true, Servers: []api.MinecraftNetworkServer{{ID: "jv-family1", Software: "paper", Eligible: true, State: "stopped"}, {ID: "jv-kids001", Software: "vanilla", State: "stopped"}}}}
	app, err := New(f, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return app, f
}
func TestMinecraftNetworkWorkspaceAndCSRF(t *testing.T) {
	app, f := newNetworkTestApp(t)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/minecraft-network", ""))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Network Whitelist", "family Survival", "Java + Bedrock server selector", "Standalone", `name="members"`, `name="welcome_message"`, `value="configure"`, `value="add"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatal("missing network UI", want)
		}
	}
	for _, forbidden := range []string{"PicoLobby", "NPC", "lobby_engine"} {
		if strings.Contains(rr.Body.String(), forbidden) {
			t.Fatal("legacy lobby UI remains", forbidden)
		}
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/minecraft-network", "csrf=wrong&action=configure"))
	if rr.Code != 403 || f.writes != 0 {
		t.Fatal("CSRF bypass")
	}
}

func TestMinecraftNetworkCompactPresentation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		app, f := newNetworkTestApp(t)
		f.network.Config.Enabled = enabled
		f.network.Config.Members = []string{"jv-family1"}
		f.network.RecoveryRequired = enabled
		f.network.Servers = append(f.network.Servers, api.MinecraftNetworkServer{ID: "unknown", Software: "https://example.invalid/icon.svg", State: "stopped"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/minecraft-network", ""))
		if rr.Code != http.StatusOK {
			t.Fatal(rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		enabledLabel := "Enabled"
		if enabled {
			enabledLabel = "Enabled · Proxy unavailable"
		}
		for _, want := range []string{
			`class="minecraft-network"`, `minecraft-network-panel`,
			`minecraft-network-status-disabled">Disabled</span>`,
			`minecraft-network-status-enabled">` + enabledLabel + `</span>`,
			`class="minecraft-player-controls"`, `class="minecraft-player-add"`,
			`class="minecraft-player-platform"`, `class="minecraft-player-input"`,
			`class="minecraft-whitelist-list"`, `No players on the network whitelist.`,
			`name="action" value="add">Add player</button>`,
			`name="members" value="jv-family1" checked>`,
			`src="/static/server-software/paper.svg" alt="">`,
			`src="/static/server-software/vanilla.svg" alt="">`,
			`minecraft-network-unavailable">Not supported by Minecraft Network</span>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing compact network presentation %q", want)
			}
		}
		for _, server := range f.network.Servers {
			row := regexp.MustCompile(`(?s)name="members" value="` + regexp.QuoteMeta(server.ID) + `".*?</label>`).FindString(body)
			if !server.Eligible {
				if row != "" {
					t.Error("unsupported server has a member checkbox")
				}
				details := regexp.MustCompile(`(?s)<details class="minecraft-network-unsupported">.*?</details>`).FindString(body)
				for _, candidate := range regexp.MustCompile(`(?s)<div class="minecraft-network-server">.*?</div>`).FindAllString(details, -1) {
					if strings.Contains(candidate, " · "+serverSoftware(server.Software)+" · "+server.State) {
						row = candidate
						break
					}
				}
			}
			icon := serverSoftwareIcon(server.Software)
			if icon == "" {
				if strings.Contains(row, "<img") {
					t.Error("unknown software must not render an icon")
				}
				continue
			}
			if !strings.HasPrefix(icon, "/static/server-software/") || !strings.Contains(row, `src="`+icon+`" alt="">`) || !strings.Contains(row, " · "+serverSoftware(server.Software)+" · "+server.State) {
				t.Errorf("server %s must render its own shared icon and text", server.ID)
			}
			if _, err := assets.ReadFile(strings.TrimPrefix(icon, "/")); err != nil {
				t.Errorf("shared icon is missing: %s: %v", icon, err)
			}
		}
		lane := regexp.MustCompile(`(?s)class="minecraft-network-lane">(.*?)<section class="minecraft-network-section"`).FindString(body)
		for _, want := range []string{`class="minecraft-network-ports"`, `name="java_port"`, `value="25565"`, `name="bedrock_port"`, `value="19132"`, `class="minecraft-network-toggle"`} {
			if !strings.Contains(lane, want) {
				t.Errorf("missing port/toggle lane contract %q", want)
			}
		}
		switchTag := `<input class="date-time-switch" role="switch" type="checkbox" name="enabled"`
		applyTag := `<button type="submit" name="action" value="configure" disabled`
		if enabled {
			switchTag += ` checked`
			if !strings.Contains(body, `value="recover"`) {
				t.Error("recovery action missing")
			}
		}
		if !strings.Contains(lane, switchTag+`>`) {
			t.Error("enabled switch contract changed")
		}
		welcome := regexp.MustCompile(`(?s)class="minecraft-network-welcome-lane">(.*?)</div>`).FindString(body)
		if !strings.Contains(welcome, `name="welcome_message" maxlength="512" value="Welcome"`) || !strings.Contains(welcome, applyTag+`>Apply</button>`) {
			t.Error("welcome message and Apply must share a lane and preserve recovery disabling")
		}
		add := regexp.MustCompile(`(?s)<form class="minecraft-player-add".*?</form>`).FindString(body)
		for _, want := range []string{`method="post" action="/workspace/minecraft-network"`, `name="csrf" value="csrf-token"`, `name="platform"`, `value="java">Java Edition`, `value="bedrock">Bedrock`, `name="name" maxlength="16" required`, `value="add">Add player</button>`} {
			if !strings.Contains(add, want) {
				t.Errorf("whitelist add contract changed: %q", want)
			}
		}
		if strings.Contains(body, `>Add<`) || strings.Contains(body, `<h2>Connection</h2>`) {
			t.Error("old oversized presentation remains")
		}
	}
}

func TestMinecraftNetworkWhitelistRemovalPresentation(t *testing.T) {
	app, f := newNetworkTestApp(t)
	f.network.Whitelist = []api.MinecraftNetworkPlayer{{Name: "Alex", Platform: "java"}, {Name: "Steve", Platform: "bedrock"}}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/minecraft-network", ""))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `class="minecraft-whitelist-table"`) || strings.Contains(body, "No players on the network whitelist.") {
		t.Fatal("populated whitelist must use the shared bounded list/table")
	}
	forms := regexp.MustCompile(`(?s)<form method="post" action="/workspace/minecraft-network"><input type="hidden" name="csrf".*?</form>`).FindAllString(body, -1)
	if len(forms) != 2 {
		t.Fatal("expected one removal form per player", len(forms))
	}
	for i, player := range f.network.Whitelist {
		for _, want := range []string{`name="csrf" value="csrf-token"`, `name="name" value="` + player.Name + `"`, `name="platform" value="` + player.Platform + `"`, `name="action" value="remove">Remove</button>`} {
			if !strings.Contains(forms[i], want) {
				t.Errorf("removal contract changed: %q", want)
			}
		}
	}
}

func TestMinecraftNetworkCompactStylesAreScoped(t *testing.T) {
	source, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(source)
	for selector, properties := range map[string][]string{
		".minecraft-network .minecraft-network-servers":                                                      {"display:grid", "repeat(auto-fit,minmax(min(100%,16rem),1fr))"},
		".minecraft-network .minecraft-network-lane":                                                         {"display:flex", "flex-wrap:wrap", "align-items:center"},
		".minecraft-network .minecraft-network-ports":                                                        {"display:flex", "flex-wrap:wrap"},
		".minecraft-network .minecraft-network-ports input":                                                  {"width:6.5rem"},
		".minecraft-network .minecraft-network-toggle":                                                       {"gap:.65rem", "margin:0 0 0 auto", "align-items:center"},
		".minecraft-network .minecraft-network-status-disabled":                                              {"color:#ffc1b8"},
		".minecraft-network .minecraft-network-status-enabled":                                               {"color:#8ad4a9", "display:none"},
		".minecraft-network .minecraft-network-toggle:has(input:checked) .minecraft-network-status-disabled": {"display:none"},
		".minecraft-network .minecraft-network-toggle:has(input:checked) .minecraft-network-status-enabled":  {"display:inline"},
		".minecraft-network .minecraft-network-welcome-lane":                                                 {"display:flex", "align-items:center", "flex-wrap:wrap"},
	} {
		rule := regexp.MustCompile(regexp.QuoteMeta(selector) + `\{([^}]+)\}`).FindString(css)
		for _, property := range properties {
			if !strings.Contains(rule, property) {
				t.Errorf("%s missing %s", selector, property)
			}
		}
	}
	_, compact, ok := strings.Cut(css, "/* Compact Minecraft Network presentation; shared Players rules stay unchanged. */")
	if !ok || !strings.Contains(compact, "@media(max-width:540px)") {
		t.Fatal("missing scoped responsive styles")
	}
	for _, rule := range regexp.MustCompile(`([^{}]+)\{`).FindAllStringSubmatch(compact, -1) {
		selector := strings.TrimSpace(rule[1])
		if strings.HasPrefix(selector, "@media") {
			continue
		}
		for _, part := range strings.Split(selector, ",") {
			if !strings.HasPrefix(strings.TrimSpace(part), ".minecraft-network ") && strings.TrimSpace(part) != ".minecraft-network" {
				t.Errorf("network style escapes workspace: %q", part)
			}
		}
	}
}
func TestMinecraftNetworkConnectionPresentation(t *testing.T) {
	app, f := newNetworkTestApp(t)
	standalone := api.InstanceRuntimeSettings{JavaPort: 25569, BedrockPort: 19136, BedrockEnabled: true}
	f.connection = api.MinecraftConnection{ID: "jv-family1", NetworkMember: true, JavaPort: 25565, BedrockPort: 19132, BedrockEnabled: true}
	got, err := app.minecraftConnectionSettings(context.Background(), "session", "jv-family1", standalone)
	if err != nil || got.JavaPort != 25565 || got.BedrockPort != 19132 {
		t.Fatal(got, err)
	}
	if standalone.JavaPort != 25569 {
		t.Fatal("stored standalone port changed")
	}
	f.connectionErr = errors.New("network recovery")
	if _, err = app.minecraftConnectionSettings(context.Background(), "session", "jv-family1", standalone); err == nil {
		t.Fatal("connection silently fell back to backend")
	}
}

func (f *networkTestAPI) Session(ctx context.Context, session string) (api.SessionInfo, error) {
	identity, err := f.serverInventoryTestAPI.Session(ctx, session)
	if f.role != "" {
		identity.Role = f.role
	}
	return identity, err
}
func (f *networkTestAPI) Status(ctx context.Context, session string) (api.Status, error) {
	status, err := f.serverInventoryTestAPI.Status(ctx, session)
	status.System.IPv4 = "192.168.1.50"
	return status, err
}
func TestMinecraftNetworkRoutesRequireAdministrator(t *testing.T) {
	for _, role := range []string{"viewer", "operator"} {
		app, f := newNetworkTestApp(t)
		f.role = role
		for _, path := range []string{"/minecraft/network", "/workspace/minecraft-network"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				rr := httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(method, "http://example"+path, "csrf=csrf-token&action=recover"))
				if rr.Code != 403 || f.writes != 0 {
					t.Fatal("non-administrator reached Minecraft Network", role, path, method, rr.Code)
				}
			}
		}
	}
}
func TestMinecraftNetworkHowToConnectAndInvitationUseProxy(t *testing.T) {
	app, f := newNetworkTestApp(t)
	f.connection = api.MinecraftConnection{ID: "jv-family1", NetworkMember: true, JavaPort: 25565, BedrockPort: 19132, BedrockEnabled: true}
	for _, path := range []string{"/minecraft/how-to-connect?server=jv-family1", "/api/minecraft/servers/invitation?server=jv-family1"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example"+path, ""))
		if rr.Code != 200 {
			t.Fatal(path, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "192.168.1.50") || !strings.Contains(rr.Body.String(), "19132") {
			t.Fatal("network endpoint absent", path, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "25567") || strings.Contains(rr.Body.String(), "19134") {
			t.Fatal("invitation exposes private backend endpoint", path)
		}
	}
}

func TestMinecraftNetworkAutomaticLifecycleAndEligibilityPresentation(t *testing.T) {
	app, f := newNetworkTestApp(t)
	// Eligibility is authoritative even when the software label would suggest otherwise.
	f.network.Servers[0].Eligible = false
	f.network.Servers[1].Eligible = true
	f.network.Config.Members = []string{f.network.Servers[1].ID}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/minecraft-network", ""))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, obsolete := range []string{"Stop selected servers before", "Start them again when", `name="members" value="jv-family1"`} {
		if strings.Contains(body, obsolete) {
			t.Errorf("obsolete instruction or unsupported toggle: %s", obsolete)
		}
	}
	for _, want := range []string{
		"Running selected servers are stopped automatically. Empty servers stop immediately; occupied servers receive the normal shutdown countdown. Servers that were running are started again after Apply.",
		`name="members" value="jv-kids001" checked>`,
		`<details class="minecraft-network-unsupported">`,
		`<summary>Standalone / unsupported servers (1)</summary>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	details := regexp.MustCompile(`(?s)<details class="minecraft-network-unsupported">.*?</details>`).FindString(body)
	if strings.Contains(details, `name="members"`) || !strings.Contains(details, `/static/server-software/paper.svg`) || !strings.Contains(details, "Not supported by Minecraft Network") {
		t.Fatal("unsupported details must be informational and retain shared icons", details)
	}
	grid := regexp.MustCompile(`(?s)<div class="minecraft-network-servers">.*?</div>`).FindString(body)
	if strings.Contains(grid, "paper.svg") || !strings.Contains(grid, "vanilla.svg") || !strings.Contains(grid, " · Vanilla · stopped") {
		t.Fatal("grid classification did not follow Eligible", grid)
	}
}

func TestMinecraftNetworkRuntimePresentation(t *testing.T) {
	for _, tc := range []struct {
		enabled, ready bool
		label          string
	}{
		{false, false, "Enabled</span>"},
		{true, true, "Enabled</span>"},
		{true, false, "Enabled · Proxy unavailable</span>"},
	} {
		app, f := newNetworkTestApp(t)
		f.network.Config.Enabled, f.network.RuntimeReady = tc.enabled, tc.ready
		rr := httptestResponse(app, authenticatedAdminRequest("GET", "http://example/workspace/minecraft-network", ""))
		body := rr.Body.String()
		if rr.Code != 200 || !strings.Contains(body, `minecraft-network-status-enabled">`+tc.label) {
			t.Fatal(rr.Code, body)
		}
		if strings.Contains(body, `name="enabled" checked`) != tc.enabled {
			t.Fatal("runtime readiness changed configured toggle", body)
		}
		if !tc.enabled && strings.Contains(body, "Proxy unavailable") {
			t.Fatal("disabled presentation changed", body)
		}
	}
}
