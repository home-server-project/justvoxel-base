package server

import (
	"strings"
	"testing"
)

func TestFirstRunConfiguredStateOverridesExploreInvitation(t *testing.T) {
	scriptSource, err := assets.ReadFile("static/first-run.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptSource)
	configured := strings.Index(script, "if (configured === true)")
	explored := strings.Index(script, "let explored = false")
	if configured < 0 || explored < 0 || configured > explored {
		t.Fatalf("configured state must be handled before Explore-first preference")
	}
	for _, want := range []string{
		"hideFirstRunUI()",
		"window.localStorage.removeItem(exploreStorageKey)",
		"stopRefreshTimer()",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("configured first-run cleanup missing %q", want)
		}
	}

	templateSource, err := assets.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(templateSource), `data-dashboard-setup-area {{if .Status.Minecraft.Configured}}hidden{{end}}`) {
		t.Fatal("Workspace setup invitation must follow the configured state")
	}
}

func TestFirstRunWorkspaceChoiceIsNonBlockingAndConfigurationDriven(t *testing.T) {
	templateSource, err := assets.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	templateText := string(templateSource)
	for _, want := range []string{
		"data-first-run-choice",
		"data-first-run-explore",
		"data-first-run-setup",
		"data-minecraft-setup-invitation",
		"href=\"/setup\"",
	} {
		if !strings.Contains(templateText, want) {
			t.Fatalf("dashboard first-run markup missing %q", want)
		}
	}

	scriptSource, err := assets.ReadFile("static/first-run.js")
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(scriptSource)
	if strings.Contains(scriptText, "window.location.replace(\"/setup\")") {
		t.Fatal("first-run flow must not force unconfigured administrators into setup")
	}
	for _, want := range []string{
		"role !== \"administrator\"",
		"configured === true",
		"window.localStorage.setItem(exploreStorageKey, \"true\")",
		"window.localStorage.removeItem(exploreStorageKey)",
		"showChoice()",
		"showInvitation()",
		"15000",
	} {
		if !strings.Contains(scriptText, want) {
			t.Fatalf("first-run behavior missing %q", want)
		}
	}

	styleSource, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styleText := string(styleSource)
	for _, want := range []string{
		".first-run-choice",
		".minecraft-setup-invitation",
		"@keyframes setup-invite-shimmer",
		"@media(prefers-reduced-motion:reduce)",
	} {
		if !strings.Contains(styleText, want) {
			t.Fatalf("first-run styling missing %q", want)
		}
	}
}

func TestMinecraftResetReopensExistingFirstRunChoice(t *testing.T) {
	appSource, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appSource)
	start := strings.Index(app, "const renderResetOperation = (operation) => {")
	if start < 0 {
		t.Fatal("reset operation renderer missing")
	}
	end := strings.Index(app[start:], "const resetCurrentOperation = async () => {")
	if end < 0 {
		t.Fatal("reset operation renderer missing")
	}
	reset := app[start : start+end]
	for _, want := range []string{
		`operation.operation_type === "minecraft_reset" && operation.state === "succeeded"`,
		"workspaceWindow?.close()",
		`window.dispatchEvent(new Event("justvoxel:minecraft-reset-complete"))`,
	} {
		if !strings.Contains(reset, want) {
			t.Fatalf("Minecraft Reset handoff missing %q", want)
		}
	}
	if strings.Index(reset, "workspaceWindow?.close()") > strings.Index(reset, `window.dispatchEvent(new Event("justvoxel:minecraft-reset-complete"))`) {
		t.Fatal("Reset workspace must close before the Welcome choice reopens")
	}
	if strings.Contains(reset, "window.location.reload()") || strings.Contains(reset, "window.location.assign(") {
		t.Fatal("Minecraft Reset handoff must retain the current page and session")
	}
	if !strings.Contains(reset, `if (operation.operation_type === "factory_reset")`) ||
		!strings.Contains(reset, "this WebUI session will be signed out") {
		t.Fatal("Full Factory Reset session warning must remain separate")
	}
	if !strings.Contains(app, `window.location.assign(target.pathname + target.search)`) ||
		!strings.Contains(app, `window.location.assign("/login")`) {
		t.Fatal("Full Factory Reset authentication redirects must remain available")
	}
	if !strings.Contains(app, `window.addEventListener("justvoxel:minecraft-reset-complete", () => void refreshDashboard())`) {
		t.Fatal("Minecraft Reset must refresh the dashboard without waiting for its poll")
	}

	firstRunSource, err := assets.ReadFile("static/first-run.js")
	if err != nil {
		t.Fatal(err)
	}
	firstRun := string(firstRunSource)
	listener := strings.Index(firstRun, `window.addEventListener("justvoxel:minecraft-reset-complete", () => {`)
	if listener < 0 {
		t.Fatal("first-run UI does not listen for Minecraft Reset completion")
	}
	handoff := firstRun[listener:]
	for _, want := range []string{
		"window.localStorage.removeItem(exploreStorageKey)",
		"resetWelcome = true",
		"void loadConfigurationState()",
	} {
		if !strings.Contains(handoff, want) {
			t.Fatalf("first-run Reset handoff missing %q", want)
		}
	}
	if !strings.Contains(firstRun, `const exploreStorageKey = "justvoxel:first-run:explore"`) ||
		!strings.Contains(firstRun, `(!resetWelcome && new URLSearchParams(window.location.search).has("workspace"))`) ||
		!strings.Contains(firstRun, "else showChoice()") ||
		!strings.Contains(firstRun, "configured = Boolean(snapshot?.status?.minecraft?.configured)") {
		t.Fatal("Minecraft Reset must reopen the existing backend-driven Welcome choice")
	}
	styleSource, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(styleSource), ".first-run-choice{position:fixed;inset:0;z-index:950;") {
		t.Fatal("Welcome choice must appear over remaining workspace windows")
	}
}
