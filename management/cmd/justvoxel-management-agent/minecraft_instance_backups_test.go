package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type instanceBackupFixture struct {
	*instanceActivationFixture
	server         *server
	storage        []string
	failStage      string
	restartFailure bool
	timerEnabled   map[string]bool
	timerActive    map[string]bool
	scheduleCalls  []string
	failApply      bool
}

func newInstanceBackupFixture(t *testing.T) *instanceBackupFixture {
	t.Helper()
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	oldBackend, oldLock, oldUnits, oldTemplates := instanceRuntimes, pluginRuntimeLockPath, instanceBackupUnits, instanceBackupTemplates
	oldStorageCommand := instanceBackupStorageCommand
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	instanceBackupUnits = t.TempDir()
	instanceBackupTemplates = filepath.Join(repositoryRoot(t), "templates/systemd")
	t.Cleanup(func() {
		instanceRuntimes = oldBackend
		pluginRuntimeLockPath = oldLock
		instanceBackupUnits = oldUnits
		instanceBackupTemplates = oldTemplates
		instanceBackupStorageCommand = oldStorageCommand
	})
	x := &instanceBackupFixture{instanceActivationFixture: f, server: roleServerForTest(roleAdministrator), timerEnabled: map[string]bool{}, timerActive: map[string]bool{}}
	x.server.operations = openTestOperationStore(t)
	base := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "nsenter" && (len(args) == 5 || len(args) == 6) && args[2] == instanceBackupHelper {
			if args[4] != f.configs[0].ID && args[4] != f.configs[1].ID {
				t.Fatal("foreign storage", args)
			}
			if args[3] == "create" && (len(args) != 6 || args[5] == "" || args[5] == args[4]) {
				t.Fatal("missing registered backup name", args)
			}
			x.storage = append(x.storage, args[3]+":"+args[4])
			if args[3] == x.failStage {
				return nil, errors.New("private helper diagnostic must not leak")
			}
			if args[3] == "inventory" {
				return []byte(`[]`), nil
			}
			return nil, nil
		}
		if command == "systemctl" {
			unit := args[len(args)-1]
			if unit == "minecraft-backup.timer" {
				switch args[0] {
				case "is-enabled":
					return []byte("enabled"), nil
				case "is-active":
					return []byte("active"), nil
				case "cat":
					return []byte("# /etc/systemd/system/minecraft-backup.timer\n[Timer]\nOnCalendar=*-*-* 04:30:00\nPersistent=true\nAccuracySec=1min\nUnit=minecraft-backup.service\n"), nil
				}
			}
			if strings.HasPrefix(unit, "minecraft-backup-jv-") {
				x.scheduleCalls = append(x.scheduleCalls, strings.Join(args, " "))
				switch args[0] {
				case "is-enabled":
					if x.timerEnabled[unit] {
						return []byte("enabled"), nil
					}
					return []byte("disabled"), instanceTestExit{}
				case "is-active":
					if x.timerActive[unit] {
						return []byte("active"), nil
					}
					return []byte("inactive"), instanceTestExit{}
				case "show":
					return []byte("inactive"), nil
				case "enable":
					if x.failApply {
						x.failApply = false
						return nil, errors.New("apply failed")
					}
					x.timerEnabled[unit] = true
					if len(args) > 2 {
						x.timerActive[unit] = true
					}
					return nil, nil
				case "disable":
					x.timerEnabled[unit] = false
					x.timerActive[unit] = false
					return nil, nil
				case "start":
					x.timerActive[unit] = true
					return nil, nil
				case "--no-block":
					return nil, nil
				}
			}
			if reflect.DeepEqual(args, []string{"start", "--", "minecraft-" + f.configs[0].ID + ".service"}) {
				// Prove release-before-start, including an independent flock holder.
				release, e := x.server.lockInstanceBackup()
				if e != nil {
					t.Fatal("restart submitted with maintenance lock held", e)
				}
				if e = release(); e != nil {
					t.Fatal(e)
				}
				if x.restartFailure {
					return nil, errors.New("restart failed")
				}
				f.states[f.configs[0].ID] = "active"
				f.containers[f.configs[0].ID] = "running"
				return nil, nil
			}
		}
		return base(ctx, command, args...)
	}
	instanceBackupStorageCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		return f.backend.command(ctx, command, args...)
	}
	return x
}

