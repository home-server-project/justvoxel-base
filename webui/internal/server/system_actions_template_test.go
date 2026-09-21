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
		"aria-label=\"Power options\"",
		"data-system-action=\"reboot\"",
		">Restart</button>",
		"data-system-action=\"poweroff\"",
		">Power off</button>",
		"data-system-action=\"firmware-reboot\" hidden",
		">Restart to UEFI/BIOS</button>",
		"data-system-action-dialog",
		"data-system-action-confirm",
		"data-system-actions-csrf",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("system power header missing %q", want)
		}
	}

	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(styles)
	for _, want := range []string{
		".system-power-button",
		".system-power-menu",
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
		"latestStatus.variant === \"hwe\"",
		"latestStatus.firmware.available === true",
		"systemDialog.showModal()",
		"body.set(\"confirm_players\", \"yes\")",
		"fetch(\"/api/system-actions/\" + selectedAction",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("system power behavior missing %q", want)
		}
	}
}
