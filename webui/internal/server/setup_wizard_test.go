package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
)

const vanillaBedrockWarning = "Bedrock cross-play is currently not supported for Vanilla. Select Paper or Purpur on Step 1."

func assertSetupApplicationLogo(t *testing.T, body string) {
	t.Helper()
	headerTemplate, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	const logo = `<img class="brand-logo" src="/static/justvoxel-logo.png" alt="JustVoxel">`
	if !strings.Contains(string(headerTemplate), logo) {
		t.Fatal("expected logo no longer matches the normal application header")
	}
	start := strings.Index(body, "<header ")
	end := strings.Index(body, "</header>")
	if start < 0 || end < start {
		t.Fatal("setup header missing")
	}
	header := body[start:end]
	if !strings.Contains(header, `class="brand-link brand-mark"`) || !strings.Contains(header, logo) || strings.Contains(header, "<strong>JV</strong>") {
		t.Fatalf("setup header does not reuse the application logo: %s", header)
	}
}

func TestAdvancedConnectionsBedrockToggleCapabilities(t *testing.T) {
	for _, serverType := range []string{"paper", "purpur", "vanilla"} {
		t.Run(serverType, func(t *testing.T) {
			app, err := New(setupWizardClient(), Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			startSetup(t, app)
			page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
			assertSetupApplicationLogo(t, page.Body.String())
			values := validServerValues()
			values.Set("server_type", serverType)
			if rr := saveServerStep(t, app, values); rr.Code != http.StatusSeeOther {
				t.Fatalf("server selection returned %d: %s", rr.Code, rr.Body.String())
			}
			page = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
			if page.Code != http.StatusOK {
				t.Fatalf("Connections returned %d", page.Code)
			}
			body := page.Body.String()
			const toggleStart = `<input class="date-time-switch" type="checkbox" name="bedrock_enabled" data-bedrock-toggle`
			start := strings.Index(body, toggleStart)
			if start < 0 || !strings.Contains(body, `<label class="date-time-automatic">`) {
				t.Fatal("Bedrock control does not use the Date & Time switch pattern")
			}
			control := body[start : start+strings.Index(body[start:], ">")+1]
			vanilla := serverType == "vanilla"
			if strings.Contains(control, "disabled") != vanilla {
				t.Fatalf("incorrect disabled state: %s", control)
			}
			if vanilla && strings.Contains(control, "checked") {
				t.Fatalf("Vanilla toggle rendered enabled: %s", control)
			}
			warning := `<p class="notice error">` + vanillaBedrockWarning + `</p>`
			if strings.Contains(body, warning) != vanilla || strings.Contains(body, vanillaBedrockWarning) != vanilla {
				t.Fatal("incorrect Vanilla warning or danger class")
			}
			if !vanilla {
				connections := validConnectionValues()
				connections.Set("bedrock_enabled", "on")
				if rr := saveConnectionsStep(t, app, connections); rr.Code != http.StatusSeeOther {
					t.Fatalf("Bedrock submission returned %d: %s", rr.Code, rr.Body.String())
				}
				draft, _ := firstRunSetupDrafts.get(app, "session-token")
				if !draft.Server.BedrockEnabled {
					t.Fatal("supported server did not preserve Bedrock enabled")
				}
			}
		})
	}
}

func TestSetupMemoryPresetsMatchDefaultsAndPreserveCustomValues(t *testing.T) {
	script, err := assets.ReadFile("static/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	program := `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const { execFileSync } = require("node:child_process");
function backendDefaults(mib, swapMiB) {
  const shell = 'source "$1"; test_mem_kib="$2"; test_swap_kib="$3"; ' +
    'awk() { if [[ $# -eq 2 && $2 == /proc/meminfo ]]; then ' +
    'printf "MemTotal: %s kB\\nSwapTotal: %s kB\\n" "$test_mem_kib" "$test_swap_kib" | command awk "$1"; ' +
    'else command awk "$@"; fi; }; suggest_memory_values';
  return execFileSync("bash", ["-c", shell, "memory-policy", "../../../mjust/libexec/common.sh", String(mib * 1024), String(swapMiB * 1024)], { encoding: "utf8" }).trim().split(/\s+/);
}
const source = ` + fmt.Sprintf("%q", string(script)) + `;
function open(mib, heap, maximum, players = "10") {
  const input = (value) => ({ value, events: {}, addEventListener(name, fn) { this.events[name] = fn; }, focus() {} });
  const game = input(heap), max = input(maximum), player = input(players);
  const buttons = ["light", "recommended", "high", "custom"].map((name) => ({
    dataset: { memoryPreset: name }, events: {}, selected: false,
    addEventListener(event, fn) { this.events[event] = fn; },
    classList: { toggle(_, selected) { buttons.find((b) => b.dataset.memoryPreset === name).selected = selected; } }
  }));
  const panel = { dataset: { systemMemoryMib: String(mib), minReserveMib: "1024", recommendedReserveMib: "2048" }, querySelectorAll() { return buttons; } };
  const status = { classList: { remove() {}, add() {} } };
  const fields = { "java-memory": game, "container-memory": max, "max-players": player, "memory-status": status };
  const document = { querySelector(selector) { return selector === "[data-memory-settings]" ? panel : null; }, getElementById(id) { return fields[id] || null; } };
  vm.runInNewContext(source, { document, window: { location: { hash: "" } } });
  return { game, max, player, selected: () => buttons.find((b) => b.selected)?.dataset.memoryPreset, click: (name) => buttons.find((b) => b.dataset.memoryPreset === name).events.click() };
}
// Fresh discovery defaults come from suggest_memory_values and must select Recommended.
for (const [mib, heap, maximum] of [
  [1536, "256M", "512M"], [2560, "1G", "1536M"],
  [3840, "2G", "2816M"], [4096, "2G", "3G"],
  [5888, "3G", "4G"], [6144, "3G", "4G"],
  [7884, "4G", "6G"], [7936, "4G", "6G"], [8064, "4G", "6G"], [8192, "4G", "6G"],
  [12288, "8G", "10G"], [16384, "8G", "12G"], [32768, "8G", "12G"]
]) {
  for (const swapMiB of [0, 65536]) {
    const defaults = backendDefaults(mib, swapMiB);
    assert.deepEqual(defaults, [heap, maximum], String(mib) + " backend defaults");
    const discovered = open(mib, ...defaults);
    assert.equal(discovered.selected(), "recommended", String(mib) + " discovery preset");
  }
  const page = open(mib, heap, maximum);
  assert.equal(page.selected(), "recommended", String(mib));
  assert.deepEqual([page.game.value, page.max.value], [heap, maximum]);
  page.click("recommended");
  assert.deepEqual([page.game.value, page.max.value], [heap, maximum], String(mib));
}
for (const [name, heap, maximum] of [["light", "2G", "3G"], ["recommended", "4G", "6G"], ["high", "5G", "6G"], ["custom", "3G", "5G"]]) {
  const page = open(7884, heap, maximum);
  assert.equal(page.selected(), name);
  assert.deepEqual([page.game.value, page.max.value], [heap, maximum]);
  if (name !== "custom") {
    page.click(name);
    assert.deepEqual([page.game.value, page.max.value], [heap, maximum]);
  }
}
const morePlayers = open(7884, "4G", "6G", "30");
assert.equal(morePlayers.selected(), "recommended");
morePlayers.player.value = "40";
morePlayers.player.events.change();
assert.deepEqual([morePlayers.game.value, morePlayers.max.value], ["4G", "6G"]);
morePlayers.click("recommended");
assert.deepEqual([morePlayers.game.value, morePlayers.max.value], ["4G", "6G"]);
const edited = open(7884, "4G", "6G");
edited.game.value = "3G";
edited.game.events.input();
assert.equal(edited.selected(), "custom");
assert.deepEqual([edited.game.value, edited.max.value], ["3G", "6G"]);
edited.max.events.change();
assert.equal(edited.selected(), "custom");
assert.deepEqual([edited.game.value, edited.max.value], ["3G", "6G"]);
edited.player.value = "20";
edited.player.events.change();
assert.deepEqual([edited.game.value, edited.max.value], ["3G", "6G"]);
const revisited = open(7884, edited.game.value, edited.max.value, "20");
assert.equal(revisited.selected(), "custom");
assert.deepEqual([revisited.game.value, revisited.max.value], ["3G", "6G"]);
for (const field of [revisited.game, revisited.max]) {
  field.events.input();
  field.events.change();
  assert.equal(revisited.selected(), "custom");
  assert.deepEqual([revisited.game.value, revisited.max.value], ["3G", "6G"]);
}
edited.game.value = "4G";
edited.game.events.change();
edited.player.value = "10";
// Detect the restored default values without replacing either field.
edited.max.events.input();
assert.equal(edited.selected(), "recommended");
assert.deepEqual([edited.game.value, edited.max.value], ["4G", "6G"]);
for (const mib of [1536, 2048, 2560, 3072, 3584, 3840, 5632, 7680, 7884, 15872, 32256]) {
  for (const name of ["light", "recommended", "high"]) {
    const page = open(mib, "1G", "2G");
    page.click(name);
    const toMiB = (value) => Number(value.slice(0, -1)) * (value.endsWith("G") ? 1024 : 1);
    assert.ok(toMiB(page.game.value) > 0);
    assert.ok(toMiB(page.game.value) < toMiB(page.max.value));
    assert.ok(mib - toMiB(page.max.value) >= 1024, String(mib) + " " + name);
  }
}
const insufficient = open(1024, "256M", "512M");
assert.equal(insufficient.selected(), "custom");
insufficient.click("recommended");
assert.deepEqual([insufficient.game.value, insufficient.max.value], ["256M", "512M"]);
assert.equal(open(7884, "4096M", "6144M").selected(), "recommended");
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup memory preset behavior: %v\n%s", err, output)
	}
}

func TestSetupEightGBClassDefaultsReachBothWizardModes(t *testing.T) {
	for _, mode := range []string{"advanced", "recommended"} {
		t.Run(mode, func(t *testing.T) {
			client := setupWizardClient()
			client.defaults.SystemMemoryMiB = 7884
			client.defaults.JavaMemory = "4G"
			client.defaults.ContainerMemory = "6G"
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			if mode == "advanced" {
				startSetup(t, app)
			} else {
				values := url.Values{"csrf": {"csrf-token"}, "server_type": {"paper"}}
				rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recommended", values.Encode()))
				if rr.Code != http.StatusSeeOther {
					t.Fatalf("recommended setup returned %d: %s", rr.Code, rr.Body.String())
				}
			}
			draft, ok := firstRunSetupDrafts.get(app, "session-token")
			if !ok || draft.Minecraft.JavaMemory != "4G" || draft.Minecraft.ContainerMemory != "6G" {
				t.Fatalf("%s fresh setup lost Recommended 4G/6G defaults: %#v", mode, draft.Minecraft)
			}
		})
	}
}

func TestSetupWizardCompactDesktopProgressContract(t *testing.T) {
	css, err := assets.ReadFile("static/setup.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".setup-stage-header{display:flex",
		"grid-template-columns:repeat(7,minmax(0,1fr))",
		".setup-stage-header .setup-progress li{font-size:1rem;height:56px",
		".setup-stage-header h1{font-size:1.9rem",
		".setup-stage-header .setup-number{flex:0 0 1.8rem",
		".setup-stage-header .setup-progress li form{display:flex",
		"border-radius:inherit",
		".setup-stage-header .setup-progress li button:focus-visible",
		"grid-template-columns:repeat(4,minmax(0,1fr))",
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("Setup wizard header missing %q", want)
		}
	}
	for _, line := range strings.Split(string(css), "\n") {
		if strings.Contains(line, ".setup-review-panel") && strings.Contains(line, ".setup-progress") {
			t.Fatal("compact progress styling must not be limited to Review")
		}
	}
	markup, err := assets.ReadFile("templates/setup_review.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `{{if ne .SetupMode "recommended"}}`) || strings.Count(string(markup), `class="setup-number"`) != 7 || strings.Count(string(markup), `action="/setup/navigate"`) != 6 {
		t.Fatal("Advanced Review must retain seven steps and navigation; Recommended omits progress")
	}
	for i, label := range []string{"Server", "Cross-play", "Memory", "Version", "Storage", "Backups", "Review"} {
		if !strings.Contains(string(markup), fmt.Sprintf(`<span class="setup-number">%d</span><span>%s</span>`, i+1, label)) {
			t.Fatalf("Setup progress missing step %d: %s", i+1, label)
		}
	}
	wizard, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{string(wizard), string(markup)} {
		if strings.Contains(source, " of 7</span>") || strings.Contains(source, "Step {{.CurrentStep}} of") {
			t.Fatal("Setup header must not display redundant step badges")
		}
	}
	for _, want := range []string{`{{range .Steps}}`, `{{if .Active}}aria-current="step"{{end}}`, `{{if .Visited}}<form method="post" action="/setup/navigate">`, `name="direction" value="jump"`, `name="step" value="{{.Number}}"`, `<span class="setup-number">{{.Number}}</span><span>{{.Name}}</span>`} {
		if !strings.Contains(string(wizard), want) {
			t.Fatalf("Setup wizard progress navigation missing %q", want)
		}
	}
}

func setupWizardClient() *fakeDiscoveryAPI {
	client := &fakeDiscoveryAPI{}
	client.defaults.MOTD = "JustVoxel Java and Bedrock Server"
	client.defaults.MaxPlayers = 10
	client.defaults.BedrockEnabled = false
	client.defaults.Timezone = "America/Toronto"
	client.defaults.JavaMemory = "4G"
	client.defaults.ContainerMemory = "6G"
	client.defaults.JavaPort = 25565
	client.defaults.BedrockPort = 19132
	client.defaults.ImageTag = "stable"
	client.defaults.VersionMode = "pinned"
	client.defaults.SystemMemoryMiB = 8192
	client.defaults.SystemReserveMinimumMiB = 1024
	client.defaults.SystemReserveRecommendedMiB = 2048
	return client
}

func startSetup(t *testing.T, app *App) {
	t.Helper()
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/start", "csrf=csrf-token"))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("start returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
}

func saveServerStep(t *testing.T, app *App, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf", "csrf-token")
	return httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/server", values.Encode()))
}

func saveConnectionsStep(t *testing.T, app *App, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf", "csrf-token")
	return httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/connections", values.Encode()))
}

func saveResourcesStep(t *testing.T, app *App, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf", "csrf-token")
	return httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/resources", values.Encode()))
}

func saveMinecraftStep(t *testing.T, app *App, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf", "csrf-token")
	return httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/minecraft", values.Encode()))
}

func validServerValues() url.Values {
	return url.Values{
		"server_type": {"paper"},
		"motd":        {"Family Minecraft"},
		"max_players": {"20"},
		"game_mode":   {"survival"},
		"timezone":    {"America/Toronto"},
	}
}

func TestSetupGameModeDefaultAndSelection(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	draft, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok || draft.Minecraft.GameMode != "survival" {
		t.Fatalf("default game mode = %q", draft.Minecraft.GameMode)
	}
	values := validServerValues()
	values.Set("game_mode", "creative")
	if rr := saveServerStep(t, app, values); rr.Code != http.StatusSeeOther {
		t.Fatalf("Creative selection returned %d: %s", rr.Code, rr.Body.String())
	}
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if draft.Minecraft.GameMode != "creative" {
		t.Fatalf("selected game mode = %q", draft.Minecraft.GameMode)
	}
	for _, mode := range []string{"survival", "creative", "adventure", "spectator"} {
		if !validSetupGameMode(mode) {
			t.Fatalf("valid game mode %q rejected", mode)
		}
	}
	for _, mode := range []string{"", "hardcore", "creative\n", "survival; exit"} {
		if validSetupGameMode(mode) {
			t.Fatalf("invalid game mode %q accepted", mode)
		}
	}
}

func validConnectionValues() url.Values {
	return url.Values{
		"bedrock_enabled": {"on"},
		"java_port":       {"25565"},
		"bedrock_port":    {"19132"},
		"direction":       {"next"},
	}
}

func validResourceValues() url.Values {
	return url.Values{
		"java_memory":      {"4G"},
		"container_memory": {"6G"},
		"direction":        {"next"},
	}
}

func validMinecraftValues() url.Values {
	return url.Values{
		"java_memory":      {"4G"},
		"container_memory": {"6G"},
		"java_port":        {"25565"},
		"bedrock_port":     {"19132"},
		"image_tag":        {"stable"},
		"version_policy":   {"recommended"},
		"version":          {""},
		"direction":        {"next"},
	}
}

func TestSetupWizardShowsRecommendedAndAdvancedChoices(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("setup welcome returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Set up your Minecraft server", "Recommended setup", "Advanced setup", "Paper", "Purpur", "Vanilla",
		"Use recommended setup", "Start advanced setup", "You can change these settings later", "mjust setup", "setup-terminal-note",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup entry missing %q: %s", want, body)
		}
	}
	for _, want := range []string{`value="purpur" disabled`, `value="vanilla" disabled`} {
		if strings.Contains(body, want) {
			t.Fatalf("setup choice disabled %q: %s", want, body)
		}
	}
	if client.configurationHit != 1 {
		t.Fatalf("configuration discovery calls = %d, want 1", client.configurationHit)
	}
	assertSetupApplicationLogo(t, body)
}

