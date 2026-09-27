package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
		{operationNeedsAttention, "runtime_rollback"},
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
