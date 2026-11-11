package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurseForgeProviderDomainRedirects(t *testing.T) {
	for _, tc := range []struct {
		name, destination string
		status, redirects int
		size              int64
		badHash, want     bool
	}{
		{"edge to mediafilez", "https://mediafilez.forgecdn.net/files/123/456/x.jar", 302, 1, 3, false, true},
		{"future host", "https://cdn42.forgecdn.net/files/123/456/x.jar", 301, 1, 3, false, true},
		{"nested host", "https://a.b.forgecdn.net/files/123/456/x.jar", 307, 1, 3, false, true},
		{"apex", "https://forgecdn.net/files/123/456/x.jar", 308, 1, 3, false, true},
		{"443", "https://forgecdn.net:443/files/123/456/x.jar", 303, 1, 3, false, true},
		{"relative", "/files/123/456/other.jar", 302, 1, 3, false, true},
		{"lookalike", "https://evilforgecdn.net/files/123/456/x.jar", 302, 1, 3, false, false},
		{"suffix", "https://forgecdn.net.attacker.example/files/123/456/x.jar", 302, 1, 3, false, false},
		{"external", "https://attacker.example/files/123/456/x.jar", 302, 1, 3, false, false},
		{"external secrets", "https://user:private-password@attacker.example/files/123/456/x.jar?key=private-query#private-fragment", 302, 1, 3, false, false},
		{"downgrade", "http://forgecdn.net/files/123/456/x.jar", 302, 1, 3, false, false},
		{"userinfo", "https://user@forgecdn.net/files/123/456/x.jar", 302, 1, 3, false, false},
		{"IP", "https://1.2.3.4/files/123/456/x.jar", 302, 1, 3, false, false},
		{"port", "https://forgecdn.net:444/files/123/456/x.jar", 302, 1, 3, false, false},
		{"missing location", "", 302, 1, 3, false, false},
		{"loop", "https://edge.forgecdn.net/files/123/456/x.jar", 302, 1, 3, false, false},
		{"five allowed", "/files/123/456/next.jar", 302, 5, 3, false, true},
		{"six refused", "/files/123/456/next.jar", 302, 6, 3, false, false},
		{"size mismatch", "https://cdn42.forgecdn.net/files/123/456/x.jar", 302, 1, 4, false, false},
		{"hash mismatch", "https://cdn42.forgecdn.net/files/123/456/x.jar", 302, 1, 3, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			curseForgeCatalogFixture(t, "paper")
			sum := sha1.Sum([]byte("jar"))
			a := pluginInstallArtifact{Source: "CurseForge", Filename: "x.jar", URL: "https://edge.forgecdn.net/files/123/456/x.jar", Size: tc.size, SHA1: hex.EncodeToString(sum[:])}
			if tc.badHash {
				a.SHA1 = strings.Repeat("0", 40)
			}
			calls := 0
			pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !safeCurseForgeArtifactURL(r.URL.String()) || r.Header.Get("x-api-key") != "private-fixture-key" {
					t.Fatal("credential sent outside validated CDN or absent on hop")
				}
				response := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 777, Body: io.NopCloser(strings.NewReader("jar"))}
				if calls <= tc.redirects {
					response.StatusCode = tc.status
					response.ContentLength = 999
					destination := tc.destination
					if tc.redirects > 1 {
						destination = "/files/123/456/hop" + strings.Repeat("x", calls) + ".jar"
					}
					if destination != "" {
						response.Header.Set("Location", destination)
					}
				}
				return response, nil
			})}
			directory := t.TempDir()
			_, err := downloadVerifiedPluginArtifact(context.Background(), a, directory)
			if (err == nil) != tc.want {
				t.Fatal(tc.name, err)
			}
			if calls > 6 {
				t.Fatal("unbounded redirects", calls)
			}
			if !tc.want && tc.redirects == 1 && !tc.badHash && tc.size == 3 && calls != 1 {
				t.Fatal("rejected destination was contacted", calls)
			}
			if strings.HasPrefix(tc.name, "external") {
				var diagnostic *pluginProviderDiagnosticError
				if !errors.As(err, &diagnostic) || diagnostic.values["redirect_destination"] != "attacker.example/files/123/456/x.jar" || diagnostic.values["http_status"] != "302" || diagnostic.values["http_content_length"] != "999" {
					t.Fatal("missing sanitized redirect evidence", err)
				}
				for _, value := range diagnostic.values {
					for _, secret := range []string{"private-password", "private-query", "private-fragment", "private-fixture-key", "x-api-key"} {
						if strings.Contains(value, secret) {
							t.Fatal("redirect evidence leaked a secret")
						}
					}
				}
			}
			if tc.want {
				raw, readErr := os.ReadFile(filepath.Join(directory, a.Filename))
				if readErr != nil || string(raw) != "jar" || calls != tc.redirects+1 {
					t.Fatal("redirect Content-Length interfered with verified download", readErr, calls)
				}
			}
			if tc.badHash || tc.size != 3 {
				var diagnostic *pluginProviderDiagnosticError
				if !errors.As(err, &diagnostic) || diagnostic.values["http_status"] != "200" || diagnostic.values["http_content_length"] != "777" || diagnostic.values["actual_bytes"] != "3" {
					t.Fatal("final response evidence was not authoritative", err)
				}
				if tc.badHash && (diagnostic.values["hash_algorithm"] != "SHA-1" || diagnostic.values["hash_result"] != "mismatch") {
					t.Fatal("actual SHA-1 was not authoritative", diagnostic.values)
				}
			}
		})
	}
}

func TestCurseForgeInitialProviderDomain(t *testing.T) {
	for _, host := range []string{"edge.forgecdn.net", "mediafilez.forgecdn.net", "cdn42.forgecdn.net", "a.b.forgecdn.net", "forgecdn.net", "forgecdn.net:443"} {
		if !safeCurseForgeArtifactURL("https://" + host + "/files/123/456/x.jar") {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"evilforgecdn.net", "forgecdn.net.attacker.example", "edge.forgecdn.net.attacker.example", "attacker.example", "1.2.3.4", "[::1]", "forgecdn.net:444", "user@forgecdn.net"} {
		if safeCurseForgeArtifactURL("https://" + host + "/files/123/456/x.jar") {
			t.Fatal(host)
		}
	}
}
