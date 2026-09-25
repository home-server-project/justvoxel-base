package server

import (
	"strings"
	"testing"
)

func TestBackupsWorkspaceOwnsUnifiedBackupTools(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(header)
	for _, want := range []string{
		"data-backups-open",
		`data-workspace-window="backups"`,
		"data-backups-workspace-dialog",
		"data-backups-refresh",
		"data-backups-workspace-content",
		`class="control-tile nav-operator-only" href="/operations#manual-backup"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("Backups workspace markup missing %q", want)
		}
	}
	for _, legacy := range []string{
		`href="/settings/backup-storage"`,
		`href="/settings/new-backups"`,
		`href="/settings/restore"`,
	} {
		if strings.Contains(markup, legacy) {
			t.Fatalf("Control Center still exposes legacy backup navigation %q", legacy)
		}
	}
}

func TestBackupsWorkspaceUsesIndependentWorkspaceRoutes(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	start := strings.Index(source, `const backupsOpen = document.querySelector("[data-backups-open]")`)
	end := strings.Index(source, `const migrationOpen = document.querySelector("[data-migration-open]")`)
	if start < 0 || end <= start {
		t.Fatal("Backups workspace source block missing")
	}
	block := source[start:end]
	for _, want := range []string{
		`document.querySelector("[data-backups-workspace-dialog]")`,
		`let currentURL = "/workspace/backups"`,
		`action.pathname.startsWith("/workspace/backups")`,
		`window.JustVoxelBackupsWorkspace`,
		`await loadBackups(href)`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Backups workspace behavior missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`/settings/new-backups`,
		`new DOMParser()`,
		`main.new-backups-shell`,
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("Backups workspace still depends on legacy page rendering %q", forbidden)
		}
	}

	templateBytes, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateBytes)
	if !strings.Contains(markup, `data-backups-workspace-root`) {
		t.Fatal("native Backups fragment root missing")
	}
	if strings.Contains(markup, "/settings/new-backups") {
		t.Fatal("native Backups fragment still posts or links to the legacy page")
	}
	for _, want := range []string{
		`action="/workspace/backups/backup"`,
		`action="/workspace/backups/automatic/plan"`,
		`action="/workspace/backups/destination/plan"`,
		`action="/workspace/backups/restore/plan"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("native Backups fragment missing route %q", want)
		}
	}
}

func TestBackupsScriptsCanInitializeInsideWorkspaceFragment(t *testing.T) {
	backups, err := assets.ReadFile("static/new-backups.js")
	if err != nil {
		t.Fatal(err)
	}
	backupSource := string(backups)
	for _, want := range []string{
		"function initNewBackups(root = document)",
		`root.querySelectorAll("[data-backup-select]")`,
		"window.JustVoxelNewBackups = { init: initNewBackups }",
		`window.JustVoxelBackupsWorkspace?.reload`,
		`"/workspace/backups?result=deleted&count="`,
		"dataset.backupWorkflowReviewClose",
	} {
		if !strings.Contains(backupSource, want) {
			t.Fatalf("Backups fragment script missing %q", want)
		}
	}

	restore, err := assets.ReadFile("static/restore-operation.js")
	if err != nil {
		t.Fatal(err)
	}
	restoreSource := string(restore)
	for _, want := range []string{
		"function initRestoreOperation(root = document)",
		`root.querySelector("[data-restore-operation]")`,
		"window.JustVoxelRestoreOperation = { init: initRestoreOperation }",
	} {
		if !strings.Contains(restoreSource, want) {
			t.Fatalf("Restore fragment script missing %q", want)
		}
	}
}
