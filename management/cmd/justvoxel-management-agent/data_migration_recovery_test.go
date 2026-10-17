package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhysicalMigrationRecoveryReusesOnlyExactDurableOperation(t *testing.T) {
	x := physicalMigrationTestFixture(t, 2)
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	operation, _, err := s.operations.beginDataMigration(testDataMigrationFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	ps, _, err := x.b.migrationParticipants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The test journal directory already exists for the fixture's fixed UUID.
	// Associate an independent operation directory through the same private writer.
	// First exercise legacy state: no participant journal can be inferred.
	if _, err = s.operations.transition(operation.OperationID, operationNeedsAttention, "interrupted", "Interrupted."); err != nil {
		t.Fatal(err)
	}
	calls := 0
	old := runAdminDataMigrationTransactionHelper
	runAdminDataMigrationTransactionHelper = func(_ context.Context, request dataMigrationTransactionRequest, handle func(dataMigrationTransactionEvent) error) error {
		calls++
		if !request.Recovery || request.OperationID != operation.OperationID || len(request.Participants) != 0 {
			t.Error("recovery supplied new identities")
		}
		if err := handle(dataMigrationTransactionEvent{Event: "progress", State: "rolling_back", Stage: "migration_recovery", Status: "Recovering."}); err != nil {
			return err
		}
		return handle(dataMigrationTransactionEvent{Event: "result", Outcome: "rolled_back", Status: "Recovered."})
	}
	defer func() { runAdminDataMigrationTransactionHelper = old }()
	body := `{"operation_id":"` + operation.OperationID + `","confirm_recovery":true}`
	rr := httptest.NewRecorder()
	s.adminDataMigrationRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/data-migration/recover", body))
	if rr.Code != http.StatusConflict || calls != 0 || !strings.Contains(rr.Body.String(), "read-only") {
		t.Fatalf("legacy recovery: %d %s", rr.Code, rr.Body.String())
	}
	// Capture modern evidence for precisely this operation, then retry normally.
	if err := os.Mkdir(filepath.Join(dataMigrationRecoveryRoot, "data-migration-"+operation.OperationID), 0700); err != nil {
		t.Fatal(err)
	}
	if err = x.b.captureMigration(context.Background(), operation.OperationID, ps); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	s.adminDataMigrationRecover(rr, surfaceRequest(http.MethodPost, "/v1/admin/data-migration/recover", body))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("modern recovery: %d %s", rr.Code, rr.Body.String())
	}
	dataMigrationRecoveryMu.Lock()
	dataMigrationRecoveryMu.Unlock()
	current, err := s.operations.get(operation.OperationID)
	if err != nil || current.State != operationRolledBack || calls != 1 {
		t.Fatalf("recovery result: %v %v calls=%d", current.State, err, calls)
	}
}
