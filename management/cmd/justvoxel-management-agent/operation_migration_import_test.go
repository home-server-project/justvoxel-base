package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

const testMigrationImportFingerprint = "sha256:8888888888888888888888888888888888888888888888888888888888888888"

func TestOperationStoreMigrationImportIsIdempotentAndSharesFamilyLock(t *testing.T) {
	store := openTestOperationStore(t)
	first, created, err := store.beginMigrationImport(testMigrationImportFingerprint)
	if err != nil || !created {
		t.Fatalf("first import begin: created=%t err=%v", created, err)
	}
	if first.OperationType != operationTypeMigrationImport {
		t.Fatalf("unexpected operation type: %#v", first)
	}
	second, created, err := store.beginMigrationImport(testMigrationImportFingerprint)
	if err != nil || created || second.OperationID != first.OperationID {
		t.Fatalf("idempotent import begin: created=%t err=%v operation=%#v", created, err, second)
	}
	if _, _, err := store.beginMigrationExport(testMigrationExportFingerprint); !errors.Is(err, errMigrationOperationBusy) {
		t.Fatalf("export during import error = %v, want migration busy", err)
	}
}

func TestOperationStoreMigrationExportBlocksImport(t *testing.T) {
	store := openTestOperationStore(t)
	if _, _, err := store.beginMigrationExport(testMigrationExportFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.beginMigrationImport(testMigrationImportFingerprint); !errors.Is(err, errMigrationOperationBusy) {
		t.Fatalf("import during export error = %v, want migration busy", err)
	}
}

func TestOperationStoreRestartMarksInterruptedMigrationImportNeedsAttention(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginMigrationImport(testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationValidating, "import_preflight", "Checking import."); err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationRunning, "importing", "Activating import."); err != nil {
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
	current, err := second.currentMigration()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != journal.OperationID {
		t.Fatalf("current server migration = %#v, want %s", current, journal.OperationID)
	}
	if current.State != operationNeedsAttention || current.Stage != "interrupted" || current.InterruptedAt == "" {
		t.Fatalf("interrupted import not preserved conservatively: %#v", current)
	}
	if !strings.Contains(current.Status, "import recovery state") {
		t.Fatalf("interrupted import status is not recovery-oriented: %q", current.Status)
	}
}
