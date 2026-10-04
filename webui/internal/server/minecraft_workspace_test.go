package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeMinecraftWorkspaceAPI struct {
	fakeDiscoveryAPI
	planned          api.AdminConfigurationChangeRequest
	applied          api.AdminConfigurationChangeRequest
	planResult       api.AdminConfigurationChangeResponse
	applyResult      api.AdminConfigurationChangeResponse
	whitelist        string
	whitelistChange  api.TextOutputResponse
	whitelistRequest [3]string
	logs             []string
}

func (f *fakeMinecraftWorkspaceAPI) AdminConfigurationPlan(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.planned = request
	if f.planResult.OK || len(f.planResult.Changes) > 0 || f.planResult.ConfirmationRequired {
		return f.planResult, nil
	}
	return api.AdminConfigurationChangeResponse{OK: true, Proposed: f.configuration}, nil
}

func (f *fakeMinecraftWorkspaceAPI) AdminConfigurationApply(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.applied = request
	if f.applyResult.OK || f.applyResult.Applied || f.applyResult.ConfirmationRequired {
		return f.applyResult, nil
	}
	return api.AdminConfigurationChangeResponse{OK: true, Applied: true, Proposed: f.configuration}, nil
}

func (f *fakeMinecraftWorkspaceAPI) Whitelist(_ context.Context, session string) (api.TextOutputResponse, error) {
	if session != "session-token" {
		return api.TextOutputResponse{}, api.ErrUnauthorized
	}
	return api.TextOutputResponse{Output: f.whitelist, WhitelistEnabled: &f.configuration.Minecraft.WhitelistEnabled}, nil
}

func (f *fakeMinecraftWorkspaceAPI) WhitelistChange(_ context.Context, session, platform, action, name string) (api.TextOutputResponse, error) {
	if session != "session-token" {
		return api.TextOutputResponse{}, api.ErrUnauthorized
	}
	f.whitelistRequest = [3]string{platform, action, name}
	return f.whitelistChange, nil
}

func (f *fakeMinecraftWorkspaceAPI) MinecraftLogs(_ context.Context, session string, limit int) (api.LogsResponse, error) {
	if session != "session-token" {
		return api.LogsResponse{}, api.ErrUnauthorized
	}
	if limit != 100 {
		return api.LogsResponse{}, &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "unexpected log limit"}
	}
	return api.LogsResponse{Lines: f.logs}, nil
}

func configuredMinecraftWorkspaceAPI() *fakeMinecraftWorkspaceAPI {
	client := &fakeMinecraftWorkspaceAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.JavaMemory = "4G"
	client.configuration.Minecraft.ContainerMemory = "6G"
	client.configuration.Minecraft.JavaPort = 25565
	client.configuration.Minecraft.BedrockEnabled = true
	client.configuration.Minecraft.BedrockPort = 19132
	client.configuration.Minecraft.Timezone = "America/Toronto"
	client.configuration.Minecraft.MaxPlayers = 10
	client.configuration.Minecraft.MOTD = "JustVoxel"
	client.configuration.Minecraft.GameMode = "survival"
	client.configuration.Minecraft.ImageTag = "stable"
	client.configuration.Minecraft.VersionMode = "pinned"
	client.configuration.Minecraft.Version = "1.21.8"
	client.configuration.Backup.Keep = 7
	client.configuration.Backup.Schedule = "*-*-* 04:30:00"
	client.configuration.Backup.TimerEnabled = true
	client.defaults.SystemMemoryMiB = 16384
	client.defaults.SystemReserveMinimumMiB = 1024
	client.defaults.SystemReserveRecommendedMiB = 2048
	return client
}

func TestMinecraftWorkspaceSettingsUsesNativeJSONDiscovery(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/settings", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"configured":true`, `"java_memory":"4G"`, `"max_players":10`, `"game_mode":"survival"`, `"system_memory_mib":16384`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("settings response missing %q: %s", want, rr.Body.String())
		}
	}
	if client.configurationHit != 1 || client.defaultsHit != 1 {
		t.Fatalf("configuration calls=%d defaults calls=%d", client.configurationHit, client.defaultsHit)
	}
}

