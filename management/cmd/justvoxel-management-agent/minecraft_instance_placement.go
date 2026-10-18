package main

import (
	"errors"
	"path/filepath"
	"strings"
)

// Absent on legacy records: those retain the validated appliance backup contract.
// Path includes the immutable ID; Root is never an archive namespace by name.
type instanceRuntimeBackupStorage struct {
	Type       string `json:"type"`
	Root       string `json:"root"`
	Path       string `json:"path"`
	MountPoint string `json:"mount_point"`
	UUID       string `json:"uuid"`
	Source     string `json:"source"`
}

func instanceBackupsFor(ctx minecraftInstanceRuntimeContext, id string) *instanceRuntimeBackupStorage {
	if ctx.BackupPlacement == nil {
		return nil
	}
	out := *ctx.BackupPlacement
	out.Path = filepath.Join(out.Root, id)
	return &out
}

func validateInstanceRecordPlacement(r instanceRuntimeRecord) error {
	id, st := r.Runtime.ID, r.Storage
	if !instanceIDPattern.MatchString(id) || st.DataPath != filepath.Join(st.Root, "minecraft-instances", id, "data") {
		return errInstanceRecovery
	}
	ctx := minecraftInstanceRuntimeContext{StorageRoot: st.Root, MountPoint: st.MountPoint, ExpectedUUID: st.UUID, ExpectedSource: st.Source, BackupPath: "/var/lib/justvoxel/backups"}
	if r.Backups != nil {
		b := r.Backups
		if b.Path != filepath.Join(b.Root, id) {
			return errInstanceRecovery
		}
		for _, p := range []string{b.Root, b.Path} {
			if validateInstanceRuntimePath(p) != nil {
				return errInstanceRecovery
			}
		}
		for _, unsafe := range []string{"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/run"} {
			if instanceRuntimeInside(b.Root, unsafe) {
				return errInstanceRecovery
			}
		}
		switch b.Type {
		case "system", "partition", "nfs", "smb":
		default:
			return errInstanceRecovery
		}
		if b.MountPoint == "" {
			if b.Type != "system" || b.UUID != "" || b.Source != "" {
				return errInstanceRecovery
			}
		} else {
			if validateInstanceRuntimePath(b.MountPoint) != nil || !instanceRuntimeInside(b.Root, b.MountPoint) || (b.UUID == "" && b.Source == "") {
				return errInstanceRecovery
			}
			if b.UUID != "" && !instanceRuntimeUUIDPattern.MatchString(b.UUID) {
				return errInstanceRecovery
			}
			if strings.ContainsAny(b.Source, "\r\n\t ") {
				return errInstanceRecovery
			}
			switch b.Type {
			case "partition":
				if b.UUID == "" || (b.Source != "" && (!instanceRuntimePathPattern.MatchString(b.Source) || filepath.Clean(b.Source) != b.Source)) {
					return errInstanceRecovery
				}
			case "nfs":
				if !strings.Contains(b.Source, ":/") {
					return errInstanceRecovery
				}
			case "smb":
				if !strings.HasPrefix(b.Source, "//") || strings.Count(b.Source, "/") < 3 {
					return errInstanceRecovery
				}
			default:
				return errInstanceRecovery
			}
		}
		if instanceRuntimeInside(b.Root, st.DataPath) || instanceRuntimeInside(st.DataPath, b.Root) {
			return errors.New("instance data overlaps backup storage")
		}
		ctx.BackupPath = b.Root
	}
	return validateInstanceStorageContext(ctx)
}
