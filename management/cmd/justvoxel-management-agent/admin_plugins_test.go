package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func pluginTestJar(t *testing.T, path string, fields map[string]string) {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, value := range fields {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(entry, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestPluginInventoryReadOnlyAndIsolatedMetadata(t *testing.T) {
	data := pluginTestData(t)
	root := filepath.Join(data, "plugins")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	pluginTestJar(t, filepath.Join(root, "manual.jar"), map[string]string{"paper-plugin.yml": "name: Example\nversion: '1.2.3'\n", "plugin.yml": "name: Other\nversion: 9\n"})
	pluginTestJar(t, filepath.Join(root, "Geyser-Spigot.jar"), map[string]string{"plugin.yml": "name: Geyser-Spigot\nversion: 2.0\n"})
	pluginTestJar(t, filepath.Join(root, "huge.jar"), map[string]string{"plugin.yml": strings.Repeat("x", pluginMetadataLimit+1)})
	if err := os.WriteFile(filepath.Join(root, "broken.jar"), []byte("not a ZIP"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.jar"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "manual.jar"), filepath.Join(root, "linked.jar")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "manual.jar"))
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := readPluginInventory(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 4 {
		t.Fatalf("unexpected inventory: %#v", plugins)
	}
	byFile := map[string]installedPlugin{}
	for _, plugin := range plugins {
		byFile[plugin.Filename] = plugin
	}
	manual := byFile["manual.jar"]
	if manual.Name != "Example" || manual.Version != "1.2.3" || manual.Management != "manual" || manual.UpdateStatus != "Update status unknown - manually managed" {
		t.Fatalf("manual metadata: %#v", manual)
	}
	if byFile["Geyser-Spigot.jar"].Management != "justvoxel" {
		t.Fatal("managed component lost classification")
	}
	if byFile["huge.jar"].Name != "" || byFile["broken.jar"].Name != "" {
		t.Fatal("invalid metadata became trusted")
	}
	after, err := os.ReadFile(filepath.Join(root, "manual.jar"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("inventory changed a JAR")
	}
}
func TestPluginInventoryRefusesSymlinkRoot(t *testing.T) {
	data := pluginTestData(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(data, "plugins")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPluginInventory(data); err == nil {
		t.Fatal("symlink root was followed")
	}
	empty, err := readPluginInventory(t.TempDir())
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing root: %v %#v", err, empty)
	}
}
func TestPluginMetadataDoesNotInterpretYAMLInstructions(t *testing.T) {
	fields := pluginMetadataScalars("name: !exec unsafe\nversion: *alias\n nested: ignored\n")
	if len(fields) != 0 {
		t.Fatalf("unsafe fields: %#v", fields)
	}
	fields = pluginMetadataScalars("name: 'Quoted name'\nversion: \"1.2\"\n")
	if fields["name"] != "Quoted name" || fields["version"] != "1.2" {
		t.Fatalf("scalar fields: %#v", fields)
	}
	fields = pluginMetadataScalars("name: first\nname: second\n")
	if fields["name"] != "" {
		t.Fatal("ambiguous metadata accepted")
	}
}
func TestPluginRoutesAuthorizeBeforeDiscovery(t *testing.T) {
	old, oldClient := runAdminDiscoveryHelper, pluginCatalogClient
	defer func() { runAdminDiscoveryHelper = old; pluginCatalogClient = oldClient }()
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unauthorized Modrinth access")
		return nil, nil
	})}
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) { t.Fatal("unauthorized discovery"); return nil, nil }
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		mux := http.NewServeMux()
		registerAdminPluginRoutes(mux, s)
		for _, path := range []string{"installed", "updates", "discover", "icon?project_id=root"} {
			rr := httptest.NewRecorder()
			route := "/v1/minecraft/instances/jv-a1b2c3/plugins/" + path
			if strings.HasPrefix(path, "icon") {
				route = "/v1/admin/plugins/" + path
			}
			mux.ServeHTTP(rr, pluginTestRequest(http.MethodGet, route, ""))
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s %s: %d", role, path, rr.Code)
			}
		}
	}
}
func TestVanillaPluginsUnsupportedWithoutCatalog(t *testing.T) {
	oldDiscovery, oldRuntime := runAdminDiscoveryHelper, runPluginRuntimeStatus
	defer func() { runAdminDiscoveryHelper = oldDiscovery; runPluginRuntimeStatus = oldRuntime }()
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return []byte(`{"configured":true,"minecraft":{"server_type":"vanilla","data_path":"/does-not-exist"}}`), nil
	}
	runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
		return []byte(`{"minecraft":{"server_type":"vanilla","version":"1.21.8"}}`), nil
	}
	oldClient := pluginCatalogClient
	defer func() { pluginCatalogClient = oldClient }()
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported workspace requested an upstream catalog")
		return nil, errors.New("unexpected catalog request")
	})}
	pluginTestDataFor(t, "vanilla", "1.21.8")
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"supported":false`) || !strings.Contains(rr.Body.String(), "Vanilla does not support plugins") || !strings.Contains(rr.Body.String(), `"minecraft_version":"1.21.8"`) {
		t.Fatalf("vanilla: %d %s", rr.Code, rr.Body.String())
	}
	var catalog pluginCatalog
	if err := json.Unmarshal(rr.Body.Bytes(), &catalog); err != nil || catalog.Modrinth || catalog.Hangar || len(catalog.Projects) != 0 {
		t.Fatalf("unsupported sources: %+v %v", catalog, err)
	}
}

type pluginTestTransport func(*http.Request) (*http.Response, error)

func (f pluginTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestModrinthPluginDefaultFiltersAndReleaseDependencies(t *testing.T) {
	oldDiscovery, oldRuntime, oldClient := runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginCatalogClient
	defer func() {
		runAdminDiscoveryHelper = oldDiscovery
		runPluginRuntimeStatus = oldRuntime
		pluginCatalogClient = oldClient
	}()
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return []byte(`{"configured":true,"minecraft":{"server_type":"purpur","data_path":"/unused"}}`), nil
	}
	runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
		return []byte(`{"minecraft":{"server_type":"purpur","version":"1.21.8"}}`), nil
	}
	calls := 0
	expectedLimit := "8"
	searchIcon := "https://cdn.modrinth.com/data/abc123/icon.png"
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "api.modrinth.com" || !strings.Contains(r.Header.Get("User-Agent"), "JustVoxel") {
			t.Fatalf("unsafe catalog request: %s", r.URL)
		}
		body := ""
		switch r.URL.Path {
		case "/v2/search":
			if r.URL.Query().Get("limit") != expectedLimit {
				t.Fatal("validated catalog limit not forwarded", r.URL)
			}
			facets := r.URL.Query().Get("facets")
			for _, filter := range []string{"versions:1.21.8", "categories:paper", "categories:purpur", "categories:spigot", "categories:bukkit", "all_project_types:plugin"} {
				if !strings.Contains(facets, filter) {
					t.Fatalf("missing facet %s: %s", filter, facets)
				}
			}
			if strings.Contains(facets, "server_side:") || strings.Contains(facets, "project_type:mod") {
				t.Fatal("deprecated server-side facets sent")
			}
			body = `{"hits":[{"project_id":"abc123","title":"Example","description":"Plugin","icon_url":` + strconv.Quote(searchIcon) + `,"versions":["1.21.8"]}],"total_hits":1}`
		case "/v2/project/abc123/version":
			if r.URL.Query().Get("include_changelog") != "false" {
				t.Fatal("unused changelog requested")
			}
			if r.URL.Query().Get("game_versions") != `["1.21.8"]` || !strings.Contains(r.URL.Query().Get("loaders"), "paper") {
				t.Fatal("version filters missing")
			}
			body = `[{"id":"beta","version_number":"2","version_type":"beta","date_published":"2026-02-01","game_versions":["1.21.8"],"loaders":["paper"]},{"id":"release","version_number":"1","version_type":"release","date_published":"2026-01-01","game_versions":["1.21.8"],"loaders":["paper"],"dependencies":[{"project_id":"required1","dependency_type":"required"},{"project_id":"optional1","dependency_type":"optional"},{"project_id":"conflict1","dependency_type":"incompatible"}]}]`
		default:
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	pluginTestDataFor(t, "purpur", "1.21.8")
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover?q=Example", ""))
	var out pluginCatalog
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil || len(out.Projects) != 1 {
		t.Fatalf("catalog: %d %s", rr.Code, rr.Body.String())
	}
	project := out.Projects[0]
	if project.IconURL != searchIcon || project.Version == nil || project.Version.ID != "release" || len(project.Version.Dependencies) != 3 || !project.Compatible || project.Compatibility != "Compatible according to publisher metadata" || calls != 2 {
		t.Fatalf("project: %#v calls=%d", project, calls)
	}
	for _, invalid := range []string{"", "https://evil.example/data/abc123/icon.png", "https://cdn.modrinth.com/data/other/icon.png", strings.Repeat("x", 2049)} {
		searchIcon = invalid
		rr := httptest.NewRecorder()
		s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover", ""))
		var catalog pluginCatalog
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &catalog) != nil || len(catalog.Projects) != 1 || catalog.Projects[0].IconURL != "" || catalog.Projects[0].Version == nil {
			t.Fatalf("invalid icon failed catalog or escaped validation: %d %s", rr.Code, rr.Body.String())
		}
	}

	for _, value := range []string{"1", "12", "13", "24"} {
		expectedLimit = value
		rr := httptest.NewRecorder()
		s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover?q=Example&limit="+value, ""))
		if rr.Code != http.StatusOK {
			t.Fatal("bounded limit rejected", value, rr.Code, rr.Body.String())
		}
	}

}
func TestPluginCatalogQueryRejectsUpstreamAndInvalidFilters(t *testing.T) {
	for _, query := range []string{"url=https://example.com", "host=example.com", "compatible_only=maybe", "release_only=0", "offset=-1", "q=a&q=b", "limit=0", "limit=25", "limit=abc", "limit=1.5", "limit=", "limit=%2B1", "limit=1&limit=12"} {
		values, _ := url.ParseQuery(query)
		if _, err := pluginQuery(values); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	query, err := pluginQuery(url.Values{})
	if err != nil || !query.Compatible || !query.Release || !query.Modrinth || query.Hangar || query.Cursor.ModrinthOffset != 0 || query.Limit != 8 {
		t.Fatal("defaults not enabled")
	}
}

func TestPluginCatalogQueryAcceptsBoundedLimit(t *testing.T) {
	for _, value := range []string{"1", "12", "13", "24"} {
		want, _ := strconv.Atoi(value)
		query, err := pluginQuery(url.Values{"limit": {value}})
		if err != nil || query.Limit != want {
			t.Fatalf("limit %s: %d %v", value, query.Limit, err)
		}
	}
}

func TestPluginInventoryManagedIdentities(t *testing.T) {
	for _, tc := range []struct {
		name, filename, descriptor, management, iconProjectID string
	}{
		{"geyser filename", "geyser-spigot.jar", "name: Example\n", "justvoxel", "wKkoqHrH"},
		{"floodgate filename", "floodgate-spigot.jar", "name: Example\n", "justvoxel", "bWrNNfkb"},
		{"viaversion filename", "viaversion.jar", "name: Example\n", "justvoxel", "P1OZGk5p"},
		{"case insensitive filename", "ViaVersion.jar", "name: Example\n", "justvoxel", "P1OZGk5p"},
		{"geyser main", "renamed-geyser.jar", "name: Example\nmain: org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin\n", "justvoxel", "wKkoqHrH"},
		{"floodgate main", "renamed-floodgate.jar", "name: Example\nmain: 'org.geysermc.floodgate.SpigotPlugin' # identity\n", "justvoxel", "bWrNNfkb"},
		{"viaversion main", "renamed-via.jar", "name: Example\nmain: com.viaversion.viaversion.ViaVersionPlugin\n", "justvoxel", "P1OZGk5p"},
		{"legacy viaversion main", "legacy-via.jar", "name: Example\nmain: us.myles.ViaVersion.ViaVersionPlugin\n", "justvoxel", "P1OZGk5p"},
		{"ordinary manual plugin", "manual.jar", "name: Example\nmain: example.ManualPlugin\n", "manual", ""},
		{"misleading geyser name", "manual.jar", "name: Geyser\nmain: example.ManualPlugin\n", "manual", ""},
		{"misleading geyser spigot name", "manual.jar", "name: Geyser-Spigot\n", "manual", ""},
		{"misleading floodgate name", "manual.jar", "name: Floodgate\n", "manual", ""},
		{"misleading viaversion name", "manual.jar", "name: ViaVersion\n", "manual", ""},
		{"similar filename", "viaversion-custom.jar", "name: ViaVersion\n", "manual", ""},
		{"similar main", "manual.jar", "name: ViaVersion\nmain: com.viaversion.viaversion.ViaVersionPluginFake\n", "manual", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := pluginTestData(t)
			root := filepath.Join(data, "plugins")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			pluginTestJar(t, filepath.Join(root, tc.filename), map[string]string{"plugin.yml": tc.descriptor})
			plugins, err := readPluginInventory(data)
			if err != nil || len(plugins) != 1 {
				t.Fatalf("inventory: %v %#v", err, plugins)
			}
			if plugins[0].IconProjectID != tc.iconProjectID {
				t.Fatalf("display icon: %#v, want %q", plugins[0], tc.iconProjectID)
			}
			if plugins[0].ProjectID != "" || plugins[0].VersionID != "" {
				t.Fatalf("display identity caused adoption: %#v", plugins[0])
			}
			if _, err := os.Stat(filepath.Join(data, ".justvoxel", "plugins.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("inventory created ownership manifest: %v", err)
			}
			wantStatus := "Update status unknown - manually managed"
			if tc.management == "justvoxel" {
				wantStatus = "Managed by JustVoxel's existing server update mechanism"
			}
			if plugins[0].Management != tc.management || plugins[0].UpdateStatus != wantStatus {
				t.Fatalf("ownership: %#v, want %s", plugins[0], tc.management)
			}
		})
	}
}

func TestPaperPluginsSupported(t *testing.T) {
	oldDiscovery, oldRuntime, oldClient := runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginCatalogClient
	defer func() {
		runAdminDiscoveryHelper = oldDiscovery
		runPluginRuntimeStatus = oldRuntime
		pluginCatalogClient = oldClient
	}()
	data := pluginTestData(t)
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return json.Marshal(map[string]any{"configured": true, "minecraft": map[string]string{"server_type": "paper", "data_path": data}})
	}
	runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
		return []byte(`{"minecraft":{"server_type":"paper","version":"1.21.8"}}`), nil
	}
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("local inventory contacted catalog")
		return nil, errors.New("unexpected catalog request")
	})}
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminPluginsInstalled(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/installed", ""))
	var out pluginInventory
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil {
		t.Fatalf("inventory: %d %s", rr.Code, rr.Body.String())
	}
	if !out.Server.Supported || out.Server.ServerType != "paper" || out.Server.ServerSoftware != "Paper" || out.Server.MinecraftVersion != "1.21.8" {
		t.Fatalf("Paper context: %#v", out.Server)
	}
}

func TestModrinthCatalogUnavailablePreservesLocalInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{name: "network failure", err: errors.New("network unavailable")},
		{name: "request timeout", err: context.DeadlineExceeded},
		{name: "upstream failure", status: http.StatusServiceUnavailable, body: "unavailable"},
		{name: "malformed JSON", status: http.StatusOK, body: `{"hits":`},
		{name: "invalid JSON types", status: http.StatusOK, body: `{"hits":"invalid","total_hits":1}`},
		{name: "null response", status: http.StatusOK, body: `null`},
		{name: "missing catalog fields", status: http.StatusOK, body: `{}`},
		{name: "null hits", status: http.StatusOK, body: `{"hits":null,"total_hits":0}`},
		{name: "missing total", status: http.StatusOK, body: `{"hits":[]}`},
		{name: "invalid total", status: http.StatusOK, body: `{"hits":[],"total_hits":-1}`},
		{name: "oversized response", status: http.StatusOK, body: strings.Repeat("x", (2<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldDiscovery, oldRuntime, oldClient := runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginCatalogClient
			defer func() {
				runAdminDiscoveryHelper = oldDiscovery
				runPluginRuntimeStatus = oldRuntime
				pluginCatalogClient = oldClient
			}()
			data := pluginTestData(t)
			root := filepath.Join(data, "plugins")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			pluginTestJar(t, filepath.Join(root, "manual.jar"), map[string]string{"plugin.yml": "name: LocalPlugin\nversion: 1.0\n"})
			runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
				return json.Marshal(map[string]any{"configured": true, "minecraft": map[string]string{"server_type": "paper", "data_path": data}})
			}
			runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
				return []byte(`{"minecraft":{"server_type":"paper","version":"1.21.8"}}`), nil
			}
			calls := 0
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v2/search" {
					t.Fatalf("unexpected catalog request: %s", r.URL)
				}
				if tc.err != nil {
					return nil, tc.err
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			s := surfaceTestServer(t, roleAdministrator)
			mux := http.NewServeMux()
			registerAdminPluginRoutes(mux, s)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover", ""))
			if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "Modrinth catalog is unavailable. Try again later.") {
				t.Fatalf("catalog failure: %d %s", rr.Code, rr.Body.String())
			}
			for _, route := range []string{"installed", "updates"} {
				rr = httptest.NewRecorder()
				mux.ServeHTTP(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/"+route, ""))
				var out pluginInventory
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil || len(out.Plugins) != 1 {
					t.Fatalf("local %s after catalog failure: %d %s", route, rr.Code, rr.Body.String())
				}
				plugin := out.Plugins[0]
				if plugin.Name != "LocalPlugin" || plugin.Version != "1.0" || plugin.Management != "manual" || plugin.UpdateStatus != "Update status unknown - manually managed" {
					t.Fatalf("local plugin changed: %#v", plugin)
				}
			}
			if calls != 1 {
				t.Fatalf("local inventory contacted catalog: %d requests", calls)
			}
		})
	}
}

func pluginIconImage(t *testing.T, kind string) []byte {
	t.Helper()
	var b bytes.Buffer
	switch kind {
	case "image/png":
		if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			t.Fatal(err)
		}
	case "image/jpeg":
		if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
			t.Fatal(err)
		}
	case "image/webp":
		data, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	return b.Bytes()
}

func TestPluginIconAuthoritativeBoundedImages(t *testing.T) {
	const goodURL = "https://cdn.modrinth.com/data/root/icon.png"
	for _, tc := range []struct {
		name, iconURL, contentType                     string
		status, want                                   int
		oversized, mismatch, wrongID, metadataRedirect bool
	}{
		{name: "png", iconURL: goodURL, contentType: "image/png", want: 200},
		{name: "jpeg", iconURL: "https://cdn.modrinth.com/data/root/icon.jpg", contentType: "image/jpeg", want: 200},
		{name: "webp", iconURL: "https://cdn.modrinth.com/data/root/icon_96.webp", contentType: "image/webp", want: 200},
		{name: "absent", want: 404},
		{name: "http", iconURL: "http://cdn.modrinth.com/data/root/icon.png", want: 502},
		{name: "host", iconURL: "https://evil.example/data/root/icon.png", want: 502},
		{name: "host suffix", iconURL: "https://cdn.modrinth.com.evil.example/data/root/icon.png", want: 502},
		{name: "credentials", iconURL: "https://user@cdn.modrinth.com/data/root/icon.png", want: 502},
		{name: "port", iconURL: "https://cdn.modrinth.com:444/data/root/icon.png", want: 502},
		{name: "other project", iconURL: "https://cdn.modrinth.com/data/other/icon.png", want: 502},
		{name: "path", iconURL: "https://cdn.modrinth.com/other/root/icon.png", want: 502},
		{name: "traversal", iconURL: "https://cdn.modrinth.com/data/root/../icon.png", want: 502},
		{name: "encoded path", iconURL: "https://cdn.modrinth.com/data/root/%69con.png", want: 502},
		{name: "query", iconURL: goodURL + "?url=https://evil.example", want: 502},
		{name: "fragment", iconURL: goodURL + "#icon", want: 502},
		{name: "redirect", iconURL: goodURL, contentType: "image/png", status: 302, want: 502},
		{name: "metadata redirect", metadataRedirect: true, want: 502},
		{name: "upstream missing", iconURL: goodURL, status: 404, want: 502},
		{name: "oversized", iconURL: goodURL, contentType: "image/png", oversized: true, want: 502},
		{name: "svg", iconURL: goodURL, contentType: "image/svg+xml", want: 502},
		{name: "html", iconURL: goodURL, contentType: "text/html", want: 502},
		{name: "binary", iconURL: goodURL, contentType: "application/octet-stream", want: 502},
		{name: "type mismatch", iconURL: goodURL, contentType: "image/png", mismatch: true, want: 502},
		{name: "identity mismatch", iconURL: goodURL, wrongID: true, want: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := pluginCatalogClient
			defer func() { pluginCatalogClient = old }()
			client := *old // Retain the production timeout and redirect policy.
			calls := 0
			imageData := pluginIconImage(t, tc.contentType)
			client.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.modrinth.com" || r.URL.Path != "/v2/project/root" || r.URL.RawQuery != "" {
						t.Fatalf("unexpected metadata request: %s", r.URL)
					}
					id := "root"
					if tc.wrongID {
						id = "other"
					}
					data, _ := json.Marshal(map[string]string{"id": id, "icon_url": tc.iconURL})
					status := 200
					if tc.metadataRedirect {
						status = 302
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://evil.example/"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
				}
				if calls != 2 || r.URL.String() != tc.iconURL || r.URL.Host != "cdn.modrinth.com" {
					t.Fatalf("unexpected image request: %s", r.URL)
				}
				data := imageData
				if tc.oversized {
					data = bytes.Repeat([]byte("x"), pluginIconLimit+1)
				}
				if tc.mismatch {
					data = []byte("<html>private upstream body</html>")
				}
				status := tc.status
				if status == 0 {
					status = 200
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {tc.contentType}, "Location": {"https://evil.example/"}}, ContentLength: -1, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})
			pluginCatalogClient = &client
			s := surfaceTestServer(t, roleAdministrator)
			mux := http.NewServeMux()
			registerAdminPluginRoutes(mux, s)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, pluginTestRequest(http.MethodGet, "/v1/admin/plugins/icon?project_id=root", ""))
			if rr.Code != tc.want || rr.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("icon: %d %s", rr.Code, rr.Body.String())
			}
			if tc.want == 200 && (rr.Header().Get("Content-Type") != tc.contentType || !bytes.Equal(rr.Body.Bytes(), imageData)) {
				t.Fatal("verified image changed")
			}
			if tc.want != 200 && (strings.Contains(rr.Body.String(), "private upstream body") || strings.Contains(rr.Body.String(), "https://")) {
				t.Fatal("upstream details exposed")
			}
			if _, err := pluginIconURL(tc.iconURL, "root"); err != nil || tc.wrongID || tc.metadataRedirect {
				if calls != 1 {
					t.Fatal("invalid resolved identity contacted CDN")
				}
			} else if calls != 2 {
				t.Fatal("image was not fetched exactly once")
			}
		})
	}
}

func TestPluginIconRejectsCallerURLAndUnboundedID(t *testing.T) {
	old := pluginCatalogClient
	defer func() { pluginCatalogClient = old }()
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request contacted upstream")
		return nil, nil
	})}
	s := surfaceTestServer(t, roleAdministrator)
	for _, query := range []string{"", "url=https://evil.example", "project_id=root&url=https://evil.example", "project_id=root&project_id=other", "project_id=", "project_id=../root", "project_id=https://evil.example", "project_id=" + strings.Repeat("a", 65), "project_id=root&bad=%xx"} {
		rr := httptest.NewRecorder()
		s.adminPluginIcon(rr, pluginTestRequest(http.MethodGet, "/v1/admin/plugins/icon?"+query, ""))
		if rr.Code != 400 {
			t.Fatalf("unsafe query accepted: %q %d", query, rr.Code)
		}
	}
	if !modrinthPluginID.MatchString(strings.Repeat("a", 64)) || modrinthPluginID.MatchString(strings.Repeat("a", 65)) {
		t.Fatal("project ID bound changed")
	}
}

// Inventory identity is presentation metadata from the verified ownership entry.
func TestPluginInventoryExposesOnlyVerifiedManifestIdentity(t *testing.T) {
	for _, kind := range []string{"verified", "hash mismatch", "name mismatch", "main mismatch", "manual", "unsafe manifest", "justvoxel"} {
		t.Run(kind, func(t *testing.T) {
			data := pluginTestData(t)
			root := filepath.Join(data, "plugins")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			filename := "example.jar"
			if kind == "justvoxel" {
				filename = "geyser-spigot.jar"
			}
			path := filepath.Join(root, filename)
			pluginTestJar(t, path, map[string]string{"plugin.yml": "name: Example\nversion: '1.0'\nmain: example.Plugin\n"})
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha512.Sum512(raw)
			entry := pluginManifestEntry{ProjectID: "project123", VersionID: "release123", VersionNumber: "1.0", Filename: filename, SHA512: hex.EncodeToString(hash[:]), PluginName: "Example", Main: "example.Plugin"}
			switch kind {
			case "hash mismatch":
				entry.SHA512 = strings.Repeat("a", 128)
			case "name mismatch":
				entry.PluginName = "Different"
			case "main mismatch":
				entry.Main = "example.Different"
			}
			if kind != "manual" && kind != "justvoxel" {
				if _, err := commitPluginManifest(data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{entry}}); err != nil {
					t.Fatal(err)
				}
				if kind == "unsafe manifest" {
					if err := os.Chmod(filepath.Join(data, ".justvoxel", "plugins.json"), 0666); err != nil {
						t.Fatal(err)
					}
				}
			}
			items, err := readPluginInventory(data)
			if err != nil || len(items) != 1 {
				t.Fatalf("inventory: %#v, %v", items, err)
			}
			item := items[0]
			wantManagement := "manual"
			if kind == "verified" {
				wantManagement = "plugins"
				if item.ProjectID != entry.ProjectID || item.VersionID != entry.VersionID {
					t.Fatalf("verified identity missing: %#v", item)
				}
			} else if item.ProjectID != "" || item.VersionID != "" {
				t.Fatalf("unverified identity exposed: %#v", item)
			}
			if kind == "justvoxel" {
				wantManagement = "justvoxel"
			}
			if item.Management != wantManagement {
				t.Fatalf("ownership changed: %#v", item)
			}
			wantIcon := ""
			if kind == "justvoxel" {
				wantIcon = "wKkoqHrH"
			}
			if item.IconProjectID != wantIcon {
				t.Fatalf("untrusted display identity: %#v", item)
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(`"icon_project_id":`)) != (kind == "justvoxel") {
				t.Fatalf("optional display identity: %s", encoded)
			}
			for _, field := range []string{`"project_id":`, `"version_id":`} {
				if bytes.Contains(encoded, []byte(field)) != (kind == "verified") {
					t.Fatalf("optional inventory identity: %s", encoded)
				}
			}
			if kind == "hash mismatch" && !strings.Contains(item.UpdateStatus, "JAR differs") {
				t.Fatal("hash attention classification lost")
			}
			if (kind == "name mismatch" || kind == "main mismatch") && !strings.Contains(item.UpdateStatus, "descriptor differs") {
				t.Fatal("descriptor attention classification lost")
			}
		})
	}
}

func TestPluginDiscoverBoundedTopUpRawCursor(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(strconv.FormatBool(exhausted), func(t *testing.T) {
			oldDiscovery, oldRuntime, oldClient := runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginCatalogClient
			t.Cleanup(func() {
				runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginCatalogClient = oldDiscovery, oldRuntime, oldClient
			})
			runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
				return []byte(`{"configured":true,"minecraft":{"server_type":"paper","data_path":"/unused"}}`), nil
			}
			runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
				return []byte(`{"minecraft":{"server_type":"paper","version":"26.2"}}`), nil
			}
			searches, inspected := 0, 0
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				var payload any
				if r.URL.Path == "/v2/search" {
					searches++
					offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
					limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
					hits := []pluginCatalogProject{}
					for i := offset; i < offset+limit; i++ {
						hits = append(hits, pluginCatalogProject{ID: "project" + strconv.Itoa(i), Name: "Example"})
					}
					payload = map[string]any{"hits": hits, "total_hits": 100}
				} else {
					inspected++
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/project/"), "/version")
					versions := []pluginCatalogVersion{}
					if !exhausted && id != "project0" {
						versions = append(versions, pluginCatalogVersion{ID: "release" + id, ProjectID: id, Number: "1", Type: "release", Loaders: []string{"paper"}, GameVersions: []string{"26.2"}})
					}
					payload = versions
				}
				raw, err := json.Marshal(payload)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, err
			})}
			pluginTestDataFor(t, "paper", "26.2")
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover?limit=12", ""))
			var out pluginCatalog
			if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil {
				t.Fatalf("catalog: %d %s", rr.Code, rr.Body.String())
			}
			if out.Total != 100 {
				t.Fatal("authoritative total lost")
			}
			if exhausted {
				if inspected != 96 || searches != 8 || len(out.Projects) != 0 || out.NextCursor == nil || out.NextCursor.ModrinthOffset != 96 {
					t.Fatalf("unbounded scan: searches=%d inspected=%d out=%+v", searches, inspected, out)
				}
			} else {
				if len(out.Projects) != 12 || out.NextCursor == nil || out.NextCursor.ModrinthOffset != 13 || inspected != 13 || searches != 2 {
					t.Fatalf("sparse page or incorrect raw cursor: searches=%d inspected=%d out=%+v", searches, inspected, out)
				}
				// The unused hits in the second fetched batch must be the next page.
				rr = httptest.NewRecorder()
				s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover?limit=12&modrinth_offset=13", ""))
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil || len(out.Projects) != 12 || out.Projects[0].ID != "project13" || out.NextCursor == nil || out.NextCursor.ModrinthOffset != 25 {
					t.Fatal("unconsumed hits skipped or duplicated", rr.Body.String())
				}
			}
		})
	}
}

func TestPluginIconSearchURLSkipsMetadataAndVerifiesBytes(t *testing.T) {
	const iconURL = "https://cdn.modrinth.com/data/root/icon.png"
	old := pluginCatalogClient
	defer func() { pluginCatalogClient = old }()
	for _, tc := range []struct {
		name, kind               string
		oversized, mismatch, xml bool
		advertisedOversize       bool
		status, want             int
	}{
		{name: "valid", kind: "image/png", status: 200, want: 200},
		{name: "advisory MIME", kind: "text/html", status: 200, want: 200},
		{name: "random bytes", kind: "image/png", mismatch: true, status: 200, want: 502},
		{name: "XML disguised as raster", kind: "image/png", xml: true, status: 200, want: 502},
		{name: "oversized Content-Length", kind: "image/png", advertisedOversize: true, status: 200, want: 502},
		{name: "oversized", kind: "image/png", oversized: true, status: 200, want: 502},
		{name: "redirect", kind: "image/png", status: 302, want: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			pluginCatalogClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls != 1 || r.URL.String() != iconURL {
					t.Fatalf("Discover icon must fetch only CDN image: %s", r.URL)
				}
				data := pluginIconImage(t, "image/png")
				if tc.oversized {
					data = bytes.Repeat([]byte("x"), pluginIconLimit+1)
				}
				if tc.mismatch {
					data = []byte("private upstream body")
				}
				if tc.xml {
					data = []byte(`<?xml version="1.0"?><svg/>`)
				}
				length := int64(-1)
				if tc.advertisedOversize {
					length = pluginIconLimit + 1
				}
				return &http.Response{StatusCode: tc.status, ContentLength: length, Header: http.Header{"Content-Type": {tc.kind}, "Location": {"https://evil.example/"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			s.adminPluginIcon(rr, pluginTestRequest(http.MethodGet, "/v1/admin/plugins/icon?"+url.Values{"project_id": {"root"}, "icon_url": {iconURL}}.Encode(), ""))
			if rr.Code != tc.want || calls != 1 {
				t.Fatalf("icon: %d, calls %d", rr.Code, calls)
			}
			if tc.want != 200 && (bytes.Contains(rr.Body.Bytes(), []byte("private upstream body")) || rr.Header().Get("Content-Type") == "image/png") {
				t.Fatal("unverified bytes exposed")
			}
		})
	}
}

func TestPluginIconCDNRepresentations(t *testing.T) {
	oldClient := pluginCatalogClient
	oldSVG, oldRGB := convertPluginSVG, convertPluginRGB
	t.Cleanup(func() {
		pluginCatalogClient = oldClient
		convertPluginSVG, convertPluginRGB = oldSVG, oldRGB
	})
	pngData := pluginIconImage(t, "image/png")
	webpData := pluginIconImage(t, "image/webp")
	jpegData := pluginIconImage(t, "image/jpeg")
	svgData := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`)
	var zipped bytes.Buffer
	zipper := gzip.NewWriter(&zipped)
	if _, err := zipper.Write(svgData); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, ext, header, wantType    string
		data, wantData, converterInput []byte
		conversion                     string
	}{
		{name: "webp with jpeg header", ext: "webp", header: "image/jpeg", data: webpData, wantType: "image/webp", wantData: webpData},
		{name: "png path delivering webp", ext: "png", header: "image/jpeg", data: webpData, wantType: "image/webp", wantData: webpData},
		{name: "png missing header", ext: "png", data: pngData, wantType: "image/png", wantData: pngData},
		{name: "png malformed header", ext: "png", header: "image/png; broken", data: pngData, wantType: "image/png", wantData: pngData},
		{name: "png generic header", ext: "png", header: "application/octet-stream", data: pngData, wantType: "image/png", wantData: pngData},
		{name: "png wrong header", ext: "png", header: "text/html", data: pngData, wantType: "image/png", wantData: pngData},
		{name: "jpeg with webp header", ext: "jpg", header: "image/webp", data: jpegData, wantType: "image/jpeg", wantData: jpegData},
		{name: "svg path delivering png", ext: "svg", data: pngData, wantType: "image/png", wantData: pngData},
		{name: "svg path delivering webp", ext: "svg", header: "image/jpeg", data: webpData, wantType: "image/webp", wantData: webpData},
		{name: "svgz path delivering png", ext: "svgz", header: "application/gzip", data: pngData, wantType: "image/png", wantData: pngData},
		{name: "rgb path delivering webp", ext: "rgb", header: "image/x-rgb", data: webpData, wantType: "image/webp", wantData: webpData},
		{name: "svg source missing header", ext: "svg", data: svgData, wantType: "image/png", wantData: pngData, conversion: "svg", converterInput: svgData},
		{name: "svg source wrong header", ext: "svg", header: "image/jpeg", data: svgData, wantType: "image/png", wantData: pngData, conversion: "svg", converterInput: svgData},
		{name: "svgz source malformed header", ext: "svgz", header: "image/png; broken", data: zipped.Bytes(), wantType: "image/png", wantData: pngData, conversion: "svg", converterInput: svgData},
		{name: "rgb source generic header", ext: "rgb", header: "application/octet-stream", data: pluginTestRGB(), wantType: "image/png", wantData: pngData, conversion: "rgb", converterInput: pluginTestRGB()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conversions := 0
			converter := func(kind string) func(context.Context, []byte, io.Writer) error {
				return func(ctx context.Context, input []byte, output io.Writer) error {
					conversions++
					deadline, ok := ctx.Deadline()
					if tc.conversion != kind || !bytes.Equal(input, tc.converterInput) || !ok || time.Until(deadline) > 3*time.Second {
						t.Fatal("unexpected conversion or unbounded deadline")
					}
					_, err := output.Write(pngData)
					return err
				}
			}
			convertPluginSVG, convertPluginRGB = converter("svg"), converter("rgb")
			iconURL := "https://cdn.modrinth.com/data/root/icon." + tc.ext
			calls := 0
			client := *oldClient
			client.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls != 1 || r.URL.String() != iconURL || r.Method != http.MethodGet {
					t.Fatal("unexpected CDN request", r.URL)
				}
				if _, present := r.Header["Accept"]; present || r.Header.Get("User-Agent") == "" {
					t.Fatal("CDN request must retain User-Agent without negotiating an image format")
				}
				header := make(http.Header)
				if tc.header != "" {
					header.Set("Content-Type", tc.header)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: int64(len(tc.data)), Body: io.NopCloser(bytes.NewReader(tc.data))}, nil
			})
			pluginCatalogClient = &client
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			s.adminPluginIcon(rr, pluginTestRequest(http.MethodGet, "/v1/admin/plugins/icon?"+url.Values{"project_id": {"root"}, "icon_url": {iconURL}}.Encode(), ""))
			if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != tc.wantType || !bytes.Equal(rr.Body.Bytes(), tc.wantData) || rr.Header().Get("X-Content-Type-Options") != "nosniff" || calls != 1 {
				t.Fatalf("CDN representation lost: status=%d type=%s", rr.Code, rr.Header().Get("Content-Type"))
			}
			wantConversions := 0
			if tc.conversion != "" {
				wantConversions = 1
			}
			if conversions != wantConversions {
				t.Fatal("unexpected conversion count", conversions)
			}
		})
	}
}

