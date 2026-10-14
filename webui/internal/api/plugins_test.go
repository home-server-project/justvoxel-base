package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPluginDiscoverExplicitSourcesAndCursors(t *testing.T) {
	for _, raw := range []string{"", "modrinth=true&hangar=false", "modrinth=false&hangar=true", "modrinth=true&hangar=true", "modrinth=false&hangar=false", "modrinth_offset=0&hangar_offset=10000&hangar_first=true&limit=12"} {
		q, _ := url.ParseQuery(raw)
		if err := ValidatePluginDiscoverQuery(q); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"modrinth=", "hangar=0", "hangar=True", "modrinth=true&modrinth=false", "hangar=true&hangar=true", "source=hangar", "source=unknown", "provider=Modrinth", "offset=0", "hangar_first=1", "hangar_first=", "hangar_offset=10001", "modrinth_offset=-1", "hangar_offset=%2B1", "modrinth_offset=", "hangar_offset=1&hangar_offset=2", "limit=0", "limit=13", "limit=1.2", "q=a&q=b", "compatible_only=", "release_only=maybe"} {
		q, _ := url.ParseQuery(raw)
		if err := ValidatePluginDiscoverQuery(q); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}

func TestPluginProviderRequestsAreExplicit(t *testing.T) {
	calls := 0
	c := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == "/v1/admin/plugins/icon" {
			if r.URL.Query().Get("source") != "Hangar" || r.URL.Query().Get("project_id") != "123" || r.URL.Query().Has("icon_url") {
				t.Fatal("icon provider lost", r.URL)
			}
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		var req map[string]any
		if json.NewDecoder(r.Body).Decode(&req) != nil || req["source"] != "Hangar" || req["project_id"] != "123" || req["version_id"] != "456" {
			t.Fatal("install provider lost", req)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"source":"Hangar","project_id":"123","version_id":"456"}`))}, nil
	})}}
	if _, err := c.AdminPluginInstallPlan(context.Background(), "session", PluginInstallRequest{Source: "Hangar", ProjectID: "123", VersionID: "456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AdminPluginInstall(context.Background(), "session", PluginInstallApplyRequest{Source: "Hangar", ProjectID: "123", VersionID: "456", PlanFingerprint: resetTestFingerprint}); err != nil {
		t.Fatal(err)
	}
	_, _ = c.AdminPluginSourceIcon(context.Background(), "session", "Hangar", "123")
	if calls != 3 {
		t.Fatal(calls)
	}
	for _, query := range []url.Values{{"source": {"Hangar"}, "project_id": {"abc"}}, {"source": {"Hangar"}, "project_id": {"123"}, "icon_url": {"https://evil.example"}}, {"source": {"Other"}, "project_id": {"123"}}} {
		if ValidatePluginIconQuery(query) == nil {
			t.Fatal("unsafe query", query)
		}
	}
}
