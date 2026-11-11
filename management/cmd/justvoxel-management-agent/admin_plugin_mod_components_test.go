package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func componentModJar(t *testing.T, loader, id string) []byte {
	t.Helper()
	descriptor := `{"schemaVersion":1,"id":"` + id + `","version":"1","environment":"*"}`
	if loader == "quilt" {
		descriptor = `{"quilt_loader":{"id":"` + id + `","version":"1"}}`
	}
	if loader == "forge" || loader == "neoforge" {
		descriptor = "[[mods]]\nmodId='" + id + "'\nversion='1'\n"
	}
	path := filepath.Join(t.TempDir(), "mod.jar")
	pluginTestJar(t, path, map[string]string{modDescriptor(loader): descriptor})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func addComponentRelease(t *testing.T, f *pluginInstallFixture, loader, project, version, filename, id string) {
	t.Helper()
	f.addJar(t, project, version, filename, project, "example.Main")
	raw := componentModJar(t, loader, id)
	sum := sha512.Sum512(raw)
	v := f.versions[version]
	v.Loaders = []string{loader}
	v.Files[0].Size, v.Files[0].Hashes.SHA512 = int64(len(raw)), hex.EncodeToString(sum[:])
	f.versions[version], f.jars[filename] = v, raw
}

func recordRuntimeComponent(t *testing.T, f *pluginInstallFixture, loader, filename string, raw []byte) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("runtime ownership requires root-private evidence")
	}
	if err := os.WriteFile(filepath.Join(f.data, "mods", filename), raw, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	path := filepath.Join(instanceRuntimes.root, "jv-a1b2c3", "crossplay-mods.json")
	manifest := map[string]any{"schema": 1, "platform": loader, "artifacts": map[string]string{filename: hex.EncodeToString(sum[:])}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenericLoaderDependencyPlans(t *testing.T) {
	for _, loader := range []string{"fabric", "neoforge", "forge", "quilt"} {
		t.Run(loader, func(t *testing.T) {
			f := newPluginInstallFixtureFor(t, loader, "1.21.8")
			if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
				t.Fatal(err)
			}
			// The Fabric VM failure is the regression example; the same resolver also
			// handles NeoForge evidence and normal Forge/Quilt provider dependencies.
			id := "shared_runtime"
			if loader == "fabric" {
				id = "fabric-api"
			}
			addComponentRelease(t, f, loader, "root", "release", "root.jar", "requested_mod")
			addComponentRelease(t, f, loader, "dependency", "depversion", "dependency.jar", id)
			v := f.versions["release"]
			v.Dependencies = []pluginDependency{{ProjectID: "dependency", Type: "required"}}
			f.versions["release"] = v
			c := pluginContentContext(loader)
			c.InstanceID, c.MinecraftVersion = "jv-a1b2c3", "1.21.8"
			reuse := loader == "fabric" || loader == "neoforge"
			runtimeFilename := "runtime-component.jar"
			if loader == "fabric" {
				runtimeFilename = "fabric-api.jar"
			}
			if reuse {
				recordRuntimeComponent(t, f, loader, runtimeFilename, f.jars["dependency.jar"])
			}
			baseCatalog := pluginCatalogClient.Transport
			hashCalls, dependencyDownloads := 0, 0
			pluginCatalogClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.Path, "/v2/version_file/") {
					hashCalls++
					dep := f.versions["depversion"]
					if r.URL.Path != "/v2/version_file/"+dep.Files[0].Hashes.SHA512 || r.URL.Query().Get("algorithm") != "sha512" {
						t.Fatal("wrong provider hash evidence")
					}
					raw, _ := json.Marshal(dep)
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
				}
				return baseCatalog.RoundTrip(r)
			})
			baseDownload := pluginDownloadClient.Transport
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if filepath.Base(r.URL.Path) == "dependency.jar" {
					dependencyDownloads++
				}
				if r.Header.Get("x-api-key") != "" {
					t.Fatal("CurseForge key sent to Modrinth")
				}
				return baseDownload.RoundTrip(r)
			})
			components, err := runtimeOwnedModComponents(c, f.data)
			if err != nil || len(components) != map[bool]int{true: 1, false: 0}[reuse] {
				t.Fatal(components, err)
			}
			plan, err := resolveProviderPluginInstallPlan(context.Background(), c, f.data, "Modrinth", "root", "release")
			if err != nil || !plan.InstallAvailable || len(plan.Artifacts) != 2 {
				t.Fatal(plan, err)
			}
			for _, a := range plan.Artifacts {
				if a.Requested {
					continue
				}
				if reuse && (a.Satisfied != runtimeModSatisfied || a.ExistingFilename != runtimeFilename) {
					t.Fatal(a)
				}
				if !reuse && a.Satisfied != "" {
					t.Fatal("invented runtime component", a)
				}
			}
			if reuse && (dependencyDownloads != 0 || hashCalls == 0) {
				t.Fatal("runtime dependency was downloaded or not provider-verified")
			}
			if !reuse && dependencyDownloads == 0 {
				t.Fatal("normal provider dependency not prepared")
			}
			journal := f.execute(t, plan, false)
			if journal.State != operationSucceeded {
				t.Fatal(journal.State, journal.Status)
			}
			entries, err := os.ReadDir(filepath.Join(f.data, "mods"))
			if err != nil || len(entries) != 2 {
				t.Fatal("duplicate dependency placement", entries, err)
			}
			manifest, _, err := readPluginManifest(f.data)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range manifest.Artifacts {
				if reuse && e.ProjectID == "dependency" {
					t.Fatal("runtime component adopted by Plugins")
				}
			}
			if loader == "fabric" {
				helper := filepath.Join(repositoryRoot(t), "mjust/libexec/managed-crossplay-mod-ownership")
				evidence := filepath.Join(instanceRuntimes.root, c.InstanceID, "crossplay-mods.json")
				if out, err := exec.Command("bash", helper, "verify", loader, f.data, evidence, "").CombinedOutput(); err != nil {
					t.Fatalf("runtime ownership guard rejected repaired dependency plan: %v\n%s", err, out)
				}
			}
			if reuse {
				changed := plan
				changed.Artifacts = append([]pluginInstallArtifact(nil), plan.Artifacts...)
				for i := range changed.Artifacts {
					if !changed.Artifacts[i].Requested {
						changed.Artifacts[i].ExistingHash = strings.Repeat("0", 128)
					}
				}
				if fingerprintPluginPlan(changed) == plan.Fingerprint {
					t.Fatal("reuse evidence missing from fingerprint")
				}
				if err := os.WriteFile(filepath.Join(f.data, "mods", runtimeFilename), []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
				if verifyRuntimeModReuse(plan) == nil {
					t.Fatal("modified runtime component reused")
				}
			}
		})
	}
}

