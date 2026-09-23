package server

import (
	"strings"
	"testing"
)

func TestSystemUpdateControlCenterDialogUX(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	for _, want := range []string{
		"data-system-update-open",
		">System Update</strong>",
		"data-system-update-dialog",
		"data-system-update-close",
		"aria-label=\"Close system update\"",
		"data-system-update-running",
		"data-system-update-staged",
		"data-system-update-state",
		"data-system-update-button",
		"data-system-update-csrf",
		"data-system-update-reboot",
		"data-system-update-backup",
		"data-system-update-quick",
		"data-system-update-reboot-button",
		">Back up Minecraft before reboot</strong>",
		">Quick reboot</strong>",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("system update dialog markup missing %q", want)
		}
	}

	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(styles)
	for _, want := range []string{
		".system-update-dialog{width:min(560px,calc(100vw - 1.5rem))",
		".system-update-dialog::backdrop",
		"height:100dvh",
		"width:100vw",
		".system-update-deployment-grid",
		".system-update-close",
		".system-update-reboot-panel",
		".system-update-option",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("system update responsive styling missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, want := range []string{
		"fetch(\"/api/system-updates\"",
		"systemUpdateDialog.showModal()",
		"event.target === systemUpdateDialog",
		"systemUpdateDialog.close()",
		"Update staged — reboot required",
		"Updating…",
		"fetch(\"/api/system-updates/reboot\"",
		"Player warning: 10 seconds",
		"Confirm reboot",
		"Creating Minecraft backup…",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("system update behavior missing %q", want)
		}
	}
}
