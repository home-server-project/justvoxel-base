package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

const recommendedStorageReserve = uint64(4 * 1024 * 1024 * 1024)

type placementFilesystem struct {
	Available uint64 `json:"available"`
	Size      uint64 `json:"size"`
	Identity  string `json:"identity"`
}
type recommendedPlacementCandidate struct {
	Device     string
	MountPoint string
	UUID       string
	System     bool
	placementFilesystem
}
type recommendedSetupPlacement struct {
	Storage adminSetupPlanStorageRequest `json:"storage"`
	Backups adminSetupPlanBackupsRequest `json:"backups"`
}

func safeRecommendedMount(path string) bool {
	if validateInstanceRuntimePath(path) != nil {
		return false
	}
	for _, unsafe := range []string{"/boot", "/etc", "/usr", "/proc", "/sys", "/run", "/dev", "/sysroot", "/root", "/var/lib", "/var/log", "/var/cache", "/var/spool", "/var/tmp", "/var/run", "/var/lock", "/tmp"} {
		if instanceRuntimeInside(path, unsafe) {
			return false
		}
	}
	return path != "/var" && path != "/var/lib"
}
func recommendedCapacityOK(fs placementFilesystem) bool {
	reserve := fs.Size / 10
	if reserve < recommendedStorageReserve {
		reserve = recommendedStorageReserve
	}
	return fs.Identity != "" && fs.Size > 0 && fs.Available <= fs.Size && fs.Available > reserve
}

// Discovery lists devices; the host-namespace probe verifies exact live mounts,
// filesystem writability and available bytes. It never prepares a filesystem.
func recommendedPlacementCandidates(inventory adminStorageDiscovery, system placementFilesystem, probe func(adminStorageDevice, string) (placementFilesystem, error)) []recommendedPlacementCandidate {
	candidates := []recommendedPlacementCandidate{}
	if recommendedCapacityOK(system) {
		candidates = append(candidates, recommendedPlacementCandidate{System: true, placementFilesystem: system})
	}
	for _, d := range inventory.Devices {
		if d.ReadOnly || d.Type != "part" || !instanceRuntimeUUIDPattern.MatchString(d.UUID) || !strings.HasPrefix(d.Path, "/dev/") {
			continue
		}
		if d.Filesystem != "xfs" && d.Filesystem != "ext4" && d.Filesystem != "btrfs" {
			continue
		}
		if len(d.Mountpoints) != 1 || !safeRecommendedMount(d.Mountpoints[0]) {
			continue
		}
		fs, err := probe(d, d.Mountpoints[0])
		if err != nil || !recommendedCapacityOK(fs) {
			continue
		}
		candidates = append(candidates, recommendedPlacementCandidate{Device: d.Path, MountPoint: d.Mountpoints[0], UUID: d.UUID, placementFilesystem: fs})
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Available != b.Available {
			return a.Available > b.Available
		}
		if a.System != b.System {
			return a.System
		}
		if a.MountPoint != b.MountPoint {
			return a.MountPoint < b.MountPoint
		}
		return a.Device < b.Device
	})
	return candidates
}
func chooseRecommendedPlacement(candidates []recommendedPlacementCandidate) (recommendedSetupPlacement, error) {
	if len(candidates) == 0 {
		return recommendedSetupPlacement{}, errors.New("No mounted writable filesystem has enough available space for recommended setup")
	}
	data, backup := candidates[0], candidates[0]
	for _, candidate := range candidates {
		if candidate.Identity != data.Identity {
			backup = candidate
			break
		}
	}
	out := recommendedSetupPlacement{}
	out.Storage = adminSetupPlanStorageRequest{Type: "system", Path: "/var/lib/justvoxel/minecraft"}
	if !data.System {
		out.Storage = adminSetupPlanStorageRequest{Type: "partition", Device: data.Device, MountPoint: data.MountPoint, Path: filepath.Join(data.MountPoint, "minecraft")}
	}
	out.Backups = adminSetupPlanBackupsRequest{Type: "system", Path: "/var/lib/justvoxel/backups"}
	if !backup.System {
		out.Backups = adminSetupPlanBackupsRequest{Type: "partition", Device: backup.Device, MountPoint: backup.MountPoint, Path: filepath.Join(backup.MountPoint, "backups")}
	}
	return out, nil
}
func probePlacementFilesystem(ctx context.Context, path, uuid, source string) (placementFilesystem, error) {
	var fs placementFilesystem
	raw, err := instanceRuntimeCommand(ctx, "nsenter", "--mount=/proc/1/ns/mnt", "--", instanceRuntimeHelper, "placement-candidate", path, uuid, source)
	if err != nil {
		return fs, err
	}
	if err = instanceDecode(raw, &fs); err != nil {
		return fs, err
	}
	return fs, nil
}
func validateRecommendedPlan(ctx context.Context, plan *adminSetupNormalizedPlan) error {
	for _, target := range []struct{ kind, path, mount, uuid, source string }{
		{plan.Storage.Type, plan.Storage.Path, plan.Storage.MountPoint, plan.Storage.ExpectedUUID, plan.Storage.ExpectedSource},
		{plan.Backups.Type, plan.Backups.Path, plan.Backups.MountPoint, plan.Backups.ExpectedUUID, plan.Backups.ExpectedSource},
	} {
		path := "/var/lib"
		if target.kind != "system" {
			if target.kind != "partition" || !safeRecommendedMount(target.mount) {
				return errInstanceRecovery
			}
			path = target.mount
		}
		fs, err := probePlacementFilesystem(ctx, path, target.uuid, target.source)
		if err != nil || !recommendedCapacityOK(fs) {
			return errInstanceRecovery
		}
	}
	return nil
}
func (s *server) adminRecommendedSetupStorage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), adminSetupPlanTimeout)
	defer cancel()
	raw, err := runAdminDiscoveryHelper(ctx, "storage")
	var inventory adminStorageDiscovery
	if err != nil || instanceDecode(raw, &inventory) != nil {
		writeError(w, 503, "Storage discovery is unavailable")
		return
	}
	system, _ := probePlacementFilesystem(ctx, "/var/lib", "", "")
	candidates := recommendedPlacementCandidates(inventory, system, func(d adminStorageDevice, point string) (placementFilesystem, error) {
		return probePlacementFilesystem(ctx, point, d.UUID, d.Path)
	})
	placement, err := chooseRecommendedPlacement(candidates)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, placement)
}
