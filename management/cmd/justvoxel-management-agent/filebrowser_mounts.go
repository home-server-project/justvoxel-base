package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The agent runs in the host mount namespace. Keep the reader injectable without
// invoking another discovery command or depending on mountpoint conventions.
var readFileBrowserMountInfo = func() ([]byte, error) { return os.ReadFile("/proc/self/mountinfo") }

type fileBrowserMount struct {
	ID           int
	ParentID     int
	MajorMinor   string
	Root         string
	Mountpoint   string
	Options      string
	Filesystem   string
	Source       string
	SuperOptions string
	ReadOnly     bool
}

func decodeFileBrowserMountPath(value string) string {
	// A single pass matters: an escaped backslash followed by 040 is literal text.
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}

func fileBrowserMountOption(options, wanted string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == wanted {
			return true
		}
	}
	return false
}

func parseFileBrowserMountInfo(data []byte) ([]fileBrowserMount, error) {
	mounts := []fileBrowserMount{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' })
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(fields) != separator+4 {
			return nil, errors.New("invalid File Browser mount table")
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil || id <= 0 {
			return nil, errors.New("invalid mount ID")
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil || parent < 0 {
			return nil, errors.New("invalid parent mount ID")
		}
		numbers := strings.Split(fields[2], ":")
		if len(numbers) != 2 {
			return nil, errors.New("invalid mount device identity")
		}
		for _, number := range numbers {
			if _, err := strconv.ParseUint(number, 10, 32); err != nil {
				return nil, errors.New("invalid mount device identity")
			}
		}
		mount := fileBrowserMount{ID: id, ParentID: parent, MajorMinor: fields[2], Root: decodeFileBrowserMountPath(fields[3]), Mountpoint: decodeFileBrowserMountPath(fields[4]), Options: fields[5], Filesystem: fields[separator+1], Source: decodeFileBrowserMountPath(fields[separator+2]), SuperOptions: fields[separator+3]}
		if !filepath.IsAbs(mount.Mountpoint) || filepath.Clean(mount.Mountpoint) != mount.Mountpoint {
			return nil, errors.New("invalid mountpoint")
		}
		mount.ReadOnly = fileBrowserMountOption(mount.Options, "ro") || fileBrowserMountOption(mount.SuperOptions, "ro")
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

func fileBrowserNetworkProtocol(filesystem string) string {
	switch filesystem {
	case "nfs", "nfs4":
		return "NFS"
	case "cifs", "smb3":
		return "SMB"
	}
	return ""
}

func fileBrowserExcludedFilesystem(filesystem string) bool {
	switch filesystem {
	case "", "proc", "sysfs", "devtmpfs", "tmpfs", "overlay", "cgroup", "cgroup2", "securityfs", "debugfs", "tracefs", "efivarfs", "ramfs", "squashfs", "autofs", "fusectl", "mqueue", "pstore", "configfs":
		return true
	}
	return false
}

func newFileBrowserFilesystem(kind, identity, name string, mount fileBrowserMount, readOnly bool) fileBrowserSource {
	sum := sha256.Sum256([]byte(strings.Join([]string{kind, mount.Filesystem, identity, mount.Mountpoint}, "\x00")))
	return fileBrowserSource{Host: mount.Mountpoint, Path: "/data/filesystems/" + hex.EncodeToString(sum[:12]), Name: name, ReadOnly: readOnly, NonRecursive: true, Mount: &mount}
}

func fileBrowserMountDepth(path string) int {
	if path == "/" {
		return 0
	}
	return strings.Count(path, "/")
}

func mountedFileBrowserSources(storage adminStorageDiscovery, mounts []fileBrowserMount) ([]fileBrowserSource, error) {
	byPath := map[string]fileBrowserMount{}
	counts := map[string]int{}
	for _, mount := range mounts {
		counts[mount.Mountpoint]++
		byPath[mount.Mountpoint] = mount
	}
	sources := []fileBrowserSource{}
	seen := map[string]bool{}
	for _, device := range storage.Devices {
		if device.System || device.MajorMinor == "" || fileBrowserExcludedFilesystem(device.Filesystem) || fileBrowserNetworkProtocol(device.Filesystem) != "" {
			continue
		}
		candidates := []fileBrowserMount{}
		for _, point := range device.Mountpoints {
			// Never select a hidden mount when an exposed mountpoint is stacked.
			if point == "/" || counts[point] > 1 {
				continue
			}
			// Only a complete filesystem with Storage's device identity is eligible.
			// A bind subtree cannot stand in for the full local filesystem.
			if mount, ok := byPath[point]; ok && mount.Filesystem == device.Filesystem && mount.MajorMinor == device.MajorMinor && mount.Root == "/" {
				candidates = append(candidates, mount)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			left, right := fileBrowserMountDepth(candidates[i].Mountpoint), fileBrowserMountDepth(candidates[j].Mountpoint)
			if left != right {
				return left < right
			}
			return candidates[i].Mountpoint < candidates[j].Mountpoint
		})
		if len(candidates) == 0 {
			continue
		}
		mount := candidates[0]
		// Repeated device records must not produce duplicate filesystem entries.
		key := mount.MajorMinor + "\x00" + mount.Filesystem
		if seen[key] {
			continue
		}
		seen[key] = true
		name := device.Label
		if name == "" {
			name = filepath.Base(mount.Mountpoint)
		}
		if device.Name != "" && (device.Label != "" || name != device.Name) {
			name += " (" + device.Name + ")"
		}
		sources = append(sources, newFileBrowserFilesystem("local", device.Path+"\x00"+device.UUID, name, mount, device.ReadOnly || mount.ReadOnly))
	}
	for _, mount := range mounts {
		protocol := fileBrowserNetworkProtocol(mount.Filesystem)
		if protocol == "" || mount.Mountpoint == "/" {
			continue
		}
		if counts[mount.Mountpoint] > 1 {
			continue
		}
		sources = append(sources, newFileBrowserFilesystem("network", mount.Source, filepath.Base(mount.Mountpoint)+" ("+protocol+")", mount, mount.ReadOnly))
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Host < sources[j].Host })
	return sources, nil
}

var discoverFileBrowserFilesystems = func(ctx context.Context) ([]fileBrowserSource, error) {
	output, err := runAdminDiscoveryHelper(ctx, "storage")
	if err != nil {
		return nil, err
	}
	var storage adminStorageDiscovery
	if err := json.Unmarshal(output, &storage); err != nil {
		return nil, err
	}
	data, err := readFileBrowserMountInfo()
	if err != nil {
		return nil, err
	}
	mounts, err := parseFileBrowserMountInfo(data)
	if err != nil {
		return nil, err
	}
	return mountedFileBrowserSources(storage, mounts)
}

func nameFileBrowserSources(sources []fileBrowserSource) {
	used := map[string]bool{}
	for i := range sources {
		base := sources[i].Name
		name := base
		for suffix := 2; used[name]; suffix++ {
			name = fmt.Sprintf("%s (%d)", base, suffix)
		}
		sources[i].Name = name
		used[name] = true
	}
}

func fileBrowserPathCovered(path string, sources []fileBrowserSource) bool {
	for _, source := range sources {
		if source.Mount != nil && instanceRuntimeInside(path, source.Host) {
			return true
		}
	}
	return false
}

func revalidateFileBrowserMounts(sources []fileBrowserSource, reader func() ([]byte, error), lstat func(string) (os.FileInfo, error)) error {
	data, err := reader()
	if err != nil {
		return err
	}
	mounts, err := parseFileBrowserMountInfo(data)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.Mount == nil {
			continue
		}
		info, err := lstat(source.Host)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("File Browser mountpoint is not a real directory")
		}
		found := false
		for _, current := range mounts {
			if current.Mountpoint != source.Host {
				continue
			}
			expected := source.Mount
			if found || current.ID != expected.ID || current.MajorMinor != expected.MajorMinor || current.Root != expected.Root || current.Filesystem != expected.Filesystem || current.Source != expected.Source || current.ReadOnly != expected.ReadOnly {
				return errors.New("File Browser filesystem mount changed; retry")
			}
			found = true
		}
		if !found {
			return errors.New("File Browser filesystem mount disappeared; retry")
		}
	}
	return nil
}
