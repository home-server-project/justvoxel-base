package server

import (
	"fmt"
	"os/exec"
	"regexp"
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
  constructor(tag) { this.dataset = {}; this.tagName = tag; this.children = []; this.events = {}; this.attributes = {}; this.disabled = false; this.checked = false; this.value = ""; this.ownText = ""; }
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
    if (parsed.pathname === "/api/minecraft/workspace/settings") payload = { minecraft: { server_type: currentSoftware, image_tag: "stable" } };
    else if (parsed.pathname === "/api/version/workspace/status") {
      const target = parsed.searchParams.get("server_type");
      payload = { server_software: (target || currentSoftware).replace(/^./, c => c.toUpperCase()), selected_candidate: "1.21.8", stack_state: "updates_available", installed: "1.21.8", update_available: true, runtime_update_required: true, server_supported: true, crossplay_enabled: true, crossplay_compatible: true, plan_fingerprint: "reviewed-" + target, image_state: imageState, managed_image_digest: "private-digest" };
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
async function review(page) {
  const form = page.content.all().find(node => node.tagName === "form");
  form.events.submit({ preventDefault() {} });
  // Submit does not return preview's Promise. All fetch/json mocks resolve
  // through microtasks, which finish before this next event-loop turn.
  await new Promise(resolve => setImmediate(resolve));
}
(async () => {
  for (const [state, label] of [["up_to_date", "Current"], ["updates_available", "Update available"], ["unavailable", "Unable to check"], ["not_applicable", "Not applicable"]]) {
    const page = await open("paper", state);
    assert.ok(page.content.textContent.includes("Container image: " + label));
    assert.ok(!page.content.textContent.includes("private-digest"));
  }
  for (const current of ["paper", "purpur", "vanilla"]) {
    const page = await open(current);
    const choices = cards(page);
    assert.deepEqual(choices.map(choice => choice.children[1].children[0].textContent), ["Paper", "Purpur", "Vanilla"]);
    for (const choice of choices) {
      const type = choice.children[1].children[0].textContent.toLowerCase();
      const isCurrent = type === current;
      assert.equal(choice.tagName, "button");
      assert.equal(choice.className.includes("is-current"), isCurrent);
      assert.equal(choice.className.includes("is-selected"), isCurrent);
      assert.equal(choice.attributes["aria-pressed"], String(isCurrent));
      assert.equal(choice.children[1].children[1].textContent, isCurrent ? "Current" : (current === "vanilla" || type === "vanilla") ? "Switch Unavailable" : "Switch Available");
      if (type === "vanilla") assert.deepEqual(choice.children[1].children.slice(2).map(node => node.textContent), ["Original Minecraft", "Java only, no plugins"]);
    }
    assert.ok(!page.content.textContent.includes("Current server software"));
    assert.ok(!page.content.textContent.includes("Change to "));
    const warning = page.content.all().find(node => node.className === "notice warning" && node.ownText.startsWith("Requires Reset Minecraft and setup again."));
    const installed = choices.find(choice => choice.className.includes("is-current"));
    const unsupported = choices.find(choice => current === "vanilla" ? choice.children[1].children[0].textContent === "Paper" : choice.children[1].children[0].textContent === "Vanilla");
    const initialCalls = page.calls.length;
    await unsupported.events.click(); await review(page);
    assert.equal(page.calls.length, initialCalls);
    assert.equal(warning.hidden, false);
    assert.equal(button(page.content, "Apply changes"), undefined);
    await installed.events.click(); assert.equal(warning.hidden, true);
    if (current === "vanilla") continue;
    const target = current === "paper" ? "purpur" : "paper";
    const selectable = choices.find(choice => choice.children[1].children[0].textContent.toLowerCase() === target);
    await selectable.events.click();
    assert.equal(page.calls.length, initialCalls);
    assert.equal(selectable.attributes["aria-pressed"], "true");
    assert.ok(selectable.className.includes("is-selected"));
    assert.equal(installed.children[1].children[1].textContent, "Current");
    const form = page.content.all().find(node => node.tagName === "form");
    assert.equal(form.elements.custom_tag.disabled, true);
    form.elements.channel.value = "latest"; form.elements.channel.events.change();
    await button(page.content, "Check for updates").events.click();
    assert.equal(button(page.content, "Apply changes"), undefined);
    await review(page);
    assert.equal(page.content.all().filter(node => "versionReview" in node.dataset).length, 1);
    assert.ok(page.content.textContent.includes("Players online 3"));
    assert.ok(page.content.textContent.includes("Minecraft version 1.21.8"));
    assert.ok(page.content.textContent.includes("Container channel stable → latest"));
    const transition = current === "paper" ? "Paper → Purpur" : "Purpur → Paper";
    assert.ok(page.content.textContent.includes("Server software " + transition));
    assert.ok(page.content.textContent.includes("cold backup"));
    assert.ok(!page.calls.some(call => call.options.method === "POST"));
    await installed.events.click();
    assert.equal(button(page.content, "Apply changes"), undefined);
    await selectable.events.click(); await review(page);
    await unsupported.events.click();
    assert.equal(warning.hidden, false);
    assert.equal(button(page.content, "Apply changes"), undefined);
    assert.equal(page.content.all().find(node => node.tagName === "input" && node.attributes.role === "switch"), undefined);
    await selectable.events.click(); await review(page);
    let apply = button(page.content, "Apply changes");
    const cancel = button(page.content, "Cancel review");
    assert.equal(cancel.className, "secondary");
    assert.equal(cancel.parent, apply.parent);
    assert.deepEqual(cancel.parent.children, [cancel, apply]);
    cancel.events.click();
    assert.equal(selectable.attributes["aria-pressed"], "false");
    assert.ok(installed.className.includes("is-selected"));
    assert.equal(form.elements.channel.value, "stable");
    assert.equal(button(page.content, "Apply changes"), undefined);
    await selectable.events.click();
    form.elements.channel.value = "custom"; form.elements.channel.events.change();
    assert.equal(form.elements.custom_tag.disabled, false);
    form.elements.custom_tag.value = "custom-release"; form.elements.custom_tag.events.input();
    await review(page);
    apply = button(page.content, "Apply changes");
    const consent = page.content.all().find(node => node.tagName === "input" && node.type === "checkbox");
    assert.equal(consent.parent.textContent.trim(), "Confirm changes and allow interruption of online players.");
    assert.equal(consent.attributes.role, "switch");
    assert.equal(consent.checked, false); assert.equal(apply.disabled, true);
    consent.checked = true; consent.events.change(); assert.equal(apply.disabled, false);
    await apply.events.click();
    const posts = page.calls.filter(call => call.options.method === "POST");
    assert.equal(posts.length, 1);
    assert.equal(posts[0].url, "/api/version/workspace/update");
    assert.equal(posts[0].options.body.get("server_type"), target);
    assert.equal(posts[0].options.body.get("image_tag"), "custom-release");
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
	if strings.Contains(text, "Recommended · plugins · Bedrock cross-play enabled by default when supported.") {
		t.Fatal("old Recommended setup Paper wording remains")
	}
	// Recommended and Advanced setup each contain the same three cards. Check
	// each card's text and icon/copy wrappers without requiring adjacent elements.
	cardPattern := regexp.MustCompile(`(?s)<label class="setup-server-type server-software-card is-available">(.*?)</label>`)
	sections := strings.SplitN(text, "{{else}}\n  <div class=\"setup-stage-header\">", 2)
	if len(sections) != 2 {
		t.Fatal("Recommended and Advanced setup sections must be present")
	}
	for sectionIndex, section := range sections {
		cards := cardPattern.FindAllStringSubmatch(section, -1)
		if len(cards) != 3 {
			t.Fatalf("setup section %d must contain three software cards", sectionIndex)
		}
		for index, software := range []struct{ value, name, description string }{
			{"paper", "Paper", "Recommended · Plugins and managed Bedrock cross-play."},
			{"purpur", "Purpur", "Plugins and managed Bedrock cross-play."},
			{"vanilla", "Vanilla", "Original Minecraft · Java only · no plugins."},
		} {
			card := cards[index][1]
			for _, want := range []string{
				`name="server_type" value="` + software.value + `"`,
				`<span class="server-software-icon" aria-hidden="true">`,
				`<span class="server-software-copy">`,
				`<strong>` + software.name + `</strong>`,
				`<span class="software-availability">Available</span>`,
				`<small>` + software.description + `</small>`,
			} {
				if strings.Count(card, want) != 1 {
					t.Fatalf("setup section %d %s card must contain %q once", sectionIndex, software.name, want)
				}
			}
			// The official Paper asset is still pending; preserve the shared local
			// SVG references for the two bundled icons.
			if software.value != "paper" && !strings.Contains(card, `<img src="/static/server-software/`+software.value+`.svg" alt="" width="48" height="48">`) {
				t.Fatalf("setup %s card must use its shared local SVG icon", software.name)
			}
		}
	}
}
