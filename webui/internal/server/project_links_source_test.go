package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
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
	aboutLinks := append(links, []struct {
		label string
		url   string
	}{
		{"btop", "https://github.com/aristocratos/btop"},
		{"Superfile", "https://github.com/yorukot/superfile"},
		{"just", "https://github.com/casey/just"},
		{"gum", "https://github.com/charmbracelet/gum"},
		{"fzf", "https://github.com/junegunn/fzf"},
		{"ujust / ugum inspiration", "https://github.com/ublue-os/packages/tree/main/packages/ublue-os-just"},
		{"ttyd", "https://github.com/tsl0922/ttyd"},
		{"File Browser", "https://github.com/gtsteffaniak/filebrowser"},
		{"Glances", "https://github.com/nicolargo/glances"},
		{"Tailscale", "https://tailscale.com/"},
		{"NetBird", "https://netbird.io/"},
		{"NordVPN Meshnet", "https://nordvpn.com/meshnet/"},
		{"Playit.gg", "https://playit.gg/"},
	}...)
	for _, link := range aboutLinks {
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
		`dd.appendChild(externalLink(text, url))`,
		`projectList.appendChild(row)`,
		`built.panel.appendChild(project)`,
		`appendLinkSection("Third-party software", [`,
		`appendLinkSection("Remote access", [`,
		`["CLI", [`, `["WebUI", [`, `["VPN / Mesh", [`, `["Public game access", [`,
		`root.append(built.panel, disclaimer)`,
		`disclaimer.textContent = "NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT."`,
	} {
		if !strings.Contains(about, required) {
			t.Errorf("System About is missing %q", required)
		}
	}
}

func TestAboutFlexLayoutAndMobileMetadata(t *testing.T) {
	cssBytes, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	for _, required := range []string{
		`.system-about-view{display:flex;flex-direction:column;min-height:100%;padding-bottom:.2rem}`,
		`.system-about-view>.panel{flex-shrink:0}`,
		`.system-about-view .disclaimer{margin:auto 0 0;padding-top:.45rem}`,
		`.system-about-view dl{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 .9rem}`,
		`.system-about-view dl>div{min-width:0}`,
		`.system-about-view .about-project dd{min-width:0}`,
	} {
		if !strings.Contains(css, required) {
			t.Errorf("About layout missing %q", required)
		}
	}
	if !regexp.MustCompile(`(?s)@media\(max-width:600px\)\{[^}]*\.system-security-form,\.system-about-view dl\{grid-template-columns:1fr\}`).MatchString(css) {
		t.Error("About mobile single-column rule missing")
	}
	for _, rule := range regexp.MustCompile(`[^{}]+\{[^{}]*\}`).FindAllString(css, -1) {
		if !strings.Contains(strings.SplitN(rule, "{", 2)[0], ".system-about-view") {
			continue
		}
		if regexp.MustCompile(`position\s*:\s*(absolute|fixed)|transform\s*:|overflow\s*:\s*hidden|white-space\s*:\s*nowrap`).MatchString(rule) {
			t.Errorf("About must retain normal flow and wrapping: %s", rule)
		}
	}
}
