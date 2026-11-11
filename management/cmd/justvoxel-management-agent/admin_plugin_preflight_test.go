package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurseForgeClientOnlyExactPreflight(t *testing.T) {
	for _, loader := range []string{"fabric", "forge"} {
		t.Run(loader, func(t *testing.T) {
			f, c := curseForgeCatalogFixture(t, loader)
			path := filepath.Join(t.TempDir(), "client.jar")
			descriptor := `{"schemaVersion":1,"id":"client_mod","version":"1","environment":"client"}`
			if loader == "forge" {
				descriptor = "clientSideOnly=true\n[[mods]]\nmodId='client_mod'\nversion='1'\n"
			}
			pluginTestJar(t, path, map[string]string{modDescriptor(loader): descriptor})
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha1.Sum(raw)
			base := curseForgeClient.Transport
			curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				if err != nil {
					return response, err
				}
				// Keep the official fixture identity/game/loader evidence and replace only
				// exact artifact bytes and their authoritative provider hash and size.
				var payload map[string]json.RawMessage
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				update := func(file *curseForgeFile) {
					file.Size = int64(len(raw))
					file.Hashes[0].Value = hex.EncodeToString(sum[:])
				}
				if r.URL.Path == "/v1/mods/10/files/20" {
					var file curseForgeFile
					if err := json.Unmarshal(payload["data"], &file); err != nil {
						t.Fatal(err)
					}
					update(&file)
					payload["data"], _ = json.Marshal(file)
				}
				if r.URL.Path == "/v1/mods/10/files" {
					var files []curseForgeFile
					if err := json.Unmarshal(payload["data"], &files); err != nil {
						t.Fatal(err)
					}
					for i := range files {
						update(&files[i])
					}
					payload["data"], _ = json.Marshal(files)
				}
				body, _ = json.Marshal(payload)
				response.Body = io.NopCloser(bytes.NewReader(body))
				return response, nil
			})
			downloads := 0
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				downloads++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})
			page, err := discoverCurseForgePlugins(context.Background(), c, pluginDiscoverQuery{Limit: 8, Compatible: true, Release: true})
			if err != nil || len(page.Projects) != 1 || page.Projects[0].InstallAvailable || !page.Projects[0].ReviewAvailable || downloads != 0 {
				t.Fatal("Discover must remain cheap and inspectable", page, err, downloads)
			}
			plan, err := resolveProviderPluginInstallPlan(context.Background(), c, f.data, "CurseForge", "10", "20")
			if err != nil || plan.InstallAvailable || plan.Compatibility != clientOnlyModRejection || downloads == 0 {
				t.Fatal(plan, err, downloads)
			}
			response := httptest.NewRecorder()
			f.s.adminPluginInstallPlan(response, pluginTestRequest(http.MethodPost, "/v1/admin/plugins/install/plan", `{"source":"CurseForge","project_id":"10","version_id":"20"}`))
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"install_available":false`) || !strings.Contains(response.Body.String(), clientOnlyModRejection) {
				t.Fatal(response.Code, response.Body.String())
			}
			response = httptest.NewRecorder()
			f.s.adminPluginInstall(response, pluginTestRequest(http.MethodPost, "/v1/admin/plugins/install", `{"source":"CurseForge","project_id":"10","version_id":"20","plan_fingerprint":"sha256:`+strings.Repeat("0", 64)+`"}`))
			if response.Code != http.StatusConflict {
				t.Fatal(response.Code, response.Body.String())
			}
			current, err := f.s.operations.currentPluginInstall()
			if err != nil || current != nil || f.backup || len(f.commands) != 0 || f.playerChecks != 0 {
				t.Fatal("client-only preflight entered installation", current, err, f.commands)
			}
			entries, err := os.ReadDir(filepath.Join(f.data, "mods"))
			if err != nil || len(entries) != 0 {
				t.Fatal("preflight placed JAR", err)
			}
			if _, err := os.Stat(pluginRuntimeLockPath); !os.IsNotExist(err) {
				t.Fatal("preflight acquired maintenance lock", err)
			}
			// An incorrect provider hash is a verification failure, never a normal
			// client-only compatibility result based on untrusted descriptor bytes.
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("bad"))}, nil
			})
			if _, err := resolveProviderPluginInstallPlan(context.Background(), c, f.data, "CurseForge", "10", "20"); err == nil {
				t.Fatal("unverified client-only metadata accepted")
			}
		})
	}
}
