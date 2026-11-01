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

func TestGatewayNavigationKeepsSwitchOutsideSurfaces(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	start := strings.Index(markup, `data-agent-gateway-dialog`)
	end := strings.Index(markup, `<template data-gateway-settings-template>`)
	if start < 0 || end <= start {
		t.Fatal("AI connections workspace missing")
	}
	workspace := markup[start:end]
	lane := regexp.MustCompile(`(?s)<nav class="minecraft-workspace-tabs gateway-navigation"[^>]*>(.*?)</nav>`).FindStringSubmatch(workspace)
	if len(lane) != 2 {
		t.Fatal("AI connections shared navigation lane missing")
	}
	buttons := regexp.MustCompile(`<button[^>]*>([^<]+)</button>`).FindAllStringSubmatch(lane[1], -1)
	want := []string{"Connections", "Add", "Help"}
	if len(buttons) != len(want) {
		t.Fatal("AI connections must have exactly three navigation tabs")
	}
	for i, label := range want {
		if buttons[i][1] != label {
			t.Errorf("tab %d: got %q, want %q", i, buttons[i][1], label)
		}
	}
	if !strings.Contains(lane[1], `data-gateway-connections aria-selected="true"`) || !strings.Contains(lane[1], `gateway-global-switch`) || !strings.Contains(lane[1], `data-gateway-enabled`) {
		t.Error("Connections must be the default tab with Gateway in the same lane")
	}
	if strings.Contains(workspace, `data-gateway-summary`) || strings.Contains(workspace, `gateway-summary`) {
		t.Error("Connections must begin directly with cards or the compact empty state")
	}
	if strings.Count(workspace, `data-gateway-help-guide`) != 1 || !strings.Contains(workspace, `data-gateway-help-providers`) {
		t.Error("Help must have a provider selector and one dynamic guide")
	}
	body := workspace[strings.Index(workspace, `</nav>`)+len(`</nav>`):]
	if strings.Contains(body, `data-gateway-enabled`) || strings.Count(workspace, `data-gateway-enabled`) != 1 {
		t.Error("Gateway must remain outside Connections, Add/Settings, and Help surfaces")
	}
	formEnd := strings.Index(markup[end:], `<dialog class="minecraft-delete-dialog" data-gateway-level4-dialog`)
	if formEnd < 0 {
		t.Fatal("AI connection settings template missing")
	}
	form := markup[end : end+formEnd]
	if strings.Contains(workspace+form, "Back to connections") || strings.Contains(form, "data-gateway-back") {
		t.Error("AI connections must use top navigation without back controls")
	}
}

func TestGatewaySettingsCompactComposition(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	match := regexp.MustCompile(`(?s)<template data-gateway-settings-template>(.*?)</template>`).FindStringSubmatch(markup)
	if len(match) != 2 {
		t.Fatal("connection settings template missing")
	}
	form := match[1]
	if strings.Count(form, `data-gateway-compatibility-summary`) != 1 || !regexp.MustCompile(`(?s)value="Automation / REST API".*?</label>\s*</div>\s*<p class="muted gateway-compatibility-summary" data-gateway-compatibility-summary aria-live="polite" hidden></p>`).MatchString(form) {
		t.Error("Add must have one compact neutral compatibility summary immediately below the provider cards")
	}
	for _, forbidden := range []string{"data-gateway-copy", "MCP address", "codex mcp", "claude mcp", "https://", "http://"} {
		if strings.Contains(form, forbidden) {
			t.Errorf("configuration template contains connection instructions: %s", forbidden)
		}
	}
	providers := regexp.MustCompile(`type="radio" name="purpose" value="([^"]+)"`).FindAllStringSubmatch(form, -1)
	wantProviders := []string{"OpenAI", "Claude", "Grok", "Gemini", "GitHub Copilot", "OpenClaw", "Other AI / MCP app", "Automation / REST API"}
	if len(providers) != len(wantProviders) {
		t.Fatal("connection settings must contain exactly the eight approved providers")
	}
	for i, want := range wantProviders {
		if providers[i][1] != want {
			t.Errorf("provider %d: got %q, want %q", i, providers[i][1], want)
		}
	}
	if strings.Count(form, `value="OpenClaw"`) != 1 || !strings.Contains(form, `/static/ai-providers/openclaw.svg`) || !strings.Contains(form, `<strong>OpenClaw</strong><small>Control UI &amp; CLI</small>`) {
		t.Error("OpenClaw must have one branded provider card with its local official icon and subtitle")
	}
	if !strings.Contains(form, `<strong>Claude</strong><small>Claude &amp; Claude Code</small>`) {
		t.Error("Claude must identify Claude and Claude Code without a separate provider record")
	}
	if !strings.Contains(form, `<strong>Gemini</strong><small>Gemini &amp; Gemini CLI</small>`) {
		t.Error("Gemini must identify Gemini and Gemini CLI without a separate provider record")
	}
	if !strings.Contains(form, `<strong>GitHub Copilot</strong><small>CLI &amp; VS Code</small>`) {
		t.Error("Copilot must identify both supported client modes")
	}
	if strings.Contains(form, "data-gateway-settings-title") || strings.Contains(form, "Choose servers") {
		t.Error("Add and Settings must omit redundant headings and the old scope choice")
	}
	if strings.Count(form, "data-gateway-all-servers") != 1 || !regexp.MustCompile(`(?s)class="gateway-server-lane" data-gateway-scope data-gateway-instance-options>\s*<label[^>]*><input[^>]*role="switch"[^>]*data-gateway-all-servers`).MatchString(form) {
		t.Error("All servers must be the first switch in the individual server lane")
	}
	previous := -1
	for _, marker := range []string{"gateway-name-access-row", "data-gateway-providers", "gateway-server-lane", "data-gateway-status hidden", "gateway-actions-lane", "data-gateway-live-note", "data-gateway-delete", "data-gateway-save", "data-gateway-advanced hidden"} {
		index := strings.Index(form, marker)
		if index <= previous {
			t.Errorf("missing or out-of-order settings element: %s", marker)
		}
		previous = index
	}
	if !regexp.MustCompile(`<label class="gateway-switch"><input[^>]*name="enabled"[^>]*aria-label="Connection enabled"[^>]*>`).MatchString(form) {
		t.Error("connection switch must have the accessible label Connection enabled")
	}
	if !regexp.MustCompile(`<span class="gateway-connection-state(?: [^"]*)?" data-gateway-connection-state>\s*Disable\s*</span>`).MatchString(form) {
		t.Error("connection switch must have a visible gateway-connection-state element with data-gateway-connection-state and initial text Disable")
	}
	if !regexp.MustCompile(`<div class="action-row gateway-save"><button[^>]*class="danger"[^>]*data-gateway-delete[^>]*>Delete</button><button type="submit" data-gateway-save>Create</button></div>`).MatchString(form) {
		t.Error("Delete must immediately precede the primary action in the shared action row")
	}
	if strings.Count(form, `name="expires_at"`) != 1 || !regexp.MustCompile(`(?s)data-gateway-status-details><input[^>]*type="datetime-local"[^>]*name="expires_at"[^>]*aria-label="Expiration"`).MatchString(form) {
		t.Error("expiration must remain one accessible editable control inside the metrics lane")
	}
	if strings.Contains(form[strings.Index(form, `<details data-gateway-advanced`):], `name="expires_at"`) {
		t.Error("expiration must not be in Advanced")
	}
	if !regexp.MustCompile(`(?s)<details data-gateway-advanced hidden>.*</details>\s*</form>\s*$`).MatchString(form) {
		t.Error("Advanced details must be collapsed and last")
	}
}

