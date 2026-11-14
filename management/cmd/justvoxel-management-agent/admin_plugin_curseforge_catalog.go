package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Fixed origin, bounded body/time and no redirects. Credentials are never sent
// to other providers or icons, or returned in errors, plans, journals or catalog JSON.
var curseForgeClient = &http.Client{
	Timeout:       6 * time.Second,
	Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}
var errCurseForgeCatalog = errors.New("CurseForge catalog is unavailable. Check API configuration and retry.")

func curseForgeJSON(ctx context.Context, path string, q url.Values, out any) error {
	if !strings.HasPrefix(path, "/v1/") || strings.ContainsAny(path, "?#\\\r\n") {
		return errCurseForgeCatalog
	}
	key, err := readCurseForgeKey()
	if err != nil {
		return errCurseForgeCatalog
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.curseforge.com"+path+"?"+q.Encode(), nil)
	if err != nil {
		return errCurseForgeCatalog
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Accept", "application/json")
	response, err := curseForgeClient.Do(req)
	if err != nil {
		return errCurseForgeCatalog
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &pluginProviderDiagnosticError{reason: "CurseForge API returned HTTP " + strconv.Itoa(response.StatusCode), values: map[string]string{"provider": "CurseForge", "stage": "provider_metadata", "http_status": strconv.Itoa(response.StatusCode), "api_path": path, "reason": "Official metadata request was not successful"}}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return errCurseForgeCatalog
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return errCurseForgeCatalog
	}
	return nil
}

type curseForgePagination struct {
	Index       int `json:"index"`
	PageSize    int `json:"pageSize"`
	ResultCount int `json:"resultCount"`
	TotalCount  int `json:"totalCount"`
}

func (p curseForgePagination) valid(index, limit, count int) bool {
	return p.Index == index && p.PageSize == limit && p.ResultCount == count && count <= limit && p.TotalCount >= index+count
}

type curseForgeContext struct {
	Game  int64
	Class int64
}

// Resolve exact official identities each time: no guessed IDs or stale cache
// after key replacement/removal, and failed lookups never poison retries.
func resolveCurseForgeContext(ctx context.Context, server pluginServerContext) (curseForgeContext, error) {
	out := curseForgeContext{}
	kind := javaServerContentKind(server.ServerType)
	if kind == "" {
		return out, errCurseForgeCatalog
	}
	for index := 0; index < 200; index += 50 {
		var games struct {
			Data []struct {
				ID   int64  `json:"id"`
				Slug string `json:"slug"`
			} `json:"data"`
			Pagination curseForgePagination `json:"pagination"`
		}
		if err := curseForgeJSON(ctx, "/v1/games", url.Values{"index": {strconv.Itoa(index)}, "pageSize": {"50"}}, &games); err != nil {
			return out, err
		}
		if games.Data == nil || !games.Pagination.valid(index, 50, len(games.Data)) {
			return out, errCurseForgeCatalog
		}
		for _, g := range games.Data {
			if g.Slug == "minecraft" {
				if g.ID <= 0 || out.Game != 0 {
					return out, errCurseForgeCatalog
				}
				out.Game = g.ID
			}
		}
		if out.Game != 0 || len(games.Data) < 50 {
			break
		}
	}
	if out.Game == 0 {
		return out, errCurseForgeCatalog
	}
	var categories struct {
		Data []struct {
			ID      int64  `json:"id"`
			Game    int64  `json:"gameId"`
			Name    string `json:"name"`
			Slug    string `json:"slug"`
			IsClass bool   `json:"isClass"`
		} `json:"data"`
	}
	if err := curseForgeJSON(ctx, "/v1/categories", url.Values{"gameId": {strconv.FormatInt(out.Game, 10)}, "classesOnly": {"true"}}, &categories); err != nil {
		return out, err
	}
	if len(categories.Data) > 256 {
		return out, errCurseForgeCatalog
	}
	slug, name := "bukkit-plugins", "Bukkit Plugins"
	if kind == "mods" {
		slug, name = "mc-mods", "Minecraft Mods"
	}
	for _, c := range categories.Data {
		if c.Slug == slug || c.Name == name {
			if c.ID <= 0 || c.Game != out.Game || !c.IsClass || out.Class != 0 {
				return out, errCurseForgeCatalog
			}
			out.Class = c.ID
		}
	}
	if out.Class == 0 {
		return out, errCurseForgeCatalog
	}
	return out, nil
}
func curseForgeLoader(server pluginServerContext) (int, string) {
	if javaServerContentKind(server.ServerType) != "mods" {
		return 0, ""
	}
	switch server.ServerType {
	case "forge":
		return 1, "Forge"
	case "fabric":
		return 4, "Fabric"
	case "quilt":
		return 5, "Quilt"
	case "neoforge":
		return 6, "NeoForge"
	}
	return 0, ""
}

type curseForgeAsset struct {
	ID           int64  `json:"id"`
	ModID        int64  `json:"modId"`
	ThumbnailURL string `json:"thumbnailUrl"`
	URL          string `json:"url"`
}

type curseForgeProject struct {
	Available            *bool            `json:"isAvailable,omitempty"`
	AllowModDistribution *bool            `json:"allowModDistribution,omitempty"`
	Logo                 *curseForgeAsset `json:"logo"`
	ID                   int64            `json:"id"`
	Game                 int64            `json:"gameId"`
	Class                int64            `json:"classId"`
	Name                 string           `json:"name"`
	Slug                 string           `json:"slug"`
	Summary              string           `json:"summary"`
}

func curseForgeInstallRestriction(p curseForgeProject) string {
	if p.Available != nil && !*p.Available {
		return "This CurseForge project is not available for installation."
	}
	return ""
}

type curseForgeDependency struct {
	ModID    int64 `json:"modId"`
	Relation int   `json:"relationType"`
}
type curseForgeFile struct {
	ID           int64    `json:"id"`
	ModID        int64    `json:"modId"`
	Game         int64    `json:"gameId"`
	Available    bool     `json:"isAvailable"`
	Filename     string   `json:"fileName"`
	DisplayName  string   `json:"displayName"`
	Size         int64    `json:"fileLength"`
	URL          string   `json:"downloadUrl"`
	GameVersions []string `json:"gameVersions"`
	Release      int      `json:"releaseType"`
	Status       int      `json:"fileStatus"`
	Published    string   `json:"fileDate"`
	Hashes       []struct {
		Value string `json:"value"`
		Algo  int    `json:"algo"`
	} `json:"hashes"`
	Dependencies []curseForgeDependency `json:"dependencies"`
}

func curseForgeRelease(n int) string {
	switch n {
	case 1:
		return "release"
	case 2:
		return "beta"
	case 3:
		return "alpha"
	}
	return ""
}

// Exact provider platform markers only. Numeric Minecraft versions, generic
// category labels and unrelated words never establish a server platform.
func curseForgePlatforms(f curseForgeFile) []string {
	out := []string{}
	for _, marker := range f.GameVersions {
		platform := strings.ToLower(marker)
		switch platform {
		case "bukkit", "spigot", "paper", "purpur", "pufferfish", "leaf", "fabric", "forge", "neoforge", "quilt":
			if !slices.Contains(out, platform) {
				out = append(out, platform)
			}
		}
	}
	return out
}
func curseForgeCompatible(f curseForgeFile, server pluginServerContext) bool {
	return server.MinecraftVersion != "" && slices.Contains(f.GameVersions, server.MinecraftVersion) && pluginLoaderOverlap(curseForgePlatforms(f), server.ServerType)
}

// Missing plugin platform evidence is eligible for exact artifact Review, not
// positive catalog compatibility. Explicit contradictory platforms still fail.
func curseForgeArtifactEligible(f curseForgeFile, server pluginServerContext) bool {
	return curseForgeCompatible(f, server) || javaServerContentKind(server.ServerType) == "plugins" && server.MinecraftVersion != "" && slices.Contains(f.GameVersions, server.MinecraftVersion) && len(curseForgePlatforms(f)) == 0
}

var curseForgeSHA1 = regexp.MustCompile(`^[0-9a-f]{40}$`)

func trustedCurseForgeCDN(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "https" && u.User == nil && net.ParseIP(host) == nil &&
		(host == "forgecdn.net" || strings.HasSuffix(host, ".forgecdn.net")) &&
		(u.Port() == "" || u.Port() == "443")
}

func safeCurseForgeArtifactURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.RawQuery != "" || u.ForceQuery {
		return false
	}
	// Provider domain boundary, independent of individual CDN hostnames.
	if !trustedCurseForgeCDN(u) {
		return false
	}
	// Split the escaped path first so encoded structural segments and separators
	// cannot change the expected CDN layout. Only the filename may be escaped.
	parts := strings.Split(u.EscapedPath(), "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "files" || !positiveCurseForgeID(parts[2]) || !positiveCurseForgeID(parts[3]) {
		return false
	}
	filename, err := url.PathUnescape(parts[4])
	return err == nil && safePluginFilename(filename)
}

// Current operational avatar allowlist, independent of artifact download URLs.
// A future CDN change leaves initials visible until this allowlist is reviewed.
var curseForgeLogoFilename = regexp.MustCompile(`^[A-Za-z0-9_-]+\.(png|jpg|jpeg|gif|webp)$`)

func safeCurseForgeLogoURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Host != "media.forgecdn.net" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "#%\\") || u.RawPath != "" || u.ForceQuery || u.RawQuery != "" {
		return false
	}
	decimal := func(value string) bool {
		return len(value) > 0 && len(value) <= 19 && strings.Trim(value, "0123456789") == ""
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) == 5 && parts[0] == "" && parts[1] == "avatars" {
		return decimal(parts[2]) && decimal(parts[3]) && curseForgeLogoFilename.MatchString(parts[4])
	}
	if len(parts) != 8 || parts[0] != "" || parts[1] != "avatars" || parts[2] != "thumbnails" || !decimal(parts[3]) || !decimal(parts[4]) || !curseForgeLogoFilename.MatchString(parts[7]) {
		return false
	}
	for _, dimension := range parts[5:7] {
		if !decimal(dimension) {
			return false
		}
		n, err := strconv.ParseUint(dimension, 10, 64)
		if err != nil || n == 0 || n > 2048 {
			return false
		}
	}
	return true
}

