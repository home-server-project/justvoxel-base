package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAdminPluginIconOnlyUsesAgentAndPreservesVerifiedType(t *testing.T) {
	for _, kind := range []string{"image/png", "image/jpeg", "image/webp"} {
		t.Run(kind, func(t *testing.T) {
			data := []byte("\x89PNG\r\n\x1a\nfixture")
			if kind == "image/jpeg" {
				data = []byte("\xff\xd8\xfffixture")
			}
			if kind == "image/webp" {
				data = []byte("RIFF\x00\x00\x00\x00WEBPVP8 fixture")
			}
			calls := 0
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Host != "unix" || r.URL.Path != "/v1/admin/plugins/icon" || len(r.URL.Query()) != 1 || r.URL.Query().Get("project_id") != "root" || r.Header.Get("Authorization") != "Bearer session-token" {
					t.Fatalf("unexpected Agent request: %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}}
			icon, err := client.AdminPluginIcon(context.Background(), "session-token", "root")
			if err != nil || calls != 1 || icon.ContentType != kind || !bytes.Equal(icon.Data, data) {
				t.Fatalf("verified image lost: %#v %v", icon, err)
			}
		})
	}
}

func TestAdminPluginIconRejectsUnsafeAgentResponse(t *testing.T) {
	for _, tc := range []struct {
		name, kind, body string
		status           int
	}{
		{"svg", "image/svg+xml", "<svg/>", 200},
		{"html", "text/html", "private upstream body", 200},
		{"binary", "application/octet-stream", "private upstream body", 200},
		{"mismatch", "image/png", "private upstream body", 200},
		{"oversized", "image/png", strings.Repeat("x", (512<<10)+1), 200},
		{"redirect", "image/png", "private upstream body", 302},
		{"not found", "application/json", "private upstream body", 404},
		{"unauthorized", "application/json", "private upstream body", 401},
		{"forbidden", "application/json", "private upstream body", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls != 1 {
					t.Fatal("Agent redirect followed")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.kind}, "Location": {"https://evil.example/"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			icon, err := client.AdminPluginIcon(context.Background(), "session-token", "root")
			if err == nil || len(icon.Data) != 0 || strings.Contains(err.Error(), "private upstream body") {
				t.Fatal("unsafe image or body exposed")
			}
			if tc.status == 404 {
				var response *ResponseError
				if !errors.As(err, &response) || response.StatusCode != 404 {
					t.Fatal("not-found lost")
				}
			}
		})
	}
}