func TestRecommendedSetupBuildsReadyPaperDraftAndJumpsToReview(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")

	values := url.Values{"csrf": {"csrf-token"}, "server_type": {"paper"}}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recommended", values.Encode()))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/review" {
		t.Fatalf("recommended setup returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}

	draft, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok {
		t.Fatal("recommended setup did not create a draft")
	}
	if draft.Mode != "recommended" || draft.Minecraft.ServerType != "paper" {
		t.Fatalf("recommended draft mode=%q server_type=%q", draft.Mode, draft.Minecraft.ServerType)
	}
	if !draft.Server.BedrockEnabled {
		t.Fatal("recommended Paper setup did not enable Bedrock by default")
	}
	if draft.Storage.Type != "system" || draft.Backups.Type != "system" {
		t.Fatalf("recommended storage=%q backups=%q, want system/system", draft.Storage.Type, draft.Backups.Type)
	}
	if !setupDraftReadyForReview(draft) {
		t.Fatalf("recommended draft is not ready for Review: %#v", draft)
	}
}

func TestRecommendedSetupRejectsUnsupportedServerTypes(t *testing.T) {
	for _, serverType := range []string{"fabric", "PAPER-invalid", ""} {
		client := setupWizardClient()
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		values := url.Values{"csrf": {"csrf-token"}, "server_type": {serverType}}
		rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recommended", values.Encode()))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s placeholder returned %d, want 400", serverType, rr.Code)
		}
		firstRunSetupDrafts.delete(app, "session-token")
	}
}

