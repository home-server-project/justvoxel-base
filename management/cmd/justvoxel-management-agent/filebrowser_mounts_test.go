package main

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestFileBrowserMountInfo(t *testing.T) {
	for _, tc := range []struct {
		name, line, point, source string
		readOnly                  bool
	}{
		{"local", `42 1 253:17 / /srv/archive rw,relatime shared:8 - xfs /dev/vdb1 rw,seclabel`, "/srv/archive", "/dev/vdb1", false},
		{"escapes", `42 1 253:17 /a\040b /srv/a\040b\134c\011d\012e rw - xfs /dev/a\134b rw`, "/srv/a b\\c\td\ne", `/dev/a\b`, false},
		{"mount ro", `42 1 253:17 / /srv/archive ro,relatime - xfs /dev/vdb1 rw`, "/srv/archive", "/dev/vdb1", true},
		{"super ro", `42 1 253:17 / /srv/archive rw - xfs /dev/vdb1 ro,seclabel`, "/srv/archive", "/dev/vdb1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mounts, err := parseFileBrowserMountInfo([]byte(tc.line + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			if len(mounts) != 1 {
				t.Fatal(mounts)
			}
			m := mounts[0]
			if m.ID != 42 || m.ParentID != 1 || m.MajorMinor != "253:17" || m.Filesystem != "xfs" || m.Mountpoint != tc.point || m.Source != tc.source || m.ReadOnly != tc.readOnly || m.Options == "" || m.SuperOptions == "" {
				t.Fatalf("incorrect parse: %#v", m)
			}
			expectedRoot := "/"
			if tc.name == "escapes" {
				expectedRoot = "/a b"
			}
			if m.Root != expectedRoot {
				t.Fatal(m.Root)
			}
		})
	}
	if got := decodeFileBrowserMountPath(`\134040`); got != `\040` {
		t.Fatalf("recursive decoding: %q", got)
	}
	for _, line := range []string{"bad", `x 1 8:1 / /srv rw - xfs /dev/vdb1 rw`, `1 0 bad / /srv rw - xfs /dev/vdb1 rw`, `1 0 8:1 / /srv rw - xfs /dev/vdb1`} {
		if _, err := parseFileBrowserMountInfo([]byte(line)); err == nil {
			t.Fatalf("accepted malformed mountinfo %q", line)
		}
	}
}

