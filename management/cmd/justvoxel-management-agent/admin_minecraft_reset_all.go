package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Reset All shares Start Over's ownership inventory, never its data policy.
type minecraftResetAllPlan struct {
	OK                   bool                   `json:"ok"`
	SchemaVersion        string                 `json:"schema_version"`
	Mode                 string                 `json:"mode"`
	PlanFingerprint      string                 `json:"plan_fingerprint"`
	Participants         []startOverParticipant `json:"participants"`
	PlayersOnline        int                    `json:"players_online"`
	TotalPlayersOnline   int                    `json:"total_players_online"`
	Players              []string               `json:"players"`
	Warnings             []string               `json:"warnings"`
	BackupAction         string                 `json:"backup_action"`
	StorageLayoutAction  string                 `json:"storage_layout_action"`
	AuthenticationAction string                 `json:"authentication_action"`
	WebUIUsersAction     string                 `json:"webui_users_action"`
}

func validResetAllDataPolicy(scope, action string) bool {
	switch scope {
	case "internal":
		return action == "delete"
	case "external", "network", "unknown":
		return action == "preserve"
	}
	return false
}

// Only the exact on-disk reset_all journal can authorize recovery. Ordinary
// lifecycle callers continue to reject pending retirement journals.
func (b *instanceRuntimeBackend) checkResetAllJournal(j instanceDeleteJournal) error {
	persisted, err := b.deleteJournal(j.ID)
	if err != nil || persisted.ResetPolicy != "reset_all" || !reflect.DeepEqual(persisted, j) || validateInstanceRecordPlacement(j.Record) != nil {
		return errInstanceRecovery
	}
	identityPath := filepath.Join(b.identities().directory, j.ID+".json")
	raw, err := b.readPrivate(identityPath)
	if err == nil {
		var identity minecraftInstanceRecord
		if j.Stage == "identity_retired" || instanceDecode(raw, &identity) != nil || identity.ID != j.ID {
			return errInstanceRecovery
		}
	} else if !errors.Is(err, os.ErrNotExist) || (j.Stage != "data_removed" && j.Stage != "identity_retired") {
		return errInstanceRecovery
	}
	names, err := b.identities().readNames()
	if err != nil {
		return err
	}
	name, exists := names[j.ID]
	if (exists && (j.Stage == "identity_retired" || name.DisplayName != j.Name)) || (!exists && j.Stage != "data_removed" && j.Stage != "identity_retired") {
		return errInstanceRecovery
	}
	return nil
}

func (b *instanceRuntimeBackend) checkResetAllLiveRuntime(j instanceDeleteJournal) error {
	if j.Stage != "intent" {
		return errInstanceRecovery
	}
	_, env, q := b.paths(j.ID)
	for path, hash := range map[string]string{env: j.Record.EnvironmentSHA256, q: j.Record.QuadletSHA256} {
		raw, err := b.readPrivate(path)
		if err != nil || instanceHash(raw) != hash {
			return errInstanceRecovery
		}
	}
	return nil
}

func (b *instanceRuntimeBackend) stopResetAllParticipant(ctx context.Context, j instanceDeleteJournal, confirmPlayers bool) error {
	if err := b.checkResetAllJournal(j); err != nil {
		return err
	}
	if err := b.checkStartOverRetirement(ctx, j); err != nil {
		return err
	}
	if err := b.checkResetAllLiveRuntime(j); err != nil {
		return err
	}
	return b.actionWithVerifiedRecordWarnings(ctx, j.Record, "stop", confirmPlayers)
}
func (b *instanceRuntimeBackend) resetAllDataPolicy(ctx context.Context, path string) (string, string, error) {
	raw, err := b.command(ctx, "nsenter", "--mount=/proc/1/ns/mnt", "--", "bash", "-c", `source /usr/libexec/justvoxel/mjust/minecraft-reset-common.sh; jv_reset_data_scope "$1"`, "reset-data-scope", path)
	if err != nil {
		return "", "", err
	}
	scope, action := strings.TrimSpace(string(raw)), "preserve"
	if scope == "internal" {
		action = "delete"
	}
	if !validResetAllDataPolicy(scope, action) {
		return "", "", errInstanceRecovery
	}
	return scope, action, nil
}

