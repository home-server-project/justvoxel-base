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

type validationTestExitError struct {
	code int
}

func (e validationTestExitError) Error() string {
	return "validation helper exited"
}

func (e validationTestExitError) ExitCode() int {
	return e.code
}

func TestLocalRootCanValidateWithoutBearerSession(t *testing.T) {
	validationFixture(t, 0)
	s := surfaceTestServer(t, roleViewer)
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()

	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		return []byte("OK: runtime files are healthy\n\nJustVoxel validation passed.\n"), nil
	}

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/validation", 0)
	rr := httptest.NewRecorder()
	s.adminValidation(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root validation returned %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"ok":true`, `"exit_code":0`, "OK: runtime files are healthy", "JustVoxel validation passed."} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("local-root validation response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestValidationFailurePreservesOutputAndExitCode(t *testing.T) {
	validationFixture(t, 0)
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()

	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		return []byte("FAIL: one or more systemd units are failed\n\nJustVoxel validation failed with 1 problem(s).\n"), validationTestExitError{code: 1}
	}

	rr := httptest.NewRecorder()
	s.adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("validation failure returned HTTP %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"ok":false`, `"exit_code":1`, "FAIL: one or more systemd units are failed", "JustVoxel validation failed with 1 problem(s)."} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("validation failure response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestValidationRejectsNonAdministratorBeforeHelper(t *testing.T) {
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()
	called := false
	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		called = true
		return nil, nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s validation status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("validation helper ran for non-Administrator")
	}
}

func TestValidationUnexpectedHelperFailureIsSanitized(t *testing.T) {
	validationFixture(t, 0)
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()

	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		return []byte("sensitive helper output"), validationTestExitError{code: 2}
	}

	rr := httptest.NewRecorder()
	s.adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected validation helper failure status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "sensitive helper output") || !strings.Contains(rr.Body.String(), "JustVoxel validation is unavailable") {
		t.Fatalf("validation helper failure leaked details or missed bounded error: %s", rr.Body.String())
	}
}

// Fixtures publish real private records/files; every external command is mocked.
// The validator is exercised through its Administrator HTTP entrypoint.
func validationFixture(t *testing.T, count int) *instanceActivationFixture {
	t.Helper()
	f := newInstanceActivationFixture(t)
	old, oldHost, oldBackup := instanceRuntimes, runAdminValidationHelper, instanceBackupStorageCommand
	instanceRuntimes = f.backend
	runAdminValidationHelper = func(ctx context.Context) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 9*time.Minute || time.Until(deadline) > 10*time.Minute {
			t.Fatal("validation deadline must support several live instances and remain bounded")
		}
		return []byte("PASS: host fixture\n"), nil
	}
	t.Cleanup(func() {
		instanceRuntimes = old
		runAdminValidationHelper = oldHost
		instanceBackupStorageCommand = oldBackup
	})
	for i, c := range f.configs {
		if i >= count {
			dir, _, _ := f.backend.paths(c.ID)
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(f.backend.store.directory, c.ID+".json")); err != nil {
				t.Fatal(err)
			}
		}
	}
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	var files []minecraftInstanceRuntimeFiles
	if len(records) > 0 {
		files, err = f.backend.render(f.snapshot, records)
		if err != nil {
			t.Fatal(err)
		}
	}
	filesByID := make(map[string]minecraftInstanceRuntimeFiles)
	for _, file := range files {
		filesByID[file.ID] = file
	}
	for _, record := range records {
		id := record.Runtime.ID
		file := filesByID[id]
		dir, env, quadlet := f.backend.paths(id)
		if err := f.backend.writePrivate(env, file.Environment, false); err != nil {
			t.Fatal(err)
		}
		if err := f.backend.writePrivate(quadlet, file.Quadlet, false); err != nil {
			t.Fatal(err)
		}
		record.EnvironmentSHA256 = instanceHash(file.Environment)
		record.QuadletSHA256 = instanceHash(file.Quadlet)
		record.Firewall = []instanceFirewallRule{{Zone: "public", Port: record.Runtime.JavaPort}, {Zone: "public", Port: record.Runtime.JavaPort, Permanent: true}}
		for _, rule := range record.Firewall {
			f.firewall[rule] = true
		}
		if err := f.backend.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(record.Storage.DataPath, 0750); err != nil {
			t.Fatal(err)
		}
		f.states[id], f.containers[id] = "active", "running"
	}
	original := f.backend.command
	f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
		switch cmd {
		case "/usr/libexec/justvoxel/mjust/validate-instance-runtime-commit":
			if len(args) != 1 || !instanceIDPattern.MatchString(args[0]) {
				t.Fatalf("unsafe commitment target: %v", args)
			}
		case "/usr/libexec/justvoxel/mjust/verify-minecraft-stack":
			if len(args) != 3 || args[0] != "--instance" {
				t.Fatalf("unsafe stack target: %v", args)
			}
			record, err := f.backend.readRecord(args[1])
			if err != nil || record.Runtime.Version != args[2] {
				t.Fatalf("wrong version/ID: %v", args)
			}
		case "ls":
			if len(args) != 3 || args[0] != "-Zd" || args[1] != "--" {
				t.Fatalf("unsafe context read: %v", args)
			}
			found := false
			for _, record := range records {
				if args[2] == record.Storage.DataPath {
					found = true
				}
			}
			if !found {
				t.Fatalf("context read used foreign path: %v", args)
			}
			f.calls = append(f.calls, cmd+" "+strings.Join(args, " "))
			return []byte("system_u:object_r:container_file_t:s0:c1,c2 data"), nil
		case "systemctl":
			if len(args) == 2 && (args[0] == "is-enabled" || args[0] == "is-active") {
				found := false
				for _, record := range records {
					if args[1] == instanceBackupUnit(record.Runtime.ID, "timer") {
						found = true
					}
				}
				if !found {
					t.Fatalf("foreign backup timer: %v", args)
				}
				f.calls = append(f.calls, cmd+" "+strings.Join(args, " "))
				if args[0] == "is-enabled" {
					return []byte("disabled"), instanceTestExit{}
				}
				return []byte("inactive"), instanceTestExit{}
			}
			return original(ctx, cmd, args...)
		default:
			return original(ctx, cmd, args...)
		}
		f.calls = append(f.calls, cmd+" "+strings.Join(args, " "))
		return []byte("private helper material must never be returned"), nil
	}
	instanceBackupStorageCommand = func(_ context.Context, cmd string, args ...string) ([]byte, error) {
		if cmd != "nsenter" || len(args) != 5 || !reflect.DeepEqual(args[:4], []string{"--mount=/proc/1/ns/mnt", "--", instanceBackupHelper, "inventory"}) {
			t.Fatalf("backup validation must use read-only inventory: %s %v", cmd, args)
		}
		if _, err := f.backend.readRecord(args[4]); err != nil {
			t.Fatal("foreign backup ID")
		}
		f.calls = append(f.calls, "backup inventory "+args[4])
		return []byte("[]"), nil
	}
	f.calls = nil
	return f
}

