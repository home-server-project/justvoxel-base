package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const bootcStatusCurrentFixture = `{"apiVersion":"org.containers.bootc/v1","status":{"booted":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"10","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"staged":null,"rollback":null,"readOnly":false}}`

const bootcStatusStagedFixture = `{"apiVersion":"org.containers.bootc/v1","status":{"booted":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"10","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"staged":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"11","imageDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},"rollback":null,"readOnly":false}}`

const bootcStatusReadOnlyFixture = `{"apiVersion":"org.containers.bootc/v1","status":{"booted":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"10","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"staged":null,"rollback":null,"readOnly":true}}`

func TestAdminSystemUpdateStatusRequiresAdministrator(t *testing.T) {
	oldStatus := runBootcStatus
	defer func() { runBootcStatus = oldStatus }()
	called := false
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		called = true
		return []byte(bootcStatusCurrentFixture), nil
	}

	s := roleServerForTest(roleViewer)
	rr := httptest.NewRecorder()
	s.adminSystemUpdateStatus(rr, authorizedRequest(http.MethodGet, "http://unix/v1/admin/system/updates", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("bootc status ran for forbidden viewer request")
	}
}

func TestAdminSystemUpdateStatusAllowsLocalRoot(t *testing.T) {
	oldStatus := runBootcStatus
	defer func() { runBootcStatus = oldStatus }()
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		return []byte(bootcStatusStagedFixture), nil
	}

	s := &server{sessions: make(map[string]session)}
	rr := httptest.NewRecorder()
	s.adminSystemUpdateStatus(rr, requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/system/updates", 0))
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"reboot_required":true`) || !strings.Contains(rr.Body.String(), `"version":"11"`) {
		t.Fatalf("unexpected system update status: %s", rr.Body.String())
	}
}

func TestAdminSystemUpdateRunsUpgradeAndReturnsStagedStatus(t *testing.T) {
	oldStatus := runBootcStatus
	oldUpgrade := runBootcUpgrade
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgrade = oldUpgrade
	}()

	statusCalls := 0
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		statusCalls++
		if statusCalls == 1 {
			return []byte(bootcStatusCurrentFixture), nil
		}
		return []byte(bootcStatusStagedFixture), nil
	}
	upgradeCalls := 0
	runBootcUpgrade = func(_ context.Context) ([]byte, error) {
		upgradeCalls++
		return []byte("upgrade complete"), nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdate(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("system update = %d: %s", rr.Code, rr.Body.String())
	}
	if upgradeCalls != 1 {
		t.Fatalf("bootc upgrade calls = %d, want 1", upgradeCalls)
	}
	if !strings.Contains(rr.Body.String(), `"reboot_required":true`) || !strings.Contains(rr.Body.String(), "update is staged") {
		t.Fatalf("unexpected update response: %s", rr.Body.String())
	}
}

func TestAdminSystemUpdateCurrentImageReturnsCurrent(t *testing.T) {
	oldStatus := runBootcStatus
	oldUpgrade := runBootcUpgrade
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgrade = oldUpgrade
	}()
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		return []byte(bootcStatusCurrentFixture), nil
	}
	runBootcUpgrade = func(_ context.Context) ([]byte, error) {
		return []byte("no changes"), nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdate(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("current system update = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"reboot_required":false`) || !strings.Contains(rr.Body.String(), "OS is current") {
		t.Fatalf("unexpected current response: %s", rr.Body.String())
	}
}

func TestAdminSystemUpdateRejectsReadOnlyBootc(t *testing.T) {
	oldStatus := runBootcStatus
	oldUpgrade := runBootcUpgrade
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgrade = oldUpgrade
	}()
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		return []byte(bootcStatusReadOnlyFixture), nil
	}
	called := false
	runBootcUpgrade = func(_ context.Context) ([]byte, error) {
		called = true
		return nil, nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdate(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates", ""))
	if rr.Code != http.StatusConflict {
		t.Fatalf("read-only update = %d, want 409: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("bootc upgrade ran on read-only bootc status")
	}
}

func TestAdminSystemUpdateFailureIsServiceUnavailable(t *testing.T) {
	oldStatus := runBootcStatus
	oldUpgrade := runBootcUpgrade
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgrade = oldUpgrade
	}()
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		return []byte(bootcStatusCurrentFixture), nil
	}
	runBootcUpgrade = func(_ context.Context) ([]byte, error) {
		return nil, errors.New("registry unavailable")
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdate(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed update = %d, want 503: %s", rr.Code, rr.Body.String())
	}
}

func TestOperatorCannotRequestSystemUpdate(t *testing.T) {
	oldStatus := runBootcStatus
	oldUpgrade := runBootcUpgrade
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgrade = oldUpgrade
	}()
	called := false
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		called = true
		return []byte(bootcStatusCurrentFixture), nil
	}
	runBootcUpgrade = func(_ context.Context) ([]byte, error) {
		called = true
		return nil, nil
	}

	s := roleServerForTest(roleOperator)
	rr := httptest.NewRecorder()
	s.adminSystemUpdate(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator update = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("bootc command ran for forbidden operator request")
	}
}
