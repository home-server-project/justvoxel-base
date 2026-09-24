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

func TestBackupsWorkspaceInterceptsPageNavigation(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	for _, want := range []string{
		`document.querySelector("[data-backups-open]")`,
		`document.querySelector("[data-backups-workspace-dialog]")`,
		`new DOMParser().parseFromString(markup, "text/html")`,
		`root.addEventListener("submit", handleSubmit)`,
		`event.preventDefault()`,
		`window.JustVoxelBackupsWorkspace`,
		`await loadBackups(href)`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("Backups workspace behavior missing %q", want)
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