// Review is read-only, including the interruption window after deleting data
// but before persisting data_removed. Missing data never authorizes a new target.
func (b *instanceRuntimeBackend) checkRetirementDataPolicy(ctx context.Context, j instanceDeleteJournal) error {
	if j.ResetPolicy == "start_over" {
		return b.applyInstanceRetirementDataPolicy(ctx, j)
	}
	if j.ResetPolicy != "reset_all" || !validResetAllDataPolicy(j.DataScope, j.DataAction) {
		return errInstanceRecovery
	}
	if j.DataAction == "delete" && j.Stage != "intent" {
		action := "absent"
		if j.Stage == "runtime_retired" {
			action = "verify"
		}
		_, err := b.deleteStorage(ctx, action, j)
		return err
	}
	identity, err := b.deleteStorage(ctx, "inspect", j)
	if err != nil || identity != j.DataIdentity {
		return errInstanceRecovery
	}
	return nil
}

func (b *instanceRuntimeBackend) validateRetirementStorage(ctx context.Context, j instanceDeleteJournal) error {
	action := "validate"
	if j.ResetPolicy == "reset_all" && j.DataAction == "delete" && j.Stage != "intent" {
		action = "restore-validate"
	}
	return b.storage(ctx, action, j.Record)
}

// A backup namespace belonging to any participant must never fall within a
// different participant's deleted data, including legacy backup destinations.
func resetAllBackupSeparation(entries []instanceDeleteJournal, legacyBackup string) error {
	for _, candidate := range entries {
		if candidate.DataAction != "delete" {
			continue
		}
		for _, participant := range entries {
			backup := legacyBackup
			if participant.Record.Backups != nil {
				backup = participant.Record.Backups.Root
			}
			if backup != "" && instanceRuntimeInside(backup, candidate.Record.Storage.DataPath) {
				return errInstanceRecovery
			}
		}
	}
	return nil
}

func registerAdminMinecraftResetAllRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/reset/minecraft/plan", s.adminMinecraftResetAllPlan)
	mux.HandleFunc("POST /v1/admin/reset/minecraft/apply", s.adminMinecraftResetAllApply)
	mux.HandleFunc("GET /v1/admin/reset/minecraft/current-operation", s.adminMinecraftResetAllCurrent)
}
func resetAllFailure(w http.ResponseWriter) {
	writeError(w, http.StatusConflict, "Minecraft removal stopped before completion. Continue removal to finish removing the remaining servers.")
}
func (s *server) resetAllCurrent() (*operationJournal, error) {
	if s.operations == nil {
		return nil, errInstanceRecovery
	}
	op, err := s.operations.currentMinecraftReset()
	if err != nil || (op != nil && op.InstanceID != "") {
		return nil, errInstanceRecovery
	}
	return op, nil
}
func (s *server) adminMinecraftResetAllCurrent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		resetAllFailure(w)
		return
	}
	// Historical selected resets remain visible for recovery through their
	// original routes; the new plan/apply routes never adopt their policy.
	op, err := s.operations.currentMinecraftReset()
	if err != nil {
		resetAllFailure(w)
		return
	}
	writeJSON(w, 200, adminOperationResponse{Operation: op})
}
func (s *server) adminMinecraftResetAllPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if _, err := s.resetAllCurrent(); err != nil {
		resetAllFailure(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	p, err := instanceRuntimes.planApplianceRetirement(ctx, "reset_all")
	if err != nil {
		resetAllFailure(w)
		return
	}
	players := []string{}
	for _, participant := range p.Participants {
		players = append(players, participant.Players...)
	}
	writeJSON(w, 200, minecraftResetAllPlan{OK: true, SchemaVersion: "v1", Mode: "minecraft", PlanFingerprint: p.PlanFingerprint, Participants: p.Participants, PlayersOnline: p.TotalPlayersOnline, TotalPlayersOnline: p.TotalPlayersOnline, Players: players, Warnings: []string{}, BackupAction: "preserve", StorageLayoutAction: "preserve", AuthenticationAction: "preserve", WebUIUsersAction: "preserve"})
}
func (s *server) adminMinecraftResetAllApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var req adminMinecraftResetApplyRequest
	if !decodeAdminMinecraftResetApplyRequest(w, r, &req) {
		return
	}
	if !operationFingerprintPattern.MatchString(req.PlanFingerprint) {
		writeError(w, 400, "Invalid reviewed Minecraft removal plan")
		return
	}
	current, err := s.resetAllCurrent()
	if err != nil {
		resetAllFailure(w)
		return
	}
	if current != nil && current.State != operationNeedsAttention {
		if current.PlanFingerprint != req.PlanFingerprint {
			resetAllFailure(w)
			return
		}
		writeJSON(w, 200, adminMinecraftResetApplyResponse{OK: true, Operation: current})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	p, err := instanceRuntimes.planApplianceRetirement(ctx, "reset_all")
	if err != nil {
		resetAllFailure(w)
		return
	}
	if p.PlanFingerprint != req.PlanFingerprint {
		writeError(w, 409, "Minecraft removal plan changed; review it again")
		return
	}
	if p.TotalPlayersOnline > 0 && !req.ConfirmPlayers {
		writeError(w, 409, "Players are online; explicit interruption confirmation is required")
		return
	}
	var op operationJournal
	created := false
	if current != nil {
		op, err = s.operations.retryMinecraftReset(current.OperationID, p.PlanFingerprint)
	} else {
		op, created, err = s.operations.beginMinecraftReset(p.PlanFingerprint)
	}
	if err != nil {
		resetAllFailure(w)
		return
	}
	writeJSON(w, http.StatusAccepted, adminMinecraftResetApplyResponse{OK: true, Created: created, Operation: &op})
	if created || current != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			_ = executeMinecraftResetAll(ctx, s, op.OperationID, p.PlanFingerprint, req.ConfirmPlayers, actor)
		}()
	}
}