func TestModCollisionUsesLoaderIdentity(t *testing.T) {
	for _, loader := range []string{"fabric", "neoforge", "forge", "quilt"} {
		t.Run(loader, func(t *testing.T) {
			f := newPluginInstallFixtureFor(t, loader, "1.21.8")
			if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
				t.Fatal(err)
			}
			addComponentRelease(t, f, loader, "root", "release", "root.jar", "same_identity")
			if err := os.WriteFile(filepath.Join(f.data, "mods", "different-name.jar"), componentModJar(t, loader, "same_identity"), 0644); err != nil {
				t.Fatal(err)
			}
			c := pluginContentContext(loader)
			c.InstanceID, c.MinecraftVersion = "jv-a1b2c3", "1.21.8"
			_, err := resolveProviderPluginInstallPlan(context.Background(), c, f.data, "Modrinth", "root", "release")
			if err == nil || !strings.Contains(err.Error(), "duplicate mod identity: same_identity") {
				t.Fatal(err)
			}
			current, _ := f.s.operations.currentPluginInstall()
			if current != nil || f.backup || len(f.commands) != 0 {
				t.Fatal("collision caused mutation")
			}
		})
	}
}

func TestRuntimeComponentEvidenceFailsClosed(t *testing.T) {
	for _, bad := range []string{"hash", "permissions", "symlink", "platform", "missing"} {
		t.Run(bad, func(t *testing.T) {
			f := newPluginInstallFixtureFor(t, "fabric", "1.21.8")
			if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
				t.Fatal(err)
			}
			recordRuntimeComponent(t, f, "fabric", "owned.jar", componentModJar(t, "fabric", "owned_mod"))
			path := filepath.Join(instanceRuntimes.root, "jv-a1b2c3", "crossplay-mods.json")
			var err error
			switch bad {
			case "hash":
				err = os.WriteFile(filepath.Join(f.data, "mods", "owned.jar"), componentModJar(t, "fabric", "other_mod"), 0644)
			case "permissions":
				err = os.Chmod(path, 0644)
			case "symlink":
				err = os.Rename(path, path+".old")
				if err == nil {
					err = os.Symlink(path+".old", path)
				}
			case "platform":
				err = os.WriteFile(path, []byte(`{"schema":1,"platform":"neoforge","artifacts":{}}`), 0600)
			case "missing":
				err = os.Remove(filepath.Join(f.data, "mods", "owned.jar"))
			}
			if err != nil {
				t.Fatal(err)
			}
			c := pluginContentContext("fabric")
			c.InstanceID = "jv-a1b2c3"
			if _, err := runtimeOwnedModComponents(c, f.data); err == nil {
				t.Fatal("unsafe evidence accepted")
			}
		})
	}
}

