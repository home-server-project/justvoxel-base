package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const serverMigrationOperationID = "23456789-1234-4abc-8def-123456789abc"
const serverMigrationFingerprint = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type fakeServerMigrationAPI struct {
	fakeAPI
	role            string
	exportDiscovery api.AdminMigrationExportDiscoveryResponse
	importDiscovery api.AdminMigrationImportDiscoveryResponse
	recovery        api.AdminMigrationRecoveryDiscoveryResponse
	current         *api.PersistentOperation
	operation       *api.PersistentOperation
	discoveryCalls  int
}

func (f *fakeServerMigrationAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeServerMigrationAPI) AdminMigrationExportDiscovery(_ context.Context, session string) (api.AdminMigrationExportDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationExportDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.exportDiscovery, nil
}

func (f *fakeServerMigrationAPI) AdminMigrationImportDiscovery(_ context.Context, session string) (api.AdminMigrationImportDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationImportDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.importDiscovery, nil
}

func (f *fakeServerMigrationAPI) AdminMigrationRecoveryDiscovery(_ context.Context, session string) (api.AdminMigrationRecoveryDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationRecoveryDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.recovery, nil
}

func (f *fakeServerMigrationAPI) AdminCurrentMigrationOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.current == nil {
		return api.PersistentOperationResponse{}, nil
	}
	copy := *f.current
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func (f *fakeServerMigrationAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.operation == nil || f.operation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	copy := *f.operation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func TestServerMigrationHubIsAdministratorOnlyAndShowsSharedWorkflows(t *testing.T) {
	client := &fakeServerMigrationAPI{
		exportDiscovery: api.AdminMigrationExportDiscoveryResponse{
			OK: true, SchemaVersion: "v1", SuggestedFilename: "justvoxel-migration-test.tar.gz",
			ConfiguredBackupAvailable: true, Devices: []api.AdminMigrationExportDevice{}, TargetKinds: []string{"local", "backup", "smb"},
		},
		importDiscovery: api.AdminMigrationImportDiscoveryResponse{
			OK: true, SchemaVersion: "v1", Configured: false, SourceKinds: []string{"local", "backup", "device"},
		},
		recovery: api.AdminMigrationRecoveryDiscoveryResponse{
			OK: true, SchemaVersion: "v1", Configured: false,
			Transactions: []api.AdminMigrationRecoverySummary{{Phase: "rolled-back-fresh", Mode: "fresh", UpdatedAt: "2026-09-21T13:00:00Z", Finalizable: true}},
		},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server-migration", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("Server Migration page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Server Migration", "Export this server", "Import a server", "Migration Recovery",
		"local, backup, smb", "Fresh, unconfigured JustVoxel appliance", "rolled-back-fresh",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Server Migration page missing %q: %s", want, page.Body.String())
		}
	}
	if client.discoveryCalls != 3 {
		t.Fatalf("discovery calls = %d, want 3", client.discoveryCalls)
	}

	client.role = "operator"
	denied := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server-migration", ""))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("operator Server Migration page status = %d, want 403", denied.Code)
	}
	if client.discoveryCalls != 3 {
		t.Fatalf("migration discovery ran for non-administrator; calls=%d", client.discoveryCalls)
	}
}

func TestServerMigrationHubReconnectsToCurrentMigrationOperation(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_import",
		PlanFingerprint: serverMigrationFingerprint, State: "running", Stage: "copying",
		Status: "Importing Minecraft data.", StartedAt: "x", UpdatedAt: "x",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeServerMigrationAPI{current: operation, operation: operation}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	entry := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server-migration", ""))
	if entry.Code != http.StatusSeeOther || entry.Header().Get("Location") != "/settings/server-migration/progress/"+serverMigrationOperationID {
		t.Fatalf("Server Migration entry did not reconnect: %d %q", entry.Code, entry.Header().Get("Location"))
	}
	if client.discoveryCalls != 0 {
		t.Fatalf("discovery ran before reconnect; calls=%d", client.discoveryCalls)
	}

	progress := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server-migration/progress/"+serverMigrationOperationID, ""))
	if progress.Code != http.StatusOK {
		t.Fatalf("Server Migration progress returned %d: %s", progress.Code, progress.Body.String())
	}
	for _, want := range []string{"Server Import", "Running", "Importing Minecraft data.", serverMigrationOperationID} {
		if !strings.Contains(progress.Body.String(), want) {
			t.Fatalf("Server Migration progress missing %q: %s", want, progress.Body.String())
		}
	}
}