func TestSetupWizardStartsWithFriendlyServerDefaults(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("server step returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"<h2>Server</h2>", "Server name / welcome message", "Family", "Maximum players", "Server software", "Paper", "America/Toronto", "Technical name: MOTD", "data-timezone-search", "data-timezone-results", "data-timezone-value=\"UTC\""} {
		if want == "Family" {
			continue
		}
		if !strings.Contains(body, want) {
			t.Fatalf("server step missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "JustVoxel Java and Bedrock Server") || !strings.Contains(body, `type="hidden" name="motd_automatic" value="true"`) {
		t.Fatal("server step did not use automatic setup MOTD")
	}
	if client.defaultsHit != 1 {
		t.Fatalf("setup defaults calls = %d, want 1", client.defaultsHit)
	}
}

func TestSetupWizardTimezoneSearchUsesLocalDatabaseWithoutDatalist(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := page.Body.String()
	if strings.Contains(body, "<datalist") || strings.Contains(body, "list=\"timezone-options\"") {
		t.Fatal("advanced setup still uses the browser timezone datalist")
	}
	for _, want := range []string{"data-timezone-search", "data-timezone-results", "Toronto, Warsaw, Amsterdam", "no Internet service is required"} {
		if !strings.Contains(body, want) {
			t.Fatalf("timezone search UI missing %q: %s", want, body)
		}
	}

	script, err := assets.ReadFile("static/timezone-search.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"data-timezone-value", "scoreZone", "cityLabel", "ArrowDown", "chooseTimezone"} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("timezone search behavior missing %q", want)
		}
	}
	if !strings.Contains(body, "/static/timezone-search.js") {
		t.Fatal("Setup does not load the shared city search")
	}

	invalid := validServerValues()
	invalid.Set("timezone", "Mars/Olympus")
	rr := saveServerStep(t, app, invalid)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Choose a timezone from the JustVoxel timezone suggestions") {
		t.Fatalf("unknown timezone was not rejected: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSetupWizardConnectionsValidateBeforeResources(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d", rr.Code)
	}

	bad := validConnectionValues()
	bad.Set("java_port", "70000")
	rr := saveConnectionsStep(t, app, bad)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Minecraft Java port must be between 1 and 65535") {
		t.Fatalf("invalid Java port was not rejected: %d %s", rr.Code, rr.Body.String())
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != 2 {
		t.Fatalf("invalid Connections form advanced to step %d", draft.CurrentStep)
	}

	rr = saveConnectionsStep(t, app, validConnectionValues())
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("valid Connections form returned %d: %s", rr.Code, rr.Body.String())
	}
	resources := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if resources.Code != http.StatusOK || !strings.Contains(resources.Body.String(), "<h2>Memory</h2>") || !strings.Contains(resources.Body.String(), "Minecraft game memory") {
		t.Fatalf("Connections did not advance to Resources: %d %s", resources.Code, resources.Body.String())
	}
}