func TestMinecraftWorkspaceMemoryPlanPreservesOtherCurrentSettings(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.planResult = api.AdminConfigurationChangeResponse{OK: true}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&tab=memory&java_memory=6G&container_memory=8G"
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.planned.JavaMemory != "6G" || client.planned.ContainerMemory != "8G" {
		t.Fatalf("memory fields not applied to request: %#v", client.planned)
	}
	if client.planned.MaxPlayers != 10 || client.planned.MOTD != "JustVoxel" || client.planned.JavaPort != 25565 {
		t.Fatalf("unrelated Minecraft settings were not preserved: %#v", client.planned)
	}
	if client.planned.BackupKeep != 7 || client.planned.BackupSchedule != "*-*-* 04:30:00" || !client.planned.BackupTimerEnabled {
		t.Fatalf("backup settings were not preserved: %#v", client.planned)
	}
}

func TestMinecraftWorkspaceCrossplayPlanCanDisableBedrockWithoutChangingOtherTabs(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.planResult = api.AdminConfigurationChangeResponse{OK: true}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&tab=crossplay&java_port=25566&bedrock_port=19133"
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.planned.JavaPort != 25566 || client.planned.BedrockPort != 19133 || client.planned.BedrockEnabled {
		t.Fatalf("cross-play request not mapped correctly: %#v", client.planned)
	}
	if client.planned.JavaMemory != "4G" || client.planned.ImageTag != "stable" || client.planned.MaxPlayers != 10 {
		t.Fatalf("cross-play update changed unrelated fields: %#v", client.planned)
	}
}

func TestMinecraftWorkspaceGameplayGameModePlanAndApplyPreserveConfiguration(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.planResult = api.AdminConfigurationChangeResponse{
		OK:              true,
		Changes:         []api.AdminConfigurationChange{{Field: "game_mode", Label: "Game mode", Before: "Survival", After: "Creative", RestartRequired: true}},
		RestartRequired: true,
	}
	client.applyResult = api.AdminConfigurationChangeResponse{OK: true, Applied: true, RestartRequired: true, RestartDeferred: true}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&tab=gameplay&max_players=12&motd=Creative+world&game_mode=creative"
	for _, path := range []string{"plan", "apply"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/"+path, body))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
		request := client.planned
		if path == "apply" {
			request = client.applied
		}
		if request.MaxPlayers != 12 || request.MOTD != "Creative world" || request.GameMode != "creative" {
			t.Fatalf("%s gameplay fields: %#v", path, request)
		}
		if request.VersionPolicy != "pinned" || request.Version != "1.21.8" || request.ImageTag != "stable" ||
			request.JavaMemory != "4G" || request.ContainerMemory != "6G" || request.JavaPort != 25565 ||
			!request.BedrockEnabled || request.BedrockPort != 19132 || request.Timezone != "America/Toronto" ||
			request.BackupKeep != 7 || request.BackupSchedule != "*-*-* 04:30:00" || !request.BackupTimerEnabled {
			t.Fatalf("%s changed unrelated settings: %#v", path, request)
		}
		if request.ConfirmPlayers {
			t.Fatalf("%s requested player confirmation for a deferred restart", path)
		}
		if path == "plan" {
			for _, want := range []string{`"label":"Game mode"`, `"before":"Survival"`, `"after":"Creative"`, `"restart_required":true`, `"memory_restart_required":false`, `"confirmation_required":false`} {
				if !strings.Contains(rr.Body.String(), want) {
					t.Fatalf("plan response missing %q: %s", want, rr.Body.String())
				}
			}
		} else {
			for _, want := range []string{`"restart_deferred":true`, `"restarted":false`} {
				if !strings.Contains(rr.Body.String(), want) {
					t.Fatalf("apply response missing %q: %s", want, rr.Body.String())
				}
			}
		}
	}
}

func TestMinecraftWorkspaceGameplayRejectsInvalidGameMode(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "hardcore", "CREATIVE"} {
		body := "csrf=csrf-token&tab=gameplay&max_players=10&motd=JustVoxel&game_mode=" + mode
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/plan", body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("mode %q status=%d body=%s", mode, rr.Code, rr.Body.String())
		}
	}
}

func TestMinecraftWorkspaceSettingsRejectOperatorBeforeAdminDiscovery(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.role = "operator"
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/settings", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator settings status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.configurationHit != 0 || client.defaultsHit != 0 {
		t.Fatalf("privileged discovery ran for operator: configuration=%d defaults=%d", client.configurationHit, client.defaultsHit)
	}
}

