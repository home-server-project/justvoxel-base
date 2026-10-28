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
	selectedIDs    []string
	instanceReads  []string
	instanceWrites []string
	fakeDiscoveryAPI
	applied          api.AdminConfigurationChangeRequest
	whitelist        string
	whitelistChange  api.TextOutputResponse
	whitelistRequest [3]string
	logs             []string
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

func (f *fakeMinecraftWorkspaceAPI) MinecraftLogs(_ context.Context, session, id string, limit int) (api.LogsResponse, error) {
	if session != "session-token" {
		return api.LogsResponse{}, api.ErrUnauthorized
	}
	if limit != 100 {
		return api.LogsResponse{}, &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "unexpected log limit"}
	}
	return api.LogsResponse{Lines: f.logs}, nil
}

func configuredMinecraftWorkspaceAPI() *fakeMinecraftWorkspaceAPI {
	client := &fakeMinecraftWorkspaceAPI{selectedIDs: []string{"jv-family1"}}
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
	client.defaults.SystemReserveRecommendedMiB = 1536
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
	if client.configurationHit != 0 || client.defaultsHit != 1 || len(client.instanceReads) != 1 || client.instanceReads[0] != "jv-family1" {
		t.Fatalf("configuration calls=%d defaults calls=%d", client.configurationHit, client.defaultsHit)
	}
}

