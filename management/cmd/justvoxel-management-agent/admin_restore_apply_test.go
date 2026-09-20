package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exactRestoreApplyFingerprint(t *testing.T) string {
	t.Helper()
	var helper adminRestorePlanHelperResponse
	if err := decodeAdminRestorePlanHelperResponse([]byte(validRestorePlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := adminRestorePlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func restoreApplyBody(t *testing.T, fingerprint string, destructive, players bool) string {
	t.Helper()
	body, err := json.Marshal(adminRestoreApplyRequest{
		PlanFingerprint: fingerprint,
		Request: adminRestorePlanRequest{BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world"},
		DestructiveConfirmed: destructive,
		PlayersConfirmed: players,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAdminRestoreApplyExactReviewedPlanCreatesQueuedOperation(t *testing.T) {
	oldWorker := startRestoreWorker
	workerCalls := 0
	startRestoreWorker = func(_ *server, _ string, plan restoreExecutionPlan) {
		workerCalls++
		if plan.ArchiveIdentity == "" {
			t.Fatal("worker did not receive reviewed archive identity")
		}
	}
	defer func() { startRestoreWorker = oldWorker }()

	oldPlan := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = oldPlan }()
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validRestorePlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactRestoreApplyFingerprint(t)

	rr := httptest.NewRecorder()
	s.adminRestoreApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/apply", restoreApplyBody(t, fingerprint, true, false)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminRestoreApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Created || response.Operation == nil || response.Operation.OperationType != operationTypeRestore {
		t.Fatalf("unexpected Restore apply response: %#v", response)
	}
	if response.Operation.PlanFingerprint != fingerprint || response.Operation.State != operationQueued {
		t.Fatalf("unexpected Restore operation: %#v", response.Operation)
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want 1", workerCalls)
	}
}

func TestAdminRestoreApplyRequiresDestructiveConfirmationBeforePreflight(t *testing.T) {
	oldPlan := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = oldPlan }()
	called := false
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte(validRestorePlanHelper), nil
	}
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))

	rr := httptest.NewRecorder()
	s.adminRestoreApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/apply", restoreApplyBody(t, exactRestoreApplyFingerprint(t), false, false)))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"destructive_confirmation_required"`) {
		t.Fatalf("confirmation status = %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("Restore planner ran without destructive confirmation")
	}
}

func TestAdminRestoreApplyRejectsStalePlanWithoutOperation(t *testing.T) {
	oldPlan := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = oldPlan }()
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validRestorePlanHelper), nil
	}
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	stale := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	rr := httptest.NewRecorder()
	s.adminRestoreApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/apply", restoreApplyBody(t, stale, true, false)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"stale_plan"`) {
		t.Fatalf("stale status = %d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentRestore(); err != nil || current != nil {
		t.Fatalf("Restore operation created from stale plan: %#v err=%v", current, err)
	}
}

func TestAdminRestoreApplyRequiresPlayerConfirmationWhenReviewedPlayersOnline(t *testing.T) {
	var helper adminRestorePlanHelperResponse
	if err := decodeAdminRestorePlanHelperResponse([]byte(validRestorePlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	helper.Requirements.PlayersConfirmationRequired = true
	helper.Requirements.Online = 1
	helper.Requirements.Players = []string{"PlayerOne"}
	fingerprint, err := adminRestorePlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(helper)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = oldPlan }()
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return payload, nil
	}
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))

	rr := httptest.NewRecorder()
	s.adminRestoreApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/apply", restoreApplyBody(t, fingerprint, true, false)))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"players_confirmation_required"`) {
		t.Fatalf("players status = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminRestoreApplySameFingerprintReconnectsWithoutPreflight(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactRestoreApplyFingerprint(t)
	operation, _, err := store.beginRestore(fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = oldPlan }()
	called := false
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return nil, nil
	}
	rr := httptest.NewRecorder()
	s.adminRestoreApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/apply", restoreApplyBody(t, fingerprint, true, false)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), operation.OperationID) || !strings.Contains(rr.Body.String(), `"created":false"`) {
		t.Fatalf("reconnect status = %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("Restore planner ran while reconnecting to current operation")
	}
}

func TestLocalRootCanApplyRestoreWithoutBearerSession(t *testing.T) {
	oldWorker := startRestoreWorker
	startRestoreWorker = func(_ *server, _ string, _ restoreExecutionPlan) {}
	defer func() { startRestoreWorker = oldWorker }()
	oldPlan := runAdminRestorePlanHelper
	defer func() { runAdminRestorePlanHelper = oldPlan }()
	runAdminRestorePlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validRestorePlanHelper), nil
	}

	s := surfaceTestServer(t, roleViewer)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	body := restoreApplyBody(t, exactRestoreApplyFingerprint(t), true, false)
	req := requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/restore/apply", 0)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.adminRestoreApply(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("local-root Restore apply returned %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminRestoreApplyRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	fingerprint := exactRestoreApplyFingerprint(t)
	body := restoreApplyBody(t, fingerprint, true, false)
	for _, invalid := range []string{
		strings.Replace(body, `"mode":"world"`, `"mode":"world","path":"/tmp/x"`, 1),
		body + ` {}`,
	} {
		rr := httptest.NewRecorder()
		s.adminRestoreApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/restore/apply", invalid))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("invalid request status = %d: %s", rr.Code, rr.Body.String())
		}
	}
}
