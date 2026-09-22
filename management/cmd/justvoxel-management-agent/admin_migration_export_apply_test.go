package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func migrationExportApplyBody(t *testing.T, fingerprint string, confirmed, playersConfirmed bool, smbPassword string) string {
	t.Helper()
	body, err := json.Marshal(adminMigrationExportApplyRequest{
		PlanFingerprint:  fingerprint,
		Request:          testMigrationExportRequest(),
		ExportConfirmed:  confirmed,
		PlayersConfirmed: playersConfirmed,
		SMBPassword:      smbPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAdminMigrationExportApplyExactReviewedPlanCreatesQueuedOperation(t *testing.T) {
	oldWorker := startMigrationExportWorker
	defer func() { startMigrationExportWorker = oldWorker }()
	workerCalls := 0
	startMigrationExportWorker = func(_ *server, _ string, plan migrationExportExecutionPlan) {
		workerCalls++
		if plan.ConfigIdentity == "" || plan.DataIdentity == "" || plan.TargetIdentity == "" ||
			plan.ExpectedMinecraftState != "running" {
			t.Fatalf("worker did not receive reviewed export identity: %#v", plan)
		}
	}

	oldPlan := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = oldPlan }()
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte(validMigrationExportPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactMigrationExportFingerprint(t)

	rr := httptest.NewRecorder()
	s.adminMigrationExportApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/migration/export/apply",
		migrationExportApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminMigrationExportApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Created || response.Operation == nil ||
		response.Operation.OperationType != operationTypeMigrationExport ||
		response.Operation.PlanFingerprint != fingerprint ||
		response.Operation.State != operationQueued {
		t.Fatalf("unexpected export apply response: %#v", response)
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want 1", workerCalls)
	}
}

func TestAdminMigrationExportApplyRequiresConfirmationBeforePlanning(t *testing.T) {
	oldPlan := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = oldPlan }()
	called := false
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(validMigrationExportPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	rr := httptest.NewRecorder()
	s.adminMigrationExportApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/migration/export/apply",
		migrationExportApplyBody(t, exactMigrationExportFingerprint(t), false, false, ""),
	))
	if rr.Code != http.StatusBadRequest ||
		!strings.Contains(rr.Body.String(), `"code":"export_confirmation_required"`) {
		t.Fatalf("confirmation status = %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("export planner ran without explicit confirmation")
	}
}

func TestAdminMigrationExportApplyRejectsStalePlanWithoutOperation(t *testing.T) {
	oldPlan := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = oldPlan }()
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte(validMigrationExportPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	stale := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	rr := httptest.NewRecorder()
	s.adminMigrationExportApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/migration/export/apply",
		migrationExportApplyBody(t, stale, true, false, ""),
	))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"stale_plan"`) {
		t.Fatalf("stale status = %d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentMigration(); err != nil || current != nil {
		t.Fatalf("export operation created from stale plan: %#v err=%v", current, err)
	}
}

func TestAdminMigrationExportApplySameFingerprintReconnectsWithoutPreflight(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactMigrationExportFingerprint(t)
	operation, _, err := store.beginMigrationExport(fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = oldPlan }()
	called := false
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return nil, nil
	}

	rr := httptest.NewRecorder()
	s.adminMigrationExportApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/migration/export/apply",
		migrationExportApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusOK ||
		!strings.Contains(rr.Body.String(), operation.OperationID) ||
		!strings.Contains(rr.Body.String(), `"created":false`) {
		t.Fatalf("reconnect status = %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("export planner ran while reconnecting to current operation")
	}
}

func TestAdminMigrationExportApplyRequiresPlayerConfirmation(t *testing.T) {
	var helper adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	helper.Requirements.PlayersConfirmationRequired = true
	helper.Requirements.Online = 1
	helper.Requirements.Players = []string{"PlayerOne"}
	fingerprint, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(helper)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = oldPlan }()
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return payload, nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	rr := httptest.NewRecorder()
	s.adminMigrationExportApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/migration/export/apply",
		migrationExportApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusBadRequest ||
		!strings.Contains(rr.Body.String(), `"code":"players_confirmation_required"`) {
		t.Fatalf("players status = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminMigrationExportApplyAcceptsLiveMeasurementDrift(t *testing.T) {
	var reviewed adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &reviewed); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := adminMigrationExportPlanFingerprint(reviewed.SchemaVersion, reviewed.Normalized, reviewed.Requirements, reviewed.Context)
	if err != nil {
		t.Fatal(err)
	}

	var current adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &current); err != nil {
		t.Fatal(err)
	}
	current.Normalized.DataBytes += 8192
	current.Normalized.TargetAvailableBytes -= 8192
	current.Normalized.TargetFilesystem = "btrfs"
	current.Requirements.MinecraftState = "stopped"
	payload, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = oldPlan }()
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return payload, nil
	}

	oldWorker := startMigrationExportWorker
	defer func() { startMigrationExportWorker = oldWorker }()
	workerCalls := 0
	startMigrationExportWorker = func(_ *server, _ string, plan migrationExportExecutionPlan) {
		workerCalls++
		if plan.ExpectedMinecraftState != "stopped" {
			t.Fatalf("worker received stale Minecraft state: %#v", plan)
		}
	}

	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	rr := httptest.NewRecorder()
	s.adminMigrationExportApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/migration/export/apply",
		migrationExportApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("live drift status = %d: %s", rr.Code, rr.Body.String())
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want 1", workerCalls)
	}
}

func TestAdminMigrationExportApplyRechecksPlayersWithoutStalePlan(t *testing.T) {
	var reviewed adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &reviewed); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := adminMigrationExportPlanFingerprint(reviewed.SchemaVersion, reviewed.Normalized, reviewed.Requirements, reviewed.Context)
	if err != nil {
		t.Fatal(err)
	}

	var current adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON([]byte(validMigrationExportPlanHelper), &current); err != nil {
		t.Fatal(err)
	}
	current.Requirements.PlayersConfirmationRequired = true
	current.Requirements.Online = 1
	current.Requirements.Players = []string{"PlayerJoinedAfterReview"}
	payload, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminMigrationExportPlanHelper
	defer func() { runAdminMigrationExportPlanHelper = oldPlan }()
	runAdminMigrationExportPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return payload, nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	rr := httptest.NewRecorder()
	s.adminMigrationExportApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/migration/export/apply",
		migrationExportApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusBadRequest ||
		!strings.Contains(rr.Body.String(), `"code":"players_confirmation_required"`) {
		t.Fatalf("player drift status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"code":"stale_plan"`) {
		t.Fatalf("live player change incorrectly invalidated reviewed export: %s", rr.Body.String())
	}
}