func TestMinecraftWorkspaceMemoryReviewIsReadOnlyAndTargetsInstance(t *testing.T) {
	app, f := phase7App(t)
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", "csrf=csrf-token&tab=memory&java_memory=6G&container_memory=8G")
	if w.Code != 200 || len(f.writes) != 0 || !strings.Contains(w.Body.String(), `"review":`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMinecraftWorkspaceCrossplayReviewKeepsSiblingConfiguration(t *testing.T) {
	app, f := phase7App(t)
	before := f.runtimes["jv-kids001"]
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", "csrf=csrf-token&tab=crossplay&java_port=25566&bedrock_port=19133")
	if w.Code != 200 || len(f.writes) != 0 || !reflect.DeepEqual(before, f.runtimes["jv-kids001"]) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMinecraftWorkspaceGameplayReviewMustMatchBeforeApply(t *testing.T) {
	app, f := phase7App(t)
	body := "csrf=csrf-token&tab=gameplay&motd=Family&max_players=10&game_mode=creative"
	plan := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
	var review struct {
		Review string `json:"review"`
	}
	if json.Unmarshal(plan.Body.Bytes(), &review) != nil || review.Review == "" {
		t.Fatal(plan.Body.String())
	}
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+review.Review)
	if w.Code != 200 || !reflect.DeepEqual(f.writes, []string{"jv-family1"}) || f.runtimes["jv-family1"].GameMode != "creative" {
		t.Fatal(w.Code, w.Body.String())
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
		`<input name="motd" type="hidden">`,
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
		`section.querySelector('[name="bedrock_enabled"]').disabled = !["paper", "purpur", "spigot", "pufferfish", "leaf"].includes(minecraft.server_type);`,
		`Geyser/Floodgate compatible with ${status.geyser_supported_version}`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("Cross-play form missing %q", want)
		}
	}
	cssBytes, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	// Cross-play's plain checkbox uses the shared switch rules, which exclude
	// only the separate destructive confirmation controls.
	const switchSelector = `input[type=checkbox]:not(.destructive-confirm-toggle):not(.storage-confirm-toggle)`
	for _, rule := range []struct {
		selector     string
		declarations []string
	}{
		{switchSelector, []string{`appearance:none`, `width:42px`, `height:24px`, `border-radius:999px`, `background:#303b42`}},
		{switchSelector + `::before`, []string{`content:""`, `width:16px`, `height:16px`, `border-radius:50%`, `background:#eef1f4`}},
		{switchSelector + `:checked`, []string{`border-color:#68bd8e`, `background:#68bd8e`}},
		{switchSelector + `:checked::before`, []string{`transform:translateX(18px)`, `background:#07130d`}},
		{switchSelector + `:focus-visible`, []string{`outline:2px solid #78c99d`, `outline-offset:2px`}},
		{switchSelector + `:disabled`, []string{`cursor:not-allowed`}},
		{`.system-ups-shutdown-switch:has(input:disabled)`, []string{`opacity:.58`, `cursor:not-allowed`}},
	} {
		_, body, found := strings.Cut(css, rule.selector+"{")
		if !found {
			t.Fatalf("shared switch styling missing selector %q", rule.selector)
		}
		body, _, found = strings.Cut(body, "}")
		if !found {
			t.Fatalf("shared switch styling has unterminated rule %q", rule.selector)
		}
		for _, want := range rule.declarations {
			if !strings.Contains(";"+body+";", ";"+want+";") {
				t.Fatalf("shared switch rule %q missing declaration %q", rule.selector, want)
			}
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
	reviewStart := strings.Index(source, `const renderSettingsReview = (root, plan, params, options = {}) => {`)
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

// This fixture models one actual registered instance. Its configuration payload
// is shared with old discovery-page fixtures only as test input data.
func (f *fakeMinecraftWorkspaceAPI) MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error) {
	out := []api.RegisteredMinecraftInstance{}
	for _, id := range f.selectedIDs {
		out = append(out, api.RegisteredMinecraftInstance{ID: id, DisplayName: "Family"})
	}
	return out, nil
}
func (f *fakeMinecraftWorkspaceAPI) MinecraftInstanceRuntimes(context.Context, string) ([]string, error) {
	return f.selectedIDs, nil
}
func (f *fakeMinecraftWorkspaceAPI) MinecraftInstanceConfiguration(_ context.Context, _, id string) (api.InstanceRuntimeSettings, error) {
	f.instanceReads = append(f.instanceReads, id)
	if len(f.selectedIDs) != 1 || id != f.selectedIDs[0] {
		return api.InstanceRuntimeSettings{}, &api.ResponseError{StatusCode: 404}
	}
	c := f.configuration.Minecraft
	return api.InstanceRuntimeSettings{JavaPort: c.JavaPort, BedrockEnabled: c.BedrockEnabled, BedrockPort: c.BedrockPort, JavaMemory: c.JavaMemory, ContainerMemory: c.ContainerMemory, Software: c.ServerType, Version: c.Version, VersionPolicy: c.VersionMode, ImageTag: c.ImageTag, Timezone: c.Timezone, GameMode: c.GameMode, Difficulty: c.Difficulty, MaxPlayers: c.MaxPlayers, Whitelist: c.WhitelistEnabled, EnforceWhitelist: c.EnforceWhitelist, MOTD: c.MOTD}, nil
}
func (f *fakeMinecraftWorkspaceAPI) MinecraftInstanceStatus(_ context.Context, _, id string) (api.MinecraftInstanceStatus, error) {
	return api.MinecraftInstanceStatus{ID: id, State: "stopped"}, nil
}
func (f *fakeMinecraftWorkspaceAPI) MinecraftInstancePlayers(_ context.Context, _, id string) (api.InstancePlayers, error) {
	return api.InstancePlayers{ID: id, State: "offline"}, nil
}
func (f *fakeMinecraftWorkspaceAPI) MinecraftInstanceLogs(_ context.Context, _, id string) (string, error) {
	f.instanceReads = append(f.instanceReads, id)
	return strings.Join(f.logs, "\n"), nil
}
func (f *fakeMinecraftWorkspaceAPI) SetMinecraftInstanceConfiguration(_ context.Context, _, id string, c api.InstanceRuntimeSettings) error {
	f.instanceWrites = append(f.instanceWrites, id)
	f.applied = configurationRequestFromDiscovery(f.configuration)
	f.applied.JavaMemory = c.JavaMemory
	f.applied.ContainerMemory = c.ContainerMemory
	f.applied.MOTD = c.MOTD
	f.applied.MaxPlayers = c.MaxPlayers
	f.applied.GameMode = c.GameMode
	f.applied.JavaPort = c.JavaPort
	f.applied.BedrockEnabled = c.BedrockEnabled
	f.applied.BedrockPort = c.BedrockPort
	enabled := c.Whitelist
	f.applied.WhitelistEnabled = &enabled
	return nil
}
func (f *fakeMinecraftWorkspaceAPI) RenameMinecraftInstance(context.Context, string, string, string) error {
	return nil
}
func (f *fakeMinecraftWorkspaceAPI) DeleteMinecraftInstance(context.Context, string, string, string) error {
	return nil
}
func (f *fakeMinecraftWorkspaceAPI) MinecraftInstanceWhitelist(ctx context.Context, session, id string, request any) (api.TextOutputResponse, error) {
	f.instanceReads = append(f.instanceReads, id)
	if len(f.selectedIDs) != 1 || id != f.selectedIDs[0] {
		return api.TextOutputResponse{}, &api.ResponseError{StatusCode: 404}
	}
	if request == nil {
		return f.Whitelist(ctx, session)
	}
	v := request.(map[string]string)
	return f.WhitelistChange(ctx, session, v["platform"], v["action"], v["name"])
}

func TestMinecraftRecentLogsKeepsOperatorPlusAuthorization(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			client := configuredMinecraftWorkspaceAPI()
			client.role = role
			client.logs = []string{"private Minecraft log"}
			app, err := New(client, Config{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/logs", ""))
			if role == "viewer" {
				if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), "private Minecraft log") {
					t.Fatalf("Viewer log access status=%d body=%s", rr.Code, rr.Body.String())
				}
			} else if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "private Minecraft log") {
				t.Fatalf("%s log access status=%d body=%s", role, rr.Code, rr.Body.String())
			}
		})
	}
}
