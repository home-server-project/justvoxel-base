package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestCurseForgeModDiscoverDefersPartialArtifactEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		mutate       func(*curseForgeFile)
	}{
		{"complete exact record", "", func(f *curseForgeFile) {}},
		{"missing filename", "CurseForge artifact filename is missing or unsafe", func(f *curseForgeFile) { f.Filename = "" }},
		{"unsafe filename", "CurseForge artifact filename is missing or unsafe", func(f *curseForgeFile) { f.Filename = "../secret.jar" }},
		{"missing size", "CurseForge artifact size is missing, invalid or exceeds the safe limit", func(f *curseForgeFile) { f.Size = 0 }},
		{"negative size", "CurseForge artifact size is missing, invalid or exceeds the safe limit", func(f *curseForgeFile) { f.Size = -1 }},
		{"over limit", "CurseForge artifact size is missing, invalid or exceeds the safe limit", func(f *curseForgeFile) { f.Size = pluginMaxJar + 1 }},
		{"missing SHA1", "CurseForge SHA-1 evidence is missing", func(f *curseForgeFile) { f.Hashes = nil }},
		{"malformed SHA1", "CurseForge SHA-1 evidence is malformed or duplicated", func(f *curseForgeFile) { f.Hashes[0].Value = "private-response" }},
		{"duplicate SHA1", "CurseForge SHA-1 evidence is malformed or duplicated", func(f *curseForgeFile) { f.Hashes = append(f.Hashes, f.Hashes[0]) }},
		{"missing URL", "CurseForge did not provide an official artifact URL for this release", func(f *curseForgeFile) { f.URL = "" }},
		{"unsafe URL", "CurseForge official artifact URL is unsafe", func(f *curseForgeFile) { f.URL = "https://attacker.invalid/private-response?key=secret" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server := curseForgeCatalogFixture(t, "fabric")
			base := curseForgeClient.Transport
			exactCalls, urlCalls, downloads := 0, 0, 0
			curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v1/mods/10/files/20/download-url" {
					urlCalls++
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`{"data":""}`))}, nil
				}
				response, err := base.RoundTrip(r)
				if err != nil || (r.URL.Path != "/v1/mods/10/files" && r.URL.Path != "/v1/mods/10/files/20") {
					return response, err
				}
				defer response.Body.Close()
				var envelope map[string]json.RawMessage
				if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
					t.Fatal(err)
				}
				if r.URL.Path == "/v1/mods/10/files" {
					var files []curseForgeFile
					if err := json.Unmarshal(envelope["data"], &files); err != nil {
						t.Fatal(err)
					}
					// Catalog evidence identifies an exact Fabric/game release, but
					// intentionally contains none of the install artifact evidence.
					for i := range files {
						files[i].Filename, files[i].Size, files[i].URL, files[i].Hashes = "", 0, "", nil
					}
					envelope["data"], _ = json.Marshal(files)
				} else {
					exactCalls++
					var file curseForgeFile
					if err := json.Unmarshal(envelope["data"], &file); err != nil {
						t.Fatal(err)
					}
					tc.mutate(&file)
					envelope["data"], _ = json.Marshal(file)
				}
				raw, err := json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})
			baseDownload := pluginDownloadClient.Transport
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				downloads++
				return baseDownload.RoundTrip(r)
			})
			page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 8, Compatible: true, Release: true})
			if err != nil || len(page.Projects) != 1 || page.Projects[0].ID != "10" || page.Projects[0].Version == nil || page.Projects[0].Version.ID != "20" || page.Projects[0].InstallAvailable || !page.Projects[0].ReviewAvailable || urlCalls != 0 || downloads != 0 {
				t.Fatal("partial artifact metadata suppressed Review or triggered preflight", page, err, urlCalls, downloads)
			}
			beforeReview := exactCalls
			plan, err := resolveProviderPluginInstallPlan(context.Background(), server, f.data, "CurseForge", "10", "20")
			if exactCalls != beforeReview+1 {
				t.Fatal("Review did not independently re-fetch the exact file", exactCalls, beforeReview)
			}
			if tc.reason == "" {
				if err != nil || !plan.InstallAvailable || downloads == 0 {
					t.Fatal("complete exact evidence did not pass verified JAR Review", plan, err, downloads)
				}
			} else if err == nil || err.Error() != tc.reason || plan.InstallAvailable || downloads != 0 {
				t.Fatal("incomplete exact evidence did not block precisely before download", plan, err, downloads)
			}
			wantURLCalls := 0
			if tc.name == "missing URL" {
				wantURLCalls = 1
			}
			if urlCalls != wantURLCalls {
				t.Fatal("unexpected URL resolution", urlCalls)
			}
			current, err := f.s.operations.currentPluginInstall()
			if err != nil || current != nil || len(f.s.operations.operations) != 0 || f.backup || len(f.commands) != 0 || f.playerChecks != 0 {
				t.Fatal("Review created an operation, backup or downtime", current, err, f.commands)
			}
			entries, err := os.ReadDir(filepath.Join(f.data, "mods"))
			if err != nil || len(entries) != 0 {
				t.Fatal("Review placed a live JAR", err)
			}
		})
	}
}
