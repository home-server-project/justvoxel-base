package main

import (
	"context"
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

func TestFreshSetupCreatesOneNormalInstanceThroughSharedCreation(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint("existing-policy-root=", existing), func(t *testing.T) {
			for _, name := range []string{"", "Family Survival"} {
				t.Run("name="+name, func(t *testing.T) { testFreshSetupCreatesOneNormalInstance(t, existing, name) })
			}
		})
	}
}
func testFreshSetupCreatesOneNormalInstance(t *testing.T, existing bool, name string) {
	f := newInstanceActivationFixture(t)
	// A fresh registry has no minecraft.json or pre-existing server records.
	f.backend.store = testMinecraftInstances(t)
	registry, err := f.backend.store.openDirectory(true)
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
	for _, c := range f.configs {
		dir, _, _ := f.backend.paths(c.ID)
		if err = os.Remove(filepath.Join(dir, "config.json")); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(dir); err != nil {
			t.Fatal(err)
		}
	}
	old, oldPolicy, oldMarker := instanceRuntimes, setupPolicyRoot, setupInstanceMarker
	instanceRuntimes = f.backend
	setupPolicyRoot = filepath.Join(t.TempDir(), "justvoxel")
	if existing {
		if err := os.Mkdir(setupPolicyRoot, 0700); err != nil {
			t.Fatal(err)
		}
	}
	setupInstanceMarker = filepath.Join(t.TempDir(), "setup-in-progress")
	t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldPolicy; setupInstanceMarker = oldMarker })
	r := setupRuntimeTransactionRequest{InstanceOnly: true, SchemaVersion: operationSchemaVersion, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint,
		Server:    setupRuntimeServer{DisplayName: name, MOTD: "Family", MaxPlayers: 20, Timezone: "UTC"},
		Minecraft: setupRuntimeMinecraft{ServerType: "paper", GameMode: "survival", JavaMemory: "1G", ContainerMemory: "2G", JavaPort: instanceTestPort(t), BedrockPort: 19132, ImageTag: "stable", VersionPolicy: "pinned", Version: "1.21.4", MinecraftUID: 1000, MinecraftGID: 1000},
		Storage:   setupRuntimeStorage{Type: "system", Path: filepath.Join(f.snapshot.StorageRoot, "minecraft")}, Backups: setupRuntimeBackups{Type: "system", Path: f.snapshot.BackupPath, Keep: 7, Schedule: "*-*-* 04:30:00"}}
	for _, action := range []string{"validate", "apply"} {
		response, err := runSetupInstanceAction(context.Background(), action, r)
		if err != nil || !response.OK {
			t.Fatal(action, response, err)
		}
	}
	e, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	id := e.Record.Runtime.ID
	if !instanceIDPattern.MatchString(id) {
		t.Fatal("first server has no generated immutable ID")
	}
	record, err := f.backend.readRecord(id)
	if err != nil || f.backend.verifyLive(record) != nil {
		t.Fatal("first server is not a committed normal runtime", err)
	}
	if record.Storage.DataPath != filepath.Join(f.snapshot.StorageRoot, "minecraft-instances", id, "data") || !record.Runtime.StartOnBoot || len(record.Firewall) == 0 {
		t.Fatal("first server skipped normal activation", record.Storage)
	}
	expectedName := name
	if name == "" {
		expectedName = instanceDefaultName("paper")
	}
	registered, err := f.backend.identities().listInstances("")
	if err != nil || len(registered) != 1 || registered[0].ID != id || registered[0].DisplayName != expectedName {
		t.Fatal("wrong registry", registered, err)
	}
	if fixed, err := f.backend.identities().read(); err != nil || fixed.ID != "" {
		t.Fatal("created minecraft.json", fixed, err)
	}
	_, env, quadlet := f.backend.paths(id)
	for _, path := range []string{env, quadlet} {
		raw, err := f.backend.readPrivate(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "ContainerName=minecraft\n") || strings.Contains(string(raw), "/etc/justvoxel/minecraft.env") {
			t.Fatal("fixed runtime artifact", path)
		}
	}
	for _, path := range []string{filepath.Join(setupPolicyRoot, "minecraft.env"), filepath.Join(f.backend.quadlets, "minecraft.container")} {
		if _, err = os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("fixed runtime file created", path)
		}
	}
}