func curseForgeProjectLogo(project curseForgeProject, expectedID int64) string {
	if project.Logo == nil || project.Logo.ID <= 0 || expectedID <= 0 || project.Logo.ModID != expectedID {
		return ""
	}
	if safeCurseForgeLogoURL(project.Logo.ThumbnailURL) {
		return project.Logo.ThumbnailURL
	}
	if safeCurseForgeLogoURL(project.Logo.URL) {
		return project.Logo.URL
	}
	return ""
}

// Display-only lookup: icon requests do not need server compatibility scope.
func curseForgeIconProject(ctx context.Context, id string) (curseForgeProject, error) {
	var response struct {
		Data curseForgeProject `json:"data"`
	}
	if !positiveCurseForgeID(id) {
		return response.Data, errCurseForgeCatalog
	}
	if err := curseForgeJSON(ctx, "/v1/mods/"+id, nil, &response); err != nil {
		return response.Data, err
	}
	p := response.Data
	if strconv.FormatInt(p.ID, 10) != id || p.Logo != nil && (p.Logo.ID <= 0 || p.Logo.ModID != p.ID) {
		return p, errCurseForgeCatalog
	}
	return p, nil
}

func positiveCurseForgeID(id string) bool { return validPluginIdentity("CurseForge", id) }
func curseForgeArtifact(ctx context.Context, f curseForgeFile, project string, server pluginServerContext, scope curseForgeContext) (a pluginInstallArtifact, failure error) {
	defer func() {
		if failure == nil {
			return
		}
		values := pluginArtifactDiagnostic(a, "artifact_metadata")
		var provider *pluginProviderDiagnosticError
		if errors.As(failure, &provider) {
			for key, value := range provider.values {
				values[key] = value
			}
		}
		values["reason"] = failure.Error()
		failure = &pluginProviderDiagnosticError{reason: failure.Error(), values: values}
	}()
	a = pluginInstallArtifact{Target: javaServerContentKind(server.ServerType), Source: "CurseForge", ProjectID: project, VersionID: strconv.FormatInt(f.ID, 10), VersionNumber: f.DisplayName, Filename: f.Filename, Size: f.Size, URL: f.URL, RequiredBy: []string{}}
	if a.VersionNumber == "" {
		a.VersionNumber = f.Filename
	}
	if !positiveCurseForgeID(project) || strconv.FormatInt(f.ModID, 10) != project || f.Game != scope.Game || !positiveCurseForgeID(a.VersionID) || !f.Available || f.Status != 4 || f.Release != 1 || !curseForgeArtifactEligible(f, server) || len(a.VersionNumber) > 256 || len(f.Dependencies) > 128 || len(f.Hashes) > 8 || len(f.GameVersions) > 256 {
		return a, errors.New("CurseForge file identity, availability or exact server compatibility could not be verified")
	}
	for _, h := range f.Hashes {
		if h.Algo == 1 {
			if a.SHA1 != "" || !curseForgeSHA1.MatchString(h.Value) {
				return a, errors.New("CurseForge SHA-1 evidence is malformed or duplicated")
			}
			a.SHA1 = h.Value
		}
	}
	// Verify exact file evidence before asking the authenticated API for a URL.
	if !safePluginFilename(a.Filename) {
		return a, errors.New("CurseForge artifact filename is missing or unsafe")
	}
	if a.Size <= 0 || a.Size > pluginMaxJar {
		return a, errors.New("CurseForge artifact size is missing, invalid or exceeds the safe limit")
	}
	if a.SHA1 == "" {
		return a, errors.New("CurseForge SHA-1 evidence is missing")
	}
	if a.URL == "" {
		var result struct {
			Data string `json:"data"`
		}
		if err := curseForgeJSON(ctx, "/v1/mods/"+project+"/files/"+a.VersionID+"/download-url", nil, &result); err != nil {
			return a, err
		}
		a.URL = result.Data
	}
	if a.URL == "" {
		return a, errors.New("CurseForge did not provide an official artifact URL for this release")
	}
	if !validPluginDownloadMetadata(a) {
		return a, errors.New("CurseForge official artifact URL is unsafe")
	}
	return a, nil
}
func exactCurseForgeProject(ctx context.Context, id string, scope curseForgeContext) (curseForgeProject, error) {
	var response struct {
		Data curseForgeProject `json:"data"`
	}
	if !positiveCurseForgeID(id) {
		return response.Data, errCurseForgeCatalog
	}
	err := curseForgeJSON(ctx, "/v1/mods/"+id, nil, &response)
	p := response.Data
	if err != nil {
		return p, err
	}
	if strconv.FormatInt(p.ID, 10) != id || p.Game != scope.Game || p.Class != scope.Class || p.Name == "" || len(p.Name) > 256 || len(p.Summary) > 4096 {
		return p, errCurseForgeCatalog
	}
	return p, nil
}
func exactCurseForgeFile(ctx context.Context, project, id string) (curseForgeFile, error) {
	var result struct {
		Data curseForgeFile `json:"data"`
	}
	if !positiveCurseForgeID(project) || !positiveCurseForgeID(id) {
		return result.Data, errCurseForgeCatalog
	}
	if err := curseForgeJSON(ctx, "/v1/mods/"+project+"/files/"+id, nil, &result); err != nil {
		return result.Data, err
	}
	if strconv.FormatInt(result.Data.ID, 10) != id || strconv.FormatInt(result.Data.ModID, 10) != project {
		return result.Data, errCurseForgeCatalog
	}
	return result.Data, nil
}

