package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const instanceDeleteHelper = "/usr/libexec/justvoxel/mjust/instance-delete"

type instanceDeleteJournal struct {
	ResetPolicy  string                `json:"reset_policy,omitempty"`
	Schema       int                   `json:"schema"`
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Record       instanceRuntimeRecord `json:"record"`
	DataIdentity string                `json:"data_identity"`
	Stage        string                `json:"stage"`
}

func (b *instanceRuntimeBackend) deletePath(id string) string {
	return filepath.Join(b.staging, "delete-"+id+".json")
}
func (b *instanceRuntimeBackend) deleteJournal(id string) (instanceDeleteJournal, error) {
	var j instanceDeleteJournal
	if !instanceIDPattern.MatchString(id) {
		return j, errInstanceID
	}
	raw, err := b.readPrivate(b.deletePath(id))
	if err != nil {
		return j, err
	}
	if instanceDecode(raw, &j) != nil || j.Schema != 1 || j.ID != id || j.Record.Runtime.ID != id || j.Name == "" || !regexp.MustCompile(`^[0-9]+:[0-9]+$`).MatchString(j.DataIdentity) {
		return j, errInstanceRecovery
	}
	if name, e := validateInstanceName(j.Name); e != nil || name != j.Name {
		return j, errInstanceRecovery
	}
	if j.Record.SchemaVersion != 1 || j.Record.Storage.DataPath != filepath.Join(j.Record.Storage.Root, "minecraft-instances", id, "data") || !instanceSHA256Pattern.MatchString(j.Record.EnvironmentSHA256) || !instanceSHA256Pattern.MatchString(j.Record.QuadletSHA256) || !instanceRuntimeSecretPattern.MatchString(j.Record.Runtime.RCONPassword) {
		return j, errInstanceRecovery
	}
	for _, rule := range j.Record.Firewall {
		if !instanceFirewallZonePattern.MatchString(rule.Zone) || !instanceRuleForRuntime(rule, j.Record.Runtime) {
			return j, errInstanceRecovery
		}
	}
	if j.ResetPolicy != "" && j.ResetPolicy != "delete" && j.ResetPolicy != "preserve" && j.ResetPolicy != "start_over" {
		return j, errInstanceRecovery
	}
	switch j.Stage {
	case "intent", "runtime_retired", "data_removed", "identity_retired":
	default:
		return j, errInstanceRecovery
	}
	return j, nil
}
func (b *instanceRuntimeBackend) requireNoInstanceDelete() error {
	entries, err := os.ReadDir(b.staging)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "delete-") {
			return instanceRuntimeFailure("Server deletion is incomplete. Select that server and retry Delete Server.")
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) deleteStorage(ctx context.Context, action string, j instanceDeleteJournal) (string, error) {
	c, st := j.Record.Runtime, j.Record.Storage
	if st.DataPath != filepath.Join(st.Root, "minecraft-instances", j.ID, "data") || validateInstanceRuntimePath(st.Root) != nil || validateInstanceRuntimePath(st.DataPath) != nil {
		return "", errInstanceRecovery
	}
	evidence := j.DataIdentity
	if evidence == "" {
		evidence = "-"
	}
	out, err := b.command(ctx, "nsenter", "--mount=/proc/1/ns/mnt", "--", instanceDeleteHelper, action, j.ID, st.Root, st.DataPath, st.MountPoint, st.UUID, st.Source, strconv.FormatUint(uint64(c.UID), 10), strconv.FormatUint(uint64(c.GID), 10), evidence)
	if err != nil {
		return "", instanceRuntimeFailure("Selected server storage could not be verified or removed. Retry Delete Server.")
	}
	if action == "inspect" {
		var result struct {
			Identity string `json:"identity"`
		}
		if instanceDecode(out, &result) != nil || !regexp.MustCompile(`^[0-9]+:[0-9]+$`).MatchString(result.Identity) {
			return "", errInstanceRecovery
		}
		return result.Identity, nil
	}
	return "", nil
}
func (b *instanceRuntimeBackend) deleteStopped(ctx context.Context, id string, retry bool) error {
	state, err := b.observeRaw(ctx, id)
	if err != nil || (state.State != "stopped" && !(retry && state.State == "configured")) {
		return instanceRuntimeFailure("Stop this server normally before deleting it. Server state must be definitely stopped.")
	}
	// Pending native worker jobs and transitional/unknown service states refuse.
	out, err := b.command(ctx, "systemctl", "show", "--property=LoadState,ActiveState,Job", "--", instanceBackupUnit(id, "service"))
	if err != nil {
		return errInstanceBusy
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			props[k] = v
		}
	}
	if props["LoadState"] != "not-found" && (props["LoadState"] != "loaded" || (props["ActiveState"] != "inactive" && props["ActiveState"] != "failed")) {
		return errInstanceBusy
	}
	if props["Job"] != "" && props["Job"] != "0" {
		return errInstanceBusy
	}
	job, e := b.command(ctx, "systemctl", "show", "--property=Job", "--value", "--", "minecraft-"+id+".service")
	if e != nil || strings.TrimSpace(string(job)) != "0" {
		return errInstanceBusy
	}
	return nil
}
func (b *instanceRuntimeBackend) deleteInstance(ctx context.Context, id, name string) error {
	return b.retireSelectedInstance(ctx, id, name, "")
}

