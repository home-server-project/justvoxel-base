package server

import (
	"bytes"
	"context"
	"encoding/json"
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
	iconCalls      int
	iconProjectID  string
	iconError      error
}

func (f *fakePluginsWorkspaceAPI) AdminPluginInventory(_ context.Context, session string, updates bool) (api.PluginInventory, error) {
	if session != "session-token" {
		return api.PluginInventory{}, api.ErrUnauthorized
	}
	f.inventoryCalls++
	f.updates = updates
	return api.PluginInventory{Server: api.PluginServerContext{ServerSoftware: "Paper", Supported: true, MinecraftVersion: "1.21.8"}, Plugins: []api.InstalledPlugin{{Filename: "manual.jar", Management: "manual", UpdateStatus: "Update status unknown - manually managed"}, {Filename: "verified.jar", Management: "plugins", ProjectID: "project123", VersionID: "release123", UpdateStatus: "Managed by Plugins; update and removal are not available yet"}}}, nil
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
	start := strings.Index(behavior, "// Plugin workspace.")
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
	for _, unsafe := range []string{"api.modrinth.com", "innerHTML", "outerHTML", "insertAdjacentHTML", "cdn.modrinth.com", "icon_url", "showModal("} {
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
	for _, want := range []string{`text("button", "Info"`, `button.type = "button"`, `button.setAttribute("aria-haspopup", "dialog")`, `button.setAttribute("aria-controls", "plugins-overlay")`, "showPanel(`Info for ${name}`)", `renderInfo();`, `overlayActions(panelAction("Close"`, `"plugins-card-actions"`, `"plugins-icon-tile"`, `header.append(identityTile(name, projectID), copy)`} {
		if !strings.Contains(info, want) {
			t.Fatalf("overlay Info behavior missing %s", want)
		}
	}
	for _, inline := range []string{`card.append(details)`, `const details =`, `panelBody.append(text("h3", name), details`, `createElement("details")`, `plugins-card-details`, `aria-expanded`} {
		if strings.Contains(behavior[start:end], inline) {
			t.Fatalf("raw or inline card detail block remains: %s", inline)
		}
	}
	discover := behavior[project:installed]
	for _, want := range []string{`addInfo(card, project.title, () => {`, `"plugins-card-description"`, `counts.set(kind, (counts.get(kind) || 0) + 1)`, `"JustVoxel managed"`, `else if (project.install_available`, `reviewPlugin(project.project_id, version.id, project)`} {
		if !strings.Contains(discover, want) {
			t.Fatalf("Discover summary or Info missing %s", want)
		}
	}
	inventory := behavior[installed:end]
	for _, want := range []string{`const name = plugin.name || plugin.filename;`, `addInfo(card, name, () => {`, `overlayHeader(name, "", [plugin.version || "Version unknown", management], projectID)`, `tab === "updates"`, `"Needs attention"`, `"plugins-inventory-status"`, `"JustVoxel managed"`, `"Managed by Plugins"`, `"Manual / unmanaged"`} {
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
	for _, want := range []string{`.plugins-workspace-body{container-type:inline-size;`, `grid-template-columns:minmax(0,1fr)`, `@container(min-width:580px){.plugins-results{grid-template-columns:repeat(2,minmax(0,1fr))}}`, `@container(min-width:860px){.plugins-results{grid-template-columns:repeat(3,minmax(0,1fr))}}`, `-webkit-line-clamp:2`, `.plugins-workspace-dialog [hidden]{display:none!important}`, `max-width:1080px`, `.plugins-card-actions`, `.plugins-icon-tile{display:grid;place-items:center;flex:none;width:40px;height:40px`, `.plugins-overlay-actions{display:flex;flex-wrap:wrap;`, `.plugins-card-actions button,.plugins-install-content button{padding:.72rem 1rem}`, `word-break:normal`, `.plugins-version-list{display:flex;flex-wrap:wrap;`, `.plugins-info-rows{grid-template-columns:minmax(0,1fr)}`} {
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
	actionStart := strings.Index(style, `.plugins-workspace-dialog .plugins-info-button{`)
	if actionStart < 0 {
		t.Fatal("plugin action layout missing")
	}
	actionEnd := strings.Index(style[actionStart:], "}")
	if actionEnd < 0 {
		t.Fatal("plugin action rule incomplete")
	}
	actionStyle := style[actionStart : actionStart+actionEnd]
	for _, override := range []string{"min-height", "padding", "font-size", "plugins-badge"} {
		if strings.Contains(actionStyle, override) {
			t.Fatal("plugin actions must inherit normal button sizing", override)
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
	for _, want := range []string{`.plugins-install-panel{position:absolute;inset:0`, `width:min(100%,640px);max-height:100%`, `overflow:auto;overscroll-behavior:contain`} {
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
}

func (f *fakePluginsInstallAPI) AdminPluginInstallPlan(_ context.Context, session string, r api.PluginInstallRequest) (api.PluginInstallPlan, error) {
	f.plans++
	return api.PluginInstallPlan{ProjectID: r.ProjectID, VersionID: r.VersionID, Name: "Authoritative plugin", Location: "/data/plugins", ConfirmationRequired: true, Online: 1, Players: []string{"Alex"}, Fingerprint: "sha256:" + strings.Repeat("a", 64)}, nil
}
func (f *fakePluginsInstallAPI) AdminPluginInstall(_ context.Context, session string, r api.PluginInstallApplyRequest) (api.PersistentOperationResponse, error) {
	f.installs++
	f.request = r
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationType: "plugin_install", OperationID: "12345678-1234-4123-8123-123456789abc", Stage: "downloading", State: "running", Status: "Downloading verified JARs."}}, nil
}
func (f *fakePluginsInstallAPI) AdminCurrentPluginInstall(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	f.operations++
	kind := f.operationType
	if kind == "" {
		kind = "plugin_install"
	}
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationType: kind, Stage: "backup", State: "running", Status: "Creating cold backup."}}, nil
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
		`project.install_available && project.compatible && server.supported && server.minecraft_version && version?.version_type === "release"`,
		`["geyser", "floodgate", "viaversion"].includes(project.slug)`,
		`pluginRequest("install/plan"`, `plan.project_name`, `overlaySection("Server compatibility", primary)`, `plan.artifacts`, `plan.installation_location`,
		`if (plan.confirmation_required)`, `plan.players`, `confirm_players: String(plan.confirmation_required === true)`,
		`plan_fingerprint: plan.plan_fingerprint`, `operation.stage`, `operation.status`, `install-operation?id=`,
		`"View in Installed"`, `"Install another plugin"`, `"Done"`, `"Managed by Plugins"`, `"JustVoxel managed"`, `"Manual / unmanaged"`,
		`addInfo(card, project.title, () => {`, `addInfo(card, name, () => {`, `"Administrator attention required"`,
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
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationType: "plugin_install", OperationID: request.OperationID, State: "resolved", Status: "Plugin state verified; Minecraft remains stopped."}}, nil
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
	for _, want := range []string{`overlayHeader(project.title, project.description`, `overlaySection("About this version")`, `about("Release"`, `about("Release type"`, `about("Published"`, `about("Source"`, `about("Project ID"`, `about("Release ID"`, `overlaySection("Compatibility")`, `compatibility("Active JustVoxel Minecraft version"`, `compatibility("Publisher-supported platforms"`, `compatibility("Publisher-supported Minecraft versions"`, `new Set(version?.game_versions || [])`, `"plugins-version-list"`, `versions.forEach((value) => versionList.append(text("li", value)))`, `overlaySection("Dependencies").append(dependencyGroups(dependencies))`} {
		if !strings.Contains(discoverInfo, want) {
			t.Fatal("structured Discover Info missing", want)
		}
	}
	helpers := script[:project]
	for _, want := range []string{`"plugins-overlay-header"`, `"plugins-overlay-identity"`, `identity.append(identityTile(name, projectID), text("h3", name))`, `["required", "Required"]`, `["optional", "Optional"]`, `["incompatible", "Incompatible"]`, `["other", "Embedded / other"]`, `dependency.name || dependency.title || dependency.file_name`, `plugins-dependency-ids`} {
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
	for _, want := range []string{`overlayHeader(plan.project_name, plan.description || project?.description`, `overlaySection("Server compatibility", primary)`, `"plugins-compatibility-state"`, `compatibility("Server software"`, `compatibility("Active Minecraft version"`, `overlaySection("Dependencies", primary)`, `artifact.satisfied ? "Already satisfied" : "Will be installed"`, `overlaySection("Files to install", primary)`, `readableSize(artifact.size)`, `overlaySection("Installation location", primary)`, `overlaySection("Version details", secondary)`, `version("Project ID"`, `version("Release ID"`, `overlaySection("Restart notice", secondary)`, `restart.classList.add("plugins-install-warning")`, `verified cold backup`, `Minecraft must restart for load verification`, `originally stopped`, `returns to stopped`, `if (plan.confirmation_required) {`, `overlaySection("Players", secondary)`, `overlayActions(panelAction("Cancel"`, `panelAction("Install", () => applyPlugin(plan))`} {
		if !strings.Contains(review, want) {
			t.Fatal("structured review missing", want)
		}
	}
	for _, old := range []string{`section("Plugin")`, `section("Compatibility")`, `section("Installation")`, `toLocaleString()} bytes`} {
		if strings.Contains(review, old) {
			t.Fatal("raw review presentation remains", old)
		}
	}
	if strings.Count(review, "player-aware shutdown") != 1 || strings.Index(review, `overlaySection("Players", secondary)`) < strings.Index(review, "if (plan.confirmation_required)") {
		t.Fatal("player warning not conditional or duplicated")
	}
	for _, want := range []string{`Number.isFinite(size)`, `"Size unavailable"`, `"MiB"`, `"KiB"`} {
		if !strings.Contains(script, want) {
			t.Fatal("truthful readable file sizes missing", want)
		}
	}
	progress := script[progressStart:poll]
	for _, want := range []string{`["Prepare", "Backup", "Install", "Restart", "Verify"]`, `[operation.stage]`, `operation.state`, `operation.status`, `"Plugin installation verified"`, `"Plugin state verified"`, `"Installation did not complete"`, `"Administrator attention required"`, `"Re-check after repair"`, `pluginRequest("install/acknowledge"`, `operation_id: operation.operation_id`, `payload.operation.state !== "resolved"`, `"View in Installed"`, `"Install another plugin"`, `"Done"`, `overlayActions(`} {
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

func (f *fakePluginsWorkspaceAPI) AdminPluginIcon(_ context.Context, session, projectID string) (api.PluginIcon, error) {
	if session != "session-token" {
		return api.PluginIcon{}, api.ErrUnauthorized
	}
	f.iconCalls++
	f.iconProjectID = projectID
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
	for _, want := range []string{`(name, projectID)`, `"plugins-icon-tile"`, `tile.dataset.tone`, `typeof projectID === "string"`, `/^[A-Za-z0-9]{1,64}$/`, `document.createElement("img")`, `image.alt = ""`, `image.hidden = true`, `image.src = ` + "`/api/plugins/workspace/icon?project_id=${encodeURIComponent(projectID)}`", `image.addEventListener("error"`, `image.remove()`, `tile.classList.remove("has-logo")`, `tile.append(image)`} {
		if !strings.Contains(helper, want) {
			t.Fatalf("shared icon contract missing %s", want)
		}
	}
	workspace := behavior[start:strings.Index(behavior, "// Bookmarked standalone pages")]
	if strings.Count(workspace, `document.createElement("img")`) != 1 || strings.Count(workspace, `image.src =`) != 1 {
		t.Fatal("plugin images must use only the shared identity helper")
	}
	for _, unsafe := range []string{"icon_url", "cdn.modrinth.com", "api.modrinth.com", "image.src = project", "image.src = plugin"} {
		if strings.Contains(workspace, unsafe) {
			t.Fatalf("upstream icon leaked into frontend: %s", unsafe)
		}
	}
	for _, want := range []string{
		`identity.append(identityTile(name, projectID), text("h3", name))`,
		`header.append(identityTile(name, projectID), copy)`,
		`overlayHeader(project.title, project.description, identityBadges, project.project_id)`,
		`}, project.project_id)`,
		`overlayHeader(plan.project_name, plan.description || project?.description, [plan.version_number, plan.server.server_software,`,
		`const projectID = plugin.project_id || undefined`,
		`overlayHeader(name, "", [plugin.version || "Version unknown", management], projectID)`,
		`}, projectID)`,
		`operationIdentity?.operation_id === operation.operation_id`,
		`project_id: plan.project_id, name: plan.project_name`,
		`overlayHeader(identity?.name || heading, "", [identity?.version], identity?.project_id)`,
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
	header := between("  const addInfo =", "  const infoRows =")
	for _, want := range []string{`copy.className = "plugins-card-copy"`, `copy.append(heading)`, `card.querySelector(".plugins-card-description, .plugins-card-release")`, `copy.append(description)`, `header.append(identityTile(name, projectID), copy)`, `header.append(actions)`} {
		if !strings.Contains(header, want) {
			t.Fatal("identity/header hierarchy missing", want)
		}
	}
	if strings.Contains(header, `card.append(actions)`) {
		t.Fatal("actions detached from compact card header")
	}
	collapsed := between("  const renderProject =", "    addInfo(card, project.title")
	for _, want := range []string{`server.server_software || server.server_type`, `const identityBadges = [version?.version_number, platform,`, `server.minecraft_version`, `identityBadges.filter(Boolean)`, `"plugins-badge"`} {
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
	if strings.Contains(discover, `...(version?.loaders || [])`) || !strings.Contains(discover, `overlayHeader(project.title, project.description, identityBadges, project.project_id)`) {
		t.Fatal("Info header must reuse concise server-context badges")
	}
	for _, want := range []string{`compatibility("Publisher-supported platforms", (version?.loaders || []).join(" / ")`, `new Set(version?.game_versions || [])`} {
		if !strings.Contains(discover, want) {
			t.Fatal("complete Compatibility metadata lost", want)
		}
	}
	inventory := between("  const renderInstalled =", "  // Review, progress")
	if !strings.Contains(inventory, `const projectID = plugin.project_id || undefined;`) || !strings.Contains(inventory, `}, projectID)`) {
		t.Fatal("inventory must reuse only known authoritative project identity")
	}
	identity := between("  const identityTile =", "  const overlayHeader =")
	if strings.Index(identity, `document.createElement("img")`) < strings.Index(identity, `if (typeof projectID === "string"`) || !strings.Contains(identity, `image.remove()`) {
		t.Fatal("unidentified and failed icons must retain fallback")
	}
	review := between("  const reviewPlugin =", "  const resumePluginInstall =")
	for _, want := range []string{`layout.className = "plugins-review-layout"`, `primary.className = "plugins-review-primary"`, `secondary.className = "plugins-review-secondary"`, `layout.append(primary, secondary)`, `overlaySection("Server compatibility", primary)`, `overlaySection("Dependencies", primary)`, `overlaySection("Files to install", primary)`, `overlaySection("Installation location", primary)`, `overlaySection("Version details", secondary)`, `overlaySection("Restart notice", secondary)`, `overlaySection("Players", secondary)`, `readableSize(artifact.size)`, `project?.version?.id === plan.version_id`, `project?.project_id === plan.project_id && project.source`} {
		if !strings.Contains(review, want) {
			t.Fatal("two-area Review structure missing", want)
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
	for _, want := range []string{`.plugins-install-stages{display:flex;flex-direction:column;align-items:flex-start;`, `.plugins-install-stages li::before{content:"○"`, `.plugins-install-stages .is-complete::before{content:"✓"`, `.plugins-install-stages [aria-current="step"]::before{content:"●"`, `.plugins-review-layout{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(0,1fr);`, `@container(max-width:500px){.plugins-review-layout{grid-template-columns:minmax(0,1fr);`, `.plugins-review-layout .plugins-info-rows{grid-template-columns:minmax(0,1fr);`} {
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
		if len(inventory.Plugins) != 2 {
			t.Fatal("inventory changed", inventory)
		}
		manual, managed := inventory.Plugins[0], inventory.Plugins[1]
		if manual.Management != "manual" || manual.ProjectID != "" || manual.VersionID != "" {
			t.Fatal("manual identity inferred", manual)
		}
		if managed.Management != "plugins" || managed.ProjectID != "project123" || managed.VersionID != "release123" {
			t.Fatal("verified identity lost", managed)
		}
		var wire struct {
			Plugins []map[string]json.RawMessage `json:"plugins"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		if _, exists := wire.Plugins[0]["project_id"]; exists {
			t.Fatal("unidentified project ID must be omitted")
		}
		if _, exists := wire.Plugins[0]["version_id"]; exists {
			t.Fatal("unidentified release ID must be omitted")
		}
	}
}
