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

func TestAdminSystemUpdateCheckRunsAgainstRegistryWithStagedDeployment(t *testing.T) {
	oldStatus := runBootcStatus
	oldCheck := runBootcUpgradeCheck
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgradeCheck = oldCheck
	}()

	statusCalls := 0
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		statusCalls++
		return []byte(bootcStatusStagedFixture), nil
	}
	checkCalls := 0
	runBootcUpgradeCheck = func(_ context.Context) ([]byte, error) {
		checkCalls++
		return []byte("Total new layers: 4     Size: 120 MB\nRemoved layers:   2     Size: 80 MB\nAdded layers:     4     Size: 120 MB\n"), nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdateCheck(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/check", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("system update check = %d: %s", rr.Code, rr.Body.String())
	}
	if checkCalls != 1 {
		t.Fatalf("bootc upgrade --check calls = %d, want 1", checkCalls)
	}
	if statusCalls != 2 {
		t.Fatalf("bootc status calls = %d, want 2", statusCalls)
	}
	for _, want := range []string{`"checked":true`, `"check_state":"update_available"`, `"reboot_required":true`, "newer JustVoxel image is available than the currently staged update"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("system update check response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminSystemUpdateCheckRecognizesLatestImageAlreadyStaged(t *testing.T) {
	oldStatus := runBootcStatus
	oldCheck := runBootcUpgradeCheck
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgradeCheck = oldCheck
	}()
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		return []byte(bootcStatusStagedFixture), nil
	}
	runBootcUpgradeCheck = func(_ context.Context) ([]byte, error) {
		return []byte("Update already staged. To apply update run `bootc update --apply`"), nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdateCheck(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/check", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("system update check = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"check_state":"staged_current"`) {
		t.Fatalf("latest staged update was not recognized: %s", rr.Body.String())
	}
}

func TestAdminSystemUpdateCheckTreatsMatchingStagedDigestAsCurrent(t *testing.T) {
	oldStatus := runBootcStatus
	oldCheck := runBootcUpgradeCheck
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgradeCheck = oldCheck
	}()
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		return []byte(bootcStatusStagedFixture), nil
	}
	runBootcUpgradeCheck = func(_ context.Context) ([]byte, error) {
		return []byte("Update available for: ostree-image-signed:docker://ghcr.io/home-server-project/justvoxel-vm:testing\n  Version: 11\n  Digest: sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"), nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdateCheck(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/check", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("system update check = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"check_state":"staged_current"`) {
		t.Fatalf("matching staged digest should be current: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "newer JustVoxel image is available than the currently staged update") {
		t.Fatalf("matching staged digest was misclassified as newer: %s", rr.Body.String())
	}
}

func TestParseBootcUpgradeCheckMatchesVersionlessOutput(t *testing.T) {
	output := "Update available for: docker://ghcr.io/home-server-project/justvoxel-base:testing\n" +
		"  Digest: sha256:125d8fd678c43eabb2ce398fd0df7d6cc365cc57513f9a7476c8b415822cb11c\n" +
		"Total new layers: 128   Size: 682.5 MB\n" +
		"Removed layers:   3     Size: 123.4 MB\n" +
		"Added layers:     3     Size: 123.4 MB\n"
	state, available := parseBootcUpgradeCheck(output)
	if state != "update_available" {
		t.Fatalf("state = %q, want update_available", state)
	}
	if available == nil || available.Digest != "sha256:125d8fd678c43eabb2ce398fd0df7d6cc365cc57513f9a7476c8b415822cb11c" {
		t.Fatalf("unexpected parsed deployment: %#v", available)
	}
}

func TestAdminSystemUpdateCheckReturnsBootcFailureDetail(t *testing.T) {
	oldStatus := runBootcStatus
	oldCheck := runBootcUpgradeCheck
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgradeCheck = oldCheck
	}()
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		return []byte(bootcStatusCurrentFixture), nil
	}
	runBootcUpgradeCheck = func(_ context.Context) ([]byte, error) {
		return []byte("error: registry access denied\ncaused by: example failure\n"), errors.New("exit status 1")
	}
	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdateCheck(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/check", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("system update check = %d, want 503: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "registry access denied") || !strings.Contains(rr.Body.String(), "example failure") {
		t.Fatalf("bootc failure detail was lost: %s", rr.Body.String())
	}
}

func TestOperatorCannotCheckSystemUpdate(t *testing.T) {
	oldStatus := runBootcStatus
	oldCheck := runBootcUpgradeCheck
	defer func() {
		runBootcStatus = oldStatus
		runBootcUpgradeCheck = oldCheck
	}()
	called := false
	runBootcStatus = func(_ context.Context) ([]byte, error) {
		called = true
		return []byte(bootcStatusCurrentFixture), nil
	}
	runBootcUpgradeCheck = func(_ context.Context) ([]byte, error) {
		called = true
		return nil, nil
	}

	s := roleServerForTest(roleOperator)
	rr := httptest.NewRecorder()
	s.adminSystemUpdateCheck(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/check", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator update check = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("bootc command ran for forbidden operator update check")
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
	if !strings.Contains(rr.Body.String(), `"reboot_required":true`) || !strings.Contains(rr.Body.String(), `"check_state":"staged_current"`) || !strings.Contains(rr.Body.String(), "downloaded and staged") {
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
	if !strings.Contains(rr.Body.String(), `"reboot_required":false`) || !strings.Contains(rr.Body.String(), `"check_state":"current"`) || !strings.Contains(rr.Body.String(), "up to date") {
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
