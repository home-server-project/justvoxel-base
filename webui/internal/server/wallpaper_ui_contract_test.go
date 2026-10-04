package server

import (
	"regexp"
	"strings"
	"testing"
)

func TestWallpaperUIContracts(t *testing.T) {
	read := func(path string) string {
		data, err := assets.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	require := func(source, label string, fragments ...string) {
		t.Helper()
		for _, fragment := range fragments {
			if !strings.Contains(source, fragment) {
				t.Fatalf("%s missing %q", label, fragment)
			}
		}
	}

	footer := read("templates/project_footer.html")
	require(footer, "Wallpaper dialog",
		`<h2 id="wallpaper-title">Wallpaper</h2>`,
		"Choose the background used by this browser.",
		`name="theme" value="v1">V1`, `name="theme" value="v2" checked>V2`,
		`name="appearance" value="auto" checked>Automatic`,
		`name="appearance" value="day">Day`, `name="appearance" value="night">Night`,
		`type="time" name="dayStart" value="07:00"`, `type="time" name="nightStart" value="19:00"`,
		`value="url">Web address`, `value="upload">Upload picture`,
		"Reset to default", "Cancel", "Apply", `data-wallpaper-error role="alert"`,
		"JPEG", "PNG", "WebP", "AVIF", "10 MiB",
		"data-wallpaper-builtin-fields", "data-wallpaper-schedule",
		"data-wallpaper-url-fields", "data-wallpaper-upload-fields",
	)
	if strings.Count(footer, `class="wallpaper-theme-card"`) != 2 ||
		strings.Count(footer, `class="wallpaper-split-preview"`) != 2 {
		t.Fatal("Wallpaper must have exactly two split-preview theme cards")
	}
	for _, theme := range []string{"v1", "v2"} {
		for _, appearance := range []string{"day", "night"} {
			base := "jv-wp-" + theme + "-" + appearance
			preview := base + "-preview.webp"
			require(footer, "theme preview", `src="/static/wallpapers/`+preview+`"`)
			if strings.Contains(footer, `src="/static/wallpapers/`+base+`.webp"`) {
				t.Fatal("Selector must load only small previews")
			}
			for _, name := range []string{base + ".webp", preview} {
				data := read("static/wallpapers/" + name)
				if len(data) < 12 || data[:4] != "RIFF" || data[8:12] != "WEBP" {
					t.Fatalf("Embedded asset %s must be a WebP", name)
				}
			}
		}
	}
	dashboard := read("templates/dashboard.html")
	require(dashboard, "dashboard fallback", `src="/static/wallpapers/jv-wp-v2-day.webp"`, "wallpaper-theme-v2")
	if strings.Contains(dashboard, "/static/justvoxel-default-wallpaper.jpg") {
		t.Fatal("Old JPG must not remain the configured-dashboard fallback")
	}
	if _, err := assets.ReadFile("static/justvoxel-default-wallpaper.jpg"); err != nil {
		t.Fatal("Old JPG must remain embedded:", err)
	}

	js := read("static/wallpaper.js")
	require(js, "default and migration",
		`mode: "builtin", theme: "v2", appearance: "auto", dayStart: "07:00", nightStart: "19:00"`,
		`if (saved.mode === "default") return defaultPreference();`,
		`if (saved.mode === "default" || saved.mode === "builtin")`,
		`if (saved.mode === "url")`, `else if (saved.mode === "upload")`,
		`timeMinutes(next.dayStart) === timeMinutes(next.nightStart)`,
	)
	require(js, "local automatic schedule",
		"const automaticAppearance", "new Date()", "now.getHours()", "now.getMinutes()",
		"day < night ? minutes >= day && minutes < night :", "minutes >= day || minutes < night",
		"const nextBoundary = Math.min(...boundaries)",
		"boundary.setHours(", "boundary.setDate(boundary.getDate() + 1)",
		"window.setTimeout(refreshBuiltin, Math.max(1, nextBoundary - now.getTime()))",
		"window.clearTimeout(automaticTimer)", `preference.appearance !== "auto"`,
		`document.addEventListener("visibilitychange"`, `window.addEventListener("focus", refreshBuiltin)`,
		`window.addEventListener("pageshow", refreshBuiltin)`, `window.addEventListener("pagehide"`,
	)
	require(js, "browser-local storage and validation",
		"localStorage.setItem", "indexedDB.open", `images.put(blob, "uploaded")`,
		`images.delete("uploaded")`, `await storedImage("delete")`,
		"validateURL", `url.protocol !== "http:" && url.protocol !== "https:"`,
		"validateBlob", `"image/jpeg", "image/png", "image/webp", "image/avif"`,
		"10 * 1024 * 1024", "await loadImage(url)", "await loadImage(candidateObjectURL)",
		"await loadImage(builtinImage(next))", "URL.revokeObjectURL",
		"currentOperation !== operation", "restoreCanceled",
	)
	// Only Apply (or the explicit reset) may save a pending selection.
	submit := strings.Index(js, `form.addEventListener("submit"`)
	if submit < 0 {
		t.Fatal("Apply handler missing")
	}
	load := strings.Index(js[submit:], "await loadImage(builtinImage(next))")
	save := strings.Index(js[submit:], "savePreference(next)")
	if load < 0 || save <= load {
		t.Fatal("Built-in wallpaper must load before Apply commits its preference")
	}
	require(js, "compact mutually exclusive fields",
		`hidden = !builtin`, `hidden = !automatic`, `hidden = mode !== "url"`, `hidden = mode !== "upload"`,
	)
	for _, forbidden := range []string{
		"matchMedia", "prefers-color-scheme", "geolocation", "sunrise", "setInterval",
		"fetch(", "XMLHttpRequest", "/api/", ".style", `setAttribute("style"`,
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("Wallpaper must not use %q", forbidden)
		}
	}
	css := read("static/app.css")
	require(css, "wallpaper layout",
		"object-fit:cover", ".dashboard-wallpaper.wallpaper-theme-v1{object-position:50% center}",
		".dashboard-wallpaper.wallpaper-theme-v2{object-position:65% center}",
		".wallpaper-dialog .wallpaper-appearance{grid-template-columns:repeat(3,minmax(0,1fr))}",
		".wallpaper-schedule{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))",
		".wallpaper-dialog [data-wallpaper-error]{padding:.4rem .5rem;overflow-wrap:anywhere}",
	)
	rules := regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	scroll := regexp.MustCompile(`overflow(?:-y)?\s*:\s*(?:auto|scroll)\b`)
	for _, rule := range rules.FindAllStringSubmatch(css, -1) {
		if strings.Contains(rule[1], "wallpaper") && scroll.MatchString(rule[2]) {
			t.Fatalf("Wallpaper must not have an internal scrollbar: %s", rule[0])
		}
	}
	require(css, "phone and short-screen layout",
		"@media(max-width:600px),(max-height:720px)",
		"@media(max-width:390px)", ".wallpaper-split-preview img{height:50px}",
		".wallpaper-dialog .system-action-dialog-content{padding:.65rem;gap:.45rem}",
		".dashboard-wallpaper.wallpaper-theme-v2{object-position:78% center}",
	)
	if strings.Contains(footer, "style=") {
		t.Fatal("Wallpaper template must not introduce inline styles")
	}
}
