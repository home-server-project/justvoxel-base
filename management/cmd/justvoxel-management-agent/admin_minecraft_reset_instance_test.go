package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func selectedResetFixture(t *testing.T, scope string) (*deleteFixture, *server, context.Context) {
	t.Helper()
	d := newDeleteFixture(t)
	oldRuntime, oldPlayers, oldLock, oldCommand := instanceRuntimes, pluginInstallPlayers, pluginRuntimeLockPath, pluginInstallCommand
	instanceRuntimes = d.f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	pluginInstallPlayers = func(context.Context, string) (int, []string, error) { return 0, []string{}, nil }
	pluginInstallCommand = func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command == "systemctl" && len(args) == 2 && args[0] == "is-active" && args[1] == "minecraft-jv-a1b2c3.service" {
			return []byte("inactive"), nil
		}
		t.Fatalf("unexpected plugin runtime command %s %v", command, args)
		return nil, errInstanceID
	}
	t.Cleanup(func() {
		instanceRuntimes = oldRuntime
		pluginInstallPlayers = oldPlayers
		pluginRuntimeLockPath = oldLock
		pluginInstallCommand = oldCommand
	})
	base := d.f.backend.command
	d.f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "nsenter" && len(args) > 2 && args[2] == "bash" {
			if args[len(args)-1] != instanceStorageFor(d.f.snapshot, d.f.configs[0].ID).DataPath {
				t.Fatal("scope inspected sibling", args)
			}
			return []byte(scope), nil
		}
		return base(ctx, command, args...)
	}
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	return d, s, withPluginInstance(context.Background(), d.f.configs[0].ID)
}

func TestSelectedMinecraftResetDataPolicyAndSibling(t *testing.T) {
	for _, scope := range []string{"internal", "external", "network", "unknown"} {
		t.Run(scope, func(t *testing.T) {
			d, s, ctx := selectedResetFixture(t, scope)
			b := d.f.backend
			id := d.f.configs[0].ID
			sibling := d.f.configs[1].ID
			data := instanceStorageFor(d.f.snapshot, id).DataPath
			siblingData := instanceStorageFor(d.f.snapshot, sibling).DataPath
			for _, path := range []string{filepath.Join(data, "world", "level.dat"), filepath.Join(data, "plugins", "manual.jar"), filepath.Join(siblingData, "world", "level.dat")} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := b.readRecord(sibling)
			if err != nil {
				t.Fatal(err)
			}
			plan, status := s.minecraftResetPlanWithRecovery(ctx)
			if status != 0 || plan.InstanceID != id || !plan.OK {
				t.Fatal(plan, status)
			}
			j, created, err := s.operations.beginMinecraftMaintenance(plan.PlanFingerprint, operationTypeMinecraftReset, id)
			if err != nil || !created || j.InstanceID != id {
				t.Fatal(j, err)
			}
			if err = executeMinecraftReset(ctx, s, j.OperationID, plan.PlanFingerprint, session{}); err != nil {
				t.Fatal(err)
			}
			j, err = s.operations.get(j.OperationID)
			if err != nil || j.State != operationSucceeded {
				t.Fatal(j, err)
			}
			if _, err = b.readRecord(id); err == nil {
				t.Fatal("reset instance still registered")
			}
			after, err := b.readRecord(sibling)
			{ // compare serialized records because firewall is a slice
				a, _ := json.Marshal(before)
				z, _ := json.Marshal(after)
				if err != nil || string(a) != string(z) {
					t.Fatal("sibling record changed", err)
				}
			}
			if _, err = os.Stat(filepath.Join(siblingData, "world", "level.dat")); err != nil {
				t.Fatal("sibling data changed", err)
			}
			if scope == "internal" {
				if !d.removed {
					t.Fatal("internal data not deleted")
				}
				if _, err = os.Stat(data); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("internal data remains", err)
				}
			} else {
				if d.removed {
					t.Fatal("preserved data removed")
				}
				if raw, err := os.ReadFile(filepath.Join(data, "world", "level.dat")); err != nil || string(raw) != "keep" {
					t.Fatal("external world changed", err)
				}
			}
			records, err := b.records()
			if err != nil || len(records) != 1 || records[0].Runtime.ID != sibling {
				t.Fatal("sibling configuration lost", records, err)
			}
			tx, err := s.readResetPluginTransaction(j.OperationID)
			if err != nil || tx.InstanceID != id || tx.Plan.DataAction != plan.DataAction {
				t.Fatal(tx, err)
			}
		})
	}
}

