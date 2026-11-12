package server

import (
	"regexp"
	"strconv"
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
	if !strings.Contains(string(networkCSS), `.network-workspace-header .system-monitor-header-button:not(.workspace-header-refresh){width:31px;height:31px;font-size:1.08rem}`) || strings.Contains(string(networkCSS), `.network-workspace-header .system-monitor-header-button{`) {
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
	settingsRule := ".workspace-window .system-monitor-header-button.system-monitor-settings-button{width:auto;min-width:82px;height:31px;min-height:31px;padding:0 .75rem;border-radius:10px;font-size:.84rem;line-height:1;text-align:center;white-space:nowrap}"
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

func TestWorkspaceHeaderGeometryAndGreenRefresh(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	css := read("static/app.css")
	network := read("static/network-workspace.css")
	rules := regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1)
	shared := ""
	actions := ""
	for _, rule := range rules {
		if strings.HasPrefix(strings.TrimSpace(rule[1]), ".workspace-window :is(.system-update-dialog-header,") && strings.Contains(rule[2], "height:36px") {
			shared = rule[0]
		}
		if strings.HasPrefix(strings.TrimSpace(rule[1]), ".workspace-window :is(.system-update-header-actions,") {
			actions = rule[0]
		}
		if shared != "" && regexp.MustCompile(`\.(?:[a-z-]+-workspace-header|system-monitor-header|system-update-dialog-header)\b`).MatchString(rule[1]) && !strings.Contains(rule[1], "-header-button") && regexp.MustCompile(`(?:^|;)\s*(?:min-)?height\s*:\s*(?:40|48|52)px`).MatchString(rule[2]) {
			t.Errorf("later rule re-expands the shared workspace header: %s", rule[0])
		}
		if (strings.Contains(rule[1], "workspace-header") || strings.Contains(rule[1], "system-monitor-header") || strings.Contains(rule[1], "system-update-dialog-header")) && !strings.Contains(rule[1], "-header-button") && regexp.MustCompile(`min-height:52px`).MatchString(rule[2]) {
			t.Errorf("obsolete workspace header geometry: %s", rule[0])
		}
	}
	for _, name := range []string{"network-workspace-header", "system-workspace-header", "minecraft-workspace-header", "plugins-workspace-header", "version-workspace-header", "system-monitor-header", "storage-workspace-header", "backups-workspace-header", "migration-workspace-header", "system-update-dialog-header"} {
		if !strings.Contains(shared, "."+name) {
			t.Errorf("%s does not share the workspace lane", name)
		}
	}
	for _, declaration := range []string{"height:36px;min-height:36px", "flex:none", "align-items:center", "min-width:0", "flex-wrap:nowrap", "padding:0 var(--jv-workspace-edge)", "border-radius:calc(var(--jv-radius-window) - 1px) calc(var(--jv-radius-window) - 1px) 0 0"} {
		if !strings.Contains(shared, declaration) {
			t.Errorf("shared workspace header missing %s", declaration)
		}
	}
	if strings.Contains(shared, "!important") {
		t.Error("workspace geometry must use the normal cascade")
	}
	for _, name := range []string{"system-update-header-actions", "system-monitor-header-actions", "storage-workspace-header-actions", "backups-workspace-header-actions", "minecraft-workspace-header-actions", "system-workspace-header-actions", "migration-workspace-header-actions", "network-workspace-header-actions", "selected-server-header-actions"} {
		if !strings.Contains(actions, "."+name) {
			t.Errorf("%s must share the contained action lane", name)
		}
	}
	for _, declaration := range []string{"display:flex", "align-items:center", "gap:var(--jv-space-xs)", "flex:none", "flex-wrap:nowrap"} {
		if !strings.Contains(actions, declaration) {
			t.Errorf("shared workspace action lane missing %s", declaration)
		}
	}
	for _, forbidden := range []string{"margin-right:calc(0px - var(--jv-workspace-edge) - 1px)", "flex:1 0 auto"} {
		if strings.Contains(css, forbidden) {
			t.Errorf("obsolete shared header geometry: %s", forbidden)
		}
	}
	if !strings.Contains(css, "--jv-radius-window:12px;") || strings.Contains(css, "--jv-radius-window:16px;") {
		t.Error("workspace windows must share the 12px outer radius")
	}
	networkRule := regexp.MustCompile(`\.network-workspace-header\{([^}]*)\}`).FindStringSubmatch(network)
	if len(networkRule) != 2 || !strings.Contains(networkRule[1], "height:40px;min-height:40px;padding:3px") || strings.Contains(networkRule[1], "48px") {
		t.Fatal("Network stylesheet must preserve its original header contract; app.css owns the live 36px lane")
	}
	for _, name := range []string{"plugins", "version"} {
		rule := regexp.MustCompile(`\.` + name + `-workspace-header\{([^}]*)\}`).FindStringSubmatch(css)
		if len(rule) != 2 || strings.Contains(rule[1], "height:") || strings.Contains(rule[1], "padding:") {
			t.Errorf("%s must inherit shared lane geometry", name)
		}
	}
	for _, want := range []string{
		`.app-header.topbar{height:32px;min-height:32px;`,
		`.workspace-window :is(.workspace-header-refresh,.workspace-header-close){height:31px;min-height:31px;border:1px solid #35414b;border-radius:10px;background:#171d22;color:#dce7e0;flex:none}`,
		`.workspace-window .workspace-header-close{width:31px;padding:0;font-size:inherit}`,
		`.workspace-window .network-workspace-header .system-monitor-header-button.workspace-header-close{width:31px;height:31px;font-size:inherit}`,
		`.workspace-window .workspace-header-refresh{width:auto;min-width:78px;padding:0 .7rem;font-size:.84rem}`,
		`.workspace-window :is(.workspace-header-refresh,.workspace-header-close):is(:hover,:focus-visible){background:#1d2a23;border-color:#4f8b69;color:#c8f2d7}`,
		`.workspace-window .server-selector>summary{height:31px;min-height:31px;padding:0 .55rem;border:1px solid #35414b;border-radius:10px;background:#171d22;color:#dce7e0;font-size:.84rem;`,
		`.workspace-window .server-selector>summary:is(:hover,:focus-visible),.workspace-window .server-selector[open]>summary{background:#1d2a23;border-color:#4f8b69;color:#c8f2d7}`,
		`[data-server-selector-compact] .server-selector>summary{padding:.15rem .3rem;font-size:.75rem}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("shared header/control contract missing %s", want)
		}
	}
	if !strings.Contains(css, "*{box-sizing:border-box}") {
		t.Fatal("workspace lane geometry requires border-box sizing")
	}
	pixels := func(source, pattern string) int {
		t.Helper()
		match := regexp.MustCompile(pattern).FindStringSubmatch(source)
		if len(match) != 2 {
			t.Fatalf("missing geometry: %s", pattern)
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	headerHeight := pixels(shared, `height:(\d+)px;`)
	divider := pixels(css, `\.minecraft-workspace-header\{[^}]*border-bottom:(\d+)px solid`)
	usable := headerHeight - divider
	if headerHeight != 36 || divider != 1 || usable != 35 {
		t.Fatalf("workspace border-box lane: outer=%d divider=%d usable=%d", headerHeight, divider, usable)
	}
	for _, selector := range []string{
		".workspace-window :is(.workspace-header-refresh,.workspace-header-close)",
		".workspace-window .server-selector>summary",
		".workspace-window .system-monitor-header-button.system-monitor-settings-button",
	} {
		controlHeight := pixels(css, regexp.QuoteMeta(selector)+`\{[^}]*\bheight:(\d+)px;`)
		gap := usable - controlHeight
		if controlHeight != 31 || gap != 4 || gap%2 != 0 || gap/2 != 2 {
			t.Errorf("%s must leave exactly 2px above and below: height=%d total gap=%d", selector, controlHeight, gap)
		}
	}
	for _, selector := range []string{".workspace-window :is(.workspace-header-refresh,.workspace-header-close)", ".workspace-window .server-selector>summary"} {
		if !strings.Contains(css, selector+"{background:#fff;border-color:#cbd4da;color:#34414a}") {
			t.Errorf("light neutral shell missing for %s", selector)
		}
	}
	for _, want := range []string{
		`.workspace-window :is(.workspace-header-refresh,.workspace-header-close):is(:hover,:focus-visible){background:#eef6f1;border-color:#86b99a;color:#184c31}`,
		`.workspace-window .server-selector>summary:is(:hover,:focus-visible),.workspace-window .server-selector[open]>summary{background:#eef6f1;border-color:#86b99a;color:#184c31}`,
		`.server-selector>summary,.server-selector-option{display:flex;align-items:center;gap:.4rem;min-width:0;padding:.4rem .55rem;border:1px solid #303942;border-radius:7px;background:#171d22;color:inherit;font-size:.85rem;line-height:1.2;cursor:pointer;list-style:none}`,
		`.server-selector-option{width:100%;margin:0;text-align:left;border-color:transparent;background:transparent}`,
		`.server-selector-option:hover,.server-selector-option[aria-pressed="true"]{background:#25382e;border-color:#4f8b69}`,
		`.server-selector-menu{position:fixed;inset:auto;margin:0;z-index:170;max-width:calc(100vw - 8px);max-height:240px;color:#eef1f4;overflow:auto;padding:.25rem;border:1px solid #303942;border-radius:8px;background:#171d22;box-shadow:0 12px 28px #0007}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("light state or unchanged selector menu contract missing %s", want)
		}
	}
	for _, want := range []string{
		`.workspace-window .workspace-header-refresh{border-color:#4f8b69;background:#25382e;color:#c8f2d7}`,
		`.workspace-window .workspace-header-refresh:is(:hover,:focus-visible){border-color:#78c99d;background:#2f4a39;color:#fff}`,
		`.workspace-window .workspace-header-refresh{border-color:#86b99a;background:#e3f1e8;color:#184c31}`,
		`.workspace-window .workspace-header-refresh:is(:hover,:focus-visible){border-color:#4f8b69;background:#d1e9da;color:#184c31}`,
		`.server-selector{position:relative;width:max-content;min-width:8rem;max-width:100%;`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("normal green header control contract missing %s", want)
		}
	}
	for _, rule := range rules {
		if strings.Contains(rule[1], "workspace-header-refresh") || strings.Contains(rule[1], "workspace-header-close") || strings.Contains(rule[1], ".workspace-window .server-selector>summary") {
			if regexp.MustCompile(`(?:^|;)\s*(?:min-|max-)?(?:height|width)\s*:\s*(?:40|48)px`).MatchString(rule[2]) {
				t.Errorf("oversized workspace control regression: %s", rule[0])
			}
		}
	}
}

func TestControlCenterTriggerMeetsTopbarEdges(t *testing.T) {
	data, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	for _, want := range []string{
		`.topbar{display:grid;grid-template-columns:1fr auto 1fr;align-items:center;`,
		`.app-header.topbar{height:32px;min-height:32px;`,
		`@media(min-width:601px){.app-header .control-center{height:32px;margin-top:-1px;margin-bottom:-1px;margin-right:calc(-.5rem - 1px)}}`,
		`.app-header .control-center-trigger{min-height:32px;height:32px;`,
		`.app-header .control-center-trigger{width:32px;padding:0}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("attached Control Center geometry missing %s", want)
		}
	}
	if !strings.Contains(css, "*{box-sizing:border-box}") {
		t.Fatal("Control Center geometry requires border-box sizing")
	}
	pixels := func(pattern string) int {
		t.Helper()
		match := regexp.MustCompile(pattern).FindStringSubmatch(css)
		if len(match) != 2 {
			t.Fatalf("missing geometry: %s", pattern)
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	topbarHeight := pixels(`\.app-header\.topbar\{height:(32)px;`)
	borderTop := pixels(`\.app-header\.topbar\{height:32px;[^}]*border:(\d+)px solid`)
	borderBottom := borderTop // The border shorthand applies to both edges.
	if borderTop != 1 || borderBottom != 1 {
		t.Fatal("topbar must retain 1px top and bottom borders")
	}
	track := topbarHeight - borderTop - borderBottom
	wrapper := pixels(`@media\(min-width:601px\)\{\.app-header \.control-center\{height:(\d+)px;`)
	marginTop := pixels(`@media\(min-width:601px\)\{\.app-header \.control-center\{[^}]*margin-top:(-?\d+)px;`)
	marginBottom := pixels(`@media\(min-width:601px\)\{\.app-header \.control-center\{[^}]*margin-bottom:(-?\d+)px;`)
	occupied := wrapper + marginTop + marginBottom
	if track != 30 || wrapper != 32 || marginTop != -1 || marginBottom != -1 || occupied != 30 || occupied != track {
		t.Fatalf("Control Center must occupy the 30px track: track=%d wrapper=%d margins=%d,%d occupied=%d", track, wrapper, marginTop, marginBottom, occupied)
	}
	if borderTop+(track-occupied)/2+marginTop != 0 || borderTop+(track-occupied)/2+marginTop+wrapper != topbarHeight {
		t.Error("Control Center border box must meet both outer topbar edges")
	}
	for _, rule := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		if !strings.Contains(rule[1], ".app-header .control-center-trigger") && !strings.Contains(rule[1], ".app-header .control-center") {
			continue
		}
		if regexp.MustCompile(`\btop\s*:\s*-4px`).MatchString(rule[2]) {
			t.Errorf("Control Center must not be shifted outside its anchor: %s", rule[0])
		}
		for _, height := range regexp.MustCompile(`(?:^|;)\s*(?:min-|max-)?height\s*:\s*(\d+)px`).FindAllStringSubmatch(rule[2], -1) {
			if height[1] != "32" {
				t.Errorf("Control Center height must stay 32px: %s", rule[0])
			}
		}
	}
}

func TestWorkspaceHeadersShareControlCenterTypography(t *testing.T) {
	data, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	headers := []string{
		"system-update-dialog-header", "system-monitor-header", "storage-workspace-header",
		"backups-workspace-header", "minecraft-workspace-header", "system-workspace-header",
		"migration-workspace-header", "version-workspace-header", "plugins-workspace-header",
		"network-workspace-header", "control-center-heading",
	}
	contracts := []struct {
		suffix string
		body   string
	}{
		{">div:first-child", "display:flex;align-items:baseline;gap:.32rem;min-width:0"},
		{">div:first-child .eyebrow", "margin:0;white-space:nowrap;color:#78c99d;font-family:inherit;font-size:.68rem;font-weight:400;letter-spacing:.12em;text-transform:uppercase;line-height:normal"},
		{">div:first-child .eyebrow::after", `content:" ·"`},
		{">div:first-child h2", "margin:0;font-family:inherit;font-size:.98rem;font-weight:700;line-height:normal;color:var(--jv-text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis"},
	}
	for _, contract := range contracts {
		var selectors []string
		for _, header := range headers {
			selector := "." + header + contract.suffix
			if header == "network-workspace-header" && contract.suffix == ">div:first-child" {
				// Outrank the same gap selector in Network's later stylesheet.
				selector = ".workspace-window " + selector
			}
			selectors = append(selectors, selector)
		}
		if !strings.Contains(css, strings.Join(selectors, ",")+"{"+contract.body+"}") {
			t.Errorf("all workspace families and Control Center must share title contract %s", contract.suffix)
		}
	}
	if !strings.Contains(css, `.control-center-heading{display:flex;align-items:center;justify-content:space-between;height:36px;min-height:36px;margin:-.8rem -.8rem .8rem;padding:0 var(--jv-workspace-edge);border-bottom:1px solid var(--jv-border);border-radius:13px 13px 0 0}`) {
		t.Error("Control Center title lane must share the 36px workspace header rhythm with no vertical padding")
	}

	// The shared first-child descendants have greater specificity than the old
	// family-only rules, including Network's dynamically loaded stylesheet.
	// Keep historical sizes before the shared contract, but reject new competing
	// title sizes or important overrides in any workspace family.
	shared := strings.Index(css, ".system-update-dialog-header>div:first-child h2,")
	if shared < 0 {
		t.Fatal("shared authoritative workspace title rule missing")
	}
	network, err := assets.ReadFile("static/network-workspace.css")
	if err != nil {
		t.Fatal(err)
	}
	rulePattern := regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	sizePattern := regexp.MustCompile(`(?:^|;)\s*font-size\s*:\s*([^;]+)`)
	for _, source := range []struct {
		name string
		css  string
	}{
		{"later app.css", css[shared:]},
		{"network-workspace.css", string(network)},
	} {
		for _, rule := range rulePattern.FindAllStringSubmatch(source.css, -1) {
			for _, header := range headers {
				if !strings.Contains(rule[1], "."+header) || (!strings.Contains(rule[1], "h2") && !strings.Contains(rule[1], ".eyebrow")) {
					continue
				}
				if strings.Contains(rule[2], "!important") {
					t.Errorf("%s must not override shared title typography: %s", source.name, rule[0])
				}
				if strings.Contains(rule[1], "h2") {
					if size := sizePattern.FindStringSubmatch(rule[2]); len(size) == 2 && size[1] != ".98rem" && !(source.name == "network-workspace.css" && strings.TrimSpace(rule[1]) == ".network-workspace-header h2" && size[1] == "1.05rem") {
						t.Errorf("%s retains an independent workspace title size: %s", source.name, rule[0])
					}
				}
			}
		}
	}
}