// Only explicit Server evidence qualifies a mod for dedicated-server Discover.
// Plugins retain their existing platform and JAR verification behavior.
func curseForgeDiscoverServerCapable(f curseForgeFile, server pluginServerContext) bool {
	if javaServerContentKind(server.ServerType) != "mods" {
		return true
	}
	for _, marker := range f.GameVersions {
		if strings.EqualFold(marker, "Server") {
			return true
		}
	}
	return false
}

// Exact alternate identity evidence only. This does not switch the install plan,
// dependency resolver or ownership manifest away from CurseForge.
func exactCurseForgeModrinthVersion(ctx context.Context, a pluginInstallArtifact, server pluginServerContext) *pluginCatalogVersion {
	if a.Source != "CurseForge" || !positiveCurseForgeID(a.ProjectID) || !positiveCurseForgeID(a.VersionID) || !server.Supported || !validPluginDownloadMetadata(a) {
		return nil
	}
	var v pluginCatalogVersion
	if err := modrinthPluginJSON(ctx, "/version_file/"+a.SHA1, url.Values{"algorithm": {"sha1"}}, &v); err != nil {
		return nil
	}
	if !modrinthPluginID.MatchString(v.ProjectID) || !modrinthPluginID.MatchString(v.ID) || v.Type != "release" || !pluginVersionCompatible(v, server) || !modrinthServerEnvironment(v.Environment) {
		return nil
	}
	for _, f := range v.Files {
		if f.Hashes.SHA1 != a.SHA1 || f.Size != a.Size {
			continue
		}
		if _, err := selectPluginArtifact([]pluginArtifactFile{f}); err == nil {
			v.Files = []pluginArtifactFile{f}
			return &v
		}
	}
	return nil
}

