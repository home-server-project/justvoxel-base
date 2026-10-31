package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var failedUpdateSafetyScenarios = []string{
	"healthy", "stopped", "failed", "configured", "activating", "unavailable",
	"digest_mismatch", "pending", "stack_failure", "missing_digest", "invalid_digest",
	"uncommitted", "missing_record", "container_inspect_failure", "invalid_image_id",
	"image_inspect_failure", "empty_running_digest",
}

type failedUpdateSafetyFixture struct {
	f                  *instanceActivationFixture
	id                 string
	verifies           int
	beforeCommand      func(context.Context)
	stackErr           error
	files              map[string][]byte
	states, containers map[string]string
}

func newFailedUpdateSafetyFixture(t *testing.T, scenario string) *failedUpdateSafetyFixture {
	t.Helper()
	f := newInstanceActivationFixture(t)
	f.backend.networkRoot = filepath.Join(t.TempDir(), "network")
	id := f.configs[0].ID
	dir, env, quadlet := f.backend.paths(id)
	record, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	record.Runtime.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	if err := f.backend.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
		t.Fatal(err)
	}
	if err := f.apply(id); err != nil {
		t.Fatal(err)
	}
	x := &failedUpdateSafetyFixture{f: f, id: id, stackErr: errors.New("full stack verification failed")}
	switch scenario {
	case "pending":
		// Leave real recoverable evidence. Automatic proof must not consume it.
		f.failReload = 2
		if err := f.apply(id); !errors.Is(err, errInstanceRecovery) {
			t.Fatalf("prepare pending transaction: %v", err)
		}
	case "missing_digest", "invalid_digest":
		record, err := f.backend.readRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		record.Runtime.ImageDigest = ""
		if scenario == "invalid_digest" {
			record.Runtime.ImageDigest = "sha256:bad"
		}
		if err := f.backend.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
			t.Fatal(err)
		}
	case "uncommitted":
		if err := os.WriteFile(env, []byte("uncommitted configuration"), 0600); err != nil {
			t.Fatal(err)
		}
	case "missing_record":
		if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
			t.Fatal(err)
		}
	}
	f.states[id], f.containers[id] = "active", "running"
	switch scenario {
	case "stopped":
		f.states[id], f.containers[id] = "inactive", ""
	case "failed":
		f.states[id], f.containers[id] = "failed", ""
	case "configured":
		delete(f.states, id)
		f.containers[id] = ""
	case "activating":
		f.states[id] = "activating"
	}
	x.files = map[string][]byte{}
	for _, path := range []string{filepath.Join(dir, "config.json"), filepath.Join(dir, "pending.json"), env, quadlet} {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		x.files[path] = data
	}
	x.states, x.containers = map[string]string{}, map[string]string{}
	for k, v := range f.states {
		x.states[k] = v
	}
	for k, v := range f.containers {
		x.containers[k] = v
	}
	oldRuntime, oldContainerVerify, oldStackVerify := instanceRuntimes, verifyMinecraftContainerCurrent, verifyMinecraftStack
	t.Cleanup(func() {
		instanceRuntimes, verifyMinecraftContainerCurrent, verifyMinecraftStack = oldRuntime, oldContainerVerify, oldStackVerify
	})
	instanceRuntimes = f.backend
	verifyMinecraftContainerCurrent = func(context.Context, string) error {
		t.Fatal("automatic proof used permissive container verifier")
		return nil
	}
	verifyMinecraftStack = func(context.Context) error { t.Fatal("automatic proof used implicit-version verifier"); return nil }
	imageID := "sha256:" + strings.Repeat("b", 64)
	f.backend.command = func(ctx context.Context, executable string, args ...string) ([]byte, error) {
		if x.beforeCommand != nil {
			x.beforeCommand(ctx)
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("proof inherited expired update context: %v", err)
		}
		switch {
		case executable == "systemctl" && reflect.DeepEqual(args, []string{"show", "--property=LoadState,ActiveState,SubState", "--", "minecraft-" + id + ".service"}):
			if scenario == "unavailable" {
				return nil, errors.New("systemd unavailable")
			}
			return f.command(ctx, executable, args...)
		case executable == "podman" && reflect.DeepEqual(args, []string{"ps", "--all", "--filter", "name=^minecraft-" + id + "$", "--format", "{{.Names}}"}):
			return f.command(ctx, executable, args...)
		case executable == "podman" && reflect.DeepEqual(args, []string{"inspect", "--type", "container", "--format", "{{.State.Status}} {{.State.ExitCode}}", "minecraft-" + id}):
			return f.command(ctx, executable, args...)
		case executable == "nsenter" && len(args) == 12 && args[0] == "--mount=/proc/1/ns/mnt" && args[1] == "--" && args[2] == instanceRuntimeHelper && args[3] == "validate" && args[4] == id:
			return f.command(ctx, executable, args...)
		case executable == "podman" && reflect.DeepEqual(args, []string{"inspect", "--type", "container", "--format", "{{.Image}}", "minecraft-" + id}):
			if scenario == "container_inspect_failure" {
				return nil, errors.New("container unavailable")
			}
			if scenario == "invalid_image_id" {
				return []byte("latest"), nil
			}
			return []byte(imageID + "\n"), nil
		case executable == "podman" && reflect.DeepEqual(args, []string{"image", "inspect", imageID, "--format", "{{.Digest}}"}):
			if scenario == "image_inspect_failure" {
				return nil, errors.New("image unavailable")
			}
			if scenario == "digest_mismatch" {
				return []byte("sha256:" + strings.Repeat("c", 64)), nil
			}
			if scenario == "empty_running_digest" {
				return nil, nil
			}
			return []byte(record.Runtime.ImageDigest + "\n"), nil
		case executable == "env" && reflect.DeepEqual(args, []string{"JUSTVOXEL_MAINTENANCE_LOCK_HELD=1", "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", "--instance", id, record.Runtime.Version}):
			x.verifies++
			if scenario == "stack_failure" {
				return nil, x.stackErr
			}
			return nil, nil
		default:
			t.Fatalf("automatic proof attempted recovery, mutation, retry or unexpected command: %s %v", executable, args)
			return nil, errors.New("unexpected command")
		}
	}
	return x
}

