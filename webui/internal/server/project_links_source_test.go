package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectLinksInDashboardAndAbout(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "1.0.0", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	links := []struct {
		label string
		url   string
	}{
		{"JustVoxel on GitHub", "https://github.com/home-server-project/justvoxel"},
		{"Home Server Project", "https://github.com/home-server-project"},
		{"Report an issue", "https://github.com/home-server-project/justvoxel/issues"},
	}

	for _, page := range []struct {
		name string
		path string
		show func(http.ResponseWriter, *http.Request)
	}{
		{"dashboard", "/", app.Handler().ServeHTTP},
	} {
		t.Run(page.name, func(t *testing.T) {
			req := authenticatedAdminRequest(http.MethodGet, "http://example"+page.path, "")
			rr := httptest.NewRecorder()
			page.show(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("response status = %d", rr.Code)
			}
			body := rr.Body.String()
			for _, link := range links {
				anchor := `<a href="` + link.url + `" target="_blank" rel="noopener noreferrer">` + link.label + `</a>`
				if !strings.Contains(body, anchor) {
					t.Errorf("missing external link %q", anchor)
				}
			}
		})
	}

	// System → About is assembled in the browser, so check its link rows and attributes at the source.
	source, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	about := string(source)
	start := strings.Index(about, "const renderAbout = async")
	if start < 0 {
		t.Fatal("System About renderer not found")
	}
	end := strings.Index(about[start:], "const systemUPSStateLabel")
	if end < 0 {
		t.Fatal("System About renderer not found")
	}
	about = about[start : start+end]
	for _, link := range links {
		row := `"` + link.label + `", "` + link.url + `"`
		if !strings.Contains(about, row) {
			t.Errorf("System About is missing %q", row)
		}
	}
	for _, required := range []string{
		`projectHeading.textContent = "Project"`,
		`link = document.createElement("a")`,
		`link.href = url`,
		`link.target = "_blank"`,
		`link.rel = "noopener noreferrer"`,
		`dd.appendChild(link)`,
		`projectList.appendChild(row)`,
		`built.panel.appendChild(project)`,
	} {
		if !strings.Contains(about, required) {
			t.Errorf("System About is missing %q", required)
		}
	}
}