func TestSetupWizardUsesCompactAlignedActions(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	step1 := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := step1.Body.String()
	for _, want := range []string{"setup-step-actions", "setup-step-navigation", "Cancel setup", ">Continue</button>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("server actions missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Save server choices and continue") {
		t.Fatal("server step still uses the long continue label")
	}

	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d", rr.Code)
	}
	step2 := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body = step2.Body.String()
	for _, want := range []string{"Cancel setup", ">Back</button>", ">Continue</button>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Minecraft actions missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Save Minecraft choices and continue") {
		t.Fatal("Minecraft step still uses the long continue label")
	}

	template, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(template)
	for _, oldLabel := range []string{"Save server choices and continue", "Save Minecraft choices and continue", "Save storage choice and continue", "Save backup choices and continue"} {
		if strings.Contains(markup, oldLabel) {
			t.Fatalf("setup template still contains old action label %q", oldLabel)
		}
	}
	if strings.Count(markup, ">Continue</button>") != 6 {
		t.Fatalf("setup template Continue button count = %d, want 6", strings.Count(markup, ">Continue</button>"))
	}
}

func TestSetupWizardStorageNoLongerLeavesFirstRunForAdvancedStorage(t *testing.T) {
	content, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(content)
	if strings.Contains(markup, "/settings/storage-provision?from=setup") {
		t.Fatal("first-run Storage still leaves the wizard for legacy Advanced Storage")
	}
	for _, want := range []string{"Use another internal disk", "data-setup-storage-prepare=\"format\"", "data-setup-storage-prepare=\"create_partition\""} {
		if !strings.Contains(markup, want) {
			t.Fatalf("first-run Storage replacement missing %q", want)
		}
	}
}
func TestSetupWizardServerStepValidatesAndPersistsChoices(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	rr := saveServerStep(t, app, validServerValues())
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("server save returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := page.Body.String()
	for _, want := range []string{"<h2>Cross-play</h2>", "Cross-play", "Enable Bedrock cross-play", "Minecraft Java port", "Bedrock UDP port", "25565", "19132"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Resources step missing %q: %s", want, body)
		}
	}
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("connections save returned %d: %s", rr.Code, rr.Body.String())
	}
	page = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body = page.Body.String()
	for _, want := range []string{"<h2>Memory</h2>", "Memory", "Minecraft game memory", "Technical name: Java heap", "Maximum Minecraft memory", "container memory limit", "8.0 GiB detected", "2.0 GiB", "1.0 GiB", "Presets are starting points based on system memory and headroom. Larger servers or heavier plugins may need High memory or Custom settings.", "Recommended", "High memory", "/static/settings.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Resources step missing %q: %s", want, body)
		}
	}
	if rr := saveResourcesStep(t, app, validResourceValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("resources save returned %d: %s", rr.Code, rr.Body.String())
	}
	page = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body = page.Body.String()
	for _, want := range []string{"<h2>Version</h2>", "Container updates", "Stable (recommended)", "Latest", "Custom", "This does not choose the Minecraft game version below.", `id="image-tag"`, `value="stable"`, "Recommended", "Specific version", `id="specific-version-field"`, `id="setup-version-preview"`, "/static/settings.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Minecraft step missing %q: %s", want, body)
		}
	}
}