func TestMinecraftWorkspaceOperatorWhitelistAndLogsAreNativeJSON(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.role = "operator"
	client.whitelist = "There are 2 whitelisted player(s): Alex, Steve"
	client.whitelistChange = api.TextOutputResponse{Output: "updated"}
	client.logs = []string{"line one", "line two"}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/whitelist", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Alex") {
		t.Fatalf("whitelist status=%d body=%s", rr.Code, rr.Body.String())
	}

	body := "csrf=csrf-token&platform=bedrock&action=add&name=PlayerOne"
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/whitelist", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("whitelist change status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.whitelistRequest != [3]string{"bedrock", "add", "PlayerOne"} {
		t.Fatalf("unexpected whitelist request: %#v", client.whitelistRequest)
	}

	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/logs", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "line one") {
		t.Fatalf("logs status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMinecraftWorkspaceClientDoesNotRenderLegacyPages(t *testing.T) {
	sourceBytes, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	start := strings.Index(source, `const minecraftOpen = document.querySelector("[data-minecraft-open]");`)
	end := strings.Index(source, `const systemWorkspaceOpen = document.querySelector("[data-system-open]");`)
	if start < 0 || end <= start {
		t.Fatal("Minecraft workspace source block not found")
	}
	block := source[start:end]

	for _, forbidden := range []string{"/settings/server", "/operations", "DOMParser", "settings.css", "settings.js"} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("Minecraft workspace still depends on legacy rendering path %q", forbidden)
		}
	}
	for _, required := range []string{
		"/api/dashboard-status",
		"/api/minecraft/workspace/settings",
		"/api/minecraft/workspace/settings/plan",
		"/api/minecraft/workspace/settings/apply",
		"/api/minecraft/workspace/whitelist",
		"/api/minecraft/workspace/logs",
	} {
		if !strings.Contains(block, required) {
			t.Fatalf("Minecraft workspace missing native endpoint %q", required)
		}
	}

	headerBytes, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(headerBytes), "data-minecraft-workspace-csrf") {
		t.Fatal("Minecraft workspace is missing its explicit CSRF source")
	}
}

func TestMinecraftWorkspaceGameplayTabRendersGameModeAndDeferredReview(t *testing.T) {
	headerBytes, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="nav-admin-only" type="button" data-minecraft-tab="gameplay" aria-selected="false">Gameplay</button>`,
		`class="nav-operator-plus" type="button" data-minecraft-tab="players" aria-selected="false">Players</button>`,
	} {
		if !strings.Contains(string(headerBytes), want) {
			t.Fatalf("Minecraft Settings missing tab %q", want)
		}
	}
	if strings.Contains(string(headerBytes), `data-minecraft-tab="whitelist"`) {
		t.Fatal("Minecraft Settings still has a standalone Whitelist tab")
	}
	sourceBytes, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	start := strings.Index(source, `} else if (tab === "gameplay") {`)
	if start < 0 {
		t.Fatal("Minecraft Gameplay form block not found")
	}
	end := strings.Index(source[start+1:], `} else if (tab === "crossplay") {`)
	if end < 0 {
		t.Fatal("Minecraft Gameplay form block not found")
	}
	players := source[start : start+1+end]
	for _, want := range []string{
		`<label>Maximum players<input name="max_players"`,
		`<label>Server welcome message (MOTD)<input name="motd" required>`,
		`<label>Game mode<select name="game_mode" required>`,
		`<option value="survival">Survival</option>`,
		`<option value="creative">Creative</option>`,
		`<option value="adventure">Adventure</option>`,
		`<option value="spectator">Spectator</option>`,
		`section.querySelector('[name="game_mode"]').value = minecraft.game_mode`,
		`form.appendChild(section)`,
	} {
		if !strings.Contains(players, want) {
			t.Fatalf("Minecraft Gameplay form missing %q", want)
		}
	}
	for _, want := range []string{
		`new FormData(form).forEach((value, key) => params.append(key, String(value)))`,
		`change.label || change.field`,
		`minecraftSettingsReviewValue(change, change.before || "—")`,
		`minecraftSettingsReviewValue(change, change.after || "—")`,
		`The new settings will be saved, but Minecraft will not restart automatically for these non-memory changes.`,
		`Minecraft settings saved. Restart remains pending.`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("Minecraft review or apply flow missing %q", want)
		}
	}
}

func TestMinecraftWorkspaceCrossplayUsesExistingCheckboxSwitch(t *testing.T) {
	sourceBytes, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	for _, want := range []string{
		`<label class="minecraft-native-toggle system-ups-shutdown-switch"><span>Enable Bedrock cross-play</span><input name="bedrock_enabled" type="checkbox"></label>`,
		`section.querySelector('[name="bedrock_enabled"]').checked = Boolean(minecraft.bedrock_enabled);`,
		`Geyser/Floodgate compatible with ${status.geyser_supported_version}.`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("Cross-play form missing %q", want)
		}
	}
	cssBytes, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.system-ups-shutdown-switch input[type=checkbox]`,
		`.system-ups-shutdown-switch input[type=checkbox]:checked`,
		`.system-ups-shutdown-switch input[type=checkbox]:focus-visible`,
	} {
		if !strings.Contains(string(cssBytes), want) {
			t.Fatalf("existing switch styling missing %q", want)
		}
	}
}

