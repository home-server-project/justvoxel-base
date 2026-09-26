package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testSetupFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func openTestOperationStore(t *testing.T) *operationStore {
	t.Helper()
	store, err := openOperationStore(filepath.Join(t.TempDir(), "management"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	return store
}

func attachTestOperationStore(t *testing.T, s *server, store *operationStore) {
	t.Helper()
	s.operations = store
}


func writeTestOperationJournal(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := operationJournal{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     id,
		OperationType:   operationTypeSetup,
		PlanFingerprint: testSetupFingerprint,
		State:           operationSucceeded,
		Stage:           "complete",
		Status:          "Setup completed.",
		StartedAt:       "2026-09-01T12:00:00Z",
		UpdatedAt:       "2026-09-01T12:05:00Z",
		FinishedAt:      "2026-09-01T12:05:00Z",
		Rollback:        operationRollback{State: "not_started"},
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOperationStoreMigratesLegacyManagementLogLayout(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	legacyOperations := filepath.Join(base, "operations")
	legacySetupLogs := filepath.Join(base, "setup-logs")
	id := "12345678-1234-4123-8123-123456789abc"

	writeTestOperationJournal(t, legacyOperations, id)
	if err := os.MkdirAll(legacySetupLogs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySetupLogs, id+".log"), []byte("legacy setup log\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()

	for _, legacy := range []string{legacyOperations, legacySetupLogs} {
		if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy directory still exists after migration: %s err=%v", legacy, err)
		}
	}
	if store.operationsDir != filepath.Join(base, "logs", "operations") {
		t.Fatalf("operationsDir = %q", store.operationsDir)
	}
	if store.setupLogsDir != filepath.Join(base, "logs", "setup-logs") {
		t.Fatalf("setupLogsDir = %q", store.setupLogsDir)
	}
	if _, err := os.Stat(filepath.Join(store.operationsDir, id+".json")); err != nil {
		t.Fatalf("migrated operation journal missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.setupLogsDir, id+".log")); err != nil {
		t.Fatalf("migrated setup log missing: %v", err)
	}
	if _, err := store.get(id); err != nil {
		t.Fatalf("migrated operation journal was not loaded: %v", err)
	}
}

func TestOperationStoreMovesLegacyDataIntoEmptyCurrentDirectory(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	legacyOperations := filepath.Join(base, "operations")
	currentOperations := filepath.Join(base, "logs", "operations")
	id := "12345678-1234-4123-8123-123456789abc"

	writeTestOperationJournal(t, legacyOperations, id)
	if err := os.MkdirAll(currentOperations, 0o700); err != nil {
		t.Fatal(err)
	}

	store, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()

	if _, err := os.Stat(legacyOperations); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy operations directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(currentOperations, id+".json")); err != nil {
		t.Fatalf("legacy operation journal was not preserved: %v", err)
	}
}

func TestOperationStoreRejectsPopulatedLegacyAndCurrentLogDirectories(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	legacyOperations := filepath.Join(base, "operations")
	currentOperations := filepath.Join(base, "logs", "operations")
	if err := os.MkdirAll(legacyOperations, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(currentOperations, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyOperations, "legacy.txt"), []byte("legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(currentOperations, "current.txt"), []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := openOperationStore(base)
	if store != nil {
		_ = store.close()
	}
	if err == nil || !strings.Contains(err.Error(), "both legacy path") {
		t.Fatalf("conflicting layout error = %v", err)
	}
	for _, path := range []string{
		filepath.Join(legacyOperations, "legacy.txt"),
		filepath.Join(currentOperations, "current.txt"),
	} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("conflict handling changed existing data at %s: %v", path, statErr)
		}
	}
}

func TestOperationStorePreflightsBothLegacyDirectoriesBeforeMovingEither(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	legacyOperations := filepath.Join(base, "operations")
	legacySetupLogs := filepath.Join(base, "setup-logs")
	currentSetupLogs := filepath.Join(base, "logs", "setup-logs")
	id := "12345678-1234-4123-8123-123456789abc"

	writeTestOperationJournal(t, legacyOperations, id)
	if err := os.MkdirAll(legacySetupLogs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(currentSetupLogs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySetupLogs, "legacy.log"), []byte("legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(currentSetupLogs, "current.log"), []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := openOperationStore(base)
	if store != nil {
		_ = store.close()
	}
	if err == nil {
		t.Fatal("expected setup-log layout conflict")
	}
	if _, statErr := os.Stat(filepath.Join(legacyOperations, id+".json")); statErr != nil {
		t.Fatalf("operations moved before setup-log preflight failed: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(base, "logs", "operations")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new operations directory unexpectedly created before migration: %v", statErr)
	}
}

func TestOperationStoreCreatesPrivatePersistentJournal(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	store, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()

	journal, created, err := store.beginSetup(testSetupFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !created || !validOperationID(journal.OperationID) {
		t.Fatalf("created=%t operation_id=%q", created, journal.OperationID)
	}
	if journal.State != operationQueued || journal.OperationType != operationTypeSetup {
		t.Fatalf("unexpected initial journal: %#v", journal)
	}

	for _, path := range []string{
		base,
		filepath.Join(base, "logs"),
		filepath.Join(base, "logs", "operations"),
		filepath.Join(base, "logs", "setup-logs"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode = %o, want 700", path, got)
		}
	}
	journalPath := filepath.Join(base, "logs", "operations", journal.OperationID+".json")
	info, err := os.Stat(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal mode = %o, want 600", got)
	}
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "token", "cookie", "csrf", "request_body", "stdout", "stderr", "environment", "argv"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("journal unexpectedly contains forbidden field %q: %s", forbidden, data)
		}
	}
}

func TestOperationStoreSameSetupIsIdempotentAndDifferentPlanIsLocked(t *testing.T) {
	store := openTestOperationStore(t)
	first, created, err := store.beginSetup(testSetupFingerprint)
	if err != nil || !created {
		t.Fatalf("first begin: created=%t err=%v", created, err)
	}
	second, created, err := store.beginSetup(testSetupFingerprint)
	if err != nil || created {
		t.Fatalf("second begin: created=%t err=%v", created, err)
	}
	if second.OperationID != first.OperationID {
		t.Fatalf("same setup created a different operation: %q != %q", second.OperationID, first.OperationID)
	}
	other := "sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	if _, _, err := store.beginSetup(other); !errors.Is(err, errSetupOperationBusy) {
		t.Fatalf("different plan error = %v, want setup busy", err)
	}
}

func TestOperationStoreConcurrentBeginCreatesExactlyOneSetup(t *testing.T) {
	store := openTestOperationStore(t)
	const callers = 24
	ids := make(chan string, callers)
	created := make(chan bool, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			journal, wasCreated, err := store.beginSetup(testSetupFingerprint)
			if err == nil {
				ids <- journal.OperationID
				created <- wasCreated
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(created)
	close(errs)

	var firstID string
	createdCount := 0
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range ids {
		if firstID == "" {
			firstID = id
		}
		if id != firstID {
			t.Fatalf("concurrent setup returned different ids: %q != %q", id, firstID)
		}
	}
	for value := range created {
		if value {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
}

func TestOperationStoreHostLockBlocksSecondStore(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	if _, _, err := first.beginSetup(testSetupFingerprint); err != nil {
		t.Fatal(err)
	}

	second, err := openOperationStore(base)
	if second != nil {
		_ = second.close()
	}
	if !errors.Is(err, errSetupLockBusy) {
		t.Fatalf("second store error = %v, want setup lock busy", err)
	}
}

func TestOperationStoreRestartMarksInterruptedSetupNeedsAttention(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginSetup(testSetupFingerprint)
	if err != nil {
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
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != journal.OperationID {
		t.Fatalf("current operation = %#v, want %s", current, journal.OperationID)
	}
	if current.State != operationNeedsAttention || current.Stage != "interrupted" || current.InterruptedAt == "" {
		t.Fatalf("interrupted journal not represented conservatively: %#v", current)
	}
	if _, _, err := second.beginSetup("sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"); !errors.Is(err, errSetupOperationBusy) {
		t.Fatalf("new setup after interruption error = %v, want setup busy", err)
	}
}

func TestOperationStoreCompletedOperationSurvivesRestartAndReleasesCurrent(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginSetup(testSetupFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	transitions := []struct {
		state  operationState
		stage  string
		status string
	}{
		{operationValidating, "validating", "Checking reviewed setup."},
		{operationRunning, "running", "Running setup transaction."},
		{operationVerifying, "verifying", "Checking setup result."},
		{operationSucceeded, "complete", "Setup completed."},
	}
	for _, transition := range transitions {
		if _, err := first.transition(journal.OperationID, transition.state, transition.stage, transition.status); err != nil {
			t.Fatalf("transition to %s: %v", transition.state, err)
		}
	}
	if current, err := first.currentSetup(); err != nil || current != nil {
		t.Fatalf("current after success = %#v err=%v", current, err)
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}

	second, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	loaded, err := second.get(journal.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != operationSucceeded || loaded.FinishedAt == "" || loaded.InterruptedAt != "" {
		t.Fatalf("completed journal changed on restart: %#v", loaded)
	}
	if current, err := second.currentSetup(); err != nil || current != nil {
		t.Fatalf("current after restart = %#v err=%v", current, err)
	}
}

func TestOperationStoreRejectsIllegalTransitionsAndUnsafeStatus(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginSetup(testSetupFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(journal.OperationID, operationSucceeded, "complete", "Setup completed."); err == nil {
		t.Fatal("queued -> succeeded unexpectedly accepted")
	}
	if _, err := store.transition(journal.OperationID, operationValidating, "bad stage", "Checking setup."); err == nil {
		t.Fatal("unsafe stage unexpectedly accepted")
	}
	if _, err := store.transition(journal.OperationID, operationValidating, "validating", "line one\nline two"); err == nil {
		t.Fatal("multiline status unexpectedly accepted")
	}
}

func TestOperationStatusAPIsRequireAdministratorAndReturnSafeState(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginSetup(testSetupFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, store)
		mux := http.NewServeMux()
		registerAdminOperationRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/operations/"+journal.OperationID, ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}

	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, store)
	mux := http.NewServeMux()
	registerAdminOperationRoutes(mux, s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/operations/"+journal.OperationID, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("operation status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminOperationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.OperationID != journal.OperationID || response.Operation.PlanFingerprint != testSetupFingerprint {
		t.Fatalf("unexpected operation response: %#v", response)
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup/current-operation", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatalf("current operation = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestOperationStatusAPIMalformedUnknownAndEmptyCurrent(t *testing.T) {
	store := openTestOperationStore(t)
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, store)
	mux := http.NewServeMux()
	registerAdminOperationRoutes(mux, s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/operations/not-a-uuid", ""))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed id status = %d, want 400", rr.Code)
	}

	unknown := "12345678-1234-4123-8123-123456789abc"
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/operations/"+unknown, ""))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", rr.Code)
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup/current-operation", ""))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"operation":null}` {
		t.Fatalf("empty current operation = %d: %q", rr.Code, rr.Body.String())
	}
}


func TestFactoryResetNeedsAttentionCanBeResolvedWithoutRetry(t *testing.T) {
	store := openTestOperationStore(t)
	fingerprint := testSetupFingerprint

	operation, created, err := store.beginFactoryReset(fingerprint)
	if err != nil || !created {
		t.Fatalf("begin factory reset: created=%t err=%v", created, err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "reset_failed", "Factory reset stopped before completion."); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.beginRestore(fingerprint); !errors.Is(err, errFactoryResetOperationBusy) {
		t.Fatalf("Restore was not blocked before resolution: %v", err)
	}

	resolved, err := store.resolveFactoryReset(operation.OperationID, "Current server state kept.")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != operationResolved || resolved.Stage != "resolved" || resolved.FinishedAt == "" {
		t.Fatalf("unexpected resolved journal: %#v", resolved)
	}
	if current, err := store.currentFactoryReset(); err != nil || current != nil {
		t.Fatalf("factory reset remained current after resolution: %#v err=%v", current, err)
	}

	restore, created, err := store.beginRestore(fingerprint)
	if err != nil || !created || restore.OperationType != operationTypeRestore {
		t.Fatalf("Restore remained blocked after resolution: created=%t operation=%#v err=%v", created, restore, err)
	}
}

func TestFactoryResetResolutionRequiresNeedsAttention(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginFactoryReset(testSetupFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.resolveFactoryReset(operation.OperationID, "Keep current server."); err == nil {
		t.Fatal("queued factory reset unexpectedly resolved")
	}
	if current, err := store.currentFactoryReset(); err != nil || current == nil || current.OperationID != operation.OperationID {
		t.Fatalf("factory reset current state changed after rejected resolution: %#v err=%v", current, err)
	}
}
