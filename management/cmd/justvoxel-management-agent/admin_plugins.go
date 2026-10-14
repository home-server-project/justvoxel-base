package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Source         string `json:"source,omitempty"`
	State          string `json:"state,omitempty"`
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
	Source    string `json:"source,omitempty"`
	Name      string `json:"name,omitempty"`
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
	Installed        bool                  `json:"installed,omitempty"`
	InstalledState   string                `json:"installed_state,omitempty"`
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
type pluginCatalogCursor struct {
	ModrinthOffset int  `json:"modrinth_offset"`
	HangarOffset   int  `json:"hangar_offset"`
	HangarFirst    bool `json:"hangar_first"`
}
type pluginCatalog struct {
	Server     pluginServerContext    `json:"server"`
	Modrinth   bool                   `json:"modrinth"`
	Hangar     bool                   `json:"hangar"`
	Projects   []pluginCatalogProject `json:"projects"`
	Cursor     pluginCatalogCursor    `json:"cursor"`
	NextCursor *pluginCatalogCursor   `json:"next_cursor"`
	Total      int                    `json:"total"` // Raw provider total, never an eligible-result count.
}

// Per-result raw positions prevent unconsumed top-up hits from being skipped.
type pluginCatalogPage struct {
	Projects   []pluginCatalogProject
	Positions  []int
	Offset     int
	NextOffset int
	Total      int
	End        int
}

func registerAdminPluginRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/plugins/addons", s.adminAddonInventory)
	mux.HandleFunc("POST /v1/admin/plugins/addons/plan", s.adminAddonPlan)
	mux.HandleFunc("POST /v1/admin/plugins/addons/install", s.adminAddonInstall)
	mux.HandleFunc("POST /v1/admin/plugins/addons/recover", s.adminAddonRecover)
	mux.HandleFunc("GET /v1/admin/plugins/installed", s.adminPluginsInstalled)
	mux.HandleFunc("GET /v1/admin/plugins/updates", s.adminPluginsInstalled)
	mux.HandleFunc("GET /v1/admin/plugins/discover", s.adminPluginsDiscover)
	mux.HandleFunc("GET /v1/admin/plugins/icon", s.adminPluginIcon)
	mux.HandleFunc("POST /v1/admin/plugins/install/plan", s.adminPluginInstallPlan)
	mux.HandleFunc("POST /v1/admin/plugins/install", s.adminPluginInstall)
	mux.HandleFunc("POST /v1/admin/plugins/install/acknowledge", s.adminPluginInstallAcknowledge)
	mux.HandleFunc("GET /v1/admin/plugins/install-operation", s.adminPluginInstallCurrent)
	mux.HandleFunc("POST /v1/admin/plugins/manage/plan", s.adminPluginManagePlan)
	mux.HandleFunc("POST /v1/admin/plugins/manage", s.adminPluginManage)
	mux.HandleFunc("POST /v1/admin/plugins/manage/acknowledge", s.adminPluginManageAcknowledge)
	mux.HandleFunc("GET /v1/admin/plugins/operation", s.adminPluginOperation)
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
	if s.operations != nil {
		current, err := s.operations.currentPluginOperation()
		if err != nil || current != nil {
			writeError(w, 409, "Plugin inventory is unavailable until the current Plugin operation reaches a verified final state")
			return
		}
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
				out = append(out, managedPluginInventoryItem(dataPath, entry))
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
				out = append(out, managedPluginInventoryItem(dataPath, managed))
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
	if serverType == "paper" {
		return []string{"paper", "spigot", "bukkit"}
	}
	return nil
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

type pluginDiscoverQuery struct {
	Compatible bool
	Release    bool
	Modrinth   bool
	Hangar     bool
	Cursor     pluginCatalogCursor
	Limit      int
	Search     string
}

func pluginQuery(q url.Values) (pluginDiscoverQuery, error) {
	out := pluginDiscoverQuery{Compatible: true, Release: true, Modrinth: true, Limit: 8, Search: q.Get("q")}
	for key, values := range q {
		if !slices.Contains([]string{"q", "compatible_only", "release_only", "modrinth", "hangar", "modrinth_offset", "hangar_offset", "hangar_first", "limit"}, key) || len(values) != 1 {
			return out, errors.New("invalid catalog query")
		}
	}
	if len(out.Search) > 200 {
		return out, errors.New("search is too long")
	}
	for key, target := range map[string]*bool{"compatible_only": &out.Compatible, "release_only": &out.Release, "modrinth": &out.Modrinth, "hangar": &out.Hangar, "hangar_first": &out.Cursor.HangarFirst} {
		if values, present := q[key]; present {
			if values[0] != "true" && values[0] != "false" {
				return out, errors.New("invalid catalog boolean")
			}
			*target = values[0] == "true"
		}
	}
	for key, target := range map[string]*int{"modrinth_offset": &out.Cursor.ModrinthOffset, "hangar_offset": &out.Cursor.HangarOffset, "limit": &out.Limit} {
		if values, present := q[key]; present {
			value := values[0]
			if value == "" || strings.Trim(value, "0123456789") != "" {
				return out, errors.New("invalid catalog position")
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 10000 || (key == "limit" && (n < 1 || n > 12)) {
				return out, errors.New("invalid catalog position")
			}
			*target = n
		}
	}
	return out, nil
}
func (s *server) adminPluginsDiscover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "invalid catalog query")
		return
	}
	query, err := pluginQuery(values)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	server, dataPath, ok := s.pluginContext(w, r)
	if !ok {
		return
	}
	out := pluginCatalog{Server: server, Modrinth: query.Modrinth, Hangar: query.Hangar, Cursor: query.Cursor, Projects: []pluginCatalogProject{}}
	if !server.Supported || (query.Compatible && server.MinecraftVersion == "") || (!query.Modrinth && !query.Hangar) {
		writeJSON(w, 200, out)
		return
	}
	timeout := 25 * time.Second
	if query.Hangar {
		timeout = 40 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	modrinth := pluginCatalogPage{Offset: query.Cursor.ModrinthOffset, End: query.Cursor.ModrinthOffset, NextOffset: -1}
	hangar := pluginCatalogPage{Offset: query.Cursor.HangarOffset, End: query.Cursor.HangarOffset, NextOffset: -1}
	if query.Modrinth {
		modrinth, err = discoverModrinthPlugins(ctx, server, query.Search, query.Compatible, query.Release, query.Cursor.ModrinthOffset, query.Limit)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
	}
	if query.Hangar {
		hangar, err = discoverHangarPlugins(ctx, server, query)
		if err != nil {
			writeError(w, 502, "Hangar catalog is unavailable. Try again later.")
			return
		}
	}
	out.Projects, out.NextCursor = mergePluginCatalogPages(modrinth, hangar, query.Cursor, query.Limit)
	out.Total = modrinth.Total + hangar.Total
	if err := annotateInstalledPluginProjects(dataPath, out.Projects); err != nil {
		writeError(w, 409, "Plugin ownership is unavailable. Discover cannot safely offer installation.")
		return
	}
	writeJSON(w, 200, out)
}

