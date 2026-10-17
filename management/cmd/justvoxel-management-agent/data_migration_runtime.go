package main

// Private, root-only physical-migration protocol. Inventory, rendering, storage
// validation and lifecycle are the same authority used by the instance API.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type dataMigrationParticipant struct {
	ID            string `json:"id"`
	Unit          string `json:"unit"`
	Container     string `json:"container"`
	State         string `json:"state"`
	Configuration string `json:"configuration"`
}
type dataMigrationRuntimeEntry struct {
	Participant       dataMigrationParticipant `json:"participant"`
	Before            instanceRuntimeRecord    `json:"before"`
	Environment       []byte                   `json:"environment"`
	Quadlet           []byte                   `json:"quadlet"`
	Target            *instanceRuntimeRecord   `json:"target,omitempty"`
	TargetEnvironment []byte                   `json:"target_environment,omitempty"`
	TargetQuadlet     []byte                   `json:"target_quadlet,omitempty"`
}
type dataMigrationRuntimeJournal struct {
	Schema      int                         `json:"schema"`
	OperationID string                      `json:"operation_id"`
	Phase       string                      `json:"phase"`
	Entries     []dataMigrationRuntimeEntry `json:"entries"`
}

var dataMigrationRecoveryRoot = "/var/lib/justvoxel/management"
var dataMigrationPermitRoot = "/run/justvoxel"

func validateDataMigrationParticipants(ps []dataMigrationParticipant) error {
	if len(ps) == 0 || len(ps) > 1024 {
		return instanceRuntimeFailure("Physical migration requires at least one verified managed instance.")
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if !instanceIDPattern.MatchString(p.ID) || seen[p.ID] || p.Unit != "minecraft-"+p.ID+".service" || p.Container != "minecraft-"+p.ID || (p.State != "running" && p.State != "stopped") || !instanceSHA256Pattern.MatchString(p.Configuration) {
			return errInstanceRecovery
		}
		seen[p.ID] = true
	}
	return nil
}
func migrationRecordHash(r instanceRuntimeRecord) string {
	data, _ := json.Marshal(r)
	return instanceHash(data)
}

