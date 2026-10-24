package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestValidDiagnosticLogIdentifier(t *testing.T) {
	const validID = "12345678-1234-4234-8234-123456789abc"
	categories := []string{"setup", "migration", "restore", "plugins", "reset", "network", "support", "operation"}
	for _, category := range categories {
		t.Run(category, func(t *testing.T) {
			if !validDiagnosticLogIdentifier(category, validID) {
				t.Fatal("valid diagnostic log identifier rejected")
			}
		})
	}

	for _, category := range []string{"unknown", "", "../setup", "setup/operation"} {
		t.Run("invalid category/"+category, func(t *testing.T) {
			if validDiagnosticLogIdentifier(category, validID) {
				t.Fatal("invalid diagnostic log category accepted")
			}
		})
	}

	invalidIDs := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"operation name", "operation-123"},
		{"traversal", "../" + validID},
		{"absolute path", "/" + validID},
		{"relative path", "logs/" + validID},
		{"backslash path", "logs\\" + validID},
		{"filename", validID + ".log"},
		{"missing hyphens", "12345678123442348234123456789abc"},
		{"short UUID", "12345678-1234-4234-8234-123456789ab"},
		{"nonhex UUID", "12345678-1234-4234-8234-123456789abg"},
		{"uppercase UUID", "12345678-1234-4234-8234-123456789ABC"},
		{"wrong UUID version", "12345678-1234-1234-8234-123456789abc"},
		{"wrong UUID variant", "12345678-1234-4234-7234-123456789abc"},
	}
	for _, category := range categories {
		for _, invalid := range invalidIDs {
			t.Run(category+"/"+invalid.name, func(t *testing.T) {
				if validDiagnosticLogIdentifier(category, invalid.id) {
					t.Fatal("invalid diagnostic log ID accepted")
				}
			})
		}
	}
}

func TestPluginsCategoryKeepsExactUUIDValidation(t *testing.T) {
	if !validDiagnosticLogIdentifier("plugins", "12345678-1234-4234-8234-123456789abc") {
		t.Fatal("Plugins log rejected")
	}
	for _, id := range []string{"../private", "12345678-1234-4234-8234-123456789abc.log", "12345678-1234-1234-8234-123456789abc"} {
		if validDiagnosticLogIdentifier("plugins", id) {
			t.Fatal("unsafe Plugins ID accepted", id)
		}
	}
}

func TestSupportGenerationExactRouteAndValidatedResponse(t *testing.T) {
	body := `{"ok":true,"log":{"category":"support","log_id":"12345678-1234-4234-8234-123456789abc","timestamp":"2026-10-17T12:00:00Z"}}`
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/logs/support" || r.URL.RawQuery != "" || r.Body != nil || r.Header.Get("Authorization") != "Bearer session" {
			t.Fatal("unsafe generation request", r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	entry, err := client.AdminGenerateSupportReport(context.Background(), "session")
	if err != nil || entry.Category != "support" {
		t.Fatal(entry, err)
	}
	original := body
	for _, bad := range []string{
		strings.Replace(original, `"support"`, `"setup"`, 1),
		strings.Replace(original, "12345678-1234-4234-8234-123456789abc", "../file", 1),
		strings.Replace(original, "2026-10-17T12:00:00Z", "invalid", 1),
		original + ` {}`, strings.Replace(original, `"ok":true`, `"ok":false`, 1),
	} {
		body = bad
		if _, err := client.AdminGenerateSupportReport(context.Background(), "session"); err == nil {
			t.Fatal("accepted malformed identity", body)
		}
	}
}

func TestNetworkGenerationExactRouteAndValidatedResponse(t *testing.T) {
	body := `{"ok":true,"log":{"category":"network","log_id":"12345678-1234-4234-8234-123456789abc","timestamp":"2026-10-17T12:00:00Z"}}`
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/logs/network" || r.URL.RawQuery != "" || r.Body != nil || r.Header.Get("Authorization") != "Bearer session" {
			t.Fatal("unsafe generation request", r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	entry, err := client.AdminGenerateNetworkReport(context.Background(), "session")
	if err != nil || entry.Category != "network" {
		t.Fatal(entry, err)
	}
	original := body
	for _, bad := range []string{
		strings.Replace(original, `"network"`, `"setup"`, 1),
		strings.Replace(original, "12345678-1234-4234-8234-123456789abc", "../file", 1),
		strings.Replace(original, "2026-10-17T12:00:00Z", "invalid", 1),
		original + ` {}`, strings.Replace(original, `"ok":true`, `"ok":false`, 1),
	} {
		body = bad
		if _, err := client.AdminGenerateNetworkReport(context.Background(), "session"); err == nil {
			t.Fatal("accepted malformed identity", body)
		}
	}
}
