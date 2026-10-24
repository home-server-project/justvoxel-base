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
	backupReads                  []string
	backupReadErr                error
	globalBackupEnabled          bool
	inventoryReads               int
}

func (f *dashboardInstanceAPI) MinecraftInstances(ctx context.Context, session string) ([]api.RegisteredMinecraftInstance, error) {
	f.inventoryReads++
	return f.addServerTestAPI.MinecraftInstances(ctx, session)
}

func (f *dashboardInstanceAPI) Status(context.Context, string) (api.Status, error) {
	s := defaultStatus()
	s.Backup.Enabled = f.globalBackupEnabled
	s.Backup.Last, s.Backup.Next = "GLOBAL LAST", "GLOBAL NEXT"
	s.Minecraft.ServerSoftware = "GLOBAL SOFTWARE"
	s.Minecraft.Version = "GLOBAL VERSION"
	s.Minecraft.JavaPort = 25599
	s.Minecraft.BedrockPort = 19999
	s.Minecraft.Bedrock = false
	return s, nil
}
func (f *dashboardInstanceAPI) MinecraftInstanceBackupSettings(_ context.Context, _, id string) (api.InstanceBackupSettings, error) {
	f.backupReads = append(f.backupReads, id)
	return f.backups[id], f.backupReadErr
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
	f := &dashboardInstanceAPI{phase7API: base, globalBackupEnabled: true}
	f.instances = []api.RegisteredMinecraftInstance{{ID: "jv-family1", DisplayName: "Family Survival"}, {ID: "jv-kids001", DisplayName: "Kids Creative"}}
	f.backups["jv-family1"] = api.InstanceBackupSettings{Enabled: true, Time: "04:00"}
	f.backups["jv-kids001"] = api.InstanceBackupSettings{Enabled: false, Time: "05:00"}
	config := f.runtimes["jv-family1"]
	config.MaxPlayers = 14
	f.runtimes["jv-family1"] = config
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running", StartOnBoot: true}
	app, err := New(f, Config{ExternalScheme: "http", ManagementAPI: "v1"})
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

func requireUnselectedDashboard(t *testing.T, page *httptest.ResponseRecorder) {
	t.Helper()
	if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Fatal(page.Code, page.Header(), page.Body.String())
	}
	for _, want := range []string{
		`data-selected-minecraft-id=""`,
		`data-dashboard-status="/api/dashboard-status"`,
		`data-configured="true"`,
		`data-instance-required="false"`,
		`data-dashboard-setup-area hidden`,
		`data-server-selector-slot data-server-workspace="minecraft"`,
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("missing dashboard contract: %s", want)
		}
	}
	if strings.Contains(page.Body.String(), "Choose a Minecraft server.") {
		t.Fatal("dashboard returned selection error")
	}
}

func TestDashboardMultipleServersWithoutSelection(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	requireUnselectedDashboard(t, dashboardInstanceRequest(app, "GET", "/", ""))
	response := dashboardInstanceRequest(app, "GET", "/api/dashboard-status", "")
	var snapshot dashboardSnapshot
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	mc := snapshot.Status.Minecraft
	if !mc.Configured || mc.State != "Choose server" || mc.Version != "" || mc.JavaPort != 0 || mc.BedrockPort != 0 || mc.MaxPlayers != 0 || mc.Players != 0 {
		t.Fatalf("fabricated selected or unconfigured state: %+v", mc)
	}
	if !snapshot.Players.Configured || snapshot.Players.State != "selection_required" || snapshot.Players.Online != 0 || snapshot.Players.Max != 0 || snapshot.Status.Backup != (api.Status{}).Backup {
		t.Fatal(snapshot.Players, snapshot.Status.Backup)
	}
	if snapshot.Status.System.IPv4 != "192.0.2.10" || snapshot.Status.System.Health != "Healthy" || snapshot.Status.JustVoxel.Version != "10-test" {
		t.Fatal("appliance status unavailable", snapshot.Status)
	}
	if strings.Contains(response.Body.String(), "GLOBAL") || strings.Contains(response.Body.String(), "Not configured") || strings.Contains(response.Body.String(), "not_configured") {
		t.Fatal("global or first-run presentation leaked", response.Body.String())
	}
	if len(f.reads) != 0 {
		t.Fatal("unselected dashboard accessed an individual server", f.reads)
	}
	selection := dashboardInstanceRequest(app, "GET", "/api/minecraft/workspace/selection", "")
	var choices struct {
		Selected selectedServerContext   `json:"selected"`
		Servers  []selectedServerContext `json:"servers"`
	}
	if selection.Code != http.StatusOK || json.Unmarshal(selection.Body.Bytes(), &choices) != nil || choices.Selected.ID != "" || len(choices.Servers) != 2 {
		t.Fatal("existing selector contract changed", selection.Code, selection.Body.String())
	}
	if strings.Join(f.reads, ",") != "configuration:jv-family1,configuration:jv-kids001" || len(f.actions) != 0 || len(f.backupReads) != 0 || len(f.backupCalls) != 0 {
		t.Fatal("unselected dashboard accessed an individual server", f.reads, f.actions, f.backupReads, f.backupCalls)
	}
	requireNoDashboardLegacy(t, f)
}

