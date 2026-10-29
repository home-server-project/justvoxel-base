package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const restoreTestArchive = "instance-backup-paper-2026-10-15-1200.tar.gz"

type instanceRestoreFixture struct {
	*instanceActivationFixture
	server        *server
	steps         []string
	fail          string
	failRollback  bool
	failRestart   bool
	beforePrepare func()
	starts        int
	warnings      []string
}

func newInstanceRestoreFixture(t *testing.T) *instanceRestoreFixture {
	t.Helper()
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	oldExclusion := instanceRestoreExclusionPath
	oldBackend, oldCommand, oldLock, oldPermit := instanceRuntimes, instanceBackupStorageCommand, pluginRuntimeLockPath, instanceRestorePermitRoot
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	instanceRestorePermitRoot = t.TempDir()
	instanceRestoreExclusionPath = filepath.Join(t.TempDir(), "restore-exclusion.lock")
	t.Cleanup(func() {
		instanceRestoreExclusionPath = oldExclusion
		instanceRuntimes = oldBackend
		instanceBackupStorageCommand = oldCommand
		pluginRuntimeLockPath = oldLock
		instanceRestorePermitRoot = oldPermit
		instanceRestoreActive.Store(false)
		instanceRestoreActiveID.Store(nil)
	})
	x := &instanceRestoreFixture{instanceActivationFixture: f, server: roleServerForTest(roleAdministrator)}
	x.server.operations = openTestOperationStore(t)
	f.backend.pause = func(context.Context, time.Duration) error { return nil }
	base := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		id := f.configs[0].ID
		if command == "podman" && len(args) == 4 && strings.HasPrefix(args[3], "say ") {
			x.warnings = append(x.warnings, args[3])
			return nil, nil
		}
		if command == "systemctl" && len(args) == 4 && args[0] == "--no-block" && args[1] == "stop" {
			if args[3] != "minecraft-"+id+".service" {
				t.Fatal("foreign stop", args)
			}
			f.states[id] = "inactive"
			f.containers[id] = "exited"
			return nil, nil
		}
		if command == "systemctl" && len(args) == 3 && args[0] == "start" {
			if args[2] != "minecraft-"+id+".service" {
				t.Fatal("foreign restart", args)
			}
			// A real independent process must acquire the flock during ExecStartPre.
			if err := exec.Command("flock", "-n", pluginRuntimeLockPath, "true").Run(); err != nil {
				t.Fatal("startup flock handoff failed", err)
			}
			if err := exec.Command("flock", "-n", instanceRestoreExclusionPath, "true").Run(); err == nil {
				t.Fatal("legacy backup could enter during startup or rollback handoff")
			}
			if release, err := x.server.lockInstanceBackup(); err == nil {
				release()
				t.Fatal("retention entered during startup")
			}
			if release, err := x.server.lockInstanceRuntime(); err == nil {
				release()
				t.Fatal("runtime reconfiguration entered during startup")
			}
			if claim, err := x.server.claimInstanceRestore(false); err == nil {
				claim.close()
				t.Fatal("second restore entered during startup")
			}
			o := x.server.operations
			for _, file := range []*os.File{o.lockFile, o.restoreLockFile, o.dataMigrationLockFile, o.migrationLockFile} {
				if err := exec.Command("flock", "-n", file.Name(), "true").Run(); err == nil {
					t.Fatal("another process entered operation claim during startup", file.Name())
				}
			}
			if !x.server.operations.setupLockHeld || !x.server.operations.restoreLockHeld {
				t.Fatal("persistent claims released during startup")
			}
			if _, err := os.Stat(filepath.Join(instanceRestorePermitRoot, "instance-restore-start-"+id+".json")); err != nil {
				t.Fatal("startup permit missing", err)
			}
			x.starts++
			if x.failRestart {
				x.failRestart = false
				return nil, errors.New("private runtime secret must not leak")
			}
			f.states[id] = "active"
			f.containers[id] = "running"
			return nil, nil
		}
		return base(ctx, command, args...)
	}
	instanceBackupStorageCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command != "nsenter" || len(args) != 8 || args[2] != instanceRestoreHelper || args[4] != f.configs[0].ID {
			t.Fatal("unsafe restore storage command", command, args)
		}
		action, id := args[3], args[4]
		x.steps = append(x.steps, action)
		if action == "plan" {
			p := instanceRestoreInspection{Metadata: instanceBackupMetadata{ID: id, Archive: restoreTestArchive, Software: "paper", Version: "1.21.4"}, CurrentSoftware: "paper", CurrentVersion: "1.21.4", Integrity: "verified", SourceAvailable: true, DestinationAvailable: true, Compatibility: "same_software_and_pinned_version", DestructiveConfirmationRequired: true, Fingerprint: instanceHash([]byte("independent-restore-inspection-fixture"))}
			return json.Marshal(p)
		}
		if action == "prepare" && x.beforePrepare != nil {
			x.beforePrepare()
		}
		if action == x.fail || (action == "rollback" && x.failRollback) {
			return nil, errors.New("private helper secret must not leak")
		}
		if action == "prepare" {
			j := instanceRestoreJournal{Schema: 1, ID: id, Transaction: strings.Repeat("b", 32), ConfigSHA256: strings.Repeat("c", 64), Parent: []uint64{1, 2}, Original: []uint64{1, 3}, Replacement: []uint64{1, 4}, Archive: restoreTestArchive, Fingerprint: args[6], Stage: "ready_to_apply", OriginallyRunning: args[7] == "true"}
			return nil, f.backend.writeJSON(filepath.Join(f.backend.root, id, "restore-journal.json"), j, true)
		}
		j, err := f.backend.restoreJournal(id)
		if err != nil {
			return nil, err
		}
		stages := map[string]string{"shutdown": "shutdown", "activate": "original_preserved", "verifying": "runtime_verification", "completed": "completed", "rollback": "original_restored", "rollback-verifying": "rollback_verification", "rolled-back": "rolled_back", "recovery-required": "recovery_required"}
		j.Stage = stages[action]
		if j.Stage == "" {
			t.Fatal("unexpected storage action", action)
		}
		return nil, f.backend.writeJSON(filepath.Join(f.backend.root, id, "restore-journal.json"), j, true)
	}
	return x
}
func (f *instanceRestoreFixture) plan(t *testing.T, running bool) instanceRestorePlan {
	t.Helper()
	id := f.configs[0].ID
	if running {
		f.states[id] = "active"
		f.containers[id] = "running"
	}
	p, err := f.backend.planInstanceRestore(context.Background(), id, restoreTestArchive)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *instanceRestoreFixture) run(t *testing.T, p instanceRestorePlan) error {
	t.Helper()
	claim, err := f.server.claimInstanceRestore(false)
	if err != nil {
		t.Fatal(err)
	}
	id := f.configs[0].ID
	instanceRestoreActiveID.Store(&id)
	err = f.server.runInstanceRestore(context.Background(), id, p, claim)
	return errors.Join(err, claim.close())
}
func TestIndependentRestoreRuntimeIntentIsolationAndStaging(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "running"}[running], func(t *testing.T) {
			f := newInstanceRestoreFixture(t)
			p := f.plan(t, running)
			id, other := f.configs[0].ID, f.configs[1].ID
			f.beforePrepare = func() {
				if running && f.states[id] != "active" {
					t.Fatal("Minecraft stopped before staging")
				}
			}
			if err := f.run(t, p); err != nil {
				t.Fatal(err)
			}
			j, err := f.backend.restoreJournal(id)
			if err != nil || j.Stage != "completed" {
				t.Fatal(j, err)
			}
			if (f.states[id] == "active") != running || f.states[other] != "inactive" {
				t.Fatal("running intent or instance isolation lost", f.states)
			}
			o := f.server.operations
			for _, path := range []string{pluginRuntimeLockPath, instanceRestoreExclusionPath, o.lockFile.Name(), o.restoreLockFile.Name(), o.dataMigrationLockFile.Name(), o.migrationLockFile.Name()} {
				if err := exec.Command("flock", "-n", path, "true").Run(); err != nil {
					t.Fatal("completed transaction leaked flock", path, err)
				}
			}
			if f.server.operations.setupLockHeld || f.server.operations.restoreLockHeld || instanceRestoreActive.Load() {
				t.Fatal("completed transaction leaked operation claim")
			}
			if (f.starts == 1) != running {
				t.Fatal("unexpected restart count", f.starts)
			}
			if slices.Index(f.steps, "prepare") > slices.Index(f.steps, "shutdown") {
				t.Fatal("downtime before preparation", f.steps)
			}
		})
	}
}
func TestIndependentRestorePreparationFailureDoesNotInterrupt(t *testing.T) {
	f := newInstanceRestoreFixture(t)
	p := f.plan(t, true)
	f.fail = "prepare"
	if err := f.run(t, p); err == nil {
		t.Fatal("preparation failure accepted")
	}
	if f.states[f.configs[0].ID] != "active" || f.starts != 0 || slices.Contains(f.steps, "shutdown") {
		t.Fatal("preparation interrupted runtime")
	}
}
func TestIndependentRestorePlayersAndUnknownPresence(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "online", true: "unknown"}[unknown], func(t *testing.T) {
			f := newInstanceRestoreFixture(t)
			p := f.plan(t, true)
			f.players = "There are 1 of a max of 20 players online: Alex"
			if unknown {
				f.failPlayers = true
			}
			err := f.run(t, p)
			if unknown {
				if err == nil || f.states[f.configs[0].ID] != "active" || slices.Contains(f.steps, "activate") {
					t.Fatal("unknown presence interrupted")
				}
			} else if err != nil || len(f.warnings) == 0 || !strings.Contains(f.warnings[0], "60 seconds") {
				t.Fatal("online players did not receive graceful countdown", err, f.warnings)
			}
		})
	}
}
func TestIndependentRestoreFailedActivationAndRecoveryRetry(t *testing.T) {
	f := newInstanceRestoreFixture(t)
	p := f.plan(t, true)
	f.fail = "activate"
	f.failRollback = true
	if err := f.run(t, p); err == nil {
		t.Fatal("failed restore reported success")
	}
	id := f.configs[0].ID
	j, err := f.backend.restoreJournal(id)
	if err != nil || j.Stage != "recovery_required" {
		t.Fatal(j, err)
	}
	if release, err := f.server.lockInstanceBackup(); err == nil {
		release()
		t.Fatal("backup entered unresolved restore")
	}
	if release, err := f.server.lockInstanceRuntime(); err == nil {
		release()
		t.Fatal("configuration entered unresolved restore")
	}
	f.fail = ""
	f.failRollback = false
	claim, err := f.server.claimInstanceRestore(true)
	if err != nil {
		t.Fatal(err)
	}
	err = claim.recover(context.Background(), id)
	err = errors.Join(err, claim.close())
	if err != nil {
		t.Fatal(err)
	}
	j, err = f.backend.restoreJournal(id)
	if err != nil || j.Stage != "rolled_back" || f.states[id] != "active" {
		t.Fatal(j, err, f.states)
	}
}
func TestIndependentRestoreRestartFailureRollsBack(t *testing.T) {
	f := newInstanceRestoreFixture(t)
	p := f.plan(t, true)
	f.failRestart = true
	if err := f.run(t, p); err == nil {
		t.Fatal("restart failure reported success")
	}
	j, err := f.backend.restoreJournal(f.configs[0].ID)
	if err != nil || j.Stage != "rolled_back" || f.starts != 2 {
		t.Fatal(j, err, f.starts)
	}
}
func TestIndependentRestoreAPIPrivilegesAndStrictRequests(t *testing.T) {
	f := newInstanceRestoreFixture(t)
	id := f.configs[0].ID
	_, environment, _ := f.backend.paths(id)
	// Explicit private values, not generic directory names or words. Never log
	// these values or response bodies, including on unexpected HTTP statuses.
	privatePaths := []string{f.backend.root, f.snapshot.StorageRoot, f.backend.staging,
		environment, instanceRestorePermitRoot, pluginRuntimeLockPath, instanceRestoreExclusionPath,
		"/etc/justvoxel/instances/", "/var/lib/justvoxel/instances/", "/run/justvoxel/",
		instanceRestoreHelper, "/synthetic-private/transaction/world", "SYNTHETIC_HELPER_STDERR", "SYNTHETIC_RUNTIME_DIAGNOSTIC"}
	for _, c := range f.configs {
		if c.RCONPassword == "" {
			t.Fatal("privacy fixture requires a nonempty credential")
		}
	}
	for _, path := range privatePaths {
		if path == "" {
			t.Fatal("privacy fixture requires nonempty private paths")
		}
	}
	check := func(role principalRole, route string, rr *httptest.ResponseRecorder, want int) {
		t.Helper()
		t.Logf("role=%s route=%s status=%d", role, route, rr.Code)
		if rr.Code != want {
			t.Errorf("role=%s route=%s status=%d want=%d", role, route, rr.Code, want)
		}
		// Decode before checking so JSON escaping cannot hide a private value.
		var response any
		if json.Unmarshal(rr.Body.Bytes(), &response) != nil {
			t.Fatalf("role=%s route=%s invalid JSON", role, route)
		}
		var inspect func(any)
		inspect = func(value any) {
			switch v := value.(type) {
			case string:
				for _, c := range f.configs {
					if strings.Contains(v, c.RCONPassword) {
						t.Errorf("role=%s route=%s leak=RCON_PASSWORD", role, route)
					}
				}
				for _, path := range privatePaths {
					if strings.Contains(v, path) {
						t.Errorf("role=%s route=%s leak=PRIVATE_PATH", role, route)
					}
				}
			case map[string]any:
				for key, item := range v {
					inspect(key)
					inspect(item)
				}
			case []any:
				for _, item := range v {
					inspect(item)
				}
			}
		}
		inspect(response)
		// Pin the public response fields independently of production structs so a
		// new private field cannot silently become part of the public contract.
		allowed := strings.Fields("error id recovery_required")
		if rr.Code == http.StatusOK {
			switch route {
			case "plan":
				allowed = strings.Fields("metadata current_software current_version integrity source_available destination_available compatibility destructive_confirmation_required required_bytes expanded_bytes entries fingerprint plan_id runtime_state players players_confirmation_required")
			case "status":
				allowed = strings.Fields("id state stage archive originally_running recovery_required")
			}
		}
		object, ok := response.(map[string]any)
		if !ok {
			t.Fatalf("role=%s route=%s expected response object", role, route)
		}
		checkFields := func(object map[string]any, allowed []string) {
			for key := range object {
				if !slices.Contains(allowed, key) {
					t.Errorf("role=%s route=%s unexpected response field", role, route)
				}
			}
		}
		checkFields(object, allowed)
		if rr.Code >= 400 {
			message, ok := object["error"].(string)
			if !ok || strings.TrimSpace(message) == "" || len(message) > 512 {
				t.Errorf("role=%s route=%s missing or unbounded public error", role, route)
			}
		}
		for key, allowed := range map[string][]string{
			"metadata": strings.Fields("schema id name software version created archive size configuration_sha256 sha256"),
			"players":  strings.Fields("id state online maximum"),
		} {
			if value, exists := object[key]; exists {
				nested, ok := value.(map[string]any)
				if !ok {
					t.Fatalf("role=%s route=%s invalid public object", role, route)
				}
				checkFields(nested, allowed)
			}
		}
	}
	request := func(mux *http.ServeMux, role principalRole, method, route, body string, want int) {
		t.Helper()
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(method, "/v1/minecraft/instances/"+id+"/backups/restore/"+route, body))
		check(role, route, rr, want)
	}
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		s := roleServerForTest(role)
		s.operations = f.server.operations
		mux := http.NewServeMux()
		registerInstanceRestoreRoutes(mux, s)
		for _, route := range []struct {
			method, path, body string
			status             int
		}{
			{"POST", "plan", `{"archive":"` + restoreTestArchive + `"}`, 200},
			{"POST", "apply", `{}`, 400}, {"GET", "status", "", 200}, {"POST", "recover", `{}`, 400},
			{"POST", "apply", `{"unknown":true}`, 400}, {"POST", "recover", `{"unknown":true}`, 400},
		} {
			want := route.status
			if role != roleAdministrator {
				want = http.StatusForbidden
			}
			request(mux, role, route.method, route.path, route.body, want)
		}
	}
	mux := http.NewServeMux()
	registerInstanceRestoreRoutes(mux, f.server)
	for _, payload := range []string{`{"archive":"../../escape"}`, `{"archive":"` + restoreTestArchive + `","path":"/tmp/world"}`} {
		want := http.StatusBadRequest
		if strings.Contains(payload, "../../escape") {
			want = http.StatusConflict
		}
		request(mux, roleAdministrator, "POST", "plan", payload, want)
	}
	// Stale reviewed identity cannot reach preparation.
	apply := `{"archive":"` + restoreTestArchive + `","plan_id":"` + strings.Repeat("d", 64) + `","confirm_destructive":true,"confirm_players":true}`
	request(mux, roleAdministrator, "POST", "apply", apply, http.StatusConflict)
	if slices.Contains(f.steps, "prepare") {
		t.Fatal("stale plan applied")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/minecraft/instances/"+id+"/backups/restore/plan", strings.NewReader(`{}`)))
	check("unauthenticated", "plan", rr, http.StatusUnauthorized)

	// Inject raw helper stderr/exec errors and runtime filesystem errors. Each
	// response must retain a bounded useful error without exposing diagnostics.
	diagnostic := "SYNTHETIC_HELPER_STDERR SYNTHETIC_RUNTIME_DIAGNOSTIC /synthetic-private/transaction/world " + environment + " " + f.configs[0].RCONPassword
	t.Run("helper errors", func(t *testing.T) {
		calls := 0
		original := instanceBackupStorageCommand
		defer func() { instanceBackupStorageCommand = original }()
		instanceBackupStorageCommand = func(context.Context, string, ...string) ([]byte, error) {
			calls++
			return []byte(diagnostic), &exec.Error{Name: diagnostic, Err: os.ErrPermission}
		}
		request(mux, roleAdministrator, "POST", "plan", `{"archive":"`+restoreTestArchive+`"}`, http.StatusConflict)
		request(mux, roleAdministrator, "POST", "apply", apply, http.StatusConflict)
		if calls != 2 {
			t.Fatal("injected failure did not reach both routes")
		}
	})
	t.Run("runtime errors", func(t *testing.T) {
		calls := 0
		original := f.backend.command
		defer func() { f.backend.command = original }()
		f.backend.command = func(context.Context, string, ...string) ([]byte, error) {
			calls++
			return []byte(diagnostic), &os.PathError{Op: "open", Path: diagnostic, Err: os.ErrPermission}
		}
		request(mux, roleAdministrator, "POST", "plan", `{"archive":"`+restoreTestArchive+`"}`, http.StatusConflict)
		request(mux, roleAdministrator, "POST", "apply", apply, http.StatusConflict)
		if calls != 2 {
			t.Fatal("injected failure did not reach both routes")
		}
	})
	t.Run("private filesystem errors", func(t *testing.T) {
		original := f.backend.root
		defer func() { f.backend.root = original }()
		f.backend.root = filepath.Join(original, "missing-private-runtime")
		request(mux, roleAdministrator, "GET", "status", "", http.StatusServiceUnavailable)
		request(mux, roleAdministrator, "POST", "recover", `{"confirm_players":true}`, http.StatusServiceUnavailable)
	})
	// Persisted results and journals contain private transaction evidence;
	// Status must expose only the public result and restore-stage projection.
	f.server.saveInstanceRestoreResult(id, errors.New(diagnostic))
	request(mux, roleAdministrator, "GET", "status", "", http.StatusOK)
	journal := instanceRestoreJournal{Schema: 1, ID: id, Transaction: strings.Repeat("b", 32), ConfigSHA256: strings.Repeat("c", 64), Parent: []uint64{1, 2}, Original: []uint64{1, 3}, Replacement: []uint64{1, 4}, Archive: restoreTestArchive, Fingerprint: instanceHash([]byte("journal-fixture")), Stage: "completed", OriginallyRunning: true}
	if err := f.backend.writeJSON(filepath.Join(f.backend.root, id, "restore-journal.json"), journal, true); err != nil {
		t.Fatal("could not create private journal fixture")
	}
	request(mux, roleAdministrator, "GET", "status", "", http.StatusOK)
}