func TestGatewayHelpReferenceSectionsStayCompact(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	start := strings.Index(markup, `<section data-gateway-help hidden>`)
	end := strings.Index(markup, `<template data-gateway-settings-template>`)
	if start < 0 || end <= start {
		t.Fatal("connection Help missing")
	}
	help := markup[start:end]
	reference := strings.Index(help, `<details class="panel" data-gateway-provider-reference>`)
	if reference < 0 {
		t.Fatal("collapsed provider reference missing")
	}
	normal := help[:reference]
	for _, phrase := range []string{"Codex, Claude Code, GitHub Copilot CLI, VS Code, OpenClaw and other local MCP clients", "when they can reach the server", "ChatGPT, Claude remote connectors, Grok and Gemini connect from outside your LAN", "See Provider reference for the exact connection options for each provider."} {
		if !strings.Contains(normal, phrase) {
			t.Errorf("Network access guidance missing: %s", phrase)
		}
	}
	for _, title := range []string{"Access levels", "Minecraft scope", "Authentication", "Network access"} {
		if !strings.Contains(normal, "<h3>"+title+"</h3>") {
			t.Errorf("visible reference section missing: %s", title)
		}
	}
	for _, title := range []string{"Minecraft scope", "Authentication", "Network access"} {
		sectionStart := strings.Index(normal, "<section class=\"panel\"><h3>"+title+"</h3>")
		if sectionStart < 0 {
			t.Fatalf("Help fact section missing: %s", title)
		}
		sectionEnd := strings.Index(normal[sectionStart:], "</section>")
		if sectionEnd < 0 {
			t.Fatalf("Help fact section not closed: %s", title)
		}
		section := normal[sectionStart : sectionStart+sectionEnd]
		if strings.Count(section, `class="gateway-help-facts"`) != 1 || strings.Count(section, `class="gateway-help-fact"`) != 2 {
			t.Errorf("Help section must share the two-fact layout: %s", title)
		}
		if regexp.MustCompile(`<(?:dl|dt|dd)\b`).MatchString(section) || len(regexp.MustCompile(`(?s)<div class="gateway-help-fact">\s*<strong>[^<]+</strong>\s*<p>[^<]+</p>\s*</div>`).FindAllString(section, -1)) != 2 {
			t.Errorf("Help facts must put each label above its description: %s", title)
		}
	}
	for _, duplicate := range []string{"Secure MCP Tunnel", "Anthropic infrastructure", "public Internet", "public HTTPS Streamable HTTP"} {
		if strings.Contains(normal, duplicate) {
			t.Errorf("generic Help duplicates provider mechanics: %s", duplicate)
		}
	}
	for _, obsolete := range []string{"Getting connected", "Choose your app", "data-gateway-help-providers", "data-gateway-help-guide"} {
		if strings.Contains(normal, obsolete) {
			t.Errorf("default Help contains onboarding: %s", obsolete)
		}
	}
	if !strings.Contains(help[reference:], `data-gateway-help-guide`) || !strings.Contains(help, `<details class="panel"><summary>Developer integration</summary>`) || regexp.MustCompile(`<details[^>]*\bopen\b`).MatchString(help) {
		t.Error("provider manuals and developer integration must remain available in collapsed details")
	}
}
