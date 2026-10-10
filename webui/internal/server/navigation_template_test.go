package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestControlCenterNavigationUX(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	for _, want := range []string{
		`href="/" aria-label="JustVoxel dashboard"`,
		`<img class="brand-logo" src="/static/justvoxel-logo.png" alt="JustVoxel">`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("authenticated header branding missing %q", want)
		}
	}
	if strings.Contains(markup, `<strong>JV</strong>`) {
		t.Fatal("authenticated header must use the approved logo")
	}
	navigationStart := strings.Index(markup, `<nav class="control-navigation"`)
	if navigationStart < 0 {
		t.Fatal("Control Center navigation missing")
	}
	navigationEnd := strings.Index(markup[navigationStart:], `</nav>`)
	if navigationEnd < 0 {
		t.Fatal("Control Center navigation missing")
	}
	navigation := markup[navigationStart : navigationStart+navigationEnd]
	for _, heading := range []string{"Session &amp; power", "Minecraft", "System", "Storage &amp; Data"} {
		if !strings.Contains(markup, `<span class="control-section-label">`+heading+`</span>`) {
			t.Fatalf("missing Control Center group %s", heading)
		}
	}
	previous := -1
	for _, heading := range []string{"Session &amp; power", "Minecraft", "System", "Storage &amp; Data"} {
		position := strings.Index(markup, `<span class="control-section-label">`+heading+`</span>`)
		if position <= previous {
			t.Fatalf("Control Center group %s is out of order", heading)
		}
		previous = position
	}
	if strings.Contains(navigation, "Temporary") {
		t.Fatal("workspace launchers still labeled Temporary")
	}
	for _, item := range []string{"data-minecraft-open", "data-system-open", "data-network-open", "data-system-monitor-open", "data-system-update-open", "data-storage-open", "data-backups-open", "data-migration-open"} {
		if strings.Count(navigation, item) != 1 {
			t.Fatalf("workspace launcher %s must appear once", item)
		}
	}
	for _, old := range []string{`href="/activity"`, `href="/operations`, `href="/settings/`, `href="/password"`, `href="/about"`} {
		if strings.Contains(navigation, old) {
			t.Fatalf("legacy standalone link remains: %s", old)
		}
	}
	for _, item := range []string{`class="control-tile nav-admin-only" type="button" data-system-update-open`, `class="control-tile nav-admin-only" type="button" data-storage-open`, `class="control-tile nav-admin-only" type="button" data-backups-open`, `class="control-tile nav-admin-only" type="button" data-migration-open`} {
		if !strings.Contains(navigation, item) {
			t.Fatalf("administrator launcher lost role restriction: %s", item)
		}
	}
	if !strings.Contains(markup, `class="nav-logout"`) || !strings.Contains(markup, `data-system-power`) {
		t.Fatal("session and power actions missing")
	}
	quickLookStart := strings.Index(markup, `<section class="quick-look"`)
	if quickLookStart < 0 {
		t.Fatal("Quick Look missing")
	}
	quickLookEnd := strings.Index(markup[quickLookStart:], `</section>`)
	if quickLookEnd < 0 {
		t.Fatal("Quick Look closing tag missing")
	}
	quickLook := markup[quickLookStart : quickLookStart+quickLookEnd]
	if !strings.Contains(quickLook, `<button class="quick-look-backup-action nav-operator-only" type="button" data-quick-look-backup-create>`) {
		t.Fatal("operator manual backup needs an accessible native Quick Look button")
	}
	if strings.Contains(markup, `/operations#manual-backup`) {
		t.Fatal("header still links to the legacy manual backup operation")
	}
}

