package server

import (
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
		`class="brand-link brand-mark" href="/"`,
		`aria-label="JustVoxel dashboard"`,
		`<strong>JV</strong>`,
		`data-topbar-clock`,
		`data-control-center`,
		`>Control Center</span>`,
		`data-system-power`,
		`href="/activity"`,
		`title="Server events and status"`,
		`href="/settings/server"`,
		`href="/operations#whitelist"`,
		`href="/operations#minecraft-logs"`,
		`href="/settings/storage"`,
		`href="/settings/data-migration"`,
		`href="/settings/backup-storage"`,
		`href="/operations#manual-backup"`,
		`href="/settings/restore"`,
		`href="/settings/server-migration"`,
		`href="/settings/activity"`,
		`href="/settings/validation"`,
		`href="/settings/users"`,
		`href="/settings/authentication"`,
		`href="/password"`,
		`href="/about"`,
		`class="nav-logout"`,
		`aria-label="Log out" data-tooltip="Log out"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("Control Center header missing %q", want)
		}
	}
	if strings.Contains(markup, `class="brand-name"`) {
		t.Fatal("brand still includes the long JustVoxel label")
	}

	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, want := range []string{
		`.topbar`,
		`.control-center-panel`,
		`.control-tile-grid`,
		`.nav-admin-only,.nav-operator-plus{display:none!important}`,
		`.control-tile.nav-admin-only,.control-tile.nav-operator-plus{display:none!important}`,
		`body.role-administrator .nav-admin-only`,
		`body.role-operator .nav-operator-plus`,
		`position:fixed;top:50px`,
		`:focus-visible`,
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("Control Center styling missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	behavior := string(script)
	for _, want := range []string{
		`document.querySelector("[data-control-center]")`,
		`document.querySelector("[data-topbar-clock]")`,
		`Intl.DateTimeFormat`,
		`controlCenter.open = false`,
		`event.key !== "Escape"`,
	} {
		if !strings.Contains(behavior, want) {
			t.Fatalf("Control Center behavior missing %q", want)
		}
	}
}

func TestAuthenticatedTemplatesUseSharedHeader(t *testing.T) {
	for _, name := range []string{
		"dashboard.html",
		"about.html",
		"operations.html",
		"activity.html",
		"admin_activity.html",
		"users.html",
		"authentication.html",
		"password.html",
		"validation.html",
		"server_settings.html",
		"storage_settings.html",
		"backup_storage.html",
		"restore.html",
		"restore_review.html",
		"restore_progress.html",
		"data_migration.html",
		"data_migration_review.html",
		"data_migration_progress.html",
		"server_migration.html",
		"server_migration_export.html",
		"server_migration_export_review.html",
		"server_migration_import.html",
		"server_migration_import_review.html",
		"server_migration_recovery.html",
		"server_migration_recovery_review.html",
		"server_migration_progress.html",
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
}

func TestOperationsExposeStableNavigationAnchors(t *testing.T) {
	content, err := assets.ReadFile("templates/operations.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(content)
	for _, want := range []string{
		`id="manual-backup"`,
		`id="whitelist"`,
		`id="minecraft-logs"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("operations template missing navigation anchor %q", want)
		}
	}
}

func TestSetupTemplatesUseThinTopbar(t *testing.T) {
	for _, name := range []string{"setup_wizard.html", "setup_review.html", "setup_progress.html"} {
		content, err := assets.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		markup := string(content)
		for _, want := range []string{
			`class="setup-header topbar setup-topbar"`,
			`class="brand-link brand-mark"`,
			`<strong>JV</strong>`,
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
		"class=\"grid dashboard-summary\"",
		"class=\"dashboard-live-panels\"",
		"id=\"minecraft-version-summary\"",
		"id=\"players-panel-count\"",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("compact dashboard missing %q", want)
		}
	}
	if strings.Contains(markup, `id="minecraft-players-summary"`) {
		t.Fatal("dashboard contains duplicate Players summary tile")
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
		".dashboard-summary{grid-template-columns:repeat(5,minmax(0,1fr))",
		".dashboard-live-panels{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr)",
		"@media(max-width:900px)",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("compact dashboard styling missing %q", want)
		}
	}
}
