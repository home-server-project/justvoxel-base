package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUPSOptionsClientUsesManagementPaths(t *testing.T) {
	paths := []string{}
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		paths = append(paths, r.URL.Path)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["enabled"] != true {
			t.Fatalf("enabled missing from %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"available":true}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.AdminSetUPSShutdown(context.Background(), "session-token", UPSShutdownRequest{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AdminSetUPSSharing(context.Background(), "session-token", UPSSharingRequest{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/v1/admin/ups/shutdown" || paths[1] != "/v1/admin/ups/sharing" {
		t.Fatalf("unexpected Management paths: %v", paths)
	}
}

func TestUPSStatusDoesNotExposePasswords(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"available":true,"monitor_password":"hidden-monitor","client_password":"hidden-client","monitor_credentials_configured":true,"sharing_credentials_configured":true}`)), Header: make(http.Header)}, nil
	})}}
	status, err := client.UPSStatus(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hidden-monitor") || strings.Contains(string(data), "hidden-client") || !status.MonitorCredentialsConfigured || !status.SharingCredentialsConfigured {
		t.Fatalf("UPS status exposed credentials or lost configuration flags: %s", data)
	}
}
