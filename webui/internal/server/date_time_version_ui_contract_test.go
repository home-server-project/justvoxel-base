package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDateTimeAndVersionWorkspaceContracts(t *testing.T) {
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	header := read("templates/header.html")
	for _, want := range []string{
		`data-date-time-open`, `data-date-time-dialog`, `data-timezone-search`, `data-date-time-automatic`,
		`data-date-time-manual`, `data-version-open`, `data-version-workspace-dialog`,
		`data-version-tab="software"`, `data-version-tab="minecraft"`, `Minecraft Settings`,
	} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q", want)
		}
	}
	if strings.Contains(header, `data-minecraft-tab="version"`) {
		t.Fatal("Version remains inside Minecraft Settings")
	}
	search := read("static/timezone-search.js")
	setup := read("static/settings.js")
	date := read("static/date-time.js")
	if !strings.Contains(search, "scoreZone") || !strings.Contains(setup, "initializeJustVoxelTimezoneSearch") || !strings.Contains(date, "initializeJustVoxelTimezoneSearch") {
		t.Fatal("Setup and Date & Time do not share the city search")
	}
	for _, want := range []string{"date.disabled = automatic.checked", "time.disabled = automatic.checked", "/api/system/workspace/date-time", "csrf"} {
		if !strings.Contains(date, want) {
			t.Fatalf("Date & Time missing %q", want)
		}
	}
	version := read("static/version-workspace.js")
	for _, want := range []string{"Paper", "Purpur", "Vanilla", "Reset Minecraft", "Installed", "Available", "Recommended", "Latest", "Specific version", "Geyser/Floodgate", "/api/minecraft/workspace/settings/plan", "/api/minecraft/workspace/settings/apply", "compatibility_blocked"} {
		if !strings.Contains(version, want) {
			t.Fatalf("Version missing %q", want)
		}
	}
	for _, want := range []string{
		`recommended: "Recommended", latest: "Latest", pinned: "Specific version"`,
		`card("Version")`,
		`<select name="policy" aria-label="Version">`,
		`await settleSelectChange()`,
		`setTimeout(resolve, 0)`,
		`previewTicket !== previewSequence`,
		`includes(minecraft.version_mode) ? minecraft.version_mode : "recommended"`,
		`line("Installed", status.installed || "Not detected")`,
		`line("Newest available", status.available ?`,
		`line("Configured", status.configured_version === "LATEST" ?`,
		`line("Version", policyLabel(minecraft.version_mode))`,
		`form.querySelector("[data-version-specific]").hidden = policy !== "pinned"`,
		`form.elements.version.required = policy === "pinned"`,
		`line(policy === "latest" ? "Will install now" : "Will install", candidate.selected_candidate)`,
	} {
		if !strings.Contains(version, want) {
			t.Fatalf("Version policy contract missing %q", want)
		}
	}
	for _, forbidden := range []string{`card("Version policy")`, `<label>Policy`, `line("Version policy",`, `Version mode`, `waitForSelectBlur`} {
		if strings.Contains(version, forbidden) {
			t.Fatalf("redundant Version label: %q", forbidden)
		}
	}
	if strings.Contains(version, `minecraft.version_mode === "latest" ? "latest" : "pinned"`) {
		t.Fatal("Recommended policy initializes as Specific version")
	}
	css := read("static/app.css")
	for _, want := range []string{
		`[hidden]{display:none!important}`,
		`.version-workspace-dialog{position:absolute`,
		`height:min(720px,calc(100dvh - 76px))`,
		`max-height:calc(100dvh - 76px)`,
		`.version-workspace-shell{display:flex;flex-direction:column;height:100%;min-height:0`,
		`.version-workspace-header{display:flex;flex:none`,
		`.version-workspace-tabs{display:flex;flex:none`,
		`.version-workspace-body{flex:1;min-height:0;min-width:0;overflow-y:auto;overflow-x:hidden`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("bounded Version workspace missing %q", want)
		}
	}
	versionDialogCSS := strings.SplitN(strings.SplitN(css, ".version-workspace-dialog{", 2)[1], "}", 2)[0]
	if strings.Contains(versionDialogCSS, "height:auto") || strings.Contains(versionDialogCSS, "max-height:none") || strings.Contains(versionDialogCSS, "overflow:visible") {
		t.Fatal("Version workspace must keep bounded outer geometry")
	}
	app := read("static/app.js")
	if strings.Contains(app, `id !== "migration" && id !== "version"`) || !strings.Contains(app, `if (Number.isFinite(saved.height) && saved.height > 0) element.style.height = saved.height + "px";`) {
		t.Fatal("Version must restore its saved height when switching tabs")
	}
	if !strings.Contains(version, `form.querySelector("[data-version-custom]").hidden = channel.value !== "custom"`) {
		t.Fatal("Version custom tag must follow channel visibility")
	}
	if !strings.Contains(version, `tabs.forEach((tab) => tab.addEventListener("click", () => { currentTab = tab.dataset.versionTab;`) || strings.Contains(version, `versionDialog.style.height`) {
		t.Fatal("Version tab switching must keep the workspace height")
	}
	if strings.Contains(app, `<label>Timezone<input name="timezone"`) {
		t.Fatal("Minecraft Players still edits timezone")
	}
	if !strings.Contains(app, "data-crossplay-compatibility") {
		t.Fatal("Cross-play compatibility missing")
	}
	if !strings.Contains(app, `Geyser/Floodgate compatible with ${status.geyser_supported_version}.`) {
		t.Fatal("Cross-play compatibility must use the Geyser-supported Java version")
	}
	if strings.Contains(app, `Geyser/Floodgate compatible with ${status.available}.`) {
		t.Fatal("Cross-play compatibility must not use the newest available Minecraft version")
	}
}
