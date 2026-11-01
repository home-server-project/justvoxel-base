package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const activationOldBoot = "11111111-1111-4111-8111-111111111111"
const activationNewBoot = "22222222-2222-4222-8222-222222222222"

func activationFixture(t *testing.T) (*operationStore, adminSystemUpdateStatus, systemUpdateActivation) {
	t.Helper()
	store := openTestOperationStore(t)
	oldBoot := readSystemUpdateBootID
	oldOstree := runUpdateOstreeStatus
	t.Cleanup(func() { readSystemUpdateBootID = oldBoot; runUpdateOstreeStatus = oldOstree })
	readSystemUpdateBootID = func() (string, error) { return activationNewBoot, nil }
	runUpdateOstreeStatus = func(context.Context) ([]byte, error) {
		return []byte("  default " + strings.Repeat("c", 64) + ".0 (staged)\n* default " + strings.Repeat("d", 64) + ".0\n"), nil
	}
	before := adminSystemUpdateStatus{OK: true, Running: adminSystemUpdateDeployment{Digest: "sha256:" + strings.Repeat("a", 64)}, Staged: &adminSystemUpdateDeployment{Digest: "sha256:" + strings.Repeat("b", 64)}, RebootRequired: true, stagedOstree: &bootcOstreeIdentity{Stateroot: "default", Checksum: strings.Repeat("c", 64)}}
	value := systemUpdateActivation{Version: 1, Expected: before.Staged.Digest, Previous: before.Running.Digest, BootID: activationOldBoot, RequestedAt: time.Now().UTC(), FailureLog: "11111111-1111-4111-8111-111111111111", SuccessLog: "22222222-2222-4222-8222-222222222222"}
	return store, before, value
}
func TestUpdateActivationNormalStagedAndSameBootRemainPending(t *testing.T) {
	store, status, value := activationFixture(t)
	got, err := store.reconcileUpdateActivation(context.Background(), status)
	if err != nil || got.ActivationFailed || got.ActivationRecoverable || got.ActivationPending {
		t.Fatal(got, err)
	}
	value.BootID = activationNewBoot
	if err := store.saveActivation(value); err != nil {
		t.Fatal(err)
	}
	got, err = store.reconcileUpdateActivation(context.Background(), status)
	if err != nil || !got.ActivationPending || got.ActivationFailed || got.ActivationRecoverable {
		t.Fatal(got, err)
	}
	logs, err := store.listDiagnosticLogs()
	if err != nil || len(logs) != 0 {
		t.Fatal(logs, err)
	}
}
func TestUpdateActivationSuccessClearsPendingAndWritesShortLog(t *testing.T) {
	store, status, value := activationFixture(t)
	if err := store.saveActivation(value); err != nil {
		t.Fatal(err)
	}
	status.Running.Digest = value.Expected
	status.Staged = nil
	for i := 0; i < 2; i++ {
		got, err := store.reconcileUpdateActivation(context.Background(), status)
		if err != nil || got.ActivationPending || got.ActivationFailed || got.ActivationRecoverable {
			t.Fatal(got, err)
		}
	}
	if _, err := os.Stat(store.activationPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	data, err := store.readDiagnosticLog("update", value.SuccessLog)
	if err != nil || len(data) > 256 || !strings.Contains(string(data), value.Expected) || strings.Contains(string(data), "ostree") {
		t.Fatal(string(data), err)
	}
	logs, err := store.listDiagnosticLogs()
	if err != nil || len(logs) != 1 {
		t.Fatal(logs, err)
	}
}
func TestUpdateActivationFailureIsAuthoritativeAndLoggedOnceAcrossRefresh(t *testing.T) {
	store, status, value := activationFixture(t)
	if err := store.saveActivation(value); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := store.reconcileUpdateActivation(context.Background(), status)
		if err != nil || !got.ActivationFailed || !got.ActivationRecoverable || got.ExpectedDigest != value.Expected {
			t.Fatal(got, err)
		}
	}
	logs, err := store.listDiagnosticLogs()
	if err != nil || len(logs) != 1 || logs[0].Category != "update" {
		t.Fatal(logs, err)
	}
	data, err := store.readDiagnosticLog("update", value.FailureLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"old_running_digest=" + value.Previous, "actual_booted_digest=" + value.Previous, "expected_digest=" + value.Expected, "recoverable=true", "bootc:", "ostree status:"} {
		if !strings.Contains(string(data), part) {
			t.Fatal(part, string(data))
		}
	}
	// A lost/replaced deployment cannot continue granting the fixed recovery action.
	status.Staged = nil
	got, err := store.reconcileUpdateActivation(context.Background(), status)
	if err != nil || !got.ActivationFailed || got.ActivationRecoverable || got.RecoveryStatus != "unavailable" {
		t.Fatal(got, err)
	}
	logs, err = store.listDiagnosticLogs()
	if err != nil || len(logs) != 1 {
		t.Fatal(logs, err)
	}
}
func TestUpdateActivationCorruptEvidenceFailsClosedAndIsPreserved(t *testing.T) {
	store, status, _ := activationFixture(t)
	for _, data := range []string{`{"version":2}`, `{"expected_digest":"; reboot"}`, strings.Repeat("x", 4097)} {
		if err := os.WriteFile(store.activationPath(), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := store.reconcileUpdateActivation(context.Background(), status)
		if err != nil || !got.ActivationFailed || got.ActivationRecoverable {
			t.Fatal(got, err)
		}
		retained, err := os.ReadFile(store.activationPath())
		if err != nil || string(retained) != data {
			t.Fatal("corrupt evidence discarded", err)
		}
	}
}
func TestUpdateOstreeTargetRequiresExactFirstDeployment(t *testing.T) {
	_, status, _ := activationFixture(t)
	identity := status.stagedOstree
	target := "  default " + identity.Checksum + ".0"
	for _, tc := range []struct {
		text              string
		staged, finalized bool
	}{
		{target + " (staged)\n", true, false}, {target + " (pending)\n", false, true}, {target + "\n", false, true},
		{target + " (finalization locked)\n", false, false}, {"* default " + identity.Checksum + ".0\n", false, false},
		{strings.Replace(target, ".0", ".1", 1) + " (staged)\n", false, false},
		{"* other " + strings.Repeat("d", 64) + ".0\n" + target + " (staged)\n", false, false},
		{target + " (staged)\n" + target + " (staged)\n", false, false},
		{"* unexpected deployment header\n" + target + " (staged)\n", false, false},
	} {
		staged, finalized := updateOstreeTarget([]byte(tc.text), identity)
		if staged != tc.staged || finalized != tc.finalized {
			t.Fatal(tc, staged, finalized)
		}
	}
}
func TestForceUpdateAuthorizationAndRejectsInput(t *testing.T) {
	oldFinalize := runSystemUpdateFinalize
	t.Cleanup(func() { runSystemUpdateFinalize = oldFinalize })
	calls := 0
	runSystemUpdateFinalize = func(context.Context) ([]byte, []byte, int, error) { calls++; return nil, nil, 0, nil }
	for _, tc := range []struct {
		role        principalRole
		body, query string
		want        int
	}{
		{roleViewer, "", "", 403}, {roleAdministrator, `{"command":"reboot","args":["now"]}`, "", 400}, {roleAdministrator, "", "?command=reboot", 400},
	} {
		s := roleServerForTest(tc.role)
		w := httptest.NewRecorder()
		s.adminSystemUpdateForce(w, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/force"+tc.query, tc.body))
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal("unauthorized command ran")
	}
}
func TestForceUpdateVerifiesDeploymentsAndRetainsRedactedStreams(t *testing.T) {
	for _, mode := range []string{"success", "success_queued", "command_failure", "unverified", "lost_target"} {
		t.Run(mode, func(t *testing.T) {
			isolateSystemUpdateLock(t)
			store, status, value := activationFixture(t)
			if err := store.saveActivation(value); err != nil {
				t.Fatal(err)
			}
			oldStatus, oldFinalize := runBootcStatus, runSystemUpdateFinalize
			t.Cleanup(func() { runBootcStatus = oldStatus; runSystemUpdateFinalize = oldFinalize })
			invoked := false
			statusAfter := 0
			runBootcStatus = func(context.Context) ([]byte, error) {
				if invoked {
					statusAfter++
				}
				fixture := strings.Replace(bootcStatusStagedFixture, `"downloadOnly":true`, `"downloadOnly":false,"ostree":{"stateroot":"default","checksum":"`+status.stagedOstree.Checksum+`","deploySerial":0}`, 1)
				if invoked && mode == "lost_target" {
					fixture = bootcStatusCurrentFixture
				}
				if invoked && mode == "success_queued" {
					fixture = queuedUpdateFixture(t, fixture)
				}
				return []byte(fixture), nil
			}
			runUpdateOstreeStatus = func(context.Context) ([]byte, error) {
				suffix := " (staged)"
				if invoked && strings.HasPrefix(mode, "success") {
					suffix = " (pending)"
				}
				if invoked && mode == "lost_target" {
					return []byte("* default " + strings.Repeat("d", 64) + ".0\n"), nil
				}
				return []byte("  default " + status.stagedOstree.Checksum + ".0" + suffix + "\n* default " + strings.Repeat("d", 64) + ".0\n"), nil
			}
			runSystemUpdateFinalize = func(context.Context) ([]byte, []byte, int, error) {
				invoked = true
				if mode == "command_failure" {
					return []byte("partial output"), []byte("finalization error\nAuthorization: Bearer registry-secret"), 7, errors.New("exit 7")
				}
				return []byte("finalization stdout"), []byte("finalization stderr"), 0, nil
			}
			s := adminServerForTest()
			s.operations = store
			w := httptest.NewRecorder()
			s.adminSystemUpdateForce(w, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/force", ""))
			if !invoked || statusAfter == 0 {
				t.Fatal("command or authoritative recheck missing")
			}
			if strings.HasPrefix(mode, "success") {
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var got adminSystemUpdateStatus
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.RecoveryStatus != "finalized" || got.ActivationRecoverable {
					t.Fatal(got)
				}
				saved, err := store.readActivation()
				if err != nil || saved.FinalizedBootID != activationNewBoot {
					t.Fatal(saved, err)
				}
				// A later reboot that still boots old must allow recovery again.
				readSystemUpdateBootID = func() (string, error) { return "33333333-3333-4333-8333-333333333333", nil }
				invoked = false
				got, err = store.reconcileUpdateActivation(context.Background(), status)
				if err != nil || !got.ActivationRecoverable {
					t.Fatal(got, err)
				}
			} else if w.Code != 503 {
				t.Fatal(w.Code, w.Body.String())
			}
			logs, err := store.listDiagnosticLogs()
			if err != nil {
				t.Fatal(err)
			}
			var evidence string
			for _, entry := range logs {
				data, err := store.readDiagnosticLog("update", entry.LogID)
				if err != nil {
					t.Fatal(err)
				}
				evidence += string(data)
			}
			for _, part := range []string{"exit_code=", "stdout:", "stderr:", "before bootc:", "after bootc:", "finalization_verified=", "no reboot requested"} {
				if !strings.Contains(evidence, part) {
					t.Fatal(part, evidence)
				}
			}
			if strings.Contains(evidence, "registry-secret") {
				t.Fatal("credential leaked")
			}
			got, err := s.collectSystemUpdateStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (mode == "command_failure" || mode == "unverified") && !got.ActivationRecoverable {
				t.Fatal(got)
			}
			if mode == "lost_target" && got.ActivationRecoverable {
				t.Fatal(got)
			}
		})
	}
}
func TestForceUpdateFixedCommandAndProtectedRebootSafety(t *testing.T) {
	data, err := os.ReadFile("system_update_activation.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `exec.CommandContext(ctx, "ostree", "admin", "finalize-staged", "--verbose")`) {
		t.Fatal("fixed argv missing")
	}
	if strings.Contains(string(data), `"sh",`) || strings.Contains(string(data), `"systemctl", "reboot"`) {
		t.Fatal("shell or automatic reboot added")
	}
	worker, err := os.ReadFile("../../../runtime/system-update-reboot-worker")
	if err != nil {
		t.Fatal(err)
	}
	text := string(worker)
	if strings.Index(text, "system-update-expect") < strings.Index(text, `"${shutdown_helper}" stop`) || strings.Index(text, "system-update-expect") > strings.Index(text, "if ! systemctl reboot") {
		t.Fatal("expectation not immediately before requested reboot")
	}
	for _, part := range []string{`warning_seconds="${JV_UPDATE_REBOOT_WARNING_SECONDS:-60}"`, `"${shutdown_helper}" stop "${instances}" confirmed "${warning_seconds}"`, `system-update-activate`} {
		if !strings.Contains(text, part) {
			t.Fatal(part)
		}
	}
	parser, err := os.ReadFile("../../../mjust/libexec/instance-host-shutdown-prepare")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(parser), `raw = re.sub(r'\x1b\[[0-9;]*m', '', raw)`) || !strings.Contains(string(parser), `if not match or '\x1b' in raw:`) {
		t.Fatal("Forge ANSI guard changed")
	}
	if _, err := os.Stat(filepath.Join("../../../mjust/libexec", "admin-system-update-reboot-json")); err != nil {
		t.Fatal(err)
	}
}

func TestNormalStagingWritesOnlyShortUpdateEvidence(t *testing.T) {
	isolateSystemUpdateLock(t)
	store := openTestOperationStore(t)
	oldStatus, oldUpgrade := runBootcStatus, runBootcUpgrade
	t.Cleanup(func() { runBootcStatus = oldStatus; runBootcUpgrade = oldUpgrade })
	staged := false
	runBootcStatus = func(context.Context) ([]byte, error) {
		if staged {
			return []byte(bootcStatusStagedFixture), nil
		}
		return []byte(bootcStatusCurrentFixture), nil
	}
	runBootcUpgrade = func(context.Context) ([]byte, error) {
		staged = true
		return []byte("verbose output must not enter successful log"), nil
	}
	s := adminServerForTest()
	s.operations = store
	w := httptest.NewRecorder()
	s.adminSystemUpdate(w, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates", ""))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	entries, err := store.listDiagnosticLogs()
	if err != nil || len(entries) != 1 || entries[0].Category != "update" {
		t.Fatal(entries, err)
	}
	data, err := store.readDiagnosticLog("update", entries[0].LogID)
	if err != nil || len(data) > 256 || !strings.Contains(string(data), "Update staged successfully") || strings.Contains(string(data), "verbose") {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(store.activationPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging alone created reboot expectation", err)
	}
}

func TestForceUpdateRefusesNormalStageAndUnavailableAuthority(t *testing.T) {
	for _, mode := range []string{"normal", "missing_expected", "ostree_unavailable", "readonly", "different_serial"} {
		t.Run(mode, func(t *testing.T) {
			isolateSystemUpdateLock(t)
			store, status, value := activationFixture(t)
			oldStatus, oldFinalize := runBootcStatus, runSystemUpdateFinalize
			t.Cleanup(func() { runBootcStatus = oldStatus; runSystemUpdateFinalize = oldFinalize })
			if mode != "normal" {
				if err := store.saveActivation(value); err != nil {
					t.Fatal(err)
				}
			}
			runBootcStatus = func(context.Context) ([]byte, error) {
				fixture := strings.Replace(bootcStatusStagedFixture, `"downloadOnly":true`, `"downloadOnly":false,"ostree":{"stateroot":"default","checksum":"`+status.stagedOstree.Checksum+`","deploySerial":0}`, 1)
				if mode == "missing_expected" {
					fixture = bootcStatusCurrentFixture
				}
				if mode == "readonly" {
					fixture = strings.Replace(fixture, `"readOnly":false`, `"readOnly":true`, 1)
				}
				if mode == "different_serial" {
					fixture = strings.Replace(fixture, `"deploySerial":0`, `"deploySerial":1`, 1)
				}
				return []byte(fixture), nil
			}
			if mode == "ostree_unavailable" {
				runUpdateOstreeStatus = func(context.Context) ([]byte, error) { return nil, errors.New("unavailable") }
			}
			runSystemUpdateFinalize = func(context.Context) ([]byte, []byte, int, error) {
				t.Fatal("unproven recovery command ran")
				return nil, nil, 0, nil
			}
			s := adminServerForTest()
			s.operations = store
			w := httptest.NewRecorder()
			s.adminSystemUpdateForce(w, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/updates/force", ""))
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestUpdateRebootExpectationPinsOnlyVerifiedStageAtomically(t *testing.T) {
	store, status, _ := activationFixture(t)
	if err := store.expectUpdateActivation(status, status.Staged.Digest); err != nil {
		t.Fatal(err)
	}
	saved, err := store.readActivation()
	if err != nil || saved == nil || saved.Expected != status.Staged.Digest || saved.Previous != status.Running.Digest || saved.BootID != activationNewBoot || saved.RequestedAt.IsZero() {
		t.Fatal(saved, err)
	}
	before, err := os.ReadFile(store.activationPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"no_stage", "wrong_digest", "invalid_digest", "download_only", "readonly"} {
		candidate := status
		digest := status.Staged.Digest
		switch mode {
		case "no_stage":
			candidate.Staged = nil
			candidate.RebootRequired = false
		case "wrong_digest":
			digest = "sha256:" + strings.Repeat("f", 64)
		case "invalid_digest":
			digest = "sha256:bad; reboot"
		case "download_only":
			candidate.stagedDownloadOnly = true
		case "readonly":
			candidate.ReadOnly = true
		}
		if err := store.expectUpdateActivation(candidate, digest); err == nil {
			t.Fatal("accepted", mode)
		}
		after, err := os.ReadFile(store.activationPath())
		if err != nil || string(after) != string(before) {
			t.Fatal("failed expectation replaced existing evidence", mode, err)
		}
	}
	info, err := os.Stat(store.activationPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	files, err := os.ReadDir(store.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range files {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatal("temporary activation write leaked")
		}
	}
}

func TestFixedFinalizeRunnerCapturesSeparateStreamsAndExitStatus(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$#\" = 3 ] && [ \"$1\" = admin ] && [ \"$2\" = finalize-staged ] && [ \"$3\" = --verbose ] || exit 99\nprintf 'full stdout\\nsecond line\\n'\nprintf 'full stderr\\n' >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(dir, "ostree"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	stdout, stderr, code, err := runSystemUpdateFinalize(context.Background())
	if err == nil || code != 7 || string(stdout) != "full stdout\nsecond line\n" || string(stderr) != "full stderr\n" {
		t.Fatal(string(stdout), string(stderr), code, err)
	}
}

func queuedUpdateFixture(t *testing.T, fixture string) string {
	t.Helper()
	var doc bootcStatusDocument
	if err := json.Unmarshal([]byte(fixture), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Status.Rollback = doc.Status.Staged
	doc.Status.Staged = nil
	doc.Status.RollbackQueued = true
	doc.Status.Rollback.DownloadOnly = false
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func TestFinalizedExpectedQueuePreservesExplicitUpdateReboot(t *testing.T) {
	store, status, value := activationFixture(t)
	value.FinalizedBootID = activationNewBoot
	if err := store.saveActivation(value); err != nil {
		t.Fatal(err)
	}
	oldStatus, oldActivate := runBootcStatus, runBootcActivate
	t.Cleanup(func() { runBootcStatus = oldStatus; runBootcActivate = oldActivate })
	fixture := strings.Replace(bootcStatusStagedFixture, `"downloadOnly":true`, `"downloadOnly":false,"ostree":{"stateroot":"default","checksum":"`+status.stagedOstree.Checksum+`","deploySerial":0}`, 1)
	fixture = queuedUpdateFixture(t, fixture)
	runBootcStatus = func(context.Context) ([]byte, error) { return []byte(fixture), nil }
	runBootcActivate = func(context.Context) error {
		t.Fatal("already finalized queue must not be activated again")
		return nil
	}
	runUpdateOstreeStatus = func(context.Context) ([]byte, error) {
		return []byte("  default " + status.stagedOstree.Checksum + ".0 (pending)\n* default " + strings.Repeat("d", 64) + ".0\n"), nil
	}
	s := adminServerForTest()
	s.operations = store
	got, err := s.collectSystemUpdateStatus(context.Background())
	if err != nil || !got.RebootRequired || got.Staged == nil || got.Staged.Digest != value.Expected || got.ActivationRecoverable || got.RecoveryStatus != "finalized" {
		t.Fatal(got, err)
	}
	if err := activateDownloadedSystemUpdate(context.Background(), value.Expected); err != nil {
		t.Fatal("existing reboot worker rejected finalized target", err)
	}
	if err := store.expectUpdateActivation(got, value.Expected); err != nil {
		t.Fatal("new reboot expectation rejected finalized target", err)
	}
	// No expectation means no change to ordinary rollback presentation.
	if err := os.Remove(store.activationPath()); err != nil {
		t.Fatal(err)
	}
	got, err = s.collectSystemUpdateStatus(context.Background())
	if err != nil || got.Staged != nil || got.RebootRequired || got.ActivationFailed || got.ActivationRecoverable {
		t.Fatal(got, err)
	}
}
