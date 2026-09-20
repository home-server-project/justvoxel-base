package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalRootCanProvisionBackupStorageWithoutBearerSession(t *testing.T) {
	s := surfaceTestServer(t, roleViewer)
	old := runAdminStorageProvisionHelper
	defer func() { runAdminStorageProvisionHelper = old }()

	calls := make([]string, 0, 3)
	runAdminStorageProvisionHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		calls = append(calls, action)
		switch action {
		case "discover":
			return []byte(`{"ok":true,"warnings":[],"whole_disks":[{"path":"/dev/vdb","size_bytes":10737418240,"system_disk":false}],"blank_partitions":[],"free_space_disks":[]}`), nil
		case "plan":
			if !strings.Contains(string(request), `"device":"/dev/vdb"`) {
				t.Fatalf("unexpected local-root storage plan request: %s", request)
			}
			return []byte(`{"ok":true,"warnings":["destructive"],"proposed":{"operation":"erase_disk","device":"/dev/vdb","mount_point":"/var/mnt/justvoxel-backup","path":"/var/mnt/justvoxel-backup/backups","confirmation":"ERASE /dev/vdb","fingerprint":"abc123"},"applied":false}`), nil
		case "apply":
			return []byte(`{"ok":true,"warnings":["destructive"],"proposed":{"operation":"erase_disk","device":"/dev/vdb","mount_point":"/var/mnt/justvoxel-backup","path":"/var/mnt/justvoxel-backup/backups","confirmation":"ERASE /dev/vdb","fingerprint":"abc123","partition":"/dev/vdb1","filesystem":"xfs"},"applied":true}`), nil
		default:
			t.Fatalf("unexpected local-root storage provision action: %s", action)
			return nil, nil
		}
	}

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/storage-provision", 0)
	rr := httptest.NewRecorder()
	s.adminStorageProvisionDiscover(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root storage discover returned %d: %s", rr.Code, rr.Body.String())
	}

	body := `{"operation":"erase_disk","device":"/dev/vdb","mount_point":"/var/mnt/justvoxel-backup","path":"/var/mnt/justvoxel-backup/backups"}`
	req = requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/storage-provision/plan", 0)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	s.adminStorageProvisionPlan(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root storage plan returned %d: %s", rr.Code, rr.Body.String())
	}

	body = `{"operation":"erase_disk","device":"/dev/vdb","mount_point":"/var/mnt/justvoxel-backup","path":"/var/mnt/justvoxel-backup/backups","confirmation":"ERASE /dev/vdb","fingerprint":"abc123"}`
	req = requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/storage-provision/apply", 0)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	s.adminStorageProvisionApply(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root storage apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 3 || calls[0] != "discover" || calls[1] != "plan" || calls[2] != "apply" {
		t.Fatalf("unexpected local-root storage helper calls: %#v", calls)
	}
}

func TestAdminStorageProvisionRequiresAdministrator(t *testing.T) {
	old := runAdminStorageProvisionHelper
	defer func() { runAdminStorageProvisionHelper = old }()
	called := false
	runAdminStorageProvisionHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(`{"ok":true,"warnings":[],"whole_disks":[],"blank_partitions":[],"free_space_disks":[]}`), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminStorageProvisionDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/storage-provision", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s discover status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("advanced storage helper ran for non-Administrator")
	}
}

func TestAdminStorageProvisionPlanReturnsExactConfirmation(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageProvisionHelper
	defer func() { runAdminStorageProvisionHelper = old }()

	runAdminStorageProvisionHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" {
			t.Fatalf("action = %q, want plan", action)
		}
		if !strings.Contains(string(request), `"device":"/dev/vdb"`) {
			t.Fatalf("request missing selected device: %s", request)
		}
		return []byte(`{"ok":true,"warnings":["destructive"],"proposed":{"operation":"erase_disk","device":"/dev/vdb","model":"Virtual Disk","size_bytes":10737418240,"mount_point":"/var/mnt/justvoxel-backup","path":"/var/mnt/justvoxel-backup/backups","confirmation":"ERASE /dev/vdb","fingerprint":"abc123"},"applied":false}`), nil
	}

	body := `{"operation":"erase_disk","device":"/dev/vdb","mount_point":"/var/mnt/justvoxel-backup","path":"/var/mnt/justvoxel-backup/backups"}`
	rr := httptest.NewRecorder()
	s.adminStorageProvisionPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-provision/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"ERASE /dev/vdb", "abc123", "destructive"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("plan response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminStorageProvisionApplyReturnsBoundedFailure(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminStorageProvisionHelper
	defer func() { runAdminStorageProvisionHelper = old }()

	runAdminStorageProvisionHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "apply" {
			t.Fatalf("action = %q, want apply", action)
		}
		return []byte(`{"ok":false,"error":"The selected device or disk layout changed after Review. Nothing was changed. Review the storage operation again.","warnings":[],"applied":false}`), nil
	}

	body := `{"operation":"erase_disk","device":"/dev/vdb","mount_point":"/var/mnt/justvoxel-backup","path":"/var/mnt/justvoxel-backup/backups","confirmation":"ERASE /dev/vdb","fingerprint":"old"}`
	rr := httptest.NewRecorder()
	s.adminStorageProvisionApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/storage-provision/apply", body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("apply status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "changed after Review") {
		t.Fatalf("bounded safety failure missing: %s", rr.Body.String())
	}
}
