package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type pluginArtifactReadFailure struct{ err error }

func (r pluginArtifactReadFailure) Read([]byte) (int, error) { return 0, r.err }

func TestCurseForgeAuthenticatedArtifactBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, source, url, sha1      string
		size                         int64
		status                       int
		wantRequest, wantKey, wantOK bool
	}{
		{"verified", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, true},
		{"incorrect Content-Length", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, true},
		{"incorrect Content-Length EOF", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, true},
		{"transport read failure", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, false},
		{"short Content-Length", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, true},
		{"absent Content-Length", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, true},
		{"non-success", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 503, true, true, false},
		{"sha1 mismatch", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", strings.Repeat("0", 40), 3, 200, true, true, false},
		{"stream oversized", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, false},
		{"stream short", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, true, true, false},
		{"size mismatch", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 4, 200, true, true, false},
		{"unauthorized", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 401, true, true, false},
		{"forbidden", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 403, true, true, false},
		{"redirect", "CurseForge", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 302, true, true, false},
		{"attacker", "CurseForge", "https://edge.forgecdn.net.evil/files/123/456/x.jar", "", 3, 200, false, false, false},
		{"icon", "CurseForge", "https://media.forgecdn.net/avatars/1/2/x.png", "", 3, 200, false, false, false},
		{"modrinth api", "CurseForge", "https://api.modrinth.com/files/123/456/x.jar", "", 3, 200, false, false, false},
		{"wrong provider", "Modrinth", "https://edge.forgecdn.net/files/123/456/x.jar", "", 3, 200, false, false, false},
		{"modrinth", "Modrinth", "https://cdn.modrinth.com/data/root/versions/release/x.jar", "", 3, 200, true, false, true},
		{"modrinth hash mismatch", "Modrinth", "https://cdn.modrinth.com/data/root/versions/release/x.jar", "", 3, 200, true, false, false},
		{"hangar", "Hangar", "https://hangarcdn.papermc.io/plugins/Author/Project/versions/1/PAPER/x.jar", "", 3, 200, true, false, true},
		{"hangar hash mismatch", "Hangar", "https://hangarcdn.papermc.io/plugins/Author/Project/versions/1/PAPER/x.jar", "", 3, 200, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := curseForgeCatalogFixture(t, "paper")
			sum := sha1.Sum([]byte("jar"))
			hash := sha512.Sum512([]byte("jar"))
			a := pluginInstallArtifact{Source: tc.source, Filename: "x.jar", URL: tc.url, Size: tc.size, SHA1: hex.EncodeToString(sum[:]), SHA512: hex.EncodeToString(hash[:])}
			h256 := sha256.Sum256([]byte("jar"))
			a.SHA256 = hex.EncodeToString(h256[:])
			if tc.name == "hangar hash mismatch" {
				a.SHA256 = strings.Repeat("0", 64)
			}
			if tc.name == "modrinth hash mismatch" {
				a.SHA512 = strings.Repeat("0", 128)
			}
			if tc.sha1 != "" {
				a.SHA1 = tc.sha1
			}
			calls := 0
			pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				expectedHeader := ""
				if tc.wantKey {
					expectedHeader = "private-fixture-key"
				}
				if r.Header.Get("x-api-key") != expectedHeader || r.URL.RawQuery != "" {
					t.Fatal("artifact credential boundary violated")
				}
				body, length := "jar", int64(3)
				if tc.name == "incorrect Content-Length" {
					length = 999
				}
				if tc.name == "short Content-Length" {
					length = 1
				}
				if tc.name == "absent Content-Length" {
					length = -1
				}
				if tc.name == "stream oversized" {
					body, length = "jars", -1
				}
				if tc.name == "stream short" {
					body, length = "ja", -1
				}
				if tc.status == 401 || tc.status == 403 {
					body = "private-response private-fixture-key"
				}
				var reader io.Reader = strings.NewReader(body)
				if tc.name == "incorrect Content-Length EOF" {
					length = 999
					reader = io.MultiReader(reader, pluginArtifactReadFailure{io.ErrUnexpectedEOF})
				}
				if tc.name == "transport read failure" {
					reader = io.MultiReader(reader, pluginArtifactReadFailure{errors.New("private transport failure")})
				}
				return &http.Response{StatusCode: tc.status, ContentLength: length, Header: http.Header{"Location": {"https://attacker.invalid/stolen"}}, Body: io.NopCloser(reader)}, nil
			})}
			_, err := downloadVerifiedPluginArtifact(context.Background(), a, t.TempDir())
			if (err == nil) != tc.wantOK || (calls == 1) != tc.wantRequest || calls > 1 {
				t.Fatal("download outcome", calls, err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "private-fixture-key") || strings.Contains(err.Error(), "private-response") {
					t.Fatal("private response leaked", err)
				}
				if (tc.status == 401 || tc.status == 403) && !strings.Contains(err.Error(), "authentication rejected") {
					t.Fatal(err)
				}
				var diagnostic *pluginProviderDiagnosticError
				if !errors.As(err, &diagnostic) {
					t.Fatal("missing safe provider evidence", err)
				}
				if tc.wantRequest && diagnostic.values["http_status"] != fmt.Sprint(tc.status) {
					t.Fatal("missing HTTP status", diagnostic.values)
				}
				j, _, e := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("a", 64), operationTypePluginInstall, "jv-a1b2c3")
				if e != nil {
					t.Fatal(e)
				}
				recordPluginFailure(f.s.operations, j.OperationID, "download", err)
				log, e := f.s.operations.readDiagnosticLog("plugins", j.OperationID)
				if e != nil || (strings.Contains(string(log), "private-fixture-key") || strings.Contains(string(log), "private-response")) {
					t.Fatal("private diagnostic", e)
				}
			}
		})
	}
}