func TestSetupVersionPreviewPresentationContract(t *testing.T) {
	markup, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `class="setup-version-preview" id="setup-version-preview"`) {
		t.Fatal("version step is missing its structured preview container")
	}
	script, err := assets.ReadFile("static/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	for _, want := range []string{
		`"setup-version-primary"`, `"setup-version-candidate"`, `policy === "recommended" ? "Recommended"`,
		`"Recommended follows the newest stable compatible Minecraft version."`, `status.available !== status.selected_candidate`,
		`"setup-version-secondary"`, `"Newest available version"`,
		`{ STABLE: "Stable", BETA: "Beta", ALPHA: "Alpha", RELEASE: "Release" }`,
		`channelLabel(status.available_channel)`, `channelLabel(status.candidate_channel)`, `Server software build channel:`,
		`"setup-version-explanation"`, `"notice success setup-version-compatibility"`, `"notice warning setup-version-compatibility"`,
		`"Latest follows newer Minecraft server versions when available."`,
		`The selected version is not currently compatible with Bedrock cross-play.`, `Current supported Bedrock version: ${supported}`,
		`next.disabled = !status.selected_candidate || (status.crossplay_enabled && !status.crossplay_compatible);`,
		`if (policy === "pinned" && !version)`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("version preview contract missing %q", want)
		}
	}
	if strings.Count(source, `fetch("/setup/version-preview?"`) != 1 {
		t.Fatal("version preview must use the existing endpoint")
	}
}

