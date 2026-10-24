package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestMinecraftNetworkLegacyRedirect(t *testing.T) {
	app, f := newNetworkTestApp(t)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/minecraft/network", ""))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/?workspace=minecraft-network" {
		t.Fatal(rr.Code, rr.Header(), rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "<form") || f.writes != 0 {
		t.Fatal("legacy route rendered Network content or changed state")
	}
}

func TestMinecraftNetworkWorkspaceMutationsRefreshContent(t *testing.T) {
	app, f := newNetworkTestApp(t)
	f.network.RecoveryRequired = true
	f.network.Whitelist = []api.MinecraftNetworkPlayer{{Platform: "java", Name: "Alex"}}
	for _, body := range []string{
		"action=recover",
		"action=configure&enabled=on&java_port=25566&bedrock_port=19133&members=jv-family1&welcome_message=Hello",
		"action=add&platform=bedrock&name=Alex",
		"action=remove&platform=java&name=Alex",
	} {
		before, reads := f.writes, f.reads
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/minecraft-network", "csrf=csrf-token&"+body))
		if rr.Code != http.StatusOK || f.writes != before+1 || f.reads != reads+1 {
			t.Fatal(body, rr.Code, f.writes, rr.Body.String())
		}
		for _, want := range []string{"Minecraft Network was updated.", "Retry recovery", `value="remove"`, "Alex", "Network Whitelist"} {
			if !strings.Contains(rr.Body.String(), want) {
				t.Fatalf("authoritative response to %s missing %s", body, want)
			}
		}
		if strings.Contains(body, "action=configure") {
			for _, want := range []string{`value="25566"`, `value="19133"`, `value="Hello"`, `value="jv-family1" checked`} {
				if !strings.Contains(rr.Body.String(), want) {
					t.Fatalf("configured response missing %s", want)
				}
			}
		}
	}
	if got := strings.Join(f.whitelistArgs, ":"); got != "java:remove:Alex" {
		t.Fatal("whitelist operation was not forwarded", got)
	}
}

func TestMinecraftNetworkWorkspaceRequiresSession(t *testing.T) {
	app, f := newNetworkTestApp(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, httptest.NewRequest(method, "http://example/workspace/minecraft-network", nil))
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" || f.writes != 0 {
			t.Fatal("unauthenticated workspace request accepted", method, rr.Code)
		}
	}
}

func TestMinecraftNetworkWorkspaceArchitecture(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		data, err := assets.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	header := read("templates/header.html")
	servers := read("templates/minecraft_servers.html")
	script := read("static/app.js")
	fragment := read("templates/minecraft_network.html")
	minecraftSection := strings.Split(strings.Split(header, `<span class="control-section-label">Minecraft</span>`)[1], `</section>`)[0]
	previous := -1
	for _, label := range []string{"Minecraft Servers", "Minecraft Settings", "How to connect", "Version", "Plugins", "Network"} {
		position := strings.Index(minecraftSection, "<strong>"+label+"</strong>")
		if position <= previous {
			t.Fatalf("Minecraft launcher order: %s", label)
		}
		previous = position
	}
	for name, source := range map[string]string{"Control Center": minecraftSection, "Minecraft Servers": servers} {
		if strings.Count(source, "data-minecraft-network-open") != 1 || strings.Contains(source, `href="/minecraft/network"`) {
			t.Fatal("incorrect workspace launcher", name)
		}
	}
	for _, want := range []string{`data-workspace-window="minecraft-network"`, `system-workspace-shell`, `system-workspace-body`, `system-workspace-content`, `data-minecraft-network-refresh`, `data-minecraft-network-close`} {
		if !strings.Contains(header, want) {
			t.Fatal("missing Network workspace shell", want)
		}
	}
	for _, want := range []string{
		`setupWorkspaceWindow(minecraftNetworkDialog,`,
		`event.target.closest("[data-minecraft-network-open]")`,
		`destination.searchParams.set("workspace", "minecraft-network")`,
		`window.history.replaceState(window.history.state, "", destination)`,
		`"minecraft-network": "[data-minecraft-network-open]"`,
		`let workspace = destination.get("workspace")`,
		`document.body.classList.contains("role-administrator") && readWorkspaceWindowState("minecraft-network").open`,
		`fetch("/workspace/minecraft-network",`,
		`new URLSearchParams(new FormData(form))`,
		`body.set(event.submitter.name, event.submitter.value)`,
	} {
		if !strings.Contains(script, want) {
			t.Fatal("missing shared workspace transport/URL state", want)
		}
	}
	for _, forbidden := range []string{"minecraft-servers-page-start", "minecraft-servers-page-end", "<!doctype", "<html", "<head", "<body", "<script", "<style", "Back to Minecraft Servers", `action="/minecraft/network"`} {
		if strings.Contains(fragment, forbidden) {
			t.Fatal("standalone page remains in workspace fragment", forbidden)
		}
	}
	for _, want := range []string{`action="/workspace/minecraft-network"`, `name="csrf"`, `name="enabled"`, `name="java_port"`, `name="bedrock_port"`, `name="members"`, `name="welcome_message"`, `value="configure"`, `value="recover"`, `value="add"`, `value="remove"`, `value="java"`, `value="bedrock"`} {
		if !strings.Contains(fragment, want) {
			t.Fatal("Network function missing", want)
		}
	}
}

