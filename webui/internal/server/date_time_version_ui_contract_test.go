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
	for _, want := range []string{"Paper", "Purpur", "Vanilla", "Coming soon", "Installed", "Available", "Recommended", "Latest", "Specific version", "Geyser/Floodgate", "/api/minecraft/workspace/settings/plan", "/api/minecraft/workspace/settings/apply", "compatibility_blocked"} {
		if !strings.Contains(version, want) {
			t.Fatalf("Version missing %q", want)
		}
	}
	for _, want := range []string{
		`recommended: "Recommended", latest: "Latest", pinned: "Specific version"`,
		`includes(minecraft.version_mode) ? minecraft.version_mode : "recommended"`,
		`line("Installed", status.installed || "Not detected")`,
		`line("Newest available", status.available ?`,
		`line("Configured", status.configured_version === "LATEST" ?`,
		`line("Version policy", policyLabel(minecraft.version_mode))`,
		`form.querySelector("[data-version-specific]").hidden = policy !== "pinned"`,
		`form.elements.version.required = policy === "pinned"`,
		`line(policy === "latest" ? "Will install now" : "Will install", candidate.selected_candidate)`,
	} {
		if !strings.Contains(version, want) {
			t.Fatalf("Version policy contract missing %q", want)
		}
	}
	if strings.Contains(version, `minecraft.version_mode === "latest" ? "latest" : "pinned"`) {
		t.Fatal("Recommended policy initializes as Specific version")
	}
	css := read("static/app.css")
	if !strings.Contains(css, ".version-workspace-dialog{position:absolute") || !strings.Contains(css, "max-height:none") || strings.Contains(css, ".version-workspace-body{overflow:auto") {
		t.Fatal("Version content must use natural height")
	}
	app := read("static/app.js")
	if strings.Contains(app, `<label>Timezone<input name="timezone"`) {
		t.Fatal("Minecraft Players still edits timezone")
	}
	if !strings.Contains(app, "data-crossplay-compatibility") {
		t.Fatal("Cross-play compatibility missing")
	}
}
