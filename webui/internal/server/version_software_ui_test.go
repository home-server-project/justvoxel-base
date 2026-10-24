package server

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestVersionSoftwareCardsAndReviewedSwitch(t *testing.T) {
	script, err := assets.ReadFile("static/version-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	program := `
const assert = require("node:assert/strict");
const vm = require("node:vm");
const source = ` + fmt.Sprintf("%q", strings.Replace(string(script), `let currentTab = "container"`, `let currentTab = "software"`, 1)) + `;
class Element {
  constructor(tag) { this.dataset = {}; this.tagName = tag; this.children = []; this.events = {}; this.attributes = {}; this.disabled = false; this.checked = false; this.value = ""; this.ownText = ""; }
  set textContent(value) { this.ownText = String(value); this.children = []; }
  get textContent() { return this.ownText + this.children.map(child => child.textContent).join(" "); }
  append(...children) { for (const child of children) { child.parent = this; this.children.push(child); } }
  appendChild(child) { this.append(child); return child; }
  insertBefore(child, reference) { const index = this.children.indexOf(reference); assert.ok(index >= 0); child.parent = this; this.children.splice(index, 0, child); }
  replaceChildren(...children) { this.children = []; this.append(...children); }
  remove() { this.parent.children = this.parent.children.filter(child => child !== this); }
  setAttribute(name, value) { this.attributes[name] = value; }
  addEventListener(name, fn) { this.events[name] = fn; }
  all() { return this.children.flatMap(child => [child, ...child.all()]); }
  querySelector(selector) {
    if (selector === "[data-version-custom]") return this.custom;
    if (selector === ".version-channel-row") return this.channelRow;
    if (selector === "[data-version-operation]") return this.all().find(node => "versionOperation" in node.dataset);
    return null;
  }
  set innerHTML(value) {
    // Model the integrated channel form and its native controls.
    assert.match(value, /select name="channel"/);
    this.elements = { channel: new Element("select"), custom_tag: new Element("input") };
    this.custom = new Element("label");
    this.custom.append(this.elements.custom_tag);
    this.channelRow = new Element("div");
    const channelLabel = new Element("label");
    channelLabel.append(this.elements.channel);
    this.channelRow.append(channelLabel, this.custom);
    this.append(this.channelRow);
  }
}
async function open(currentSoftware, imageState = "up_to_date") {
  const content = new Element("div"), state = new Element("span"), launcher = new Element("button");
  const dialog = new Element("dialog");
  const close = new Element("button"), refresh = new Element("button");
  dialog.querySelector = selector => ({ "[data-version-content]": content, "[data-version-state]": state, "[data-version-close]": close, "[data-version-refresh]": refresh })[selector];
  dialog.querySelectorAll = () => [];
  const document = {
    querySelector: selector => ({ "[data-version-open]": launcher, "[data-version-workspace-dialog]": dialog, "[data-minecraft-workspace-csrf]": { value: "csrf-token" } })[selector],
    createElement: tag => new Element(tag),
    createTextNode: text => { const node = new Element("text"); node.textContent = text; return node; }
  };
  const calls = [];
  let openWindow;
  async function fetch(url, options) {
    calls.push({ url, options });
    const parsed = new URL(url, "http://example");
    let payload;
    if (parsed.pathname === "/api/minecraft/workspace/settings") payload = { minecraft: { id: "jv-family1", server_type: currentSoftware, image_tag: "stable" } };
    else if (parsed.pathname === "/api/version/workspace/status") {
      const target = parsed.searchParams.get("server_type");
      payload = { server_software: (target || currentSoftware).replace(/^./, c => c.toUpperCase()), selected_candidate: "1.21.8", stack_state: "updates_available", installed: "1.21.8", update_available: true, runtime_update_required: true, server_supported: true, crossplay_enabled: true, crossplay_compatible: true, plan_fingerprint: "reviewed-" + target, image_state: imageState, managed_image_digest: "private-digest" };
    } else if (parsed.pathname === "/api/minecraft/workspace/snapshot") payload = { players: { online: 3 } };
    else if (parsed.pathname === "/api/version/workspace/update") {
      assert.equal(options.method, "POST");
      currentSoftware = options.body.get("server_type");
      payload = { operation: { operation_id: "switch-operation" } };
    } else if (parsed.pathname === "/api/version/workspace/update-operation") {
      payload = parsed.searchParams.has("id") ? { operation: { state: "succeeded", status: "Software switched." } } : {};
    } else throw new Error("Unexpected request: " + url);
    return { ok: true, json: async () => payload };
  }
  vm.runInNewContext(source, {
    location: {origin: "http://example", search: "?server=jv-family1"},
    justVoxelServerSelection: async () => {},
    justVoxelSelectionGeneration: {version:0},
    justVoxelSelectedURL: value => {const u=new URL(value,"http://example");u.searchParams.set("server","jv-family1");return u.pathname+u.search;},
    window: new EventTarget(),
    document, fetch, URLSearchParams, setTimeout,
    setupWorkspaceWindow: (_, options) => { openWindow = options.onOpen; return { open: options.onOpen }; },
    readWorkspaceWindowState: () => ({ open: false })
  });
  await openWindow();
  assert.equal(state.textContent, "");
  return { content, state, calls };
}
function cards(page) { return page.content.all().filter(node => node.className?.split(" ").includes("version-software-choice")); }
function button(root, text) { return root.all().find(node => node.tagName === "button" && node.textContent === text); }
async function review(page) {
  const form = page.content.all().find(node => node.tagName === "form");
  form.events.submit({ preventDefault() {} });
  // Submit does not return preview's Promise. All fetch/json mocks resolve
  // through microtasks, which finish before this next event-loop turn.
  await new Promise(resolve => setImmediate(resolve));
}
(async () => {
  for (const current of ["paper", "purpur"]) {
    const page = await open(current);
    const choices = cards(page);
    assert.deepEqual(choices.map(choice => choice.children[1].children[0].textContent), ["Paper", "Purpur"]);
    assert.ok(!page.content.textContent.includes("Vanilla"));
    assert.ok(!page.content.textContent.includes("Container channel"));
    assert.ok(!page.content.textContent.includes("Check for updates"));
    assert.equal(page.content.all().filter(node => node.tagName === "select").length, 0);
    const target = current === "paper" ? "purpur" : "paper";
    const selectable = choices.find(choice => choice.children[1].children[0].textContent.toLowerCase() === target);
    await selectable.events.click(); await review(page);
    const consent = page.content.all().find(node => node.tagName === "input" && node.type === "checkbox");
    const apply = button(page.content, "Apply changes");
    assert.equal(consent.checked, false); assert.equal(apply.disabled, true);
    assert.ok(page.content.textContent.includes("cold backup"));
    consent.checked = true; consent.events.change();
    await apply.events.click();
    const posts = page.calls.filter(call => call.options.method === "POST");
    assert.equal(posts.length, 1);
    assert.equal(posts[0].options.body.get("server_type"), target);
    assert.equal(posts[0].options.body.has("image_tag"), false);
    assert.equal(posts[0].options.body.get("confirm_players"), "yes");
    assert.equal(posts[0].options.body.get("csrf"), "csrf-token");
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("server software card behavior: %v\n%s", err, output)
	}
}

func TestSetupSimpleSoftwareCardWording(t *testing.T) {
	data, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, old := range []string{"Recommended · plugins · Bedrock cross-play enabled by default when supported.", "Use recommended setup"} {
		if strings.Contains(text, old) {
			t.Fatalf("old First Setup presentation remains: %s", old)
		}
	}
	for _, want := range []string{`{{template "setup-simple-card" .}}`, `{{range .SimpleSoftware}}`, `src="/static/server-software/{{.ID}}.svg"`, `class="server-software-copy"`, `<strong>{{.Name}}</strong><small>{{.Description}}</small>`} {
		if !strings.Contains(text, want) {
			t.Fatalf("shared software card missing %s", want)
		}
	}
	if simpleSoftware("plugins")[0].Description != "Plugins · Cross-play" {
		t.Fatal("Paper Simple description changed")
	}
}
