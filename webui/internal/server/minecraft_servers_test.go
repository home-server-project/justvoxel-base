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
		"jv-family1": {Software: "paper", Version: "1.21.8", JavaPort: 25567, BedrockEnabled: true, BedrockPort: 19134, StartOnBoot: true, MOTD: "Welcome family"},
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
	return httptestResponse(app, authenticatedAdminRequest(method, "http://example"+minecraftServersPath+path, body))
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
func TestMinecraftServersInventoryOrderAndCompactAdd(t *testing.T) {
	app, f := newServerInventoryApp(t)
	response := inventoryRequest(app, "GET", "", "")
	body := response.Body.String()
	if response.Code != 200 {
		t.Fatalf("status %d: %s", response.Code, body)
	}
	previous := -1
	for _, label := range []string{`class="panel minecraft-add-server-card"`, "<h2>family Survival</h2>", "<h2>Kids Creative</h2>"} {
		index := strings.Index(body, label)
		if index <= previous {
			t.Fatalf("wrong order/missing %s", label)
		}
		previous = index
	}
	for _, want := range []string{`href="/minecraft/servers/add"`, "Paper · 1.21.8", "Purpur · 1.21.8", "25567", "Enabled · 19134", "Disabled", "Running", "Stopped", "Open server"} {
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
	compact, err := os.ReadFile("static/setup.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compact), "width: 10rem; min-height: 9rem") {
		t.Fatal("Add card lost compact dimensions")
	}
	cards, err := os.ReadFile("static/minecraft-servers.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"width: 22rem; min-height: 17rem", "align-items: flex-start", ":focus-visible", "@media (max-width: 700px)"} {
		if !strings.Contains(string(cards), want) {
			t.Errorf("missing card treatment %s", want)
		}
	}
}

func TestMinecraftServerCardControlsAndIdentity(t *testing.T) {
	app, f := newServerInventoryApp(t)
	body := inventoryRequest(app, "GET", "", "").Body.String()
	add := regexp.MustCompile(`<a class="panel minecraft-add-server-card"[^>]*>.*?</a>`).FindString(body)
	if strings.Count(body, `class="panel minecraft-add-server-card"`) != 1 || !strings.Contains(add, `href="/minecraft/servers/add"`) {
		t.Fatal("missing or duplicate Add Server tile")
	}
	for _, forbidden := range []string{"data-server-id", "data-minecraft-server-card", "data-server-move-"} {
		if strings.Contains(add, forbidden) {
			t.Fatal("Add Server participates in ordering", add)
		}
	}
	cards := regexp.MustCompile(`(?s)<article class="panel minecraft-server-card".*?</article>`).FindAllString(body, -1)
	if len(cards) != 2 || strings.Index(body, add) > strings.Index(body, cards[0]) {
		t.Fatal("Add Server must precede all real cards")
	}
	for i, id := range []string{"jv-family1", "jv-kids001"} {
		card := cards[i]
		for _, want := range []string{
			`data-minecraft-server-card data-server-id="` + id + `"`,
			`href="/?workspace=minecraft&amp;server=` + id + `">Settings</a>`,
			`href="/minecraft/servers/view/` + id + `">Open server</a>`,
			`href="/minecraft/how-to-connect?server=` + id + `">How to connect</a>`,
			"Start on Boot", ">More</summary>", "data-server-normal-actions", "data-server-arrange-actions hidden",
			"data-server-move-earlier", "data-server-move-later",
		} {
			if !strings.Contains(card, want) {
				t.Errorf("%s missing %s", id, want)
			}
		}
		for _, workspace := range []string{"backups", "version", "plugins"} {
			if !strings.Contains(card, `href="/?workspace=`+workspace+`&amp;server=`+id+`"`) {
				t.Errorf("%s missing selected %s", id, workspace)
			}
		}
		moreStart, moreEnd := strings.Index(card, "<details"), strings.Index(card, "</details>")
		if moreStart < 0 || moreEnd < moreStart {
			t.Fatalf("%s missing More menu", id)
		}
		more := card[moreStart:moreEnd]
		for _, forbidden := range []string{"Rename", "Delete", ">Settings<"} {
			if strings.Contains(more, forbidden) {
				t.Errorf("unexpected More entry %s", forbidden)
			}
		}
		wantActions, absentActions := []string{"stop", "restart"}, []string{"start"}
		boot := "Enabled"
		if id == "jv-kids001" {
			wantActions, absentActions = []string{"start"}, []string{"stop", "restart"}
			boot = "Disabled"
		}
		if !strings.Contains(card, "<span>Start on Boot</span><strong>"+boot+"</strong>") {
			t.Errorf("%s wrong boot status", id)
		}
		for _, action := range wantActions {
			if !strings.Contains(card, `<form method="post" action="/minecraft/servers/view/`+id+`/`+action+`"><input type="hidden" name="csrf" value="csrf-token">`) {
				t.Errorf("%s missing exact POST/CSRF %s", id, action)
			}
		}
		for _, action := range absentActions {
			if strings.Contains(card, "/"+action+`"`) {
				t.Errorf("%s invalid lifecycle %s", id, action)
			}
		}
		if strings.Contains(card, "/restart") && !strings.Contains(more, "/restart") {
			t.Error("Restart must be in More")
		}
		other := "jv-family1"
		if id == other {
			other = "jv-kids001"
		}
		if strings.Contains(card, other) {
			t.Error("another identity leaked into card")
		}
	}
	for _, want := range []string{"data-server-arrange hidden>Arrange", "data-server-arrange-done hidden>Done", `src="/static/minecraft-servers.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if len(f.actions) != 0 {
		t.Fatal("card rendering mutated server")
	}
	requireNoPrivateServerContent(t, body)
}

func TestMinecraftServerCardsUnavailableLifecycle(t *testing.T) {
	for _, state := range []string{"starting", "stopping", "configured", "recovery_required", "invalid_configuration", "degraded", "unknown"} {
		t.Run(state, func(t *testing.T) {
			app, f := newServerInventoryApp(t)
			f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: state}
			body := inventoryRequest(app, "GET", "", "").Body.String()
			card := regexp.MustCompile(`(?s)<article[^>]*data-server-id="jv-family1".*?</article>`).FindString(body)
			if card == "" || strings.Contains(card, `<form method="post"`) {
				t.Fatal("unavailable state exposes lifecycle", card)
			}
		})
	}
	app, f := newServerInventoryApp(t)
	f.configurationErrors["jv-family1"] = true
	body := inventoryRequest(app, "GET", "", "").Body.String()
	card := regexp.MustCompile(`(?s)<article[^>]*data-server-id="jv-family1".*?</article>`).FindString(body)
	if card == "" || strings.Contains(card, "Start on Boot") || strings.Contains(card, `<form method="post"`) {
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
	for _, want := range []string{"family Survival", "Welcome family", "Start on Boot", "Enabled", "minecraft-workspace-shell", "minecraft-overview-grid", "/view/jv-family1/restart", "/view/jv-family1/stop", `name="csrf" value="csrf-token"`} {
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
