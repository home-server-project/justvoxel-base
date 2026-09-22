package main

import (
	"context"
	"encoding/json"
	"io"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exactSetupApplyFingerprint(t *testing.T) string {
	t.Helper()
	var response adminSetupPlanResponse
	if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &response); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func setupApplyBody(t *testing.T, fingerprint string) string {
	t.Helper()
	var request adminSetupPlanRequest
	if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(adminSetupApplyRequest{PlanFingerprint: fingerprint, Request: request, EULAAccepted: true})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAdminSetupApplyRequiresAdministratorBeforePreflight(t *testing.T) {
	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
	}()
	called := false
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte(validAdminSetupPlanResponse), nil
	}
	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		called = true
		return []byte(`{"configured":false}`), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		attachTestOperationStore(t, s, openTestOperationStore(t))
		rr := httptest.NewRecorder()
		s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, exactSetupApplyFingerprint(t))))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("preflight ran for non-Administrator")
	}
}

func TestLocalRootCanApplyFirstRunSetupWithoutBearerSession(t *testing.T) {
	oldWorker := startSetupWorker
	startSetupWorker = func(_ *server, _ string, _ adminSetupNormalizedPlan, secret []byte) { zeroBytes(secret) }
	defer func() { startSetupWorker = oldWorker }()

	s := surfaceTestServer(t, roleViewer)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
	}()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validAdminSetupPlanResponse), nil
	}
	runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
		if action != "configuration" {
			t.Fatalf("unexpected discovery action: %s", action)
		}
		return []byte(`{"configured":false,"minecraft":{},"backup":{}}`), nil
	}

	fingerprint := exactSetupApplyFingerprint(t)
	req := requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/setup/apply", 0)
	req.Body = io.NopCloser(strings.NewReader(setupApplyBody(t, fingerprint)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("local-root setup apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"created":true`) || !strings.Contains(rr.Body.String(), `"operation_id"`) {
		t.Fatalf("unexpected local-root setup apply response: %s", rr.Body.String())
	}
}

func TestAdminSetupApplyExactReviewedPlanCreatesQueuedOperation(t *testing.T) {
	oldWorker := startSetupWorker
	workerCalls := 0
	startSetupWorker = func(_ *server, _ string, _ adminSetupNormalizedPlan, secret []byte) {
		workerCalls++
		zeroBytes(secret)
	}
	defer func() { startSetupWorker = oldWorker }()
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
	}()
	planCalls := 0
	discoveryCalls := 0
	runAdminSetupPlanHelper = func(_ context.Context, payload []byte) ([]byte, error) {
		planCalls++
		if strings.Contains(strings.ToLower(string(payload)), "password") {
			t.Fatalf("apply preflight payload contains password data: %s", payload)
		}
		return []byte(validAdminSetupPlanResponse), nil
	}
	runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
		discoveryCalls++
		if action != "configuration" {
			t.Fatalf("discovery action = %q", action)
		}
		return []byte(`{"configured":false,"minecraft":{},"backup":{}}`), nil
	}

	fingerprint := exactSetupApplyFingerprint(t)
	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, fingerprint)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rr.Code, rr.Body.String())
	}
	var response adminSetupApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Created || response.Operation == nil {
		t.Fatalf("unexpected apply response: %#v", response)
	}
	if response.Operation.PlanFingerprint != fingerprint || response.Operation.State != operationQueued {
		t.Fatalf("unexpected operation: %#v", response.Operation)
	}
	if planCalls != 1 || discoveryCalls != 1 {
		t.Fatalf("preflight calls plan=%d discovery=%d, want 1/1", planCalls, discoveryCalls)
	}
	current, err := store.currentSetup()
	if err != nil || current == nil || current.OperationID != response.Operation.OperationID {
		t.Fatalf("current=%#v err=%v", current, err)
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want exactly one", workerCalls)
	}
}

func TestAdminSetupApplyRequiresExplicitEULAAcceptance(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	var request adminSetupPlanRequest
	if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(adminSetupApplyRequest{PlanFingerprint: exactSetupApplyFingerprint(t), Request: request})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", string(body)))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"eula_required"`) {
		t.Fatalf("EULA status = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminSetupApplyRejectsStaleReviewedFingerprintWithoutOperation(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
	}()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validAdminSetupPlanResponse), nil
	}
	discoveryCalled := false
	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		discoveryCalled = true
		return []byte(`{"configured":false}`), nil
	}

	stale := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, stale)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"stale_plan"`) {
		t.Fatalf("stale status = %d: %s", rr.Code, rr.Body.String())
	}
	if discoveryCalled {
		t.Fatal("configuration preflight ran after stale fingerprint was detected")
	}
	if current, err := store.currentSetup(); err != nil || current != nil {
		t.Fatalf("current=%#v err=%v, want none", current, err)
	}
}

func TestAdminSetupApplyRejectsConfiguredAppliance(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
	}()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validAdminSetupPlanResponse), nil
	}
	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"configured":true,"minecraft":{},"backup":{}}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, exactSetupApplyFingerprint(t))))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"already_configured"`) {
		t.Fatalf("configured status = %d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentSetup(); err != nil || current != nil {
		t.Fatalf("current=%#v err=%v, want none", current, err)
	}
}

