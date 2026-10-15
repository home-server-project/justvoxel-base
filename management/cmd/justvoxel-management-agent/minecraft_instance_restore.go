package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

const instanceRestoreHelper = "/usr/libexec/justvoxel/mjust/instance-restore"

var instanceRestoreArchivePattern = regexp.MustCompile(`^instance-backup-[a-z][a-z0-9-]{0,70}-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{4}(-[1-9][0-9]{0,2})?\.tar\.gz$`)
var instanceRestoreActive atomic.Bool
var instanceRestoreActiveID atomic.Pointer[string]
var instanceRestoreExclusionPath = "/run/justvoxel-minecraft-restore-exclusion.lock"
var instanceRestorePermitRoot = "/run/justvoxel"

// Private evidence includes directory inode identities, never API-supplied paths.
// Data-engine schema is versioned independently from runtime activation journals.
type instanceRestoreJournal struct {
	Schema            int      `json:"schema"`
	ID                string   `json:"id"`
	Transaction       string   `json:"transaction"`
	ConfigSHA256      string   `json:"config_sha256"`
	Parent            []uint64 `json:"parent"`
	Original          []uint64 `json:"original"`
	Replacement       []uint64 `json:"replacement,omitempty"`
	OriginallyRunning bool     `json:"originally_running"`
	Stage             string   `json:"stage"`
	Archive           string   `json:"archive"`
	Fingerprint       string   `json:"fingerprint"`
}
type instanceRestoreInspection struct {
	Metadata                        instanceBackupMetadata `json:"metadata"`
	CurrentSoftware                 string                 `json:"current_software"`
	CurrentVersion                  string                 `json:"current_version"`
	Integrity                       string                 `json:"integrity"`
	SourceAvailable                 bool                   `json:"source_available"`
	DestinationAvailable            bool                   `json:"destination_available"`
	Compatibility                   string                 `json:"compatibility"`
	DestructiveConfirmationRequired bool                   `json:"destructive_confirmation_required"`
	RequiredBytes                   int64                  `json:"required_bytes"`
	ExpandedBytes                   int64                  `json:"expanded_bytes"`
	Entries                         int64                  `json:"entries"`
	Fingerprint                     string                 `json:"fingerprint"`
}
type instanceRestorePlan struct {
	instanceRestoreInspection
	PlanID                      string          `json:"plan_id"`
	RuntimeState                string          `json:"runtime_state"`
	Players                     instancePlayers `json:"players"`
	PlayersConfirmationRequired bool            `json:"players_confirmation_required"`
}
type instanceRestoreRequest struct {
	Archive            string `json:"archive"`
	PlanID             string `json:"plan_id"`
	ConfirmDestructive bool   `json:"confirm_destructive"`
	ConfirmPlayers     bool   `json:"confirm_players"`
}

func restoreTerminal(stage string) bool { return stage == "completed" || stage == "rolled_back" }
func (b *instanceRuntimeBackend) restoreJournal(id string) (instanceRestoreJournal, error) {
	var j instanceRestoreJournal
	if !instanceIDPattern.MatchString(id) {
		return j, errInstanceID
	}
	raw, err := b.readPrivate(filepath.Join(b.root, id, "restore-journal.json"))
	if err != nil {
		return j, err
	}
	if instanceDecode(raw, &j) != nil || j.Schema != 1 || j.ID != id || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(j.Transaction) || !instanceSHA256Pattern.MatchString(j.ConfigSHA256) || len(j.Original) != 2 || len(j.Parent) != 2 || !instanceRestoreArchivePattern.MatchString(j.Archive) {
		return j, errInstanceRecovery
	}
	return j, nil
}

