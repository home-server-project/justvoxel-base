package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakePluginsWorkspaceAPI struct {
	fakeDiscoveryAPI
	inventoryCalls int
	catalogCalls   int
	updates        bool
	query          url.Values
}

func (f *fakePluginsWorkspaceAPI) AdminPluginInventory(_ context.Context, session string, updates bool) (api.PluginInventory, error) {
	if session != "session-token" {
		return api.PluginInventory{}, api.ErrUnauthorized
	}
	f.inventoryCalls++
	f.updates = updates
	return api.PluginInventory{Server: api.PluginServerContext{ServerSoftware: "Paper", Supported: true, MinecraftVersion: "1.21.8"}, Plugins: []api.InstalledPlugin{{Filename: "manual.jar", Management: "manual", UpdateStatus: "Update status unknown - manually managed"}}}, nil
}
func (f *fakePluginsWorkspaceAPI) AdminPluginDiscover(_ context.Context, session string, query url.Values) (api.PluginCatalog, error) {
	if session != "session-token" {
		return api.PluginCatalog{}, api.ErrUnauthorized
	}
	f.catalogCalls++
	f.query = query
	return api.PluginCatalog{Source: "Modrinth", Projects: []api.PluginCatalogProject{}}, nil
}
func TestPluginsWorkspaceRoutesEnforceAdministrator(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "administrator"} {
		client := &fakePluginsWorkspaceAPI{}
		client.role = role
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"installed", "updates", "discover"} {
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/"+path, ""))
			want := http.StatusForbidden
			if role == "administrator" {
				want = http.StatusOK
			}
			if rr.Code != want {
				t.Fatalf("%s %s: %d %s", role, path, rr.Code, rr.Body.String())
			}
		}
		if role != "administrator" && (client.inventoryCalls != 0 || client.catalogCalls != 0) {
			t.Fatal("unauthorized plugin access")
		}
		if role == "administrator" && (client.inventoryCalls != 2 || client.catalogCalls != 1 || !client.updates) {
			t.Fatal("plugin read routes not forwarded")
		}
	}
}
func TestPluginsWorkspaceForwardsCatalogFiltersAndRejectsMutation(t *testing.T) {
	client := &fakePluginsWorkspaceAPI{}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/discover?q=example&compatible_only=false&release_only=true&offset=8", ""))
	if rr.Code != http.StatusOK || client.query.Get("q") != "example" || client.query.Get("compatible_only") != "false" || client.query.Get("offset") != "8" {
		t.Fatalf("query not forwarded: %d %#v", rr.Code, client.query)
	}
	for _, path := range []string{"discover", "installed", "updates"} {
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/"+path, "csrf=csrf-token"))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("mutation allowed: %s %d", path, rr.Code)
		}
	}
	if client.inventoryCalls != 0 || client.catalogCalls != 1 {
		t.Fatal("mutation reached Management API")
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://example/api/plugins/workspace/installed", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth: %d", rr.Code)
	}
}
func TestPluginsWorkspaceUsesExistingWindowAndSafeRendering(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	markup, behavior := string(header), string(script)
	for _, want := range []string{`class="control-tile nav-admin-only" type="button" data-plugins-open`, `data-workspace-window="plugins"`, `data-plugins-tab="discover"`, `data-plugins-tab="installed"`, `data-plugins-tab="updates"`, `name="compatible_only" type="checkbox" checked`, `name="release_only" type="checkbox" checked`} {
		if !strings.Contains(markup, want) {
			t.Fatalf("plugin contract missing %s", want)
		}
	}
	start := strings.Index(behavior, "// Read-only plugin workspace.")
	end := strings.Index(behavior, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("workspace implementation missing")
	}
	pluginScript := behavior[start:end]
	for _, want := range []string{"setupWorkspaceWindow(pluginsDialog", `document.body.classList.contains("role-administrator")`, `node.textContent = value`, `/api/plugins/workspace/`, "Manual / unmanaged", "JustVoxel managed"} {
		if !strings.Contains(pluginScript, want) {
			t.Fatalf("behavior missing %s", want)
		}
	}
	for _, unsafe := range []string{"api.modrinth.com", "innerHTML", "outerHTML", "insertAdjacentHTML", `createElement("img")`, "showModal(", "method: \"POST\""} {
		if strings.Contains(pluginScript, unsafe) {
			t.Fatalf("unsafe plugin browser behavior %s", unsafe)
		}
	}
}

func TestPluginsWorkspaceCompactInfoPresentation(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	behavior := string(script)
	start := strings.Index(behavior, "  const addInfo = (card, name) => {")
	project := strings.Index(behavior, "  const renderProject = (project, server) => {")
	installed := strings.Index(behavior, "  const renderInstalled = (plugin) => {")
	if start < 0 || project <= start || installed <= project {
		t.Fatal("compact plugin renderers missing")
	}
	end := strings.Index(behavior[installed:], "  const load = async () => {")
	if end < 0 {
		t.Fatal("plugin load boundary missing")
	}
	info := behavior[start:project]
	for _, want := range []string{`text("button", "Info"`, `button.type = "button"`, `details.hidden = true`, `button.setAttribute("aria-expanded", "false")`, `button.setAttribute("aria-controls", details.id)`, `details.hidden = !details.hidden`, `button.setAttribute("aria-expanded", String(!details.hidden))`, `card.append(details)`} {
		if !strings.Contains(info, want) {
			t.Fatalf("collapsed per-card Info behavior missing %s", want)
		}
	}
	discover := behavior[project:installed]
	for _, want := range []string{`addInfo(card, project.title)`, `"plugins-card-description"`, `counts.set(kind, (counts.get(kind) || 0) + 1)`, `details.append(list)`, `dependency.project_id`, `dependency.version_id`, `dependency.file_name`, `project.metadata_error`} {
		if !strings.Contains(discover, want) {
			t.Fatalf("Discover summary or Info metadata missing %s", want)
		}
	}
	inventory := behavior[installed : installed+end]
	if !strings.Contains(inventory, `const name = plugin.name || plugin.filename;`) || !strings.Contains(inventory, `card.append(text("h3", name)`) {
		t.Fatal("Installed/Updates title must fall back to the JAR filename when plugin.name is empty")
	}
	for _, want := range []string{`addInfo(card, name)`, `tab === "updates"`, `details.append(text("p", ` + "`JAR: ${plugin.filename}`", `plugin.metadata_status`, `details.append(text("p", plugin.update_status`} {
		if !strings.Contains(inventory, want) {
			t.Fatalf("Installed/Updates summary or Info metadata missing %s", want)
		}
	}
	for _, unsafe := range []string{`card.append(list)`, `card.append(text("p", project.compatibility`, `card.append(text("p", plugin.metadata_status`, `card.append(text("p", plugin.update_status`} {
		if strings.Contains(discover+inventory, unsafe) {
			t.Fatalf("full metadata leaked into scanning view: %s", unsafe)
		}
	}
	for _, want := range []string{`.plugins-workspace-body{container-type:inline-size;`, `grid-template-columns:minmax(0,1fr)`, `@container(min-width:580px){.plugins-results{grid-template-columns:repeat(2,minmax(0,1fr))}}`, `@container(min-width:860px){.plugins-results{grid-template-columns:repeat(3,minmax(0,1fr))}}`, `-webkit-line-clamp:2`, `.plugins-workspace-dialog [hidden]{display:none!important}`} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("compact workspace styling missing %s", want)
		}
	}
}