func validationRequest(t *testing.T, wantOK bool) adminValidationResponse {
	t.Helper()
	rr := httptest.NewRecorder()
	surfaceTestServer(t, roleAdministrator).adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
	var result adminValidationResponse
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &result) != nil {
		t.Fatalf("validation expected HTTP 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if result.OK != wantOK || result.ExitCode != map[bool]int{true: 0, false: 1}[wantOK] {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Contains(result.Output, "private helper") || strings.Contains(result.Output, strings.Repeat("a", 48)) || strings.Contains(result.Output, "minecraft.service") {
		t.Fatal("private/fixed-runtime output")
	}
	return result
}

func TestApplianceValidationZeroOneManyAndStoppedSibling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		count   int
		stopped bool
	}{{"zero", 0, false}, {"zero without legacy", 0, false}, {"one running", 1, false}, {"two running", 2, false}, {"running and stopped", 2, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := validationFixture(t, tc.count)
			if tc.name == "zero without legacy" {
				if err := os.Remove(filepath.Join(f.backend.store.directory, "minecraft.json")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.stopped {
				f.states["jv-d4e5f6"], f.containers["jv-d4e5f6"] = "inactive", "exited"
			}
			result := validationRequest(t, true)
			for i, c := range f.configs {
				calls := strings.Join(f.calls, "\n")
				if i >= tc.count {
					if strings.Contains(calls, c.ID) {
						t.Fatal("absent sibling targeted")
					}
					continue
				}
				for _, want := range []string{"validate-instance-runtime-commit " + c.ID, "validate " + c.ID + " ", "backup inventory " + c.ID, "minecraft-backup-" + c.ID + ".timer", "minecraft-" + c.ID + ".service", "name=^minecraft-" + c.ID + "$"} {
					if !strings.Contains(calls, want) {
						t.Fatalf("missing exact instance check %q: %s", want, calls)
					}
				}
				live := strings.Contains(calls, "verify-minecraft-stack --instance "+c.ID+" "+c.Version)
				if live == (tc.stopped && i == 1) {
					t.Fatalf("wrong live verification for %s", c.ID)
				}
				if !strings.Contains(result.Output, c.ID) {
					t.Fatal("report missing ID")
				}
			}
			for _, call := range f.calls {
				if strings.Contains(call, "minecraft.service") || strings.Contains(call, "exec minecraft ") || strings.Contains(call, "minecraft-backup.timer") || strings.Contains(call, "--first-run") {
					t.Fatalf("legacy target: %s", call)
				}
			}
		})
	}
}