func TestCurseForgeDiscoveryDefersArtifactValidationToPlan(t *testing.T) {
	for _, kind := range []string{"missing URL", "unsafe URL", "missing SHA1", "duplicate SHA1", "invalid SHA1", "zero size", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			f, server := curseForgeCatalogFixture(t, "fabric")
			original := curseForgeClient.Transport
			curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				response, err := original.RoundTrip(r)
				if err != nil {
					return response, err
				}
				if !strings.HasPrefix(r.URL.Path, "/v1/mods/10/files") {
					return response, nil
				}
				var envelope map[string]json.RawMessage
				if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				mutate := func(file *curseForgeFile) {
					switch kind {
					case "missing URL":
						file.URL = ""
					case "unsafe URL":
						file.URL = "https://attacker.invalid/x.jar"
					case "missing SHA1":
						file.Hashes = nil
					case "duplicate SHA1":
						file.Hashes = append(file.Hashes, file.Hashes[0])
					case "invalid SHA1":
						file.Hashes[0].Value = "bad"
					case "zero size":
						file.Size = 0
					case "oversized":
						file.Size = pluginMaxJar + 1
					}
				}
				if r.URL.Path == "/v1/mods/10/files" {
					var files []curseForgeFile
					if json.Unmarshal(envelope["data"], &files) != nil {
						t.Fatal("files")
					}
					for i := range files {
						mutate(&files[i])
					}
					envelope["data"], _ = json.Marshal(files)
				} else {
					var file curseForgeFile
					if json.Unmarshal(envelope["data"], &file) != nil {
						t.Fatal("file")
					}
					mutate(&file)
					envelope["data"], _ = json.Marshal(file)
				}
				raw, _ := json.Marshal(envelope)
				response.Body = io.NopCloser(bytes.NewReader(raw))
				return response, nil
			})
			page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 8, Compatible: true, Release: true})
			if err != nil || len(page.Projects) != 1 || page.Projects[0].InstallAvailable || !page.Projects[0].ReviewAvailable {
				t.Fatal(page, err)
			}
			if _, err := resolveCurseForgeInstallPlan(context.Background(), server, f.data, "10", "20"); err == nil {
				t.Fatal("direct plan bypassed metadata validation")
			}
		})
	}
}

