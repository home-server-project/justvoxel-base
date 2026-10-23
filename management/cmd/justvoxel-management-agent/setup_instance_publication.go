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
	"strings"
	"syscall"
	"time"
)

const setupRuntimeCommitted = "runtime_committed"

var setupInstancePermitRoot = "/run/justvoxel/setup-instance-start"

// Only schema 2 has provisional semantics. Record is the immutable preparation
// intent, then the exact committed runtime, always persisted before mutation.
type setupInstancePublication struct {
	StartAttempted bool                   `json:"start_attempted,omitempty"`
	StartError     string                 `json:"start_error,omitempty"`
	Stage          string                 `json:"stage"`
	Name           string                 `json:"name"`
	BackupSettings instanceBackupSettings `json:"backup_settings"`
	BackupUnits    map[string][]byte      `json:"backup_units"`
}

// Publish precisely the identity from protected Setup evidence. The registry
// lock covers validation and both publications; immutable identities are never
// replaced. Matching retained metadata completes an interrupted publication.
func (s *minecraftInstanceStore) publishSetupInstance(id, name, software string) error {
	validated, err := validateInstanceName(name)
	if err != nil || validated == "" || validated != name || !instanceIDPattern.MatchString(id) || !validSetupServerType(software) {
		return errInstanceRecovery
	}
	dir, err := s.openDirectory(true)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	instances, names, err := s.registeredInstances("")
	if err != nil {
		return err
	}
	expected := minecraftInstanceNameRecord{ID: id, DisplayName: name, Software: software}
	old, hasName := names[id]
	if hasName && old != expected {
		return errInstanceRecovery
	}
	found := false
	for _, instance := range instances {
		if instance.ID == id {
			found = true
		}
		if instance.ID != id && strings.EqualFold(instance.DisplayName, name) {
			return errInstanceNameDuplicate
		}
	}
	if found {
		existing, readErr := s.readRecord(id + ".json")
		if !hasName || readErr != nil || existing != id {
			return errInstanceRecovery
		}
		namesDir, err := s.openNames(false)
		if err != nil {
			return err
		}
		err = namesDir.Sync()
		namesDir.Close()
		if err != nil {
			return err
		}
		return dir.Sync()
	}
	namesDir, err := s.openNames(true)
	if err != nil {
		return err
	}
	defer namesDir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	if !hasName {
		if err = s.publishRegistryRecord(namesDir, id+".json", ".name-*", expected, false); err != nil {
			return err
		}
	}
	return s.publishRegistryRecord(dir, id+".json", ".instance-*", minecraftInstanceRecord{ID: id}, false)
}

