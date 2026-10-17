package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type dashboardInstanceAPI struct {
	*phase7API
	legacyPlayers, legacyActions int
	playerID, runtimeID          string
	playerErr                    error
	confirmation                 bool
	confirmed                    bool
}

func (f *dashboardInstanceAPI) Status(context.Context, string) (api.Status, error) {
	s := defaultStatus()
	s.Minecraft.ServerSoftware = "GLOBAL SOFTWARE"
	s.Minecraft.Version = "GLOBAL VERSION"
	s.Minecraft.JavaPort = 25599
	s.Minecraft.BedrockPort = 19999
	s.Minecraft.Bedrock = false
	return s, nil
}
func (f *dashboardInstanceAPI) Players(context.Context, string) (api.Players, error) {
	f.legacyPlayers++
	return api.Players{}, errors.New("legacy players forbidden")
}
func (f *dashboardInstanceAPI) MinecraftAction(context.Context, string, string, bool) (api.MinecraftActionResponse, error) {
	f.legacyActions++
	return api.MinecraftActionResponse{}, errors.New("legacy action forbidden")
}
func (f *dashboardInstanceAPI) MinecraftInstanceStatus(ctx context.Context, session, id string) (api.MinecraftInstanceStatus, error) {
	s, err := f.serverInventoryTestAPI.MinecraftInstanceStatus(ctx, session, id)
	if f.runtimeID != "" {
		s.ID = f.runtimeID
	}
	return s, err
}
func (f *dashboardInstanceAPI) MinecraftInstancePlayers(_ context.Context, _, id string) (api.InstancePlayers, error) {
	f.reads = append(f.reads, "players:"+id)
	if f.playerID != "" {
		id = f.playerID
	}
	return api.InstancePlayers{ID: id, State: "running", Online: 3, Maximum: 14}, f.playerErr
}
func (f *dashboardInstanceAPI) MinecraftInstanceActionConfirmed(_ context.Context, _, id, action string, confirm bool) (api.MinecraftActionResponse, error) {
	f.actions = append(f.actions, id+":"+action)
	f.confirmed = confirm
	return api.MinecraftActionResponse{OK: !f.confirmation || confirm, ConfirmationRequired: f.confirmation && !confirm, Online: 3}, nil
}
func newDashboardInstanceApp(t *testing.T) (*App, *dashboardInstanceAPI) {
	t.Helper()
	_, base := phase7App(t)
	f := &dashboardInstanceAPI{phase7API: base}
	config := f.runtimes["jv-family1"]
	config.MaxPlayers = 14
	f.runtimes["jv-family1"] = config
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running", StartOnBoot: true}
	app, err := New(f, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	return app, f
}
func dashboardInstanceRequest(app *App, method, path, form string) *httptest.ResponseRecorder {
	if method == http.MethodPost {
		return httptestResponse(app, actionRequest("http://example"+path, form))
	}
	return httptestResponse(app, authenticatedAdminRequest(method, "http://example"+path, form))
}
func requireNoDashboardLegacy(t *testing.T, f *dashboardInstanceAPI) {
	t.Helper()
	if f.legacyActions != 0 || f.legacyPlayers != 0 {
		t.Fatalf("legacy calls: actions=%d players=%d", f.legacyActions, f.legacyPlayers)
	}
}
func TestDashboardInstanceReadsAndPolling(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	page := dashboardInstanceRequest(app, "GET", "/?server=jv-family1", "")
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body.String())
	}
	for _, want := range []string{`data-dashboard-status="/api/dashboard-status?server=jv-family1"`, `data-selected-minecraft-id="jv-family1"`, `href="/minecraft/how-to-connect?server=jv-family1"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Count(page.Body.String(), `name="server" value="jv-family1"`) != 3 {
		t.Fatal("all Quick Look action forms must retain selection")
	}
	f.reads = nil
	response := dashboardInstanceRequest(app, "GET", "/api/dashboard-status?server=jv-family1", "")
	var snapshot dashboardSnapshot
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	expected := []string{"configuration:jv-family1", "status:jv-family1", "players:jv-family1"}
	if !reflect.DeepEqual(f.reads, expected) {
		t.Fatal(f.reads)
	}
	mc := snapshot.Status.Minecraft
	if !mc.Configured || mc.State != "Running" || mc.ServerSoftware != "Paper" || mc.ServerType != "paper" || mc.Version != "1.21.8" || mc.MaxPlayers != 14 || mc.JavaPort != 25567 || !mc.Bedrock || mc.BedrockPort != 19134 || mc.Players != 3 || snapshot.Players.Online != 3 || snapshot.Players.State != "running" || snapshot.Players.Max != 14 {
		t.Fatalf("unexpected selected presentation: %+v %+v", mc, snapshot.Players)
	}
	if !snapshot.Status.Backup.Enabled || snapshot.Status.System.IPv4 != "192.0.2.10" {
		t.Fatal("appliance fields changed")
	}
	if len(f.actions) != 0 {
		t.Fatal(f.actions)
	}
	requireNoDashboardLegacy(t, f)
	source, _ := assets.ReadFile("static/app.js")
	for _, want := range []string{`const statusURL = dashboard.dataset.dashboardStatus;`, `const statusURL = dashboard?.dataset.dashboardStatus || globalThis.justVoxelSelectedURL("/api/dashboard-status");`, `target.searchParams.set("server", dashboard.dataset.selectedMinecraftId);`, `window.history.replaceState(window.history.state, "", target);`} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("missing polling/reload plumbing: %s", want)
		}
	}
}
func TestDashboardInstanceLifecycleAndConfirmation(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			app, f := newDashboardInstanceApp(t)
			f.confirmation = true
			first := dashboardInstanceRequest(app, "POST", "/minecraft/"+action+"?server=jv-family1", "csrf=token")
			if first.Code != 200 || !strings.Contains(first.Body.String(), `name="server" value="jv-family1"`) || !strings.Contains(first.Body.String(), `href="/?server=jv-family1">Cancel`) {
				t.Fatal(first.Code, first.Body.String())
			}
			second := dashboardInstanceRequest(app, "POST", "/minecraft/"+action, "csrf=token&confirm_players=yes&server=jv-family1")
			if second.Code != 303 || second.Header().Get("Location") != "/?result="+action+"&server=jv-family1" || !f.confirmed {
				t.Fatal(second.Code, second.Header(), f.confirmed)
			}
			if !reflect.DeepEqual(f.actions, []string{"jv-family1:" + action, "jv-family1:" + action}) {
				t.Fatal(f.actions)
			}
			for _, read := range f.reads {
				if !strings.HasSuffix(read, ":jv-family1") {
					t.Fatal("sibling read:", read)
				}
			}
			conflict := dashboardInstanceRequest(app, "POST", "/minecraft/"+action+"?server=jv-kids001", "csrf=token&confirm_players=yes&server=jv-family1")
			if conflict.Code != 400 || len(f.actions) != 2 {
				t.Fatal("confirmation changed target", conflict.Code, f.actions)
			}
			requireNoDashboardLegacy(t, f)
		})
	}
}
func TestDashboardInstanceSelectionFailures(t *testing.T) {
	for _, query := range []string{"?server=family", "?server=jv-stale1", "?server=jv-family1&server=jv-kids001", ""} {
		for _, path := range []string{"/", "/api/dashboard-status", "/minecraft/how-to-connect", "/minecraft/start", "/minecraft/stop", "/minecraft/restart"} {
			t.Run(path+query, func(t *testing.T) {
				app, f := newDashboardInstanceApp(t)
				method, form := "GET", ""
				if strings.HasPrefix(path, "/minecraft/") && path != "/minecraft/how-to-connect" {
					method, form = "POST", "csrf=token"
				}
				response := dashboardInstanceRequest(app, method, path+query, form)
				if response.Code < 400 {
					t.Fatal(response.Code, response.Body.String())
				}
				if len(f.reads) != 0 || len(f.actions) != 0 {
					t.Fatal("read/mutated unresolved or sibling server", f.reads, f.actions)
				}
				requireNoDashboardLegacy(t, f)
			})
		}
	}
}
func TestDashboardInstanceZeroAndSoleSelection(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{true: "zero", false: "sole"}[zero], func(t *testing.T) {
			app, f := newDashboardInstanceApp(t)
			f.instances = []api.RegisteredMinecraftInstance{{ID: "jv-family1"}}
			if zero {
				f.instances = nil
				f.runtimes = nil
			}
			page := dashboardInstanceRequest(app, "GET", "/", "")
			if page.Code != 200 {
				t.Fatal(page.Code, page.Body.String())
			}
			if strings.Contains(page.Body.String(), `data-configured="true"`) == zero {
				t.Fatal("incorrect configured state")
			}
			if !zero && !strings.Contains(page.Body.String(), `data-dashboard-status="/api/dashboard-status?server=jv-family1"`) {
				t.Fatal("sole identity not pinned")
			}
			guide := dashboardInstanceRequest(app, "GET", "/minecraft/how-to-connect", "")
			if guide.Code != 200 {
				t.Fatal(guide.Code, guide.Body.String())
			}
			snapshot := dashboardInstanceRequest(app, "GET", "/api/dashboard-status", "")
			if snapshot.Code != 200 {
				t.Fatal(snapshot.Code, snapshot.Body.String())
			}
			if zero {
				if !strings.Contains(guide.Body.String(), "Minecraft is not configured yet") || len(f.reads) != 0 {
					t.Fatal("zero-instance setup experience changed")
				}
				for _, action := range []string{"start", "stop", "restart"} {
					response := dashboardInstanceRequest(app, "POST", "/minecraft/"+action, "csrf=token")
					if response.Code != 409 || len(f.actions) != 0 {
						t.Fatal("zero-instance lifecycle did not fail closed")
					}
				}
			}
			requireNoDashboardLegacy(t, f)
		})
	}
}
func TestDashboardInstanceReadEvidenceFailsClosed(t *testing.T) {
	for _, kind := range []string{"configuration", "runtime identity", "player identity", "players error"} {
		t.Run(kind, func(t *testing.T) {
			app, f := newDashboardInstanceApp(t)
			switch kind {
			case "configuration":
				f.configurationErrors["jv-family1"] = true
			case "runtime identity":
				f.runtimeID = "jv-kids001"
			case "player identity":
				f.playerID = "jv-kids001"
			case "players error":
				f.playerErr = errors.New("unavailable")
			}
			response := dashboardInstanceRequest(app, "GET", "/api/dashboard-status?server=jv-family1", "")
			var snapshot dashboardSnapshot
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
				t.Fatal(response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "GLOBAL") {
				t.Fatal("global Minecraft fallback")
			}
			switch kind {
			case "configuration":
				if snapshot.Status.Minecraft.Configured || snapshot.Status.Minecraft.JavaPort != 0 {
					t.Fatal(snapshot.Status.Minecraft)
				}
			case "runtime identity":
				if snapshot.Status.Minecraft.State != "Unavailable" {
					t.Fatal(snapshot.Status.Minecraft)
				}
			default:
				if snapshot.Players.State != "unavailable" || snapshot.Players.Online != 0 || snapshot.Status.Minecraft.Players != 0 {
					t.Fatal(snapshot.Players)
				}
			}
			requireNoDashboardLegacy(t, f)
		})
	}
}
func TestConnectionGuideInstanceIsolation(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	response := dashboardInstanceRequest(app, "GET", "/minecraft/how-to-connect?server=jv-family1", "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, want := range []string{"Paper 1.21.8", "25567", "19134", "Join with Bedrock Edition", "192.0.2.10"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, absent := range []string{"GLOBAL", "25599", "19999", "25568", "19135", "Purpur"} {
		if strings.Contains(response.Body.String(), absent) {
			t.Fatalf("global/sibling leak %s", absent)
		}
	}
	if !reflect.DeepEqual(f.reads, []string{"configuration:jv-family1"}) || len(f.actions) != 0 {
		t.Fatal(f.reads, f.actions)
	}
	f.configurationErrors["jv-family1"] = true
	failed := dashboardInstanceRequest(app, "GET", "/minecraft/how-to-connect?server=jv-family1", "")
	if failed.Code != http.StatusOK || strings.Contains(failed.Body.String(), "25599") || strings.Contains(failed.Body.String(), "25567") {
		t.Fatal("unverified configuration exposed", failed.Code)
	}
	requireNoDashboardLegacy(t, f)
}