func TestMinecraftWorkspaceMigrationContract(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)

	minecraftSection := strings.Index(markup, `<span class="control-section-label">Minecraft</span>`)
	minecraftTile := strings.Index(markup, `data-minecraft-open`)
	if minecraftSection < 0 || minecraftTile < minecraftSection {
		t.Fatal("Minecraft launcher is missing from its group")
	}

	for _, want := range []string{
		`data-workspace-window="minecraft"`,
		`data-minecraft-tab="overview"`,
		`data-minecraft-tab="memory"`,
		`data-minecraft-tab="gameplay"`,
		`data-minecraft-tab="players"`,
		`data-minecraft-tab="crossplay"`,
		`data-version-open`,
		`data-version-tab="software"`,
		`data-version-tab="minecraft"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("Minecraft workspace migration contract missing %q", want)
		}
	}
	if strings.Contains(markup, `data-minecraft-tab="whitelist"`) {
		t.Fatal("Minecraft workspace still contains the stale whitelist tab")
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	behavior := string(script)
	for _, want := range []string{
		`document.querySelector("[data-minecraft-open]")`,
		`requestWorkspaceJSON("/api/dashboard-status")`,
		`"/api/minecraft/workspace/settings"`,
		`"/api/minecraft/workspace/settings/plan"`,
		`"/api/minecraft/workspace/settings/apply"`,
		`"/api/minecraft/workspace/whitelist"`,
		`"/api/minecraft/workspace/logs"`,
		`section.className = "panel details minecraft-overview-logs"`,
		`title.textContent = "Recent logs"`,
		`const settingsTabs = new Set(["memory", "gameplay", "crossplay"])`,
	} {
		if !strings.Contains(behavior, want) {
			t.Fatalf("Minecraft workspace behavior missing %q", want)
		}
	}
}

func TestSystemWorkspaceMigrationContract(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)

	systemSection := strings.Index(markup, `<span class="control-section-label">System</span>`)
	systemTile := strings.Index(markup, `data-system-open`)
	if systemSection < 0 || systemTile < systemSection {
		t.Fatal("System launcher is missing from its group")
	}

	for _, want := range []string{
		`data-workspace-window="system"`,
		`data-system-tab="health"`,
		`data-system-tab="history"`,
		`data-system-tab="users"`,
		`data-system-tab="security"`,
		`data-system-tab="about"`,
		`data-system-tab="ups"`,
		`data-system-ups-tab`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("System workspace migration contract missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	behavior := string(script)
	for _, want := range []string{
		`document.querySelector("[data-system-open]")`,
		`"/api/system/workspace/health"`,
		`"/api/system/workspace/history"`,
		`"/api/system/workspace/users"`,
		`"/api/system/workspace/security"`,
		`"/api/system/workspace/about"`,
		`fetch("/api/ups"`,
		`action.pathname !== "/api/ups/source"`,
		`host.placeholder = "Hostname or IP address"`,
		`options.textContent = "Options"`,
		`actions.append(options, save)`,
		`finalRow.append(nameLabel, actions)`,
		`form.append(csrf, modeLabel, hostLabel, portLabel, driverLabel, deviceLabel, finalRow)`,
		`title.textContent = "UPS options"`,
		`section.textContent = "Saved UPS source"`,
		`copy.textContent = "Remove this saved UPS connection and return to a UPS connected directly to this machine."`,
		`forget.textContent = "Remove saved source"`,
		`title.textContent = "Remove saved source?"`,
		`forget.textContent = "Confirm removal"`,
		`"/api/ups/source/forget"`,
		`"/api/ups/shutdown"`,
		`"/api/ups/sharing"`,
		`shutdownTitle.textContent = "Automatic shutdown"`,
		`"Shut down after UPS has been on battery"`,
		`"Shutdown delay (seconds)"`,
		`sharingTitle.textContent = "Network sharing"`,
		`"Share this UPS over the network"`,
		`sharingToggle.field.classList.add("system-ups-shutdown-switch")`,
		`sharingToggle.input.setAttribute("role", "switch")`,
		`sharingToggle.input.disabled = true`,
		`const sharingAvailable = selectedMode === "local"`,
		`if (snapshot.available && user?.role === "administrator")`,
		`if (!snapshot.available) return`,
		`name.name = "ups_name"`,
		`driver.name = "driver"`,
		`device.name = "device_port"`,
		`monitorPassword = settingField("Monitor password", "monitor_password", "password", "")`,
		`clientPassword = settingField("Client password", "client_password", "password", "")`,
		`snapshot.source.host + ":" + snapshot.source.port`,
		`data-system-workspace-csrf`,
	} {
		if !strings.Contains(behavior, want) && !strings.Contains(markup, want) {
			t.Fatalf("System workspace behavior missing %q", want)
		}
	}
	for _, old := range []string{"192.168.0.51", "blackbox.lan"} {
		if strings.Contains(behavior, old) {
			t.Fatalf("UPS UI contains private example %q", old)
		}
	}

	systemStart := strings.Index(behavior, `const systemWorkspaceOpen = document.querySelector("[data-system-open]")`)
	if systemStart < 0 {
		t.Fatal("System workspace source block missing")
	}
	systemBlock := behavior[systemStart:]
	for _, forbidden := range []string{
		`new DOMParser()`,
		`systemFetchPage("/settings/validation")`,
		`"/settings/activity"`,
		`systemFetchPage("/settings/users")`,
		`systemFetchPage("/settings/authentication")`,
		`systemFetchPage("/about")`,
	} {
		if strings.Contains(systemBlock, forbidden) {
			t.Fatalf("System workspace still depends on legacy rendering path %q", forbidden)
		}
	}
}

func TestWorkspaceCleanupLayoutContract(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	headerClose := strings.Index(markup, "</header>")
	quickLook := strings.Index(markup, `class="quick-look"`)
	systemMonitor := strings.Index(markup, `data-workspace-window="system-monitor"`)
	if headerClose < 0 || quickLook < 0 || systemMonitor < 0 || headerClose > quickLook || headerClose > systemMonitor {
		t.Fatal("topbar must close before Quick Look and workspace windows")
	}
	if strings.Contains(markup, `data-workspace-resize-handle`) {
		t.Fatal("workspaces must use native corner resizing")
	}
	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".quick-look{position:fixed", ".system-security-switcher", ".system-health-view", ".password-field-shell", ".password-reveal-button", ".destructive-confirm-slider", "min-width:min(620px"} {
		if !strings.Contains(string(styles), want) {
			t.Fatalf("workspace cleanup CSS missing %q", want)
		}
	}
}

func TestQuickLookNetworkFitsSingleTileWithoutTruncatingAddresses(t *testing.T) {
	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(styles)
	if strings.Contains(css, ".quick-look-network-tile{grid-column:span 2") {
		t.Fatal("network Quick Look tile still spans two columns")
	}
	for _, want := range []string{
		".quick-look-grid{display:grid;grid-template-columns:repeat(6,minmax(0,1fr))",
		"@media(max-width:900px){.quick-look-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}",
		".quick-look-network-tile{gap:.15rem;justify-content:center;height:auto",
		".quick-look-network-row,.network-overlay-list>span{display:grid!important;grid-template-columns:3.8rem minmax(0,1fr)",
		".quick-look-network-row>strong,.network-overlay-list>span>strong{min-width:0;overflow-wrap:anywhere",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("network Quick Look layout missing %q", want)
		}
	}
}

func TestBackupsLibraryLeadsWorkspaceAndOwnsActions(t *testing.T) {
	content, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(content)
	library := strings.Index(markup, `class="backup-library-section"`)
	automatic := strings.Index(markup, `class="backup-automatic-section"`)
	destination := strings.Index(markup, `class="backup-destination-section"`)
	if library < 0 || automatic < 0 || destination < 0 || !(library < automatic && automatic < destination) {
		t.Fatal("Backup library must be the first main Backups section")
	}
	command := strings.Index(markup, `class="backup-command-bar"`)
	files := strings.Index(markup, `class="backup-file-grid"`)
	if command < library || (files >= 0 && command < files) || command > automatic {
		t.Fatal("Backup controls must live inside Backup library below the backup files")
	}
	styles, err := assets.ReadFile("static/new-backups.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(styles), ".backup-destination-change>summary{display:flex") {
		t.Fatal("Change destination must be a normal right-aligned action button")
	}

	for _, want := range []string{"data-backup-delete-confirmation-control", "data-destructive-slider", "data-destructive-toggle", "data-destructive-submit"} {
		if !strings.Contains(markup, want) {
			t.Fatalf("Backups destructive confirmation UI missing %q", want)
		}
	}
	if strings.Contains(markup, "Type exactly") || strings.Contains(markup, "Type RESTORE to continue") {
		t.Fatal("Backups workspace still requires typed destructive confirmation phrases")
	}
}

func TestMigrationWorkspaceUsesIndependentWorkspaceRoutes(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	for _, want := range []string{
		"data-migration-open",
		`data-workspace-window="migration"`,
		`data-migration-tab="export"`,
		`data-migration-tab="import"`,
		`data-migration-tab="recovery"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("Migration workspace markup missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	migrationStart := strings.Index(source, `const migrationOpen = document.querySelector("[data-migration-open]")`)
	monitorStart := strings.Index(source, `const systemMonitorOpen = document.querySelector("[data-system-monitor-open]")`)
	if migrationStart < 0 || monitorStart <= migrationStart {
		t.Fatal("Migration workspace source block missing")
	}
	block := source[migrationStart:monitorStart]
	if !strings.Contains(block, `const sourceKind = body.get("source_kind") || ""`) {
		t.Fatal("Migration SMB password replay must remain scoped to the Migration workspace")
	}
	for _, want := range []string{
		`document.querySelector("[data-migration-open]")`,
		`"/workspace/migration/export"`,
		`"/workspace/migration/import"`,
		`"/workspace/migration/recovery"`,
		`window.JustVoxelServerMigrationExport?.init(root)`,
		`window.JustVoxelServerMigrationImport?.init(root)`,
		`window.JustVoxelServerMigrationOperation?.init(root)`,
		`let migrationSourceSMBPassword = ""`,
		`root.querySelectorAll("[data-migration-source-password-repeat]")`,
		`data-migration-workspace-root`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Migration workspace behavior missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`/settings/server-migration`,
		`new DOMParser()`,
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("Migration workspace still depends on legacy rendering %q", forbidden)
		}
	}

	for _, name := range []string{
		"migration_workspace.html",
		"migration_workspace_export.html",
		"migration_workspace_export_review.html",
		"migration_workspace_import.html",
		"migration_workspace_import_review.html",
		"migration_workspace_recovery.html",
		"migration_workspace_recovery_review.html",
		"migration_workspace_progress.html",
	} {
		templateBytes, err := assets.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		template := string(templateBytes)
		if !strings.Contains(template, "data-migration-workspace-root") {
			t.Fatalf("Migration workspace fragment %s is missing its workspace root", name)
		}
		if strings.Contains(template, "/settings/server-migration") {
			t.Fatalf("Migration workspace fragment %s still references legacy routes", name)
		}
	}
}

func TestAuthenticatedTemplatesUseSharedHeader(t *testing.T) {
	templates, err := assets.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range templates {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		content, err := assets.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		markup := string(content)
		favicon := `<link rel="icon" type="image/png" href="/static/justvoxel-favicon.png">`
		headStart := strings.Index(markup, "<head>")
		if headStart < 0 {
			if strings.Contains(markup, favicon) {
				t.Fatalf("partial %s must not own a favicon", entry.Name())
			}
			continue
		}
		headEnd := strings.Index(markup[headStart:], "</head>")
		if headEnd < 0 || !strings.Contains(markup[headStart:headStart+headEnd], favicon) {
			t.Fatalf("full document %s must reference the approved favicon in its head", entry.Name())
		}
	}
	for _, name := range []string{
		"dashboard.html",
		"storage_browser.html",
	} {
		content, err := assets.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		markup := string(content)
		if !strings.Contains(markup, `{{template "app-header" .}}`) {
			t.Fatalf("%s does not use shared appliance header", name)
		}
		if !strings.Contains(markup, `/static/app.js`) {
			t.Fatalf("%s does not load shared navigation behavior", name)
		}
	}
	password, err := assets.ReadFile("templates/password.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(password), `{{template "app-header" .}}`) || strings.Contains(string(password), `/static/app.js`) {
		t.Fatal("password page must remain outside shared navigation")
	}
}

func TestStandaloneOperationsUIRetired(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	// The GET / catch-all makes retired POST routes return 405, proving
	// their POST handlers are no longer registered.
	for _, route := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/operations", http.StatusNotFound},
		{http.MethodPost, "/operations/backup", http.StatusMethodNotAllowed},
		{http.MethodPost, "/operations/whitelist", http.StatusMethodNotAllowed},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := httptestResponse(app, authenticatedAdminRequest(route.method, route.path, ""))
			if response.Code != route.status {
				t.Fatalf("retired route returned %d, want HTTP %d", response.Code, route.status)
			}
		})
	}
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), `path === "/operations"`) {
		t.Fatal("navigation retains the retired Operations branch")
	}
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(header), `href="/operations`) || strings.Contains(string(header), `href='/operations`) {
		t.Fatal("header links to retired Operations UI")
	}
	if _, err := assets.ReadFile("templates/operations.html"); err == nil {
		t.Fatal("retired Operations template remains embedded")
	}
}

