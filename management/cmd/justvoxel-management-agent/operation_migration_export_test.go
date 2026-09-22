package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationStoreMigrationExportIsIdempotent(t *testing.T) {
	store := openTestOperationStore(t)
	first, created, err := store.beginMigrationExport(testMigrationExportFingerprint)
	if err != nil || !created {
		t.Fatalf("first export begin: created=%t err=%v", created, err)
	}
	if first.OperationType != operationTypeMigrationExport {
		t.Fatalf("unexpected operation type: %#v", first)
	}
	second, created, err := store.beginMigrationExport(testMigrationExportFingerprint)
	if err != nil || created || second.OperationID != first.OperationID {
		t.Fatalf("idempotent export begin: created=%t err=%v operation=%#v", created, err, second)
	}
	other := "sha256:7777777777777777777777777777777777777777777777777777777777777777"
	if _, _, err := store.beginMigrationExport(other); !errors.Is(err, errMigrationOperationBusy) {
		t.Fatalf("different export plan error = %v, want busy", err)
	}
}

func TestOperationStoreRestartMarksInterruptedMigrationExportNeedsAttention(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginMigrationExport(testMigrationExportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationValidating, "export_preflight", "Checking export."); err != nil {
		t.Fatal(err)
	}
	if _, err := first.transition(journal.OperationID, operationRunning, "exporting", "Creating export."); err != nil {
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
		t.Fatalf("interrupted export not preserved conservatively: %#v", current)
	}
	if !strings.Contains(current.Status, "destination and Minecraft runtime") {
		t.Fatalf("interrupted export status is not recovery-oriented: %q", current.Status)
	}
}

func TestCurrentMigrationOperationAPIRequiresAdministratorAndAllowsLocalRoot(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginMigrationExport(testMigrationExportFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, store)
		mux := http.NewServeMux()
		registerAdminOperationRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/migration/current-operation", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}

	s := surfaceTestServer(t, roleViewer)
	attachTestOperationStore(t, s, store)
	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/migration/current-operation", 0)
	rr := httptest.NewRecorder()
	s.adminCurrentMigrationOperation(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatalf("local-root current migration operation = %d: %s", rr.Code, rr.Body.String())
	}
}