func TestRuntimeDependencyProviderEvidenceCannotBeGuessed(t *testing.T) {
	for _, mismatch := range []string{"project", "version", "loader", "game", "hash", "unavailable"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newPluginInstallFixtureFor(t, "fabric", "1.21.8")
			if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
				t.Fatal(err)
			}
			addComponentRelease(t, f, "fabric", "root", "release", "root.jar", "requested_mod")
			addComponentRelease(t, f, "fabric", "dependency", "depversion", "dependency.jar", "shared_mod")
			// The runtime has a different release of the same local mod identity.
			runtime := componentModJar(t, "fabric", "shared_mod")
			runtime = append(runtime, []byte("different-release")...)
			recordRuntimeComponent(t, f, "fabric", "runtime.jar", runtime)
			sum := sha512.Sum512(runtime)
			localHash := hex.EncodeToString(sum[:])
			v := f.versions["release"]
			v.Dependencies = []pluginDependency{{ProjectID: "dependency", VersionID: "depversion", Type: "required"}}
			f.versions["release"] = v
			base := pluginCatalogClient.Transport
			pluginCatalogClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if !strings.HasPrefix(r.URL.Path, "/v2/version_file/") {
					return base.RoundTrip(r)
				}
				mapped := f.versions["depversion"]
				mapped.Files = append([]pluginArtifactFile(nil), mapped.Files...)
				mapped.Files[0].Hashes.SHA512, mapped.Files[0].Size = localHash, int64(len(runtime))
				switch mismatch {
				case "project":
					mapped.ProjectID = "anotherproject"
				case "version":
					mapped.ID = "anotherrelease"
				case "loader":
					mapped.Loaders = []string{"quilt"}
				case "game":
					mapped.GameVersions = []string{"1.20.1"}
				case "hash":
					mapped.Files[0].Hashes.SHA512 = strings.Repeat("0", 128)
				case "unavailable":
					return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}"))}, nil
				}
				raw, _ := json.Marshal(mapped)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})
			c := pluginContentContext("fabric")
			c.InstanceID, c.MinecraftVersion = "jv-a1b2c3", "1.21.8"
			if _, err := resolveProviderPluginInstallPlan(context.Background(), c, f.data, "Modrinth", "root", "release"); err == nil {
				t.Fatal("unproven runtime dependency accepted", mismatch)
			}
			current, _ := f.s.operations.currentPluginInstall()
			if current != nil || f.backup || len(f.commands) != 0 {
				t.Fatal("unproven dependency caused mutation")
			}
		})
	}
}

func TestGenericResolverHasNoMagicDependencyIdentity(t *testing.T) {
	for _, name := range []string{"admin_plugin_install.go", "admin_plugin_mod_components.go", "admin_plugin_curseforge_catalog.go"} {
		raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "management/cmd/justvoxel-management-agent", name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("fabric-api")) {
			t.Fatal("generic resolver contains a magic dependency identity", name)
		}
	}
}
