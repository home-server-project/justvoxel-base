package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// All filesystem and command operations are fake; no runtime directory or PID 1 is needed.
func fakeHostStorageOutput(t *testing.T) {
	t.Helper()
	read, remove := readHostStorageOutput, removeHostStorageOutput
	t.Cleanup(func() {
		readHostStorageOutput, removeHostStorageOutput = read, remove
	})
	readHostStorageOutput = func(string) ([]byte, error) {
		t.Fatal("unexpected output read")
		return nil, nil
	}
	removeHostStorageOutput = func(string) error { return nil }
}

func TestHostStorageMutationCommand(t *testing.T) {
	old := runHostStorageCommand
	defer func() { runHostStorageCommand = old }()
	fakeHostStorageOutput(t)
	units := map[string]bool{}
	for _, helper := range []string{adminStorageProvisionHelper, adminStorageMountsHelper, adminStorageActionsHelper} {
		request := []byte("{\"device\":\"/dev/vdb1\",\"confirmation\":\"$(false)\"}\n")
		response := []byte(`{"ok":false,"error":"This partition is protected or unavailable for storage actions.","warnings":[],"applied":false}`)
		calls, reads := 0, 0
		var outputPath string
		var removed []string
		removeHostStorageOutput = func(path string) error {
			removed = append(removed, path)
			if len(removed) == 1 {
				return os.ErrNotExist
			}
			return nil
		}
		readHostStorageOutput = func(path string) ([]byte, error) {
			reads++
			if calls != 1 || path != outputPath || len(removed) != 1 {
				t.Fatalf("output read before execution or from wrong path: %q", path)
			}
			return response, nil
		}
		runHostStorageCommand = func(_ context.Context, executable string, args []string, stdin []byte) ([]byte, error) {
			calls++
			if executable != "/usr/bin/systemd-run" || len(args) != 13 {
				t.Fatalf("unexpected command: %s %q", executable, args)
			}
			unit := strings.TrimPrefix(args[9], "--unit=")
			if !regexp.MustCompile(`^justvoxel-host-storage-[0-9a-f]{32}\.service$`).MatchString(unit) || units[unit] {
				t.Fatalf("invalid or reused internal unit: %q", unit)
			}
			units[unit] = true
			outputPath = strings.TrimPrefix(args[7], "--property=StandardOutput=file:")
			if outputPath != "/run/justvoxel/"+unit+".json" || !reflect.DeepEqual(removed, []string{outputPath}) {
				t.Fatalf("output path or stale cleanup changed: %q %q", outputPath, removed)
			}
			want := []string{
				"--quiet", "--wait", "--collect", "--property=Type=exec", "--property=TimeoutStopSec=5s",
				"--property=StandardInput=data", "--property=StandardInputData=" + base64.StdEncoding.EncodeToString(request),
				"--property=StandardOutput=file:" + outputPath, "--property=StandardError=journal",
				"--unit=" + unit, "--", helper, "apply",
			}
			if !reflect.DeepEqual(args, want) || len(stdin) != 0 {
				t.Fatalf("argv/stdin changed: %q %q", args, stdin)
			}
			for _, arg := range args {
				if arg == "--pipe" {
					t.Fatal("FIFO transport must not be used")
				}
			}
			return []byte("launcher output must not become helper JSON"), nil
		}
		output, err := hostStorageMutation(context.Background(), helper, "apply", request)
		if err != nil || string(output) != string(response) || calls != 1 || reads != 1 {
			t.Fatalf("output=%q err=%v calls=%d reads=%d", output, err, calls, reads)
		}
		if !reflect.DeepEqual(removed, []string{outputPath, outputPath}) {
			t.Fatalf("output cleanup missing: %q", removed)
		}
	}
}

// The command fake advances this context to a deadline without a timer or sleep.
type hostStorageTestContext struct {
	context.Context
	err error
}

func (ctx *hostStorageTestContext) Err() error {
	if ctx.err != nil {
		return ctx.err
	}
	return ctx.Context.Err()
}