func (x *failedUpdateSafetyFixture) assertReadOnly(t *testing.T) {
	t.Helper()
	for path, before := range x.files {
		after, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if (before == nil) != errors.Is(err, os.ErrNotExist) || !bytes.Equal(before, after) {
			t.Fatalf("automatic proof changed runtime/recovery evidence: %s", path)
		}
	}
	if !reflect.DeepEqual(x.states, x.f.states) || !reflect.DeepEqual(x.containers, x.f.containers) {
		t.Fatal("automatic proof changed service/container state")
	}
}

func assertFailedUpdateProofCount(t *testing.T, x *failedUpdateSafetyFixture, scenario string) {
	t.Helper()
	want := 0
	if scenario == "healthy" || scenario == "stack_failure" {
		want = 1
	}
	if x.verifies != want {
		t.Fatalf("full verifications=%d want=%d", x.verifies, want)
	}
	x.assertReadOnly(t)
}

func testFailedMinecraftUpdateWorker(t *testing.T, kind string) {
	t.Helper()
	for _, scenario := range failedUpdateSafetyScenarios {
		t.Run(scenario, func(t *testing.T) {
			x := newFailedUpdateSafetyFixture(t, scenario)
			s := surfaceTestServer(t, roleAdministrator)
			s.operations = openTestOperationStore(t)
			journal, _, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, kind, x.id)
			if err != nil {
				t.Fatal(err)
			}
			oldContainer, oldStack := runMinecraftContainerUpdate, runMinecraftStackUpdate
			t.Cleanup(func() { runMinecraftContainerUpdate, runMinecraftStackUpdate = oldContainer, oldStack })
			updateErr := errors.New("original update failed")
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runMinecraftContainerUpdate = func(ctx context.Context, r minecraftContainerUpdateRequest) error {
				if kind != operationTypeMinecraftContainerUpdate || r.InstanceID != x.id {
					t.Fatal("wrong update target")
				}
				if _, ok := ctx.Value(operationTraceKey{}).(operationTraceContext); !ok {
					t.Fatal("lost protected trace")
				}
				calls++
				cancel()
				return updateErr
			}
			runMinecraftStackUpdate = func(ctx context.Context, _ string, _ bool, _, _ string) error {
				if kind != operationTypeMinecraftUpdate || ctx.Value(instanceVersionKey{}) != x.id {
					t.Fatal("wrong stack update target")
				}
				calls++
				cancel()
				return updateErr
			}
			x.beforeCommand = func(ctx context.Context) {
				deadline, bounded := ctx.Deadline()
				if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 90*time.Second {
					t.Fatal("proof needs fresh bounded context")
				}
				durable, err := readOperationJournal(filepath.Join(s.operations.operationsDir, journal.OperationID+".json"))
				if err != nil || durable.State != operationNeedsAttention || durable.Stage != "update_failed" {
					t.Fatalf("proof preceded durable failure: %+v %v", durable, err)
				}
				assertStartupUpdateLocks(t, s.operations, true)
			}
			if kind == operationTypeMinecraftContainerUpdate {
				err = executeMinecraftContainerUpdate(ctx, s, journal.OperationID, minecraftContainerUpdateRequest{InstanceID: x.id, PlanFingerprint: testMinecraftResetFingerprint}, session{})
			} else {
				err = executeMinecraftStackUpdate(ctx, s, journal.OperationID, minecraftStackUpdateRequest{InstanceID: x.id, PlanFingerprint: testMinecraftResetFingerprint}, session{})
			}
			if !errors.Is(err, updateErr) || calls != 1 {
				t.Fatalf("lost failure or retried update: %v calls=%d", err, calls)
			}
			if scenario == "stack_failure" && !errors.Is(err, x.stackErr) {
				t.Fatal("lost full verification failure", err)
			}
			assertFailedUpdateProofCount(t, x, scenario)
			finished, err := s.operations.get(journal.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.operations.currentMinecraftMaintenance()
			if err != nil {
				t.Fatal(err)
			}
			safe := scenario == "healthy"
			assertStartupUpdateLocks(t, s.operations, !safe)
			if safe {
				if finished.State != operationResolved || current != nil || s.operations.currentMinecraftResetID != "" {
					t.Fatalf("verified runtime retained locks: %+v", finished)
				}
				for _, text := range []string{"update stopped", "Already-running committed runtime independently verified", "kept automatically", "No update was retried"} {
					if !strings.Contains(finished.Status, text) {
						t.Fatalf("misleading failure status: %s", finished.Status)
					}
				}
			} else {
				wantStatus := "Update stopped. Check the details below. You can keep the current version or try again."
				if kind == operationTypeMinecraftUpdate {
					wantStatus = "Server update stopped. Review Minecraft state before continuing."
				}
				if finished.State != operationNeedsAttention || finished.Stage != "update_failed" || finished.Status != wantStatus || current == nil || current.OperationID != journal.OperationID {
					t.Fatalf("unsafe failure changed: %+v", finished)
				}
			}
			_, created, err := s.operations.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypePluginInstall, "jv-d4e5f6")
			if safe && (err != nil || !created) || !safe && err == nil {
				t.Fatalf("competing maintenance: created=%v err=%v", created, err)
			}
		})
	}
}

func TestFailedUpdateReleaseRejectsInvalidInstanceID(t *testing.T) {
	x := newFailedUpdateSafetyFixture(t, "healthy")
	x.beforeCommand = func(context.Context) { t.Fatal("invalid instance reached runtime command") }
	for _, id := range []string{"", "minecraft", "invalid-instance", "jv-../../etc"} {
		if !errors.Is(verifyMinecraftFailedUpdateSafeToRelease(context.Background(), id), errInstanceID) {
			t.Fatalf("invalid immutable ID accepted: %q", id)
		}
	}
	x.assertReadOnly(t)
}
