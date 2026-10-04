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
const source = ` + fmt.Sprintf("%q", string(script)) + `;
class Element {
  constructor(tag) { this.tagName = tag; this.children = []; this.events = {}; this.attributes = {}; this.disabled = false; this.checked = false; this.value = ""; this.ownText = ""; }
  set textContent(value) { this.ownText = String(value); this.children = []; }
  get textContent() { return this.ownText + this.children.map(child => child.textContent).join(" "); }
  append(...children) { for (const child of children) { child.parent = this; this.children.push(child); } }
  appendChild(child) { this.append(child); return child; }
  replaceChildren(...children) { this.children = []; this.append(...children); }
  remove() { this.parent.children = this.parent.children.filter(child => child !== this); }
  setAttribute(name, value) { this.attributes[name] = value; }
  addEventListener(name, fn) { this.events[name] = fn; }
  all() { return this.children.flatMap(child => [child, ...child.all()]); }
  querySelector(selector) {
    if (selector === "[data-version-custom]") return this.custom;
    return null;
  }
  set innerHTML(value) {
    // Only the unchanged container-channel form uses innerHTML in the Software tab.
    assert.match(value, /select name="channel"/);
    this.elements = { channel: new Element("select"), custom_tag: new Element("input") };
    this.custom = new Element("label");
  }
}
async function open(currentSoftware) {
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
    if (parsed.pathname === "/api/minecraft/workspace/settings") payload = { minecraft: { server_type: currentSoftware, image_tag: "stable" } };
    else if (parsed.pathname === "/api/version/workspace/status") {
      const target = parsed.searchParams.get("server_type");
      payload = { server_software: (target || currentSoftware).replace(/^./, c => c.toUpperCase()), selected_candidate: "1.21.8", stack_state: "updates_available", server_supported: true, crossplay_enabled: true, crossplay_compatible: true, plan_fingerprint: "reviewed-" + target };
    } else if (parsed.pathname === "/api/dashboard-status") payload = { players: { online: 3 } };
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
(async () => {
  for (const current of ["paper", "purpur", "vanilla"]) {
    const page = await open(current);
    const choices = cards(page);
    assert.deepEqual(choices.map(choice => choice.children[0].textContent), ["Paper", "Purpur", "Vanilla"]);
    for (const choice of choices) {
      const type = choice.children[0].textContent.toLowerCase();
      const isCurrent = type === current;
      assert.equal(choice.className.includes("is-current"), isCurrent);
      assert.equal(choice.children[1].textContent, isCurrent ? "Current" : (current === "vanilla" || type === "vanilla") ? "Unavailable" : "Available");
      if (!isCurrent && (current === "vanilla" || type === "vanilla")) {
        assert.equal(choice.attributes["aria-disabled"], "true");
        assert.ok(choice.children.some(node => node.textContent === "Requires Reset Minecraft and setup again."));
        assert.equal(choice.events.click, undefined);
        assert.equal(choice.tagName, "div");
      } else if (isCurrent) {
        assert.equal(choice.events.click, undefined);
        assert.ok(!choice.textContent.includes("Requires Reset"));
      }
    }
    assert.ok(!page.content.textContent.includes("Current server software"));
    assert.ok(!page.content.textContent.includes("Change to "));
    if (current === "vanilla") continue;
    const target = current === "paper" ? "purpur" : "paper";
    const selectable = choices.find(choice => choice.children[0].textContent.toLowerCase() === target);
    assert.equal(selectable.tagName, "button");
    await selectable.events.click();
    assert.equal(selectable.disabled, true);
    assert.ok(page.calls.some(call => new URL(call.url, "http://example").searchParams.get("server_type") === target));
    assert.ok(page.calls.some(call => call.url === "/api/dashboard-status"));
    assert.ok(page.content.textContent.includes("Review server software change"));
    assert.ok(page.content.textContent.includes("Players online 3"));
    assert.ok(page.content.textContent.includes("cold backup"));
    assert.ok(page.content.textContent.includes("restore the previous configuration"));
    let apply = button(page.content, "Apply server software change");
    assert.equal(apply.disabled, true);
    assert.ok(!page.calls.some(call => call.options.method === "POST"));
    button(page.content, "Cancel review").events.click();
    assert.equal(selectable.disabled, false);
    assert.equal(button(page.content, "Apply server software change"), undefined);
    await selectable.events.click();
    apply = button(page.content, "Apply server software change");
    const consent = page.content.all().find(node => node.tagName === "input" && node.type === "checkbox");
    assert.ok(consent.parent.textContent.includes("Confirm the reviewed switch"));
    consent.checked = true;
    consent.events.change();
    assert.equal(apply.disabled, false);
    await apply.events.click();
    const posts = page.calls.filter(call => call.options.method === "POST");
    assert.equal(posts.length, 1);
    assert.equal(posts[0].url, "/api/version/workspace/update");
    assert.equal(posts[0].options.body.get("server_type"), target);
    assert.equal(posts[0].options.body.get("csrf"), "csrf-token");
    assert.equal(posts[0].options.body.get("plan_fingerprint"), "reviewed-" + target);
    assert.equal(posts[0].options.body.get("confirm_players"), "yes");
    assert.ok(page.calls.some(call => call.url === "/api/version/workspace/update-operation?id=switch-operation"));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("server software card behavior: %v\n%s", err, output)
	}
}

func TestSetupRecommendedPaperCardWording(t *testing.T) {
	data, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	want := `<strong>Paper</strong><small>Recommended · Plugins and managed Bedrock cross-play.</small>`
	if strings.Count(text, want) != 1 {
		t.Fatal("Recommended setup Paper card must contain the exact approved wording once")
	}
	if strings.Contains(text, "Recommended · plugins · Bedrock cross-play enabled by default when supported.") {
		t.Fatal("old Recommended setup Paper wording remains")
	}
	for _, unchanged := range []string{
		`<strong>Purpur</strong><small>Plugins and managed Bedrock cross-play.</small>`,
		`<strong>Vanilla</strong><small>Original Minecraft · Java only · no plugins.</small>`,
	} {
		if !strings.Contains(text, unchanged) {
			t.Fatalf("setup card wording changed: %s", unchanged)
		}
	}
}
