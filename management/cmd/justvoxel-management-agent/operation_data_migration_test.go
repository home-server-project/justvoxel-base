package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testDataMigrationFingerprint = "sha256:4444444444444444444444444444444444444444444444444444444444444444"

func TestOperationStoreDataMigrationIsIdempotent(t *testing.T) {
	store := openTestOperationStore(t)
	first, created, err := store.beginDataMigration(testDataMigrationFingerprint)
	if err != nil || !created {
		t.Fatalf("first data migration begin: created=%t err=%v", created, err)
	}
	if first.OperationType != operationTypeDataMigration {
		t.Fatalf("unexpected operation type: %#v", first)
	}
	second, created, err := store.beginDataMigration(testDataMigrationFingerprint)
	if err != nil || created || second.OperationID != first.OperationID {
		t.Fatalf("idempotent begin: created=%t err=%v operation=%#v", created, err, second)
	}
	other := "sha256:5555555555555555555555555555555555555555555555555555555555555555"
	if _, _, err := store.beginDataMigration(other); !errors.Is(err, errDataMigrationOperationBusy) {
		t.Fatalf("different migration plan error = %v, want busy", err)
	}
}

func TestOperationStoreRestartMarksInterruptedDataMigrationNeedsAttention(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginDataMigration(testDataMigrationFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationValidating, "preflight", "Checking migration plan."); err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationRunning, "copying", "Copying Minecraft data."); err != nil {
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
	current, err := second.currentDataMigration()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != journal.OperationID {
		t.Fatalf("current data migration = %#v, want %s", current, journal.OperationID)
	}
	if current.State != operationNeedsAttention || current.Stage != "interrupted" || current.InterruptedAt == "" {
		t.Fatalf("interrupted migration not preserved conservatively: %#v", current)
	}
	if !strings.Contains(current.Status, "preserved migration state") {
		t.Fatalf("interrupted migration status is not recovery-oriented: %q", current.Status)
	}
}

func TestCompletedDataMigrationReleasesCurrent(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginDataMigration(testDataMigrationFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range []struct {
		state  operationState
		stage  string
		status string
	}{
		{operationValidating, "preflight", "Checking migration plan."},
		{operationRunning, "copying", "Copying Minecraft data."},
		{operationVerifying, "runtime", "Validating Minecraft."},
		{operationSucceeded, "completed", "Minecraft data migration completed."},
	} {
		if _, err := store.transition(journal.OperationID, tr.state, tr.stage, tr.status); err != nil {
			t.Fatalf("transition to %s: %v", tr.state, err)
		}
	}
	if current, err := store.currentDataMigration(); err != nil || current != nil {
		t.Fatalf("current after success = %#v err=%v", current, err)
	}
}

func TestCurrentDataMigrationOperationAPIRequiresAdministratorAndAllowsLocalRoot(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginDataMigration(testDataMigrationFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, store)
		mux := http.NewServeMux()
		registerAdminOperationRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/data-migration/current-operation", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}

	s := surfaceTestServer(t, roleViewer)
	attachTestOperationStore(t, s, store)
	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/data-migration/current-operation", 0)
	rr := httptest.NewRecorder()
	s.adminCurrentDataMigrationOperation(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatalf("local-root current migration operation = %d: %s", rr.Code, rr.Body.String())
	}
}