// A separate flock process models a legacy worker that already owns exclusion.
// Restore must refuse promptly, including when that worker waits for maintenance.
func TestIndependentRestoreRefusesLegacyOwnerWithoutCircularWait(t *testing.T) {
	f := newInstanceRestoreFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", `exec 8>"$1"; chmod 600 "$1"; flock -n 8 || exit 1; echo ready; read -r release`, "legacy-backup", instanceRestoreExclusionPath)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	ready := make([]byte, 6)
	if _, err = io.ReadFull(output, ready); err != nil || string(ready) != "ready\n" {
		t.Fatal("legacy owner did not start", err)
	}
	// Model a caller that entered legacy backup with maintenance already held.
	maintenance, err := acquirePluginRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	started := time.Now()
	for _, recovery := range []bool{false, true} {
		if claim, err := f.server.claimInstanceRestore(recovery); err == nil {
			claim.close()
			t.Fatal("Restore or recovery interrupted an already running legacy backup")
		}
	}
	if time.Since(started) > time.Second {
		t.Fatal("exclusion acquisition waited")
	}
	if f.server.operations.setupLockHeld || f.server.operations.restoreLockHeld || instanceRestoreActive.Load() {
		t.Fatal("refusal leaked operation claims")
	}
}

func TestIndependentRestoreMaintenanceRefusalUnwindsExclusion(t *testing.T) {
	f := newInstanceRestoreFixture(t)
	maintenance, err := acquirePluginRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	for _, recovery := range []bool{false, true} {
		if claim, err := f.server.claimInstanceRestore(recovery); err == nil {
			claim.close()
			t.Fatal("Restore bypassed existing maintenance")
		}
		if err := exec.Command("flock", "-n", instanceRestoreExclusionPath, "true").Run(); err != nil {
			t.Fatal("maintenance refusal leaked exclusion", err)
		}
	}
	if f.server.operations.setupLockHeld || instanceRestoreActive.Load() {
		t.Fatal("maintenance refusal leaked claims")
	}
}

