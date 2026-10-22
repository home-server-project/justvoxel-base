package server

import (
	"os/exec"
	"strings"
	"testing"
)

func TestMinecraftResetRecoveryBrowserFlow(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	start := strings.Index(source, "  const systemResetOperationLabel =")
	end := strings.Index(source, "  const renderAbout =")
	if start < 0 || end < start {
		t.Fatal("reset functions missing")
	}
	program := `
const assert = require("node:assert/strict");
class Element {
 constructor(tag) { this.tagName = tag; this.children = []; this.events = {}; this.dataset = {}; this.style = {setProperty(){}}; this.classList = {add(){},toggle(){}}; }
 append(...nodes) { this.children.push(...nodes); }
 appendChild(node) { this.append(node); return node; }
 replaceChildren(...nodes) { this.children = nodes; }
 addEventListener(name, callback) { this.events[name] = callback; }
 setAttribute() {}
 all() { return this.children.flatMap(child => [child, ...child.all()]); }
}
const content = new Element("div"), state = new Element("p");
const document = {createElement: tag => new Element(tag)};
const consoleCalls = [];
const window = {JustVoxelLiveConsole: {updateState(){},open(value){consoleCalls.push(value)}}, clearTimeout(){}, setTimeout(){throw Error("terminal recovery must not poll")}};
let resetPollTimer = 0, activeFactoryResetOperationID = "", loadSequence = 0, currentTab = "reset";
const clearResetPoll = () => {};
const systemPanel = () => ({panel: new Element("section"),heading: new Element("header")});
const systemResetList = () => new Element("section");
const resetLiveConsoleUnauthorized = () => {};
const calls = [];
const originalID = "12345678-1234-4123-8123-123456789abc";
let operation = {operation_type:"minecraft_reset",operation_id:originalID,state:"needs_attention",stage:"interrupted",status:"Removal interrupted"};
let failPlan = false;
const systemFetchJSON = async url => {
 calls.push(url);
 if (url.endsWith("/factory/current")) return {};
 assert.ok(url.endsWith("/minecraft/current"));
 return {operation};
};
const systemPostForm = async (url, fields) => {
 calls.push(url);
 if (url.endsWith("/plan")) {
  if (failPlan) throw Error("Minecraft reset recovery could not be verified. No additional removal was performed.");
  return {plan_fingerprint:"reviewed",participants:[]};
 }
 assert.ok(url.endsWith("/apply"));
 assert.equal(fields.plan_fingerprint,"reviewed");
 // The Management Agent retries the current persistent ID after its own review.
 return {operation:{...operation,state:"needs_attention"}};
};
` + source[start:end] + `
const button = label => content.all().find(node => node.tagName === "button" && (node.textContent === label || node.innerHTML?.startsWith("<strong>" + label + "</strong>")));
(async () => {
 await planReset("minecraft");
 assert.deepEqual(calls,["/api/system/workspace/reset/minecraft/current"]);
 assert.ok(button("Continue removal"));
 assert.ok(button("Live Console"));
 assert.ok(content.all().some(node => node.textContent === "Removal interrupted"));
 assert.equal(button("Reset Minecraft"),undefined);
 button("Live Console").events.click();
 assert.equal(consoleCalls[0].operationID,originalID);
 await button("Continue removal").events.click();
 assert.ok(calls.at(-1).endsWith("/minecraft/plan"));
 assert.ok(button("Reset Minecraft"));
 const slider = content.all().find(node => node.type === "range");
 const confirm = content.all().find(node => node.type === "checkbox");
 slider.value = "100"; slider.events.input();
 confirm.checked = true; confirm.events.change();
 assert.equal(button("Reset Minecraft").disabled,false);
 await button("Reset Minecraft").events.click();
 assert.equal(operation.operation_id,originalID);
 assert.ok(button("Continue removal"));
 operation = null;
 calls.length = 0;
 await renderReset(loadSequence);
 assert.ok(button("Reset Minecraft"));
 assert.ok(button("Full Factory Reset"));
 failPlan = true;
 await button("Reset Minecraft").events.click();
 assert.ok(state.textContent.includes("No additional removal was performed"));
 assert.equal(button("Continue removal"),undefined);
 failPlan = false;
 await button("Reset Minecraft").events.click();
 assert.ok(button("Reset Minecraft"));
 assert.ok(calls.at(-1).endsWith("/minecraft/plan"));
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reset recovery: %v\n%s", err, output)
	}
}

func TestDashboardCanonicalBrowserURL(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	end := strings.Index(source, "globalThis.justVoxelServerSelection =")
	if end < 0 {
		t.Fatal("selection URL handling missing")
	}
	if !strings.Contains(source, `if (workspace === "version" && tab === "switch") tab = "software";`) {
		t.Fatal("canonical Switch tab does not open the existing control")
	}
	program := `
const assert = require("node:assert/strict");
const canonical = "/?result=started&tab=switch&workspace=version";
const location = new URL("http://example/?server=jv-stale1&workspace=version&tab=software&result=started#saved");
const dashboard = {dataset:{dashboardUrl:canonical,selectedMinecraftId:""}};
const document = {querySelector:() => dashboard};
const window = {location,history:{state:{saved:true},replaceState(state,title,target){assert.equal(state.saved,true); location.href = new URL(target,location).href;}}};
globalThis.location = location;
` + source[:end] + `
assert.equal(location.pathname + location.search + location.hash,canonical + "#saved");
for (const path of ["/api/dashboard-status","/api/minecraft/workspace/selection"]) {
 const target = new URL(justVoxelSelectedURL(path),location);
 assert.equal(target.searchParams.get("selection"),"none");
 assert.equal(target.searchParams.has("server"),false);
}
const blocked = new URL(justVoxelSelectedURL("/api/version/workspace/status"),location);
assert.equal(blocked.searchParams.has("server"),true);
assert.equal(blocked.searchParams.get("server"),"");
location.searchParams.set("server","jv-family1");
dashboard.dataset.selectedMinecraftId = "jv-family1";
const selected = new URL(justVoxelSelectedURL("/api/dashboard-status"),location);
assert.equal(selected.searchParams.get("server"),"jv-family1");
assert.equal(selected.searchParams.has("selection"),false);
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser selection recovery: %v\n%s", err, output)
	}
}
