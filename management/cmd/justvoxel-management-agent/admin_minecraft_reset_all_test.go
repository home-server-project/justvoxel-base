package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// All commands are simulated. Data and preservation assertions use real files
// under the fixture root; no host runtime is touched.
type resetAllFixture struct {
	*startOverFixture
	scopes      map[string]string
	removedData map[string]int
	failData    string
}

func newResetAllFixture(t *testing.T, n int) *resetAllFixture {
	t.Helper()
	count := n
	if count > 2 {
		count = 2
	}
	x := &resetAllFixture{startOverFixture: newStartOverFixture(t, count), scopes: map[string]string{}, removedData: map[string]int{}}
	b := x.f.x.b
	if n == 3 {
		registerLogicalForTest(t, b.identities(), "third01", "paper", "Third server")
		record := x.f.x.records[0]
		record.Runtime.ID = "jv-third01"
		record.Runtime.JavaPort = 25568
		record.Storage = instanceStorageFor(x.f.x.snapshot, record.Runtime.ID)
		configs := []minecraftInstanceRuntimeConfig{x.f.x.records[0].Runtime, x.f.x.records[1].Runtime, record.Runtime}
		files, err := b.identities().renderInstanceRuntimes(x.f.x.snapshot, configs)
		if err != nil {
			t.Fatal(err)
		}
		f := files[2]
		record.EnvironmentSHA256, record.QuadletSHA256 = instanceHash(f.Environment), instanceHash(f.Quadlet)
		dir, env, q := b.paths(record.Runtime.ID)
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err = b.writePrivate(env, f.Environment, false); err != nil {
			t.Fatal(err)
		}
		if err = b.writePrivate(q, f.Quadlet, false); err != nil {
			t.Fatal(err)
		}
		if err = b.writeJSON(filepath.Join(dir, "config.json"), record, false); err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(record.Storage.DataPath, 0700); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"service", "timer"} {
			if err = b.writePrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(record.Runtime.ID, suffix)), []byte("unit fixture"), false); err != nil {
				t.Fatal(err)
			}
		}
		x.f.x.records = append(x.f.x.records, record)
		x.f.x.states[record.Runtime.ID] = "stopped"
	}
	base := b.command
	b.pause = func(context.Context, time.Duration) error { return nil }
	b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "nsenter" && len(args) > 3 && args[2] == "bash" {
			scope := x.scopes[args[len(args)-1]]
			if scope == "" {
				scope = "internal"
			}
			return []byte(scope), nil
		}
		if command == "nsenter" && len(args) > 6 && args[2] == instanceRuntimeHelper && args[3] == "restore-validate" {
			return nil, nil // Model exact mount validation with the data leaf absent.
		}
		if command == "nsenter" && len(args) > 6 && args[2] == instanceDeleteHelper {
			id, action, path := args[4], args[3], args[6]
			var st syscall.Stat_t
			err := syscall.Stat(path, &st)
			if action == "inspect" {
				if err != nil {
					return nil, err
				}
				return []byte(fmt.Sprintf(`{"identity":"%d:%d"}`, st.Dev, st.Ino)), nil
			}
			j, e := b.deleteJournal(id)
			if e != nil || j.ResetPolicy != "reset_all" || !validResetAllDataPolicy(j.DataScope, j.DataAction) {
				t.Fatal("mutation/verification without durable policy", j, e)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if err == nil && fmt.Sprintf("%d:%d", st.Dev, st.Ino) != j.DataIdentity {
				return nil, errInstanceRecovery
			}
			if action == "absent" {
				if err == nil {
					return nil, errInstanceRecovery
				}
				return nil, nil
			}
			if action == "verify" {
				return nil, nil
			}
			if action != "remove" || j.DataAction != "delete" {
				t.Fatal("unsafe data action", args, j)
			}
			if x.failData == id {
				return nil, errors.New("simulated data interruption")
			}
			if err == nil {
				x.removedData[id]++
			}
			return nil, os.RemoveAll(path)
		}
		return base(ctx, command, args...)
	}
	return x
}
func (x *resetAllFixture) review(t *testing.T) startOverPlan {
	t.Helper()
	p, err := x.f.x.b.planApplianceRetirement(context.Background(), "reset_all")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (x *resetAllFixture) execute(t *testing.T, p startOverPlan, confirm bool) (operationJournal, error) {
	t.Helper()
	op, _, err := x.s.operations.beginMinecraftReset(p.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	err = executeMinecraftResetAll(context.Background(), x.s, op.OperationID, p.PlanFingerprint, confirm, session{})
	result, e := x.s.operations.get(op.OperationID)
	if e != nil {
		t.Fatal(e)
	}
	return result, err
}
func TestResetAllTwoServersStorageAndPreservation(t *testing.T) {
	for _, scope := range []string{"internal", "external", "network", "unknown"} {
		t.Run(scope, func(t *testing.T) {
			x := newResetAllFixture(t, 2)
			b := x.f.x.b
			x.f.x.states[x.f.x.records[0].Runtime.ID] = "running"
			x.scopes[x.f.x.records[1].Storage.DataPath] = scope
			modern := x.setBackupPlacement(t, 0, true)
			before := map[string][]byte{}
			// Both archive formats and their recognition metadata must survive.
			for _, path := range []string{filepath.Join(modern.Path, "archive.tar.gz"), filepath.Join(modern.Path, "archive.tar.gz.json"), filepath.Join(x.f.x.snapshot.BackupPath, "archive.tar.gz"), filepath.Join(x.f.x.snapshot.BackupPath, "archive.tar.gz.json"), filepath.Join(startOverPolicyRoot, "authentication.env"), filepath.Join(startOverPolicyRoot, "webui-users.json"), filepath.Join(startOverPolicyRoot, "storage-mounts"), filepath.Join(startOverPolicyRoot, "smb-backup.credentials"), startOverFstabPath} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("keep "+path), 0600); err != nil {
					t.Fatal(err)
				}
				before[path], _ = os.ReadFile(path)
			}
			for _, r := range x.f.x.records {
				if err := os.WriteFile(filepath.Join(r.Storage.DataPath, "world"), []byte("world"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p := x.review(t)
			if len(p.Participants) != 2 || p.Participants[0].State != "running" || p.Participants[1].State != "stopped" {
				t.Fatal(p.Participants)
			}
			op, err := x.execute(t, p, false)
			if err != nil || op.State != operationSucceeded || op.InstanceID != "" {
				t.Fatal(op, err)
			}
			for i, r := range x.f.x.records {
				_, err := os.Stat(r.Storage.DataPath)
				if i == 0 || scope == "internal" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatal("internal data survived", err)
					}
				} else if err != nil {
					t.Fatal("preserved data lost", err)
				}
				for _, path := range []string{filepath.Join(b.root, r.Runtime.ID), filepath.Join(b.identities().directory, r.Runtime.ID+".json"), filepath.Join(b.identities().directory, "names", r.Runtime.ID+".json"), b.deletePath(r.Runtime.ID), filepath.Join(b.quadlets, "minecraft-"+r.Runtime.ID+".container"), filepath.Join(instanceBackupUnits, instanceBackupUnit(r.Runtime.ID, "service")), filepath.Join(instanceBackupUnits, instanceBackupUnit(r.Runtime.ID, "timer"))} {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("retired state survived", path, err)
					}
				}
			}
			for path, want := range before {
				got, err := os.ReadFile(path)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("preservation failure", path, err)
				}
			}
			for _, name := range []string{"justvoxel.conf", "minecraft-backup.env"} {
				if _, err := os.Stat(filepath.Join(startOverPolicyRoot, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("active policy survived", err)
				}
			}
			empty := x.review(t)
			if len(empty.entries) != 0 {
				t.Fatal("final inventory not empty")
			}
		})
	}
}

