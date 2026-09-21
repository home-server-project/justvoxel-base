package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exactDataMigrationFingerprint(t *testing.T) string {
	t.Helper()
	var helper adminDataMigrationHelperResponse
	if err := decodeAdminDataMigrationJSON([]byte(validDataMigrationPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := adminDataMigrationPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func dataMigrationApplyBody(t *testing.T, fingerprint string, migrationConfirmed, playersConfirmed bool, confirmation string) string {
	t.Helper()
	body, err := json.Marshal(adminDataMigrationApplyRequest{
		PlanFingerprint: fingerprint,
		Request: adminDataMigrationRequest{
			Operation:  "use_partition",
			Device:     "/dev/vdb1",
			MountPoint: "/var/mnt/justvoxel-data",
			Path:       "/var/mnt/justvoxel-data/minecraft",
			SizeGiB:    "all",
		},
		MigrationConfirmed: migrationConfirmed,
		Confirmation:       confirmation,
		PlayersConfirmed:   playersConfirmed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAdminDataMigrationApplyExactReviewedPlanCreatesQueuedOperation(t *testing.T) {
	oldWorker := startDataMigrationWorker
	workerCalls := 0
	startDataMigrationWorker = func(_ *server, _ string, plan dataMigrationExecutionPlan) {
		workerCalls++
		if plan.TargetIdentity == "" || plan.ConfigIdentity == "" || plan.ExpectedMinecraftState != "running" {
			t.Fatalf("worker did not receive reviewed execution identity: %#v", plan)
		}
	}
	defer func() { startDataMigrationWorker = oldWorker }()

	oldPlan := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = oldPlan }()
	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte(validDataMigrationPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactDataMigrationFingerprint(t)

	rr := httptest.NewRecorder()
	s.adminDataMigrationApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/data-migration/apply",
		dataMigrationApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminDataMigrationApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Created || response.Operation == nil ||
		response.Operation.OperationType != operationTypeDataMigration ||
		response.Operation.PlanFingerprint != fingerprint ||
		response.Operation.State != operationQueued {
		t.Fatalf("unexpected migration apply response: %#v", response)
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want 1", workerCalls)
	}
}

func TestAdminDataMigrationApplyRequiresExplicitMigrationConfirmationBeforePlanning(t *testing.T) {
	oldPlan := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = oldPlan }()
	called := false
	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(validDataMigrationPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	rr := httptest.NewRecorder()
	s.adminDataMigrationApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/data-migration/apply",
		dataMigrationApplyBody(t, exactDataMigrationFingerprint(t), false, false, ""),
	))
	if rr.Code != http.StatusBadRequest ||
		!strings.Contains(rr.Body.String(), `"code":"migration_confirmation_required"`) {
		t.Fatalf("confirmation status = %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("migration planner ran without explicit migration confirmation")
	}
}

func TestAdminDataMigrationApplyRejectsStalePlanWithoutOperation(t *testing.T) {
	oldPlan := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = oldPlan }()
	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte(validDataMigrationPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	stale := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	rr := httptest.NewRecorder()
	s.adminDataMigrationApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/data-migration/apply",
		dataMigrationApplyBody(t, stale, true, false, ""),
	))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"stale_plan"`) {
		t.Fatalf("stale status = %d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentDataMigration(); err != nil || current != nil {
		t.Fatalf("migration operation created from stale plan: %#v err=%v", current, err)
	}
}

func TestAdminDataMigrationApplySameFingerprintReconnectsWithoutPreflight(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactDataMigrationFingerprint(t)
	operation, _, err := store.beginDataMigration(fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = oldPlan }()
	called := false
	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return nil, nil
	}

	rr := httptest.NewRecorder()
	s.adminDataMigrationApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/data-migration/apply",
		dataMigrationApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusOK ||
		!strings.Contains(rr.Body.String(), operation.OperationID) ||
		!strings.Contains(rr.Body.String(), `"created":false`) {
		t.Fatalf("reconnect status = %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("migration planner ran while reconnecting to current operation")
	}
}

func TestAdminDataMigrationApplyRequiresPlayerConfirmationFromAuthoritativePlan(t *testing.T) {
	var helper adminDataMigrationHelperResponse
	if err := decodeAdminDataMigrationJSON([]byte(validDataMigrationPlanHelper), &helper); err != nil {
		t.Fatal(err)
	}
	helper.Requirements.PlayersConfirmationRequired = true
	helper.Requirements.Online = 1
	helper.Requirements.Players = []string{"PlayerOne"}
	fingerprint, err := adminDataMigrationPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(helper)
	if err != nil {
		t.Fatal(err)
	}

	oldPlan := runAdminDataMigrationPlanHelper
	defer func() { runAdminDataMigrationPlanHelper = oldPlan }()
	runAdminDataMigrationPlanHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return payload, nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	rr := httptest.NewRecorder()
	s.adminDataMigrationApply(rr, surfaceRequest(
		http.MethodPost,
		"/v1/admin/data-migration/apply",
		dataMigrationApplyBody(t, fingerprint, true, false, ""),
	))
	if rr.Code != http.StatusBadRequest ||
		!strings.Contains(rr.Body.String(), `"code":"players_confirmation_required"`) {
		t.Fatalf("players status = %d: %s", rr.Code, rr.Body.String())
	}
}
