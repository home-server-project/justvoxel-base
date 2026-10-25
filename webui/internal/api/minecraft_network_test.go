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