// Resolve fixture templates without relying on the shell's working directory.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	p, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(p, "templates/systemd/minecraft-backup.timer.in")); err == nil {
			return p
		}
		next := filepath.Dir(p)
		if next == p {
			t.Fatal("repository root unavailable")
		}
		p = next
	}
}
func TestInstanceBackupDailySpacing(t *testing.T) {
	for _, c := range []struct {
		a, b string
		ok   bool
	}{{"04:00", "04:30", true}, {"04:01", "04:30", false}, {"23:50", "00:10", false}, {"23:30", "00:00", true}, {"00:00", "23:31", false}, {"12:00", "00:00", true}} {
		if backupTimesSeparated(c.a, c.b) != c.ok {
			t.Fatal(c)
		}
	}
	for _, raw := range []string{"[Timer]\nOnCalendar=daily", "[Timer]\nOnCalendar=*-*-* 04:30:00\nOnBootSec=1h", "[Timer]\nOnCalendar=*-*-* 04:30:00\nOnCalendar=", "# /a\n[Timer]\nOnCalendar=*-*-* 04:30:00\n# /b"} {
		if _, err := legacyBackupDaily(raw); err == nil {
			t.Fatal("accepted ambiguous legacy timer", raw)
		}
	}
	if got, err := legacyBackupDaily("[Timer]\nOnCalendar=*-*-* 23:59:00\nPersistent=true"); err != nil || got != "23:59" {
		t.Fatal(got, err)
	}
	for _, v := range []instanceBackupSettings{{Time: "24:00", Retention: 1}, {Time: "1:00", Retention: 1}, {Time: "04:00", Retention: 0}, {Time: "04:00", Retention: 366}} {
		if validInstanceBackupSettings(v) {
			t.Fatal(v)
		}
	}
}
func TestInstanceBackupLifecycleAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		running                  bool
		stage                    string
		playersFail, restartFail bool
	}{{"stopped", false, "", false, false}, {"running", true, "", false, false}, {"mount-space", true, "preflight", false, false}, {"integrity-publication", true, "create", false, false}, {"retention", true, "retain", false, false}, {"RCON", true, "", true, false}, {"restart", true, "", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInstanceBackupFixture(t)
			id, other := f.configs[0].ID, f.configs[1].ID
			f.failStage = tc.stage
			f.failPlayers = tc.playersFail
			f.restartFailure = tc.restartFail
			if tc.running {
				f.states[id] = "active"
				f.containers[id] = "running"
			}
			otherState := f.states[other]
			err := f.server.runInstanceBackup(context.Background(), id)
			failed := (tc.stage != "" && tc.stage != "retain") || tc.playersFail || tc.restartFail
			if (err != nil) != failed {
				t.Fatal(err)
			}
			status, e := f.backend.backupStatus(id)
			if e != nil {
				t.Fatal(e)
			}
			if (status.State == "failed") != failed {
				t.Fatal(status)
			}
			if tc.stage == "retain" && status.Message == "" {
				t.Fatal("retention warning hidden")
			}
			if status.RestartRequired != tc.restartFail {
				t.Fatal("restart intent", status)
			}
			if f.states[other] != otherState {
				t.Fatal("other instance touched")
			}
			for _, call := range f.storage {
				if !strings.HasSuffix(call, ":"+id) {
					t.Fatal(call)
				}
			}
			if tc.stage == "preflight" || tc.playersFail {
				for _, call := range f.storage {
					if strings.HasPrefix(call, "create:") {
						t.Fatal("archive before preflight/shutdown", call)
					}
				}
			}
			if !tc.running && f.states[id] != "inactive" {
				t.Fatal("stopped instance restarted")
			}
			if tc.running && !tc.restartFail && f.states[id] != "active" {
				t.Fatal("running intent lost")
			}
			raw, _ := os.ReadFile(f.backend.backupPath(id, "status"))
			if strings.Contains(string(raw), "private helper") || strings.Contains(string(raw), f.configs[0].RCONPassword) {
				t.Fatal("secret in status")
			}
		})
	}
}
func TestInstanceBackupInterruptedIntentAndMaintenanceConflict(t *testing.T) {
	f := newInstanceBackupFixture(t)
	id := f.configs[0].ID
	old := instanceBackupStatus{State: "running", Stage: "archive", RestartRequired: true, OriginallyRunning: true}
	if err := f.backend.writeJSON(f.backend.backupPath(id, "status"), old, false); err != nil {
		t.Fatal(err)
	}
	if err := f.server.runInstanceBackup(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if f.states[id] != "active" {
		t.Fatal("interrupted running intent lost")
	}
	release, err := f.server.lockInstanceBackup()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = f.server.runInstanceBackup(context.Background(), f.configs[1].ID); !errors.Is(err, errInstanceBackupDeferred) {
		t.Fatal("active maintenance did not defer backup", err)
	}
}
func TestInstanceBackupSchedulesAndRollback(t *testing.T) {
	f := newInstanceBackupFixture(t)
	a, b := f.configs[0].ID, f.configs[1].ID
	ctx := context.Background()
	v := instanceBackupSettings{Enabled: true, Time: "23:50", Retention: 3}
	if err := f.backend.configureBackups(ctx, a, v); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.configureBackups(ctx, b, instanceBackupSettings{Enabled: true, Time: "00:10", Retention: 9}); err == nil {
		t.Fatal("midnight collision")
	}
	if err := f.backend.configureBackups(ctx, b, instanceBackupSettings{Enabled: true, Time: "04:01", Retention: 9}); err == nil {
		t.Fatal("legacy collision")
	}
	timer, _ := os.ReadFile(filepath.Join(instanceBackupUnits, instanceBackupUnit(a, "timer")))
	if !strings.Contains(string(timer), "Persistent=true") || !strings.Contains(string(timer), "OnCalendar=*-*-* 23:50:00") {
		t.Fatal(string(timer))
	}
	f.failApply = true
	next := v
	next.Time = "22:00"
	if err := f.backend.configureBackups(ctx, a, next); err == nil {
		t.Fatal("failed apply succeeded")
	}
	got, err := f.backend.backupSettings(a)
	if err != nil || got != v {
		t.Fatal("rollback settings", got, err)
	}
	restored, _ := os.ReadFile(filepath.Join(instanceBackupUnits, instanceBackupUnit(a, "timer")))
	if string(restored) != string(timer) {
		t.Fatal("rollback timer")
	}
	if _, err := os.Lstat(f.backend.backupPath(a, "schedule-pending")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rollback journal not cleared")
	}
	v.Enabled = false
	if err := f.backend.configureBackups(ctx, a, v); err != nil {
		t.Fatal(err)
	}
	if f.timerEnabled[instanceBackupUnit(a, "timer")] || f.timerActive[instanceBackupUnit(a, "timer")] {
		t.Fatal("disable failed")
	}
	gotB, _ := f.backend.backupSettings(b)
	if gotB.Retention != 7 {
		t.Fatal("other retention changed")
	}
}
func TestInstanceBackupAPIReadOnlyAndAuthorization(t *testing.T) {
	f := newInstanceBackupFixture(t)
	id := f.configs[0].ID
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		s := roleServerForTest(role)
		s.operations = f.server.operations
		mux := http.NewServeMux()
		registerInstanceBackupRoutes(mux, s)
		for _, suffix := range []string{"", "/settings", "/status"} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest("GET", "/v1/minecraft/instances/"+id+"/backups"+suffix, ""))
			if rr.Code != 200 {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "rcon") || strings.Contains(rr.Body.String(), f.configs[0].RCONPassword) {
				t.Fatal("secret disclosure")
			}
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/backups/run", `{}`))
		if rr.Code != 403 {
			t.Fatal("web session ran worker", rr.Code)
		}
		if role != roleAdministrator {
			rr = httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest("PUT", "/v1/minecraft/instances/"+id+"/backups/settings", `{"enabled":true,"time":"12:00","retention":3}`))
			if rr.Code != 403 {
				t.Fatal("nonadmin settings", rr.Code)
			}
		}
	}
	if _, err := os.Lstat(f.backend.backupPath(id, "settings")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created settings")
	}
	for _, call := range f.scheduleCalls {
		if strings.Contains(call, "start") || strings.Contains(call, "enable") {
			t.Fatal("read changed timer", call)
		}
	}
	mux := http.NewServeMux()
	registerInstanceBackupRoutes(mux, f.server)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/minecraft/instances/"+id+"/backups", nil))
	if rr.Code != 401 {
		t.Fatal("unauthenticated read", rr.Code)
	}
}
func TestInstanceOperatorQuotaSharedAcrossInstances(t *testing.T) {
	f := newInstanceBackupFixture(t)
	store, _ := openTestWebUIStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	user, err := store.createWebUser("InstanceBackupOperator", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	s := operatorServerForStore(store, user)
	s.operations = f.server.operations
	mux := http.NewServeMux()
	registerInstanceBackupRoutes(mux, s)
	post := func(id string) int {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/backups", `{}`))
		return rr.Code
	}
	if code := post(f.configs[0].ID); code != 202 {
		t.Fatal(code)
	}
	if code := post(f.configs[1].ID); code != 429 {
		t.Fatal("instance switch bypassed cooldown", code)
	}
	usedFirst, _, err := store.operatorBackupUsage(user.ID)
	if err != nil || usedFirst != 1 {
		t.Fatal(usedFirst, err)
	}
	now = now.Add(16 * time.Minute)
	if code := post(f.configs[1].ID); code != 202 {
		t.Fatal(code)
	}
	now = now.Add(16 * time.Minute)
	if code := post(f.configs[1].ID); code != 409 {
		t.Fatal("instance quotas separated", code)
	}
	for _, id := range []string{f.configs[0].ID, f.configs[1].ID} {
		var audits int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='instance_backup' AND target=? AND success=1`, id).Scan(&audits); err != nil || audits != 1 {
			t.Fatal(id, audits, err)
		}
	}
	var notifications int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind='operator_backup_limit' AND user_id=? AND resolved_at IS NULL`, user.ID).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatal(notifications, err)
	}
	starts := []string{}
	for _, call := range f.scheduleCalls {
		if strings.HasPrefix(call, "--no-block start ") {
			starts = append(starts, call)
		}
	}
	wantStarts := []string{"--no-block start minecraft-backup-" + f.configs[0].ID + ".service", "--no-block start minecraft-backup-" + f.configs[1].ID + ".service"}
	if !reflect.DeepEqual(starts, wantStarts) {
		t.Fatal(starts)
	}
	used, _, err := store.operatorBackupUsage(user.ID)
	if err != nil || used != 2 {
		t.Fatal(used, err)
	}
}