func TestPluginIconRejectsInvalidSearchURLQuery(t *testing.T) {
	old := pluginCatalogClient
	defer func() { pluginCatalogClient = old }()
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid URL contacted upstream")
		return nil, nil
	})}
	s := surfaceTestServer(t, roleAdministrator)
	const good = "https://cdn.modrinth.com/data/root/icon.png"
	for _, raw := range []string{"", "http://cdn.modrinth.com/data/root/icon.png", "https://evil.example/data/root/icon.png", "https://cdn.modrinth.com/data/other/icon.png", "https://user@cdn.modrinth.com/data/root/icon.png", good + "?x=1", good + "?", good + "#x", good + "#", "https://cdn.modrinth.com/data/root/" + strings.Repeat("a", 2048) + ".png"} {
		rr := httptest.NewRecorder()
		s.adminPluginIcon(rr, pluginTestRequest(http.MethodGet, "/v1/admin/plugins/icon?"+url.Values{"project_id": {"root"}, "icon_url": {raw}}.Encode(), ""))
		if rr.Code != 400 {
			t.Fatalf("unsafe icon URL accepted %q: %d", raw, rr.Code)
		}
	}
	for _, query := range []url.Values{
		{"project_id": {"root"}, "icon_url": {good, good}},
		{"project_id": {"root", "root"}, "icon_url": {good}},
		{"project_id": {"root"}, "icon_url": {good}, "unknown": {"x"}},
	} {
		rr := httptest.NewRecorder()
		s.adminPluginIcon(rr, pluginTestRequest(http.MethodGet, "/v1/admin/plugins/icon?"+query.Encode(), ""))
		if rr.Code != 400 {
			t.Fatalf("unsafe icon query accepted: %d", rr.Code)
		}
	}
}

