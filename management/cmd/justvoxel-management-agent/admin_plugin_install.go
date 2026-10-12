package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	pluginMaxArtifacts  = 16
	pluginMaxDepth      = 6
	pluginMaxJar        = 128 << 20
	pluginMaxDownload   = 256 << 20
	pluginManifestLimit = 1 << 20
)

type pluginArtifactFile struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
	Hashes   struct {
		SHA512 string `json:"sha512"`
	} `json:"hashes"`
}
type pluginInstallRequest struct {
	ProjectID string `json:"project_id"`
	VersionID string `json:"version_id"`
}
type pluginInstallApplyRequest struct {
	ProjectID       string `json:"project_id"`
	VersionID       string `json:"version_id"`
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}
type pluginInstallArtifact struct {
	ProjectID     string   `json:"project_id"`
	VersionID     string   `json:"version_id"`
	VersionNumber string   `json:"version_number"`
	Name          string   `json:"project_name"`
	Filename      string   `json:"filename"`
	Size          int64    `json:"size"`
	SHA512        string   `json:"-"`
	URL           string   `json:"-"`
	Requested     bool     `json:"requested"`
	RequiredBy    []string `json:"required_by"`
	Satisfied     string   `json:"satisfied,omitempty"`
	// Reuse evidence is fingerprinted independently of the CDN artifact.
	ExistingFilename string `json:"-"`
	ExistingHash     string `json:"-"`
	PluginName       string `json:"-"`
	Main             string `json:"-"`
}
type pluginInstallPlan struct {
	ProjectID            string                  `json:"project_id"`
	VersionID            string                  `json:"version_id"`
	Name                 string                  `json:"project_name"`
	Description          string                  `json:"description"`
	VersionNumber        string                  `json:"version_number"`
	Compatibility        string                  `json:"compatibility"`
	Server               pluginServerContext     `json:"server"`
	Location             string                  `json:"installation_location"`
	RestartRequired      bool                    `json:"restart_required"`
	Artifacts            []pluginInstallArtifact `json:"artifacts"`
	DisplayDependencies  []pluginDependency      `json:"display_dependencies"`
	Online               int                     `json:"online"`
	Players              []string                `json:"players"`
	ConfirmationRequired bool                    `json:"confirmation_required"`
	Fingerprint          string                  `json:"plan_fingerprint"`
	DataPath             string                  `json:"-"`
}
type pluginManifestEntry struct {
	ProjectID     string   `json:"project_id"`
	VersionID     string   `json:"version_id"`
	VersionNumber string   `json:"version_number"`
	Filename      string   `json:"filename"`
	SHA512        string   `json:"sha512"`
	PluginName    string   `json:"plugin_name"`
	Main          string   `json:"main_class,omitempty"`
	Requested     bool     `json:"requested"`
	RequiredBy    []string `json:"required_by"`
}
type pluginManifest struct {
	SchemaVersion string                `json:"schema_version"`
	Artifacts     []pluginManifestEntry `json:"artifacts"`
}

var pluginHashPattern = regexp.MustCompile(`^[0-9a-f]{128}$`)
var pluginNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var pluginMainPattern = regexp.MustCompile(`^[A-Za-z0-9_.]{1,512}$`)

func safePluginFilename(name string) bool {
	return len(name) > 4 && len(name) <= 200 && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00\r\n") && strings.EqualFold(filepath.Ext(name), ".jar")
}
func safePluginArtifactURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "cdn.modrinth.com" && u.User == nil && u.Fragment == "" && strings.HasPrefix(u.Path, "/data/") && u.RawQuery == "" && len(raw) <= 2048
}
func selectPluginArtifact(files []pluginArtifactFile) (pluginArtifactFile, error) {
	var jars, primary []pluginArtifactFile
	for _, f := range files {
		if f.Primary {
			primary = append(primary, f)
		}
		if safePluginFilename(f.Filename) {
			jars = append(jars, f)
		}
	}
	var f pluginArtifactFile
	switch {
	case len(primary) == 1:
		f = primary[0]
	case len(primary) == 0 && len(jars) == 1:
		f = jars[0]
	default:
		return f, errors.New("Release must identify exactly one unambiguous plugin JAR")
	}
	if !safePluginFilename(f.Filename) || !safePluginArtifactURL(f.URL) || f.Size <= 0 || f.Size > pluginMaxJar || !pluginHashPattern.MatchString(f.Hashes.SHA512) {
		return f, errors.New("Release has unsafe or incomplete plugin file metadata")
	}
	return f, nil
}
func managedPluginSlug(slug string) string {
	switch slug {
	case "geyser", "floodgate", "viaversion":
		return slug
	}
	return ""
}
func managedPluginIdentity(filename, main string) string {
	switch main {
	case "org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin":
		return "geyser"
	case "org.geysermc.floodgate.SpigotPlugin":
		return "floodgate"
	case "com.viaversion.viaversion.ViaVersionPlugin", "us.myles.ViaVersion.ViaVersionPlugin":
		return "viaversion"
	}
	switch strings.ToLower(filename) {
	case "geyser-spigot.jar":
		return "geyser"
	case "floodgate-spigot.jar":
		return "floodgate"
	case "viaversion.jar":
		return "viaversion"
	}
	return ""
}

func decodePluginInstallJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "Invalid plugin install request")
		return false
	}
	return true
}
func (s *server) resolvePluginInstall(w http.ResponseWriter, r *http.Request, project, version string) (pluginInstallPlan, error) {
	serverContext, path, ok := s.pluginContext(&pluginContextSink{header: make(http.Header)}, r)
	if !ok {
		return pluginInstallPlan{}, errors.New("Server context unavailable")
	}
	return resolvePluginInstallPlan(r.Context(), serverContext, path, project, version)
}
func (s *server) adminPluginInstallPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var request pluginInstallRequest
	if !decodePluginInstallJSON(w, r, &request) {
		return
	}
	if !modrinthPluginID.MatchString(request.ProjectID) || !modrinthPluginID.MatchString(request.VersionID) {
		writeError(w, 400, "Invalid Modrinth identity")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	plan, err := s.resolvePluginInstall(w, r.WithContext(ctx), request.ProjectID, request.VersionID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := reviewRootPluginArtifact(ctx, plan); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	online, names, err := pluginInstallPlayers(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Player status unavailable; Minecraft has not been changed")
		return
	}
	plan.Online, plan.Players, plan.ConfirmationRequired = online, names, online > 0
	writeJSON(w, http.StatusOK, plan)
}
func (s *server) adminPluginInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request pluginInstallApplyRequest
	if !decodePluginInstallJSON(w, r, &request) {
		return
	}
	if !modrinthPluginID.MatchString(request.ProjectID) || !modrinthPluginID.MatchString(request.VersionID) || !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeError(w, 400, "Invalid reviewed plugin install")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Plugin operation tracking unavailable")
		return
	}
	// An active transaction may already have placed its files. A duplicate is
	// a lookup, not a second apply; do not mistake those files for a new collision.
	current, err := s.operations.currentPluginInstall()
	if err != nil {
		writeError(w, 503, "Plugin operation tracking unavailable")
		return
	}
	if current != nil && current.PlanFingerprint == request.PlanFingerprint {
		writeJSON(w, http.StatusAccepted, adminOperationResponse{Operation: current})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	plan, err := s.resolvePluginInstall(w, r.WithContext(ctx), request.ProjectID, request.VersionID)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if plan.Fingerprint != request.PlanFingerprint {
		writeError(w, 409, "Plugin plan changed; review again")
		return
	}
	// Repeated requests return the existing worker, even if player state has changed.
	current, err = s.operations.currentPluginInstall()
	if err != nil {
		writeError(w, 503, "Plugin operation tracking unavailable")
		return
	}
	if current != nil && current.PlanFingerprint == plan.Fingerprint {
		writeJSON(w, http.StatusAccepted, adminOperationResponse{Operation: current})
		return
	}
	online, names, err := pluginInstallPlayers(ctx)
	if err != nil {
		writeError(w, 503, "Player status unavailable; Minecraft has not been changed")
		return
	}
	if online > 0 && !request.ConfirmPlayers {
		writeJSON(w, http.StatusConflict, minecraftActionResponse{ConfirmationRequired: true, Online: online, Players: names, Message: "Players joined; review and confirm the restart"})
		return
	}
	journal, created, err := s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
	if err != nil {
		writeError(w, 409, "Another Minecraft maintenance operation requires attention")
		return
	}
	writeJSON(w, http.StatusAccepted, adminOperationResponse{Operation: &journal})
	if created {
		startPluginInstallWorker(s, journal.OperationID, plan, request.ConfirmPlayers, actor)
	}
}
func (s *server) adminPluginInstallCurrent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Plugin operation tracking unavailable")
		return
	}
	journal, err := s.operations.currentPluginInstall()
	if err != nil {
		writeError(w, 503, "Plugin operation tracking unavailable")
		return
	}
	writeJSON(w, 200, adminOperationResponse{Operation: journal})
}

