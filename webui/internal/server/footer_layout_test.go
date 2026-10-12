package server

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestFooterRenderedAcrossDesktopAndSetupFlows(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, data any) string {
		t.Helper()
		var body bytes.Buffer
		if err := app.templates.ExecuteTemplate(&body, name, data); err != nil {
			t.Fatal(err)
		}
		return body.String()
	}
	footerPattern := regexp.MustCompile(`(?s)<footer class="project-footer">.*?</footer>`)
	desktop := render("dashboard.html", map[string]any{
		"Status": map[string]any{"Minecraft": map[string]any{"Configured": true}},
	})
	if !strings.Contains(desktop, `data-configured="true"`) {
		t.Fatal("fixture must render the configured desktop")
	}
	footer := footerPattern.FindString(desktop)
	if footer == "" {
		t.Fatal("configured desktop footer missing")
	}
	for _, link := range []struct{ url, label string }{
		{"https://github.com/home-server-project/justvoxel", "JustVoxel on GitHub"},
		{"https://github.com/home-server-project", "Home Server Project"},
		{"https://github.com/home-server-project/justvoxel/issues", "Report an issue"},
	} {
		if !strings.Contains(footer, `<a href="`+link.url+`" target="_blank" rel="noopener noreferrer">`+link.label+`</a>`) {
			t.Fatalf("footer navigation changed: %s", link.label)
		}
	}
	for _, want := range []string{
		`<nav class="project-links" aria-label="Project links">`,
		`<button class="project-wallpaper-button" type="button" data-wallpaper-open>Wallpaper</button>`,
		`<p class="disclaimer">NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT.</p>`,
	} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer content/accessibility changed: %s", want)
		}
	}
	for _, fixture := range []struct {
		name, template string
		data           any
	}{
		{"entry", "setup_wizard.html", setupWizardPageData{}},
		{"advanced", "setup_wizard.html", setupWizardPageData{Started: true, CurrentStep: 1}},
		{"recommended review", "setup_review.html", setupReviewPageData{SetupMode: "recommended"}},
		{"advanced review", "setup_review.html", setupReviewPageData{SetupMode: "advanced"}},
		{"progress", "setup_progress.html", setupProgressPageData{}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			body := render(fixture.template, fixture.data)
			if footers := footerPattern.FindAllString(body, -1); len(footers) != 1 || footers[0] != footer {
				t.Fatal("setup must render the same single footer as the configured desktop")
			}
			if !regexp.MustCompile(`</main>\s*<footer class="project-footer">`).MatchString(body) {
				t.Fatal("setup footer must follow the main panel container in page flow")
			}
			if strings.Count(body, `<script src="/static/wallpaper.js" defer></script>`) != 1 {
				t.Fatal("shared wallpaper behavior must remain loaded once")
			}
		})
	}
}
