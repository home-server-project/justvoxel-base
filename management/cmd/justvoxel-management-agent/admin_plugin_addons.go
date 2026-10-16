package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const addonUploadLimit = (128 << 20) + (64 << 10)
const addonManifestName = "plugin-addons.json"
const operationTypePluginAddonInstall = "plugin_addon_install"

type addonDefinition struct{ Kind, Name, Host, Directory, Accept string }

var addonDefinitions = []addonDefinition{
	{"placeholderapi_expansion", "PlaceholderAPI Expansions", "PlaceholderAPI", "expansions", ".jar"},
	{"geyser_extension", "Geyser Extensions", "Geyser-Spigot", "extensions", ".jar"},
	{"bluemap_addon", "BlueMap Add-ons", "BlueMap", "packs", ".jar"},
	{"skript_script", "Skript Scripts", "Skript", "scripts", ".sk"},
	{"worldedit_schematic", "WorldEdit Schematics", "WorldEdit", "schematics", ".schem,.schematic"},
}

func addonDefinitionFor(kind string) (addonDefinition, error) {
	for _, d := range addonDefinitions {
		if d.Kind == kind {
			return d, nil
		}
	}
	return addonDefinition{}, errors.New("Unknown Add-ons type")
}

var addonFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_. -]{0,179}$`)

func safeAddonFilename(name string) bool {
	return addonFilenamePattern.MatchString(name) && !strings.Contains(name, "..") && !strings.HasSuffix(name, " ") && !strings.HasSuffix(name, ".")
}
func safeAddonRelative(path string) bool {
	if path == "" || len(path) > 512 || filepath.IsAbs(path) || strings.ContainsAny(path, "\\:\x00") || filepath.Clean(path) != path {
		return false
	}
	for _, p := range strings.Split(path, "/") {
		if !addonFilenamePattern.MatchString(p) || strings.Contains(p, "..") {
			return false
		}
	}
	return true
}

// All components are opened relative to a pinned data root with NOFOLLOW.
// Creation is allowed only for the authoritative destination, never for a host.
func openAddonDirectory(data, relative string, create bool) (*os.File, error) {
	if !filepath.IsAbs(data) || !safeAddonRelative(relative) {
		return nil, errors.New("Unsafe Add-ons path")
	}
	fd, err := unix.Open(data, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(relative, "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(e, unix.ENOENT) && create {
			var st unix.Stat_t
			if unix.Fstat(fd, &st) != nil {
				unix.Close(fd)
				return nil, errors.New("Parent identity unavailable")
			}
			e = unix.Mkdirat(fd, part, 0755)
			if e == nil {
				next, e = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if e == nil {
					e = unix.Fchown(next, int(st.Uid), int(st.Gid))
					if e == nil {
						e = unix.Fsync(fd)
					}
					if e != nil {
						unix.Close(next)
					}
				}
			}
		}
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), relative), nil
}
func addonDirectoryEvidence(data, relative string) (string, error) {
	parts := strings.Split(relative, "/")
	rootFD, err := unix.Open(data, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	var rootStat unix.Stat_t
	err = unix.Fstat(rootFD, &rootStat)
	unix.Close(rootFD)
	if err != nil {
		return "", err
	}
	evidence := []string{fmt.Sprintf("%d:%d:%d:%d", rootStat.Dev, rootStat.Ino, rootStat.Uid, rootStat.Gid)}
	for i := range parts {
		f, err := openAddonDirectory(data, strings.Join(parts[:i+1], "/"), false)
		if errors.Is(err, unix.ENOENT) {
			evidence = append(evidence, "missing")
			break
		}
		if err != nil {
			return "", errors.New("Add-ons parent must be a safe real directory")
		}
		var st unix.Stat_t
		err = unix.Fstat(int(f.Fd()), &st)
		f.Close()
		if err != nil {
			return "", err
		}
		evidence = append(evidence, fmt.Sprintf("%d:%d:%d:%d", st.Dev, st.Ino, st.Uid, st.Gid))
	}
	return strings.Join(evidence, "/"), nil
}
func readAddonFile(root *os.File, name string, limit int64) ([]byte, error) {
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("Unsafe or oversized Add-ons file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("Add-ons file exceeds limit")
	}
	return raw, nil
}
func worldEditSaveDir(raw []byte) (string, error) {
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", errors.New("Invalid WorldEdit config")
	}
	value := "schematics"
	seen := false
	for _, line := range strings.Split(string(raw), "\n") {
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(line, "#") {
			continue
		}
		key, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "saveDir" {
			continue
		}
		if seen {
			return "", errors.New("Duplicate WorldEdit saveDir")
		}
		seen = true
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "\"") || strings.HasPrefix(v, "'") {
			quote := v[0]
			end := strings.IndexByte(v[1:], quote)
			if end < 0 {
				return "", errors.New("Invalid quoted saveDir")
			}
			end++
			tail := strings.TrimSpace(v[end+1:])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return "", errors.New("Invalid saveDir suffix")
			}
			v = v[1:end]
		} else {
			v, _, _ = strings.Cut(v, " #")
			v = strings.TrimSpace(v)
		}
		if !safeAddonRelative(v) {
			return "", errors.New("WorldEdit saveDir must be a non-empty safe relative path")
		}
		value = v
	}
	return value, nil
}

type addonHost struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	Available bool   `json:"available"`
	Identity  string `json:"-"`
}

func readAddonHostInventory(data string) ([]installedPlugin, error) {
	root, err := openAddonDirectory(data, "plugins", false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	before, err := root.Stat()
	if err != nil {
		return nil, err
	}
	inventory, err := readPluginInventory(data)
	if err != nil {
		return nil, err
	}
	current, err := openAddonDirectory(data, "plugins", false)
	if err != nil {
		return nil, err
	}
	after, err := current.Stat()
	current.Close()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("Host inventory directory changed")
	}
	return inventory, nil
}
func resolveAddonHost(data string, d addonDefinition, inventory []installedPlugin) (addonHost, string, error) {
	host := addonHost{Status: "unavailable", Message: "Install the required host plugin first."}
	relative := "plugins/" + d.Host + "/" + d.Directory
	var selected *installedPlugin
	for i := range inventory {
		p := &inventory[i]
		match := strings.EqualFold(p.Name, d.Host) && p.MetadataStatus == "Metadata read" && p.Main != ""
		if d.Kind == "geyser_extension" {
			match = p.Management == "justvoxel" && managedPluginIdentity(p.Filename, p.Main) == "geyser" && p.MetadataStatus == "Metadata read"
		}
		if !match {
			continue
		}
		if selected != nil {
			return host, relative, errors.New("Ambiguous host plugin identity")
		}
		selected = p
	}
	if selected == nil {
		return host, relative, nil
	}
	if selected.State == "disabled" {
		host.Status = "disabled"
		host.Message = "Host plugin is disabled. Enable it in Installed first."
		return host, relative, nil
	}
	manifest, _, err := readPluginManifest(data)
	if err != nil {
		return host, relative, err
	}
	for _, e := range manifest.Artifacts {
		if e.Filename == selected.Filename && selected.Management != "plugins" {
			host.Message = "Host ownership needs attention."
			return host, relative, nil
		}
	}
	root, err := openAddonDirectory(data, "plugins/"+d.Host, false)
	if err != nil {
		host.Message = "Host data directory is missing or unsafe."
		return host, relative, nil
	}
	defer root.Close()
	if d.Kind == "worldedit_schematic" {
		raw, e := readAddonFile(root, "config.yml", 256<<10)
		if e != nil && !errors.Is(e, unix.ENOENT) {
			return host, relative, e
		}
		dir, e := worldEditSaveDir(raw)
		if e != nil {
			return host, relative, e
		}
		relative = "plugins/WorldEdit/" + dir
	}
	_, err = addonDirectoryEvidence(data, relative)
	if err != nil {
		return host, relative, err
	}
	plugins, err := openAddonDirectory(data, "plugins", false)
	if err != nil {
		return host, relative, err
	}
	hash, err := hashPluginAt(plugins, selected.Filename)
	plugins.Close()
	if err != nil {
		return host, relative, err
	}
	identity, _ := json.Marshal(selected)
	host = addonHost{Status: "installed", Message: "Host installed; runtime loading is performed by the host.", Available: true, Identity: string(identity) + ":" + selected.Main + ":" + hash}
	return host, relative, nil
}

type addonEntry struct {
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Target   string `json:"target_relative_path"`
	SHA512   string `json:"sha512"`
	Name     string `json:"display_name,omitempty"`
	Identity string `json:"descriptor_identity,omitempty"`
}
type addonManifest struct {
	SchemaVersion string       `json:"schema_version"`
	Artifacts     []addonEntry `json:"artifacts"`
}
type addonItem struct {
	addonEntry
	State        string `json:"state"`
	Management   string `json:"management"`
	UpdateStatus string `json:"update_status"`
}
type addonSection struct {
	Kind        string      `json:"kind"`
	Name        string      `json:"name"`
	Accept      string      `json:"accept"`
	Host        addonHost   `json:"host"`
	Destination string      `json:"destination"`
	Files       []addonItem `json:"files"`
}
type addonInventory struct {
	Server   pluginServerContext `json:"server"`
	Sections []addonSection      `json:"sections"`
}

func validateAddonManifest(m addonManifest) error {
	if m.SchemaVersion != "v1" || m.Artifacts == nil || len(m.Artifacts) > 1024 {
		return errors.New("Invalid Add-ons manifest schema")
	}
	seen := map[string]bool{}
	for _, e := range m.Artifacts {
		d, err := addonDefinitionFor(e.Kind)
		prefix := "plugins/" + d.Host + "/" + d.Directory + "/"
		if e.Kind == "worldedit_schematic" {
			prefix = "plugins/WorldEdit/"
		}
		if err != nil || !safeAddonFilename(e.Filename) || !safeAddonRelative(e.Target) || filepath.Base(e.Target) != e.Filename || !strings.HasPrefix(e.Target, prefix) || !pluginHashPattern.MatchString(e.SHA512) || len(e.Name) > 256 || len(e.Identity) > 512 || seen[e.Target] {
			return errors.New("Invalid Add-ons ownership entry")
		}
		seen[e.Target] = true
		ext := strings.ToLower(filepath.Ext(e.Filename))
		if !strings.Contains(","+d.Accept+",", ","+ext+",") {
			return errors.New("Invalid Add-ons ownership extension")
		}
		if e.Kind == "worldedit_schematic" && filepath.Dir(e.Target) == "plugins/WorldEdit" {
			return errors.New("Schematic manifest lacks saveDir")
		}
		if e.Kind != "worldedit_schematic" && e.Target != prefix+e.Filename {
			return errors.New("Invalid fixed Add-ons destination")
		}
	}
	return nil
}
func readAddonManifestAt(root *os.File, empty addonManifest) (addonManifest, []byte, error) {
	var st unix.Stat_t
	if unix.Fstat(int(root.Fd()), &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return empty, nil, errors.New("Unsafe Add-ons ownership directory")
	}
	raw, err := readAddonFile(root, addonManifestName, pluginManifestLimit)
	if errors.Is(err, unix.ENOENT) {
		return empty, nil, nil
	}
	if err != nil {
		return empty, nil, err
	}
	var fs unix.Stat_t
	if unix.Fstatat(int(root.Fd()), addonManifestName, &fs, unix.AT_SYMLINK_NOFOLLOW) != nil || fs.Uid != uint32(os.Geteuid()) || fs.Mode&0022 != 0 {
		return empty, nil, errors.New("Unsafe Add-ons manifest permissions")
	}
	var m addonManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.Decode(new(any)) != io.EOF {
		return empty, nil, errors.New("Malformed Add-ons manifest")
	}
	if err = validateAddonManifest(m); err != nil {
		return empty, nil, err
	}
	return m, raw, nil
}
func openAddonState(data string, create bool) (*os.File, error) {
	fd, err := unix.Open(data, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if create {
		if e := unix.Mkdirat(fd, ".justvoxel", 0700); e != nil && !errors.Is(e, unix.EEXIST) {
			return nil, e
		}
		if e := unix.Fsync(fd); e != nil {
			return nil, e
		}
	}
	next, err := unix.Openat(fd, ".justvoxel", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(next), ".justvoxel"), nil
}
func loadAddonManifest(data string) (addonManifest, []byte, error) {
	empty := addonManifest{"v1", []addonEntry{}}
	root, err := openAddonState(data, false)
	if errors.Is(err, unix.ENOENT) {
		return empty, nil, nil
	}
	if err != nil {
		return empty, nil, err
	}
	defer root.Close()
	return readAddonManifestAt(root, empty)
}
func hashAddonAt(root *os.File, name string) (string, error) {
	if !safeAddonFilename(name) {
		return "", errors.New("Unsafe filename")
	}
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > pluginMaxJar {
		return "", errors.New("Unsafe managed content")
	}
	h := sha512.New()
	n, err := io.Copy(h, io.LimitReader(f, pluginMaxJar+1))
	if err != nil || n > pluginMaxJar {
		return "", errors.New("Content hash unavailable")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func verifyAddonEntry(data string, e addonEntry) error {
	root, err := openAddonDirectory(data, filepath.Dir(e.Target), false)
	if err != nil {
		return err
	}
	defer root.Close()
	hash, err := hashAddonAt(root, e.Filename)
	if err != nil || hash != e.SHA512 {
		return errors.New("Managed content is missing or changed; needs attention")
	}
	return nil
}
func addonCollision(data string, e addonEntry, m addonManifest) error {
	for _, owned := range m.Artifacts {
		if owned.Target == e.Target {
			if err := verifyAddonEntry(data, owned); err != nil {
				return err
			}
			return errors.New("This target is already managed; overwrite and updates are unavailable")
		}
	}
	root, err := openAddonDirectory(data, filepath.Dir(e.Target), false)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	var st unix.Stat_t
	err = unix.Fstatat(int(root.Fd()), e.Filename, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return errors.New("Target already exists or cannot be verified; manual files are never adopted")
}

var addonScalarPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+ -]{0,255}$`)

