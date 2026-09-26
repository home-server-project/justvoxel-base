package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testFactoryResetFingerprint = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestOperationStoreFactoryResetIsIdempotentAndBlocksOtherOperations(t *testing.T) {
	store := openTestOperationStore(t)
	first, created, err := store.beginFactoryReset(testFactoryResetFingerprint)
	if err != nil || !created {
		t.Fatalf("first factory reset begin: created=%t err=%v", created, err)
	}
	if first.OperationType != operationTypeFactoryReset {
		t.Fatalf("unexpected operation type: %#v", first)
	}
	second, created, err := store.beginFactoryReset(testFactoryResetFingerprint)
	if err != nil || created || second.OperationID != first.OperationID {
		t.Fatalf("idempotent factory reset begin: created=%t err=%v operation=%#v", created, err, second)
	}
	other := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if _, _, err := store.beginFactoryReset(other); !errors.Is(err, errFactoryResetOperationBusy) {
		t.Fatalf("different factory reset plan error = %v, want factory reset busy", err)
	}

	for name, begin := range map[string]func() error{
		"minecraft reset": func() error {
			_, _, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
			return err
		},
		"setup": func() error {
			_, _, err := store.beginSetup(testSetupFingerprint)
			return err
		},
		"restore": func() error {
			_, _, err := store.beginRestore("sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
			return err
		},
		"data migration": func() error {
			_, _, err := store.beginDataMigration(testDataMigrationFingerprint)
			return err
		},
		"server migration": func() error {
			_, _, err := store.beginMigrationExport("sha256:abababababababababababababababababababababababababababababababab")
			return err
		},
	} {
		err := begin()
		if name == "minecraft reset" {
			if !errors.Is(err, errMinecraftResetOperationBusy) {
				t.Fatalf("%s while factory reset active error = %v, want Minecraft reset busy", name, err)
			}
			continue
		}
		if !errors.Is(err, errFactoryResetOperationBusy) {
			t.Fatalf("%s while factory reset active error = %v, want factory reset busy", name, err)
		}
	}
}

func TestOperationStoreFactoryResetNeedsAttentionCanBeRetried(t *testing.T) {
	store := openTestOperationStore(t)
	operation, created, err := store.beginFactoryReset(testFactoryResetFingerprint)
	if err != nil || !created {
		t.Fatalf("begin factory reset: created=%t err=%v", created, err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "reset_failed", "Factory reset stopped."); err != nil {
		t.Fatal(err)
	}
	retried, err := store.retryFactoryReset(operation.OperationID, testFactoryResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != operationQueued || retried.Stage != "queued" || retried.Status != "Full factory reset retry queued." {
		t.Fatalf("unexpected retried factory reset: %#v", retried)
	}
	if current, err := store.currentFactoryReset(); err != nil || current == nil || current.OperationID != operation.OperationID {
		t.Fatalf("retried factory reset is not current: %#v err=%v", current, err)
	}
}

func TestOperationStoreFactoryResetCannotStartDuringOtherOperation(t *testing.T) {
	tests := []struct {
		name  string
		begin func(*operationStore) error
	}{
		{
			name: "minecraft reset",
			begin: func(store *operationStore) error {
				_, _, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
				return err
			},
		},
		{
			name: "setup",
			begin: func(store *operationStore) error {
				_, _, err := store.beginSetup(testSetupFingerprint)
				return err
			},
		},
		{
			name: "restore",
			begin: func(store *operationStore) error {
				_, _, err := store.beginRestore("sha256:bcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbc")
				return err
			},
		},
		{
			name: "data migration",
			begin: func(store *operationStore) error {
				_, _, err := store.beginDataMigration(testDataMigrationFingerprint)
				return err
			},
		},
		{
			name: "server migration",
			begin: func(store *operationStore) error {
				_, _, err := store.beginMigrationExport("sha256:cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestOperationStore(t)
			if err := tt.begin(store); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.beginFactoryReset(testFactoryResetFingerprint); !errors.Is(err, errFactoryResetOperationBusy) {
				t.Fatalf("factory reset begin error = %v, want factory reset busy", err)
			}
		})
	}
}

func TestOperationStoreRestartMarksInterruptedFactoryResetNeedsAttention(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginFactoryReset(testFactoryResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationValidating, "validating", "Revalidating full factory reset plan."); err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationRunning, "resetting_runtime", "Removing appliance-owned state."); err != nil {
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
	current, err := second.currentFactoryReset()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != journal.OperationID {
		t.Fatalf("current factory reset = %#v, want %s", current, journal.OperationID)
	}
	if current.State != operationNeedsAttention || current.Stage != "interrupted" || current.InterruptedAt == "" {
		t.Fatalf("interrupted factory reset not preserved conservatively: %#v", current)
	}
	if !strings.Contains(current.Status, "Review appliance state") {
		t.Fatalf("interrupted factory reset status is not recovery-oriented: %q", current.Status)
	}
}

func TestCompletedFactoryResetReleasesAllOperationLocks(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginFactoryReset(testFactoryResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range []struct {
		state  operationState
		stage  string
		status string
	}{
		{operationValidating, "validating", "Revalidating full factory reset plan."},
		{operationRunning, "resetting_runtime", "Removing appliance-owned state."},
		{operationVerifying, "verifying", "Verifying fresh first-use state."},
		{operationSucceeded, "complete", "Full factory reset completed."},
	} {
		if _, err := store.transition(journal.OperationID, tr.state, tr.stage, tr.status); err != nil {
			t.Fatalf("transition to %s: %v", tr.state, err)
		}
	}
	if current, err := store.currentFactoryReset(); err != nil || current != nil {
		t.Fatalf("current after success = %#v err=%v", current, err)
	}
	if _, _, err := store.beginSetup(testSetupFingerprint); err != nil {
		t.Fatalf("setup remained blocked after factory reset success: %v", err)
	}
}

func TestCurrentFactoryResetOperationAPIRequiresAdministratorAndAllowsLocalRoot(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginFactoryReset(testFactoryResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, store)
		mux := http.NewServeMux()
		registerAdminOperationRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/reset/factory/current-operation", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}

	s := surfaceTestServer(t, roleViewer)
	attachTestOperationStore(t, s, store)
	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/reset/factory/current-operation", 0)
	rr := httptest.NewRecorder()
	s.adminCurrentFactoryResetOperation(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatalf("local-root current factory reset operation = %d: %s", rr.Code, rr.Body.String())
	}
}
