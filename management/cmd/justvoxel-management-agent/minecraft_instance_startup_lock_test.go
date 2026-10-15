package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Model systemd's separate ExecStartPre process with an actual flock, not an
// instant-success systemctl mock. The child reports contention before the
// command can return and retains the acquired lock until explicitly released.
func TestInstanceStartupMaintenanceLockHandoff(t *testing.T) {
	for _, role := range []principalRole{roleAdministrator, roleOperator} {
		for _, action := range []string{"start", "restart"} {
			t.Run(string(role)+"/"+action, func(t *testing.T) {
				f := newInstanceActivationFixture(t)
				f.activateBoth()
				id := f.configs[0].ID
				if action == "restart" {
					if err := f.backend.action(context.Background(), id, "start"); err != nil {
						t.Fatal(err)
					}
				}
				oldBackend, oldLock := instanceRuntimes, pluginRuntimeLockPath
				instanceRuntimes = f.backend
				pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
				t.Cleanup(func() { instanceRuntimes = oldBackend; pluginRuntimeLockPath = oldLock })
				s := roleServerForTest(role)
				if role == roleOperator && action == "restart" {
					store, _ := openTestWebUIStore(t)
					user, err := store.createWebUser("LockOperator", webUserRoleOperator, "operator-password")
					if err != nil {
						t.Fatal(err)
					}
					s = operatorServerForStore(store, user)
				}
				s.operations = openTestOperationStore(t)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, "bash", "-c", `
exec 9>"$1"
if flock -n 9; then echo bypassed; exit 1; fi
echo waiting
flock -w 5 9 || exit 1
echo acquired
read -r release
`, "startup-prerequisite", pluginRuntimeLockPath)
				out, err := child.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				in, err := child.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer in.Close()
				lines := bufio.NewScanner(out)
				started, waited := false, false
				defer func() {
					cancel()
					if started && !waited {
						_ = child.Wait()
					}
				}()
				base := f.backend.command
				f.backend.command = func(commandCtx context.Context, command string, args ...string) ([]byte, error) {
					if command != "systemctl" || !slices.Contains(args, "start") {
						return base(commandCtx, command, args...)
					}
					if args[len(args)-1] != "minecraft-"+id+".service" {
						t.Fatal("startup escaped selected instance", args)
					}
					if action == "restart" && f.states[id] != "inactive" {
						t.Fatal("restart queued before verified graceful stop")
					}
					if err := child.Start(); err != nil {
						return nil, err
					}
					started = true
					if !lines.Scan() || lines.Text() != "waiting" {
						return nil, fmt.Errorf("prerequisite did not encounter Agent lock: %q", lines.Text())
					}
					if !slices.Contains(args, "--no-block") {
						// A synchronous start waits for ExecStartPre, which cannot
						// complete while withInstanceMutation retains the lock.
						waited = true
						return nil, child.Wait()
					}
					return nil, nil // systemd accepted the job; prerequisite still waits.
				}
				mux := http.NewServeMux()
				registerInstanceRuntimeRoutes(mux, s)
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/"+action, `{}`))
				if rr.Code != http.StatusOK || !started {
					t.Fatal("startup request failed", rr.Code, rr.Body.String())
				}
				if !lines.Scan() || lines.Text() != "acquired" {
					t.Fatal("prerequisite could not acquire released Agent lock", lines.Text(), lines.Err())
				}
				// Independent maintenance must not enter while preparation runs.
				if lock, err := acquirePluginRuntimeLock(); err == nil {
					lock.Close()
					t.Fatal("independent maintenance bypassed startup prerequisite")
				}
				if err := exec.CommandContext(ctx, "flock", "-n", pluginRuntimeLockPath, "true").Run(); err == nil {
					t.Fatal("independent process bypassed startup prerequisite")
				}
				if _, err := io.WriteString(in, "release\n"); err != nil {
					t.Fatal(err)
				}
				waited = true
				if err := child.Wait(); err != nil {
					t.Fatal("startup prerequisite failed", err)
				}
				release, err := s.lockInstanceRuntime()
				if err != nil {
					t.Fatal("maintenance remained blocked after preparation", err)
				}
				if err := release(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
