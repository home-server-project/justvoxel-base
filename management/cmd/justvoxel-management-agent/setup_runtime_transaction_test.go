package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func setupPlanForRuntimeTest(t *testing.T) *adminSetupNormalizedPlan {
	t.Helper()
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
	runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, payload []byte) ([]byte, error) {
		events = append(events, "runtime:"+action)
		if strings.Contains(strings.ToLower(string(payload)), "password") {
			t.Fatalf("runtime payload contains password field: %s", payload)
		}
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"runtime_preflight"}`), nil
		case "apply":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_configured"}`), nil
		case "verify":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_verified"}`), nil
		case "commit":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_committed"}`), nil
		default:
			return nil, errors.New("unexpected runtime action")
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

func TestExecuteSetupTransactionRuntimeFailureRollsBackRuntimeBeforeStorage(t *testing.T) {
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
	runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		events = append(events, "runtime:"+action)
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"runtime_preflight"}`), nil
		case "apply":
			return []byte(`{"ok":true,"applied":true,"phase":"runtime_configured"}`), nil
		case "verify":
			return []byte(`{"ok":false,"applied":false,"phase":"runtime_verify","error":"Minecraft runtime verification failed."}`), nil
		case "rollback":
			return []byte(`{"ok":true,"applied":false,"phase":"runtime_rolled_back"}`), nil
		default:
			return nil, errors.New("unexpected runtime action")
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
	runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"runtime_preflight"}`), nil
		case "apply":
			return []byte(`{"ok":false,"applied":false,"phase":"runtime_config","error":"runtime failed"}`), nil
		case "rollback":
			return []byte(`{"ok":false,"applied":false,"phase":"runtime_rollback","error":"rollback failed"}`), nil
		default:
			return nil, errors.New("unexpected runtime action")
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
	runAdminSetupRuntimeTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "verify" {
			t.Fatalf("unexpected action %q", action)
		}
		return []byte(`{"ok":true,"applied":true,"phase":"runtime_verified","evidence":{"rcon_result":"ready","rcon_wait_seconds":"17","final_validation":"passed","final_validation_output":"OK: minecraft.service is active"}}`), nil
	}
	response, err := runSetupRuntimeTransactionAction(context.Background(), "verify", setupRuntimeVerifyTimeout, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Evidence["rcon_result"] != "ready" || response.Evidence["rcon_wait_seconds"] != "17" || response.Evidence["final_validation"] != "passed" {
		t.Fatalf("unexpected helper evidence: %#v", response.Evidence)
	}
}

