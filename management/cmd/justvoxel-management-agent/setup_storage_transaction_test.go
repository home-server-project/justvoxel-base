package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func systemSetupPlanForStorageTest(t *testing.T) *adminSetupNormalizedPlan {
	t.Helper()
	var response adminSetupPlanResponse
	if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &response); err != nil {
		t.Fatal(err)
	}
	return response.Normalized
}

func beginStorageTestOperation(t *testing.T, store *operationStore) operationJournal {
	t.Helper()
	op, created, err := store.beginSetup(testSetupFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("setup operation was not created")
	}
	return op
}

func TestSetupStorageRequestCarriesNFSAndTransientSMBSecret(t *testing.T) {
	for _, tc := range []struct {
		name, backupType, source, password string
		wantPassword bool
	}{
		{name: "nfs", backupType: "nfs", source: "nas:/backups"},
		{name: "smb", backupType: "smb", source: "//nas/backups", password: "super-secret", wantPassword: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestOperationStore(t)
			operation := beginStorageTestOperation(t, store)
			plan := systemSetupPlanForStorageTest(t)
			plan.Backups.Type = tc.backupType
			plan.Backups.Path = "/var/mnt/backups/minecraft"
			plan.Backups.MountPoint = "/var/mnt/backups"
			plan.Backups.Source = tc.source
			plan.Backups.ExpectedSource = tc.source
			plan.Backups.Username = ""
			plan.Backups.Domain = ""
			plan.Backups.CredentialsRequired = false
			if tc.backupType == "smb" {
				plan.Backups.Username = "backup-user"
				plan.Backups.CredentialsRequired = true
			}

			old := runAdminSetupStorageTransactionHelper
			defer func() { runAdminSetupStorageTransactionHelper = old }()
			runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, payload []byte) ([]byte, error) {
				body := string(payload)
				for _, want := range []string{`"type":"` + tc.backupType + `"`, `"source":"` + tc.source + `"`} {
					if !strings.Contains(body, want) {
						t.Fatalf("payload missing %s: %s", want, body)
					}
				}
				hasPassword := strings.Contains(body, `"smb_password":`)
				if tc.wantPassword != hasPassword {
					t.Fatalf("SMB password field presence mismatch in payload: %s", body)
				}
				if tc.wantPassword && !strings.Contains(body, tc.password) {
					t.Fatalf("SMB password value missing from helper payload: %s", body)
				}
				if action == "validate" {
					return []byte(`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"}`), nil
				}
				return []byte(`{"ok":true,"applied":true,"phase":"storage_verified","rollback_state":"not_started","rollback_result":""}`), nil
			}
			if err := executeSetupStorage(context.Background(), store, operation.OperationID, plan, tc.password); err != nil {
				t.Fatal(err)
			}
			current, err := store.currentSetup()
			if err != nil || current == nil || current.State != operationRunning || current.Stage != "storage_verified" {
				t.Fatalf("current=%#v err=%v", current, err)
			}
			journalData, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(journalData), tc.password) {
				t.Fatal("SMB password leaked into operation journal")
			}
		})
	}
}

func TestExecuteSetupLocalStorageSuccessLeavesRunningForLaterA5Stages(t *testing.T) {
	store := openTestOperationStore(t)
	operation := beginStorageTestOperation(t, store)
	plan := systemSetupPlanForStorageTest(t)

	old := runAdminSetupStorageTransactionHelper
	defer func() { runAdminSetupStorageTransactionHelper = old }()
	actions := []string{}
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, payload []byte) ([]byte, error) {
		actions = append(actions, action)
		body := string(payload)
		if strings.Contains(strings.ToLower(body), "password") {
			t.Fatalf("A5.3 payload contains a secret field: %s", body)
		}
		for _, want := range []string{operation.OperationID, testSetupFingerprint, `"type":"system"`, `"path":"/var/lib/justvoxel/minecraft"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("A5.3 payload missing %q: %s", want, body)
			}
		}
		switch action {
		case "validate":
			return []byte(`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"}`), nil
		case "apply":
			return []byte(`{"ok":true,"applied":true,"phase":"storage_verified","rollback_state":"not_started","rollback_result":""}`), nil
		default:
			t.Fatalf("unexpected action %q", action)
			return nil, nil
		}
	}

	if err := executeSetupLocalStorage(context.Background(), store, operation.OperationID, plan); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "validate,apply" {
		t.Fatalf("actions = %v, want validate,apply", actions)
	}
	current, err := store.currentSetup()
	if err != nil || current == nil {
		t.Fatalf("current=%#v err=%v", current, err)
	}
	if current.State != operationRunning || current.Stage != "storage_verified" {
		t.Fatalf("current state/stage = %s/%s, want running/storage_verified", current.State, current.Stage)
	}
}

