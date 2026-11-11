package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Local loader identity is independent of provider project/version identity.
// Runtime ownership is never inferred from an artifact name or a catalog title.
type trustedModComponent struct {
	Filename string
	SHA512   string
	Size     int64
	IDs      []string
}

func modArtifactIdentities(file *os.File, filename, loader string) ([]string, error) {
	if _, err := inspectStagedMod(file, filename, loader); err != nil {
		return nil, err
	}
	st, err := file.Stat()
	if err != nil {
		return nil, err
	}
	jar, err := zip.NewReader(file, st.Size())
	if err != nil {
		return nil, err
	}
	for _, entry := range jar.File {
		if entry.Name != modDescriptor(loader) {
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(stream, pluginMetadataLimit+1))
		stream.Close()
		if err != nil || len(raw) > pluginMetadataLimit {
			return nil, errors.New("Unreadable loader identity")
		}
		ids := []string{}
		if loader == "fabric" || loader == "quilt" {
			var d struct {
				ID    string `json:"id"`
				Quilt struct {
					ID string `json:"id"`
				} `json:"quilt_loader"`
			}
			if json.Unmarshal(raw, &d) != nil {
				return nil, errors.New("Invalid loader identity")
			}
			if loader == "quilt" {
				ids = append(ids, d.Quilt.ID)
			} else {
				ids = append(ids, d.ID)
			}
		} else {
			// Only literal modId assignments in [[mods]] tables count. Ignore
			// multiline descriptions and dependency tables; never execute TOML/JAR code.
			inMod, multiline := false, ""
			for _, line := range strings.Split(string(raw), "\n") {
				line = strings.TrimSpace(line)
				if multiline != "" {
					if forgeMultilineClosed(line, multiline) {
						multiline = ""
					}
					continue
				}
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if strings.HasPrefix(line, "[") {
					header, _, _ := strings.Cut(line, "#")
					inMod = strings.TrimSpace(header) == "[[mods]]"
					continue
				}
				key, value, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				key, value = strings.TrimSpace(key), strings.TrimSpace(value)
				if inMod && (key == "modId" || key == "\"modId\"" || key == "'modId'") {
					matches := modTOMLIDPattern.FindStringSubmatch("modId=" + value)
					if len(matches) < 2 {
						return nil, errors.New("Unverifiable loader mod identity")
					}
					ids = append(ids, matches[1])
				}
				for _, delimiter := range []string{"\"\"\"", "'''"} {
					if strings.HasPrefix(value, delimiter) && !forgeMultilineClosed(value[len(delimiter):], delimiter) {
						multiline = delimiter
					}
				}
			}
		}
		if len(ids) == 0 {
			return nil, errors.New("Loader mod identity unavailable")
		}
		sort.Strings(ids)
		for i, id := range ids {
			if !modIDPattern.MatchString(id) || i > 0 && ids[i-1] == id {
				return nil, errors.New("Ambiguous loader mod identity")
			}
		}
		return ids, nil
	}
	return nil, errors.New("Loader mod identity unavailable")
}

