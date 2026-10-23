package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakePluginsWorkspaceAPI struct {
	fakeDiscoveryAPI
	selectedIDs    []string
	selectedSeen   string
	inventoryCalls int
	catalogCalls   int
	updates        bool
	query          url.Values
	iconCalls      int
	iconURL        string
	iconProjectID  string
	iconSource     string
	iconError      error
	iconResult     *api.PluginIcon
}

func (f *fakePluginsWorkspaceAPI) AdminPluginInventory(ctx context.Context, session string, updates bool) (api.PluginInventory, error) {
	if session != "session-token" {
		return api.PluginInventory{}, api.ErrUnauthorized
	}
	f.selectedSeen = api.MinecraftMaintenanceInstance(ctx)
	f.inventoryCalls++
	f.updates = updates
	return api.PluginInventory{Server: api.PluginServerContext{ServerSoftware: "Paper", Supported: true, MinecraftVersion: "1.21.8"}, Plugins: []api.InstalledPlugin{{Filename: "manual.jar", Management: "manual", UpdateStatus: "Update status unknown - manually managed"}, {Filename: "verified.jar", Management: "plugins", ProjectID: "project123", VersionID: "release123", UpdateStatus: "Managed by Plugins; update and removal are not available yet"}, {Filename: "floodgate-spigot.jar", Management: "justvoxel", IconProjectID: "bWrNNfkb"}}}, nil
}
func (f *fakePluginsWorkspaceAPI) AdminPluginDiscover(_ context.Context, session string, query url.Values) (api.PluginCatalog, error) {
	if session != "session-token" {
		return api.PluginCatalog{}, api.ErrUnauthorized
	}
	f.catalogCalls++
	f.query = query
	return api.PluginCatalog{Modrinth: true, Projects: []api.PluginCatalogProject{}}, nil
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
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/discover?q=example&compatible_only=false&release_only=true&modrinth_offset=8", ""))
	if rr.Code != http.StatusOK || client.query.Get("q") != "example" || client.query.Get("compatible_only") != "false" || client.query.Get("modrinth_offset") != "8" {
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
	for _, want := range []string{`class="control-tile nav-admin-only" type="button" data-plugins-open`, `data-workspace-window="plugins"`, `data-plugins-tab="discover"`, `data-plugins-tab="installed"`, `role="switch" name="compatible_only" type="checkbox" checked`, `role="switch" name="release_only" type="checkbox" checked`} {
		if !strings.Contains(markup, want) {
			t.Fatalf("plugin contract missing %s", want)
		}
	}
	start := strings.Index(behavior, "// Plugin workspace.")
	end := strings.Index(behavior, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("workspace implementation missing")
	}
	pluginScript := behavior[start:end]
	for _, obsolete := range []string{"plugins-server-context", "data-plugins-context"} {
		if strings.Contains(markup, obsolete) || strings.Contains(pluginScript, obsolete) {
			t.Fatalf("obsolete Plugins context lane remains: %s", obsolete)
		}
	}
	if strings.Contains(pluginScript, "context.textContent") {
		t.Fatal("Plugins still writes the legacy context row")
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), ".plugins-server-context") {
		t.Fatal("obsolete Plugins context styling remains")
	}
	for _, want := range []string{`const server = payload.server;`, `acceptDiscoverContext(server)`, `control.disabled = !server.supported`, `if (!server.supported)`, `renderProject(item, server)`, `const platform = server.server_software || server.server_type;`, `server.minecraft_version`, `project.compatible && server.supported`, `data-server-selector-slot data-server-workspace="plugins"`} {
		if !strings.Contains(pluginScript, want) && !strings.Contains(markup, want) {
			t.Fatalf("authoritative Plugins server context missing: %s", want)
		}
	}
	for _, want := range []string{"setupWorkspaceWindow(pluginsDialog", `document.body.classList.contains("role-administrator")`, `node.textContent = value`, `/api/plugins/workspace/`, "Manual / unmanaged", "JustVoxel managed"} {
		if !strings.Contains(pluginScript, want) {
			t.Fatalf("behavior missing %s", want)
		}
	}
	for _, unsafe := range []string{"api.modrinth.com", "innerHTML", "outerHTML", "insertAdjacentHTML", "cdn.modrinth.com", "showModal("} {
		if strings.Contains(pluginScript, unsafe) {
			t.Fatalf("unsafe plugin browser behavior %s", unsafe)
		}
	}
}

// These source assertions protect structure; they do not establish visual acceptance.
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
	start := strings.Index(behavior, "  const identityTile =")
	project := strings.Index(behavior, "  const renderProject =")
	installed := strings.Index(behavior, "  const renderInstalled =")
	end := strings.Index(behavior, "  // Review, progress")
	if start < 0 || project <= start || installed <= project || end <= installed {
		t.Fatal("compact plugin renderers missing")
	}
	info := behavior[start:project]
	for _, want := range []string{`card.tabIndex = 0`, `card.setAttribute("role", "button")`, `card.setAttribute("aria-haspopup", "dialog")`, `card.setAttribute("aria-controls", "plugins-overlay")`, `event.key === "Enter" || event.key === " "`, `node.classList.toggle("is-selected", node === card)`, `card.addEventListener("click", open)`, "showPanel(`Info for ${name}`, true)", `renderInfo();`, `overlayActions(...actions, panelAction("Close", closePanel))`, `"plugins-icon-tile"`, `header.append(identityTile(name, projectID, iconURL, source), copy)`} {
		if !strings.Contains(info, want) {
			t.Fatalf("whole-card overlay behavior missing %s", want)
		}
	}
	for _, forbidden := range []string{`text("button", "Info"`, `text("button", "Install"`, `plugins-card-actions`} {
		if strings.Contains(behavior[start:end], forbidden) {
			t.Fatal("separate card action remains", forbidden)
		}
	}

	for _, inline := range []string{`card.append(details)`, `const details =`, `panelBody.append(text("h3", name), details`, `createElement("details")`, `plugins-card-details`, `aria-expanded`} {
		if strings.Contains(behavior[start:end], inline) {
			t.Fatalf("raw or inline card detail block remains: %s", inline)
		}
	}
	discover := behavior[project:installed]
	for _, want := range []string{`interactiveCard(card, project.title, () => reviewRejection`, `"plugins-card-description"`, `counts.set(kind, (counts.get(kind) || 0) + 1)`, `"JustVoxel managed"`, `const installable = ["Modrinth", "Hangar"].includes(project.source) && !managed && !project.installed && project.install_available`, `reviewPlugin(project.project_id, version.id, project, readOnlyReview)`} {
		if !strings.Contains(discover, want) {
			t.Fatalf("Discover summary or Info missing %s", want)
		}
	}
	inventory := behavior[installed:end]
	for _, want := range []string{`const name = plugin.name || plugin.filename;`, `addInfo(card, name, () => {`, `overlayHeader(name, "", [[plugin.version || "Version unknown", "plugins-badge-version"], management, [plugin.management === "plugins" && "Installed", "plugins-badge-installed"], [stateLabel, stateClass], [attention && "Needs attention", "plugins-badge-attention"]], projectID, plugin.management === "plugins" ? installedStatus() : null, undefined, plugin.source || "Modrinth")`, `plugin.update_status?.startsWith("Update available")`, `"Needs attention"`, `"plugins-inventory-status"`, `"JustVoxel managed"`, `"Managed by Plugins"`, `"Manual / unmanaged"`} {
		if !strings.Contains(inventory, want) {
			t.Fatalf("Installed/Updates presentation missing %s", want)
		}
	}
	for _, unsafe := range []string{`card.append(text("p", project.compatibility`, `card.append(text("p", plugin.metadata_status`, `card.append(text("p", plugin.update_status`, `card.append(text("p", project.project_id`, `card.append(text("p", version.id`} {
		if strings.Contains(discover+inventory, unsafe) {
			t.Fatalf("full metadata leaked into scanning view: %s", unsafe)
		}
	}
	style := string(css)
	for _, want := range []string{`.plugins-workspace-body{container-type:inline-size;`, `grid-template-columns:minmax(0,1fr)`, `@container(min-width:580px){.plugins-results{grid-template-columns:repeat(2,minmax(0,1fr))}}`, `@container(min-width:860px){.plugins-results{grid-template-columns:repeat(3,minmax(0,1fr))}}`, `@container(min-width:1140px){.plugins-results{grid-template-columns:repeat(4,minmax(0,1fr))}}`, `@container(min-width:1420px){.plugins-results{grid-template-columns:repeat(5,minmax(0,1fr))}}`, `-webkit-line-clamp:2`, `.plugins-workspace-dialog [hidden]{display:none!important}`, `max-width:1800px`, `.plugins-result-card.is-selected`, `.plugins-icon-tile{display:grid;place-items:center;flex:none;width:40px;height:40px`, `.plugins-overlay-actions{display:flex;flex-wrap:wrap;`, `word-break:normal`, `.plugins-version-list{display:flex;flex-wrap:wrap;`, `.plugins-info-rows{display:flex;flex-wrap:wrap;gap:.12rem 1rem;`} {
		if !strings.Contains(style, want) {
			t.Fatalf("compact workspace styling missing %s", want)
		}
	}
	compactStyle := strings.ReplaceAll(strings.ReplaceAll(style, " ", ""), "\n", "")
	for _, bad := range []string{`grid-auto-rows:1fr`, `min-height:230px`, `padding:.38rem.75rem`} {
		if strings.Contains(compactStyle, bad) {
			t.Fatalf("artificial plugin geometry remains %s", bad)
		}
	}

}

func TestPluginsWorkspaceBoundedOverlay(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(script), "// Plugin workspace.")
	end := strings.Index(string(script), "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("plugin workspace missing")
	}
	behavior := string(script)[start:end]
	for _, want := range []string{`panel.id = "plugins-overlay"`, `panel.setAttribute("aria-modal", "true")`, `panel.tabIndex = -1`, `workspaceShell.inert = true`, `workspaceShell.inert = false`, `returnFocus?.isConnected`, `event.key === "Escape"`, `event.key === "Tab"`, `focusedLabel`, `status.setAttribute("role", "status")`} {
		if !strings.Contains(behavior, want) {
			t.Fatalf("overlay contract missing %s", want)
		}
	}
	if strings.Count(behavior, `panel.setAttribute("role", "dialog")`) != 1 {
		t.Fatal("Info and installation must share one overlay")
	}
	for _, want := range []string{`.plugins-install-panel{position:absolute;inset:0`, `width:min(100%,760px);max-height:100%`, `min-width:0;overflow:auto;overscroll-behavior:contain;overflow-wrap:anywhere;padding:.75rem;`} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("bounded overlay style missing %s", want)
		}
	}
}

type fakePluginsInstallAPI struct {
	fakePluginsWorkspaceAPI
	plans, installs, operations int
	request                     api.PluginInstallApplyRequest
	operationType               string
	planError                   error
}

