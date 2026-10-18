package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Start Over has no fixed-runtime fallback. Only immutable registered ownership
// or an exact Start Over retirement journal can authorize a participant.
type startOverParticipant struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	State         string   `json:"state"`
	PlayersOnline int      `json:"players_online"`
	Players       []string `json:"players"`
}
type startOverPlan struct {
	OK                           bool                   `json:"ok"`
	SchemaVersion                string                 `json:"schema_version"`
	Mode                         string                 `json:"mode"`
	PlanFingerprint              string                 `json:"plan_fingerprint"`
	Participants                 []startOverParticipant `json:"participants"`
	TotalPlayersOnline           int                    `json:"total_players_online"`
	ConfigurationBackupSupported bool                   `json:"configuration_backup_supported"`
	DataAction                   string                 `json:"data_action"`
	BackupAction                 string                 `json:"backup_action"`
	StorageLayoutAction          string                 `json:"storage_layout_action"`
	AuthenticationAction         string                 `json:"authentication_action"`
	WebUIUsersAction             string                 `json:"webui_users_action"`
	PodmanImagesAction           string                 `json:"podman_images_action"`
	entries                      []instanceDeleteJournal
	legacyBackupPath             string
	preserved                    map[string]string
}
type startOverRequest struct {
	PlanFingerprint         string `json:"plan_fingerprint"`
	ConfirmPlayers          bool   `json:"confirm_players"`
	SaveConfigurationBackup bool   `json:"save_configuration_backup"`
}

var startOverPolicyRoot = "/etc/justvoxel"
var startOverArchiveRoot = "/var/lib/justvoxel/config-backups"

func registerAdminStartOverRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/start-over/plan", s.adminStartOverPlan)
	mux.HandleFunc("POST /v1/admin/start-over/apply", s.adminStartOverApply)
}
func startOverRoot(w http.ResponseWriter, r *http.Request) (session, bool) {
	actor, ok := localRootAdministrator(r)
	if !ok {
		writeError(w, 403, "Start Over requires local root administration")
	}
	return actor, ok
}
func startOverFailure(w http.ResponseWriter, err error) {
	var safe *instanceRuntimeSafeError
	if err != nil && !errors.As(err, &safe) && !errors.Is(err, errInstanceNotFound) {
		writeError(w, 500, "Start Over could not complete a backend check. Review and retry Start Over; retirement evidence has been retained.")
		return
	}
	writeError(w, 409, "Start Over could not verify Minecraft ownership or recovery state. Resolve the pending operation or retry Start Over.")
}
func (s *server) adminStartOverPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := startOverRoot(w, r); !ok {
		return
	}
	release, err := s.claimMinecraftMutation()
	if err != nil {
		startOverFailure(w, err)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	plan, err := instanceRuntimes.planStartOver(ctx)
	if err != nil {
		startOverFailure(w, err)
		return
	}
	writeJSON(w, 200, plan)
}
func (s *server) adminStartOverApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := startOverRoot(w, r)
	if !ok {
		return
	}
	var req *startOverRequest
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil || instanceDecode(raw, &req) != nil || req == nil || !operationFingerprintPattern.MatchString(req.PlanFingerprint) {
		writeError(w, 400, "Invalid Start Over request")
		return
	}
	release, err := s.claimMinecraftMutation()
	if err != nil {
		startOverFailure(w, err)
		return
	}
	defer release()
	// The request remains synchronous; disconnecting leaves per-instance durable
	// evidence, and the next review resumes exactly that participant.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	plan, err := instanceRuntimes.planStartOver(ctx)
	if err != nil {
		startOverFailure(w, err)
		return
	}
	if plan.PlanFingerprint != req.PlanFingerprint {
		writeError(w, 409, "Start Over plan changed; review it again")
		return
	}
	if plan.TotalPlayersOnline > 0 && !req.ConfirmPlayers {
		writeError(w, 409, "Players are online; explicit interruption confirmation is required")
		return
	}
	archive := ""
	if req.SaveConfigurationBackup {
		archive, err = instanceRuntimes.archiveStartOverConfiguration(plan.entries)
		if err != nil {
			writeError(w, 500, "Configuration backup could not be verified. Start Over has not changed Minecraft.")
			return
		}
	}
	// Resume the interrupted participant before beginning another retirement,
	// even when its immutable ID sorts after a remaining registered instance.
	ordered := append([]instanceDeleteJournal{}, plan.entries...)
	journals, err := instanceRuntimes.startOverJournals()
	if err != nil {
		startOverFailure(w, err)
		return
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		_, a := journals[ordered[i].ID]
		_, b := journals[ordered[j].ID]
		return a && !b
	})
	for _, j := range ordered {
		if err = instanceRuntimes.applyInstanceRetirementDataPolicy(ctx, j); err != nil {
			startOverFailure(w, err)
			return
		}
		if _, e := instanceRuntimes.deleteJournal(j.ID); errors.Is(e, os.ErrNotExist) {
			if err = instanceRuntimes.actionWithWarnings(ctx, j.ID, "stop", req.ConfirmPlayers); err != nil {
				startOverFailure(w, err)
				return
			}
		} else if e != nil {
			startOverFailure(w, e)
			return
		}
		if err = instanceRuntimes.retireSelectedInstance(ctx, j.ID, j.Name, "start_over"); err != nil {
			startOverFailure(w, err)
			return
		}
	}
	// Re-prove the empty appliance before removing only the active setup policy.
	empty, err := instanceRuntimes.planStartOver(ctx)
	if err != nil || len(empty.entries) != 0 {
		startOverFailure(w, err)
		return
	}
	for _, j := range plan.entries {
		if err = instanceRuntimes.applyInstanceRetirementDataPolicy(ctx, j); err != nil {
			startOverFailure(w, err)
			return
		}
	}
	preserved, err := instanceRuntimes.startOverPreservationEvidence(plan.entries, plan.legacyBackupPath)
	if err != nil || !reflect.DeepEqual(preserved, plan.preserved) {
		startOverFailure(w, err)
		return
	}
	for _, name := range []string{"justvoxel.conf", "minecraft-backup.env"} {
		if err = instanceRuntimes.removePrivate(filepath.Join(startOverPolicyRoot, name)); err != nil {
			writeError(w, 500, "Minecraft retired; retry Start Over to finish setup policy cleanup")
			return
		}
	}
	for _, name := range []string{"justvoxel.conf", "minecraft-backup.env"} {
		if _, err = os.Lstat(filepath.Join(startOverPolicyRoot, name)); !errors.Is(err, os.ErrNotExist) {
			startOverFailure(w, err)
			return
		}
	}
	preserved, err = instanceRuntimes.startOverPreservationEvidence(plan.entries, plan.legacyBackupPath)
	if err != nil || !reflect.DeepEqual(preserved, plan.preserved) {
		startOverFailure(w, err)
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "start_over", "minecraft_instances", true, fmt.Sprintf("Retired %d Minecraft instances; data and backups preserved", len(plan.entries)))
	}
	writeJSON(w, 200, map[string]any{"ok": true, "participants_retired": len(plan.entries), "configuration_backup_path": archive})
}