func TestSetupWizardServerStepRejectsInvalidPlayerLimit(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	values := validServerValues()
	values.Set("max_players", "0")
	rr := saveServerStep(t, app, values)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid server step returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Maximum players must be a positive number") || !strings.Contains(rr.Body.String(), `value="0"`) {
		t.Fatalf("invalid server step did not explain and preserve value: %s", rr.Body.String())
	}
	draft, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok || draft.CurrentStep != 1 {
		t.Fatalf("invalid server form advanced draft: %#v", draft)
	}
}

func TestSetupWizardSpecificVersionRequiresExplicitValue(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d", rr.Code)
	}
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("connections save returned %d", rr.Code)
	}
	if rr := saveResourcesStep(t, app, validResourceValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("resources save returned %d", rr.Code)
	}

	values := validMinecraftValues()
	values.Set("version_policy", "pinned")
	values.Set("version", "")
	rr := saveMinecraftStep(t, app, values)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Specific Minecraft version is invalid") {
		t.Fatalf("missing specific version was not rejected: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSetupWizardMinecraftStepAdvancesOnlyAfterValidation(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d", rr.Code)
	}
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("connections save returned %d", rr.Code)
	}

	bad := validResourceValues()
	bad.Set("container_memory", "4G")
	rr := saveResourcesStep(t, app, bad)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "must be larger than Minecraft game memory") {
		t.Fatalf("invalid memory was not rejected: %d %s", rr.Code, rr.Body.String())
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != 3 {
		t.Fatalf("invalid Resources form advanced to step %d", draft.CurrentStep)
	}

	goodResources := validResourceValues()
	goodResources.Set("java_memory", "5G")
	goodResources.Set("container_memory", "7G")
	rr = saveResourcesStep(t, app, goodResources)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("valid Resources form returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	minecraftPage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if minecraftPage.Code != http.StatusOK || !strings.Contains(minecraftPage.Body.String(), "<h2>Version</h2>") || !strings.Contains(minecraftPage.Body.String(), "Container updates") {
		t.Fatalf("Resources step did not advance to Minecraft: %d %s", minecraftPage.Code, minecraftPage.Body.String())
	}

	rr = saveMinecraftStep(t, app, validMinecraftValues())
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("valid Minecraft form returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<h2>Storage</h2>") || !strings.Contains(page.Body.String(), "Use another internal disk") {
		t.Fatalf("Minecraft step did not advance to storage: %d %s", page.Code, page.Body.String())
	}
}

