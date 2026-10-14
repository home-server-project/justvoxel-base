package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestAdminPluginIconOnlyUsesAgentAndPreservesVerifiedType(t *testing.T) {
	for _, kind := range []string{"image/png", "image/jpeg", "image/bmp", "image/gif", "image/webp"} {
		t.Run(kind, func(t *testing.T) {
			data := []byte("\x89PNG\r\n\x1a\nfixture")
			if kind == "image/jpeg" {
				data = []byte("\xff\xd8\xfffixture")
			}
			if kind == "image/webp" {
				data = []byte("RIFF\x00\x00\x00\x00WEBPVP8 fixture")
			}
			if kind == "image/bmp" {
				data = []byte("BMfixture")
			}
			if kind == "image/gif" {
				data = []byte("GIF89afixture")
			}
			calls := 0
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Accept") != "image/png, image/jpeg, image/bmp, image/gif, image/webp" {
					t.Fatal("request must accept exactly the five canonical raster types")
				}
				if r.Method != http.MethodGet || r.URL.Host != "unix" || r.URL.Path != "/v1/admin/plugins/icon" || !reflect.DeepEqual(r.URL.Query(), url.Values{"source": {"Modrinth"}, "project_id": {"root"}}) || r.Header.Get("Authorization") != "Bearer session-token" {
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
		{"svg disguised as png", "image/png", "<svg/>", 200},
		{"xml disguised as gif", "image/gif", `<?xml version="1.0"?><svg/>`, 200},
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

func TestAdminPluginIconForwardsSearchURLOnlyToAgent(t *testing.T) {
	const iconURL = "https://cdn.modrinth.com/data/root/icon.png"
	data := []byte("\x89PNG\r\n\x1a\nfixture")
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Host != "unix" || r.URL.Path != "/v1/admin/plugins/icon" || !reflect.DeepEqual(query, url.Values{"source": {"Modrinth"}, "project_id": {"root"}, "icon_url": {iconURL}}) || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("search icon bypassed Agent: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}}
	icon, err := client.AdminPluginIcon(context.Background(), "session-token", "root", iconURL)
	if err != nil || !bytes.Equal(icon.Data, data) {
		t.Fatal("verified search icon lost", err)
	}
	if _, err := client.AdminPluginIcon(context.Background(), "session-token", "root", iconURL, iconURL); err == nil {
		t.Fatal("duplicate icon URL accepted")
	}
}

func TestAdminPluginSourceIconHangarOnlyUsesAgent(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nfixture")
	calls := 0
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Host != "unix" || r.URL.Path != "/v1/admin/plugins/icon" || !reflect.DeepEqual(r.URL.Query(), url.Values{"source": {"Hangar"}, "project_id": {"123"}}) || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("unexpected Hangar Agent request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}}
	icon, err := client.AdminPluginSourceIcon(context.Background(), "session-token", "Hangar", "123")
	if err != nil || calls != 1 || icon.ContentType != "image/png" || !bytes.Equal(icon.Data, data) {
		t.Fatalf("verified Hangar icon lost: %#v %v", icon, err)
	}
	// Exercise the shared helper to prove caller-supplied URLs cannot reach the Agent.
	for _, iconURL := range []string{"", "https://hangar.papermc.io/icon.png", "https://cdn.modrinth.com/data/root/icon.png"} {
		if _, err := client.adminPluginIcon(context.Background(), "session-token", "Hangar", "123", iconURL); err == nil {
			t.Fatal("Hangar accepted caller-supplied icon URL")
		}
	}
	if calls != 1 {
		t.Fatal("rejected Hangar icon URL caused a request")
	}
}

func TestPluginCatalogSearchIconContract(t *testing.T) {
	var project PluginCatalogProject
	if err := json.Unmarshal([]byte(`{"project_id":"root","icon_url":"https://cdn.modrinth.com/data/root/icon.png"}`), &project); err != nil || project.IconURL != "https://cdn.modrinth.com/data/root/icon.png" {
		t.Fatal("catalog icon lost", err)
	}
	encoded, err := json.Marshal(PluginCatalogProject{ID: "root"})
	if err != nil || bytes.Contains(encoded, []byte(`"icon_url"`)) {
		t.Fatal("absent icon must remain optional", err)
	}
}