func TestPluginIconDocumentedExtensions(t *testing.T) {
	for _, ext := range []string{"png", "jpg", "jpeg", "bmp", "gif", "webp", "svg", "svgz", "rgb"} {
		if _, err := pluginIconURL("https://cdn.modrinth.com/data/root/icon."+ext, "root"); err != nil {
			t.Fatal(ext, err)
		}
	}
	for _, ext := range []string{"exe", "PNG", "xml", "pnm"} {
		if _, err := pluginIconURL("https://cdn.modrinth.com/data/root/icon."+ext, "root"); err == nil {
			t.Fatal("accepted", ext)
		}
	}
}

func TestPluginIconRasterFamilies(t *testing.T) {
	for _, tc := range []struct {
		mime string
		data []byte
	}{
		{"image/png", pluginIconImage(t, "image/png")},
		{"image/jpeg", pluginIconImage(t, "image/jpeg")},
		{"image/bmp", []byte("BMfixture")},
		{"image/gif", []byte("GIF89afixture")},
		{"image/webp", pluginIconImage(t, "image/webp")},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			for _, ext := range []string{"png", "jpg", "jpeg", "bmp", "gif", "webp", "svg", "svgz", "rgb"} {
				data, kind, err := verifiedPluginIcon(context.Background(), ext, tc.data)
				if err != nil || kind != tc.mime || !bytes.Equal(data, tc.data) {
					t.Fatal("raster lost for extension", ext, kind, err)
				}
			}
		})
	}
	for _, data := range [][]byte{[]byte("<svg/>"), []byte(`<?xml version="1.0"?><svg/>`), []byte("<html>bad</html>"), {0, 1, 2, 3}} {
		for _, ext := range []string{"png", "jpg", "jpeg", "bmp", "gif", "webp"} {
			if output, _, err := verifiedPluginIcon(context.Background(), ext, data); err == nil || len(output) != 0 {
				t.Fatal("bad raster signature accepted", ext)
			}
		}
	}
}