func TestNewSetupRecoveryBeforeRuntimePublicationIsSafeNoOp(t *testing.T) {
	f := newInstanceActivationFixture(t)
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	response, handled, err := recoverSetupInstance(context.Background(), operationJournal{InstanceSetup: true, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint})
	if err != nil || !handled || !response.OK || response.Phase != "runtime_rolled_back" {
		t.Fatal(response, handled, err)
	}
}

func TestSetupPolicyPublicationAndBoundedRollback(t *testing.T) {
	for _, mode := range []string{"fresh", "existing", "unrelated", "old-evidence"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			old, oldRoot, oldMarker := instanceRuntimes, setupPolicyRoot, setupInstanceMarker
			instanceRuntimes = f.backend
			setupPolicyRoot = filepath.Join(t.TempDir(), "justvoxel")
			setupInstanceMarker = filepath.Join(t.TempDir(), "setup-in-progress")
			t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldRoot; setupInstanceMarker = oldMarker })
			if mode == "existing" || mode == "old-evidence" {
				if err := os.Mkdir(setupPolicyRoot, 0700); err != nil {
					t.Fatal(err)
				}
			}
			r := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint}
			if _, err := runSetupInstanceAction(context.Background(), "validate", r); err != nil {
				t.Fatal(err)
			}
			e, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
			if err != nil || e.HadPolicyRoot == nil || *e.HadPolicyRoot != (mode == "existing" || mode == "old-evidence") {
				t.Fatal(e, err)
			}
			// Stop after actual policy publication, before any server is created.
			f.backend.snapshot = func(context.Context) (minecraftInstanceRuntimeContext, uint32, uint32, error) {
				return minecraftInstanceRuntimeContext{}, 0, 0, fmt.Errorf("injected after publication")
			}
			if _, err = runSetupInstanceAction(context.Background(), "apply", r); err == nil || !strings.Contains(err.Error(), "injected after publication") {
				t.Fatal("did not reach publication", err)
			}
			for name, expected := range e.Policy {
				raw, err := f.backend.readPrivate(filepath.Join(setupPolicyRoot, name))
				if err != nil || string(raw) != string(expected) {
					t.Fatal(name, err)
				}
			}
			if mode == "unrelated" {
				if err = os.WriteFile(filepath.Join(setupPolicyRoot, "keep"), []byte("unrelated"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "old-evidence" {
				e.HadPolicyRoot = nil
				if err = f.backend.writeJSON(setupInstancePath(r.OperationID), e, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = runSetupInstanceAction(context.Background(), "rollback", r); err != nil {
				t.Fatal(err)
			}
			for name := range e.Policy {
				if _, err = os.Lstat(filepath.Join(setupPolicyRoot, name)); !os.IsNotExist(err) {
					t.Fatal("policy survived rollback", name, err)
				}
			}
			_, err = os.Lstat(setupPolicyRoot)
			if mode == "fresh" {
				if !os.IsNotExist(err) {
					t.Fatal("new empty root survived", err)
				}
			} else if err != nil {
				t.Fatal("pre-existing or occupied directory lost", err)
			}
			if mode == "unrelated" {
				raw, err := os.ReadFile(filepath.Join(setupPolicyRoot, "keep"))
				if err != nil || string(raw) != "unrelated" {
					t.Fatal("unrelated content lost", err)
				}
			}
			if _, err = runSetupInstanceAction(context.Background(), "rollback", r); err != nil {
				t.Fatal("rollback not repeatable", err)
			}
		})
	}
}

func TestSetupRejectsUnsafePolicyRootAndPreservesUnexpectedFiles(t *testing.T) {
	for _, mode := range []string{"symlink", "unsafe", "changed-file"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			old, oldRoot := instanceRuntimes, setupPolicyRoot
			instanceRuntimes = f.backend
			setupPolicyRoot = filepath.Join(t.TempDir(), "justvoxel")
			t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldRoot })
			r := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint}
			switch mode {
			case "symlink":
				if err := os.Symlink(t.TempDir(), setupPolicyRoot); err != nil {
					t.Fatal(err)
				}
			case "unsafe":
				if err := os.Mkdir(setupPolicyRoot, 0777); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(setupPolicyRoot, 0777); err != nil {
					t.Fatal(err)
				}
			}
			_, err := runSetupInstanceAction(context.Background(), "validate", r)
			if mode != "changed-file" {
				if err == nil {
					t.Fatal("unsafe root accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			dir, err := f.backend.privateDirectory(setupPolicyRoot, true)
			if err != nil {
				t.Fatal(err)
			}
			dir.Close()
			path := filepath.Join(setupPolicyRoot, "justvoxel.conf")
			if err = f.backend.writePrivate(path, []byte("unexpected"), false); err != nil {
				t.Fatal(err)
			}
			if _, err = runSetupInstanceAction(context.Background(), "rollback", r); err == nil {
				t.Fatal("rollback removed unproven policy")
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != "unexpected" {
				t.Fatal(raw, err)
			}
		})
	}
}