func TestSetupWizardMinecraftBackPreservesUnsavedValues(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	_ = saveServerStep(t, app, validServerValues())
	_ = saveConnectionsStep(t, app, validConnectionValues())
	resources := validResourceValues()
	resources.Set("java_memory", "5G")
	resources.Set("container_memory", "7G")
	_ = saveResourcesStep(t, app, resources)

	values := validMinecraftValues()
	values.Set("image_tag", "java21")
	values.Set("direction", "back")
	back := saveMinecraftStep(t, app, values)
	if back.Code != http.StatusSeeOther {
		t.Fatalf("Minecraft back returned %d: %s", back.Code, back.Body.String())
	}
	resourcesPage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	for _, want := range []string{"<h2>Memory</h2>", `value="5G"`, `value="7G"`} {
		if !strings.Contains(resourcesPage.Body.String(), want) {
			t.Fatalf("Resources draft lost %q: %s", want, resourcesPage.Body.String())
		}
	}
	_ = saveResourcesStep(t, app, resources)
	minecraftPage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	for _, want := range []string{`value="java21"`, `value="custom" selected`, "Custom container tag"} {
		if !strings.Contains(minecraftPage.Body.String(), want) {
			t.Fatalf("Minecraft draft lost %q: %s", want, minecraftPage.Body.String())
		}
	}
}

func TestSetupWizardGenericNavigationCannotSkipRealForms(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=next"))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("generic next returned %d", rr.Code)
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != 1 {
		t.Fatalf("generic navigation skipped required server form to step %d", draft.CurrentStep)
	}
}

func TestSetupWizardCancelDiscardsDraft(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	startSetup(t, app)
	cancel := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/cancel", "csrf=csrf-token"))
	if cancel.Code != http.StatusSeeOther || cancel.Header().Get("Location") != "/setup" {
		t.Fatalf("cancel returned %d %q", cancel.Code, cancel.Header().Get("Location"))
	}

	welcome := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if welcome.Code != http.StatusOK || !strings.Contains(welcome.Body.String(), "Set up your Minecraft server") {
		t.Fatalf("cancel did not discard draft: %d %s", welcome.Code, welcome.Body.String())
	}
	if strings.Contains(welcome.Body.String(), "<h2>Server</h2>") {
		t.Fatal("cancelled draft still rendered as active setup")
	}
}