func pluginTestRGB() []byte {
	data := make([]byte, 515)
	binary.BigEndian.PutUint16(data[:2], 0x01da)
	data[3] = 1
	binary.BigEndian.PutUint16(data[4:6], 3)
	binary.BigEndian.PutUint16(data[6:8], 1)
	binary.BigEndian.PutUint16(data[8:10], 1)
	binary.BigEndian.PutUint16(data[10:12], 3)
	return data
}

func TestPluginIconBoundedConversions(t *testing.T) {
	oldSVG, oldRGB := convertPluginSVG, convertPluginRGB
	t.Cleanup(func() { convertPluginSVG, convertPluginRGB = oldSVG, oldRGB })
	pngData := pluginIconImage(t, "image/png")
	svgData := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`)
	var zipped bytes.Buffer
	zipper := gzip.NewWriter(&zipped)
	if _, err := zipper.Write(svgData); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ext         string
		data, input []byte
	}{
		{"svg", svgData, svgData},
		{"svgz", zipped.Bytes(), svgData},
		{"rgb", pluginTestRGB(), pluginTestRGB()},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			calls := 0
			converter := func(ctx context.Context, input []byte, output io.Writer) error {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second || !bytes.Equal(input, tc.input) {
					t.Fatal("unbounded conversion or wrong input")
				}
				_, err := output.Write(pngData)
				return err
			}
			convertPluginSVG, convertPluginRGB = converter, converter
			data, mime, err := verifiedPluginIcon(context.Background(), tc.ext, tc.data)
			if err != nil || calls != 1 || mime != "image/png" || !bytes.Equal(data, pngData) || bytes.Equal(data, tc.data) {
				t.Fatal("conversion not authoritative", err)
			}
			for _, failure := range []string{"raw SVG", "invalid PNG", "failure", "timeout", "overflow"} {
				t.Run(failure, func(t *testing.T) {
					converter := func(ctx context.Context, _ []byte, output io.Writer) error {
						switch failure {
						case "raw SVG":
							_, err := output.Write(svgData)
							return err
						case "invalid PNG":
							_, err := output.Write([]byte("not a PNG"))
							return err
						case "failure":
							return errors.New("converter failed")
						case "timeout":
							return context.DeadlineExceeded
						case "overflow":
							_, _ = output.Write(bytes.Repeat([]byte("x"), pluginIconLimit+1))
							_, _ = output.Write(pngData)
							return nil
						}
						return nil
					}
					convertPluginSVG, convertPluginRGB = converter, converter
					data, _, err := verifiedPluginIcon(context.Background(), tc.ext, tc.data)
					if err == nil || len(data) != 0 {
						t.Fatal("unsafe conversion exposed")
					}
				})
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			convertPluginSVG, convertPluginRGB = converter, converter
			if _, _, err := verifiedPluginIcon(ctx, tc.ext, tc.data); err == nil {
				t.Fatal("expired conversion accepted")
			}
		})
	}
}

func TestPluginIconMalformedConversionInputs(t *testing.T) {
	oldSVG, oldRGB := convertPluginSVG, convertPluginRGB
	t.Cleanup(func() { convertPluginSVG, convertPluginRGB = oldSVG, oldRGB })
	never := func(context.Context, []byte, io.Writer) error { t.Fatal("invalid input reached converter"); return nil }
	convertPluginSVG, convertPluginRGB = never, never
	var overflow bytes.Buffer
	zip := gzip.NewWriter(&overflow)
	if _, err := zip.Write(bytes.Repeat([]byte("x"), pluginIconLimit+1)); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("<svg/>"), {0x1f, 0x8b}, overflow.Bytes()} {
		if _, _, err := verifiedPluginIcon(context.Background(), "svgz", data); err == nil {
			t.Fatal("invalid gzip accepted")
		}
	}
	for _, mutation := range []string{"magic", "truncated", "zero width", "zero height", "wide", "tall", "channels", "storage", "depth", "dimension", "colormap"} {
		t.Run(mutation, func(t *testing.T) {
			data := pluginTestRGB()
			switch mutation {
			case "magic":
				data[0] = 0
			case "truncated":
				data = data[:511]
			case "zero width":
				binary.BigEndian.PutUint16(data[6:8], 0)
			case "zero height":
				binary.BigEndian.PutUint16(data[8:10], 0)
			case "wide":
				binary.BigEndian.PutUint16(data[6:8], 1025)
			case "tall":
				binary.BigEndian.PutUint16(data[8:10], 1025)
			case "channels":
				binary.BigEndian.PutUint16(data[10:12], 5)
			case "storage":
				data[2] = 2
			case "depth":
				data[3] = 0
			case "dimension":
				binary.BigEndian.PutUint16(data[4:6], 0)
			case "colormap":
				data[107] = 1
			}
			if validPluginRGB(data) {
				t.Fatal("invalid header accepted")
			}
			if _, _, err := verifiedPluginIcon(context.Background(), "rgb", data); err == nil {
				t.Fatal("invalid RGB accepted")
			}
		})
	}
	if !validPluginRGB(pluginTestRGB()) {
		t.Fatal("valid RGB header rejected")
	}
	buffer := &pluginIconBuffer{limit: 4}
	if _, err := buffer.Write([]byte("12345")); err == nil || buffer.Len() > 4 || !buffer.overflow {
		t.Fatal("buffer overflow not bounded")
	}
}

func TestPluginIconCopyCannotBypassBound(t *testing.T) {
	// A reader without WriterTo exercises io.Copy's destination ReadFrom shortcut.
	buffer := &pluginIconBuffer{limit: 8}
	reader := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 32))}
	if _, err := io.Copy(buffer, reader); err == nil || buffer.Len() > 8 || !buffer.overflow {
		t.Fatal("copy bypassed output limit")
	}
}

func TestPluginInventoryPhysicalManagedStateWithoutSchemaChange(t *testing.T) {
	for _, tc := range []string{"enabled", "disabled", "duplicate", "hash mismatch", "descriptor mismatch"} {
		t.Run(tc, func(t *testing.T) {
			state := "enabled"
			if tc != "enabled" {
				state = "disabled"
			}
			f := newPluginManageFixture(t, state)
			switch tc {
			case "duplicate":
				if err := os.WriteFile(filepath.Join(f.data, "plugins", f.entry.Filename), f.jars[f.entry.Filename], 0644); err != nil {
					t.Fatal(err)
				}
			case "hash mismatch":
				if err := os.WriteFile(filepath.Join(f.data, ".justvoxel", "disabled-plugins", f.entry.Filename), []byte("changed JAR"), 0644); err != nil {
					t.Fatal(err)
				}
			case "descriptor mismatch":
				m, raw, err := readPluginManifest(f.data)
				if err != nil {
					t.Fatal(err)
				}
				m.Artifacts[0].Main = "other.Main"
				if _, err = commitPluginManifest(f.data, raw, m); err != nil {
					t.Fatal(err)
				}
			}
			items, err := readPluginInventory(f.data)
			if err != nil || len(items) != 1 {
				t.Fatal(items, err)
			}
			safe := tc == "enabled" || tc == "disabled"
			if safe {
				if items[0].Management != "plugins" || items[0].State != state || items[0].ProjectID != f.entry.ProjectID || items[0].VersionID != f.entry.VersionID {
					t.Fatal("managed identity missing", items)
				}
			} else {
				if items[0].Management == "plugins" || items[0].State != "" || !strings.HasPrefix(items[0].UpdateStatus, "Attention:") {
					t.Fatal("unsafe state was managed", items)
				}
			}
			_, raw, err := readPluginManifest(f.data)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"state"`) || strings.Contains(string(raw), `"enabled"`) || strings.Contains(string(raw), `"disabled"`) {
				t.Fatal("v1 manifest schema changed")
			}
		})
	}
}
func TestPluginInventoryPrivateDisabledStoreDoesNotAdoptFiles(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	disabled, err := openDisabledPluginDirectory(f.data, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := disabled.Stat()
	disabled.Close()
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("disabled store not private", err)
	}
	if err = os.WriteFile(filepath.Join(f.data, ".justvoxel", "disabled-plugins", "unowned.jar"), f.jars[f.entry.Filename], 0644); err != nil {
		t.Fatal(err)
	}
	items, err := readPluginInventory(f.data)
	if err != nil || len(items) != 1 {
		t.Fatal("private unowned JAR adopted", items, err)
	}
	if err = os.Chmod(filepath.Join(f.data, ".justvoxel", "disabled-plugins"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), "disable", f.entry.ProjectID); err == nil {
		t.Fatal("unsafe disabled store accepted")
	}
}

