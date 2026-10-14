package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPluginAuthoritativeProviderMappings(t *testing.T) {
	for _, tc := range []struct {
		server  string
		loaders []string
	}{{"paper", []string{"paper", "spigot", "bukkit"}}, {"purpur", []string{"purpur", "paper", "spigot", "bukkit"}}} {
		platform, err := hangarPluginPlatform(tc.server)
		if err != nil || platform != "PAPER" || !slices.Equal(pluginLoaders(tc.server), tc.loaders) {
			t.Fatal(tc, platform, err)
		}
	}
	for _, unknown := range []string{"", "velocity", "Paper · Minecraft 26.2", "future"} {
		if _, err := hangarPluginPlatform(unknown); err == nil || len(pluginLoaders(unknown)) != 0 {
			t.Fatal("unknown target mapped", unknown)
		}
	}
	for _, id := range []string{"0", "01", "-1", "+1", " 1", "1 ", "1e2", "1.0", "abc", "9223372036854775808", strings.Repeat("1", 64)} {
		if validPluginIdentity("Hangar", id) {
			t.Fatal("invalid numeric identity", id)
		}
	}
	if !validPluginIdentity("Hangar", "9223372036854775807") || !validPluginIdentity("Modrinth", "99IreA6y") || validPluginIdentity("Unknown", "1") {
		t.Fatal("provider validation")
	}
	if pluginIdentity("Hangar", "123") == pluginIdentity("Modrinth", "123") || pluginParentIdentity("123") != pluginIdentity("Modrinth", "123") || validPluginParent("Other:1") {
		t.Fatal("provider identities collided")
	}
}

type hangarInstallFixture struct {
	*pluginInstallFixture
	releases   map[string]hangarVersion
	serverType string
}