func TestSetupTemplatesUseThinTopbar(t *testing.T) {
	for _, name := range []string{"setup_wizard.html", "setup_review.html", "setup_progress.html"} {
		content, err := assets.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		markup := string(content)
		branding := `<img class="brand-logo" src="/static/justvoxel-logo.png" alt="JustVoxel">`
		for _, want := range []string{
			`class="setup-header topbar setup-topbar"`,
			`class="brand-link brand-mark"`,
			branding,
			`data-topbar-clock`,
			`/static/app.js`,
		} {
			if !strings.Contains(markup, want) {
				t.Fatalf("%s thin setup topbar missing %q", name, want)
			}
		}
	}
}

func TestDashboardCompactResponsiveLayout(t *testing.T) {
	content, err := assets.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(content)
	for _, want := range []string{
		`data-configured="{{.Status.Minecraft.Configured}}"`,
		`data-dashboard-setup-area`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("compact dashboard missing %q", want)
		}
	}
	if strings.Contains(markup, `dashboard-summary`) {
		t.Fatal("dashboard contains the legacy summary strip")
	}
	if strings.Contains(markup, "<h2>Appliance</h2>") {
		t.Fatal("dashboard contains oversized Appliance panel")
	}

	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, want := range []string{
		".dashboard-wallpaper{position:fixed;inset:0;width:100%;height:100dvh;object-fit:cover;object-position:center",
		"#dashboard::before{content:\"\";position:fixed;inset:0;",
		// The 40px floating panel plus its vertical margins still occupies 48px.
		"min-height:calc(100dvh - 48px)",
		".app-header.topbar{",
		".dashboard-setup-area{display:grid;place-items:center",
		"#dashboard .dashboard-setup-area .minecraft-setup-invitation{width:min(560px",
		"@media(max-width:900px)",
		"@media(max-width:600px){\n  .app-header.topbar{margin:4px;gap:.2rem;padding:2px .3rem}",
		"@media(max-width:600px){.dashboard-wallpaper.wallpaper-theme-v2{object-position:78% center}}",
		"inset:52px .25rem .25rem .25rem!important",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("compact dashboard styling missing %q", want)
		}
	}
	// Inspect the final height rule, which overrides the original full-width panel.
	headerStart := strings.LastIndex(styles, ".app-header.topbar{height:")
	if headerStart < 0 {
		t.Fatal("authenticated header height rule is missing")
	}
	headerEnd := strings.Index(styles[headerStart:], "}")
	if headerEnd < 0 {
		t.Fatal("authenticated header rule is incomplete")
	}
	header := styles[headerStart+len(".app-header.topbar{") : headerStart+headerEnd]
	for _, want := range []string{"height:40px", "min-height:40px", "margin:4px 8px", "top:4px", "border-radius:12px"} {
		if !strings.Contains(";"+header+";", ";"+want+";") {
			t.Fatalf("authenticated header must retain %s", want)
		}
	}
	if !strings.Contains(markup, `<img class="dashboard-wallpaper wallpaper-theme-v2" data-dashboard-wallpaper src="/static/wallpapers/jv-wp-v2-day.webp" alt="" aria-hidden="true">`) {
		t.Fatal("dashboard must include the decorative V2 Day built-in wallpaper fallback")
	}
	if _, err := assets.ReadFile("static/justvoxel-default-wallpaper.jpg"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(styles, "conic-gradient(") {
		t.Fatal("dashboard must not retain the generated voxel wallpaper")
	}
	for _, want := range []string{"object-fit:cover", ".dashboard-wallpaper.wallpaper-theme-v2{object-position:65% center}", `#dashboard::before`} {
		if !strings.Contains(styles, want) {
			t.Fatalf("dashboard wallpaper behavior missing %q", want)
		}
	}
	appJS, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"const workspaceTopInset = () => {", "Math.round((topbar?.getBoundingClientRect().bottom || 0) + 8)", "const minTop = workspaceTopInset();"} {
		if !strings.Contains(string(appJS), want) {
			t.Fatalf("workspace clearance below the floating panel missing %q", want)
		}
	}
	for _, removed := range []string{"spotlightActive", "hideSpotlight", "--voxel-x", "--voxel-y", `dashboard.addEventListener("pointermove"`} {
		if strings.Contains(string(appJS), removed) {
			t.Fatalf("dashboard retains removed spotlight behavior %q", removed)
		}
	}
	for _, removed := range []string{"#dashboard::after", "data-spotlight-active", "--voxel-x", "--voxel-y"} {
		if strings.Contains(styles, removed) {
			t.Fatalf("dashboard retains removed spotlight styling %q", removed)
		}
	}
}