func TestMountedFileBrowserSources(t *testing.T) {
	mounts, err := parseFileBrowserMountInfo([]byte(`1 0 8:1 / / rw - xfs /dev/vda1 rw
2 1 8:17 / /srv/archive rw - xfs /dev/vdb1 rw
3 1 8:17 / /a/deeper/archive rw - xfs /dev/vdb1 rw
4 1 8:17 / /zzz rw - xfs /dev/vdb1 rw
5 1 8:33 / /opt/family-disk ro - ext4 /dev/vdc1 rw
6 1 8:49 / /srv/vdd1 rw - ext4 /dev/vdd1 rw
7 1 0:40 / /home/voxel/family rw - cifs //nas/family rw
8 1 0:41 / /elsewhere/family ro - smb3 //other/family rw
9 1 0:42 / /srv/media rw - nfs nas:/media rw
10 1 0:43 / /opt/remote/media rw - nfs4 nas:/media2 ro
11 1 8:65 / /srv/read-only-device rw - ext4 /dev/vde1 rw
`))
	if err != nil {
		t.Fatal(err)
	}
	storage := adminStorageDiscovery{Devices: []adminStorageDevice{
		{Name: "vda1", MajorMinor: "8:1", Path: "/dev/vda1", Filesystem: "xfs", Mountpoints: []string{"/"}, System: true},
		{Name: "vdb1", MajorMinor: "8:17", Path: "/dev/vdb1", Filesystem: "xfs", Label: "BACKUP", Mountpoints: []string{"/missing", "/a/deeper/archive", "/zzz", "/srv/archive"}},
		{Name: "vdc1", MajorMinor: "8:33", Path: "/dev/vdc1", Filesystem: "ext4", Mountpoints: []string{"/opt/family-disk"}},
		{Name: "vdd1", MajorMinor: "8:49", Path: "/dev/vdd1", Filesystem: "ext4", Mountpoints: []string{"/srv/vdd1"}},
		{Name: "vde1", MajorMinor: "8:65", Path: "/dev/vde1", Filesystem: "ext4", Mountpoints: []string{"/srv/read-only-device"}, ReadOnly: true},
		{Name: "empty", Path: "/dev/vdf1", Mountpoints: []string{"/not-mounted"}},
	}}
	sources, err := mountedFileBrowserSources(storage, mounts)
	if err != nil {
		t.Fatal(err)
	}
	again, err := mountedFileBrowserSources(storage, mounts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sources, again) {
		t.Fatal("discovery is not deterministic")
	}
	// Mount table and inventory enumeration order must not change identities.
	for i, j := 0, len(mounts)-1; i < j; i, j = i+1, j-1 {
		mounts[i], mounts[j] = mounts[j], mounts[i]
	}
	reordered, err := mountedFileBrowserSources(storage, mounts)
	if err != nil || !reflect.DeepEqual(sources, reordered) {
		t.Fatalf("mount order changed sources: %#v %v", reordered, err)
	}
	// Restore the fixture order for the later canonical-mountpoint example.
	for i, j := 0, len(mounts)-1; i < j; i, j = i+1, j-1 {
		mounts[i], mounts[j] = mounts[j], mounts[i]
	}
	nameFileBrowserSources(sources)
	want := map[string]struct {
		name string
		ro   bool
	}{
		"/zzz":                  {"BACKUP (vdb1)", false}, // shallowest mount, despite input order
		"/opt/family-disk":      {"family-disk (vdc1)", true},
		"/srv/vdd1":             {"vdd1", false},
		"/srv/read-only-device": {"read-only-device (vde1)", true},
		"/home/voxel/family":    {"family (SMB) (2)", false},
		"/elsewhere/family":     {"family (SMB)", true},
		"/srv/media":            {"media (NFS) (2)", false},
		"/opt/remote/media":     {"media (NFS)", true},
	}
	if len(sources) != len(want) {
		t.Fatalf("sources: %#v", sources)
	}
	targets := map[string]bool{}
	for _, source := range sources {
		expected, ok := want[source.Host]
		if !ok || source.Name != expected.name || source.ReadOnly != expected.ro || !source.NonRecursive || source.Mount == nil {
			t.Fatalf("source: %#v", source)
		}
		if !regexp.MustCompile(`^/data/filesystems/[a-f0-9]{24}$`).MatchString(source.Path) || targets[source.Path] {
			t.Fatal("unsafe or colliding target", source.Path)
		}
		targets[source.Path] = true
		args := fileBrowserContainerArgs([]fileBrowserSource{source})
		bind := args[len(args)-2]
		if !strings.Contains(bind, ",bind-nonrecursive=true") || strings.Contains(bind, "rslave") || strings.Contains(bind, ",ro=true") != source.ReadOnly {
			t.Fatal(bind)
		}
	}
	// Same-depth tie uses lexical mountpoint order, independent of inventory order.
	storage.Devices[1].Mountpoints = []string{"/zzz", "/srv/archive", "/aaa"}
	alternate := mounts[1]
	alternate.ID = 12
	alternate.Mountpoint = "/aaa"
	sources, err = mountedFileBrowserSources(storage, append(mounts, alternate))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Name == "BACKUP (vdb1)" && source.Host != "/aaa" {
			t.Fatal("canonical mountpoint", source.Host)
		}
	}
}

func TestFileBrowserLocalDeviceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, majorMinor string
		want             int
	}{
		{"matching device with different source alias", "8:17", 1},
		{"replaced device at same path and filesystem", "8:33", 0},
		{"missing device identity", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := adminStorageDiscovery{Devices: []adminStorageDevice{{Path: "/dev/disk/by-uuid/archive", MajorMinor: tc.majorMinor, Filesystem: "xfs", Mountpoints: []string{"/arbitrary/archive"}}}}
			mounts := []fileBrowserMount{{MajorMinor: "8:17", Root: "/", Mountpoint: "/arbitrary/archive", Filesystem: "xfs", Source: "/dev/vdb1"}}
			sources, err := mountedFileBrowserSources(storage, mounts)
			if err != nil || len(sources) != tc.want {
				t.Fatalf("sources = %#v, error = %v", sources, err)
			}
		})
	}
}