// The cross-play manifest is one source of runtime ownership, not a loader
// dependency policy. Forge/Quilt currently have no such runtime source.
func runtimeOwnedModComponents(server pluginServerContext, dataPath string) ([]trustedModComponent, error) {
	out := []trustedModComponent{}
	if server.ServerType != "fabric" && server.ServerType != "neoforge" {
		return out, nil
	}
	if !instanceIDPattern.MatchString(server.InstanceID) {
		return nil, errors.New("Runtime mod instance identity unavailable")
	}
	directory := filepath.Join(instanceRuntimes.root, server.InstanceID)
	dir, err := instanceRuntimes.privateDirectory(directory, false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, errors.New("Unsafe runtime mod ownership directory")
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), "crossplay-mods.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return out, nil
	}
	if err != nil {
		return nil, errors.New("Unsafe runtime mod ownership file")
	}
	file := os.NewFile(uintptr(fd), "crossplay-mods.json")
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || st.Size <= 0 || st.Size > 65536 {
		return nil, errors.New("Unsafe runtime mod ownership permissions or size")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	var manifest struct {
		Schema    int               `json:"schema"`
		Platform  string            `json:"platform"`
		Artifacts map[string]string `json:"artifacts"`
	}
	if err != nil || len(raw) > 65536 || instanceDecode(raw, &manifest) != nil || manifest.Schema != 1 || manifest.Platform != server.ServerType || manifest.Artifacts == nil || len(manifest.Artifacts) > pluginMaxArtifacts {
		return nil, errors.New("Invalid runtime mod ownership evidence")
	}
	root, err := openContentDirectory(dataPath, "mods")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for name, expected := range manifest.Artifacts {
		if !safePluginFilename(name) || len(expected) != 64 || strings.Trim(expected, "0123456789abcdef") != "" {
			return nil, errors.New("Invalid runtime mod artifact evidence")
		}
		jar, err := openPluginAt(root, name)
		if err != nil {
			return nil, errors.New("Runtime-owned mod missing or unsafe")
		}
		h := sha256.New()
		h512 := sha512.New()
		n, hashErr := io.Copy(io.MultiWriter(h, h512), io.LimitReader(jar, pluginMaxJar+1))
		ids, inspectErr := modArtifactIdentities(jar, name, server.ServerType)
		jar.Close()
		hash := hex.EncodeToString(h512.Sum(nil))
		if hashErr != nil || n <= 0 || n > pluginMaxJar || hex.EncodeToString(h.Sum(nil)) != expected || inspectErr != nil {
			return nil, errors.New("Runtime-owned mod hash or loader identity differs from ownership")
		}
		if err := verifyManagedArtifactAt(root, pluginManifestEntry{Target: "mods", Filename: name, SHA512: hash}); err != nil {
			return nil, errors.New("Runtime-owned mod changed during verification")
		}
		out = append(out, trustedModComponent{Filename: name, SHA512: hash, Size: n, IDs: ids})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, nil
}

func runtimeModrinthVersion(ctx context.Context, server pluginServerContext, component trustedModComponent) (pluginCatalogVersion, bool) {
	var v pluginCatalogVersion
	err := modrinthPluginJSON(ctx, "/version_file/"+component.SHA512, url.Values{"algorithm": {"sha512"}}, &v)
	if err != nil || !modrinthPluginID.MatchString(v.ProjectID) || !modrinthPluginID.MatchString(v.ID) || !pluginVersionCompatible(v, server) || !modrinthServerEnvironment(v.Environment) {
		return v, false
	}
	for _, f := range v.Files {
		if f.Hashes.SHA512 == component.SHA512 && f.Size == component.Size && f.Size > 0 && f.Size <= pluginMaxJar {
			return v, true
		}
	}
	return v, false
}

func modIDsOverlap(a, b []string) bool {
	for _, id := range a {
		if slices.Contains(b, id) {
			return true
		}
	}
	return false
}

const runtimeModSatisfied = "Already provided by the server / JustVoxel runtime"
const modDependencyConflict = "Required dependency is already present but could not be verified as compatible."

// Exact read-only preflight precedes operation creation. All active JAR identities
// are checked, including manual files, which can block but never satisfy ownership.
func prepareModInstallPlan(ctx context.Context, plan *pluginInstallPlan) (failure error) {
	values := map[string]string{"provider": pluginSource(plan.Source), "loader": plan.Server.ServerType, "minecraft_version": plan.Server.MinecraftVersion, "project_id": plan.ProjectID, "release_id": plan.VersionID}
	defer func() {
		if failure == nil || failure.Error() == clientOnlyModRejection {
			return
		}
		var diagnostic *pluginProviderDiagnosticError
		if errors.As(failure, &diagnostic) {
			for k, v := range diagnostic.values {
				values[k] = v
			}
		}
		values["reason"] = failure.Error()
		failure = &pluginProviderDiagnosticError{reason: failure.Error(), values: values}
	}()
	if javaServerContentKind(plan.Server.ServerType) != "mods" {
		return nil
	}
	runtime, err := runtimeOwnedModComponents(plan.Server, plan.DataPath)
	if err != nil {
		return err
	}
	root, err := openContentDirectory(plan.DataPath, "mods")
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := root.ReadDir(1025)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) > 1024 {
		return errors.New("Too many active mod artifacts")
	}
	owners := map[string]string{}
	existing := map[string][]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			continue
		}
		jar, err := openPluginAt(root, entry.Name())
		if err != nil {
			return errors.New("Active mod identity cannot be safely verified")
		}
		ids, err := modArtifactIdentities(jar, entry.Name(), plan.Server.ServerType)
		jar.Close()
		if err != nil {
			return err
		}
		existing[entry.Name()] = ids
		for _, id := range ids {
			if owners[id] != "" {
				values["dependency_identity"], values["dependency_state"] = id, "conflicting"
				return fmt.Errorf("Installation would create a duplicate mod identity: %s", id)
			}
			owners[id] = entry.Name()
		}
	}
	directory, err := os.MkdirTemp("", "justvoxel-mod-preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	for i := range plan.Artifacts {
		a := &plan.Artifacts[i]
		values["dependency_project_id"], values["dependency_release_id"] = a.ProjectID, a.VersionID
		values["dependency_state"] = "new"
		if a.Satisfied != "" {
			values["dependency_state"] = "Plugins-managed"
		}
		if a.Satisfied == runtimeModSatisfied {
			values["dependency_state"] = "runtime-managed"
		}
		var ids []string
		if a.Satisfied != "" {
			hash, err := hashPluginAt(root, a.ExistingFilename)
			if err != nil || hash != a.ExistingHash || len(existing[a.ExistingFilename]) == 0 {
				return errors.New(modDependencyConflict)
			}
			ids = existing[a.ExistingFilename]
			jar, err := openPluginAt(root, a.ExistingFilename)
			if err != nil {
				return err
			}
			sideErr := inspectDedicatedModSide(jar, plan.Server.ServerType)
			jar.Close()
			if sideErr != nil {
				return sideErr
			}
		} else {
			hash, err := downloadVerifiedPluginArtifact(ctx, *a, directory)
			if err != nil {
				return err
			}
			jar, err := os.Open(filepath.Join(directory, a.Filename))
			if err != nil {
				return err
			}
			ids, err = modArtifactIdentities(jar, a.Filename, plan.Server.ServerType)
			if err == nil {
				err = inspectDedicatedModSide(jar, plan.Server.ServerType)
			}
			jar.Close()
			if err != nil {
				return err
			}
			// Cross-provider reuse requires exact official artifact equality as well as
			// loader identity. Same ID alone never proves version compatibility.
			for _, component := range runtime {
				if !modIDsOverlap(ids, component.IDs) {
					continue
				}
				if a.Requested || hash != component.SHA512 || !slices.Equal(ids, component.IDs) {
					values["dependency_state"] = "conflicting"
					values["dependency_identity"] = strings.Join(ids, ",")
					return errors.New(modDependencyConflict)
				}
				a.Satisfied, a.ExistingFilename, a.ExistingHash = runtimeModSatisfied, component.Filename, component.SHA512
			}
		}
		for _, id := range ids {
			values["dependency_identity"] = id
			if owner := owners[id]; owner != "" && (a.Satisfied == "" || owner != a.ExistingFilename) {
				values["dependency_state"] = "conflicting"
				return fmt.Errorf("Installation would create a duplicate mod identity: %s", id)
			}
			if a.Satisfied == "" {
				owners[id] = a.Filename
			}
		}
	}
	plan.Fingerprint = fingerprintPluginPlan(*plan)
	return nil
}

func verifyRuntimeModReuse(plan pluginInstallPlan) error {
	if javaServerContentKind(plan.Server.ServerType) != "mods" {
		return nil
	}
	components, err := runtimeOwnedModComponents(plan.Server, plan.DataPath)
	if err != nil {
		return err
	}
	for _, a := range plan.Artifacts {
		if a.Satisfied != runtimeModSatisfied {
			continue
		}
		found := false
		for _, c := range components {
			if c.Filename == a.ExistingFilename && c.SHA512 == a.ExistingHash {
				found = true
			}
		}
		if !found {
			return errors.New("Runtime-provided dependency ownership changed; review again")
		}
	}
	return nil
}
