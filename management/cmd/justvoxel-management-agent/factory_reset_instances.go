package main

// Factory Reset owns all managed Minecraft instances. This private evidence is
// never returned through an API: it includes runtime credentials and pinned data
// identities. The public review binds its digest, not its contents.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type factoryResetInstance struct {
	Retirement instanceDeleteJournal `json:"retirement"`
	State      string                `json:"state"`
	Scope      string                `json:"scope"`
	Names      []string              `json:"-"`
}
type factoryResetInstancesJournal struct {
	Schema      int                    `json:"schema"`
	OperationID string                 `json:"operation_id"`
	Entries     []factoryResetInstance `json:"entries"`
}

func factoryResetEvidencePath(s *server, op string) string {
	return filepath.Join(s.operations.baseDir, "factory-reset-"+op+".json")
}
func (b *instanceRuntimeBackend) factoryResetMarker(id string) string {
	dir, _, _ := b.paths(id)
	return filepath.Join(dir, "factory-reset.json")
}
func (b *instanceRuntimeBackend) factoryResetScope(ctx context.Context, path string) (string, error) {
	out, err := b.command(ctx, "nsenter", "--mount=/proc/1/ns/mnt", "--", "bash", "-c", `source /usr/libexec/justvoxel/mjust/minecraft-reset-common.sh; jv_reset_path_scope "$1"`, "factory-reset-scope", path)
	scope := strings.TrimSpace(string(out))
	if err != nil || (scope != "internal" && scope != "external" && scope != "network" && scope != "unknown") {
		return "", errInstanceRecovery
	}
	return scope, nil
}