func TestWallpaperSettingsUX(t *testing.T) {
	content, err := assets.ReadFile("templates/project_footer.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(content)
	for _, want := range []string{
		`name="mode" value="builtin" hidden checked`,
		`<fieldset class="wallpaper-themes">`,
		`<legend class="muted">Built-in</legend>`,
		`name="theme" value="v1">V1`,
		`name="theme" value="v2" checked>V2`,
		`<fieldset class="wallpaper-appearance"`,
		`<legend class="muted">Appearance</legend>`,
		`name="appearance" value="auto" checked>Automatic`,
		`name="appearance" value="day">Day`,
		`name="appearance" value="night">Night`,
		`Day starts<input type="time" name="dayStart" value="07:00"`,
		`Night starts<input type="time" name="nightStart" value="19:00"`,
		`<fieldset class="wallpaper-custom">`,
		`<legend class="muted">Custom</legend>`,
		`<input type="radio" name="mode" value="url">Web address`,
		`<input type="radio" name="mode" value="upload">Upload picture`,
		`data-wallpaper-reset>Reset to default</button>`,
		`data-wallpaper-cancel>Cancel</button>`,
		`<button type="submit">Apply</button>`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("wallpaper settings markup missing %q", want)
		}
	}
	for _, forbidden := range []string{`Default JustVoxel`, `value="default"`} {
		if strings.Contains(markup, forbidden) {
			t.Fatalf("wallpaper settings still contains duplicate default choice %q", forbidden)
		}
	}

	content, err = assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(content)
	for _, want := range []string{
		".wallpaper-panel{width:100%;max-width:760px;margin:0 auto;overflow:visible}",
		".wallpaper-panel .wallpaper-theme-card{display:grid",
		".wallpaper-split-preview{display:grid;grid-template-columns:1fr 1fr",
		".wallpaper-panel .wallpaper-appearance{grid-template-columns:repeat(3,minmax(0,1fr))}",
		".wallpaper-schedule{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))",
		".dashboard-wallpaper.wallpaper-theme-v1{object-position:",
		".dashboard-wallpaper.wallpaper-theme-v2{object-position:",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("wallpaper settings styling missing %q", want)
		}
	}
	if strings.Contains(styles, ".wallpaper-panel{max-height:calc(100dvh - 2rem);overflow:auto}") {
		t.Fatal("wallpaper dialog must not retain the old internal scrolling layout")
	}

	content, err = assets.ReadFile("static/wallpaper.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, want := range []string{
		`mode: "builtin", theme: "v2", appearance: "auto", dayStart: "07:00", nightStart: "19:00"`,
		`if (saved.mode === "default") return defaultPreference();`,
		"const reset = async () => {\n    preference = defaultPreference();",
		`savePreference(preference)`,
		`await storedImage("delete")`,
		`try { await reset(); }`,
		`input.checked = input.value === preference.mode`,
		`form.querySelector('button[type="submit"]').disabled = busy;`,
		`hidden = mode !== "url"`,
		`hidden = mode !== "upload"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("wallpaper settings behavior missing %q", want)
		}
	}
	submitStart := strings.Index(script, `form.addEventListener("submit"`)
	if submitStart < 0 {
		t.Fatal("wallpaper Apply handler missing")
	}
	for _, want := range []string{
		`if (!["builtin", "url", "upload"].includes(mode)) return;`,
		`if (mode === "builtin")`,
		`savePreference(next)`,
		`else if (mode === "url")`,
		`savePreference({ mode: "url", url })`,
		`else if (mode === "upload")`,
		`savePreference({ mode: "upload" })`,
	} {
		if !strings.Contains(script[submitStart:], want) {
			t.Fatalf("wallpaper Apply behavior missing %q", want)
		}
	}
}

func TestStorageBrowserInteractionContract(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`data-storage-disk`,
		`data-storage-partitions`,
		`data-storage-partition`,
		`data-storage-detail-dialog`,
		`data-storage-detail-close`,
		`data-storage-whole-disk`,
		`data-storage-whole-disk-dialog`,
		`storage-partition-swap`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("storage browser markup missing %q", want)
		}
	}

	scriptContent, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptContent)
	for _, want := range []string{
		`function showDetails(panel)`,
		`if (options.inlineDetails) panel.hidden = false;`,
		`else panel.showModal();`,
		`showDetails(detailDialog);`,
		`function hideDetails(panel)`,
		`if (options.inlineDetails) panel.hidden = true;`,
		`else panel.close();`,
		`hideDetails(detailDialog);`,
		`detailClose?.addEventListener("click"`,
		`detailDialog?.addEventListener("cancel", closeActionMenu)`,
		`"/api/new-storage/whole-disk/" + phase`,
		`button.setAttribute("aria-pressed"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("storage browser behavior missing %q", want)
		}
	}
}

func TestStorageBrowserResponsiveStyling(t *testing.T) {
	content, err := assets.ReadFile("static/storage-browser.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(content)
	for _, want := range []string{
		".storage-disk-grid{display:grid",
		".storage-partition-grid{display:grid",
		".storage-detail-dialog::backdrop",
		".storage-partition-swap",
		"@media(max-width:700px)",
		"@media(max-width:420px)",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("storage browser responsive styling missing %q", want)
		}
	}
}

func TestStorageBrowserReviewedActionFlow(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`data-storage-action-menu`,
		`data-storage-action="mount-for-now"`,
		`data-storage-action="mount-permanently"`,
		`data-storage-action="make-permanent"`,
		`data-storage-action="mount-now"`,
		`data-storage-action="unmount-for-now"`,
		`data-storage-action="remove-permanent"`,
		`data-storage-action="format"`,
		`data-storage-action-dialog`,
		`data-storage-action-review-button`,
		`data-storage-action-apply-button`,
		`data-storage-confirm-slider`,
		`data-storage-confirm-toggle`,
		`data-storage-protected-note`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("storage action markup missing %q", want)
		}
	}

	scriptContent, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptContent)
	for _, want := range []string{
		`"/api/new-storage/" + request.family + "/" + phase`,
		`family: "actions"`,
		`family: "mounts"`,
		`reviewedPlan.fingerprint`,
		`data.system === "Yes"`,
		`data.readonly === "Yes"`,
		`refreshStorageWorkspace();`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("storage action behavior missing %q", want)
		}
	}

	cssContent, err := assets.ReadFile("static/storage-browser.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(cssContent)
	for _, want := range []string{
		".storage-action-menu-popover",
		".storage-action-dialog::backdrop",
		".storage-action-danger",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("storage action styling missing %q", want)
		}
	}
}

