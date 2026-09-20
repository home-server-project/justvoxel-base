package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testRestoreFingerprint = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestOperationStoreRestoreIsIdempotentAndIndependentFromSetup(t *testing.T) {
	store := openTestOperationStore(t)

	setup, setupCreated, err := store.beginSetup(testSetupFingerprint)
	if err != nil || !setupCreated {
		t.Fatalf("begin setup: created=%t err=%v", setupCreated, err)
	}
	restore, restoreCreated, err := store.beginRestore(testRestoreFingerprint)
	if err != nil || !restoreCreated {
		t.Fatalf("begin restore: created=%t err=%v", restoreCreated, err)
	}
	if setup.OperationID == restore.OperationID || restore.OperationType != operationTypeRestore {
		t.Fatalf("unexpected restore operation: %#v", restore)
	}

	same, created, err := store.beginRestore(testRestoreFingerprint)
	if err != nil || created || same.OperationID != restore.OperationID {
		t.Fatalf("idempotent restore begin: created=%t err=%v operation=%#v", created, err, same)
	}
	other := "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	if _, _, err := store.beginRestore(other); !errors.Is(err, errRestoreOperationBusy) {
		t.Fatalf("different restore plan error = %v, want restore busy", err)
	}
}

func TestOperationStoreRestartMarksInterruptedRestoreNeedsAttention(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginRestore(testRestoreFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationValidating, "validating", "Checking restore plan."); err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationRunning, "staging", "Preparing restore staging."); err != nil {
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

	current, err := second.currentRestore()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != journal.OperationID {
		t.Fatalf("current restore = %#v, want %s", current, journal.OperationID)
	}
	if current.State != operationNeedsAttention || current.Stage != "interrupted" || current.InterruptedAt == "" {
		t.Fatalf("interrupted restore was not preserved conservatively: %#v", current)
	}
	if !strings.Contains(current.Status, "recovery state") {
		t.Fatalf("interrupted restore status does not direct recovery review: %q", current.Status)
	}
	if _, _, err := second.beginRestore("sha256:3333333333333333333333333333333333333333333333333333333333333333"); !errors.Is(err, errRestoreOperationBusy) {
		t.Fatalf("new restore after interruption error = %v, want restore busy", err)
	}
}

func TestOperationStoreCompletedRestoreReleasesCurrent(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginRestore(testRestoreFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range []struct {
		state operationState
		stage string
		status string
	}{
		{operationValidating, "validating", "Checking restore plan."},
		{operationRunning, "running", "Running restore transaction."},
		{operationVerifying, "verifying", "Validating restored Minecraft."},
		{operationSucceeded, "complete", "Restore completed."},
	} {
		if _, err := store.transition(journal.OperationID, tr.state, tr.stage, tr.status); err != nil {
			t.Fatalf("transition to %s: %v", tr.state, err)
		}
	}
	if current, err := store.currentRestore(); err != nil || current != nil {
		t.Fatalf("current restore after success = %#v err=%v", current, err)
	}
	loaded, err := store.get(journal.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != operationSucceeded || loaded.FinishedAt == "" {
		t.Fatalf("completed restore journal = %#v", loaded)
	}
}

func TestOperationStoreRestoreHostLockBlocksSecondStore(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	if _, _, err := first.beginRestore(testRestoreFingerprint); err != nil {
		t.Fatal(err)
	}

	second, err := openOperationStore(base)
	if second != nil {
		_ = second.close()
	}
	if !errors.Is(err, errRestoreLockBusy) {
		t.Fatalf("second store error = %v, want restore lock busy", err)
	}
}

func TestCurrentRestoreOperationAPIRequiresAdministratorAndAllowsLocalRoot(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginRestore(testRestoreFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, store)
		mux := http.NewServeMux()
		registerAdminOperationRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/restore/current-operation", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}

	s := surfaceTestServer(t, roleViewer)
	attachTestOperationStore(t, s, store)
	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/restore/current-operation", 0)
	rr := httptest.NewRecorder()
	s.adminCurrentRestoreOperation(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatalf("local-root current restore operation = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCurrentRestoreOperationAPIEmpty(t *testing.T) {
	store := openTestOperationStore(t)
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, store)
	mux := http.NewServeMux()
	registerAdminOperationRoutes(mux, s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/restore/current-operation", ""))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"operation":null}` {
		t.Fatalf("empty current restore operation = %d: %q", rr.Code, rr.Body.String())
	}
}
