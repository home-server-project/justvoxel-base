package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type redirectRoleAPI struct {
	*fakeAPI
	role string
}

func (f *redirectRoleAPI) Session(ctx context.Context, session string) (api.SessionInfo, error) {
	identity, err := f.fakeAPI.Session(ctx, session)
	identity.Role = f.role
	return identity, err
}

func TestLegacyPagesOpenWorkspaceDestinations(t *testing.T) {
	app, err := New(&redirectRoleAPI{fakeAPI: &fakeAPI{}, role: "administrator"}, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, destination string }{
		{"/activity", "/?tab=history&workspace=system"},
		{"/settings/server", "/?tab=memory&workspace=minecraft"},
		{"/settings/validation", "/?tab=health&workspace=system"},
		{"/settings/activity", "/?tab=history&workspace=system"},
		{"/settings/users", "/?tab=users&workspace=system"},
		{"/settings/authentication", "/?tab=security&workspace=system"},
		{"/password", "/?tab=security&workspace=system"},
		{"/about", "/?tab=about&workspace=system"},
		{"/settings/new-storage", "/?workspace=storage"},
		{"/settings/storage-provision", "/?workspace=storage"},
		{"/settings/data-migration", "/?workspace=storage"},
		{"/settings/new-backups", "/?workspace=backups"},
		{"/settings/server-migration", "/?tab=export&workspace=migration"},
		{"/settings/server-migration/import", "/?tab=import&workspace=migration"},
		{"/settings/server-migration/recovery", "/?tab=recovery&workspace=migration"},
	} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example"+tc.path, ""))
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tc.destination {
			t.Errorf("%s: got %d %q, want redirect to %q", tc.path, rr.Code, rr.Header().Get("Location"), tc.destination)
		}
	}
	result := httptest.NewRecorder()
	app.Handler().ServeHTTP(result, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-backups?result=restore&restore_operation=op-123", ""))
	if location := result.Header().Get("Location"); !strings.Contains(location, "result=restore") || !strings.Contains(location, "restore_operation=op-123") {
		t.Fatalf("backup redirect lost result context: %q", location)
	}
}

func TestLegacyWorkspaceRedirectKeepsRoleBoundary(t *testing.T) {
	for _, role := range []string{"operator", "viewer"} {
		app, err := New(&redirectRoleAPI{fakeAPI: &fakeAPI{}, role: role}, Config{ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		denied := httptest.NewRecorder()
		app.Handler().ServeHTTP(denied, authenticatedAdminRequest(http.MethodGet, "http://example/settings/users", ""))
		if denied.Code != http.StatusForbidden {
			t.Errorf("%s received administrator workspace: %d", role, denied.Code)
		}
		allowed := httptest.NewRecorder()
		app.Handler().ServeHTTP(allowed, authenticatedAdminRequest(http.MethodGet, "http://example/activity", ""))
		if allowed.Code != http.StatusSeeOther || allowed.Header().Get("Location") != "/?tab=history&workspace=system" {
			t.Errorf("%s lost public activity: %d %q", role, allowed.Code, allowed.Header().Get("Location"))
		}
	}
}

func TestLegacyActionAndProgressRoutesRemainRegistered(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/settings/data-migration/apply",
		"/settings/server-migration/export/apply",
		"/settings/new-backups/backup",
		"/password",
	} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "http://example"+path, nil))
		if rr.Code == http.StatusNotFound || rr.Code == http.StatusMethodNotAllowed {
			t.Errorf("legacy action route %s disappeared: %d", path, rr.Code)
		}
	}
	for _, path := range []string{
		"/settings/data-migration/progress/operation-id",
		"/settings/server-migration/progress/operation-id",
		"/settings/restore/progress/operation-id",
	} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example"+path, ""))
		if rr.Code == http.StatusNotFound {
			t.Errorf("legacy progress route %s disappeared", path)
		}
	}
}
