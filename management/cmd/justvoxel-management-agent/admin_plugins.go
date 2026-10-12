package main

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	pluginMetadataLimit = 64 << 10
	pluginMaxZipEntries = 8192
)

// No redirects, caller-selected origins, credentials, or artifact downloads.
var pluginCatalogClient = &http.Client{
	Timeout:       6 * time.Second,
	Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}
var runPluginRuntimeStatus = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, statusHelper).Output()
}

type pluginServerContext struct {
	Configured       bool   `json:"configured"`
	ServerType       string `json:"server_type"`
	ServerSoftware   string `json:"server_software"`
	MinecraftVersion string `json:"minecraft_version"`
	Supported        bool   `json:"supported"`
	Message          string `json:"message,omitempty"`
}
type installedPlugin struct {
	Name           string `json:"name,omitempty"`
	Version        string `json:"version,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	IconProjectID  string `json:"icon_project_id,omitempty"` // Display only; never generic ownership evidence.
	VersionID      string `json:"version_id,omitempty"`
	Filename       string `json:"filename"`
	Management     string `json:"management"`
	UpdateStatus   string `json:"update_status"`
	MetadataStatus string `json:"metadata_status"`
	Main           string `json:"-"`
}
type pluginInventory struct {
	Server  pluginServerContext `json:"server"`
	Plugins []installedPlugin   `json:"plugins"`
}
type pluginDependency struct {
	ProjectID string `json:"project_id,omitempty"`
	VersionID string `json:"version_id,omitempty"`
	Filename  string `json:"file_name,omitempty"`
	Type      string `json:"dependency_type"`
}
type pluginCatalogVersion struct {
	ID           string               `json:"id"`
	ProjectID    string               `json:"project_id"`
	Files        []pluginArtifactFile `json:"files,omitempty"`
	Number       string               `json:"version_number"`
	Type         string               `json:"version_type"`
	Published    string               `json:"date_published"`
	GameVersions []string             `json:"game_versions"`
	Loaders      []string             `json:"loaders"`
	Dependencies []pluginDependency   `json:"dependencies"`
}
type pluginCatalogProject struct {
	IconURL          string                `json:"icon_url,omitempty"`
	ID               string                `json:"project_id"`
	Slug             string                `json:"slug"`
	InstallAvailable bool                  `json:"install_available"`
	Name             string                `json:"title"`
	Description      string                `json:"description"`
	Source           string                `json:"source"`
	Versions         []string              `json:"versions"`
	Version          *pluginCatalogVersion `json:"version,omitempty"`
	Compatible       bool                  `json:"compatible"`
	Compatibility    string                `json:"compatibility"`
	MetadataError    string                `json:"metadata_error,omitempty"`
}
type pluginCatalog struct {
	Server     pluginServerContext    `json:"server"`
	Source     string                 `json:"source"`
	Projects   []pluginCatalogProject `json:"projects"`
	Offset     int                    `json:"offset"`
	NextOffset int                    `json:"next_offset"`
	Total      int                    `json:"total"`
}

func registerAdminPluginRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/plugins/installed", s.adminPluginsInstalled)
	mux.HandleFunc("GET /v1/admin/plugins/updates", s.adminPluginsInstalled)
	mux.HandleFunc("GET /v1/admin/plugins/discover", s.adminPluginsDiscover)
	mux.HandleFunc("GET /v1/admin/plugins/icon", s.adminPluginIcon)
	mux.HandleFunc("POST /v1/admin/plugins/install/plan", s.adminPluginInstallPlan)
	mux.HandleFunc("POST /v1/admin/plugins/install", s.adminPluginInstall)
	mux.HandleFunc("POST /v1/admin/plugins/install/acknowledge", s.adminPluginInstallAcknowledge)
	mux.HandleFunc("GET /v1/admin/plugins/install-operation", s.adminPluginInstallCurrent)
}

// Reuse authoritative discovery and runtime status; never parse appliance config here.
func (s *server) pluginContext(w http.ResponseWriter, r *http.Request) (pluginServerContext, string, bool) {
	var configuration adminConfigurationDiscovery
	if !s.collectAdminDiscovery(w, r, "configuration", &configuration) {
		return pluginServerContext{}, "", false
	}
	mc := configuration.Minecraft
	out := pluginServerContext{Configured: configuration.Configured, ServerType: mc.ServerType, Supported: configuration.Configured && (mc.ServerType == "paper" || mc.ServerType == "purpur")}
	switch mc.ServerType {
	case "paper":
		out.ServerSoftware = "Paper"
	case "purpur":
		out.ServerSoftware = "Purpur"
	case "vanilla":
		out.ServerSoftware = "Vanilla"
	}
	if !out.Supported {
		out.Message = "Plugins are available for Paper and Purpur. Vanilla does not support plugins."
		if !out.Configured {
			out.Message = "Set up a Minecraft server before browsing plugins."
		}
		if !out.Configured {
			return out, mc.DataPath, true
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	data, err := runPluginRuntimeStatus(ctx)
	var runtime struct {
		Minecraft struct {
			Version    string `json:"version"`
			ServerType string `json:"server_type"`
		} `json:"minecraft"`
	}
	if err != nil || json.Unmarshal(data, &runtime) != nil {
		if out.Message == "" {
			out.Message = "Current Minecraft version is unavailable."
		}
	} else if runtime.Minecraft.ServerType != out.ServerType {
		out.Message = "Server configuration changed. Refresh Plugins."
	} else if versionInputPattern.MatchString(runtime.Minecraft.Version) && len(runtime.Minecraft.Version) <= 64 && runtime.Minecraft.Version != "Unavailable" && runtime.Minecraft.Version != "LATEST" {
		out.MinecraftVersion = runtime.Minecraft.Version
	} else {
		if out.Message == "" {
			out.Message = "Current Minecraft version is unavailable."
		}
	}
	return out, mc.DataPath, true
}

func (s *server) adminPluginsInstalled(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	server, path, ok := s.pluginContext(w, r)
	if !ok {
		return
	}
	out := pluginInventory{Server: server, Plugins: []installedPlugin{}}
	if server.Supported {
		var err error
		out.Plugins, err = readPluginInventory(path)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Plugin inventory is unavailable: plugins must be a readable directory, not a symbolic link.")
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func readPluginInventory(dataPath string) ([]installedPlugin, error) {
	out := []installedPlugin{}
	if !filepath.IsAbs(dataPath) {
		return nil, errors.New("invalid data path")
	}
	// Keep directory identity pinned while inspecting entries. O_NOFOLLOW also
	// protects against replacing the root or a JAR with a symlink between checks.
	fd, err := unix.Open(filepath.Join(dataPath, "plugins"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		manifest, _, manifestErr := readPluginManifest(dataPath)
		if manifestErr == nil {
			for _, entry := range manifest.Artifacts {
				out = append(out, installedPlugin{Name: entry.PluginName, Filename: entry.Filename, Management: "manual", MetadataStatus: "Managed JAR missing or unavailable", UpdateStatus: "Attention: plugin directory is missing; ownership is unverified"})
			}
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	root := os.NewFile(uintptr(fd), "plugins")
	defer root.Close()
	entries, err := root.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".jar") || !entry.Type().IsRegular() {
			continue
		}
		jarFD, err := unix.Openat(fd, entry.Name(), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOENT) {
				continue
			}
			item := installedPlugin{Filename: entry.Name(), Management: "manual", UpdateStatus: "Update status unknown - manually managed", MetadataStatus: "JAR could not be read"}
			classifyPluginManagement(&item, "")
			out = append(out, item)
			continue
		}
		file := os.NewFile(uintptr(jarFD), entry.Name())
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			continue
		}
		item := inspectPluginJar(file, info.Size(), entry.Name())
		classifyGenericPluginInventory(dataPath, file, &item)
		file.Close()
		out = append(out, item)
	}
	// Missing manifest files are attention evidence, never trusted ownership.
	manifest, _, manifestErr := readPluginManifest(dataPath)
	if manifestErr == nil {
		for _, managed := range manifest.Artifacts {
			present := false
			for _, item := range out {
				if item.Filename == managed.Filename {
					present = true
					break
				}
			}
			if !present {
				out = append(out, installedPlugin{Name: managed.PluginName, Filename: managed.Filename, Management: "manual", MetadataStatus: "Managed JAR missing or unavailable", UpdateStatus: "Attention: manifest JAR is missing or unavailable; ownership is unverified"})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	}
	return out, nil
}

func inspectPluginJar(file io.ReaderAt, size int64, filename string) installedPlugin {
	item := installedPlugin{Filename: filename, Management: "manual", UpdateStatus: "Update status unknown - manually managed", MetadataStatus: "Metadata unavailable"}
	classifyPluginManagement(&item, "")
	// Bound ZIP central-directory allocation as well as decompression.
	if size <= 0 || size > 128<<20 || !pluginZipDirectoryBounded(file, size) {
		return item
	}
	archive, err := zip.NewReader(file, size)
	if err != nil {
		item.MetadataStatus = "Malformed JAR"
		return item
	}
	for _, wanted := range []string{"paper-plugin.yml", "plugin.yml"} {
		seen := false
		for _, entry := range archive.File {
			if entry.Name == wanted {
				if seen {
					continue
				}
				seen = true
			}
			if entry.Name != wanted || entry.UncompressedSize64 > pluginMetadataLimit {
				continue
			}
			reader, err := entry.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(reader, pluginMetadataLimit+1))
			reader.Close()
			if err != nil || len(data) > pluginMetadataLimit {
				continue
			}
			if wanted == "plugin.yml" {
				if main := managedPluginMain.FindStringSubmatch(string(data)); main != nil {
					classifyPluginManagement(&item, main[1])
				}
			}
			fields := pluginMetadataScalars(string(data))
			if main := managedPluginMain.FindStringSubmatch(string(data)); main != nil {
				item.Main = main[1]
				classifyPluginManagement(&item, item.Main)
			}
			if item.Name == "" {
				item.Name = fields["name"]
			}
			if item.Version == "" {
				item.Version = fields["version"]
			}
		}
	}
	if item.Name != "" || item.Version != "" {
		item.MetadataStatus = "Metadata read"
	}
	classifyPluginManagement(&item, "")
	return item
}

// Match the established plugin.yml identity rules in mjust/libexec/managed-plugin-files.
var managedPluginMain = regexp.MustCompile(`(?m)^[ 	]*main:\s*['"]?([A-Za-z0-9_.]+)['"]?\s*(?:#.*)?$`)

func classifyPluginManagement(item *installedPlugin, main string) {
	owned := false
	switch strings.ToLower(item.Filename) {
	case "geyser-spigot.jar":
		owned = true
		item.IconProjectID = "wKkoqHrH"
	case "floodgate-spigot.jar":
		owned = true
		item.IconProjectID = "bWrNNfkb"
	case "viaversion.jar":
		owned = true
		item.IconProjectID = "P1OZGk5p"
	}
	switch main {
	case "org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin":
		owned = true
		item.IconProjectID = "wKkoqHrH"
	case "org.geysermc.floodgate.SpigotPlugin":
		owned = true
		item.IconProjectID = "bWrNNfkb"
	case "com.viaversion.viaversion.ViaVersionPlugin",
		"us.myles.ViaVersion.ViaVersionPlugin":
		owned = true
		item.IconProjectID = "P1OZGk5p"
	}
	if owned {
		item.Management = "justvoxel"
		item.UpdateStatus = "Managed by JustVoxel's existing server update mechanism"
	}
}

// Inspect the standard ZIP end record before archive/zip allocates entries.
// ZIP64 and unusually large directories remain listed with unknown metadata.
func pluginZipDirectoryBounded(file io.ReaderAt, size int64) bool {
	length := min(size, int64(65557))
	tail := make([]byte, int(length))
	if _, err := file.ReadAt(tail, size-length); err != nil {
		return false
	}
	for i := len(tail) - 22; i >= 0; i-- {
		if string(tail[i:i+4]) != "PK\x05\x06" {
			continue
		}
		if i+22+int(binary.LittleEndian.Uint16(tail[i+20:i+22])) != len(tail) {
			continue
		}
		count := binary.LittleEndian.Uint16(tail[i+10 : i+12])
		centralSize := binary.LittleEndian.Uint32(tail[i+12 : i+16])
		centralOffset := binary.LittleEndian.Uint32(tail[i+16 : i+20])
		// Require a single-disk standard directory ending immediately at EOCD.
		// This also excludes ZIP64 locators and ambiguous directory offsets.
		return binary.LittleEndian.Uint16(tail[i+4:i+6]) == 0 &&
			binary.LittleEndian.Uint16(tail[i+6:i+8]) == 0 &&
			binary.LittleEndian.Uint16(tail[i+8:i+10]) == count &&
			count <= pluginMaxZipEntries && centralSize <= 1<<20 &&
			int64(centralOffset)+int64(centralSize) == size-length+int64(i)
	}
	return false
}

// Extract only simple top-level scalar name/version fields. Do not interpret
// YAML tags, aliases, nesting, or multiline values in untrusted plugin metadata.
func pluginMetadataScalars(data string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || (key != "name" && key != "version") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) > 256 || value == "" {
			continue
		}
		if value[0] == '"' {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				continue
			}
			value = decoded
		} else if value[0] == '\'' {
			if len(value) < 2 || value[len(value)-1] != '\'' {
				continue
			}
			value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		} else {
			if strings.ContainsAny(value, "&*!>|{}[]\"'") {
				continue
			}
			value, _, _ = strings.Cut(value, " #")
			value = strings.TrimSpace(value)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			continue
		}
		if _, exists := out[key]; exists {
			out[key] = ""
		} else {
			out[key] = value
		}
	}
	return out
}

