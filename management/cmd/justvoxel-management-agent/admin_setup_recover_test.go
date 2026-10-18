package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setupRecoveryOperation(t *testing.T, state operationState, stage string) (*server, *operationStore, operationJournal) {
	t.Helper()
	store := openTestOperationStore(t)
	op := beginStorageTestOperation(t, store)
	switch state {
	case operationRunning, operationSucceeded:
		if _, err := store.transition(op.OperationID, operationValidating, "validating", "Validating setup."); err != nil {
			t.Fatal(err)
		}
		if state == operationSucceeded {
			if _, err := store.transition(op.OperationID, operationRunning, "running", "Running setup."); err != nil {
				t.Fatal(err)
			}
			if _, err := store.transition(op.OperationID, operationVerifying, "verifying", "Verifying setup."); err != nil {
				t.Fatal(err)
			}
		}
	case operationRollingBack, operationRolledBack:
		if _, err := store.transition(op.OperationID, operationFailed, "setup_failed", "Setup failed."); err != nil {
			t.Fatal(err)
		}
		if state == operationRolledBack {
			if _, err := store.transition(op.OperationID, operationRollingBack, "storage_rollback", "Rolling back storage."); err != nil {
				t.Fatal(err)
			}
		}
	}
	if state != operationQueued {
		if _, err := store.transition(op.OperationID, state, stage, "Setup stopped."); err != nil {
			t.Fatal(err)
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, store)
	return s, store, op
}

func TestAdminSetupRecoverRequiresAdministratorAndExactState(t *testing.T) {
	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, openTestOperationStore(t))
		rr := httptest.NewRecorder()
		s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"00000000-0000-4000-8000-000000000000","retry_recovery":true}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d", role, rr.Code)
		}
	}
	for _, tc := range []struct {
		state operationState
		stage string
	}{
		{operationNeedsAttention, "runtime_unknown"},
		{operationFailed, "storage_rollback"},
		{operationRollingBack, "storage_rollback"},
		{operationRunning, "storage_rollback"},
		{operationQueued, "queued"},
		{operationSucceeded, "complete"},
		{operationRolledBack, "setup_rolled_back"},
	} {
		t.Run(string(tc.state)+"_"+tc.stage, func(t *testing.T) {
			s, _, op := setupRecoveryOperation(t, tc.state, tc.stage)
			rr := httptest.NewRecorder()
			s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"`+op.OperationID+`","retry_recovery":true}`))
			want := http.StatusConflict
			if tc.state == operationSucceeded || tc.state == operationRolledBack {
				want = http.StatusNotFound
			}
			if rr.Code != want {
				t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
	s, _, op := setupRecoveryOperation(t, operationNeedsAttention, "storage_rollback")
	for _, body := range []string{
		`{}`,
		`{"operation_id":"` + op.OperationID + `"}`,
		`{"operation_id":"` + op.OperationID + `","retry_recovery":false}`,
		`{"operation_id":"` + op.OperationID + `","retry_recovery":"true"}`,
		`{"operation_id":"00000000-0000-4000-8000-000000000000"}`,
		`{"operation_id":"` + op.OperationID + `","retry_recovery":true,"unexpected":true}`,
	} {
		rr := httptest.NewRecorder()
		s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("request %s status = %d", body, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"00000000-0000-4000-8000-000000000000","retry_recovery":true}`))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("noncurrent operation status = %d", rr.Code)
	}
}

func TestAdminSetupRecoverSuccessReleasesSetupLock(t *testing.T) {
	s, store, op := setupRecoveryOperation(t, operationNeedsAttention, "storage_rollback")
	old := runAdminSetupStorageTransactionHelper
	defer func() { runAdminSetupStorageTransactionHelper = old }()
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, body []byte) ([]byte, error) {
		if action != "recover" || !strings.Contains(string(body), op.OperationID) || !strings.Contains(string(body), testSetupFingerprint) || strings.Contains(string(body), "smb_password") {
			t.Fatalf("invalid recovery request: %s %s", action, body)
		}
		return []byte(`{"ok":true,"applied":false,"phase":"storage_rolled_back","rollback_state":"succeeded","rollback_result":"rolled_back"}`), nil
	}
	rr := httptest.NewRecorder()
	s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"`+op.OperationID+`","retry_recovery":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var result adminOperationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || result.Operation == nil || result.Operation.State != operationRolledBack || result.Operation.Stage != "setup_rolled_back" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	current, err := store.currentSetup()
	if err != nil || current != nil {
		t.Fatalf("setup lock retained: %#v, %v", current, err)
	}
	if _, created, err := store.beginSetup(testSetupFingerprint); err != nil || !created {
		t.Fatalf("setup lock was not released: created=%v err=%v", created, err)
	}
}

