package server

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func correctionSource(t *testing.T, name string) string {
	t.Helper()
	data, err := assets.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetupVersionPreviewMetadataAndCompatibility(t *testing.T) {
	source := correctionSource(t, "static/settings.js")
	start := strings.Index(source, "  const channelLabel =")
	if start < 0 {
		t.Fatal("version preview renderer missing")
	}
	end := strings.Index(source[start:], "  const refresh =")
	if end < 0 {
		t.Fatal("version preview renderer missing")
	}
	program := `
const assert = require("node:assert/strict");
const document = { createElement: () => ({ children: [], textContent: "", append(...nodes) { this.children.push(...nodes); } }) };
const preview = { replaceChildren(...nodes) { this.children = nodes; } };
const next = { disabled: true };
` + source[start:start+end] + `
const flatten = (node) => [node.textContent || "", ...(node.children || []).flatMap(flatten)];
const text = () => flatten(preview).join(" ");
const fixture = { selected_candidate: "1.21.8", available: "1.21.8", candidate_channel: "RELEASE",
  geyser_supported_version: "1.21.8", crossplay_enabled: true, crossplay_compatible: true };
for (const policy of ["recommended", "latest", "pinned"]) {
  show(fixture, policy);
  assert.equal(next.disabled, false);
  assert.equal(preview.children.length, 1);
  assert(text().includes("1.21.8"));
  assert(text().includes("Server software build channel: Release"));
  assert(!text().includes("pre-release"));
  assert(!text().includes("Newest available version"));
  assert.equal(preview.children.filter(n => n.className === "notice success setup-version-panel").length, 1);
  assert.equal(preview.children[0].children.filter(n => /compatibility/.test(n.className)).length, 1);
}
show({ ...fixture, candidate_channel: "BETA" }, "recommended");
assert(text().includes("Server software build channel: Beta"));
assert(!text().includes("Stable choice"));
show({ ...fixture, candidate_channel: "", crossplay_enabled: false }, "pinned");
assert(text().includes("Server software build channel: Unavailable"));
assert.equal(preview.children[0].children.filter(n => /compatibility/.test(n.className)).length, 0);
for (const policy of ["latest", "pinned"]) {
  show({ ...fixture, crossplay_compatible: false, geyser_supported_version: "1.21.7" }, policy);
  assert.equal(next.disabled, true);
  assert(text().includes("Current supported Bedrock version: 1.21.7"));
  assert(text().includes("Choose Recommended, choose a compatible Specific version, or disable Bedrock cross-play."));
  assert.equal(preview.children.filter(n => n.className === "notice warning setup-version-panel").length, 1);
  assert.equal(preview.children.filter(n => /success/.test(n.className)).length, 0);
}
show({ ...fixture, available: "1.21.9", available_channel: "ALPHA" }, "recommended");
assert(text().includes("Newest available version"));
assert(text().includes("1.21.9 · Server build: Alpha"));
assert(text().includes("Recommended follows the newest stable compatible Minecraft version."));
show({ ...fixture, selected_candidate: "", reason: "No build found" }, "pinned");
assert.equal(next.disabled, true);
assert(text().includes("No build found"));
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("version preview behavior: %v\n%s", err, output)
	}
}

func TestTopbarClockDisplayPreferences(t *testing.T) {
	source := correctionSource(t, "static/app.js")
	start := strings.Index(source, "const clockPreferenceKey =")
	if start < 0 {
		t.Fatal("clock display preferences missing")
	}
	end := strings.Index(source[start:], "\nif (controlCenter)")
	if end < 0 {
		t.Fatal("clock display preferences missing")
	}
	encoded, err := json.Marshal(source[start : start+end])
	if err != nil {
		t.Fatal(err)
	}
	program := `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const stored = new Map();
const instant = Date.parse("2026-10-09T17:05:00Z");
class FixedDate extends Date { constructor(...args) { super(...(args.length ? args : [instant])); } static now() { return instant; } }
function browser() {
  const handlers = new Map();
  const clock = { dataset: { systemTimezone: "UTC", systemNow: String(instant) },
    replaceChildren(...nodes) { this.children = nodes; }, setAttribute() {} };
  const document = { querySelector: () => clock, createElement: () => ({ textContent: "" }) };
  const window = { localStorage: { getItem: key => stored.get(key), setItem: (key, value) => stored.set(key, value) },
    setInterval() {}, addEventListener: (name, fn) => handlers.set(name, fn),
    dispatchEvent: event => handlers.get(event.type)?.(event) };
  const context = vm.createContext({ document, window, Intl, Date: FixedDate, Event: class { constructor(type) { this.type = type; } } });
  return { context, window, clock };
}
` + "const script = " + string(encoded) + ";\n" + `
let page = browser();
vm.runInContext(script, page.context);
assert.equal(page.clock.children[0].textContent, "05:05 PM");
assert.equal(page.clock.children[2].textContent, "10/09/2026");
page.window.JustVoxelClockDisplay.save({ time: "24", date: "dmy" });
assert.equal(page.clock.children[0].textContent, "17:05");
assert.equal(page.clock.children[2].textContent, "09/10/2026");
assert.equal(page.clock.dataset.systemTimezone, "UTC");
assert.equal(page.clock.dataset.systemNow, String(instant));
page = browser();
vm.runInContext(script, page.context);
assert.equal(page.clock.children[0].textContent, "17:05");
assert.equal(page.clock.children[2].textContent, "09/10/2026");
page.window.JustVoxelClockDisplay.save({ time: "invalid", date: "invalid" });
assert.equal(page.clock.children[2].textContent, "09/10/2026");
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clock display behavior: %v\n%s", err, output)
	}
}

