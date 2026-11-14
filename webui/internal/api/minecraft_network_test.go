package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMinecraftNetworkRuntimeReadinessResponse(t *testing.T) {
	for _, tc := range []struct {
		body           string
		enabled, ready bool
	}{
		{`{"config":{"enabled":false},"runtime_ready":false}`, false, false},
		{`{"config":{"enabled":true},"runtime_ready":true}`, true, true},
		{`{"config":{"enabled":true},"runtime_ready":false}`, true, false},
		{`{"config":{"enabled":true}}`, true, false},
	} {
		client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Path != "/v1/minecraft/network" {
				t.Fatal(r.Method, r.URL.Path)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}}
		status, err := client.MinecraftNetwork(context.Background(), "token")
		if err != nil || status.Config.Enabled != tc.enabled || status.RuntimeReady != tc.ready {
			t.Fatal(status, err)
		}
	}
}

func TestMinecraftNetworkCompatibilityAndRuntimeTransport(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/minecraft/network" {
			t.Fatal(r.Method, r.URL)
		}
		body := `{"servers":[{"id":"jv-abc123","software":"forge","eligible":false,"state":"running","compatibility_status":"compatibility-unavailable","compatibility_reason":"Network compatibility is not currently available for Minecraft 26.3."}]}`
		if r.URL.Query().Get("runtime_only") == "true" {
			body = `{"servers":[{"id":"jv-abc123","state":"starting"}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	status, err := client.MinecraftNetwork(context.Background(), "token")
	if err != nil || len(status.Servers) != 1 || status.Servers[0].Eligible || status.Servers[0].CompatibilityStatus != "compatibility-unavailable" || status.Servers[0].CompatibilityReason == "" {
		t.Fatal(status, err)
	}
	status, err = client.MinecraftNetworkRuntime(context.Background(), "token")
	if err != nil || len(status.Servers) != 1 || status.Servers[0].State != "starting" {
		t.Fatal(status, err)
	}
}