// Ownership suppresses installation even when the owned JAR needs attention.
// Read the existing v1 manifest without migrating or inferring ownership by name.
func annotateInstalledPluginProjects(dataPath string, projects []pluginCatalogProject) error {
	manifest, _, err := readPluginManifest(dataPath)
	if err != nil {
		return err
	}
	owned := make(map[string]pluginManifestEntry, len(manifest.Artifacts))
	for _, entry := range manifest.Artifacts {
		owned[pluginIdentity(entry.Source, entry.ProjectID)] = entry
	}
	for i := range projects {
		project := &projects[i]
		entry, ok := owned[pluginIdentity(project.Source, project.ID)]
		if !ok {
			continue
		}
		project.Installed = true
		project.InstallAvailable = false
		project.InstalledState = "attention"
		if state, err := resolveManagedPluginLocation(dataPath, entry); err == nil {
			project.InstalledState = state
		}
	}
	return nil
}

// Fetch each provider independently, then consume only the records shown. Each
// provider's order and unused raw positions survive the composite page boundary.
func mergePluginCatalogPages(m, h pluginCatalogPage, cursor pluginCatalogCursor, limit int) ([]pluginCatalogProject, *pluginCatalogCursor) {
	projects := []pluginCatalogProject{}
	mi, hi := 0, 0
	next := cursor
	for len(projects) < limit && (mi < len(m.Projects) || hi < len(h.Projects)) {
		if (next.HangarFirst && hi < len(h.Projects)) || mi == len(m.Projects) {
			projects = append(projects, h.Projects[hi])
			hi++
			next.HangarFirst = false
		} else {
			projects = append(projects, m.Projects[mi])
			mi++
			next.HangarFirst = true
		}
	}
	position := func(page pluginCatalogPage, used int) int {
		if used == len(page.Projects) {
			return page.End
		}
		if used == 0 {
			return page.Offset
		}
		return page.Positions[used-1]
	}
	next.ModrinthOffset = position(m, mi)
	next.HangarOffset = position(h, hi)
	if mi < len(m.Projects) || hi < len(h.Projects) || m.NextOffset >= 0 || h.NextOffset >= 0 {
		return projects, &next
	}
	return projects, nil
}