func TestWritePrivateStillRejectsMissingParent(t *testing.T) {
	f := newInstanceActivationFixture(t)
	parent := filepath.Join(t.TempDir(), "missing")
	if err := f.backend.writePrivate(filepath.Join(parent, "policy"), []byte("value"), false); !os.IsNotExist(err) {
		t.Fatal("generic publication created parent", err)
	}
	if _, err := os.Lstat(parent); !os.IsNotExist(err) {
		t.Fatal("missing parent was created", err)
	}
}

// Bind a fixture runtime to protected Setup evidence. All recovery targets
// come from this operation-bound record.
func bindSetupRuntimeFixture(t *testing.T, f *instanceActivationFixture, r setupRuntimeTransactionRequest) setupInstanceEvidence {
	t.Helper()
	old, oldRoot, oldMarker := instanceRuntimes, setupPolicyRoot, setupInstanceMarker
	instanceRuntimes = f.backend
	setupPolicyRoot = t.TempDir()
	setupInstanceMarker = filepath.Join(t.TempDir(), "setup-in-progress")
	t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldRoot; setupInstanceMarker = oldMarker })
	record, err := f.backend.readRecord(f.configs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	e := setupInstanceEvidence{Schema: 1, Request: r, Record: record, Policy: setupInstancePolicy(r), HadInstancesRoot: true}
	dir, err := f.backend.privateDirectory(f.backend.staging, true)
	if err != nil {
		t.Fatal(err)
	}
	dir.Close()
	if err = f.backend.writeJSON(setupInstancePath(r.OperationID), e, false); err != nil {
		t.Fatal(err)
	}
	for name, value := range e.Policy {
		if err = f.backend.writePrivate(filepath.Join(setupPolicyRoot, name), value, false); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.backend.writePrivate(setupInstanceMarker, []byte(r.OperationID+" "+r.PlanFingerprint+"\n"), false); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSetupSlowStartReachesNormalCommit(t *testing.T) {
	f := newInstanceBackupFixture(t)
	store := openTestOperationStore(t)
	op := beginStorageTestOperation(t, store)
	plan := setupPlanForRuntimeTest(t)
	plan.Backups.Automatic = true
	plan.Backups.Schedule = "*-*-* 06:30:00"
	plan.Backups.Keep = 9
	r, err := setupRuntimeRequestForOperation(op, plan)
	if err != nil {
		t.Fatal(err)
	}
	e := bindSetupRuntimeFixture(t, f.instanceActivationFixture, r)
	id := e.Record.Runtime.ID
	oldStorage := runAdminSetupStorageTransactionHelper
	t.Cleanup(func() { runAdminSetupStorageTransactionHelper = oldStorage })
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action == "validate" {
			return []byte(`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"}`), nil
		}
		if action == "apply" {
			return []byte(`{"ok":true,"applied":true,"phase":"storage_verified","rollback_state":"not_started","rollback_result":""}`), nil
		}
		t.Fatal(action)
		return nil, nil
	}
	// Publication is covered above; run the real Verify and Commit under the
	// actual persistent-operation orchestrator, including its action deadlines.
	runSetupInstanceRuntimeAction = func(ctx context.Context, action string, request setupRuntimeTransactionRequest) (setupRuntimeTransactionResponse, error) {
		if action == "validate" || action == "apply" {
			return setupRuntimeTransactionResponse{OK: true, Applied: action == "apply"}, nil
		}
		return runSetupInstanceAction(ctx, action, request)
	}
	base := f.backend.command
	started, verified, pauses := false, false, 0
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "systemctl" && reflect.DeepEqual(args, []string{"--no-block", "start", "--", "minecraft-" + id + ".service"}) {
			started = true
			f.states[id] = "activating"
			return nil, nil
		}
		if command == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" {
			if !started || pauses < 3 || f.states[id] != "active" || !reflect.DeepEqual(args, []string{"--instance", id, r.Minecraft.Version}) {
				t.Fatal("verification before readiness", args)
			}
			verified = true
			return nil, nil
		}
		return base(ctx, command, args...)
	}
	f.backend.pause = func(ctx context.Context, _ time.Duration) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 19*time.Minute {
			t.Fatal("first-boot verify allowance shortened")
		}
		if verified {
			t.Fatal("unexpected wait after verification")
		}
		if _, err := os.Stat(f.backend.backupPath(id, "settings")); !os.IsNotExist(err) {
			t.Fatal("backup settings created before commit")
		}
		pauses++
		if pauses == 3 {
			f.states[id] = "active"
			f.containers[id] = "running"
		}
		return ctx.Err()
	}
	if err = executeSetupTransaction(context.Background(), store, op.OperationID, plan, nil); err != nil {
		t.Fatal(err)
	}
	finished, err := store.get(op.OperationID)
	if err != nil || finished.State != operationSucceeded || !verified {
		t.Fatal(finished, err)
	}
	if current, err := store.currentSetup(); err != nil || current != nil {
		t.Fatal("unfinished setup", current, err)
	}
	if _, err := os.Stat(setupInstanceMarker); !os.IsNotExist(err) {
		t.Fatal("setup marker survived commit", err)
	}
	settings, err := f.backend.backupSettings(id)
	if err != nil || !settings.Enabled || settings.Time != "06:30" || settings.Retention != 9 || !f.timerEnabled[instanceBackupUnit(id, "timer")] {
		t.Fatal(settings, err)
	}
}

