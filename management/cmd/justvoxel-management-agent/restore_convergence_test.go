package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGlobalRestoreExecutionRoutesRetired(t *testing.T) {
	// Only read compatibility and instance routes survive. No global request may
	// be translated into an instance target or reach a Restore execution worker.
	mux := http.NewServeMux()
	s := &server{}
	registerAdminOperationRoutes(mux, s)
	registerMinecraftRoutes(mux, s)
	for _, route := range []struct{ method, path string }{
		{"GET", "backups"}, {"POST", "plan"}, {"POST", "apply"},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(route.method, "/v1/admin/restore/"+route.path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("retired %s %s returned %d", route.method, route.path, w.Code)
		}
	}
}
