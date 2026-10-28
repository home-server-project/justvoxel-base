package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

const bootcStatusCurrentFixture = `{"apiVersion":"org.containers.bootc/v1","status":{"booted":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"10","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"staged":null,"rollback":null,"readOnly":false}}`

const bootcStatusStagedFixture = `{"apiVersion":"org.containers.bootc/v1","status":{"booted":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"10","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"staged":{"downloadOnly":true,"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"11","imageDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},"rollback":null,"readOnly":false}}`

const bootcStatusReadOnlyFixture = `{"apiVersion":"org.containers.bootc/v1","status":{"booted":{"image":{"image":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing"},"version":"10","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"staged":null,"rollback":null,"readOnly":true}}`

func TestAdminSystemUpdateStatusRequiresAdministrator(t *testing.T) {
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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
	isolateSystemUpdateLock(t)
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

func isolateSystemUpdateLock(t *testing.T) {
	t.Helper()
	old := acquireSystemUpdateLock
	t.Cleanup(func() { acquireSystemUpdateLock = old })
	dir := t.TempDir()
	acquireSystemUpdateLock = func() (*os.File, error) { return os.CreateTemp(dir, "system-update-lock-") }
}
func TestDownloadOnlyStageAndAutomaticSchedulingContract(t *testing.T) {
	source, err := os.ReadFile("admin_system_updates.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{`exec.CommandContext(ctx, "bootc", "upgrade", "--check")`, `exec.CommandContext(ctx, "bootc", "upgrade", "--download-only")`, `exec.CommandContext(ctx, "bootc", "upgrade", "--from-downloaded")`} {
		if !strings.Contains(string(source), command) {
			t.Fatal(command)
		}
	}
	if strings.Contains(string(source), `"--apply"`) {
		t.Fatal("bootc owns reboot")
	}
	service, err := os.ReadFile("../../../system_files/usr/lib/systemd/system/justvoxel-system-update-stage.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), "ExecStart=/usr/libexec/justvoxel/mjust/os-update") || strings.Contains(string(service), "bootc ") || strings.Contains(string(service), "reboot") {
		t.Fatal("automatic stage bypasses API or reboots")
	}
	timer, err := os.ReadFile("../../../system_files/usr/lib/systemd/system/justvoxel-system-update-stage.timer")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OnBootSec=20min", "OnUnitInactiveSec=5h", "RandomizedDelaySec=10min"} {
		if !strings.Contains(string(timer), want) {
			t.Fatal(want)
		}
	}
	build, err := os.ReadFile("../../../build_files/build-common.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"systemctl enable justvoxel-system-update-stage.timer", "systemctl --root=/ disable bootc-fetch-apply-updates.timer", "systemctl --root=/ mask bootc-fetch-apply-updates.timer"} {
		if !strings.Contains(string(build), want) {
			t.Fatal(want)
		}
	}
	client, err := os.ReadFile("../../../mjust/libexec/system-update-api.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), `"${JV_SYSTEM_UPDATE_API_CLIENT}" POST /v1/admin/system/updates`) {
		t.Fatal("stage does not use Management API")
	}
	check := regexp.MustCompile(`(?s)var runBootcUpgradeCheck =.*?\n}`).FindString(string(source))
	if strings.Contains(check, "--download-only") || strings.Contains(check, "--from-downloaded") {
		t.Fatal("metadata check mutates deployments")
	}
}
func TestDownloadedActivationVerifiesExactStageAndSupportsQueuedDeployment(t *testing.T) {
	oldStatus, oldActivate := runBootcStatus, runBootcActivate
	t.Cleanup(func() { runBootcStatus = oldStatus; runBootcActivate = oldActivate })
	digest := "sha256:" + strings.Repeat("b", 64)
	for _, downloadOnly := range []bool{true, false} {
		activated := false
		calls := 0
		runBootcStatus = func(context.Context) ([]byte, error) {
			fixture := bootcStatusStagedFixture
			if !downloadOnly || activated {
				fixture = strings.Replace(fixture, `"downloadOnly":true`, `"downloadOnly":false`, 1)
			}
			return []byte(fixture), nil
		}
		runBootcActivate = func(context.Context) error { calls++; activated = true; return nil }
		if err := activateDownloadedSystemUpdate(context.Background(), digest); err != nil {
			t.Fatal(err)
		}
		if (calls == 1) != downloadOnly {
			t.Fatal("normal queued stage compatibility lost")
		}
	}
	for _, reason := range []string{"activation failed", "stage replaced", "activation not verified"} {
		calls := 0
		runBootcStatus = func(context.Context) ([]byte, error) {
			fixture := bootcStatusStagedFixture
			if reason == "stage replaced" {
				fixture = strings.ReplaceAll(fixture, digest, "sha256:"+strings.Repeat("c", 64))
			}
			return []byte(fixture), nil
		}
		runBootcActivate = func(context.Context) error {
			calls++
			if reason == "activation failed" {
				return errors.New("activation failed")
			}
			return nil
		}
		if err := activateDownloadedSystemUpdate(context.Background(), digest); err == nil {
			t.Fatal(reason)
		}
		if reason == "stage replaced" && calls != 0 {
			t.Fatal("activated a different image")
		}
	}
}
func TestAutomaticStageFailureDoesNotChangeRunningDeployment(t *testing.T) {
	isolateSystemUpdateLock(t)
	oldStatus, oldUpgrade := runBootcStatus, runBootcUpgrade
	t.Cleanup(func() { runBootcStatus = oldStatus; runBootcUpgrade = oldUpgrade })
	runBootcStatus = func(context.Context) ([]byte, error) { return []byte(bootcStatusCurrentFixture), nil }
	runBootcUpgrade = func(context.Context) ([]byte, error) { return nil, errors.New("download failed") }
	s := &server{sessions: make(map[string]session)}
	w := httptest.NewRecorder()
	s.adminSystemUpdate(w, requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/system/updates", 0))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	after, err := collectAdminSystemUpdateStatus(context.Background())
	if err != nil || after.Staged != nil || after.RebootRequired || after.Running.Digest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("running deployment changed: %+v %v", after, err)
	}
}