func discoverModrinthPlugins(ctx context.Context, server pluginServerContext, searchText string, compatible, release bool, offset, limit int) (pluginCatalogPage, error) {
	out := pluginCatalogPage{Projects: []pluginCatalogProject{}, Offset: offset, NextOffset: -1, End: offset}
	loaders := pluginLoaders(server.ServerType)
	facets := [][]string{{"all_project_types:plugin"}}
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
	inspectionBudget := 8 * limit
	cursor, scanned := offset, 0
	for len(out.Projects) < limit && scanned < inspectionBudget && cursor <= 10000 {
		var search struct {
			Hits  []pluginCatalogProject `json:"hits"`
			Total *int                   `json:"total_hits"`
		}
		batchLimit := min(limit, inspectionBudget-scanned)
		if err := modrinthPluginJSON(ctx, "/search", url.Values{"query": {searchText}, "facets": {string(encoded)}, "limit": {strconv.Itoa(batchLimit)}, "offset": {strconv.Itoa(cursor)}}, &search); err != nil {
			return out, errors.New("Modrinth catalog is unavailable. Try again later.")
		}
		if search.Hits == nil || search.Total == nil || len(search.Hits) > batchLimit || *search.Total < 0 {
			return out, errors.New("Modrinth catalog is unavailable. Try again later.")
		}
		out.Total = *search.Total
		if cursor >= 10000 {
			break
		}
		if len(search.Hits) == 0 {
			break
		}
		for _, project := range search.Hits {
			if len(out.Projects) == limit || cursor >= out.Total || cursor >= 10000 {
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
				out.Positions = append(out.Positions, cursor)
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
			out.Positions = append(out.Positions, cursor)
		}
		if cursor >= out.Total {
			break
		}
	}
	if scanned > 0 && cursor < out.Total && cursor < 10000 {
		out.NextOffset = cursor
	}
	out.End = cursor
	return out, nil
}

// Hangar metadata is fetched only from its fixed official API.
func hangarPluginJSON(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://hangar.papermc.io/api/v1"+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "JustVoxel/PluginWorkspace (https://github.com/home-server-project/justvoxel-base)")
	req.Header.Set("Accept", "application/json")
	response, err := pluginCatalogClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Hangar response %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return errors.New("Hangar response too large")
	}
	return json.Unmarshal(data, out)
}

type hangarPagination struct {
	Count  *int `json:"count"`
	Offset *int `json:"offset"`
	Limit  *int `json:"limit"`
}

func (p hangarPagination) valid(offset, limit, received int) bool {
	return p.Count != nil && *p.Count >= 0 && *p.Count <= 1000000000 && p.Offset != nil && *p.Offset == offset && p.Limit != nil && *p.Limit == limit && received <= limit
}

type hangarProject struct {
	AvatarURL   string `json:"avatarUrl"`
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Namespace   struct {
		Owner string `json:"owner"`
		Slug  string `json:"slug"`
	} `json:"namespace"`
}
type hangarVersion struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"projectId"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	Channel   struct {
		Name  string   `json:"name"`
		Flags []string `json:"flags"`
	} `json:"channel"`
	PlatformDependencies map[string][]string           `json:"platformDependencies"`
	PluginDependencies   map[string][]hangarDependency `json:"pluginDependencies"`
	Downloads            map[string]hangarDownload     `json:"downloads"`
}