func selectCurseForgeFile(ctx context.Context, project string, server pluginServerContext, compatible, release, discover bool) (*curseForgeFile, error) {
	for index := 0; index < 75; index += 25 {
		q := url.Values{"index": {strconv.Itoa(index)}, "pageSize": {"25"}}
		if compatible {
			q.Set("gameVersion", server.MinecraftVersion)
			if loader, _ := curseForgeLoader(server); loader != 0 {
				q.Set("modLoaderType", strconv.Itoa(loader))
			}
		}
		var page struct {
			Data       []curseForgeFile     `json:"data"`
			Pagination curseForgePagination `json:"pagination"`
		}
		if err := curseForgeJSON(ctx, "/v1/mods/"+project+"/files", q, &page); err != nil {
			return nil, err
		}
		if page.Data == nil || !page.Pagination.valid(index, 25, len(page.Data)) {
			return nil, errCurseForgeCatalog
		}
		sort.SliceStable(page.Data, func(i, j int) bool { return page.Data[i].Published > page.Data[j].Published })
		for _, v := range page.Data {
			if discover && !curseForgeDiscoverServerCapable(v, server) {
				continue
			}
			if !v.Available || v.Status != 4 || curseForgeRelease(v.Release) == "" || release && v.Release != 1 || compatible && !(curseForgeCompatible(v, server) || !discover && curseForgeArtifactEligible(v, server)) {
				continue
			}
			_, loader := curseForgeLoader(server)
			if loader != "" && !slices.Contains(curseForgePlatforms(v), strings.ToLower(loader)) {
				continue
			}
			exact, err := exactCurseForgeFile(ctx, project, strconv.FormatInt(v.ID, 10))
			if err != nil {
				return nil, err
			}
			if discover && !curseForgeDiscoverServerCapable(exact, server) {
				continue
			}
			if !exact.Available || exact.Status != 4 || curseForgeRelease(exact.Release) == "" || release && exact.Release != 1 || compatible && !(curseForgeCompatible(exact, server) || !discover && curseForgeArtifactEligible(exact, server)) || loader != "" && !slices.Contains(curseForgePlatforms(exact), strings.ToLower(loader)) {
				continue
			}
			return &exact, nil
		}
		if index+len(page.Data) >= page.Pagination.TotalCount || len(page.Data) == 0 {
			break
		}
	}
	return nil, nil
}
func discoverCurseForgePlugins(ctx context.Context, server pluginServerContext, q pluginDiscoverQuery) (pluginCatalogPage, error) {
	offset := q.Cursor.CurseForgeOffset
	out := pluginCatalogPage{Projects: []pluginCatalogProject{}, Offset: offset, End: offset, NextOffset: -1}
	scope, err := resolveCurseForgeContext(ctx, server)
	if err != nil {
		return out, err
	}
	query := url.Values{"gameId": {strconv.FormatInt(scope.Game, 10)}, "classId": {strconv.FormatInt(scope.Class, 10)}, "searchFilter": {q.Search}, "index": {strconv.Itoa(offset)}, "pageSize": {strconv.Itoa(q.Limit)}}
	if q.Compatible {
		query.Set("gameVersion", server.MinecraftVersion)
		if loader, _ := curseForgeLoader(server); loader != 0 {
			query.Set("modLoaderType", strconv.Itoa(loader))
		}
	}
	inspectionBudget := 8 * q.Limit
	cursor, scanned := offset, 0
	upstreamEnd := false
	for len(out.Projects) < q.Limit && scanned < inspectionBudget && cursor < 10000 {
		batchLimit := min(q.Limit, inspectionBudget-scanned)
		query.Set("index", strconv.Itoa(cursor))
		query.Set("pageSize", strconv.Itoa(batchLimit))
		var page struct {
			Data       []curseForgeProject  `json:"data"`
			Pagination curseForgePagination `json:"pagination"`
		}
		if err := curseForgeJSON(ctx, "/v1/mods/search", query, &page); err != nil {
			return out, err
		}
		if page.Data == nil || !page.Pagination.valid(cursor, batchLimit, len(page.Data)) {
			return out, errCurseForgeCatalog
		}
		out.Total = page.Pagination.TotalCount
		if len(page.Data) == 0 {
			upstreamEnd = true
			break
		}
		for _, p := range page.Data {
			if len(out.Projects) == q.Limit || cursor >= out.Total || cursor >= 10000 {
				break
			}
			cursor++
			scanned++
			id := strconv.FormatInt(p.ID, 10)
			if !positiveCurseForgeID(id) || p.Game != scope.Game || p.Class != scope.Class || p.Name == "" || len(p.Name) > 256 || len(p.Summary) > 4096 {
				continue
			}
			project := pluginCatalogProject{ID: id, Source: "CurseForge", Name: p.Name, Slug: p.Slug, Description: p.Summary, Compatibility: "Compatibility unknown", IconURL: curseForgeProjectLogo(p, p.ID)}
			f, err := selectCurseForgeFile(ctx, id, server, q.Compatible, q.Release, true)
			if err != nil {
				project.MetadataError = "Release metadata unavailable"
			} else if f == nil {
				continue
			} else {
				loaders := curseForgePlatforms(*f)
				project.Version = &pluginCatalogVersion{ID: strconv.FormatInt(f.ID, 10), ProjectID: id, Number: f.DisplayName, Type: curseForgeRelease(f.Release), Published: f.Published, GameVersions: f.GameVersions, Loaders: loaders, Dependencies: []pluginDependency{}}
				for _, d := range f.Dependencies {
					project.Version.Dependencies = append(project.Version.Dependencies, pluginDependency{Source: "CurseForge", ProjectID: strconv.FormatInt(d.ModID, 10), Type: curseForgeRelation(d.Relation)})
				}
				project.Compatible = curseForgeCompatible(*f, server)
				project.Compatibility = "Not listed as compatible according to publisher metadata"
				if javaServerContentKind(server.ServerType) == "plugins" && len(loaders) == 0 {
					project.Compatibility = "Plugin platform compatibility unknown; exact artifact validation required during Review."
				}
				if project.Compatible {
					project.Compatibility = "Compatible Minecraft/loader release. Dedicated-server eligibility is checked during Review."
				}
				if restriction := curseForgeInstallRestriction(p); restriction != "" {
					project.Compatibility = restriction
				} else if javaServerContentKind(server.ServerType) == "mods" {
					// Discover establishes candidate identity only. Exact Review
					// re-fetches and verifies all install-grade artifact evidence.
					project.ReviewAvailable = server.Supported && project.Compatible && f.Game == scope.Game
				} else {
					_, artifactErr := curseForgeArtifact(ctx, *f, id, server, scope)
					project.InstallAvailable = artifactErr == nil && server.Supported
					if artifactErr != nil {
						project.Compatibility = artifactErr.Error()
					}
				}
			}
			out.Projects = append(out.Projects, project)
			out.Positions = append(out.Positions, cursor)
		}
		if cursor >= out.Total {
			upstreamEnd = true
			break
		}
	}
	out.End = cursor
	if !upstreamEnd && cursor < out.Total && cursor < 10000 {
		out.NextOffset = cursor
	}
	return out, nil
}
func curseForgeRelation(n int) string {
	switch n {
	case 1:
		return "embedded"
	case 2:
		return "optional"
	case 3:
		return "required"
	case 4:
		return "tool"
	case 5:
		return "incompatible"
	case 6:
		return "include"
	}
	return "unknown"
}