func TestInstanceBackupNowSelectedTargetAndAccounting(t *testing.T) {
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		for _, fail := range []bool{false, true} {
			t.Run(string(role)+map[bool]string{false: "/accepted", true: "/rejected"}[fail], func(t *testing.T) {
				f := newInstanceBackupFixture(t)
				store, _ := openTestWebUIStore(t)
				s := roleServerForTest(role)
				s.store, s.operations = store, f.server.operations
				var user webUser
				if role == roleOperator {
					var err error
					user, err = store.createWebUser("NativeBackupOperator", webUserRoleOperator, "operator-password")
					if err != nil {
						t.Fatal(err)
					}
					s = operatorServerForStore(store, user)
					s.operations = f.server.operations
				}
				id := f.configs[1].ID
				starts := 0
				base := f.backend.command
				f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
					if command == "systemctl" && len(args) > 1 && args[0] == "--no-block" && args[1] == "start" {
						if !reflect.DeepEqual(args, []string{"--no-block", "start", "minecraft-backup-" + id + ".service"}) {
							t.Fatal("backup escaped selected immutable target", args)
						}
						starts++
						if fail {
							return nil, errors.New("systemd rejected request")
						}
					}
					return base(ctx, command, args...)
				}
				mux := http.NewServeMux()
				registerInstanceBackupRoutes(mux, s)
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/backups", `{}`))
				wantCode, wantStarts, wantAudits := 202, 1, 1
				if fail {
					wantCode, wantAudits = 503, 0
				}
				if role == roleViewer {
					wantCode, wantStarts, wantAudits = 403, 0, 0
				}
				if rr.Code != wantCode || starts != wantStarts {
					t.Fatal(rr.Code, rr.Body.String(), starts)
				}
				var audits, badTargets int
				if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='instance_backup' AND target=? AND success=1`, id).Scan(&audits); err != nil || audits != wantAudits {
					t.Fatal(audits, err)
				}
				if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='manual_backup' OR (action='instance_backup' AND target<>?) OR target='minecraft-backup.service'`, id).Scan(&badTargets); err != nil || badTargets != 0 {
					t.Fatal(badTargets, err)
				}
				if role == roleOperator {
					used, last, err := store.operatorBackupUsage(user.ID)
					if err != nil || used != wantAudits || (last != nil) != (wantAudits == 1) {
						t.Fatal(used, last, err)
					}
				}
			})
		}
	}
}