func TestSelectedMinecraftResetFinalInstance(t *testing.T) {
	d, s, ctx := selectedResetFixture(t, "internal")
	b := d.f.backend
	sibling := d.f.configs[1].ID
	dir, env, q := b.paths(sibling)
	for _, path := range []string{env, q, filepath.Join(dir, "config.json"), filepath.Join(b.store.directory, sibling+".json")} {
		if err := b.removePrivate(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	plan, status := s.minecraftResetPlanWithRecovery(ctx)
	if status != 0 {
		t.Fatal(plan, status)
	}
	j, _, err := s.operations.beginMinecraftMaintenance(plan.PlanFingerprint, operationTypeMinecraftReset, plan.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = executeMinecraftReset(ctx, s, j.OperationID, plan.PlanFingerprint, session{}); err != nil {
		t.Fatal(err)
	}
	records, err := b.records()
	if err != nil || len(records) != 0 {
		t.Fatal("final reset did not allow zero servers", records, err)
	}
}

func TestSelectedMinecraftResetRecoveryKeepsIDAndPolicy(t *testing.T) {
	d, s, ctx := selectedResetFixture(t, "internal")
	plan, status := s.minecraftResetPlanWithRecovery(ctx)
	if status != 0 {
		t.Fatal(plan, status)
	}
	j, _, err := s.operations.beginMinecraftMaintenance(plan.PlanFingerprint, operationTypeMinecraftReset, plan.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	d.failRemove = true
	if executeMinecraftReset(ctx, s, j.OperationID, plan.PlanFingerprint, session{}) == nil {
		t.Fatal("failure ignored")
	}
	persisted, err := s.readResetPluginTransaction(j.OperationID)
	if err != nil || persisted.InstanceID != plan.InstanceID {
		t.Fatal(err)
	}
	// A newly observed preservation classification must not change destructive policy on retry.
	base := d.f.backend.command
	d.f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "nsenter" && len(args) > 2 && args[2] == "bash" {
			return []byte("network"), nil
		}
		return base(ctx, command, args...)
	}
	if _, status = s.minecraftResetPlanWithRecovery(withPluginInstance(context.Background(), "jv-d4e5f6")); status == 0 {
		t.Fatal("recovery switched instance")
	}
	recovered, status := s.minecraftResetPlanWithRecovery(ctx)
	if status != 0 || recovered.InstanceID != plan.InstanceID || recovered.DataAction != "delete" {
		t.Fatal(recovered, status)
	}
	if _, err = s.operations.retryMinecraftReset(j.OperationID, plan.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	d.failRemove = false
	if err = executeMinecraftReset(ctx, s, j.OperationID, plan.PlanFingerprint, session{}); err != nil {
		t.Fatal(err)
	}
	if !d.removed {
		t.Fatal("persisted internal policy lost")
	}
}

func TestSelectedResetRoutesBindPlanApplyAndStatus(t *testing.T) {
	_, s, _ := selectedResetFixture(t, "internal")
	oldWorker := startMinecraftResetWorker
	startMinecraftResetWorker = func(*server, string, string, session) {}
	t.Cleanup(func() { startMinecraftResetWorker = oldWorker })
	mux := http.NewServeMux()
	registerAdminMinecraftResetRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/reset/plan", ""))
	var plan adminMinecraftResetPlanResponse
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &plan) != nil || plan.InstanceID != "jv-a1b2c3" {
		t.Fatal(rr.Code, rr.Body.String())
	}
	body := `{"plan_fingerprint":"` + plan.PlanFingerprint + `","confirm_players":false}`
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/reset/apply", body))
	if rr.Code != 202 || !strings.Contains(rr.Body.String(), `"instance_id":"jv-a1b2c3"`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, path := range []string{"/v1/minecraft/instances/jv-d4e5f6/reset/current-operation", "/v1/minecraft/instances/jv-stale1/reset/plan"} {
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest("GET", path, ""))
		if rr.Code == 200 {
			t.Fatal("mismatch accepted", path)
		}
	}
}

func TestSelectedResetOldEvidenceFailsClosed(t *testing.T) {
	_, s, ctx := selectedResetFixture(t, "internal")
	plan, status := s.minecraftResetPlanWithRecovery(ctx)
	if status != 0 {
		t.Fatal(plan)
	}
	j, _, err := s.operations.beginMinecraftMaintenance(plan.PlanFingerprint, operationTypeMinecraftReset, plan.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.resetPluginEvidenceDirectory(j.OperationID, true)
	if err != nil {
		t.Fatal(err)
	}
	tx := minecraftResetPluginTransaction{SchemaVersion: "v1", OperationID: j.OperationID, Plan: plan}
	if err = publishResetPluginTransaction(dir, &tx); err != nil {
		t.Fatal(err)
	}
	dir.Close()
	if executeMinecraftReset(ctx, s, j.OperationID, plan.PlanFingerprint, session{}) == nil {
		t.Fatal("missing immutable evidence accepted")
	}
	current, _ := s.operations.get(j.OperationID)
	if current.State != operationNeedsAttention {
		t.Fatal(current)
	}
}

func TestSelectedPreservedResetManagedPluginsRecoverOnlySelected(t *testing.T) {
	d, s, ctx := selectedResetFixture(t, "network")
	data := instanceStorageFor(d.f.snapshot, "jv-a1b2c3").DataPath
	sibling := instanceStorageFor(d.f.snapshot, "jv-d4e5f6").DataPath
	for _, root := range []string{data, sibling} {
		if err := os.Mkdir(filepath.Join(root, "plugins"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	pluginTestJar(t, filepath.Join(data, "plugins", "root.jar"), map[string]string{"plugin.yml": "name: Root\nversion: 1\nmain: example.Root\n"})
	root, err := openPluginDirectory(data)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hashPluginAt(root, "root.jar")
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	entry := pluginManifestEntry{Source: "Modrinth", ProjectID: "root", VersionID: "release", VersionNumber: "1", Filename: "root.jar", SHA512: hash, PluginName: "Root", Main: "example.Root"}
	if _, err = commitPluginManifest(data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	siblingFile := filepath.Join(sibling, "plugins", "sibling.jar")
	if err = os.WriteFile(siblingFile, []byte("sibling plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, status := s.minecraftResetPlanWithRecovery(ctx)
	if status != 0 {
		t.Fatal(plan, status)
	}
	j, _, err := s.operations.beginMinecraftMaintenance(plan.PlanFingerprint, operationTypeMinecraftReset, plan.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	d.f.failReload = 1
	if executeMinecraftReset(ctx, s, j.OperationID, plan.PlanFingerprint, session{}) == nil {
		t.Fatal("reload failure ignored")
	}
	tx, err := s.readResetPluginTransaction(j.OperationID)
	if err != nil || tx.InstanceID != "jv-a1b2c3" || tx.Plugins.DataPath != data {
		t.Fatal(tx, err)
	}
	if _, err = s.operations.retryMinecraftReset(j.OperationID, plan.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	if err = executeMinecraftReset(ctx, s, j.OperationID, plan.PlanFingerprint, session{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(data, "plugins", "root.jar")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("managed JAR survived reset", err)
	}
	if _, err = os.Stat(filepath.Join(data, ".justvoxel", "plugins.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ownership survived reset", err)
	}
	if raw, err := os.ReadFile(siblingFile); err != nil || string(raw) != "sibling plugin" {
		t.Fatal("sibling plugin changed", err)
	}
}