// Called before the provisional runtime exists. No normal registry publication.
func (b *instanceRuntimeBackend) prepareSetupInstance(ctx context.Context, c minecraftInstanceRuntimeConfig, snapshot minecraftInstanceRuntimeContext, e *setupInstanceEvidence, save func() error) error {
	if err := b.requireNoStartOver(); err != nil {
		return err
	}
	registry, err := b.identities().openDirectory(true)
	if err != nil {
		return err
	}
	registry.Close()
	records, err := b.records()
	if err != nil {
		return err
	}
	for _, r := range records {
		pending, err := b.pending(r.Runtime.ID)
		if err != nil || pending {
			return errInstanceRecovery
		}
	}
	var entropy [6]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return err
	}
	c.ID = "jv-" + hex.EncodeToString(entropy[:])
	names, err := b.identities().readNames()
	if err != nil {
		return err
	}
	if _, exists := names[c.ID]; exists {
		return errInstanceDuplicate
	}
	// Retired IDs, partial runtime, Delete evidence and existing data are collisions.
	for _, path := range []string{filepath.Join(b.identities().directory, c.ID+".json"), filepath.Join(b.root, c.ID), b.deletePath(c.ID)} {
		if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errInstanceRecovery
		}
	}
	name, err := validateInstanceName(e.Request.Server.DisplayName)
	if err != nil {
		return err
	}
	instances, err := b.identities().listInstances("")
	if err != nil {
		return err
	}
	if name == "" {
		name = generatedInstanceName(instances, c.ID, c.Software)
	}
	if instanceNameUsed(instances, c.ID, name) {
		return errInstanceNameDuplicate
	}
	next := instanceRuntimeRecord{SchemaVersion: 1, Runtime: c, Storage: instanceStorageFor(snapshot, c.ID), Backups: instanceBackupsFor(snapshot, c.ID), Firewall: []instanceFirewallRule{}}
	candidates := instanceReplaceRecord(records, next)
	if c.StartOnBoot {
		if err = b.admitMemory(ctx, snapshot.TotalMemoryMiB, candidates, c, true); err != nil {
			return err
		}
	}
	files, err := b.renderCandidate(snapshot, candidates, c.ID)
	if err != nil {
		return err
	}
	var selected minecraftInstanceRuntimeFiles
	for _, f := range files {
		if f.ID == c.ID {
			selected = f
		}
	}
	if selected.ID == "" {
		return errInstanceRecovery
	}
	if err = b.storage(ctx, "preflight-new", next); err != nil {
		return err
	}
	if err = b.validateSoftware(ctx, c); err != nil {
		return err
	}
	held, err := instanceHoldPort(c.JavaPort)
	if err != nil {
		return err
	}
	defer func() {
		for _, l := range held {
			l.Close()
		}
	}()
	if c.BedrockEnabled {
		udp, err := instanceHoldCrossplayPort(c.BedrockPort)
		if err != nil {
			return err
		}
		defer udp.Close()
	}
	changes, owned, err := b.planFirewall(ctx, next, next)
	if err != nil {
		return err
	}
	next.Firewall = owned
	next.EnvironmentSHA256, next.QuadletSHA256 = instanceHash(selected.Environment), instanceHash(selected.Quadlet)
	e.Record = next
	e.Publication = &setupInstancePublication{Stage: "prepared", Name: name}
	// This is the ownership boundary, before any per-ID resource is created.
	if err = save(); err != nil {
		return err
	}
	root, err := b.privateDirectory(b.root, true)
	if err != nil {
		return err
	}
	root.Close()
	dir, env, quadlet := b.paths(c.ID)
	d, err := b.privateDirectory(dir, true)
	if err != nil {
		return err
	}
	d.Close()
	// Validate an inactive exact bundle before publishing into systemd's search path.
	bundle, err := os.MkdirTemp(b.staging, ".setup-runtime-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(bundle)
	if err = os.Chown(bundle, b.identities().uid, b.identities().gid); err != nil {
		return err
	}
	for path, data := range map[string][]byte{filepath.Join(bundle, c.ID+".env"): selected.Environment, filepath.Join(bundle, "minecraft-"+c.ID+".container"): selected.Quadlet} {
		if err = b.writePrivate(path, data, false); err != nil {
			return err
		}
	}
	if err = b.validateBundle(ctx, bundle, []minecraftInstanceRuntimeFiles{selected}); err != nil {
		return err
	}
	// Pin the root-private generated data parent before provisioning. This
	// survives interruption before the data inode can be recorded.
	for _, path := range []string{filepath.Dir(filepath.Dir(next.Storage.DataPath)), filepath.Dir(next.Storage.DataPath)} {
		d, err := b.privateDirectory(path, true)
		if err != nil {
			return err
		}
		d.Close()
	}
	e.DataParentIdentity, err = b.setupDataParentIdentity(next)
	if err != nil {
		return err
	}
	if err = save(); err != nil {
		return err
	}
	if err = b.storage(ctx, "provision", next); err != nil {
		return err
	}
	j := instanceDeleteJournal{ID: c.ID, Record: next}
	e.DataIdentity, err = b.setupInstanceData(ctx, "inspect", j)
	if err != nil {
		return err
	}
	if err = save(); err != nil {
		return err
	}
	if err = b.storage(ctx, "validate", next); err != nil {
		return err
	}
	for _, change := range changes {
		if err = b.changeFirewall(ctx, change); err != nil {
			return err
		}
	}
	if err = b.writeJSON(filepath.Join(dir, "config.json"), next, false); err != nil {
		return err
	}
	if err = b.writePrivate(env, selected.Environment, false); err != nil {
		return err
	}
	if err = b.writePrivate(quadlet, selected.Quadlet, false); err != nil {
		return err
	}
	if _, err = b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err = b.verifyLive(next); err != nil {
		return err
	}
	state, err := b.observeRaw(ctx, c.ID)
	if err != nil || state.State != "stopped" {
		return errInstanceRecovery
	}
	e.Publication.Stage = "runtime_prepared"
	return save()
}