func resolvePluginInstallPlan(ctx context.Context, serverContext pluginServerContext, dataPath, projectID, versionID string) (pluginInstallPlan, error) {
	plan := pluginInstallPlan{ProjectID: projectID, VersionID: versionID, Server: serverContext, DataPath: dataPath, Location: filepath.Join(dataPath, "plugins"), RestartRequired: true, Compatibility: "Compatible according to publisher metadata", Artifacts: []pluginInstallArtifact{}, DisplayDependencies: []pluginDependency{}}
	if !serverContext.Supported || (serverContext.ServerType != "paper" && serverContext.ServerType != "purpur") || serverContext.MinecraftVersion == "" || !filepath.IsAbs(dataPath) || !modrinthPluginID.MatchString(projectID) || !modrinthPluginID.MatchString(versionID) {
		return plan, errors.New("A known Paper or Purpur Minecraft version and configured data path are required")
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
		if (project != "" && !modrinthPluginID.MatchString(project)) || (version != "" && !modrinthPluginID.MatchString(version)) || (project == "" && version == "") {
			return errors.New("Required dependency lacks a safe Modrinth identity")
		}
		var v pluginCatalogVersion
		if version != "" {
			if err := modrinthPluginJSON(ctx, "/version/"+version, nil, &v); err != nil {
				return errors.New("Required release metadata unavailable")
			}
			if v.ID != version || !modrinthPluginID.MatchString(v.ProjectID) || (project != "" && v.ProjectID != project) {
				return errors.New("Modrinth project/version identity mismatch")
			}
			if parent != "" && !pluginLoaderOverlap(v.Loaders, serverContext.ServerType) {
				plan.DisplayDependencies = append(plan.DisplayDependencies, pluginDependency{ProjectID: project, VersionID: version, Type: "required"})
				return nil
			}
		} else {
			var dependencyProject struct {
				ID      string   `json:"id"`
				Name    string   `json:"title"`
				Loaders []string `json:"loaders"`
			}
			if err := modrinthPluginJSON(ctx, "/project/"+project, nil, &dependencyProject); err != nil {
				return errors.New("Required dependency project metadata unavailable")
			}
			if dependencyProject.ID != project {
				return errors.New("Modrinth dependency project identity mismatch")
			}
			if len(dependencyProject.Loaders) > 0 && !pluginLoaderOverlap(dependencyProject.Loaders, serverContext.ServerType) {
				plan.DisplayDependencies = append(plan.DisplayDependencies, pluginDependency{ProjectID: project, Type: "required"})
				return nil
			}
			var versions []pluginCatalogVersion
			games, _ := json.Marshal([]string{serverContext.MinecraftVersion})
			loaders, _ := json.Marshal(pluginLoaders(serverContext.ServerType))
			if err := modrinthPluginJSON(ctx, "/project/"+project+"/version", url.Values{"game_versions": {string(games)}, "loaders": {string(loaders)}, "include_changelog": {"false"}}, &versions); err != nil {
				return errors.New("Required dependency release unavailable")
			}
			sort.Slice(versions, func(i, j int) bool {
				if versions[i].Published == versions[j].Published {
					return versions[i].ID < versions[j].ID
				}
				return versions[i].Published > versions[j].Published
			})
			found := false
			for _, candidate := range versions {
				if candidate.Type == "release" && pluginVersionCompatible(candidate, serverContext) {
					v = candidate
					found = true
					break
				}
			}
			if !found {
				name := dependencyProject.Name
				if name == "" {
					name = project
				}
				loader := "Paper"
				if serverContext.ServerType == "purpur" {
					loader = "Purpur"
				}
				return fmt.Errorf("Required dependency %s has no compatible %s release for Minecraft %s", name, loader, serverContext.MinecraftVersion)
			}
			if v.ProjectID != project {
				return errors.New("Modrinth dependency project/version identity mismatch")
			}
		}
		if !modrinthPluginID.MatchString(v.ID) || !modrinthPluginID.MatchString(v.ProjectID) || v.Type != "release" || !pluginVersionCompatible(v, serverContext) || v.Number == "" || len(v.Number) > 256 {
			return errors.New("Only compatible, identifiable release versions can be installed")
		}
		project = v.ProjectID
		if active[project] {
			return errors.New("Required dependency cycle detected")
		}
		if index, ok := seen[project]; ok {
			if plan.Artifacts[index].VersionID != v.ID {
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
		f, err := selectPluginArtifact(v.Files)
		if err != nil {
			return err
		}
		if filenames[strings.ToLower(f.Filename)] {
			return errors.New("Required releases collide on a filename")
		}
		filenames[strings.ToLower(f.Filename)] = true
		total += f.Size
		if total > pluginMaxDownload {
			return errors.New("Combined plugin download exceeds the safe limit")
		}
		var metadata struct {
			ID          string `json:"id"`
			Slug        string `json:"slug"`
			Name        string `json:"title"`
			Description string `json:"description"`
		}
		if err := modrinthPluginJSON(ctx, "/project/"+project, nil, &metadata); err != nil || metadata.ID != project || metadata.Name == "" {
			return errors.New("Plugin project metadata unavailable")
		}
		a := pluginInstallArtifact{ProjectID: project, VersionID: v.ID, VersionNumber: v.Number, Name: metadata.Name, Filename: f.Filename, Size: f.Size, SHA512: f.Hashes.SHA512, URL: f.URL, Requested: parent == "", RequiredBy: []string{}}
		if parent != "" {
			a.RequiredBy = append(a.RequiredBy, parent)
		}
		if parent == "" {
			plan.Name, plan.Description, plan.VersionNumber = metadata.Name, metadata.Description, v.Number
		}
		component := managedPluginSlug(metadata.Slug)
		if component == "" {
			component = managedPluginIdentity(f.Filename, "")
		}
		if component != "" {
			if a.Requested {
				return errors.New("This plugin is JustVoxel managed; use its existing server mechanism")
			}
			filename, hash, identity, err := findManagedPluginComponent(root, component)
			if err != nil {
				return err
			}
			a.Satisfied, a.ExistingFilename, a.ExistingHash, a.PluginName, a.Main = "JustVoxel managed", filename, hash, identity.Name, identity.Main
		} else {
			for _, entry := range manifest.Artifacts {
				if entry.ProjectID != project {
					continue
				}
				if a.Requested {
					return errors.New("Plugin already installed; plugin updates are not available in Stage 2")
				}
				if entry.VersionID != v.ID || entry.Filename != f.Filename || entry.SHA512 != a.SHA512 {
					return errors.New("Installed dependency requires an update; Stage 2 cannot upgrade dependencies")
				}
				hash, err := hashPluginAt(root, entry.Filename)
				if err != nil || hash != entry.SHA512 {
					return errors.New("Managed dependency no longer matches its ownership manifest")
				}
				jar, err := openPluginAt(root, entry.Filename)
				if err != nil {
					return errors.New("Managed dependency identity unavailable")
				}
				identity, err := inspectStagedPlugin(jar, entry.Filename)
				jar.Close()
				if err != nil || identity.Name != entry.PluginName || identity.Main != entry.Main {
					return errors.New("Managed dependency descriptor differs from its manifest")
				}
				a.Satisfied, a.ExistingFilename, a.ExistingHash, a.PluginName, a.Main = "Managed by Plugins", entry.Filename, hash, entry.PluginName, entry.Main
			}
		}
		if a.Satisfied == "" {
			var stat unix.Stat_t
			err := unix.Fstatat(int(root.Fd()), a.Filename, &stat, unix.AT_SYMLINK_NOFOLLOW)
			if err == nil || !errors.Is(err, unix.ENOENT) {
				return errors.New("Plugin target filename already exists or cannot be safely checked")
			}
		}
		seen[project] = len(plan.Artifacts)
		plan.Artifacts = append(plan.Artifacts, a)
		active[project] = true
		if len(v.Dependencies) > 128 {
			return errors.New("Release dependency metadata exceeds the safe limit")
		}
		sort.Slice(v.Dependencies, func(i, j int) bool {
			x, _ := json.Marshal(v.Dependencies[i])
			y, _ := json.Marshal(v.Dependencies[j])
			return string(x) < string(y)
		})
		for _, d := range v.Dependencies {
			if d.Type == "required" {
				displayStart := len(plan.DisplayDependencies)
				if err := visit(d.ProjectID, d.VersionID, project, depth+1); err != nil {
					return err
				}
				if len(plan.DisplayDependencies) == displayStart+1 {
					skipped := plan.DisplayDependencies[displayStart]
					if skipped.Type == "required" && skipped.ProjectID == d.ProjectID && skipped.VersionID == d.VersionID {
						plan.DisplayDependencies[displayStart] = d
					}
				}
			} else {
				plan.DisplayDependencies = append(plan.DisplayDependencies, d)
			}
		}
		delete(active, project)
		return nil
	}
	if err := visit(projectID, versionID, "", 0); err != nil {
		return plan, err
	}
	sort.Slice(plan.Artifacts, func(i, j int) bool { return plan.Artifacts[i].ProjectID < plan.Artifacts[j].ProjectID })
	for i := range plan.Artifacts {
		sort.Strings(plan.Artifacts[i].RequiredBy)
	}
	sort.Slice(plan.DisplayDependencies, func(i, j int) bool {
		a, _ := json.Marshal(plan.DisplayDependencies[i])
		b, _ := json.Marshal(plan.DisplayDependencies[j])
		return string(a) < string(b)
	})
	plan.DisplayDependencies = slices.CompactFunc(plan.DisplayDependencies, func(a, b pluginDependency) bool { return a == b })
	plan.Fingerprint = fingerprintPluginPlan(plan)
	return plan, nil
}

const pluginDirectReviewRejection = "This release is not a direct Paper/Bukkit plugin and cannot be installed into the plugins directory."

// Review only the exact requested artifact. Installation still inspects every
// staged artifact independently before downtime; review creates no operation.
func reviewRootPluginArtifact(ctx context.Context, plan pluginInstallPlan) error {
	for _, artifact := range plan.Artifacts {
		if !artifact.Requested {
			continue
		}
		directory, err := os.MkdirTemp("", "justvoxel-plugin-review-")
		if err != nil {
			return errors.New("Plugin review staging unavailable")
		}
		defer os.RemoveAll(directory)
		if err := downloadPluginArtifact(ctx, artifact, directory); err != nil {
			return err
		}
		file, err := os.Open(filepath.Join(directory, artifact.Filename))
		if err != nil {
			return errors.New("Plugin review artifact unavailable")
		}
		_, inspectErr := inspectStagedPlugin(file, artifact.Filename)
		closeErr := file.Close()
		if inspectErr != nil {
			return errors.New(pluginDirectReviewRejection)
		}
		return closeErr
	}
	return errors.New("Requested plugin artifact unavailable")
}

func pluginLoaderOverlap(loaders []string, serverType string) bool {
	for _, loader := range loaders {
		if slices.Contains(pluginLoaders(serverType), loader) {
			return true
		}
	}
	return false
}

func fingerprintPluginPlan(plan pluginInstallPlan) string {
	plan.Artifacts = slices.Clone(plan.Artifacts)
	sort.Slice(plan.Artifacts, func(i, j int) bool { return plan.Artifacts[i].ProjectID < plan.Artifacts[j].ProjectID })
	for i := range plan.Artifacts {
		plan.Artifacts[i].RequiredBy = slices.Clone(plan.Artifacts[i].RequiredBy)
		sort.Strings(plan.Artifacts[i].RequiredBy)
	}
	plan.DisplayDependencies = slices.Clone(plan.DisplayDependencies)
	sort.Slice(plan.DisplayDependencies, func(i, j int) bool {
		a, _ := json.Marshal(plan.DisplayDependencies[i])
		b, _ := json.Marshal(plan.DisplayDependencies[j])
		return string(a) < string(b)
	})
	type evidence struct {
		Artifact                                  pluginInstallArtifact
		URL, Hash, ExistingFilename, ExistingHash string
	}
	artifacts := []evidence{}
	for _, a := range plan.Artifacts {
		artifacts = append(artifacts, evidence{a, a.URL, a.SHA512, a.ExistingFilename, a.ExistingHash})
	}
	data, _ := json.Marshal(struct {
		Server, Version, Path, Project, Release string
		Artifacts                               []evidence
		Display                                 []pluginDependency
	}{plan.Server.ServerType, plan.Server.MinecraftVersion, plan.DataPath, plan.ProjectID, plan.VersionID, artifacts, plan.DisplayDependencies})
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func openPluginDirectory(dataPath string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(dataPath, "plugins"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "plugins"), nil
}
func openPluginAt(root *os.File, name string) (*os.File, error) {
	if !safePluginFilename(name) {
		return nil, errors.New("unsafe JAR filename")
	}
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > pluginMaxJar {
		f.Close()
		return nil, errors.New("unsafe JAR file")
	}
	return f, nil
}
func hashPluginAt(root *os.File, name string) (string, error) {
	f, err := openPluginAt(root, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha512.New()
	n, err := io.Copy(h, io.LimitReader(f, pluginMaxJar+1))
	if err != nil || n > pluginMaxJar {
		return "", errors.New("JAR hash unavailable")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func readPluginManifest(dataPath string) (pluginManifest, []byte, error) {
	empty := pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{}}
	fd, err := unix.Open(filepath.Join(dataPath, ".justvoxel"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return empty, nil, nil
	}
	if err != nil {
		return empty, nil, err
	}
	defer unix.Close(fd)
	var directoryStat unix.Stat_t
	if unix.Fstat(fd, &directoryStat) != nil || directoryStat.Uid != uint32(os.Geteuid()) || directoryStat.Mode&0022 != 0 {
		return empty, nil, errors.New("unsafe ownership directory permissions")
	}
	mfd, err := unix.Openat(fd, "plugins.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return empty, nil, nil
	}
	if err != nil {
		return empty, nil, err
	}
	f := os.NewFile(uintptr(mfd), "plugins.json")
	defer f.Close()
	var manifestStat unix.Stat_t
	if unix.Fstat(mfd, &manifestStat) != nil || manifestStat.Uid != uint32(os.Geteuid()) {
		return empty, nil, errors.New("unsafe plugin manifest owner")
	}
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > pluginManifestLimit || stat.Mode().Perm()&0022 != 0 {
		return empty, nil, errors.New("unsafe plugin manifest")
	}
	raw, err := io.ReadAll(io.LimitReader(f, pluginManifestLimit+1))
	if err != nil || len(raw) > pluginManifestLimit {
		return empty, nil, errors.New("plugin manifest too large")
	}
	var m pluginManifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || m.SchemaVersion != "v1" || m.Artifacts == nil || len(m.Artifacts) > 1024 {
		return empty, nil, errors.New("malformed plugin manifest")
	}
	projects, filenames := map[string]bool{}, map[string]bool{}
	for _, e := range m.Artifacts {
		if !modrinthPluginID.MatchString(e.ProjectID) || !modrinthPluginID.MatchString(e.VersionID) || e.VersionNumber == "" || len(e.VersionNumber) > 256 || !safePluginFilename(e.Filename) || !pluginHashPattern.MatchString(e.SHA512) || !pluginNamePattern.MatchString(e.PluginName) || managedPluginDescriptor(e.Filename, e.PluginName, e.Main) != "" || (e.Main != "" && !pluginMainPattern.MatchString(e.Main)) || projects[e.ProjectID] || filenames[strings.ToLower(e.Filename)] || len(e.RequiredBy) > 1024 {
			return empty, nil, errors.New("invalid plugin manifest identity")
		}
		for _, parent := range e.RequiredBy {
			if !modrinthPluginID.MatchString(parent) {
				return empty, nil, errors.New("invalid dependency relationship")
			}
		}
		projects[e.ProjectID], filenames[strings.ToLower(e.Filename)] = true, true
	}
	return m, raw, nil
}
func classifyGenericPluginInventory(dataPath string, f *os.File, item *installedPlugin) {
	if item.Management == "justvoxel" {
		return
	}
	manifest, _, err := readPluginManifest(dataPath)
	if err != nil {
		item.UpdateStatus = "Attention: plugin ownership manifest is unavailable or unsafe"
		return
	}
	for _, e := range manifest.Artifacts {
		if e.Filename != item.Filename {
			continue
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			item.UpdateStatus = "Attention: managed JAR could not be verified"
			return
		}
		hash := sha512.New()
		n, err := io.Copy(hash, io.LimitReader(f, pluginMaxJar+1))
		if err != nil || n > pluginMaxJar || hex.EncodeToString(hash.Sum(nil)) != e.SHA512 {
			item.UpdateStatus = "Attention: JAR differs from plugin ownership manifest; manual / unmanaged"
			return
		}
		if item.Name != e.PluginName || item.Main != e.Main {
			item.UpdateStatus = "Attention: plugin descriptor differs from ownership manifest; manual / unmanaged"
			return
		}
		item.Management = "plugins"
		item.ProjectID = e.ProjectID
		item.VersionID = e.VersionID
		item.UpdateStatus = "Managed by Plugins; update and removal are not available yet"
		return
	}
}

// Staged descriptors are stricter than read-only inventory: ambiguous descriptors
// cannot become installation or rollback identity evidence.
func inspectStagedPlugin(file *os.File, filename string) (installedPlugin, error) {
	stat, err := file.Stat()
	if err != nil || stat.Size() > pluginMaxJar || !pluginZipDirectoryBounded(file, stat.Size()) {
		return installedPlugin{}, errors.New("Malformed or oversized plugin JAR")
	}
	archive, err := zip.NewReader(file, stat.Size())
	if err != nil {
		return installedPlugin{}, err
	}
	var identity installedPlugin
	found := false
	for _, wanted := range []string{"paper-plugin.yml", "plugin.yml"} {
		count := 0
		for _, entry := range archive.File {
			if entry.Name != wanted {
				continue
			}
			count++
			if count > 1 || entry.UncompressedSize64 > pluginMetadataLimit {
				return identity, errors.New("Ambiguous or oversized plugin descriptor")
			}
			r, err := entry.Open()
			if err != nil {
				return identity, err
			}
			data, err := io.ReadAll(io.LimitReader(r, pluginMetadataLimit+1))
			closeErr := r.Close()
			if err != nil || closeErr != nil || len(data) > pluginMetadataLimit {
				return identity, errors.New("Unreadable plugin descriptor")
			}
			fields := pluginMetadataScalars(string(data))
			name := fields["name"]
			if !pluginNamePattern.MatchString(name) {
				return identity, errors.New("Plugin descriptor lacks a usable plugin name")
			}
			main := ""
			mainCount := 0
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "main:") {
					mainCount++
					match := managedPluginMain.FindStringSubmatch(line)
					if match == nil {
						return identity, errors.New("Malformed plugin main class")
					}
					main = match[1]
				}
			}
			if mainCount > 1 {
				return identity, errors.New("Ambiguous plugin main class")
			}
			if found && (identity.Name != name || (identity.Main != "" && main != "" && identity.Main != main)) {
				return identity, errors.New("Conflicting Paper/Bukkit plugin identities")
			}
			if !found {
				identity = installedPlugin{Name: name, Version: fields["version"], Filename: filename, Management: "manual"}
			}
			if main != "" {
				identity.Main = main
			}
			found = true
		}
	}
	if !found {
		return identity, errors.New("JAR has no Paper/Bukkit plugin descriptor")
	}
	classifyPluginManagement(&identity, identity.Main)
	return identity, nil
}
func findManagedPluginComponent(root *os.File, component string) (string, string, installedPlugin, error) {
	entries, err := root.Readdirnames(-1)
	if err != nil {
		return "", "", installedPlugin{}, err
	}
	if _, err := root.Seek(0, io.SeekStart); err != nil {
		return "", "", installedPlugin{}, err
	}
	var result installedPlugin
	hash, filename := "", ""
	for _, name := range entries {
		if !safePluginFilename(name) {
			continue
		}
		f, err := openPluginAt(root, name)
		if err != nil {
			continue
		}
		var identity installedPlugin
		canonical := managedPluginIdentity(name, "")
		if canonical != "" {
			// Canonical ownership is authoritative even when metadata is complex.
			if canonical != component {
				f.Close()
				continue
			}
			identity = installedPlugin{Filename: name, Management: "justvoxel"}
		} else {
			identity, err = inspectStagedPlugin(f, name)
			if err != nil || managedPluginIdentity("", identity.Main) != component {
				f.Close()
				continue
			}
		}
		if filename != "" {
			f.Close()
			return "", "", result, errors.New("Multiple copies of a JustVoxel component require attention")
		}
		// Hash the same safely opened regular file used for ownership discovery.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return "", "", result, err
		}
		h := sha512.New()
		n, hashErr := io.Copy(h, io.LimitReader(f, pluginMaxJar+1))
		closeErr := f.Close()
		if hashErr != nil || closeErr != nil || n <= 0 || n > pluginMaxJar {
			return "", "", result, errors.New("JAR hash unavailable")
		}
		filename, result, hash = name, identity, hex.EncodeToString(h.Sum(nil))
	}
	if filename == "" {
		return "", "", result, errors.New("Required JustVoxel-managed component is not available")
	}
	return filename, hash, result, nil
}

var pluginInstallPlayers = func(ctx context.Context) (int, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, code, err := runWebHelper(ctx, "players")
	var view struct {
		Configured bool     `json:"configured"`
		State      string   `json:"state"`
		Online     *int     `json:"online"`
		Names      []string `json:"names"`
	}
	if err != nil || code != 0 || len(raw) > 64<<10 || json.Unmarshal(raw, &view) != nil || !view.Configured || view.Online == nil || *view.Online < 0 || *view.Online > 10000 || (view.State != "running" && view.State != "stopped") || (view.State == "stopped" && *view.Online != 0) {
		return 0, nil, errors.New("Player status unavailable")
	}
	return *view.Online, view.Names, nil
}

var pluginDownloadClient = &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func downloadPluginArtifact(ctx context.Context, a pluginInstallArtifact, directory string) error {
	if !safePluginArtifactURL(a.URL) || !safePluginFilename(a.Filename) || a.Size <= 0 || a.Size > pluginMaxJar || !pluginHashPattern.MatchString(a.SHA512) {
		return errors.New("Unsafe artifact metadata")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "JustVoxel/PluginWorkspace-Stage2")
	response, err := pluginDownloadClient.Do(req)
	if err != nil {
		return errors.New("Plugin download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || (response.ContentLength >= 0 && response.ContentLength != a.Size) {
		return errors.New("Plugin download size or response mismatch")
	}
	f, err := os.OpenFile(filepath.Join(directory, a.Filename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha512.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(response.Body, a.Size+1))
	if err != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA512 {
		return errors.New("Plugin download hash or size verification failed")
	}
	return f.Sync()
}

// All execution remains in the privileged agent. Output is bounded and private.
var pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(os.Environ(), "JUSTVOXEL_MAINTENANCE_LOCK_HELD=1")
	var output pluginBoundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.Bytes(), err
}

type pluginBoundedOutput struct{ bytes.Buffer }

func (b *pluginBoundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 64<<10 {
		b.Buffer.Write(p[:min(n, (64<<10)-b.Len())])
	}
	return n, nil
}

var pluginRuntimeLockPath = "/run/justvoxel-minecraft-maintenance.lock"

func acquirePluginRuntimeLock() (*os.File, error) {
	fd, err := unix.Open(pluginRuntimeLockPath, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "minecraft-maintenance")
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("Unsafe Minecraft maintenance lock")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("Minecraft maintenance lock is busy")
	}
	return f, nil
}
func pluginServiceActive(ctx context.Context) (bool, error) {
	out, err := pluginInstallCommand(ctx, "systemctl", "is-active", "minecraft.service")
	switch strings.TrimSpace(string(out)) {
	case "active":
		return err == nil, err
	case "inactive":
		return false, nil
	default:
		return false, errors.New("Minecraft service state is unavailable")
	}
}
func pluginService(ctx context.Context, action string) error {
	_, err := pluginInstallCommand(ctx, "systemctl", action, "minecraft.service")
	return err
}
func verifyPluginRuntime(ctx context.Context, version string) error {
	deadline := time.NewTimer(900 * time.Second)
	defer deadline.Stop()
	for {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := pluginInstallCommand(probe, "podman", "exec", "minecraft", "rcon-cli", "list")
		active, stateErr := pluginServiceActive(probe)
		cancel()
		if err == nil && stateErr == nil && active {
			break
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return errors.New("Minecraft runtime readiness timed out")
		case <-timer.C:
		}
	}
	verify, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err := pluginInstallCommand(verify, "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", version)
	return err
}

// Startup proof is confined to the last initialization segment in latest.log.
const pluginStartupLogLimit = 16 << 20
const pluginStartupLineLimit = 16 << 10

var pluginStartupPrefix = regexp.MustCompile(`^(?:\[[0-9: .-]{1,64}\] )?\[[^\]\r\n]{1,256}\]:\s*`)
var pluginStartupInventory = regexp.MustCompile(`^\[PluginInitializerManager\] (Paper|Bukkit) plugins \(([0-9]+)\):$`)
var pluginStartupEntry = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.-]{0,127}) \(([^\r\n]{1,256})\)$`)

func splitPluginStartupEntries(line string) ([]string, bool) {
	var entries []string
	start, depth := 0, 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 || (depth == 0 && i+1 < len(line) && !strings.HasPrefix(line[i+1:], ", ")) {
				return nil, false
			}
		case ',':
			if depth == 0 {
				if !strings.HasPrefix(line[i:], ", ") {
					return nil, false
				}
				entries = append(entries, line[start:i])
				i++
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	return append(entries, line[start:]), true
}

func pluginStartupNamesLoaded(raw []byte, expected []string) bool {
	if len(raw) > pluginStartupLogLimit || !utf8.Valid(raw) || len(raw) == 0 || raw[len(raw)-1] != '\n' || len(expected) == 0 || len(expected) > pluginMaxArtifacts {
		return false
	}
	wanted := map[string]bool{}
	for _, name := range expected {
		if !pluginNamePattern.MatchString(name) || wanted[name] {
			return false
		}
		wanted[name] = true
	}
	inventory, enabled := map[string]bool{}, map[string]bool{}
	groups := map[string]bool{}
	started, valid := false, true
	inventoryList := false
	remaining := 0
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), pluginStartupLineLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(pluginStartupPrefix.ReplaceAllString(scanner.Text(), ""))
		if line == "[PluginInitializerManager] Initializing plugins..." {
			started, valid, remaining = true, true, 0
			inventoryList = false
			inventory, enabled, groups = map[string]bool{}, map[string]bool{}, map[string]bool{}
			continue
		}
		if !started {
			continue
		}
		if strings.ContainsAny(line, "\x00") {
			valid = false
		}
		if strings.HasPrefix(line, "[PluginInitializerManager] Paper plugins") || strings.HasPrefix(line, "[PluginInitializerManager] Bukkit plugins") {
			match := pluginStartupInventory.FindStringSubmatch(line)
			if match == nil || remaining != 0 {
				valid = false
				continue
			}
			count, err := strconv.Atoi(match[2])
			if err != nil || count > 4096 || groups[match[1]] {
				valid = false
				continue
			}
			groups[match[1]], remaining = true, count
			inventoryList = true
			continue
		}
		if remaining > 0 {
			if !strings.HasPrefix(line, "- ") {
				valid = false
				remaining = 0
			} else {
				entries, balanced := splitPluginStartupEntries(strings.TrimPrefix(line, "- "))
				if !balanced {
					valid = false
				}
				for _, entry := range entries {
					match := pluginStartupEntry.FindStringSubmatch(entry)
					if match == nil || remaining <= 0 {
						valid = false
						continue
					}
					if inventory[match[1]] {
						valid = false
					}
					inventory[match[1]] = true
					remaining--
				}
				continue
			}
		} else if inventoryList && strings.HasPrefix(line, "- ") {
			valid = false
		} else {
			inventoryList = false
		}
		for name := range wanted {
			logger := "[" + name + "] "
			if strings.HasPrefix(line, logger+"Enabling ") {
				if enabled[name] || strings.TrimSpace(strings.TrimPrefix(line, logger+"Enabling ")) == "" {
					valid = false
				}
				enabled[name] = true
			}
			// Paper's standard failure messages identify the plugin by logger or
			// by the name immediately following the load/enable failure phrase.
			lower := strings.ToLower(line)
			if strings.HasPrefix(line, logger) {
				message := strings.ToLower(strings.TrimPrefix(line, logger))
				for _, failure := range []string{"error ", "exception", "failed ", "could not ", "disabling "} {
					if strings.HasPrefix(message, failure) {
						valid = false
					}
				}
			}
			for _, phrase := range []string{"Error occurred while enabling ", "Error occurred while loading ", "Could not load plugin ", "Could not enable plugin ", "Failed to load plugin ", "Failed to enable plugin "} {
				if at := strings.Index(lower, strings.ToLower(phrase)); at >= 0 {
					tail := strings.TrimLeft(line[at+len(phrase):], "'\"")
					if tail == name || strings.HasPrefix(tail, name+" ") || strings.HasPrefix(tail, name+"'") || strings.HasPrefix(tail, name+"\"") || strings.HasPrefix(tail, name+":") {
						valid = false
					}
				}
			}
		}
	}
	if scanner.Err() != nil || !started || !valid || remaining != 0 {
		return false
	}
	for name := range wanted {
		if !inventory[name] || !enabled[name] {
			return false
		}
	}
	return true
}

func openPluginStartupLogs(dataPath string) (int, error) {
	if !filepath.IsAbs(dataPath) || filepath.Clean(dataPath) != dataPath {
		return -1, errors.New("unsafe data path")
	}
	// Walk every directory without following symlinks, then pin logs and the file.
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Join(dataPath, "logs"), "/"), "/") {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return -1, openErr
		}
		fd = next
	}
	return fd, nil
}

func verifyPluginStartup(dataPath string, expected []string) error {
	failure := errors.New("Installed plugin runtime state could not be verified")
	fd, err := openPluginStartupLogs(dataPath)
	if err != nil {
		return failure
	}
	defer unix.Close(fd)
	logFD, err := unix.Openat(fd, "latest.log", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return failure
	}
	file := os.NewFile(uintptr(logFD), "latest.log")
	defer file.Close()
	var initial unix.Stat_t
	if unix.Fstat(logFD, &initial) != nil || initial.Mode&unix.S_IFMT != unix.S_IFREG || initial.Size > pluginStartupLogLimit {
		return failure
	}
	raw, err := io.ReadAll(io.LimitReader(file, pluginStartupLogLimit+1))
	if err != nil || !pluginStartupNamesLoaded(raw, expected) {
		return failure
	}
	// Refuse a replaced log or directory while proof was being read.
	currentFD, err := openPluginStartupLogs(dataPath)
	if err != nil {
		return failure
	}
	defer unix.Close(currentFD)
	var directory, currentDirectory unix.Stat_t
	if unix.Fstat(fd, &directory) != nil || unix.Fstat(currentFD, &currentDirectory) != nil || directory.Dev != currentDirectory.Dev || directory.Ino != currentDirectory.Ino {
		return failure
	}
	var current unix.Stat_t
	if unix.Fstatat(fd, "latest.log", &current, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return failure
	}
	var opened unix.Stat_t
	if unix.Fstat(logFD, &opened) != nil || !pluginStartupSnapshotStable(initial, current, opened, int64(len(raw))) {
		return failure
	}
	return nil
}

func pluginStartupSnapshotStable(initial, current, opened unix.Stat_t, snapshotSize int64) bool {
	// Proof uses the completed bounded snapshot. Appends are safe, but observed
	// truncation, replacement, and growth beyond the log bound are not.
	if snapshotSize <= 0 || snapshotSize > pluginStartupLogLimit || initial.Size > snapshotSize {
		return false
	}
	for _, stat := range []unix.Stat_t{initial, current, opened} {
		if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Dev != initial.Dev || stat.Ino != initial.Ino || stat.Size < 0 || stat.Size > pluginStartupLogLimit {
			return false
		}
	}
	return current.Size >= snapshotSize && opened.Size >= current.Size
}

var startPluginInstallWorker = func(s *server, id string, plan pluginInstallPlan, confirm bool, actor session) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		_ = executePluginInstall(ctx, s, id, plan, confirm, actor)
	}()
}

// Durable transaction evidence is private, independent of browser-safe journals.
// It records all potential placements before touching the plugin directory.
type pluginTransaction struct {
	DataPath          string                `json:"data_path,omitempty"`
	MinecraftVersion  string                `json:"minecraft_version,omitempty"`
	SchemaVersion     string                `json:"schema_version"`
	OperationID       string                `json:"operation_id"`
	PlanFingerprint   string                `json:"plan_fingerprint"`
	OriginallyRunning bool                  `json:"originally_running"`
	Artifacts         []pluginManifestEntry `json:"artifacts"`
}

// Preflight records creation before any Minecraft/service/plugin mutation. It
// deliberately contains no original service-state claim.
type pluginPreflight struct {
	SchemaVersion    string `json:"schema_version"`
	OperationID      string `json:"operation_id"`
	PlanFingerprint  string `json:"plan_fingerprint"`
	DataPath         string `json:"data_path"`
	MinecraftVersion string `json:"minecraft_version"`
	ProjectID        string `json:"project_id"`
	VersionID        string `json:"version_id"`
}

var pluginEvidenceSync = (*os.File).Sync

func openPluginEvidenceDirectory(directory string) (*os.File, error) {
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), directory)
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
		f.Close()
		return nil, errors.New("Unsafe transaction directory")
	}
	return f, nil
}

// Publish only complete, synced evidence into the pinned private directory.
// NOREPLACE preserves existing evidence, including an unsafe existing entry.
func publishPluginEvidence(directory *os.File, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > pluginManifestLimit || (name == "preflight.json" && len(raw) > 4096) {
		return errors.New("Transaction evidence too large")
	}
	unique, err := newOperationID()
	if err != nil {
		return err
	}
	temporary := "." + name + "-" + unique + ".tmp"
	fd, err := unix.Openat(int(directory.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(directory.Fd()), temporary, 0)
	f := os.NewFile(uintptr(fd), temporary)
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = pluginEvidenceSync(f)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := unix.Renameat2(int(directory.Fd()), temporary, int(directory.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return pluginEvidenceSync(directory)
}

func persistPluginTransaction(directory string, tx pluginTransaction) error {
	d, err := openPluginEvidenceDirectory(directory)
	if err != nil {
		return err
	}
	defer d.Close()
	return publishPluginEvidence(d, "transaction.json", tx)
}
func executePluginInstall(ctx context.Context, s *server, id string, plan pluginInstallPlan, confirm bool, actor session) (result error) {
	succeeded := false
	defer func() {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "plugin_install", plan.ProjectID, succeeded, "Third-party plugin installation")
		}
	}()
	failUntouched := func(cause error) error {
		journal, err := s.operations.get(id)
		if err != nil {
			return errors.Join(cause, err)
		}
		if journal.State == operationQueued {
			// Keep pre-evidence failure atomic: an intermediate failed journal
			// could otherwise restart without evidence and retain exclusion.
			_, err = s.operations.transition(id, operationRolledBack, "complete", "Plugin installation cancelled safely: "+cause.Error()+". Minecraft and plugin files were left untouched.")
			return errors.Join(cause, err)
		}
		_, err = s.operations.transition(id, operationFailed, "rollback", "Plugin installation failed before file changes. Minecraft and plugin files were left untouched.")
		if err == nil {
			_, err = s.operations.transition(id, operationRollingBack, "rollback", "No plugin changes need rollback.")
		}
		if err == nil {
			_, err = s.operations.transition(id, operationRolledBack, "complete", "Plugin installation cancelled safely: "+cause.Error()+". Plugin files were left untouched.")
		}
		return errors.Join(cause, err)
	}
	stageRoot := filepath.Join(s.operations.baseDir, "plugin-install")
	if err := ensurePrivateDirectory(stageRoot); err != nil {
		return failUntouched(errors.New("Private staging unavailable"))
	}
	directory := filepath.Join(stageRoot, id)
	if err := os.Mkdir(directory, 0700); err != nil {
		return failUntouched(errors.New("Private transaction staging unavailable"))
	}
	defer cleanupTerminalPluginStaging(s, id, actor)
	evidenceDirectory, err := openPluginEvidenceDirectory(directory)
	if err != nil {
		return failUntouched(errors.New("Private transaction evidence unavailable"))
	}
	defer evidenceDirectory.Close()
	preflight := pluginPreflight{SchemaVersion: "v1", OperationID: id, PlanFingerprint: plan.Fingerprint, DataPath: plan.DataPath, MinecraftVersion: plan.Server.MinecraftVersion, ProjectID: plan.ProjectID, VersionID: plan.VersionID}
	if err := publishPluginEvidence(evidenceDirectory, "preflight.json", preflight); err != nil {
		return failUntouched(errors.New("Pre-mutation evidence could not be saved"))
	}
	// Persist the staging directory entry as well as its evidence contents.
	parent, err := openPluginEvidenceDirectory(stageRoot)
	if err != nil {
		return failUntouched(err)
	}
	err = pluginEvidenceSync(parent)
	closeErr := parent.Close()
	if err != nil || closeErr != nil {
		return failUntouched(errors.Join(err, closeErr))
	}
	// Persist creation of the staging root itself in the operation-store base.
	base, err := openPluginEvidenceDirectory(s.operations.baseDir)
	if err != nil {
		return failUntouched(err)
	}
	err = pluginEvidenceSync(base)
	closeErr = base.Close()
	if err != nil || closeErr != nil {
		return failUntouched(errors.Join(err, closeErr))
	}
	// Queued remains authoritative proof of no runtime mutation until all
	// preflight evidence and its directory entries are durable.
	if _, err := s.operations.transition(id, operationValidating, "validating", "Validating the reviewed plugin installation."); err != nil {
		return failUntouched(err)
	}
	if _, err := s.operations.transition(id, operationRunning, "downloading", "Downloading and verifying plugin JARs before Minecraft downtime."); err != nil {
		return err
	}
	for i := range plan.Artifacts {
		a := &plan.Artifacts[i]
		if a.Satisfied != "" {
			continue
		}
		if err := downloadPluginArtifact(ctx, *a, directory); err != nil {
			return failUntouched(err)
		}
		f, err := os.Open(filepath.Join(directory, a.Filename))
		if err != nil {
			return failUntouched(errors.New("Staged JAR unavailable"))
		}
		identity, err := inspectStagedPlugin(f, a.Filename)
		f.Close()
		if err != nil {
			return failUntouched(err)
		}
		a.PluginName, a.Main = identity.Name, identity.Main
		if component := managedPluginDescriptor(a.Filename, a.PluginName, a.Main); component != "" {
			if a.Requested {
				return failUntouched(errors.New("Requested JAR is a JustVoxel-managed component"))
			}
			root, err := openPluginDirectory(plan.DataPath)
			if err != nil {
				return failUntouched(err)
			}
			filename, hash, existing, err := findManagedPluginComponent(root, component)
			root.Close()
			if err != nil {
				return failUntouched(err)
			}
			a.Satisfied, a.ExistingFilename, a.ExistingHash, a.PluginName, a.Main = "JustVoxel managed", filename, hash, existing.Name, existing.Main
		}
	}
	// Re-resolve context without a browser session using the existing discovery helper.
	if err := revalidatePluginInstall(ctx, s, plan); err != nil {
		return failUntouched(err)
	}
	root, err := openPluginDirectory(plan.DataPath)
	if err != nil {
		return failUntouched(errors.New("Plugin directory unavailable"))
	}
	defer root.Close()
	manifest, manifestRaw, err := readPluginManifest(plan.DataPath)
	if err != nil {
		return failUntouched(errors.New("Ownership manifest unsafe"))
	}
	if err := checkPluginInstallCollisions(root, plan); err != nil {
		return failUntouched(err)
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		return failUntouched(err)
	}
	defer lock.Close()
	// CLI maintenance may have completed during download. Recheck after its lock.
	if err := revalidatePluginInstall(ctx, s, plan); err != nil {
		return failUntouched(err)
	}
	if err := checkPluginDirectoryIdentity(plan.DataPath, root); err != nil {
		return failUntouched(err)
	}
	if err := checkPluginInstallCollisions(root, plan); err != nil {
		return failUntouched(err)
	}
	originallyRunning, err := pluginServiceActive(ctx)
	if err != nil {
		return failUntouched(err)
	}
	tx := pluginTransaction{DataPath: plan.DataPath, MinecraftVersion: plan.Server.MinecraftVersion, SchemaVersion: "v1", OperationID: id, PlanFingerprint: plan.Fingerprint, OriginallyRunning: originallyRunning, Artifacts: []pluginManifestEntry{}}
	for _, a := range plan.Artifacts {
		if a.Satisfied == "" {
			tx.Artifacts = append(tx.Artifacts, pluginManifestArtifact(a))
		}
	}
	if err := s.operations.pluginProgress(id, "player_check", "Checking current players immediately before the cold backup."); err != nil {
		return failUntouched(err)
	}
	online, _, err := pluginInstallPlayers(ctx)
	if err != nil {
		return failUntouched(errors.New("Final player status unavailable"))
	}
	if online > 0 && !confirm {
		return failUntouched(errors.New("A player joined; review and confirm the restart"))
	}
	// minecraft-backup may stop Minecraft: durable evidence precedes this boundary.
	if err := publishPluginEvidence(evidenceDirectory, "transaction.json", tx); err != nil {
		return failUntouched(errors.New("Transaction evidence could not be saved"))
	}
	if err := s.operations.pluginProgress(id, "backup", "Creating the existing verified cold Minecraft backup."); err != nil {
		return failUntouched(err)
	}
	created := []pluginInstallArtifact{}
	rollback := func(cause error) error {
		return rollbackPluginInstall(s, id, plan, root, created, originallyRunning, cause)
	}
	_, err = pluginInstallCommand(ctx, "/usr/libexec/justvoxel/minecraft-backup", "--leave-stopped")
	if err != nil {
		return rollback(errors.New("Cold backup failed or did not create a verified archive; no plugins were installed"))
	}
	if active, err := pluginServiceActive(ctx); err != nil || active {
		return rollback(errors.New("Minecraft did not stop after the cold backup"))
	}
	if err := checkPluginDirectoryIdentity(plan.DataPath, root); err != nil {
		return rollback(err)
	}
	if err := checkPluginInstallCollisions(root, plan); err != nil {
		return rollback(err)
	}
	if err := s.operations.pluginProgress(id, "installing", "Placing verified plugin JARs without replacing existing files."); err != nil {
		return rollback(err)
	}
	for _, a := range plan.Artifacts {
		if a.Satisfied != "" {
			continue
		}
		placed, err := placePluginArtifact(root, directory, id, a)
		if placed {
			created = append(created, a)
		}
		if err != nil {
			return rollback(errors.New("Atomic plugin placement failed"))
		}
	}
	if err := s.operations.pluginProgress(id, "restarting", "Starting Minecraft for runtime verification."); err != nil {
		return rollback(err)
	}
	if err := pluginService(ctx, "start"); err != nil {
		return rollback(errors.New("Minecraft could not start"))
	}
	if _, err := s.operations.transition(id, operationVerifying, "verifying", "Verifying Minecraft and installed plugin runtime state."); err != nil {
		return rollback(err)
	}
	if err := verifyPluginRuntime(ctx, plan.Server.MinecraftVersion); err != nil {
		return rollback(errors.New("Existing Minecraft stack verification failed"))
	}
	expected := []string{}
	for _, a := range created {
		expected = append(expected, a.PluginName)
	}
	if err := verifyPluginStartup(plan.DataPath, expected); err != nil {
		return rollback(err)
	}
	if !originallyRunning {
		if err := pluginService(ctx, "stop"); err != nil {
			return rollback(errors.New("Minecraft could not return to its original stopped state"))
		}
		if active, err := pluginServiceActive(ctx); err != nil || active {
			return rollback(errors.New("Original stopped state could not be verified"))
		}
	}
	if err := checkPluginDirectoryIdentity(plan.DataPath, root); err != nil {
		return rollback(err)
	}
	for _, a := range created {
		hash, err := hashPluginAt(root, a.Filename)
		if err != nil || hash != a.SHA512 {
			return rollback(errors.New("Installed plugin changed during verification"))
		}
	}
	for _, a := range plan.Artifacts {
		if a.Satisfied != "" {
			hash, err := hashPluginAt(root, a.ExistingFilename)
			if err != nil || hash != a.ExistingHash {
				return rollback(errors.New("Satisfied dependency changed during verification"))
			}
		}
	}
	for _, a := range plan.Artifacts {
		if a.Satisfied == "JustVoxel managed" {
			continue
		}
		matched := false
		for i := range manifest.Artifacts {
			e := &manifest.Artifacts[i]
			if e.ProjectID == a.ProjectID {
				matched = true
				for _, parent := range a.RequiredBy {
					if !slices.Contains(e.RequiredBy, parent) {
						e.RequiredBy = append(e.RequiredBy, parent)
					}
				}
				sort.Strings(e.RequiredBy)
			}
		}
		if !matched {
			manifest.Artifacts = append(manifest.Artifacts, pluginManifestArtifact(a))
		}
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool { return manifest.Artifacts[i].ProjectID < manifest.Artifacts[j].ProjectID })
	committed, err := commitPluginManifest(plan.DataPath, manifestRaw, manifest)
	if err != nil {
		if committed {
			_, journalErr := s.operations.transition(id, operationNeedsAttention, "interrupted", "Plugin runtime verified, but ownership commit durability could not be proven. Administrator attention is required.")
			return errors.Join(err, journalErr)
		}
		return rollback(errors.New("Plugin ownership manifest could not be committed"))
	}
	status := "Plugin installed. Minecraft runtime verified; plugin verified active. Minecraft is running."
	if !originallyRunning {
		status = "Plugin installed. Minecraft runtime verified; plugin verified active. Minecraft returned to its original stopped state."
	}
	if _, err := s.operations.transition(id, operationSucceeded, "complete", status); err != nil {
		return err
	}
	succeeded = true
	return nil
}
func pluginManifestArtifact(a pluginInstallArtifact) pluginManifestEntry {
	return pluginManifestEntry{a.ProjectID, a.VersionID, a.VersionNumber, a.Filename, a.SHA512, a.PluginName, a.Main, a.Requested, a.RequiredBy}
}
func revalidatePluginInstall(ctx context.Context, s *server, plan pluginInstallPlan) error {
	// pluginContext remains the single authoritative context implementation.
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/", nil)
	recorder := &pluginContextSink{header: make(http.Header)}
	serverContext, dataPath, ok := s.pluginContext(recorder, request)
	if !ok {
		return errors.New("Authoritative server context unavailable")
	}
	current, err := resolvePluginInstallPlan(ctx, serverContext, dataPath, plan.ProjectID, plan.VersionID)
	if err != nil || current.Fingerprint != plan.Fingerprint {
		return errors.New("Plugin plan or server context changed; review again")
	}
	return nil
}
func checkPluginDirectoryIdentity(dataPath string, root *os.File) error {
	current, err := openPluginDirectory(dataPath)
	if err != nil {
		return errors.New("Plugin directory changed")
	}
	defer current.Close()
	a, err := root.Stat()
	if err != nil {
		return err
	}
	b, err := current.Stat()
	if err != nil || !os.SameFile(a, b) {
		return errors.New("Plugin directory changed")
	}
	return nil
}
func checkPluginInstallCollisions(root *os.File, plan pluginInstallPlan) error {
	// A fresh descriptor-bound inventory detects identity collisions, including a
	// manually renamed JAR. Unknown descriptors block safely rather than guessing.
	names, err := root.Readdirnames(-1)
	if err != nil {
		return err
	}
	if _, err := root.Seek(0, io.SeekStart); err != nil {
		return err
	}
	identities := map[string]installedPlugin{}
	for _, filename := range names {
		if !safePluginFilename(filename) {
			continue
		}
		f, err := openPluginAt(root, filename)
		if err != nil {
			return errors.New("An existing plugin JAR cannot be safely inspected")
		}
		// Reserved appliance JARs still require safe regular-file semantics.
		if managedPluginIdentity(filename, "") != "" {
			f.Close()
			continue
		}
		identity, err := inspectStagedPlugin(f, filename)
		f.Close()
		if err != nil {
			return errors.New("An existing plugin identity is unavailable; installation blocked")
		}
		identities[filename] = identity
	}
	stagedNames, stagedMains := map[string]bool{}, map[string]bool{}
	for _, a := range plan.Artifacts {
		if a.Satisfied != "" {
			hash, err := hashPluginAt(root, a.ExistingFilename)
			if err != nil || hash != a.ExistingHash {
				return errors.New("Satisfied dependency changed")
			}
			continue
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(int(root.Fd()), a.Filename, &stat, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
			return errors.New("Plugin target already exists")
		}
		name := strings.ToLower(a.PluginName)
		if name == "" || stagedNames[name] || (a.Main != "" && stagedMains[a.Main]) {
			return errors.New("Staged plugin identities are ambiguous or duplicated")
		}
		stagedNames[name] = true
		if a.Main != "" {
			stagedMains[a.Main] = true
		}
		for _, existing := range identities {
			if strings.EqualFold(existing.Name, a.PluginName) || (a.Main != "" && existing.Main == a.Main) {
				return errors.New("Plugin identity already exists under another filename; manual JARs cannot be adopted")
			}
		}
	}
	return nil
}

func placePluginArtifact(root *os.File, directory, id string, a pluginInstallArtifact) (bool, error) {
	if managedPluginDescriptor(a.Filename, a.PluginName, a.Main) != "" {
		return false, errors.New("JustVoxel components cannot be placed generically")
	}
	source, err := os.Open(filepath.Join(directory, a.Filename))
	if err != nil {
		return false, err
	}
	defer source.Close()
	temp := ".justvoxel-" + id + "-" + a.Filename + ".tmp"
	fd, err := unix.Openat(int(root.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0644)
	if err != nil {
		return false, err
	}
	f := os.NewFile(uintptr(fd), temp)
	defer unix.Unlinkat(int(root.Fd()), temp, 0)
	hash := sha512.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(source, a.Size+1))
	if err == nil && (n != a.Size || hex.EncodeToString(hash.Sum(nil)) != a.SHA512) {
		err = errors.New("Staged artifact changed")
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = unix.Fchmod(fd, 0644)
	}
	closeErr := f.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := unix.Renameat2(int(root.Fd()), temp, int(root.Fd()), a.Filename, unix.RENAME_NOREPLACE); err != nil {
		return false, err
	}
	return true, root.Sync()
}
func commitPluginManifest(dataPath string, previous []byte, manifest pluginManifest) (bool, error) {
	_, raw, err := readPluginManifest(dataPath)
	if err != nil || !bytes.Equal(raw, previous) {
		return false, errors.New("Ownership manifest changed")
	}
	dataFD, err := unix.Open(dataPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(dataFD)
	if err := unix.Mkdirat(dataFD, ".justvoxel", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return false, err
	}
	fd, err := unix.Openat(dataFD, ".justvoxel", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(fd)
	tempID, err := newOperationID()
	if err != nil {
		return false, err
	}
	temp := ".plugins-" + tempID + ".tmp"
	mfd, err := unix.Openat(fd, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return false, err
	}
	f := os.NewFile(uintptr(mfd), temp)
	defer unix.Unlinkat(fd, temp, 0)
	raw, err = json.Marshal(manifest)
	if err == nil && len(raw) > pluginManifestLimit {
		err = errors.New("Ownership manifest exceeds safe size")
	}
	if err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	// Validate the existing manifest again immediately before the atomic replacement.
	_, current, err := readPluginManifest(dataPath)
	if err != nil || !bytes.Equal(current, previous) {
		return false, errors.New("Ownership manifest changed")
	}
	var pinned, currentDirectory unix.Stat_t
	if unix.Fstat(fd, &pinned) != nil || unix.Fstatat(dataFD, ".justvoxel", &currentDirectory, unix.AT_SYMLINK_NOFOLLOW) != nil || pinned.Dev != currentDirectory.Dev || pinned.Ino != currentDirectory.Ino {
		return false, errors.New("Ownership directory changed")
	}
	if err := unix.Renameat(fd, temp, fd, "plugins.json"); err != nil {
		return false, err
	}
	if err := unix.Fsync(fd); err != nil {
		return true, err
	}
	return true, unix.Fsync(dataFD)
}
func removePluginTransactionArtifact(root *os.File, a pluginInstallArtifact) error {
	if managedPluginDescriptor(a.Filename, a.PluginName, a.Main) != "" {
		return errors.New("JustVoxel components cannot be removed generically")
	}
	hash, err := hashPluginAt(root, a.Filename)
	if err != nil || hash != a.SHA512 {
		return errors.New("Transaction JAR changed; it was not deleted")
	}
	if err := unix.Unlinkat(int(root.Fd()), a.Filename, 0); err != nil {
		return err
	}
	return root.Sync()
}
func rollbackPluginInstall(s *server, id string, plan pluginInstallPlan, root *os.File, created []pluginInstallArtifact, originallyRunning bool, cause error) error {
	if _, err := s.operations.transition(id, operationFailed, "rollback", "Plugin installation failed; restoring the previous runtime."); err != nil {
		return errors.Join(cause, err)
	}
	if _, err := s.operations.transition(id, operationRollingBack, "rollback", "Removing only unchanged JARs created by this installation."); err != nil {
		return errors.Join(cause, err)
	}
	// Recovery has its own bounded context; a timed-out worker must still recover.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if len(created) == 0 {
		var recoverErr error
		active, stateErr := pluginServiceActive(ctx)
		recoverErr = stateErr
		if recoverErr == nil && originallyRunning {
			if !active {
				recoverErr = pluginService(ctx, "start")
			}
			if recoverErr == nil {
				recoverErr = verifyPluginRuntime(ctx, plan.Server.MinecraftVersion)
			}
		}
		if recoverErr == nil && !originallyRunning && active {
			recoverErr = pluginService(ctx, "stop")
		}
		if recoverErr == nil && !originallyRunning {
			active, stateErr = pluginServiceActive(ctx)
			if stateErr != nil || active {
				recoverErr = errors.New("Original stopped state unavailable")
			}
		}
		if recoverErr != nil {
			_, err := s.operations.transition(id, operationNeedsAttention, "rollback", "Plugin installation failed before placement. Automatic recovery of the original service state could not be proven; administrator attention is required.")
			return errors.Join(cause, recoverErr, err)
		}
		_, err := s.operations.transition(id, operationRolledBack, "complete", "Plugin installation failed before placement. Plugin files were untouched and the original Minecraft service state was restored.")
		return errors.Join(cause, err)
	}
	recoverErr := pluginService(ctx, "stop")
	if recoverErr == nil {
		active, err := pluginServiceActive(ctx)
		if err != nil || active {
			recoverErr = errors.New("Failed runtime could not be proven stopped")
		}
	}
	if recoverErr == nil {
		recoverErr = checkPluginDirectoryIdentity(plan.DataPath, root)
	}
	if recoverErr == nil {
		for _, a := range created {
			if err := removePluginTransactionArtifact(root, a); err != nil {
				recoverErr = errors.Join(recoverErr, err)
			}
		}
	}
	if recoverErr == nil && originallyRunning {
		recoverErr = pluginService(ctx, "start")
		if recoverErr == nil {
			recoverErr = verifyPluginRuntime(ctx, plan.Server.MinecraftVersion)
		}
	}
	if recoverErr != nil {
		_, err := s.operations.transition(id, operationNeedsAttention, "rollback", "Plugin installation failed. Automatic recovery could not be proven; administrator attention is required. Transaction evidence and cold backup were preserved.")
		return errors.Join(cause, recoverErr, err)
	}
	message := "Plugin installation failed: " + cause.Error() + ". Previous Minecraft runtime restored and verified."
	if !originallyRunning {
		message = "Plugin installation failed: " + cause.Error() + ". Previous plugin files restored; Minecraft returned to its original stopped state."
	}
	_, err := s.operations.transition(id, operationRolledBack, "complete", message)
	return errors.Join(cause, err)
}

// Context collection errors are converted into one installer API error, without
// writing a second response or exposing discovery output to the browser.
type pluginContextSink struct{ header http.Header }

func (w *pluginContextSink) Header() http.Header         { return w.header }
func (w *pluginContextSink) Write(p []byte) (int, error) { return len(p), nil }
func (w *pluginContextSink) WriteHeader(int)             {}

// Cleanup follows a durable terminal result. It never changes runtime state or
// converts a verified result into a failed transaction.
var removePluginStaging = os.RemoveAll

// Startup queued cleanup pins the private parent so a redirected staging root
// cannot cause removal outside this operation's private staging directory.
func cleanupQueuedPluginStaging(base, id string) error {
	if !validOperationID(id) {
		return errors.New("Invalid plugin operation ID")
	}
	parent, err := openPluginEvidenceDirectory(filepath.Join(base, "plugin-install"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	return removePluginStaging(filepath.Join("/proc/self/fd", strconv.Itoa(int(parent.Fd())), id))
}

func cleanupTerminalPluginStaging(s *server, id string, actor session) {
	journal, err := s.operations.get(id)
	if err != nil || journal.OperationType != operationTypePluginInstall || (journal.State != operationSucceeded && journal.State != operationRolledBack && journal.State != operationResolved) {
		return
	}
	directory := filepath.Join(s.operations.baseDir, "plugin-install", id)
	if err := removePluginStaging(directory); err != nil {
		_ = s.operations.pluginCleanupWarning(id)
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "plugin_install_cleanup", id, false, "Verified terminal plugin state kept; private staging cleanup failed")
		}
	}
}

var errPluginEvidenceAbsent = errors.New("Transaction evidence is absent")

func readPluginEvidence(base, id, name string) ([]byte, error) {
	// Pin both private directories and refuse redirected or writable evidence.
	fd, err := unix.Open(filepath.Join(base, "plugin-install"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	dfd, err := unix.Openat(fd, id, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dfd)
	for _, directory := range []int{fd, dfd} {
		var stat unix.Stat_t
		if unix.Fstat(directory, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
			return nil, errors.New("Unsafe transaction directory")
		}
	}
	tfd, err := unix.Openat(dfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, errPluginEvidenceAbsent
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(tfd), name)
	defer f.Close()
	stat, err := f.Stat()
	var owner unix.Stat_t
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > pluginManifestLimit || stat.Mode().Perm()&0077 != 0 || unix.Fstat(tfd, &owner) != nil || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("Unsafe transaction evidence")
	}
	raw, err := io.ReadAll(io.LimitReader(f, pluginManifestLimit+1))
	if err != nil || len(raw) > pluginManifestLimit {
		return nil, errors.New("Transaction evidence too large")
	}
	return raw, nil
}

func readPluginTransaction(base, id string) (pluginTransaction, error) {
	var tx pluginTransaction
	raw, err := readPluginEvidence(base, id, "transaction.json")
	if err != nil {
		return tx, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["originally_running"] == nil || string(fields["originally_running"]) == "null" {
		return tx, errors.New("Original service state evidence is missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&tx) != nil || decoder.Decode(new(any)) != io.EOF || tx.SchemaVersion != "v1" || tx.OperationID != id || !operationFingerprintPattern.MatchString(tx.PlanFingerprint) || len(tx.Artifacts) == 0 || len(tx.Artifacts) > pluginMaxArtifacts {
		return tx, errors.New("Invalid transaction evidence")
	}
	projects, filenames := map[string]bool{}, map[string]bool{}
	for _, a := range tx.Artifacts {
		if !modrinthPluginID.MatchString(a.ProjectID) || !modrinthPluginID.MatchString(a.VersionID) || a.VersionNumber == "" || len(a.VersionNumber) > 256 || !safePluginFilename(a.Filename) || !pluginHashPattern.MatchString(a.SHA512) || !pluginNamePattern.MatchString(a.PluginName) || managedPluginDescriptor(a.Filename, a.PluginName, a.Main) != "" || (a.Main != "" && !pluginMainPattern.MatchString(a.Main)) || projects[a.ProjectID] || filenames[strings.ToLower(a.Filename)] || len(a.RequiredBy) > pluginMaxArtifacts {
			return tx, errors.New("Invalid transaction artifact")
		}
		for _, parent := range a.RequiredBy {
			if !modrinthPluginID.MatchString(parent) {
				return tx, errors.New("Invalid transaction dependency")
			}
		}
		projects[a.ProjectID], filenames[strings.ToLower(a.Filename)] = true, true
	}
	return tx, nil
}

// Both proofs are read-only: acknowledgement cannot repair or delete a JAR.
func provePluginTransactionFiles(dataPath string, tx pluginTransaction) (bool, error) {
	root, err := openPluginDirectory(dataPath)
	if err != nil {
		return false, err
	}
	defer root.Close()
	manifest, _, err := readPluginManifest(dataPath)
	if err != nil {
		return false, err
	}
	names, err := root.Readdirnames(-1)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		if strings.HasPrefix(name, ".justvoxel-"+tx.OperationID+"-") {
			return false, errors.New("Transaction placement residue requires administrator review")
		}
	}
	installed, previous := true, true
	for _, a := range tx.Artifacts {
		hash, hashErr := hashPluginAt(root, a.Filename)
		if hashErr != nil || hash != a.SHA512 {
			installed = false
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(int(root.Fd()), a.Filename, &stat, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
			previous = false
		}
		exact := false
		for _, entry := range manifest.Artifacts {
			if reflect.DeepEqual(entry, a) {
				exact = true
			}
			if entry.ProjectID == a.ProjectID || entry.VersionID == a.VersionID || strings.EqualFold(entry.Filename, a.Filename) || entry.SHA512 == a.SHA512 {
				previous = false
			}
		}
		if !exact {
			installed = false
		}
	}
	if err := checkPluginDirectoryIdentity(dataPath, root); err != nil {
		return false, err
	}
	if !installed && !previous {
		return false, errors.New("Neither installed nor previous plugin state can be proven")
	}
	return installed, nil
}

func readPluginPreflight(base, id string) (pluginPreflight, error) {
	var evidence pluginPreflight
	raw, err := readPluginEvidence(base, id, "preflight.json")
	if err != nil {
		return evidence, err
	}
	if len(raw) > 4096 {
		return evidence, errors.New("Preflight evidence too large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&evidence) != nil || d.Decode(new(any)) != io.EOF || evidence.SchemaVersion != "v1" || !validOperationID(evidence.OperationID) || evidence.OperationID != id || !operationFingerprintPattern.MatchString(evidence.PlanFingerprint) || !filepath.IsAbs(evidence.DataPath) || filepath.Clean(evidence.DataPath) != evidence.DataPath || len(evidence.DataPath) > 4096 || strings.ContainsAny(evidence.DataPath, "\x00\r\n") || evidence.MinecraftVersion == "" || len(evidence.MinecraftVersion) > 128 || strings.ContainsAny(evidence.MinecraftVersion, "\x00\r\n") || !modrinthPluginID.MatchString(evidence.ProjectID) || !modrinthPluginID.MatchString(evidence.VersionID) {
		return evidence, errors.New("Invalid preflight evidence")
	}
	return evidence, nil
}

func provePluginPreflight(base string, journal operationJournal, dataPath string) error {
	if _, err := readPluginEvidence(base, journal.OperationID, "transaction.json"); !errors.Is(err, errPluginEvidenceAbsent) {
		return errors.New("Mutation evidence is present or unsafe")
	}
	root, err := openPluginDirectory(dataPath)
	if err != nil {
		return err
	}
	defer root.Close()
	names, err := root.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, name := range names {
		if strings.HasPrefix(name, ".justvoxel-"+journal.OperationID+"-") {
			return errors.New("Transaction placement residue requires review")
		}
	}
	return checkPluginDirectoryIdentity(dataPath, root)
}

// Called with the existing maintenance lock held. This path never changes
// service state, retries installation, removes plugin files, or restores backups.
func (s *server) acknowledgePluginPreflight(w http.ResponseWriter, r *http.Request, journal operationJournal, actor session) {
	evidence, err := readPluginPreflight(s.operations.baseDir, journal.OperationID)
	if err != nil || evidence.PlanFingerprint != journal.PlanFingerprint {
		writeError(w, 409, "Pre-mutation evidence could not be verified; evidence was preserved")
		return
	}
	serverContext, dataPath, ok := s.pluginContext(&pluginContextSink{header: make(http.Header)}, r)
	if !ok || !serverContext.Supported || (serverContext.ServerType != "paper" && serverContext.ServerType != "purpur") || dataPath != evidence.DataPath || serverContext.MinecraftVersion != evidence.MinecraftVersion {
		writeError(w, 409, "Pre-mutation server context could not be verified")
		return
	}
	if err := provePluginPreflight(s.operations.baseDir, journal, dataPath); err != nil {
		writeError(w, 409, "Pre-mutation state could not be proven; evidence was preserved")
		return
	}
	active, err := pluginServiceActive(r.Context())
	if err != nil {
		writeError(w, 409, "Minecraft service state could not be verified")
		return
	}
	if active {
		if err := verifyPluginRuntime(r.Context(), evidence.MinecraftVersion); err != nil {
			writeError(w, 409, "Minecraft runtime could not be verified")
			return
		}
	}
	finalEvidence, evidenceErr := readPluginPreflight(s.operations.baseDir, journal.OperationID)
	finalContext, finalPath, contextOK := s.pluginContext(&pluginContextSink{header: make(http.Header)}, r)
	finalActive, stateErr := pluginServiceActive(r.Context())
	if evidenceErr != nil || finalEvidence != evidence || !contextOK || !finalContext.Supported || finalContext.ServerType != serverContext.ServerType || finalPath != dataPath || finalContext.MinecraftVersion != evidence.MinecraftVersion || stateErr != nil || finalActive != active || provePluginPreflight(s.operations.baseDir, journal, dataPath) != nil {
		writeError(w, 409, "Pre-mutation evidence or service state changed during re-check")
		return
	}
	status := "Plugin installation was interrupted before Minecraft or plugin files were changed."
	resolved, err := s.operations.transition(journal.OperationID, operationResolved, "resolved", status)
	if err != nil {
		writeError(w, 409, "Plugin installation could not be resolved")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "plugin_install_acknowledge", journal.OperationID, true, status)
	}
	cleanupTerminalPluginStaging(s, journal.OperationID, actor)
	if current, err := s.operations.get(resolved.OperationID); err == nil {
		resolved = current
	}
	writeJSON(w, 200, adminOperationResponse{Operation: &resolved})
}

func (s *server) adminPluginInstallAcknowledge(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
	}
	if !decodePluginInstallJSON(w, r, &request) {
		return
	}
	if !validOperationID(request.OperationID) {
		writeError(w, 400, "Invalid plugin acknowledgement")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Plugin operation tracking unavailable")
		return
	}
	journal, err := s.operations.currentMinecraftMaintenance()
	if err != nil || journal == nil || journal.OperationType != operationTypePluginInstall || journal.OperationID != request.OperationID || journal.State != operationNeedsAttention {
		writeError(w, 409, "No matching plugin installation requires attention")
		return
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		writeError(w, 409, "Minecraft maintenance is busy; evidence was preserved")
		return
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	tx, err := readPluginTransaction(s.operations.baseDir, journal.OperationID)
	if errors.Is(err, errPluginEvidenceAbsent) {
		s.acknowledgePluginPreflight(w, r.WithContext(ctx), *journal, actor)
		return
	}
	if err != nil || tx.PlanFingerprint != journal.PlanFingerprint {
		writeError(w, 409, "Transaction evidence could not be verified; keep this operation for review")
		return
	}
	serverContext, dataPath, ok := s.pluginContext(&pluginContextSink{header: make(http.Header)}, r.WithContext(ctx))
	// Earlier v1 evidence has no path/version fields. Use authoritative appliance
	// context, still requiring every recorded artifact and the exact fingerprint.
	if !ok || !serverContext.Supported || !filepath.IsAbs(dataPath) || (tx.OriginallyRunning && serverContext.MinecraftVersion == "" && tx.MinecraftVersion == "") || (tx.DataPath != "" && tx.DataPath != dataPath) || (tx.MinecraftVersion != "" && serverContext.MinecraftVersion != "" && tx.MinecraftVersion != serverContext.MinecraftVersion) {
		writeError(w, 409, "Transaction server context could not be verified")
		return
	}
	installed, err := provePluginTransactionFiles(dataPath, tx)
	if err != nil {
		writeError(w, 409, "Plugin state is not coherent; repair and re-check. Evidence was preserved")
		return
	}
	active, err := pluginServiceActive(ctx)
	if err != nil || active != tx.OriginallyRunning {
		writeError(w, 409, "Minecraft must match its original running or stopped state before re-check")
		return
	}
	if active {
		version := serverContext.MinecraftVersion
		if version == "" {
			version = tx.MinecraftVersion
		}
		if err := verifyPluginRuntime(ctx, version); err != nil {
			writeError(w, 409, "Minecraft runtime could not be verified")
			return
		}
		if installed {
			expected := []string{}
			for _, a := range tx.Artifacts {
				expected = append(expected, a.PluginName)
			}
			if err := verifyPluginStartup(dataPath, expected); err != nil {
				writeError(w, 409, "Installed plugin runtime state could not be verified")
				return
			}
		}
	}
	// Runtime probing can take time; re-check the file proof and final state.
	finalInstalled, proofErr := provePluginTransactionFiles(dataPath, tx)
	active, stateErr := pluginServiceActive(ctx)
	if proofErr != nil || finalInstalled != installed || stateErr != nil || active != tx.OriginallyRunning {
		writeError(w, 409, "Plugin or service state changed during re-check")
		return
	}
	status := "Previous plugin state verified after administrator repair; Minecraft is running."
	if installed {
		status = "Installed plugin state and active plugin identities verified after administrator repair; Minecraft is running."
	}
	if !active {
		status = "Previous plugin state verified after administrator repair; Minecraft remains stopped."
		if installed {
			status = "Installed plugin files and ownership verified after administrator repair; Minecraft remains stopped."
		}
	}
	resolved, err := s.operations.transition(journal.OperationID, operationResolved, "resolved", status)
	if err != nil {
		writeError(w, 409, "Plugin installation could not be resolved")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "plugin_install_acknowledge", journal.OperationID, true, status)
	}
	cleanupTerminalPluginStaging(s, journal.OperationID, actor)
	if current, err := s.operations.get(resolved.OperationID); err == nil {
		resolved = current
	}
	writeJSON(w, 200, adminOperationResponse{Operation: &resolved})
}

func managedPluginDescriptor(filename, name, main string) string {
	if component := managedPluginIdentity(filename, main); component != "" {
		return component
	}
	switch strings.ToLower(name) {
	case "geyser", "geyser-spigot":
		return "geyser"
	case "floodgate":
		return "floodgate"
	case "viaversion":
		return "viaversion"
	}
	return ""
}
