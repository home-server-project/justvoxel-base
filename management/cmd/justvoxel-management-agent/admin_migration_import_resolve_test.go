package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminMigrationImportResolveKeepsValidatedCurrentStateAndReleasesLock(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	operation, _, err := store.beginMigrationImport(testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationValidating, "import_preflight", "Checking Import."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "import_backend_interrupted", "Import backend stopped."); err != nil {
		t.Fatal(err)
	}

	previous := runAdminMigrationRecoveryHelper
	runAdminMigrationRecoveryHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "resolve-check" {
			t.Fatalf("unexpected recovery helper action %q", action)
		}
		return []byte(`{"ok":true,"schema_version":"v1","safe_to_resolve":true}`), nil
	}
	t.Cleanup(func() { runAdminMigrationRecoveryHelper = previous })

	body, err := json.Marshal(adminMigrationImportResolveRequest{OperationID: operation.OperationID, KeepCurrentState: true})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.adminMigrationImportResolve(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/import/resolve", string(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve status=%d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"state":"resolved"`) {
		t.Fatalf("resolve response did not report resolved state: %s", rr.Body.String())
	}
	if current, err := store.currentMigration(); err != nil || current != nil {
		t.Fatalf("Import remained current after resolve: %#v err=%v", current, err)
	}
}

func TestAdminMigrationImportResolveRefusesRetainedRecoveryState(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	operation, _, err := store.beginMigrationImport(testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationValidating, "import_preflight", "Checking Import."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "import_backend_interrupted", "Import backend stopped."); err != nil {
		t.Fatal(err)
	}

	previous := runAdminMigrationRecoveryHelper
	runAdminMigrationRecoveryHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "resolve-check" {
			t.Fatalf("unexpected recovery helper action %q", action)
		}
		return []byte(`{"ok":false,"schema_version":"v1","safe_to_resolve":false,"code":"retained_transaction","error":"Retained Import transaction state still exists and must be reviewed through Migration Recovery."}`), nil
	}
	t.Cleanup(func() { runAdminMigrationRecoveryHelper = previous })

	body, _ := json.Marshal(adminMigrationImportResolveRequest{OperationID: operation.OperationID, KeepCurrentState: true})
	rr := httptest.NewRecorder()
	s.adminMigrationImportResolve(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/import/resolve", string(body)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("retained state resolve status=%d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentMigration(); err != nil || current == nil || current.OperationID != operation.OperationID {
		t.Fatalf("Import was released despite retained recovery state: %#v err=%v", current, err)
	}
}

func TestAdminMigrationImportResolveRequiresExplicitKeepCurrentState(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	operation, _, err := store.beginMigrationImport(testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationValidating, "import_preflight", "Checking Import."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "import_backend_interrupted", "Import backend stopped."); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(adminMigrationImportResolveRequest{OperationID: operation.OperationID})
	rr := httptest.NewRecorder()
	s.adminMigrationImportResolve(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/import/resolve", string(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing keep-current confirmation status=%d: %s", rr.Code, rr.Body.String())
	}
}
