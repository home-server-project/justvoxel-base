package server

import (
	"strings"
	"testing"
)

func TestGroupedHeaderNavigationUX(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	for _, want := range []string{
		`class="brand-link" href="/"`,
		`aria-label="JustVoxel dashboard"`,
		`data-nav-group="server"`,
		`>Server</summary>`,
		`data-nav-group="storage"`,
		`>Storage &amp; Backups</summary>`,
		`data-nav-group="administration"`,
		`>Administration</summary>`,
		`href="/activity"`,
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
		`class="nav-logout"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("grouped header missing %q", want)
		}
	}

	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, want := range []string{
		`.nav-group`,
		`.nav-trigger:hover`,
		`.nav-menu`,
		`.nav-admin-only,.nav-operator-plus{display:none!important}`,
		`body.role-administrator .nav-admin-only`,
		`body.role-operator .nav-operator-plus`,
		`:focus-visible`,
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("grouped navigation styling missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	behavior := string(script)
	for _, want := range []string{
		`document.querySelectorAll(".nav-group")`,
		`other.open = false`,
		`event.key !== "Escape"`,
		`data-nav-group`,
	} {
		if !strings.Contains(behavior, want) {
			t.Fatalf("grouped navigation behavior missing %q", want)
		}
	}
}

func TestAuthenticatedTemplatesUseSharedHeader(t *testing.T) {
	for _, name := range []string{
		"dashboard.html",
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
			t.Fatalf("%s does not load grouped navigation behavior", name)
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
