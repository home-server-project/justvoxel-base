package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContainerStrictInstanceContractAndStaleReview(t *testing.T) {
	whitelistSurfaceFixture(t)
	oldStatus, oldWorker := runMinecraftContainerStatus, startMinecraftContainerUpdateWorker
	defer func() { runMinecraftContainerStatus = oldStatus; startMinecraftContainerUpdateWorker = oldWorker }()
	reads, starts := 0, 0
	runMinecraftContainerStatus = func(_ context.Context, id string) (minecraftContainerStatus, error) {
		reads++
		if id != "jv-a1b2c3" {
			t.Fatal("wrong immutable instance")
		}
		return minecraftContainerStatus{InstanceID: id, Image: "docker.io/itzg/minecraft-server:stable", State: "update_available", Running: true, PlanFingerprint: testMinecraftResetFingerprint}, nil
	}
	startMinecraftContainerUpdateWorker = func(_ *server, _ string, r minecraftContainerUpdateRequest, _ session) {
		starts++
		if r.InstanceID != "jv-a1b2c3" {
			t.Fatal("wrong worker instance")
		}
	}
	body := `{"plan_fingerprint":"` + testMinecraftResetFingerprint + `","restart_now":false}`
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		w := httptest.NewRecorder()
		s.adminMinecraftContainerUpdate(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/update", body))
		if w.Code != 403 || reads != 0 {
			t.Fatal("authorization bypass")
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	for _, field := range []string{"image", "repository", "unit", "container", "path", "command", "image_tag", "policy", "server_type"} {
		w := httptest.NewRecorder()
		s.adminMinecraftContainerUpdate(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/update", strings.TrimSuffix(body, "}")+`,"`+field+`":"arbitrary"}`))
		if w.Code != 400 || reads != 0 {
			t.Fatalf("accepted %s", field)
		}
	}
	w := httptest.NewRecorder()
	s.adminMinecraftContainerUpdate(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/update", strings.Replace(body, testMinecraftResetFingerprint, "sha256:"+strings.Repeat("7", 64), 1)))
	if w.Code != 409 || starts != 0 {
		t.Fatal("stale review mutated")
	}
	w = httptest.NewRecorder()
	s.adminMinecraftContainerUpdate(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/update", body))
	if w.Code != http.StatusAccepted || starts != 1 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	for _, kind := range []string{operationTypeMinecraftUpdate, operationTypeMinecraftReset, operationTypePluginInstall} {
		if _, _, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, kind, "jv-a1b2c3"); err == nil {
			t.Fatalf("concurrent %s", kind)
		}
	}
	if !supportedOperationTrace(operationTypeMinecraftContainerUpdate) {
		t.Fatal("trace unsupported")
	}
}

func TestContainerStoppedAndRestartOnlyDecisions(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := runMinecraftContainerStatus
	defer func() { runMinecraftContainerStatus = old }()
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	for _, item := range []struct {
		state                    string
		running, restart, backup bool
	}{
		{"update_available", false, true, false}, {"restart_needed", true, false, false}, {"up_to_date", true, true, false}, {"update_available", true, false, true}, {"unavailable", true, true, false},
	} {
		runMinecraftContainerStatus = func(_ context.Context, id string) (minecraftContainerStatus, error) {
			return minecraftContainerStatus{InstanceID: id, State: item.state, Running: item.running, PlanFingerprint: testMinecraftResetFingerprint}, nil
		}
		body := `{"plan_fingerprint":"` + testMinecraftResetFingerprint + `","restart_now":` + boolText(item.restart) + `,"backup_before_restart":` + boolText(item.backup) + `}`
		w := httptest.NewRecorder()
		s.adminMinecraftContainerUpdate(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/update", body))
		if w.Code < 400 {
			t.Fatal("unsafe or impossible decisions accepted")
		}
	}
}
func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestContainerWorkerFailureRetainsMaintenance(t *testing.T) {
	for _, scenario := range []string{"safe", "recovered", "verification_failed", "recovery_failed"} {
		t.Run(scenario, func(t *testing.T) {
			oldRun, oldVerify, oldRuntime := runMinecraftContainerUpdate, verifyMinecraftContainerCurrent, instanceRuntimes
			t.Cleanup(func() {
				runMinecraftContainerUpdate, verifyMinecraftContainerCurrent, instanceRuntimes = oldRun, oldVerify, oldRuntime
			})
			f := newInstanceActivationFixture(t)
			f.backend.networkRoot = filepath.Join(t.TempDir(), "network")
			instanceRuntimes = f.backend
			id := f.configs[0].ID
			if scenario == "recovered" {
				f.failReload = 2
				if err := f.apply(id); !errors.Is(err, errInstanceRecovery) {
					t.Fatalf("prepare recoverable runtime: %v", err)
				}
			}
			if scenario == "recovery_failed" {
				dir, _, _ := f.backend.paths(id)
				if err := f.backend.writeJSON(filepath.Join(dir, "pending.json"), map[string]any{}, false); err != nil {
					t.Fatal(err)
				}
			}
			s := surfaceTestServer(t, roleAdministrator)
			s.operations = openTestOperationStore(t)
			journal, _, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftContainerUpdate, id)
			if err != nil {
				t.Fatal(err)
			}
			updateErr, verifyErr := errors.New("update failed"), errors.New("verification failed")
			calls, verifies := 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runMinecraftContainerUpdate = func(ctx context.Context, r minecraftContainerUpdateRequest) error {
				calls++
				if _, ok := ctx.Value(operationTraceKey{}).(operationTraceContext); !ok || r.InstanceID != id {
					t.Fatal("trace or immutable instance detached")
				}
				cancel()
				return updateErr
			}
			verifyMinecraftContainerCurrent = func(ctx context.Context, selected string) error {
				verifies++
				deadline, bounded := ctx.Deadline()
				if selected != id || ctx.Err() != nil || !bounded || time.Until(deadline) > 90*time.Second {
					t.Fatal("verification lost immutable instance or fresh bounded context")
				}
				var durable operationJournal
				data, err := os.ReadFile(filepath.Join(s.operations.operationsDir, journal.OperationID+".json"))
				if err != nil || json.Unmarshal(data, &durable) != nil || durable.State != operationNeedsAttention || durable.Stage != "update_failed" {
					t.Fatalf("verification preceded durable failure: %+v %v", durable, err)
				}
				if pending, err := f.backend.pending(id); err != nil || pending {
					t.Fatal("verification preceded runtime recovery", pending, err)
				}
				if scenario == "verification_failed" {
					return verifyErr
				}
				return nil
			}
			err = executeMinecraftContainerUpdate(ctx, s, journal.OperationID, minecraftContainerUpdateRequest{InstanceID: id, PlanFingerprint: testMinecraftResetFingerprint}, session{})
			if !errors.Is(err, updateErr) || calls != 1 {
				t.Fatalf("update failure lost or retried: %v calls=%d", err, calls)
			}
			if scenario == "verification_failed" && !errors.Is(err, verifyErr) {
				t.Fatal("verification error lost", err)
			}
			if scenario == "recovery_failed" {
				if verifies != 0 || !errors.Is(err, errContainerRuntimeRecovery) {
					t.Fatal("failed recovery bypassed or lost", verifies, err)
				}
			} else if verifies != 1 {
				t.Fatal("runtime verification not performed exactly once", verifies)
			}
			finished, err := s.operations.get(journal.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.operations.currentMinecraftMaintenance()
			if err != nil {
				t.Fatal(err)
			}
			safe := scenario == "safe" || scenario == "recovered"
			if safe {
				if finished.State != operationResolved || current != nil || s.operations.currentMinecraftResetID != "" || !strings.Contains(finished.Status, "stopped") || !strings.Contains(finished.Status, "kept automatically") {
					t.Fatalf("verified runtime retained maintenance or lost failure status: %+v current=%+v", finished, current)
				}
			} else if finished.State != operationNeedsAttention || finished.Stage != "update_failed" || finished.Status != "Update stopped. Check the details below. You can keep the current version or try again." || current == nil || current.OperationID != journal.OperationID {
				t.Fatalf("unsafe failure changed or released maintenance: %+v current=%+v", finished, current)
			}
			for _, held := range []bool{s.operations.setupLockHeld, s.operations.restoreLockHeld, s.operations.dataMigrationLockHeld, s.operations.migrationLockHeld} {
				if held == safe {
					t.Fatal("incorrect shared exclusion lock ownership")
				}
			}
			_, created, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypePluginInstall, "jv-d4e5f6")
			if safe && (err != nil || !created) || !safe && err == nil {
				t.Fatalf("competing maintenance: created=%v err=%v", created, err)
			}
		})
	}
}

func TestContainerPullActualOutputThroughProtectedTrace(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeMinecraftContainerUpdate)
	helper, err := os.ReadFile("../../../mjust/libexec/operation-trace.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	library := filepath.Join(dir, "trace.sh")
	if err = os.WriteFile(library, helper, 0600); err != nil {
		t.Fatal(err)
	}
	for _, exitCode := range []string{"0", "17"} {
		// Only the fixture function runs; there is no real Podman/network invocation.
		script := `source "$1"
podman() { printf 'Copying blob fixture\nCopying config fixture\nWriting manifest fixture\n\033[31mred\033[0m\ntoken=fixture-secret\n'; return "${FIXTURE_PULL_EXIT}"; }
jv_trace_pull docker.io/itzg/minecraft-server@sha256:fixture
`
		cmd := exec.Command("bash", "-c", `export FIXTURE_PULL_EXIT="$2"`+"\n"+script, "fixture", library, exitCode)
		finish, err := attachOperationTrace(withOperationTrace(context.Background(), store, id), cmd)
		if err != nil {
			t.Fatal(err)
		}
		runErr := cmd.Run()
		finish(runErr)
		if (runErr == nil) != (exitCode == "0") {
			t.Fatal("pull exit status lost")
		}
	}
	data, err := store.readTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Copying blob fixture", "Copying config fixture", "Writing manifest fixture", "[exit 17]"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("trace lost %s", want)
		}
	}
	if strings.Contains(string(data), "fixture-secret") || strings.Contains(string(data), "\033") {
		t.Fatal("trace not sanitized")
	}
}

func TestContainerInterruptedJournalKeepsExclusionAndSelectedIdentity(t *testing.T) {
	store := openTestOperationStore(t)
	journal, _, err := store.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftContainerUpdate, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.transition(journal.OperationID, operationValidating, "validating", "Reviewing image"); err != nil {
		t.Fatal(err)
	}
	base := store.baseDir
	if err = store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	current, err := reopened.currentMinecraftMaintenance()
	if err != nil || current == nil || current.State != operationNeedsAttention || current.InstanceID != "jv-a1b2c3" || current.OperationType != operationTypeMinecraftContainerUpdate {
		t.Fatalf("interrupted operation lost: %+v %v", current, err)
	}
	if _, _, err = reopened.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftUpdate, "jv-sibling"); err == nil {
		t.Fatal("interrupted container operation released exclusion")
	}
}

