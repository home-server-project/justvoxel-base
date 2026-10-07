package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyActionAndProgressRoutesRemainRegistered(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/password",
	} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "http://example"+path, nil))
		if rr.Code == http.StatusNotFound || rr.Code == http.StatusMethodNotAllowed {
			t.Errorf("legacy action route %s disappeared: %d", path, rr.Code)
		}
	}
}
