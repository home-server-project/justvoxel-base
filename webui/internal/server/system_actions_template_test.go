package server

import (
	"strings"
	"testing"
)

func TestSystemPowerHeaderAndDialogUX(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	for _, want := range []string{
		"data-system-power",
		"data-control-center",
		"control-power-actions",
		"control-icon-button",
		"data-system-action=\"reboot\"",
		"aria-label=\"Reboot\"",
		"data-tooltip=\"Reboot\"",
		"data-system-action=\"poweroff\"",
		"aria-label=\"Power off\"",
		"data-tooltip=\"Power off\"",
		"data-system-action=\"firmware-reboot\"",
		"aria-label=\"Reboot to UEFI/BIOS\"",
		"data-tooltip=\"Reboot to UEFI/BIOS\"",
		"aria-label=\"Log out\"",
		"data-tooltip=\"Log out\"",
		"control-action-icon-firmware",
		"data-system-action-dialog",
		"data-system-action-confirm",
		"data-system-actions-csrf",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("system power header missing %q", want)
		}
	}

	if !strings.Contains(markup, `<rect x="8" y="9" width="8" height="8" rx="1"></rect>`) {
		t.Fatal("custom UEFI firmware icon lost its microchip body")
	}
	if strings.Contains(markup, `title="Reboot to UEFI/BIOS"`) {
		t.Fatal("system actions should use explicit custom tooltips instead of native title bubbles")
	}

	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(styles)
	for _, want := range []string{
		".control-power-actions",
		".control-center-panel",
		".control-icon-button",
		".control-action-icon",
		".control-icon-button[data-tooltip]::after",
		".control-icon-button[data-tooltip]:focus-visible::after",
		".system-action-dialog",
		".system-action-dialog::backdrop",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("system power styling missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, want := range []string{
		"fetch(\"/api/system-actions\"",
		"latestStatus.variant === \"hws\"",
		"latestStatus.firmware.available === true",
		"systemDialog.showModal()",
		"body.set(\"confirm_players\", \"yes\")",
		"fetch(\"/api/system-actions/\" + selectedAction",
		"Reboot JustVoxel?",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("system power behavior missing %q", want)
		}
	}
}