func TestIndependentRestoreMissingReviewedConfirmationCannotPrepare(t *testing.T) {
	f := newInstanceRestoreFixture(t)
	plan := f.plan(t, true)
	mux := http.NewServeMux()
	registerInstanceRestoreRoutes(mux, f.server)
	for _, request := range []instanceRestoreRequest{
		{Archive: restoreTestArchive, PlanID: plan.PlanID, ConfirmPlayers: true},
		{Archive: restoreTestArchive, ConfirmDestructive: true, ConfirmPlayers: true},
		{Archive: restoreTestArchive, PlanID: plan.PlanID, ConfirmDestructive: true},
	} {
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+f.configs[0].ID+"/backups/restore/apply", string(body)))
		if rr.Code < 400 || slices.Contains(f.steps, "prepare") {
			t.Fatal("unreviewed restore applied", rr.Code)
		}
		if instanceRestoreActive.Load() {
			t.Fatal("refused confirmation leaked claim")
		}
	}
}

func TestInstanceRestoreArchiveBasenames(t *testing.T) {
	for _, name := range []string{restoreTestArchive, "Paper1-backup-paper-2026-10-28-0831.tar.gz", "Family's Server-backup-paper-2026-10-28-0831-2.tar.gz", "家族のサーバー-backup-paper-2026-10-28-0831.tar.gz"} {
		if !validInstanceRestoreArchive(name) {
			t.Errorf("valid archive rejected: %q", name)
		}
	}
	for _, name := range []string{"../Paper1-backup-paper-2026-10-28-0831.tar.gz", "/Paper1-backup-paper-2026-10-28-0831.tar.gz", "jv-family1/Paper1-backup-paper-2026-10-28-0831.tar.gz", "bad\nname-backup-paper-2026-10-28-0831.tar.gz", "Paper1-backup-paper-2026-10-28-0831.tar.gz.partial"} {
		if validInstanceRestoreArchive(name) {
			t.Errorf("unsafe archive accepted: %q", name)
		}
	}
}
