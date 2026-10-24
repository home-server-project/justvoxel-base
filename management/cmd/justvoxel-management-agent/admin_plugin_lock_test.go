package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testPluginMaintenanceLock(t *testing.T) *pluginMaintenanceLock {
	t.Helper()
	lock, err := acquirePluginMaintenanceLock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	return lock
}

func assertPluginMaintenanceBlocked(t *testing.T) {
	t.Helper()
	if err := exec.Command("flock", "-n", pluginRuntimeLockPath, "true").Run(); err == nil {
		t.Fatal("independent maintenance acquired owned lock")
	}
}

// Apply the real prerequisite to existing install/Add-ons and lifecycle fixtures,
// so their acknowledgement and recovery tests also reject starts under flock.
func testPluginStartupPrerequisite(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "flock", "-w", "1", pluginRuntimeLockPath, "true").Run(); err != nil {
		t.Fatal("startup prerequisite could not acquire maintenance", err)
	}
}

func TestPluginStartupLockHandoff(t *testing.T) {
	for _, outcome := range []string{"success", "start failure", "reacquire failure"} {
		t.Run(outcome, func(t *testing.T) {
			oldPath, oldCommand := pluginRuntimeLockPath, pluginInstallCommand
			pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
			t.Cleanup(func() { pluginRuntimeLockPath, pluginInstallCommand = oldPath, oldCommand })
			lock := testPluginMaintenanceLock(t)
			assertPluginMaintenanceBlocked(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, "bash", "-c", `
exec 9>"$1"
if flock -n 9; then echo bypassed; exit 1; fi
echo blocked
read -r go
flock -w 1 9 || exit 1
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
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				cancel()
				if !waited {
					_ = child.Wait()
				}
			}()
			lines := bufio.NewScanner(out)
			if !lines.Scan() || lines.Text() != "blocked" {
				t.Fatal("prerequisite did not encounter Agent flock", lines.Text())
			}
			startFailure := errors.New("start failed")
			var rival *os.File
			defer func() {
				if rival != nil {
					rival.Close()
				}
			}()
			pluginInstallCommand = func(_ context.Context, command string, args ...string) ([]byte, error) {
				if command != "systemctl" || len(args) != 2 || args[0] != "start" || args[1] != "minecraft-jv-a1b2c3.service" {
					t.Fatal(command, args)
				}
				if lock.protected() {
					t.Fatal("Agent retained maintenance descriptor during start")
				}
				if _, err := io.WriteString(in, "go\n"); err != nil {
					t.Fatal(err)
				}
				if !lines.Scan() || lines.Text() != "acquired" {
					t.Fatal("ExecStartPre could not acquire same flock", lines.Text())
				}
				assertPluginMaintenanceBlocked(t)
				if _, err := io.WriteString(in, "release\n"); err != nil {
					t.Fatal(err)
				}
				waited = true
				if err := child.Wait(); err != nil {
					t.Fatal("startup failed", err)
				}
				if outcome == "reacquire failure" {
					rival, err = acquirePluginRuntimeLock()
					if err != nil {
						t.Fatal(err)
					}
				}
				if outcome == "start failure" {
					return nil, startFailure
				}
				return nil, nil
			}
			err = lock.startHandoff(func() error {
				_, err := pluginInstallCommand(ctx, "systemctl", "start", "minecraft-jv-a1b2c3.service")
				return err
			})
			if outcome == "reacquire failure" {
				if err == nil || lock.protected() {
					t.Fatal("failed reacquisition reported protection", err)
				}
				// A second start cannot proceed without ownership.
				if lock.startHandoff(func() error { t.Fatal("unprotected start invoked"); return nil }) == nil {
					t.Fatal("unprotected continuation allowed")
				}
				rival.Close()
				rival = nil
			} else {
				if outcome == "success" && err != nil {
					t.Fatal(err)
				}
				if outcome == "start failure" && !errors.Is(err, startFailure) {
					t.Fatal("lost start failure", err)
				}
				if !lock.protected() {
					t.Fatal("caller resumed without maintenance protection")
				}
				assertPluginMaintenanceBlocked(t)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			if err := exec.Command("flock", "-n", pluginRuntimeLockPath, "true").Run(); err != nil {
				t.Fatal("final descriptor leaked", err)
			}
		})
	}
}

// Exercise real startup flocks through each transaction, including its rollback.
func TestPluginOperationsLockHandoff(t *testing.T) {
	for _, operation := range []string{"install", "enable", "disable", "remove", "addons"} {
		for _, outcome := range []string{"success", "start failure", "reacquire failure"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				var f *pluginInstallFixture
				var run func() operationJournal
				var target string
				switch operation {
				case "install":
					f = newPluginInstallFixture(t)
					p := f.plan(t)
					target = filepath.Join(f.data, "plugins", "root.jar")
					run = func() operationJournal { return f.execute(t, p, false) }
				case "addons":
					f = newPluginInstallFixture(t)
					d, _ := addonDefinitionFor("skript_script")
					addonTestHost(t, f.data, d)
					file := addonTestFile(t, []byte("on join: stop"))
					p, err := resolveAddonPlan(addonTestContext(), f.data, addonUpload{Kind: d.Kind, Filename: "test.sk", File: file})
					if err != nil {
						t.Fatal(err)
					}
					target = filepath.Join(f.data, p.Target)
					run = func() operationJournal {
						j, _, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginAddonInstall, p.Server.InstanceID)
						if err != nil {
							t.Fatal(err)
						}
						executeAddonInstall(f.s, j.OperationID, p, file, false, session{})
						j, err = f.s.operations.get(j.OperationID)
						if err != nil {
							t.Fatal(err)
						}
						return j
					}
				default:
					state := "enabled"
					if operation == "enable" {
						state = "disabled"
					}
					m := newPluginManageFixture(t, state)
					f = m.pluginInstallFixture
					p := m.managePlan(t, operation)
					run = func() operationJournal { return m.manage(t, p, false) }
				}
				base := pluginInstallCommand
				starts, stopsAfterLoss := 0, 0
				var rival *os.File
				defer func() {
					if rival != nil {
						rival.Close()
					}
				}()
				pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
					if command == "systemctl" && args[0] == "start" {
						starts++
						if len(args) != 2 || args[1] != "minecraft-jv-a1b2c3.service" {
							t.Fatal("wrong synchronous start", args)
						}
						// A separate ExecStartPre process must finish before systemctl returns.
						if err := exec.CommandContext(ctx, "flock", "-w", "1", pluginRuntimeLockPath, "true").Run(); err != nil {
							t.Fatal("self-deadlocked startup", err)
						}
						if starts == 1 && outcome == "start failure" {
							return nil, errors.New("startup failed")
						}
						out, startErr := base(ctx, command, args...)
						if starts == 1 && outcome == "reacquire failure" {
							var err error
							rival, err = acquirePluginRuntimeLock()
							if err != nil {
								t.Fatal(err)
							}
						}
						return out, startErr
					} else if command == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" {
						assertPluginMaintenanceBlocked(t)
					} else if command == "systemctl" && args[0] == "stop" && rival != nil {
						stopsAfterLoss++
					}
					return base(ctx, command, args...)
				}
				j := run()
				want := operationSucceeded
				if outcome == "start failure" {
					want = operationRolledBack
				}
				if outcome == "reacquire failure" {
					want = operationNeedsAttention
				}
				if j.State != want || starts == 0 {
					t.Fatal("unexpected result", starts, j.State, j.Status)
				}
				if outcome == "start failure" && starts < 2 {
					t.Fatal("rollback did not exercise restart")
				}
				if outcome == "reacquire failure" {
					if starts != 1 || stopsAfterLoss != 0 {
						t.Fatal("ordinary rollback ran without maintenance")
					}
					if operation == "install" || operation == "addons" {
						if _, err := os.Stat(target); err != nil {
							t.Fatal("unprotected rollback removed evidence", err)
						}
					} else {
						tx, err := readPluginManageTransaction(f.s.operations, j.OperationID)
						if err != nil {
							t.Fatal(err)
						}
						state, err := resolveManagedPluginLocation(f.data, tx.Entry)
						if operation == "remove" {
							stage, e := openPluginOperationStaging(f.data, j.OperationID, false)
							if e != nil {
								t.Fatal(e)
							}
							defer stage.Close()
							if e := verifyManagedPluginAt(stage, tx.Entry); e != nil {
								t.Fatal("rollback copy lost", e)
							}
						} else {
							want := "disabled"
							if operation == "enable" {
								want = "enabled"
							}
							if err != nil || state != want {
								t.Fatal("unprotected lifecycle rollback", state, err)
							}
						}
					}
				}
			})
		}
	}
}

func TestPluginLostLockBlocksRollback(t *testing.T) {
	for _, kind := range []string{operationTypePluginInstall, operationTypePluginDisable} {
		t.Run(kind, func(t *testing.T) {
			s := &server{operations: openTestOperationStore(t)}
			j, _, err := s.operations.beginMinecraftMaintenance(testSetupFingerprint, kind, "jv-a1b2c3")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.operations.transition(j.OperationID, operationValidating, "validating", "test"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.operations.transition(j.OperationID, operationRunning, "restarting", "test"); err != nil {
				t.Fatal(err)
			}
			oldCommand := pluginInstallCommand
			t.Cleanup(func() { pluginInstallCommand = oldCommand })
			pluginInstallCommand = func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("ordinary runtime rollback attempted without maintenance")
				return nil, nil
			}
			// Deliberately unusable file evidence: the ownership guard must return
			// before even attempting normal rollback file access or runtime mutation.
			if kind == operationTypePluginInstall {
				err = rollbackPluginInstall(s, j.OperationID, pluginInstallPlan{}, nil, nil, true, errors.New("start"), &pluginMaintenanceLock{})
			} else {
				err = rollbackPluginManage(s, j.OperationID, pluginManageTransaction{Action: "disable"}, errors.New("start"), &pluginMaintenanceLock{})
			}
			if err == nil {
				t.Fatal("unprotected rollback reported success")
			}
			result, err := s.operations.get(j.OperationID)
			if err != nil || result.State != operationNeedsAttention {
				t.Fatal("lost recovery reservation", result, err)
			}
			current, err := s.operations.currentMinecraftMaintenance()
			if err != nil || current == nil || current.OperationID != j.OperationID {
				t.Fatal("evidence reservation released", err)
			}
		})
	}
	if err := rollbackAddon(context.Background(), addonTransaction{}, &pluginMaintenanceLock{}); err == nil {
		t.Fatal("unprotected Add-ons rollback reported success")
	}
}