func TestStorageBrowserMinecraftMigrationHandoff(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`data-minecraft-candidate=`,
		`data-storage-minecraft-migrate`,
		`data-storage-minecraft-dialog`,
		`data-storage-minecraft-review`,
		`data-storage-minecraft-apply`,
		`data-storage-minecraft-progress`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("New Storage Minecraft migration handoff missing %q", want)
		}
	}
	if strings.Contains(markup, "/settings/data-migration") {
		t.Fatal("New Storage still posts Minecraft migration to the legacy Data Migration page")
	}

	scriptContent, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptContent)
	for _, want := range []string{
		`selectedPartition.minecraftCandidate !== "Yes"`,
		`migrationMount.readOnly = Boolean(existingMount)`,
		`"/var/mnt/justvoxel-data"`,
		`migrationDialog.showModal()`,
		`"/api/new-storage/minecraft-data/plan"`,
		`"/api/new-storage/minecraft-data/apply"`,
		`"/api/new-storage/minecraft-data/progress/"`,
		`showMigrationProgress(payload.operation)`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("New Storage Minecraft migration behavior missing %q", want)
		}
	}
	if strings.Contains(script, "/settings/data-migration") {
		t.Fatal("New Storage JavaScript still depends on the legacy Data Migration page")
	}
}

