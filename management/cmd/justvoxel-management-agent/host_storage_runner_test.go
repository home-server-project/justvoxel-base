package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestHostStorageMutationCommand(t *testing.T) {
	old := runHostStorageCommand
	defer func() { runHostStorageCommand = old }()
	units := map[string]bool{}
	for _, helper := range []string{adminStorageProvisionHelper, adminStorageMountsHelper, adminStorageActionsHelper} {
		request := []byte("{\"device\":\"/dev/vdb1\",\"confirmation\":\"$(false)\"}\n")
		calls := 0
		runHostStorageCommand = func(_ context.Context, executable string, args []string, stdin []byte) ([]byte, error) {
			calls++
			if executable != "/usr/bin/systemd-run" || len(args) != 10 {
				t.Fatalf("unexpected command: %s %q", executable, args)
			}
			unit := strings.TrimPrefix(args[6], "--unit=")
			if !regexp.MustCompile(`^justvoxel-host-storage-[0-9a-f]{32}\.service$`).MatchString(unit) || units[unit] {
				t.Fatalf("invalid or reused internal unit: %q", unit)
			}
			units[unit] = true
			want := []string{"--quiet", "--pipe", "--wait", "--collect", "--property=Type=exec", "--property=TimeoutStopSec=5s", "--unit=" + unit, "--", helper, "apply"}
			if !reflect.DeepEqual(args, want) || string(stdin) != string(request) {
				t.Fatalf("argv/stdin changed: %q %q", args, stdin)
			}
			return []byte(`{"ok":true}`), nil
		}
		output, err := hostStorageMutation(context.Background(), helper, "apply", request)
		if err != nil || string(output) != `{"ok":true}` || calls != 1 {
			t.Fatalf("output=%q err=%v calls=%d", output, err, calls)
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
				var unit string
				runHostStorageCommand = func(commandCtx context.Context, executable string, args []string, request []byte) ([]byte, error) {
					calls++
					if calls == 1 {
						unit = strings.TrimPrefix(args[6], "--unit=")
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
					if commandCtx.Err() != nil || !ok || time.Until(deadline) > 10*time.Second {
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
				if reason == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			})
		}
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
