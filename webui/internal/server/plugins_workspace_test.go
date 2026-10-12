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
	for _, unsafe := range []string{"api.modrinth.com", "innerHTML", "outerHTML", "insertAdjacentHTML", `createElement("img")`, "showModal("} {
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
	for _, want := range []string{`addInfo(card, project.title)`, `"plugins-card-description"`, `counts.set(kind, (counts.get(kind) || 0) + 1)`, `row("Dependencies", dependencyGroups(dependencies))`, `row("Release ID", version.id)`, `project.metadata_error`} {
		if !strings.Contains(discover, want) {
			t.Fatalf("Discover summary or Info metadata missing %s", want)
		}
	}
	inventory := behavior[installed : installed+end]
	if !strings.Contains(inventory, `const name = plugin.name || plugin.filename;`) || !strings.Contains(inventory, `card.append(text("h3", name)`) {
		t.Fatal("Installed/Updates title must fall back to the JAR filename when plugin.name is empty")
	}
	for _, want := range []string{`addInfo(card, name)`, `row("JAR", plugin.filename)`, `row("Metadata", plugin.metadata_status`, `row("Management", management)`, `row("Update status", updateStatus)`} {
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
		`pluginRequest("install/plan"`, `plan.project_name`, `section("Plugin")`, `plan.artifacts`, `plan.installation_location`,
		`if (plan.confirmation_required)`, `plan.players`, `confirm_players: String(plan.confirmation_required === true)`,
		`plan_fingerprint: plan.plan_fingerprint`, `operation.stage`, `operation.status`, `install-operation?id=`,
		`"View in Installed"`, `"Install another plugin"`, `"Done"`, `"Managed by Plugins"`, `"JustVoxel managed"`, `"Manual / unmanaged"`,
		`addInfo(card, project.title)`, `addInfo(card, name)`, `"Administrator attention required"`,
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
	detail := strings.Index(script[project:installed], "    const details = addInfo") + project
	discoverInfo := script[detail:installed]
	for _, repeated := range []string{"project.description", "project.compatibility", "version.version_number", "project.versions.join"} {
		if strings.Contains(discoverInfo, repeated) {
			t.Fatal("Info repeats collapsed metadata", repeated)
		}
	}
	for _, want := range []string{`row("Source"`, `row("Project ID"`, `row("Release ID"`, `row("Release type"`, `row("Published"`, `row("Platforms"`, `row("Supported Minecraft versions", disclosure)`, `new Set(version.game_versions || [])`, `text("summary", ` + "`${versions.length} supported Minecraft versions`", `versions.join(" · ")`, `row("Dependencies", dependencyGroups(dependencies))`} {
		if !strings.Contains(discoverInfo, want) {
			t.Fatal("missing additional Info", want)
		}
	}
	if strings.Contains(discoverInfo, "disclosure.open") || strings.Contains(discoverInfo, `setAttribute("open"`) {
		t.Fatal("versions expanded by default")
	}
	helpers := script[:project]
	for _, want := range []string{`["required", "Required"]`, `["optional", "Optional"]`, `["incompatible", "Incompatible"]`, `["other", "Embedded / other"]`, `dependency.name || dependency.title || dependency.file_name`, `plugins-dependency-ids`} {
		if !strings.Contains(helpers, want) {
			t.Fatal("dependency grouping missing", want)
		}
	}
	inventory := script[installed:strings.Index(script, "  // Review, progress")]
	for _, label := range []string{"JAR", "Metadata", "Management", "Update status"} {
		if strings.Count(inventory, `row("`+label+`",`) != 1 {
			t.Fatal("inventory concept duplicated", label)
		}
	}
	for _, duplicate := range []string{"Managed by JustVoxel’s", `text("p", management`, "Installed through Plugins."} {
		if strings.Contains(inventory, duplicate) {
			t.Fatal("duplicate ownership explanation", duplicate)
		}
	}
	review := script[strings.Index(script, "  const reviewPlugin ="):strings.Index(script, "  const resumePluginInstall =")]
	for _, want := range []string{`section("Plugin")`, `section("Compatibility")`, `section("Installation")`, `installation("Files to install"`, `installation("Required dependencies"`, `installation("Target plugins directory"`, `installation("Restart required"`, `if (plan.confirmation_required) {`, `section("Players")`} {
		if !strings.Contains(review, want) {
			t.Fatal("structured review missing", want)
		}
	}
	if strings.Count(review, "player-aware shutdown") != 1 || strings.Index(review, `section("Players")`) < strings.Index(review, "if (plan.confirmation_required)") {
		t.Fatal("player warning not conditional or duplicated")
	}
	progress := script[strings.Index(script, "  const showOperation ="):strings.Index(script, "  const pollOperation =")]
	for _, want := range []string{`["Prepare", "Backup", "Install", "Restart", "Verify"]`, `[operation.stage]`, `operation.state`, `operation.status`, `"Re-check after repair"`, `pluginRequest("install/acknowledge"`, `operation_id: operation.operation_id`, `"View in Installed"`, `"Install another plugin"`, `"Done"`} {
		if !strings.Contains(progress, want) {
			t.Fatal("persistent progress action missing", want)
		}
	}
	if strings.Count(progress, "operation.status") != 1 {
		t.Fatal("persistent status duplicated")
	}
	attention := progress[strings.Index(progress, "    if (attention) {"):strings.Index(progress, "    return terminal;")]
	if !strings.Contains(attention, `} else if (terminal)`) || strings.Contains(attention, `panelAction("Done", closePanel`) {
		t.Fatal("attention exposes blind Done")
	}
	for _, unsafe := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", `createElement("img")`, `createElement("svg")`, "artifact.url", "artifact.URL", "cdn.modrinth.com", "fakeSeconds", "elapsedSeconds", "countdown", "percent", "%"} {
		if strings.Contains(script, unsafe) {
			t.Fatal("unsafe or invented plugin presentation", unsafe)
		}
	}
	if !strings.Contains(script, "node.textContent = value") {
		t.Fatal("publisher rendering must remain text-only")
	}
}
