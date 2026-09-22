package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type failingStorageBrowserMigrationAPI struct {
	fakeDiscoveryAPI
	migrationCalls int
}

func (f *failingStorageBrowserMigrationAPI) AdminDataMigrationDiscovery(_ context.Context, session string) (api.AdminDataMigrationDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.migrationCalls++
	return api.AdminDataMigrationDiscoveryResponse{}, errors.New("migration discovery unavailable")
}

func TestValidationNewStorageMinecraftMigrationDiscoveryFailureIsNonFatal(t *testing.T) {
	client := &failingStorageBrowserMigrationAPI{}
	client.configuration.Configured = true
	client.storage.Devices = []api.AdminStorageDevice{
		{Name: "vdb", Path: "/dev/vdb", Type: "disk", SizeBytes: 100 * 1024 * 1024 * 1024, Model: "Data Disk"},
		{Name: "vdb1", Path: "/dev/vdb1", Parent: "vdb", Type: "part", SizeBytes: 100 * 1024 * 1024 * 1024, Filesystem: "xfs", UUID: "data-uuid"},
	}

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-storage", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("New Storage returned %d after migration discovery failure: %s", rr.Code, rr.Body.String())
	}
	if client.migrationCalls != 1 {
		t.Fatalf("migration discovery calls=%d, want 1", client.migrationCalls)
	}
	if !strings.Contains(rr.Body.String(), "Minecraft storage assignment is temporarily unavailable") {
		t.Fatalf("New Storage did not explain migration discovery failure: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "/dev/vdb1") {
		t.Fatal("normal storage inventory disappeared when migration discovery failed")
	}
}

func TestValidationNewStorageMinecraftMigrationDiscoveryIsAdministratorOnly(t *testing.T) {
	client := &failingStorageBrowserMigrationAPI{}
	client.role = "operator"

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/new-storage", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("Operator New Storage status=%d, want 403", rr.Code)
	}
	if client.migrationCalls != 0 {
		t.Fatal("Minecraft migration discovery ran for Operator")
	}
}