func TestMinecraftWorkspaceReviewHumanizesOnlyBedrockValues(t *testing.T) {
	sourceBytes, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	helperStart := strings.Index(source, `const minecraftSettingsReviewValue = (change, value) => {`)
	reviewStart := strings.Index(source, `const renderSettingsReview = (root, plan, params) => {`)
	if helperStart < 0 || reviewStart <= helperStart {
		t.Fatal("Minecraft settings review source blocks not found")
	}
	reviewEnd := strings.Index(source[reviewStart:], `const renderSettingsTab = async`)
	if reviewEnd < 0 {
		t.Fatal("Minecraft settings review end not found")
	}
	helper := source[helperStart:reviewStart]
	review := source[reviewStart : reviewStart+reviewEnd]
	for _, want := range []string{
		`if (change.field !== "bedrock_enabled") return value;`,
		`String(value).trim().toLowerCase()`,
		`case "yes":`,
		`case "true":`,
		`case "on":`,
		`return "Enabled";`,
		`case "no":`,
		`case "false":`,
		`case "off":`,
		`return "Disabled";`,
		`default:`,
		`return value;`,
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("Minecraft review value helper missing %q", want)
		}
	}
	if !strings.Contains(helper, "case \"yes\":\n      case \"true\":\n      case \"on\":\n        return \"Enabled\";") ||
		!strings.Contains(helper, "case \"no\":\n      case \"false\":\n      case \"off\":\n        return \"Disabled\";") {
		t.Error("Minecraft review must map yes/true/on to Enabled and no/false/off to Disabled")
	}
	if strings.Contains(review, `document.createElement("code")`) {
		t.Error("Minecraft review still renders change values as code")
	}
	for _, want := range []string{
		`const before = document.createElement("span");`,
		`const after = document.createElement("span");`,
		`before.className = "minecraft-native-change-value";`,
		`after.className = "minecraft-native-change-value";`,
		`before.textContent = minecraftSettingsReviewValue(change, change.before || "—");`,
		`after.textContent = minecraftSettingsReviewValue(change, change.after || "—");`,
		`arrow.textContent = "→";`,
		`const applyParams = new URLSearchParams(params);`,
		`requestWorkspaceJSON("/api/minecraft/workspace/settings/apply", {`,
		`body: applyParams.toString(),`,
	} {
		if !strings.Contains(review, want) {
			t.Errorf("Minecraft review missing %q", want)
		}
	}
	if !strings.Contains(source, `requestWorkspaceJSON("/api/minecraft/workspace/settings/plan", {`) ||
		!strings.Contains(source, `body: params.toString(),`) {
		t.Error("Minecraft settings plan request source changed")
	}

	cssBytes, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	if !strings.Contains(css, `.minecraft-native-change-value{max-width:300px;min-width:0;overflow-wrap:anywhere;font-family:inherit}`) ||
		strings.Contains(css, `.minecraft-native-change-row code{`) {
		t.Error("Minecraft review values must use wrapping, normal-font CSS")
	}
}

