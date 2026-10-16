package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshSetupCreatesOneNormalInstanceThroughSharedCreation(t *testing.T) {
	f := newInstanceActivationFixture(t)
	// A fresh registry has no minecraft.json or pre-existing server records.
	f.backend.store = testMinecraftInstances(t)
	registry, err := f.backend.store.openDirectory(true)
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
	for _, c := range f.configs {
		dir, _, _ := f.backend.paths(c.ID)
		if err = os.Remove(filepath.Join(dir, "config.json")); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(dir); err != nil {
			t.Fatal(err)
		}
	}
	old, oldPolicy, oldMarker := instanceRuntimes, setupPolicyRoot, setupInstanceMarker
	instanceRuntimes = f.backend
	setupPolicyRoot = t.TempDir()
	setupInstanceMarker = filepath.Join(t.TempDir(), "setup-in-progress")
	t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldPolicy; setupInstanceMarker = oldMarker })
	r := setupRuntimeTransactionRequest{InstanceOnly: true, SchemaVersion: operationSchemaVersion, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint,
		Server:    setupRuntimeServer{MOTD: "Family", MaxPlayers: 20, Timezone: "UTC"},
		Minecraft: setupRuntimeMinecraft{ServerType: "paper", GameMode: "survival", JavaMemory: "1G", ContainerMemory: "2G", JavaPort: instanceTestPort(t), BedrockPort: 19132, ImageTag: "stable", VersionPolicy: "pinned", Version: "1.21.4", MinecraftUID: 1000, MinecraftGID: 1000},
		Storage:   setupRuntimeStorage{Type: "system", Path: filepath.Join(f.snapshot.StorageRoot, "minecraft")}, Backups: setupRuntimeBackups{Type: "system", Path: f.snapshot.BackupPath, Keep: 7, Schedule: "*-*-* 04:30:00"}}
	for _, action := range []string{"validate", "apply"} {
		response, err := runSetupInstanceAction(context.Background(), action, r)
		if err != nil || !response.OK {
			t.Fatal(action, response, err)
		}
	}
	e, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	id := e.Record.Runtime.ID
	if !instanceIDPattern.MatchString(id) {
		t.Fatal("first server has no generated immutable ID")
	}
	record, err := f.backend.readRecord(id)
	if err != nil || f.backend.verifyLive(record) != nil {
		t.Fatal("first server is not a committed normal runtime", err)
	}
	if record.Storage.DataPath != filepath.Join(f.snapshot.StorageRoot, "minecraft-instances", id, "data") || !record.Runtime.StartOnBoot || len(record.Firewall) == 0 {
		t.Fatal("first server skipped normal activation", record.Storage)
	}
	registered, err := f.backend.identities().listInstances("")
	if err != nil || len(registered) != 1 || registered[0].ID != id {
		t.Fatal("wrong registry", registered, err)
	}
	if fixed, err := f.backend.identities().read(); err != nil || fixed.ID != "" {
		t.Fatal("created minecraft.json", fixed, err)
	}
	_, env, quadlet := f.backend.paths(id)
	for _, path := range []string{env, quadlet} {
		raw, err := f.backend.readPrivate(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "ContainerName=minecraft\n") || strings.Contains(string(raw), "/etc/justvoxel/minecraft.env") {
			t.Fatal("fixed runtime artifact", path)
		}
	}
	for _, path := range []string{filepath.Join(setupPolicyRoot, "minecraft.env"), filepath.Join(f.backend.quadlets, "minecraft.container")} {
		if _, err = os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("fixed runtime file created", path)
		}
	}
}

func TestNewSetupRecoveryBeforeRuntimePublicationIsSafeNoOp(t *testing.T) {
	f := newInstanceActivationFixture(t)
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	response, handled, err := recoverSetupInstance(context.Background(), operationJournal{InstanceSetup: true, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint})
	if err != nil || !handled || !response.OK || response.Phase != "runtime_rolled_back" {
		t.Fatal(response, handled, err)
	}
}