func TestApplianceValidationProblemsContinueWithSafeSibling(t *testing.T) {
	for _, problem := range []string{"missing runtime", "hash", "permission", "pending", "commit gate", "storage", "context", "firewall", "failed service", "transitional", "live stack", "backup timer", "backup failed", "backup interrupted", "backup restart", "backup destination", "orphan Quadlet", "unregistered runtime", "duplicate registry", "mismatched registry"} {
		t.Run(problem, func(t *testing.T) {
			f := validationFixture(t, 2)
			id := "jv-a1b2c3"
			dir, env, _ := f.backend.paths(id)
			must := func(err error) {
				if err != nil {
					t.Fatal(err)
				}
			}
			command := f.backend.command
			switch problem {
			case "missing runtime":
				must(os.Remove(filepath.Join(dir, "config.json")))
			case "hash":
				must(os.WriteFile(env, []byte("changed private environment"), 0600))
			case "permission":
				must(os.Chmod(env, 0644))
			case "pending":
				must(f.backend.writeJSON(filepath.Join(dir, "pending.json"), map[string]string{"state": "recovery"}, false))
			case "failed service":
				f.states[id], f.containers[id] = "failed", "exited"
			case "transitional":
				f.states[id] = "activating"
			case "firewall":
				for rule := range f.firewall {
					if rule.Port == f.configs[0].JavaPort {
						delete(f.firewall, rule)
					}
				}
			case "backup failed", "backup interrupted", "backup restart":
				status := instanceBackupStatus{State: strings.TrimPrefix(problem, "backup ")}
				if problem == "backup restart" {
					status.State = "completed"
					status.RestartRequired = true
				}
				must(f.backend.writeJSON(f.backend.backupPath(id, "status"), status, false))
			case "backup timer":
				must(f.backend.writeJSON(f.backend.backupPath(id, "settings"), instanceBackupSettings{Enabled: true, Time: "04:00", Retention: 7}, false))
			case "orphan Quadlet":
				must(os.WriteFile(filepath.Join(f.backend.quadlets, "minecraft-jv-orphan1.container"), []byte("foreign"), 0600))
			case "unregistered runtime":
				must(os.Remove(filepath.Join(f.backend.store.directory, id+".json")))
			case "duplicate registry":
				must(os.WriteFile(filepath.Join(f.backend.store.directory, "duplicate.json"), []byte(`{"id":"jv-a1b2c3"}`), 0600))
			case "mismatched registry":
				must(os.WriteFile(filepath.Join(f.backend.store.directory, id+".json"), []byte(`{"id":"jv-foreign1"}`), 0600))
			case "backup destination":
				original := instanceBackupStorageCommand
				instanceBackupStorageCommand = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
					if args[len(args)-1] == id {
						return nil, errors.New("secret destination detail")
					}
					return original(ctx, cmd, args...)
				}
			}
			f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if (problem == "commit gate" && strings.HasSuffix(cmd, "/validate-instance-runtime-commit") && joined == id) || (problem == "live stack" && strings.HasSuffix(cmd, "/verify-minecraft-stack") && strings.Contains(joined, id)) || (problem == "storage" && cmd == "nsenter" && strings.Contains(joined, "validate "+id+" ")) {
					return []byte("secret command output"), instanceTestExit{}
				}
				if problem == "context" && cmd == "ls" && strings.Contains(joined, id) {
					return []byte("system_u:object_r:wrong_t:s0"), nil
				}
				return command(ctx, cmd, args...)
			}
			result := validationRequest(t, false)
			if strings.Contains(result.Output, "secret") {
				t.Fatal("private diagnostic leaked")
			}
			// Invalid registry evidence prevents trusting any inventory. Other problems
			// must leave the safe sibling fully checked, including live verification.
			if problem != "duplicate registry" && problem != "mismatched registry" && !strings.Contains(strings.Join(f.calls, "\n"), "verify-minecraft-stack --instance jv-d4e5f6 LATEST") {
				t.Fatal("safe sibling silently skipped")
			}
		})
	}
}

func TestApplianceValidationInstanceInfrastructureFailureIsSanitized(t *testing.T) {
	f := validationFixture(t, 1)
	original := f.backend.command
	f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
		if strings.HasSuffix(cmd, "/verify-minecraft-stack") {
			return []byte("private runtime output"), errors.New("private exec details")
		}
		return original(ctx, cmd, args...)
	}
	rr := httptest.NewRecorder()
	surfaceTestServer(t, roleAdministrator).adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "private") {
		t.Fatalf("infrastructure response: %d %s", rr.Code, rr.Body.String())
	}
}