func hangarVersionStable(v hangarVersion) bool {
	// An absent flags list is not authoritative stability evidence. Hangar has
	// custom channel names; never guess release/beta/alpha from their spelling.
	if v.Channel.Name == "" || v.Channel.Flags == nil {
		return false
	}
	for _, flag := range v.Channel.Flags {
		if !slices.Contains([]string{"FROZEN", "PINNED", "SENDS_NOTIFICATIONS", "HIDE_BY_DEFAULT"}, flag) {
			return false
		}
	}
	return true
}
func selectHangarVersion(ctx context.Context, projectID int64, server pluginServerContext, compatible, release bool, projects ...hangarProject) (*pluginCatalogVersion, bool, error) {
	platform, err := hangarPluginPlatform(server.ServerType)
	if err != nil {
		return nil, false, err
	}
	query := url.Values{"platform": {platform}, "limit": {"25"}}
	if compatible {
		query.Set("platformVersion", server.MinecraftVersion)
	}
	// Bound inspection even when many unstable versions precede a stable one.
	for offset, batches := 0, 0; batches < 3 && offset < 75; batches++ {
		query.Set("offset", strconv.Itoa(offset))
		var versions struct {
			Pagination hangarPagination `json:"pagination"`
			Result     []hangarVersion  `json:"result"`
		}
		if err := hangarPluginJSON(ctx, "/projects/"+strconv.FormatInt(projectID, 10)+"/versions", query, &versions); err != nil {
			return nil, false, err
		}
		if versions.Result == nil || !versions.Pagination.valid(offset, 25, len(versions.Result)) {
			return nil, false, errors.New("invalid Hangar versions")
		}
		for _, v := range versions.Result {
			paper := v.PlatformDependencies[platform]
			if v.ID <= 0 || v.ProjectID != projectID || len(v.Name) == 0 || len(v.Name) > 256 || len(v.Channel.Name) > 128 || len(v.Channel.Flags) > 16 || len(v.CreatedAt) > 64 || len(paper) == 0 || len(paper) > 256 {
				continue
			}
			valid := true
			for _, game := range paper {
				if len(game) == 0 || len(game) > 64 {
					valid = false
				}
			}
			if !valid || (release && !hangarVersionStable(v)) {
				continue
			}
			matches := server.MinecraftVersion != "" && slices.Contains(paper, server.MinecraftVersion)
			if compatible && !matches {
				continue
			}
			// Keep provider-native channel wording and exact PAPER publisher metadata.
			dependencies := []pluginDependency{}
			if len(v.PluginDependencies[platform]) > 128 {
				continue
			}
			for _, dep := range v.PluginDependencies[platform] {
				if len(dep.Name) == 0 || len(dep.Name) > 256 {
					valid = false
					break
				}
				kind := "optional"
				if dep.Required {
					kind = "required"
				}
				dependencies = append(dependencies, pluginDependency{Source: "Hangar", Name: dep.Name, ProjectID: hangarDependencyID(dep), Type: kind})
			}
			if !valid {
				continue
			}
			return &pluginCatalogVersion{ID: strconv.FormatInt(v.ID, 10), ProjectID: strconv.FormatInt(v.ProjectID, 10), Files: hangarCatalogFiles(v, platform, projects), Number: v.Name, Type: v.Channel.Name, Published: v.CreatedAt, GameVersions: paper, Loaders: []string{"paper"}, Dependencies: dependencies}, matches, nil
		}
		if len(versions.Result) == 0 || offset+len(versions.Result) >= *versions.Pagination.Count {
			return nil, false, nil
		}
		offset += len(versions.Result)
	}
	return nil, false, nil
}
func discoverHangarPlugins(ctx context.Context, server pluginServerContext, q pluginDiscoverQuery) (pluginCatalogPage, error) {
	platform, err := hangarPluginPlatform(server.ServerType)
	if err != nil {
		return pluginCatalogPage{}, err
	}
	offset, limit := q.Cursor.HangarOffset, q.Limit
	out := pluginCatalogPage{Projects: []pluginCatalogProject{}, Offset: offset, End: offset, NextOffset: -1}
	// Count upstream projects, including rejected metadata and releases.
	inspectionBudget := 8 * limit
	cursor, scanned := offset, 0
	for len(out.Projects) < limit && scanned < inspectionBudget && cursor <= 10000 {
		batchLimit := min(limit, inspectionBudget-scanned)
		query := url.Values{"q": {q.Search}, "platform": {platform}, "offset": {strconv.Itoa(cursor)}, "limit": {strconv.Itoa(batchLimit)}, "sort": {"downloads"}}
		if q.Compatible && server.MinecraftVersion != "" {
			query.Set("version", server.MinecraftVersion)
		}
		var search struct {
			Pagination hangarPagination `json:"pagination"`
			Result     []hangarProject  `json:"result"`
		}
		if err := hangarPluginJSON(ctx, "/projects", query, &search); err != nil {
			return out, err
		}
		if search.Result == nil || !search.Pagination.valid(cursor, batchLimit, len(search.Result)) {
			return out, errors.New("invalid Hangar projects")
		}
		out.Total = *search.Pagination.Count
		if cursor >= 10000 {
			break
		}
		if len(search.Result) == 0 {
			break
		}
		for _, hit := range search.Result {
			if len(out.Projects) == limit || cursor >= out.Total || cursor >= 10000 {
				break
			}
			cursor++
			scanned++
			if hit.ID <= 0 || len(hit.Name) == 0 || len(hit.Name) > 256 || len(hit.Description) > 4096 || len(hit.Namespace.Slug) > 128 {
				continue
			}
			version, matches, err := selectHangarVersion(ctx, hit.ID, server, q.Compatible, q.Release, hit)
			if err != nil {
				return out, err
			}
			if version == nil {
				continue
			}
			project := pluginCatalogProject{ID: strconv.FormatInt(hit.ID, 10), Slug: hit.Namespace.Slug, Name: hit.Name, Description: hit.Description, Source: "Hangar", Version: version, Compatible: matches, Versions: version.GameVersions, InstallAvailable: matches && len(version.Files) == 1, Compatibility: "Compatibility unknown"}
			if _, err := hangarAvatarURL(hit.AvatarURL); err == nil {
				project.IconURL = hit.AvatarURL
			}
			if matches {
				project.Compatibility = "Compatible according to publisher metadata"
			} else if server.MinecraftVersion != "" {
				project.Compatibility = "Not listed as compatible according to publisher metadata"
			}
			out.Projects = append(out.Projects, project)
			out.Positions = append(out.Positions, cursor)
		}
		if cursor >= out.Total {
			break
		}
	}
	out.End = cursor
	if scanned > 0 && cursor < out.Total && cursor < 10000 {
		out.NextOffset = cursor
	}
	return out, nil
}