// minecraft.json reserves the historical fixed-runtime identity. It is not
// an independent runtime record (the authoritative renderer forbids adopting
// it). Keep that compatibility record for shared backup helpers; never operate
// its unit. The separate fixed-runtime inspection below must prove inactivity.
func (b *instanceRuntimeBackend) migrationRegisteredInstances() ([]minecraftRegisteredInstance, error) {
	ids, err := b.identities().listInstances("")
	if err != nil {
		return nil, err
	}
	legacy, err := b.identities().readRecord("minecraft.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	managed := []minecraftRegisteredInstance{}
	for _, id := range ids {
		if id.ID != legacy {
			managed = append(managed, id)
		}
	}
	return managed, nil
}

func (b *instanceRuntimeBackend) migrationParticipants(ctx context.Context) ([]dataMigrationParticipant, int, error) {
	// listInstances validates duplicate/legacy identity records. records validates
	// runtime coverage; the exact sets must agree, including stopped siblings.
	ids, err := b.migrationRegisteredInstances()
	if err != nil {
		return nil, 0, err
	}
	records, err := b.records()
	if err != nil {
		return nil, 0, err
	}
	if len(ids) != len(records) || len(ids) == 0 {
		return nil, 0, instanceRuntimeFailure("Every registered instance must have an authoritative runtime; empty appliances cannot migrate.")
	}
	snapshot, _, _, err := b.snapshot(ctx)
	if err != nil {
		return nil, 0, err
	}
	if _, err = b.render(snapshot, records); err != nil {
		return nil, 0, err
	} // also refuses legacy fixed-runtime identities
	if err = b.requireNoInstanceRestore(); err != nil {
		return nil, 0, err
	}
	if err = b.migrationFixedRuntimeInactive(ctx); err != nil {
		return nil, 0, err
	}
	ps := []dataMigrationParticipant{}
	online := 0
	for _, r := range records {
		if _, e := b.readPrivate(b.migrationMarker(r.Runtime.ID)); !errors.Is(e, os.ErrNotExist) {
			return nil, 0, errInstanceRecovery
		}
		state, e := b.observe(ctx, r.Runtime.ID)
		if e != nil {
			return nil, 0, e
		}
		if state.State != "running" && state.State != "stopped" {
			return nil, 0, errInstanceRecovery
		}
		if _, e = b.backupStorage(ctx, "preflight", r.Runtime.ID); e != nil {
			return nil, 0, e
		}
		if state.State == "running" {
			p, e := b.players(ctx, r.Runtime.ID)
			if e != nil {
				return nil, 0, e
			}
			online += p.Online
		}
		ps = append(ps, dataMigrationParticipant{r.Runtime.ID, "minecraft-" + r.Runtime.ID + ".service", "minecraft-" + r.Runtime.ID, state.State, migrationRecordHash(r)})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	return ps, online, validateDataMigrationParticipants(ps)
}
func (b *instanceRuntimeBackend) migrationFixedRuntimeInactive(ctx context.Context) error {
	// Inspection only: never stop, start, adopt, or fall back to a fixed runtime.
	fixed, err := b.command(ctx, "systemctl", "show", "--property=ActiveState", "--value", "--", "minecraft.service")
	if err != nil || strings.TrimSpace(string(fixed)) != "inactive" {
		return errInstanceRecovery
	}
	fixed, err = b.command(ctx, "podman", "ps", "--all", "--filter", "name=^minecraft$", "--format", "{{.Names}}")
	if err != nil || strings.TrimSpace(string(fixed)) != "" {
		return errInstanceRecovery
	}
	return nil
}

func (b *instanceRuntimeBackend) migrationJournalPath(op string) string {
	return filepath.Join(dataMigrationRecoveryRoot, "data-migration-"+op, "runtime.json")
}
func (b *instanceRuntimeBackend) migrationMarker(id string) string {
	return filepath.Join(b.root, id, "data-migration.json")
}
func (b *instanceRuntimeBackend) saveMigration(j dataMigrationRuntimeJournal) error {
	return b.writeJSON(b.migrationJournalPath(j.OperationID), j, true)
}
func (b *instanceRuntimeBackend) loadMigration(op string) (dataMigrationRuntimeJournal, error) {
	var j dataMigrationRuntimeJournal
	raw, err := b.readPrivate(b.migrationJournalPath(op))
	if err != nil {
		return j, err
	}
	if instanceDecode(raw, &j) != nil || j.Schema != 1 || j.OperationID != op {
		return j, errInstanceRecovery
	}
	ps := []dataMigrationParticipant{}
	for _, e := range j.Entries {
		p := e.Participant
		ps = append(ps, p)
		if e.Before.Runtime.ID != p.ID || migrationRecordHash(e.Before) != p.Configuration || instanceHash(e.Environment) != e.Before.EnvironmentSHA256 || instanceHash(e.Quadlet) != e.Before.QuadletSHA256 {
			return j, errInstanceRecovery
		}
		if e.Target != nil {
			expected := e.Before
			expected.Storage = e.Target.Storage
			expected.EnvironmentSHA256 = e.Target.EnvironmentSHA256
			expected.QuadletSHA256 = e.Target.QuadletSHA256
			if !reflect.DeepEqual(expected, *e.Target) || e.Target.Storage.DataPath != filepath.Join(e.Target.Storage.Root, "minecraft-instances", p.ID, "data") || instanceHash(e.TargetEnvironment) != e.Target.EnvironmentSHA256 || instanceHash(e.TargetQuadlet) != e.Target.QuadletSHA256 {
				return j, errInstanceRecovery
			}
		}
	}
	return j, validateDataMigrationParticipants(ps)
}
func (b *instanceRuntimeBackend) checkMigrationSet(j dataMigrationRuntimeJournal) error {
	ids, err := b.migrationRegisteredInstances()
	if err != nil {
		return err
	}
	if len(ids) != len(j.Entries) {
		return errInstanceRecovery
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id.ID] = true
	}
	for _, e := range j.Entries {
		markerRaw, markerErr := b.readPrivate(b.migrationMarker(e.Participant.ID))
		if !errors.Is(markerErr, os.ErrNotExist) {
			var marker struct {
				Schema      int    `json:"schema"`
				ID          string `json:"id"`
				OperationID string `json:"operation_id"`
			}
			if markerErr != nil || instanceDecode(markerRaw, &marker) != nil || marker.Schema != 1 || marker.ID != e.Participant.ID || marker.OperationID != j.OperationID {
				return errInstanceRecovery
			}
		}
		if !seen[e.Participant.ID] {
			return errInstanceRecovery
		}
		// During publication interruption each file may be either preserved version,
		// but nothing outside the exact journal-owned old/target bytes is accepted.
		_, env, q := b.paths(e.Participant.ID)
		for _, v := range []struct {
			path          string
			before, after []byte
		}{{env, e.Environment, e.TargetEnvironment}, {q, e.Quadlet, e.TargetQuadlet}} {
			raw, err := b.readPrivate(v.path)
			if err != nil || (!reflect.DeepEqual(raw, v.before) && !reflect.DeepEqual(raw, v.after)) {
				return errInstanceRecovery
			}
		}
		r, err := b.readRecord(e.Participant.ID)
		if err != nil || (!reflect.DeepEqual(r, e.Before) && (e.Target == nil || !reflect.DeepEqual(r, *e.Target))) {
			return errInstanceRecovery
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) captureMigration(ctx context.Context, op string, reviewed []dataMigrationParticipant) error {
	if validateDataMigrationParticipants(reviewed) != nil {
		return errInstanceRecovery
	}
	live, _, err := b.migrationParticipants(ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(live, reviewed) {
		return instanceRuntimeFailure("Migration participants changed after Review.")
	}
	j := dataMigrationRuntimeJournal{Schema: 1, OperationID: op, Phase: "captured"}
	for _, p := range live {
		r, err := b.readRecord(p.ID)
		if err != nil {
			return err
		}
		_, env, q := b.paths(p.ID)
		eb, err := b.readPrivate(env)
		if err != nil {
			return err
		}
		qb, err := b.readPrivate(q)
		if err != nil {
			return err
		}
		j.Entries = append(j.Entries, dataMigrationRuntimeEntry{Participant: p, Before: r, Environment: eb, Quadlet: qb})
	}
	encoded, err := json.Marshal(j)
	if err != nil || len(encoded) > 1<<20 {
		return errInstanceRecovery
	}
	if err = b.writeJSON(b.migrationJournalPath(op), j, false); err != nil {
		return err
	}
	// Durable identity and original state precede every shutdown and copy.
	for _, e := range j.Entries {
		if err = b.writeJSON(b.migrationMarker(e.Participant.ID), map[string]any{"schema": 1, "operation_id": op, "id": e.Participant.ID}, false); err != nil {
			return err
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) quiesceMigration(ctx context.Context, j dataMigrationRuntimeJournal, confirmed bool) error {
	if err := b.migrationFixedRuntimeInactive(ctx); err != nil {
		return err
	}
	if err := b.checkMigrationSet(j); err != nil {
		return err
	}
	for _, e := range j.Entries {
		s, err := b.observeRaw(ctx, e.Participant.ID)
		if err != nil {
			return err
		}
		if s.State == "running" {
			if err = b.actionWithWarnings(ctx, e.Participant.ID, "stop", confirmed); err != nil {
				return err
			}
		} else if s.State != "stopped" {
			return errInstanceRecovery
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) relocateMigration(ctx context.Context, j *dataMigrationRuntimeJournal) error {
	if err := b.migrationFixedRuntimeInactive(ctx); err != nil {
		return err
	}
	if err := b.checkMigrationSet(*j); err != nil {
		return err
	}
	snapshot, _, _, err := b.snapshot(ctx)
	if err != nil {
		return err
	}
	records := []instanceRuntimeRecord{}
	for _, e := range j.Entries {
		s, err := b.observeRaw(ctx, e.Participant.ID)
		if err != nil || s.State != "stopped" {
			return errInstanceRecovery
		}
		r := e.Before
		r.Storage = instanceStorageFor(snapshot, r.Runtime.ID)
		records = append(records, r)
		if err = b.storage(ctx, "validate", r); err != nil {
			return err
		}
	}
	files, err := b.render(snapshot, records)
	if err != nil {
		return err
	}
	stage, err := b.privateDirectory(b.staging, true)
	if err != nil {
		return err
	}
	stage.Close()
	configs := make([]minecraftInstanceRuntimeConfig, 0, len(records))
	for _, r := range records {
		configs = append(configs, r.Runtime)
	}
	bundle := "data-migration-" + j.OperationID
	if err = b.identities().stageInstanceRuntimes(snapshot, configs, b.staging, bundle); err != nil {
		return err
	}
	if err = b.validateBundle(ctx, filepath.Join(b.staging, bundle), files); err != nil {
		return err
	}
	for i := range j.Entries {
		e := &j.Entries[i]
		for _, f := range files {
			if f.ID == e.Participant.ID {
				r := e.Before
				r.Storage = instanceStorageFor(snapshot, r.Runtime.ID)
				r.EnvironmentSHA256 = instanceHash(f.Environment)
				r.QuadletSHA256 = instanceHash(f.Quadlet)
				e.Target = &r
				e.TargetEnvironment = f.Environment
				e.TargetQuadlet = f.Quadlet
			}
		}
		if e.Target == nil {
			return errInstanceRecovery
		}
	}
	j.Phase = "publishing"
	encoded, err := json.Marshal(j)
	if err != nil || len(encoded) > 1<<20 {
		return errInstanceRecovery
	}
	if err = b.saveMigration(*j); err != nil {
		return err
	}
	return b.publishMigration(ctx, *j, false)
}
func (b *instanceRuntimeBackend) publishMigration(ctx context.Context, j dataMigrationRuntimeJournal, rollback bool) error {
	if err := b.migrationFixedRuntimeInactive(ctx); err != nil {
		return err
	}
	if err := b.checkMigrationSet(j); err != nil {
		return err
	}
	for _, e := range j.Entries {
		s, err := b.observeRaw(ctx, e.Participant.ID)
		if err != nil || s.State != "stopped" {
			return errInstanceRecovery
		}
		r := e.Target
		env, q := e.TargetEnvironment, e.TargetQuadlet
		if rollback {
			r = &e.Before
			env = e.Environment
			q = e.Quadlet
		}
		if r == nil {
			return errInstanceRecovery
		}
		if err = b.storage(ctx, "validate", *r); err != nil {
			return err
		}
		dir, ep, qp := b.paths(e.Participant.ID)
		if err = b.writePrivate(ep, env, true); err != nil {
			return err
		}
		if err = b.writePrivate(qp, q, true); err != nil {
			return err
		}
		if err = b.writeJSON(filepath.Join(dir, "config.json"), r, true); err != nil {
			return err
		}
	}
	_, err := b.command(ctx, "systemctl", "daemon-reload")
	return err
}
func (b *instanceRuntimeBackend) restoreMigrationStates(ctx context.Context, j dataMigrationRuntimeJournal) (result error) {
	if err := b.migrationFixedRuntimeInactive(ctx); err != nil {
		return err
	}
	if err := b.checkMigrationSet(j); err != nil {
		return err
	}
	for _, e := range j.Entries {
		id := e.Participant.ID
		r, err := b.readRecord(id)
		if err != nil {
			return err
		}
		if err = b.verifyLive(r); err != nil {
			return err
		}
		if err = b.storage(ctx, "validate", r); err != nil {
			return err
		}
		s, err := b.observeRaw(ctx, id)
		if err != nil {
			return err
		}
		if e.Participant.State == "stopped" {
			if s.State != "stopped" {
				return errInstanceRecovery
			}
			continue
		}
		proc, err := os.ReadFile("/proc/self/stat")
		if err != nil {
			return err
		}
		fields := strings.Fields(string(proc)[strings.LastIndex(string(proc), ")")+1:])
		if len(fields) < 20 {
			return errInstanceRecovery
		}
		permit := filepath.Join(dataMigrationPermitRoot, "data-migration-start-"+id+".json")
		if err = b.writeJSON(permit, map[string]any{"id": id, "operation_id": j.OperationID, "agent_pid": os.Getpid(), "agent_start": fields[19]}, true); err != nil {
			return err
		}
		// Shell releases maintenance only for this verified startup; registry and
		// restore-exclusion stay held, as does the Agent's durable operation claim.
		_, startErr := b.command(ctx, "systemctl", "start", "--", e.Participant.Unit)
		if startErr == nil {
			_, startErr = b.command(ctx, "env", "JUSTVOXEL_MAINTENANCE_LOCK_HELD=1", "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", "--instance", id, r.Runtime.Version)
		}
		removeErr := b.removePrivate(permit)
		if err = errors.Join(startErr, removeErr); err != nil {
			return err
		}
		s, err = b.observe(ctx, id)
		if err != nil || s.State != "running" {
			return errInstanceRecovery
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) finishMigration(j dataMigrationRuntimeJournal) error {
	// Keep the journal until every startup gate has been removed. A retry can
	// finish an interrupted cleanup without losing the immutable participant set.
	for _, e := range j.Entries {
		raw, err := b.readPrivate(b.migrationMarker(e.Participant.ID))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var marker struct {
			Schema      int    `json:"schema"`
			OperationID string `json:"operation_id"`
			ID          string `json:"id"`
		}
		if instanceDecode(raw, &marker) != nil || marker.Schema != 1 || marker.OperationID != j.OperationID || marker.ID != e.Participant.ID {
			return errInstanceRecovery
		}
		if err = b.removePrivate(b.migrationMarker(e.Participant.ID)); err != nil {
			return err
		}
	}
	return nil
}
func dataMigrationRuntimeCommand(ctx context.Context, args []string) error {
	if os.Geteuid() != 0 || len(args) == 0 {
		return errInstanceID
	}
	b := instanceRuntimes
	if args[0] == "plan" && len(args) == 1 {
		ps, online, err := b.migrationParticipants(ctx)
		if err != nil {
			return err
		}
		state := "stopped"
		for _, p := range ps {
			if p.State == "running" {
				state = "running"
			}
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"participants": ps, "state": state, "online": online, "names": []string{}})
	}
	if len(args) != 2 || !operationIDPattern.MatchString(args[1]) {
		return errInstanceID
	}
	op := args[1]
	if args[0] == "capture" {
		var ps []dataMigrationParticipant
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return err
		}
		if instanceDecode(raw, &ps) != nil {
			return errInstanceRecovery
		}
		return b.captureMigration(ctx, op, ps)
	}
	j, err := b.loadMigration(op)
	if err != nil {
		return instanceRuntimeFailure("Migration has no verified participant evidence; legacy state is read-only.")
	}
	if err = b.checkMigrationSet(j); err != nil {
		return err
	}
	for _, e := range j.Entries {
		cleanup, err := b.migrationPermit(j.OperationID, e.Participant.ID)
		if err != nil {
			return err
		}
		defer cleanup()
	}
	switch args[0] {
	case "quiesce":
		for _, e := range j.Entries {
			s, err := b.observeRaw(ctx, e.Participant.ID)
			if err != nil || s.State != e.Participant.State {
				return errInstanceRecovery
			}
		}
		return b.quiesceMigration(ctx, j, os.Getenv("JV_MIGRATION_PLAYERS_CONFIRMED") == "true")
	case "backup":
		if err = b.checkMigrationSet(j); err != nil {
			return err
		}
		for _, e := range j.Entries {
			if err = instanceMaintenanceColdBackup(ctx, e.Participant.ID); err != nil {
				return err
			}
		}
		return nil
	case "relocate":
		return b.relocateMigration(ctx, &j)
	case "quiesce-recovery":
		return b.quiesceMigration(ctx, j, os.Getenv("JV_MIGRATION_PLAYERS_CONFIRMED") == "true")
	case "rollback":
		return b.publishMigration(ctx, j, true)
	case "restore":
		return b.restoreMigrationStates(ctx, j)
	case "finish":
		return b.finishMigration(j)
	default:
		return fmt.Errorf("unsupported migration runtime phase")
	}
}

func (b *instanceRuntimeBackend) migrationPermit(op, id string) (func(), error) {
	proc, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(proc)[strings.LastIndex(string(proc), ")")+1:])
	if len(fields) < 20 {
		return nil, errInstanceRecovery
	}
	path := filepath.Join(dataMigrationPermitRoot, "data-migration-start-"+id+".json")
	if err = b.writeJSON(path, map[string]any{"id": id, "operation_id": op, "agent_pid": os.Getpid(), "agent_start": fields[19]}, true); err != nil {
		return nil, err
	}
	return func() { _ = b.removePrivate(path) }, nil
}
