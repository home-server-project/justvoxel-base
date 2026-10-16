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
	for _, label := range []string{`class="panel minecraft-add-server-card"`, "<h2>Original server</h2>", "<h2>family Survival</h2>", "<h2>Kids Creative</h2>"} {
		index := strings.Index(body, label)
		if index <= previous {
			t.Fatalf("wrong order/missing %s", label)
		}
		previous = index
	}
	for _, want := range []string{`href="/minecraft/servers/add"`, "Paper · 1.21.8", "Purpur · 1.21.8", "25567", "Enabled · 19134", "Disabled", "Running", "Stopped", "Open Server"} {
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
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Independent servers could not be checked") || !strings.Contains(response.Body.String(), "Original server") {
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
func TestMinecraftOriginalServerUsesLegacyOnly(t *testing.T) {
	app, f := newServerInventoryApp(t)
	response := inventoryRequest(app, "GET", "/view/original", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Original server") || !strings.Contains(response.Body.String(), "25565") || strings.Contains(response.Body.String(), "25567") {
		t.Fatal(response.Body.String())
	}
	if len(f.reads) != 0 {
		t.Fatal("legacy read independent configuration/status")
	}
	response = inventoryRequest(app, "POST", "/view/original/stop", "csrf=csrf-token&confirm_players=yes")
	if response.Code != 303 || f.action != "stop" || f.confirmed || len(f.actions) != 0 {
		t.Fatal("legacy lifecycle isolation/override failure")
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

func TestMinecraftOriginalUnavailableAndSelectedStatusMismatch(t *testing.T) {
	app, f := newServerInventoryApp(t)
	f.legacyStatusError = true
	result := inventoryRequest(app, "GET", "/view/original", "")
	body := result.Body.String()
	if result.Code != 200 || !strings.Contains(body, "Status unavailable") || strings.Contains(body, ">Stopped<") || strings.Contains(body, "/original/start") {
		t.Fatal(body)
	}
	requireNoPrivateServerContent(t, body)
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-kids001", State: "stopped"}
	result = inventoryRequest(app, "GET", "/view/jv-family1", "")
	body = result.Body.String()
	if !strings.Contains(body, "Status unavailable") || strings.Contains(body, ">Stopped<") {
		t.Fatal("another server status accepted")
	}
}

func TestMinecraftLegacyLifecycleRefusalHasNoOverride(t *testing.T) {
	app, f := newServerInventoryApp(t)
	f.actionResult = api.MinecraftActionResponse{ConfirmationRequired: true, Message: "Players are online. Server was not interrupted."}
	result := inventoryRequest(app, "POST", "/view/original/stop", "csrf=csrf-token&confirm_players=yes")
	body := result.Body.String()
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Players are online") || strings.Contains(body, `name="confirm_players"`) || f.confirmed || len(f.actions) != 0 {
		t.Fatal("legacy refusal/override mismatch")
	}
}