func newHangarInstallFixture(t *testing.T, serverType string) *hangarInstallFixture {
	t.Helper()
	f := &hangarInstallFixture{pluginInstallFixture: newPluginInstallFixture(t), releases: map[string]hangarVersion{}, serverType: serverType}
	f.add(t, 1, 10, "root.jar", "Root", "example.Root")
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return json.Marshal(map[string]any{"configured": true, "minecraft": map[string]string{"server_type": f.serverType, "data_path": f.data}})
	}
	runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
		return json.Marshal(map[string]any{"minecraft": map[string]string{"server_type": f.serverType, "version": "1.21.8"}})
	}
	pluginCatalogClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "hangar.papermc.io" || r.Header.Get("Authorization") != "" {
			t.Fatal("unsafe Hangar API request", r.URL)
		}
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 5 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "projects" {
			t.Fatal("unexpected path", r.URL)
		}
		id := parts[4]
		v, ok := f.releases[id]
		if !ok {
			return nil, fmt.Errorf("missing project")
		}
		var payload any
		switch len(parts) {
		case 5:
			metadata := hangarProject{ID: v.ProjectID, Name: v.Name, Description: "Hangar plugin"}
			metadata.Namespace.Owner = "Author"
			metadata.Namespace.Slug = "Project" + id
			payload = metadata
		case 6:
			if parts[5] != "versions" || r.URL.Query().Get("platform") != "PAPER" || r.URL.Query().Get("platformVersion") != "1.21.8" {
				t.Fatal("selected target lost", r.URL)
			}
			payload = map[string]any{"pagination": map[string]int{"count": 1, "offset": 0, "limit": 25}, "result": []hangarVersion{v}}
		case 7:
			if !validPluginIdentity("Hangar", parts[6]) {
				t.Fatal("exact release not fetched", r.URL)
			}
			payload = v
		default:
			t.Fatal("unexpected metadata", r.URL)
		}
		raw, err := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, err
	})}
	pluginDownloadClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if !safeHangarArtifactURL(r.URL.String()) {
			t.Fatal("unsafe download", r.URL)
		}
		parts := strings.Split(r.URL.Path, "/")
		id := strings.TrimPrefix(parts[3], "Project")
		v := f.releases[id]
		raw := f.jars[v.Downloads["PAPER"].FileInfo.Name]
		if f.failDownload {
			return nil, fmt.Errorf("download failed")
		}
		if f.badDownload {
			raw = slices.Clone(raw)
			raw[0] ^= 1
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(raw)), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})}
	return f
}
func (f *hangarInstallFixture) add(t *testing.T, project, release int64, filename, name, main string) {
	t.Helper()
	jar := hangarTestJar(t, map[string]string{"plugin.yml": "name: " + name + "\nversion: 1.0\nmain: " + main + "\n"})
	f.jars[filename] = jar
	hash := sha256.Sum256(jar)
	v := hangarVersion{ID: release, ProjectID: project, Name: "1.0", CreatedAt: "2026-10-01T00:00:00Z", PlatformDependencies: map[string][]string{"PAPER": {"1.21.8"}}, PluginDependencies: map[string][]hangarDependency{"PAPER": {}}, Downloads: map[string]hangarDownload{"PAPER": {DownloadURL: testHangarDownloadURL(project, "1.0", filename), FileInfo: &hangarFileInfo{Name: filename, Size: int64(len(jar)), SHA256: hex.EncodeToString(hash[:])}}}}
	v.Channel.Name = "Release"
	v.Channel.Flags = []string{}
	f.releases[strconv.FormatInt(project, 10)] = v
}
func (f *hangarInstallFixture) hangarPlan(t *testing.T) pluginInstallPlan {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server, path, ok := f.s.pluginContext(httptest.NewRecorder(), req)
	if !ok {
		t.Fatal("no authoritative context")
	}
	p, err := resolveProviderPluginInstallPlan(context.Background(), server, path, "Hangar", "1", "10")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestHangarInstallUsesExistingColdTransaction(t *testing.T) {
	for _, serverType := range []string{"paper", "purpur"} {
		for _, running := range []bool{true, false} {
			t.Run(serverType+fmt.Sprint(running), func(t *testing.T) {
				f := newHangarInstallFixture(t, serverType)
				f.active = running
				p := f.hangarPlan(t)
				if p.Server.ServerType != serverType || !strings.EqualFold(p.Server.ServerSoftware, serverType) || p.Server.MinecraftVersion != "1.21.8" || p.Source != "Hangar" {
					t.Fatal("target identity changed", p.Server)
				}
				if err := reviewRootPluginArtifact(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				j := f.execute(t, p, false)
				if j.State != operationSucceeded || !f.backup || f.active != running {
					t.Fatal(j.State, j.Status, f.commands)
				}
				m, _, err := readPluginManifest(f.data)
				if err != nil || len(m.Artifacts) != 1 {
					t.Fatal(err, m)
				}
				sum := sha512.Sum512(f.jars["root.jar"])
				if m.Artifacts[0].Source != "Hangar" || m.Artifacts[0].SHA512 != hex.EncodeToString(sum[:]) {
					t.Fatal("local ownership hash missing", m)
				}
				inventory, err := readPluginInventory(f.data)
				if err != nil || len(inventory) != 1 || inventory[0].Source != "Hangar" || inventory[0].Management != "plugins" {
					t.Fatal(inventory, err)
				}
			})
		}
	}
}
func TestHangarInstallFailuresPreserveRuntime(t *testing.T) {
	for _, kind := range []string{"size", "hash", "download", "descriptor", "loader", "backup", "runtime", "startup", "external", "identity", "incompatible"} {
		t.Run(kind, func(t *testing.T) {
			f := newHangarInstallFixture(t, "purpur")
			v := f.releases["1"]
			d := v.Downloads["PAPER"]
			switch kind {
			case "external":
				d.DownloadURL = ""
				d.ExternalURL = "https://external.example/root.jar"
			case "identity":
				v.ID = 11
			case "incompatible":
				v.PlatformDependencies["PAPER"] = []string{"26.2"}
			case "size":
				d.FileInfo.Size++
			case "hash":
				f.badDownload = true
			case "download":
				f.failDownload = true
			case "descriptor", "loader":
				files := map[string]string{"plugin.yml": "name: \n"}
				if kind == "loader" {
					files = map[string]string{"fabric.mod.json": "{}"}
				}
				raw := hangarTestJar(t, files)
				f.jars["root.jar"] = raw
				hash := sha256.Sum256(raw)
				d.FileInfo.Size = int64(len(raw))
				d.FileInfo.SHA256 = hex.EncodeToString(hash[:])
			case "backup":
				f.failBackup = true
			case "runtime":
				f.failVerify = true
			case "startup":
				f.missingPlugin = true
			}
			v.Downloads["PAPER"] = d
			f.releases["1"] = v
			server := pluginServerContext{ServerType: "purpur", ServerSoftware: "Purpur", Supported: true, MinecraftVersion: "1.21.8"}
			p, err := resolveHangarPluginInstallPlan(context.Background(), server, f.data, "1", "10")
			if slices.Contains([]string{"external", "identity", "incompatible"}, kind) {
				if err == nil || len(f.commands) > 0 {
					t.Fatal("unsafe metadata accepted", kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			j := f.execute(t, p, false)
			if j.State != operationRolledBack {
				t.Fatal(j.State, j.Status)
			}
			if !f.active {
				t.Fatal("original state lost")
			}
			if _, err := os.Stat(filepath.Join(f.data, "plugins", "root.jar")); !os.IsNotExist(err) {
				t.Fatal("failed artifact kept")
			}
			if slices.Contains([]string{"size", "hash", "download", "descriptor", "loader"}, kind) && (len(f.commands) > 0 || f.backup) {
				t.Fatal("downtime before validation")
			}
		})
	}
}
func TestHangarDependenciesAndLimits(t *testing.T) {
	for _, kind := range []string{"internal", "optional", "external", "cycle", "depth", "artifacts", "download bound", "filename"} {
		t.Run(kind, func(t *testing.T) {
			f := newHangarInstallFixture(t, "paper")
			id := int64(2)
			f.add(t, 2, 20, "dependency.jar", "Dependency", "example.Dependency")
			v := f.releases["1"]
			v.PluginDependencies["PAPER"] = []hangarDependency{{Name: "Dependency", Required: true, ProjectID: &id}}
			switch kind {
			case "optional":
				v.PluginDependencies["PAPER"][0].Required = false
			case "external":
				v.PluginDependencies["PAPER"][0].ProjectID = nil
				v.PluginDependencies["PAPER"][0].ExternalURL = "https://external.example/dependency.jar"
			case "cycle":
				dep := f.releases["2"]
				root := int64(1)
				dep.PluginDependencies["PAPER"] = []hangarDependency{{Name: "Root", Required: true, ProjectID: &root}}
				f.releases["2"] = dep
			case "depth":
				for i := int64(2); i < 10; i++ {
					f.add(t, i, i*10, fmt.Sprintf("p%d.jar", i), fmt.Sprintf("P%d", i), fmt.Sprintf("example.P%d", i))
					dep := f.releases[strconv.FormatInt(i, 10)]
					next := i + 1
					dep.PluginDependencies["PAPER"] = []hangarDependency{{Name: "Next", Required: true, ProjectID: &next}}
					f.releases[strconv.FormatInt(i, 10)] = dep
				}
			case "artifacts":
				v.PluginDependencies["PAPER"] = nil
				for i := int64(2); i < 19; i++ {
					f.add(t, i, i*10, fmt.Sprintf("p%d.jar", i), fmt.Sprintf("P%d", i), fmt.Sprintf("example.P%d", i))
					id := i
					v.PluginDependencies["PAPER"] = append(v.PluginDependencies["PAPER"], hangarDependency{Name: "Child", Required: true, ProjectID: &id})
				}
			case "download bound":
				v.Downloads["PAPER"].FileInfo.Size = pluginMaxJar
				dep := f.releases["2"]
				dep.Downloads["PAPER"].FileInfo.Size = pluginMaxJar
				f.releases["2"] = dep
				f.add(t, 3, 30, "third.jar", "Third", "example.Third")
				id := int64(3)
				v.PluginDependencies["PAPER"] = append(v.PluginDependencies["PAPER"], hangarDependency{Name: "Third", Required: true, ProjectID: &id})
			case "filename":
				dep := f.releases["2"]
				dep.Downloads["PAPER"].FileInfo.Name = "root.jar"
				f.releases["2"] = dep
			}
			f.releases["1"] = v
			p, err := resolveHangarPluginInstallPlan(context.Background(), pluginServerContext{ServerType: "paper", Supported: true, MinecraftVersion: "1.21.8"}, f.data, "1", "10")
			if kind != "internal" && kind != "optional" {
				if err == nil {
					t.Fatal("unsafe graph accepted", kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "optional" {
				if len(p.Artifacts) != 1 || len(p.DisplayDependencies) != 1 {
					t.Fatal("optional auto-installed", p)
				}
			} else {
				if len(p.Artifacts) != 2 || !slices.Equal(p.Artifacts[1].RequiredBy, []string{"Hangar:1"}) {
					t.Fatal("parent identity", p.Artifacts)
				}
			}
		})
	}
}
func TestPluginProviderOwnershipBackwardCompatibility(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	m, raw, err := readPluginManifest(f.data)
	if err != nil {
		t.Fatal(err)
	}
	m.Artifacts[0].Source = ""
	if _, err := commitPluginManifest(f.data, raw, m); err != nil {
		t.Fatal(err)
	}
	_, before, _ := readPluginManifest(f.data)
	inventory, err := readPluginInventory(f.data)
	if err != nil || inventory[0].Source != "Modrinth" {
		t.Fatal(inventory, err)
	}
	_, after, _ := readPluginManifest(f.data)
	if !bytes.Equal(before, after) {
		t.Fatal("legacy read rewrote manifest")
	}
	legacy := m.Artifacts[0]
	legacy.ProjectID = "123"
	legacy.VersionID = "456"
	hangar := legacy
	hangar.Source = "Hangar"
	hangar.Filename = "hangar.jar"
	m.Artifacts = []pluginManifestEntry{legacy, hangar}
	if err := validatePluginManifest(m); err != nil {
		t.Fatal("cross-provider collision", err)
	}
	for _, source := range []string{"Unknown", "hangar", "Hangar"} {
		bad := m
		bad.Artifacts = slices.Clone(m.Artifacts)
		bad.Artifacts[1].Source = source
		bad.Artifacts[1].VersionID = "notNumeric"
		if _, err := commitPluginManifest(f.data, before, bad); err == nil {
			t.Fatal("malformed provider committed")
		}
	}
	if pluginManifestArtifact(pluginInstallArtifact{ProjectID: "a"}).Source != "Modrinth" {
		t.Fatal("new Modrinth source missing")
	}
}
func TestHangarInstallManifestCanonicalProviderOrder(t *testing.T) {
	for _, source := range []string{"", "Modrinth"} {
		t.Run("existing_source="+source, func(t *testing.T) {
			f := newHangarInstallFixture(t, "paper")
			f.add(t, 123, 456, "root.jar", "Root", "example.Root")
			jar := hangarTestJar(t, map[string]string{"plugin.yml": "name: Legacy\nversion: 1.0\nmain: example.Legacy\n"})
			hash := sha512.Sum512(jar)
			legacy := pluginManifestArtifact(pluginInstallArtifact{Source: source, ProjectID: "123", VersionID: "456", VersionNumber: "1.0", Filename: "legacy.jar", SHA512: hex.EncodeToString(hash[:]), PluginName: "Legacy", Main: "example.Legacy", Requested: true})
			legacy.Source = source // Preserve the deployed source-less v1 representation.
			if err := os.WriteFile(filepath.Join(f.data, "plugins", legacy.Filename), jar, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := commitPluginManifest(f.data, nil, pluginManifest{"v1", []pluginManifestEntry{legacy}}); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			server, path, ok := f.s.pluginContext(httptest.NewRecorder(), request)
			if !ok {
				t.Fatal("no authoritative context")
			}
			plan, err := resolveProviderPluginInstallPlan(context.Background(), server, path, "Hangar", "123", "456")
			if err != nil {
				t.Fatal(err)
			}
			if j := f.execute(t, plan, false); j.State != operationSucceeded {
				t.Fatal(j.State, j.Status)
			}
			manifest, before, err := readPluginManifest(f.data)
			if err != nil || len(manifest.Artifacts) != 2 {
				t.Fatal("cross-provider entries did not both survive", manifest, err)
			}
			if pluginIdentity(manifest.Artifacts[0].Source, manifest.Artifacts[0].ProjectID) != "Hangar:123" || pluginIdentity(manifest.Artifacts[1].Source, manifest.Artifacts[1].ProjectID) != "Modrinth:123" {
				t.Fatal("persisted manifest is not in canonical provider order", manifest)
			}
			if !reflect.DeepEqual(manifest.Artifacts[1], legacy) {
				t.Fatal("untouched Modrinth entry was rewritten", manifest.Artifacts[1])
			}
			inventory, err := readPluginInventory(f.data)
			if err != nil || len(inventory) != 2 {
				t.Fatal(inventory, err)
			}
			identities := []string{}
			for _, item := range inventory {
				if item.Management != "plugins" {
					t.Fatal("provider ownership lost", item)
				}
				identities = append(identities, pluginIdentity(item.Source, item.ProjectID))
			}
			slices.Sort(identities)
			if !slices.Equal(identities, []string{"Hangar:123", "Modrinth:123"}) {
				t.Fatal("inventory provider identities collided", identities)
			}
			_, after, err := readPluginManifest(f.data)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("reading/inventory migrated persisted manifest", err)
			}
		})
	}
}

func TestHangarLifecycleAndPreservedReset(t *testing.T) {
	for _, action := range []string{"enable", "disable", "remove", "reset"} {
		t.Run(action, func(t *testing.T) {
			state := "enabled"
			if action == "enable" {
				state = "disabled"
			}
			f := newPluginManageFixture(t, state)
			m, raw, _ := readPluginManifest(f.data)
			e := m.Artifacts[0]
			e.Source = "Hangar"
			e.ProjectID = "1"
			e.VersionID = "10"
			m.Artifacts[0] = e
			f.entry = e
			if _, err := commitPluginManifest(f.data, raw, m); err != nil {
				t.Fatal(err)
			}
			if action == "reset" {
				plan := adminMinecraftResetPlanResponse{DataPath: f.data, DataScope: "network", DataAction: "preserve"}
				if err := prepareMinecraftResetPlugins(&plan); err != nil {
					t.Fatal(err)
				}
				if len(plan.PluginEvidence.Entries) != 1 || plan.PluginEvidence.Entries[0].Entry.Source != "Hangar" {
					t.Fatal("reset provider lost")
				}
				id, err := newOperationID()
				if err != nil {
					t.Fatal(err)
				}
				tx := minecraftResetPluginTransaction{SchemaVersion: "v1", OperationID: id, Plan: plan, Plugins: *plan.PluginEvidence}
				if err := stageResetPluginJars(tx); err != nil {
					t.Fatal(err)
				}
				if err := restoreResetPluginJars(tx); err != nil {
					t.Fatal(err)
				}
				if state, err := resolveManagedPluginLocation(f.data, e); err != nil || state != "enabled" {
					t.Fatal("Hangar reset rollback failed", state, err)
				}
				if err := stageResetPluginJars(tx); err != nil {
					t.Fatal(err)
				}
				if err := commitResetPluginCleanup(tx); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(f.data, "plugins", e.Filename)); !os.IsNotExist(err) {
					t.Fatal("Hangar reset JAR remained")
				}
				if _, err := os.Stat(filepath.Join(f.data, "plugins", "Root", "config.yml")); err != nil {
					t.Fatal("reset erased configuration", err)
				}
				return
			}
			p, err := f.s.resolvePluginManage(context.Background(), action, "1", "Hangar")
			if err != nil {
				t.Fatal(err)
			}
			j := f.manage(t, p, false)
			if j.State != operationSucceeded {
				t.Fatal(j.State, j.Status)
			}
			m, _, err = readPluginManifest(f.data)
			if err != nil {
				t.Fatal(err)
			}
			if action == "remove" {
				if len(m.Artifacts) != 0 {
					t.Fatal("not removed")
				}
			} else {
				if len(m.Artifacts) != 1 || m.Artifacts[0].Source != "Hangar" {
					t.Fatal("ownership lost")
				}
			}
		})
	}
}
func TestHangarIconTrustBoundary(t *testing.T) {
	for _, kind := range []string{"trusted", "user", "default", "host", "path", "redirect", "oversize", "unsupported", "identity", "caller URL"} {
		t.Run(kind, func(t *testing.T) {
			old := pluginCatalogClient
			t.Cleanup(func() { pluginCatalogClient = old })
			avatar := "https://hangarcdn.papermc.io/avatars/project/1.webp?v=2"
			status := 200
			data := pluginIconImage(t, "image/webp")
			count := 0
			switch kind {
			case "user":
				avatar = "https://hangarcdn.papermc.io/avatars/user/123e4567-e89b-12d3-a456-426614174000.webp?v=7"
			case "default":
				avatar = "https://hangarcdn.papermc.io/avatars/default/default.webp?v=0"
			case "host":
				avatar = "https://evil.example/avatars/project/1.webp?v=2"
			case "path":
				avatar = "https://hangarcdn.papermc.io/other/icon.png"
			case "redirect":
				status = 302
			case "oversize":
				data = bytes.Repeat([]byte("a"), pluginIconLimit+1)
			case "unsupported":
				avatar = "https://hangarcdn.papermc.io/avatars/project/1.svg?v=2"
				data = []byte("<svg/>")
			}
			pluginCatalogClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				count++
				if r.URL.Host == "hangar.papermc.io" {
					id := int64(1)
					if kind == "identity" {
						id = 2
					}
					if r.URL.Path != "/api/v1/projects/1" {
						t.Fatal("exact project not re-fetched", r.URL)
					}
					raw, _ := json.Marshal(hangarProject{ID: id, AvatarURL: avatar})
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
				}
				if r.URL.String() != avatar || r.URL.Host != "hangarcdn.papermc.io" {
					t.Fatal("unsafe image fetch", r.URL)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://evil.example/"}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			path := "/v1/admin/plugins/icon?source=Hangar&project_id=1"
			if kind == "caller URL" {
				path += "&icon_url=https://evil.example/icon.png"
			}
			s.adminPluginIcon(rr, surfaceRequest(http.MethodGet, path, ""))
			want := 502
			if kind == "trusted" || kind == "user" {
				want = 200
			}
			if kind == "default" {
				want = 404
				if count != 1 {
					t.Fatal("default avatar contacted CDN", count)
				}
			}
			if kind == "caller URL" {
				want = 400
			}
			if rr.Code != want {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if want == 200 && (rr.Header().Get("Content-Type") != "image/webp" || !bytes.Equal(rr.Body.Bytes(), data)) {
				t.Fatal("verified raster lost")
			}
			if kind == "caller URL" && count != 0 {
				t.Fatal("caller URL reached upstream", count)
			}
			if want == 200 && count != 2 {
				t.Fatal("metadata and fixed-CDN image not fetched", count)
			}
			if kind == "redirect" && count != 2 {
				t.Fatal("redirect followed")
			}
		})
	}
}

func hangarTestJar(t *testing.T, fields map[string]string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.jar")
	pluginTestJar(t, path, fields)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testHangarDownloadURL(id int64, version, filename string) string {
	p := hangarProject{ID: id}
	p.Namespace.Owner = "Author"
	p.Namespace.Slug = "Project" + strconv.FormatInt(id, 10)
	return hangarDownloadURL(p, version, "PAPER", filename)
}

func TestHangarDiscoveryAuthoritativeInstallAvailability(t *testing.T) {
	for _, kind := range []string{"direct", "incompatible", "external", "unsafe origin", "wrong release path"} {
		t.Run(kind, func(t *testing.T) {
			f := newHangarInstallFixture(t, "purpur")
			v := f.releases["1"]
			d := v.Downloads["PAPER"]
			switch kind {
			case "incompatible":
				v.PlatformDependencies["PAPER"] = []string{"26.2"}
			case "external":
				d.DownloadURL = ""
				d.ExternalURL = "https://external.example/plugin.jar"
			case "unsafe origin":
				d.DownloadURL = strings.Replace(d.DownloadURL, "hangarcdn.papermc.io", "evil.example", 1)
			case "wrong release path":
				d.DownloadURL = strings.Replace(d.DownloadURL, "1.0", "2.0", 1)
			}
			v.Downloads["PAPER"] = d
			metadata := hangarProject{ID: 1, Name: "Plugin", AvatarURL: "https://hangarcdn.papermc.io/avatars/project/1.webp?v=1"}
			metadata.Namespace.Owner = "Author"
			metadata.Namespace.Slug = "Project1"
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "hangar.papermc.io" || r.URL.Scheme != "https" || r.URL.Query().Get("platform") != "PAPER" {
					t.Fatal("provider context", r.URL)
				}
				var payload any
				if r.URL.Path == "/api/v1/projects" {
					payload = map[string]any{"pagination": map[string]int{"count": 1, "offset": 0, "limit": 1}, "result": []hangarProject{metadata}}
				} else {
					payload = map[string]any{"pagination": map[string]int{"count": 1, "offset": 0, "limit": 25}, "result": []hangarVersion{v}}
				}
				raw, _ := json.Marshal(payload)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}
			out, err := discoverHangarPlugins(context.Background(), pluginServerContext{ServerType: "purpur", ServerSoftware: "Purpur", Supported: true, MinecraftVersion: "1.21.8"}, pluginDiscoverQuery{Limit: 1})
			if err != nil || len(out.Projects) != 1 {
				t.Fatal(err, out)
			}
			p := out.Projects[0]
			if p.Source != "Hangar" || p.IconURL != metadata.AvatarURL || p.InstallAvailable != (kind == "direct") || p.Version.Type != "Release" {
				t.Fatal(p)
			}
		})
	}
}
func TestHangarReusesVerifiedManagedDependency(t *testing.T) {
	f := newHangarInstallFixture(t, "purpur")
	f.add(t, 2, 20, "dependency.jar", "Dependency", "example.Dependency")
	v := f.releases["1"]
	id := int64(2)
	v.PluginDependencies["PAPER"] = []hangarDependency{{Name: "Dependency", Required: true, ProjectID: &id}}
	f.releases["1"] = v
	dep := f.releases["2"]
	metadata := hangarProject{ID: 2}
	metadata.Namespace.Owner = "Author"
	metadata.Namespace.Slug = "Project2"
	a, err := hangarArtifact(dep, "PAPER", metadata)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha512.Sum512(f.jars["dependency.jar"])
	a.SHA512 = hex.EncodeToString(hash[:])
	a.PluginName = "Dependency"
	a.Main = "example.Dependency"
	entry := pluginManifestArtifact(a)
	if err := os.WriteFile(filepath.Join(f.data, "plugins", "dependency.jar"), f.jars["dependency.jar"], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitPluginManifest(f.data, nil, pluginManifest{"v1", []pluginManifestEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	p := f.hangarPlan(t)
	if len(p.Artifacts) != 2 || p.Artifacts[1].Satisfied != "Managed by Plugins" || p.Artifacts[1].ExistingHash != a.SHA512 {
		t.Fatal("reuse lost", p)
	}
	// Fingerprints bind upstream SHA-256, source and authoritative target, and stay
	// stable when locally calculated SHA-512 is filled in after download.
	changed := p
	changed.Artifacts = slices.Clone(p.Artifacts)
	changed.Artifacts[0].SHA512 = strings.Repeat("a", 128)
	if fingerprintPluginPlan(changed) != p.Fingerprint {
		t.Fatal("local hash invalidated reviewed plan")
	}
	changed.Source = "Modrinth"
	if fingerprintPluginPlan(changed) == p.Fingerprint {
		t.Fatal("provider not fingerprinted")
	}
}
func TestPluginProviderRecoveryEvidence(t *testing.T) {
	f := newHangarInstallFixture(t, "paper")
	f.failVerify = true
	f.failRollbackVerify = true
	j := f.execute(t, f.hangarPlan(t), false)
	if j.State != operationNeedsAttention {
		t.Fatal(j.State, j.Status)
	}
	tx, err := readPluginTransaction(f.s.operations.baseDir, j.OperationID)
	if err != nil || tx.Artifacts[0].Source != "Hangar" {
		t.Fatal(tx, err)
	}
	path := filepath.Join(f.s.operations.baseDir, "plugin-install", j.OperationID, "transaction.json")
	for _, source := range []string{"Other", "hangar", "Hangar"} {
		bad := tx
		bad.Artifacts = slices.Clone(tx.Artifacts)
		bad.Artifacts[0].Source = source
		bad.Artifacts[0].ProjectID = "notNumeric"
		raw, _ := json.Marshal(bad)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readPluginTransaction(f.s.operations.baseDir, j.OperationID); err == nil {
			t.Fatal("malformed provider transaction accepted")
		}
	}
}
func TestPluginRecoveryCrossProviderIDsDoNotCollide(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	m, raw, _ := readPluginManifest(f.data)
	e := m.Artifacts[0]
	e.ProjectID = "1"
	e.VersionID = "10"
	m.Artifacts[0] = e
	if _, err := commitPluginManifest(f.data, raw, m); err != nil {
		t.Fatal(err)
	}
	other := e
	other.Source = "Hangar"
	other.Filename = "hangar.jar"
	other.SHA512 = strings.Repeat("a", 128)
	tx := pluginTransaction{OperationID: "unused", Artifacts: []pluginManifestEntry{other}}
	installed, err := provePluginTransactionFiles(f.data, tx)
	if err != nil || installed {
		t.Fatal("different provider's ownership blocked proven previous state", installed, err)
	}
}

func TestHangarManagedDescriptorRemainsReadOnly(t *testing.T) {
	f := newHangarInstallFixture(t, "purpur")
	f.add(t, 1, 10, "root.jar", "Root", "org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin")
	p := f.hangarPlan(t)
	if err := reviewRootPluginArtifact(context.Background(), p); err == nil || !strings.Contains(err.Error(), "JustVoxel managed") {
		t.Fatal("managed descriptor offered generic install", err)
	}
	if len(f.commands) != 0 || f.backup {
		t.Fatal("read-only review touched Minecraft")
	}
}

func TestHangarAvatarURL(t *testing.T) {
	const cdn = "https://hangarcdn.papermc.io"
	for _, path := range []string{
		"/avatars/project/123.webp?v=4",
		"/avatars/user/123e4567-e89b-12d3-a456-426614174000.webp?v=7",
		"/avatars/default/default.webp?v=0",
	} {
		if _, err := hangarAvatarURL(cdn + path); err != nil {
			t.Fatal("official avatar rejected", path, err)
		}
	}
	for _, raw := range []string{
		cdn + "/avatars/other/123.webp?v=4",
		cdn + "/other/project/123.webp?v=4",
		cdn + "/avatars/user/123e4567-e89b-12d3-a456-42661417400.webp?v=7",
		cdn + "/avatars/user/123E4567-e89b-12d3-a456-426614174000.webp?v=7",
		cdn + "/avatars/user/123e4567-e89b-12d3-a456-426614174000.webp",
		cdn + "/avatars/project/0.webp?v=4",
		cdn + "/avatars/project/-1.webp?v=4",
		cdn + "/avatars/project/abc.webp?v=4",
		cdn + "/avatars/project/01.webp?v=4",
		cdn + "/avatars/project/9223372036854775808.webp?v=4",
		cdn + "/avatars/default/default.webp?v=1",
		cdn + "/avatars/default/default.webp?v=00",
		cdn + "/avatars/default/other.webp?v=0",
		cdn + "/avatars/project/123.webp?v=4&other=1",
		cdn + "/avatars/project/123.webp?v=4&v=5",
		cdn + "/avatars/project/123.webp?v=-1",
		cdn + "/avatars/project/123.webp?v=12345678901234567890",
		cdn + "/avatars/project/123.webp?v=",
		cdn + "/avatars/project/123.webp",
		cdn + "/avatars/project/123.png?v=4",
		cdn + "/avatars/project/123.webp?v=4#fragment",
		cdn + "/avatars/project/123.webp?v=4#",
		cdn + "/avatars/project/%31%32%33.webp?v=4",
		"https://user@hangarcdn.papermc.io/avatars/project/123.webp?v=4",
		"https://hangarcdn.papermc.io:443/avatars/project/123.webp?v=4",
		"https://evil.example/avatars/project/123.webp?v=4",
		"http://hangarcdn.papermc.io/avatars/project/123.webp?v=4",
	} {
		if _, err := hangarAvatarURL(raw); err == nil {
			t.Error("unsafe avatar accepted", raw)
		}
	}
}
