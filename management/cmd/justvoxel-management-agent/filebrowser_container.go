package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"
)

const fileBrowserImage = "ghcr.io/gtsteffaniak/filebrowser:stable-slim"
const fileBrowserConfig = "/usr/share/justvoxel/filebrowser/config.yaml"
const fileBrowserRuntimeConfig = "/run/justvoxel-filebrowser/config.yaml"

type fileBrowserSource struct {
	Host         string
	Path         string
	Name         string
	ReadOnly     bool
	NonRecursive bool
	Propagation  string
}

func safeFileBrowserRoot(path string) error {
	if err := validateInstanceRuntimePath(path); err != nil {
		return err
	}
	// Reject both sensitive trees and broad ancestors which would include them.
	for _, private := range []string{
		"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/run", "/root", "/home",
		"/var/lib/containers", "/var/lib/justvoxel-web", "/var/lib/justvoxel-instance-runtime",
		stateDir, minecraftInstanceDirectory, "/var/lib/justvoxel/management",
		"/var/lib/justvoxel/config-backups", "/var/lib/justvoxel/state", "/var/lib/justvoxel/exports",
	} {
		if instanceRuntimeInside(path, private) || instanceRuntimeInside(private, path) {
			return errors.New("File Browser root includes private state")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("File Browser source is not a directory")
	}
	return nil
}

// Automatic roots are fixed appliance policy, never supplied by a caller.
// Lstat prevents a symlink at an automatic root from redirecting the source.
func automaticFileBrowserSources(lstat func(string) (os.FileInfo, error)) ([]fileBrowserSource, error) {
	candidates := []fileBrowserSource{
		{Host: "/var/mnt", Path: "/data/mounted/var-mnt", Name: "Mounted storage", Propagation: "rslave"},
		{Host: "/mnt", Path: "/data/mounted/mnt", Name: "Mounts", Propagation: "rslave"},
		{Host: "/media", Path: "/data/mounted/media", Name: "Media", Propagation: "rslave"},
		{Host: "/run/media", Path: "/data/mounted/run-media", Name: "Removable media", Propagation: "rslave"},
		{Host: "/usr", Path: "/data/system/usr", Name: "System /usr (read-only)", ReadOnly: true},
		{Host: "/boot", Path: "/data/system/boot", Name: "System /boot (read-only)", ReadOnly: true},
	}
	sources := []fileBrowserSource{}
	for _, source := range candidates {
		info, err := lstat(source.Host)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		sources = append(sources, source)
	}
	return sources, nil
}

var discoverAutomaticFileBrowserSources = func() ([]fileBrowserSource, error) {
	return automaticFileBrowserSources(os.Lstat)
}

// Preserve registered committed data paths and authoritative backup roots as
// exact nonrecursive sources, independently of the automatic parent sources.
func fileBrowserSources(ctx context.Context) ([]fileBrowserSource, error) {
	b := instanceRuntimes
	records, err := b.records()
	if err != nil {
		return nil, err
	}
	sources := []fileBrowserSource{}
	backups := map[string]bool{}
	for _, record := range records {
		if err := validateInstanceRecordPlacement(record); err != nil {
			return nil, err
		}
		if err := b.storage(ctx, "validate", record); err != nil {
			return nil, err
		}
		if err := safeFileBrowserRoot(record.Storage.DataPath); err != nil {
			return nil, err
		}
		name := "Minecraft Servers"
		if len(records) > 1 {
			name += " (" + record.Runtime.ID + ")"
		}
		sources = append(sources, fileBrowserSource{Host: record.Storage.DataPath, Path: "/data/minecraft/" + record.Runtime.ID, Name: name, NonRecursive: true})
		// Inventory validates backup mount identity without creating/changing data.
		// Unavailable optional backup storage must never expose a fallback directory.
		if _, err := b.backupStorage(ctx, "inventory", record.Runtime.ID); err != nil {
			continue
		}
		root := ""
		if record.Backups != nil {
			root = record.Backups.Root
		} else {
			snapshot, _, _, err := b.snapshot(ctx)
			if err != nil {
				return nil, err
			}
			root = snapshot.BackupPath
		}
		if err := safeFileBrowserRoot(root); err != nil {
			return nil, err
		}
		backups[root] = true
	}
	// Stable source names/paths survive recreation and persisted user scopes.
	roots := make([]string, 0, len(backups))
	for root := range backups {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for i, root := range roots {
		name := "Backups"
		if len(roots) > 1 {
			name += fmt.Sprintf(" (%d)", i+1)
		}
		sources = append(sources, fileBrowserSource{Host: root, Path: fmt.Sprintf("/data/backups/%d", i+1), Name: name, NonRecursive: true})
	}
	automatic, err := discoverAutomaticFileBrowserSources()
	if err != nil {
		return nil, err
	}
	sources = append(sources, automatic...)
	if len(sources) == 0 {
		return nil, errors.New("No File Browser sources are available")
	}
	return sources, nil
}

func fileBrowserContainerArgs(sources []fileBrowserSource) []string {
	args := []string{"run", "--rm", "--replace", "--name", "justvoxel-filebrowser", "--pull=newer",
		"--publish", "127.0.0.1:61209:8080", "--security-opt", "label=disable", "--security-opt", "no-new-privileges",
		"--cap-drop=ALL",
		// Backup archives are root-owned 0600 under root-owned 0700 directories
		// (instance-backup). This one capability permits file management without
		// changing host ownership/modes or running FileBrowser as root.
		"--cap-add=DAC_OVERRIDE", "--user", "1000:1000", "--health-cmd=none",
		"--env", "FILEBROWSER_CONFIG=/etc/filebrowser/config.yaml",
		"--mount", "type=volume,source=justvoxel-filebrowser-state,target=/home/filebrowser/data",
		"--mount", "type=bind,source=" + fileBrowserRuntimeConfig + ",target=/etc/filebrowser/config.yaml,ro=true"}
	for _, source := range sources {
		mount := "type=bind,source=" + source.Host + ",target=" + source.Path
		if source.NonRecursive {
			mount += ",bind-nonrecursive=true"
		}
		if source.Propagation != "" {
			mount += ",bind-propagation=" + source.Propagation
		}
		if source.ReadOnly {
			mount += ",ro=true"
		}
		args = append(args, "--mount", mount)
	}
	return append(args, fileBrowserImage)
}

func renderFileBrowserConfig(base []byte, sources []fileBrowserSource) ([]byte, error) {
	// JSON is a YAML subset; packaged and generated files use the upstream YAML
	// schema, with JSON encoding to avoid interpolating paths into YAML syntax.
	var config map[string]any
	if err := json.Unmarshal(base, &config); err != nil {
		return nil, err
	}
	server, ok := config["server"].(map[string]any)
	if !ok {
		return nil, errors.New("File Browser server configuration missing")
	}
	entries := []any{}
	for _, source := range sources {
		entries = append(entries, map[string]any{"path": source.Path, "name": source.Name, "config": map[string]any{
			"readOnly": source.ReadOnly, "private": true, "defaultEnabled": true, "defaultUserScope": "/",
			"rules":              []any{map[string]any{"ignoreSymlinks": true}},
			"defaultPermissions": map[string]bool{"view": true, "download": true, "create": !source.ReadOnly, "modify": !source.ReadOnly, "delete": !source.ReadOnly, "configured": true},
		}})
	}
	server["sources"] = entries
	return json.MarshalIndent(config, "", "  ")
}

func runFileBrowserContainer() error {
	if os.Geteuid() != 0 {
		return errors.New("File Browser service requires appliance privileges")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	sources, err := fileBrowserSources(ctx)
	if err != nil {
		return err
	}
	base, err := os.ReadFile(fileBrowserConfig)
	if err != nil {
		return err
	}
	config, err := renderFileBrowserConfig(base, sources)
	if err != nil {
		return err
	}
	if err := os.WriteFile(fileBrowserRuntimeConfig, config, 0644); err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	// Replace this service process so systemd owns the foreground Podman process.
	return syscall.Exec("/usr/bin/podman", append([]string{"podman"}, fileBrowserContainerArgs(sources)...), os.Environ())
}