const pluginIconLimit = 512 << 10

var pluginIconFilename = regexp.MustCompile(`^[A-Za-z0-9_-]+\.(png|jpg|jpeg|bmp|gif|webp|svg|svgz|rgb)$`)

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
	source := pluginSource(query.Get("source"))
	if err != nil || !validPluginIconQuery(query) {
		http.Error(w, "Invalid plugin icon request.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	rawIcon, supplied := query["icon_url"]
	var icon *url.URL
	if source == "Hangar" {
		var project hangarProject
		if err := hangarPluginJSON(ctx, "/projects/"+projectID, nil, &project); err != nil || strconv.FormatInt(project.ID, 10) != projectID {
			http.Error(w, "Plugin icon unavailable.", 502)
			return
		}
		icon, err = hangarAvatarURL(project.AvatarURL)
		if err != nil {
			http.Error(w, "Plugin icon unavailable.", 502)
			return
		}
		if project.AvatarURL == "https://hangarcdn.papermc.io/avatars/default/default.webp?v=0" {
			http.Error(w, "No custom plugin icon.", http.StatusNotFound)
			return
		}
	} else if supplied {
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
	response, err := pluginCatalogClient.Do(req)
	if err != nil {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > pluginIconLimit {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, pluginIconLimit+1))
	if err != nil || len(data) > pluginIconLimit {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	data, contentType, err := verifiedPluginIcon(ctx, strings.TrimPrefix(filepath.Ext(icon.Path), "."), data)
	if err != nil {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}

// Only these two icon-specific converters are injectable; callers cannot choose commands.
var convertPluginSVG = func(ctx context.Context, input []byte, output io.Writer) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/rsvg-convert", "--format=png", "--width=96", "--height=96", "--keep-aspect-ratio")
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = output
	return cmd.Run()
}
var convertPluginRGB = func(ctx context.Context, input []byte, output io.Writer) error {
	// A private, bounded PNM buffer joins the two fixed commands under one deadline.
	intermediate := &pluginIconBuffer{limit: 16 << 20}
	first := exec.CommandContext(ctx, "/usr/bin/sgitopnm")
	first.Stdin = bytes.NewReader(input)
	first.Stdout = intermediate
	if err := first.Run(); err != nil {
		return err
	}
	second := exec.CommandContext(ctx, "/usr/bin/pnmtopng")
	second.Stdin = bytes.NewReader(intermediate.Bytes())
	second.Stdout = output
	return second.Run()
}

type pluginIconBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

// Do not embed bytes.Buffer: its promoted ReadFrom would bypass the Write limit.
func (b *pluginIconBuffer) Bytes() []byte { return b.buffer.Bytes() }
func (b *pluginIconBuffer) Len() int      { return b.buffer.Len() }

func (b *pluginIconBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.overflow = true
		return 0, errors.New("icon conversion output exceeds limit")
	}
	return b.buffer.Write(p)
}

func validPluginRGB(data []byte) bool {
	if len(data) < 512 || binary.BigEndian.Uint16(data[:2]) != 0x01da || data[2] > 1 || (data[3] != 1 && data[3] != 2) {
		return false
	}
	dimension := binary.BigEndian.Uint16(data[4:6])
	width := binary.BigEndian.Uint16(data[6:8])
	height := binary.BigEndian.Uint16(data[8:10])
	channels := binary.BigEndian.Uint16(data[10:12])
	return dimension >= 1 && dimension <= 3 && width > 0 && height > 0 && width <= 1024 && height <= 1024 && channels > 0 && channels <= 4 && binary.BigEndian.Uint32(data[104:108]) == 0
}

func verifiedPluginIcon(ctx context.Context, extension string, data []byte) ([]byte, string, error) {
	unavailable := errors.New("plugin icon unavailable")
	if len(data) == 0 || len(data) > pluginIconLimit {
		return nil, "", unavailable
	}
	// CDN transforms can change the representation without changing the path or headers.
	// Only the bounded bytes determine the canonical raster MIME.
	switch kind := http.DetectContentType(data); kind {
	case "image/png", "image/jpeg", "image/bmp", "image/gif", "image/webp":
		return data, kind, nil
	}
	var convert func(context.Context, []byte, io.Writer) error
	switch extension {
	case "svg", "svgz":
		if extension == "svgz" {
			if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
				return nil, "", unavailable
			}
			reader, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, "", unavailable
			}
			decompressed, err := io.ReadAll(io.LimitReader(reader, pluginIconLimit+1))
			closeErr := reader.Close()
			if err != nil || closeErr != nil || len(decompressed) > pluginIconLimit || len(decompressed) == 0 {
				return nil, "", unavailable
			}
			data = decompressed
		}
		convert = convertPluginSVG
	case "rgb":
		if !validPluginRGB(data) {
			return nil, "", unavailable
		}
		convert = convertPluginRGB
	default:
		return nil, "", unavailable
	}
	conversionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output := &pluginIconBuffer{limit: pluginIconLimit}
	if err := convert(conversionCtx, data, output); err != nil || conversionCtx.Err() != nil || output.overflow || http.DetectContentType(output.Bytes()) != "image/png" {
		return nil, "", unavailable
	}
	return output.Bytes(), "image/png", nil
}