func TestAutomaticStageReturnsAuthoritativeStagedDeployment(t *testing.T) {
	isolateSystemUpdateLock(t)
	oldStatus, oldUpgrade := runBootcStatus, runBootcUpgrade
	t.Cleanup(func() { runBootcStatus = oldStatus; runBootcUpgrade = oldUpgrade })
	downloaded := false
	runBootcStatus = func(context.Context) ([]byte, error) {
		if downloaded {
			return []byte(bootcStatusStagedFixture), nil
		}
		return []byte(bootcStatusCurrentFixture), nil
	}
	runBootcUpgrade = func(context.Context) ([]byte, error) { downloaded = true; return []byte("downloaded"), nil }
	s := &server{sessions: make(map[string]session)}
	w := httptest.NewRecorder()
	s.adminSystemUpdate(w, requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/system/updates", 0))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reboot_required":true`) || !strings.Contains(w.Body.String(), `"check_state":"staged_current"`) || !strings.Contains(w.Body.String(), "sha256:"+strings.Repeat("b", 64)) {
		t.Fatalf("automatic stage: %d %s", w.Code, w.Body.String())
	}
}
func TestStageDoesNotClaimSuccessWithoutVerifiedDeployment(t *testing.T) {
	isolateSystemUpdateLock(t)
	oldStatus, oldUpgrade := runBootcStatus, runBootcUpgrade
	t.Cleanup(func() { runBootcStatus = oldStatus; runBootcUpgrade = oldUpgrade })
	// A cached update is available, but a zero-exit upgrade did not stage it.
	available := strings.Replace(bootcStatusCurrentFixture, `"booted":{"image":`, `"booted":{"cachedUpdate":{"image":{"image":"ghcr.io/home-server-project/justvoxel-base:testing"},"imageDigest":"sha256:`+strings.Repeat("b", 64)+`"},"image":`, 1)
	runBootcStatus = func(context.Context) ([]byte, error) { return []byte(available), nil }
	runBootcUpgrade = func(context.Context) ([]byte, error) { return []byte("done"), nil }
	s := &server{sessions: make(map[string]session)}
	w := httptest.NewRecorder()
	s.adminSystemUpdate(w, requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/system/updates", 0))
	if w.Code != 503 {
		t.Fatalf("unverified staging accepted: %d %s", w.Code, w.Body.String())
	}
}
