package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testMigrationRecoveryFingerprint = "sha256:9999999999999999999999999999999999999999999999999999999999999999"

func beginAttentionMigrationRecovery(t *testing.T, store *operationStore) operationJournal {
	t.Helper()
	operation, _, err := store.beginMigrationRecovery(testMigrationRecoveryFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationValidating, "recovery_preflight", "Checking recovery."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "recovery_backend_invalid", "Recovery backend returned invalid progress data."); err != nil {
		t.Fatal(err)
	}
	return operation
}

func TestAdminMigrationRecoveryResolvePersistedOperation(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	operation := beginAttentionMigrationRecovery(t, first)
	if err := first.close(); err != nil {
		t.Fatal(err)
	}
	store, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, store)
	previous := runAdminMigrationRecoveryHelper
	runAdminMigrationRecoveryHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "resolve-check" {
			t.Fatalf("unexpected helper action %q", action)
		}
		return []byte(`{"ok":true,"schema_version":"v1","safe_to_resolve":true}`), nil
	}
	t.Cleanup(func() { runAdminMigrationRecoveryHelper = previous })

	body, _ := json.Marshal(adminMigrationRecoveryResolveRequest{OperationID: operation.OperationID, KeepCurrentState: true})
	rr := httptest.NewRecorder()
	s.adminMigrationRecoveryResolve(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/recovery/resolve", string(body)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"state":"resolved"`) {
		t.Fatalf("resolve status=%d: %s", rr.Code, rr.Body.String())
	}
	journal, err := store.get(operation.OperationID)
	if err != nil || journal.State != operationResolved || journal.FinishedAt == "" {
		t.Fatalf("resolved journal = %#v, err=%v", journal, err)
	}
	persisted, err := readOperationJournal(filepath.Join(store.operationsDir, operation.OperationID+".json"))
	if err != nil || persisted.State != operationResolved || persisted.FinishedAt == "" {
		t.Fatalf("persisted journal = %#v, err=%v", persisted, err)
	}
	if store.currentMigrationID != "" || store.migrationLockHeld {
		t.Fatalf("terminal transition retained migration ownership: id=%q lock=%t", store.currentMigrationID, store.migrationLockHeld)
	}
	if current, err := store.currentMigration(); err != nil || current != nil {
		t.Fatalf("recovery remained current: %#v err=%v", current, err)
	}
	if _, _, err := store.beginMigrationExport(testMigrationExportFingerprint); err != nil {
		t.Fatalf("migration lock remained held: %v", err)
	}
}

func TestAdminMigrationRecoveryResolveFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		output     string
		wrongID    bool
		wrongType  bool
		running    bool
		confirmed  bool
		wantStatus int
	}{
		{name: "unsafe", output: `{"ok":true,"schema_version":"v1","safe_to_resolve":false}`, confirmed: true, wantStatus: http.StatusConflict},
		{name: "malformed", output: `{`, confirmed: true, wantStatus: http.StatusInternalServerError},
		{name: "wrong schema", output: `{"ok":true,"schema_version":"v2","safe_to_resolve":true}`, confirmed: true, wantStatus: http.StatusInternalServerError},
		{name: "wrong id", output: `{"ok":true,"schema_version":"v1","safe_to_resolve":true}`, wrongID: true, confirmed: true, wantStatus: http.StatusNotFound},
		{name: "wrong type", output: `{"ok":true,"schema_version":"v1","safe_to_resolve":true}`, wrongType: true, confirmed: true, wantStatus: http.StatusConflict},
		{name: "running", output: `{"ok":true,"schema_version":"v1","safe_to_resolve":true}`, running: true, confirmed: true, wantStatus: http.StatusConflict},
		{name: "confirmation missing", output: `{"ok":true,"schema_version":"v1","safe_to_resolve":true}`, wantStatus: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := surfaceTestServer(t, roleAdministrator)
			store := openTestOperationStore(t)
			attachTestOperationStore(t, s, store)
			var operation operationJournal
			var err error
			if tc.wrongType {
				operation, _, err = store.beginMigrationImport(testMigrationImportFingerprint)
			} else {
				operation, _, err = store.beginMigrationRecovery(testMigrationRecoveryFingerprint)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.transition(operation.OperationID, operationValidating, "recovery_preflight", "Checking recovery."); err != nil {
				t.Fatal(err)
			}
			if tc.running {
				if _, err := store.transition(operation.OperationID, operationRunning, "recovering", "Recovering."); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := store.transition(operation.OperationID, operationNeedsAttention, "recovery_backend_invalid", "Recovery needs attention."); err != nil {
					t.Fatal(err)
				}
			}
			previous := runAdminMigrationRecoveryHelper
			runAdminMigrationRecoveryHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
				if action != "resolve-check" {
					t.Fatalf("unexpected helper action %q", action)
				}
				return []byte(tc.output), nil
			}
			t.Cleanup(func() { runAdminMigrationRecoveryHelper = previous })
			id := operation.OperationID
			if tc.wrongID {
				id = "12345678-1234-4123-8123-123456789abc"
			}
			body, _ := json.Marshal(adminMigrationRecoveryResolveRequest{OperationID: id, KeepCurrentState: tc.confirmed})
			rr := httptest.NewRecorder()
			s.adminMigrationRecoveryResolve(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/recovery/resolve", string(body)))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d: %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			current, err := store.currentMigration()
			if err != nil || current == nil || current.OperationID != operation.OperationID {
				t.Fatalf("current = %#v, err=%v", current, err)
			}
			if _, _, err := store.beginMigrationExport(testMigrationExportFingerprint); !errors.Is(err, errMigrationOperationBusy) {
				t.Fatalf("migration lock released after refusal: %v", err)
			}
		})
	}
}
