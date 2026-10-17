package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type factoryResetFixture struct {
	x          *physicalMigrationFixture
	removed    map[string]bool
	removals   map[string]int
	scope      string
	failRemove string
}

func newFactoryResetFixture(t *testing.T, n int) *factoryResetFixture {
	t.Helper()
	x := physicalMigrationTestFixture(t, n)
	f := &factoryResetFixture{x: x, removed: map[string]bool{}, removals: map[string]int{}, scope: "internal"}
	oldUnits, oldLock, oldExclusion := instanceBackupUnits, pluginRuntimeLockPath, instanceRestoreExclusionPath
	instanceBackupUnits = t.TempDir()
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance")
	instanceRestoreExclusionPath = filepath.Join(t.TempDir(), "restore")
	t.Cleanup(func() {
		instanceBackupUnits, pluginRuntimeLockPath, instanceRestoreExclusionPath = oldUnits, oldLock, oldExclusion
	})
	base := x.b.command
	x.b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		call := command + " " + strings.Join(args, " ")
		handled := func(out string) ([]byte, error) { x.calls = append(x.calls, call); return []byte(out), nil }
		if command == "systemctl" && args[0] == "show" && args[1] == "--property=ActiveState" && strings.HasPrefix(args[len(args)-1], "minecraft-backup.") {
			return handled("inactive\n")
		}
		if command == "systemctl" && args[0] == "list-units" {
			return handled("")
		}
		if command == "systemctl" && args[0] == "show" && args[1] == "--property=LoadState,ActiveState,Job" {
			return handled("LoadState=loaded\nActiveState=inactive\nJob=0\n")
		}
		if command == "systemctl" && args[0] == "show" && args[1] == "--property=Job" {
			return handled("0\n")
		}
		if command == "systemctl" && args[0] == "disable" {
			return handled("")
		}
		if command == "systemctl" && args[0] == "show" && args[1] == "--property=LoadState,ActiveState,SubState" {
			id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "minecraft-"), ".service")
			_, _, q := x.b.paths(id)
			if _, e := os.Stat(q); errors.Is(e, os.ErrNotExist) {
				return handled("LoadState=not-found\nActiveState=inactive\n")
			}
		}
		if command == "podman" && args[0] == "ps" && args[3] == "name=^minecraft-jv-" {
			names := []string{}
			for _, r := range x.records {
				if !f.removed[r.Runtime.ID] {
					names = append(names, "minecraft-"+r.Runtime.ID)
				}
			}
			return handled(strings.Join(names, "\n"))
		}
		if command == "podman" && args[0] == "ps" && strings.HasPrefix(args[3], "name=^minecraft-") {
			id := strings.TrimSuffix(strings.TrimPrefix(args[3], "name=^minecraft-"), "$")
			if f.removed[id] {
				return handled("")
			}
		}
		if command == "podman" && args[0] == "rm" {
			id := strings.TrimPrefix(args[len(args)-1], "minecraft-")
			if !instanceIDPattern.MatchString(id) {
				t.Fatal("fixed or foreign container", args)
			}
			f.removed[id] = true
			return handled("")
		}
		if command == "nsenter" && len(args) > 3 && args[2] == "bash" {
			return handled(f.scope + "\n")
		}
		if command == "nsenter" && len(args) > 4 && args[2] == instanceDeleteHelper {
			id := args[4]
			if args[3] == "inspect" {
				return handled("{\"identity\":\"1:2\"}")
			}
			if f.failRemove == id {
				return nil, errors.New("simulated storage failure")
			}
			data := args[6]
			if _, err := os.Stat(data); err == nil {
				f.removals[id]++
			}
			if err := os.RemoveAll(data); err != nil {
				t.Fatal(err)
			}
			return handled("")
		}
		return base(ctx, command, args...)
	}
	for _, r := range x.records {
		for _, suffix := range []string{"timer", "service"} {
			if err := x.b.writePrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(r.Runtime.ID, suffix)), []byte("unit fixture"), false); err != nil {
				t.Fatal(err)
			}
		}
	}
	return f
}
func useEmptyFactoryResetRuntime(t *testing.T) { t.Helper(); newFactoryResetFixture(t, 0) }
func factoryFixtureServer(t *testing.T) *server {
	t.Helper()
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	return s
}
func TestFactoryResetAllManagedInstances(t *testing.T) {
	for _, states := range [][]string{{}, {"stopped"}, {"running"}, {"running", "stopped"}, {"running", "running"}} {
		t.Run(strings.Join(states, "-"), func(t *testing.T) {
			f := newFactoryResetFixture(t, len(states))
			x := f.x
			ctx := withPluginInstance(context.Background(), "jv-foreign1")
			for i, state := range states {
				x.states[x.records[i].Runtime.ID] = state
			}
			entries, _, err := x.b.factoryResetParticipants(ctx)
			if err != nil || len(entries) != len(states) {
				t.Fatal(entries, err)
			}
			s := factoryFixtureServer(t)
			op, _, err := s.operations.beginFactoryReset(testFactoryResetFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			unrelated := filepath.Join(x.snapshot.StorageRoot, "unrelated.txt")
			if err = os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(x.snapshot.BackupPath, "archive.tar")
			if err = os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(backup, []byte("keep until appliance cleanup"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = s.retireFactoryResetInstances(ctx, op.OperationID, adminFactoryResetPlanResponse{Instances: entries}); err != nil {
				t.Fatal(err)
			}
			for i, r := range x.records {
				id := r.Runtime.ID
				if f.removals[id] != 1 {
					t.Fatal("world removal count", id, f.removals[id])
				}
				count := 0
				for _, call := range x.calls {
					if call == "systemctl stop -- minecraft-"+id+".service" {
						count++
					}
					if call == "systemctl start -- minecraft-"+id+".service" {
						t.Fatal("started stopped sibling")
					}
				}
				want := 0
				if states[i] == "running" {
					want = 1
				}
				if count != want {
					t.Fatal("shutdown count", id, count, want)
				}
				for _, path := range []string{filepath.Join(x.b.root, id), filepath.Join(x.b.store.directory, id+".json"), r.Storage.DataPath, filepath.Join(x.b.quadlets, "minecraft-"+id+".container"), filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "timer"))} {
					if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
						t.Fatal("surviving state", path, e)
					}
				}
			}
			for _, path := range []string{unrelated, backup} {
				if _, err = os.Stat(path); err != nil {
					t.Fatal("unrelated/shared state removed", err)
				}
			}
			for _, call := range x.calls {
				if strings.Contains(call, "stop -- minecraft.service") || strings.Contains(call, "rm --ignore -- minecraft") && !strings.Contains(call, "minecraft-jv-") {
					t.Fatal("fixed lifecycle target", call)
				}
			}
			if err = s.retireFactoryResetInstances(ctx, op.OperationID, adminFactoryResetPlanResponse{Instances: entries}); err != nil {
				t.Fatal("idempotent completion", err)
			}
		})
	}
}
func TestFactoryResetPreservesExternalInstanceWorlds(t *testing.T) {
	f := newFactoryResetFixture(t, 2)
	f.scope = "external"
	entries, _, err := f.x.b.factoryResetParticipants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := factoryFixtureServer(t)
	op, _, _ := s.operations.beginFactoryReset(testFactoryResetFingerprint)
	if err = s.retireFactoryResetInstances(context.Background(), op.OperationID, adminFactoryResetPlanResponse{Instances: entries}); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.x.records {
		if _, err = os.Stat(r.Storage.DataPath); err != nil {
			t.Fatal("external world deleted", err)
		}
	}
}
func TestFactoryResetScopeInconsistencyFailsBeforeMutation(t *testing.T) {
	for _, kind := range []string{"missing_registry", "duplicate_registry", "missing_quadlet", "orphan_quadlet", "unknown_runtime", "migration_marker", "restore_journal", "fixed_service", "fixed_container", "unknown_container", "unknown_unit", "orphan_timer", "loaded_orphan_timer", "uninspectable_storage"} {
		t.Run(kind, func(t *testing.T) {
			f := newFactoryResetFixture(t, 2)
			b := f.x.b
			id := f.x.records[1].Runtime.ID
			switch kind {
			case "missing_registry":
				os.Remove(filepath.Join(b.store.directory, id+".json"))
			case "duplicate_registry":
				b.writeJSON(filepath.Join(b.store.directory, "duplicate.json"), minecraftInstanceRecord{ID: id}, false)
			case "missing_quadlet":
				_, _, q := b.paths(id)
				os.Remove(q)
			case "orphan_quadlet":
				b.writePrivate(filepath.Join(b.quadlets, "minecraft-jv-orphan1.container"), []byte("orphan"), false)
			case "orphan_timer":
				b.writePrivate(filepath.Join(instanceBackupUnits, "minecraft-backup-jv-orphan1.timer"), []byte("orphan"), false)
			case "migration_marker":
				b.writeJSON(b.migrationMarker(id), map[string]string{"id": id}, false)
			case "restore_journal":
				b.writePrivate(filepath.Join(b.root, id, "restore-journal.json"), []byte(`{"schema":1}`), false)
			case "unknown_runtime":
				base := b.command
				b.command = func(ctx context.Context, c string, a ...string) ([]byte, error) {
					if c == "systemctl" && a[0] == "show" && strings.HasSuffix(a[len(a)-1], id+".service") {
						return []byte("LoadState=loaded\nActiveState=activating\n"), nil
					}
					return base(ctx, c, a...)
				}
			default:
				base := b.command
				b.command = func(ctx context.Context, c string, a ...string) ([]byte, error) {
					if kind == "fixed_service" && reflect.DeepEqual(a, []string{"show", "--property=ActiveState", "--value", "--", "minecraft.service"}) {
						return []byte("active"), nil
					}
					if kind == "fixed_container" && c == "podman" && a[0] == "ps" && a[3] == "name=^minecraft$" {
						return []byte("minecraft"), nil
					}
					if kind == "unknown_container" && c == "podman" && a[0] == "ps" && a[3] == "name=^minecraft-jv-" {
						return []byte("minecraft-jv-foreign1"), nil
					}
					if kind == "unknown_unit" && c == "systemctl" && a[0] == "list-units" {
						return []byte("minecraft-jv-foreign1.service loaded active running foreign"), nil
					}
					if kind == "loaded_orphan_timer" && c == "systemctl" && a[0] == "list-units" && a[len(a)-1] == "minecraft-backup-jv-*.timer" {
						return []byte("minecraft-backup-jv-foreign1.timer loaded active waiting foreign"), nil
					}
					if kind == "uninspectable_storage" && c == "nsenter" {
						return nil, errors.New("storage unavailable")
					}
					return base(ctx, c, a...)
				}
			}
			if _, _, err := b.factoryResetParticipants(context.Background()); err == nil {
				t.Fatal("unsafe scope accepted")
			}
			for _, call := range f.x.calls {
				if strings.Contains(call, "systemctl stop") || strings.Contains(call, "podman rm") || strings.Contains(call, "instance-delete remove") {
					t.Fatal("preflight mutated", call)
				}
			}
		})
	}
}
func TestFactoryResetInterruptedParticipantCleanupRetriesExactSet(t *testing.T) {
	f := newFactoryResetFixture(t, 2)
	b := f.x.b
	ctx := context.Background()
	entries, _, err := b.factoryResetParticipants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := factoryFixtureServer(t)
	op, _, _ := s.operations.beginFactoryReset(testFactoryResetFingerprint)
	f.failRemove = entries[1].Retirement.ID
	if err = s.retireFactoryResetInstances(ctx, op.OperationID, adminFactoryResetPlanResponse{Instances: entries}); err == nil {
		t.Fatal("injected failure ignored")
	}
	evidence, err := loadFactoryResetEvidence(s, op.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Entries[0].Retirement.Stage != "identity_retired" || evidence.Entries[1].Retirement.Stage != "runtime_retired" {
		t.Fatal("lost stages", evidence)
	}
	if err = b.checkFactoryResetEvidence(ctx, evidence); err != nil {
		t.Fatal("safe interrupted state stranded", err)
	}
	f.failRemove = ""
	if err = s.retireFactoryResetInstances(ctx, op.OperationID, adminFactoryResetPlanResponse{Instances: entries}); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if f.removals[e.Retirement.ID] != 1 {
			t.Fatal("wrong removal count", f.removals)
		}
	}
}
func TestFactoryResetReviewBindsAllParticipantsWithoutPrivateEvidence(t *testing.T) {
	f := newFactoryResetFixture(t, 2)
	old := runAdminFactoryResetHelper
	t.Cleanup(func() { runAdminFactoryResetHelper = old })
	runAdminFactoryResetHelper = func(context.Context, ...string) ([]byte, error) { return []byte(validFactoryResetPlanHelper), nil }
	s := factoryFixtureServer(t)
	plan, status := s.factoryResetPlan(context.Background())
	if status != 0 {
		t.Fatal(plan)
	}
	raw, _ := json.Marshal(plan)
	for _, private := range []string{"retirement", "rcon_password", f.x.records[0].Runtime.RCONPassword, f.x.records[0].Storage.DataPath, "data_identity"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private evidence exposed", private)
		}
	}
	// Removing the sibling from BOTH authorities must still invalidate Review.
	id := f.x.records[1].Runtime.ID
	os.RemoveAll(filepath.Join(f.x.b.root, id))
	os.Remove(filepath.Join(f.x.b.store.directory, id+".json"))
	_, _, q := f.x.b.paths(id)
	os.Remove(q)
	for _, suffix := range []string{"timer", "service"} {
		os.Remove(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, suffix)))
	}
	f.removed[id] = true
	f.x.records = f.x.records[:1]
	oldMode := readAuthMode
	t.Cleanup(func() { readAuthMode = oldMode })
	readAuthMode = func() (authMode, error) { return authModeSystem, nil }
	body, _ := json.Marshal(adminFactoryResetApplyRequest{PlanFingerprint: plan.PlanFingerprint, ConfirmPlayers: true})
	rr := httptest.NewRecorder()
	s.adminFactoryResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/apply?server="+f.x.records[0].Runtime.ID, string(body)))
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), "stale_plan") {
		t.Fatal(rr.Code, rr.Body.String())
	}
}
func TestFactoryResetReviewBindsSharedCleanupUnderInstanceSummary(t *testing.T) {
	newFactoryResetFixture(t, 1)
	oldHelper := runAdminFactoryResetHelper
	t.Cleanup(func() { runAdminFactoryResetHelper = oldHelper })
	helperPlan := validFactoryResetPlanHelper
	runAdminFactoryResetHelper = func(context.Context, ...string) ([]byte, error) {
		return []byte(helperPlan), nil
	}
	s := factoryFixtureServer(t)
	reviewed, status := s.factoryResetPlan(context.Background())
	if status != 0 {
		t.Fatal(reviewed, status)
	}
	// The managed instance remains internal; only historical cleanup changes.
	helperPlan = strings.Replace(helperPlan, `"data_scope":"internal","data_action":"delete"`, `"data_scope":"external","data_action":"preserve"`, 1)
	current, status := s.factoryResetPlan(context.Background())
	if status != 0 {
		t.Fatal(current, status)
	}
	if current.DataScope != reviewed.DataScope || current.DataAction != reviewed.DataAction {
		t.Fatal("instance summary changed unexpectedly")
	}
	if current.PlanFingerprint == reviewed.PlanFingerprint {
		t.Fatal("shared cleanup policy change did not invalidate Review")
	}
	oldMode := readAuthMode
	t.Cleanup(func() { readAuthMode = oldMode })
	readAuthMode = func() (authMode, error) { return authModeSystem, nil }
	body, _ := json.Marshal(adminFactoryResetApplyRequest{PlanFingerprint: reviewed.PlanFingerprint, ConfirmPlayers: true})
	rr := httptest.NewRecorder()
	s.adminFactoryResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/apply", string(body)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "stale_plan") {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestLegacyFactoryResetRetryDoesNotGuessParticipants(t *testing.T) {
	newFactoryResetFixture(t, 0)
	s := factoryFixtureServer(t)
	op, _, _ := s.operations.beginFactoryReset(testFactoryResetFingerprint)
	s.operations.transition(op.OperationID, operationNeedsAttention, "interrupted", "Interrupted reset.")
	if plan, status := s.factoryResetPlan(context.Background()); status != 409 || plan.OK {
		t.Fatal("legacy ownership invented", plan, status)
	}
}