func TestFileBrowserLocalCompleteFilesystemRoot(t *testing.T) {
	storage := adminStorageDiscovery{Devices: []adminStorageDevice{{MajorMinor: "8:17", Filesystem: "xfs", Mountpoints: []string{"/short", "/real/filesystem", "/deep/real/filesystem", "/zzzz/filesystem"}}}}
	subtree := fileBrowserMount{ID: 1, MajorMinor: "8:17", Root: "/subtree", Mountpoint: "/short", Filesystem: "xfs"}
	complete := fileBrowserMount{ID: 2, MajorMinor: "8:17", Root: "/", Mountpoint: "/real/filesystem", Filesystem: "xfs"}
	deeper, lexical := complete, complete
	deeper.ID, deeper.Mountpoint = 3, "/deep/real/filesystem"
	lexical.ID, lexical.Mountpoint = 4, "/zzzz/filesystem"
	for _, mounts := range [][]fileBrowserMount{{subtree, lexical, deeper, complete}, {complete, deeper, lexical, subtree}} {
		sources, err := mountedFileBrowserSources(storage, mounts)
		if err != nil || len(sources) != 1 || sources[0].Host != complete.Mountpoint {
			t.Fatalf("complete filesystem not selected: %#v %v", sources, err)
		}
	}
	sources, err := mountedFileBrowserSources(storage, []fileBrowserMount{subtree})
	if err != nil || len(sources) != 0 {
		t.Fatalf("subtree exposed as complete filesystem: %#v %v", sources, err)
	}
}

func TestFileBrowserStackedMountsDoNotAbortDiscovery(t *testing.T) {
	storage := adminStorageDiscovery{Devices: []adminStorageDevice{
		{Name: "stacked", MajorMinor: "8:17", Filesystem: "xfs", Mountpoints: []string{"/stacked"}},
		{Name: "valid", MajorMinor: "8:33", Filesystem: "ext4", Mountpoints: []string{"/arbitrary/local"}},
	}}
	mounts := []fileBrowserMount{
		{ID: 1, MajorMinor: "8:17", Root: "/", Mountpoint: "/stacked", Filesystem: "xfs"},
		{ID: 2, MajorMinor: "8:49", Root: "/", Mountpoint: "/stacked", Filesystem: "xfs"},
		{ID: 3, MajorMinor: "8:33", Root: "/", Mountpoint: "/arbitrary/local", Filesystem: "ext4"},
		{ID: 4, MajorMinor: "0:40", Root: "/export", Mountpoint: "/stacked-network", Filesystem: "nfs"},
		{ID: 5, MajorMinor: "0:41", Root: "/", Mountpoint: "/stacked-network", Filesystem: "cifs"},
		{ID: 6, MajorMinor: "0:42", Root: "/export", Mountpoint: "/anywhere/nfs", Filesystem: "nfs4"},
		{ID: 7, MajorMinor: "0:43", Root: "/share", Mountpoint: "/anywhere/smb", Filesystem: "smb3"},
	}
	for _, alternate := range []bool{false, true} {
		t.Run(map[bool]string{false: "only stacked local candidate", true: "alternate canonical local mount"}[alternate], func(t *testing.T) {
			inventory := storage
			inventory.Devices = append([]adminStorageDevice(nil), storage.Devices...)
			table := append([]fileBrowserMount(nil), mounts...)
			want := []string{"/anywhere/nfs", "/anywhere/smb", "/arbitrary/local"}
			if alternate {
				inventory.Devices[0].Mountpoints = []string{"/stacked", "/other/local"}
				table = append(table, fileBrowserMount{ID: 8, MajorMinor: "8:17", Root: "/", Mountpoint: "/other/local", Filesystem: "xfs"})
				want = append(want, "/other/local")
			}
			for order := 0; order < 2; order++ {
				sources, err := mountedFileBrowserSources(inventory, table)
				if err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, source := range sources {
					got = append(got, source.Host)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("sources = %v, want %v", got, want)
				}
				for i, j := 0, len(table)-1; i < j; i, j = i+1, j-1 {
					table[i], table[j] = table[j], table[i]
				}
			}
		})
	}
}