func addonDescriptorScalar(raw []byte, keys []string) (map[string]string, error) {
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, errors.New("Invalid descriptor text")
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, errors.New("Malformed extension descriptor")
		}
		wanted := false
		for _, k := range keys {
			if key == k {
				wanted = true
			}
		}
		if !wanted {
			continue
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("Duplicate descriptor field")
		}
		v = strings.TrimSpace(v)
		v, _, _ = strings.Cut(v, " #")
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"')) {
			v = v[1 : len(v)-1]
		}
		if !addonScalarPattern.MatchString(v) {
			return nil, errors.New("Invalid descriptor scalar")
		}
		out[key] = v
	}
	for _, key := range keys {
		if out[key] == "" {
			return nil, errors.New("Missing extension descriptor field: " + key)
		}
	}
	return out, nil
}
func validateAddon(file *os.File, kind, filename string) (string, string, string, error) {
	d, err := addonDefinitionFor(kind)
	if err != nil {
		return "", "", "", err
	}
	if !safeAddonFilename(filename) {
		return "", "", "", errors.New("Unsafe upload filename")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !strings.Contains(","+d.Accept+",", ","+ext+",") {
		return "", "", "", errors.New("Wrong file extension")
	}
	st, err := file.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > pluginMaxJar {
		return "", "", "", errors.New("Empty or oversized file")
	}
	if kind == "worldedit_schematic" {
		return filename, "", "Bounded non-empty schematic; WorldEdit performs clipboard format loading.", nil
	}
	if kind == "skript_script" {
		if st.Size() > 4<<20 {
			return "", "", "", errors.New("Script exceeds 4 MiB")
		}
		raw := make([]byte, st.Size())
		if _, err := file.ReadAt(raw, 0); err != nil {
			return "", "", "", err
		}
		if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 || len(bytes.TrimSpace(raw)) == 0 {
			return "", "", "", errors.New("Script must be non-empty UTF-8 without NUL bytes")
		}
		return filename, "", "Text format checked; Skript performs runtime parsing and loading.", nil
	}
	if !pluginZipDirectoryBounded(file, st.Size()) {
		return "", "", "", errors.New("Unbounded or malformed JAR directory")
	}
	archive, err := zip.NewReader(file, st.Size())
	if err != nil {
		return "", "", "", errors.New("Malformed ZIP/JAR")
	}
	entries := map[string]*zip.File{}
	classes := false
	var total uint64
	for _, entry := range archive.File {
		if entries[entry.Name] != nil {
			return "", "", "", errors.New("Duplicate JAR entry")
		}
		entries[entry.Name] = entry
		total += entry.UncompressedSize64
		if total > 256<<20 || entry.UncompressedSize64 > 128<<20 {
			return "", "", "", errors.New("Expanded JAR exceeds limit")
		}

		reader, e := entry.Open()
		if e != nil {
			return "", "", "", errors.New("Invalid JAR entry")
		}
		var headerSize int64
		if kind == "placeholderapi_expansion" && strings.HasSuffix(entry.Name, ".class") && entry.UncompressedSize64 >= 4 {
			var header [4]byte
			if _, e = io.ReadFull(reader, header[:]); e != nil {
				reader.Close()
				return "", "", "", errors.New("Corrupt class entry")
			}
			headerSize = 4
			if header == [4]byte{0xca, 0xfe, 0xba, 0xbe} {
				classes = true
			}
		}
		n, e := io.Copy(io.Discard, io.LimitReader(reader, int64(entry.UncompressedSize64)-headerSize+1))
		n += headerSize
		reader.Close()
		if e != nil || uint64(n) != entry.UncompressedSize64 {
			return "", "", "", errors.New("Corrupt JAR entry")
		}
	}
	if kind == "placeholderapi_expansion" {
		for _, wrong := range []string{"plugin.yml", "paper-plugin.yml", "velocity-plugin.json", "extension.yml", "bluemap.addon.json"} {
			if entries[wrong] != nil {
				return "", "", "", errors.New("Wrong hosted-content descriptor")
			}
		}
		if !classes {
			return "", "", "", errors.New("Expansion JAR lacks normal class content")
		}
		return filename, "", "JAR format checked; PlaceholderAPI performs runtime loading. Static validation does not prove a PlaceholderExpansion class.", nil
	}
	wanted := "extension.yml"
	if kind == "bluemap_addon" {
		wanted = "bluemap.addon.json"
	}
	descriptor := entries[wanted]
	if descriptor == nil || descriptor.UncompressedSize64 > 32<<10 {
		return "", "", "", errors.New("Missing or oversized root " + wanted)
	}
	reader, err := descriptor.Open()
	if err != nil {
		return "", "", "", err
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (32<<10)+1))
	reader.Close()
	if err != nil || len(raw) > 32<<10 {
		return "", "", "", errors.New("Invalid descriptor")
	}
	if kind == "geyser_extension" {
		fields, err := addonDescriptorScalar(raw, []string{"id", "name", "main", "api", "version"})
		if err != nil {
			return "", "", "", err
		}
		if !pluginMainPattern.MatchString(fields["main"]) {
			return "", "", "", errors.New("Invalid extension main")
		}
		return fields["name"], fields["id"], "JAR and extension descriptor checked; Geyser performs runtime loading.", nil
	}
	var fields struct {
		ID                   string   `json:"id"`
		Entrypoint           string   `json:"entrypoint"`
		Dependencies         []string `json:"dependencies"`
		OptionalDependencies []string `json:"optionalDependencies"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if dec.Decode(&fields) != nil || dec.Decode(new(any)) != io.EOF || !addonScalarPattern.MatchString(fields.ID) || !pluginMainPattern.MatchString(fields.Entrypoint) {
		return "", "", "", errors.New("Malformed BlueMap add-on descriptor")
	}
	return fields.ID, fields.ID, "JAR and native add-on descriptor checked; BlueMap performs runtime loading.", nil
}

type addonUpload struct {
	Kind, Filename, Fingerprint string
	Confirm                     bool
	File                        *os.File
}

func (u *addonUpload) close() error {
	if u.File == nil {
		return nil
	}
	name := u.File.Name()
	err := u.File.Close()
	removeErr := os.Remove(name)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	u.File = nil
	return errors.Join(err, removeErr)
}

func parseAddonUpload(w http.ResponseWriter, r *http.Request, apply bool) (u addonUpload, err error) {
	if r.URL.RawQuery != "" {
		return u, errors.New("Query fields are not accepted")
	}
	r.Body = http.MaxBytesReader(w, r.Body, addonUploadLimit)
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return u, errors.New("Multipart file upload required")
	}
	reader, e := r.MultipartReader()
	if e != nil {
		return u, e
	}
	seen := map[string]bool{}
	defer func() {
		if err != nil {
			err = errors.Join(err, u.close())
		}
	}()
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return u, e
		}
		key := part.FormName()
		if seen[key] {
			return u, errors.New("Duplicate upload field")
		}
		seen[key] = true
		if key == "file" {
			// Parse raw disposition: multipart.Part.FileName strips paths on Unix.
			_, disposition, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if e != nil || !safeAddonFilename(disposition["filename"]) {
				return u, errors.New("Unsafe upload filename")
			}
			u.Filename = disposition["filename"]
			u.File, e = os.CreateTemp("", "justvoxel-addon-*")
			if e != nil {
				return u, e
			}
			n, e := io.Copy(u.File, io.LimitReader(part, pluginMaxJar+1))
			if e != nil || n <= 0 || n > pluginMaxJar {
				return u, errors.New("Empty or oversized upload")
			}
			if e = u.File.Sync(); e != nil {
				return u, e
			}
		} else {
			if part.FileName() != "" || !(key == "type" || apply && (key == "plan_fingerprint" || key == "confirm_players")) {
				return u, errors.New("Unknown upload field")
			}
			raw, e := io.ReadAll(io.LimitReader(part, 513))
			if e != nil || len(raw) > 512 {
				return u, errors.New("Oversized upload field")
			}
			value := string(raw)
			switch key {
			case "type":
				u.Kind = value
			case "plan_fingerprint":
				u.Fingerprint = value
			case "confirm_players":
				if value != "true" && value != "false" {
					return u, errors.New("Invalid player confirmation")
				}
				u.Confirm = value == "true"
			}
		}
		part.Close()
	}
	if _, e = addonDefinitionFor(u.Kind); e != nil {
		return u, e
	}
	if u.File == nil || apply && (!operationFingerprintPattern.MatchString(u.Fingerprint) || !seen["confirm_players"]) {
		return u, errors.New("Incomplete reviewed upload")
	}
	return u, nil
}

type addonPlan struct {
	addonEntry
	Size                 int64               `json:"size"`
	Destination          string              `json:"destination"`
	Host                 addonHost           `json:"host"`
	Server               pluginServerContext `json:"server"`
	RestartRequired      bool                `json:"restart_required"`
	Validation           string              `json:"validation"`
	Online               int                 `json:"online"`
	Players              []string            `json:"players"`
	ConfirmationRequired bool                `json:"confirmation_required"`
	Fingerprint          string              `json:"plan_fingerprint"`
	DataPath             string              `json:"-"`
	Environment          string              `json:"-"`
}

func resolveAddonPlan(serverContext pluginServerContext, data string, u addonUpload) (addonPlan, error) {
	p := addonPlan{Server: serverContext, DataPath: data, RestartRequired: u.Kind != "worldedit_schematic", Players: []string{}}
	if !serverContext.Supported || serverContext.MinecraftVersion == "" {
		return p, errors.New("A known Paper or Purpur server is required")
	}
	name, identity, validation, err := validateAddon(u.File, u.Kind, u.Filename)
	if err != nil {
		return p, err
	}
	d, _ := addonDefinitionFor(u.Kind)
	inventory, err := readAddonHostInventory(data)
	if err != nil {
		return p, err
	}
	host, dest, err := resolveAddonHost(data, d, inventory)
	if err != nil {
		return p, err
	}
	if !host.Available {
		return p, errors.New(host.Message)
	}
	p.Host, p.Destination, p.Validation = host, dest, validation
	p.Environment, err = addonDirectoryEvidence(data, dest)
	if err != nil {
		return p, err
	}
	st, err := u.File.Stat()
	if err != nil {
		return p, err
	}
	p.Size = st.Size()
	if _, err = u.File.Seek(0, io.SeekStart); err != nil {
		return p, err
	}
	h := sha512.New()
	if _, err = io.Copy(h, io.LimitReader(u.File, pluginMaxJar+1)); err != nil {
		return p, err
	}
	p.addonEntry = addonEntry{u.Kind, u.Filename, dest + "/" + u.Filename, hex.EncodeToString(h.Sum(nil)), name, identity}
	m, raw, err := loadAddonManifest(data)
	if err != nil {
		return p, err
	}
	if err = addonCollision(data, p.addonEntry, m); err != nil {
		return p, err
	}
	evidence, _ := json.Marshal(struct {
		Entry               addonEntry
		Size                int64
		Server              pluginServerContext
		Data, Host, Parents string
		Manifest            []byte
	}{p.addonEntry, p.Size, p.Server, data, host.Identity, p.Environment, raw})
	sum := sha256.Sum256(evidence)
	p.Fingerprint = "sha256:" + hex.EncodeToString(sum[:])
	return p, nil
}
func (s *server) adminAddonInventory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations != nil {
		op, err := s.operations.currentPluginOperation()
		if err != nil || op != nil {
			writeError(w, 409, "Add-ons inventory awaits verified maintenance completion")
			return
		}
	}
	sc, data, ok := s.pluginContext(w, r)
	if !ok {
		return
	}
	out := addonInventory{Server: sc, Sections: []addonSection{}}
	if !sc.Supported {
		writeJSON(w, 200, out)
		return
	}
	inventory, err := readAddonHostInventory(data)
	if err != nil {
		writeError(w, 503, "Host inventory unavailable")
		return
	}
	m, _, err := loadAddonManifest(data)
	if err != nil {
		writeError(w, 409, "Add-ons ownership manifest needs attention")
		return
	}
	for _, d := range addonDefinitions {
		host, dest, e := resolveAddonHost(data, d, inventory)
		if e != nil {
			host.Available = false
			host.Status = "unavailable"
			host.Message = e.Error()
		}
		section := addonSection{d.Kind, d.Name, d.Accept, host, dest, []addonItem{}}
		for _, entry := range m.Artifacts {
			if entry.Kind != d.Kind {
				continue
			}
			item := addonItem{entry, "installed", "Managed by Add-ons", "Updates are not available yet"}
			if verifyAddonEntry(data, entry) != nil {
				item.State = "needs_attention"
			}
			section.Files = append(section.Files, item)
		}
		out.Sections = append(out.Sections, section)
	}
	writeJSON(w, 200, out)
}
func (s *server) adminAddonPlan(w http.ResponseWriter, r *http.Request) {
	s.adminAddonUpload(w, r, false)
}
func (s *server) adminAddonInstall(w http.ResponseWriter, r *http.Request) {
	s.adminAddonUpload(w, r, true)
}
func (s *server) adminAddonUpload(w http.ResponseWriter, r *http.Request, apply bool) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	u, err := parseAddonUpload(w, r, apply)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	defer u.close()
	sc, data, ok := s.pluginContext(w, r)
	if !ok {
		return
	}
	p, err := resolveAddonPlan(sc, data, u)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if p.RestartRequired {
		p.Online, p.Players, err = pluginInstallPlayers(r.Context(), pluginInstanceID(r.Context()))
		if err != nil {
			writeError(w, 503, "Player status unavailable")
			return
		}
		p.ConfirmationRequired = p.Online > 0
	}
	if !apply {
		if err = u.close(); err != nil {
			writeError(w, 503, "Temporary upload cleanup failed; review was not retained")
			return
		}
		writeJSON(w, 200, p)
		return
	}
	if p.Fingerprint != u.Fingerprint {
		writeError(w, 409, "Add-ons file, host or destination changed; review again")
		return
	}
	if p.ConfirmationRequired && !u.Confirm {
		writeError(w, 409, "Players are online; review and confirm the restart")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Maintenance tracking unavailable")
		return
	}
	journal, created, err := s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginAddonInstall, p.Server.InstanceID)
	if err != nil {
		writeError(w, 409, "Another maintenance operation requires attention")
		return
	}
	if created {
		file := u.File
		u.File = nil
		go func() {
			defer func() { name := file.Name(); file.Close(); os.Remove(name) }()
			executeAddonInstall(s, journal.OperationID, p, file, u.Confirm, actor)
		}()
	}
	writeJSON(w, 202, adminOperationResponse{Operation: &journal})
}
