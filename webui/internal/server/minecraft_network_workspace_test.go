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
		"static/minecraft-servers.css": "b39b7f286d3a8819a19fef8ef4e2f53db890eb06cec424f7765e19d6cea87bab",
		"static/network-workspace.css": "0b22bc4263f0a3769f59874767366039f8c76a33486cb506b8cd20f23fa94585",
		"static/new-backups.css":       "e414942748a1d69286bec340e0e804be95394d864139787560f3cc0cf1a761da",
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
	program := "const source = " + fmt.Sprintf("%q", source[start:end]) + ";\n" + `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const element = () => ({events: {}, addEventListener(name, fn) {this.events[name] = fn;}});
const content = element(), state = element(), refresh = element(), close = element();
const classes = new Set();
state.classList = {toggle(name, on) {on ? classes.add(name) : classes.delete(name);}, remove(...names) {names.forEach(name => classes.delete(name));}};
let role = "administrator", savedOpen = false, form, success = false, failOperation = false;
const toggle = {setAttribute(name, value) {this[name] = value;}}, badge = {}, heading = {};
let authoritative = {enabled: false, java_port: "25565", bedrock_port: "19132", members: ["jv-family1"], welcome_message: "Welcome", whitelist_enforced: "true"};
class Form {
  constructor(config) {
    this.dataset = {recoveryRequired: "false"};
    this.apply = {disabled: true}; this.cancel = {disabled: true};
    this.elements = [
      {name: "csrf", value: "csrf-token"},
      {name: "enabled", value: "on", type: "checkbox", checked: config.enabled},
      ...["java_port", "bedrock_port", "welcome_message", "whitelist_enforced"].map(name => ({name, value: config[name]})),
      ...["jv-family1", "jv-kids001"].map(value => ({name: "members", value, type: "checkbox", checked: config.members.includes(value)})),
    ];
    this.elements.namedItem = name => this.elements.find(field => field.name === name);
  }
  querySelector(selector) {
    if (selector === "[data-minecraft-network-cancel]") return this.cancel;
    if (selector === '[value="configure"]') return this.apply;
    throw new Error("unexpected form selector " + selector);
  }
}
class FormData extends URLSearchParams {
  constructor(form) {super(form.elements.filter(field => field.type !== "checkbox" || field.checked).map(field => [field.name, field.value]));}
}
content.querySelector = selector => ({
  "#minecraft-network-configure": form,
  "[data-network-whitelist-switch]": toggle,
  "[data-network-whitelist-badge]": badge,
  "[data-network-whitelist-heading]": heading,
  ".notice.success": success ? {} : null,
})[selector];
content.querySelectorAll = selector => {
  assert.equal(selector, ".notice.success");
  return success ? [{remove() {success = false;}}] : [];
};
const dialog = element();
dialog.querySelector = key => ({
  "[data-minecraft-network-content]": content, "[data-minecraft-network-state]": state,
  "[data-minecraft-network-refresh]": refresh, "[data-minecraft-network-close]": close
})[key];
let serverRefreshes = 0;
const servers = {open: true, querySelector(key) {
  assert.equal(key, "[data-minecraft-servers-refresh]");
  return {click() {serverRefreshes++;}};
}};
const document = element();
document.querySelector = selector => selector === "[data-minecraft-network-workspace-dialog]" ? dialog : servers;
document.body = {classList: {contains: value => value === "role-" + role}};
document.createRange = () => ({selectNodeContents() {}, createContextualFragment: html => html});
const rendered = [];
content.replaceChildren = html => {rendered.push(html); form = new Form(authoritative); success = html === "success";};
const controlCenter = {open: true};
const window = {location: {href: "http://example/?workspace=minecraft-servers&tab=overview"}, history: {
  state: {kept: true}, replaceState(state, unused, url) {assert.equal(state.kept, true); window.location.href = String(url);}
}};
let opened = 0, closed = 0;
const requests = [];
vm.runInNewContext(source, {
  document, window, controlCenter, URL, URLSearchParams, FormData, HTMLFormElement: Form,
  readWorkspaceWindowState: () => ({open: savedOpen}),
  setupWorkspaceWindow: (target, options) => {
    assert.equal(target, dialog);
    dialog.addEventListener("close", options.onClose);
    return {open() {opened++; return options.onOpen();}, close() {closed++; dialog.events.close();}};
  },
  fetch: async (url, options) => {
    assert.equal(url, "/workspace/minecraft-network");
    requests.push({url, options});
    if (options.method === "POST") {
      assert.equal(success, false, "old success removed before POST");
      assert.equal(state.textContent, "Updating Minecraft Network…");
      assert.equal(classes.has("notice") && classes.has("warning"), true);
      assert.equal(content.inert, true);
      assert.equal(options.credentials, "same-origin");
      assert.equal(options.body.get("csrf"), "csrf-token");
      if (options.body.get("action") === "configure" && !failOperation) {
        authoritative = {enabled: options.body.get("enabled") === "on", java_port: options.body.get("java_port"), bedrock_port: options.body.get("bedrock_port"), members: options.body.getAll("members"), welcome_message: options.body.get("welcome_message"), whitelist_enforced: options.body.get("whitelist_enforced")};
      }
    }
    return {ok: true, redirected: false, text: async () => options.method === "POST" && !failOperation ? "success" : "authoritative fragment"};
  }
});
const settle = () => new Promise(resolve => setImmediate(resolve));
const field = name => form.elements.namedItem(name);
const clean = () => {assert.equal(form.cancel.disabled, true); assert.equal(form.apply.disabled, true);};
const dirty = () => {assert.equal(form.cancel.disabled, false); assert.equal(form.apply.disabled, false);};
const click = selector => content.events.click({target: {closest: key => key === selector ? {} : null}});
const submit = (action, target = form) => {
  let prevented = false;
  content.events.submit({target, submitter: action ? {name: "action", value: action} : null, preventDefault() {prevented = true;}});
  assert.equal(prevented, true);
};
const launch = () => {
  let prevented = false;
  document.events.click({target: {closest(selector) {
    assert.equal(selector, "[data-minecraft-network-open]"); return {};
  }}, preventDefault() {prevented = true;}});
  assert.equal(prevented, true);
};
(async () => {
  assert.equal(opened, 0);
  // Both static and dynamic launchers use this delegated handler.
  for (const launcher of ["control-center", "servers-action-bar"]) {
    launch(); await settle(); clean();
    assert.equal(controlCenter.open, false);
    const url = new URL(window.location.href);
    assert.equal(url.searchParams.get("workspace"), "minecraft-network");
    assert.equal(url.searchParams.has("tab"), false);
  }
  assert.equal(opened, 2);
  assert.equal(rendered.length, 2);
  const posts = () => requests.filter(request => request.options.method === "POST").length;
  const noPosts = posts();
  submit("configure"); await settle(); assert.equal(posts(), noPosts); clean();
  submit(null); await settle(); assert.equal(posts(), noPosts);
  for (const name of ["enabled", "java_port", "bedrock_port", "members", "welcome_message", "whitelist_enforced"]) {
    const input = name === "members" ? form.elements.filter(field => field.name === "members")[1] : field(name);
    const before = {value: input.value, checked: input.checked};
    if (input.type === "checkbox") input.checked = !input.checked;
    else input.value = name === "whitelist_enforced" ? "false" : name.endsWith("port") ? "25570" : "New welcome";
    content.events.input(); dirty();
    Object.assign(input, before); content.events.change(); clean();
  }
  field("java_port").value = "025565"; content.events.input(); clean();
  click("[data-minecraft-network-cancel]"); clean();
  // Input in the separate Add player form does not define configuration dirtiness.
  content.events.input({target: new Form(authoritative)}); clean();
  // Recovery continues to block configure while allowing draft cancellation.
  form.dataset.recoveryRequired = "true";
  field("welcome_message").value = "Blocked by recovery"; content.events.input();
  assert.equal(form.apply.disabled, true); assert.equal(form.cancel.disabled, false);
  submit("configure"); await settle(); assert.equal(posts(), noPosts);
  click("[data-minecraft-network-cancel]"); form.dataset.recoveryRequired = "false"; clean();
  // Toggle is draft-only and updates the heading, badge, and hidden form value.
  click("[data-network-whitelist-switch]"); dirty();
  assert.equal(field("whitelist_enforced").value, "false");
  assert.equal(toggle["aria-checked"], "false"); assert.equal(badge.textContent, "Disabled");
  assert.equal(heading.textContent, "Network Whitelist");
  assert.equal(posts(), noPosts);
  click("[data-network-whitelist-switch]"); clean();
  assert.equal(badge.textContent, "Enabled"); assert.equal(heading.textContent, "Network Whitelist: Enforced");
  // Cancel restores every field, including external hidden values and checkboxes.
  for (const input of form.elements) {
    if (input.name === "csrf") continue;
    if (input.type === "checkbox") input.checked = !input.checked;
    else input.value = input.name === "whitelist_enforced" ? "false" : "changed";
  }
  content.events.input(); dirty(); click("[data-minecraft-network-cancel]"); clean();
  assert.equal(field("enabled").checked, authoritative.enabled);
  for (const name of ["java_port", "bedrock_port", "welcome_message", "whitelist_enforced"]) assert.equal(field(name).value, authoritative[name]);
  assert.deepEqual(new FormData(form).getAll("members"), authoritative.members);
  assert.equal(posts(), noPosts);
  click("[data-network-whitelist-switch]");
  content.events.reset({target: form, preventDefault() {}}); clean();
  assert.equal(toggle["aria-checked"], "true");
  // Both explicit and native close/cancel discard drafts without network writes.
  for (const closeWindow of [() => close.events.click(), () => dialog.events.close(), () => dialog.events.cancel()]) {
    field("welcome_message").value = "Draft"; click("[data-network-whitelist-switch]"); dirty();
    closeWindow(); clean();
    assert.equal(field("welcome_message").value, "Welcome");
    assert.equal(toggle["aria-checked"], "true"); assert.equal(posts(), noPosts);
  }
  assert.equal(closed, 1);
  launch(); await settle(); clean();
  // Successful configure refreshes cards once and establishes the returned baseline.
  field("welcome_message").value = "Saved welcome";
  form.elements.filter(field => field.name === "members")[1].checked = true;
  click("[data-network-whitelist-switch]"); dirty();
  submit("configure"); await settle(); clean();
  assert.equal(serverRefreshes, 1); assert.equal(posts(), noPosts + 1);
  assert.equal(requests.at(-1).options.method, "POST");
  assert.equal(requests.at(-1).options.body.get("action"), "configure");
  assert.deepEqual(requests.at(-1).options.body.getAll("members"), ["jv-family1", "jv-kids001"]);
  assert.equal(rendered.at(-1), "success");
  assert.equal(field("welcome_message").value, "Saved welcome");
  assert.equal(field("whitelist_enforced").value, "false");
  assert.equal(classes.has("warning"), false); assert.equal(content.inert, false);
  submit("configure"); await settle(); assert.equal(posts(), noPosts + 1);
  // Separate whitelist operations replace drafts with authoritative configuration.
  for (const action of ["recover", "add", "remove"]) {
    field("welcome_message").value = "Unsaved"; content.events.input(); dirty();
    const mutation = new Form(authoritative);
    const previousRenders = rendered.length;
    submit(action, mutation); await settle(); clean();
    assert.equal(rendered.length, previousRenders + 1);
    assert.equal(rendered.at(-1), "success");
    assert.equal(content.inert, false);
    assert.deepEqual(requests.at(-1).options.body.getAll("members"), ["jv-family1", "jv-kids001"]);
    assert.equal(serverRefreshes, 2, "only recovery refreshes server cards");
    assert.equal(requests.at(-1).options.body.get("action"), action);
    assert.equal(field("welcome_message").value, "Saved welcome");
  }
  // HTTP-200 operation errors do not count as successful configuration.
  failOperation = true;
  field("welcome_message").value = "Rejected"; content.events.change();
  submit("configure"); await settle(); clean(); assert.equal(serverRefreshes, 2);
  failOperation = false;
  servers.open = false;
  submit("recover", new Form(authoritative)); await settle(); assert.equal(serverRefreshes, 2);
  field("welcome_message").value = "Saved while servers closed"; content.events.input();
  submit("configure"); await settle(); clean(); assert.equal(serverRefreshes, 2);
  const beforeRefresh = posts(); refresh.events.click(); await settle(); clean();
  assert.equal(requests.at(-1).options.method, "GET"); assert.equal(posts(), beforeRefresh);
  assert.equal(serverRefreshes, 2);
  const wasOpened = opened;
  role = "operator";
  document.events.click({target: {closest() {return {}; }}, preventDefault() {throw new Error("non-admin launcher accepted");}});
  assert.equal(opened, wasOpened);
})().catch(error => {console.error(error); process.exitCode = 1;});
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Minecraft Network workspace behavior: %v\n%s", err, output)
	}
}

func TestMinecraftNetworkWhitelistConfigureAndFragment(t *testing.T) {
	for _, enforced := range []bool{true, false} {
		t.Run(fmt.Sprint(enforced), func(t *testing.T) {
			app, f := newNetworkTestApp(t)
			body := fmt.Sprintf("csrf=csrf-token&action=configure&java_port=25565&bedrock_port=19132&whitelist_enforced=%t", enforced)
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/minecraft-network", body))
			if rr.Code != http.StatusOK || f.writes != 1 || f.network.Config.WhitelistEnforced != enforced {
				t.Fatal("enforcement was not forwarded", rr.Code, rr.Body.String())
			}
			heading, badge := "Network Whitelist</h2>", "Disabled"
			if enforced {
				heading, badge = "Network Whitelist: Enforced</h2>", "Enabled"
			}
			for _, want := range []string{
				heading, `class="badge" data-network-whitelist-badge>` + badge + `</span>`,
				`class="minecraft-whitelist-switch" role="switch"`, fmt.Sprintf(`aria-checked="%t"`, enforced),
				fmt.Sprintf(`name="whitelist_enforced" form="minecraft-network-configure" value="%t"`, enforced),
				`Lobby: Java + Bedrock server selector`, `data-minecraft-network-cancel disabled>Cancel</button>`,
				`name="action" value="configure" disabled>Apply</button>`,
			} {
				if !strings.Contains(rr.Body.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, forbidden := range []string{"Lobby: JustVoxel virtual lobby", "Network whitelist: Enforced", "<h2>Lobby</h2>"} {
				if strings.Contains(rr.Body.String(), forbidden) {
					t.Errorf("old summary remains: %s", forbidden)
				}
			}
		})
	}
}
