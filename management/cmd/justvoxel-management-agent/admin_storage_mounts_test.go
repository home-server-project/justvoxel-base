package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminStorageMountsRequireAdministrator(t *testing.T) {
	old := runAdminStorageMountsHelper
	defer func() { runAdminStorageMountsHelper = old }()
	called := false
	runAdminStorageMountsHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(`{"ok":true,"warnings":[],"proposed":{"device":"/dev/vdb1"},"applied":false}`), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminStorageMountPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-mounts/plan", `{"operation":"persist","device":"/dev/vdb1","mount_point":"/var/mnt/vdb1"}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s permanent mount status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("permanent mount helper ran for non-Administrator")
	}
}

func TestAdminStorageMountStatusUsesSelectedDevice(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageMountsHelper
	defer func() { runAdminStorageMountsHelper = old }()

	runAdminStorageMountsHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "status" {
			t.Fatalf("action = %q, want status", action)
		}
		if string(request) != `{"device":"/dev/vdb1"}` {
			t.Fatalf("unexpected status request: %s", request)
		}
		return []byte(`{"ok":true,"warnings":[],"proposed":{"device":"/dev/vdb1","filesystem":"xfs","uuid":"uuid-1","mount_point":"/var/mnt/vdb1","current_mount_point":"/var/mnt/vdb1","persistence":"justvoxel","mounted":true},"applied":false}`), nil
	}

	rr := httptest.NewRecorder()
	req := surfaceRequest(http.MethodGet, "/v1/admin/storage-mounts/status?device=%2Fdev%2Fvdb1", "")
	s.adminStorageMountStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status returned %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"/var/mnt/vdb1", "uuid-1", "justvoxel"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("status response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminStorageMountPlanReturnsReviewedIdentity(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageMountsHelper
	defer func() { runAdminStorageMountsHelper = old }()

	runAdminStorageMountsHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" {
			t.Fatalf("action = %q, want plan", action)
		}
		if !strings.Contains(string(request), `"mount_point":"/var/mnt/vdb1"`) {
			t.Fatalf("request missing mount point: %s", request)
		}
		return []byte(`{"ok":true,"warnings":[],"proposed":{"operation":"persist","device":"/dev/vdb1","filesystem":"ext4","uuid":"uuid-1","mount_point":"/var/mnt/vdb1","persistence":"none","fingerprint":"abc123","changed":true},"applied":false}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminStorageMountPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-mounts/plan", `{"operation":"persist","device":"/dev/vdb1","mount_point":"/var/mnt/vdb1"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan returned %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"uuid-1", "abc123", "/var/mnt/vdb1", "persist"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("plan response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminStorageMountApplyCarriesFingerprint(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageMountsHelper
	defer func() { runAdminStorageMountsHelper = old }()

	runAdminStorageMountsHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "apply" {
			t.Fatalf("action = %q, want apply", action)
		}
		if !strings.Contains(string(request), `"fingerprint":"abc123"`) {
			t.Fatalf("review fingerprint missing at apply: %s", request)
		}
		return []byte(`{"ok":true,"warnings":[],"proposed":{"operation":"persist","device":"/dev/vdb1","filesystem":"xfs","uuid":"uuid-1","mount_point":"/var/mnt/vdb1","current_mount_point":"/var/mnt/vdb1","persistence":"justvoxel","fingerprint":"abc123","mounted":true},"applied":true}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminStorageMountApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-mounts/apply", `{"operation":"persist","device":"/dev/vdb1","mount_point":"/var/mnt/vdb1","fingerprint":"abc123"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"applied":true`) {
		t.Fatalf("apply response did not report success: %s", rr.Body.String())
	}
}

func TestAdminStorageMountApplyReturnsBoundedStaleReview(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageMountsHelper
	defer func() { runAdminStorageMountsHelper = old }()

	runAdminStorageMountsHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "apply" {
			t.Fatalf("action = %q, want apply", action)
		}
		return []byte(`{"ok":false,"error":"The selected filesystem or permanent-mount settings changed after Review. Nothing was changed. Review the action again.","warnings":[],"applied":false}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminStorageMountApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-mounts/apply", `{"operation":"remove","device":"/dev/vdb1","mount_point":"/var/mnt/vdb1","fingerprint":"old"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("stale apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "changed after Review") {
		t.Fatalf("stale review failure missing: %s", rr.Body.String())
	}
}
