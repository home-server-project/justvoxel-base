package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func setupPlanForRuntimeTest(t *testing.T) *adminSetupNormalizedPlan {
	t.Helper()
	oldInstance := runSetupInstanceRuntimeAction
	t.Cleanup(func() { runSetupInstanceRuntimeAction = oldInstance })
	// Orchestration tests fake the runtime boundary; instance publication itself
	// is covered by setup_instance_transaction_test.go with the real backend.
	runSetupInstanceRuntimeAction = func(ctx context.Context, action string, request setupRuntimeTransactionRequest) (setupRuntimeTransactionResponse, error) {
		payload, err := json.Marshal(request)
		if err != nil {
			return setupRuntimeTransactionResponse{}, err
		}
		return runSetupRuntimeTransactionPayload(ctx, action, setupRuntimeVerifyTimeout, payload)
	}
	var response adminSetupPlanResponse
	if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &response); err != nil {
		t.Fatal(err)
	}
	return response.Normalized
}

func TestExecuteSetupTransactionSuccess(t *testing.T) {
	store := openTestOperationStore(t)
	operation := beginStorageTestOperation(t, store)
	plan := setupPlanForRuntimeTest(t)

	oldStorage := runAdminSetupStorageTransactionHelper
	oldRuntime := runAdminSetupRuntimeTransactionHelper
	defer func() {
		runAdminSetupStorageTransactionHelper = oldStorage
		runAdminSetupRuntimeTransactionHelper = oldRuntime
	}()

	events := []string{}
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		events = append(events, "storage:"+action)
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"}`), nil
		case "apply":
			return []byte(`{"ok":true,"applied":true,"phase":"storage_verified","rollback_state":"not_started","rollback_result":""}`), nil
		default:
			return nil, errors.New("unexpected storage action")
		}
	}
	runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, payload []byte) ([]byte, []byte, error) {
		events = append(events, "runtime:"+action)
		if strings.Contains(strings.ToLower(string(payload)), "password") {
			t.Fatalf("runtime payload contains password field: %s", payload)
		}
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"runtime_preflight"}`), nil, nil
		case "apply":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_configured"}`), nil, nil
		case "verify":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_verified"}`), nil, nil
		case "commit":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_committed"}`), nil, nil
		default:
			return nil, nil, errors.New("unexpected runtime action")
		}
	}

	if err := executeSetupTransaction(context.Background(), store, operation.OperationID, plan, nil); err != nil {
		t.Fatal(err)
	}
	finished, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != operationSucceeded || finished.Stage != "completed" {
		t.Fatalf("finished=%#v", finished)
	}
	if current, err := store.currentSetup(); err != nil || current != nil {
		t.Fatalf("current=%#v err=%v, want none", current, err)
	}
	want := "storage:validate,storage:apply,runtime:validate,runtime:apply,runtime:verify,runtime:commit"
	if strings.Join(events, ",") != want {
		t.Fatalf("events=%v want=%s", events, want)
	}
}

func TestExecuteSetupTransactionCrashLoopRollsBackRuntimeBeforeStorage(t *testing.T) {
	store := openTestOperationStore(t)
	operation := beginStorageTestOperation(t, store)
	plan := setupPlanForRuntimeTest(t)

	oldStorage := runAdminSetupStorageTransactionHelper
	oldRuntime := runAdminSetupRuntimeTransactionHelper
	defer func() {
		runAdminSetupStorageTransactionHelper = oldStorage
		runAdminSetupRuntimeTransactionHelper = oldRuntime
	}()

	events := []string{}
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		events = append(events, "storage:"+action)
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"}`), nil
		case "apply":
			return []byte(`{"ok":true,"applied":true,"phase":"storage_verified","rollback_state":"not_started","rollback_result":""}`), nil
		case "rollback":
			return []byte(`{"ok":true,"applied":false,"phase":"storage_rolled_back","rollback_state":"succeeded","rollback_result":"rolled_back"}`), nil
		default:
			return nil, errors.New("unexpected storage action")
		}
	}
	runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, []byte, error) {
		events = append(events, "runtime:"+action)
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"runtime_preflight"}`), nil, nil
		case "apply":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_configured"}`), nil, nil
		case "verify":
			return []byte(`{"ok":false,"applied":false,"phase":"runtime_verify","error":"Minecraft runtime verification failed.","evidence":{"rcon_result":"crash_loop"}}`), nil, nil
		case "rollback":
			return []byte(`{"ok":true,"applied":false,"phase":"runtime_rolled_back"}`), []byte("runtime rollback diagnostic"), nil
		default:
			return nil, nil, errors.New("unexpected runtime action")
		}
	}

	if err := executeSetupTransaction(context.Background(), store, operation.OperationID, plan, nil); err == nil {
		t.Fatal("expected runtime verification failure")
	}
	finished, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != operationRolledBack {
		t.Fatalf("state=%s want rolled_back", finished.State)
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "runtime:rollback,storage:rollback") {
		t.Fatalf("rollback order incorrect: %s", joined)
	}
}