// Reset supplies a durable, separate data policy; ordinary Delete always removes data.
func (b *instanceRuntimeBackend) retireSelectedInstance(ctx context.Context, id, name, resetPolicy string) error {
	if !instanceIDPattern.MatchString(id) || (resetPolicy != "" && resetPolicy != "delete" && resetPolicy != "preserve" && resetPolicy != "start_over") {
		return errInstanceID
	}
	entries, err := os.ReadDir(b.staging)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "delete-") && entry.Name() != "delete-"+id+".json" {
			return errInstanceRecovery
		}
	}
	legacy, err := b.identities().read()
	if err != nil || legacy.ID == id {
		return errInstanceID
	}
	// Serialize name confirmation with the existing registry rename lock. Never
	// wait for an unbounded registry transaction while holding appliance locks.
	registry, err := b.identities().openDirectory(false)
	if err != nil {
		return err
	}
	defer registry.Close()
	if syscall.Flock(int(registry.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errInstanceBusy
	}
	defer syscall.Flock(int(registry.Fd()), syscall.LOCK_UN)
	j, err := b.deleteJournal(id)
	retry := err == nil
	if retry && j.ResetPolicy != resetPolicy {
		return errInstanceRecovery
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	names, err := b.identities().readNames()
	if err != nil {
		return err
	}
	if names[id].DisplayName == "" || names[id].DisplayName != name {
		return instanceRuntimeFailure("Type the server's current name exactly. Review Delete Server again if it was renamed.")
	}
	if retry && j.Name != name {
		return errInstanceRecovery
	}
	if retry && resetPolicy == "start_over" {
		return b.resumeStartOverRetirement(ctx, j)
	}
	if !retry {
		if err = b.requireNoInstanceRestore(); err != nil {
			return err
		}
		record, e := b.readRecord(id)
		if e != nil {
			return e
		}
		if e = b.verifyLive(record); e != nil {
			return e
		}
		snapshot, _, _, e := b.snapshot(ctx)
		if e != nil {
			return e
		}
		if record.Storage != instanceStorageFor(snapshot, id) {
			return errInstanceRecovery
		}
		if e = b.storage(ctx, "validate", record); e != nil {
			return e
		}
		for _, path := range []string{b.backupPath(id, "schedule-pending")} {
			if _, e = b.readPrivate(path); !errors.Is(e, os.ErrNotExist) {
				return errInstanceRecovery
			}
		}
		status, e := b.backupStatus(id)
		if e != nil {
			return e
		}
		if status.State == "running" || status.RestartRequired {
			return errInstanceRecovery
		}
		// Only verified terminal Restore evidence may be retired.
		if restore, e := b.restoreJournal(id); e == nil {
			if !restoreTerminal(restore.Stage) {
				return errInstanceRecovery
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return errInstanceRecovery
		}
		dir, _, _ := b.paths(id)
		entries, e := os.ReadDir(dir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			switch entry.Name() {
			case "config.json", "minecraft.env", "backup-settings.json", "backup-status.json", "restore-result.json", "backup-restore-result.json", "restore-journal.json":
			default:
				return errInstanceRecovery
			}
		}
		if e = b.deleteStopped(ctx, id, false); e != nil {
			return e
		}
		j = instanceDeleteJournal{ResetPolicy: resetPolicy, Schema: 1, ID: id, Name: name, Record: record, Stage: "intent"}
		j.DataIdentity, e = b.deleteStorage(ctx, "inspect", j)
		if e != nil {
			return e
		}
		if f, e := b.privateDirectory(b.staging, true); e != nil {
			return e
		} else {
			f.Close()
		}
		if e = b.writeJSON(b.deletePath(id), j, false); e != nil {
			return e
		}
	}
	if resetPolicy == "start_over" {
		return b.resumeStartOverRetirement(ctx, j)
	}
	if err = b.deleteStopped(ctx, id, true); err != nil {
		return err
	}
	snapshot, _, _, err := b.snapshot(ctx)
	if err != nil {
		return err
	}
	if j.Record.Storage != instanceStorageFor(snapshot, id) {
		return errInstanceRecovery
	}
	if record, e := b.readRecord(id); e == nil {
		a, _ := json.Marshal(record)
		z, _ := json.Marshal(j.Record)
		if !bytes.Equal(a, z) {
			return errInstanceRecovery
		}
	} else if (j.Stage != "data_removed" && j.Stage != "identity_retired") || (!errors.Is(e, os.ErrNotExist) && !errors.Is(e, errInstanceNotFound)) {
		return errInstanceRecovery
	}
	save := func(stage string) error { j.Stage = stage; return b.writeJSON(b.deletePath(id), j, true) }
	if j.Stage == "intent" {
		if err = b.retireInstanceRuntime(ctx, j); err != nil {
			return err
		}
		if err = save("runtime_retired"); err != nil {
			return err
		}
	}
	if j.Stage == "runtime_retired" {
		if err = b.applyInstanceRetirementDataPolicy(ctx, j); err != nil {
			return err
		}
		if err = save("data_removed"); err != nil {
			return err
		}
	}
	if j.Stage == "data_removed" {
		if err = b.applyInstanceRetirementDataPolicy(ctx, j); err != nil {
			return err
		}
		if err = b.retireInstanceStateAndIdentity(j); err != nil {
			return err
		}
		if err = save("identity_retired"); err != nil {
			return err
		}
	}
	dir, _, _ := b.paths(id)
	if err = os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent, err := instanceOpen(b.root, syscall.O_RDONLY|syscall.O_DIRECTORY)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err = parent.Sync(); err != nil {
		return err
	}
	if resetPolicy != "" {
		return nil
	}
	return b.removePrivate(b.deletePath(id))
}
func (s *server) instanceDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	// Decode a non-null object and prove EOF before reserving mutation exclusion.
	var request *struct {
		ConfirmName *string `json:"confirm_name"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil || instanceDecode(raw, &request) != nil || request == nil || request.ConfirmName == nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	release, err := s.claimMinecraftMutation()
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	err = instanceRuntimes.deleteInstance(ctx, id, *request.ConfirmName)
	cancel()
	err = errors.Join(err, release())
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}

// Recovery inventory is separate from active registration. It exposes only
// presentation and stage; the private deletion journal never leaves the Agent.
func (s *server) instanceDeletionInventory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	entries, err := os.ReadDir(instanceRuntimes.staging)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, 200, map[string]any{"deletions": []minecraftRegisteredInstance{}})
		return
	}
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	out := []minecraftRegisteredInstance{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "delete-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "delete-"), ".json")
		j, e := instanceRuntimes.deleteJournal(id)
		if e != nil {
			instanceRuntimeError(w, e, "")
			return
		}
		// Reset owns recovery of its retirement journal; it is not a Delete retry.
		if j.ResetPolicy != "" {
			continue
		}
		out = append(out, minecraftRegisteredInstance{ID: id, DisplayName: j.Name})
	}
	writeJSON(w, 200, map[string]any{"deletions": out})
}

// Shared retirement stages retain Delete Server's ordering and exact ownership checks.
// The caller durably records intent and its data policy before invoking them.
func (b *instanceRuntimeBackend) retireInstanceRuntime(ctx context.Context, j instanceDeleteJournal) error {
	id := j.ID
	var err error
	timer := instanceBackupUnit(id, "timer")
	if _, e := os.Lstat(filepath.Join(instanceBackupUnits, timer)); e == nil {
		if _, e = b.command(ctx, "systemctl", "disable", "--now", "--", timer); e != nil {
			return errInstanceBusy
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if err = b.deleteStopped(ctx, id, true); err != nil {
		return err
	}
	for _, rule := range j.Record.Firewall {
		if err = b.changeFirewall(ctx, instanceFirewallChange{Rule: rule, Add: false}); err != nil {
			return err
		}
	}
	if _, err = b.command(ctx, "podman", "rm", "--ignore", "--", "minecraft-"+id); err != nil {
		return errInstanceBusy
	}
	_, env, q := b.paths(id)
	for _, path := range []string{env, q, filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "timer")), filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "service"))} {
		if err = b.removePrivate(path); err != nil {
			return err
		}
	}
	if _, err = b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return errInstanceBusy
	}
	return nil
}
func (b *instanceRuntimeBackend) removeInstanceData(ctx context.Context, j instanceDeleteJournal) error {
	_, err := b.deleteStorage(ctx, "remove", j)
	return err
}
func (b *instanceRuntimeBackend) retireInstanceStateAndIdentity(j instanceDeleteJournal) error {
	id := j.ID
	var err error
	// Remove only known operational state. Archives/metadata live elsewhere.
	for _, file := range []string{"settings", "status", "restore-result"} {
		if err = b.removePrivate(b.backupPath(id, file)); err != nil {
			return err
		}
	}
	dir, _, _ := b.paths(id)
	if err = b.removePrivate(filepath.Join(dir, "restore-journal.json")); err != nil {
		return err
	}
	if err = b.removePrivate(filepath.Join(dir, "restore-result.json")); err != nil {
		return err
	}
	if err = b.removePrivate(filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	if err = os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent, e := instanceOpen(b.root, syscall.O_RDONLY|syscall.O_DIRECTORY)
	if e != nil {
		return e
	}
	e = parent.Sync()
	parent.Close()
	if e != nil {
		return e
	}
	// Identity LAST, synced, while historical name evidence remains untouched.
	if err = b.removePrivate(filepath.Join(b.identities().directory, id+".json")); err != nil {
		return err
	}
	return nil
}

func (b *instanceRuntimeBackend) applyInstanceRetirementDataPolicy(ctx context.Context, j instanceDeleteJournal) error {
	if j.ResetPolicy == "start_over" {
		identity, err := b.deleteStorage(ctx, "inspect", j)
		if err != nil || identity != j.DataIdentity {
			return errInstanceRecovery
		}
		return nil
	}
	if j.ResetPolicy == "preserve" {
		return nil
	}
	return b.removeInstanceData(ctx, j)
}
