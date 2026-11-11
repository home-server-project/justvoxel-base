package server

import (
	"os/exec"
	"strings"
	"testing"
)

func TestQuickLookConcealmentAndAlignment(t *testing.T) {
	read := func(name string) string {
		data, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	header := read("templates/header.html")
	if !strings.Contains(header, `data-quick-look-panel inert aria-hidden="true"`) {
		t.Fatal("drawer content must start inaccessible while its toggle remains available")
	}
	css := read("static/app.css")
	for _, want := range []string{
		`.quick-look-panel{visibility:hidden;`,
		`.quick-look{position:fixed;top:40px;`,
		`.quick-look.is-open>.quick-look-panel{visibility:visible;transform:translateY(0);transition-delay:0s;pointer-events:auto}`,
		`.quick-look{left:4px;right:4px}`,
		`z-index:70;clip-path:inset(0 -100vmax -100vmax)`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("drawer visibility or responsive contract missing %q", want)
		}
	}
	for _, selector := range []string{".quick-look-panel{", ".app-header.topbar{height:36px;"} {
		start := strings.Index(css, selector)
		if start < 0 {
			t.Fatalf("missing %s", selector)
		}
		end := strings.Index(css[start:], "}")
		if end < 0 {
			t.Fatalf("unclosed %s", selector)
		}
		rule := css[start : start+end]
		shape := "border:1px solid #303942;border-radius:12px;"
		if selector == ".quick-look-panel{" {
			shape = "border:1px solid #303942;border-radius:0 0 12px 12px;border-top:0;"
		}
		if !strings.Contains(rule, shape) {
			t.Fatalf("drawer and topbar must share their border and rounded shape: %s", selector)
		}
		if strings.HasPrefix(selector, ".app-header") && !strings.Contains(rule, "background:#151a1f;") {
			t.Fatal("topbar must use an opaque background")
		}
	}

	outer := css[strings.Index(css, ".quick-look{"):]
	outer = outer[:strings.Index(outer, "}")]
	if strings.Contains(outer, "transform:") {
		t.Fatal("outer drawer must remain stationary")
	}
	for _, want := range []string{
		".quick-look-panel{visibility:hidden;transform:translateY(-100%)",
		".quick-look-handle{position:absolute;top:0;transition:top .2s ease;",
		".quick-look.is-open>.quick-look-handle{top:100%}",
		".app-header.topbar.quick-look-open{border-bottom-left-radius:0;border-bottom-right-radius:0;box-shadow:none}",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("joined drawer presentation missing %q", want)
		}
	}

	script := read("static/app.js")
	start := strings.Index(script, `  const topbar = document.querySelector(".topbar");`)
	if start < 0 {
		t.Fatal("drawer positioning missing")
	}
	end := strings.Index(script[start:], "  const setValue =")
	if end < 0 {
		t.Fatal("drawer toggle boundary missing")
	}
	program := `
const assert = require("node:assert/strict");
let bounds = { left: 8, width: 1184, bottom: 44 }, resize, refreshes = 0, cleared = 0;
let identity = { role: "administrator", username: "test" }, refreshTimer = null;
const panel = { inert: true, attributes: {}, setAttribute(key, value) { this.attributes[key] = value; } };
const classes = new Set();
const quickLook = { style: {}, querySelector: () => panel, classList: { toggle(name, open) { open ? classes.add(name) : classes.delete(name); } } };
const quickLookToggle = { attributes: {}, setAttribute(key, value) { this.attributes[key] = value; } };
const topbarClasses = new Set();
const document = { querySelector: () => ({ getBoundingClientRect: () => bounds,
  classList: { toggle(name, open) { open ? topbarClasses.add(name) : topbarClasses.delete(name); } } }) };
const stored = new Map();
const window = { innerWidth: 1200, addEventListener: (event, callback) => { assert.equal(event, "resize"); resize = callback; },
  localStorage: { setItem: (key, value) => stored.set(key, value) },
  setInterval: () => 1, clearInterval: () => { cleared++; } };
const refreshQuickLook = () => { refreshes++; };
let selectionRefreshes = 0;
globalThis.justVoxelServerSelection = (slot, refresh) => {
  assert.equal(slot, panel); assert.equal(refresh, true); selectionRefreshes++;
  return { then(callback) { callback(); return { catch() {} }; } };
};
` + script[start:start+end] + `
assert.deepEqual(quickLook.style, { top: "44px", left: "8px", width: "1184px", right: "auto" });
setOpen(true);
assert(classes.has("is-open"));
assert(topbarClasses.has("quick-look-open"));
assert.equal(panel.inert, false);
assert.equal(panel.attributes["aria-hidden"], "false");
assert.equal(quickLookToggle.attributes["aria-expanded"], "true");
assert.equal(refreshes, 1);
assert.equal(selectionRefreshes, 1);
assert.equal(stored.get(stateKey("test")), "open");
setOpen(false);
assert(!classes.has("is-open"));
assert(!topbarClasses.has("quick-look-open"));
assert.equal(panel.inert, true);
assert.equal(panel.attributes["aria-hidden"], "true");
assert.equal(quickLookToggle.attributes["aria-expanded"], "false");
assert.equal(refreshTimer, null);
assert.equal(cleared, 1);
assert.equal(stored.get(stateKey("test")), "closed");
window.innerWidth = 390;
bounds = { left: 4, width: 382, bottom: 44 };
resize();
assert.deepEqual(quickLook.style, { top: "44px", left: "4px", width: "382px", right: "auto" });
setOpen(true, false);
assert.equal(panel.inert, false);
assert.equal(stored.get(stateKey("test")), "closed");
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("drawer concealment and alignment: %v\n%s", err, output)
	}
}
