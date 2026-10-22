package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type serverInventoryTestAPI struct {
	legacyStatusError bool
	*addServerTestAPI
	states                            map[string]api.MinecraftInstanceStatus
	configurationErrors, statusErrors map[string]bool
	reads, actions                    []string
	inventoryError, actionError       error
}

func (f *serverInventoryTestAPI) Status(ctx context.Context, session string) (api.Status, error) {
	if f.legacyStatusError {
		return api.Status{}, errors.New("private /host/legacy secret")
	}
	return f.fakeDiscoveryAPI.Status(ctx, session)
}
func (f *serverInventoryTestAPI) MinecraftInstanceRuntimes(ctx context.Context, session string) ([]string, error) {
	if f.inventoryError != nil {
		return nil, f.inventoryError
	}
	return f.addServerTestAPI.MinecraftInstanceRuntimes(ctx, session)
}
func (f *serverInventoryTestAPI) MinecraftInstanceConfiguration(ctx context.Context, session, id string) (api.InstanceRuntimeSettings, error) {
	f.reads = append(f.reads, "configuration:"+id)
	if f.configurationErrors[id] {
		return api.InstanceRuntimeSettings{}, errors.New("private /host/config rcon_password=secret")
	}
	return f.addServerTestAPI.MinecraftInstanceConfiguration(ctx, session, id)
}
func (f *serverInventoryTestAPI) MinecraftInstanceStatus(_ context.Context, _, id string) (api.MinecraftInstanceStatus, error) {
	f.reads = append(f.reads, "status:"+id)
	if f.statusErrors[id] {
		return api.MinecraftInstanceStatus{}, errors.New("private /host/runtime token=secret")
	}
	return f.states[id], nil
}
func (f *serverInventoryTestAPI) MinecraftInstanceAction(_ context.Context, _, id, action string) error {
	f.actions = append(f.actions, id+":"+action)
	return f.actionError
}
func newServerInventoryApp(t *testing.T) (*App, *serverInventoryTestAPI) {
	t.Helper()
	_, base := newAddServerTestApp(t)
	base.instances = []api.RegisteredMinecraftInstance{{ID: "jv-legacy1", DisplayName: "Legacy registry name"}, {ID: "jv-kids001", DisplayName: "Kids Creative"}, {ID: "jv-family1", DisplayName: "family Survival"}}
	base.runtimes = map[string]api.InstanceRuntimeSettings{
		"jv-family1": {Software: "paper", Version: "1.21.8", JavaMemory: "2G", ContainerMemory: "3G", JavaPort: 25567, BedrockEnabled: true, BedrockPort: 19134, StartOnBoot: true, MOTD: "Welcome family"},
		"jv-kids001": {Software: "purpur", Version: "1.21.8", JavaPort: 25568, BedrockPort: 19135, MOTD: "Kids only"},
	}
	base.configuration.Minecraft.ServerType = "vanilla"
	base.configuration.Minecraft.Version = "1.21.7"
	f := &serverInventoryTestAPI{addServerTestAPI: base, states: map[string]api.MinecraftInstanceStatus{
		"jv-family1": {ID: "jv-family1", State: "running", StartOnBoot: true},
		"jv-kids001": {ID: "jv-kids001", State: "stopped"},
	}, configurationErrors: map[string]bool{}, statusErrors: map[string]bool{}}
	app, err := New(f, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return app, f
}
func inventoryRequest(app *App, method, path, body string) *httptest.ResponseRecorder {
	target := minecraftServersPath + path
	if path == "" {
		target = "/workspace/minecraft-servers"
	}
	return httptestResponse(app, authenticatedAdminRequest(method, "http://example"+target, body))
}

var htmlTags = regexp.MustCompile(`<[^>]*>`)

func requireNoPrivateServerContent(t *testing.T, body string) {
	t.Helper()
	visible := htmlTags.ReplaceAllString(body, "")
	for _, forbidden := range []string{"/private/", "/host/", "rcon_password", "token=secret"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("private response content: %s", forbidden)
		}
	}
	for _, forbidden := range []string{"jv-family1", "jv-kids001", "jv-legacy1", "/private/", "/host/", "rcon_password", "token=secret", "quadlet", "UID/GID"} {
		if strings.Contains(visible, forbidden) {
			t.Errorf("private visible content: %s", forbidden)
		}
	}
	for _, forbidden := range []string{`name="software"`, `name="version"`, `name="java_port"`, `name="bedrock_port"`, `name="java_memory"`, `name="container_memory"`, `name="storage"`, `name="start_on_boot"`, "Edit Settings", "How to Connect", "delete-world"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("out-of-scope control: %s", forbidden)
		}
	}
}
func TestMinecraftServersInventoryOrderAndActionBar(t *testing.T) {
	app, f := newServerInventoryApp(t)
	response := inventoryRequest(app, "GET", "", "")
	body := response.Body.String()
	if response.Code != 200 {
		t.Fatalf("status %d: %s", response.Code, body)
	}
	previous := -1
	for _, label := range []string{`>Add Server</a>`, `>Rearrange</button>`, `>Import</a>`, "<h2>family Survival</h2>", "<h2>Kids Creative</h2>"} {
		index := strings.Index(body, label)
		if index <= previous {
			t.Fatalf("wrong order/missing %s", label)
		}
		previous = index
	}
	for _, want := range []string{`href="/minecraft/servers/add"`, "Paper · 1.21.8", "Purpur · 1.21.8", "25567", ">19134</strong>", "Disabled", "Running", "Stopped", "2G / 3G"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "19135") {
		t.Fatal("disabled Bedrock exposed an active port")
	}
	requireNoPrivateServerContent(t, body)
	if len(f.actions) != 0 || f.action != "" {
		t.Fatal("inventory mutated runtime")
	}
	if strings.Contains(body, "minecraft-add-server-card") || !strings.Contains(body, `href="/?workspace=migration&amp;tab=import"`) {
		t.Fatal("incorrect action bar", body)
	}
	cards, err := os.ReadFile("static/minecraft-servers.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"repeat(auto-fill,minmax(min(100%,420px),420px))", "box-shadow", ":focus-visible", "@media (max-width: 700px)", "@container (max-width: 600px)"} {
		if !strings.Contains(string(cards), want) {
			t.Errorf("missing card treatment %s", want)
		}
	}

}

