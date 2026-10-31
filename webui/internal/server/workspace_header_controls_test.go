package server

import (
	"regexp"
	"strings"
	"testing"
)

func TestWorkspaceHeaderControls(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	refresh := regexp.MustCompile(`<button[^>]*data-([a-z-]+)-refresh\b[^>]*>(.*?)</button>`)
	close := regexp.MustCompile(`<button[^>]*data-([a-z-]+)-close\b[^>]*>(.*?)</button>`)
	refreshes := make(map[string]string)
	closes := make(map[string]string)
	for _, match := range refresh.FindAllStringSubmatch(string(header), -1) {
		refreshes[match[1]] = match[0]
		if match[2] != "Refresh" || !strings.Contains(match[0], "workspace-header-refresh") || strings.Contains(match[0], "↻") {
			t.Errorf("%s refresh is not the shared text control", match[1])
		}
	}
	for _, match := range close.FindAllStringSubmatch(string(header), -1) {
		closes[match[1]] = match[0]
		if !strings.Contains(match[2], "<svg ") || !strings.Contains(match[0], "workspace-header-close") || strings.Contains(match[0], "&times;") || strings.Contains(match[0], "×") || !strings.Contains(match[0], "aria-label=") {
			t.Errorf("%s close is not the shared accessible SVG control", match[1])
		}
	}
	for _, name := range []string{"network", "system", "minecraft", "storage", "backups", "migration", "version", "system-update", "files", "terminal", "gateway"} {
		if refreshes[name] == "" || closes[name] == "" {
			t.Errorf("%s workspace lost its header controls", name)
		}
	}
	if refreshes["system-monitor"] != "" {
		t.Error("System Monitor must refresh automatically without a manual button")
	}
	if closes["system-monitor"] == "" {
		t.Error("system monitor lost its close control")
	}
	app, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	live := strings.Split(strings.Split(string(app), "window.JustVoxelLiveConsole = (() => {")[1], "\n})();")[0]
	for _, required := range []string{"workspace-header-close", `createElementNS("http://www.w3.org/2000/svg", "svg")`, `svg.setAttribute("viewBox", "0 0 24 24")`, `svg.setAttribute("stroke", "currentColor")`, `path.setAttribute("d", "M5 5l14 14M19 5L5 19")`, `dismiss.setAttribute("aria-label", "Close Live Console")`} {
		if !strings.Contains(live, required) {
			t.Errorf("Live Console shared SVG close contract missing %s", required)
		}
	}
	if strings.Contains(live, "×") || strings.Contains(live, "&times;") {
		t.Error("Live Console must not use a literal close glyph")
	}

	controlCenter := regexp.MustCompile(`(?s)<summary class="control-center-trigger"[^>]*>(.*?)</summary>`).FindStringSubmatch(string(header))
	if len(controlCenter) != 2 || !strings.Contains(controlCenter[1], `<svg class="control-center-icon"`) || !strings.Contains(controlCenter[1], `aria-hidden="true"`) || !strings.Contains(controlCenter[1], `Control Center`) || strings.Contains(controlCenter[1], "⚙") {
		t.Error("Control Center must retain its label and use an accessible SVG gear")
	}
	networkCSS, err := assets.ReadFile("static/network-workspace.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(networkCSS), `.network-workspace-header .system-monitor-header-button:not(.workspace-header-refresh){`) || strings.Contains(string(networkCSS), `.network-workspace-header .system-monitor-header-button{`) {
		t.Error("network header button rule must leave shared Refresh typography and geometry intact")
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{".workspace-header-refresh{", ".workspace-header-close{", ".workspace-header-close svg{"} {
		if !strings.Contains(string(css), class) {
			t.Errorf("shared workspace control CSS missing %s", class)
		}
	}
	if !strings.Contains(string(css), ".control-center-trigger .control-center-icon{display:block;flex:none;width:19px;height:19px;stroke:currentColor") {
		t.Error("Control Center gear icon must use currentColor SVG sizing")
	}
	settingsRule := ".workspace-window .system-monitor-header-button.system-monitor-settings-button{width:auto;min-width:82px;height:32px;padding:0 .75rem;font-size:.84rem;line-height:1;text-align:center;white-space:nowrap}"
	if !strings.Contains(string(css), settingsRule) {
		t.Error("workspace Settings must retain the shared text button geometry")
	}
	if strings.Contains(string(css), ".system-monitor-header .system-monitor-header-button.system-monitor-settings-button") || strings.Contains(string(css), ".system-monitor-header-button .gear-icon{") {
		t.Error("obsolete System Monitor-only Settings selector or gear styling")
	}
	for _, name := range []string{"system-monitor", "files", "terminal"} {
		attribute := "data-" + name + "-settings-toggle"
		if name == "system-monitor" {
			attribute = "data-system-monitor-profile-toggle"
		}
		button := regexp.MustCompile(`<button[^>]*` + attribute + `\b[^>]*>Settings</button>`).FindString(string(header))
		if !regexp.MustCompile(`class="[^"]*\bsystem-monitor-settings-button\b[^"]*"`).MatchString(button) {
			t.Errorf("%s Settings must use the shared text button class", name)
		}
	}
}