func (b *instanceRuntimeBackend) setupConflicts(e setupInstanceEvidence) error {
	id := e.Record.Runtime.ID
	dir, _, _ := b.paths(id)
	for _, path := range []string{b.deletePath(id), filepath.Join(dir, "pending.json"), filepath.Join(dir, "factory-reset.json"), filepath.Join(dir, "restore-journal.json"), filepath.Join(dir, "data-migration.json")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errInstanceRecovery
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) exactSetupRuntime(ctx context.Context, e setupInstanceEvidence) error {
	if e.Publication == nil || e.Record.Runtime.ID == "" || e.RolledBack {
		return errInstanceRecovery
	}
	if err := b.setupConflicts(e); err != nil {
		return err
	}
	record, err := b.readRuntimeConfig(e.Record.Runtime.ID)
	if err != nil || !reflect.DeepEqual(record, e.Record) {
		return errInstanceRecovery
	}
	if err = b.verifyLive(record); err != nil {
		return err
	}
	if err = b.storage(ctx, "validate", record); err != nil {
		return err
	}
	parent, err := b.setupDataParentIdentity(record)
	if err != nil || e.DataParentIdentity == "" || parent != e.DataParentIdentity {
		return errInstanceRecovery
	}
	identity, err := b.setupInstanceData(ctx, "inspect", instanceDeleteJournal{ID: record.Runtime.ID, Record: record})
	if err != nil || e.DataIdentity == "" || identity != e.DataIdentity {
		return errInstanceRecovery
	}
	return nil
}
func (b *instanceRuntimeBackend) setupPublicationPresent(e setupInstanceEvidence) (bool, error) {
	id := e.Record.Runtime.ID
	if id == "" {
		return false, nil
	}
	// A name without identity is an interrupted publication only after all
	// preparation was durably completed, never an invitation to erase it.
	names, err := b.identities().readNames()
	if err != nil {
		return false, err
	}
	_, hasName := names[id]
	_, err = b.readPrivate(filepath.Join(b.identities().directory, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return hasName, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
func (b *instanceRuntimeBackend) finishSetupPublication(ctx context.Context, e *setupInstanceEvidence, save func() error) error {
	if e.Publication == nil || (e.Publication.Stage != "commit_ready" && e.Publication.Stage != "published" && e.Publication.Stage != "finalized") {
		return errInstanceRecovery
	}
	if err := b.exactSetupRuntime(ctx, *e); err != nil {
		return err
	}
	// Backup settings/units are part of the prepared commitment too.
	if err := b.verifySetupBackups(*e); err != nil {
		return err
	}
	if err := b.identities().publishSetupInstance(e.Record.Runtime.ID, e.Publication.Name, e.Record.Runtime.Software); err != nil {
		return err
	}
	e.Publication.Stage = "published"
	if err := save(); err != nil {
		return err
	}
	if err := b.removePrivate(setupPermitPath(e.Record.Runtime.ID)); err != nil {
		return err
	}
	if err := clearSetupInstanceMarker(*e); err != nil {
		return err
	}
	e.Publication.Stage = "finalized"
	if err := save(); err != nil {
		return err
	}
	// At most one normal start submission, bound to the schema-2 reviewed intent.
	// Persist before submission so recovery can never restart a server later
	// stopped by its owner. A start failure cannot undo publication.
	if e.Schema == 2 && e.Request.AddServer && e.Request.StartAfterSetup && !e.Publication.StartAttempted {
		e.Publication.StartAttempted = true
		if err := save(); err != nil {
			return err
		}
		if err := b.action(ctx, e.Record.Runtime.ID, "start"); err != nil {
			e.Publication.StartError = "Server created, but it could not be started. Use Start after checking Minecraft memory capacity and server status."
			return save()
		}
	}
	return nil
}

func setupPermitPath(id string) string {
	return filepath.Join(setupInstancePermitRoot, "setup-instance-start-"+id+".json")
}
func (b *instanceRuntimeBackend) permitSetupStart(e setupInstanceEvidence) error {
	if e.Schema != 2 || e.Publication == nil || e.Publication.Stage != "runtime_prepared" || e.RolledBack {
		return errInstanceRecovery
	}
	if err := b.setupConflicts(e); err != nil {
		return err
	}
	dir, err := b.privateDirectory(setupInstancePermitRoot, true)
	if err != nil {
		return err
	}
	dir.Close()
	proc, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(proc)[strings.LastIndex(string(proc), ")")+1:])
	if len(fields) < 20 {
		return errInstanceRecovery
	}
	raw, err := b.readPrivate(setupInstancePath(e.Request.OperationID))
	if err != nil {
		return err
	}
	return b.writeJSON(setupPermitPath(e.Record.Runtime.ID), map[string]any{"schema": 1, "id": e.Record.Runtime.ID, "operation_id": e.Request.OperationID, "plan_fingerprint": e.Request.PlanFingerprint, "evidence_sha256": instanceHash(raw), "agent_pid": os.Getpid(), "agent_start": fields[19]}, true)
}
func (b *instanceRuntimeBackend) verifyProvisionalSetup(ctx context.Context, e *setupInstanceEvidence, save func() error) (result error) {
	if e.Publication == nil || e.Publication.Stage != "runtime_prepared" {
		return errInstanceRecovery
	}
	// Install cleanup before permit publication: an fsync error can follow a
	// successful atomic write, and must not leave a usable live permit behind.
	defer func() { result = errors.Join(result, b.removePrivate(setupPermitPath(e.Record.Runtime.ID))) }()
	if err := b.exactSetupRuntime(ctx, *e); err != nil {
		return err
	}
	if err := b.permitSetupStart(*e); err != nil {
		return err
	}
	id := e.Record.Runtime.ID
	// Preserve Add Server's stopped preparation contract.
	if !e.Request.AddServer {
		held, err := instanceHoldPort(e.Record.Runtime.JavaPort)
		if err != nil {
			return err
		}
		for _, l := range held {
			l.Close()
		}
		if _, err = b.command(ctx, "systemctl", "--no-block", "start", "--", "minecraft-"+id+".service"); err != nil {
			return err
		}
		if err = b.waitSetupInstanceRunning(ctx, id); err != nil {
			return err
		}
		if _, err = b.command(ctx, "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", "--instance", id, e.Record.Runtime.Version); err != nil {
			return instanceRuntimeFailure("The selected Minecraft instance did not pass readiness verification.")
		}
	} else {
		state, err := b.observeRaw(ctx, id)
		if err != nil || state.State != "stopped" {
			return errInstanceRecovery
		}
	}
	e.Publication.Stage = "verified"
	return save()
}

// Backup units are prepared before identity publication. The existing public
// backup API still requires a registered identity. No timer can back up an
// unpublished server because the normal backup worker requires that identity.
func (b *instanceRuntimeBackend) prepareSetupBackups(ctx context.Context, e *setupInstanceEvidence, save func() error) error {
	id := e.Record.Runtime.ID
	daily, err := legacyBackupDaily("[Timer]\nOnCalendar=" + e.Request.Backups.Schedule + "\n")
	if err != nil {
		return err
	}
	settings := instanceBackupSettings{Enabled: e.Request.Backups.Automatic, Time: daily, Retention: e.Request.Backups.Keep}
	if !validInstanceBackupSettings(settings) {
		return errInstanceRecovery
	}
	instances, err := b.identities().listInstances("")
	if err != nil {
		return err
	}
	records := []instanceRuntimeRecord{}
	for _, instance := range instances {
		r, err := b.readRecord(instance.ID)
		if err != nil {
			return err
		}
		records = append(records, r)
	}
	if err = b.backupSeparationRecords(ctx, id, settings, records); err != nil {
		return err
	}
	if e.Publication.BackupUnits == nil {
		units := map[string][]byte{}
		for _, suffix := range []string{"service", "timer"} {
			path := filepath.Join(instanceBackupUnits, instanceBackupUnit(id, suffix))
			if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return errInstanceRecovery
			}
			raw, err := os.ReadFile(filepath.Join(instanceBackupTemplates, "minecraft-instance-backup."+suffix+".in"))
			if err != nil {
				return err
			}
			units[suffix] = []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), "@@INSTANCE_ID@@", id), "@@TIME@@", daily))
		}
		if _, err = os.Lstat(b.backupPath(id, "settings")); !errors.Is(err, os.ErrNotExist) {
			return errInstanceRecovery
		}
		e.Publication.BackupSettings = settings
		e.Publication.BackupUnits = units
		if err = save(); err != nil {
			return err
		}
	}
	if e.Publication.BackupSettings != settings {
		return errInstanceRecovery
	}
	for suffix, data := range e.Publication.BackupUnits {
		if suffix != "timer" && suffix != "service" {
			return errInstanceRecovery
		}
		if err = b.writeSetupExact(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, suffix)), data); err != nil {
			return err
		}
	}
	if raw, readErr := b.readPrivate(b.backupPath(id, "settings")); readErr == nil {
		var existing instanceBackupSettings
		if instanceDecode(raw, &existing) != nil || existing != settings {
			return errInstanceRecovery
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		if err = b.writeJSON(b.backupPath(id, "settings"), settings, false); err != nil {
			return err
		}
	} else {
		return readErr
	}
	if _, err = b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if settings.Enabled {
		if _, err = b.command(ctx, "systemctl", "enable", "--now", instanceBackupUnit(id, "timer")); err != nil {
			return err
		}
	}
	return b.verifySetupBackups(*e)
}
func (b *instanceRuntimeBackend) writeSetupExact(path string, data []byte) error {
	old, err := b.readPrivate(path)
	if err == nil {
		if instanceHash(old) != instanceHash(data) {
			return errInstanceRecovery
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return b.writePrivate(path, data, false)
}
func (b *instanceRuntimeBackend) verifySetupBackups(e setupInstanceEvidence) error {
	if len(e.Publication.BackupUnits) != 2 {
		return errInstanceRecovery
	}
	for _, suffix := range []string{"service", "timer"} {
		expected, ok := e.Publication.BackupUnits[suffix]
		if !ok {
			return errInstanceRecovery
		}
		raw, err := b.readPrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(e.Record.Runtime.ID, suffix)))
		if err != nil || instanceHash(raw) != instanceHash(expected) {
			return errInstanceRecovery
		}
	}
	raw, err := b.readPrivate(b.backupPath(e.Record.Runtime.ID, "settings"))
	var settings instanceBackupSettings
	if err != nil || instanceDecode(raw, &settings) != nil || settings != e.Publication.BackupSettings {
		return errInstanceRecovery
	}
	return nil
}

