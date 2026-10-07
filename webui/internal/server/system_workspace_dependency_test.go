package server

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStandaloneSystemSettingsUIRetired(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	for _, path := range []string{
		"/activity",
		"/settings/activity",
		"/settings/users",
		"/settings/authentication",
		"/settings/validation",
		"/about",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example"+path, ""))
			if rr.Code != http.StatusNotFound {
				t.Fatalf("retired GET route returned %d, want 404", rr.Code)
			}
		})
	}
	// The GET / catch-all returns 405 once the old POST handlers are removed.
	for _, path := range []string{
		"/settings/activity/notifications/1/resolve",
		"/settings/users/create",
		"/settings/users/1/role",
		"/settings/users/1/enabled",
		"/settings/users/1/password",
		"/settings/users/1/delete",
		"/settings/users/1/restart-allowance/reset",
		"/settings/users/1/backup-allowance/reset",
		"/settings/authentication",
	} {
		t.Run("POST "+path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example"+path, "csrf=csrf-token"))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("retired POST route returned %d, want 405", rr.Code)
			}
		})
	}
	for _, name := range []string{
		"about.html",
		"activity.html",
		"admin_activity.html",
		"authentication.html",
		"users.html",
		"validation.html",
	} {
		if _, err := assets.ReadFile("templates/" + name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("retired embedded template %s: got %v, want file not found", name, err)
		}
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate System Workspace dependency test source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "system_workspace.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/system/workspace/health",
		"/api/system/workspace/history",
		"/api/system/workspace/users",
		"/api/system/workspace/security",
		"/api/system/workspace/about",
	} {
		if !strings.Contains(string(source), `"GET `+path+`"`) {
			t.Errorf("current System Workspace route %s missing", path)
		}
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/password", ""))
	if rr.Code != http.StatusOK {
		t.Errorf("retained GET /password returned %d, want 200", rr.Code)
	}
}

func TestSystemWorkspaceDoesNotDependOnLegacyPageInterfaces(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate System Workspace dependency test source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "system_workspace.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)

	for _, forbidden := range []string{
		"adminValidationAPI",
		"rolePagesAPI",
		"adminUsersAPI",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("System Workspace still depends on legacy page interface %q", forbidden)
		}
	}

	for _, want := range []string{
		"type systemWorkspaceValidationAPI interface",
		"type systemWorkspaceHistoryAPI interface",
		"type systemWorkspaceUsersAPI interface",
		"type systemWorkspaceResetAPI interface",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("System Workspace missing neutral API contract %q", want)
		}
	}
}