func TestAdminSetupRecoverFailureKeepsNeedsAttention(t *testing.T) {
	s, store, op := setupRecoveryOperation(t, operationNeedsAttention, "storage_rollback")
	old := runAdminSetupStorageTransactionHelper
	defer func() { runAdminSetupStorageTransactionHelper = old }()
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, body []byte) ([]byte, error) {
		return []byte(`{"ok":false,"applied":false,"phase":"storage_rollback","rollback_state":"failed","rollback_result":"needs_attention","error":"conflicting fstab"}`), nil
	}
	rr := httptest.NewRecorder()
	s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"`+op.OperationID+`","retry_recovery":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	current, err := store.currentSetup()
	if err != nil || current == nil || current.State != operationNeedsAttention || current.Stage != "storage_rollback" || current.Rollback.State != "failed" || current.Rollback.Result != "needs_attention" {
		t.Fatalf("unexpected current setup: %#v, %v", current, err)
	}
}

func TestInterruptedSetupStorageRecoveryReloadKeepsLock(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	op := beginStorageTestOperation(t, first)
	if _, err := first.transition(op.OperationID, operationNeedsAttention, "storage_rollback", "Storage needs attention."); err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(op.OperationID, operationRollingBack, "storage_recovery", "Retrying storage recovery."); err != nil {
		t.Fatal(err)
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}

	second, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	current, err := second.currentSetup()
	if err != nil || current == nil || current.OperationID != op.OperationID || current.State != operationNeedsAttention || current.Stage != "storage_rollback" || current.Rollback.State != "failed" || current.Rollback.Result != "needs_attention" || current.InterruptedAt == "" {
		t.Fatalf("reloaded setup = %#v, err = %v", current, err)
	}
	if !second.setupLockHeld {
		t.Fatal("setup lock was not retained")
	}
	otherFingerprint := "sha256:" + strings.Repeat("b", 64)
	if _, _, err := second.beginSetup(otherFingerprint); err != errSetupOperationBusy {
		t.Fatalf("new setup error = %v, want setup busy", err)
	}
}

func TestRuntimeSetupRecoverySequenceAndLock(t *testing.T) {
	for _, outcome := range []string{"success", "legacy_success", "legacy_runtime_failed", "runtime_failed", "runtime_interrupted", "runtime_unverified", "storage_failed"} {
		t.Run(outcome, func(t *testing.T) {
			s, store, op := setupRecoveryOperation(t, operationNeedsAttention, "runtime_rollback")
			oldRuntime, oldStorage := runAdminSetupRuntimeTransactionHelper, runAdminSetupStorageTransactionHelper
			defer func() {
				runAdminSetupRuntimeTransactionHelper, runAdminSetupStorageTransactionHelper = oldRuntime, oldStorage
			}()
			var events []string
			runAdminSetupRuntimeTransactionHelper = func(ctx context.Context, action string, body []byte) ([]byte, []byte, error) {
				events = append(events, "runtime:"+action)
				var identity map[string]string
				if err := json.Unmarshal(body, &identity); err != nil || len(identity) != 3 || identity["operation_id"] != op.OperationID || identity["plan_fingerprint"] != op.PlanFingerprint {
					t.Fatalf("invalid recovery identity: %s", body)
				}
				if !store.setupLockHeld {
					t.Fatal("lock released before runtime verification")
				}
				switch outcome {
				case "runtime_failed", "legacy_runtime_failed":
					return []byte(`{"ok":false,"applied":false,"phase":"runtime_rollback","error":"changed data identity"}`), nil, nil
				case "runtime_interrupted":
					return nil, nil, context.DeadlineExceeded
				case "runtime_unverified":
					return []byte(`{"ok":true,"applied":true,"phase":"runtime_rolled_back"}`), nil, nil
				}
				if outcome == "legacy_success" {
					return []byte(`{"ok":true,"applied":false,"phase":"runtime_rolled_back","evidence":{"recovery_mode":"legacy_verified_clean","firewall_recovery":"preserved_unknown_ports"}}`), nil, nil
				}
				return []byte(`{"ok":true,"applied":false,"phase":"runtime_rolled_back"}`), nil, nil
			}
			runAdminSetupStorageTransactionHelper = func(ctx context.Context, action string, body []byte) ([]byte, error) {
				events = append(events, "storage:"+action)
				if !store.setupLockHeld {
					t.Fatal("lock released before storage verification")
				}
				journal, _ := store.get(op.OperationID)
				if journal.Stage != "storage_recovery" {
					t.Fatalf("stage = %s", journal.Stage)
				}
				if outcome == "storage_failed" {
					return []byte(`{"ok":false,"applied":false,"phase":"storage_rollback","rollback_state":"failed","rollback_result":"needs_attention","error":"changed mount"}`), nil
				}
				return []byte(`{"ok":true,"applied":false,"phase":"storage_rolled_back","rollback_state":"succeeded","rollback_result":"rolled_back"}`), nil
			}
			rr := httptest.NewRecorder()
			s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"`+op.OperationID+`","retry_recovery":true}`))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
			}
			journal, err := store.get(op.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "success" || outcome == "legacy_success" {
				if journal.State != operationRolledBack || store.setupLockHeld {
					t.Fatalf("incomplete recovery: %#v", journal)
				}
				if strings.Join(events, ",") != "runtime:recover,storage:recover" {
					t.Fatalf("sequence = %v", events)
				}
				if _, created, err := store.beginSetup("sha256:" + strings.Repeat("c", 64)); err != nil || !created {
					t.Fatalf("restart: %v %v", created, err)
				}
			} else {
				stage := "runtime_rollback"
				if outcome == "storage_failed" {
					stage = "storage_rollback"
				} else if len(events) != 1 {
					t.Fatalf("storage ran without runtime verification: %v", events)
				}
				if journal.State != operationNeedsAttention || journal.Stage != stage || !store.setupLockHeld {
					t.Fatalf("unsafe failed recovery: %#v", journal)
				}
				if outcome == "legacy_runtime_failed" {
					if !strings.Contains(journal.Status, "changed data identity") {
						t.Fatalf("concrete recovery failure missing: %s", journal.Status)
					}
					// The same operation can retry after the unsafe condition clears.
					outcome = "legacy_success"
					events = nil
					retry := httptest.NewRecorder()
					s.adminSetupRecover(retry, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"`+op.OperationID+`","retry_recovery":true}`))
					journal, err = store.get(op.OperationID)
					if retry.Code != http.StatusOK || err != nil || journal.State != operationRolledBack || store.setupLockHeld || strings.Join(events, ",") != "runtime:recover,storage:recover" {
						t.Fatalf("legacy retry did not finish safely: status=%d operation=%#v events=%v err=%v", retry.Code, journal, events, err)
					}
				}
			}
		})
	}
}

