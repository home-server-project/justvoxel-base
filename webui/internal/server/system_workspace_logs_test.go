package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestWorkspaceLogIdentifiersAreAllowlisted(t *testing.T) {
	id := "12345678-1234-4123-8123-123456789abc"
	for _, category := range []string{"setup", "migration", "restore", "reset", "operation"} {
		if !validWorkspaceLogID(category, id) {
			t.Fatalf("rejected %s log", category)
		}
	}
	for _, input := range []struct{ category, id string }{
		{"unknown", id}, {"operation", "../" + id}, {"setup", "/etc/passwd"},
		{"operation", "12345678-1234-4123-8123-123456789abc/other"},
		{"operation", "12345678-1234-5123-8123-123456789abc"},
	} {
		if validWorkspaceLogID(input.category, input.id) {
			t.Fatalf("accepted unsafe log: %#v", input)
		}
	}
}

type fakeDiagnosticLogsAPI struct {
	*fakeAPI
	role  string
	reads int
}

func (f *fakeDiagnosticLogsAPI) Session(ctx context.Context, session string) (api.SessionInfo, error) {
	identity, err := f.fakeAPI.Session(ctx, session)
	identity.Role = f.role
	return identity, err
}
func (f *fakeDiagnosticLogsAPI) AdminDiagnosticLogs(_ context.Context, _ string) ([]api.DiagnosticLogEntry, error) {
	return []api.DiagnosticLogEntry{{Category: "migration", LogID: "12345678-1234-4123-8123-123456789abc", OperationType: "migration_import", State: "rolled_back"}}, nil
}
func (f *fakeDiagnosticLogsAPI) AdminDiagnosticLog(_ context.Context, _, _, _ string) ([]byte, error) {
	f.reads++
	return []byte("backend rejected <unsafe>\nrollback succeeded\n"), nil
}

func TestSystemWorkspaceLogsAreAdministratorOnlyAndDownloadText(t *testing.T) {
	client := &fakeDiagnosticLogsAPI{fakeAPI: &fakeAPI{}, role: "viewer"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	id := "12345678-1234-4123-8123-123456789abc"
	for _, path := range []string{"/api/system/workspace/logs", "/api/system/workspace/logs/migration/" + id} {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, authenticatedAdminRequest(http.MethodGet, "http://example"+path, ""))
		if response.Code != http.StatusForbidden {
			t.Fatalf("viewer %s returned %d", path, response.Code)
		}
	}
	if client.reads != 0 {
		t.Fatal("viewer reached log API")
	}
	client.role = "administrator"
	listing := httptest.NewRecorder()
	app.Handler().ServeHTTP(listing, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs", ""))
	if listing.Code != http.StatusOK || !strings.Contains(listing.Body.String(), id) {
		t.Fatalf("log listing = %d %s", listing.Code, listing.Body.String())
	}
	read := httptest.NewRecorder()
	app.Handler().ServeHTTP(read, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/migration/"+id, ""))
	if read.Code != http.StatusOK || read.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(read.Body.String(), "rollback succeeded\n") {
		t.Fatalf("log read = %d %s", read.Code, read.Body.String())
	}
	download := httptest.NewRecorder()
	app.Handler().ServeHTTP(download, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/migration/"+id+"?download=1", ""))
	if download.Code != http.StatusOK || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("log download = %d", download.Code)
	}
	invalid := httptest.NewRecorder()
	app.Handler().ServeHTTP(invalid, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/operation/invalid", ""))
	if invalid.Code != http.StatusBadRequest || client.reads != 2 {
		t.Fatalf("invalid log id = %d; reads=%d", invalid.Code, client.reads)
	}
}