func TestPluginDiscoverSourceCursorContract(t *testing.T) {
	for _, raw := range []string{"modrinth=true&hangar=false", "modrinth=false&hangar=true", "modrinth=true&hangar=true", "modrinth=false&hangar=false", "modrinth_offset=10000&hangar_offset=10000&hangar_first=true&limit=12", "limit=24"} {
		q, _ := url.ParseQuery(raw)
		if _, err := pluginQuery(q); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"modrinth=", "hangar=1", "modrinth=True", "hangar=true&hangar=false", "modrinth=true&modrinth=true", "hangar_first=maybe", "hangar_first=true&hangar_first=false", "modrinth_offset=10001", "hangar_offset=-1", "hangar_offset=", "modrinth_offset=%2B1", "modrinth_offset=1&modrinth_offset=2", "hangar_offset=1.2", "source=hangar", "provider=Hangar", "offset=0", "unknown=true"} {
		q, _ := url.ParseQuery(raw)
		if _, err := pluginQuery(q); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}

func TestHangarPaperCompatibilityAndNativeChannel(t *testing.T) {
	old := pluginCatalogClient
	t.Cleanup(func() { pluginCatalogClient = old })
	id := int64(31)
	version := func(number int64, channel string, flags []string, games map[string][]string) hangarVersion {
		v := hangarVersion{ID: number, ProjectID: id, Name: strconv.FormatInt(number, 10), PlatformDependencies: games}
		v.Channel.Name, v.Channel.Flags = channel, flags
		return v
	}
	paper := func(game string) map[string][]string { return map[string][]string{"PAPER": {game}} }
	tests := []struct {
		name                string
		versions            []hangarVersion
		compatible, release bool
		want                string
		matches             bool
	}{
		{"stable custom channel", []hangarVersion{version(1, "Production", []string{}, paper("1.21.8"))}, true, true, "Production", true},
		{"unstable excluded", []hangarVersion{version(1, "Release", []string{"UNSTABLE"}, paper("1.21.8")), version(2, "Stable", []string{"PINNED"}, paper("1.21.8"))}, true, true, "Stable", true},
		{"native beta browsing", []hangarVersion{version(1, "Nightly", []string{"UNSTABLE"}, paper("1.21.8"))}, true, false, "Nightly", true},
		{"unknown flags", []hangarVersion{version(1, "Release", []string{"NEW_FLAG"}, paper("1.21.8"))}, true, true, "", false},
		{"missing flags", []hangarVersion{version(1, "Release", nil, paper("1.21.8"))}, true, true, "", false},
		{"velocity excluded", []hangarVersion{version(1, "Release", []string{}, map[string][]string{"VELOCITY": {"1.21.8"}})}, false, false, "", false},
		{"other paper excluded", []hangarVersion{version(1, "Release", []string{}, paper("1.20.4"))}, true, true, "", false},
		{"other paper browsed", []hangarVersion{version(1, "Release", []string{}, paper("1.20.4"))}, false, true, "Release", false},
		{"no wildcard inference", []hangarVersion{version(1, "Release", []string{}, paper("1.21.x"))}, true, true, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "hangar.papermc.io" || r.URL.Path != "/api/v1/projects/31/versions" || r.URL.Query().Get("platform") != "PAPER" || r.Header.Get("Authorization") != "" {
					t.Fatal(r.URL)
				}
				if (r.URL.Query().Get("platformVersion") == "1.21.8") != tc.compatible {
					t.Fatal("compatibility query", r.URL)
				}
				body, _ := json.Marshal(map[string]any{"pagination": map[string]int{"count": len(tc.versions), "offset": 0, "limit": 25}, "result": tc.versions})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}
			got, matches, err := selectHangarVersion(context.Background(), id, pluginServerContext{InstanceID: "jv-a1b2c3", ServerType: "paper", MinecraftVersion: "1.21.8"}, tc.compatible, tc.release)
			if err != nil || matches != tc.matches || (tc.want == "") != (got == nil) {
				t.Fatalf("%+v %v %v", got, matches, err)
			}
			if got != nil && (got.Type != tc.want || !slices.Contains(got.Loaders, "paper") || len(got.Files) != 0) {
				t.Fatal(got)
			}
		})
	}
}