func executeMinecraftResetAll(ctx context.Context, s *server, id, fingerprint string, confirmPlayers bool, actor session) error {
	fail := func(err error) error {
		_, _ = s.operations.transition(id, operationNeedsAttention, "interrupted", "Minecraft removal stopped before completion. Continue removal to finish removing the remaining servers.")
		return err
	}
	op, err := s.operations.get(id)
	if err != nil {
		return err
	}
	if op.OperationType != operationTypeMinecraftReset || op.InstanceID != "" || op.PlanFingerprint != fingerprint {
		return fail(errInstanceRecovery)
	}
	if _, err = s.operations.transition(id, operationValidating, "revalidating", "Revalidating Minecraft removal."); err != nil {
		return err
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		return fail(err)
	}
	defer lock.Close()
	ctx = withOperationTrace(ctx, s.operations, id)
	b := instanceRuntimes
	p, err := b.planApplianceRetirement(ctx, "reset_all")
	if err != nil || p.PlanFingerprint != fingerprint || (p.TotalPlayersOnline > 0 && !confirmPlayers) {
		return fail(errInstanceRecovery)
	}
	if _, err = s.operations.transition(id, operationRunning, "removing_servers", "Removing all configured Minecraft servers."); err != nil {
		return err
	}
	journals, err := b.applianceRetirementJournals("reset_all")
	if err != nil {
		return fail(err)
	}
	ordered := append([]instanceDeleteJournal{}, p.entries...)
	sort.SliceStable(ordered, func(i, j int) bool { _, a := journals[ordered[i].ID]; _, z := journals[ordered[j].ID]; return a && !z })
	for _, j := range ordered {
		if _, retry := journals[j.ID]; !retry {
			if err = b.persistResetAllIntent(ctx, j); err != nil {
				return fail(err)
			}
		}
		if j.Stage == "intent" {
			observed, e := b.observeRaw(ctx, j.ID)
			if e != nil {
				return fail(e)
			}
			if observed.State == "running" || observed.State == "failed" {
				if err = b.stopResetAllParticipant(ctx, j, confirmPlayers); err != nil {
					return fail(err)
				}
			}
		}
		if err = b.resumeStartOverRetirement(ctx, j); err != nil {
			return fail(err)
		}
	}
	if _, err = s.operations.transition(id, operationVerifying, "finalizing", "Verifying Minecraft removal."); err != nil {
		return err
	}
	empty, err := b.planApplianceRetirement(ctx, "reset_all")
	if err != nil || len(empty.entries) != 0 {
		return fail(errInstanceRecovery)
	}
	if err = b.finishResetAllIdentities(p); err != nil {
		return fail(err)
	}

	preserved, err := b.startOverPreservationEvidence(p.entries, p.legacyBackupPath)
	if err != nil || !reflect.DeepEqual(preserved, p.preserved) {
		return fail(errInstanceRecovery)
	}
	for _, name := range []string{"justvoxel.conf", "minecraft-backup.env"} {
		if err = b.removePrivate(filepath.Join(startOverPolicyRoot, name)); err != nil {
			return fail(err)
		}
		if _, err = os.Lstat(filepath.Join(startOverPolicyRoot, name)); !errors.Is(err, os.ErrNotExist) {
			return fail(errInstanceRecovery)
		}
	}
	preserved, err = b.startOverPreservationEvidence(p.entries, p.legacyBackupPath)
	if err != nil || !reflect.DeepEqual(preserved, p.preserved) {
		return fail(errInstanceRecovery)
	}
	if _, err = s.operations.transition(id, operationSucceeded, "complete", "All Minecraft servers removed. Minecraft is ready for first setup."); err != nil {
		return err
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "reset_minecraft", "minecraft_instances", true, "All Minecraft servers retired; backups, authentication and storage preserved")
	}
	return nil
}