func TestFactoryResetPlayerAwareShutdownAndDurableGates(t *testing.T) {
	f := newFactoryResetFixture(t, 2)
	b := f.x.b
	id := f.x.records[1].Runtime.ID
	f.x.states[id] = "running"
	base := b.command
	warned := false
	b.pause = func(context.Context, time.Duration) error { return nil }
	b.command = func(ctx context.Context, c string, a ...string) ([]byte, error) {
		if c == "podman" && a[0] == "exec" && a[len(a)-1] == "list" {
			return []byte("There are 1 of a max of 20 players online: Alex"), nil
		}
		if c == "podman" && a[0] == "exec" && strings.HasPrefix(a[len(a)-1], "say ") {
			warned = true
			return nil, nil
		}
		if c == "systemctl" && a[0] == "--no-block" && a[1] == "stop" {
			for _, r := range f.x.records {
				if _, err := b.readPrivate(b.factoryResetMarker(r.Runtime.ID)); err != nil {
					t.Fatal("shutdown before all gates", err)
				}
			}
			f.x.states[id] = "stopped"
			return nil, nil
		}
		return base(ctx, c, a...)
	}
	entries, online, err := b.factoryResetParticipants(context.Background())
	if err != nil || online != 1 {
		t.Fatal(online, err)
	}
	s := factoryFixtureServer(t)
	op, _, _ := s.operations.beginFactoryReset(testFactoryResetFingerprint)
	if err = s.retireFactoryResetInstances(context.Background(), op.OperationID, adminFactoryResetPlanResponse{Instances: entries}); err != nil {
		t.Fatal(err)
	}
	if !warned {
		t.Fatal("online sibling was not warned")
	}
}
func TestFactoryResetRecoveryRejectsAddedOrReplacedParticipant(t *testing.T) {
	for _, kind := range []string{"added", "changed_record", "changed_marker", "changed_data_identity"} {
		t.Run(kind, func(t *testing.T) {
			f := newFactoryResetFixture(t, 2)
			b := f.x.b
			entries, _, err := b.factoryResetParticipants(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			s := factoryFixtureServer(t)
			op, _, _ := s.operations.beginFactoryReset(testFactoryResetFingerprint)
			j := factoryResetInstancesJournal{Schema: 1, OperationID: op.OperationID, Entries: entries}
			if err = b.writeJSON(factoryResetEvidencePath(s, op.OperationID), j, false); err != nil {
				t.Fatal(err)
			}
			id := entries[1].Retirement.ID
			switch kind {
			case "added":
				b.writeJSON(filepath.Join(b.store.directory, "jv-new123.json"), minecraftInstanceRecord{ID: "jv-new123"}, false)
			case "changed_record":
				r := entries[1].Retirement.Record
				r.Runtime.JavaPort++
				b.writeJSON(filepath.Join(b.root, id, "config.json"), r, true)
			case "changed_marker":
				b.writeJSON(b.factoryResetMarker(id), map[string]string{"id": id, "operation_id": migrationRuntimeTestID}, false)
			case "changed_data_identity":
				base := b.command
				b.command = func(ctx context.Context, c string, a ...string) ([]byte, error) {
					if c == "nsenter" && a[2] == instanceDeleteHelper && a[3] == "inspect" && a[4] == id {
						return []byte(`{"identity":"2:3"}`), nil
					}
					return base(ctx, c, a...)
				}
			}
			if err = b.checkFactoryResetEvidence(context.Background(), j); err == nil {
				t.Fatal("replacement accepted")
			}
			for _, call := range f.x.calls {
				if strings.Contains(call, "podman rm") || strings.Contains(call, "systemctl stop") {
					t.Fatal("mutated", call)
				}
			}
		})
	}
}
func TestFactoryResetResolvePreservesUsableRecovery(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			f := newFactoryResetFixture(t, 1)
			b := f.x.b
			entries, _, err := b.factoryResetParticipants(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			s := factoryFixtureServer(t)
			op, _, _ := s.operations.beginFactoryReset(testFactoryResetFingerprint)
			j := factoryResetInstancesJournal{Schema: 1, OperationID: op.OperationID, Entries: entries}
			b.writeJSON(factoryResetEvidencePath(s, op.OperationID), j, false)
			b.writeJSON(b.factoryResetMarker(entries[0].Retirement.ID), map[string]string{"id": entries[0].Retirement.ID, "operation_id": op.OperationID}, false)
			if partial {
				if err = b.retireInstanceRuntime(context.Background(), entries[0].Retirement); err != nil {
					t.Fatal(err)
				}
			}
			err = s.resolveFactoryResetParticipants(context.Background(), op.OperationID)
			if (err != nil) != partial {
				t.Fatal("resolve", partial, err)
			}
			_, markerErr := b.readPrivate(b.factoryResetMarker(entries[0].Retirement.ID))
			if partial && markerErr != nil {
				t.Fatal("partial recovery gate lost")
			}
			if !partial && !errors.Is(markerErr, os.ErrNotExist) {
				t.Fatal("intact server remains gated")
			}
		})
	}
}
