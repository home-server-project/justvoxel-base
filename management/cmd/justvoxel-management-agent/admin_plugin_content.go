package main

import (
	"archive/zip"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

func pluginContentContext(software string) pluginServerContext {
	out := pluginServerContext{ServerType: software, ContentKind: javaServerContentKind(software)}
	out.Supported = out.ContentKind != ""
	if c, ok := javaServerSoftware(software); ok {
		out.ServerSoftware = c.Name
	}
	out.Providers.Modrinth, out.Providers.CurseForge = out.Supported, out.Supported
	_, err := hangarPluginPlatform(software)
	out.Providers.Hangar = out.ContentKind == "plugins" && err == nil
	if software == "vanilla" {
		out.Message = "Vanilla does not support server plugins or loader mods."
	} else if software == "bedrock" {
		out.ServerSoftware = "Bedrock"
		out.Message = "This Java Plugins & Mods catalog does not apply to native Bedrock."
	} else if !out.Supported {
		out.Message = "Plugins & Mods are unavailable for this server software."
	}
	return out
}

// Empty target is the deployed v1 plugin contract. Preserve the raw field.
func pluginTarget(target string) string {
	if target == "" {
		return "plugins"
	}
	return target
}
func validPluginTarget(target string) bool {
	return target == "" || target == "plugins" || target == "mods"
}
func contentManifestIdentity(e pluginManifestEntry) string {
	return pluginTarget(e.Target) + ":" + pluginIdentity(e.Source, e.ProjectID)
}
func openContentDirectory(dataPath, target string) (*os.File, error) {
	if !filepath.IsAbs(dataPath) || (target != "plugins" && target != "mods") {
		return nil, errors.New("Unsupported content directory")
	}
	if target == "plugins" {
		return openPluginDirectory(dataPath)
	}
	data, err := openPluginDataDirectory(dataPath)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	fd, err := unix.Openat(int(data.Fd()), "mods", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "mods"), nil
}
func verifyContentStartup(path string, expected []string, target string) error {
	if target == "mods" {
		return nil
	} // Runtime health is checked independently; arbitrary mod functionality is not proven.
	return verifyPluginStartup(path, expected)
}

func modDescriptor(loader string) string {
	switch loader {
	case "fabric":
		return "fabric.mod.json"
	case "quilt":
		return "quilt.mod.json"
	case "forge":
		return "META-INF/mods.toml"
	case "neoforge":
		return "META-INF/neoforge.mods.toml"
	}
	return ""
}
func inspectStagedMod(file *os.File, filename, loader string) (installedPlugin, error) {
	item := installedPlugin{Filename: filename, Management: "manual", MetadataStatus: "Loader descriptor verified"}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > pluginMaxJar || !pluginZipDirectoryBounded(file, stat.Size()) {
		return item, errors.New("Malformed or oversized mod JAR")
	}
	archive, err := zip.NewReader(file, stat.Size())
	if err != nil {
		return item, errors.New("Malformed mod JAR")
	}
	wanted := modDescriptor(loader)
	if wanted == "" {
		return item, errors.New("Unsupported mod loader")
	}
	counts := map[string]int{}
	for _, entry := range archive.File {
		switch entry.Name {
		case "fabric.mod.json", "quilt.mod.json", "META-INF/mods.toml", "META-INF/neoforge.mods.toml", "META-INF/sponge_plugins.json":
			counts[entry.Name]++
			if counts[entry.Name] > 1 || entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > pluginMetadataLimit || !entry.Mode().IsRegular() {
				return item, errors.New("Ambiguous or oversized loader descriptor")
			}
			stream, err := entry.Open()
			if err != nil {
				return item, errors.New("Unreadable loader descriptor")
			}
			raw, readErr := io.ReadAll(io.LimitReader(stream, pluginMetadataLimit+1))
			closeErr := stream.Close()
			if readErr != nil || closeErr != nil || len(raw) == 0 || len(raw) > pluginMetadataLimit {
				return item, errors.New("Unreadable loader descriptor")
			}
			if entry.Name == wanted && !validModDescriptor(raw, loader) {
				return item, errors.New("Invalid loader descriptor identity")
			}
		}
	}
	if counts[wanted] != 1 {
		return item, errors.New("JAR lacks the descriptor required by the selected mod loader")
	}
	return item, nil
}

func readModInventory(dataPath, loader string) ([]installedPlugin, error) {
	out := []installedPlugin{}
	manifest, _, manifestErr := readPluginManifest(dataPath)
	root, err := openContentDirectory(dataPath, "mods")
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return nil, err
	}
	var entries []os.DirEntry
	if root != nil {
		defer root.Close()
		entries, err = root.ReadDir(-1)
		if err != nil {
			return nil, err
		}
	}
	for _, entry := range entries {
		if !safePluginFilename(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		jar, err := openPluginAt(root, entry.Name())
		if err != nil {
			continue
		}
		item, inspectErr := inspectStagedMod(jar, entry.Name(), loader)
		jar.Close()
		item.UpdateStatus = "Manual / unmanaged; lifecycle actions are not available yet"
		if inspectErr != nil {
			item.MetadataStatus = "Matching loader descriptor unavailable"
		}
		if manifestErr != nil {
			item.UpdateStatus = "Attention: ownership manifest unavailable"
		} else {
			for _, e := range manifest.Artifacts {
				if pluginTarget(e.Target) != "mods" || e.Filename != entry.Name() {
					continue
				}
				item.Source, item.ProjectID, item.VersionID = pluginSource(e.Source), e.ProjectID, e.VersionID
				item.State = "attention"
				verifyErr := verifyManagedArtifactAt(root, e)
				if verifyErr != nil || inspectErr != nil {
					item.UpdateStatus = "Attention: mod differs from ownership evidence"
					break
				}
				item.Management, item.State, item.Source, item.ProjectID, item.VersionID = "plugins", "installed", pluginSource(e.Source), e.ProjectID, e.VersionID
				item.Name, item.Version = e.PluginName, e.VersionNumber
				item.UpdateStatus = "Managed content; lifecycle actions are not available yet"
			}
		}
		out = append(out, item)
	}
	if manifestErr == nil {
		for _, e := range manifest.Artifacts {
			if e.Target != "mods" {
				continue
			}
			present := false
			for _, item := range out {
				if item.Filename == e.Filename {
					present = true
					break
				}
			}
			if !present {
				out = append(out, installedPlugin{Filename: e.Filename, Name: e.PluginName, Version: e.VersionNumber, Source: pluginSource(e.Source), ProjectID: e.ProjectID, VersionID: e.VersionID, Management: "manual", State: "attention", MetadataStatus: "Managed mod missing or unsafe", UpdateStatus: "Attention: ownership evidence does not match a safe active mod"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, nil
}
func checkModInstallCollisions(root *os.File, plan pluginInstallPlan) error {
	seen := map[string]bool{}
	for _, a := range plan.Artifacts {
		name := strings.ToLower(a.Filename)
		if pluginTarget(a.Target) != "mods" || seen[name] {
			return errors.New("Ambiguous mod target")
		}
		seen[name] = true
		if a.Satisfied != "" {
			hash, err := hashPluginAt(root, a.ExistingFilename)
			if err != nil || hash != a.ExistingHash {
				return errors.New("Managed dependency changed")
			}
			continue
		}
		var st unix.Stat_t
		if err := unix.Fstatat(int(root.Fd()), a.Filename, &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
			return errors.New("Mod target already exists or cannot be safely checked")
		}
	}
	return nil
}

// Reset and inventory share artifact verification without extending plugin lifecycle.
func verifyManagedArtifactAt(root *os.File, e pluginManifestEntry) error {
	if !validPluginTarget(e.Target) {
		return errors.New("Invalid managed artifact target")
	}
	if pluginTarget(e.Target) == "plugins" {
		return verifyManagedPluginAt(root, e)
	}
	if root == nil || !safePluginFilename(e.Filename) || !pluginHashPattern.MatchString(e.SHA512) {
		return errors.New("Incomplete mod ownership")
	}
	f, err := openPluginAt(root, e.Filename)
	if err != nil {
		return err
	}
	defer f.Close()
	var original unix.Stat_t
	if unix.Fstat(int(f.Fd()), &original) != nil || original.Size <= 0 || original.Size > pluginMaxJar {
		return errors.New("Unsafe managed mod")
	}
	// The exact durable hash is authoritative; accept only the loader descriptors
	// supported by installation, without querying providers or executing JAR code.
	descriptor := false
	for _, loader := range []string{"fabric", "quilt", "forge", "neoforge"} {
		if _, err := inspectStagedMod(f, e.Filename, loader); err == nil {
			descriptor = true
			break
		}
	}
	if !descriptor {
		return errors.New("Managed mod descriptor unavailable")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha512.New()
	n, err := io.Copy(h, io.LimitReader(f, pluginMaxJar+1))
	if err != nil || n != original.Size || hex.EncodeToString(h.Sum(nil)) != e.SHA512 {
		return errors.New("Managed mod hash differs from ownership")
	}
	var current, opened unix.Stat_t
	if unix.Fstatat(int(root.Fd()), e.Filename, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(int(f.Fd()), &opened) != nil || current.Mode&unix.S_IFMT != unix.S_IFREG || current.Dev != original.Dev || current.Ino != original.Ino || current.Size != original.Size || current.Mtim != original.Mtim || current.Ctim != original.Ctim || opened.Size != original.Size || opened.Mtim != original.Mtim || opened.Ctim != original.Ctim {
		return errors.New("Managed mod changed during verification")
	}
	return nil
}
func resolveResetManagedLocation(path string, e pluginManifestEntry) (string, error) {
	if pluginTarget(e.Target) == "plugins" {
		return resolveManagedPluginLocation(path, e)
	}
	if e.Target != "mods" {
		return "", errors.New("Invalid managed target")
	}
	root, err := openContentDirectory(path, "mods")
	if err != nil {
		return "", err
	}
	defer root.Close()
	return "enabled", verifyManagedArtifactAt(root, e)
}

func validManagedDescriptorEvidence(e pluginManifestEntry) bool {
	if pluginTarget(e.Target) == "mods" {
		return e.Main == "" && (e.PluginName == "" || pluginNamePattern.MatchString(e.PluginName))
	}
	return pluginNamePattern.MatchString(e.PluginName) && managedPluginDescriptor(e.Filename, e.PluginName, e.Main) == "" && (e.Main == "" || pluginMainPattern.MatchString(e.Main))
}

var modIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
var modTOMLIDPattern = regexp.MustCompile(`(?m)^\s*modId\s*=\s*["']([a-z][a-z0-9_]{1,63})["']\s*(#.*)?$`)

func validModDescriptor(raw []byte, loader string) bool {
	if loader == "forge" || loader == "neoforge" {
		// Bounded static evidence only; do not attempt to evaluate TOML or JAR code.
		return strings.Contains(string(raw), "[[mods]]") && modTOMLIDPattern.Match(raw)
	}
	var descriptor struct {
		ID    string `json:"id"`
		Quilt struct {
			ID string `json:"id"`
		} `json:"quilt_loader"`
	}
	if json.Unmarshal(raw, &descriptor) != nil {
		return false
	}
	if loader == "quilt" {
		return modIDPattern.MatchString(descriptor.Quilt.ID)
	}
	return modIDPattern.MatchString(descriptor.ID)
}

// Side fields follow the official Fabric loader metadata and Forge mod-file
// contracts: https://docs.fabricmc.net/develop/loader/fabric-mod-json
// https://docs.minecraftforge.net/en/latest/gettingstarted/modfiles/
// Side inspection is confined to exact verified artifacts during Review and
// staging. Read-only inventory and deployed recovery identity remain unchanged.
func inspectDedicatedModSide(file *os.File, loader string) error {
	if loader != "fabric" && loader != "forge" {
		return nil // No authoritative side field supported for these loaders.
	}
	stat, err := file.Stat()
	if err != nil || stat.Size() <= 0 || stat.Size() > pluginMaxJar || !pluginZipDirectoryBounded(file, stat.Size()) {
		return errors.New("Malformed or oversized mod JAR")
	}
	archive, err := zip.NewReader(file, stat.Size())
	if err != nil {
		return errors.New("Malformed mod JAR")
	}
	var raw []byte
	count := 0
	for _, entry := range archive.File {
		if entry.Name != modDescriptor(loader) {
			continue
		}
		count++
		if count > 1 || entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > pluginMetadataLimit || !entry.Mode().IsRegular() {
			return errors.New("Ambiguous or oversized loader descriptor")
		}
		stream, err := entry.Open()
		if err != nil {
			return errors.New("Unreadable loader descriptor")
		}
		raw, err = io.ReadAll(io.LimitReader(stream, pluginMetadataLimit+1))
		closeErr := stream.Close()
		if err != nil || closeErr != nil || len(raw) > pluginMetadataLimit {
			return errors.New("Unreadable loader descriptor")
		}
	}
	if count != 1 {
		return errors.New("JAR lacks the descriptor required by the selected mod loader")
	}
	return dedicatedModDescriptorSide(raw, loader)
}

const clientOnlyModRejection = "This mod is client-only and cannot be installed on a dedicated server."

func dedicatedModDescriptorSide(raw []byte, loader string) error {
	if len(raw) == 0 || len(raw) > pluginMetadataLimit {
		return errors.New("Malformed or oversized loader descriptor")
	}
	if loader == "fabric" {
		// Decode top-level fields explicitly so duplicate environment evidence
		// cannot be hidden by encoding/json's last-value-wins behavior.
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			return errors.New("Malformed Fabric descriptor")
		}
		environment, seen := "*", false
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return errors.New("Malformed Fabric descriptor")
			}
			var value json.RawMessage
			if decoder.Decode(&value) != nil {
				return errors.New("Malformed Fabric descriptor")
			}
			if key == "environment" {
				if seen || string(value) == "null" || json.Unmarshal(value, &environment) != nil {
					return errors.New("Invalid Fabric environment metadata")
				}
				seen = true
			}
		}
		if _, err := decoder.Token(); err != nil || decoder.Decode(new(any)) != io.EOF {
			return errors.New("Malformed Fabric descriptor")
		}
		switch environment {
		case "client":
			return errors.New(clientOnlyModRejection)
		case "*", "server":
			return nil
		default:
			return errors.New("Invalid Fabric environment metadata")
		}
	}
	if loader == "forge" {
		// Forge's clientSideOnly is a file-level boolean, before any tables.
		// Ignore comments and multiline string contents; never interpret
		// dependency-side fields or strings as a client-only declaration.
		multiline := ""
		seen := false
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
				break
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if key == "clientSideOnly" || key == "'clientSideOnly'" || key == "\"clientSideOnly\"" {
				value, _, _ = strings.Cut(value, "#")
				value = strings.TrimSpace(value)
				if seen || (value != "true" && value != "false") {
					return errors.New("Invalid Forge clientSideOnly metadata")
				}
				seen = true
				if value == "true" {
					return errors.New(clientOnlyModRejection)
				}
			}
			for _, delimiter := range []string{"\"\"\"", "'''"} {
				if strings.HasPrefix(value, delimiter) && !forgeMultilineClosed(value[len(delimiter):], delimiter) {
					multiline = delimiter
				}
			}
		}
	}
	return nil
}

// Basic TOML multiline strings honor escapes; literal multiline strings do not.
// This only locates the end of a string, never interprets code or dependency side.
func forgeMultilineClosed(value, delimiter string) bool {
	for i := 0; i < len(value); i++ {
		if delimiter == "\"\"\"" && value[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(value[i:], delimiter) {
			return true
		}
	}
	return false
}