func TestExecuteSetupTransactionRuntimeRollbackFailureNeedsAttention(t *testing.T) {
	for _, tc := range []struct {
		name, stdout   string
		executionError bool
	}{
		{"failed JSON", `{"ok":false,"applied":false,"phase":"runtime_rollback","error":"rollback failed"}`, false},
		{"malformed stdout", `warning before JSON {`, false},
		{"trailing JSON", `{"ok":true,"applied":false,"phase":"runtime_rolled_back"}{}`, false},
		{"trailing text", `{"ok":true,"applied":false,"phase":"runtime_rolled_back"}unexpected`, false},
		{"unknown fields", `{"ok":true,"applied":false,"phase":"runtime_rolled_back","unexpected":true}`, false},
		{"unconfirmed phase", `{"ok":true,"applied":false,"phase":"runtime_rollback"}`, false},
		{"still applied", `{"ok":true,"applied":true,"phase":"runtime_rolled_back"}`, false},
		{"contradictory error", `{"ok":true,"applied":false,"phase":"runtime_rolled_back","error":"rollback failed"}`, false},
		{"execution failure with success JSON", `{"ok":true,"applied":false,"phase":"runtime_rolled_back"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestOperationStore(t)
			operation := beginStorageTestOperation(t, store)
			plan := setupPlanForRuntimeTest(t)

			oldStorage := runAdminSetupStorageTransactionHelper
			oldRuntime := runAdminSetupRuntimeTransactionHelper
			defer func() {
				runAdminSetupStorageTransactionHelper = oldStorage
				runAdminSetupRuntimeTransactionHelper = oldRuntime
			}()

			storageRollbackCalled := false
			runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
				switch action {
				case "validate":
					return []byte(`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"}`), nil
				case "apply":
					return []byte(`{"ok":true,"applied":true,"phase":"storage_verified","rollback_state":"not_started","rollback_result":""}`), nil
				case "rollback":
					storageRollbackCalled = true
					return []byte(`{"ok":true,"applied":false,"phase":"storage_rolled_back","rollback_state":"succeeded","rollback_result":"rolled_back"}`), nil
				default:
					return nil, errors.New("unexpected storage action")
				}
			}
			runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, []byte, error) {
				switch action {
				case "validate":
					return []byte(`{"ok":true,"applied":false,"phase":"runtime_preflight"}`), nil, nil
				case "apply":
					return []byte(`{"ok":false,"applied":false,"phase":"runtime_config","error":"runtime failed"}`), nil, nil
				case "rollback":
					if tc.executionError {
						return []byte(tc.stdout), []byte("rollback command failed"), errors.New("exit status 1")
					}
					return []byte(tc.stdout), []byte("rollback diagnostic"), nil
				default:
					return nil, nil, errors.New("unexpected runtime action")
				}
			}

			if err := executeSetupTransaction(context.Background(), store, operation.OperationID, plan, nil); err == nil {
				t.Fatal("expected runtime failure")
			}
			current, err := store.currentSetup()
			if err != nil || current == nil || current.State != operationNeedsAttention {
				t.Fatalf("current=%#v err=%v", current, err)
			}
			if storageRollbackCalled {
				t.Fatal("storage rollback ran after runtime rollback could not be confirmed")
			}
		})
	}
}

func TestSetupRuntimeRequestExcludesSMBSecret(t *testing.T) {
	store := openTestOperationStore(t)
	operation := beginStorageTestOperation(t, store)
	plan := setupPlanForRuntimeTest(t)
	plan.Backups.Type = "smb"
	plan.Backups.Source = "//nas/backups"
	plan.Backups.ExpectedSource = "//nas/backups"
	plan.Backups.Username = "backup-user"
	plan.Backups.CredentialsRequired = true

	request, err := setupRuntimeRequestForOperation(operation, plan)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(payload)), "password") || strings.Contains(string(payload), "backup-user") {
		t.Fatalf("runtime payload contains storage credential data: %s", payload)
	}
}

