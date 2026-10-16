package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestMigrationWorkspaceSelectionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		instances   []api.RegisteredMinecraftInstance
		status      int
	}{
		{"zero", "", []api.RegisteredMinecraftInstance{}, 409},
		{"multiple", "", []api.RegisteredMinecraftInstance{{ID: "jv-a1b2c3"}, {ID: "jv-d4e5f6"}}, 409},
		{"stale", "?server=jv-stale1", []api.RegisteredMinecraftInstance{{ID: "jv-a1b2c3"}}, 404},
		{"malformed", "?server=Family", []api.RegisteredMinecraftInstance{{ID: "jv-a1b2c3"}}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeServerMigrationImportAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{instances: tc.instances}}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			req := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/import"+tc.query, "")
			req.Header.Set(migrationWorkspaceFragmentHeader, "1")
			page := httptestResponse(app, req)
			if page.Code != tc.status || client.discoveryCalls != 0 || client.applyCalls != 0 {
				t.Fatal(page.Code, page.Body.String())
			}
			if strings.Contains(page.Body.String(), "Fresh JustVoxel destination") {
				t.Fatal("zero destination fell into fresh Import")
			}
		})
	}
}
func TestMigrationWorkspaceSelectedReviewAndProgressLinks(t *testing.T) {
	client := &fakeServerMigrationImportAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{
		exportDiscovery: importMediaFixture(), importDiscovery: importDiscoveryFixture(true),
		instances: []api.RegisteredMinecraftInstance{{ID: "jv-a1b2c3"}, {ID: "jv-d4e5f6"}},
	}, plan: successfulImportPlan("replace")}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	review := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/workspace/migration/import/review?server=jv-a1b2c3", configuredImportRequestValues("local")))
	if review.Code != 200 || !strings.Contains(review.Body.String(), `action="/workspace/migration/import/apply?server=jv-a1b2c3"`) {
		t.Fatal(review.Code, review.Body.String())
	}
	client.operation = &api.PersistentOperation{InstanceID: "jv-a1b2c3", OperationID: serverMigrationOperationID, OperationType: "migration_import", State: "needs_attention"}
	req := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/progress/"+serverMigrationOperationID+"?server=jv-a1b2c3", "")
	req.Header.Set(migrationWorkspaceFragmentHeader, "1")
	progress := httptestResponse(app, req)
	for _, link := range []string{"progress/" + serverMigrationOperationID + "?server=jv-a1b2c3", "tab=recovery&amp;server=jv-a1b2c3"} {
		if progress.Code != 200 || !strings.Contains(progress.Body.String(), link) {
			t.Fatal(progress.Code, progress.Body.String())
		}
	}
	req = authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/progress/"+serverMigrationOperationID+"?server=jv-d4e5f6", "")
	req.Header.Set(migrationWorkspaceFragmentHeader, "1")
	if response := httptestResponse(app, req); response.Code != 404 {
		t.Fatal("sibling reconnected", response.Code)
	}
}