func TestFileBrowserNetworkExcludesHostRootOnly(t *testing.T) {
	for _, fs := range []string{"nfs", "nfs4", "cifs", "smb3"} {
		t.Run(fs, func(t *testing.T) {
			mounts := []fileBrowserMount{
				{ID: 1, Root: "/export", Mountpoint: "/", Filesystem: fs},
				{ID: 2, Root: "/export/subtree", Mountpoint: "/arbitrary/network/location", Filesystem: fs},
			}
			sources, err := mountedFileBrowserSources(adminStorageDiscovery{}, mounts)
			if err != nil || len(sources) != 1 || sources[0].Host != mounts[1].Mountpoint || sources[0].Mount.Root != mounts[1].Root {
				t.Fatalf("network root exclusion: %#v %v", sources, err)
			}
		})
	}
}

func TestFileBrowserExcludedFilesystems(t *testing.T) {
	for _, fs := range []string{"proc", "sysfs", "devtmpfs", "tmpfs", "overlay", "cgroup", "cgroup2", "securityfs", "debugfs", "tracefs", "efivarfs", "ramfs", "squashfs", "autofs", "fusectl", "mqueue", "pstore", "configfs", ""} {
		sources, err := mountedFileBrowserSources(adminStorageDiscovery{Devices: []adminStorageDevice{{Name: "pseudo", MajorMinor: "8:17", Filesystem: fs, Mountpoints: []string{"/arbitrary"}}}}, []fileBrowserMount{{MajorMinor: "8:17", Root: "/", Mountpoint: "/arbitrary", Filesystem: fs}})
		if err != nil || len(sources) != 0 {
			t.Fatalf("exposed %s: %#v %v", fs, sources, err)
		}
	}
}

func TestFileBrowserUsesStorageAuthorityAndMountReader(t *testing.T) {
	oldHelper, oldReader := runAdminDiscoveryHelper, readFileBrowserMountInfo
	t.Cleanup(func() { runAdminDiscoveryHelper, readFileBrowserMountInfo = oldHelper, oldReader })
	runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
		if action != "storage" {
			t.Fatalf("unexpected discovery %s", action)
		}
		return []byte(`{"devices":[{"name":"vdb1","path":"/dev/vdb1","major_minor":"8:17","filesystem":"xfs","mountpoints":["/arbitrary/data"]}]}`), nil
	}
	readFileBrowserMountInfo = func() ([]byte, error) { return []byte("42 1 8:17 / /arbitrary/data rw - xfs /dev/vdb1 rw\n"), nil }
	sources, err := discoverFileBrowserFilesystems(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("%#v %v", sources, err)
	}
	readFileBrowserMountInfo = func() ([]byte, error) { return nil, errors.New("unreadable mount table") }
	if _, err := discoverFileBrowserFilesystems(context.Background()); err == nil {
		t.Fatal("mountinfo failure ignored")
	}
}