func TestMinecraftServerCardControlsAndIdentity(t *testing.T) {
	app, f := newServerInventoryApp(t)
	body := inventoryRequest(app, "GET", "", "").Body.String()
	cards := regexp.MustCompile(`(?s)<article class="panel minecraft-server-card".*?</article>`).FindAllString(body, -1)
	if len(cards) != 2 {
		t.Fatal("incorrect inventory", body)
	}
	for i, id := range []string{"jv-family1", "jv-kids001"} {
		card := cards[i]
		for _, want := range []string{`data-server-id="` + id + `"`, "data-server-running", "data-server-boot", "Server running", "Start on Boot", "data-server-arrange-actions hidden", "data-server-move-earlier", "data-server-move-later"} {
			if !strings.Contains(card, want) {
				t.Errorf("%s missing %s", id, want)
			}
		}
		heading := regexp.MustCompile(`(?s)<div class="minecraft-server-heading">.*?</div>`).FindString(card)
		if !strings.Contains(heading, "<h2>") || !strings.Contains(heading, "minecraft-server-state") {
			t.Fatal("name and status must share row", heading)
		}
		row := regexp.MustCompile(`(?s)<div class="minecraft-server-software-row">.*?</details>`).FindString(card)
		if !strings.Contains(row, "· 1.21.8") || !strings.Contains(row, `>⋯</summary>`) {
			t.Fatal("software/menu row", row)
		}
		previous := -1
		for _, label := range []string{">Settings</a>", ">Backups</a>", ">Version</a>", ">Plugins</a>", ">Invite friends</button>", ">How to connect</a>", ">Delete server</button>"} {
			index := strings.Index(row, label)
			if index <= previous {
				t.Errorf("missing/out-of-order %s", label)
			}
			previous = index
		}
		for _, workspace := range []string{"minecraft", "backups", "version", "plugins"} {
			if !strings.Contains(row, `href="/?workspace=`+workspace+`&amp;server=`+id+`" data-server-workspace="`+workspace+`"`) {
				t.Errorf("missing selected %s", workspace)
			}
		}
		if !strings.Contains(row, `href="/minecraft/how-to-connect?server=`+id+`" target="_blank" rel="noopener"`) {
			t.Error("lost guide selection")
		}
		for _, forbidden := range []string{"Open server", ">More<", "invite=1", ">Start</button>", ">Stop</button>", "Kill", "Live RAM", "RAM usage"} {
			if strings.Contains(card, forbidden) {
				t.Errorf("obsolete/invalid control %s", forbidden)
			}
		}
		if !strings.Contains(row, `<hr>`) || !strings.Contains(row, `class="storage-action-danger" data-server-delete>Delete server`) {
			t.Error("missing final destructive menu action")
		}
		running := regexp.MustCompile(`<input[^>]*data-server-running[^>]*>`).FindString(card)
		boot := regexp.MustCompile(`<input[^>]*data-server-boot[^>]*>`).FindString(card)
		if i == 0 {
			if !strings.Contains(running, " checked") || !strings.Contains(boot, " checked") || !strings.Contains(card, `minecraft-server-state success`) || !strings.Contains(row, "data-server-restart") {
				t.Error("running configuration lost", card)
			}
		} else {
			if strings.Contains(running, " checked") || strings.Contains(boot, " checked") || !strings.Contains(card, `minecraft-server-state error`) || strings.Contains(row, "data-server-restart") {
				t.Error("stopped configuration lost", card)
			}
		}
		other := "jv-kids001"
		if i == 1 {
			other = "jv-family1"
		}
		if strings.Contains(card, other) {
			t.Error("cross-server identity")
		}
	}
	if strings.Contains(body, `src="/static/minecraft-servers.js"`) {
		t.Error("inventory fragment must not load the shared Minecraft Servers asset")
	}
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(header), `src="/static/minecraft-servers.js"`) != 1 || !strings.Contains(string(header), `<script src="/static/minecraft-servers.js" defer></script>`) {
		t.Error("shared header must load the deferred Minecraft Servers asset exactly once")
	}
	if len(f.actions) != 0 {
		t.Fatal("card rendering mutated server")
	}
	requireNoPrivateServerContent(t, body)
}