func TestMinecraftWorkspaceWhitelistParsing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		output  string
		parsed  bool
		entries []minecraftWorkspaceWhitelistEntry
	}{
		{"imported identities", "There are 5 whitelisted player(s): .SirGiggles0, SirGiggles0, .CatchaLlama, .T025712, bagel2716", true, []minecraftWorkspaceWhitelistEntry{
			{Display: "SirGiggles0", Platform: "bedrock", Identity: ".SirGiggles0"},
			{Display: "SirGiggles0", Platform: "java", Identity: "SirGiggles0"},
			{Display: "CatchaLlama", Platform: "bedrock", Identity: ".CatchaLlama"},
			{Display: "T025712", Platform: "bedrock", Identity: ".T025712"},
			{Display: "bagel2716", Platform: "java", Identity: "bagel2716"},
		}},
		{"one leading dot only", "There are 1 whitelisted player(s): ..Player", true, []minecraftWorkspaceWhitelistEntry{{Display: ".Player", Platform: "bedrock", Identity: "..Player"}}},
		{"blank", " \n", true, []minecraftWorkspaceWhitelistEntry{}},
		{"zero", "There are 0 whitelisted player(s):\n", true, []minecraftWorkspaceWhitelistEntry{}},
		{"no players", "There are no whitelisted players\n", true, []minecraftWorkspaceWhitelistEntry{}},
		{"unexpected", "RCON is unavailable", false, []minecraftWorkspaceWhitelistEntry{}},
		{"count mismatch", "There are 2 whitelisted player(s): Alex", false, []minecraftWorkspaceWhitelistEntry{}},
		{"empty identity", "There are 2 whitelisted player(s): Alex,", false, []minecraftWorkspaceWhitelistEntry{}},
		{"dot only", "There are 1 whitelisted player(s): .", false, []minecraftWorkspaceWhitelistEntry{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, parsed := parseMinecraftWorkspaceWhitelist(tc.output)
			if parsed != tc.parsed || !reflect.DeepEqual(entries, tc.entries) {
				t.Fatalf("got entries=%#v parsed=%v, want entries=%#v parsed=%v", entries, parsed, tc.entries, tc.parsed)
			}
		})
	}
}

