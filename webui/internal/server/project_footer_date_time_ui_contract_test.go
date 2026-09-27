package server

import (
	"os"
	"strings"
	"testing"
)

func TestProjectFooterAndDateTimeUIContracts(t *testing.T) {
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	header := read("templates/header.html")
	dateTime := read("static/date-time.js")
	appCSS := read("static/app.css")
	if strings.Contains(header, "data-topbar-timezone") || strings.Contains(dateTime, "data-topbar-timezone") {
		t.Fatal("Timezone must not be displayed in the top bar")
	}
	for _, want := range []string{"data-topbar-clock", "topbar-time-utility", "data-date-time-open", "data-date-time-canonical", "data-timezone-search", `type="checkbox" name="automatic" data-date-time-automatic`, "date-time-switch"} {
		if !strings.Contains(header, want) {
			t.Fatalf("Date & Time template missing %q", want)
		}
	}
	for _, want := range []string{".topbar-time .topbar-time-utility", ".date-time-switch{appearance:none", ".date-time-switch:checked{border-color:#4f9e6e;background:#4f9e6e", ".date-time-switch:focus-visible"} {
		if !strings.Contains(appCSS, want) {
			t.Fatalf("Date & Time styling missing %q", want)
		}
	}
	for _, want := range []string{"date.disabled = automatic.checked", "time.disabled = automatic.checked", "warning.hidden = automatic.checked", "automatic.addEventListener(\"change\", syncManual)"} {
		if !strings.Contains(dateTime, want) {
			t.Fatalf("Automatic time behavior missing %q", want)
		}
	}

	footer := read("templates/project_footer.html")
	links := []string{
		`href="https://github.com/home-server-project/justvoxel" target="_blank" rel="noopener noreferrer">JustVoxel on GitHub`,
		`href="https://github.com/home-server-project" target="_blank" rel="noopener noreferrer">Home Server Project`,
		`href="https://github.com/home-server-project/justvoxel/issues" target="_blank" rel="noopener noreferrer">Report an issue`,
	}
	last := -1
	for _, link := range links {
		position := strings.Index(footer, link)
		if position <= last {
			t.Fatalf("Project link missing or out of order: %q", link)
		}
		last = position
	}
	if position := strings.Index(footer, "NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT."); position <= last {
		t.Fatal("Project disclaimer must follow the links")
	}
	for _, path := range []string{"templates/dashboard.html", "templates/setup_wizard.html", "templates/setup_review.html", "templates/setup_progress.html"} {
		page := read(path)
		if strings.Count(page, `{{template "project-footer" .}}`) != 1 || strings.Contains(page, `class="setup-footer`) || strings.Contains(page, `class="dashboard-footer`) {
			t.Fatalf("%s must use the shared project footer", path)
		}
	}
	setupCSS := read("static/setup.css")
	for _, want := range []string{"#dashboard{position:relative;isolation:isolate;display:flex;flex-direction:column;min-height:calc(100dvh - 48px)", ".project-footer{margin-top:auto", ".setup-body{display:flex;flex-direction:column;min-height:100dvh}", ".setup-shell{display:flex;flex:1;flex-direction:column"} {
		if !strings.Contains(appCSS+setupCSS, want) {
			t.Fatalf("Footer page layout missing %q", want)
		}
	}
	if strings.Contains(setupCSS, "height:clamp(520px") || strings.Contains(setupCSS, "height:calc(100dvh - 185px)") || strings.Contains(setupCSS, "overflow-y:auto;overscroll-behavior:contain") {
		t.Fatal("Setup content must not scroll inside a fixed page panel")
	}
}