func TestSetupRuntimeHelperEvidenceIsAccepted(t *testing.T) {
	request := setupRuntimeTransactionRequest{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     "12345678-1234-4123-8123-123456789abc",
		PlanFingerprint: testSetupFingerprint,
		Minecraft:       setupRuntimeMinecraft{MinecraftUID: 1000, MinecraftGID: 1000},
	}
	old := runAdminSetupRuntimeTransactionHelper
	defer func() { runAdminSetupRuntimeTransactionHelper = old }()
	runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, []byte, error) {
		if action != "verify" {
			t.Fatalf("unexpected action %q", action)
		}
		return []byte(`{"ok":true,"applied":true,"phase":"runtime_verified","evidence":{"rcon_result":"ready","rcon_wait_seconds":"17","final_validation":"passed","final_validation_output":"OK: minecraft.service is active"}}`), nil, nil
	}
	response, err := runSetupRuntimeTransactionAction(context.Background(), "verify", setupRuntimeVerifyTimeout, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Evidence["rcon_result"] != "ready" || response.Evidence["rcon_wait_seconds"] != "17" || response.Evidence["final_validation"] != "passed" {
		t.Fatalf("unexpected helper evidence: %#v", response.Evidence)
	}
}

func TestSetupRuntimeRollbackSeparatesStderrFromJSON(t *testing.T) {
	old := runAdminSetupRuntimeTransactionHelper
	defer func() { runAdminSetupRuntimeTransactionHelper = old }()
	request := setupRuntimeTransactionRequest{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     "12345678-1234-4123-8123-123456789abc",
		PlanFingerprint: testSetupFingerprint,
		Minecraft:       setupRuntimeMinecraft{MinecraftUID: 1000, MinecraftGID: 1000},
	}
	runAdminSetupRuntimeTransactionHelper = func(ctx context.Context, action string, _ []byte) ([]byte, []byte, error) {
		if action != "rollback" {
			t.Fatalf("unexpected action %q", action)
		}
		// Exercise actual stdout/stderr capture, not only a mock response.
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `printf '%s' '{"ok":true,"applied":false,"phase":"runtime_rolled_back"}'; printf 'diagnostic password=private-secret\n' >&2`)
		return captureSetupRuntimeTransactionOutput(cmd)
	}
	response, err := runSetupRuntimeTransactionAction(context.Background(), "rollback", setupRuntimeRollbackTimeout, request)
	if err != nil || !response.OK || response.Phase != "runtime_rolled_back" {
		t.Fatalf("rollback response=%#v err=%v", response, err)
	}
	diagnostic := response.Evidence["helper_stderr"]
	if !strings.Contains(diagnostic, "diagnostic") || strings.Contains(diagnostic, "private-secret") || strings.ContainsAny(diagnostic, "\r\n") {
		t.Fatalf("unsafe or missing diagnostic: %q", diagnostic)
	}
}

func TestSetupRuntimeStderrCaptureIsBounded(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", `printf '%s' "$RUNTIME_DIAGNOSTIC" >&2; printf 'stdout is separate'`)
	cmd.Env = []string{"RUNTIME_DIAGNOSTIC=" + strings.Repeat("x", 32*1024)}
	stdout, stderr, err := captureSetupRuntimeTransactionOutput(cmd)
	if err != nil || string(stdout) != "stdout is separate" || len(stderr) != 16*1024 {
		t.Fatalf("stdout=%q err=%v retained stderr bytes=%d", stdout, err, len(stderr))
	}
}

func TestInstanceOnlySetupActionTimeouts(t *testing.T) {
	old := runSetupInstanceRuntimeAction
	t.Cleanup(func() { runSetupInstanceRuntimeAction = old })
	for _, action := range []string{"validate", "apply", "verify", "rollback", "commit"} {
		t.Run(action, func(t *testing.T) {
			runSetupInstanceRuntimeAction = func(ctx context.Context, got string, _ setupRuntimeTransactionRequest) (setupRuntimeTransactionResponse, error) {
				if got != action {
					t.Fatal(got)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing action deadline")
				}
				<-ctx.Done()
				return setupRuntimeTransactionResponse{}, ctx.Err()
			}
			_, err := runSetupRuntimeTransactionAction(context.Background(), action, time.Millisecond, setupRuntimeTransactionRequest{InstanceOnly: true})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}