func (b *instanceRuntimeBackend) startOverJournals() (map[string]instanceDeleteJournal, error) {
	out := map[string]instanceDeleteJournal{}
	files, err := os.ReadDir(b.staging)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		// Activation and other unpublished transactions must not be guessed away.
		if !strings.HasPrefix(f.Name(), "delete-") || !strings.HasSuffix(f.Name(), ".json") {
			return nil, errInstanceRecovery
		}
		id := strings.TrimSuffix(strings.TrimPrefix(f.Name(), "delete-"), ".json")
		j, e := b.deleteJournal(id)
		if e != nil || j.ResetPolicy != "start_over" {
			return nil, errInstanceRecovery
		}
		out[id] = j
	}
	if len(out) > 1 {
		return nil, errInstanceRecovery
	}
	return out, nil
}
func (b *instanceRuntimeBackend) planStartOver(ctx context.Context) (startOverPlan, error) {
	p := startOverPlan{OK: true, SchemaVersion: "v1", Mode: "start_over", Participants: []startOverParticipant{}, ConfigurationBackupSupported: true, DataAction: "preserve", BackupAction: "preserve", StorageLayoutAction: "preserve", AuthenticationAction: "preserve", WebUIUsersAction: "preserve", PodmanImagesAction: "preserve"}
	journals, err := b.startOverJournals()
	if err != nil {
		return p, err
	}
	ids, err := b.migrationRegisteredInstances()
	if err != nil {
		return p, err
	}
	names, err := b.identities().readNames()
	if err != nil {
		return p, err
	}
	registered := map[string]bool{}
	records := map[string]instanceRuntimeRecord{}
	for _, id := range ids {
		registered[id.ID] = true
	}
	dirs, err := os.ReadDir(b.root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	for _, d := range dirs {
		id := d.Name()
		if !instanceIDPattern.MatchString(id) || !d.IsDir() {
			return p, errInstanceRecovery
		}
		r, e := b.readRecord(id)
		if e != nil {
			j, ok := journals[id]
			if !ok || (j.Stage != "data_removed" && j.Stage != "identity_retired") || (!errors.Is(e, os.ErrNotExist) && !errors.Is(e, errInstanceNotFound)) {
				return p, errInstanceRecovery
			}
		} else {
			records[id] = r
		}
	}
	all := map[string]bool{}
	for id := range registered {
		all[id] = true
	}
	for id := range records {
		all[id] = true
	}
	for id := range journals {
		all[id] = true
	}
	if len(all) > 1024 {
		return p, errInstanceRecovery
	}
	sorted := []string{}
	for id := range all {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	coverage := []factoryResetInstance{}
	for _, id := range sorted {
		j, retry := journals[id]
		r, hasRecord := records[id]
		state := "stopped"
		players := 0
		playerNames := []string{}
		if retry {
			if hasRecord && !reflect.DeepEqual(r, j.Record) {
				return p, errInstanceRecovery
			}
			if !hasRecord && j.Stage != "data_removed" && j.Stage != "identity_retired" {
				return p, errInstanceRecovery
			}
			if names[id].DisplayName != j.Name {
				return p, errInstanceRecovery
			}
			if err = b.checkStartOverRetirement(ctx, j); err != nil {
				return p, err
			}
			state = "retiring"
		} else {
			if !registered[id] || !hasRecord || names[id].DisplayName == "" {
				return p, errInstanceRecovery
			}
			if err = b.verifyLive(r); err != nil {
				return p, err
			}
			if err = b.startOverInstanceIdle(ctx, id); err != nil {
				return p, err
			}
			observed, e := b.observe(ctx, id)
			if e != nil || (observed.State != "running" && observed.State != "stopped" && observed.State != "failed") {
				return p, errInstanceRecovery
			}
			state = observed.State
			if state == "failed" {
				// observeRaw rejects live failed containers; repeat it before disruption.
				raw, e := b.observeRaw(ctx, id)
				if e != nil || raw.State != "failed" {
					return p, errInstanceRecovery
				}
			}
			if state == "running" {
				players, playerNames, err = b.factoryResetPlayers(ctx, id)
				if err != nil {
					return p, err
				}
				for i, name := range playerNames {
					playerNames[i] = startOverPlayerName(name)
				}
			}
			j = instanceDeleteJournal{Schema: 1, ID: id, Name: names[id].DisplayName, Record: r, ResetPolicy: "start_over", Stage: "intent"}
			j.DataIdentity, err = b.deleteStorage(ctx, "inspect", j)
			if err != nil {
				return p, err
			}
		}
		if validateInstanceRecordPlacement(j.Record) != nil {
			return p, errInstanceRecovery
		}
		if err = b.storage(ctx, "validate", j.Record); err != nil {
			return p, err
		}
		bounded := j
		bounded.Stage = "identity_retired"
		rawJournal, e := json.Marshal(bounded)
		if e != nil || len(rawJournal) > 1<<20 {
			return p, errInstanceRecovery
		}
		p.entries = append(p.entries, j)
		coverage = append(coverage, factoryResetInstance{Retirement: j})
		p.Participants = append(p.Participants, startOverParticipant{ID: id, DisplayName: j.Name, State: state, PlayersOnline: players, Players: playerNames})
		p.TotalPlayersOnline += players
	}
	// This existing coverage check rejects orphan modern units/containers and
	// refuses fixed-runtime activity; it never operates a fixed runtime.
	if err = b.factoryResetCoverage(ctx, coverage); err != nil {
		return p, err
	}
	// Also refuse inactive fixed files: no legacy ownership is assumed here.
	for _, path := range []string{filepath.Join(b.quadlets, "minecraft.container"), filepath.Join(instanceBackupUnits, "minecraft-backup.service"), filepath.Join(instanceBackupUnits, "minecraft-backup.timer")} {
		if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			return p, errInstanceRecovery
		}
	}
	evidence, err := b.startOverConfigurationEvidence(p.entries)
	if err != nil {
		return p, err
	}
	for _, j := range p.entries {
		if j.Record.Backups == nil {
			snapshot, _, _, snapshotErr := b.snapshot(ctx)
			if snapshotErr != nil {
				return p, snapshotErr
			}
			p.legacyBackupPath = snapshot.BackupPath
			break
		}
	}
	p.preserved, err = b.startOverPreservationEvidence(p.entries, p.legacyBackupPath)
	if err != nil {
		return p, err
	}
	// Presentation names/player counts are intentionally excluded. Ownership,
	// storage identity, journal progress and committed configuration are bound.
	entries := append([]instanceDeleteJournal{}, p.entries...)
	for i := range entries {
		entries[i].Name = ""
	}
	payload, err := json.Marshal(struct {
		Entries       []instanceDeleteJournal
		Configuration map[string]string
		Preserved     map[string]string
	}{entries, evidence, p.preserved})
	if err != nil {
		return p, err
	}
	p.PlanFingerprint = "sha256:" + instanceHash(payload)
	return p, nil
}
func (b *instanceRuntimeBackend) startOverInstanceIdle(ctx context.Context, id string) error {
	if err := b.factoryResetBackupIdle(ctx, id); err != nil {
		return err
	}
	status, err := b.backupStatus(id)
	if err != nil || status.State == "running" || status.RestartRequired {
		return errInstanceRecovery
	}
	if restore, e := b.restoreJournal(id); e == nil {
		if !restoreTerminal(restore.Stage) {
			return errInstanceRecovery
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	dir, _, _ := b.paths(id)
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		switch f.Name() {
		case "config.json", "minecraft.env", "backup-settings.json", "backup-status.json", "restore-result.json", "backup-restore-result.json", "restore-journal.json":
		default:
			return errInstanceRecovery
		}
		if _, err = b.readPrivate(filepath.Join(dir, f.Name())); err != nil {
			return err
		}
	}
	job, err := b.command(ctx, "systemctl", "show", "--property=Job", "--value", "--", "minecraft-"+id+".service")
	if !systemdJobIdle(job, err) {
		return errInstanceBusy
	}
	return nil
}
func (b *instanceRuntimeBackend) checkStartOverRetirement(ctx context.Context, j instanceDeleteJournal) error {
	if j.ResetPolicy != "start_over" {
		return errInstanceRecovery
	}
	if err := b.factoryResetBackupIdle(ctx, j.ID); err != nil {
		return err
	}
	status, err := b.backupStatus(j.ID)
	if err != nil || status.State == "running" || status.RestartRequired {
		return errInstanceRecovery
	}
	if restore, e := b.restoreJournal(j.ID); e == nil {
		if !restoreTerminal(restore.Stage) {
			return errInstanceRecovery
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	dirPath, _, _ := b.paths(j.ID)
	files, e := os.ReadDir(dirPath)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	for _, f := range files {
		switch f.Name() {
		case "config.json", "minecraft.env", "backup-settings.json", "backup-status.json", "restore-result.json", "backup-restore-result.json", "restore-journal.json":
		default:
			return errInstanceRecovery
		}
	}
	if err := b.applyInstanceRetirementDataPolicy(ctx, j); err != nil {
		return err
	}
	if err := b.storage(ctx, "validate", j.Record); err != nil {
		return err
	}
	if err := b.deleteStopped(ctx, j.ID, true); err != nil {
		return err
	}
	r, err := b.readRecord(j.ID)
	if err == nil {
		if !reflect.DeepEqual(r, j.Record) {
			return errInstanceRecovery
		}
	} else if (!errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInstanceNotFound)) || (j.Stage != "data_removed" && j.Stage != "identity_retired") {
		return errInstanceRecovery
	}
	dir, _, _ := b.paths(j.ID)
	raw, e := b.readPrivate(filepath.Join(dir, "config.json"))
	if e == nil {
		var record instanceRuntimeRecord
		if instanceDecode(raw, &record) != nil || !reflect.DeepEqual(record, j.Record) {
			return errInstanceRecovery
		}
	} else if !errors.Is(e, os.ErrNotExist) || (j.Stage != "data_removed" && j.Stage != "identity_retired") {
		return errInstanceRecovery
	}
	// Validate surviving published artifacts against pinned hashes. Missing files
	// are possible after interruption inside runtime retirement, never replacements.
	_, env, q := b.paths(j.ID)
	for path, hash := range map[string]string{env: j.Record.EnvironmentSHA256, q: j.Record.QuadletSHA256} {
		raw, e := b.readPrivate(path)
		if e == nil {
			if instanceHash(raw) != hash {
				return errInstanceRecovery
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) resumeStartOverRetirement(ctx context.Context, j instanceDeleteJournal) error {
	if err := b.checkStartOverRetirement(ctx, j); err != nil {
		return err
	}
	save := func(stage string) error { j.Stage = stage; return b.writeJSON(b.deletePath(j.ID), j, true) }
	if j.Stage == "intent" {
		if err := b.retireInstanceRuntime(ctx, j); err != nil {
			return err
		}
		if err := save("runtime_retired"); err != nil {
			return err
		}
	}
	if j.Stage == "runtime_retired" {
		if err := b.applyInstanceRetirementDataPolicy(ctx, j); err != nil {
			return err
		}
		if err := save("data_removed"); err != nil {
			return err
		}
	}
	if j.Stage == "data_removed" {
		if err := b.retireInstanceStateAndIdentity(j); err != nil {
			return err
		}
		if err := save("identity_retired"); err != nil {
			return err
		}
	}
	if err := b.applyInstanceRetirementDataPolicy(ctx, j); err != nil {
		return err
	}
	dir, env, q := b.paths(j.ID)
	for _, path := range []string{dir, env, q, filepath.Join(b.identities().directory, j.ID+".json"), filepath.Join(instanceBackupUnits, instanceBackupUnit(j.ID, "service")), filepath.Join(instanceBackupUnits, instanceBackupUnit(j.ID, "timer"))} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errInstanceRecovery
		}
	}
	out, err := b.command(ctx, "podman", "ps", "--all", "--filter", "name=^minecraft-"+j.ID+"$", "--format", "{{.Names}}")
	if err != nil || strings.TrimSpace(string(out)) != "" {
		return errInstanceRecovery
	}
	for _, rule := range j.Record.Firewall {
		present, err := b.queryFirewall(ctx, rule)
		if err != nil || present {
			return errInstanceRecovery
		}
	}
	// Identity retirement syncs its parent; journal removal is the final commit.
	return b.removePrivate(b.deletePath(j.ID))
}

func startOverPlayerName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '\u009b' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, "?"))
	runes := []rune(name)
	if len(runes) > 64 {
		runes = runes[:64]
	}
	return string(runes)
}

func (b *instanceRuntimeBackend) requireNoStartOver() error {
	files, err := os.ReadDir(b.staging)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Name(), "delete-") || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(f.Name(), "delete-"), ".json")
		j, e := b.deleteJournal(id)
		if e != nil || j.ResetPolicy == "start_over" {
			return errInstanceRecovery
		}
	}
	return nil
}
