package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupReviewedFriendlyNameIsCanonicalAndAuthoritative(t *testing.T) {
	oldHelper, oldRuntime := runAdminSetupPlanHelper, instanceRuntimes
	f := newInstanceActivationFixture(t)
	instanceRuntimes = f.backend
	t.Cleanup(func() { runAdminSetupPlanHelper = oldHelper; instanceRuntimes = oldRuntime })
	runAdminSetupPlanHelper = func(_ context.Context, payload []byte) ([]byte, error) {
		if strings.Contains(string(payload), "display_name") {
			t.Fatal("name leaked into strict shell contract")
		}
		return []byte(validAdminSetupPlanResponse), nil
	}
	var request adminSetupPlanRequest
	if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
		t.Fatal(err)
	}
	request.Server.DisplayName = "  Family Survival  "
	plan, err := authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil || !plan.OK || plan.Normalized.Server.DisplayName != "Family Survival" || plan.Normalized.Server.MOTD != "Family server" {
		t.Fatal(plan, err)
	}
	// Apply uses this same authoritative revalidation of the reviewed request.
	repeated, err := authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil || repeated.PlanFingerprint != plan.PlanFingerprint {
		t.Fatal("Apply revalidation changed identity", err)
	}
	runtime, runtimeErr := setupRuntimeRequestForOperation(operationJournal{OperationType: operationTypeSetup, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: plan.PlanFingerprint}, plan.Normalized)
	if runtimeErr != nil || runtime.Server.DisplayName != "Family Survival" {
		t.Fatal(runtime, runtimeErr)
	}
	request.Server.DisplayName = "Creative"
	changed, err := authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil || changed.PlanFingerprint == plan.PlanFingerprint {
		t.Fatal("name not fingerprinted", err)
	}
	request.Server.DisplayName = ""
	unnamed, err := authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil || unnamed.Normalized.Server.DisplayName != "" {
		t.Fatal("Recommended acquired an explicit name", err)
	}
	for _, name := range []string{".", "..", " ", "Bad\nName", "Bad\u200bName", strings.Repeat("x", 65)} {
		request.Server.DisplayName = name
		if _, err := authoritativeAdminSetupPlan(context.Background(), request); err == nil || err.status != http.StatusBadRequest {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
}

func TestSetupApplyRevalidatesReviewedNameBeforeStartingWorker(t *testing.T) {
	f := newInstanceActivationFixture(t)
	oldRuntime, oldHelper, oldDiscovery, oldWorker := instanceRuntimes, runAdminSetupPlanHelper, runAdminDiscoveryHelper, startSetupWorker
	instanceRuntimes = f.backend
	t.Cleanup(func() {
		instanceRuntimes = oldRuntime
		runAdminSetupPlanHelper = oldHelper
		runAdminDiscoveryHelper = oldDiscovery
		startSetupWorker = oldWorker
	})
	runAdminSetupPlanHelper = func(context.Context, []byte) ([]byte, error) { return []byte(validAdminSetupPlanResponse), nil }
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return []byte(`{"configured":false,"minecraft":{},"backup":{}}`), nil
	}
	var request adminSetupPlanRequest
	if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
		t.Fatal(err)
	}
	request.Server.DisplayName = "Family Survival"
	plan, err := authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	startSetupWorker = func(_ *server, _ string, normalized adminSetupNormalizedPlan, secret []byte) {
		zeroBytes(secret)
		called = true
		if normalized.Server.DisplayName != "Family Survival" {
			t.Fatal("worker lost reviewed name")
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	attachTestOperationStore(t, s, openTestOperationStore(t))
	// Changing only the name after Review must fail before any worker starts.
	request.Server.DisplayName = "Changed"
	body, marshalErr := json.Marshal(adminSetupApplyRequest{PlanFingerprint: plan.PlanFingerprint, Request: request, EULAAccepted: true})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	response := httptest.NewRecorder()
	s.adminSetupApply(response, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", string(body)))
	if response.Code != http.StatusConflict || called {
		t.Fatal("changed name accepted", response.Code, response.Body.String())
	}
	request.Server.DisplayName = "Family Survival"
	body, marshalErr = json.Marshal(adminSetupApplyRequest{PlanFingerprint: plan.PlanFingerprint, Request: request, EULAAccepted: true})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	response = httptest.NewRecorder()
	s.adminSetupApply(response, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", string(body)))
	if response.Code != http.StatusAccepted || !called {
		t.Fatal(response.Code, response.Body.String())
	}
	request.Server.DisplayName = "JV Paper"
	if _, err := authoritativeAdminSetupPlan(context.Background(), request); err == nil || err.status != http.StatusBadRequest {
		t.Fatal("duplicate registered name accepted")
	}
}