func TestDashboardMultipleServersLoginDestination(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	request := httptest.NewRequest(http.MethodPost, "http://example/login", strings.NewReader("username=admin&password=secret"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login := httptestResponse(app, request)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/" {
		t.Fatal("successful login destination changed", login.Code, login.Header())
	}
	request = httptest.NewRequest(http.MethodGet, "http://example"+login.Header().Get("Location"), nil)
	for _, cookie := range login.Result().Cookies() {
		if cookie.Value != "" {
			request.AddCookie(cookie)
		}
	}
	requireUnselectedDashboard(t, httptestResponse(app, request))
	if len(f.reads) != 0 || len(f.actions) != 0 || len(f.backupReads) != 0 || len(f.backupCalls) != 0 {
		t.Fatal("login destination accessed an individual server", f.reads, f.actions, f.backupReads, f.backupCalls)
	}
	requireNoDashboardLegacy(t, f)
}

func TestAddServerMultipleServersWithoutSelection(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	page := dashboardInstanceRequest(app, "GET", "/minecraft/servers/add", "")
	if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") || !strings.Contains(page.Body.String(), "<h1>Add Minecraft Server</h1>") || strings.Contains(page.Body.String(), "Choose a Minecraft server.") {
		t.Fatal(page.Code, page.Body.String())
	}
	if f.inventoryReads != 0 || len(f.reads) != 0 || len(f.actions) != 0 || len(f.backupReads) != 0 || len(f.backupCalls) != 0 {
		t.Fatal("Add Server resolved an existing server", f.inventoryReads, f.reads, f.actions, f.backupReads, f.backupCalls)
	}
	requireNoDashboardLegacy(t, f)
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
		t.Fatal("selected backup or appliance host fields changed")
	}
	if len(f.actions) != 0 {
		t.Fatal(f.actions)
	}
	requireNoDashboardLegacy(t, f)
	source, _ := assets.ReadFile("static/app.js")
	js := string(source)
	if strings.Count(js, `const statusURL = "/api/dashboard-status";`) != 2 {
		t.Fatal("Dashboard and Quick Look must both resolve the selected server at polling time")
	}
	for _, poll := range []struct{ start, end, render string }{
		{"const refreshDashboard = async", "if (pendingAction)", "renderDashboard(snapshot);"},
		{"async function refreshQuickLook()", "const loadQuickLookIdentity =", "renderQuickLook(snapshot, providers);"},
	} {
		start := strings.Index(js, poll.start)
		if start < 0 {
			t.Fatalf("missing selected-server poll: %s", poll.start)
		}
		end := strings.Index(js[start:], poll.end)
		if end < 0 {
			t.Fatalf("missing polling boundary: %s", poll.end)
		}
		body := js[start : start+end]
		for _, want := range []string{
			`const selection = globalThis.justVoxelSelectionGeneration.minecraft;`,
			`fetch(globalThis.justVoxelSelectedURL(statusURL, "minecraft")`,
			`if (selection !== globalThis.justVoxelSelectionGeneration.minecraft) return;`,
			poll.render,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("selected-server polling missing %q", want)
			}
		}
		if strings.Index(body, `if (selection !== globalThis.justVoxelSelectionGeneration.minecraft) return;`) > strings.Index(body, poll.render) {
			t.Fatal("old-server polling response must be rejected before rendering")
		}
	}
	for _, want := range []string{
		`const statusURL = "/api/dashboard-status";`,
		`target.searchParams.set("server", dashboard.dataset.selectedMinecraftId);`,
		`window.history.replaceState(window.history.state, "", target);`,
		`window.addEventListener("justvoxel:server-selection-changed",`,
		`void refreshDashboard();`, `lastMonitorData = null;`, `void refreshQuickLook();`,
		// An absent explicit selection stays absent; inventory order supplies no fallback.
		`initialServerDestination.getAll("server").forEach(id => target.searchParams.append("server", id))`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing in-page polling plumbing: %s", want)
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
	for _, query := range []string{"?server=", "?server=family", "?server=jv-stale1", "?server=jv-family1&server=jv-kids001", "?server=jv-family1&server=jv-family1", ""} {
		for _, path := range []string{"/", "/api/dashboard-status", "/minecraft/how-to-connect", "/minecraft/start", "/minecraft/stop", "/minecraft/restart"} {
			if path == "/" || (query == "" && (path == "/api/dashboard-status" || path == "/minecraft/how-to-connect")) {
				continue
			}
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
				if len(f.reads) != 0 || len(f.actions) != 0 || len(f.backupReads) != 0 || len(f.backupCalls) != 0 {
					t.Fatal("read/mutated unresolved or sibling server", f.reads, f.actions, f.backupReads, f.backupCalls)
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

func TestDashboardSelectedBackupSettings(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	// Poll A, B, then A again: global appliance values must never win.
	for _, tc := range []struct {
		id              string
		enabled, global bool
	}{
		{"jv-family1", true, false},
		{"jv-kids001", false, true},
		{"jv-family1", true, false},
	} {
		f.globalBackupEnabled = tc.global
		f.backupReads = nil
		response := dashboardInstanceRequest(app, "GET", "/api/dashboard-status?server="+tc.id, "")
		var snapshot dashboardSnapshot
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
			t.Fatal(response.Code, response.Body.String())
		}
		if !reflect.DeepEqual(f.backupReads, []string{tc.id}) || snapshot.Status.Backup.Enabled != tc.enabled || snapshot.Status.Backup.Last != "" || snapshot.Status.Backup.Next != "" {
			t.Fatal(f.backupReads, snapshot.Status.Backup)
		}
		if snapshot.Status.Minecraft.Players != 3 || snapshot.Players.Online != 3 || snapshot.Players.Max != 14 {
			t.Fatal("selected player behavior changed", snapshot)
		}
	}
}

func TestDashboardBackupWithoutSelectionOrAvailableSettings(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	f.globalBackupEnabled = true
	data, err := app.dashboardData(context.Background(), "session-token", "", selectedServerContext{})
	if err != nil || data.Status.Backup != (api.Status{}).Backup || len(f.backupReads) != 0 || !data.Status.Minecraft.Configured || data.Status.Minecraft.State != "Choose server" || data.SelectedMinecraftID != "" || data.InstanceRequired {
		t.Fatal("fabricated selected state", data.Status, err, f.backupReads)
	}
	f.backupReadErr = errors.New("unavailable")
	data, err = app.dashboardData(context.Background(), "session-token", "", selectedServerContext{ID: "jv-family1"})
	if err != nil || data.Status.Backup != (api.Status{}).Backup || data.Status.Minecraft.State != "Running" || data.Players.Online != 3 {
		t.Fatal("independent selected reads changed", data.Status, data.Players, err)
	}
}

func TestDashboardBackupAuthenticationErrors(t *testing.T) {
	for _, want := range []error{api.ErrUnauthorized, api.ErrPasswordChangeRequired} {
		app, f := newDashboardInstanceApp(t)
		f.backupReadErr = want
		_, err := app.dashboardData(context.Background(), "session-token", "", selectedServerContext{ID: "jv-family1"})
		if !errors.Is(err, want) || !reflect.DeepEqual(f.backupReads, []string{"jv-family1"}) {
			t.Fatal(err, f.backupReads)
		}
	}
}

func TestDashboardBrowserSelectionRecovery(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		app, f := newDashboardInstanceApp(t)
		f.instances = f.instances[:count]
		page := dashboardInstanceRequest(app, "GET", "/?server=jv-stale1&workspace=version&tab=software&result=started", "")
		if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
			t.Fatal(page.Code, page.Body.String())
		}
		for _, want := range []string{`data-selected-minecraft-id=""`, `data-dashboard-url="/?result=started&amp;tab=switch&amp;workspace=version"`} {
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("missing %s", want)
			}
		}
		if len(f.reads) != 0 || len(f.backupReads) != 0 || len(f.actions) != 0 {
			t.Fatal("stale selection resolved another server", f.reads, f.backupReads, f.actions)
		}
	}
}