func TestInterruptedRuntimeSetupRecoveryReload(t *testing.T) {
	for _, stage := range []string{"runtime_recovery", "runtime_rollback", "runtime_config", "minecraft_verify", "final_validation"} {
		t.Run(stage, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "management")
			store, err := openOperationStore(base)
			if err != nil {
				t.Fatal(err)
			}
			op := beginStorageTestOperation(t, store)
			if _, err := store.transition(op.OperationID, operationNeedsAttention, "runtime_rollback", "Runtime needs attention."); err != nil {
				t.Fatal(err)
			}
			if _, err := store.transition(op.OperationID, operationRollingBack, "runtime_recovery", "Retrying runtime recovery."); err != nil {
				t.Fatal(err)
			}
			if _, err := store.updateProgress(op.OperationID, operationRollingBack, stage, "Recovery in progress."); err != nil {
				t.Fatal(err)
			}
			if err := store.close(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := openOperationStore(base)
			if err != nil {
				t.Fatal(err)
			}
			defer reloaded.close()
			current, err := reloaded.currentSetup()
			if err != nil || current == nil || current.Stage != "runtime_rollback" || current.State != operationNeedsAttention || current.InterruptedAt == "" || !reloaded.setupLockHeld {
				t.Fatalf("recovery state: %#v %v", current, err)
			}
			if _, err := reloaded.transition(op.OperationID, operationRollingBack, "runtime_recovery", "Retrying preserved recovery."); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetupRecoveryRetriesNormalDeleteBusy(t *testing.T) {
	for _, mode := range []string{"transient", "persistent", "resume-journal"} {
		t.Run(mode, func(t *testing.T) {
			d := newDeleteFixture(t)
			f := d.f
			s, store, op := setupRecoveryOperation(t, operationNeedsAttention, "runtime_rollback")
			r := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: op.OperationID, PlanFingerprint: op.PlanFingerprint}
			e := bindSetupRuntimeFixture(t, f, r)
			id, sibling := e.Record.Runtime.ID, f.configs[1].ID
			_, siblingEnv, siblingQuadlet := f.backend.paths(sibling)
			preserved := map[string]string{}
			for _, path := range []string{filepath.Join(f.backend.store.directory, sibling+".json"), filepath.Join(f.backend.store.directory, "names", sibling+".json"), filepath.Join(f.backend.root, sibling, "config.json"), siblingEnv, siblingQuadlet, filepath.Join(instanceBackupUnits, instanceBackupUnit(sibling, "timer")), filepath.Join(instanceBackupUnits, instanceBackupUnit(sibling, "service"))} {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				preserved[path] = string(raw)
			}
			siblingState, siblingContainer := f.states[sibling], f.containers[sibling]
			if mode == "resume-journal" {
				d.failRemove = true
				if err := f.backend.deleteInstance(context.Background(), id, "JV Paper"); err == nil {
					t.Fatal("expected interrupted normal Delete")
				}
				d.failRemove = false
			}
			base := f.backend.command
			busy, busyChecks, waits := true, 0, 0
			journalSeen := false
			f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "systemctl" && reflect.DeepEqual(args, []string{"show", "--property=Job", "--value", "--", "minecraft-" + id + ".service"}) && busy {
					busyChecks++
					return []byte("42\n"), nil // Normal deleteStopped safety check returns errInstanceBusy.
				}
				if command == "nsenter" && len(args) > 4 && args[2] == instanceDeleteHelper && args[3] == "remove" {
					journal, err := f.backend.deleteJournal(id)
					if err != nil || journal.ID != id || (journal.Stage != "runtime_retired" && journal.Stage != "data_removed") {
						t.Fatal("storage removal without durable Delete evidence", journal, err)
					}
					journalSeen = true
				}
				return base(ctx, command, args...)
			}
			f.backend.pause = func(ctx context.Context, duration time.Duration) error {
				if duration != 250*time.Millisecond || !store.setupLockHeld || busyChecks == 0 {
					t.Fatal("invalid busy retry", duration, busyChecks)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
					t.Fatal("unbounded Delete retry")
				}
				if mode != "resume-journal" {
					if _, err := os.Stat(f.backend.deletePath(id)); !os.IsNotExist(err) {
						t.Fatal("busy check must precede journal creation", err)
					}
				}
				waits++
				if mode != "persistent" && waits == 2 {
					busy = false
				}
				return nil
			}
			oldStorage := runAdminSetupStorageTransactionHelper
			t.Cleanup(func() { runAdminSetupStorageTransactionHelper = oldStorage })
			storageRecovered := false
			runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
				recovered, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
				if action != "recover" || !d.removed || !journalSeen || err != nil || !recovered.RolledBack || !store.setupLockHeld {
					t.Fatal("storage recovery before verified runtime retirement", recovered, err)
				}
				storageRecovered = true
				return []byte(`{"ok":true,"applied":false,"phase":"storage_rolled_back","rollback_state":"succeeded","rollback_result":"rolled_back"}`), nil
			}
			rr := httptest.NewRecorder()
			s.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"`+op.OperationID+`","retry_recovery":true}`))
			if rr.Code != http.StatusOK {
				t.Fatal(rr.Code, rr.Body.String())
			}
			finished, err := store.get(op.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			current, err := store.currentSetup()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "persistent" {
				if waits != 40 || finished.State != operationNeedsAttention || finished.Stage != "runtime_rollback" || finished.Rollback.State != "failed" || current == nil || !store.setupLockHeld || d.removed || storageRecovered {
					t.Fatal("persistent busy lost recovery", waits, finished, current)
				}
				recovered, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
				if err != nil || recovered.RolledBack {
					t.Fatal("lost Setup evidence", recovered, err)
				}
				_, env, quadlet := f.backend.paths(id)
				for _, path := range []string{setupInstanceMarker, setupInstancePath(op.OperationID), filepath.Join(f.backend.root, id, "config.json"), env, quadlet, filepath.Join(f.backend.store.directory, id+".json"), filepath.Join(f.backend.store.directory, "names", id+".json")} {
					if _, err := os.Stat(path); err != nil {
						t.Fatal("lost recovery evidence", path, err)
					}
				}
			} else if waits != 2 || finished.State != operationRolledBack || current != nil || store.setupLockHeld || !storageRecovered || !journalSeen {
				t.Fatal("transient busy stranded Setup", waits, finished, current)
			}
			if mode != "persistent" {
				for _, path := range []string{setupInstanceMarker, f.backend.deletePath(id), filepath.Join(f.backend.root, id), filepath.Join(f.backend.store.directory, id+".json")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("retirement incomplete", path, err)
					}
				}
			}
			for path, expected := range preserved {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != expected {
					t.Fatal("sibling changed", path, err)
				}
			}
			if f.states[sibling] != siblingState || f.containers[sibling] != siblingContainer {
				t.Fatal("sibling runtime changed")
			}
		})
	}
}