func TestPluginDiscoverProviderIsolationAndCompositePagination(t *testing.T) {
	pluginTestDataFor(t, "paper", "1.21.8")
	oldClient := pluginCatalogClient
	t.Cleanup(func() { pluginCatalogClient = oldClient })
	calls := map[string]int{}
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.User != nil || r.Header.Get("Authorization") != "" {
			t.Fatal(r.URL)
		}
		calls[r.URL.Host]++
		var payload any
		switch {
		case r.URL.Host == "api.modrinth.com" && r.URL.Path == "/v2/search":
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			hits := []pluginCatalogProject{}
			for i := offset; i < min(offset+limit, 17); i++ {
				hits = append(hits, pluginCatalogProject{ID: strconv.Itoa(i + 1), Name: "Same name", Slug: "same-slug"})
			}
			payload = map[string]any{"hits": hits, "total_hits": 17}
		case r.URL.Host == "api.modrinth.com" && strings.HasSuffix(r.URL.Path, "/version"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/project/"), "/version")
			versions := []pluginCatalogVersion{}
			if id != "2" {
				versions = append(versions, pluginCatalogVersion{ID: "v" + id, ProjectID: id, Type: "release", Loaders: []string{"paper"}, GameVersions: []string{"1.21.8"}})
			}
			payload = versions
		case r.URL.Host == "hangar.papermc.io" && r.URL.Path == "/api/v1/projects":
			if !slices.Equal(r.URL.Query()["platform"], []string{"PAPER"}) || !slices.Equal(r.URL.Query()["version"], []string{"1.21.8"}) || r.URL.Query().Has("platformVersion") || r.URL.Query().Get("q") != "same" {
				t.Fatal(r.URL)
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			hits := []hangarProject{}
			for i := offset; i < min(offset+limit, 11); i++ {
				hit := hangarProject{ID: int64(i + 1), Name: "Same name"}
				hit.Namespace.Slug = "same-slug"
				hits = append(hits, hit)
			}
			payload = map[string]any{"pagination": map[string]int{"count": 11, "offset": offset, "limit": limit}, "result": hits}
		case r.URL.Host == "hangar.papermc.io" && strings.HasSuffix(r.URL.Path, "/versions"):
			id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/versions"), 10, 64)
			versions := []hangarVersion{}
			if id != 3 {
				v := hangarVersion{ID: id, ProjectID: id, Name: "1", PlatformDependencies: map[string][]string{"PAPER": {"1.21.8"}}}
				v.Channel.Name = "Release"
				v.Channel.Flags = []string{}
				versions = append(versions, v)
			}
			payload = map[string]any{"pagination": map[string]int{"count": len(versions), "offset": 0, "limit": 25}, "result": versions}
		default:
			t.Fatal("unexpected catalog endpoint", r.URL)
		}
		raw, err := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, err
	})}
	s := surfaceTestServer(t, roleAdministrator)
	read := func(q url.Values) pluginCatalog {
		t.Helper()
		rr := httptest.NewRecorder()
		s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover?"+q.Encode(), ""))
		var out pluginCatalog
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &out) != nil {
			t.Fatal(rr.Code, rr.Body.String())
		}
		return out
	}
	for _, tc := range []struct{ m, h bool }{{true, false}, {false, true}, {false, false}, {true, true}} {
		calls = map[string]int{}
		out := read(url.Values{"q": {"same"}, "modrinth": {strconv.FormatBool(tc.m)}, "hangar": {strconv.FormatBool(tc.h)}})
		if (calls["api.modrinth.com"] > 0) != tc.m || (calls["hangar.papermc.io"] > 0) != tc.h || out.Modrinth != tc.m || out.Hangar != tc.h {
			t.Fatal(tc, calls, out)
		}
		for _, p := range out.Projects {
			if p.Source == "Hangar" && (p.InstallAvailable || p.IconURL != "") {
				t.Fatal("Hangar install/icon escaped", p)
			}
		}
	}
	for _, limit := range []int{1, 3, 12, 24} {
		q := url.Values{"q": {"same"}, "modrinth": {"true"}, "hangar": {"true"}, "limit": {strconv.Itoa(limit)}}
		seen := map[string]bool{}
		last := map[string]int{}
		firstSource := ""
		pages := 0
		for {
			out := read(q)
			repeat := read(q)
			if !reflect.DeepEqual(out, repeat) {
				t.Fatal("non-deterministic replay")
			}
			if out.Total != 28 {
				t.Fatal("raw totals", out.Total)
			}
			for _, p := range out.Projects {
				key := p.Source + ":" + p.ID
				n, _ := strconv.Atoi(p.ID)
				if seen[key] || n <= last[p.Source] {
					t.Fatal("duplicate/order", key)
				}
				if firstSource == "" {
					firstSource = p.Source
				} else if limit == 1 && pages == 1 && p.Source == firstSource {
					t.Fatal("capacity 1 starves provider")
				}
				seen[key] = true
				last[p.Source] = n
				if p.Source == "Hangar" && p.InstallAvailable {
					t.Fatal("Hangar installable")
				}
			}
			pages++
			if pages > 40 {
				t.Fatal("pagination does not finish")
			}
			if out.NextCursor == nil {
				break
			}
			c := out.NextCursor
			q.Set("modrinth_offset", strconv.Itoa(c.ModrinthOffset))
			q.Set("hangar_offset", strconv.Itoa(c.HangarOffset))
			q.Set("hangar_first", strconv.FormatBool(c.HangarFirst))
		}
		if len(seen) != 26 {
			t.Fatal("skipped records", limit, len(seen))
		}
	}
	bounded := read(url.Values{"q": {"same"}, "modrinth": {"true"}, "hangar": {"true"}, "modrinth_offset": {"10000"}, "hangar_offset": {"10000"}})
	if bounded.NextCursor != nil || len(bounded.Projects) != 0 || bounded.Total != 28 {
		t.Fatal("terminal cursor lost raw totals", bounded)
	}

}

