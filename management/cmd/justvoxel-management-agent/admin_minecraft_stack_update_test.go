package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMinecraftStackStatusStatesAndPrivateArtifactMetadata(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := runAdminVersionStatus
	defer func() { runAdminVersionStatus = old }()
	admin := surfaceTestServer(t, roleAdministrator)
	for _, state := range []string{"up_to_date", "updates_available", "waiting_for_compatibility", "unavailable"} {
		data, err := json.Marshal(map[string]any{"stack_state": state, "update_available": state == "updates_available", "viaversion_state": "managed_automatically", "managed_artifacts": map[string]string{"sha256": "private"}})
		if err != nil {
			t.Fatal(err)
		}
		runAdminVersionStatus = func(_ context.Context, _, _, _, _ string) ([]byte, error) { return data, nil }
		rr := httptest.NewRecorder()
		admin.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/version/status", ""))
		var result adminVersionStatus
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &result) != nil || result.StackState != state || result.UpdateAvailable != (state == "updates_available") || result.ViaVersionState != "managed_automatically" {
			t.Fatalf("state %s: %s", state, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "private") || strings.Contains(rr.Body.String(), "managed_artifacts") {
			t.Fatal("private download metadata reached browser")
		}
	}
}

func TestMinecraftStackApplyAuthorizationStrictRequestAndReview(t *testing.T) {
	whitelistSurfaceFixture(t)
	oldStatus, oldWorker := runAdminVersionStatus, startMinecraftStackUpdateWorker
	defer func() { runAdminVersionStatus = oldStatus; startMinecraftStackUpdateWorker = oldWorker }()
	reads, starts := 0, 0
	runAdminVersionStatus = func(_ context.Context, _, _, _, _ string) ([]byte, error) {
		reads++
		return []byte(`{"stack_state":"updates_available","update_available":true,"server_supported":true,"plan_fingerprint":"` + testMinecraftResetFingerprint + `"}`), nil
	}
	startMinecraftStackUpdateWorker = func(_ *server, _ string, request minecraftStackUpdateRequest, _ session) {
		starts++
		if !request.ConfirmPlayers {
			t.Fatal("explicit player consent lost")
		}
	}
	body := `{"plan_fingerprint":"` + testMinecraftResetFingerprint + `","confirm_players":true}`
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminMinecraftStackUpdate(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update", body))
		if rr.Code != http.StatusForbidden || reads != 0 || starts != 0 {
			t.Fatalf("role %s reached apply", role)
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	for _, invalid := range []string{`{}`, body + ` {}`, `{"plan_fingerprint":"` + testMinecraftResetFingerprint + `","url":"https://example.invalid"}`} {
		rr := httptest.NewRecorder()
		s.adminMinecraftStackUpdate(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update", invalid))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid request: %d %s", rr.Code, rr.Body.String())
		}
	}
	stale := strings.Replace(body, testMinecraftResetFingerprint, "sha256:"+strings.Repeat("7", 64), 1)
	rr := httptest.NewRecorder()
	s.adminMinecraftStackUpdate(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update", stale))
	if rr.Code != http.StatusConflict || starts != 0 {
		t.Fatal("stale plan applied")
	}
	for i := 0; i < 2; i++ {
		rr = httptest.NewRecorder()
		s.adminMinecraftStackUpdate(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update", body))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("apply: %d %s", rr.Code, rr.Body.String())
		}
	}
	if starts != 1 {
		t.Fatal("duplicate apply started a second worker")
	}
	if _, _, err := s.operations.beginMinecraftReset(testMinecraftResetFingerprint); err == nil {
		t.Fatal("reset started during update")
	}
	if reset, err := s.operations.currentMinecraftReset(); err != nil || reset != nil {
		t.Fatal("update presented as reset")
	}
}

func TestMinecraftStackWorkerFailureIsPersistentAndNeverRetries(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := runMinecraftStackUpdate
	defer func() { runMinecraftStackUpdate = old }()
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	journal, _, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftUpdate, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runMinecraftStackUpdate = func(_ context.Context, _ string, _ bool, _, _ string) error {
		calls++
		return errors.New("fixture failure")
	}
	if executeMinecraftStackUpdate(context.Background(), s, journal.OperationID, minecraftStackUpdateRequest{InstanceID: "jv-a1b2c3", PlanFingerprint: testMinecraftResetFingerprint}, session{}) == nil {
		t.Fatal("failure reported success")
	}
	current, err := s.operations.currentMinecraftMaintenance()
	if err != nil || current == nil || current.State != operationNeedsAttention || calls != 1 {
		t.Fatalf("failure state: %#v %v calls=%d", current, err, calls)
	}
}

func TestMinecraftStackWorkerSuccessAndAcknowledgeRequireRuntimeVerification(t *testing.T) {
	whitelistSurfaceFixture(t)
	oldRun, oldVerify := runMinecraftStackUpdate, verifyMinecraftStack
	defer func() { runMinecraftStackUpdate = oldRun; verifyMinecraftStack = oldVerify }()
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	journal, _, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftUpdate, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	runMinecraftStackUpdate = func(_ context.Context, fingerprint string, players bool, software, tag string) error {
		if fingerprint != testMinecraftResetFingerprint || !players || software != "purpur" || tag != "latest" {
			t.Fatal("successful combined request lost selections")
		}
		return nil
	}
	if err := executeMinecraftStackUpdate(context.Background(), s, journal.OperationID, minecraftStackUpdateRequest{InstanceID: "jv-a1b2c3", ServerType: "purpur", ImageTag: "latest", PlanFingerprint: testMinecraftResetFingerprint, ConfirmPlayers: true}, session{}); err != nil {
		t.Fatal(err)
	}
	finished, err := s.operations.get(journal.OperationID)
	if err != nil || finished.State != operationSucceeded {
		t.Fatalf("completion: %#v %v", finished, err)
	}
	journal, _, err = s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftUpdate, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.operations.transition(journal.OperationID, operationNeedsAttention, "interrupted", "Review current server."); err != nil {
		t.Fatal(err)
	}
	body := `{"operation_id":"` + journal.OperationID + `"}`
	verifyMinecraftStack = func(_ context.Context) error { return errors.New("not ready") }
	rr := httptest.NewRecorder()
	s.adminMinecraftStackUpdateAcknowledge(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update/acknowledge", body))
	if rr.Code != http.StatusConflict {
		t.Fatal("unverified runtime acknowledged")
	}
	verifyMinecraftStack = func(_ context.Context) error { return nil }
	rr = httptest.NewRecorder()
	s.adminMinecraftStackUpdateAcknowledge(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update/acknowledge", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("acknowledge: %s", rr.Body.String())
	}
	if current, err := s.operations.currentMinecraftMaintenance(); err != nil || current != nil {
		t.Fatal("acknowledged update retained locks")
	}
}

func TestMinecraftStackInterruptedUpdateRecoversWithoutRetry(t *testing.T) {
	whitelistSurfaceFixture(t)
	base := t.TempDir()
	first, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := first.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftUpdate, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.transition(journal.OperationID, operationValidating, "validating", "Checking stack."); err != nil {
		t.Fatal(err)
	}
	if _, err = first.transition(journal.OperationID, operationRunning, "updating", "Updating stack."); err != nil {
		t.Fatal(err)
	}
	if err = first.close(); err != nil {
		t.Fatal(err)
	}
	second, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	current, err := second.currentMinecraftMaintenance()
	if err != nil || current == nil || current.OperationType != operationTypeMinecraftUpdate || current.State != operationNeedsAttention || current.Stage != "interrupted" {
		t.Fatalf("recovery: %#v %v", current, err)
	}
	if _, _, err = second.beginMinecraftMaintenance("sha256:"+strings.Repeat("7", 64), operationTypeMinecraftUpdate, "jv-a1b2c3"); err == nil {
		t.Fatal("interrupted update automatically retried")
	}
}

func TestCombinedStackSelectionValidationAndWorkerBinding(t *testing.T) {
	whitelistSurfaceFixture(t)
	oldStatus, oldWorker := runAdminVersionStatus, startMinecraftStackUpdateWorker
	defer func() { runAdminVersionStatus = oldStatus; startMinecraftStackUpdateWorker = oldWorker }()
	reads, starts := 0, 0
	runAdminVersionStatus = func(_ context.Context, policy, version, software, tag string) ([]byte, error) {
		reads++
		if policy != "" || version != "" || software != "purpur" || tag != "release_1.2-3" {
			t.Fatalf("combined proposal lost: %q %q %q %q", policy, version, software, tag)
		}
		return []byte(`{"stack_state":"updates_available","update_available":true,"server_supported":true,"plan_fingerprint":"` + testMinecraftResetFingerprint + `"}`), nil
	}
	startMinecraftStackUpdateWorker = func(_ *server, _ string, request minecraftStackUpdateRequest, _ session) {
		starts++
		if request.ServerType != "purpur" || request.ImageTag != "release_1.2-3" || request.PlanFingerprint != testMinecraftResetFingerprint || !request.ConfirmPlayers {
			t.Fatalf("worker did not receive reviewed selections: %#v", request)
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	for _, tag := range []string{"../bad", "-bad", "tag:other", strings.Repeat("a", 129)} {
		body, err := json.Marshal(minecraftStackUpdateRequest{InstanceID: "jv-a1b2c3", ServerType: "purpur", ImageTag: tag, PlanFingerprint: testMinecraftResetFingerprint})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		s.adminMinecraftStackUpdate(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update", string(body)))
		if rr.Code != http.StatusBadRequest || reads != 0 || starts != 0 {
			t.Fatalf("invalid tag reached maintenance: %q %d", tag, rr.Code)
		}
	}
	body, err := json.Marshal(minecraftStackUpdateRequest{InstanceID: "jv-a1b2c3", ServerType: "purpur", ImageTag: "release_1.2-3", PlanFingerprint: testMinecraftResetFingerprint, ConfirmPlayers: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		s.adminMinecraftStackUpdate(rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/version/update", string(body)))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("combined update: %d %s", rr.Code, rr.Body.String())
		}
	}
	if starts != 1 {
		t.Fatalf("combined proposal started %d operations", starts)
	}
}

func TestCombinedStackWorkerPersistsIncompleteRecovery(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := runMinecraftStackUpdate
	defer func() { runMinecraftStackUpdate = old }()
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	journal, _, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftUpdate, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runMinecraftStackUpdate = func(_ context.Context, fingerprint string, players bool, software, tag string) error {
		calls++
		if fingerprint != testMinecraftResetFingerprint || !players || software != "purpur" || tag != "latest" {
			t.Fatal("reviewed combined request lost")
		}
		return &minecraftStackMaintenanceError{message: "Previous server recovery is incomplete."}
	}
	err = executeMinecraftStackUpdate(context.Background(), s, journal.OperationID, minecraftStackUpdateRequest{InstanceID: "jv-a1b2c3", ServerType: "purpur", ImageTag: "latest", PlanFingerprint: testMinecraftResetFingerprint, ConfirmPlayers: true}, session{})
	current, readErr := s.operations.currentMinecraftMaintenance()
	if err == nil || readErr != nil || current == nil || current.State != operationNeedsAttention || !strings.Contains(current.Status, "recovery is incomplete") || calls != 1 {
		t.Fatalf("incomplete recovery: %#v %v %v calls=%d", current, err, readErr, calls)
	}
}