// This cleanup never calls Delete, creates Delete journals, or erases registry
// records. Every removed file and data directory is bound to schema-2 intent.
func (b *instanceRuntimeBackend) rollbackProvisionalSetup(ctx context.Context, e *setupInstanceEvidence, save func() error) error {
	id := e.Record.Runtime.ID
	if id == "" {
		return nil
	}
	if present, err := b.setupPublicationPresent(*e); err != nil || present {
		return errInstanceRecovery
	}
	if err := b.setupConflicts(*e); err != nil {
		return err
	}
	if err := b.removePrivate(setupPermitPath(id)); err != nil {
		return err
	}
	state, err := b.observeRaw(ctx, id)
	if err != nil {
		return err
	}
	if state.State == "running" {
		players, err := b.players(ctx, id)
		if err != nil {
			return err
		}
		if players.Online > 0 {
			if err = b.stopWithWarnings(ctx, id); err != nil {
				return err
			}
		} else {
			stopCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
			_, err = b.command(stopCtx, "systemctl", "stop", "--", "minecraft-"+id+".service")
			cancel()
			if err != nil {
				return err
			}
		}
	} else if state.State == "starting" {
		if _, err = b.command(ctx, "systemctl", "stop", "--", "minecraft-"+id+".service"); err != nil {
			return err
		}
	}
	if err = b.waitSetupInstanceInactive(ctx, id); err != nil {
		return err
	}
	dir, env, q := b.paths(id)
	// Prove all existing commitments before any destructive cleanup.
	for path, hash := range map[string]string{env: e.Record.EnvironmentSHA256, q: e.Record.QuadletSHA256} {
		raw, err := b.readPrivate(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || instanceHash(raw) != hash {
			return errInstanceRecovery
		}
	}
	if raw, err := b.readPrivate(filepath.Join(dir, "config.json")); err == nil {
		var r instanceRuntimeRecord
		if instanceDecode(raw, &r) != nil || !reflect.DeepEqual(r, e.Record) {
			return errInstanceRecovery
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e.DataIdentity == "" && e.DataParentIdentity != "" {
		parent, parentErr := b.setupDataParentIdentity(e.Record)
		if parentErr != nil || parent != e.DataParentIdentity {
			return errInstanceRecovery
		}
		if err = b.storage(ctx, "provision", e.Record); err != nil {
			return err
		}
		e.DataIdentity, err = b.setupInstanceData(ctx, "inspect", instanceDeleteJournal{ID: id, Record: e.Record})
		if err != nil {
			return err
		}
		if err = save(); err != nil {
			return err
		}
	}
	if _, err = os.Lstat(e.Record.Storage.DataPath); err == nil {
		identity, inspectErr := b.setupInstanceData(ctx, "inspect", instanceDeleteJournal{ID: id, Record: e.Record})
		if inspectErr != nil || e.DataIdentity == "" || identity != e.DataIdentity {
			return errInstanceRecovery
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e.Publication.Stage != "rolling_back" {
		e.Publication.Stage = "rolling_back"
		if err = save(); err != nil {
			return err
		}
	}
	if len(e.Publication.BackupUnits) > 0 {
		for suffix, expected := range e.Publication.BackupUnits {
			if suffix != "timer" && suffix != "service" {
				return errInstanceRecovery
			}
			path := filepath.Join(instanceBackupUnits, instanceBackupUnit(id, suffix))
			raw, err := b.readPrivate(path)
			if err == nil && instanceHash(raw) != instanceHash(expected) {
				return errInstanceRecovery
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if raw, readErr := b.readPrivate(b.backupPath(id, "settings")); readErr == nil {
			var settings instanceBackupSettings
			if instanceDecode(raw, &settings) != nil || settings != e.Publication.BackupSettings {
				return errInstanceRecovery
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if _, statErr := os.Lstat(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "timer"))); statErr == nil {
			if _, err = b.command(ctx, "systemctl", "disable", "--now", instanceBackupUnit(id, "timer")); err != nil {
				return err
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		for suffix := range e.Publication.BackupUnits {
			if err = b.removePrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, suffix))); err != nil {
				return err
			}
		}
		raw, readErr := b.readPrivate(b.backupPath(id, "settings"))
		if readErr == nil {
			var v instanceBackupSettings
			if instanceDecode(raw, &v) != nil || v != e.Publication.BackupSettings {
				return errInstanceRecovery
			}
			if err = b.removePrivate(b.backupPath(id, "settings")); err != nil {
				return err
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
	}
	ports := map[string]bool{}
	for _, rule := range e.Record.Firewall {
		key := fmt.Sprintf("%d/%s", rule.Port, rule.protocol())
		if ports[key] {
			continue
		}
		ports[key] = true
		held, holdErr := instanceHoldFirewallPort(rule)
		if holdErr != nil {
			return errInstanceRecovery
		}
		for _, listener := range held {
			defer listener.Close()
		}
	}
	for _, rule := range e.Record.Firewall {
		if err = b.changeFirewall(ctx, instanceFirewallChange{Rule: rule, Add: false}); err != nil {
			return err
		}
	}
	if _, err = b.command(ctx, "podman", "rm", "--ignore", "--", "minecraft-"+id); err != nil {
		return err
	}
	for _, path := range []string{env, q} {
		if err = b.removePrivate(path); err != nil {
			return err
		}
	}
	if _, err = b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, statErr := os.Lstat(e.Record.Storage.DataPath); statErr == nil && e.DataIdentity != "" {
		if _, err = b.setupInstanceData(ctx, "remove", instanceDeleteJournal{ID: id, Record: e.Record, DataIdentity: e.DataIdentity}); err != nil {
			return err
		}
	}
	if err = b.removePrivate(filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	if err = os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.Remove(filepath.Dir(e.Record.Storage.DataPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (b *instanceRuntimeBackend) setupDataParentIdentity(record instanceRuntimeRecord) (string, error) {
	d, err := b.privateDirectory(filepath.Dir(record.Storage.DataPath), false)
	if err != nil {
		return "", err
	}
	defer d.Close()
	info, err := d.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := infoSysStat(info)
	if !ok {
		return "", errInstanceRecovery
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}

// Reuse only the existing inode-bound data helper, never Delete's lifecycle or
// journal. Setup errors must keep the user on Setup recovery.
func (b *instanceRuntimeBackend) setupInstanceData(ctx context.Context, action string, evidence instanceDeleteJournal) (string, error) {
	identity, err := b.deleteStorage(ctx, action, evidence)
	if err != nil {
		return "", instanceRuntimeFailure("Setup instance data could not be safely verified. Retry Setup recovery.")
	}
	return identity, nil
}
