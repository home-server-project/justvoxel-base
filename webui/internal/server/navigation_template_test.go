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
		`href="/settings/new-storage"`,
		`href="/settings/data-migration"`,
		`href="/settings/backup-storage"`,
		`href="/settings/new-backups"`,
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
		"storage_browser.html",
		"backup_storage.html",
		"new_backups.html",
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
		`detailDialog.showModal()`,
		`detailClose?.addEventListener("click"`,
		`detailDialog?.addEventListener("cancel", closeActionMenu)`,
		`"/api/new-storage/whole-disk/" + phase`,
		`button.setAttribute("aria-pressed"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("storage browser behavior missing %q", want)
		}
	}
	if strings.Contains(script, "detailDialog?.close()") && !strings.Contains(script, "detailClose?.addEventListener") {
		t.Fatal("storage detail dialog can close without explicit close control")
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
		`window.location.reload()`,
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
		`action="/settings/data-migration/review"`,
		`name="operation" value="use_partition"`,
		`name="size_gib" value="all"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("New Storage Minecraft migration handoff missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`name="operation" value="erase_disk"`,
		`name="operation" value="format_partition"`,
		`name="operation" value="create_partition"`,
	} {
		if strings.Contains(markup, forbidden) {
			t.Fatalf("New Storage unexpectedly exposes deferred destructive migration operation %q", forbidden)
		}
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
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("New Storage Minecraft migration behavior missing %q", want)
		}
	}
}

func TestNewBackupsCompactLibraryLayout(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/new_backups.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		"Backup Now",
		"Restore selected",
		"backup-command-bar",
		"backup-file-grid",
		"backup-file-card",
		"/settings/new-backups/backup",
		"/settings/new-backups/restore/plan",
		"/settings/new-backups/restore/apply",
		"/static/restore-operation.js",
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

func TestNewBackupsReviewedMultiDeleteFlow(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/new_backups.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`data-backup-select`,
		`data-backup-delete-selected`,
		`data-backup-delete-dialog`,
		`data-backup-delete-confirmation-phrase`,
		`/static/new-backups.js`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("New Backups delete UI missing %q", want)
		}
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
		`window.location.assign("/settings/new-backups?result=deleted&count="`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("New Backups delete behavior missing %q", want)
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
	templateContent, err := assets.ReadFile("templates/new_backups.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`name="automatic_enabled"`,
		`name="daily_time" type="time"`,
		`name="backup_keep" type="number"`,
		`/settings/new-backups/automatic/plan`,
		`/settings/new-backups/automatic/apply`,
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
