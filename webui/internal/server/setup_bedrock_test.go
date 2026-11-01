package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func (f *addServerTestAPI) AdminSetupNativeBedrockPreview(_ context.Context, _ string, players int) (api.AdminVersionStatus, error) {
	return api.AdminVersionStatus{ServerType: "bedrock", ServerSupported: true, SelectedCandidate: "1.26.50.1", CandidateChannel: "RELEASE", ImageTag: "stable", BedrockTCPPort: 19132, BedrockUDPStart: 19140, BedrockUDPEnd: 19140 + players - 1}, nil
}
func TestSimpleNativeBedrockStatesAndReview(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	input := regexp.MustCompile(`<input[^>]+name="family"[^>]+value="original"[^>]*>`).FindString(body)
	if input == "" || strings.Contains(input, "disabled") || strings.Contains(body, "Not implemented") {
		t.Fatal("Bedrock family unavailable")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"family": {"original"}}))
	body = addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	if strings.Count(body, `name="server_type"`) != 2 || !strings.Contains(body, "Native Bedrock server · no plugins or mods") {
		t.Fatal("native software card missing")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"server_type": {"bedrock"}}))
	body = addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	for _, field := range []string{"timezone", "java_port", "bedrock_tcp_port", "bedrock_udp_start", "image_tag", "version", "java_memory"} {
		if strings.Contains(body, `name="`+field+`"`) {
			t.Fatal("Simple exposed internal setting", field)
		}
	}
	if strings.Contains(body, `value="spectator"`) {
		t.Fatal("Bedrock offered Spectator")
	}
	for _, mode := range []string{"survival", "creative", "adventure"} {
		if !strings.Contains(body, `value="`+mode+`"`) {
			t.Fatal("Bedrock game mode missing", mode)
		}
	}
	if rr := addWizardRequest(app, http.MethodPost, "/simple", url.Values{"display_name": {"Bedrock family"}, "game_mode": {"spectator"}}); rr.Code != 400 {
		t.Fatal("Bedrock Spectator accepted")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"display_name": {"Bedrock family"}, "game_mode": {"survival"}}))
	draft, _ := setupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupReviewStep || draft.Minecraft.Version != "1.26.50.1" || draft.Minecraft.VersionPolicy != "pinned" || draft.Server.BedrockEnabled {
		t.Fatal("native runtime resolution lost", draft.Minecraft)
	}
	request, err := setupPlanRequestFromDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(request)
	if strings.Contains(string(raw), `"timezone"`) {
		t.Fatal("independent timezone in plan")
	}
	rr := addWizardRequest(app, http.MethodGet, "/review", nil)
	body = rr.Body.String()
	if rr.Code != 200 {
		t.Fatal(rr.Code, body)
	}
	for _, want := range []string{"Bedrock", "1.26.50.1", "Native Bedrock connection", "TCP signaling port", "UDP gameplay range", "Start on Boot", "Start after setup"} {
		if !strings.Contains(body, want) {
			t.Fatal("native Review missing", want)
		}
	}
	for _, forbidden := range []string{"<dt>Timezone</dt>", "<dt>Java</dt>", "Minecraft game memory", "Server update channel", "Geyser", "Floodgate"} {
		if strings.Contains(body, forbidden) {
			t.Fatal("native Review exposes Java/timezone", forbidden)
		}
	}
}
func TestAdvancedNativeBedrockSixStepsAndValidation(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	server := validServerValues()
	server.Set("server_type", "bedrock")
	server.Set("display_name", "Bedrock world")
	server.Set("game_mode", "spectator")
	if rr := addWizardRequest(app, http.MethodPost, "/server", server); rr.Code != 400 {
		t.Fatal("Advanced Bedrock Spectator accepted", rr.Code)
	}
	server.Set("game_mode", "adventure")
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/server", server))
	body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	for _, want := range []string{`class="setup-version-columns"`, `class="setup-version-column" aria-label="Connection"`, `class="setup-version-preview" id="setup-version-preview"`, "TCP signaling port", "UDP gameplay range", "1.26.50.1", `notice success setup-version-panel`, `setup-version-kicker">Recommended`, `setup-version-candidate`, "Minecraft build classification: Release"} {
		if !strings.Contains(body, want) {
			t.Fatal("native Step 2 missing", want)
		}
	}
	for _, forbidden := range []string{`aria-label="Native Bedrock connection"`, `<h3>Native Bedrock connection</h3>`, `name="loader_version"`, `<dl>`, `name="timezone"`, `name="java_port"`, `name="bedrock_enabled"`, `name="image_tag"`, `java21`, `Geyser`, `Floodgate`} {
		if strings.Contains(body, forbidden) {
			t.Fatal("Java field in native Step 2", forbidden)
		}
	}
	if strings.Count(body, `class="setup-version-columns"`) != 1 || strings.Count(body, `id="setup-version-preview"`) != 1 || strings.Count(body, `class="setup-version-column"`) != 2 {
		t.Fatal("native Step 2 must use one shared version layout and preview")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/connections", url.Values{"direction": {"next"}}))
	resolved, _ := setupDrafts.get(app, "session-token")
	if resolved.Minecraft.VersionPolicy != "pinned" || resolved.Minecraft.Version != "1.26.50.1" || resolved.Minecraft.ResolvedVersion != "1.26.50.1" {
		t.Fatal("native Advanced setup lost its authoritative exact release")
	}
	body = addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	if strings.Contains(body, `name="java_memory"`) || strings.Contains(body, "Java heap") || !strings.Contains(body, `name="container_memory"`) {
		t.Fatal("Bedrock memory uses Java heap")
	}
	for _, want := range []string{`class="setup-memory"`, `data-memory-mode="container"`, `data-memory-settings`, `data-other-memory-mib=`, "System memory", "Recommended headroom", "Minimum headroom", "Linux, cache, backups, updates and temporary spikes", `data-memory-preset="light">Light`, `data-memory-preset="recommended">Recommended`, `data-memory-preset="high">High memory`, `data-memory-preset="custom">Custom`, `id="container-memory"`, `id="memory-status"`} {
		if !strings.Contains(body, want) {
			t.Fatal("native memory component missing", want)
		}
	}
	if strings.Count(body, `name="container_memory"`) != 1 || strings.Contains(body, `id="java-memory"`) {
		t.Fatal("native memory must have exactly one editable memory value")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/resources", url.Values{"container_memory": {"2G"}, "direction": {"next"}}))
	draft, _ := setupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupStorageStep || draft.Minecraft.JavaMemory != "" || len(setupWizardSteps) != 6 {
		t.Fatal("native Advanced step progression changed")
	}
}

// Exercise the shared controller with no Java input and compare its maximum
// presets and reserve/status behavior against Java mode over the same totals.
func TestNativeBedrockContainerMemoryController(t *testing.T) {
	script, err := assets.ReadFile("static/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	controller := strings.SplitN(string(script), "const versionPolicy", 2)[0]
	program := `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const source = ` + fmt.Sprintf("%q", controller) + `;
function open(containerOnly, total, other = 0, maximum = "6G") {
  const input = value => ({value, events: {}, focused: false, focus() { this.focused = true; }, addEventListener(name, fn) { this.events[name] = fn; }});
  const game = containerOnly ? null : input("4G"), max = input(maximum);
  const classes = new Set();
  const status = {textContent: "", classList: {remove(...names) { names.forEach(n => classes.delete(n)); }, add(n) { classes.add(n); }}};
  const buttons = ["light", "recommended", "high", "custom"].map(name => ({
    dataset: {memoryPreset: name}, selected: false, events: {},
    addEventListener(event, fn) { this.events[event] = fn; },
    classList: {toggle(_, selected) { buttons.find(b => b.dataset.memoryPreset === name).selected = selected; }}
  }));
  const panel = {dataset: {memoryMode: containerOnly ? "container" : "", systemMemoryMib: String(total), minReserveMib: "1024", recommendedReserveMib: "1536", otherMemoryMib: String(other)}, querySelectorAll() { return buttons; }};
  const fields = {"java-memory": game, "container-memory": max, "memory-status": status};
  const document = {querySelector(selector) { return selector === "[data-memory-settings]" ? panel : null; }, getElementById(id) { return fields[id] || null; }};
  vm.runInNewContext(source, {document});
  return {game, max, status, classes, click(name) { buttons.find(b => b.dataset.memoryPreset === name).events.click(); }, selected() { return buttons.find(b => b.selected)?.dataset.memoryPreset; }};
}
for (const total of [1024, 2048, 2560, 3840, 7884, 8192, 16384, 32768]) {
  for (const preset of ["light", "recommended", "high"]) {
    const java = open(false, total), native = open(true, total);
    native.click(preset); java.click(preset);
    assert.equal(native.game, null);
    assert.equal(native.max.value, java.max.value, total + " " + preset);
    assert.equal(native.status.textContent, java.status.textContent);
    assert.deepEqual([...native.classes], [...java.classes]);
  }
}
const native = open(true, 8192);
assert.equal(native.selected(), "recommended");
native.click("light"); assert.equal(native.max.value, "3G");
native.click("high"); assert.equal(native.max.value, "7G");
native.click("recommended"); assert.equal(native.max.value, "6G");
native.click("custom"); assert.equal(native.max.focused, true);
assert.equal(native.selected(), "custom"); assert.equal(native.max.value, "6G");
native.max.value = "5G"; native.max.events.input(); assert.equal(native.selected(), "custom");
native.max.value = "6144M"; native.max.events.change(); assert.equal(native.selected(), "recommended");
for (const [total, other, max, severity, text] of [
  [8192, 0, "8G", "danger", "Not enough memory"],
  [8192, 0, "7G", "warning", "Tight memory configuration"],
  [8192, 4096, "6G", "warning", "Other configured servers"],
  [8192, 0, "6G", "", "Memory remaining outside Minecraft"],
  [8192, 0, "invalid", "warning", "Enter memory"],
  [0, 0, "2G", "warning", "could not be detected"]
]) {
  const native = open(true, total, other, max), java = open(false, total, other, max);
  assert.equal(native.status.textContent, java.status.textContent);
  assert(native.status.textContent.includes(text));
  assert.equal(severity ? native.classes.has(severity) : native.classes.size === 0, true);
}
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native Bedrock memory controller: %v\n%s", err, output)
	}
}