func TestContainerRetentionStrictNumericDefaultAndAuthorization(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := runMinecraftContainerRetention
	t.Cleanup(func() { runMinecraftContainerRetention = old })
	calls := 0
	got := -1
	runMinecraftContainerRetention = func(_ context.Context, id string, n int) error {
		calls++
		got = n
		if id != "jv-a1b2c3" {
			t.Fatal(id)
		}
		return nil
	}
	for _, body := range []string{`{"keep_previous_images":-1}`, `{"keep_previous_images":1.5}`, `{"keep_previous_images":"1"}`, `{"keep_previous_images":1,"keep_previous_images":2}`, `{"keep_previous_images":1,"KEEP_PREVIOUS_IMAGES":2}`, `{"keep_previous_images":1,"command":"prune"}`, `{} {}`} {
		s := surfaceTestServer(t, roleAdministrator)
		w := httptest.NewRecorder()
		s.adminMinecraftContainerRetention(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/retention", body))
		if w.Code != 400 || calls != 0 {
			t.Fatalf("accepted %s: %d", body, w.Code)
		}
	}
	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		w := httptest.NewRecorder()
		s.adminMinecraftContainerRetention(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/retention", `{"keep_previous_images":0}`))
		if w.Code != 403 || calls != 0 {
			t.Fatal("retention authorization bypass")
		}
	}
	for _, item := range []struct {
		body string
		want int
	}{{`{}`, 1}, {`{"keep_previous_images":0}`, 0}, {`{"keep_previous_images":2}`, 2}, {`{"keep_previous_images":25}`, 25}} {
		s := surfaceTestServer(t, roleAdministrator)
		w := httptest.NewRecorder()
		s.adminMinecraftContainerRetention(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/retention", item.body))
		if w.Code != 200 || got != item.want {
			t.Fatalf("retention %s: %d %d", item.body, w.Code, got)
		}
	}
}
func TestContainerRestoreRequiresRetainedDigestAndReviewedLocalFingerprint(t *testing.T) {
	whitelistSurfaceFixture(t)
	oldStatus, oldWorker := runMinecraftContainerStatus, startMinecraftContainerUpdateWorker
	t.Cleanup(func() { runMinecraftContainerStatus = oldStatus; startMinecraftContainerUpdateWorker = oldWorker })
	digest := "sha256:" + strings.Repeat("a", 64)
	running := true
	starts := 0
	runMinecraftContainerStatus = func(_ context.Context, id string) (minecraftContainerStatus, error) {
		return minecraftContainerStatus{InstanceID: id, State: "unavailable", Running: running, RestoreFingerprint: testMinecraftResetFingerprint, PreviousImages: []retainedContainerImage{{Digest: digest}}}, nil
	}
	startMinecraftContainerUpdateWorker = func(_ *server, _ string, r minecraftContainerUpdateRequest, _ session) {
		starts++
		if r.RestoreDigest != digest || r.RestartNow != running {
			t.Fatal("restore identity changed")
		}
	}
	for _, active := range []bool{true, false} {
		running = active
		s := surfaceTestServer(t, roleAdministrator)
		s.operations = openTestOperationStore(t)
		body := `{"plan_fingerprint":"` + testMinecraftResetFingerprint + `","restore_digest":"` + digest + `","restart_now":` + boolText(running) + `}`
		for _, bad := range []string{strings.Replace(body, digest, "stable", 1), strings.Replace(body, digest, "sha256:"+strings.Repeat("b", 64), 1), strings.Replace(body, testMinecraftResetFingerprint, "sha256:"+strings.Repeat("c", 64), 1), strings.TrimSuffix(body, "}") + `,"restore_digest":"` + digest + `"}`} {
			w := httptest.NewRecorder()
			s.adminMinecraftContainerUpdate(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/update", bad))
			if w.Code < 400 {
				t.Fatal("unsafe restore accepted")
			}
		}
		w := httptest.NewRecorder()
		s.adminMinecraftContainerUpdate(w, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/container/update", body))
		if w.Code != 202 {
			t.Fatalf("offline restore: %d %s", w.Code, w.Body.String())
		}
	}
	if starts != 2 {
		t.Fatal(starts)
	}
}