// Match Delete Server's existing registry serialization. Intent prevents a
// subsequent rename while the graceful stop runs outside the registry lock.
func (b *instanceRuntimeBackend) persistResetAllIntent(ctx context.Context, j instanceDeleteJournal) error {
	registry, err := b.identities().openDirectory(false)
	if err != nil {
		return err
	}
	defer registry.Close()
	if syscall.Flock(int(registry.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errInstanceBusy
	}
	defer syscall.Flock(int(registry.Fd()), syscall.LOCK_UN)
	names, err := b.identities().readNames()
	if err != nil || names[j.ID].DisplayName != j.Name {
		return errInstanceRecovery
	}
	record, err := b.readRecord(j.ID)
	if err != nil || !reflect.DeepEqual(record, j.Record) {
		return errInstanceRecovery
	}
	if err = b.verifyLive(record); err != nil {
		return err
	}
	if err = b.checkRetirementDataPolicy(ctx, j); err != nil {
		return err
	}
	if err = b.validateRetirementStorage(ctx, j); err != nil {
		return err
	}
	if f, e := b.privateDirectory(b.staging, true); e != nil {
		return e
	} else {
		f.Close()
	}
	return b.writeJSON(b.deletePath(j.ID), j, false)
}

func (b *instanceRuntimeBackend) finishResetAllIdentities(p startOverPlan) error {
	registry, err := b.identities().openDirectory(false)
	if errors.Is(err, os.ErrNotExist) && p.legacyIdentity == "" && len(p.retirementNames) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	defer registry.Close()
	if syscall.Flock(int(registry.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errInstanceBusy
	}
	defer syscall.Flock(int(registry.Fd()), syscall.LOCK_UN)
	legacy, err := b.identities().read()
	if err != nil || legacy.ID != p.legacyIdentity {
		return errInstanceRecovery
	}
	registered, names, err := b.identities().registeredInstances("")
	if err != nil {
		return err
	}
	for _, instance := range registered {
		if instance.ID != legacy.ID {
			return errInstanceRecovery
		}
	}
	for nameID, record := range names {
		if reviewed, ok := p.retirementNames[nameID]; !ok || !reflect.DeepEqual(reviewed, record) {
			return errInstanceRecovery
		}
	}
	// Fixed runtime coverage has already proved this legacy identity inactive.
	if legacy.ID != "" {
		if err = b.removePrivate(filepath.Join(b.identities().directory, "minecraft.json")); err != nil {
			return err
		}
	}
	for nameID := range names {
		if err = b.removePrivate(filepath.Join(b.identities().directory, "names", nameID+".json")); err != nil {
			return err
		}
	}
	registered, names, err = b.identities().registeredInstances("")
	if err != nil || len(registered) != 0 || len(names) != 0 {
		return errInstanceRecovery
	}
	return nil
}

// Include the policy and reviewed bounded actions in addition to the shared
// ownership/configuration fingerprint. No runtime secret is returned to callers.
func resetAllFingerprint(base string, entries []instanceDeleteJournal, names map[string]minecraftInstanceNameRecord, legacy string) (string, error) {
	raw, err := json.Marshal(struct {
		Policy                                                                    string
		Base                                                                      string
		LegacyIdentity                                                            string
		Entries                                                                   []instanceDeleteJournal
		Names                                                                     map[string]minecraftInstanceNameRecord
		BackupAction, StorageLayoutAction, AuthenticationAction, WebUIUsersAction string
	}{"reset_all", base, legacy, entries, names, "preserve", "preserve", "preserve", "preserve"})
	if err != nil {
		return "", err
	}
	return "sha256:" + instanceHash(raw), nil
}