func TestSetupStartupFailsClosed(t *testing.T) {
	for _, mode := range []string{"failed", "timeout", "cancelled", "stopping", "configured", "stopped-after-starting", "submission-gap"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			id := f.configs[0].ID
			allowance := time.Minute
			if mode == "timeout" {
				allowance = time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), allowance)
			defer cancel()
			f.states[id] = "activating"
			pauses := 0
			if mode == "failed" {
				f.states[id] = "failed"
			}
			if mode == "stopping" {
				f.states[id] = "deactivating"
			}
			if mode == "configured" {
				delete(f.states, id)
			}
			if mode == "submission-gap" {
				f.states[id] = "inactive"
			}
			f.backend.pause = func(context.Context, time.Duration) error {
				pauses++
				switch mode {
				case "timeout":
					<-ctx.Done()
					return ctx.Err()
				case "cancelled":
					cancel()
					return ctx.Err()
				case "stopped-after-starting":
					f.states[id] = "inactive"
				case "submission-gap":
					f.states[id] = "active"
					f.containers[id] = "running"
				default:
					t.Fatal("unsafe state was retried")
				}
				return nil
			}
			err := f.backend.waitSetupInstanceRunning(ctx, id)
			if mode == "submission-gap" {
				if err != nil || pauses != 1 {
					t.Fatal(err, pauses)
				}
				return
			}
			var safe *instanceRuntimeSafeError
			if !errors.As(err, &safe) {
				t.Fatal("missing safe failure", err)
			}
			if mode == "failed" && pauses != 0 {
				t.Fatal("failed startup did not fail promptly")
			}
		})
	}
}