func TestInstanceBackupOccupiedShutdownAndTimeout(t *testing.T) {
	for _, scenario := range []string{"occupied-success", "countdown-timeout", "stop-confirmation-timeout"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstanceBackupFixture(t)
			id, other := f.configs[0].ID, f.configs[1].ID
			otherState, otherContainer := f.states[other], f.containers[other]
			f.states[id] = "active"
			f.containers[id] = "running"
			f.players = "There are 1 of a max of 20 players online: Alex"
			base := f.backend.command
			wantWarnings := []string{
				"say Server shutting down in 60 seconds",
				"say Server shutting down in 30 seconds",
				"say Server shutting down in 15 seconds",
				"say Server shutting down in 10 seconds",
				"say Server shutting down in 5 seconds",
				"say Server shutting down in 4 seconds",
				"say Server shutting down in 3 seconds",
				"say Server shutting down in 2 seconds",
				"say Server shutting down in 1 second",
			}
			var events []string
			elapsed := time.Duration(0)
			signaled, stopRequested, stoppedObserved, stopWaitTimedOut := false, false, false, false
			f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				for _, arg := range args {
					if strings.Contains(arg, other) {
						t.Fatal("command escaped selected instance", command, args)
					}
				}
				if command == "podman" && len(args) == 4 && strings.HasPrefix(args[3], "say ") {
					if args[0] != "exec" || args[1] != "minecraft-"+id || args[2] != "rcon-cli" || signaled || stopRequested {
						t.Fatal("invalid warning target or order", args)
					}
					events = append(events, args[3])
					return nil, nil
				}
				if command == "podman" && len(args) > 0 && args[0] == "kill" {
					if !reflect.DeepEqual(args, []string{"kill", "--signal", "SIGUSR1", "minecraft-" + id}) || elapsed != time.Minute || !reflect.DeepEqual(events, wantWarnings) {
						t.Fatal("graceful signal before complete warning sequence or wrong target", args, elapsed, events)
					}
					signaled = true
					events = append(events, "SIGUSR1")
					return nil, nil
				}
				if command == "systemctl" && (args[0] == "stop" || (len(args) > 1 && args[1] == "stop")) {
					if !reflect.DeepEqual(args, []string{"--no-block", "stop", "--", "minecraft-" + id + ".service"}) || !signaled || stopRequested {
						t.Fatal("invalid service stop target or order", args)
					}
					stopRequested = true
					events = append(events, "stop")
					if scenario == "stop-confirmation-timeout" {
						f.states[id], f.containers[id] = "deactivating", "running"
					} else {
						f.states[id], f.containers[id] = "inactive", "exited"
					}
					return nil, nil
				}
				if command == "nsenter" && len(args) == 6 && args[2] == instanceBackupHelper && args[3] == "create" && !stoppedObserved {
					t.Fatal("archive before stopped-state confirmation")
				}
				out, err := base(ctx, command, args...)
				if err == nil && stopRequested && command == "podman" && reflect.DeepEqual(args, []string{"inspect", "--type", "container", "--format", "{{.State.Status}} {{.State.ExitCode}}", "minecraft-" + id}) && f.states[id] == "inactive" && f.containers[id] == "exited" {
					stoppedObserved = true
				}
				return out, err
			}
			f.backend.pause = func(_ context.Context, d time.Duration) error {
				if stopRequested {
					if scenario != "stop-confirmation-timeout" || d != time.Second || f.states[id] != "deactivating" || f.containers[id] != "running" {
						t.Fatal("unexpected stopped-state observation wait")
					}
					stopWaitTimedOut = true
					return context.DeadlineExceeded
				}
				if f.states[id] != "active" || f.containers[id] != "running" || signaled {
					t.Fatal("server interrupted during countdown")
				}
				elapsed += d
				if scenario == "countdown-timeout" && elapsed >= time.Minute {
					return context.DeadlineExceeded
				}
				return nil
			}
			err := f.server.runInstanceBackup(context.Background(), id)
			failed := scenario != "occupied-success"
			if (err != nil) != failed || elapsed != time.Minute {
				t.Fatal(err, elapsed)
			}
			wantEvents := append([]string(nil), wantWarnings...)
			if scenario != "countdown-timeout" {
				wantEvents = append(wantEvents, "SIGUSR1", "stop")
			} else if signaled || stopRequested {
				t.Fatal("countdown failure interrupted server")
			}
			if !reflect.DeepEqual(events, wantEvents) {
				t.Fatal("shutdown warning/signal/stop order", events)
			}
			status, statusErr := f.backend.backupStatus(id)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if !status.OriginallyRunning || status.RestartRequired != (scenario == "stop-confirmation-timeout") || status.ArchivePublished != !failed {
				t.Fatal(status)
			}
			created := false
			for _, call := range f.storage {
				if !strings.HasSuffix(call, ":"+id) {
					t.Fatal("storage escaped selected instance", call)
				}
				if strings.HasPrefix(call, "create:") {
					created = true
				}
			}
			if created != !failed {
				t.Fatal("unexpected archive creation", f.storage)
			}
			if failed {
				if status.State != "failed" || status.Stage != "shutdown" {
					t.Fatal(status)
				}
			} else if status.State != "succeeded" || status.Stage != "complete" || !stoppedObserved {
				t.Fatal(status, stoppedObserved)
			}
			if scenario == "stop-confirmation-timeout" {
				if !stopWaitTimedOut || stoppedObserved || f.states[id] != "deactivating" || f.containers[id] != "running" {
					t.Fatal("post-interruption timeout did not retain unverified shutdown state")
				}
			} else if f.states[id] != "active" || f.containers[id] != "running" {
				t.Fatal("original running state lost")
			}
			if f.states[other] != otherState || f.containers[other] != otherContainer {
				t.Fatal("other instance touched")
			}
		})
	}
}
func TestInstanceBackupWorkerCollisionAndStrictRequests(t *testing.T) {
	f := newInstanceBackupFixture(t)
	id := f.configs[0].ID
	instanceBackupWorkerMu.Lock()
	err := f.server.runInstanceBackup(context.Background(), id)
	instanceBackupWorkerMu.Unlock()
	if !errors.Is(err, errInstanceBackupDeferred) || len(f.storage) != 0 {
		t.Fatal("concurrent worker did not defer before storage", err, f.storage)
	}
	mux := http.NewServeMux()
	registerInstanceBackupRoutes(mux, f.server)
	for _, tc := range []struct{ method, suffix, body string }{{"PUT", "/settings", `{"enabled":false,"time":"04:00","retention":2,"path":"/tmp/foreign"}`}, {"POST", "", `{"path":"/tmp/foreign"}`}} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(tc.method, "/v1/minecraft/instances/"+id+"/backups"+tc.suffix, tc.body))
		if rr.Code != http.StatusBadRequest {
			t.Fatal(rr.Code, rr.Body.String())
		}
	}
}