func TestHangarBoundedHTTPSAndRedirects(t *testing.T) {
	old := pluginCatalogClient
	t.Cleanup(func() { pluginCatalogClient = old })
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"redirect", "{}", 302}, {"too large", strings.Repeat(" ", 2<<20) + "{}", 200}, {"invalid JSON", "{}{}", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := *old
			calls := 0
			client.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" || r.URL.Host != "hangar.papermc.io" || r.URL.Path != "/api/v1/projects" || r.Header.Get("Authorization") != "" {
					t.Fatal(r.URL)
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://evil.example/"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			pluginCatalogClient = &client
			var out any
			if hangarPluginJSON(context.Background(), "/projects", url.Values{"limit": {"1"}}, &out) == nil || calls != 1 {
				t.Fatal("unsafe response accepted or followed", calls)
			}
		})
	}
	if old.Timeout <= 0 || old.Timeout > 6*time.Second || old.CheckRedirect == nil {
		t.Fatal("unbounded catalog client")
	}
}

func TestHangarVersionInspectionBoundedAndValidation(t *testing.T) {
	old := pluginCatalogClient
	t.Cleanup(func() { pluginCatalogClient = old })
	calls := 0
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset != calls*25 {
			t.Fatal("skipped version cursor", r.URL)
		}
		calls++
		versions := []hangarVersion{}
		for i := 0; i < 25; i++ {
			v := hangarVersion{ID: int64(offset + i + 1), ProjectID: 1, Name: "beta", PlatformDependencies: map[string][]string{"PAPER": {"1.21.8"}}}
			v.Channel.Name = "Beta"
			v.Channel.Flags = []string{"UNSTABLE"}
			versions = append(versions, v)
		}
		body, _ := json.Marshal(map[string]any{"pagination": map[string]int{"count": 1000, "offset": offset, "limit": 25}, "result": versions})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	v, _, err := selectHangarVersion(context.Background(), 1, pluginServerContext{InstanceID: "jv-a1b2c3", ServerType: "paper", MinecraftVersion: "1.21.8"}, true, true)
	if v != nil || err != nil || calls != 3 {
		t.Fatal("unbounded version scan", v, err, calls)
	}
	for _, body := range []string{`{}`, `{"pagination":{"count":1,"offset":1,"limit":25},"result":[]}`, `{"pagination":{"count":-1,"offset":0,"limit":25},"result":[]}`} {
		pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		if _, _, err := selectHangarVersion(context.Background(), 1, pluginServerContext{InstanceID: "jv-a1b2c3", ServerType: "paper", MinecraftVersion: "1.21.8"}, true, true); err == nil {
			t.Fatal("unvalidated versions", body)
		}
	}
}

func TestHangarDiscoveryFillsPageAndBoundsInspection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		compatible bool
		game       string
		rejectAll  bool
	}{
		{"compatible", true, "1.21.8", false},
		{"unfiltered", false, "1.21.8", false},
		{"unknown version", true, "", true},
		{"bounded rejection", true, "1.21.8", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := pluginCatalogClient
			t.Cleanup(func() { pluginCatalogClient = old })
			const total = 200
			limit := 12
			if tc.rejectAll {
				limit = 24 // At most 192 candidates may be inspected.
			}
			inspected, batches := 0, 0
			avatars := []string{
				"https://hangarcdn.papermc.io/avatars/user/123e4567-e89b-12d3-a456-426614174000.webp?v=7",
				"https://hangarcdn.papermc.io/avatars/default/default.webp?v=0",
			}
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "hangar.papermc.io" {
					t.Fatal("unsafe API request", r.URL)
				}
				query := r.URL.Query()
				if !slices.Equal(query["platform"], []string{"PAPER"}) {
					t.Fatal("platform query", r.URL)
				}
				var payload any
				if r.URL.Path == "/api/v1/projects" {
					batches++
					if tc.compatible && tc.game != "" {
						if !slices.Equal(query["version"], []string{tc.game}) {
							t.Fatal("project version query", r.URL)
						}
					} else if query.Has("version") {
						t.Fatal("unexpected project version query", r.URL)
					}
					if query.Has("platformVersion") {
						t.Fatal("version-list filter sent to project search", r.URL)
					}
					offset, _ := strconv.Atoi(query.Get("offset"))
					batchLimit, _ := strconv.Atoi(query.Get("limit"))
					hits := []hangarProject{}
					for i := offset; i < min(offset+batchLimit, total); i++ {
						hit := hangarProject{ID: int64(i + 1), Name: "Plugin", AvatarURL: avatars[i%2]}
						// Some projects fail metadata validation before version inspection.
						if i >= 10 && i < 36 && i%2 == 0 {
							hit.Name = ""
						}
						hits = append(hits, hit)
					}
					payload = map[string]any{"pagination": map[string]int{"count": total, "offset": offset, "limit": batchLimit}, "result": hits}
				} else if strings.HasSuffix(r.URL.Path, "/versions") {
					inspected++
					if query.Has("version") || (tc.compatible && !slices.Equal(query["platformVersion"], []string{tc.game})) || (!tc.compatible && query.Has("platformVersion")) {
						t.Fatal("version-list compatibility query", r.URL)
					}
					id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/versions"), 10, 64)
					v := hangarVersion{ID: id, ProjectID: id, Name: "1", PlatformDependencies: map[string][]string{"PAPER": {"1.21.8"}}}
					v.Channel.Name, v.Channel.Flags = "Release", []string{}
					// Only 10 qualify within the old 36-project budget.
					if tc.rejectAll || (id > 10 && id <= 36) {
						v.Channel.Flags = []string{"UNSTABLE"}
					}
					payload = map[string]any{"pagination": map[string]int{"count": 1, "offset": 0, "limit": 25}, "result": []hangarVersion{v}}
				} else {
					t.Fatal("unexpected endpoint", r.URL)
				}
				raw, err := json.Marshal(payload)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, err
			})}
			server := pluginServerContext{InstanceID: "jv-a1b2c3", ServerType: "purpur", MinecraftVersion: tc.game}
			q := pluginDiscoverQuery{Limit: limit, Compatible: tc.compatible, Release: true}
			first, err := discoverHangarPlugins(context.Background(), server, q)
			if err != nil {
				t.Fatal(err)
			}
			if tc.rejectAll {
				if len(first.Projects) != 0 || first.End != 8*limit || first.NextOffset != 8*limit || batches != 8 || inspected > 8*limit {
					t.Fatalf("inspection bound: page=%+v batches=%d inspected=%d", first, batches, inspected)
				}
				return
			}
			if len(first.Projects) != limit || first.NextOffset != 38 || first.End != 38 || batches != 4 {
				t.Fatalf("short page or incorrect cursor: %+v batches=%d", first, batches)
			}
			want := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "37", "38"}
			seen := map[string]bool{}
			for i, p := range first.Projects {
				if p.ID != want[i] || p.IconURL != avatars[(first.Positions[i]-1)%2] {
					t.Fatal("qualifying project/avatar lost", p, first.Positions)
				}
				seen[p.ID] = true
			}
			q.Cursor.HangarOffset = first.NextOffset
			second, err := discoverHangarPlugins(context.Background(), server, q)
			if err != nil || len(second.Projects) != limit || second.NextOffset != 50 {
				t.Fatal("next page", second, err)
			}
			for i, p := range second.Projects {
				if seen[p.ID] || p.ID != strconv.Itoa(39+i) {
					t.Fatal("duplicate or skipped eligible project", p)
				}
			}
			q.Cursor.HangarOffset = 0
			replay, err := discoverHangarPlugins(context.Background(), server, q)
			if err != nil || !reflect.DeepEqual(first, replay) {
				t.Fatal("previous cursor replay changed", replay, err)
			}
		})
	}
}

func TestModrinthDiscoveryExactFacets(t *testing.T) {
	for _, tc := range []struct {
		name       string
		serverType string
		compatible bool
		loaders    []string
	}{
		{"Paper compatible", "paper", true, []string{"categories:paper", "categories:spigot", "categories:bukkit"}},
		{"Purpur compatible", "purpur", true, []string{"categories:purpur", "categories:paper", "categories:spigot", "categories:bukkit"}},
		{"Paper broad", "paper", false, []string{"categories:paper", "categories:purpur", "categories:spigot", "categories:bukkit"}},
		{"Purpur broad", "purpur", false, []string{"categories:paper", "categories:purpur", "categories:spigot", "categories:bukkit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := pluginCatalogClient
			t.Cleanup(func() { pluginCatalogClient = old })
			calls := 0
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.modrinth.com" || r.URL.Path != "/v2/search" {
					t.Fatal("API must remain fixed HTTPS v2", r.URL)
				}
				want := url.Values{"query": {"Example"}, "limit": {"12"}, "offset": {"0"}}
				facets := [][]string{{"all_project_types:plugin"}, tc.loaders}
				if tc.compatible {
					facets = append(facets, []string{"versions:1.21.8"})
				}
				encoded, _ := json.Marshal(facets)
				want.Set("facets", string(encoded))
				if !reflect.DeepEqual(r.URL.Query(), want) {
					t.Fatalf("unexpected query: got %v want %v", r.URL.Query(), want)
				}
				for key := range r.Header {
					lower := strings.ToLower(key)
					if lower == "authorization" || strings.Contains(lower, "api-key") || strings.Contains(lower, "apikey") {
						t.Fatal("unexpected authentication", key)
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hits":[],"total_hits":0}`))}, nil
			})}
			_, err := discoverModrinthPlugins(context.Background(), pluginServerContext{InstanceID: "jv-a1b2c3", ServerType: tc.serverType, MinecraftVersion: "1.21.8"}, "Example", tc.compatible, true, 0, 12)
			if err != nil || calls != 1 {
				t.Fatal("discovery", calls, err)
			}
		})
	}
}

