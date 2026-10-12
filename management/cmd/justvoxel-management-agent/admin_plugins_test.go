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
	data := t.TempDir()
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
	data := t.TempDir()
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
			mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/"+path, ""))
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
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminPluginsDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"supported":false`) || !strings.Contains(rr.Body.String(), "Vanilla does not support plugins") || !strings.Contains(rr.Body.String(), `"minecraft_version":"1.21.8"`) {
		t.Fatalf("vanilla: %d %s", rr.Code, rr.Body.String())
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
			for _, filter := range []string{"versions:1.21.8", "categories:paper", "categories:purpur", "categories:spigot", "categories:bukkit", "project_type:mod"} {
				if !strings.Contains(facets, filter) {
					t.Fatalf("missing facet %s: %s", filter, facets)
				}
			}
			if strings.Contains(facets, "server_side:") {
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
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminPluginsDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover?q=Example", ""))
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
		s.adminPluginsDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover", ""))
		var catalog pluginCatalog
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &catalog) != nil || len(catalog.Projects) != 1 || catalog.Projects[0].IconURL != "" || catalog.Projects[0].Version == nil {
			t.Fatalf("invalid icon failed catalog or escaped validation: %d %s", rr.Code, rr.Body.String())
		}
	}

	for _, value := range []string{"1", "12"} {
		expectedLimit = value
		rr := httptest.NewRecorder()
		s.adminPluginsDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover?q=Example&limit="+value, ""))
		if rr.Code != http.StatusOK {
			t.Fatal("bounded limit rejected", value, rr.Code, rr.Body.String())
		}
	}

}
func TestPluginCatalogQueryRejectsUpstreamAndInvalidFilters(t *testing.T) {
	for _, query := range []string{"url=https://example.com", "host=example.com", "compatible_only=maybe", "release_only=0", "offset=-1", "q=a&q=b", "limit=0", "limit=13", "limit=abc", "limit=1.5", "limit=", "limit=%2B1", "limit=1&limit=12"} {
		values, _ := url.ParseQuery(query)
		if _, _, _, _, err := pluginQuery(values); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	compatible, release, offset, limit, err := pluginQuery(url.Values{})
	if err != nil || !compatible || !release || offset != 0 || limit != 8 {
		t.Fatal("defaults not enabled")
	}
}

func TestPluginCatalogQueryAcceptsBoundedLimit(t *testing.T) {
	for _, value := range []string{"1", "12"} {
		want, _ := strconv.Atoi(value)
		_, _, _, limit, err := pluginQuery(url.Values{"limit": {value}})
		if err != nil || limit != want {
			t.Fatalf("limit %s: %d %v", value, limit, err)
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
			data := t.TempDir()
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
	data := t.TempDir()
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
	s.adminPluginsInstalled(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/installed", ""))
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
			data := t.TempDir()
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
			mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover", ""))
			if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "Modrinth catalog is unavailable. Try again later.") {
				t.Fatalf("catalog failure: %d %s", rr.Code, rr.Body.String())
			}
			for _, route := range []string{"installed", "updates"} {
				rr = httptest.NewRecorder()
				mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/"+route, ""))
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
			mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/icon?project_id=root", ""))
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
		s.adminPluginIcon(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/icon?"+query, ""))
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
			data := t.TempDir()
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
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			s.adminPluginsDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover?limit=12", ""))
			var out pluginCatalog
			if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil {
				t.Fatalf("catalog: %d %s", rr.Code, rr.Body.String())
			}
			if out.Total != 100 {
				t.Fatal("authoritative total lost")
			}
			if exhausted {
				if inspected != 36 || searches != 3 || len(out.Projects) != 0 || out.NextOffset != 36 {
					t.Fatalf("unbounded scan: searches=%d inspected=%d out=%+v", searches, inspected, out)
				}
			} else {
				if len(out.Projects) != 12 || out.NextOffset != 13 || inspected != 13 || searches != 2 {
					t.Fatalf("sparse page or incorrect raw cursor: searches=%d inspected=%d out=%+v", searches, inspected, out)
				}
				// The unused hits in the second fetched batch must be the next page.
				rr = httptest.NewRecorder()
				s.adminPluginsDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover?limit=12&offset=13", ""))
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil || len(out.Projects) != 12 || out.Projects[0].ID != "project13" || out.NextOffset != 25 {
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
		name, kind          string
		oversized, mismatch bool
		status, want        int
	}{
		{name: "valid", kind: "image/png", status: 200, want: 200},
		{name: "bad MIME", kind: "text/html", status: 200, want: 502},
		{name: "mismatch", kind: "image/png", mismatch: true, status: 200, want: 502},
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
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.kind}, "Location": {"https://evil.example/"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			s.adminPluginIcon(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/icon?"+url.Values{"project_id": {"root"}, "icon_url": {iconURL}}.Encode(), ""))
			if rr.Code != tc.want || calls != 1 {
				t.Fatalf("icon: %d, calls %d", rr.Code, calls)
			}
			if tc.want != 200 && (bytes.Contains(rr.Body.Bytes(), []byte("private upstream body")) || rr.Header().Get("Content-Type") == "image/png") {
				t.Fatal("unverified bytes exposed")
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
		s.adminPluginIcon(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/icon?"+url.Values{"project_id": {"root"}, "icon_url": {raw}}.Encode(), ""))
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
		s.adminPluginIcon(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/icon?"+query.Encode(), ""))
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
		ext, mime string
		data      []byte
	}{
		{"png", "image/png", pluginIconImage(t, "image/png")},
		{"jpg", "image/jpeg", pluginIconImage(t, "image/jpeg")},
		{"jpeg", "image/jpeg", pluginIconImage(t, "image/jpeg")},
		{"bmp", "image/bmp", []byte("BMfixture")},
		{"gif", "image/gif", []byte("GIF89afixture")},
		{"webp", "image/webp", pluginIconImage(t, "image/webp")},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			data, kind, err := verifiedPluginIcon(context.Background(), tc.ext, tc.mime, tc.data)
			if err != nil || kind != tc.mime || !bytes.Equal(data, tc.data) {
				t.Fatal("raster lost", kind, err)
			}
			if _, _, err := verifiedPluginIcon(context.Background(), tc.ext, "text/html", tc.data); err == nil {
				t.Fatal("bad MIME accepted")
			}
			if _, _, err := verifiedPluginIcon(context.Background(), tc.ext, tc.mime, []byte("<svg/>")); err == nil {
				t.Fatal("bad signature accepted")
			}
			other := "png"
			if tc.ext == "png" {
				other = "jpg"
			}
			if _, _, err := verifiedPluginIcon(context.Background(), other, tc.mime, tc.data); err == nil {
				t.Fatal("extension mismatch accepted")
			}
		})
	}
	for _, alias := range []string{"image/x-bmp", "image/x-ms-bmp"} {
		_, kind, err := verifiedPluginIcon(context.Background(), "bmp", alias, []byte("BMfixture"))
		if err != nil || kind != "image/bmp" {
			t.Fatal("BMP alias not canonicalized")
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
		ext, mime   string
		data, input []byte
	}{
		{"svg", "image/svg+xml", svgData, svgData},
		{"svgz", "image/svg+xml", zipped.Bytes(), svgData},
		{"rgb", "image/x-rgb", pluginTestRGB(), pluginTestRGB()},
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
			data, mime, err := verifiedPluginIcon(context.Background(), tc.ext, tc.mime, tc.data)
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
					data, _, err := verifiedPluginIcon(context.Background(), tc.ext, tc.mime, tc.data)
					if err == nil || len(data) != 0 {
						t.Fatal("unsafe conversion exposed")
					}
				})
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			convertPluginSVG, convertPluginRGB = converter, converter
			if _, _, err := verifiedPluginIcon(ctx, tc.ext, tc.mime, tc.data); err == nil {
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
		if _, _, err := verifiedPluginIcon(context.Background(), "svgz", "image/svg+xml", data); err == nil {
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
			if _, _, err := verifiedPluginIcon(context.Background(), "rgb", "image/x-rgb", data); err == nil {
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
