package server

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestStandaloneRestoreUIRetired(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/restore/progress/operation-id", http.StatusNotFound},
		{http.MethodGet, "/settings/restore", http.StatusNotFound},
		{http.MethodGet, "/settings/restore/review", http.StatusNotFound},
		{http.MethodPost, "/settings/restore/apply", http.StatusMethodNotAllowed},
		{http.MethodGet, "/settings/restore/progress/operation-id", http.StatusNotFound},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := httptestResponse(app, authenticatedAdminRequest(tc.method, "http://example"+tc.path, ""))
			if response.Code != tc.status {
				t.Fatalf("retired Restore route returned %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
	for _, path := range []string{
		"templates/restore.html",
		"templates/restore_review.html",
		"templates/restore_progress.html",
	} {
		if _, err := assets.ReadFile(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("retired template %s: expected absence, got %v", path, err)
		}
	}
	content, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"/workspace/backups/restore/plan",
		"/workspace/backups/restore/apply",
		"/workspace/backups/restore/recover",
	} {
		if !strings.Contains(string(content), route) {
			t.Errorf("Backups Workspace replacement is missing %q", route)
		}
	}
}