func TestDashboardBrowserLegacyVersionTab(t *testing.T) {
	app, _ := newDashboardInstanceApp(t)
	page := dashboardInstanceRequest(app, "GET", "/?server=jv-family1&workspace=version&tab=software", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-dashboard-url="/?server=jv-family1&amp;tab=switch&amp;workspace=version"`) || !strings.Contains(page.Body.String(), `data-selected-minecraft-id="jv-family1"`) {
		t.Fatal(page.Code, page.Body.String())
	}
}

func TestDashboardBrowserSelectionCheckErrors(t *testing.T) {
	for _, selectionErr := range []error{api.ErrUnauthorized, api.ErrPasswordChangeRequired, errors.New("inventory unavailable")} {
		app, f := newDashboardInstanceApp(t)
		f.inventoryError = selectionErr
		page := dashboardInstanceRequest(app, "GET", "/?server=jv-stale1&workspace=version&tab=software", "")
		if strings.Contains(page.Header().Get("Content-Type"), "application/json") || strings.Contains(page.Body.String(), `"ok":false`) {
			t.Fatal("dashboard emitted API error", page.Body.String())
		}
		switch selectionErr {
		case api.ErrUnauthorized:
			if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/login" {
				t.Fatal(page.Code, page.Header())
			}
			cleared := false
			for _, cookie := range page.Result().Cookies() {
				if cookie.Name == sessionCookie && cookie.MaxAge < 0 {
					cleared = true
				}
			}
			if !cleared {
				t.Fatal("unusable session not cleared")
			}
		case api.ErrPasswordChangeRequired:
			if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/password" {
				t.Fatal(page.Code, page.Header())
			}
		default:
			if page.Code != http.StatusBadGateway || !strings.Contains(page.Body.String(), "management service unavailable") {
				t.Fatal(page.Code, page.Body.String())
			}
		}
	}
	app, _ := newDashboardInstanceApp(t)
	page := httptestResponse(app, httptest.NewRequest("GET", "http://example/?server=jv-stale1&workspace=version&tab=software", nil))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/login" {
		t.Fatal(page.Code, page.Header())
	}
}

func TestDashboardClearedSelectionDoesNotChooseSoleReplacement(t *testing.T) {
	app, f := newDashboardInstanceApp(t)
	f.instances = f.instances[:1]
	for _, path := range []string{"/api/dashboard-status?selection=none", "/api/minecraft/workspace/selection?selection=none"} {
		response := dashboardInstanceRequest(app, "GET", path, "")
		wantReads := ""
		if strings.HasPrefix(path, "/api/minecraft/workspace/selection") {
			wantReads = "configuration:jv-family1"
		}
		if response.Code != http.StatusOK || strings.Join(f.reads, ",") != wantReads || len(f.backupReads) != 0 {
			t.Fatal(response.Code, response.Body.String(), f.reads)
		}
		if path == "/api/minecraft/workspace/selection?selection=none" && !strings.Contains(response.Body.String(), `"selected":{"id":"","name":""}`) {
			t.Fatal(response.Body.String())
		}
	}
	// Existing API callers still resolve the sole server unless explicitly unselected.
	response := dashboardInstanceRequest(app, "GET", "/api/minecraft/workspace/selection", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"selected":{"id":"jv-family1"`) {
		t.Fatal(response.Code, response.Body.String())
	}
}