func TestSystemCorrectionsPreserveMenusAndMetadata(t *testing.T) {
	app := correctionSource(t, "static/app.js")
	for _, want := range []string{
		`trigger.textContent = "Manage"`, `trigger.setAttribute("aria-label", "Manage " + user.username)`,
		`"restart-allowance/reset"`, `"backup-allowance/reset"`, `"role", "Save role"`,
		`"enabled", user.enabled`, `"password", "Change password"`, `"delete", "Delete account", true`,
		`choices.showPopover()`, `button.disabled = disabled`, `minimum_password_len`,
		`root.append(securityHeading, switcher)`, `authHeading.append(authLabel, authTitle, authDescription)`,
		`passwordHeading.append(passwordLabel, passwordTitle, passwordDescription)`,
		`String(payload.variant || "")`, `variantName ? ` + "`JustVoxel ${variantName}`",
		`payload.justvoxel`, `payload.webui`, `payload.management_api`, `payload.commit`, `payload.username`,
	} {
		if !strings.Contains(app, want) {
			t.Fatalf("System correction contract missing %q", want)
		}
	}
	aboutStart := strings.Index(app, "  const renderAbout =")
	aboutEnd := strings.Index(app[aboutStart:], "  const systemUPSStateLabel =")
	about := app[aboutStart : aboutStart+aboutEnd]
	if strings.Contains(about, `badge.className`) || strings.Contains(about, `systemPanel("JustVoxel", "About")`) {
		t.Fatal("About retains redundant title or variant badge")
	}
	header := correctionSource(t, "templates/header.html")
	for _, want := range []string{`data-topbar-time-format`, `data-topbar-date-format`, `12-hour time`, `24-hour time`, `MM/DD/YYYY`, `DD/MM/YYYY`} {
		if !strings.Contains(header, want) {
			t.Fatalf("clock preference control missing %q", want)
		}
	}
	css := correctionSource(t, "static/app.css")
	for _, want := range []string{`.system-user-menu.storage-action-menu summary{width:auto`, `.system-workspace-content .system-security-pane{width:100%`, `.wallpaper-panel .action-row button{width:auto;min-height:36px`} {
		if !strings.Contains(css, want) {
			t.Fatalf("System panel/action styling missing %q", want)
		}
	}
}

func TestWizardStoragePreparationsStayInline(t *testing.T) {
	markup := correctionSource(t, "templates/setup_wizard.html")
	if strings.Contains(markup, "data-setup-storage-manage") || strings.Contains(markup, "setup-storage-manager") {
		t.Fatal("wizard must not open a Storage workspace")
	}
	if strings.Count(markup, "data-setup-partition-actions") != 2 || strings.Count(markup, "data-setup-disk-review") != 1 {
		t.Fatal("both steps need inline partition actions and one focused dialog")
	}
}

func TestWizardInlineStorageBehaviorAndStateRefresh(t *testing.T) {
	cmd := exec.Command("node", "../../../tests/test-setup-inline-storage.js")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("inline wizard disk operations: %v\n%s", err, output)
	}
}

func TestAboutUsesInstalledVariantAndSuppliedVersions(t *testing.T) {
	source := correctionSource(t, "static/app.js")
	start := strings.Index(source, "  const renderAbout =")
	if start < 0 {
		t.Fatal("About renderer missing")
	}
	end := strings.Index(source[start:], "  const systemUPSStateLabel =")
	if end < 0 {
		t.Fatal("About renderer boundary missing")
	}
	program := `
const assert = require("node:assert/strict");
const node = () => ({ textContent: "", children: [], append(...nodes) { this.children.push(...nodes); }, appendChild(child) { this.children.push(child); } });
const document = { createElement: node };
const content = { replaceChildren(...nodes) { this.children = nodes; } };
let loadSequence = 1, payload, heading;
const systemFetchJSON = async () => payload;
const systemPanel = (label, title) => { heading = title; return { panel: node(), heading: node() }; };
const flatten = n => [n.textContent || "", ...(n.children || []).flatMap(flatten)].join(" ");
` + source[start:start+end] + `
(async () => {
  for (const [variant, expected] of [["justvoxel-vm", "JustVoxel VM"], ["justvoxel-hws", "JustVoxel HWS"], ["vm", "JustVoxel VM"], ["hws", "JustVoxel HWS"], ["unknown", "JustVoxel"]]) {
    payload = { variant, justvoxel: "image-from-metadata", webui: "webui-from-metadata", management_api: "api-from-metadata", commit: "actual-source-revision", username: "signed-in-user", role: "administrator" };
    await renderAbout(1);
    assert.equal(heading, expected);
    const text = flatten(content);
    for (const value of [payload.justvoxel, payload.webui, payload.management_api, payload.commit, payload.username]) assert(text.includes(value));
    assert(!text.includes("JustVoxel About"));
    const links = [];
    function visit(n) { if (n.href) links.push(n.href); (n.children || []).forEach(visit); }
    visit(content);
    assert.equal(links.length, 3);
    assert(links.includes("https://github.com/home-server-project/justvoxel/issues"));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("About metadata presentation: %v\n%s", err, output)
	}
}
