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
	for _, want := range []string{"data-topbar-clock", `data-system-tab="date-time"`, "data-date-time-panel", "data-date-time-canonical", "data-timezone-search", `type="checkbox" name="automatic" data-date-time-automatic`, "date-time-switch"} {
		if !strings.Contains(header, want) {
			t.Fatalf("Date & Time template missing %q", want)
		}
	}
	for _, want := range []string{".date-time-switch{appearance:none", ".date-time-switch:checked{border-color:#4f9e6e;background:#4f9e6e", ".date-time-switch:focus-visible"} {
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
	wallpaperPosition := strings.Index(footer, `type="button" data-wallpaper-open>Wallpaper</button>`)
	if wallpaperPosition <= last {
		t.Fatal("Wallpaper button must follow Report an issue")
	}
	last = wallpaperPosition
	if position := strings.Index(footer, "NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT."); position <= last {
		t.Fatal("Project disclaimer must follow the links")
	}
	for _, path := range []string{"templates/dashboard.html", "templates/setup_wizard.html", "templates/setup_review.html", "templates/setup_progress.html"} {
		page := read(path)
		if strings.Count(page, `{{template "project-footer" .}}`) != 1 || strings.Contains(page, `class="setup-footer`) || strings.Contains(page, `class="dashboard-footer`) {
			t.Fatalf("%s must use the shared project footer", path)
		}
	}
	wallpaperJS := read("static/wallpaper.js")
	for _, want := range []string{".project-wallpaper-button{display:none;", `#dashboard[data-configured="true"] .project-wallpaper-button{display:inline}`} {
		if !strings.Contains(appCSS, want) {
			t.Fatalf("Configured-dashboard-only wallpaper control styling missing %q", want)
		}
	}
	if strings.Contains(appCSS, "#dashboard .project-wallpaper-button{display:inline}") {
		t.Fatal("Wallpaper control must not be shown on an unconfigured dashboard")
	}
	for _, want := range []string{
		`#dashboard[data-configured="false"] .dashboard-wallpaper{display:none}`,
		`#dashboard[data-configured="false"]::before{content:none;display:none}`,
	} {
		if !strings.Contains(appCSS, want) {
			t.Fatalf("Unconfigured dashboard wallpaper and overlay suppression missing %q", want)
		}
	}
	for _, want := range []string{
		`.dashboard-wallpaper{position:fixed;inset:0;width:100%;height:100dvh;object-fit:cover;object-position:center;z-index:-3;pointer-events:none}`,
		`#dashboard::before{content:"";position:fixed;inset:0;z-index:-2;pointer-events:none;background:#07100b66}`,
	} {
		if !strings.Contains(appCSS, want) {
			t.Fatalf("Configured dashboard wallpaper behavior missing %q", want)
		}
	}
	for _, forbidden := range []string{"#dashboard::after", "data-spotlight-active", "--voxel-x", "--voxel-y"} {
		if strings.Contains(appCSS, forbidden) {
			t.Fatalf("Removed dashboard spotlight styling must not contain %q", forbidden)
		}
	}
	if !strings.Contains(read("templates/dashboard.html"), `data-configured="{{.Status.Minecraft.Configured}}"`) || !strings.Contains(read("static/app.js"), "dashboard.dataset.configured = String(Boolean(status.minecraft.configured));") {
		t.Fatal("Wallpaper visibility must use the existing synchronized dashboard configuration state")
	}
	if !strings.Contains(wallpaperJS, "const wallpaper = document.querySelector(\"[data-dashboard-wallpaper]\");\n  if (!wallpaper) return;\n  const form =") {
		t.Fatal("Wallpaper must require the dashboard wallpaper element before initialization")
	}
	for _, want := range []string{"justvoxel-wallpaper-preference", "localStorage.setItem", "indexedDB.open", `images.put(blob, "uploaded")`, `images.delete("uploaded")`, "URL.createObjectURL", "URL.revokeObjectURL", `url.protocol !== "http:" && url.protocol !== "https:"`, "10 * 1024 * 1024", `"image/jpeg", "image/png", "image/webp", "image/avif"`, "await loadImage(url)", "await loadImage(candidateObjectURL)"} {
		if !strings.Contains(wallpaperJS, want) {
			t.Fatalf("browser wallpaper behavior missing %q", want)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "/api/", "FileReader", "readAsDataURL"} {
		if strings.Contains(wallpaperJS, forbidden) {
			t.Fatalf("wallpaper must remain browser-local without %q", forbidden)
		}
	}
	if !strings.Contains(footer, `<script src="/static/wallpaper.js" defer></script>`) {
		t.Fatal("shared footer must load wallpaper controls")
	}
	timezoneJS := read("static/timezone-search.js")
	if strings.Contains(timezoneJS, `addEventListener("focus", renderTimezoneResults)`) {
		t.Fatal("timezone focus must not open suggestions")
	}
	for _, want := range []string{`addEventListener("click", renderTimezoneResults)`, `addEventListener("input", renderTimezoneResults)`, `event.key === "Escape"`, `button.addEventListener("click", () => chooseTimezone(zone))`} {
		if !strings.Contains(timezoneJS, want) {
			t.Fatalf("timezone search interaction missing %q", want)
		}
	}

	setupCSS := read("static/setup.css")
	for _, want := range []string{"#dashboard{position:relative;isolation:isolate;display:flex;flex-direction:column;min-height:calc(100dvh - 48px)", ".project-footer{margin-top:auto", ".setup-body{display:flex;flex-direction:column;min-height:100dvh}", ".setup-shell{display:flex;flex:1;flex-direction:column"} {
		if !strings.Contains(appCSS+setupCSS, want) {
			t.Fatalf("Footer page layout missing %q", want)
		}
	}
	// Auto overflow is a safety fallback for small screens, large inventories and
	// long warnings, including the storage confirmation dialog. It does not force
	// scrolling when ordinary step content fits the desktop frame.
	if strings.Contains(setupCSS, "height:clamp(520px") || strings.Contains(setupCSS, "height:calc(100dvh - 185px)") || strings.Contains(setupCSS, "overflow-y:scroll") {
		t.Fatal("Setup must not use obsolete fixed panel heights or force vertical scrolling")
	}
	for _, want := range []string{
		`.setup-body .setup-wizard-panel .setup-form>.setup-step-content{display:flex;flex:1;flex-direction:column;gap:.42rem;min-height:0;min-width:0;overflow-y:auto;overflow-x:hidden}`,
		`.setup-body .setup-wizard-panel .setup-form>.setup-step-actions{position:static;flex:none;margin-top:.4rem}`,
		`.setup-body .setup-wizard-panel .setup-form>.setup-step-content{display:contents}`,
	} {
		if !strings.Contains(setupCSS, want) {
			t.Fatalf("Setup overflow safety and reachable actions missing %q", want)
		}
	}
}
