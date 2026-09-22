package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalRootCanReconnectToSetupOperationWithoutBearerSession(t *testing.T) {
	s := surfaceTestServer(t, roleViewer)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	operation, _, err := store.beginSetup("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/setup/current-operation", 0)
	rr := httptest.NewRecorder()
	s.adminCurrentSetupOperation(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root current setup operation returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), operation.OperationID) {
		t.Fatalf("current setup operation missing id: %s", rr.Body.String())
	}
}