func modrinthPluginJSON(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.modrinth.com/v2"+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "JustVoxel/PluginWorkspace-Cycle1 (https://github.com/home-server-project/justvoxel-base)")
	req.Header.Set("Accept", "application/json")
	response, err := pluginCatalogClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog response %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return errors.New("catalog response too large")
	}
	return json.Unmarshal(data, out)
}

var modrinthPluginID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

func pluginLoaders(serverType string) []string {
	if serverType == "purpur" {
		return []string{"purpur", "paper", "spigot", "bukkit"}
	}
	return []string{"paper", "spigot", "bukkit"}
}
func pluginVersionCompatible(v pluginCatalogVersion, server pluginServerContext) bool {
	if server.MinecraftVersion == "" || !slices.Contains(v.GameVersions, server.MinecraftVersion) {
		return false
	}
	for _, loader := range pluginLoaders(server.ServerType) {
		if slices.Contains(v.Loaders, loader) {
			return true
		}
	}
	return false
}
func pluginQuery(q url.Values) (bool, bool, int, int, error) {
	for key, values := range q {
		if (key != "q" && key != "compatible_only" && key != "release_only" && key != "offset" && key != "limit") || len(values) != 1 {
			return false, false, 0, 0, errors.New("invalid catalog query")
		}
	}
	if len(q.Get("q")) > 200 {
		return false, false, 0, 0, errors.New("search is too long")
	}
	compatible, release := true, true
	for key, target := range map[string]*bool{"compatible_only": &compatible, "release_only": &release} {
		if value := q.Get(key); value != "" {
			if value != "true" && value != "false" {
				return false, false, 0, 0, errors.New("invalid filter")
			}
			*target = value == "true"
		}
	}
	offset := 0
	if value := q.Get("offset"); value != "" {
		var err error
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 || offset > 10000 {
			return false, false, 0, 0, errors.New("invalid offset")
		}
	}
	limit := 8
	if values, present := q["limit"]; present {
		value := values[0]
		if value == "" || strings.Trim(value, "0123456789") != "" {
			return false, false, 0, 0, errors.New("invalid limit")
		}
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 12 {
			return false, false, 0, 0, errors.New("invalid limit")
		}
	}
	return compatible, release, offset, limit, nil
}
func (s *server) adminPluginsDiscover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid catalog query")
		return
	}
	compatible, release, offset, limit, err := pluginQuery(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	server, _, ok := s.pluginContext(w, r)
	if !ok {
		return
	}
	out := pluginCatalog{Server: server, Source: "Modrinth", Projects: []pluginCatalogProject{}, Offset: offset, NextOffset: -1}
	if !server.Supported {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if compatible && server.MinecraftVersion == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	loaders := pluginLoaders(server.ServerType)
	facets := [][]string{{"project_type:mod"}}
	loaderFacets := []string{}
	if compatible {
		for _, loader := range loaders {
			loaderFacets = append(loaderFacets, "categories:"+loader)
		}
		facets = append(facets, loaderFacets, []string{"versions:" + server.MinecraftVersion})
	} else {
		facets = append(facets, []string{"categories:paper", "categories:purpur", "categories:spigot", "categories:bukkit"})
	}
	encoded, _ := json.Marshal(facets)
	cursor, scanned := offset, 0
	for len(out.Projects) < limit && scanned < 3*limit && cursor <= 10000 {
		var search struct {
			Hits  []pluginCatalogProject `json:"hits"`
			Total *int                   `json:"total_hits"`
		}
		batchLimit := min(limit, 3*limit-scanned)
		if err := modrinthPluginJSON(ctx, "/search", url.Values{"query": {r.URL.Query().Get("q")}, "facets": {string(encoded)}, "limit": {strconv.Itoa(batchLimit)}, "offset": {strconv.Itoa(cursor)}}, &search); err != nil {
			writeError(w, http.StatusBadGateway, "Modrinth catalog is unavailable. Try again later.")
			return
		}
		if search.Hits == nil || search.Total == nil || len(search.Hits) > batchLimit || *search.Total < 0 {
			writeError(w, http.StatusBadGateway, "Modrinth catalog is unavailable. Try again later.")
			return
		}
		out.Total = *search.Total
		if len(search.Hits) == 0 {
			break
		}
		for _, project := range search.Hits {
			if len(out.Projects) == limit || cursor >= out.Total || cursor > 10000 {
				break
			}
			cursor++
			scanned++
			if !modrinthPluginID.MatchString(project.ID) {
				continue
			}
			if _, err := pluginIconURL(project.IconURL, project.ID); err != nil {
				project.IconURL = ""
			}
			project.Version = nil
			project.InstallAvailable = false
			project.Compatible = false
			project.MetadataError = ""
			project.Source = "Modrinth"
			project.Compatibility = "Compatibility unknown"
			query := url.Values{"include_changelog": {"false"}}
			if compatible {
				games, _ := json.Marshal([]string{server.MinecraftVersion})
				platforms, _ := json.Marshal(loaders)
				query.Set("game_versions", string(games))
				query.Set("loaders", string(platforms))
			}
			var versions []pluginCatalogVersion
			if err := modrinthPluginJSON(ctx, "/project/"+project.ID+"/version", query, &versions); err != nil {
				project.MetadataError = "Release and dependency metadata unavailable"
				out.Projects = append(out.Projects, project)
				continue
			}
			sort.SliceStable(versions, func(i, j int) bool { return versions[i].Published > versions[j].Published })
			for _, v := range versions {
				if release && v.Type != "release" {
					continue
				}
				// Always restrict selected versions to server plugin platforms, including
				// when browsing beyond the active server's compatibility filter.
				plugin := false
				for _, loader := range v.Loaders {
					if slices.Contains([]string{"paper", "purpur", "spigot", "bukkit"}, loader) {
						plugin = true
					}
				}
				if !plugin {
					continue
				}
				matches := pluginVersionCompatible(v, server)
				if compatible && !matches {
					continue
				}
				_, fileErr := selectPluginArtifact(v.Files)
				project.InstallAvailable = matches && server.Supported && v.Type == "release" && v.ProjectID == project.ID && modrinthPluginID.MatchString(v.ID) && fileErr == nil && managedPluginSlug(project.Slug) == ""
				v.Files = nil // Artifact URLs never leave the privileged agent.
				project.Version = &v
				project.Compatible = matches
				if matches {
					project.Compatibility = "Compatible according to publisher metadata"
				} else if server.MinecraftVersion != "" {
					project.Compatibility = "Not listed as compatible according to publisher metadata"
				}
				break
			}
			// Projects without a qualifying release do not pass the release filter.
			if project.Version == nil {
				continue
			}
			out.Projects = append(out.Projects, project)
		}
		if cursor >= out.Total {
			break
		}
	}
	if scanned > 0 && cursor < out.Total && cursor <= 10000 {
		out.NextOffset = cursor
	}
	writeJSON(w, http.StatusOK, out)
}

const pluginIconLimit = 512 << 10

var pluginIconFilename = regexp.MustCompile(`^[A-Za-z0-9_-]+\.(png|jpg|jpeg|webp)$`)

func pluginIconURL(raw, projectID string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Host != "cdn.modrinth.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Fragment != "" || u.RawPath != "" {
		return nil, errors.New("invalid plugin icon URL")
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "data" || parts[2] != projectID || !pluginIconFilename.MatchString(parts[3]) {
		return nil, errors.New("invalid plugin icon path")
	}
	return u, nil
}

func (s *server) adminPluginIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	projectID := query.Get("project_id")
	if err != nil || len(query) > 2 || len(query["project_id"]) != 1 || (len(query) == 2 && len(query["icon_url"]) != 1) || len(query["icon_url"]) > 1 || !modrinthPluginID.MatchString(projectID) {
		http.Error(w, "Invalid plugin icon request.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	rawIcon, supplied := query["icon_url"]
	var icon *url.URL
	if supplied {
		icon, err = pluginIconURL(rawIcon[0], projectID)
		if err != nil {
			http.Error(w, "Invalid plugin icon request.", http.StatusBadRequest)
			return
		}
	} else {
		var project struct {
			ID      string `json:"id"`
			IconURL string `json:"icon_url"`
		}
		if err := modrinthPluginJSON(ctx, "/project/"+projectID, nil, &project); err != nil || project.ID != projectID {
			http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
			return
		}
		if project.IconURL == "" {
			http.Error(w, "Plugin icon unavailable.", http.StatusNotFound)
			return
		}
		icon, err = pluginIconURL(project.IconURL, projectID)
		if err != nil {
			http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
			return
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, icon.String(), nil)
	if err != nil {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	req.Header.Set("User-Agent", "JustVoxel/PluginWorkspace (https://github.com/home-server-project/justvoxel-base)")
	req.Header.Set("Accept", "image/png, image/jpeg, image/webp")
	response, err := pluginCatalogClient.Do(req)
	if err != nil {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	contentType, _, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || response.ContentLength > pluginIconLimit || typeErr != nil || (contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp") {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, pluginIconLimit+1))
	if err != nil || len(data) > pluginIconLimit || http.DetectContentType(data) != contentType {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}
