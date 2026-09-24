package server

import (
	"strings"
	"testing"
)

func TestSystemUpdateWorkspaceUX(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	for _, want := range []string{
		"data-system-update-open",
		">System Update</strong>",
		`data-workspace-window="system-update"`,
		"data-system-update-dialog",
		"data-system-update-refresh",
		"data-system-update-close",
		`aria-label="Close system update"`,
		"data-system-update-running",
		"data-system-update-staged",
		"data-system-update-available",
		"data-system-update-state",
		"data-system-update-button",
		">Download &amp; stage</button>",
		"data-system-update-csrf",
		"data-system-update-reboot",
		"data-system-update-reboot-title",
		"data-system-update-backup",
		"data-system-update-quick",
		"data-system-update-reboot-button",
		">Back up Minecraft before reboot</strong>",
		">Quick reboot</strong>",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("system update workspace markup missing %q", want)
		}
	}

	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(styles)
	for _, want := range []string{
		".system-update-dialog{width:min(620px,calc(100vw - 1rem))",
		"resize:both",
		".system-update-dialog-shell{display:flex;flex-direction:column;height:100%",
		".system-update-dialog-header{display:flex",
		"cursor:move",
		".system-update-deployment-grid",
		"grid-template-columns:repeat(auto-fit,minmax(155px,1fr))",
		".system-update-reboot-panel",
		".system-update-option",
		"@media(max-width:700px)",
		"resize:none",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("system update workspace styling missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, want := range []string{
		"const initSystemUpdateWorkspace = () =>",
		"fetch(\"/api/system-updates\"",
		"fetch(\"/api/system-updates/check\"",
		"setupWorkspaceWindow(systemUpdateDialog",
		"onOpen: () =>",
		"checkSystemUpdate();",
		`status.check_state === "update_available"`,
		`latestStatus?.check_state === "update_available"`,
		"await applySystemUpdate(true)",
		`updateButton.textContent = "Download & stage"`,
		"fetch(\"/api/system-updates/reboot\"",
		"Player warning: 10 seconds",
		"Confirm reboot",
		"Creating Minecraft backup…",
		"refreshButton?.addEventListener(\"click\", checkSystemUpdate)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("system update workspace behavior missing %q", want)
		}
	}
	if strings.Contains(js, "systemUpdateDialog.showModal()") {
		t.Fatal("System Update still opens as a modal instead of a workspace window")
	}
}