// Exercise the existing mode-switching code with zero, one, and multiple server cards.
func TestMinecraftNetworkActionBarArrangeVisibility(t *testing.T) {
	data, err := assets.ReadFile("static/minecraft-servers.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start := strings.Index(source, "  let arranging = false;")
	if start < 0 {
		t.Fatal("server arrange initialization missing")
	}
	end := strings.Index(source[start:], "  for (const card of rendered) {")
	if end < 0 {
		t.Fatal("server arrange initialization boundary missing")
	}
	end += start
	program := `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const source = ` + fmt.Sprintf("%q", source[start:end]) + `;
for (const count of [0, 1, 2, 3]) {
  const control = () => ({hidden: true, events: {}, addEventListener(name, fn) {this.events[name] = fn;}, focus() {}});
  const arrange = control(), done = control();
  const rendered = Array.from({length: count}, () => {
    const normal = {hidden: false, inert: false};
    const controls = {'[data-server-move-earlier]': {}, '[data-server-move-later]': {}, '[data-server-arrange-actions]': {hidden: true}, 'details': {open: true}};
    return {normal, controls, querySelector: key => controls[key], querySelectorAll: () => [normal]};
  });
  vm.runInNewContext(source, {arrange, done, rendered, cards: () => rendered, window: {localStorage: {setItem() {}}}});
  assert.equal(arrange.hidden, count === 0);
  assert.equal(done.hidden, true);
  if (count === 0) continue;
  arrange.events.click();
  assert.equal(arrange.hidden, true);
  assert.equal(done.hidden, false);
  for (const [index, card] of rendered.entries()) {
    assert.equal(card.normal.hidden, true);
    assert.equal(card.normal.inert, true);
    assert.equal(card.controls['[data-server-arrange-actions]'].hidden, false);
    assert.equal(card.controls['[data-server-move-earlier]'].disabled, index === 0);
    assert.equal(card.controls['[data-server-move-later]'].disabled, index === count - 1);
  }
  done.events.click();
  assert.equal(arrange.hidden, false);
  assert.equal(done.hidden, true);
  for (const card of rendered) {
    assert.equal(card.normal.hidden, false);
    assert.equal(card.normal.inert, false);
    assert.equal(card.controls['[data-server-arrange-actions]'].hidden, true);
  }
}
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("server arrange behavior: %v\n%s", err, output)
	}
}

// Preserve the CSS surfaces frozen by the Minecraft Network correction.
func TestMinecraftNetworkCorrectionPreservesCSS(t *testing.T) {
	baseline := map[string]string{
		"static/how-to-connect.css":    "5d99042ae7f87dc47bf09f248794725cca7af2a08a656bbd4d4fc2bc3926ce06",
		"static/minecraft-servers.css": "66b3fc388e6a40f24dad0bff2768b9039323381acc763a3d565896cc82f77c56",
		"static/network-workspace.css": "0b22bc4263f0a3769f59874767366039f8c76a33486cb506b8cd20f23fa94585",
		"static/new-backups.css":       "23e64d2ff226fdc6488e4a124b77f8f12264861ded37278031e9c02252a1dc49",
		"static/playit-setup.css":      "fdaae36a639c41bc22374d42a70803b771e4a07f35810464080923e7582d39b1",
		"static/setup-review.css":      "02d4fe58915d40862c728f3541932e9f60870ebaee6ebb70be452e441a782df3",
		"static/storage-browser.css":   "fbdfb24d4ce62fac8ef6ca6d1bf053258c2f2668b0b23217ef489c3388a003c6",
	}
	for path, want := range baseline {
		data, err := assets.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
			t.Errorf("stylesheet changed outside approved Network correction: %s", path)
		}
	}
}

func TestMinecraftNetworkLaunchersAndFragmentTransport(t *testing.T) {
	data, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start := strings.Index(source, "const minecraftNetworkDialog =")
	end := strings.Index(source, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("Minecraft Network workspace initialization missing")
	}
	program := `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const source = ` + fmt.Sprintf("%q", source[start:end]) + `;
const element = () => ({events: {}, addEventListener(name, fn) {this.events[name] = fn;}});
const content = element(), state = element(), refresh = element(), close = element();
let role = "administrator", savedOpen = false;
const dialog = {querySelector: key => ({
  "[data-minecraft-network-content]": content, "[data-minecraft-network-state]": state,
  "[data-minecraft-network-refresh]": refresh, "[data-minecraft-network-close]": close
})[key]};
const document = element();
document.querySelector = () => dialog;
document.body = {classList: {contains: value => value === "role-" + role}};
document.createRange = () => ({selectNodeContents() {}, createContextualFragment: html => html});
const rendered = [];
content.replaceChildren = html => rendered.push(html);
const controlCenter = {open: true};
const window = {location: {href: "http://example/?workspace=minecraft-servers&tab=overview"}, history: {
  state: {kept: true}, replaceState(state, unused, url) {assert.equal(state.kept, true); window.location.href = String(url);}
}};
let opened = 0, closed = 0;
const requests = [];
class Form {constructor(values) {this.values = values;}}
class FormData {constructor(form) {this.form = form;} [Symbol.iterator]() {return this.form.values[Symbol.iterator]();}}
vm.runInNewContext(source, {
  document, window, controlCenter, URL, URLSearchParams, FormData, HTMLFormElement: Form,
  readWorkspaceWindowState: () => ({open: savedOpen}),
  setupWorkspaceWindow: (target, options) => {
    assert.equal(target, dialog);
    return {open() {opened++; return options.onOpen();}, close() {closed++;}};
  },
  fetch: async (url, options) => {
    requests.push({url, options});
    return {ok: true, redirected: false, text: async () => "authoritative fragment " + requests.length};
  }
});
const settle = () => new Promise(resolve => setImmediate(resolve));
(async () => {
  assert.equal(opened, 0);
  // Both launchers (including the dynamically inserted action-bar button)
  // match the same delegated selector and therefore the same window control.
  for (const launcher of ["control-center", "servers-action-bar"]) {
    let prevented = false;
    document.events.click({target: {closest(selector) {
      assert.equal(selector, "[data-minecraft-network-open]"); return {launcher};
    }}, preventDefault() {prevented = true;}});
    await settle();
    assert.equal(prevented, true);
    assert.equal(controlCenter.open, false);
    const url = new URL(window.location.href);
    assert.equal(url.searchParams.get("workspace"), "minecraft-network");
    assert.equal(url.searchParams.has("tab"), false);
  }
  assert.equal(opened, 2);
  assert.equal(rendered.length, 2);
  for (const action of ["configure", "recover", "add", "remove"]) {
    const form = new Form([["csrf", "csrf-token"], ["members", "jv-family1"], ["members", "jv-kids001"]]);
    let prevented = false;
    content.events.submit({target: form, submitter: {name: "action", value: action}, preventDefault() {prevented = true;}});
    await settle();
    assert.equal(prevented, true);
    const request = requests.at(-1);
    assert.equal(request.url, "/workspace/minecraft-network");
    assert.equal(request.options.method, "POST");
    assert.equal(request.options.credentials, "same-origin");
    assert.equal(request.options.body.get("csrf"), "csrf-token");
    assert.equal(request.options.body.get("action"), action);
    assert.deepEqual(request.options.body.getAll("members"), ["jv-family1", "jv-kids001"]);
    assert.equal(rendered.at(-1), "authoritative fragment " + requests.length);
    assert.equal(content.inert, false);
  }
  refresh.events.click(); await settle();
  assert.equal(requests.at(-1).options.method, "GET");
  close.events.click(); assert.equal(closed, 1);
  role = "operator";
  document.events.click({target: {closest() {return {}; }}, preventDefault() {throw new Error("non-admin launcher accepted");}});
  assert.equal(opened, 2);
})().catch(error => {console.error(error); process.exitCode = 1;});
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Minecraft Network workspace behavior: %v\n%s", err, output)
	}
}