func TestMinecraftServerCardsUnavailableLifecycle(t *testing.T) {
	for _, state := range []string{"starting", "stopping", "configured", "failed", "recovery_required", "invalid_configuration", "degraded", "unknown"} {
		t.Run(state, func(t *testing.T) {
			app, f := newServerInventoryApp(t)
			f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: state}
			body := inventoryRequest(app, "GET", "", "").Body.String()
			card := regexp.MustCompile(`(?s)<article[^>]*data-server-id="jv-family1".*?</article>`).FindString(body)
			if card == "" || !regexp.MustCompile(`<input[^>]*data-server-running[^>]* disabled>`).MatchString(card) || strings.Contains(card, "data-server-restart") {
				t.Fatal("unavailable state exposes lifecycle", card)
			}
		})
	}
	app, f := newServerInventoryApp(t)
	f.configurationErrors["jv-family1"] = true
	body := inventoryRequest(app, "GET", "", "").Body.String()
	card := regexp.MustCompile(`(?s)<article[^>]*data-server-id="jv-family1".*?</article>`).FindString(body)
	if card == "" || !regexp.MustCompile(`<input[^>]*data-server-boot[^>]* disabled>`).MatchString(card) || !regexp.MustCompile(`<input[^>]*data-server-running[^>]* disabled>`).MatchString(card) {
		t.Fatal("unknown configuration exposes boot or lifecycle", card)
	}
}
func TestMinecraftServerStatePresentation(t *testing.T) {
	for state, want := range map[string]string{"running": "Running", "stopped": "Stopped", "starting": "Starting", "stopping": "Stopping", "configured": "Configured", "failed": "Failed", "recovery_required": "Recovery required", "invalid_configuration": "Configuration needs attention", "degraded": "Degraded", "unknown": "Status unavailable", "unavailable": "Status unavailable"} {
		t.Run(state, func(t *testing.T) {
			app, f := newServerInventoryApp(t)
			f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: state}
			response := inventoryRequest(app, "GET", "/view/jv-family1", "")
			body := response.Body.String()
			if response.Code != 200 || !strings.Contains(body, "<strong>"+want+"</strong>") {
				t.Fatalf("wrong state %s", body)
			}
			if state != "stopped" && strings.Contains(body, ">Stopped<") {
				t.Fatal("false stopped")
			}
		})
	}
}
func TestMinecraftServerUnavailableDoesNotHideOtherCards(t *testing.T) {
	for _, configuration := range []bool{false, true} {
		app, f := newServerInventoryApp(t)
		f.statusErrors["jv-family1"] = true
		f.configurationErrors["jv-family1"] = configuration
		response := inventoryRequest(app, "GET", "", "")
		body := response.Body.String()
		if response.Code != 200 || !strings.Contains(body, "Status unavailable") || !strings.Contains(body, "Kids Creative") {
			t.Fatal(body)
		}
		if configuration && !strings.Contains(body, "Server configuration could not be checked.") {
			t.Fatal(body)
		}
		requireNoPrivateServerContent(t, body)
	}
	app, f := newServerInventoryApp(t)
	f.inventoryError = errors.New("private /host/registry secret")
	response := inventoryRequest(app, "GET", "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Minecraft instances could not be checked") || strings.Contains(response.Body.String(), "Original server") {
		t.Fatal(response.Body.String())
	}
	requireNoPrivateServerContent(t, response.Body.String())
}
func TestMinecraftSelectedServerIsolationAndReadOnly(t *testing.T) {
	app, f := newServerInventoryApp(t)
	response := inventoryRequest(app, "GET", "/view/jv-family1", "")
	body := response.Body.String()
	for _, want := range []string{"family Survival", "Welcome family", "Start on Boot", ">19134</strong>", "minecraft-workspace-shell", "minecraft-overview-grid", "/view/jv-family1/restart", "/view/jv-family1/stop", `name="csrf" value="csrf-token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "Kids Creative") || strings.Contains(body, "Kids only") || strings.Contains(body, "25568") {
		t.Fatal("another server leaked into selection")
	}
	if !reflect.DeepEqual(f.reads, []string{"configuration:jv-family1", "status:jv-family1"}) || len(f.actions) != 0 || f.action != "" {
		t.Fatalf("selection read/mutated other target: %v %v", f.reads, f.actions)
	}
	requireNoPrivateServerContent(t, body)
	for _, id := range []string{"invalid", "jv-missing1", "jv-legacy1"} {
		result := inventoryRequest(app, "GET", "/view/"+id, "")
		if result.Code != 404 {
			t.Fatalf("%s status %d", id, result.Code)
		}
	}
}
func TestMinecraftZeroServerInventoryHasNoSyntheticServer(t *testing.T) {
	app, f := newServerInventoryApp(t)
	f.instances = nil
	f.runtimes = map[string]api.InstanceRuntimeSettings{}
	response := inventoryRequest(app, "GET", "", "")
	if response.Code != 200 || strings.Contains(response.Body.String(), "Original server") || strings.Contains(response.Body.String(), "Open Server") {
		t.Fatal(response.Code, response.Body.String())
	}
	if len(f.reads) != 0 || len(f.actions) != 0 {
		t.Fatal("zero-server inventory used a runtime")
	}
}

func TestMinecraftSelectedLifecycleSecurityAndRefusal(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		app, f := newServerInventoryApp(t)
		response := inventoryRequest(app, "POST", "/view/jv-family1/"+action, "csrf=csrf-token&confirm_players=yes")
		if response.Code != 303 || !reflect.DeepEqual(f.actions, []string{"jv-family1:" + action}) || f.action != "" {
			t.Fatalf("wrong target %d %v", response.Code, f.actions)
		}
		if response.Header().Get("Location") != serverOverviewURL("jv-family1") {
			t.Fatal("lost selection")
		}
	}
	for _, role := range []string{"administrator", "operator", "viewer"} {
		app, f := newServerInventoryApp(t)
		f.role = role
		for _, path := range []string{"", "/view/jv-family1"} {
			result := inventoryRequest(app, "GET", path, "")
			want := 200
			if role != "administrator" {
				want = 403
			}
			if result.Code != want {
				t.Fatalf("%s GET %s: %d", role, path, result.Code)
			}
		}
		response := inventoryRequest(app, "POST", "/view/jv-family1/start", "")
		if response.Code != 403 || len(f.actions) != 0 {
			t.Fatal("CSRF bypass")
		}
		if role != "administrator" {
			response = inventoryRequest(app, "POST", "/view/jv-family1/start", "csrf=csrf-token")
			if response.Code != 403 || len(f.actions) != 0 {
				t.Fatal("role bypass")
			}
		}
	}
	app, f := newServerInventoryApp(t)
	f.actionError = &api.ResponseError{StatusCode: 409, Message: "Players are online. Server was not interrupted."}
	response := inventoryRequest(app, "POST", "/view/jv-family1/stop", "csrf=csrf-token")
	if !strings.Contains(response.Body.String(), `role="alert"`) || !strings.Contains(response.Body.String(), "Players are online") || len(f.actions) != 1 {
		t.Fatal("refusal lost/retried")
	}
	f.actionError = errors.New("private /host/config secret")
	response = inventoryRequest(app, "POST", "/view/jv-family1/stop", "csrf=csrf-token")
	requireNoPrivateServerContent(t, response.Body.String())
	f.actions = nil
	for _, path := range []string{"/view/jv-missing1/start", "/view/jv-family1/delete", "/view/jv-legacy1/start"} {
		response = inventoryRequest(app, "POST", path, "csrf=csrf-token")
		if response.Code != 404 || len(f.actions) != 0 {
			t.Fatalf("invalid target/action accepted: %s", path)
		}
	}
	for _, path := range []string{"", "/view/jv-family1"} {
		response = httptestResponse(app, httptest.NewRequest("GET", "http://example"+minecraftServersPath+path, nil))
		if response.Code != 303 || response.Header().Get("Location") != "/login" {
			t.Fatal("anonymous page access")
		}
	}
	request := authenticatedAdminRequest("POST", "http://example"+serverOverviewURL("jv-family1")+"/start", "csrf=csrf-token")
	request.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "1"})
	response = httptestResponse(app, request)
	if response.Code != 303 || response.Header().Get("Location") != "/password" || len(f.actions) != 0 {
		t.Fatal("password change bypass")
	}
}

func TestMinecraftSelectedStatusMismatchFailsClosed(t *testing.T) {
	app, f := newServerInventoryApp(t)
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-kids001", State: "stopped"}
	result := inventoryRequest(app, "GET", "/view/jv-family1", "")
	if !strings.Contains(result.Body.String(), "Status unavailable") || strings.Contains(result.Body.String(), ">Stopped<") {
		t.Fatal(result.Body.String())
	}
}

func TestMinecraftStaleLifecycleHasNoFallback(t *testing.T) {
	app, f := newServerInventoryApp(t)
	result := inventoryRequest(app, "POST", "/view/jv-absent1/stop", "csrf=csrf-token&confirm_players=yes")
	if result.Code != 404 || len(f.actions) != 0 || f.action != "" {
		t.Fatal(result.Code, result.Body.String())
	}
}

func TestMinecraftOneServerInventoryContainsExactlyActualIdentity(t *testing.T) {
	app, f := newServerInventoryApp(t)
	f.instances = []api.RegisteredMinecraftInstance{{ID: "jv-family1", DisplayName: "Family"}}
	delete(f.runtimes, "jv-kids001")
	servers, message := app.minecraftServerInventory(context.Background(), "session-token")
	if message != "" || len(servers) != 1 || servers[0].ContextID != "jv-family1" || servers[0].Name != "Family" {
		t.Fatal(servers, message)
	}
}

func TestMinecraftServersWorkspaceCompatibilityAndHeader(t *testing.T) {
	app, _ := newServerInventoryApp(t)
	for _, result := range []string{"created", "deleted"} {
		response := httptestResponse(app, authenticatedAdminRequest("GET", "http://example/minecraft/servers?"+result+"=yes", ""))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/?"+result+"=yes&workspace=minecraft-servers" {
			t.Fatal(response.Code, response.Header())
		}
		fragment := httptestResponse(app, authenticatedAdminRequest("GET", "http://example/workspace/minecraft-servers?"+result+"=yes", ""))
		body := fragment.Body.String()
		if fragment.Code != 200 || strings.Contains(body, "<!doctype") || strings.Contains(body, "data-server-selector-slot") || !strings.Contains(body, "notice success") {
			t.Fatal(body)
		}
	}
	header, _ := assets.ReadFile("templates/header.html")
	markup := string(header)
	if strings.Contains(markup, `href="/minecraft/servers"`) || !strings.Contains(markup, `type="button" data-minecraft-servers-open`) {
		t.Fatal("inventory launcher must open a workspace")
	}
	window := regexp.MustCompile(`(?s)<dialog[^>]*data-workspace-window="minecraft-servers".*?</dialog>`).FindString(markup)
	if window == "" || strings.Contains(window, "data-server-selector-slot") || !strings.Contains(window, "data-workspace-drag-handle") {
		t.Fatal(window)
	}
	for _, name := range []string{"minecraft", "version", "plugins", "backups", "migration"} {
		window := regexp.MustCompile(`(?s)<dialog[^>]*data-workspace-window="` + name + `".*?</dialog>`).FindString(markup)
		lane := strings.Split(window, "data-workspace-drag-handle")[1]
		lane = strings.Split(lane, "data-"+name+"-refresh")[0]
		if !strings.Contains(lane, `data-server-selector-slot data-server-workspace="`+name+`"`) || strings.Count(window, "data-server-selector-slot") != 1 {
			t.Fatalf("missing common header slot: %s", name)
		}
	}
}

func TestMinecraftCardLifecycleJSON(t *testing.T) {
	for _, tc := range []struct {
		query, body, role string
		code              int
		action            string
	}{
		{"?server=jv-family1", "csrf=csrf-token&action=stop", "administrator", 200, "jv-family1:stop"},
		{"?server=jv-kids001", "csrf=csrf-token&action=start", "administrator", 200, "jv-kids001:start"},
		{"", "csrf=csrf-token&action=start", "administrator", 400, ""},
		{"?server=jv-family1&server=jv-kids001", "csrf=csrf-token&action=stop", "administrator", 400, ""},
		{"?server=jv-missing1", "csrf=csrf-token&action=start", "administrator", 404, ""},
		{"?server=jv-family1", "action=stop", "administrator", 403, ""},
		{"?server=jv-family1", "csrf=csrf-token&action=restart", "administrator", 400, ""},
		{"?server=jv-family1", "csrf=csrf-token&action=kill", "administrator", 400, ""},
		{"?server=jv-family1", "csrf=csrf-token&action=start", "administrator", 409, ""},
		{"?server=jv-family1", "csrf=csrf-token&action=stop", "operator", 403, ""},
		{"?server=jv-family1", "csrf=csrf-token&action=stop", "viewer", 403, ""},
	} {
		t.Run(tc.query+tc.body+tc.role, func(t *testing.T) {
			app, client := newServerInventoryApp(t)
			client.role = tc.role
			response := httptestResponse(app, authenticatedAdminRequest("POST", "/api/minecraft/servers/lifecycle"+tc.query, tc.body))
			if response.Code != tc.code {
				t.Fatalf("%d: %s", response.Code, response.Body.String())
			}
			if tc.action == "" {
				if len(client.actions) != 0 {
					t.Fatal("unauthorized action", client.actions)
				}
			} else if !reflect.DeepEqual(client.actions, []string{tc.action}) {
				t.Fatal("wrong selected lifecycle", client.actions)
			}
			requireNoPrivateServerContent(t, response.Body.String())
		})
	}
	app, client := newServerInventoryApp(t)
	client.actionError = errors.New("private /host/runtime token=secret")
	response := httptestResponse(app, authenticatedAdminRequest("POST", "/api/minecraft/servers/lifecycle?server=jv-family1", "csrf=csrf-token&action=stop"))
	if response.Code < 400 || strings.Contains(response.Body.String(), `"ok":true`) || len(client.actions) != 1 {
		t.Fatal("false success/retry", response.Body.String())
	}
	requireNoPrivateServerContent(t, response.Body.String())
	for _, state := range []string{"starting", "stopping", "failed", "recovery_required", "invalid_configuration"} {
		app, client := newServerInventoryApp(t)
		client.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: state}
		response := httptestResponse(app, authenticatedAdminRequest("POST", "/api/minecraft/servers/lifecycle?server=jv-family1", "csrf=csrf-token&action=start"))
		if response.Code != 409 || len(client.actions) != 0 {
			t.Fatal("unsafe state", state, response.Body.String())
		}
	}
}

func TestMinecraftCardRestartJSON(t *testing.T) {
	app, client := newServerInventoryApp(t)
	request := authenticatedAdminRequest("POST", "/minecraft/servers/view/jv-family1/restart", "csrf=csrf-token")
	request.Header.Set("Accept", "application/json")
	response := httptestResponse(app, request)
	if response.Code != 200 || response.Header().Get("Location") != "" || !reflect.DeepEqual(client.actions, []string{"jv-family1:restart"}) {
		t.Fatal(response.Code, client.actions)
	}
	client.actionError = &api.ResponseError{StatusCode: 409, Message: "Players are online."}
	request = authenticatedAdminRequest("POST", "/minecraft/servers/view/jv-family1/restart", "csrf=csrf-token")
	request.Header.Set("Accept", "application/json")
	response = httptestResponse(app, request)
	if response.Code != 409 || strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatal("restart refusal lost", response.Body.String())
	}
}

func TestMinecraftServerSoftwareArtwork(t *testing.T) {
	app, f := newServerInventoryApp(t)
	for _, software := range []string{"vanilla", "paper", "purpur", "fabric", "forge", "neoforge", "", "../paper", "https://example.test/icon"} {
		t.Run(software, func(t *testing.T) {
			c := f.runtimes["jv-family1"]
			c.Software = software
			f.runtimes["jv-family1"] = c
			summary := app.independentServerSummary(context.Background(), "session", api.RegisteredMinecraftInstance{ID: "jv-family1", DisplayName: "Family"})
			want := ""
			if software == "vanilla" || software == "paper" || software == "purpur" {
				want = "/static/server-software/" + software + ".svg"
				if _, err := os.Stat("static/server-software/" + software + ".svg"); err != nil {
					t.Fatal(err)
				}
			}
			if summary.SoftwareIcon != want {
				t.Fatalf("software %q: icon %q, want %q", software, summary.SoftwareIcon, want)
			}
			body := inventoryRequest(app, "GET", "", "").Body.String()
			card := strings.Split(strings.Split(body, `data-server-id="jv-family1"`)[1], "</article>")[0]
			if want == "" {
				if strings.Contains(card, "minecraft-server-software-icon") {
					t.Fatal("unsupported software rendered artwork")
				}
			} else {
				icon := strings.Index(card, `src="`+want+`" alt=""`)
				text := strings.Index(card, "<span>"+serverSoftware(software)+" · ")
				if icon < 0 || text < icon {
					t.Fatal("local software icon must precede software/version text")
				}
			}
		})
	}
}