// Reject orphan runtime artifacts rather than guessing their owners. A legacy
// fixed runtime is inspected only to refuse it, never operated or adopted.
func (b *instanceRuntimeBackend) factoryResetCoverage(ctx context.Context, entries []factoryResetInstance) error {
	if err := b.migrationFixedRuntimeInactive(ctx); err != nil {
		return err
	}
	// Historical backup workers must already be inactive. Their files may be
	// removed as compatibility cleanup, but cannot stand in for instance jobs.
	for _, unit := range []string{"minecraft-backup.service", "minecraft-backup.timer"} {
		out, err := b.command(ctx, "systemctl", "show", "--property=ActiveState", "--value", "--", unit)
		if err != nil || strings.TrimSpace(string(out)) != "inactive" {
			return errInstanceRecovery
		}
	}
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.Retirement.ID] = true
	}
	for _, query := range []struct {
		command        string
		args           []string
		prefix, suffix string
	}{
		{"podman", []string{"ps", "--all", "--filter", "name=^minecraft-jv-", "--format", "{{.Names}}"}, "minecraft-", ""},
		{"systemctl", []string{"list-units", "--state=active,activating,deactivating,failed", "--plain", "--no-legend", "--no-pager", "minecraft-jv-*.service"}, "minecraft-", ".service"},
		{"systemctl", []string{"list-units", "--state=active,activating,deactivating", "--plain", "--no-legend", "--no-pager", "minecraft-backup-jv-*.service"}, "minecraft-backup-", ".service"},
		// A loaded timer can outlive its unit file. Include it independently of
		// the on-disk inventory so an orphan cannot survive empty-runtime proof.
		{"systemctl", []string{"list-units", "--state=active,activating,deactivating,failed", "--plain", "--no-legend", "--no-pager", "minecraft-backup-jv-*.timer"}, "minecraft-backup-", ".timer"},
	} {
		out, err := b.command(ctx, query.command, query.args...)
		if err != nil {
			return errInstanceRecovery
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			name := fields[0]
			id := strings.TrimSuffix(strings.TrimPrefix(name, query.prefix), query.suffix)
			if !ids[id] || seen[id] || name != query.prefix+id+query.suffix {
				return errInstanceRecovery
			}
			seen[id] = true
		}
	}
	for _, area := range []struct{ path, prefix, suffix string }{
		{b.quadlets, "minecraft-jv-", ".container"}, {instanceBackupUnits, "minecraft-backup-jv-", ""},
	} {
		files, err := os.ReadDir(area.path)
		if errors.Is(err, os.ErrNotExist) && area.path == instanceBackupUnits {
			continue
		}
		if err != nil {
			return err
		}
		for _, f := range files {
			if !strings.HasPrefix(f.Name(), area.prefix) {
				continue
			}
			id := strings.TrimPrefix(f.Name(), "minecraft-")
			if area.path == instanceBackupUnits {
				id = strings.TrimPrefix(f.Name(), "minecraft-backup-")
				if !strings.HasSuffix(id, ".timer") && !strings.HasSuffix(id, ".service") {
					return errInstanceRecovery
				}
				id = strings.TrimSuffix(strings.TrimSuffix(id, ".timer"), ".service")
				if f.Name() != instanceBackupUnit(id, "timer") && f.Name() != instanceBackupUnit(id, "service") {
					return errInstanceRecovery
				}
			} else {
				id = strings.TrimSuffix(id, area.suffix)
				if f.Name() != "minecraft-"+id+area.suffix {
					return errInstanceRecovery
				}
			}
			if !ids[id] {
				return errInstanceRecovery
			}
			if _, err := b.readPrivate(filepath.Join(area.path, f.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) factoryResetParticipants(ctx context.Context) ([]factoryResetInstance, int, error) {
	ids, err := b.migrationRegisteredInstances()
	if err != nil {
		return nil, 0, err
	}
	records, err := b.records()
	if err != nil {
		return nil, 0, err
	}
	runtimeDirs, dirErr := os.ReadDir(b.root)
	if dirErr != nil && !errors.Is(dirErr, os.ErrNotExist) {
		return nil, 0, dirErr
	}
	if len(runtimeDirs) != len(records) || len(ids) != len(records) || len(records) > 1024 {
		return nil, 0, errInstanceRecovery
	}
	registered := map[string]bool{}
	for _, id := range ids {
		registered[id.ID] = true
	}
	if err = b.requireNoInstanceRestore(); err != nil {
		return nil, 0, err
	}
	entries := []factoryResetInstance{}
	online := 0
	for _, r := range records {
		id := r.Runtime.ID
		if !registered[id] || validateInstanceRecordPlacement(r) != nil {
			return nil, 0, errInstanceRecovery
		}
		if err = b.verifyLive(r); err != nil {
			return nil, 0, err
		}
		if err = b.storage(ctx, "validate", r); err != nil {
			return nil, 0, err
		}
		dir, _, _ := b.paths(id)
		files, e := os.ReadDir(dir)
		if e != nil {
			return nil, 0, e
		}
		for _, f := range files {
			switch f.Name() {
			case "config.json", "minecraft.env", "backup-settings.json", "backup-status.json", "restore-result.json", "backup-restore-result.json", "restore-journal.json":
			default:
				return nil, 0, errInstanceRecovery
			}
			if _, e = b.readPrivate(filepath.Join(dir, f.Name())); e != nil {
				return nil, 0, e
			}
		}
		if e = b.factoryResetBackupIdle(ctx, id); e != nil {
			return nil, 0, e
		}
		status, e := b.backupStatus(id)
		if e != nil || status.State == "running" || status.RestartRequired {
			return nil, 0, errInstanceRecovery
		}
		job, jobErr := b.command(ctx, "systemctl", "show", "--property=Job", "--value", "--", "minecraft-"+id+".service")
		if !systemdJobIdle(job, jobErr) {
			return nil, 0, errInstanceBusy
		}
		state, e := b.observe(ctx, id)
		if e != nil || (state.State != "running" && state.State != "stopped") {
			return nil, 0, errInstanceRecovery
		}
		names := []string{}
		if state.State == "running" {
			players, liveNames, e := b.factoryResetPlayers(ctx, id)
			if e != nil {
				return nil, 0, e
			}
			online += players
			names = liveNames
		}
		scope, e := b.factoryResetScope(ctx, r.Storage.DataPath)
		if e != nil {
			return nil, 0, e
		}
		policy := "preserve"
		if scope == "internal" {
			policy = "delete"
		}
		j := instanceDeleteJournal{Schema: 1, ID: id, Name: "Factory Reset", Record: r, ResetPolicy: policy, Stage: "intent"}
		j.DataIdentity, e = b.deleteStorage(ctx, "inspect", j)
		if e != nil {
			return nil, 0, e
		}
		entries = append(entries, factoryResetInstance{Retirement: j, State: state.State, Scope: scope, Names: names})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Retirement.ID < entries[j].Retirement.ID })
	if !factoryResetEvidenceFits(entries) {
		return nil, 0, errInstanceRecovery
	}
	if err = b.factoryResetCoverage(ctx, entries); err != nil {
		return nil, 0, err
	}
	return entries, online, nil
}
func factoryResetParticipantsHash(entries []factoryResetInstance) string {
	if len(entries) == 0 {
		return ""
	}
	// Stage changes are progress, not a change of destructive scope.
	copy := append([]factoryResetInstance{}, entries...)
	for i := range copy {
		copy[i].Retirement.Stage = "intent"
	}
	raw, _ := json.Marshal(copy)
	return instanceHash(raw)
}
func loadFactoryResetEvidence(s *server, op string) (factoryResetInstancesJournal, error) {
	var j factoryResetInstancesJournal
	raw, err := instanceRuntimes.readPrivate(factoryResetEvidencePath(s, op))
	if err != nil {
		return j, err
	}
	if instanceDecode(raw, &j) != nil || j.Schema != 1 || j.OperationID != op || j.Entries == nil || len(j.Entries) > 1024 {
		return j, errInstanceRecovery
	}
	seen := map[string]bool{}
	for _, e := range j.Entries {
		r := e.Retirement
		if !instanceIDPattern.MatchString(r.ID) || seen[r.ID] || r.Schema != 1 || r.Record.Runtime.ID != r.ID || !regexp.MustCompile(`^[0-9]+:[0-9]+$`).MatchString(r.DataIdentity) || r.Record.SchemaVersion != 1 || !instanceRuntimeSecretPattern.MatchString(r.Record.Runtime.RCONPassword) || r.Record.Storage.DataPath != filepath.Join(r.Record.Storage.Root, "minecraft-instances", r.ID, "data") || !instanceSHA256Pattern.MatchString(r.Record.EnvironmentSHA256) || !instanceSHA256Pattern.MatchString(r.Record.QuadletSHA256) || !validFactoryResetStorageAction(e.Scope, r.ResetPolicy) || (e.State != "running" && e.State != "stopped") {
			return j, errInstanceRecovery
		}
		seen[r.ID] = true
		switch r.Stage {
		case "intent", "runtime_retired", "data_removed", "identity_retired":
		default:
			return j, errInstanceRecovery
		}
	}
	return j, nil
}

// Recovery accepts only the original immutable participants and exact surviving
// committed bytes. Missing files are permitted only at their retirement stage.
func (b *instanceRuntimeBackend) checkFactoryResetEvidence(ctx context.Context, j factoryResetInstancesJournal) error {
	if err := b.factoryResetCoverage(ctx, j.Entries); err != nil {
		return err
	}
	ids, err := b.migrationRegisteredInstances()
	if err != nil {
		return err
	}
	expected := map[string]factoryResetInstance{}
	for _, e := range j.Entries {
		expected[e.Retirement.ID] = e
	}
	for _, id := range ids {
		if _, ok := expected[id.ID]; !ok {
			return errInstanceRecovery
		}
	}
	dirs, err := os.ReadDir(b.root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, d := range dirs {
		if _, ok := expected[d.Name()]; !ok || !d.IsDir() {
			return errInstanceRecovery
		}
	}
	registered := map[string]bool{}
	for _, id := range ids {
		registered[id.ID] = true
	}
	for _, e := range j.Entries {
		r := e.Retirement
		id := r.ID
		late := r.Stage == "data_removed" || r.Stage == "identity_retired"
		if !registered[id] && !late {
			return errInstanceRecovery
		}
		dir, env, q := b.paths(id)
		files, er := os.ReadDir(dir)
		if er != nil && !(late && errors.Is(er, os.ErrNotExist)) {
			return errInstanceRecovery
		}
		for _, file := range files {
			switch file.Name() {
			case "config.json", "minecraft.env", "backup-settings.json", "backup-status.json", "restore-result.json", "backup-restore-result.json", "restore-journal.json", "factory-reset.json":
			default:
				return errInstanceRecovery
			}
			if _, er = b.readPrivate(filepath.Join(dir, file.Name())); er != nil {
				return errInstanceRecovery
			}
		}
		live, er := b.readRecord(id)
		if er == nil {
			if !reflect.DeepEqual(live, r.Record) {
				return errInstanceRecovery
			}
		} else if !late || (!errors.Is(er, os.ErrNotExist) && !errors.Is(er, errInstanceNotFound)) {
			return errInstanceRecovery
		}
		for _, f := range []struct{ path, hash string }{{env, r.Record.EnvironmentSHA256}, {q, r.Record.QuadletSHA256}} {
			raw, er := b.readPrivate(f.path)
			if errors.Is(er, os.ErrNotExist) {
				continue
			}
			if er != nil || instanceHash(raw) != f.hash {
				return errInstanceRecovery
			}
		}
		marker, er := b.readPrivate(b.factoryResetMarker(id))
		if er == nil {
			var m struct {
				OperationID string `json:"operation_id"`
				ID          string `json:"id"`
			}
			if instanceDecode(marker, &m) != nil || m.ID != id || m.OperationID != j.OperationID {
				return errInstanceRecovery
			}
		} else if !errors.Is(er, os.ErrNotExist) {
			return errInstanceRecovery
		}
		if !late {
			if validateInstanceRecordPlacement(r.Record) != nil {
				return errInstanceRecovery
			}
			if er = b.factoryResetBackupIdle(ctx, id); er != nil {
				return er
			}
			if r.Stage == "intent" {
				identity, inspectErr := b.deleteStorage(ctx, "inspect", r)
				if inspectErr != nil || identity != r.DataIdentity {
					return errInstanceRecovery
				}
				scope, scopeErr := b.factoryResetScope(ctx, r.Record.Storage.DataPath)
				if scopeErr != nil || scope != e.Scope {
					return errInstanceRecovery
				}
			}
		}
		state, er := b.observeRaw(ctx, id)
		if er != nil {
			return er
		}
		if state.State != "stopped" && state.State != "configured" && !(r.Stage == "intent" && state.State == "running") {
			return errInstanceRecovery
		}
		if r.Stage == "intent" && state.State == "running" {
			if er = b.verifyLive(r.Record); er != nil {
				return er
			}
		}
		if restore, er := b.restoreJournal(id); er == nil {
			if !restoreTerminal(restore.Stage) {
				return errInstanceRecovery
			}
		} else if !errors.Is(er, os.ErrNotExist) {
			return errInstanceRecovery
		}
	}
	return nil
}
func (s *server) factoryResetPlan(ctx context.Context) (adminFactoryResetPlanResponse, int) {
	// A legacy interrupted operation has no ownership evidence. Keep its attention
	// and keep-current-state resolution, but never guess participants for retry.
	var evidence *factoryResetInstancesJournal
	if s.operations != nil {
		current, err := s.operations.currentFactoryReset()
		if err != nil {
			return factoryResetPlanFailure()
		}
		if current != nil {
			j, e := loadFactoryResetEvidence(s, current.OperationID)
			if e == nil {
				evidence = &j
			} else if current.State == operationNeedsAttention || !errors.Is(e, os.ErrNotExist) {
				return factoryResetPlanFailure()
			}
		}
	}
	plan, status := authoritativeAdminFactoryResetPlan(ctx)
	if status != 0 {
		return plan, status
	}
	if !s.factoryResetStorageBoundary(plan) {
		return factoryResetPlanFailure()
	}
	var err error
	if evidence != nil {
		err = instanceRuntimes.checkFactoryResetEvidence(ctx, *evidence)
		plan.Instances = evidence.Entries
	} else {
		var online int
		plan.Instances, online, err = instanceRuntimes.factoryResetParticipants(ctx)
		plan.PlayersOnline += online
		for _, e := range plan.Instances {
			plan.Players = append(plan.Players, e.Names...)
		}
	}
	if err != nil {
		return factoryResetPlanFailure()
	}
	if evidence != nil {
		plan.PlayersOnline = 0
		for _, e := range evidence.Entries {
			state, er := instanceRuntimes.observeRaw(ctx, e.Retirement.ID)
			if er != nil {
				return factoryResetPlanFailure()
			}
			if state.State == "running" {
				online, names, er := instanceRuntimes.factoryResetPlayers(ctx, e.Retirement.ID)
				if er != nil {
					return factoryResetPlanFailure()
				}
				plan.PlayersOnline += online
				plan.Players = append(plan.Players, names...)
			}
		}
	}
	shared := plan
	plan.SharedPlan = &shared
	if len(plan.Instances) > 0 {
		scope := plan.Instances[0].Scope
		for _, e := range plan.Instances {
			if e.Scope != scope {
				return factoryResetPlanFailure()
			}
		}
		plan.MinecraftConfigured = true
		plan.DataScope = scope
		plan.DataAction = plan.Instances[0].Retirement.ResetPolicy
		if plan.DataPath == "" {
			plan.DataPath = "Managed Minecraft instances"
		}
	}
	plan.PlanFingerprint, err = factoryResetPlanFingerprint(plan)
	if err != nil {
		return factoryResetPlanFailure()
	}
	return plan, 0
}
func factoryResetPlanFailure() (adminFactoryResetPlanResponse, int) {
	return adminFactoryResetPlanResponse{Code: "instance_scope_unavailable", Error: "Factory Reset cannot verify all managed Minecraft state. Resolve pending appliance recovery before retrying.", Players: []string{}, Warnings: []string{}}, 409
}
func (s *server) retireFactoryResetInstances(ctx context.Context, op string, plan adminFactoryResetPlanResponse) error {
	b := instanceRuntimes
	j, err := loadFactoryResetEvidence(s, op)
	if errors.Is(err, os.ErrNotExist) {
		j = factoryResetInstancesJournal{Schema: 1, OperationID: op, Entries: plan.Instances}
		if j.Entries == nil {
			j.Entries = []factoryResetInstance{}
		}
		if !factoryResetEvidenceFits(j.Entries) {
			return errInstanceRecovery
		}
		if err = b.writeJSON(factoryResetEvidencePath(s, op), j, false); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if factoryResetParticipantsHash(j.Entries) != factoryResetParticipantsHash(plan.Instances) {
		return errInstanceRecovery
	}
	if err = b.checkFactoryResetEvidence(ctx, j); err != nil {
		return err
	}
	traceStage(ctx, "Stopping managed Minecraft instances")
	// All identities are durable before any timer, shutdown or data mutation.
	for _, e := range j.Entries {
		if e.Retirement.Stage == "intent" {
			if err = b.writeJSON(b.factoryResetMarker(e.Retirement.ID), map[string]string{"id": e.Retirement.ID, "operation_id": op}, true); err != nil {
				return err
			}
		}
	}
	for _, e := range j.Entries {
		if e.Retirement.Stage != "intent" {
			continue
		}
		state, er := b.observeRaw(ctx, e.Retirement.ID)
		if er != nil {
			return er
		}
		if state.State == "running" {
			if er = b.actionWithWarnings(ctx, e.Retirement.ID, "stop", true); er != nil {
				return er
			}
		}
	}
	traceStage(ctx, "Removing managed Minecraft runtime")
	save := func() error { return b.writeJSON(factoryResetEvidencePath(s, op), j, true) }
	for i := range j.Entries {
		r := &j.Entries[i].Retirement
		if r.Stage == "intent" {
			if err = b.retireInstanceRuntime(ctx, *r); err != nil {
				return err
			}
			r.Stage = "runtime_retired"
			if err = save(); err != nil {
				return err
			}
		}
		if r.Stage == "runtime_retired" {
			if err = b.applyInstanceRetirementDataPolicy(ctx, *r); err != nil {
				return err
			}
			r.Stage = "data_removed"
			if err = save(); err != nil {
				return err
			}
		}
		if r.Stage == "data_removed" {
			if err = b.applyInstanceRetirementDataPolicy(ctx, *r); err != nil {
				return err
			}
			if err = b.removePrivate(b.factoryResetMarker(r.ID)); err != nil {
				return err
			}
			if err = b.retireInstanceStateAndIdentity(*r); err != nil {
				return err
			}
			r.Stage = "identity_retired"
			if err = save(); err != nil {
				return err
			}
		}
	}
	return b.factoryResetEmpty(ctx)
}
func (b *instanceRuntimeBackend) factoryResetEmpty(ctx context.Context) error {
	ids, err := b.migrationRegisteredInstances()
	if err != nil || len(ids) != 0 {
		return errInstanceRecovery
	}
	records, err := b.records()
	if err != nil || len(records) != 0 {
		return errInstanceRecovery
	}
	return b.factoryResetCoverage(ctx, nil)
}

func (b *instanceRuntimeBackend) factoryResetBackupIdle(ctx context.Context, id string) error {
	out, err := b.command(ctx, "systemctl", "show", "--property=LoadState,ActiveState,Job", "--", instanceBackupUnit(id, "service"))
	if err != nil {
		return errInstanceRecovery
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
	if !systemdJobIdle([]byte(props["Job"]), nil) {
		return errInstanceBusy
	}
	return nil
}

// Keep-current-state is safe only before retirement, when every committed
// runtime is intact. Never resolve away the sole retry path for partial erasure.
func (s *server) resolveFactoryResetParticipants(ctx context.Context, op string) error {
	j, err := loadFactoryResetEvidence(s, op)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // deployed informational journals
	if err != nil {
		return err
	}
	if err = instanceRuntimes.checkFactoryResetEvidence(ctx, j); err != nil {
		return err
	}
	for _, e := range j.Entries {
		if e.Retirement.Stage != "intent" {
			return errInstanceRecovery
		}
		if err = instanceRuntimes.verifyLive(e.Retirement.Record); err != nil {
			return err
		}
	}
	for _, e := range j.Entries {
		if err = instanceRuntimes.removePrivate(instanceRuntimes.factoryResetMarker(e.Retirement.ID)); err != nil {
			return err
		}
	}
	return nil
}

// Historical data/backup paths remain compatibility cleanup targets, never a
// license to erase a storage root, participant tree, registry or recovery state.
func (s *server) factoryResetStorageBoundary(plan adminFactoryResetPlanResponse) bool {
	protected := []string{"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/run", "/var/lib/justvoxel/management", "/var/lib/justvoxel/webui", instanceRuntimes.root, instanceRuntimes.quadlets, instanceRuntimes.staging, instanceRuntimes.identities().directory}
	if s.operations != nil {
		protected = append(protected, s.operations.baseDir)
	}
	for _, target := range []struct{ path, action string }{{plan.DataPath, plan.DataAction}, {plan.BackupPath, plan.BackupAction}} {
		if target.action != "delete" {
			continue
		}
		if validateInstanceRuntimePath(target.path) != nil {
			return false
		}
		for _, p := range protected {
			if instanceRuntimeInside(target.path, p) || instanceRuntimeInside(p, target.path) {
				return false
			}
		}
	}
	return true
}

func (b *instanceRuntimeBackend) factoryResetPlayers(ctx context.Context, id string) (int, []string, error) {
	raw, err := b.rcon(ctx, id, "list")
	if err != nil {
		return 0, nil, err
	}
	players, err := parseInstancePlayers(id, raw)
	if err != nil {
		return 0, nil, err
	}
	names := []string{}
	if players.Online > 0 {
		parts := instancePlayerListPattern.FindStringSubmatch(strings.TrimSpace(string(raw)))
		names = strings.Split(strings.TrimSpace(parts[3]), ", ")
	}
	return players.Online, names, nil
}

// readPrivate has a 1 MiB bound. Reserve the longest stage for every entry so
// no later progress write can produce evidence that recovery cannot read.
func factoryResetEvidenceFits(entries []factoryResetInstance) bool {
	bounded := append([]factoryResetInstance{}, entries...)
	for i := range bounded {
		bounded[i].Retirement.Stage = "identity_retired"
	}
	raw, err := json.Marshal(factoryResetInstancesJournal{Schema: 1, OperationID: "00000000-0000-4000-8000-000000000000", Entries: bounded})
	return err == nil && len(raw) <= 1<<20
}