func TestExecuteSetupLocalStoragePreflightFailureClosesWithoutMutation(t *testing.T) {
	store := openTestOperationStore(t)
	operation := beginStorageTestOperation(t, store)
	plan := systemSetupPlanForStorageTest(t)
	old := runAdminSetupStorageTransactionHelper
	defer func() { runAdminSetupStorageTransactionHelper = old }()
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "validate" {
			t.Fatalf("unexpected action after rejected preflight: %q", action)
		}
		return []byte(`{"ok":false,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes","error":"Reviewed local storage no longer matches the current appliance state."}`), nil
	}
	if err := executeSetupLocalStorage(context.Background(), store, operation.OperationID, plan); err == nil {
		t.Fatal("expected rejected local-storage preflight")
	}
	finished, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != operationRolledBack {
		t.Fatalf("finished state = %s, want rolled_back", finished.State)
	}
}

func TestExecuteSetupLocalStorageRollbackFailureNeedsAttention(t *testing.T) {
	store := openTestOperationStore(t)
	operation := beginStorageTestOperation(t, store)
	plan := systemSetupPlanForStorageTest(t)
	old := runAdminSetupStorageTransactionHelper
	defer func() { runAdminSetupStorageTransactionHelper = old }()
	runAdminSetupStorageTransactionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action == "validate" {
			return []byte(`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"}`), nil
		}
		return []byte(`{"ok":false,"applied":false,"phase":"storage_rollback","rollback_state":"failed","rollback_result":"needs_attention","error":"Storage rollback could not be completed safely; recovery evidence was preserved."}`), nil
	}
	if err := executeSetupLocalStorage(context.Background(), store, operation.OperationID, plan); err == nil {
		t.Fatal("expected storage rollback failure")
	}
	current, err := store.currentSetup()
	if err != nil || current == nil || current.State != operationNeedsAttention {
		t.Fatalf("current=%#v err=%v", current, err)
	}
}

func TestSetupStorageHelperResponseIsStrict(t *testing.T) {
	request := setupStorageTransactionRequest{
		SchemaVersion: operationSchemaVersion,
		OperationID: "12345678-1234-4123-8123-123456789abc",
		PlanFingerprint: testSetupFingerprint,
		Storage: setupStorageTransactionTarget{Type: "system", Path: "/var/lib/justvoxel/minecraft"},
		Backups: setupStorageTransactionTarget{Type: "system", Path: "/var/lib/justvoxel/backups"},
	}
	old := runAdminSetupStorageTransactionHelper
	defer func() { runAdminSetupStorageTransactionHelper = old }()
	for _, output := range []string{
		`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes","unexpected":true}`,
		`{"ok":true,"applied":false,"phase":"storage_preflight","rollback_state":"not_started","rollback_result":"no_changes"} {}`,
		`{"ok":true,"applied":false,"phase":"","rollback_state":"not_started","rollback_result":"no_changes"}`,
	} {
		runAdminSetupStorageTransactionHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
			return []byte(output), nil
		}
		if _, err := runSetupStorageTransactionAction(context.Background(), "validate", setupStorageValidateTimeout, request); err == nil {
			t.Fatalf("invalid helper response accepted: %s", output)
		}
	}
}

func TestAdminSetupApplyStillDoesNotDispatchStorageExecution(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	oldStorage := runAdminSetupStorageTransactionHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
		runAdminSetupStorageTransactionHelper = oldStorage
	}()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validAdminSetupPlanResponse), nil
	}
	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"configured":false,"minecraft":{},"backup":{}}`), nil
	}
	storageCalled := false
	runAdminSetupStorageTransactionHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		storageCalled = true
		return nil, errors.New("A5.3 must not dispatch")
	}
	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, exactSetupApplyFingerprint(t))))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("apply status = %d: %s", rr.Code, rr.Body.String())
	}
	if storageCalled {
		t.Fatal("A5.2 Apply endpoint dispatched A5.3 storage execution")
	}
}