func TestMinecraftWorkspaceWhitelistPermissionsAndStructuredResponse(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			client := configuredMinecraftWorkspaceAPI()
			client.role = role
			client.whitelist = "There are 2 whitelisted player(s): .SirGiggles0, SirGiggles0"
			app, err := New(client, Config{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/whitelist", ""))
			if role == "viewer" {
				if rr.Code != http.StatusForbidden {
					t.Fatalf("viewer GET status=%d", rr.Code)
				}
			} else {
				var response minecraftWorkspaceWhitelistResponse
				if rr.Code != http.StatusOK {
					t.Fatalf("GET status=%d", rr.Code)
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if !response.OK || !response.Parsed || len(response.Entries) != 2 || response.Entries[0].Identity != ".SirGiggles0" || response.Entries[1].Identity != "SirGiggles0" {
					t.Fatalf("unexpected response: %#v", response)
				}
				for _, output := range []string{"", "Unexpected backend response"} {
					client.whitelist = output
					rr = httptest.NewRecorder()
					app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/whitelist", ""))
					if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"entries":[]`) {
						t.Fatalf("fallback status=%d body=%s", rr.Code, rr.Body.String())
					}
					if output != "" && (!strings.Contains(rr.Body.String(), `"parsed":false`) || !strings.Contains(rr.Body.String(), output)) {
						t.Fatalf("missing fallback: %s", rr.Body.String())
					}
				}
			}
			for _, mutation := range [][3]string{{"java", "add", "Alex"}, {"java", "remove", "SirGiggles0"}, {"bedrock", "remove", "SirGiggles0"}} {
				rr = httptest.NewRecorder()
				body := "csrf=csrf-token&platform=" + mutation[0] + "&action=" + mutation[1] + "&name=" + mutation[2]
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/whitelist", body))
				if role == "viewer" {
					if rr.Code != http.StatusForbidden || client.whitelistRequest != [3]string{} {
						t.Fatalf("viewer mutation status=%d request=%#v", rr.Code, client.whitelistRequest)
					}
				} else if rr.Code != http.StatusOK || client.whitelistRequest != mutation {
					t.Fatalf("mutation status=%d request=%#v", rr.Code, client.whitelistRequest)
				}
			}
			client.whitelistRequest = [3]string{}
			rr = httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/whitelist", "csrf=wrong&platform=java&action=remove&name=Alex"))
			if rr.Code != http.StatusForbidden || client.whitelistRequest != [3]string{} {
				t.Fatalf("CSRF bypass status=%d", rr.Code)
			}
		})
	}
}

func TestMinecraftWorkspacePlayersClientUsesBoundedWhitelistActions(t *testing.T) {
	sourceBytes, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	start := strings.Index(source, "  const renderPlayers =")
	end := strings.Index(source, "  const renderLogs =")
	if start < 0 || end <= start {
		t.Fatal("Players source block missing")
	}
	block := source[start:end]
	for _, want := range []string{
		`requestWorkspaceJSON("/api/dashboard-status")`,
		`Online players:`, `onlineIdentities.has(entry.identity) ? "Online" : "Offline"`,
		`const onlineIdentities = new Set`, `Platform</th>`, `Player</th>`, `Status</th>`, `Actions</th>`,
		`name="platform"`, `name="name"`, `value="java"`, `value="bedrock"`,
		`payload.whitelist_enabled`, `payload.can_toggle`, `toggle.setAttribute("role", "switch")`,
		`toggle.setAttribute("aria-checked", String(enabled))`, `toggle.type = "button"`,
		`enabled === true ? "Enabled" : enabled === false ? "Disabled"`,
		`if (enabled && !window.confirm(`, `Players not on the whitelist will be allowed to join.`,
		`enabled: String(!enabled)`, `await loadCurrentTab();`,
		`payload.entries.forEach`, `row.dataset.playerIdentity = entry.identity`,
		`remove.textContent = "Remove"`, `remove.addEventListener("click"`,
		`platform: entry.platform`, `action: "remove"`,
		`name: entry.platform === "bedrock" ? entry.display : entry.identity`,
		`params.set("action", "add")`, `params.set("csrf", csrf)`,
		`requestWorkspaceJSON("/api/minecraft/workspace/whitelist", {`, `method: "POST"`,
		`await loadCurrentTab(result.message || "Whitelist updated.")`, `Add player`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Players missing %q", want)
		}
	}
	for _, forbidden := range []string{`<h2>Online</h2>`, `No players online.`, `player-chip`, `name="action"`, `row.addEventListener`, `console/command`, `name="command"`, `action: "ban"`, `action: "kick"`, `action: "pardon"`} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("unexpected Players surface %q", forbidden)
		}
	}
	if !strings.Contains(source, `new Set(["memory", "gameplay", "crossplay"])`) || !strings.Contains(source, `currentTab === "players") await renderPlayers`) {
		t.Fatal("incorrect gameplay/players dispatch")
	}
}

func TestMinecraftWorkspaceWhitelistTogglePreservesConfigurationAndPermissions(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		for _, enabled := range []bool{true, false} {
			t.Run(role+"/"+strconv.FormatBool(enabled), func(t *testing.T) {
				client := configuredMinecraftWorkspaceAPI()
				client.role = role
				client.configuration.Minecraft.WhitelistEnabled = !enabled
				client.configuration.Minecraft.EnforceWhitelist = true
				app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
				if err != nil {
					t.Fatal(err)
				}
				rr := httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/whitelist/state", "csrf=csrf-token&enabled="+strconv.FormatBool(enabled)))
				if role != "administrator" {
					if rr.Code != http.StatusForbidden || client.applied.WhitelistEnabled != nil {
						t.Fatalf("unauthorized toggle: %d %#v", rr.Code, client.applied)
					}
					return
				}
				if rr.Code != http.StatusOK {
					t.Fatalf("toggle: %d %s", rr.Code, rr.Body.String())
				}
				expected := configurationRequestFromDiscovery(client.configuration)
				expected.WhitelistEnabled = &enabled
				if !reflect.DeepEqual(client.applied, expected) {
					t.Fatalf("toggle changed other settings: %#v", client.applied)
				}
				if !client.configuration.Minecraft.EnforceWhitelist {
					t.Fatal("enforcement changed")
				}
			})
		}
	}
}

func TestMinecraftWorkspaceWhitelistStateAndPreservation(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		client := configuredMinecraftWorkspaceAPI()
		client.configuration.Minecraft.WhitelistEnabled = enabled
		for _, role := range []string{"administrator", "operator"} {
			client.role = role
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/whitelist", ""))
			var out minecraftWorkspaceWhitelistResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.WhitelistEnabled == nil || *out.WhitelistEnabled != enabled || out.CanToggle != (role == "administrator") {
				t.Fatalf("incorrect state: %#v", out)
			}
		}
		request := configurationRequestFromDiscovery(client.configuration)
		if request.WhitelistEnabled == nil || *request.WhitelistEnabled != enabled {
			t.Fatal("configuration did not preserve whitelist")
		}
	}
}