func TestInstanceBackupRunDeferredBeforeMutation(t *testing.T) {
	for _, contention := range []string{"maintenance", "plugin-maintenance", "worker"} {
		for _, previous := range []string{"absent", "running", "succeeded", "failed"} {
			t.Run(contention+"/"+previous, func(t *testing.T) {
				f := newInstanceBackupFixture(t)
				id := f.configs[0].ID
				f.states[id], f.containers[id] = "active", "running"
				path := f.backend.backupPath(id, "status")
				var before []byte
				if previous != "absent" {
					status := instanceBackupStatus{State: previous, LastAttempt: "2026-01-01T00:00:00Z", LastSuccess: "2025-12-31T00:00:00Z", Stage: "archive", RestartRequired: true, OriginallyRunning: true}
					if err := f.backend.writeJSON(path, status, false); err != nil {
						t.Fatal(err)
					}
					var err error
					before, err = os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
				}
				switch contention {
				case "maintenance":
					release, err := f.server.lockInstanceBackup()
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := release(); err != nil {
							t.Error(err)
						}
					}()
				case "plugin-maintenance":
					lock, err := acquirePluginRuntimeLock()
					if err != nil {
						t.Fatal(err)
					}
					defer lock.Close()
				case "worker":
					instanceBackupWorkerMu.Lock()
					defer instanceBackupWorkerMu.Unlock()
				}
				// A deferred worker must not even observe or command Minecraft.
				commands := 0
				base := f.backend.command
				f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
					commands++
					return base(ctx, command, args...)
				}
				mux := http.NewServeMux()
				registerInstanceBackupRoutes(mux, f.server)
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, localRootMinecraftRequest("POST", "/v1/minecraft/instances/"+id+"/backups/run", `{}`))
				if rr.Code != http.StatusOK {
					t.Fatal(rr.Code, rr.Body.String())
				}
				var response map[string]any
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"ok": true, "id": id, "deferred": true, "reason": "appliance_maintenance"}
				if !reflect.DeepEqual(response, want) {
					t.Fatal(response)
				}
				if commands != 0 || len(f.storage) != 0 || len(f.scheduleCalls) != 0 {
					t.Fatal("deferred worker performed runtime, storage, or schedule work", commands, f.storage, f.scheduleCalls)
				}
				after, err := os.ReadFile(path)
				if previous == "absent" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatal("deferred worker recorded an attempt", err)
					}
				} else if err != nil || string(after) != string(before) {
					t.Fatal("deferred worker changed previous status", err, string(after))
				}
			})
		}
	}
}