func TestBackupsWorkspaceCompactLibraryLayout(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	loaderContent, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`loadBackupsScript("/static/new-backups.js"`,
		`loadBackupsScript("/static/restore-operation.js"`,
		`window.JustVoxelNewBackups?.init(root)`,
		`window.JustVoxelRestoreOperation?.init(root)`,
	} {
		if !strings.Contains(string(loaderContent), want) {
			t.Fatalf("Backups workspace loader missing %q", want)
		}
	}
	for _, want := range []string{
		"Backup Now",
		"Restore selected",
		"backup-command-bar",
		"backup-file-grid",
		"backup-file-card",
		"/workspace/backups/backup",
		"/workspace/backups/restore/plan",
		"/workspace/backups/restore/apply",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("New Backups template missing %q", want)
		}
	}
	for _, forbidden := range []string{`name="backup_schedule"`, `name="backup_destination"`} {
		if strings.Contains(markup, forbidden) {
			t.Fatalf("New Backups unexpectedly exposes unimplemented control %q", forbidden)
		}
	}

	cssContent, err := assets.ReadFile("static/new-backups.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(cssContent)
	for _, want := range []string{
		".backup-command-bar",
		".backup-file-grid{display:grid",
		"@media(max-width:700px)",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("New Backups styling missing %q", want)
		}
	}
}