func TestModrinthDiscoveryReleasePageFillAndLargeTotal(t *testing.T) {
	for _, rejectAll := range []bool{false, true} {
		t.Run(strconv.FormatBool(rejectAll), func(t *testing.T) {
			old := pluginCatalogClient
			t.Cleanup(func() { pluginCatalogClient = old })
			const total = 5200 // Synthetic upstream candidate total.
			limit := 12
			if rejectAll {
				limit = 24 // At most 192 candidates may be inspected.
			}
			inspected, searches := 0, 0
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				var payload any
				switch {
				case r.URL.Path == "/v2/search":
					searches++
					offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
					batchLimit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
					hits := []pluginCatalogProject{}
					for i := offset; i < min(offset+batchLimit, total); i++ {
						hits = append(hits, pluginCatalogProject{ID: "project" + strconv.Itoa(i+1), Name: "Example"})
					}
					payload = map[string]any{"hits": hits, "total_hits": total}
				case strings.HasPrefix(r.URL.Path, "/v2/project/") && strings.HasSuffix(r.URL.Path, "/version"):
					inspected++
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/project/"), "/version")
					n, _ := strconv.Atoi(strings.TrimPrefix(id, "project"))
					v := pluginCatalogVersion{ID: "release" + id, ProjectID: id, Type: "release", Loaders: []string{"paper"}, GameVersions: []string{"1.21.8"}}
					// Only ten releases occur within the old 3*limit window.
					if rejectAll || (n > 10 && n <= 36) {
						v.Type = "beta"
					}
					payload = []pluginCatalogVersion{v}
				default:
					t.Fatal("unexpected endpoint", r.URL)
				}
				raw, err := json.Marshal(payload)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw))}, err
			})}
			server := pluginServerContext{InstanceID: "jv-a1b2c3", ServerType: "paper", MinecraftVersion: "1.21.8", Supported: true}
			read := func(offset int) pluginCatalogPage {
				t.Helper()
				page, err := discoverModrinthPlugins(context.Background(), server, "", true, true, offset, limit)
				if err != nil || page.Total != total {
					t.Fatalf("raw provider total lost: %+v err=%v", page, err)
				}
				return page
			}
			first := read(0)
			if rejectAll {
				if len(first.Projects) != 0 || first.End != 8*limit || first.NextOffset != 8*limit || inspected != 8*limit || searches != 8 {
					t.Fatalf("inspection bound: page=%+v inspected=%d searches=%d", first, inspected, searches)
				}
				return
			}
			if len(first.Projects) != limit || first.End != 38 || first.NextOffset != 38 || inspected != 38 || searches != 4 {
				t.Fatalf("page fill/cursor: %+v inspected=%d searches=%d", first, inspected, searches)
			}
			wantPositions := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 37, 38}
			if !slices.Equal(first.Positions, wantPositions) {
				t.Fatal("rejected candidates did not advance cursor", first.Positions)
			}
			for i, p := range first.Projects {
				if p.ID != "project"+strconv.Itoa(wantPositions[i]) || p.Version == nil || p.Version.Type != "release" {
					t.Fatal("incorrect release selection", p)
				}
				if p.InstallAvailable {
					t.Fatal("discoverable project without a direct artifact became installable", p)
				}
			}
			// Continue through several local pages, including beyond the old scan budget.
			offset := first.NextOffset
			for pageIndex := 0; pageIndex < 5; pageIndex++ {
				page := read(offset)
				if len(page.Projects) != limit || page.End != offset+limit || page.NextOffset != offset+limit {
					t.Fatal("pagination stopped early", page)
				}
				for i, p := range page.Projects {
					if p.ID != "project"+strconv.Itoa(offset+i+1) || page.Positions[i] != offset+i+1 {
						t.Fatal("duplicate or skipped eligible project", p, page.Positions)
					}
				}
				offset = page.NextOffset
			}
			if replay := read(0); !reflect.DeepEqual(first, replay) {
				t.Fatal("original cursor replay changed", replay)
			}
		})
	}
}

func TestPluginDiscoverInstalledOwnership(t *testing.T) {
	for _, source := range []string{"", "Modrinth", "Hangar"} {
		for _, state := range []string{"enabled", "disabled", "attention", "manual"} {
			t.Run(source+"/"+state, func(t *testing.T) {
				data := pluginTestData(t)
				root := filepath.Join(data, "plugins")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "example.jar")
				pluginTestJar(t, path, map[string]string{"plugin.yml": "name: Example\nversion: '1.0'\nmain: example.Plugin\n"})
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha512.Sum512(raw)
				entry := pluginManifestEntry{Source: source, ProjectID: "123", VersionID: "456", VersionNumber: "1.0", Filename: "example.jar", SHA512: hex.EncodeToString(hash[:]), PluginName: "Example", Main: "example.Plugin"}
				if state != "manual" {
					if _, err := commitPluginManifest(data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{entry}}); err != nil {
						t.Fatal(err)
					}
				}
				if state == "disabled" {
					disabled, err := openDisabledPluginDirectory(data, true)
					if err != nil {
						t.Fatal(err)
					}
					disabled.Close()
					if err := os.Rename(path, filepath.Join(data, ".justvoxel", "disabled-plugins", "example.jar")); err != nil {
						t.Fatal(err)
					}
				}
				if state == "attention" {
					if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				projects := []pluginCatalogProject{{Source: "Modrinth", ID: "123", Name: "Different catalog title", InstallAvailable: true}, {Source: "Hangar", ID: "123", Name: "Example", InstallAvailable: true}, {Source: pluginSource(source), ID: "789", Name: "Example", InstallAvailable: true}, {Source: "Modrinth", ID: "special", Slug: "geyser", InstallAvailable: false}}
				before, _ := os.ReadFile(filepath.Join(data, ".justvoxel", "plugins.json"))
				if err := annotateInstalledPluginProjects(data, projects); err != nil {
					t.Fatal(err)
				}
				for _, p := range projects {
					want := state != "manual" && p.ID == "123" && p.Source == pluginSource(source)
					if p.Installed != want {
						t.Fatalf("provider isolation: %#v", p)
					}
					if want && (p.InstallAvailable || p.InstalledState != state) {
						t.Fatalf("owned state: %#v", p)
					}
					if !want && p.InstalledState != "" {
						t.Fatalf("guessed ownership: %#v", p)
					}
					if p.ID != "special" && !want && !p.InstallAvailable {
						t.Fatalf("unowned install suppressed: %#v", p)
					}
				}
				after, _ := os.ReadFile(filepath.Join(data, ".justvoxel", "plugins.json"))
				if !bytes.Equal(before, after) {
					t.Fatal("discovery mutated v1 manifest")
				}
			})
		}
	}
}

func TestPluginDiscoverUnsafeOwnershipFailsClosed(t *testing.T) {
	data := pluginTestData(t)
	if err := os.Mkdir(filepath.Join(data, ".justvoxel"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, ".justvoxel", "plugins.json"), []byte(`{"schema_version":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := annotateInstalledPluginProjects(data, []pluginCatalogProject{{Source: "Modrinth", ID: "123", InstallAvailable: true}}); err == nil {
		t.Fatal("unsafe ownership accepted")
	}
}

func TestPluginDiscoverResponseIncludesAuthoritativeOwnership(t *testing.T) {
	data := pluginTestData(t)
	entry := pluginManifestEntry{ProjectID: "123", VersionID: "456", VersionNumber: "old-release", Filename: "example.jar", SHA512: strings.Repeat("a", 128), PluginName: "Example", Main: "example.Plugin"}
	if _, err := commitPluginManifest(data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	oldDiscovery, oldRuntime, oldClient := runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginCatalogClient
	t.Cleanup(func() {
		runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginCatalogClient = oldDiscovery, oldRuntime, oldClient
	})
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return json.Marshal(map[string]any{"configured": true, "minecraft": map[string]string{"server_type": "paper", "data_path": data}})
	}
	runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
		return []byte(`{"minecraft":{"server_type":"paper","version":"1.21.8"}}`), nil
	}
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		body := `[{"id":"999","project_id":"123","version_number":"new-release","version_type":"release","loaders":["paper"],"game_versions":["1.21.8"]}]`
		if r.URL.Path == "/v2/search" {
			body = `{"hits":[{"project_id":"123","slug":"example","title":"Example","description":"Plugin","versions":["1.21.8"]}],"total_hits":1}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover", ""))
	var out pluginCatalog
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &out) != nil || len(out.Projects) != 1 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	p := out.Projects[0]
	if !p.Installed || p.InstalledState != "attention" || p.InstallAvailable || p.Version == nil || p.Version.ID != "999" {
		t.Fatalf("different catalog release exposed installation: %#v", p)
	}
}

func TestPluginDiscoverBothSourcesOffNeverRequestsCatalog(t *testing.T) {
	pluginTestData(t)
	oldClient := pluginCatalogClient
	defer func() { pluginCatalogClient = oldClient }()
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("both disabled sources requested an upstream catalog")
		return nil, errors.New("unexpected catalog request")
	})}
	rr := httptest.NewRecorder()
	surfaceTestServer(t, roleAdministrator).adminPluginsDiscover(rr, pluginTestRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/plugins/discover?modrinth=false&hangar=false", ""))
	var catalog pluginCatalog
	if err := json.Unmarshal(rr.Body.Bytes(), &catalog); err != nil || rr.Code != http.StatusOK || !catalog.Server.Supported || catalog.Modrinth || catalog.Hangar || len(catalog.Projects) != 0 {
		t.Fatalf("disabled sources: %d %+v %v", rr.Code, catalog, err)
	}
}
