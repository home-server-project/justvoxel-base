package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testMinecraftResetFingerprint = "sha256:6666666666666666666666666666666666666666666666666666666666666666"

func TestOperationStoreMinecraftResetIsIdempotentAndBlocksOtherOperations(t *testing.T) {
	store := openTestOperationStore(t)
	first, created, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
	if err != nil || !created {
		t.Fatalf("first Minecraft reset begin: created=%t err=%v", created, err)
	}
	if first.OperationType != operationTypeMinecraftReset {
		t.Fatalf("unexpected operation type: %#v", first)
	}
	second, created, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
	if err != nil || created || second.OperationID != first.OperationID {
		t.Fatalf("idempotent reset begin: created=%t err=%v operation=%#v", created, err, second)
	}
	other := "sha256:7777777777777777777777777777777777777777777777777777777777777777"
	if _, _, err := store.beginMinecraftReset(other); !errors.Is(err, errMinecraftResetOperationBusy) {
		t.Fatalf("different reset plan error = %v, want reset busy", err)
	}

	for name, begin := range map[string]func() error{
		"setup": func() error {
			_, _, err := store.beginSetup(testSetupFingerprint)
			return err
		},
		"restore": func() error {
			_, _, err := store.beginRestore("sha256:8888888888888888888888888888888888888888888888888888888888888888")
			return err
		},
		"data migration": func() error {
			_, _, err := store.beginDataMigration(testDataMigrationFingerprint)
			return err
		},
		"server migration": func() error {
			_, _, err := store.beginMigrationExport("sha256:9999999999999999999999999999999999999999999999999999999999999999")
			return err
		},
	} {
		if err := begin(); !errors.Is(err, errMinecraftResetOperationBusy) {
			t.Fatalf("%s while reset active error = %v, want reset busy", name, err)
		}
	}
}

func TestOperationStoreMinecraftResetCannotStartDuringOtherOperation(t *testing.T) {
	tests := []struct {
		name  string
		begin func(*operationStore) error
	}{
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
				_, _, err := store.beginRestore("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
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
				_, _, err := store.beginMigrationExport("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
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
			if _, _, err := store.beginMinecraftReset(testMinecraftResetFingerprint); !errors.Is(err, errMinecraftResetOperationBusy) {
				t.Fatalf("reset begin error = %v, want reset busy", err)
			}
		})
	}
}

func TestOperationStoreRestartMarksInterruptedMinecraftResetNeedsAttention(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginMinecraftReset(testMinecraftResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationValidating, "validating", "Revalidating Minecraft reset plan."); err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationRunning, "resetting", "Removing Minecraft state."); err != nil {
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
	current, err := second.currentMinecraftReset()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != journal.OperationID {
		t.Fatalf("current reset = %#v, want %s", current, journal.OperationID)
	}
	if current.State != operationNeedsAttention || current.Stage != "interrupted" || current.InterruptedAt == "" {
		t.Fatalf("interrupted reset not preserved conservatively: %#v", current)
	}
	if !strings.Contains(current.Status, "Review appliance state") {
		t.Fatalf("interrupted reset status is not recovery-oriented: %q", current.Status)
	}
}

func TestCompletedMinecraftResetReleasesAllOperationLocks(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range []struct {
		state  operationState
		stage  string
		status string
	}{
		{operationValidating, "validating", "Revalidating Minecraft reset plan."},
		{operationRunning, "resetting", "Removing Minecraft state."},
		{operationVerifying, "verifying", "Verifying first-setup mode."},
		{operationSucceeded, "complete", "Minecraft reset completed."},
	} {
		if _, err := store.transition(journal.OperationID, tr.state, tr.stage, tr.status); err != nil {
			t.Fatalf("transition to %s: %v", tr.state, err)
		}
	}
	if current, err := store.currentMinecraftReset(); err != nil || current != nil {
		t.Fatalf("current after success = %#v err=%v", current, err)
	}
	if _, _, err := store.beginSetup(testSetupFingerprint); err != nil {
		t.Fatalf("setup remained blocked after reset success: %v", err)
	}
}

func TestCurrentMinecraftResetOperationAPIRequiresAdministratorAndAllowsLocalRoot(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, store)
		mux := http.NewServeMux()
		registerAdminOperationRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/reset/minecraft/current-operation", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}

	s := surfaceTestServer(t, roleViewer)
	attachTestOperationStore(t, s, store)
	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/reset/minecraft/current-operation", 0)
	rr := httptest.NewRecorder()
	s.adminCurrentMinecraftResetOperation(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatalf("local-root current reset operation = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestOperationStoreMinecraftResetNeedsAttentionCanBeRetried(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationValidating, "validating", "Revalidating Minecraft reset plan."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationRunning, "resetting", "Removing Minecraft state."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "reset_failed", "Minecraft reset stopped before completion."); err != nil {
		t.Fatal(err)
	}

	newFingerprint := "sha256:abababababababababababababababababababababababababababababababab"
	retried, err := store.retryMinecraftReset(operation.OperationID, newFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if retried.OperationID != operation.OperationID || retried.PlanFingerprint != newFingerprint ||
		retried.State != operationQueued || retried.Stage != "queued" ||
		retried.Status != "Minecraft reset retry queued." ||
		retried.FinishedAt != "" || retried.InterruptedAt != "" {
		t.Fatalf("unexpected retried Minecraft reset journal: %#v", retried)
	}
	current, err := store.currentMinecraftReset()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != operation.OperationID || current.State != operationQueued {
		t.Fatalf("retried Minecraft reset did not remain current: %#v", current)
	}
}

func TestOperationStoreMinecraftResetRetryRequiresNeedsAttention(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginMinecraftReset(testMinecraftResetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.retryMinecraftReset(operation.OperationID, testMinecraftResetFingerprint); err == nil {
		t.Fatal("queued Minecraft reset unexpectedly accepted for retry")
	}
}