func TestHostStorageMutationCleanup(t *testing.T) {
	fakeHostStorageOutput(t)
	old := runHostStorageCommand
	defer func() { runHostStorageCommand = old }()
	for _, reason := range []string{"cancel", "timeout", "execution failure"} {
		for _, cleanupFails := range []bool{false, true} {
			t.Run(reason+"/cleanupFails="+map[bool]string{false: "false", true: "true"}[cleanupFails], func(t *testing.T) {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx := &hostStorageTestContext{Context: parent}
				failure := errors.New("launcher failed")
				cleanupFailure := errors.New("stop failed")
				calls := 0
				var unit, outputPath string
				var removed []string
				removeHostStorageOutput = func(path string) error {
					removed = append(removed, path)
					return nil
				}
				runHostStorageCommand = func(commandCtx context.Context, executable string, args []string, request []byte) ([]byte, error) {
					calls++
					if calls == 1 {
						unit = strings.TrimPrefix(args[9], "--unit=")
						outputPath = strings.TrimPrefix(args[7], "--property=StandardOutput=file:")
						switch reason {
						case "cancel":
							cancel()
							return nil, nil
						case "timeout":
							ctx.err = context.DeadlineExceeded
							return nil, nil
						}
						return []byte("helper stderr"), failure
					}
					if calls != 2 || executable != "/usr/bin/systemctl" || !reflect.DeepEqual(args, []string{"stop", "--", unit}) || len(request) != 0 {
						t.Fatalf("unexpected cleanup: %s %q", executable, args)
					}
					deadline, ok := commandCtx.Deadline()
					if commandCtx.Err() != nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Second {
						t.Fatal("cleanup must use an independent bounded context")
					}
					if cleanupFails {
						return nil, cleanupFailure
					}
					return nil, nil
				}
				_, err := hostStorageMutation(ctx, adminStorageMountsHelper, "apply", nil)
				wantErr := failure
				if reason == "timeout" {
					wantErr = context.DeadlineExceeded
				} else if reason == "cancel" {
					wantErr = context.Canceled
				}
				if calls != 2 || !errors.Is(err, wantErr) || (cleanupFails && !errors.Is(err, cleanupFailure)) {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
				if !reflect.DeepEqual(removed, []string{outputPath, outputPath}) {
					t.Fatalf("failed operation output cleanup missing: %q", removed)
				}
				if reason == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			})
		}
	}
}

func TestHostStorageMutationOutputFailures(t *testing.T) {
	old := runHostStorageCommand
	defer func() { runHostStorageCommand = old }()
	for _, reason := range []string{"stale removal", "read"} {
		t.Run(reason, func(t *testing.T) {
			fakeHostStorageOutput(t)
			failure := errors.New("filesystem failed")
			calls, removals := 0, 0
			removeHostStorageOutput = func(string) error {
				removals++
				if reason == "stale removal" {
					return failure
				}
				return nil
			}
			readHostStorageOutput = func(string) ([]byte, error) { return nil, failure }
			runHostStorageCommand = func(context.Context, string, []string, []byte) ([]byte, error) {
				calls++
				return nil, nil
			}
			_, err := hostStorageMutation(context.Background(), adminStorageActionsHelper, "apply", nil)
			if !errors.Is(err, failure) {
				t.Fatalf("filesystem failure lost: %v", err)
			}
			if reason == "stale removal" {
				if calls != 0 || removals != 1 {
					t.Fatalf("stale removal failure launched command: calls=%d removals=%d", calls, removals)
				}
			} else if calls != 1 || removals != 2 {
				t.Fatalf("read failure skipped cleanup: calls=%d removals=%d", calls, removals)
			}
		})
	}
}

func TestHostStorageMutationRejectsInvalidInvocation(t *testing.T) {
	old := runHostStorageCommand
	defer func() { runHostStorageCommand = old }()
	runHostStorageCommand = func(context.Context, string, []string, []byte) ([]byte, error) {
		t.Fatal("invalid invocation executed a command")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx            context.Context
		helper, action string
	}{
		{context.Background(), "/request-controlled/helper", "apply"},
		{context.Background(), adminStorageActionsHelper, "plan"},
		{ctx, adminStorageActionsHelper, "apply"},
	} {
		if _, err := hostStorageMutation(tc.ctx, tc.helper, tc.action, nil); err == nil {
			t.Fatal("invalid invocation succeeded")
		}
	}
}

type hostStorageRouteCase struct {
	name, method, path, helper, action, body, failure string
	handler                                           func(*server, http.ResponseWriter, *http.Request)
}

func hostStorageRouteCases() []hostStorageRouteCase {
	provision := `{"operation":"erase_disk","device":"/dev/vdb","mount_point":"/var/mnt/backup","path":"/var/mnt/backup/backups","confirmation":"ERASE /dev/vdb","fingerprint":"review123"}`
	mount := `{"operation":"persist","device":"/dev/vdb1","mount_point":"/var/mnt/data","fingerprint":"review123"}`
	action := `{"operation":"format","device":"/dev/vdb1","confirmation":"FORMAT /dev/vdb1","fingerprint":"review123"}`
	return []hostStorageRouteCase{
		{"provision discover", "GET", "/v1/admin/storage-provision", adminStorageProvisionHelper, "discover", "", "", (*server).adminStorageProvisionDiscover},
		{"provision plan", "POST", "/v1/admin/storage-provision/plan", adminStorageProvisionHelper, "plan", provision, "", (*server).adminStorageProvisionPlan},
		{"provision apply", "POST", "/v1/admin/storage-provision/apply", adminStorageProvisionHelper, "apply", provision, "storage provisioning operation failed", (*server).adminStorageProvisionApply},
		{"mount status", "GET", "/v1/admin/storage-mounts/status?device=%2Fdev%2Fvdb1", adminStorageMountsHelper, "status", `{"device":"/dev/vdb1"}`, "", (*server).adminStorageMountStatus},
		{"mount plan", "POST", "/v1/admin/storage-mounts/plan", adminStorageMountsHelper, "plan", mount, "", (*server).adminStorageMountPlan},
		{"mount apply", "POST", "/v1/admin/storage-mounts/apply", adminStorageMountsHelper, "apply", mount, "permanent mount operation failed", (*server).adminStorageMountApply},
		{"action plan", "POST", "/v1/admin/storage-actions/plan", adminStorageActionsHelper, "plan", action, "", (*server).adminStorageActionPlan},
		{"action apply", "POST", "/v1/admin/storage-actions/apply", adminStorageActionsHelper, "apply", action, "storage action failed", (*server).adminStorageActionApply},
	}
}

func fakeHostStorageRoutes(t *testing.T, fake func(context.Context, string, string, []byte, bool) ([]byte, error)) {
	t.Helper()
	provision, mounts, actions, host := runAdminStorageProvisionHelper, runAdminStorageMountsHelper, runAdminStorageActionsHelper, runHostStorageMutation
	t.Cleanup(func() {
		runAdminStorageProvisionHelper, runAdminStorageMountsHelper, runAdminStorageActionsHelper, runHostStorageMutation = provision, mounts, actions, host
	})
	runAdminStorageProvisionHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
		return fake(ctx, adminStorageProvisionHelper, action, request, false)
	}
	runAdminStorageMountsHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
		return fake(ctx, adminStorageMountsHelper, action, request, false)
	}
	runAdminStorageActionsHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
		return fake(ctx, adminStorageActionsHelper, action, request, false)
	}
	runHostStorageMutation = func(ctx context.Context, helper, action string, request []byte) ([]byte, error) {
		return fake(ctx, helper, action, request, true)
	}
}

