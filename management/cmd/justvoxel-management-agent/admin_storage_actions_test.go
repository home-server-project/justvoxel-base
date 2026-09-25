package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminStorageActionsRequireAdministrator(t *testing.T) {
	old := runAdminStorageActionsHelper
	defer func() { runAdminStorageActionsHelper = old }()
	called := false
	runAdminStorageActionsHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(`{"ok":true,"warnings":[],"applied":false}`), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminStorageActionPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-actions/plan", `{"operation":"format","device":"/dev/vdb1"}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s storage action status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("storage action helper ran for non-Administrator")
	}
}

func TestAdminStorageActionPlanReturnsReviewEvidence(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageActionsHelper
	defer func() { runAdminStorageActionsHelper = old }()

	runAdminStorageActionsHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" {
			t.Fatalf("action = %q, want plan", action)
		}
		if !strings.Contains(string(request), `"device":"/dev/vdb1"`) {
			t.Fatalf("request missing selected partition: %s", request)
		}
		return []byte(`{"ok":true,"warnings":["destructive"],"proposed":{"operation":"format","device":"/dev/vdb1","filesystem":"ext4","target_filesystem":"xfs","role":"Minecraft","confirmation":"FORMAT /dev/vdb1","fingerprint":"abc123","destructive":true},"applied":false}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminStorageActionPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-actions/plan", `{"operation":"format","device":"/dev/vdb1"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"FORMAT /dev/vdb1", "abc123", "Minecraft", "destructive"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("plan response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminStorageActionApplyReturnsBoundedFailure(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageActionsHelper
	defer func() { runAdminStorageActionsHelper = old }()

	runAdminStorageActionsHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "apply" {
			t.Fatalf("action = %q, want apply", action)
		}
		return []byte(`{"ok":false,"error":"The selected partition changed after Review. Nothing was changed. Review the action again.","warnings":[],"applied":false}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminStorageActionApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-actions/apply", `{"operation":"format","device":"/dev/vdb1","confirmation":"FORMAT /dev/vdb1","fingerprint":"old"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("apply status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "changed after Review") {
		t.Fatalf("bounded safety failure missing: %s", rr.Body.String())
	}
}

func TestAdminStorageActionPlanCarriesCreatePartitionGeometry(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageActionsHelper
	defer func() { runAdminStorageActionsHelper = old }()

	runAdminStorageActionsHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" {
			t.Fatalf("action = %q, want plan", action)
		}
		body := string(request)
		for _, want := range []string{`"operation":"create_partition"`, `"device":"/dev/vda"`, `"size_gib":"55"`, `"free_start":"102401MiB"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("create partition request missing %q: %s", want, body)
			}
		}
		return []byte(`{"ok":true,"warnings":[],"proposed":{"operation":"create_partition","device":"/dev/vda","target_filesystem":"xfs","size_gib":"55","free_start":"102401MiB","free_end":"204800MiB","planned_end":"158721.00MiB","confirmation":"CREATE PARTITION /dev/vda","fingerprint":"layout123","destructive":true},"applied":false}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminStorageActionPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-actions/plan", `{"operation":"create_partition","device":"/dev/vda","size_gib":"55","free_start":"102401MiB"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"CREATE PARTITION /dev/vda", "layout123", "102401MiB"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("plan response missing %q: %s", want, rr.Body.String())
		}
	}
}
