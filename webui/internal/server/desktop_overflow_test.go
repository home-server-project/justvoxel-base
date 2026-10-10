package server

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDesktopWorkspaceViewportBounds(t *testing.T) {
	source, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "const workspaceTopInset =")
	end := strings.Index(script, "const setupWorkspaceWindow =")
	if start < 0 || end <= start {
		t.Fatal("workspace geometry helpers missing")
	}
	program := `
const assert = require("node:assert/strict");
const window = { innerHeight: 800 };
const document = { documentElement: { clientWidth: 1200 }, querySelector: () => ({ getBoundingClientRect: () => ({ bottom: 44 }) }) };
const workspaceCompactQuery = { matches: false };
function makeWindow(left, top, width, height) {
  const style = { left: left + "px", top: top + "px", width: width + "px", height: height + "px", setProperty(key, value) { this[key] = value; } };
  return { open: true, style, getBoundingClientRect() {
    return { left: parseFloat(style.left), top: parseFloat(style.top),
      width: Math.min(parseFloat(style.width), parseFloat(style["--workspace-available-width"] || Infinity)),
      height: Math.min(parseFloat(style.height), parseFloat(style["--workspace-available-height"] || Infinity)) };
  } };
}
` + script[start:end] + `
function assertFits(element) {
  const rect = element.getBoundingClientRect();
  assert(rect.left >= 8 && rect.top >= 52);
  assert(rect.left + rect.width <= document.documentElement.clientWidth - 8);
  assert(rect.top + rect.height <= window.innerHeight - 8);
}
const first = makeWindow(1100, 750, 900, 650);
const second = makeWindow(40, 80, 400, 300);
clampWorkspaceWindow(first); clampWorkspaceWindow(second);
assertFits(first); assertFits(second);
assert.equal(second.style.left, "40px");
assert.equal(second.style.top, "80px");
// Native resize is capped at the space left at the current position.
second.style.width = "2000px"; second.style.height = "2000px";
assertFits(second);
clampWorkspaceWindow(second); assertFits(second);
// Restore oversized geometry after shrinking the browser, including short screens.
document.documentElement.clientWidth = 720; window.innerHeight = 200;
clampWorkspaceWindow(first); clampWorkspaceWindow(second);
assertFits(first); assertFits(second);
first.style.left = "-200px"; first.style.top = "-100px";
clampWorkspaceWindow(first); assertFits(first);
// Compact layouts and closed windows are owned by CSS, without desktop mutations.
workspaceCompactQuery.matches = true;
const compact = makeWindow(-50, -50, 900, 800);
clampWorkspaceWindow(compact); assert.equal(compact.style.left, "-50px");
workspaceCompactQuery.matches = false; compact.open = false;
clampWorkspaceWindow(compact); assert.equal(compact.style.top, "-50px");
`
	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("workspace bounds: %v\n%s", err, output)
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`body:has(> #dashboard){display:flex;flex-direction:column;height:100dvh;min-height:0}`,
		`body>#dashboard{flex:1;min-height:0;width:100%;overflow:auto}`,
		`dialog.workspace-window{`,
		`position:fixed;right:auto;bottom:auto;`,
		`max-width:var(--workspace-available-width,calc(100vw - 16px));`,
		`max-height:var(--workspace-available-height,var(--jv-workspace-height));`,
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("desktop layout missing %q", want)
		}
	}
	if strings.Contains(script[strings.Index(script, "const setupWorkspaceWindow ="):strings.Index(script, "const storageOpen =")], "requestAnimationFrame") {
		t.Fatal("restored workspace geometry must be bounded before the first paint")
	}
}