func TestExplicitSetupRecoveryStopsTrackedInstanceBeforeActivationRecovery(t *testing.T) {
	for _, mode := range []string{"starting", "running", "players-online", "players-unknown", "stop-error", "still-starting", "settling"} {
		t.Run(mode, func(t *testing.T) {
			d := newDeleteFixture(t)
			f := d.f
			server, store, op := setupRecoveryOperation(t, operationNeedsAttention, "runtime_rollback")
			r := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: op.OperationID, PlanFingerprint: op.PlanFingerprint}
			e := bindSetupRuntimeFixture(t, f, r)
			id, sibling := e.Record.Runtime.ID, f.configs[1].ID
			// Valid pending activation evidence must still go through recover().
			_, env, quadlet := f.backend.paths(id)
			envBytes, err := f.backend.readPrivate(env)
			if err != nil {
				t.Fatal(err)
			}
			quadletBytes, err := f.backend.readPrivate(quadlet)
			if err != nil {
				t.Fatal(err)
			}
			pending := instanceActivationEvidence{SchemaVersion: 1, ID: id, Previous: e.Record, Target: e.Record, Environment: envBytes, Quadlet: quadletBytes}
			dir, _, _ := f.backend.paths(id)
			if err = f.backend.writeJSON(filepath.Join(dir, "pending.json"), pending, false); err != nil {
				t.Fatal(err)
			}
			f.states[id] = "activating"
			if mode == "running" || mode == "players-online" || mode == "players-unknown" {
				f.states[id] = "active"
				f.containers[id] = "running"
			}
			if mode == "players-online" {
				f.players = "There are 1 of a max of 20 players online: Alex"
			}
			if mode == "players-unknown" {
				f.players = "unavailable"
			}
			base := f.backend.command
			stopped := false
			elapsed := time.Duration(0)
			warnings := []string{}
			settlePauses := 0
			siblingState, siblingContainer := f.states[sibling], f.containers[sibling]
			f.backend.pause = func(ctx context.Context, duration time.Duration) error {
				if mode == "still-starting" || mode == "settling" {
					if !stopped || duration != 250*time.Millisecond {
						t.Fatal("invalid settle wait", stopped, duration)
					}
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
						t.Fatal("unbounded settle wait")
					}
					settlePauses++
					if mode == "settling" {
						f.states[id] = "deactivating"
						if settlePauses == 3 {
							f.states[id] = "inactive"
							f.containers[id] = "exited"
						}
					}
					return nil
				}
				if mode != "players-online" || !stopped {
					t.Fatal("unexpected recovery countdown")
				}
				elapsed += duration
				if elapsed >= 60*time.Second {
					f.states[id] = "inactive"
					f.containers[id] = "exited"
				}
				return nil
			}
			f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "podman" && len(args) == 4 && args[0] == "exec" && args[2] == "rcon-cli" {
					if args[1] != "minecraft-"+id {
						t.Fatal("player safety escaped target", args)
					}
					if strings.HasPrefix(args[3], "say ") {
						warnings = append(warnings, args[3])
						return nil, nil
					}
				}
				if command == "systemctl" && reflect.DeepEqual(args, []string{"--no-block", "stop", "--", "minecraft-" + id + ".service"}) {
					if mode != "players-online" || len(warnings) != 1 || warnings[0] != "say Server shutting down in 60 seconds" {
						t.Fatal("shutdown requested without initial player warning")
					}
					stopped = true
					f.states[id] = "deactivating"
					return nil, nil
				}
				if command == "systemctl" && reflect.DeepEqual(args, []string{"stop", "--", "minecraft-" + id + ".service"}) {
					if mode == "players-online" || mode == "players-unknown" {
						t.Fatal("unsafe immediate interruption")
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("unbounded rollback stop")
					}
					if mode == "running" {
						if deadline, _ := ctx.Deadline(); time.Until(deadline) > 150*time.Second {
							t.Fatal("zero-player stop exceeded normal safety timeout")
						}
					}
					if mode == "stop-error" {
						return nil, fmt.Errorf("injected stop failure")
					}
					stopped = true
					if mode == "still-starting" || mode == "settling" {
						return nil, nil
					}
				}
				if command == "systemctl" && len(args) == 1 && args[0] == "daemon-reload" && (!stopped || f.states[id] != "inactive" || f.containers[id] == "running") {
					t.Fatal("activation recovery before stop")
				}
				return base(ctx, command, args...)
			}
			oldStorage := runAdminSetupStorageTransactionHelper
			t.Cleanup(func() { runAdminSetupStorageTransactionHelper = oldStorage })
			runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
				if action != "recover" || !stopped || !d.removed {
					t.Fatal("storage recovery before runtime rollback")
				}
				return []byte(`{"ok":true,"applied":false,"phase":"storage_rolled_back","rollback_state":"succeeded","rollback_result":"rolled_back"}`), nil
			}
			rr := httptest.NewRecorder()
			server.adminSetupRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/recover", `{"operation_id":"`+r.OperationID+`","retry_recovery":true}`))
			if rr.Code != http.StatusOK {
				t.Fatal(rr.Code, rr.Body.String())
			}
			finished, err := store.get(r.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "stop-error" || mode == "still-starting" || mode == "players-unknown" {
				if finished.State != operationNeedsAttention || finished.Stage != "runtime_rollback" || d.removed {
					t.Fatal("unsafe recovery succeeded", finished)
				}
				if current, err := store.currentSetup(); err != nil || current == nil {
					t.Fatal("lost recovery lock", err)
				}
				for _, path := range []string{setupInstanceMarker, setupInstancePath(r.OperationID), filepath.Join(dir, "pending.json"), env, quadlet} {
					if _, err := os.Stat(path); err != nil {
						t.Fatal("lost recovery evidence", path, err)
					}
				}
			} else {
				if finished.State != operationRolledBack || !stopped || !d.removed {
					t.Fatal(finished)
				}
				if current, err := store.currentSetup(); err != nil || current != nil {
					t.Fatal("setup lock retained", current, err)
				}
				if _, err := os.Stat(setupInstanceMarker); !os.IsNotExist(err) {
					t.Fatal("marker survived recovery", err)
				}
				recovered, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
				if err != nil || !recovered.RolledBack {
					t.Fatal(recovered, err)
				}
			}
			if (mode == "settling" && settlePauses != 3) || (mode == "still-starting" && settlePauses != 120) {
				t.Fatal("unexpected settle polling", settlePauses)
			}
			if mode == "players-online" && (elapsed != 60*time.Second || len(warnings) != 9) {
				t.Fatal("player shutdown countdown changed", elapsed, warnings)
			}
			if _, err := f.backend.readRecord(sibling); err != nil {
				t.Fatal("sibling touched", err)
			}
			if f.states[sibling] != siblingState || f.containers[sibling] != siblingContainer {
				t.Fatal("sibling runtime changed")
			}
		})
	}
}