func TestNewBackupsMobileLibraryStaysSingleColumn(t *testing.T) {
	content, err := assets.ReadFile("static/new-backups.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(content)
	for _, want := range []string{
		"@media(max-width:700px)",
		".backup-file-grid{grid-template-columns:1fr}",
		".backup-file-status-warning",
		".backup-command-bar{align-items:stretch;flex-direction:column}",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("New Backups mobile styling missing %q", want)
		}
	}
}

func TestBackupsWorkspaceReviewedMultiDeleteFlow(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	loaderContent, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`loadBackupsScript("/static/new-backups.js"`,
		`window.JustVoxelNewBackups?.init(root)`,
	} {
		if !strings.Contains(string(loaderContent), want) {
			t.Fatalf("Backups workspace loader missing %q", want)
		}
	}
	for _, want := range []string{
		`data-backup-select`,
		`data-backup-delete-selected`,
		`data-backup-delete-dialog`,
		`data-backup-delete-confirmation-control`,
		`data-destructive-slider`,
		`data-destructive-toggle`,
		`data-destructive-submit`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("New Backups delete UI missing %q", want)
		}
	}
	if strings.Contains(markup, "data-backup-delete-confirmation-phrase") || strings.Contains(markup, "Type exactly") {
		t.Fatal("New Backups delete UI still contains the retired typed-confirmation flow")
	}

	scriptContent, err := assets.ReadFile("static/new-backups.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptContent)
	for _, want := range []string{
		`"/api/new-backups/delete/plan"`,
		`"/api/new-backups/delete/apply"`,
		`reviewedPlan.fingerprint`,
		`reviewedPlan.confirmation`,
		`ids.forEach((id) => body.append("backup_id", id))`,
		`const workspaceURL = "/workspace/backups?result=deleted&count="`,
		`window.JustVoxelBackupsWorkspace?.reload`,
		`await window.JustVoxelBackupsWorkspace.reload(workspaceURL)`,
		`throw new Error("Backups Workspace reload is unavailable. Refresh the workspace to see the updated library.")`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("New Backups delete behavior missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`/settings/new-backups`,
		`pageURL`,
		`window.location.assign(pageURL)`,
		`main.new-backups-shell`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Backups workspace delete behavior retains retired standalone UI %q", forbidden)
		}
	}

	cssContent, err := assets.ReadFile("static/new-backups.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(cssContent)
	for _, want := range []string{
		".backup-file-selectable:has(.backup-file-checkbox:checked)",
		".backup-delete-dialog::backdrop",
		".backup-delete-review-list",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("New Backups delete styling missing %q", want)
		}
	}
}

