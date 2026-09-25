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

const validMinecraftResetPlanHelper = `{"ok":true,"schema_version":"v1","mode":"minecraft","data_path":"/var/lib/justvoxel/minecraft","data_scope":"internal","data_action":"delete","backup_path":"/var/lib/justvoxel/backups","backup_action":"preserve","storage_layout_action":"preserve","authentication_action":"preserve","webui_users_action":"preserve","players_online":0,"players":[],"warnings":[]}`

func exactMinecraftResetFingerprint(t *testing.T) string {
	t.Helper()
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := minecraftResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func TestAdminMinecraftResetPlanComputesFingerprintAndPreservesNonMinecraftState(t *testing.T) {
	old := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = old }()
	runAdminMinecraftResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "plan" {
			t.Fatalf("unexpected reset helper args: %#v", args)
		}
		return []byte(validMinecraftResetPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminMinecraftResetPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/plan", "{}"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"\"plan_fingerprint\":\"sha256:",
		"\"data_action\":\"delete\"",
		"\"backup_action\":\"preserve\"",
		"\"storage_layout_action\":\"preserve\"",
		"\"authentication_action\":\"preserve\"",
		"\"webui_users_action\":\"preserve\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("reset plan missing %q: %s", want, body)
		}
	}
}

func TestAdminMinecraftResetPlanRejectsChangedPreservationContract(t *testing.T) {
	old := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = old }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(strings.Replace(validMinecraftResetPlanHelper, "\"backup_action\":\"preserve\"", "\"backup_action\":\"delete\"", 1)), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminMinecraftResetPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/plan", "{}"))
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "\"code\":\"invalid_plan\"") {
		t.Fatalf("changed preservation contract status=%d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminMinecraftResetPlanAllowsExternalMinecraftDataOnlyWhenPreserved(t *testing.T) {
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.DataScope = "external"
	plan.DataAction = "preserve"
	if err := validateAdminMinecraftResetPlan(plan); err != nil {
		t.Fatalf("external preserved data rejected: %v", err)
	}
	plan.DataAction = "delete"
	if err := validateAdminMinecraftResetPlan(plan); err == nil {
		t.Fatal("external Minecraft data deletion unexpectedly accepted")
	}
}

func TestAdminMinecraftResetApplyCreatesQueuedOperationFromExactPlan(t *testing.T) {
	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(validMinecraftResetPlanHelper), nil
	}

	oldWorker := startMinecraftResetWorker
	defer func() { startMinecraftResetWorker = oldWorker }()
	workerCalls := 0
	startMinecraftResetWorker = func(_ *server, _ string, fingerprint string, _ session) {
		workerCalls++
		if fingerprint != exactMinecraftResetFingerprint(t) {
			t.Fatalf("worker fingerprint = %q", fingerprint)
		}
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, err := json.Marshal(adminMinecraftResetApplyRequest{
		PlanFingerprint: exactMinecraftResetFingerprint(t),
		ConfirmPlayers:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminMinecraftResetApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Created || response.Operation == nil ||
		response.Operation.OperationType != operationTypeMinecraftReset ||
		response.Operation.State != operationQueued {
		t.Fatalf("unexpected reset apply response: %#v", response)
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want 1", workerCalls)
	}
}

func TestAdminMinecraftResetApplyRequiresPlayerConfirmation(t *testing.T) {
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.PlayersOnline = 2
	plan.Players = []string{"Alex", "Steve"}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := minecraftResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}

	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return payload, nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: fingerprint})
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusConflict ||
		!strings.Contains(rr.Body.String(), "\"code\":\"players_online\"") ||
		!strings.Contains(rr.Body.String(), "Alex") {
		t.Fatalf("player confirmation status=%d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentMinecraftReset(); err != nil || current != nil {
		t.Fatalf("reset operation created without player confirmation: %#v err=%v", current, err)
	}
}

func TestAdminMinecraftResetApplyRejectsStalePlan(t *testing.T) {
	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(validMinecraftResetPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	stale := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	body, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: stale})
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "\"code\":\"stale_plan\"") {
		t.Fatalf("stale reset status=%d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentMinecraftReset(); err != nil || current != nil {
		t.Fatalf("reset operation created from stale plan: %#v err=%v", current, err)
	}
}

func TestAdminMinecraftResetApplySameFingerprintReconnectsWithoutPlanning(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactMinecraftResetFingerprint(t)
	operation, _, err := store.beginMinecraftReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	called := false
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		called = true
		return nil, errors.New("must not run")
	}

	body, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: fingerprint})
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusOK ||
		!strings.Contains(rr.Body.String(), operation.OperationID) ||
		!strings.Contains(rr.Body.String(), "\"created\":false") {
		t.Fatalf("reconnect status=%d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("reset planner ran while reconnecting to current operation")
	}
}

func TestExecuteMinecraftResetCompletesPersistentOperation(t *testing.T) {
	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "plan" {
			return []byte(validMinecraftResetPlanHelper), nil
		}
		if len(args) == 2 && args[0] == "apply" && args[1] == "--confirm-players" {
			return []byte(`{"ok":true,"schema_version":"v1","mode":"minecraft","data_path":"/var/lib/justvoxel/minecraft","data_scope":"internal","data_action":"delete","backup_path":"/var/lib/justvoxel/backups","backup_action":"preserve","authentication_action":"preserve","webui_users_action":"preserve","message":"Minecraft reset completed."}`), nil
		}
		t.Fatalf("unexpected helper args: %#v", args)
		return nil, nil
	}

	oldDiscovery := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = oldDiscovery }()
	runAdminDiscoveryHelper = func(_ context.Context, kind string) ([]byte, error) {
		if kind != "configuration" {
			t.Fatalf("unexpected discovery kind %q", kind)
		}
		return []byte(`{"configured":false}`), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactMinecraftResetFingerprint(t)
	operation, _, err := store.beginMinecraftReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := executeMinecraftReset(context.Background(), s, operation.OperationID, fingerprint, session{}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != operationSucceeded || finished.Stage != "complete" || finished.FinishedAt == "" {
		t.Fatalf("reset operation did not complete: %#v", finished)
	}
	if current, err := store.currentMinecraftReset(); err != nil || current != nil {
		t.Fatalf("reset remained current after success: %#v err=%v", current, err)
	}
}
