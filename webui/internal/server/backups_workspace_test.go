package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
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
		`class="quick-look-backup-action nav-operator-only" type="button" data-quick-look-backup-create`,
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
		`action="/workspace/backups/restore/resolve-reset"`,
		`Keep current server and continue Restore`,
		`Retry Restore review`,
		`Player status is temporarily unavailable.`,
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

func TestBackupsWorkspaceNormalCardsShowOnlyFilenameAndSize(t *testing.T) {
	source, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(source)
	_, card, found := strings.Cut(markup, "{{range .Backups}}")
	if !found {
		t.Fatal("backup card range missing")
	}
	card, _, found = strings.Cut(card, "</label>")
	if !found {
		t.Fatal("backup selection card missing")
	}
	for _, removed := range []string{".CreatedLabel", ".Version", ".Bedrock", ".Variant", ".MetadataStatus", "Ready", "Metadata missing", "Metadata invalid"} {
		if strings.Contains(card, removed) {
			t.Errorf("normal card retains %q", removed)
		}
	}
	view, err := template.New("card").Parse(card + "</label>")
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"valid", "missing", "invalid"} {
		var output bytes.Buffer
		err := view.Execute(&output, backupsWorkspaceBackupView{
			ID: "minecraft-2026-10-02-211241.tar.gz", Size: "274.7 MiB",
			CreatedLabel: "Oct 3, 2026", Version: "full Paper version output", Bedrock: true,
			Variant: "justvoxel-vm", MetadataStatus: status,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"minecraft-2026-10-02-211241.tar.gz", "274.7 MiB", "data-backup-select", `type="checkbox"`, "data-backup-card"} {
			if !strings.Contains(output.String(), want) {
				t.Errorf("rendered card missing %q", want)
			}
		}
		for _, removed := range []string{"Oct 3, 2026", "full Paper version output", "Bedrock", "justvoxel-vm", "Ready", "Metadata missing", "Metadata invalid"} {
			if strings.Contains(output.String(), removed) {
				t.Errorf("rendered card retains %q", removed)
			}
		}
	}
	for _, control := range []string{"data-backup-restore-selected", "data-backup-delete-selected"} {
		if !strings.Contains(markup, control) {
			t.Errorf("backup control missing %q", control)
		}
	}
}

