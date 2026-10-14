package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Empty source is the deployed v1 Modrinth contract. Never mutate persisted
// entries just to normalize their identity.
func pluginSource(source string) string {
	if source == "" {
		return "Modrinth"
	}
	return source
}
func pluginRequestSource(sources []string) string {
	if len(sources) == 0 {
		return "Modrinth"
	}
	return pluginSource(sources[0])
}

var hangarIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func validPluginIdentity(source, id string) bool {
	switch pluginSource(source) {
	case "Modrinth":
		return modrinthPluginID.MatchString(id)
	case "Hangar":
		n, err := strconv.ParseInt(id, 10, 64)
		return hangarIDPattern.MatchString(id) && err == nil && n > 0
	}
	return false
}
func pluginIdentity(source, project string) string { return pluginSource(source) + ":" + project }

// Legacy bare parents mean Modrinth; Hangar parents are explicitly qualified.
func pluginParentIdentity(parent string) string {
	if !strings.Contains(parent, ":") {
		return pluginIdentity("Modrinth", parent)
	}
	return parent
}
func validPluginParent(parent string) bool {
	source, id, qualified := strings.Cut(parent, ":")
	if !qualified {
		return validPluginIdentity("Modrinth", parent)
	}
	return source != "" && validPluginIdentity(source, id)
}
func hangarPluginPlatform(serverType string) (string, error) {
	switch serverType {
	case "paper", "purpur":
		return "PAPER", nil
	}
	return "", errors.New("No Hangar platform mapping exists for the selected server type")
}
func resolveProviderPluginInstallPlan(ctx context.Context, server pluginServerContext, path, source, project, version string) (pluginInstallPlan, error) {
	switch pluginSource(source) {
	case "Modrinth":
		return resolvePluginInstallPlan(ctx, server, path, project, version)
	case "Hangar":
		return resolveHangarPluginInstallPlan(ctx, server, path, project, version)
	}
	return pluginInstallPlan{}, errors.New("Unsupported plugin provider")
}

type hangarDependency struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	ProjectID   *int64 `json:"projectId"`
	ExternalURL string `json:"externalUrl"`
}

func hangarDependencyID(d hangarDependency) string {
	if d.ProjectID != nil && *d.ProjectID > 0 {
		return strconv.FormatInt(*d.ProjectID, 10)
	}
	return ""
}

type hangarDownload struct {
	DownloadURL string          `json:"downloadUrl"`
	ExternalURL string          `json:"externalUrl"`
	FileInfo    *hangarFileInfo `json:"fileInfo"`
}

var hangarHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Construct the official hosted CDN path from trusted project/release metadata.
// The API download endpoint redirects in production, so it is never followed.
func hangarDownloadURL(project hangarProject, version, platform, filename string) string {
	return "https://hangarcdn.papermc.io/plugins/" + url.PathEscape(project.Namespace.Owner) + "/" + url.PathEscape(project.Namespace.Slug) + "/versions/" + hangarDownloadSegment(version) + "/" + platform + "/" + hangarDownloadSegment(filename)
}
func hangarDownloadSegment(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(value), "+", "%20"), "%2A", "*")
}
func safeHangarPathSegment(value string, limit int) bool {
	return value != "" && len(value) <= limit && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00\r\n")
}
func safeHangarArtifactURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Host != "hangarcdn.papermc.io" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	parts := strings.Split(u.Path, "/")
	return len(parts) == 8 && parts[0] == "" && parts[1] == "plugins" && safeHangarPathSegment(parts[2], 128) && safeHangarPathSegment(parts[3], 128) && parts[4] == "versions" && safeHangarPathSegment(parts[5], 256) && parts[6] == "PAPER" && safePluginFilename(parts[7])
}
func hangarArtifact(v hangarVersion, platform string, project hangarProject) (pluginInstallArtifact, error) {
	d, ok := v.Downloads[platform]
	if !ok || d.DownloadURL == "" || d.ExternalURL != "" || d.FileInfo == nil {
		return pluginInstallArtifact{}, errors.New("Hangar external or missing downloads are not supported; a direct Hangar-hosted plugin JAR is required")
	}
	f := d.FileInfo
	if project.ID != v.ProjectID || !safeHangarPathSegment(project.Namespace.Owner, 128) || !safeHangarPathSegment(project.Namespace.Slug, 128) || !safeHangarPathSegment(v.Name, 256) {
		return pluginInstallArtifact{}, errors.New("Unsafe Hangar project/release metadata")
	}
	endpoint := hangarDownloadURL(project, v.Name, platform, f.Name)
	if d.DownloadURL != endpoint || !safeHangarArtifactURL(endpoint) {
		return pluginInstallArtifact{}, errors.New("Unsafe Hangar download metadata")
	}
	a := pluginInstallArtifact{Source: "Hangar", ProjectID: strconv.FormatInt(v.ProjectID, 10), VersionID: strconv.FormatInt(v.ID, 10), VersionNumber: v.Name, Filename: f.Name, Size: f.Size, SHA256: f.SHA256, URL: endpoint, RequiredBy: []string{}}
	if !validPluginIdentity(a.Source, a.ProjectID) || !validPluginIdentity(a.Source, a.VersionID) || !validPluginDownloadMetadata(a) {
		return a, errors.New("Hangar release has unsafe or incomplete file metadata")
	}
	return a, nil
}
func validPluginDownloadMetadata(a pluginInstallArtifact) bool {
	if !safePluginFilename(a.Filename) || a.Size <= 0 || a.Size > pluginMaxJar {
		return false
	}
	switch pluginSource(a.Source) {
	case "Modrinth":
		return safePluginArtifactURL(a.URL) && pluginHashPattern.MatchString(a.SHA512)
	case "Hangar":
		return safeHangarArtifactURL(a.URL) && hangarHashPattern.MatchString(a.SHA256)
	}
	return false
}
func fingerprintPluginHash(a pluginInstallArtifact) string {
	if pluginSource(a.Source) == "Hangar" {
		return a.SHA256
	}
	return a.SHA512
}
func hangarCatalogFiles(v hangarVersion, platform string, projects []hangarProject) []pluginArtifactFile {
	if len(projects) != 1 {
		return nil
	}
	a, err := hangarArtifact(v, platform, projects[0])
	if err != nil {
		return nil
	}
	return []pluginArtifactFile{{Filename: a.Filename, Size: a.Size}}
}

// Exact selection uses Hangar's official nameOrId route with numeric identities.
// Dependency selection scans only bounded compatible official release pages.
func exactHangarVersion(ctx context.Context, project, selected string, server pluginServerContext) (hangarVersion, error) {
	if selected != "" {
		var exact hangarVersion
		if err := hangarPluginJSON(ctx, "/projects/"+project+"/versions/"+selected, nil, &exact); err != nil {
			return exact, errors.New("Exact Hangar release unavailable")
		}
		if strconv.FormatInt(exact.ID, 10) != selected || strconv.FormatInt(exact.ProjectID, 10) != project || !safeHangarPathSegment(exact.Name, 256) {
			return exact, errors.New("Hangar project/version identity mismatch")
		}
		return exact, nil
	}
	platform, err := hangarPluginPlatform(server.ServerType)
	if err != nil {
		return hangarVersion{}, err
	}
	for offset := 0; offset < 75; offset += 25 {
		var page struct {
			Pagination hangarPagination `json:"pagination"`
			Result     []hangarVersion  `json:"result"`
		}
		q := url.Values{"platform": {platform}, "platformVersion": {server.MinecraftVersion}, "limit": {"25"}, "offset": {strconv.Itoa(offset)}}
		if err := hangarPluginJSON(ctx, "/projects/"+project+"/versions", q, &page); err != nil {
			return hangarVersion{}, errors.New("Hangar release metadata unavailable")
		}
		if page.Result == nil || !page.Pagination.valid(offset, 25, len(page.Result)) {
			return hangarVersion{}, errors.New("Invalid Hangar releases")
		}
		for _, v := range page.Result {
			if strconv.FormatInt(v.ProjectID, 10) != project || v.ID <= 0 || !hangarVersionStable(v) || !slices.Contains(v.PlatformDependencies[platform], server.MinecraftVersion) {
				continue
			}
			exact, err := exactHangarVersion(ctx, project, strconv.FormatInt(v.ID, 10), server)
			if err != nil {
				return exact, err
			}
			if exact.Name != v.Name {
				return exact, errors.New("Hangar release identity changed")
			}
			return exact, nil
		}
		if offset+len(page.Result) >= *page.Pagination.Count || len(page.Result) == 0 {
			break
		}
	}
	return hangarVersion{}, errors.New("No compatible Hangar dependency release exists within the bounded review")
}