func (f *fakePluginsInstallAPI) AdminPluginInstallPlan(_ context.Context, session string, r api.PluginInstallRequest) (api.PluginInstallPlan, error) {
	f.plans++
	if f.planError != nil {
		return api.PluginInstallPlan{}, f.planError
	}
	return api.PluginInstallPlan{Source: r.Source, ProjectID: r.ProjectID, VersionID: r.VersionID, Name: "Authoritative plugin", Location: "/data/plugins", ConfirmationRequired: true, Online: 1, Players: []string{"Alex"}, Fingerprint: "sha256:" + strings.Repeat("a", 64)}, nil
}
func (f *fakePluginsInstallAPI) AdminPluginInstall(_ context.Context, session string, r api.PluginInstallApplyRequest) (api.PersistentOperationResponse, error) {
	f.installs++
	f.request = r
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: "jv-family1", OperationType: "plugin_install", OperationID: "12345678-1234-4123-8123-123456789abc", Stage: "downloading", State: "running", Status: "Downloading verified JARs."}}, nil
}
func (f *fakePluginsInstallAPI) AdminCurrentPluginInstall(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	f.operations++
	kind := f.operationType
	if kind == "" {
		kind = "plugin_install"
	}
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: "jv-family1", OperationType: kind, Stage: "backup", State: "running", Status: "Creating cold backup."}}, nil
}
func (f *fakePluginsInstallAPI) AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error) {
	return f.AdminCurrentPluginInstall(ctx, session)
}
func TestPluginsInstallRoutesAuthorizationCSRFAndContracts(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "administrator"} {
		client := &fakePluginsInstallAPI{}
		client.role = role
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		body := "csrf=csrf-token&project_id=root&version_id=release"
		for _, route := range []string{"install/plan", "install", "install-operation"} {
			rr := httptest.NewRecorder()
			method := http.MethodPost
			requestBody := body
			if route == "install" {
				requestBody += "&plan_fingerprint=sha256:" + strings.Repeat("a", 64) + "&confirm_players=true"
			}
			if route == "install-operation" {
				method = http.MethodGet
				requestBody = ""
			}
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(method, "http://example/api/plugins/workspace/"+route, requestBody))
			want := http.StatusForbidden
			if role == "administrator" {
				want = http.StatusOK
				if route == "install" {
					want = http.StatusAccepted
				}
			}
			if rr.Code != want {
				t.Fatalf("%s %s: %d %s", role, route, rr.Code, rr.Body.String())
			}
		}
		if role != "administrator" && (client.plans != 0 || client.installs != 0 || client.operations != 0) {
			t.Fatal("unauthorized install forwarded")
		}
		if role == "administrator" && (client.plans != 1 || client.installs != 1 || !client.request.ConfirmPlayers) {
			t.Fatal("reviewed installation not forwarded")
		}
	}
	client := &fakePluginsInstallAPI{}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"project_id=root&version_id=release", "csrf=wrong&project_id=root&version_id=release", "csrf=csrf-token&project_id=root&version_id=release&url=https://evil", "csrf=csrf-token&project_id=root&version_id=release&filename=x.jar", "csrf=csrf-token&project_id=root&project_id=other&version_id=release"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/install/plan", body))
		if rr.Code != 400 && rr.Code != 403 {
			t.Fatal("unsafe form accepted", rr.Code)
		}
	}
	if client.plans != 0 {
		t.Fatal("unsafe request reached management")
	}
	client.operationType = "minecraft_update"
	for _, query := range []string{"", "?id=12345678-1234-4123-8123-123456789abc"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/install-operation"+query, ""))
		if rr.Code != 404 {
			t.Fatal("update confused with plugin install")
		}
	}
}
func TestPluginsInstallReviewProgressAndOwnershipRendering(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start := strings.Index(script, "// Plugin workspace.")
	end := strings.Index(script, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("plugin workspace missing")
	}
	script = script[start:end]
	for _, want := range []string{
		`project.install_available && project.compatible && server.supported && server.minecraft_version && version?.id`,
		`["geyser", "floodgate", "viaversion"].includes(project.slug)`,
		`pluginRequest("install/plan"`, `plan.project_name`, `overlaySection("Compatibility")`, `plan.artifacts`, `plan.installation_location`,
		`if (plan.confirmation_required)`, `plan.players`, `confirm_players: String(plan.confirmation_required === true)`,
		`plan_fingerprint: plan.plan_fingerprint`, `operation.stage`, `operation.status`, `operation?id=`,
		`"View in Installed"`, `"Done"`, `"Managed by Plugins"`, `"JustVoxel managed"`, `"Manual / unmanaged"`,
		`interactiveCard(card, project.title, () => reviewRejection`, `addInfo(card, name, () => {`, `"Administrator attention required"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing Stage 2 behavior %s", want)
		}
	}
	for _, unsafe := range []string{"innerHTML", "insertAdjacentHTML", "api.modrinth.com", "cdn.modrinth.com", "artifact.url", "artifact.URL", "fakeSeconds", "elapsedSeconds"} {
		if strings.Contains(script, unsafe) {
			t.Fatalf("unsafe or fake behavior: %s", unsafe)
		}
	}
}

func (f *fakePluginsInstallAPI) AdminPluginInstallAcknowledge(_ context.Context, session string, request api.PluginInstallAcknowledgeRequest) (api.PersistentOperationResponse, error) {
	f.operations++
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: "jv-family1", OperationType: "plugin_install", OperationID: request.OperationID, State: "resolved", Status: "Plugin state verified; Minecraft remains stopped."}}, nil
}

func TestPluginsAcknowledgeRequiresAdministratorCSRFAndExactForm(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "administrator"} {
		client := &fakePluginsInstallAPI{}
		client.role = role
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{"csrf=csrf-token&operation_id=12345678-1234-4123-8123-123456789abc", "csrf=wrong&operation_id=id", "csrf=csrf-token&operation_id=id&force=true", "csrf=csrf-token&operation_id=id&operation_id=other", "csrf=csrf-token"} {
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/install/acknowledge", body))
			want := 403
			if role == "administrator" {
				if strings.Contains(body, "operation_id=12345678") {
					want = 200
				} else if !strings.Contains(body, "csrf=wrong") {
					want = 400
				}
			}
			if rr.Code != want {
				t.Fatal(role, body, rr.Code, rr.Body.String())
			}
			if want == 200 && !strings.Contains(rr.Body.String(), `"state":"resolved"`) {
				t.Fatal("resolved operation not returned")
			}
		}
		expectedCalls := 0
		if role == "administrator" {
			expectedCalls = 1
		}
		if client.operations != expectedCalls {
			t.Fatal("invalid re-check forwarded")
		}
	}
}

func TestPluginsAdditionalInfoAndStructuredInstallPresentation(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start := strings.Index(script, "// Plugin workspace.")
	end := strings.Index(script, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("plugin workspace missing")
	}
	script = script[start:end]
	project := strings.Index(script, "  const renderProject =")
	installed := strings.Index(script, "  const renderInstalled =")
	reviewStart := strings.Index(script, "  const reviewPlugin =")
	resume := strings.Index(script, "  const resumePluginInstall =")
	progressStart := strings.Index(script, "  const showOperation =")
	poll := strings.Index(script, "  const pollOperation =")
	inventoryEnd := strings.Index(script, "  // Review, progress")
	if project < 0 || installed <= project || inventoryEnd <= installed || progressStart <= inventoryEnd || poll <= progressStart || reviewStart <= poll || resume <= reviewStart {
		t.Fatal("plugin presentation boundaries missing")
	}
	discoverInfo := script[project:installed]
	for _, want := range []string{`overlayHeader(project.title, project.description`, `overlaySection("About this version")`, `about("Release"`, `about(project.source === "Hangar" ? "Channel" : "Release type", version?.version_type || "Not provided")`, `about("Published"`, `about("Source"`, `about("Project ID"`, `about("Release ID"`, `overlaySection("Compatibility")`, `compatibility("Active Minecraft version"`, `compatibility("Supported platforms"`, `compatibility("Supported versions"`, `new Set(version?.game_versions || [])`, `versions.join(" / ")`, `overlaySection("Dependencies").append(dependencyGroups(dependencies))`} {
		if !strings.Contains(discoverInfo, want) {
			t.Fatal("structured Discover Info missing", want)
		}
	}
	helpers := script[:project]
	for _, want := range []string{`"plugins-overlay-header"`, `"plugins-overlay-identity"`, `identity.append(identityTile(name, projectID, iconURL, source), text("h3", name))`, `["required", "Required"]`, `["optional", "Optional"]`, `["incompatible", "Incompatible"]`, `["other", "Embedded / other"]`, `dependency.name || dependency.title || dependency.file_name`, `plugins-dependency-ids`} {
		if !strings.Contains(helpers, want) {
			t.Fatal("overlay header or dependency grouping missing", want)
		}
	}
	inventory := script[installed:inventoryEnd]
	for _, label := range []string{"JAR", "Metadata", "Management", "Update status"} {
		if strings.Count(inventory, `overlaySection("`+label+`")`) != 1 {
			t.Fatal("inventory concept missing or duplicated", label)
		}
	}
	for _, want := range []string{`if (plugin.project_id || plugin.version_id)`, `identity("Project ID", plugin.project_id)`, `identity("Release ID", plugin.version_id)`} {
		if !strings.Contains(inventory, want) {
			t.Fatal("available installed identity missing", want)
		}
	}
	for _, duplicate := range []string{`row("Management", management)`, `text("p", management`, "Installed through Plugins."} {
		if strings.Contains(inventory, duplicate) {
			t.Fatal("duplicate ownership explanation", duplicate)
		}
	}
	review := script[reviewStart:resume]
	for _, want := range []string{`overlayHeader(plan.project_name, plan.description || project?.description`, `overlaySection("About this version")`, `about("Release"`, `about(plan.source === "Hangar" ? "Channel" : "Release type"`, `about("Published"`, `about("Source"`, `about("Project ID"`, `about("Release ID"`, `overlaySection("Compatibility")`, `compatibility("Result"`, `compatibility("Server software"`, `compatibility("Active Minecraft version"`, `compatibility("Supported platforms"`, `compatibility("Supported versions"`, `overlaySection("Dependencies")`, `artifact.satisfied ? "Already satisfied" : "Will be installed"`, `const informational = plan.display_dependencies || []`, `overlaySection("Files to install")`, `readableSize(artifact.size)`, `overlaySection("Installation")`, `plan.installation_location`, `overlaySection("Restart")`, `restart("Required", plan.restart_required ? "Yes" : "No")`, `Verified cold backup before changes`, `Minecraft starts/restarts to load and verify plugins`, `Returns to stopped after verification`, `if (plan.confirmation_required) {`, `overlaySection("Players")`, `panelAction("Install", () => applyPlugin(plan))`} {
		if !strings.Contains(review, want) {
			t.Fatal("combined plugin overlay missing", want)
		}
	}
	for _, forbidden := range []string{"plugins-review-layout", "plugins-review-primary", "plugins-review-secondary", "plugins-install-warning"} {
		if strings.Contains(review, forbidden) {
			t.Fatal("rejected install review presentation remains", forbidden)
		}
	}

	for _, old := range []string{`section("Plugin")`, `section("Compatibility")`, `section("Installation")`, `toLocaleString()} bytes`} {
		if strings.Contains(review, old) {
			t.Fatal("raw review presentation remains", old)
		}
	}
	if strings.Count(review, "player-aware shutdown") != 1 || strings.Index(review, `overlaySection("Players")`) < strings.Index(review, "if (plan.confirmation_required)") {
		t.Fatal("player warning not conditional or duplicated")
	}
	for _, want := range []string{`Number.isFinite(size)`, `"Size unavailable"`, `"MiB"`, `"KiB"`} {
		if !strings.Contains(script, want) {
			t.Fatal("truthful readable file sizes missing", want)
		}
	}
	progress := script[progressStart:poll]
	for _, want := range []string{`["Prepare", "Backup", "Install", "Restart", "Verify"]`, `[operation.stage]`, `operation.state`, `operation.status`, `"Plugin installation verified"`, `"Plugin state verified"`, `"Installation did not complete"`, `"Administrator attention required"`, `"Re-check recovery"`, `pluginRequest(lifecycle ? "manage/acknowledge" : "install/acknowledge"`, `operation_id: operation.operation_id`, `payload.operation.state !== "resolved"`, `"View in Installed"`, `"Done"`, `overlayActions(`} {
		if !strings.Contains(progress, want) {
			t.Fatal("persistent progress action missing", want)
		}
	}
	if strings.Count(progress, "operation.status") != 1 {
		t.Fatal("persistent status duplicated")
	}
	attention := progress[strings.Index(progress, "    if (attention) {"):strings.Index(progress, "    return terminal;")]
	if !strings.Contains(attention, `} else if (terminal && !success)`) || strings.Contains(attention, `panelAction("Done", closePanel`) {
		t.Fatal("attention exposes blind Done")
	}
	for _, unsafe := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", `createElement("svg")`, "artifact.url", "artifact.URL", "cdn.modrinth.com", "fakeSeconds", "elapsedSeconds", "countdown", "percent", "ETA", "%"} {
		if strings.Contains(script, unsafe) {
			t.Fatal("unsafe or invented plugin presentation", unsafe)
		}
	}
	if !strings.Contains(script, `const button = text("button", label, secondary ? "secondary" : "");`) {
		t.Fatal("overlay actions must use normal buttons rather than badges")
	}
	if !strings.Contains(script, "node.textContent = value") {
		t.Fatal("publisher rendering must remain text-only")
	}
}

func (f *fakePluginsWorkspaceAPI) AdminPluginIcon(_ context.Context, session, projectID string, iconURL ...string) (api.PluginIcon, error) {
	if session != "session-token" {
		return api.PluginIcon{}, api.ErrUnauthorized
	}
	f.iconCalls++
	f.iconProjectID = projectID
	if len(iconURL) == 1 {
		f.iconURL = iconURL[0]
	}
	if f.iconResult != nil {
		return *f.iconResult, f.iconError
	}
	return api.PluginIcon{ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nfixture")}, f.iconError
}

func TestPluginsWorkspaceIconAdministratorAndOnlyProjectID(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "administrator"} {
		client := &fakePluginsWorkspaceAPI{}
		client.role = role
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/icon?project_id=root", ""))
		if role != "administrator" {
			if rr.Code != 403 || client.iconCalls != 0 {
				t.Fatal("unauthorized icon reached Agent")
			}
		} else {
			if rr.Code != 200 || client.iconCalls != 1 || client.iconProjectID != "root" || rr.Header().Get("Content-Type") != "image/png" || rr.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(rr.Body.Bytes(), []byte("\x89PNG\r\n\x1a\nfixture")) {
				t.Fatal("verified icon not forwarded safely")
			}
		}
	}
	client := &fakePluginsWorkspaceAPI{}
	client.role = "administrator"
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "url=https://evil.example", "project_id=root&url=https://evil.example", "project_id=root&project_id=other", "project_id=../root", "project_id=" + strings.Repeat("a", 65), "project_id=root&bad=%xx"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/icon?"+query, ""))
		if rr.Code != 400 {
			t.Fatalf("unsafe icon query accepted %q: %d", query, rr.Code)
		}
	}
	if client.iconCalls != 0 {
		t.Fatal("unsafe icon query forwarded")
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://example/api/plugins/workspace/icon?project_id=root", nil))
	if rr.Code != 401 {
		t.Fatal("anonymous icon allowed")
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/icon?project_id=root", ""))
	if rr.Code != 405 || client.iconCalls != 0 {
		t.Fatal("icon mutation allowed")
	}
	for _, status := range []int{404, 503, 502} {
		client.iconError = &api.ResponseError{StatusCode: status, Message: "private upstream body"}
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/icon?project_id=root", ""))
		if rr.Code != status || rr.Header().Get("X-Content-Type-Options") != "nosniff" || strings.Contains(rr.Body.String(), "private upstream body") || strings.Contains(rr.Body.String(), "{\"") {
			t.Fatal("icon failure not safely preserved")
		}
	}
}

func TestPluginsWorkspaceSharedSameOriginLogoAndFallback(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	behavior := string(raw)
	start := strings.Index(behavior, "  const identityTile =")
	if start < 0 {
		t.Fatal("identity helper missing")
	}
	end := strings.Index(behavior[start:], "  const overlayHeader =")
	if end < 0 {
		t.Fatal("identity helper missing")
	}
	helper := behavior[start : start+end]
	for _, want := range []string{`(name, projectID, iconURL, source = "Modrinth")`, `"plugins-icon-tile"`, `tile.dataset.tone`, `typeof projectID === "string"`, `/^[A-Za-z0-9]{1,64}$/`, `document.createElement("img")`, `image.alt = ""`, `image.hidden = true`, `const query = new URLSearchParams({ source, project_id: projectID })`, `query.set("icon_url", iconURL)`, `image.src = ` + "`/api/plugins/workspace/icon?${query}`", `image.addEventListener("error"`, `image.remove()`, `tile.classList.remove("has-logo")`, `tile.append(image)`} {
		if !strings.Contains(helper, want) {
			t.Fatalf("shared icon contract missing %s", want)
		}
	}
	workspace := behavior[start:strings.Index(behavior, "// Bookmarked standalone pages")]
	if strings.Count(workspace, `document.createElement("img")`) != 1 || strings.Count(workspace, `image.src =`) != 1 {
		t.Fatal("plugin images must use only the shared identity helper")
	}
	for _, unsafe := range []string{"cdn.modrinth.com", "api.modrinth.com", "image.src = project", "image.src = plugin"} {
		if strings.Contains(workspace, unsafe) {
			t.Fatalf("upstream icon leaked into frontend: %s", unsafe)
		}
	}
	for _, want := range []string{
		`identity.append(identityTile(name, projectID, iconURL, source), text("h3", name))`,
		`header.append(identityTile(name, projectID, iconURL, source), copy)`,
		`const iconProjectID = ["Modrinth", "Hangar"].includes(project.source) ? project.project_id : undefined;`,
		`overlayHeader(project.title, project.description, identityBadges, iconProjectID, project.installed && !managed ? installedStatus() : null, project.icon_url || "", project.source)`,
		`}, iconProjectID, project.icon_url || "", project.source)`,
		`overlayHeader(plan.project_name, plan.description || project?.description, [[plan.version_number, "plugins-badge-version"], [plan.server.server_software || plan.server.server_type,`,
		`const projectID = plugin.project_id || plugin.icon_project_id || undefined`,
		`overlayHeader(name, "", [[plugin.version || "Version unknown", "plugins-badge-version"], management, [plugin.management === "plugins" && "Installed", "plugins-badge-installed"], [stateLabel, stateClass], [attention && "Needs attention", "plugins-badge-attention"]], projectID, plugin.management === "plugins" ? installedStatus() : null, undefined, plugin.source || "Modrinth")`,
		`}, projectID, undefined, plugin.source || "Modrinth")`,
		`operationIdentity?.operation_id === operation.operation_id`,
		`project_id: plan.project_id, name: plan.project_name`,
		`overlayHeader(identity?.name || heading, "", [[identity?.version, "plugins-badge-version"]], identity?.project_id, null, undefined, identity?.source || "Modrinth")`,
	} {
		if !strings.Contains(workspace, want) {
			t.Fatalf("identity reuse missing %s", want)
		}
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`width:40px;height:40px`, `.plugins-icon-tile{position:relative;overflow:hidden}`, `.plugins-icon-tile img{position:absolute;inset:0;width:100%;height:100%;object-fit:contain`, `.plugins-icon-tile.has-logo{color:transparent}`} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("fixed logo geometry missing %s", want)
		}
	}
}

func TestPluginsWorkspaceApprovedCompactHierarchyAndStageFlow(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	// Fail clearly if renderer boundaries change rather than slicing invalid indices.
	between := func(start, end string) string {
		t.Helper()
		a := strings.Index(script, start)
		if a < 0 {
			t.Fatal("missing boundary", start)
		}
		b := strings.Index(script[a:], end)
		if b < 0 {
			t.Fatal("missing boundary", end)
		}
		return script[a : a+b]
	}
	header := between("  const interactiveCard =", "  const infoRows =")
	for _, want := range []string{`copy.className = "plugins-card-copy"`, `copy.append(heading)`, `card.querySelector(".plugins-card-description, .plugins-card-release")`, `copy.append(description)`, `header.append(identityTile(name, projectID, iconURL, source), copy)`} {
		if !strings.Contains(header, want) {
			t.Fatal("identity/header hierarchy missing", want)
		}
	}
	if strings.Contains(header, `card.append(actions)`) {
		t.Fatal("actions detached from compact card header")
	}
	collapsed := between("  const renderProject =", "    const renderInfo =")
	for _, want := range []string{`server.server_software || server.server_type`, `const identityBadges = [[version?.version_number, "plugins-badge-version"], [platform, "plugins-badge-server"],`, `server.minecraft_version`, `identityBadges.filter(([value]) => value)`, `"plugins-badge-minecraft"`} {
		if !strings.Contains(collapsed, want) {
			t.Fatal("concise release/platform/MC badges missing", want)
		}
	}
	for _, forbidden := range []string{"project.project_id", "version.id", "version.loaders", "version.game_versions"} {
		if strings.Contains(collapsed, forbidden) {
			t.Fatal("raw identity or publisher lists leaked into collapsed Discover", forbidden)
		}
	}
	discover := between("  const renderProject =", "  const renderInstalled =")
	if strings.Contains(discover, `...(version?.loaders || [])`) || !strings.Contains(discover, `overlayHeader(project.title, project.description, identityBadges, iconProjectID, project.installed && !managed ? installedStatus() : null, project.icon_url || "", project.source)`) {
		t.Fatal("Info header must reuse concise server-context badges")
	}
	for _, want := range []string{`compatibility("Supported platforms", (version?.loaders || []).join(" / ")`, `new Set(version?.game_versions || [])`} {
		if !strings.Contains(discover, want) {
			t.Fatal("complete Compatibility metadata lost", want)
		}
	}
	inventory := between("  const renderInstalled =", "  // Review, progress")
	if !strings.Contains(inventory, `const projectID = plugin.project_id || plugin.icon_project_id || undefined;`) || !strings.Contains(inventory, `}, projectID, undefined, plugin.source || "Modrinth")`) {
		t.Fatal("inventory must reuse only known authoritative project identity")
	}
	identity := between("  const identityTile =", "  const overlayHeader =")
	if strings.Index(identity, `document.createElement("img")`) < strings.Index(identity, `if (typeof projectID === "string"`) || !strings.Contains(identity, `image.remove()`) {
		t.Fatal("unidentified and failed icons must retain fallback")
	}
	review := between("  const reviewPlugin =", "  const resumePluginInstall =")
	for _, want := range []string{`overlaySection("About this version")`, `overlaySection("Compatibility")`, `overlaySection("Dependencies")`, `overlaySection("Files to install")`, `overlaySection("Installation")`, `overlaySection("Restart")`, `overlaySection("Players")`, `readableSize(artifact.size)`, `project?.version?.id === plan.version_id`, `about("Source", plan.source)`} {
		if !strings.Contains(review, want) {
			t.Fatal("single-column overlay structure missing", want)
		}
	}

	progress := between("  const showOperation =", "  const pollOperation =")
	if strings.Count(progress, `operation.status`) != 1 || !strings.Contains(progress, `index !== undefined && position < index`) || !strings.Contains(progress, `position === index`) || !strings.Contains(progress, `})[operation.stage]`) {
		t.Fatal("progress must derive real states from the persisted stage and render status once")
	}
	rawCSS, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(rawCSS)
	for _, want := range []string{`.plugins-install-stages{display:flex;flex-direction:column;align-items:flex-start;`, `.plugins-install-stages li::before{content:"○"`, `.plugins-install-stages .is-complete::before{content:"✓"`, `.plugins-install-stages [aria-current="step"]::before{content:"●"`} {
		if !strings.Contains(css, want) {
			t.Fatal("compact responsive layout missing", want)
		}
	}
}

func TestPluginsInventoryForwardsVerifiedOptionalIdentity(t *testing.T) {
	client := &fakePluginsWorkspaceAPI{}
	client.role = "administrator"
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"installed", "updates"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/"+route, ""))
		if rr.Code != http.StatusOK {
			t.Fatal(route, rr.Code)
		}
		var inventory api.PluginInventory
		if err := json.Unmarshal(rr.Body.Bytes(), &inventory); err != nil {
			t.Fatal(err)
		}
		if len(inventory.Plugins) != 3 {
			t.Fatal("inventory changed", inventory)
		}
		manual, managed := inventory.Plugins[0], inventory.Plugins[1]
		if manual.Management != "manual" || manual.ProjectID != "" || manual.VersionID != "" || manual.IconProjectID != "" {
			t.Fatal("manual identity inferred", manual)
		}
		if managed.Management != "plugins" || managed.ProjectID != "project123" || managed.VersionID != "release123" {
			t.Fatal("verified identity lost", managed)
		}
		component := inventory.Plugins[2]
		if component.Management != "justvoxel" || component.IconProjectID != "bWrNNfkb" || component.ProjectID != "" || component.VersionID != "" {
			t.Fatal("display identity changed ownership", component)
		}
		var wire struct {
			Plugins []map[string]json.RawMessage `json:"plugins"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		if _, exists := wire.Plugins[0]["icon_project_id"]; exists {
			t.Fatal("manual display identity must be omitted")
		}
		if _, exists := wire.Plugins[0]["project_id"]; exists {
			t.Fatal("unidentified project ID must be omitted")
		}
		if _, exists := wire.Plugins[0]["version_id"]; exists {
			t.Fatal("unidentified release ID must be omitted")
		}
	}
}

func TestPluginsWorkspaceBoundedDiscoverQuery(t *testing.T) {
	for _, tc := range []struct {
		query    string
		accepted bool
	}{
		{"", true}, {"limit=1", true}, {"limit=12", true}, {"limit=13", true}, {"limit=24", true},
		{"limit=0", false}, {"limit=25", false}, {"limit=abc", false},
		{"limit=1.5", false}, {"limit=", false}, {"limit=%2B1", false},
		{"modrinth=true&hangar=false", true}, {"modrinth=false&hangar=true", true}, {"modrinth=true&hangar=true", true}, {"modrinth=false&hangar=false", true},
		{"modrinth_offset=10000&hangar_offset=10000&hangar_first=true", true},
		{"hangar=", false}, {"modrinth=1", false}, {"hangar=true&hangar=false", false}, {"modrinth=true&modrinth=true", false},
		{"hangar_offset=10001", false}, {"modrinth_offset=-1", false}, {"hangar_first=1", false}, {"hangar_offset=", false}, {"modrinth_offset=1&modrinth_offset=2", false}, {"offset=0", false},
		{"limit=1&limit=12", false}, {"source=hangar", false}, {"url=https://example.com", false}, {"limit=1&bad=%xx", false}, {"server=jv-a1b2c3&limit=1&bad=%xx", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			client := &fakePluginsWorkspaceAPI{}
			client.role = "administrator"
			app, err := New(client, Config{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/discover?"+tc.query, ""))
			if tc.accepted {
				if rr.Code != http.StatusOK || client.catalogCalls != 1 {
					t.Fatal("catalog query rejected", rr.Code)
				}
				values, _ := url.ParseQuery(tc.query)
				if client.query.Encode() != values.Encode() {
					t.Fatal("limit was not forwarded")
				}
			} else if rr.Code != http.StatusBadRequest || client.catalogCalls != 0 {
				t.Fatal("invalid catalog query forwarded", rr.Code)
			}
		})
	}
}

func TestPluginsWorkspaceToolbarCapacityAndPagination(t *testing.T) {
	rawHeader, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	rawJS, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	rawCSS, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	header, js, css := string(rawHeader), string(rawJS), string(rawCSS)
	start := strings.Index(js, "// Plugin workspace.")
	end := strings.Index(js, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("plugin workspace missing")
	}
	js = js[start:end]
	for _, want := range []string{`aria-label="Search plugins"`, `placeholder="Search plugins"`, `role="switch" name="modrinth" data-plugins-modrinth checked`, `role="switch" name="hangar" data-plugins-hangar>Hangar`, `>Compatible only`, `>Release only`} {
		if !strings.Contains(header, want) {
			t.Fatal("toolbar control missing", want)
		}
	}
	for _, forbidden := range []string{`>Search plugins<input`, `data-plugins-source`, `Source: Modrinth`} {
		if strings.Contains(header, forbidden) {
			t.Fatal("visible toolbar text remains", forbidden)
		}
	}
	for _, want := range []string{`limit: String(pageLimit)`, `results.getBoundingClientRect()`, `style.gridTemplateColumns`, `results.clientHeight`, `style.getPropertyValue("--plugins-card-height")`, `columns * rows`, `Math.min(24`, `new ResizeObserver`, `setTimeout(() => updateCapacity(), 200)`, `resetCursors();`, `pageIndex = 0`, `const currentPage = pageIndex + 1`, `totalPages = Math.max(1, Math.ceil(payload.total / pageLimit))`, "pageLabel.textContent = `Page ${currentPage} of ${totalPages}`", `cursor = pageCursors[--pageIndex]`, `pageCursors[pageIndex + 1] = nextCursor`, `event.preventDefault()`} {
		if !strings.Contains(js, want) {
			t.Fatal("dynamic catalog behavior missing", want)
		}
	}
	for _, forbidden := range []string{`Catalog page`, `offset / 8`, `offset - 8`, `hangar.com`} {
		if strings.Contains(js, forbidden) {
			t.Fatal("obsolete or fake catalog behavior", forbidden)
		}
	}
	page := strings.Index(header, "<span data-plugins-page>")
	previous := strings.Index(header, "data-plugins-previous")
	next := strings.Index(header, "data-plugins-next")
	if page < 0 || previous <= page || next <= previous {
		t.Fatal("pagination order changed")
	}
	for _, want := range []string{`display:flex;flex-direction:column;gap:.55rem;flex:1`, `.plugins-search{display:flex;flex:none;align-items:center;flex-wrap:nowrap`, `flex:1 1 120px`, `.plugins-search-filters{display:flex;flex:none;align-items:center;gap:.5rem;margin-left:auto}`, `--plugins-card-height:144px`, `height:var(--plugins-card-height)`, `.plugins-result-card.is-selected{border-color:var(--jv-success)`, `color-mix(in srgb,var(--jv-success) 18%,var(--jv-surface-sunken))`, `.plugins-result-card:focus-visible`, `justify-content:flex-end;gap:.6rem;margin-top:auto`, `@container(max-width:680px){.plugins-search{flex-wrap:wrap}`} {
		if !strings.Contains(css, want) {
			t.Fatal("approved layout rule missing", want)
		}
	}
	for _, selector := range []string{".plugins-search", ".plugins-results", ".plugins-pagination"} {
		start := strings.Index(css, selector+"{")
		if start < 0 {
			t.Fatal("plugin lane missing", selector)
		}
		rule := css[start : start+strings.Index(css[start:], "}")+1]
		for _, want := range []string{"width:100%;", "max-width:1800px;", "margin-inline:auto"} {
			if !strings.Contains(rule, want) {
				t.Fatal("shared centered plugin lane rule missing", selector, want)
			}
		}
		for _, forbidden := range []string{"scrollbar-gutter", "padding"} {
			if strings.Contains(rule, forbidden) {
				t.Fatal("plugin lane reserves unwanted gutter or padding", selector, forbidden)
			}
		}
		if selector == ".plugins-results" && !strings.Contains(rule, "overflow:auto") {
			t.Fatal("plugin results scrolling missing")
		}
	}
	for _, forbidden := range []string{`plugins-review-layout`, `plugins-review-primary`, `plugins-review-secondary`, `plugins-card-actions`, `min-height:230px`} {
		if strings.Contains(css, forbidden) {
			t.Fatal("rejected layout remains", forbidden)
		}
	}
}

func TestPluginCorrectionReviewAndCursorStructure(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start, end := strings.Index(script, "// Plugin workspace."), strings.Index(script, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("plugin workspace missing")
	}
	script = script[start:end]
	for _, forbidden := range []string{`offset - pageLimit`, `Math.floor(offset / pageLimit)`, `Publisher-supported`, `versions.forEach`, `dependency.dependency_type !== "required"`, `innerHTML`, `insertAdjacentHTML`, `cdn.modrinth.com`, `plugins-review-layout`, `plugins-review-primary`, `plugins-review-secondary`, `plugins-review-install-action`} {
		if strings.Contains(script, forbidden) {
			t.Fatal("rejected behavior remains", forbidden)
		}
	}
	for _, want := range []string{`pageCursors = [firstCursor()]`, `previous.disabled = pageIndex === 0`, `next.disabled = !nextCursor`, `nextCursor = payload.next_cursor`, `pageCursors[pageIndex + 1] = nextCursor`, `cursor = pageCursors[--pageIndex]`, `const currentPage = pageIndex + 1`, `resetCursors();`, `form.elements.q.addEventListener("input"`, `readOnlyReview("The publisher lists Paper support, but this JAR is not a direct Paper/Bukkit plugin. It may require an external loader or a different installation path, so JustVoxel will not place it in the plugins directory.")`, `renderInfo();`, `"Supported platforms"`, `"Supported versions"`, `versions.join(" / ")`, `"plugins-publisher-dependency-row"`, `values.join(" / ")`, `text("li", artifact.filename, "plugins-file-row")`, `readableSize(artifact.size)`, `overlayActions(panelAction("Done", closePanel, true))`, `overlayActions(panelAction("Close", closePanel))`} {
		if !strings.Contains(script, want) {
			t.Fatal("correction missing", want)
		}
	}
	reviewStart, reviewEnd := strings.Index(script, "  const reviewPlugin ="), strings.Index(script, "  const resumePluginInstall =")
	review := script[reviewStart:reviewEnd]
	if strings.Contains(review, `panelAction("Cancel"`) {
		t.Fatal("installation correction review must retain its existing close actions")
	}
	identity := strings.Index(review, `overlayHeader(plan.project_name`)
	install := strings.Index(review, `panelAction("Install"`)
	about := strings.Index(review, `overlaySection("About this version")`)
	if identity < 0 || install <= identity || about <= install {
		t.Fatal("Install must be passed to the identity header before details")
	}
	headerEnd := strings.Index(review[identity:], "\n") + identity
	if headerEnd < install || !strings.Contains(review[identity:headerEnd], `plan.project_id, panelAction("Install", () => applyPlugin(plan))`) {
		t.Fatal("Install must be the optional action passed within overlayHeader")
	}
	if strings.Count(review, `panelAction("Install"`) != 1 || strings.Contains(review, `overlayActions(panelAction("Install"`) {
		t.Fatal("Install must only appear in the successful identity header")
	}
	if strings.Index(review, `pluginRequest("install/plan"`) >= install {
		t.Fatal("Install shown before plan review")
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`.plugins-overlay-actions{display:flex;flex-wrap:wrap;justify-content:flex-end`, `.plugins-file-row{display:flex;align-items:baseline;justify-content:space-between`, `.plugins-info-row{display:flex;align-items:baseline`, `width:min(100%,760px);max-height:100%;min-width:0;overflow:auto;overscroll-behavior:contain;overflow-wrap:anywhere;padding:.75rem;`, `.plugins-info-rows{display:flex;flex-wrap:wrap;gap:.12rem 1rem;`, `.plugins-info-row dt{flex:none}`, `.plugins-publisher-dependency-row{display:flex;align-items:baseline;gap:.65rem;margin:.15rem 0}`, `@container(max-width:700px){.plugins-search-filters{flex-wrap:wrap}.plugins-file-row{flex-wrap:wrap}.plugins-publisher-dependency-row{flex-wrap:wrap}}`, `@container(max-width:450px){.plugins-info-row{flex-wrap:wrap}.plugins-info-row dt{flex-basis:100%}}`} {
		if !strings.Contains(string(css), want) {
			t.Fatal("compact overlay rule missing", want)
		}
	}
	for _, forbidden := range []string{`overflow:visible;overscroll-behavior:contain`, `@container(max-height:650px)`, `.plugins-install-panel{container-type:size}`, `.plugins-info-rows{display:grid`, `.plugins-info-rows{grid-template-columns:`, `plugins-review-install-action`} {
		if strings.Contains(string(css), forbidden) {
			t.Fatal("unsafe or stale overlay rule remains", forbidden)
		}
	}
	if strings.Count(string(css), `.plugins-info-rows{`) != 1 {
		t.Fatal("info rows must have one clear flex rule")
	}
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(header), `class="plugins-search-filters"`) {
		t.Fatal("source/filter group missing")
	}
}

func TestPluginWorkspaceDirectReviewRejectionIsForwardedWithoutInstall(t *testing.T) {
	message := "This release is not a direct Paper/Bukkit plugin and cannot be installed into the plugins directory."
	client := &fakePluginsInstallAPI{planError: &api.ResponseError{StatusCode: http.StatusConflict, Message: message}}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/install/plan", "csrf=csrf-token&project_id=root&version_id=release"))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), message) {
		t.Fatalf("review rejection lost: %d %s", rr.Code, rr.Body.String())
	}
	if client.plans != 1 || client.installs != 0 || client.operations != 0 {
		t.Fatal("rejected review started install or operation")
	}
}

// Source assertions protect the inventory gate without claiming runtime acceptance.
func TestPluginsWorkspaceInventoryRequiresVerifiedOperationState(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start := strings.Index(script, "  const pluginOperationSettled =")
	if start < 0 {
		t.Fatal("inventory safety helper missing")
	}
	end := strings.Index(script[start:], "\n")
	if end < 0 {
		t.Fatal("inventory safety helper boundary missing")
	}
	helper := script[start : start+end]
	if helper != `  const pluginOperationSettled = (operation) => !operation || ["succeeded", "rolled_back", "resolved"].includes(operation.state);` {
		t.Fatal("inventory safety must allow no operation or only proven safe final states")
	}
	for _, tc := range []struct {
		state string
		safe  bool
	}{
		{"succeeded", true}, {"rolled_back", true}, {"resolved", true},
		{"queued", false}, {"validating", false}, {"downloading", false},
		{"player_check", false}, {"backup", false}, {"installing", false},
		{"restarting", false}, {"verifying", false}, {"failed", false},
		{"rolling_back", false}, {"needs_attention", false}, {"running", false},
		{"unknown", false}, {"", false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			if allowed := strings.Contains(helper, `"`+tc.state+`"`); allowed != tc.safe {
				t.Fatal("incorrect inventory state allowlist", tc.state)
			}
		})
	}
}

func TestPluginsWorkspaceInventoryGatePrecedesFetchAndRendering(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start := strings.Index(script, "  const load =")
	if start < 0 {
		t.Fatal("inventory gate missing")
	}
	end := strings.Index(script[start:], "  const discoverCapacity =")
	if end < 0 {
		t.Fatal("inventory loader boundary missing")
	}
	loader := script[start : start+end]
	gate := `      if (tab !== "discover") {
        const installation = await pluginRequest("operation", null, controller.signal);
        if (current !== generation) return;
        if (!pluginInventorySafe(installation.operation)) {
          state.textContent = "Plugin operation is still being verified or recovered. Installed inventory will be available after the operation reaches a verified final state.";
          inventoryTimer = setTimeout(() => recheckBlockedInventory(current), 1500);
          return;
        }
      }`
	check := strings.Index(loader, gate)
	clear := strings.Index(loader, "results.replaceChildren();")
	fetch := strings.Index(loader, "const response = await fetch(globalThis.justVoxelSelectedURL(`/api/plugins/workspace/${tab}")
	render := strings.Index(loader, `items.forEach((item) => results.append(tab === "discover" ? renderProject(item, server) : renderInstalled(item)))`)
	if clear < 0 || check <= clear || fetch <= check+len(gate) || render <= fetch {
		t.Fatal("Installed/Updates must clear stale cards and check operation before inventory fetch/render; Discover must bypass the check")
	}
	if strings.Count(loader, "await fetch(") != 1 || strings.Count(loader, `pluginRequest("operation"`) != 1 {
		t.Fatal("inventory must not bypass the operation gate")
	}
	for _, forbidden := range []string{"filename", "timestamp", "mtime", "birthtime", "adopt", "manual", "disabled = true"} {
		// Pagination disabling is existing Discover behavior; only inspect the new gate.
		if strings.Contains(gate, forbidden) {
			t.Fatal("inventory gate must use operation state without ownership heuristics or disabling controls", forbidden)
		}
	}
	for _, want := range []string{
		`pluginsDialog.querySelector("[data-plugins-refresh]").addEventListener("click", () => { resetCursors(); load(); })`,
		`pluginsDialog.querySelectorAll("[data-plugins-tab]").forEach((button) => button.addEventListener("click"`,
		`load(); resumePluginInstall();`,
	} {
		if !strings.Contains(script, want) {
			t.Fatal("inventory gate removed refresh, tabs or install resume", want)
		}
	}
}

func TestPluginsWorkspaceVMCorrectionHeaderButtonsAndBackdrop(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start, end := strings.Index(script, "// Plugin workspace."), strings.Index(script, "// Bookmarked standalone pages")
	if start < 0 || end <= start {
		t.Fatal("plugin workspace missing")
	}
	script = script[start:end]
	between := func(first, next string) string {
		t.Helper()
		a, b := strings.Index(script, first), strings.Index(script, next)
		if a < 0 || b <= a {
			t.Fatal("section missing", first, next)
		}
		return script[a:b]
	}
	header := between("  const overlayHeader =", "  const overlaySection =")
	for _, want := range []string{`projectID, action = null`, `identity.append(identityTile(name, projectID, iconURL, source), text("h3", name));`, `if (action) identity.append(action);`, `header.append(identity);`} {
		if !strings.Contains(header, want) {
			t.Fatal("shared top identity action missing", want)
		}
	}
	if strings.Index(header, `if (action)`) >= strings.Index(header, `header.append(identity)`) || strings.Index(header, `header.append(identity)`) >= strings.Index(header, `if (description)`) {
		t.Fatal("identity action must precede description and badges")
	}
	info := between("    const renderInfo =", "    // Publisher data uses textContent")
	if strings.Contains(info, `panelAction("Install"`) || strings.Contains(info, `applyPlugin(`) {
		t.Fatal("read-only information exposes Install")
	}
	for _, want := range []string{`showPanel(project.title, true)`, `renderInfo();`, `overlayActions(panelAction("Close", closePanel))`} {
		if !strings.Contains(info, want) {
			t.Fatal("read-only view missing", want)
		}
	}
	panel := between("  let operationIdentity =", "  const panelAction =")
	for _, want := range []string{`let overlayDismissible = false`, `dismissible = false`, `overlayDismissible = dismissible`, `overlayDismissible = false`, `returnFocus?.isConnected`, `returnFocus.focus()`} {
		if !strings.Contains(panel, want) {
			t.Fatal("overlay state/focus missing", want)
		}
	}
	click := between(`  panel.addEventListener("click"`, `  panel.addEventListener("keydown"`)
	if !strings.Contains(click, `if (event.target === panel && overlayDismissible) closePanel();`) || strings.Count(click, "closePanel()") != 1 || strings.Contains(click, "contains(") || strings.Contains(click, "closest(") {
		t.Fatal("backdrop must require direct target and dismissible state")
	}
	for _, want := range []string{"showPanel(`Info for ${name}`, true)", `event.key === "Escape"`, `event.stopPropagation(); closePanel();`} {
		if !strings.Contains(script, want) {
			t.Fatal("passive info / Esc behavior missing", want)
		}
	}
	review := between("  const reviewPlugin =", "  const resumePluginInstall =")
	if strings.Count(review, `showPanel("Plugin installation", true)`) != 2 {
		t.Fatal("review preparation and review must allow backdrop dismissal")
	}
	for _, area := range []string{between("  const showOperation =", "  const pollOperation ="), between("  const applyPlugin =", "  const readableSize ="), between("  const panelError =", "  const showOperation =")} {
		if (!strings.Contains(area, `showPanel();`) && !strings.Contains(area, `showPanel(lifecycle ? "Plugin management" : "Plugin installation", false, sameOperation);`)) || strings.Contains(area, `showPanel("Plugin installation", true)`) || strings.Contains(area, "overlayDismissible = true") {
			t.Fatal("operation rendering or submission allowed backdrop dismissal")
		}
	}
	rawCSS, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(rawCSS)
	for _, want := range []string{
		`.workspace-window .workspace-header-close{width:36px;height:36px;padding:0;font-size:inherit}`,
		`:where(.workspace-window) :is(button,.button-link){padding:.5rem .75rem;border-radius:8px;line-height:1.25}`,
		`.plugins-overlay-identity h3{flex:1;margin:0;min-width:0}`,
		`.plugins-install-content .plugins-overlay-identity button{flex:none;margin:0 0 0 auto}`,
		`.plugins-pagination button{flex:none;margin:0}`,
		`.plugins-search button{flex:none;margin:0}`,
	} {
		if !strings.Contains(css, want) {
			t.Fatal("unified plugin geometry missing", want)
		}
	}
	for _, rule := range strings.Split(css, "}") {
		selector, declarations, ok := strings.Cut(rule, "{")
		if !ok || !strings.Contains(selector, ".plugins-") || !strings.Contains(selector, "button") {
			continue
		}
		if strings.Contains(declarations, "height:36px") || strings.Contains(declarations, "height:42px") {
			t.Fatal("Plugins button height override remains", rule)
		}
	}
	// Static toolbar buttons and dynamically created actions all use the same
	// button selector; cards and filters remain different element types.
	rawHeader, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<button class="system-monitor-header-button workspace-header-refresh" type="button" data-plugins-refresh`,
		`<button class="system-monitor-header-button workspace-header-close" type="button" data-plugins-close`,
		`<button type="button" data-plugins-tab="discover"`,
		`<button type="button" data-plugins-tab="installed"`,
		`<button type="button" data-plugins-previous`,
		`<button type="button" data-plugins-next`,
	} {
		if !strings.Contains(string(rawHeader), want) {
			t.Fatal("Plugins toolbar must use actual buttons covered by the shared rule", want)
		}
	}
	for _, want := range []string{
		`const button = text("button", label, secondary ? "secondary" : "");`,
		`panelAction("Install"`, `panelAction("Close"`, `panelAction("Done"`,
		`panelAction("View in Installed"`,
		`panelAction("Re-check recovery"`,
		`const card = document.createElement("article");`,
	} {
		if !strings.Contains(script, want) {
			t.Fatal("Plugins actions must share button geometry while cards remain articles", want)
		}
	}
	for _, want := range []string{
		`.plugins-result-card{display:flex;flex-direction:column;height:var(--plugins-card-height);`,
		`.plugins-filter input{appearance:none;position:relative;flex:none;width:28px;height:16px;`,
		`.plugins-search-field input{width:100%;margin-bottom:0}`,
	} {
		if !strings.Contains(css, want) {
			t.Fatal("card and input geometry must remain independent of button sizing", want)
		}
	}
}

func pluginWorkspaceScript(t *testing.T) string {
	t.Helper()
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	a, b := strings.Index(s, "// Plugin workspace."), strings.Index(s, "// Bookmarked standalone pages")
	if a < 0 || b <= a {
		t.Fatal("plugin workspace missing")
	}
	return s[a:b]
}

func pluginScriptSection(t *testing.T, script, first, next string) string {
	t.Helper()
	a, b := strings.Index(script, first), strings.Index(script, next)
	if a < 0 || b <= a {
		t.Fatal("plugin section missing", first, next)
	}
	return script[a:b]
}

func TestPluginsWorkspaceApprovedLiveSearchAndSharedTabs(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(header), `<button type="submit">Search</button>`) {
		t.Fatal("visible Search button remains")
	}
	raw, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	shared := `.workspace-window :is(.minecraft-workspace-tabs,.system-workspace-tabs,.migration-workspace-tabs,.version-workspace-tabs,.plugins-workspace-tabs) button{min-height:34px;padding:.38rem .7rem;border-radius:8px;font-size:.86rem}`
	if !strings.Contains(css, shared) {
		t.Fatal("Plugins tabs must use the existing shared geometry")
	}
	if strings.Contains(css, `.workspace-window.plugins-workspace-dialog button{`) {
		t.Fatal("Plugins-wide button override remains")
	}
	script := pluginWorkspaceScript(t)
	input := pluginScriptSection(t, script, `  form.elements.q.addEventListener("input"`, `  form.addEventListener("submit"`)
	for _, want := range []string{`clearTimeout(searchTimer)`, `++generation`, `controller?.abort()`, `resetCursors()`, `searchTimer = setTimeout(() => load(), 250)`} {
		if !strings.Contains(input, want) {
			t.Fatal("live debounced search missing", want)
		}
	}
	submit := pluginScriptSection(t, script, `  form.addEventListener("submit"`, `  previous.addEventListener`)
	if !strings.Contains(submit, `event.preventDefault(); clearTimeout(searchTimer); resetCursors(); load();`) {
		t.Fatal("form submit/Enter must load immediately")
	}
	if !strings.Contains(submit, `"change", () => { clearTimeout(searchTimer); resetCursors(); load(); }`) {
		t.Fatal("filters must reload immediately")
	}
	if !strings.Contains(script, `onClose: () => { clearTimeout(capacityTimer); clearTimeout(searchTimer); clearInventoryTimer();`) {
		t.Fatal("workspace close must clear timers")
	}
}

func TestPluginsWorkspaceStableOperationIdentity(t *testing.T) {
	script := pluginWorkspaceScript(t)
	operation := pluginScriptSection(t, script, "  const showOperation =", "  const pollOperation =")
	for _, want := range []string{`const sameOperation = !panel.hidden && operationViewID === operation.operation_id`, `showPanel(lifecycle ? "Plugin management" : "Plugin installation", false, sameOperation)`, `if (!sameOperation) {`, `operationViewID = operation.operation_id`} {
		if !strings.Contains(operation, want) {
			t.Fatal("operation identity lifecycle missing", want)
		}
	}
	header := strings.Index(operation, "overlayHeader(")
	guard := strings.Index(operation, "if (!sameOperation) {")
	end := strings.Index(operation[guard:], "} else") + guard
	if header < guard || header >= end || strings.Count(operation, "overlayHeader(") != 1 {
		t.Fatal("operation header must be created only for a new identity view")
	}
	panel := pluginScriptSection(t, script, "  const showPanel =", "  const panelAction =")
	if !strings.Contains(panel, `Array.from(panelBody.children).slice(1).forEach((node) => node.remove())`) || !strings.Contains(panel, `if (preserveIdentity) {`) {
		t.Fatal("polling must preserve the header and logo")
	}
	close := pluginScriptSection(t, script, "  const closePanel =", "  const showPanel =")
	if !strings.Contains(panel, `operationViewID = null;`) || !strings.Contains(close, `panel.hidden = true;`) || !strings.Contains(close, `operationViewID = null;`) {
		t.Fatal("new operations and reopened views must replace identity")
	}
}

func TestPluginsWorkspaceOperationGatesAndInventoryRecheck(t *testing.T) {
	script := pluginWorkspaceScript(t)
	for _, area := range []struct{ first, next, request string }{
		{"  const reviewPlugin =", "  const resumePluginInstall =", `pluginRequest("install/plan"`},
		{"  const applyPlugin =", "  const readableSize =", `pluginRequest("install"`},
	} {
		section := pluginScriptSection(t, script, area.first, area.next)
		guard := strings.Index(section, `if (await blockPluginInstall(current)) return;`)
		if guard < 0 || strings.Index(section, area.request) <= guard {
			t.Fatal("active operation gate must precede request", area.request)
		}
	}
	resume := pluginScriptSection(t, script, "  const resumePluginInstall =", `  panel.addEventListener("click"`)
	if !strings.Contains(resume, `pluginOperationSettled(payload.operation)`) {
		t.Fatal("opening Plugins must skip settled operations")
	}
	recheck := pluginScriptSection(t, script, "  const recheckBlockedInventory =", "  const load =")
	for _, want := range []string{`inventoryTimer = null`, `!pluginsDialog.open || tab === "discover" || current !== generation`, `pluginRequest("operation", null, controller.signal)`, `if (pluginInventorySafe(payload.operation)) { load(); return; }`, `inventoryTimer = setTimeout(() => recheckBlockedInventory(current), 1500)`} {
		if !strings.Contains(recheck, want) {
			t.Fatal("automatic safe inventory return missing", want)
		}
	}
	loader := pluginScriptSection(t, script, "  const discoverQuery =", "  const discoverCapacity =")
	if !strings.Contains(loader, `clearInventoryTimer();`) {
		t.Fatal("safe inventory load must clear its timer")
	}
	tabs := pluginScriptSection(t, script, `  pluginsDialog.querySelectorAll("[data-plugins-tab]").forEach((button) => button.addEventListener`, `  form.elements.q.addEventListener`)
	if !strings.Contains(tabs, `clearInventoryTimer();`) {
		t.Fatal("switching tabs must clear inventory timer")
	}
}

// Execute the production gate/resume/recheck functions with controlled requests.
// These checks establish frontend behavior, not real-VM visual acceptance.
func TestPluginsWorkspaceOperationGateBehavior(t *testing.T) {
	script := pluginWorkspaceScript(t)
	settled := pluginScriptSection(t, script, "  const pluginOperationSettled =", "  let overlayDismissible =")
	gates := pluginScriptSection(t, script, "  const resumeActiveOperation =", "  const readableSize =")
	review := pluginScriptSection(t, script, "  const reviewPlugin =", "  const resumePluginInstall =")
	resume := pluginScriptSection(t, script, "  const resumePluginInstall =", `  panel.addEventListener("click"`)
	inventory := pluginScriptSection(t, script, "  const pluginInventorySafe =", "  const load =")
	program := `
const assert = require("node:assert/strict");
let operation = null, requests = [], shown = [], installGeneration = 0, installController = null, pollTimer = null, operationIdentity = null;
let generation = 1, tab = "installed", inventoryTimer = null, controller = new AbortController(), loads = 0, timers = new Map(), timerID = 0;
const setTimeout = (fn, delay) => { timers.set(++timerID, {fn, delay}); return timerID; };
const clearTimeout = (id) => timers.delete(id);
const pluginsDialog = {open:true}, panel = {hidden:true}, panelBody = {append(){}}, state = {textContent:""};
const text = () => ({}), overlayActions = () => {}, panelAction = () => ({}), showPanel = () => {};
const closePanel = () => {}, panelError = () => {}, pollOperation = () => {}, load = () => { clearInventoryTimer(); loads++; };
const stopPolling = () => { clearTimeout(pollTimer); installController?.abort(); ++installGeneration; };
const clearInventoryTimer = () => { clearTimeout(inventoryTimer); inventoryTimer = null; };
const showOperation = (op) => { shown.push(op.state); panel.hidden = false; return ["succeeded","rolled_back","resolved","needs_attention"].includes(op.state); };
const pluginRequest = async (route) => { requests.push(route); if (route !== "operation") throw new Error("stop after authorized request"); return {operation}; };
` + settled + gates + review + resume + inventory + `
(async () => {
 for (const state of [null,"succeeded","rolled_back","resolved","queued","validating","running","verifying","rolling_back","needs_attention","unknown",""]) {
  operation = state === null ? null : {operation_id:"current", state};
  const safe = state === null || ["succeeded","rolled_back","resolved"].includes(state);
  assert.equal(pluginOperationSettled(operation), safe);
  for (const invoke of [() => reviewPlugin("root","release"), () => applyPlugin({project_id:"root",version_id:"release"})]) {
   requests = []; shown = []; timers.clear(); panel.hidden = true;
   await invoke();
   assert.equal(requests[0], "operation");
   assert.equal(requests.length, safe ? 2 : 1);
   if (safe) assert(["install/plan","install"].includes(requests[1]));
   assert.deepEqual(shown, safe ? [] : [state]);
  }
  requests = []; shown = []; timers.clear(); panel.hidden = true;
  await resumePluginInstall();
  assert.deepEqual(shown, safe ? [] : [state]);
  if (state === "needs_attention") assert.equal(timers.size, 0);
 }
 for (const inventoryTab of ["installed"]) {
  tab = inventoryTab; pluginsDialog.open = true; operation = {state:"needs_attention"}; requests = []; timers.clear(); loads = 0;
  await recheckBlockedInventory(generation);
  assert.deepEqual(requests, ["operation"]); assert.equal(loads, 0); assert.equal(timers.size, 1);
  const timer = timers.get(inventoryTimer); timers.delete(inventoryTimer);
  operation = {state:"resolved"}; await timer.fn();
  assert.equal(loads, 1); assert.equal(timers.size, 0); assert.equal(inventoryTimer, null);
 }
 for (const closed of [false,true]) {
  requests = []; timers.clear(); pluginsDialog.open = !closed; tab = closed ? "installed" : "discover";
  await recheckBlockedInventory(generation);
  assert.equal(requests.length, 0); assert.equal(timers.size, 0);
 }
})().catch((error) => { console.error(error); process.exitCode = 1; });
`
	command := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("operation gates: %v\n%s", err, output)
	}
}

func TestPluginsWorkspaceOperationKeepsLogoDOM(t *testing.T) {
	script := pluginWorkspaceScript(t)
	panel := pluginScriptSection(t, script, "  const showPanel =", "  const panelAction =")
	operation := pluginScriptSection(t, script, "  const showOperation =", "  const pollOperation =")
	program := `
const assert = require("node:assert/strict");
class Node {
 constructor(tag = "div") { this.tag = tag; this.children = []; this.hidden = false; this.textContent = ""; this.isConnected = true; this.classList = {add() {}}; }
 get lastElementChild() { return this.children.at(-1); }
 append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
 replaceChildren(...nodes) { this.children = []; this.append(...nodes); }
 remove() { this.parent.children = this.parent.children.filter((node) => node !== this); }
 setAttribute() {} focus() {} contains() { return false; }
 querySelector() { return this.children[0].children[0]; }
 querySelectorAll() { return []; }
}
const document = {activeElement:null, createElement: (tag) => new Node(tag)};
const queueMicrotask = () => {};
const panel = new Node(), panelBody = new Node(), workspaceShell = {}, pluginsDialog = {querySelector: () => null};
panel.hidden = true;
let overlayDismissible = false, returnFocus = null, operationViewID = null, operationIdentity = null, headerCount = 0;
const text = (tag, value) => { const node = new Node(tag); node.textContent = value; return node; };
const overlayHeader = (name) => { const header = new Node("header"), title = text("h3",name), logo = new Node("img"); header.append(title,logo); panelBody.append(header); headerCount++; };
const overlayActions = (...buttons) => { const actions = new Node(); actions.append(...buttons); panelBody.append(actions); return actions; };
const panelAction = (label) => text("button",label), closePanel = () => {}, load = () => {};
` + panel + operation + `
for (const operation_type of ["plugin_install","plugin_enable","plugin_disable","plugin_remove"]) {
panel.hidden = true; headerCount = 0; operationViewID = null;
const op = {operation_type,operation_id:"one",state:"running",stage:"backup",status:"Backing up"};
showOperation(op);
const header = panelBody.children[0], image = header.children[1];
for (const stage of ["installing","restarting","verifying"]) {
 showOperation({...op,stage,status:stage});
 assert.equal(panelBody.children[0],header); assert.equal(panelBody.children[0].children[1],image);
}
showOperation({...op,state:"succeeded",status:"Verified"});
assert.equal(headerCount,1); assert.equal(panelBody.children[0],header);
showOperation({...op,operation_id:"two"});
assert.equal(headerCount,2); assert.notEqual(panelBody.children[0],header);
panel.hidden = true; showOperation({...op,operation_id:"two"});
assert.equal(headerCount,3);
}
`
	command := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("operation DOM lifecycle: %v\n%s", err, output)
	}
}

func TestPluginsWorkspaceLiveSearchBehavior(t *testing.T) {
	script := pluginWorkspaceScript(t)
	events := pluginScriptSection(t, script, `  form.elements.q.addEventListener("input"`, `  previous.addEventListener`)
	program := `
const assert = require("node:assert/strict");
const events = {}, formEvents = {}, filterEvents = [];
const form = {elements:{q:{addEventListener:(event,handler) => events[event] = handler}},addEventListener:(event,handler) => formEvents[event] = handler,querySelectorAll:() => [0,1,2,3].map((index) => ({addEventListener:(event,handler) => filterEvents[index] = handler}))};
let generation = 0, aborts = 0, resets = 0, loads = 0, searchTimer = null, timerID = 0;
const controller = {abort:() => aborts++}, previous = {}, next = {}, pageLabel = {};
const pluginsDialog = {querySelector:() => pageLabel}, resetCursors = () => resets++, load = () => loads++;
const timers = new Map(), setTimeout = (fn,delay) => { timers.set(++timerID,{fn,delay}); return timerID; }, clearTimeout = (id) => timers.delete(id);
` + events + `
events.input(); events.input(); events.input();
assert.equal(aborts,3); assert.equal(generation,3); assert.equal(resets,3); assert.equal(loads,0); assert.equal(timers.size,1);
assert.equal(timers.get(searchTimer).delay,250);
const timer = timers.get(searchTimer); timers.delete(searchTimer); timer.fn(); assert.equal(loads,1);
events.input(); let prevented = false;
formEvents.submit({preventDefault:() => prevented = true});
assert(prevented); assert.equal(timers.size,0); assert.equal(loads,2);
for (const filter of filterEvents) { events.input(); filter(); assert.equal(timers.size,0); }
assert.equal(loads,6);
`
	command := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("live plugin search: %v\n%s", err, output)
	}
}

func TestPluginWorkspaceApprovedPolish(t *testing.T) {
	script := pluginWorkspaceScript(t)
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(header), `data-plugins-tab="updates"`) || strings.Contains(script, `tab === "updates"`) || strings.Contains(script, `"Install another plugin"`) {
		t.Fatal("removed workspace actions remain")
	}
	if !strings.Contains(string(header), `data-plugins-tab="discover"`) || !strings.Contains(string(header), `data-plugins-tab="installed"`) {
		t.Fatal("supported tabs missing")
	}
	operation := pluginScriptSection(t, script, "  const showOperation =", "  const pollOperation =")
	success := pluginScriptSection(t, operation, "    if (success) {", "    if (attention) {")
	if strings.Count(success, "panelAction(") != 2 || !strings.Contains(success, `panelAction("View in Installed"`) || !strings.Contains(success, `}, true), panelAction("Done", () => { closePanel(); load(); }));`) || !strings.Contains(success, `pluginsDialog.querySelector('[data-plugins-tab="installed"]').click()`) {
		t.Fatal("success must offer secondary Installed and primary refreshing Done")
	}
	inventory := pluginScriptSection(t, script, "  const renderInstalled =", "  // Review, progress")
	for _, want := range []string{`plugin.update_status?.startsWith("Update available")`, `if (updateAvailable) summary = plugin.update_status;`, `if (attention) summary = "Needs attention"`, `status.classList.add("is-attention")`, `}, projectID, undefined, plugin.source || "Modrinth")`} {
		if !strings.Contains(inventory, want) {
			t.Fatal("Installed status/fallback lost", want)
		}
	}
	project := pluginScriptSection(t, script, "  const renderProject =", "  const renderInstalled =")
	for _, want := range []string{`iconProjectID, project.icon_url`, `iconProjectID, project.installed && !managed ? installedStatus() : null, project.icon_url`} {
		if !strings.Contains(project, want) {
			t.Fatal("Discover must supply validated search icon", want)
		}
	}
	review := pluginScriptSection(t, script, "  const reviewPlugin =", "  const resumePluginInstall =")
	for _, want := range []string{`project?.project_id === plan.project_id ? project.icon_url || "" : undefined`, `Loader-based server mod - not supported by the current version of JustVoxel Plugin installer. Should be available in future updates.`} {
		if !strings.Contains(review, want) {
			t.Fatal("Discover review explanation/icon missing", want)
		}
	}
}

func TestPluginsWorkspaceIconSearchURLValidationAndForwarding(t *testing.T) {
	client := &fakePluginsWorkspaceAPI{}
	client.role = "administrator"
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	const good = "https://cdn.modrinth.com/data/root/icon.png"
	request := func(query url.Values) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/icon?"+query.Encode(), ""))
		return rr
	}
	for _, raw := range []string{"", "http://cdn.modrinth.com/data/root/icon.png", "https://evil.example/data/root/icon.png", "https://cdn.modrinth.com/data/other/icon.png", "https://user@cdn.modrinth.com/data/root/icon.png", good + "?x=1", good + "?", good + "#x", good + "#", "https://cdn.modrinth.com/data/root/" + strings.Repeat("a", 2048) + ".png"} {
		if rr := request(url.Values{"project_id": {"root"}, "icon_url": {raw}}); rr.Code != 400 {
			t.Fatalf("unsafe icon accepted %q: %d", raw, rr.Code)
		}
	}
	for _, query := range []url.Values{
		{"project_id": {"root"}, "icon_url": {good, good}},
		{"project_id": {"root", "root"}, "icon_url": {good}},
		{"project_id": {"root"}, "icon_url": {good}, "unknown": {"x"}},
	} {
		if rr := request(query); rr.Code != 400 {
			t.Fatalf("unsafe query accepted: %d", rr.Code)
		}
	}
	if client.iconCalls != 0 {
		t.Fatal("unsafe icon forwarded to Agent")
	}
	if rr := request(url.Values{"project_id": {"root"}, "icon_url": {good}}); rr.Code != 200 || client.iconURL != good || client.iconProjectID != "root" || client.iconCalls != 1 {
		t.Fatal("validated Discover icon not forwarded")
	}
	client.iconURL = ""
	if rr := request(url.Values{"project_id": {"root"}}); rr.Code != 200 || client.iconURL != "" || client.iconCalls != 2 {
		t.Fatal("Installed project-only fallback broken")
	}
}

func TestPluginWorkspaceLoaderReviewRejectionIsForwardedWithoutInstall(t *testing.T) {
	message := "Loader-based server mod - not supported by the current version of JustVoxel Plugin installer. Should be available in future updates."
	client := &fakePluginsInstallAPI{planError: &api.ResponseError{StatusCode: http.StatusConflict, Message: message}}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/install/plan", "csrf=csrf-token&project_id=root&version_id=release"))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), message) || client.plans != 1 || client.installs != 0 || client.operations != 0 {
		t.Fatal("loader rejection lost or started mutation")
	}
}

func TestPluginIdentityTileUsesSameOriginForSearchAndInstalled(t *testing.T) {
	script := pluginWorkspaceScript(t)
	helper := pluginScriptSection(t, script, "  const identityTile =", "  const overlayHeader =")
	program := `
const assert = require("node:assert/strict");
const text = () => ({dataset:{}, setAttribute(){}, append(image){this.image=image;}, classList:{add(){},remove(){}}});
const document = {createElement:()=>({addEventListener(){}})};
` + helper + `
const icon = "https://cdn.modrinth.com/data/root/icon.png";
const discover = identityTile("Example", "root", icon);
const installed = identityTile("Example", "root");
for (const tile of [discover, installed]) {
 const url = new URL(tile.image.src, "https://appliance.example");
 assert.equal(url.origin, "https://appliance.example");
 assert.equal(url.pathname, "/api/plugins/workspace/icon");
 assert.equal(url.searchParams.get("project_id"), "root");
}
assert.equal(new URL(discover.image.src, "https://appliance.example").searchParams.get("icon_url"), icon);
assert.equal(new URL(installed.image.src, "https://appliance.example").searchParams.has("icon_url"), false);
assert.equal(identityTile("Manual", undefined).image, undefined);
assert.equal(new URL(identityTile("No search icon", "root", "").image.src, "https://appliance.example").searchParams.has("icon_url"), false);
`
	if output, err := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program).CombinedOutput(); err != nil {
		t.Fatalf("same-origin icon delivery: %v\n%s", err, output)
	}
}

func TestPluginsWorkspaceVerifiedRasterBoundary(t *testing.T) {
	for _, tc := range []struct {
		kind, data string
		want       int
	}{
		{"image/bmp", "BMfixture", 200},
		{"image/gif", "GIF89afixture", 200},
		{"image/svg+xml", "<svg/>", 502},
		{"image/png", "<svg/>", 502},
		{"image/gif", `<?xml version="1.0"?><svg/>`, 502},
		{"image/x-rgb", "\x01\xda", 502},
	} {
		t.Run(tc.kind+tc.data, func(t *testing.T) {
			client := &fakePluginsWorkspaceAPI{iconResult: &api.PluginIcon{ContentType: tc.kind, Data: []byte(tc.data)}}
			client.role = "administrator"
			app, err := New(client, Config{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/icon?project_id=root", ""))
			if rr.Code != tc.want || rr.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("unsafe icon boundary", rr.Code)
			}
			if tc.want == 200 && (rr.Header().Get("Content-Type") != tc.kind || rr.Body.String() != tc.data) {
				t.Fatal("verified image changed")
			}
		})
	}
}

func TestPluginsWorkspaceDocumentedIconExtensions(t *testing.T) {
	for _, extension := range []string{"png", "jpg", "jpeg", "bmp", "gif", "webp", "svg", "svgz", "rgb"} {
		if _, err := pluginIconURL("https://cdn.modrinth.com/data/root/icon."+extension, "root"); err != nil {
			t.Fatal(extension, err)
		}
	}
	for _, extension := range []string{"xml", "exe", "PNG"} {
		if _, err := pluginIconURL("https://cdn.modrinth.com/data/root/icon."+extension, "root"); err == nil {
			t.Fatal("unexpected extension", extension)
		}
	}
}

func TestPluginIdentityTileBoundedFallbackBehavior(t *testing.T) {
	helper := pluginScriptSection(t, pluginWorkspaceScript(t), "  const identityTile =", "  const overlayHeader =")
	program := `
const assert = require("node:assert/strict");
const text = (tag,value) => ({textContent:value,dataset:{},setAttribute(){},append(image){this.image=image;image.parent=this;},classList:{values:new Set(),add(v){this.values.add(v);},remove(v){this.values.delete(v);},contains(v){return this.values.has(v);}}});
const document = {createElement:()=>({requests:[],listeners:{},removed:false,addEventListener(name,fn){this.listeners[name]=fn;},set src(value){this.requests.push(value);this.source=value;},get src(){return this.source;},remove(){this.removed=true;},emit(name){this.listeners[name]();}})};
` + helper + `
const icon = "https://cdn.modrinth.com/data/root/icon.png";
const query = (image,index=0) => {
 const url = new URL(image.requests[index],"https://appliance.example");
 assert.equal(url.origin,"https://appliance.example");
 assert.equal(url.pathname,"/api/plugins/workspace/icon");
 assert.equal(url.searchParams.get("project_id"),"root");
 return url.searchParams;
};
const first = identityTile("Example","root",icon), image = first.image;
assert.equal(first.textContent,"EX"); assert.equal(image.hidden,true);
assert.equal(query(image).get("icon_url"),icon);
image.emit("error");
assert.equal(image.requests.length,2); assert.equal(query(image,1).has("icon_url"),false);
assert.equal(image.hidden,true); assert.equal(first.classList.contains("has-logo"),false);
image.emit("error"); assert.equal(image.removed,true); assert.equal(first.textContent,"EX");
for(let i=0;i<10;i++) image.emit("error");
assert.equal(image.requests.length,2); assert.equal(first.classList.contains("has-logo"),false);
for (const value of ["",undefined]) {
 const tile = identityTile("Example","root",value), img = tile.image;
 assert.equal(query(img).has("icon_url"),false); assert.equal(img.requests.length,1);
 img.emit("error"); assert.equal(img.removed,true); img.emit("error"); assert.equal(img.requests.length,1);
}
const success = identityTile("Example","root",icon), loaded = success.image;
loaded.emit("load"); assert.equal(loaded.hidden,false); assert.equal(success.classList.contains("has-logo"),true);
assert.equal(loaded.requests.length,1); loaded.emit("error"); assert.equal(loaded.requests.length,1);
const fallback = identityTile("Example","root",icon), retried = fallback.image;
retried.emit("error"); retried.emit("load");
assert.equal(retried.requests.length,2); assert.equal(retried.hidden,false);
assert.equal(fallback.classList.contains("has-logo"),true); assert.equal(retried.removed,false);
const hangarTile=identityTile("Hangar","123","https://evil.example/icon.png","Hangar"), hangarImage=hangarTile.image;
const hangarURL=new URL(hangarImage.src,"https://appliance.example");assert.equal(hangarURL.searchParams.get("source"),"Hangar");assert.equal(hangarURL.searchParams.get("project_id"),"123");assert(!hangarURL.searchParams.has("icon_url"));
hangarImage.emit("error");assert.equal(hangarImage.removed,true);assert.equal(hangarTile.textContent,"HA");assert.equal(hangarImage.requests.length,1);assert(!hangarTile.classList.contains("has-logo"));
assert.equal(identityTile("Manual",undefined).image,undefined);
`
	if output, err := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program).CombinedOutput(); err != nil {
		t.Fatalf("icon fallback: %v\n%s", err, output)
	}
}

func TestPluginsWorkspaceCardSelectionLifecycle(t *testing.T) {
	script := pluginWorkspaceScript(t)
	card := pluginScriptSection(t, script, "  const interactiveCard =", "  const addInfo =")
	close := pluginScriptSection(t, script, "  const closePanel =", "  const showPanel =")
	program := `
const assert = require("node:assert/strict");
class Card {
 constructor(){this.attrs={};this.events={};this.heading={};this.classList={values:new Set(),toggle(v,on){if(on)this.values.add(v);else this.values.delete(v);},remove(v){this.values.delete(v);},contains(v){return this.values.has(v);}};}
 setAttribute(k,v){this.attrs[k]=v;} addEventListener(k,v){this.events[k]=v;}
 querySelector(s){return s==="h3"?this.heading:null;} insertBefore(){} focus(){}
}
const cards=[new Card(),new Card()],results={querySelectorAll:()=>cards};
const document={createElement:()=>({append(){}})},identityTile=()=>({});
let activations=0,stops=0,operationViewID="one",overlayDismissible=true,returnFocus=null;
const panel={hidden:false},workspaceShell={inert:true},pluginsDialog={querySelector:()=>null};
const stopPolling=()=>{stops++;};
` + card + close + `
for(const c of cards) {interactiveCard(c,"Example",()=>{activations++;});assert.equal(c.attrs["aria-pressed"],"false");assert.equal(c.classList.contains("is-selected"),false);}
cards[0].events.click(); assert.equal(cards[0].classList.contains("is-selected"),true); assert.equal(cards[0].attrs["aria-pressed"],"true");
cards[1].events.keydown({key:"Enter",preventDefault(){}});
assert.equal(activations,2); assert.equal(cards[0].classList.contains("is-selected"),false);assert.equal(cards[1].classList.contains("is-selected"),true);
closePanel(); assert.equal(panel.hidden,true);assert.equal(workspaceShell.inert,false);
for(const c of cards){assert.equal(c.classList.contains("is-selected"),false);assert.equal(c.attrs["aria-pressed"],"false");}
`
	if output, err := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program).CombinedOutput(); err != nil {
		t.Fatalf("selection lifecycle: %v\n%s", err, output)
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	selected := `.plugins-result-card.is-selected{border-color:var(--jv-success);background:color-mix(in srgb,var(--jv-success) 18%,var(--jv-surface-sunken))}`
	hover := `@media(hover:hover) and (pointer:fine){` + strings.Replace(selected, ".is-selected", ":hover", 1) + `}`
	for _, want := range []string{selected, hover, `.plugins-result-card:focus-visible{outline:2px solid var(--jv-success);outline-offset:-3px}`} {
		if !strings.Contains(string(css), want) {
			t.Fatal("protected card visual missing", want)
		}
	}
	discover := pluginScriptSection(t, script, "  const renderProject =", "  const renderInstalled =")
	installed := pluginScriptSection(t, script, "  const renderInstalled =", "  // Review, progress")
	info := pluginScriptSection(t, script, "  const addInfo =", "  const infoRows =")
	if !strings.Contains(discover, "interactiveCard(card,") || !strings.Contains(installed, "addInfo(card,") || !strings.Contains(info, "interactiveCard(card,") {
		t.Fatal("cards do not share interaction")
	}
}

func TestManagedPluginLifecycleUIContract(t *testing.T) {
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	start := strings.Index(script, "  const renderInstalled =")
	end := strings.Index(script, "  // Review, progress")
	inventory := script[start:end]
	for _, want := range []string{`plugin.management === "plugins" && ["enabled", "disabled"].includes(plugin.state)`, `plugin.state === "disabled" ? "Disabled" : "Enabled"`, `plugin.state === "disabled" ? "enable" : "disable"`, `action === "enable" ? "Enable" : "Disable"`, `"plugins-state-action"`, `pluginAction("Remove"`, `"plugins-remove-action"`} {
		if !strings.Contains(inventory, want) {
			t.Fatal("managed UI contract missing", want)
		}
	}
	for _, want := range []string{`pluginRequest("manage/plan"`, `pluginRequest("manage"`, `slider.type = "range"`, `finalConfirm.className = "destructive-confirm-toggle"`, `Number(slider.value) !== 100 || !finalConfirm.checked`, `overlayActions(panelAction("Cancel", returnToInstalled, true), apply)`, `See System -> Logs -> Plugins for diagnostic details.`, `pluginRequest("operation"`, `["Prepare", "Backup", "Change", "Restart", "Verify"]`, `pluginsDialog.querySelector('[data-plugins-tab="installed"]').click()`} {
		if !strings.Contains(script, want) {
			t.Fatal("review or progress missing", want)
		}
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`button.plugins-state-action`, `button.plugins-remove-action`, `button.plugins-done-action`, `@media(prefers-color-scheme:light)`, `.plugins-result-card.is-selected`} {
		if !strings.Contains(string(css), want) {
			t.Fatal("Plugin style missing", want)
		}
	}
}

type fakePluginsManageAPI struct {
	fakePluginsInstallAPI
	manageCalls   int
	manageRequest api.PluginManageApplyRequest
}

func (f *fakePluginsManageAPI) AdminPluginManagePlan(_ context.Context, _ string, r api.PluginManageRequest) (api.PluginManagePlan, error) {
	f.manageCalls++
	return api.PluginManagePlan{Action: r.Action, ProjectID: r.ProjectID, State: "enabled", Fingerprint: "sha256:" + strings.Repeat("a", 64)}, nil
}
func (f *fakePluginsManageAPI) AdminPluginManage(_ context.Context, _ string, r api.PluginManageApplyRequest) (api.PersistentOperationResponse, error) {
	f.manageCalls++
	f.manageRequest = r
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: "jv-family1", OperationType: "plugin_" + r.Action, OperationID: "12345678-1234-4123-8123-123456789abc", State: "running"}}, nil
}
func (f *fakePluginsManageAPI) AdminPluginManageAcknowledge(_ context.Context, _ string, r api.PluginInstallAcknowledgeRequest) (api.PersistentOperationResponse, error) {
	f.manageCalls++
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: "jv-family1", OperationType: "plugin_disable", OperationID: r.OperationID, State: "resolved"}}, nil
}
func (f *fakePluginsManageAPI) AdminCurrentPluginOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error) {
	return f.AdminCurrentPluginInstall(ctx, session)
}
func TestPluginManageWorkspaceAuthorizationAndStrictForms(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "administrator"} {
		t.Run(role, func(t *testing.T) {
			client := &fakePluginsManageAPI{}
			client.role = role
			app, err := New(client, Config{})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				route, body string
				code        int
			}{{"manage/plan", "csrf=csrf-token&action=disable&project_id=root", 200}, {"manage", "csrf=csrf-token&action=disable&project_id=root&plan_fingerprint=sha256:" + strings.Repeat("a", 64) + "&confirm_players=true", 202}, {"manage/acknowledge", "csrf=csrf-token&operation_id=12345678-1234-4123-8123-123456789abc", 200}} {
				rr := httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/"+tc.route, tc.body))
				want := 403
				if role == "administrator" {
					want = tc.code
				}
				if rr.Code != want {
					t.Fatal(tc.route, rr.Code, rr.Body.String())
				}
			}
			if role != "administrator" && client.manageCalls != 0 {
				t.Fatal("non-administrator reached lifecycle API")
			}
			if role == "administrator" {
				if client.manageRequest.Action != "disable" || client.manageRequest.ProjectID != "root" || !client.manageRequest.ConfirmPlayers {
					t.Fatal("reviewed action contract changed")
				}
				calls := client.manageCalls
				for _, body := range []string{"csrf=wrong&action=remove&project_id=root", "csrf=csrf-token&action=remove&action=enable&project_id=root", "csrf=csrf-token&action=remove&project_id=root&filename=manual.jar"} {
					rr := httptest.NewRecorder()
					app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/manage/plan", body))
					if rr.Code < 400 || client.manageCalls != calls {
						t.Fatal("invalid lifecycle form forwarded", rr.Code)
					}
				}
			}
		})
	}
}
func TestGenericPluginWorkspaceOperationAcceptsAllFourMutations(t *testing.T) {
	for _, kind := range []string{"plugin_install", "plugin_enable", "plugin_disable", "plugin_remove"} {
		t.Run(kind, func(t *testing.T) {
			client := &fakePluginsManageAPI{}
			client.role = "administrator"
			client.operationType = kind
			app, err := New(client, Config{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/operation", ""))
			if rr.Code != 200 || !strings.Contains(rr.Body.String(), kind) {
				t.Fatal("active mutation not discovered", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPluginsDiscoverSourceControlsAndReadOnlyCards(t *testing.T) {
	script := pluginWorkspaceScript(t)
	header, _ := assets.ReadFile("templates/header.html")
	for _, want := range []string{`name="modrinth" data-plugins-modrinth checked`, `name="hangar" data-plugins-hangar>Hangar`, `placeholder="Search plugins"`} {
		if !strings.Contains(string(header), want) {
			t.Fatal("source control", want)
		}
	}
	for _, bad := range []string{`aria-readonly="true"`, `data-plugins-hangar disabled`, `plugins-source-fixed`, `plugins-source-unavailable`} {
		if strings.Contains(string(header), bad) {
			t.Fatal("placeholder remains", bad)
		}
	}
	for _, want := range []string{`modrinth: String(form.elements.modrinth.checked)`, `hangar: String(form.elements.hangar.checked)`, `modrinth_offset: String(position.modrinth_offset)`, `hangar_offset: String(position.hangar_offset)`, `hangar_first: String(position.hangar_first)`, `input[name="modrinth"],input[name="hangar"]`, `Enable Modrinth or Hangar to browse plugins.`, `resetCursors();`} {
		if !strings.Contains(script, want) {
			t.Fatal("source/cursor request", want)
		}
	}
	if strings.Contains(script, `data-plugins-modrinth]").addEventListener("click"`) {
		t.Fatal("fixed source handler remains")
	}
	helpers := pluginScriptSection(t, script, "  const text =", "  const renderInstalled =")
	actions := pluginScriptSection(t, script, "  const panelAction =", "  const returnToInstalled =")
	program := `
const assert = require("node:assert/strict");
const imageRequests = [];
class Node {
 constructor(tag) { this.tag=tag; this.children=[]; this.events={}; this.attrs={}; this.dataset={}; this.className=""; this.textContent=""; this.classList={add:(c)=>{this.className+=" "+c},remove:()=>{},toggle:()=>{}}; }
 append(...nodes) { this.children.push(...nodes); }
 insertBefore(node,before) { this.children.splice(this.children.indexOf(before),0,node); }
 setAttribute(k,v) { this.attrs[k]=v; }
 addEventListener(k,v) { this.events[k]=v; }
 focus() {}
 querySelector(selector) { if(selector==="h3") return this.children.find(n=>n.tag==="h3"); return this.children.find(n=>n.className==="plugins-card-description"); }
 querySelectorAll() {return this.children;}
 set src(value) { imageRequests.push(value); }
}
const document = {createElement:(tag)=>new Node(tag)};
const results = new Node("div"), panelBody = new Node("div");
const stopPolling=()=>{}, showPanel=()=>{panelBody.children=[]}, closePanel=()=>{};
const reviews=[]; const reviewPlugin=(id,version,project)=>{reviews.push(project.source)};
` + helpers + actions + pluginScriptSection(t, script, "  const renderInstalled =", "  // Review, progress") + `
const server = {supported:true,minecraft_version:"1.21.8",server_type:"paper"};
const base = {title:"Same name",description:"Plugin",project_id:"123",slug:"same",compatible:true,compatibility:"Compatible according to publisher metadata",version:{id:"1",version_number:"1",version_type:"release",loaders:["paper"],game_versions:["1.21.8"],dependencies:[]}};
const hangar = renderProject({...base,source:"Hangar",install_available:false,icon_url:"https://hangar.papermc.io/avatar.png"},server);
results.append(hangar); hangar.events.click();
const flatten=(n)=>[n,...n.children.flatMap(flatten)];
let nodes=flatten(panelBody);
assert.equal(imageRequests.length,2);
assert(imageRequests.every(request=>new URL(request,"https://appliance.example").searchParams.get("source")==="Hangar"));
assert(imageRequests.every(request=>!new URL(request,"https://appliance.example").searchParams.has("icon_url")));
assert(nodes.some(n=>n.textContent==="Hangar"));
assert(!nodes.some(n=>n.textContent==="Install"));
assert(nodes.some(n=>n.textContent==="Channel"));
assert(!nodes.some(n=>n.textContent==="Release type"));
for (const root of [hangar,panelBody]) {
 const tiles=flatten(root).filter(n=>n.className.includes("plugins-icon-tile"));
 assert.equal(tiles.length,1); assert.equal(tiles[0].textContent,"SA");
 assert(tiles[0].children.some(n=>n.tag==="img"));
}
const close = nodes.find(n=>n.tag==="button" && n.textContent==="Close");
assert(!close.className.includes("plugins-done-action")); assert(!close.className.includes("secondary"));
for (const source of ["Modrinth","Hangar"]) {
 for (const state of ["enabled","disabled","attention"]) {
  const owned=renderProject({...base,source,install_available:true,installed:true,installed_state:state},server);
  results.append(owned); owned.events.click();
  assert.deepEqual(reviews,[]); // reviewPlugin is the only entry to install/plan.
  const cardNodes=flatten(owned), infoNodes=flatten(panelBody);
  assert(cardNodes.some(n=>n.textContent==="Installed"));
  assert(infoNodes.some(n=>n.textContent==="Installed" && n.tag!=="button"));
  const semantics=[["1","version"],["paper","server"],["MC 1.21.8","minecraft"],["Installed","installed"],[state==="enabled"?"Enabled":state==="disabled"?"Disabled":"Needs attention",state==="attention"?"attention":state]];
  for(const rootNodes of [cardNodes,infoNodes]) for(const [label,semantic] of semantics) assert(rootNodes.some(n=>n.textContent===label && n.className.includes("plugins-badge-"+semantic)));
  const topRight=infoNodes.find(n=>n.className.includes("plugins-overlay-installed"));
  assert(topRight);assert.equal(topRight.tag,"span");assert.equal(topRight.attrs.role,undefined);assert.deepEqual(topRight.events,{});
  assert(infoNodes.some(n=>n.textContent==="Managed by Plugins"));
  assert(infoNodes.some(n=>n.textContent==="Updates are not available yet"));
  assert(infoNodes.some(n=>n.textContent===(state==="enabled"?"Enabled":state==="disabled"?"Disabled":"Needs attention")));
  assert(!infoNodes.some(n=>n.tag==="button" && n.textContent==="Install"));
 }
}
// Inventory uses the same header slot only for Plugins ownership.
for(const management of ["plugins","manual","justvoxel"]) for(const state of ["enabled","disabled","attention"]) {
 const plugin={name:"Inventory",filename:"inventory.jar",version:"2",management,state,update_status:state==="attention"?"Attention: changed JAR":"",metadata_status:"Metadata read"};
 const card=renderInstalled(plugin);results.append(card);card.events.click();
 const nodes=flatten(panelBody),status=nodes.find(n=>n.className.includes("plugins-overlay-installed"));
 assert.equal(Boolean(status),management==="plugins");
 if(status){assert.equal(status.tag,"span");assert.equal(status.textContent,"Installed");assert.deepEqual(status.events,{});assert.equal(status.attrs.role,undefined);assert(status.className.includes("plugins-badge-installed"));}
 assert(nodes.some(n=>n.textContent==="2" && n.className.includes("plugins-badge-version")));
 assert(!nodes.some(n=>n.tag==="button" && n.textContent==="Install"));
}
// Future authoritative server labels receive the same badge, without name matching.
for(const label of ["Future server","Fabric","Quilt"]) {
 const card=renderProject({...base,source:"Hangar"},{...server,server_software:label});results.append(card);card.events.click();
 for(const root of [card,panelBody]) assert(flatten(root).some(n=>n.textContent===label && n.className.includes("plugins-badge-server")));
}
const imagesBeforeNoninstalled=imageRequests.length;
const installableHangar=renderProject({...base,source:"Hangar",install_available:true},server);
results.append(installableHangar); installableHangar.events.click(); assert.deepEqual(reviews,["Hangar"]);
const incompatibleHangar=renderProject({...base,source:"Hangar",install_available:true,compatible:false},server);
results.append(incompatibleHangar); incompatibleHangar.events.click(); assert.deepEqual(reviews,["Hangar"]);
const modrinth=renderProject({...base,source:"Modrinth",install_available:false,icon_url:"https://cdn.modrinth.com/data/123/icon.png"},server);
results.append(modrinth); modrinth.events.click();
assert.equal(imageRequests.length,imagesBeforeNoninstalled+5);
for (const request of imageRequests.slice(-2)) {assert(request.startsWith("/api/plugins/workspace/icon?")); const q=new URL(request,"https://appliance.example").searchParams; assert.equal(q.get("project_id"),"123"); assert.equal(q.get("icon_url"),"https://cdn.modrinth.com/data/123/icon.png");}
nodes=flatten(panelBody);
assert(nodes.some(n=>n.textContent==="Modrinth"));
assert(nodes.some(n=>n.textContent==="Release type"));
assert(!nodes.some(n=>n.textContent==="Channel"));
for (const root of [modrinth,panelBody]) {
 const tiles=flatten(root).filter(n=>n.className.includes("plugins-icon-tile"));
 assert.equal(tiles.length,1); assert.equal(tiles[0].textContent,"SA");
 assert.equal(tiles[0].children.filter(n=>n.tag==="img").length,1);
}
`
	command := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("catalog card source boundary: %v\n%s", err, out)
	}
}

func TestPluginsDiscoverCompositeCursorNavigation(t *testing.T) {
	script := pluginWorkspaceScript(t)
	cursors := pluginScriptSection(t, script, "  const firstCursor =", "  const text =")
	navigation := pluginScriptSection(t, script, `  previous.addEventListener("click"`, "  // Role discovery")
	filters := pluginScriptSection(t, script, `  form.querySelectorAll('input[name="compatible_only"]`, `  previous.addEventListener`)
	program := `
const assert=require("node:assert/strict");
const controls=[{name:"compatible_only"},{name:"release_only"},{name:"modrinth"},{name:"hangar"}];
const form={querySelectorAll:(selector)=>{for(const c of controls)assert(selector.includes('input[name="'+c.name+'"]'));return controls.map(c=>({addEventListener:(event,fn)=>c.change=fn}));}};
const previous={addEventListener:(event,fn)=>previous.click=fn},next={addEventListener:(event,fn)=>next.click=fn};
let searchTimer=null,loads=0;
const clearTimeout=()=>{},load=()=>{loads++};
let totalPages=1;const pageLabel={};
` + cursors + navigation + filters + `
const original=firstCursor();
const second={modrinth_offset:7,hangar_offset:3,hangar_first:true};
const third={modrinth_offset:9,hangar_offset:8,hangar_first:false};
nextCursor=second;next.click();assert.deepEqual(cursor,second);
nextCursor=third;next.click();assert.deepEqual(cursor,third);
previous.click();assert.deepEqual(cursor,second);
previous.click();assert.deepEqual(cursor,original);
assert.equal(pageCursors.length,3);
for(const c of controls){pageIndex=2;cursor=third;nextCursor=third;c.change();assert.equal(pageIndex,0);assert.deepEqual(cursor,original);assert.deepEqual(pageCursors,[original]);assert.equal(nextCursor,null);}
assert.equal(loads,8);
`
	command := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("composite cursor navigation: %v\n%s", err, out)
	}
}

func TestPluginsDiscoverRequestsEnabledSources(t *testing.T) {
	script := pluginWorkspaceScript(t)
	loader := pluginScriptSection(t, script, "  const discoverQuery =", "  const discoverCapacity =")
	program := `
const assert=require("node:assert/strict");
const requests=[];
const elements={q:{value:"query"},compatible_only:{checked:true},release_only:{checked:true},modrinth:{checked:true},hangar:{checked:false}};
const form={elements,querySelectorAll:()=>Object.values(elements),querySelector:selector=>{
 assert.equal(selector,'input[name="hangar"]');return elements.hangar;
}};
const state={},context={},pagination={},previous={},next={};
const pageLabel={};
let pageCursors=[],totalPages=1,discoverServerKey=null;const resetCursors=()=>{throw new Error("unexpected context reset")};
const document={body:{classList:{contains:()=>true}}};
const pluginsDialog={};
const results={classList:{toggle:()=>{}},replaceChildren:()=>{},append:()=>{throw new Error("unexpected result")}};
let generation=0,controller=null,tab="discover",pageLimit=1,pageIndex=0,nextCursor=null,total=0;
let cursor={modrinth_offset:8,hangar_offset:3,hangar_first:true};
const clearInventoryTimer=()=>{};
const renderProject=()=>{throw new Error("unexpected card")};
const server={supported:true,minecraft_version:"1.21.8",server_type:"paper",server_software:"Paper"};
const window={location:{assign:()=>{throw new Error("unexpected navigation")}}};
const fetch=async(url,options)=>{
 requests.push({url,options});
 return {ok:true,status:200,json:async()=>({server,modrinth:elements.modrinth.checked,hangar:server.server_type!=="spigot"&&elements.hangar.checked,projects:[],cursor,next_cursor:null,total})};
};
` + loader + `
(async()=>{
 for(const [m,h] of [[true,false],[false,true],[true,true],[false,false]]){
  elements.modrinth.checked=m;elements.hangar.checked=h;
  const before=controller;
  const requestCount=requests.length;
  await load();
  assert.equal(requests.length,requestCount+1);
  if(before)assert(before.signal.aborted);
  const req=requests.at(-1);assert(req.url.startsWith("/api/plugins/workspace/discover?"));
  const q=new URL(req.url,"https://appliance.example").searchParams;
  assert.equal(q.get("modrinth"),String(m));assert.equal(q.get("hangar"),String(h));
  assert.equal(q.get("modrinth_offset"),"8");assert.equal(q.get("hangar_offset"),"3");assert.equal(q.get("hangar_first"),"true");
  assert.equal(q.get("q"),"query");assert.equal(q.get("limit"),"1");assert.equal(req.options.credentials,"same-origin");
  assert(!elements.modrinth.disabled);assert(!elements.hangar.disabled);
 }
 // The local workspace request is allowed; neither provider is enabled upstream.
 assert.equal(new URL(requests.at(-1).url,"https://appliance.example").searchParams.get("modrinth"),"false");
 assert.equal(new URL(requests.at(-1).url,"https://appliance.example").searchParams.get("hangar"),"false");
 assert.equal(state.textContent,"Enable Modrinth or Hangar to browse plugins.");
 assert.equal(pageLabel.textContent,"Page 1 of 1");
 assert(next.disabled);assert(previous.disabled);
 elements.modrinth.checked=true;elements.hangar.checked=true;
 total=25;pageLimit=8;
 await load();assert.equal(pageLabel.textContent,"Page 1 of 4");
 pageIndex=1;
 await load();assert.equal(pageLabel.textContent,"Page 2 of 4");assert(!previous.disabled);
 total=0;pageIndex=0;
 await load();assert.equal(pageLabel.textContent,"Page 1 of 1");
 // A new selected-server context starts with no cursor history.
 discoverServerKey=null;
 server.server_type="spigot";server.server_software="Spigot";
 elements.modrinth.checked=true;elements.hangar.checked=true;
 await load();assert(elements.hangar.disabled);assert(!elements.hangar.checked);assert(!elements.modrinth.disabled);
 await load();
 const spigotQuery=new URL(requests.at(-1).url,"https://appliance.example").searchParams;
 assert.equal(spigotQuery.get("modrinth"),"true");assert.equal(spigotQuery.get("hangar"),"false");
})().catch(error=>{console.error(error);process.exitCode=1;});
`
	command := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("source requests: %v\n%s", err, out)
	}
}

func (f *fakePluginsWorkspaceAPI) AdminPluginSourceIcon(ctx context.Context, session, source, project string) (api.PluginIcon, error) {
	f.iconSource = source
	return f.AdminPluginIcon(ctx, session, project)
}
func TestPluginsWorkspaceHangarContracts(t *testing.T) {
	client := &fakePluginsInstallAPI{}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/plugins/workspace/icon?source=Hangar&project_id=123", ""))
	if rr.Code != 200 || client.iconSource != "Hangar" || client.iconProjectID != "123" || client.iconURL != "" {
		t.Fatal("provider icon not proxied", rr.Code, client.iconSource)
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/install/plan", "csrf=csrf-token&source=Hangar&project_id=123&version_id=456"))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"source":"Hangar"`) {
		t.Fatal("plan source lost", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/plugins/workspace/install", "csrf=csrf-token&source=Hangar&project_id=123&version_id=456&plan_fingerprint=sha256:"+strings.Repeat("a", 64)+"&confirm_players=false"))
	if rr.Code != 202 || client.request.Source != "Hangar" {
		t.Fatal("apply source lost", rr.Code, rr.Body.String())
	}
	script := pluginWorkspaceScript(t)
	if !strings.Contains(script, `source: project?.source || "Modrinth"`) || !strings.Contains(script, `source: plan.source, project_id: plan.project_id`) || strings.Contains(script, `context.textContent.split`) {
		t.Fatal("authoritative source/context contract missing")
	}
}

func TestPluginsPageCursorInvalidationEvents(t *testing.T) {
	script := pluginWorkspaceScript(t)
	cursors := pluginScriptSection(t, script, "  const firstCursor =", "  const text =")
	capacity := pluginScriptSection(t, script, "  const updateCapacity =", "  const capacityObserver =")
	workspace := pluginScriptSection(t, script, "  const workspaceWindow = setupWorkspaceWindow(pluginsDialog,", "  // Role discovery")
	program := `
const assert=require("node:assert/strict");
const eventNode=()=>({events:{},addEventListener(event,fn){this.events[event]=fn}});
const pageLabel={};let totalPages=1,pageLimit=8,capacityTimer=null,searchTimer=null,loads=0,resumes=0,closes=0,options;
let wantedCapacity=8;const discoverCapacity=()=>wantedCapacity;
const controls=["compatible_only","release_only","modrinth","hangar"].map(name=>({...eventNode(),name}));
const form={...eventNode(),elements:{q:eventNode()},querySelectorAll:()=>controls};
const previous=eventNode(),next=eventNode(),pluginsOpen=eventNode(),refresh=eventNode(),close=eventNode();
const tabs=[{...eventNode(),dataset:{pluginsTab:"discover"},setAttribute:()=>{}},{...eventNode(),dataset:{pluginsTab:"installed"},setAttribute:()=>{}}];
const pluginsDialog={open:true,querySelector:s=>s.includes("refresh")?refresh:close,querySelectorAll:()=>tabs};
const state={};const document={body:{classList:{contains:()=>true}},querySelector:()=>null};
const setupWorkspaceWindow=(dialog,callbacks)=>{options=callbacks;return {open:callbacks.onOpen,close:callbacks.onClose}};
const clearTimeout=()=>{},setTimeout=(fn)=>{searchTimer=fn;return fn},clearInventoryTimer=()=>{},closePanel=()=>closes++,resumePluginInstall=()=>resumes++;
` + cursors + `
const load=()=>{loads++;generation++;controller?.abort();};
` + capacity + workspace + `
let discoverServerKey=null;
` + pluginScriptSection(t, script, "  const acceptDiscoverContext =", "  const load =") + `
const second={modrinth_offset:7,hangar_offset:4,hangar_first:true};
const dirty=()=>{pageCursors=[firstCursor(),second];pageIndex=1;cursor=second;nextCursor=second;totalPages=85;pageLabel.textContent="Page 2 of 85";controller=new AbortController()};
const clean=()=>{assert.equal(pageIndex,0);assert.deepEqual(cursor,firstCursor());assert.deepEqual(pageCursors,[firstCursor()]);assert.equal(nextCursor,null);assert.equal(pageLabel.textContent,"Page 1 of 1")};
assert(acceptDiscoverContext({server_software:"Paper",minecraft_version:"1"}));
dirty();assert(!acceptDiscoverContext({server_software:"Future",minecraft_version:"2"}));clean();
dirty();refresh.events.click();clean();assert.equal(loads,1);
for(const control of controls){dirty();const signal=controller.signal;control.events.change();clean();assert(signal.aborted)}
dirty();const searchSignal=controller.signal;form.elements.q.events.input();clean();assert(searchSignal.aborted);searchTimer();
dirty();let prevented=false;form.events.submit({preventDefault:()=>prevented=true});clean();assert(prevented);
dirty();wantedCapacity=12;updateCapacity();clean();assert.equal(pageLimit,12);
dirty();const closeSignal=controller.signal;options.onClose();clean();assert(closeSignal.aborted);assert.equal(closes,1);
dirty();options.onOpen();clean();assert.equal(resumes,1);
dirty();tabs[1].events.click();clean();assert.equal(tab,"installed");tabs[0].events.click();clean();assert.equal(tab,"discover");
// Normal Previous/Next still navigate the composite cursor history.
nextCursor=second;next.events.click();assert.deepEqual(cursor,second);assert.equal(pageIndex,1);
previous.events.click();assert.deepEqual(cursor,firstCursor());assert.equal(pageIndex,0);
`
	if out, err := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program).CombinedOutput(); err != nil {
		t.Fatalf("cursor invalidation: %v\n%s", err, out)
	}
}

func TestPluginsInstalledReviewNeverRequestsInstallPlan(t *testing.T) {
	script := pluginWorkspaceScript(t)
	review := pluginScriptSection(t, script, "  const reviewPlugin =", "  const resumePluginInstall =")
	program := `
const assert=require("node:assert/strict");
let requests=0,infos=0;
const pluginRequest=()=>{requests++;throw new Error("installed project called install/plan")};
const stopPolling=()=>{throw new Error("installed project entered installation")};
` + review + `
(async()=>{
 for(const source of ["Modrinth","Hangar"])await reviewPlugin("123","new-catalog-release",{source,installed:true},()=>infos++);
 assert.equal(infos,2);assert.equal(requests,0);
})().catch(error=>{console.error(error);process.exitCode=1});
`
	if out, err := exec.Command("node", "-e", "globalThis.justVoxelSelectedURL = value => value;\n"+program).CombinedOutput(); err != nil {
		t.Fatalf("installed review guard: %v\n%s", err, out)
	}
}

func TestPluginsPlainPaginationAndSemanticBadgeStyles(t *testing.T) {
	script := pluginWorkspaceScript(t)
	header, _ := assets.ReadFile("templates/header.html")
	css, _ := assets.ReadFile("static/app.css")
	if !strings.Contains(string(header), `<span data-plugins-page>Page 1 of 1</span>`) {
		t.Fatal("plain page label missing")
	}
	for _, forbidden := range []string{"data-plugins-page-input", "data-plugins-page-total", "inputmode=", "jumpToPage", "Finding page", "reachableIndex", "(page - 1) * pageLimit", "(requestedPage - 1) * pageLimit"} {
		if strings.Contains(script, forbidden) || strings.Contains(string(header), forbidden) || strings.Contains(string(css), forbidden) {
			t.Fatal("page jump remains", forbidden)
		}
	}
	style := string(css)
	light := strings.LastIndex(style, "@media(prefers-color-scheme:light)")
	if light < 0 {
		t.Fatal("light theme missing")
	}
	for _, semantic := range []string{"version", "server", "minecraft", "installed", "enabled", "disabled", "attention"} {
		selector := ".plugins-badge-" + semantic + "{color:"
		if !strings.Contains(style[:light], selector) || !strings.Contains(style[light:], selector) {
			t.Fatal("semantic dark/light treatment missing", semantic)
		}
	}
	for _, forbidden := range []string{"plugins-badge-Paper", "plugins-badge-Purpur", `server === "Paper"`, `server === "Purpur"`, `platform === "Paper"`, `platform === "Purpur"`} {
		if strings.Contains(script, forbidden) || strings.Contains(style, forbidden) {
			t.Fatal("server-specific styling", forbidden)
		}
	}
	if !strings.Contains(script, `panelAction("Install", () => applyPlugin(plan))`) {
		t.Fatal("top-right install action missing")
	}
}

func (f *fakePluginsWorkspaceAPI) MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error) {
	ids := f.selectedIDs
	if ids == nil {
		ids = []string{"jv-family1"}
	}
	out := []api.RegisteredMinecraftInstance{}
	for _, id := range ids {
		out = append(out, api.RegisteredMinecraftInstance{ID: id, DisplayName: id})
	}
	return out, nil
}
func (f *fakePluginsWorkspaceAPI) MinecraftInstanceRuntimes(context.Context, string) ([]string, error) {
	if f.selectedIDs == nil {
		return []string{"jv-family1"}, nil
	}
	return f.selectedIDs, nil
}
func TestPluginsSelectedServerRequests(t *testing.T) {
	for _, tc := range []struct {
		query, want string
		status      int
	}{{"?server=jv-family1", "jv-family1", 200}, {"?server=jv-sibling1", "jv-sibling1", 200}, {"", "", 409}, {"?server=jv-stale1", "", 404}, {"?server=bad", "", 400}} {
		client := &fakePluginsWorkspaceAPI{selectedIDs: []string{"jv-family1", "jv-sibling1"}}
		client.role = "administrator"
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest("GET", "/api/plugins/workspace/installed"+tc.query, ""))
		if rr.Code != tc.status || client.selectedSeen != tc.want {
			t.Fatal(tc, rr.Code, client.selectedSeen, rr.Body.String())
		}
	}
}
