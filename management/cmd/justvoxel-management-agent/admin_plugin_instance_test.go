package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func pluginTestData(t *testing.T) string { return pluginTestDataFor(t, "paper", "1.21.8") }
func pluginTestDataFor(t *testing.T, software, version string) string {
	t.Helper()
	f := newInstanceActivationFixtureFor(t, software, version)
	f.activateBoth()
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	return instanceStorageFor(f.snapshot, f.configs[0].ID).DataPath
}
func pluginTestRequest(method, path, body string) *http.Request {
	r := surfaceRequest(method, path, body)
	r.SetPathValue("id", "jv-a1b2c3")
	return r.WithContext(withPluginInstance(r.Context(), "jv-a1b2c3"))
}
func TestPluginSelectedContextAndRuntimeTargets(t *testing.T) {
	f := newPluginInstallFixture(t)
	record, err := instanceRuntimes.readRecord("jv-d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	sc, path, ok := f.s.pluginContext(&pluginContextSink{header: make(http.Header)}, pluginTestRequest("GET", "/", ""))
	if !ok || sc.InstanceID != "jv-a1b2c3" || path != f.data || path == record.Storage.DataPath || sc.MinecraftVersion != "1.21.8" {
		t.Fatal(sc, path)
	}
	if err := pluginService(context.Background(), sc.InstanceID, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := pluginService(context.Background(), "stale", "stop"); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if err := pluginService(context.Background(), "jv-stale1", "start"); err == nil {
		t.Fatal("stale ID accepted")
	}
	if !strings.Contains(strings.Join(f.commands, "\n"), "minecraft-jv-a1b2c3.service") {
		t.Fatal(f.commands)
	}
	if strings.Contains(strings.Join(f.commands, "\n"), "minecraft.service") {
		t.Fatal(f.commands)
	}
	if _, err := instanceRuntimes.readRecord("jv-d4e5f6"); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "data" {
		t.Fatal(path)
	}
}

// Legacy fixture inputs are intentionally disconnected from production context discovery.
var runPluginRuntimeStatus = func(context.Context) ([]byte, error) { return nil, errInstanceID }

func TestInstanceMaintenanceColdBackupSelectedOnly(t *testing.T) {
	f := newPluginInstallFixture(t)
	sibling, err := instanceRuntimes.readRecord("jv-d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	if err = instanceMaintenanceColdBackup(context.Background(), "jv-a1b2c3"); err != nil {
		t.Fatal(err)
	}
	if !f.backup || f.active {
		t.Fatal("cold backup did not leave selected instance stopped", f.commands)
	}
	after, err := instanceRuntimes.readRecord("jv-d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	if after.Runtime != sibling.Runtime || after.Storage != sibling.Storage {
		t.Fatal("sibling changed")
	}
	for _, command := range f.commands {
		if strings.Contains(command, "jv-d4e5f6") || strings.Contains(command, "minecraft-backup") {
			t.Fatal("backup targeted sibling/global runtime", command)
		}
	}
}

func TestPluginDurableIdentityBeforeColdBackup(t *testing.T) {
	f := newPluginInstallFixture(t)
	p := f.plan(t)
	j, _, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginInstall, p.Server.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	backup := instanceBackupStorageCommand
	instanceBackupStorageCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if len(args) > 3 && args[3] == "preflight" {
			tx, err := readPluginTransaction(f.s.operations.baseDir, j.OperationID)
			if err != nil || tx.InstanceID != j.InstanceID || j.InstanceID != "jv-a1b2c3" {
				t.Fatal("backup before durable immutable identity", tx, err)
			}
		}
		return backup(ctx, command, args...)
	}
	if err = executePluginInstall(context.Background(), f.s, j.OperationID, p, false, session{}); err != nil {
		t.Fatal(err)
	}
}

func TestAddonRuntimeRecoveryUsesPersistedInstance(t *testing.T) {
	f := newPluginInstallFixture(t)
	tx := addonTransaction{InstanceID: "jv-a1b2c3", DataPath: f.data, Version: "1.21.8", RestartRequired: true, OriginallyRunning: false}
	lock := testPluginMaintenanceLock(t)
	// Caller selection cannot redirect the durable transaction's recovery target.
	if err := restoreAddonRuntime(withPluginInstance(context.Background(), "jv-d4e5f6"), tx, lock); err != nil {
		t.Fatal(err)
	}
	if f.active {
		t.Fatal("selected original stopped state not restored")
	}
	for _, command := range f.commands {
		if strings.Contains(command, "jv-d4e5f6") || strings.Contains(command, "minecraft.service") {
			t.Fatal("recovery targeted sibling", command)
		}
	}
	tx.InstanceID = ""
	if err := restoreAddonRuntime(context.Background(), tx, lock); err == nil {
		t.Fatal("old Add-ons evidence without ID accepted")
	}
}

func TestPluginManageTransactionPersistsInstance(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	p := f.managePlan(t, "disable")
	j := f.manage(t, p, false)
	tx, err := readPluginManageTransaction(f.s.operations, j.OperationID)
	if err != nil || j.InstanceID != "jv-a1b2c3" || tx.InstanceID != j.InstanceID || tx.DataPath != f.data {
		t.Fatal(j, tx, err)
	}
	for _, command := range f.commands {
		if strings.Contains(command, "jv-d4e5f6") {
			t.Fatal("management touched sibling", command)
		}
	}
}