func TestInstanceBackupRunExecutionResult(t *testing.T) {
	for _, stage := range []string{"", "preflight", "create"} {
		t.Run("stage="+stage, func(t *testing.T) {
			f := newInstanceBackupFixture(t)
			id := f.configs[0].ID
			f.failStage = stage
			mux := http.NewServeMux()
			registerInstanceBackupRoutes(mux, f.server)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, localRootMinecraftRequest("POST", "/v1/minecraft/instances/"+id+"/backups/run", `{}`))
			wantCode, wantState := http.StatusOK, "succeeded"
			if stage != "" {
				wantCode, wantState = http.StatusConflict, "failed"
			}
			if rr.Code != wantCode {
				t.Fatal(rr.Code, rr.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if _, exists := response["deferred"]; exists {
				t.Fatal("begun backup classified as deferred", response)
			}
			if stage == "" && !reflect.DeepEqual(response, map[string]any{"ok": true, "id": id}) {
				t.Fatal("normal success response changed", response)
			}
			if stage != "" && (response["error"] == nil || response["ok"] == true) {
				t.Fatal("backup failure reported as success", response)
			}
			status, err := f.backend.backupStatus(id)
			if err != nil || status.State != wantState || status.LastAttempt == "" || len(f.storage) == 0 {
				t.Fatal(status, err, f.storage)
			}
		})
	}
}

func TestInstanceBackupNowMaintenanceConflictUnchanged(t *testing.T) {
	f := newInstanceBackupFixture(t)
	id := f.configs[0].ID
	release, err := f.server.lockInstanceBackup()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	mux := http.NewServeMux()
	registerInstanceBackupRoutes(mux, f.server)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/backups", `{}`))
	if rr.Code != http.StatusConflict || strings.Contains(rr.Body.String(), `"deferred"`) {
		t.Fatal("manual maintenance probe behavior changed", rr.Code, rr.Body.String())
	}
	if len(f.storage) != 0 || len(f.scheduleCalls) != 0 {
		t.Fatal("manual backup queued during maintenance", f.storage, f.scheduleCalls)
	}
}

func TestInstanceBackupPendingScheduleRecovery(t *testing.T) {
	f := newInstanceBackupFixture(t)
	id := f.configs[0].ID
	ctx := context.Background()
	old := instanceBackupSettings{Enabled: false, Time: "12:00", Retention: 4}
	if err := f.backend.configureBackups(ctx, id, old); err != nil {
		t.Fatal(err)
	}
	service, _ := f.backend.readPrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "service")))
	timer, _ := f.backend.readPrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "timer")))
	evidence := instanceBackupApply{Previous: old, Service: service, Timer: timer, HadService: true, HadTimer: true}
	if err := f.backend.writeJSON(f.backend.backupPath(id, "schedule-pending"), evidence, false); err != nil {
		t.Fatal(err)
	}
	// Model a process exit between settings publication and timer application.
	changed := old
	changed.Retention = 9
	if err := f.backend.writeJSON(f.backend.backupPath(id, "settings"), changed, true); err != nil {
		t.Fatal(err)
	}
	if err := f.server.runInstanceBackup(ctx, id); err == nil {
		t.Fatal("interrupted apply allowed backup")
	}
	if err := f.backend.configureBackups(ctx, id, old); err != nil {
		t.Fatal(err)
	}
	got, _ := f.backend.backupSettings(id)
	if got != old {
		t.Fatal(got)
	}
}
