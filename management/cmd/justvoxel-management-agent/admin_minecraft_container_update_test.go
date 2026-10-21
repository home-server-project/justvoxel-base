package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	old := runMinecraftContainerUpdate
	defer func() { runMinecraftContainerUpdate = old }()
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	journal, _, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypeMinecraftContainerUpdate, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	runMinecraftContainerUpdate = func(ctx context.Context, r minecraftContainerUpdateRequest) error {
		if _, ok := ctx.Value(operationTraceKey{}).(operationTraceContext); !ok {
			t.Fatal("trace detached")
		}
		return errors.New("interrupted fixture")
	}
	if executeMinecraftContainerUpdate(context.Background(), s, journal.OperationID, minecraftContainerUpdateRequest{InstanceID: "jv-a1b2c3", PlanFingerprint: testMinecraftResetFingerprint}, session{}) == nil {
		t.Fatal("failure lost")
	}
	current, _ := s.operations.currentMinecraftMaintenance()
	if current == nil || current.State != operationNeedsAttention {
		t.Fatal("failure released maintenance")
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
