package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// New setups retain the storage transaction and its recovery contract. This
// evidence owns only appliance policy files and a normal generated instance.
// Old setup evidence continues through the original runtime recovery helper.
type setupInstanceEvidence struct {
	Schema           int                            `json:"schema"`
	Request          setupRuntimeTransactionRequest `json:"request"`
	Record           instanceRuntimeRecord          `json:"record"`
	Policy           map[string][]byte              `json:"policy"`
	DataIdentity     string                         `json:"data_identity,omitempty"`
	HadInstancesRoot bool                           `json:"had_instances_root"`
	RolledBack       bool                           `json:"rolled_back"`
}

var runSetupInstanceRuntimeAction = runSetupInstanceAction

var setupPolicyRoot = "/etc/justvoxel"
var setupInstanceMarker = "/var/lib/justvoxel/management/setup-in-progress"

func setupInstancePath(id string) string {
	return filepath.Join(instanceRuntimes.staging, "setup-"+id+".json")
}
func setupInstanceRead(id, fingerprint string) (setupInstanceEvidence, error) {
	var e setupInstanceEvidence
	if !validOperationID(id) || !operationFingerprintPattern.MatchString(fingerprint) {
		return e, errInstanceRecovery
	}
	raw, err := instanceRuntimes.readPrivate(setupInstancePath(id))
	if err != nil {
		return e, err
	}
	if instanceDecode(raw, &e) != nil || e.Schema != 1 || !e.Request.InstanceOnly || e.Request.OperationID != id || e.Request.PlanFingerprint != fingerprint {
		return e, errInstanceRecovery
	}
	if e.Record.Runtime.ID != "" && (!instanceIDPattern.MatchString(e.Record.Runtime.ID) || e.Record.Storage.DataPath != filepath.Join(e.Record.Storage.Root, "minecraft-instances", e.Record.Runtime.ID, "data")) {
		return e, errInstanceRecovery
	}
	expected := setupInstancePolicy(e.Request)
	if !reflect.DeepEqual(e.Policy, expected) {
		return e, errInstanceRecovery
	}
	return e, nil
}
func setupInstancePolicy(r setupRuntimeTransactionRequest) map[string][]byte {
	// Shell policy contains appliance storage only. No Minecraft server identity,
	// environment, Quadlet or default service is created here.
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
	policy := "# Appliance storage policy; Minecraft servers live in instances/.\nAPPLIANCE_CONFIGURED=yes\n"
	for _, item := range [][2]string{{"DATA_PATH", r.Storage.Path}, {"DATA_MOUNT_POINT", r.Storage.MountPoint}, {"DATA_EXPECTED_UUID", r.Storage.ExpectedUUID}, {"DATA_EXPECTED_SOURCE", r.Storage.ExpectedSource}, {"BACKUP_TYPE", r.Backups.Type}, {"BACKUP_PATH", r.Backups.Path}, {"BACKUP_MOUNT_POINT", r.Backups.MountPoint}, {"BACKUP_EXPECTED_UUID", r.Backups.ExpectedUUID}, {"BACKUP_EXPECTED_SOURCE", r.Backups.ExpectedSource}} {
		policy += item[0] + "=" + quote(item[1]) + "\n"
	}
	backup := "# Appliance backup destination; instance workers supply server identity.\n"
	for _, item := range [][2]string{{"MINECRAFT_DATA_PATH", r.Storage.Path}, {"MINECRAFT_BACKUP_PATH", r.Backups.Path}, {"BACKUP_TYPE", r.Backups.Type}, {"BACKUP_KEEP", strconv.Itoa(r.Backups.Keep)}, {"BACKUP_MOUNT_POINT", r.Backups.MountPoint}, {"BACKUP_EXPECTED_UUID", r.Backups.ExpectedUUID}, {"BACKUP_EXPECTED_SOURCE", r.Backups.ExpectedSource}} {
		backup += item[0] + "=" + quote(item[1]) + "\n"
	}
	return map[string][]byte{"justvoxel.conf": []byte(policy), "minecraft-backup.env": []byte(backup)}
}
func runSetupInstanceAction(ctx context.Context, action string, r setupRuntimeTransactionRequest) (setupRuntimeTransactionResponse, error) {
	b := instanceRuntimes
	response := setupRuntimeTransactionResponse{Phase: "runtime_" + action}
	if !validOperationID(r.OperationID) || !operationFingerprintPattern.MatchString(r.PlanFingerprint) {
		return response, errInstanceRecovery
	}
	e, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
	if action == "validate" && errors.Is(err, os.ErrNotExist) {
		for name := range setupInstancePolicy(r) {
			if _, err = b.readPrivate(filepath.Join(setupPolicyRoot, name)); !errors.Is(err, os.ErrNotExist) {
				return response, errInstanceRecovery
			}
		}
		dir, createErr := b.privateDirectory(b.staging, true)
		if createErr != nil {
			return response, createErr
		}
		dir.Close()
		e = setupInstanceEvidence{Schema: 1, Request: r, Policy: setupInstancePolicy(r)}
		if info, statErr := os.Lstat(filepath.Join(filepath.Dir(r.Storage.Path), "minecraft-instances")); statErr == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return response, errInstanceRecovery
			}
			e.HadInstancesRoot = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return response, statErr
		}
		err = b.writeJSON(setupInstancePath(r.OperationID), e, false)
	}
	if action == "rollback" && errors.Is(err, os.ErrNotExist) {
		return setupRuntimeTransactionResponse{OK: true, Phase: "runtime_rolled_back"}, nil
	}
	if err != nil {
		return response, err
	}
	if !reflect.DeepEqual(e.Request, r) {
		return response, errInstanceRecovery
	}
	save := func() error { return b.writeJSON(setupInstancePath(r.OperationID), e, true) }
	switch action {
	case "validate":
		response.OK = true
	case "apply":
		if e.RolledBack || e.Record.Runtime.ID != "" {
			return response, errInstanceRecovery
		}
		if err = b.writePrivate(setupInstanceMarker, []byte(r.OperationID+" "+r.PlanFingerprint+"\n"), false); err != nil {
			return response, err
		}
		for name, data := range e.Policy {
			if err = b.writePrivate(filepath.Join(setupPolicyRoot, name), data, false); err != nil {
				return response, err
			}
		}
		snapshot, uid, gid, err := b.snapshot(ctx)
		if err != nil {
			return response, err
		}
		if snapshot.StorageRoot != filepath.Dir(r.Storage.Path) || snapshot.MountPoint != r.Storage.MountPoint || snapshot.ExpectedUUID != r.Storage.ExpectedUUID || snapshot.ExpectedSource != r.Storage.ExpectedSource || snapshot.BackupPath != r.Backups.Path || uid != r.Minecraft.MinecraftUID || gid != r.Minecraft.MinecraftGID {
			return response, errInstanceRecovery
		}
		var secret [24]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return response, err
		}
		port := 0
		if r.Server.BedrockEnabled {
			port = r.Minecraft.BedrockPort
		}
		c := minecraftInstanceRuntimeConfig{JavaPort: r.Minecraft.JavaPort, BedrockEnabled: r.Server.BedrockEnabled, BedrockPort: port, JavaMemory: r.Minecraft.JavaMemory, ContainerMemory: r.Minecraft.ContainerMemory, Software: r.Minecraft.ServerType, Version: r.Minecraft.Version, VersionPolicy: r.Minecraft.VersionPolicy, PaperChannel: "default", ImageTag: r.Minecraft.ImageTag, StartOnBoot: true, EULAAccepted: true, UID: uid, GID: gid, Timezone: r.Server.Timezone, GameMode: r.Minecraft.GameMode, Difficulty: "normal", MaxPlayers: r.Server.MaxPlayers, Whitelist: true, EnforceWhitelist: true, MOTD: r.Server.MOTD, RCONPassword: hex.EncodeToString(secret[:])}
		channel, err := b.command(ctx, instanceRuntimeHelper, "channel", c.Software, c.Version, c.VersionPolicy)
		if err != nil {
			return response, instanceRuntimeFailure("Selected release channel could not be verified.")
		}
		c.PaperChannel = strings.TrimSpace(string(channel))
		_, err = b.createTracked(ctx, c, "", snapshot, func(record instanceRuntimeRecord) error { e.Record = record; return save() })
		if err != nil {
			return response, err
		}
		response.OK = true
		response.Applied = true
	case "verify":
		id := e.Record.Runtime.ID
		if id == "" {
			return response, errInstanceRecovery
		}
		record, err := b.readRecord(id)
		if err != nil {
			return response, err
		}
		if err = b.verifyLive(record); err != nil {
			return response, err
		}
		if err = b.actionWithWarnings(ctx, id, "start", true); err != nil {
			return response, err
		}
		if _, err = b.command(ctx, "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", "--instance", id, r.Minecraft.Version); err != nil {
			return response, instanceRuntimeFailure("The selected Minecraft instance did not pass readiness verification.")
		}
		response.OK = true
		response.Applied = true
	case "commit":
		id := e.Record.Runtime.ID
		state, err := b.observe(ctx, id)
		if err != nil || state.State != "running" {
			return response, errInstanceRecovery
		}
		daily, err := legacyBackupDaily("[Timer]\nOnCalendar=" + r.Backups.Schedule + "\n")
		if err != nil {
			return response, err
		}
		if err = b.configureBackups(ctx, id, instanceBackupSettings{Enabled: r.Backups.Automatic, Time: daily, Retention: r.Backups.Keep}); err != nil {
			return response, err
		}
		if err = clearSetupInstanceMarker(e); err != nil {
			return response, err
		}
		response.OK = true
		response.Applied = true
	case "rollback":
		if err = rollbackSetupInstance(ctx, &e, save); err != nil {
			return response, err
		}
		response.OK = true
		response.Phase = "runtime_rolled_back"
	default:
		return response, fmt.Errorf("invalid setup instance action")
	}
	return response, nil
}
func rollbackSetupInstance(ctx context.Context, e *setupInstanceEvidence, save func() error) error {
	b := instanceRuntimes
	if e.RolledBack {
		return nil
	}
	id := e.Record.Runtime.ID
	if id != "" {
		// Only the generated ID recorded before registration can be recovered here.
		state, err := b.observeRaw(ctx, id)
		if err != nil {
			return err
		}
		if state.State == "running" {
			if err = b.actionWithWarnings(ctx, id, "stop", true); err != nil {
				return err
			}
		}
		if pending, err := b.pending(id); err != nil {
			return err
		} else if pending {
			if err = b.recover(ctx, id); err != nil {
				return err
			}
		}
		record, err := b.readRecord(id)
		if journal, journalErr := b.deleteJournal(id); journalErr == nil {
			if err = b.deleteInstance(ctx, id, journal.Name); err != nil {
				return err
			}
		} else if !errors.Is(journalErr, os.ErrNotExist) {
			return journalErr
		} else if err == nil && record.EnvironmentSHA256 != "" {
			if _, pendingErr := b.readPrivate(b.backupPath(id, "schedule-pending")); pendingErr == nil {
				if err = b.configureBackups(ctx, id, instanceBackupSettings{Enabled: false, Time: "04:00", Retention: 7}); err != nil {
					return err
				}
			} else if !errors.Is(pendingErr, os.ErrNotExist) {
				return pendingErr
			}
			names, err := b.identities().readNames()
			if err != nil {
				return err
			}
			if err = b.deleteInstance(ctx, id, names[id].DisplayName); err != nil {
				return err
			}
		} else {
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInstanceNotFound) {
				return err
			}
			// A failed initial activation leaves no committed runtime. Never adopt or
			// erase any unexpected environment/Quadlet; normal activation recovery must
			// first prove its own rollback.
			dir, env, q := b.paths(id)
			for _, path := range []string{env, q} {
				if _, err = b.readPrivate(path); !errors.Is(err, os.ErrNotExist) {
					return errInstanceRecovery
				}
			}
			if record.Runtime.ID != "" && !reflect.DeepEqual(record, e.Record) {
				return errInstanceRecovery
			}
			if _, parentErr := os.Lstat(filepath.Dir(e.Record.Storage.DataPath)); parentErr == nil {
				// Complete only our existing bounded provisioning contract; its
				// private marker is required to repair an interrupted empty mkdir.
				if err = b.storage(ctx, "provision", e.Record); err != nil {
					return err
				}
			} else if !errors.Is(parentErr, os.ErrNotExist) {
				return parentErr
			}
			if _, err = os.Lstat(e.Record.Storage.DataPath); err == nil {
				j := instanceDeleteJournal{ID: id, Record: e.Record}
				if e.DataIdentity == "" {
					e.DataIdentity, err = b.deleteStorage(ctx, "inspect", j)
					if err != nil {
						return err
					}
					if err = save(); err != nil {
						return err
					}
				}
				j.DataIdentity = e.DataIdentity
				if _, err = b.deleteStorage(ctx, "remove", j); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err = b.removePrivate(filepath.Join(dir, "config.json")); err != nil {
				return err
			}
			if err = os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err = b.removePrivate(filepath.Join(b.identities().directory, id+".json")); err != nil {
				return err
			}
		}
		// Empty instance parents contain no world bytes. Remove only this generated
		// ID's parent; leave sibling instances and appliance storage intact.
		if err := os.Remove(filepath.Dir(e.Record.Storage.DataPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if id != "" && !e.HadInstancesRoot {
		root := filepath.Join(e.Record.Storage.Root, "minecraft-instances")
		if err := os.Remove(root); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for name, expected := range e.Policy {
		path := filepath.Join(setupPolicyRoot, name)
		raw, err := b.readPrivate(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if instanceHash(raw) != instanceHash(expected) {
			return errInstanceRecovery
		}
		if err = b.removePrivate(path); err != nil {
			return err
		}
	}
	if err := clearSetupInstanceMarker(*e); err != nil {
		return err
	}
	e.RolledBack = true
	return save()
}

// A restart loads protected instance evidence, never reconstructs a target from
// appliance settings or a friendly name. Absence alone selects old recovery.
func recoverSetupInstance(ctx context.Context, current operationJournal) (setupRuntimeTransactionResponse, bool, error) {
	e, err := setupInstanceRead(current.OperationID, current.PlanFingerprint)
	if errors.Is(err, os.ErrNotExist) {
		if current.InstanceSetup {
			return setupRuntimeTransactionResponse{OK: true, Phase: "runtime_rolled_back"}, true, nil
		}
		return setupRuntimeTransactionResponse{}, false, nil
	}
	if err != nil {
		return setupRuntimeTransactionResponse{}, true, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	response, err := runSetupInstanceAction(ctx, "rollback", e.Request)
	return response, true, err
}

func clearSetupInstanceMarker(e setupInstanceEvidence) error {
	raw, err := instanceRuntimes.readPrivate(setupInstanceMarker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(raw) != e.Request.OperationID+" "+e.Request.PlanFingerprint+"\n" {
		return errInstanceRecovery
	}
	return instanceRuntimes.removePrivate(setupInstanceMarker)
}