func TestHostStorageRouteSelectionAndRequest(t *testing.T) {
	for _, tc := range hostStorageRouteCases() {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			fakeHostStorageRoutes(t, func(ctx context.Context, helper, action string, request []byte, host bool) ([]byte, error) {
				calls++
				if helper != tc.helper || action != tc.action || host != (tc.action == "apply") {
					t.Fatalf("wrong runner: helper=%s action=%s host=%v", helper, action, host)
				}
				var got, want map[string]string
				if tc.body == "" {
					if len(request) != 0 {
						t.Fatalf("unexpected discovery stdin: %q", request)
					}
				} else {
					if err := json.Unmarshal(request, &got); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("request changed: got=%v want=%v", got, want)
					}
				}
				limit := 15 * time.Second
				if tc.action == "discover" {
					limit = 10 * time.Second
				} else if host {
					limit = 90 * time.Second
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > limit {
					t.Fatal("operation timeout changed")
				}
				return []byte(`{"ok":true,"warnings":[],"proposed":{"operation":"format","device":"/dev/vdb1","confirmation":"FORMAT /dev/vdb1","fingerprint":"review123"},"applied":true}`), nil
			})
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			tc.handler(s, rr, surfaceRequest(tc.method, tc.path, tc.body))
			if rr.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body.String())
			}
		})
	}
}

func TestHostStorageApplyBoundedExecutionFailure(t *testing.T) {
	for _, tc := range hostStorageRouteCases() {
		if tc.action != "apply" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			fakeHostStorageRoutes(t, func(_ context.Context, helper, action string, _ []byte, host bool) ([]byte, error) {
				if !host || helper != tc.helper || action != "apply" {
					t.Fatal("apply did not use host runner")
				}
				return []byte("private helper output"), errors.New("private execution details")
			})
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			tc.handler(s, rr, surfaceRequest(tc.method, tc.path, tc.body))
			if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), tc.failure) || strings.Contains(rr.Body.String(), "private") {
				t.Fatalf("unbounded or changed failure: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHostStorageRoutesRequireAdministrator(t *testing.T) {
	for _, tc := range hostStorageRouteCases() {
		for _, role := range []principalRole{roleOperator, roleViewer} {
			t.Run(tc.name+"/"+string(role), func(t *testing.T) {
				fakeHostStorageRoutes(t, func(context.Context, string, string, []byte, bool) ([]byte, error) {
					t.Fatal("storage runner called for non-Administrator")
					return nil, nil
				})
				s := surfaceTestServer(t, role)
				rr := httptest.NewRecorder()
				tc.handler(s, rr, surfaceRequest(tc.method, tc.path, tc.body))
				if rr.Code != http.StatusForbidden {
					t.Fatalf("status=%d want 403", rr.Code)
				}
			})
		}
	}
}
