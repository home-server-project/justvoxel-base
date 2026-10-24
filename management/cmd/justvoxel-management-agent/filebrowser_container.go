package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
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
	Mount        *fileBrowserMount
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

// Discover mounted filesystems before considering exact curated application roots.
func fileBrowserSources(ctx context.Context) ([]fileBrowserSource, error) {
	sources, err := discoverFileBrowserFilesystems(ctx)
	if err != nil {
		return nil, err
	}
	filesystems := append([]fileBrowserSource(nil), sources...)
	b := instanceRuntimes
	records, err := b.records()
	if err != nil {
		return nil, err
	}
	backups := map[string]bool{}
	for _, record := range records {
		if err := validateInstanceRecordPlacement(record); err != nil {
			return nil, err
		}
		if err := b.storage(ctx, "validate", record); err != nil {
			return nil, err
		}
		name := "Minecraft Servers"
		if len(records) > 1 {
			name += " (" + record.Runtime.ID + ")"
		}
		if !fileBrowserPathCovered(record.Storage.DataPath, filesystems) {
			if err := safeFileBrowserRoot(record.Storage.DataPath); err != nil {
				return nil, err
			}
			sources = append(sources, fileBrowserSource{Host: record.Storage.DataPath, Path: "/data/minecraft/" + record.Runtime.ID, Name: name, NonRecursive: true})
		}
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
		if !fileBrowserPathCovered(root, filesystems) {
			if err := safeFileBrowserRoot(root); err != nil {
				return nil, err
			}
			backups[root] = true
		}
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
	if len(sources) == 0 {
		return nil, errors.New("No File Browser sources are available")
	}
	nameFileBrowserSources(sources)
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
		fields := []string{"type=bind", "source=" + source.Host, "target=" + source.Path}
		if source.NonRecursive {
			fields = append(fields, "bind-nonrecursive=true")
		}
		if source.ReadOnly {
			fields = append(fields, "ro=true")
		}
		// Podman's --mount value is CSV, not shell text. Quote literal commas
		// and quotes in host mountpoints without creating additional options.
		var encoded strings.Builder
		writer := csv.NewWriter(&encoded)
		_ = writer.Write(fields) // strings.Builder writes cannot fail.
		writer.Flush()
		mount := strings.TrimSuffix(encoded.String(), "\n")
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
	args := append([]string{"podman"}, fileBrowserContainerArgs(sources)...)
	if err := revalidateFileBrowserMounts(sources, readFileBrowserMountInfo, os.Lstat); err != nil {
		return err
	}
	// Replace this service process so systemd owns the foreground Podman process.
	return syscall.Exec("/usr/bin/podman", args, os.Environ())
}