func TestModrinthExactVersionDedicatedServerPolicy(t *testing.T) {
	for _, environment := range []string{"client_and_server", "client_only_server_optional", "server_only", "server_only_client_optional", "dedicated_server_only", "client_or_server", "client_or_server_prefers_both", "client_only", "singleplayer_only", "unknown", "", "future_environment"} {
		t.Run(environment, func(t *testing.T) {
			f := newPluginInstallFixtureFor(t, "fabric", "26.3")
			if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
				t.Fatal(err)
			}
			version := f.versions["release"]
			version.Environment, version.Loaders, version.GameVersions = environment, []string{"fabric"}, []string{"26.3"}
			f.versions["release"] = version
			server := pluginContentContext("fabric")
			server.InstanceID = "jv-a1b2c3"
			server.MinecraftVersion = "26.3"
			want := environment != "client_only" && environment != "singleplayer_only" && environment != "unknown" && environment != "" && environment != "future_environment"
			if modrinthServerEnvironment(environment) != want {
				t.Fatal("environment mapping")
			}
			_, err := resolvePluginInstallPlan(context.Background(), server, f.data, "root", "release")
			if (err == nil) != want {
				t.Fatal("direct version plan", err)
			}
			original := pluginCatalogClient.Transport
			pluginCatalogClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v2/search" {
					return original.RoundTrip(r)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"hits":[{"project_id":"root","title":"Iris Shaders"}],"total_hits":1}`))}, nil
			})
			for _, compatible := range []bool{true, false} {
				page, err := discoverModrinthPlugins(context.Background(), server, "", compatible, true, 0, 8)
				if err != nil {
					t.Fatal(err)
				}
				if !want {
					if len(page.Projects) != 0 {
						t.Fatal("non-server release appeared in Discover", compatible, page)
					}
				} else if len(page.Projects) != 1 || page.Projects[0].InstallAvailable || !page.Projects[0].ReviewAvailable {
					t.Fatal("server-capable release missing Review", compatible, page)
				}

			}
		})
	}
}

func TestModrinthRequiredDependencyEnvironment(t *testing.T) {
	for _, exact := range []bool{true, false} {
		for _, environment := range []string{"client_only", "singleplayer_only", "unknown", "", "future", "server_only"} {
			t.Run(environment+map[bool]string{true: "/exact", false: "/selected"}[exact], func(t *testing.T) {
				f := newPluginInstallFixture(t)
				f.addJar(t, "dependency", "dependencyversion", "dependency.jar", "Dependency", "test.Dependency")
				v := f.versions["dependencyversion"]
				v.Environment = environment
				f.versions["dependencyversion"] = v
				root := f.versions["release"]
				d := pluginDependency{ProjectID: "dependency", Type: "required"}
				if exact {
					d.VersionID = "dependencyversion"
				}
				root.Dependencies = []pluginDependency{d}
				f.versions["release"] = root
				server := pluginContentContext("paper")
				server.MinecraftVersion = "1.21.8"
				_, err := resolvePluginInstallPlan(context.Background(), server, f.data, "root", "release")
				if (err == nil) != (environment == "server_only") {
					t.Fatal("dependency environment bypass", err)
				}
			})
		}
	}
}

func TestExactFabricForgeSideReviewBeforeOperation(t *testing.T) {
	for _, tc := range []struct {
		name, loader, descriptor string
		reject                   bool
	}{
		{"client", "fabric", `{"id":"example","environment":"client"}`, true},
		{"server", "fabric", `{"id":"example","environment":"server"}`, false},
		{"both", "fabric", `{"id":"example","environment":"*"}`, false},
		{"default", "fabric", `{"id":"example"}`, false},
		{"wrong type", "fabric", `{"id":"example","environment":42}`, true},
		{"null", "fabric", `{"id":"example","environment":null}`, true},
		{"duplicate field", "fabric", `{"id":"example","environment":"client","environment":"server"}`, true},
		{"malformed", "fabric", `{"id":"example",`, true},
		{"forge client", "forge", "clientSideOnly=true\n[[mods]]\nmodId='example'\n", true},
		{"forge server", "forge", "clientSideOnly=false\n[[mods]]\nmodId='example'\n", false},
		{"forge unknown", "forge", "[[mods]]\nmodId='example'\n", false},
		{"forge dependency side", "forge", "[[mods]]\nmodId='example'\n[[dependencies.example]]\nside='CLIENT'\n", false},
		{"forge string", "forge", "description='''\nclientSideOnly=true\n'''\n[[mods]]\nmodId='example'\n", false},
		{"oversized", "fabric", strings.Repeat("x", pluginMetadataLimit+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPluginInstallFixtureFor(t, tc.loader, "1.21.8")
			if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "test.jar")
			pluginTestJar(t, path, map[string]string{modDescriptor(tc.loader): tc.descriptor})
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Exercise CurseForge authentication, verified hashing and exact Review.
			oldDir := curseForgeDirectory
			curseForgeDirectory = curseForgeTestDirectory(t)
			t.Cleanup(func() { curseForgeDirectory = oldDir })
			dir, err := openCurseForgeDirectory(curseForgeDirectory)
			if err != nil {
				t.Fatal(err)
			}
			err = replaceCurseForgeKey(dir, "private-fixture-key")
			dir.Close()
			if err != nil {
				t.Fatal(err)
			}
			sum := sha1.Sum(raw)
			pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("x-api-key") != "private-fixture-key" {
					t.Fatal("missing artifact authentication")
				}
				return &http.Response{StatusCode: 200, ContentLength: int64(len(raw)), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}
			server := pluginContentContext(tc.loader)
			plan := pluginInstallPlan{Server: server, Artifacts: []pluginInstallArtifact{{Source: "CurseForge", Requested: true, Filename: "test.jar", Size: int64(len(raw)), SHA1: hex.EncodeToString(sum[:]), URL: "https://edge.forgecdn.net/files/123/456/test.jar"}}}
			err = reviewRootPluginArtifact(context.Background(), plan)
			if (err != nil) != tc.reject {
				t.Fatal("side review", err)
			}
			if tc.name == "client" || tc.name == "forge client" {
				if err == nil || err.Error() != clientOnlyModRejection {
					t.Fatal("actionable rejection", err)
				}
			}
			operation, err := f.s.operations.currentPluginInstall()
			if err != nil || operation != nil || f.backup || len(f.commands) != 0 {
				t.Fatal("Review created operation or downtime", operation, err)
			}
			entries, err := os.ReadDir(filepath.Join(f.data, "mods"))
			if err != nil || len(entries) != 0 {
				t.Fatal("Review placed live JAR", err)
			}
		})
	}
}

func TestModSideDuplicateZipEntries(t *testing.T) {
	var raw bytes.Buffer
	writer := zip.NewWriter(&raw)
	for i := 0; i < 2; i++ {
		entry, err := writer.Create("fabric.mod.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(entry, `{"id":"example","environment":"server"}`); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "duplicate.jar")
	if err := os.WriteFile(path, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if inspectDedicatedModSide(file, "fabric") == nil {
		t.Fatal("duplicate descriptors accepted")
	}
}

func TestPluginVerifierFailurePrivateEvidenceAndRollback(t *testing.T) {
	f := newPluginInstallFixture(t)
	f.failVerify = true
	original := pluginInstallCommand
	pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		out, err := original(ctx, command, args...)
		if command == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" && err != nil {
			return []byte("ERROR: exact stack reason\nAuthorization: Bearer sensitive-auth\nx-api-key: sensitive-key\nCookie: sensitive-cookie\ntoken=sensitive-token\n" + strings.Repeat("bounded evidence\n", 10000)), errors.New("private command failure")
		}
		return out, err
	}
	journal := f.execute(t, f.plan(t), false)
	if journal.State != operationRolledBack {
		t.Fatal(journal)
	}
	log, err := f.s.operations.readDiagnosticLog("plugins", journal.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stack_verification", "Existing Minecraft stack verification failed", "ERROR: exact stack reason", "rollback_cause", "rollback_result", "rolled_back"} {
		if !strings.Contains(string(log), want) {
			t.Fatal("missing diagnostic", want)
		}
	}
	for _, private := range []string{"sensitive-auth", "sensitive-key", "sensitive-cookie", "sensitive-token", "private command failure"} {
		if strings.Contains(string(log), private) || strings.Contains(journal.Status, private) {
			t.Fatal("private evidence exposed", private)
		}
	}
	if strings.Contains(journal.Status, "exact stack reason") || len(log) > operationDiagnosticLimit || strings.Count(string(log), "bounded evidence") > 5000 {
		t.Fatal("diagnostic/browser bounds")
	}
}

func TestConfiguredCurseForgeKeyNeverReachesHangar(t *testing.T) {
	curseForgeCatalogFixture(t, "paper")
	f := newHangarInstallFixture(t, "paper")
	plan := f.hangarPlan(t) // Fixture checks both API and artifact credential boundaries.
	if err := reviewRootPluginArtifact(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafeCurseForgeArtifactRejectedBeforeKeyAccess(t *testing.T) {
	old := curseForgeDirectory
	curseForgeDirectory = filepath.Join(t.TempDir(), "no-key")
	t.Cleanup(func() { curseForgeDirectory = old })
	_, err := downloadVerifiedPluginArtifact(context.Background(), pluginInstallArtifact{
		Source: "CurseForge", Filename: "x.jar", Size: 3, SHA1: strings.Repeat("0", 40),
		URL: "https://attacker.invalid/files/123/456/x.jar",
	}, t.TempDir())
	if err == nil || err.Error() != "Unsafe artifact metadata" {
		t.Fatal("key access preceded URL validation", err)
	}
}

func TestPluginInstallFailureCausesAndResultsLogged(t *testing.T) {
	for _, stage := range []string{"download", "hash", "startup"} {
		t.Run(stage, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			plan := f.plan(t)
			switch stage {
			case "download":
				f.failDownload = true
			case "hash":
				f.badDownload = true
			case "startup":
				f.missingPlugin = true
			}
			journal := f.execute(t, plan, false)
			if journal.State != operationRolledBack {
				t.Fatal(journal)
			}
			log, err := f.s.operations.readDiagnosticLog("plugins", journal.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Plugin operation failed", "reason=", "rollback_result", "rolled_back"} {
				if !strings.Contains(string(log), want) {
					t.Fatal("failure evidence missing", stage, want)
				}
			}
			if stage == "startup" && !strings.Contains(string(log), "startup_verification") {
				t.Fatal("startup failure stage missing")
			}
		})
	}
}

func TestVerifierDiagnosticConfiguredKeyAndBoundaryRedaction(t *testing.T) {
	f, _ := curseForgeCatalogFixture(t, "paper")
	j, _, err := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("a", 64), operationTypePluginInstall, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	// A sensitive field crossing an output chunk boundary must be sanitized
	// before chunking, and the literal configured key must never be retained.
	output := "ERROR: exact stack reason\n" + strings.Repeat("x", 2040) + "Authorization: Bearer boundary-sensitive\nprivate-fixture-key\n"
	recordPluginFailure(f.s.operations, j.OperationID, "stack_verification", &pluginRuntimeVerificationError{output: output})
	log, err := f.s.operations.readDiagnosticLog("plugins", j.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "ERROR: exact stack reason") {
		t.Fatal("useful reason removed")
	}
	for _, secret := range []string{"private-fixture-key", "boundary-sensitive", "Authorization"} {
		if strings.Contains(string(log), secret) {
			t.Fatal("private verifier detail leaked", secret)
		}
	}
}

func TestPluginReviewFailureDiagnosticWithoutMaintenance(t *testing.T) {
	for _, status := range []int{302, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f, _ := curseForgeCatalogFixture(t, "fabric")
			pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("x-api-key") != "private-fixture-key" {
					t.Fatal("missing authenticated download")
				}
				return &http.Response{StatusCode: status, ContentLength: 999, Header: http.Header{"Location": {"https://attacker.invalid/?key=private-fixture-key"}}, Body: io.NopCloser(strings.NewReader("private-fixture-key response-secret"))}, nil
			})}
			rr := httptest.NewRecorder()
			f.s.adminPluginInstallPlan(rr, pluginTestRequest(http.MethodPost, "/", `{"source":"CurseForge","project_id":"10","version_id":"20"}`))
			var payload struct {
				Error string `json:"error"`
				ID    string `json:"diagnostic_id"`
			}
			if json.Unmarshal(rr.Body.Bytes(), &payload) != nil || rr.Code != 409 || !validOperationID(payload.ID) {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if status == http.StatusFound {
				if !strings.Contains(payload.Error, "CurseForge artifact redirected outside the trusted CDN domain.") {
					t.Fatal("concise redirect reason missing", payload.Error)
				}
			} else if !strings.Contains(payload.Error, fmt.Sprint(status)) {
				t.Fatal("concise HTTP reason missing", payload.Error)
			}
			if len(f.s.operations.operations) != 0 {
				t.Fatal("review created a maintenance operation")
			}
			if current, err := f.s.operations.currentPluginInstall(); err != nil || current != nil {
				t.Fatal("review reserved maintenance", current, err)
			}
			logs, err := f.s.operations.listDiagnosticLogs()
			if err != nil || len(logs) != 1 || logs[0].Category != "plugins" || logs[0].LogID != payload.ID || logs[0].OperationID != "" {
				t.Fatal(logs, err)
			}
			data, err := f.s.operations.readDiagnosticLog("plugins", payload.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"http_status=\"" + fmt.Sprint(status) + "\"", "project_id=\"10\"", "release_id=\"20\"", "http_content_length=\"999\"", "hash_algorithm=\"SHA-1\""} {
				if !strings.Contains(string(data), expected) {
					t.Fatal("missing diagnostic evidence", expected, string(data))
				}
			}
			for _, secret := range []string{"private-fixture-key", "response-secret", "attacker.invalid", "x-api-key"} {
				if strings.Contains(string(data)+rr.Body.String(), secret) {
					t.Fatal("diagnostic leak", secret)
				}
			}
			info, err := os.Stat(filepath.Join(f.s.operations.logsDir, "plugins-logs", payload.ID+".log"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("private log permissions", err)
			}
			read := httptest.NewRecorder()
			request := surfaceRequest(http.MethodGet, "/?download=1", "")
			request.SetPathValue("category", "plugins")
			request.SetPathValue("id", payload.ID)
			f.s.adminDiagnosticLogRead(read, request)
			if read.Code != 200 || read.Body.String() != string(data) || !strings.Contains(read.Header().Get("Content-Disposition"), payload.ID) {
				t.Fatal("normal download path unavailable", read.Code)
			}
		})
	}
}

func TestPluginStartFailureEvidenceBoundsAndRedaction(t *testing.T) {
	f, _ := curseForgeCatalogFixture(t, "paper")
	f.s.operations.diagnosticHostname = "private-appliance"
	journal, _, err := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("a", 64), operationTypePluginInstall, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	writePluginStartup(t, f.data, pluginFixtureStartup+"[Root] startup diagnostic\n")
	calls := 0
	pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("diagnostic command without deadline")
		}
		if !strings.Contains(strings.Join(args, " "), "minecraft-jv-a1b2c3.service") {
			t.Fatal("wrong service", args)
		}
		return []byte("Result=exit-code\nprivate-fixture-key\nhttps://user:private-pass@example.invalid/?key=private-query\n"), errors.New("evidence command failed")
	}
	output := "useful start reason on private-appliance 10.20.30.40\nprivate-fixture-key\n" + strings.Repeat("x", 2040) + "Authorization: Bearer boundary-value\n" + strings.Repeat("bounded detail\n", 10000)
	recordPluginStartFailure(f.s.operations, journal.OperationID, f.data, []string{"Root"}, &pluginStartError{cause: errors.New("exit status 1"), output: output})
	data, err := f.s.operations.readDiagnosticLog("plugins", journal.OperationID)
	if err != nil || calls != 2 || len(data) > operationDiagnosticLimit {
		t.Fatal("evidence bounds", calls, len(data), err)
	}
	for _, want := range []string{"useful start reason", "Result=exit-code", "startup diagnostic", "Startup evidence command unavailable"} {
		if !strings.Contains(string(data), want) {
			t.Fatal("missing startup evidence", want)
		}
	}
	for _, secret := range []string{"private-appliance", "10.20.30.40", "private-fixture-key", "private-pass", "private-query", "boundary-value"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("start diagnostic leak", secret)
		}
	}
}