func TestSetupWizardRejectsMutationWithoutCSRF(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example/setup/start", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("setup start without CSRF status = %d, want 403", rr.Code)
	}
	if _, ok := firstRunSetupDrafts.get(app, "session-token"); ok {
		t.Fatal("setup draft was created without CSRF")
	}
}

func TestSetupWizardIsAdministratorOnly(t *testing.T) {
	client := setupWizardClient()
	client.role = "operator"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator setup status = %d, want 403", rr.Code)
	}
	if client.configurationHit != 0 {
		t.Fatalf("privileged configuration discovery ran for operator %d times", client.configurationHit)
	}
}

func TestConfiguredApplianceCannotEnterFirstRunWizard(t *testing.T) {
	client := setupWizardClient()
	client.configuration.Configured = true
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("configured setup returned %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestDashboardIncludesNonBlockingFirstRunChoiceForUnconfiguredAdministrator(t *testing.T) {
	dashboard, err := assets.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	dashboardContent := string(dashboard)
	for _, want := range []string{`/static/first-run.js`, `data-first-run-choice`, `data-minecraft-setup-invitation`} {
		if !strings.Contains(dashboardContent, want) {
			t.Fatalf("dashboard first-run flow missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/first-run.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	if strings.Contains(content, `window.location.replace("/setup")`) {
		t.Fatal("first-run flow still forces an unconfigured Administrator into setup")
	}
	for _, want := range []string{`role !== "administrator"`, `configured === true`, `showChoice()`, `showInvitation()`} {
		if !strings.Contains(content, want) {
			t.Fatalf("first-run Workspace flow missing %q", want)
		}
	}
}

func TestSetupWizardVisitedStepNavigation(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Code)
	}
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Code)
	}
	for _, step := range []struct{ target, status, current int }{{1, http.StatusSeeOther, 1}, {3, http.StatusSeeOther, 3}, {4, http.StatusBadRequest, 3}} {
		body := fmt.Sprintf("csrf=csrf-token&direction=jump&step=%d", step.target)
		rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", body))
		if rr.Code != step.status {
			t.Fatalf("jump to %d returned %d", step.target, rr.Code)
		}
		draft, _ := firstRunSetupDrafts.get(app, "session-token")
		if draft.CurrentStep != step.current || draft.HighestStep != 3 {
			t.Fatalf("jump to %d changed visited steps: %#v", step.target, draft)
		}
	}
}

func TestRecommendedSetupServerSoftwareCapabilities(t *testing.T) {
	for _, serverType := range []string{"paper", "purpur", "vanilla"} {
		t.Run(serverType, func(t *testing.T) {
			client := setupWizardClient()
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			values := url.Values{"csrf": {"csrf-token"}, "server_type": {serverType}}
			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recommended", values.Encode()))
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			draft, ok := firstRunSetupDrafts.get(app, "session-token")
			if !ok || draft.Minecraft.ServerType != serverType || draft.Server.BedrockEnabled != (serverType != "vanilla") {
				t.Fatalf("wrong capabilities: %#v", draft)
			}
			request, err := setupPlanRequestFromDraft(draft)
			if err != nil {
				t.Fatal(err)
			}
			if request.Minecraft.ServerType != serverType {
				t.Fatal("server software lost from request")
			}
		})
	}
}

func TestVanillaAdvancedConnectionsCannotEnableBedrock(t *testing.T) {
	app, err := New(setupWizardClient(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	values := validServerValues()
	values.Set("server_type", "vanilla")
	if rr := saveServerStep(t, app, values); rr.Code != http.StatusSeeOther {
		t.Fatalf("server: %d %s", rr.Code, rr.Body.String())
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if !strings.Contains(page.Body.String(), `<p class="notice error">`+vanillaBedrockWarning+`</p>`) || !strings.Contains(page.Body.String(), "data-bedrock-toggle disabled") {
		t.Fatal("Vanilla Connections did not disable Bedrock")
	}
	connections := validConnectionValues()
	connections.Set("bedrock_enabled", "on")
	if rr := saveConnectionsStep(t, app, connections); rr.Code != http.StatusBadRequest {
		t.Fatalf("Bedrock accepted: %d", rr.Code)
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.Server.BedrockEnabled || draft.CurrentStep != 2 {
		t.Fatal("Rejected Bedrock request changed the saved draft")
	}
	connections.Del("bedrock_enabled")
	if rr := saveConnectionsStep(t, app, connections); rr.Code != http.StatusSeeOther {
		t.Fatalf("Java-only Connections: %d %s", rr.Code, rr.Body.String())
	}
}