func TestNewBackupsAutomaticPolicyControls(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`name="automatic_enabled"`,
		`name="daily_time" type="time"`,
		`name="backup_keep" type="number"`,
		`/workspace/backups/automatic/plan`,
		`/workspace/backups/automatic/apply`,
		"After a successful backup, JustVoxel automatically removes older backups",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("New Backups automatic policy UI missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`name="backup_schedule"`,
		"Systemd calendar format",
	} {
		if strings.Contains(markup, forbidden) {
			t.Fatalf("New Backups exposed raw scheduler control %q", forbidden)
		}
	}

	cssContent, err := assets.ReadFile("static/new-backups.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(cssContent)
	for _, want := range []string{
		".backup-automatic-section",
		".backup-automatic-form{display:grid",
		"@media(max-width:520px)",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("New Backups automatic policy styling missing %q", want)
		}
	}
}

func TestNewBackupsAutomaticPolicyResponsiveLayout(t *testing.T) {
	content, err := assets.ReadFile("static/new-backups.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(content)
	for _, want := range []string{
		"@media(max-width:850px)",
		".backup-automatic-form{grid-template-columns:1fr 1fr}",
		"@media(max-width:520px)",
		".backup-automatic-form{grid-template-columns:1fr}",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("New Backups automatic policy responsive styling missing %q", want)
		}
	}
}

func TestCompactUsersAndFiveRowMonitorContract(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	js, css := string(script), string(styles)
	start := strings.Index(js, "  const renderUsers =")
	end := strings.Index(js[start:], "  const renderSecurity =")
	users := js[start : start+end]
	for _, want := range []string{"system-users-table", "Username", "Role", "Status", "Restart", "Backup", "Actions", "storage-action-menu system-user-menu", "dialog.showModal()", "heading.textContent = title +", "user.restart_used ?? 0", "user.backup_used ?? 0", "Primary administrator", "minimum_password_len", "restart-allowance/reset", "backup-allowance/reset", `"role", "Save role"`, `"enabled", user.enabled`, `"password", "Change password"`, `"delete", "Delete account", true`, "data-destructive-slider", "data-destructive-toggle", "submit.className = \"danger\""} {
		if !strings.Contains(users, want) {
			t.Fatalf("compact Users contract missing %q", want)
		}
	}
	for _, removed := range []string{`card.className = "user-card"`, `quota.className = "quota-grid"`, "managementRow", "openManagement"} {
		if strings.Contains(users, removed) {
			t.Fatalf("Users retains expanded card presentation %q", removed)
		}
	}
	for _, want := range []string{
		`.system-users-view .user-list{display:block;min-height:0;overflow:auto`,
		`.system-monitor-card:is([data-monitor-card="filesystem"],[data-monitor-card="diskio"],[data-monitor-card="network"]) .system-monitor-table{max-height:calc(9.36rem + 6px)}`,
		`.system-monitor-card:is([data-monitor-card="filesystem"],[data-monitor-card="diskio"],[data-monitor-card="network"]) :is(th,td){line-height:1rem}`,
		`.system-monitor-card[data-monitor-card="processes"] .system-monitor-table{max-height:none}`,
		`.system-monitor-table th{position:sticky;top:0`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("compact table styling missing %q", want)
		}
	}
}
