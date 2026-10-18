package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestRecommendedPlacementFiltersRanksAndSeparatesFilesystems(t *testing.T) {
	const gib = uint64(1024 * 1024 * 1024)
	devices := []adminStorageDevice{}
	add := func(path, mount, fs string, ro bool) {
		devices = append(devices, adminStorageDevice{Type: "part", Path: path, Filesystem: fs, UUID: "fixture-uuid", Mountpoints: []string{mount}, ReadOnly: ro, SizeBytes: 10000 * gib})
	}
	add("/dev/a", "/srv/a", "xfs", false)
	add("/dev/b", "/srv/b", "ext4", false)
	add("/dev/c", "/srv/c", "btrfs", false)
	for _, unsafe := range []string{"/boot", "/etc/storage", "/proc", "/sys", "/run/storage", "/dev/shm", "/var/lib/containers/storage", "/srv/../unsafe", "relative"} {
		add("/dev/unsafe", unsafe, "xfs", false)
	}
	add("/dev/readonly", "/srv/readonly", "xfs", true)
	add("/dev/mount-ro", "/srv/mount-ro", "xfs", false)
	add("/dev/pseudo", "/srv/pseudo", "tmpfs", false)
	add("/dev/unmounted", "", "ext4", false)
	add("/dev/low", "/srv/low", "ext4", false)
	available := map[string]placementFilesystem{
		"/dev/a":   {Available: 30 * gib, Size: 100 * gib, Identity: "a"},
		"/dev/b":   {Available: 80 * gib, Size: 100 * gib, Identity: "b"},
		"/dev/c":   {Available: 80 * gib, Size: 100 * gib, Identity: "b"},
		"/dev/low": {Available: 2 * gib, Size: 100 * gib, Identity: "low"},
	}
	probed := []string{}
	inventory := adminStorageDiscovery{Devices: devices}
	candidates := recommendedPlacementCandidates(inventory, placementFilesystem{Available: 20 * gib, Size: 100 * gib, Identity: "system"}, func(d adminStorageDevice, _ string) (placementFilesystem, error) {
		probed = append(probed, d.Path)
		if d.Path == "/dev/mount-ro" {
			return placementFilesystem{}, errors.New("read-only mount")
		}
		return available[d.Path], nil
	})
	for _, p := range probed {
		if p == "/dev/unsafe" || p == "/dev/readonly" || p == "/dev/pseudo" || p == "/dev/unmounted" {
			t.Fatal("ineligible mount was probed", p)
		}
	}
	placement, err := chooseRecommendedPlacement(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if placement.Storage.Device != "/dev/b" || placement.Backups.Device != "/dev/a" {
		t.Fatal("available-space rank, tie, or filesystem separation wrong", placement)
	}
	// Inventory order and total block-device capacity cannot change this choice.
	for left, right := 0, len(devices)-1; left < right; left, right = left+1, right-1 {
		devices[left], devices[right] = devices[right], devices[left]
	}
	reordered := recommendedPlacementCandidates(inventory, placementFilesystem{Available: 20 * gib, Size: 100 * gib, Identity: "system"}, func(d adminStorageDevice, _ string) (placementFilesystem, error) { return available[d.Path], nil })
	again, err := chooseRecommendedPlacement(reordered)
	if err != nil || !reflect.DeepEqual(placement, again) {
		t.Fatal("nondeterministic placement", again, err)
	}
}

func TestRecommendedSingleFilesystemFallbackAndReserve(t *testing.T) {
	fs := placementFilesystem{Available: 12 << 30, Size: 100 << 30, Identity: "system"}
	candidates := recommendedPlacementCandidates(adminStorageDiscovery{}, fs, func(adminStorageDevice, string) (placementFilesystem, error) {
		t.Fatal("unexpected probe")
		return placementFilesystem{}, nil
	})
	out, err := chooseRecommendedPlacement(candidates)
	if err != nil || out.Storage.Type != "system" || out.Backups.Type != "system" {
		t.Fatal("system fallback unavailable", err)
	}
	if _, err = chooseRecommendedPlacement(nil); err == nil {
		t.Fatal("unsafe fallback bypassed reserve")
	}
	if recommendedCapacityOK(placementFilesystem{Available: 100 << 30, Size: 2000 << 30, Identity: "large"}) {
		t.Fatal("proportional conservative reserve bypassed")
	}
	only := recommendedPlacementCandidate{MountPoint: "/srv/data", Device: "/dev/a", placementFilesystem: fs}
	out, err = chooseRecommendedPlacement([]recommendedPlacementCandidate{only})
	if err != nil || out.Storage.Device != out.Backups.Device || out.Storage.Path == out.Backups.Path {
		t.Fatal("single filesystem lost isolated data/backup directories", out, err)
	}
}