func resolveCurseForgeInstallPlan(ctx context.Context, server pluginServerContext, dataPath, project, version string) (pluginInstallPlan, error) {
	target := javaServerContentKind(server.ServerType)
	plan := pluginInstallPlan{Source: "CurseForge", ProjectID: project, VersionID: version, Server: server, DataPath: dataPath, Location: filepath.Join(dataPath, target), RestartRequired: true, Compatibility: "Compatible Minecraft/loader release. Dedicated-server eligibility is checked during Review.", Artifacts: []pluginInstallArtifact{}, DisplayDependencies: []pluginDependency{}}
	if !server.Supported || target == "" || server.MinecraftVersion == "" || !filepath.IsAbs(dataPath) || !positiveCurseForgeID(project) || !positiveCurseForgeID(version) {
		return plan, errCurseForgeCatalog
	}
	scope, err := resolveCurseForgeContext(ctx, server)
	if err != nil {
		return plan, err
	}
	root, err := openContentDirectory(dataPath, target)
	if err != nil {
		return plan, errors.New("Content target must be an existing real directory")
	}
	defer root.Close()
	manifest, _, err := readPluginManifest(dataPath)
	if err != nil {
		return plan, err
	}
	var runtime []trustedModComponent
	if target == "mods" {
		runtime, err = runtimeOwnedModComponents(server, dataPath)
		if err != nil {
			return plan, err
		}
	}
	seen, active := map[string]int{}, map[string]bool{}
	filenames := map[string]bool{}
	var total int64
	var visit func(string, string, string, int) error
	visit = func(project, version, parent string, depth int) error {
		if depth > pluginMaxDepth {
			return errors.New("Required dependency depth exceeds the safe limit")
		}
		if !positiveCurseForgeID(project) || version != "" && !positiveCurseForgeID(version) {
			return errCurseForgeCatalog
		}
		key := pluginIdentity("CurseForge", project)
		if active[key] {
			return errors.New("Required dependency cycle detected")
		}
		if index, ok := seen[key]; ok {
			if version != "" && plan.Artifacts[index].VersionID != version {
				return errors.New("Conflicting dependency versions")
			}
			if parent != "" && !slices.Contains(plan.Artifacts[index].RequiredBy, parent) {
				plan.Artifacts[index].RequiredBy = append(plan.Artifacts[index].RequiredBy, parent)
			}
			return nil
		}
		if len(plan.Artifacts) >= pluginMaxArtifacts {
			return errors.New("Required dependency graph exceeds the safe artifact limit")
		}
		metadata, err := exactCurseForgeProject(ctx, project, scope)
		if err != nil {
			return err
		}
		if restriction := curseForgeInstallRestriction(metadata); restriction != "" {
			plan.Name, plan.Description, plan.Compatibility = metadata.Name, metadata.Summary, restriction
			return errPluginNotInstallable
		}
		var f curseForgeFile
		if version != "" {
			f, err = exactCurseForgeFile(ctx, project, version)
		} else {
			var selected *curseForgeFile
			selected, err = selectCurseForgeFile(ctx, project, server, true, true, false)
			if err == nil && selected == nil {
				return errors.New("No compatible required dependency release within bounded review")
			}
			if selected != nil {
				f = *selected
			}
		}
		if err != nil {
			return err
		}
		a, err := curseForgeArtifact(ctx, f, project, server, scope)
		if err != nil {
			return err
		}
		if target == "plugins" && !curseForgeCompatible(f, server) {
			plan.Compatibility = "Plugin platform compatibility unknown; exact JAR structure is checked before downtime and activation after restart."
		}
		a.Name, a.Requested = metadata.Name, parent == ""
		if parent != "" {
			a.RequiredBy = append(a.RequiredBy, parent)
		} else {
			plan.Name, plan.Description, plan.VersionNumber = metadata.Name, metadata.Summary, a.VersionNumber
		}
		if filenames[strings.ToLower(a.Filename)] {
			return errors.New("Required releases collide on a filename")
		}
		filenames[strings.ToLower(a.Filename)] = true
		total += a.Size
		if total > pluginMaxDownload {
			return errors.New("Combined download exceeds the safe limit")
		}
		if target == "plugins" && managedPluginIdentity(a.Filename, "") != "" {
			return errors.New("JustVoxel managed components use their existing server mechanism")
		}
		for _, e := range manifest.Artifacts {
			if pluginTarget(e.Target) != target || pluginIdentity(e.Source, e.ProjectID) != key {
				continue
			}
			if a.Requested {
				return errors.New("Managed content already installed")
			}
			if e.VersionID != a.VersionID || e.Filename != a.Filename {
				return errors.New("Installed dependency requires an unsupported update")
			}
			if err := verifyManagedArtifactAt(root, e); err != nil {
				return errors.New("Managed dependency differs from ownership evidence")
			}
			jar, err := openPluginAt(root, e.Filename)
			if err != nil {
				return err
			}
			h := sha1.New()
			n, hashErr := io.Copy(h, io.LimitReader(jar, a.Size+1))
			jar.Close()
			if hashErr != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA1 {
				return errors.New("Installed dependency differs from authoritative CurseForge file")
			}
			a.Satisfied, a.ExistingFilename, a.ExistingHash, a.PluginName, a.Main = "Managed content", e.Filename, e.SHA512, e.PluginName, e.Main
		}
		if !a.Requested && a.Satisfied == "" {
			for _, component := range runtime {
				jar, err := openPluginAt(root, component.Filename)
				if err != nil {
					return err
				}
				h := sha1.New()
				n, readErr := io.Copy(h, io.LimitReader(jar, a.Size+1))
				jar.Close()
				if readErr == nil && n == a.Size && hex.EncodeToString(h.Sum(nil)) == a.SHA1 {
					a.Satisfied, a.ExistingFilename, a.ExistingHash = runtimeModSatisfied, component.Filename, component.SHA512
				}
			}
		}
		if a.Satisfied == "" {
			var st unix.Stat_t
			if err := unix.Fstatat(int(root.Fd()), a.Filename, &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) && (err != nil || !slices.ContainsFunc(runtime, func(c trustedModComponent) bool { return c.Filename == a.Filename })) {
				return errors.New("Content target filename already exists or cannot be safely checked")
			}
		}
		seen[key] = len(plan.Artifacts)
		plan.Artifacts = append(plan.Artifacts, a)
		active[key] = true
		deps := slices.Clone(f.Dependencies)
		sort.Slice(deps, func(i, j int) bool {
			if deps[i].ModID == deps[j].ModID {
				return deps[i].Relation < deps[j].Relation
			}
			return deps[i].ModID < deps[j].ModID
		})
		for _, d := range deps {
			id := strconv.FormatInt(d.ModID, 10)
			if d.Relation == 3 {
				if err := visit(id, "", key, depth+1); err != nil {
					return err
				}
			} else {
				plan.DisplayDependencies = append(plan.DisplayDependencies, pluginDependency{Source: "CurseForge", ProjectID: id, Type: curseForgeRelation(d.Relation)})
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