func TestFileBrowserMountRevalidation(t *testing.T) {
	directory, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	symlink, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	original := "42 1 8:17 / /srv/archive rw - xfs /dev/vdb1 rw\n"
	mounts, err := parseFileBrowserMountInfo([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	source := newFileBrowserFilesystem("local", "/dev/vdb1", "archive", mounts[0], false)
	for _, tc := range []struct {
		name, line string
		info       os.FileInfo
		wantErr    bool
	}{
		{"same", original, directory, false},
		{"gone", "", directory, true},
		{"mount ID", strings.Replace(original, "42 1", "43 1", 1), directory, true},
		{"device", strings.Replace(original, "8:17", "8:33", 1), directory, true},
		{"source", strings.Replace(original, "/dev/vdb1", "/dev/vdc1", 1), directory, true},
		{"filesystem", strings.Replace(original, "xfs", "ext4", 1), directory, true},
		{"read only", strings.Replace(original, "rw -", "ro -", 1), directory, true},
		{"root", strings.Replace(original, "/ /srv", "/sub /srv", 1), directory, true},
		{"path", strings.Replace(original, "/srv/archive", "/srv/other", 1), directory, true},
		{"symlink", original, symlink, true},
		{"missing directory", original, nil, true},
		{"stacked", original + strings.Replace(original, "42 1", "43 1", 1), directory, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := revalidateFileBrowserMounts([]fileBrowserSource{source}, func() ([]byte, error) { return []byte(tc.line), nil }, func(path string) (os.FileInfo, error) {
				if path != source.Host {
					t.Fatal(path)
				}
				if tc.info == nil {
					return nil, os.ErrNotExist
				}
				return tc.info, nil
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("revalidation: %v", err)
			}
		})
	}
}

func TestFileBrowserNamesAndCoverage(t *testing.T) {
	sources := []fileBrowserSource{{Name: "family (SMB)"}, {Name: "family (SMB)"}, {Name: "family (SMB) (2)"}, {Name: "family (SMB)"}}
	nameFileBrowserSources(sources)
	names := []string{}
	for _, source := range sources {
		names = append(names, source.Name)
	}
	if !reflect.DeepEqual(names, []string{"family (SMB)", "family (SMB) (2)", "family (SMB) (2) (2)", "family (SMB) (3)"}) {
		t.Fatal(names)
	}
	source := fileBrowserSource{Host: "/some/place/vdc1", Mount: &fileBrowserMount{}}
	for _, tc := range []struct {
		path    string
		covered bool
	}{{source.Host, true}, {source.Host + "/minecraft/data", true}, {source.Host + "/backups", true}, {source.Host + "-other/backups", false}, {"/var/lib/justvoxel/minecraft", false}, {"/var/lib/justvoxel/backups", false}} {
		if got := fileBrowserPathCovered(tc.path, []fileBrowserSource{source}); got != tc.covered {
			t.Fatalf("coverage %s = %v", tc.path, got)
		}
	}
}

func TestFileBrowserMountArgumentsPreserveLiteralPaths(t *testing.T) {
	source := fileBrowserSource{Host: "/srv/a,ro=true\" quoted", Path: "/data/filesystems/abc", NonRecursive: true}
	args := fileBrowserContainerArgs([]fileBrowserSource{source})
	fields, err := csv.NewReader(strings.NewReader(args[len(args)-2])).Read()
	if err != nil || !reflect.DeepEqual(fields, []string{"type=bind", "source=" + source.Host, "target=" + source.Path, "bind-nonrecursive=true"}) {
		t.Fatalf("mount options injected: %v %v", fields, err)
	}
}

func TestFileBrowserLocalAuthorityExcludesSystemAndUnmountedDevices(t *testing.T) {
	storage := adminStorageDiscovery{Devices: []adminStorageDevice{
		{Name: "system", MajorMinor: "8:17", Filesystem: "xfs", Mountpoints: []string{"/arbitrary/system-disk"}, System: true},
		{Name: "stale", MajorMinor: "8:17", Filesystem: "xfs", Mountpoints: []string{"/arbitrary/unmounted"}},
		{Name: "wrong-type", MajorMinor: "8:17", Filesystem: "ext4", Mountpoints: []string{"/arbitrary/wrong-type"}},
	}}
	sources, err := mountedFileBrowserSources(storage, []fileBrowserMount{
		{MajorMinor: "8:17", Root: "/", Mountpoint: "/arbitrary/system-disk", Filesystem: "xfs"},
		{MajorMinor: "8:17", Root: "/", Mountpoint: "/arbitrary/wrong-type", Filesystem: "tmpfs"},
	})
	if err != nil || len(sources) != 0 {
		t.Fatalf("exposed hidden or stale devices: %#v %v", sources, err)
	}
}