// Called under existing appliance operation protection. An interrupted restore
// blocks configuration, version changes, and backups until dedicated recovery.
func (b *instanceRuntimeBackend) requireNoInstanceRestore() error {
	records, err := b.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		j, e := b.restoreJournal(record.Runtime.ID)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !restoreTerminal(j.Stage) {
			return instanceRuntimeFailure("Independent restore recovery is required. Use the instance backup Restore Recovery API.")
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) restoreStorage(ctx context.Context, action, id, archive, fingerprint string, running bool) ([]byte, error) {
	if !instanceIDPattern.MatchString(id) {
		return nil, errInstanceID
	}
	out, err := instanceBackupStorageCommand(ctx, "nsenter", "--mount=/proc/1/ns/mnt", "--", instanceRestoreHelper, action, id, archive, fingerprint, strconv.FormatBool(running))
	if err != nil {
		var refusal struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(out, &refusal) == nil {
			switch refusal.Error {
			case "incompatible_software_version":
				return nil, instanceRuntimeFailure("Restore requires the same software and exact pinned Minecraft version. Version conversion and downgrade are unsupported.")
			case "unverified_version":
				return nil, instanceRuntimeFailure("Restore compatibility cannot be verified for an unpinned or unsupported version.")
			case "insufficient_staging_capacity":
				return nil, instanceRuntimeFailure("Destination storage has insufficient capacity for restore staging and rollback headroom.")
			case "stale_plan":
				return nil, instanceRuntimeFailure("Restore plan is stale. Review Restore again before Apply.")
			}
		}
		return nil, instanceRuntimeFailure("Independent restore validation, compatibility, storage, capacity, or transaction check failed. Review the archive again; interrupted operations require Restore Recovery.")
	}
	return out, nil
}
func (b *instanceRuntimeBackend) planInstanceRestore(ctx context.Context, id, archive string) (instanceRestorePlan, error) {
	var plan instanceRestorePlan
	if !instanceRestoreArchivePattern.MatchString(archive) || len(archive) > 160 {
		return plan, instanceRuntimeFailure("Invalid independent archive identifier.")
	}
	record, err := b.readRecord(id)
	if err != nil {
		return plan, err
	}
	if err = b.verifyLive(record); err != nil {
		return plan, err
	}
	state, err := b.observe(ctx, id)
	if err != nil {
		return plan, err
	}
	if state.State != "running" && state.State != "stopped" {
		return plan, instanceRuntimeFailure("Instance must be verifiably running or stopped before Restore.")
	}
	plan.RuntimeState = state.State
	plan.Players = instancePlayers{ID: id, State: "offline"}
	if state.State == "running" {
		plan.Players, err = b.players(ctx, id)
		if err != nil {
			return plan, err
		}
		plan.PlayersConfirmationRequired = true
	}
	out, err := b.restoreStorage(ctx, "plan", id, archive, "", state.State == "running")
	if err != nil {
		return plan, err
	}
	if instanceDecode(out, &plan.instanceRestoreInspection) != nil || plan.Metadata.ID != id || plan.Metadata.Archive != archive || plan.Integrity != "verified" || !instanceSHA256Pattern.MatchString(plan.Fingerprint) {
		return plan, instanceRuntimeFailure("Restore inspection is unavailable.")
	}
	// The reviewed identity commits immutable storage/archive/runtime evidence and
	// running-state intent. A player joining later still receives the countdown.
	plan.PlanID = instanceHash([]byte(plan.Fingerprint + ":" + plan.RuntimeState))
	return plan, nil
}

func registerInstanceRestoreRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/backups/restore/plan", s.instanceRestorePlan)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/backups/restore/apply", s.instanceRestoreApply)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/backups/restore/status", s.instanceRestoreStatus)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/backups/restore/recover", s.instanceRestoreRecover)
}
func (s *server) instanceRestorePlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct {
		Archive string `json:"archive"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()
	plan, err := instanceRuntimes.planInstanceRestore(ctx, id, request.Archive)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, 200, plan)
}
func (s *server) instanceRestoreStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	if _, err := instanceRuntimes.readRecord(id); err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	j, err := instanceRuntimes.restoreJournal(id)
	activeID := instanceRestoreActiveID.Load()
	active := activeID != nil && *activeID == id
	if errors.Is(err, os.ErrNotExist) {
		state := "idle"
		if active {
			state = "validating"
		} else if raw, e := instanceRuntimes.readPrivate(instanceRuntimes.backupPath(id, "restore-result")); e == nil {
			var result struct {
				State   string `json:"state"`
				Message string `json:"message"`
			}
			if instanceDecode(raw, &result) != nil {
				instanceRuntimeError(w, errInstanceRecovery, id)
				return
			}
			state = result.State
			if state == "preparing" {
				state = "interrupted"
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			instanceRuntimeError(w, e, id)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "state": state})
		return
	}
	if err != nil {
		writeJSON(w, 200, map[string]any{"id": id, "state": "recovery_required", "recovery_required": true})
		return
	}
	state := j.Stage
	if active && restoreTerminal(j.Stage) {
		state = "validating"
	}
	required := !restoreTerminal(j.Stage) && !active
	if required {
		state = "recovery_required"
	}
	writeJSON(w, 200, map[string]any{"id": id, "state": state, "stage": j.Stage, "archive": j.Archive, "originally_running": j.OriginallyRunning, "recovery_required": required})
}

// The fixed root-private exclusion is shared with the legacy worker. Never
// unlink it: every contender must flock the same inode, even after a crash.
func acquireInstanceRestoreExclusion() (*os.File, error) {
	fd, err := unix.Open(instanceRestoreExclusionPath, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errInstanceBusy
	}
	f := os.NewFile(uintptr(fd), "minecraft-restore-exclusion")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		f.Close()
		return nil, errInstanceBusy
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		f.Close()
		return nil, errInstanceBusy
	}
	return f, nil
}

// Retain the existing setup/restore/migration claims and store mutex throughout
// startup. Only the maintenance flock is released for Phase 3B ExecStartPre.
// No other Agent backup, restore, configuration, or lifecycle mutation can enter.
type instanceRestoreClaim struct {
	s           *server
	maintenance *os.File
	exclusion   *os.File
}

func (s *server) claimInstanceRestore(recovery bool) (*instanceRestoreClaim, error) {
	if !instanceRestoreActive.CompareAndSwap(false, true) {
		return nil, errInstanceBusy
	}
	fail := func(err error) (*instanceRestoreClaim, error) { instanceRestoreActive.Store(false); return nil, err }
	if s.operations == nil {
		return fail(errInstanceBusy)
	}
	o := s.operations
	if !o.mu.TryLock() {
		return fail(errInstanceBusy)
	}
	unlock := func(err error) (*instanceRestoreClaim, error) { o.mu.Unlock(); return fail(err) }
	if o.currentSetupID != "" || o.currentRestoreID != "" || o.currentDataMigrationID != "" || o.currentMigrationID != "" || o.currentMinecraftResetID != "" || o.currentFactoryResetID != "" {
		return unlock(errInstanceBusy)
	}
	if !recovery {
		if err := instanceRuntimes.requireNoInstanceRestore(); err != nil {
			return unlock(err)
		}
	}
	if err := o.acquireMinecraftResetLocks(); err != nil {
		return unlock(errInstanceBusy)
	}
	exclusion, err := acquireInstanceRestoreExclusion()
	if err != nil {
		return unlock(errors.Join(err, o.releaseMinecraftResetLocks()))
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		return unlock(errors.Join(errInstanceBusy, exclusion.Close(), o.releaseMinecraftResetLocks()))
	}
	return &instanceRestoreClaim{s: s, maintenance: lock, exclusion: exclusion}, nil
}
func (c *instanceRestoreClaim) close() error {
	var err error
	if c.maintenance != nil {
		err = c.maintenance.Close()
		c.maintenance = nil
	}
	if c.exclusion != nil {
		err = errors.Join(err, c.exclusion.Close())
		c.exclusion = nil
	}
	err = errors.Join(err, c.s.operations.releaseMinecraftResetLocks())
	instanceRestoreActiveID.Store(nil)
	instanceRestoreActive.Store(false)
	c.s.operations.mu.Unlock()
	return err
}
func (c *instanceRestoreClaim) verifyIntent(ctx context.Context, id string, running bool) (result error) {
	b := instanceRuntimes
	record, err := b.readRecord(id)
	if err != nil {
		return err
	}
	if err = b.verifyLive(record); err != nil {
		return err
	}
	if err = b.storage(ctx, "validate", record); err != nil {
		return err
	}
	if running {
		j, e := b.restoreJournal(id)
		if e != nil {
			return e
		}
		proc, e := os.ReadFile("/proc/self/stat")
		if e != nil {
			return e
		}
		fields := strings.Fields(string(proc)[strings.LastIndex(string(proc), ")")+1:])
		if len(fields) < 20 {
			return errInstanceRecovery
		}
		permit := filepath.Join(instanceRestorePermitRoot, "instance-restore-start-"+id+".json")
		if e = b.writeJSON(permit, map[string]any{"id": id, "transaction": j.Transaction, "agent_pid": os.Getpid(), "agent_start": fields[19]}, true); e != nil {
			return e
		}
		defer func() { result = errors.Join(result, b.removePrivate(permit)) }()
		if c.maintenance != nil {
			if err = c.maintenance.Close(); err != nil {
				return err
			}
			c.maintenance = nil
		}
		// Synchronous start is safe now; other persistent operation claims stay held.
		if _, err = b.command(ctx, "systemctl", "start", "--", "minecraft-"+id+".service"); err != nil {
			return instanceRuntimeFailure("Instance restart could not be verified.")
		}
	}

	if !running {
		state, e := b.observe(ctx, id)
		if e == nil && state.State == "failed" {
			// A failed startup can leave a stopped container and a failed
			// systemd unit. Clear only this instance's failed service state.
			_, e = b.command(ctx, "systemctl", "reset-failed", "--", "minecraft-"+id+".service")
			if e == nil {
				state, e = b.observe(ctx, id)
			}
		}
		if e != nil || state.State != "stopped" {
			return instanceRuntimeFailure("Originally stopped instance did not remain stopped.")
		}
		return nil
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	for {
		state, e := b.observe(verifyCtx, id)
		if e != nil || (state.State != "running" && state.State != "starting") {
			return instanceRuntimeFailure("Restored instance runtime failed verification.")
		}
		if state.State == "running" {
			if _, e = b.players(verifyCtx, id); e == nil {
				return nil
			}
		}
		if b.waitMaintenance(verifyCtx, 2*time.Second) != nil {
			return instanceRuntimeFailure("Minecraft readiness could not be verified before the safety timeout.")
		}
	}
}
func (c *instanceRestoreClaim) reacquire() error {
	if c.maintenance != nil {
		return nil
	}
	var err error
	c.maintenance, err = acquirePluginRuntimeLock()
	return err
}
func (s *server) instanceRestoreApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request instanceRestoreRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !request.ConfirmDestructive || !instanceSHA256Pattern.MatchString(request.PlanID) {
		writeError(w, 400, "Reviewed plan and destructive confirmation are required.")
		return
	}
	claim, err := s.claimInstanceRestore(false)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	instanceRestoreActiveID.Store(&id)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	plan, err := instanceRuntimes.planInstanceRestore(ctx, id, request.Archive)
	cancel()
	if err == nil && (plan.PlanID != request.PlanID || (plan.PlayersConfirmationRequired && !request.ConfirmPlayers)) {
		err = instanceRuntimeFailure("Restore plan is stale or player interruption has not been confirmed. Review Restore again.")
	}
	if err != nil {
		err = errors.Join(err, claim.close())
		instanceRuntimeError(w, err, id)
		return
	}

	if err = instanceRuntimes.writeJSON(instanceRuntimes.backupPath(id, "restore-result"), map[string]any{"state": "preparing"}, true); err != nil {
		err = errors.Join(err, claim.close())
		instanceRuntimeError(w, err, id)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		result := s.runInstanceRestore(ctx, id, plan, claim)
		result = errors.Join(result, claim.close())
		s.saveInstanceRestoreResult(id, result)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "state": "accepted"})
}
func (s *server) runInstanceRestore(ctx context.Context, id string, plan instanceRestorePlan, claim *instanceRestoreClaim) error {
	var err error
	b := instanceRuntimes
	running := plan.RuntimeState == "running"
	step := func(action string) error {
		_, e := b.restoreStorage(ctx, action, id, plan.Metadata.Archive, plan.Fingerprint, running)
		return e
	}
	// Preparation errors never stop Minecraft. A journal, if created, retains
	// evidence and is resolved through the same recovery endpoint.
	if err = step("prepare"); err != nil {
		return err
	}
	if err = step("shutdown"); err != nil {
		return err
	}
	if running {
		err = b.actionWithWarnings(ctx, id, "stop", true)
	}
	if err == nil {
		state, e := b.observeRaw(ctx, id)
		if e != nil || state.State != "stopped" {
			err = errInstanceBusy
		}
	}
	if err == nil {
		err = step("activate")
	}
	if err == nil {
		err = step("verifying")
	}
	if err == nil {
		err = claim.verifyIntent(ctx, id, running)
	}
	if err == nil {
		err = step("completed")
	}
	if err != nil {
		recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer recoveryCancel()
		return errors.Join(err, claim.recover(recoveryCtx, id))
	}
	return nil
}
func (s *server) saveInstanceRestoreResult(id string, result error) {
	state, message := "finished", ""
	if result != nil {
		state = "failed"
		message = "Restore did not complete. Check recovery_required and retry Restore Recovery when required."
	}
	if err := instanceRuntimes.writeJSON(instanceRuntimes.backupPath(id, "restore-result"), map[string]any{"state": state, "message": message}, true); err != nil {
		// Journals remain authoritative if the separate diagnostic cannot be saved.
		log.Print("Independent restore result could not be persisted")
	}
}

func (c *instanceRestoreClaim) recover(ctx context.Context, id string) (result error) {
	b := instanceRuntimes
	j, err := b.restoreJournal(id)
	if err != nil {
		return err
	}
	if restoreTerminal(j.Stage) {
		return nil
	}
	step := func(action string) error {
		_, e := b.restoreStorage(ctx, action, id, j.Archive, j.Fingerprint, j.OriginallyRunning)
		return e
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, step("recovery-required"))
		}
	}()
	if err = c.reacquire(); err != nil {
		return err
	}
	state, err := b.observeRaw(ctx, id)
	if err != nil {
		return err
	}
	// For failed preparation the original directory has never moved. Do not
	// interrupt a running original merely to close a staging journal.
	originalUnchanged := j.Stage == "staging" || j.Stage == "ready_to_apply"
	if !originalUnchanged {
		if state.State == "running" {
			if err = b.actionWithWarnings(ctx, id, "stop", true); err != nil {
				return err
			}
		} else if state.State != "stopped" && state.State != "failed" {
			return errInstanceBusy
		}
	}
	// observeRaw has already proved a failed service has no live container.
	// Permit rollback even if its data directory is temporarily moved aside.
	if !originalUnchanged && state.State != "stopped" && state.State != "failed" {
		state, err = b.observeRaw(ctx, id)
		if err != nil || (state.State != "stopped" && state.State != "failed") {
			return errInstanceBusy
		}
	}
	if _, err = b.restoreStorage(ctx, "rollback", id, j.Archive, j.Fingerprint, j.OriginallyRunning); err != nil {
		return err
	}
	if err = step("rollback-verifying"); err != nil {
		return err
	}
	if err = c.verifyIntent(ctx, id, j.OriginallyRunning); err != nil {
		return err
	}
	return step("rolled-back")
}
func (s *server) instanceRestoreRecover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct {
		ConfirmPlayers bool `json:"confirm_players"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !request.ConfirmPlayers {
		writeError(w, 400, "Administrator confirmation for safe recovery interruption is required.")
		return
	}
	claim, err := s.claimInstanceRestore(true)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	instanceRestoreActiveID.Store(&id)
	if _, err = instanceRuntimes.restoreJournal(id); err != nil {
		err = errors.Join(err, claim.close())
		instanceRuntimeError(w, err, id)
		return
	}
	go func() {

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		result := claim.recover(ctx, id)
		result = errors.Join(result, claim.close())
		s.saveInstanceRestoreResult(id, result)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "state": "recovering"})
}
