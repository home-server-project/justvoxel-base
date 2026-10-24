package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAdminFileBrowserActionsAreExplicit(t *testing.T) {
	calls := 0
	expectedAction := "refresh"
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/admin/filebrowser/"+expectedAction || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}}
	for _, action := range []string{"start", "stop", "refresh", "availability"} {
		expectedAction = action
		if err := client.AdminFileBrowserAction(context.Background(), "session-token", action, "on-demand"); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"restart", "mount", "refresh/other.service", "../system/reboot", ""} {
		if err := client.AdminFileBrowserAction(context.Background(), "session-token", action, ""); err == nil {
			t.Fatal("unbounded action", action)
		}
	}
	if calls != 4 {
		t.Fatalf("unbounded actions sent %d requests", calls)
	}
}