func managedPluginInventoryItem(path string, e pluginManifestEntry) installedPlugin {
	item := installedPlugin{Name: e.PluginName, Filename: e.Filename, Management: "manual", MetadataStatus: "Managed JAR missing or unavailable", UpdateStatus: "Attention: manifest JAR is missing or unavailable; ownership is unverified"}
	state, err := resolveManagedPluginLocation(path, e)
	if err != nil {
		item.UpdateStatus = "Attention: " + err.Error()
		return item
	}
	item.Management = "plugins"
	item.State = state
	item.Source = pluginSource(e.Source)
	item.ProjectID = e.ProjectID
	item.VersionID = e.VersionID
	item.Version = e.VersionNumber
	var root *os.File
	if state == "enabled" {
		root, err = openPluginDirectory(path)
	} else {
		root, err = openDisabledPluginDirectory(path, false)
	}
	if err == nil {
		jar, eerr := openPluginAt(root, e.Filename)
		if eerr == nil {
			identity, ierr := inspectStagedPlugin(jar, e.Filename)
			jar.Close()
			if ierr == nil && identity.Version != "" {
				item.Version = identity.Version
			}
		}
		root.Close()
	}
	item.Main = e.Main
	item.MetadataStatus = "Metadata read"
	item.UpdateStatus = "Managed by Plugins; updates are not available yet"
	return item
}