func resolveHangarPluginInstallPlan(ctx context.Context, server pluginServerContext, dataPath, project, version string) (pluginInstallPlan, error) {
	plan := pluginInstallPlan{Source: "Hangar", ProjectID: project, VersionID: version, Server: server, DataPath: dataPath, Location: filepath.Join(dataPath, "plugins"), RestartRequired: true, Compatibility: "Compatible according to publisher metadata", Artifacts: []pluginInstallArtifact{}, DisplayDependencies: []pluginDependency{}}
	platform, err := hangarPluginPlatform(server.ServerType)
	if err != nil || !server.Supported || server.MinecraftVersion == "" || !filepath.IsAbs(dataPath) || !validPluginIdentity("Hangar", project) || !validPluginIdentity("Hangar", version) {
		return plan, errors.New("A known selected server type/version and exact Hangar identities are required")
	}
	manifest, _, err := readPluginManifest(dataPath)
	if err != nil {
		return plan, errors.New("Plugin ownership manifest is unsafe or malformed")
	}
	root, err := openPluginDirectory(dataPath)
	if err != nil {
		return plan, errors.New("Plugins must be an existing real directory")
	}
	defer root.Close()
	seen, active := map[string]int{}, map[string]bool{}
	filenames := map[string]bool{}
	var total int64
	var visit func(string, string, string, int) error
	visit = func(project, version, parent string, depth int) error {
		if depth > pluginMaxDepth {
			return errors.New("Required dependency depth exceeds the safe limit")
		}
		if !validPluginIdentity("Hangar", project) || (version != "" && !validPluginIdentity("Hangar", version)) {
			return errors.New("Invalid Hangar dependency identity")
		}
		key := pluginIdentity("Hangar", project)
		if active[key] {
			return errors.New("Required dependency cycle detected")
		}
		if index, ok := seen[key]; ok {
			if version != "" && plan.Artifacts[index].VersionID != version {
				return errors.New("Required dependencies request conflicting versions")
			}
			if parent != "" && !slices.Contains(plan.Artifacts[index].RequiredBy, parent) {
				plan.Artifacts[index].RequiredBy = append(plan.Artifacts[index].RequiredBy, parent)
			}
			return nil
		}
		if len(plan.Artifacts) >= pluginMaxArtifacts {
			return errors.New("Required dependency graph exceeds the safe artifact limit")
		}
		var metadata hangarProject
		if err := hangarPluginJSON(ctx, "/projects/"+project, nil, &metadata); err != nil || strconv.FormatInt(metadata.ID, 10) != project || metadata.Name == "" || len(metadata.Name) > 256 || len(metadata.Description) > 4096 {
			return errors.New("Hangar project identity or metadata unavailable")
		}
		v, err := exactHangarVersion(ctx, project, version, server)
		if err != nil {
			return err
		}
		if !slices.Contains(v.PlatformDependencies[platform], server.MinecraftVersion) {
			return errors.New("Hangar release does not support the selected Minecraft version")
		}
		a, err := hangarArtifact(v, platform, metadata)
		if err != nil {
			return err
		}
		a.Name = metadata.Name
		a.Requested = parent == ""
		if parent != "" {
			a.RequiredBy = append(a.RequiredBy, parent)
		} else {
			plan.Name, plan.Description, plan.VersionNumber = metadata.Name, metadata.Description, v.Name
		}
		if filenames[strings.ToLower(a.Filename)] {
			return errors.New("Required releases collide on a filename")
		}
		filenames[strings.ToLower(a.Filename)] = true
		total += a.Size
		if total > pluginMaxDownload {
			return errors.New("Combined plugin download exceeds the safe limit")
		}
		if component := managedPluginIdentity(a.Filename, ""); component != "" {
			if a.Requested {
				return errors.New("This plugin is JustVoxel managed; use its existing server mechanism")
			}
			filename, hash, identity, err := findManagedPluginComponent(root, component)
			if err != nil {
				return err
			}
			a.Satisfied, a.ExistingFilename, a.ExistingHash, a.PluginName, a.Main = "JustVoxel managed", filename, hash, identity.Name, identity.Main
		} else {
			for _, e := range manifest.Artifacts {
				if pluginIdentity(e.Source, e.ProjectID) != key {
					continue
				}
				if a.Requested {
					return errors.New("Plugin already installed; plugin updates are not available in Stage 2")
				}
				if e.VersionID != a.VersionID || e.Filename != a.Filename {
					return errors.New("Installed dependency requires an update; Stage 2 cannot upgrade dependencies")
				}
				if err := verifyManagedPluginAt(root, e); err != nil {
					return err
				}
				jar, err := openPluginAt(root, e.Filename)
				if err != nil {
					return err
				}
				h := sha256.New()
				n, hashErr := io.Copy(h, io.LimitReader(jar, a.Size+1))
				jar.Close()
				if hashErr != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
					return errors.New("Installed dependency differs from authoritative Hangar artifact")
				}
				a.Satisfied, a.ExistingFilename, a.ExistingHash, a.PluginName, a.Main = "Managed by Plugins", e.Filename, e.SHA512, e.PluginName, e.Main
			}
		}
		if a.Satisfied == "" {
			var st unix.Stat_t
			if err := unix.Fstatat(int(root.Fd()), a.Filename, &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
				return errors.New("Plugin target filename already exists or cannot be safely checked")
			}
		}
		seen[key] = len(plan.Artifacts)
		plan.Artifacts = append(plan.Artifacts, a)
		active[key] = true
		deps := slices.Clone(v.PluginDependencies[platform])
		if len(deps) > 128 {
			return errors.New("Release dependency metadata exceeds the safe limit")
		}
		sort.Slice(deps, func(i, j int) bool {
			if deps[i].Name == deps[j].Name {
				return hangarDependencyID(deps[i]) < hangarDependencyID(deps[j])
			}
			return deps[i].Name < deps[j].Name
		})
		for _, d := range deps {
			if d.Name == "" || len(d.Name) > 256 {
				return errors.New("Invalid Hangar dependency metadata")
			}
			id := hangarDependencyID(d)
			if d.Required {
				if id == "" {
					return errors.New("Required Hangar dependency " + d.Name + " has no internal project identity; external dependency downloads are not supported")
				}
				if err := visit(id, "", key, depth+1); err != nil {
					return err
				}
			} else {
				plan.DisplayDependencies = append(plan.DisplayDependencies, pluginDependency{Source: "Hangar", Name: d.Name, ProjectID: id, Type: "optional"})
			}
		}
		delete(active, key)
		return nil
	}
	if err := visit(project, version, "", 0); err != nil {
		return plan, err
	}
	sort.Slice(plan.Artifacts, func(i, j int) bool { return plan.Artifacts[i].ProjectID < plan.Artifacts[j].ProjectID })
	for i := range plan.Artifacts {
		sort.Strings(plan.Artifacts[i].RequiredBy)
	}
	plan.Fingerprint = fingerprintPluginPlan(plan)
	return plan, nil
}

