package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func networkAssertMaintenance(t *testing.T, held bool) {
	t.Helper()
	lock, err := acquirePluginRuntimeLock()
	if lock != nil {
		defer lock.Close()
	}
	if (err != nil) != held {
		t.Fatalf("maintenance held = %v, want %v: %v", err != nil, held, err)
	}
}

func networkLockFixture(t *testing.T, f *instanceActivationFixture) (*server, *server) {
	t.Helper()
	oldBackend, oldPath := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes, pluginRuntimeLockPath = oldBackend, oldPath })
	s := adminServerForTest()
	s.operations = openTestOperationStore(t)
	rival := adminServerForTest()
	var err error
	rival.operations, err = openOperationStore(s.operations.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rival.operations.close() })
	return s, rival
}

func networkAssertOperationExcluded(t *testing.T, s, rival *server) {
	t.Helper()
	if s.operations.mu.TryLock() {
		s.operations.mu.Unlock()
		t.Fatal("Agent operation mutex released before Configure finished")
	}
	// Use a second operation store with independent descriptors for the same
	// appliance locks. This mutation must fail even after maintenance is free.
	if release, err := rival.lockInstanceRuntime(); !errors.Is(err, errInstanceBusy) {
		if release != nil {
			_ = release()
		}
		t.Fatalf("competing Agent runtime mutation was not excluded: %v", err)
	}
}

func TestMinecraftNetworkConfigureLockPhases(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "prepare-failure", "incomplete-rollback"} {
		t.Run(mode, func(t *testing.T) {
			f, c := networkTransactionFixture(t)
			if mode != "commit" {
				if err := networkApply(context.Background(), f.backend, c); err != nil {
					t.Fatal(err)
				}
			}
			id := c.Members[0]
			f.states[id], f.containers[id] = "active", "running"
			h := networkLifecycleControls(f)
			s, rival := networkLockFixture(t, f)
			switch mode {
			case "rollback":
				f.failReload = 1
			case "incomplete-rollback":
				f.failReload = 2
			case "prepare-failure":
				h.failPrepare = true
			}
			c.WelcomeMessage = "Updated welcome"
			publications, reloads, starts, probes := 0, 0, 0, 0
			checkStartup := func() {
				t.Helper()
				networkAssertMaintenance(t, false)
				networkAssertOperationExcluded(t, s, rival)
				if err := networkPending(f.backend); err != nil {
					t.Fatal("runtime start before safe durable boundary", err)
				}
				if mode == "rollback" && (publications != 2 || reloads != 2) {
					t.Fatal("restoration before completed rollback", publications, reloads)
				}
				// Reservations must close before the maintenance handoff/start.
				ports, err := instanceHoldPort(c.JavaPort)
				if err != nil {
					t.Fatal("public port reservation survived into startup", err)
				}
				for _, port := range ports {
					_ = port.Close()
				}
				udp, err := instanceHoldUDP(c.BedrockPort)
				if err != nil {
					t.Fatal("UDP reservation survived into startup", err)
				}
				_ = udp.Close()
			}
			original := f.backend.command
			f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == minecraftNetworkHelper || (command == "systemctl" && (args[0] == "daemon-reload" || args[0] == "stop")) {
					// Includes forward publication and every rollback helper/reload.
					networkAssertMaintenance(t, true)
					if command == minecraftNetworkHelper && args[0] == "publish" {
						publications++
					}
					if command == "systemctl" && args[0] == "daemon-reload" {
						reloads++
					}
				}
				if command == "systemctl" && len(args) > 1 && args[0] == "--no-block" && args[1] == "start" {
					checkStartup() // Both infrastructure and normal backend starts.
					starts++
				}
				if command == "systemctl" && args[0] == "is-active" {
					checkStartup()
					probes++
				}
				return original(ctx, command, args...)
			}
			networkTCPReady = func(context.Context, int) bool { checkStartup(); probes++; return true }
			payload, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			s.minecraftNetworkConfigure(rr, authorizedRequest("PUT", "/v1/minecraft/network", string(payload)))
			if mode == "commit" {
				if rr.Code != 200 || publications != 1 || reloads != 1 || probes != 3 {
					t.Fatal(rr.Code, rr.Body.String(), publications, reloads, probes)
				}
			} else if rr.Code == 200 {
				t.Fatal("injected failure reported success")
			}
			if mode == "incomplete-rollback" {
				if starts != 0 || networkPending(f.backend) == nil {
					t.Fatal("unsafe rollback restarted runtime or lost evidence", starts)
				}
			} else if starts != 2 {
				t.Fatal("infrastructure/backend restoration missing", starts)
			}
			networkAssertMaintenance(t, false)
			if !s.operations.mu.TryLock() {
				t.Fatal("final cleanup retained Agent mutex")
			}
			s.operations.mu.Unlock()
			if err := rival.operations.acquireMinecraftResetLocks(); err != nil {
				t.Fatal("final cleanup retained appliance exclusions", err)
			}
			if err := rival.operations.releaseMinecraftResetLocks(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMinecraftNetworkMaintenanceReleaseIsIdempotent(t *testing.T) {
	f, _ := networkTransactionFixture(t)
	s, rival := networkLockFixture(t, f)
	releaseMaintenance, releaseAll, err := s.lockPhasedInstanceRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseAll()
	networkAssertMaintenance(t, true)
	if err := releaseMaintenance(); err != nil {
		t.Fatal(err)
	}
	networkAssertOperationExcluded(t, s, rival)
	other, err := acquirePluginRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := releaseMaintenance(); err != nil {
		t.Fatal(err)
	}
	if err := releaseAll(); err != nil {
		t.Fatal(err)
	}
	if err := releaseAll(); err != nil {
		t.Fatal(err)
	}
	networkAssertMaintenance(t, true) // Cleanup must not close another owner's FD.
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	networkAssertMaintenance(t, false)
}

func TestMinecraftNetworkMaintenanceReleaseFailurePreventsStartup(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			f, c := networkTransactionFixture(t)
			id := c.Members[0]
			f.states[id], f.containers[id] = "active", "running"
			h := networkLifecycleControls(f)
			s, rival := networkLockFixture(t, f)
			releaseMaintenance, releaseAll, err := s.lockPhasedInstanceRuntime()
			if err != nil {
				t.Fatal(err)
			}
			defer releaseAll()
			if rollback {
				f.failReload = 1
			}
			attempts := 0
			err = networkApplyWithMaintenanceRelease(context.Background(), f.backend, c, func() error {
				attempts++
				networkAssertMaintenance(t, true)
				networkAssertOperationExcluded(t, s, rival)
				if networkPending(f.backend) != nil {
					t.Fatal("release attempted before durable state was safe")
				}
				return errors.New("private close failure")
			})
			if err == nil || !strings.Contains(err.Error(), networkMaintenanceReleaseMessage) || strings.Contains(err.Error(), "private close failure") || attempts != 1 || len(h.starts) != 0 {
				t.Fatal("release failure did not fail safely", err, attempts, h.starts)
			}
			if err := releaseMaintenance(); err != nil {
				t.Fatal(err)
			}
			if err := releaseAll(); err != nil {
				t.Fatal(err)
			}
			networkAssertMaintenance(t, false)
			unlock, err := rival.lockInstanceRuntime()
			if err != nil {
				t.Fatal("release failure stranded other operation exclusions", err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