func TestAdminSetupApplySameFingerprintIsIdempotentAndDifferentPlanConflicts(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactSetupApplyFingerprint(t)
	original, _, err := store.beginSetup(fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
	}()
	called := false
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return nil, errors.New("should not run")
	}
	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		called = true
		return nil, errors.New("should not run")
	}

	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, fingerprint)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), original.OperationID) || !strings.Contains(rr.Body.String(), `"created":false`) {
		t.Fatalf("idempotent status = %d: %s", rr.Code, rr.Body.String())
	}

	other := "sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	rr = httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, other)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"setup_busy"`) {
		t.Fatalf("different-plan status = %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("preflight ran while an operation was already active")
	}
}

func TestAdminSetupApplyRejectsUnknownSecretAndTrailingJSON(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))

	oldPlan := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = oldPlan }()
	called := false
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte(validAdminSetupPlanResponse), nil
	}

	body := setupApplyBody(t, exactSetupApplyFingerprint(t))
	withPassword := strings.Replace(body, `"domain":""`, `"domain":"","password":"secret"`, 1)
	oversized := strings.Replace(body, `"Family server"`, `"`+strings.Repeat("x", adminSetupApplyRequestLimit)+`"`, 1)
	for _, tc := range []string{withPassword, body + ` {}`, oversized} {
		rr := httptest.NewRecorder()
		s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", tc))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
	}
	if called {
		t.Fatal("planner ran for rejected apply request")
	}
}

func TestAdminSetupApplyRequiresTransientSMBPasswordWithoutPersistingIt(t *testing.T) {
	oldWorker := startSetupWorker
	startSetupWorker = func(_ *server, _ string, _ adminSetupNormalizedPlan, secret []byte) { zeroBytes(secret) }
	defer func() { startSetupWorker = oldWorker }()
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	var planned adminSetupPlanResponse
	if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &planned); err != nil {
		t.Fatal(err)
	}
	planned.Normalized.Backups.Type = "smb"
	planned.Normalized.Backups.Path = "/var/mnt/backups/minecraft"
	planned.Normalized.Backups.MountPoint = "/var/mnt/backups"
	planned.Normalized.Backups.Source = "//nas/backups"
	planned.Normalized.Backups.ExpectedSource = "//nas/backups"
	planned.Normalized.Backups.Username = "backup-user"
	planned.Normalized.Backups.CredentialsRequired = true
	planned.Requirements.SMBPasswordRequired = true
	planned.Requirements.NetworkBackupValidationOnApply = true
	fingerprint, err := adminSetupPlanFingerprint(planned.SchemaVersion, planned.Normalized, planned.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	plannerJSON, err := json.Marshal(planned)
	if err != nil {
		t.Fatal(err)
	}

	var request adminSetupPlanRequest
	if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
		t.Fatal(err)
	}
	request.Backups.Type = "smb"
	request.Backups.Path = "/var/mnt/backups/minecraft"
	request.Backups.MountPoint = "/var/mnt/backups"
	request.Backups.Source = "//nas/backups"
	request.Backups.Username = "backup-user"
	body, err := json.Marshal(adminSetupApplyRequest{PlanFingerprint: fingerprint, Request: request, EULAAccepted: true})
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminSetupPlanHelper
	oldDiscovery := runAdminDiscoveryHelper
	defer func() {
		runAdminSetupPlanHelper = oldPlan
		runAdminDiscoveryHelper = oldDiscovery
	}()
	runAdminSetupPlanHelper = func(_ context.Context, payload []byte) ([]byte, error) {
		if strings.Contains(string(payload), "super-secret") {
			t.Fatal("SMB secret leaked into authoritative planning payload")
		}
		return plannerJSON, nil
	}
	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"configured":false,"minecraft":{},"backup":{}}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", string(body)))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"smb_password_required"`) {
		t.Fatalf("missing password status = %d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentSetup(); err != nil || current != nil {
		t.Fatalf("operation created without SMB password: %#v err=%v", current, err)
	}

	body, err = json.Marshal(adminSetupApplyRequest{PlanFingerprint: fingerprint, Request: request, SMBPassword: "super-secret", EULAAccepted: true})
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", string(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("SMB apply contract status = %d: %s", rr.Code, rr.Body.String())
	}
	current, err := store.currentSetup()
	if err != nil || current == nil {
		t.Fatalf("current=%#v err=%v", current, err)
	}
	journalJSON, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(journalJSON), "super-secret") {
		t.Fatal("SMB password leaked into operation journal")
	}
}

func TestAdminSetupApplyPreflightFailureIsSanitized(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))

	oldPlan := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = oldPlan }()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("secret helper output /etc/private"), errors.New("exit 1")
	}

	rr := httptest.NewRecorder()
	s.adminSetupApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", setupApplyBody(t, exactSetupApplyFingerprint(t))))
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "/etc/private") {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
}