var hangarAvatarVersion = regexp.MustCompile(`^[0-9]{1,19}$`)
var hangarUserAvatarSubject = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func hangarAvatarURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Host != "hangarcdn.papermc.io" || u.User != nil || u.ForceQuery || u.RawPath != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("Invalid Hangar avatar URL")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["v"]) != 1 || !hangarAvatarVersion.MatchString(query.Get("v")) {
		return nil, errors.New("Invalid Hangar avatar version")
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "avatars" || !strings.HasSuffix(parts[3], ".webp") {
		return nil, errors.New("Invalid Hangar avatar path")
	}
	subject := strings.TrimSuffix(parts[3], ".webp")
	valid := false
	switch parts[2] {
	case "project":
		valid = validPluginIdentity("Hangar", subject)
	case "user":
		valid = hangarUserAvatarSubject.MatchString(subject)
	case "default":
		valid = subject == "default" && query.Get("v") == "0"
	}
	if !valid {
		return nil, errors.New("Invalid Hangar avatar subject")
	}
	return u, nil
}
func validPluginIconQuery(query url.Values) bool {
	for key, values := range query {
		if !slices.Contains([]string{"source", "project_id", "icon_url"}, key) || len(values) != 1 {
			return false
		}
	}
	source := pluginSource(query.Get("source"))
	if !validPluginIdentity(source, query.Get("project_id")) {
		return false
	}
	return source != "Hangar" || len(query["icon_url"]) == 0
}

type hangarFileInfo struct {
	Name   string `json:"name"`
	Size   int64  `json:"sizeBytes"`
	SHA256 string `json:"sha256Hash"`
}
