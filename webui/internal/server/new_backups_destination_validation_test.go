package server

import (
	"net/http"
	"testing"
)

func TestValidationNewBackupsDestinationGuardsCSRFAndRole(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=wrong&type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups"
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/destination/plan", body))
	if page.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF destination plan status=%d, want 403", page.Code)
	}
	if client.destinationPlanCalls != 0 {
		t.Fatal("destination plan reached privileged API after CSRF rejection")
	}

	client.role = "operator"
	body = "csrf=csrf-token&type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups"
	page = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/new-backups/destination/plan", body))
	if page.Code != http.StatusForbidden {
		t.Fatalf("operator destination plan status=%d, want 403", page.Code)
	}
	if client.destinationPlanCalls != 0 {
		t.Fatal("destination plan reached privileged API for Operator")
	}
}