func TestQuickLookManualBackupUsesWorkspaceNativeAPI(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<button class="quick-look-backup-action nav-operator-only" type="button" data-quick-look-backup-create`,
		`value="{{.CSRF}}" data-quick-look-backup-csrf`,
	} {
		if !strings.Contains(string(header), want) {
			t.Fatalf("Quick Look markup missing %q", want)
		}
	}
	if strings.Contains(string(header), "/operations#manual-backup") {
		t.Fatal("Quick Look still links to Operations manual backup")
	}
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	start := strings.Index(source, `const backupCreate = quickLook.querySelector`)
	if start < 0 {
		t.Fatal("Quick Look manual backup action missing")
	}
	end := strings.Index(source[start:], `const setActionAvailability`)
	if end < 0 {
		t.Fatal("Quick Look action boundary missing")
	}
	block := source[start : start+end]
	for _, want := range []string{
		`fetch("/api/backups/manual"`, `method: "POST"`, `credentials: "same-origin"`,
		`[data-quick-look-backup-csrf]`, `new URLSearchParams({ csrf: backupCSRF?.value || "" })`,
		`if (backupCreate.disabled) return`, `backupCreate.disabled = true`,
		`backupCreate.textContent = "Starting…"`, `response.ok && result.ok`,
		`? result.message`, `: result.error`, `setValue("[data-quick-look-backup]"`,
		`finally`, `backupCreate.disabled = false`, `backupCreate.textContent = "Create backup"`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Quick Look action missing %q", want)
		}
	}
	if strings.Contains(source, "/operations#manual-backup") || strings.Contains(block, "/operations") {
		t.Fatal("Quick Look manual backup still navigates to Operations")
	}
	markup, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `action="/workspace/backups/backup"`) {
		t.Fatal("Administrator Backups Workspace manual backup route changed")
	}
	for _, role := range []string{"operator", "viewer"} {
		t.Run("workspace-denies-"+role, func(t *testing.T) {
			client := &fakeNewBackupsAPI{role: role}
			app, err := New(client, Config{ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			for _, request := range []*http.Request{
				authenticatedAdminRequest(http.MethodGet, "http://example/workspace/backups", ""),
				authenticatedAdminRequest(http.MethodPost, "http://example/workspace/backups/backup", "csrf=csrf-token"),
			} {
				response := httptestResponse(app, request)
				if response.Code != http.StatusForbidden || client.backupCalls != 0 || client.discoveryCalls != 0 {
					t.Fatalf("workspace role %s: status=%d backup=%d discovery=%d", role, response.Code, client.backupCalls, client.discoveryCalls)
				}
			}
		})
	}
}

func TestQuickLookManualBackupAPIRoles(t *testing.T) {
	const successMessage = "Backup started. It will appear in the library when the backup service finishes."
	for _, role := range []string{"administrator", "operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			client := &fakeNewBackupsAPI{role: role}
			app, err := New(client, Config{ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/backups/manual", "csrf=csrf-token"))
			wantStatus, wantCalls := http.StatusOK, 1
			if role == "viewer" {
				wantStatus, wantCalls = http.StatusForbidden, 0
			}
			if response.Code != wantStatus || client.backupCalls != wantCalls {
				t.Fatalf("status=%d calls=%d, want status=%d calls=%d: %s", response.Code, client.backupCalls, wantStatus, wantCalls, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected response headers: %v", response.Header())
			}
			var result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if role != "viewer" && (!result.OK || result.Message != successMessage) {
				t.Fatalf("unexpected success response: %+v", result)
			}
			if role == "viewer" && (result.OK || result.Error == "") {
				t.Fatalf("unexpected Viewer rejection: %+v", result)
			}
		})
	}
	for _, guard := range []string{"invalid-csrf", "must-change-password", "missing-session", "expired-session"} {
		t.Run(guard, func(t *testing.T) {
			client := &fakeNewBackupsAPI{role: "operator"}
			app, err := New(client, Config{ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			body := "csrf=csrf-token"
			if guard == "invalid-csrf" {
				body = "csrf=wrong"
			}
			request := authenticatedAdminRequest(http.MethodPost, "http://example/api/backups/manual", body)
			wantStatus := http.StatusForbidden
			if guard == "must-change-password" {
				request.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "1"})
			}
			if guard == "missing-session" || guard == "expired-session" {
				request.Header.Del("Cookie")
				request.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
				if guard == "expired-session" {
					request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "expired"})
				}
				wantStatus = http.StatusUnauthorized
			}
			response := httptestResponse(app, request)
			if response.Code != wantStatus || client.backupCalls != 0 {
				t.Fatalf("guard status=%d calls=%d: %s", response.Code, client.backupCalls, response.Body.String())
			}
		})
	}
	t.Run("session-must-change-password", func(t *testing.T) {
		client := &quickLookPasswordChangeAPI{fakeNewBackupsAPI: &fakeNewBackupsAPI{role: "operator"}}
		app, err := New(client, Config{ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		response := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/backups/manual", "csrf=csrf-token"))
		if response.Code != http.StatusForbidden || client.backupCalls != 0 {
			t.Fatalf("password change status=%d calls=%d", response.Code, client.backupCalls)
		}
	})
	for _, tc := range []struct {
		name    string
		result  api.ManualBackupResponse
		message string
	}{
		{"normal-failure", api.ManualBackupResponse{}, "Manual backup could not be started."},
		{"cooldown", api.ManualBackupResponse{Reason: "backup_cooldown", RetryAfterSeconds: 65}, "Backup available in 1m 05s."},
		{"reset-required", api.ManualBackupResponse{AdministratorResetRequired: true}, "Backup unavailable. Administrator reset required."},
		{"limit-reached", api.ManualBackupResponse{Reason: "backup_limit_reached"}, "Backup unavailable. Administrator reset required."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeNewBackupsAPI{role: "operator", backupResult: tc.result, backupErr: errors.New("backup rejected")}
			app, err := New(client, Config{ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/backups/manual", "csrf=csrf-token"))
			var result struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadRequest || result.OK || result.Error != tc.message || client.backupCalls != 1 {
				t.Fatalf("backup rejection status=%d result=%+v calls=%d", response.Code, result, client.backupCalls)
			}
		})
	}
}

type quickLookPasswordChangeAPI struct {
	*fakeNewBackupsAPI
}

func (f *quickLookPasswordChangeAPI) Session(ctx context.Context, session string) (api.SessionInfo, error) {
	identity, err := f.fakeNewBackupsAPI.Session(ctx, session)
	identity.MustChange = true
	return identity, err
}

func TestStandaloneBackupStorageUIRetired(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/settings/backup-storage", http.StatusNotFound},
		{http.MethodPost, "/settings/backup-storage/plan", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/backup-storage/apply", http.StatusMethodNotAllowed},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := httptestResponse(app, authenticatedAdminRequest(route.method, route.path, ""))
			if response.Code != route.status {
				t.Fatalf("retired route returned %d, want HTTP %d", response.Code, route.status)
			}
		})
	}
	if _, err := assets.ReadFile("templates/backup_storage.html"); err == nil {
		t.Fatal("retired standalone Backup Storage template remains embedded")
	}
	markup, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{
		`action="/workspace/backups/destination/plan"`,
		`action="/workspace/backups/destination/apply"`,
	} {
		if !strings.Contains(string(markup), action) {
			t.Fatalf("Backups workspace missing replacement %q", action)
		}
	}
}

func TestStandaloneNewBackupsUIRetired(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/settings/new-backups", http.StatusNotFound},
		{http.MethodPost, "/settings/new-backups/backup", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/new-backups/automatic/plan", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/new-backups/automatic/apply", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/new-backups/destination/plan", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/new-backups/destination/apply", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/new-backups/restore/plan", http.StatusMethodNotAllowed},
		{http.MethodPost, "/settings/new-backups/restore/apply", http.StatusMethodNotAllowed},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := httptestResponse(app, authenticatedAdminRequest(route.method, route.path, ""))
			if response.Code != route.status {
				t.Fatalf("retired route returned %d, want HTTP %d", response.Code, route.status)
			}
		})
	}
	if _, err := assets.ReadFile("templates/new_backups.html"); err == nil {
		t.Fatal("retired standalone New Backups template remains embedded")
	}
	script, err := assets.ReadFile("static/new-backups.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"/settings/new-backups", "main.new-backups-shell", "pageURL"} {
		if strings.Contains(string(script), retired) {
			t.Fatalf("Backups script retains standalone fallback %q", retired)
		}
	}
}