func TestResetAllInstanceCountsPlanApplyAndEmptyState(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			x := newResetAllFixture(t, count)
			p := x.review(t)
			if len(p.Participants) != count {
				t.Fatal("wrong participant count", p.Participants)
			}
			op, err := x.execute(t, p, false)
			if err != nil || op.State != operationSucceeded {
				t.Fatal(op, err)
			}
			if empty := x.review(t); len(empty.Participants) != 0 || len(empty.entries) != 0 {
				t.Fatal("final inventory not empty", empty.Participants)
			}
		})
	}
}

func TestResetAllFactoryResetExclusionPreservesEvidence(t *testing.T) {
	for _, state := range []operationState{operationQueued, operationNeedsAttention} {
		t.Run(string(state), func(t *testing.T) {
			x := newResetAllFixture(t, 2)
			p := x.review(t)
			op, _, err := x.s.operations.beginFactoryReset(testFactoryResetFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			entries, _, err := x.f.x.b.factoryResetParticipants(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			evidence := factoryResetInstancesJournal{Schema: 1, OperationID: op.OperationID, Entries: entries}
			path := factoryResetEvidencePath(x.s, op.OperationID)
			if err = x.f.x.b.writeJSON(path, evidence, false); err != nil {
				t.Fatal(err)
			}
			if state == operationNeedsAttention {
				if _, err = x.s.operations.transition(op.OperationID, state, "interrupted", "Interrupted Factory Reset."); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: p.PlanFingerprint, ConfirmPlayers: true})
			rr := httptest.NewRecorder()
			x.s.adminMinecraftResetAllApply(rr, surfaceRequest("POST", "/v1/admin/reset/minecraft/apply", string(body)))
			if rr.Code != 409 || !strings.Contains(rr.Body.String(), "Minecraft reset recovery could not be verified") {
				t.Fatal("competing Reset Minecraft accepted", rr.Code, rr.Body.String())
			}
			if current, e := x.s.operations.currentFactoryReset(); e != nil || current == nil || current.OperationID != op.OperationID || current.State != state {
				t.Fatal("Factory Reset recovery changed", current, e)
			}
			if current, e := x.s.operations.currentMinecraftReset(); e != nil || current != nil {
				t.Fatal("Reset Minecraft stole recovery", current, e)
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("Factory Reset evidence changed", err)
			}
			for _, call := range x.f.x.calls {
				if strings.Contains(call, "systemctl stop") || strings.Contains(call, "podman rm") || strings.Contains(call, "instance-delete remove") {
					t.Fatal("competing reset mutated runtime", call)
				}
			}
		})
	}
}
func TestResetAllStaleReviewRefusesBeforeMutation(t *testing.T) {
	for _, change := range []string{"config", "storage", "scope", "participant"} {
		t.Run(change, func(t *testing.T) {
			x := newResetAllFixture(t, 2)
			p := x.review(t)
			b := x.f.x.b
			switch change {
			case "config":
				if err := os.WriteFile(filepath.Join(startOverPolicyRoot, "justvoxel.conf"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "storage":
				path := x.f.x.records[0].Storage.DataPath
				if err := os.Rename(path, path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "scope":
				x.scopes[x.f.x.records[0].Storage.DataPath] = "external"
			case "participant":
				if err := os.Remove(filepath.Join(b.identities().directory, x.f.x.records[0].Runtime.ID+".json")); err != nil {
					t.Fatal(err)
				}
			}
			x.f.x.calls = nil
			op, err := x.execute(t, p, false)
			if err == nil || op.State != operationNeedsAttention || len(startOverMutations(x.f.x.calls)) != 0 {
				t.Fatal("stale review mutated", op, err, x.f.x.calls)
			}
		})
	}
}
func TestResetAllOnlinePlayersAcrossParticipants(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(fmt.Sprint(confirmed), func(t *testing.T) {
			x := newResetAllFixture(t, 2)
			id := x.f.x.records[1].Runtime.ID
			x.f.x.states[id] = "running"
			x.players = "There are 1 of a max of 20 players online: Alex"
			p := x.review(t)
			if p.TotalPlayersOnline != 1 || p.Participants[1].PlayersOnline != 1 {
				t.Fatal(p.Participants)
			}
			x.f.x.calls = nil
			op, err := x.execute(t, p, confirmed)
			if !confirmed {
				if err == nil || len(startOverMutations(x.f.x.calls)) != 0 {
					t.Fatal("unconfirmed interruption", err)
				}
			} else {
				calls := strings.Join(x.f.x.calls, "\n")
				if err != nil || op.State != operationSucceeded || !strings.Contains(calls, "minecraft-"+id+" rcon-cli say Server shutting down in 60 seconds") {
					t.Fatal("graceful interruption missing", err, calls)
				}
			}
		})
	}
}
func TestResetAllContinueSameOperationResumesIncompleteFirst(t *testing.T) {
	x := newResetAllFixture(t, 3)
	p := x.review(t)
	first, second, third := p.entries[0].ID, p.entries[1].ID, p.entries[2].ID
	x.failData = second
	op, err := x.execute(t, p, false)
	if err == nil || op.State != operationNeedsAttention || x.removedData[first] != 1 || x.removedData[third] != 0 {
		t.Fatal(op, err, x.removedData)
	}
	b := x.f.x.b
	j, err := b.deleteJournal(second)
	if err != nil || j.ResetPolicy != "reset_all" || j.Stage != "runtime_retired" || j.DataAction != "delete" {
		t.Fatal(j, err)
	}
	if _, _, err := x.s.operations.beginSetup(testSetupFingerprint); err == nil {
		t.Fatal("setup allowed during removal")
	}
	x.failData = ""
	next := x.review(t)
	if len(next.entries) != 2 || next.Participants[0].ID != second {
		t.Fatal("completed participant reintroduced", next.Participants)
	}
	retry, err := x.s.operations.retryMinecraftReset(op.OperationID, next.PlanFingerprint)
	if err != nil || retry.OperationID != op.OperationID {
		t.Fatal(retry, err)
	}
	if err = executeMinecraftResetAll(context.Background(), x.s, retry.OperationID, next.PlanFingerprint, false, session{}); err != nil {
		t.Fatal(err)
	}
	finished, err := x.s.operations.get(op.OperationID)
	if err != nil || finished.OperationID != op.OperationID || finished.State != operationSucceeded {
		t.Fatal("continuation did not complete the original persistent operation", finished, err)
	}
	for _, id := range []string{first, second, third} {
		if x.removedData[id] != 1 {
			t.Fatal("duplicate or missing data removal", x.removedData)
		}
	}
}
func TestResetAllReviewAfterDataRemovalBeforeStageSave(t *testing.T) {
	x := newResetAllFixture(t, 1)
	b := x.f.x.b
	j := x.review(t).entries[0]
	if err := b.writeJSON(b.deletePath(j.ID), j, false); err != nil {
		t.Fatal(err)
	}
	if err := b.retireInstanceRuntime(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	j.Stage = "runtime_retired"
	if err := b.writeJSON(b.deletePath(j.ID), j, true); err != nil {
		t.Fatal(err)
	}
	if err := b.removeInstanceData(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	reviewed := x.review(t) // Must inspect, never reclassify or recreate the missing data.
	if reviewed.Participants[0].DataAction != "delete" {
		t.Fatal(reviewed.Participants)
	}
	if _, err := x.execute(t, reviewed, false); err != nil {
		t.Fatal(err)
	}
}
func TestResetAllAdminReviewIgnoresSelectionAndRequiresAdministrator(t *testing.T) {
	x := newResetAllFixture(t, 2)
	var fingerprint string
	for _, query := range []string{"", "?server=jv-family1", "?server=jv-sibling", "?server=invalid&server=another"} {
		r := httptest.NewRequest("POST", "/v1/admin/reset/minecraft/plan"+query, nil)
		r = r.WithContext(context.WithValue(r.Context(), peerUIDKey{}, uint32(0)))
		w := httptest.NewRecorder()
		x.s.adminMinecraftResetAllPlan(w, r)
		var p minecraftResetAllPlan
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || len(p.Participants) != 2 {
			t.Fatal(w.Code, w.Body.String())
		}
		if fingerprint != "" && p.PlanFingerprint != fingerprint {
			t.Fatal("selected server changed plan")
		}
		fingerprint = p.PlanFingerprint
	}
	w := httptest.NewRecorder()
	x.s.adminMinecraftResetAllPlan(w, httptest.NewRequest("POST", "/v1/admin/reset/minecraft/plan", nil))
	if w.Code == 200 {
		t.Fatal("unauthenticated administrator route")
	}
}

func TestResetAllApplyRejectsStaleReviewWithoutCreatingOperation(t *testing.T) {
	x := newResetAllFixture(t, 2)
	p := x.review(t)
	x.scopes[x.f.x.records[0].Storage.DataPath] = "unknown"
	raw, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: p.PlanFingerprint})
	r := httptest.NewRequest("POST", "/v1/admin/reset/minecraft/apply", strings.NewReader(string(raw)))
	r = r.WithContext(context.WithValue(r.Context(), peerUIDKey{}, uint32(0)))
	w := httptest.NewRecorder()
	x.f.x.calls = nil
	x.s.adminMinecraftResetAllApply(w, r)
	if w.Code != 409 || len(startOverMutations(x.f.x.calls)) != 0 {
		t.Fatal(w.Code, w.Body.String(), x.f.x.calls)
	}
	current, err := x.s.operations.currentMinecraftReset()
	if err != nil || current != nil {
		t.Fatal("stale Apply created an operation", current, err)
	}
}
func TestResetAllPersistedPolicyIsolation(t *testing.T) {
	for _, action := range []string{"delete", "preserve", "", "start_over", "anything"} {
		name := action
		if name == "" {
			name = "empty"
		}
		// An unnamed subtest gets #00 in t.TempDir(), which is intentionally
		// rejected by immutable runtime placement validation.
		t.Run(name, func(t *testing.T) {
			x := newResetAllFixture(t, 1)
			b := x.f.x.b
			j := x.review(t).entries[0]
			j.DataAction = action
			if err := b.writeJSON(b.deletePath(j.ID), j, false); err != nil {
				t.Fatal(err)
			}
			_, err := b.deleteJournal(j.ID)
			if (err == nil) != (action == "delete") {
				t.Fatal("invalid reset_all action accepted", action, err)
			}
			if _, err := b.planStartOver(context.Background()); err == nil {
				t.Fatal("Start Over accepted destructive reset_all journal")
			}
		})
	}
}
func TestResetAllFinalProofRejectsOrphanRuntime(t *testing.T) {
	x := newResetAllFixture(t, 1)
	p := x.review(t)
	base := x.f.x.b.command
	orphan := false
	x.f.x.b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "podman" && args[0] == "rm" {
			orphan = true
		}
		if orphan && command == "podman" && args[0] == "ps" && args[3] == "name=^minecraft-jv-" {
			return []byte("minecraft-jv-orphan1"), nil
		}
		return base(ctx, command, args...)
	}
	op, err := x.execute(t, p, false)
	if err == nil || op.State != operationNeedsAttention {
		t.Fatal("orphan runtime accepted", op, err)
	}
	if _, err := os.Stat(filepath.Join(startOverPolicyRoot, "justvoxel.conf")); err != nil {
		t.Fatal("policy cleaned before empty proof", err)
	}
}
func TestResetAllBackupPathCannotBeDeletedWithData(t *testing.T) {
	x := newResetAllFixture(t, 1)
	p := x.review(t)
	if err := resetAllBackupSeparation(p.entries, filepath.Join(p.entries[0].Record.Storage.DataPath, "backups")); err == nil {
		t.Fatal("backup path within deleted data accepted")
	}
}
func TestResetAllOperationReloadKeepsMaintenanceAndSameID(t *testing.T) {
	x := newResetAllFixture(t, 1)
	p := x.review(t)
	op, _, err := x.s.operations.beginMinecraftReset(p.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.s.operations.transition(op.OperationID, operationValidating, "revalidating", "Revalidating"); err != nil {
		t.Fatal(err)
	}
	// Model a restart after durable intent, before stopping the still-running server.
	j := p.entries[0]
	x.f.x.states[j.ID] = "running"
	if err = x.f.x.b.writeJSON(x.f.x.b.deletePath(j.ID), j, false); err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(filepath.Dir(x.s.operations.operationsDir))
	if err = x.s.operations.close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.close()
	x.s.operations = reloaded
	current, err := reloaded.currentMinecraftReset()
	if err != nil || current == nil || current.OperationID != op.OperationID || current.InstanceID != "" || current.State != operationNeedsAttention {
		t.Fatal(current, err)
	}
	if _, _, err = reloaded.beginSetup(testSetupFingerprint); err == nil {
		t.Fatal("setup allowed after reload")
	}
	review := x.review(t)
	resumed, err := reloaded.retryMinecraftReset(current.OperationID, review.PlanFingerprint)
	if err != nil || resumed.OperationID != op.OperationID {
		t.Fatal(resumed, err)
	}
	if err = executeMinecraftResetAll(context.Background(), x.s, resumed.OperationID, review.PlanFingerprint, false, session{}); err != nil {
		t.Fatal(err)
	}
}

func TestResetAllCompletedDataStageRequiresActualAbsence(t *testing.T) {
	x := newResetAllFixture(t, 1)
	b := x.f.x.b
	j := x.review(t).entries[0]
	j.Stage = "data_removed"
	if err := b.writeJSON(b.deletePath(j.ID), j, false); err != nil {
		t.Fatal(err)
	}
	if err := b.checkRetirementDataPolicy(context.Background(), j); err == nil {
		t.Fatal("completed stage accepted surviving internal data")
	}
}

func TestResetAllRecoveryStagesReuseExactPolicy(t *testing.T) {
	for _, scope := range []string{"internal", "external", "network", "unknown"} {
		for _, stage := range []string{"intent", "runtime_retired", "data_removed", "data_removed_identity_cleanup", "identity_retired"} {
			t.Run(scope+"-"+stage, func(t *testing.T) {
				x := newResetAllFixture(t, 1)
				b := x.f.x.b
				x.scopes[x.f.x.records[0].Storage.DataPath] = scope
				j := x.review(t).entries[0]
				if err := b.persistResetAllIntent(context.Background(), j); err != nil {
					t.Fatal(err)
				}
				if stage != "intent" {
					if err := b.retireInstanceRuntime(context.Background(), j); err != nil {
						t.Fatal(err)
					}
					j.Stage = "runtime_retired"
					if err := b.writeJSON(b.deletePath(j.ID), j, true); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasPrefix(stage, "data_removed") || stage == "identity_retired" {
					if err := b.applyInstanceRetirementDataPolicy(context.Background(), j); err != nil {
						t.Fatal(err)
					}
					j.Stage = "data_removed"
					if err := b.writeJSON(b.deletePath(j.ID), j, true); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "data_removed_identity_cleanup" || stage == "identity_retired" {
					if err := b.retireInstanceStateAndIdentity(j); err != nil {
						t.Fatal(err)
					}
					if stage == "identity_retired" {
						j.Stage = stage
					}
					if err := b.writeJSON(b.deletePath(j.ID), j, true); err != nil {
						t.Fatal(err)
					}
				}
				base := b.command
				b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
					if command == "nsenter" && len(args) > 3 && args[2] == "bash" {
						t.Fatal("recovery attempted to reclassify durable data policy")
					}
					return base(ctx, command, args...)
				}
				p := x.review(t)
				if len(p.entries) != 1 || !reflect.DeepEqual(p.entries[0], j) {
					t.Fatal("journal evidence changed during recovery", p.entries, j)
				}
				if _, err := b.planStartOver(context.Background()); err == nil {
					t.Fatal("Start Over adopted reset_all evidence")
				}
				if _, err := x.execute(t, p, false); err != nil {
					t.Fatal(err)
				}
				_, err := os.Stat(j.Record.Storage.DataPath)
				if (j.DataAction == "delete" && !errors.Is(err, os.ErrNotExist)) || (j.DataAction == "preserve" && err != nil) {
					t.Fatal("durable data policy was not honored", j.DataScope, j.DataAction, err)
				}
			})
		}
	}
}

func TestResetAllRecoveryRejectsReplacementArtifacts(t *testing.T) {
	for _, artifact := range []string{"environment", "quadlet", "identity", "name", "data"} {
		t.Run(artifact, func(t *testing.T) {
			x := newResetAllFixture(t, 1)
			b := x.f.x.b
			j := x.review(t).entries[0]
			if err := b.persistResetAllIntent(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			if err := b.retireInstanceRuntime(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			j.Stage = "runtime_retired"
			if err := b.writeJSON(b.deletePath(j.ID), j, true); err != nil {
				t.Fatal(err)
			}
			dir, env, q := b.paths(j.ID)
			path := map[string]string{"environment": env, "quadlet": q, "identity": filepath.Join(b.identities().directory, j.ID+".json"), "name": filepath.Join(b.identities().directory, "names", j.ID+".json")}[artifact]
			if artifact == "data" {
				if err := os.Rename(j.Record.Storage.DataPath, j.Record.Storage.DataPath+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(j.Record.Storage.DataPath, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
				t.Fatal(dir, err)
			}
			if _, err := b.planApplianceRetirement(context.Background(), "reset_all"); err == nil {
				t.Fatal("replacement artifact accepted", artifact)
			}
		})
	}
}

func TestResetAllPlanFailureWithoutRecoveryDoesNotPromiseContinuation(t *testing.T) {
	x := newResetAllFixture(t, 2)
	if err := os.Remove(filepath.Join(x.f.x.b.identities().directory, x.f.x.records[0].Runtime.ID+".json")); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/admin/reset/minecraft/plan", nil)
	r = r.WithContext(context.WithValue(r.Context(), peerUIDKey{}, uint32(0)))
	w := httptest.NewRecorder()
	x.f.x.calls = nil
	x.s.adminMinecraftResetAllPlan(w, r)
	if w.Code != 409 || strings.Contains(w.Body.String(), "Continue removal") || !strings.Contains(w.Body.String(), "No additional removal was performed.") || len(startOverMutations(x.f.x.calls)) != 0 {
		t.Fatal(w.Code, w.Body.String(), x.f.x.calls)
	}
	current, err := x.s.operations.currentMinecraftReset()
	if err != nil || current != nil {
		t.Fatal(current, err)
	}
}
