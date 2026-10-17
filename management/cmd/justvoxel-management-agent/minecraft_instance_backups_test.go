package main

import (
	"context"
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
		if command == "nsenter" && len(args) == 5 && args[2] == instanceBackupHelper {
			if args[4] != f.configs[0].ID && args[4] != f.configs[1].ID {
				t.Fatal("foreign storage", args)
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
	if err = f.server.runInstanceBackup(context.Background(), f.configs[1].ID); err == nil {
		t.Fatal("active maintenance accepted backup")
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
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "occupied", true: "timeout"}[timeout], func(t *testing.T) {
			f := newInstanceBackupFixture(t)
			id := f.configs[0].ID
			f.states[id] = "active"
			f.containers[id] = "running"
			f.players = "There are 1 of a max of 20 players online: Alex"
			base := f.backend.command
			warnings := 0
			elapsed := time.Duration(0)
			f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "podman" && len(args) == 4 && strings.HasPrefix(args[3], "say ") {
					if args[1] != "minecraft-"+id {
						t.Fatal("warning escaped target")
					}
					warnings++
					return nil, nil
				}
				if command == "systemctl" && reflect.DeepEqual(args, []string{"--no-block", "stop", "--", "minecraft-" + id + ".service"}) {
					f.states[id] = "deactivating"
					return nil, nil
				}
				return base(ctx, command, args...)
			}
			f.backend.pause = func(_ context.Context, d time.Duration) error {
				elapsed += d
				if timeout && elapsed >= 60*time.Second {
					return context.DeadlineExceeded
				}
				if elapsed >= 60*time.Second {
					f.states[id] = "inactive"
					f.containers[id] = "exited"
				}
				return nil
			}
			err := f.server.runInstanceBackup(context.Background(), id)
			if (err != nil) != timeout || warnings == 0 {
				t.Fatal(err, warnings)
			}
			status, _ := f.backend.backupStatus(id)
			if timeout {
				if !status.RestartRequired || status.ArchivePublished {
					t.Fatal(status)
				}
				for _, call := range f.storage {
					if strings.HasPrefix(call, "create:") {
						t.Fatal("archive during unverified shutdown")
					}
				}
			} else if status.State != "succeeded" || f.states[id] != "active" {
				t.Fatal(status)
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
	if err == nil || len(f.storage) != 0 {
		t.Fatal("concurrent worker accepted")
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
