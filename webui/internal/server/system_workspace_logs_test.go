package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestWorkspaceLogIdentifiersAreAllowlisted(t *testing.T) {
	id := "12345678-1234-4123-8123-123456789abc"
	for _, category := range []string{"setup", "migration", "restore", "plugins", "reset", "operation"} {
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
	role         string
	reads        int
	units        []api.FailedUnit
	unitError    error
	unitReads    int
	unitLists    int
	journalError error
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
	for _, path := range []string{"/api/system/workspace/logs", "/api/system/workspace/logs/migration/" + id, "/api/system/workspace/logs/reset/" + id} {
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
	// Reset forensic diagnostics remain available in System Logs.
	reset := httptest.NewRecorder()
	app.Handler().ServeHTTP(reset, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/reset/"+id, ""))
	if reset.Code != http.StatusOK || reset.Header().Get("Content-Type") != "text/plain; charset=utf-8" || reset.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reset log read = %d %s", reset.Code, reset.Body.String())
	}

	download := httptest.NewRecorder()
	app.Handler().ServeHTTP(download, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/migration/"+id+"?download=1", ""))
	if download.Code != http.StatusOK || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("log download = %d", download.Code)
	}
	invalid := httptest.NewRecorder()
	app.Handler().ServeHTTP(invalid, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/operation/invalid", ""))
	if invalid.Code != http.StatusBadRequest || client.reads != 3 {
		t.Fatalf("invalid log id = %d; reads=%d", invalid.Code, client.reads)
	}
}

func (f *fakeDiagnosticLogsAPI) AdminFailedUnits(_ context.Context, _ string) ([]api.FailedUnit, error) {
	f.unitLists++
	if f.units == nil && f.unitError == nil {
		return []api.FailedUnit{}, nil
	}
	return f.units, f.unitError
}
func (f *fakeDiagnosticLogsAPI) AdminFailedUnitJournal(_ context.Context, _, unit string) ([]byte, error) {
	f.unitReads++
	if f.journalError != nil {
		return nil, f.journalError
	}
	return []byte("bounded journal for " + unit + "\n"), nil
}

func TestSystemWorkspaceFailedUnitsSeparateLiveCollectionAndDownload(t *testing.T) {
	unit := `srv-my\x20disk.mount`
	client := &fakeDiagnosticLogsAPI{fakeAPI: &fakeAPI{}, role: "administrator", units: []api.FailedUnit{{Unit: unit, Description: "Disk", ActiveState: "failed", SubState: "failed"}}}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	listing := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs", ""))
		return rr
	}
	rr := listing()
	var payload struct {
		Logs  []api.DiagnosticLogEntry `json:"logs"`
		Units []api.FailedUnit         `json:"failed_units"`
		Error string                   `json:"failed_units_error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 200 || len(payload.Logs) != 1 || payload.Logs[0].Category != "migration" || len(payload.Units) != 1 || payload.Units[0].Unit != unit || payload.Error != "" {
		t.Fatalf("separate logs %#v: %s", payload, rr.Body.String())
	}
	path := "http://example/api/system/workspace/logs/failed/" + url.PathEscape(unit)
	for _, suffix := range []string{"", "?download=1"} {
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, path+suffix, ""))
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), unit) || rr.Header().Get("Content-Type") != "text/plain; charset=utf-8" || rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("journal: %d %s", rr.Code, rr.Body.String())
		}
		if suffix != "" && !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("missing download")
		}
	}
	client.units = []api.FailedUnit{}
	rr = listing()
	if !strings.Contains(rr.Body.String(), `"failed_units":[]`) || client.unitLists != 2 {
		t.Fatalf("refresh: %s", rr.Body.String())
	}
	client.unitError = errors.New("privileged secret error")
	rr = listing()
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"failed_units_error":"Failed systemd units are unavailable."`) || !strings.Contains(rr.Body.String(), "migration") || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("enumeration failure: %s", rr.Body.String())
	}
	client.journalError = &api.ResponseError{StatusCode: 404, Message: "privileged secret error"}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, path, ""))
	if rr.Code != 404 || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("journal failure: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSystemWorkspaceFailedJournalAuthorizationAndIdentity(t *testing.T) {
	client := &fakeDiagnosticLogsAPI{fakeAPI: &fakeAPI{}, role: "viewer"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"viewer", "operator"} {
		client.role = role
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/failed/minecraft.service", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s read failed journal: %d", role, rr.Code)
		}
	}
	client.role = "administrator"
	for _, invalid := range []string{"*.service", "a/b.mount", "a.service;reboot", "a.service\n", `a\bad.mount`} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/failed/"+url.PathEscape(invalid), ""))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("accepted unsafe %q: %d", invalid, rr.Code)
		}
	}
	if client.unitReads != 0 {
		t.Fatal("unsafe journal proxy reached Agent client")
	}
}

func TestPluginsLogsReadAndDownloadExactAgentContent(t *testing.T) {
	client := &fakeDiagnosticLogsAPI{fakeAPI: &fakeAPI{}, role: "administrator"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	id := "12345678-1234-4123-8123-123456789abc"
	for _, query := range []string{"", "?download=1"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/system/workspace/logs/plugins/"+id+query, ""))
		if rr.Code != 200 || rr.Body.String() != "backend rejected <unsafe>\nrollback succeeded\n" {
			t.Fatal("Agent log changed", rr.Code, rr.Body.String())
		}
		if query != "" && !strings.Contains(rr.Header().Get("Content-Disposition"), "justvoxel-plugins-"+id+".log") {
			t.Fatal("wrong download filename")
		}
	}
}
