package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshSetupCreatesOneNormalInstanceThroughSharedCreation(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint("existing-policy-root=", existing), func(t *testing.T) {
			for _, name := range []string{"", "Family Survival"} {
				t.Run("name="+name, func(t *testing.T) { testFreshSetupCreatesOneNormalInstance(t, existing, name) })
			}
		})
	}
}
func testFreshSetupCreatesOneNormalInstance(t *testing.T, existing bool, name string) {
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
	setupPolicyRoot = filepath.Join(t.TempDir(), "justvoxel")
	if existing {
		if err := os.Mkdir(setupPolicyRoot, 0700); err != nil {
			t.Fatal(err)
		}
	}
	setupInstanceMarker = filepath.Join(t.TempDir(), "setup-in-progress")
	t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldPolicy; setupInstanceMarker = oldMarker })
	r := setupRuntimeTransactionRequest{InstanceOnly: true, SchemaVersion: operationSchemaVersion, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint,
		Server:    setupRuntimeServer{DisplayName: name, MOTD: "Family", MaxPlayers: 20, Timezone: "UTC"},
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
	expectedName := name
	if name == "" {
		expectedName = instanceDefaultName("paper")
	}
	registered, err := f.backend.identities().listInstances("")
	if err != nil || len(registered) != 1 || registered[0].ID != id || registered[0].DisplayName != expectedName {
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

func TestSetupPolicyPublicationAndBoundedRollback(t *testing.T) {
	for _, mode := range []string{"fresh", "existing", "unrelated", "old-evidence"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			old, oldRoot, oldMarker := instanceRuntimes, setupPolicyRoot, setupInstanceMarker
			instanceRuntimes = f.backend
			setupPolicyRoot = filepath.Join(t.TempDir(), "justvoxel")
			setupInstanceMarker = filepath.Join(t.TempDir(), "setup-in-progress")
			t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldRoot; setupInstanceMarker = oldMarker })
			if mode == "existing" || mode == "old-evidence" {
				if err := os.Mkdir(setupPolicyRoot, 0700); err != nil {
					t.Fatal(err)
				}
			}
			r := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint}
			if _, err := runSetupInstanceAction(context.Background(), "validate", r); err != nil {
				t.Fatal(err)
			}
			e, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
			if err != nil || e.HadPolicyRoot == nil || *e.HadPolicyRoot != (mode == "existing" || mode == "old-evidence") {
				t.Fatal(e, err)
			}
			// Stop after actual policy publication, before any server is created.
			f.backend.snapshot = func(context.Context) (minecraftInstanceRuntimeContext, uint32, uint32, error) {
				return minecraftInstanceRuntimeContext{}, 0, 0, fmt.Errorf("injected after publication")
			}
			if _, err = runSetupInstanceAction(context.Background(), "apply", r); err == nil || !strings.Contains(err.Error(), "injected after publication") {
				t.Fatal("did not reach publication", err)
			}
			for name, expected := range e.Policy {
				raw, err := f.backend.readPrivate(filepath.Join(setupPolicyRoot, name))
				if err != nil || string(raw) != string(expected) {
					t.Fatal(name, err)
				}
			}
			if mode == "unrelated" {
				if err = os.WriteFile(filepath.Join(setupPolicyRoot, "keep"), []byte("unrelated"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "old-evidence" {
				e.HadPolicyRoot = nil
				if err = f.backend.writeJSON(setupInstancePath(r.OperationID), e, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = runSetupInstanceAction(context.Background(), "rollback", r); err != nil {
				t.Fatal(err)
			}
			for name := range e.Policy {
				if _, err = os.Lstat(filepath.Join(setupPolicyRoot, name)); !os.IsNotExist(err) {
					t.Fatal("policy survived rollback", name, err)
				}
			}
			_, err = os.Lstat(setupPolicyRoot)
			if mode == "fresh" {
				if !os.IsNotExist(err) {
					t.Fatal("new empty root survived", err)
				}
			} else if err != nil {
				t.Fatal("pre-existing or occupied directory lost", err)
			}
			if mode == "unrelated" {
				raw, err := os.ReadFile(filepath.Join(setupPolicyRoot, "keep"))
				if err != nil || string(raw) != "unrelated" {
					t.Fatal("unrelated content lost", err)
				}
			}
			if _, err = runSetupInstanceAction(context.Background(), "rollback", r); err != nil {
				t.Fatal("rollback not repeatable", err)
			}
		})
	}
}

func TestSetupRejectsUnsafePolicyRootAndPreservesUnexpectedFiles(t *testing.T) {
	for _, mode := range []string{"symlink", "unsafe", "changed-file"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			old, oldRoot := instanceRuntimes, setupPolicyRoot
			instanceRuntimes = f.backend
			setupPolicyRoot = filepath.Join(t.TempDir(), "justvoxel")
			t.Cleanup(func() { instanceRuntimes = old; setupPolicyRoot = oldRoot })
			r := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint}
			switch mode {
			case "symlink":
				if err := os.Symlink(t.TempDir(), setupPolicyRoot); err != nil {
					t.Fatal(err)
				}
			case "unsafe":
				if err := os.Mkdir(setupPolicyRoot, 0777); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(setupPolicyRoot, 0777); err != nil {
					t.Fatal(err)
				}
			}
			_, err := runSetupInstanceAction(context.Background(), "validate", r)
			if mode != "changed-file" {
				if err == nil {
					t.Fatal("unsafe root accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			dir, err := f.backend.privateDirectory(setupPolicyRoot, true)
			if err != nil {
				t.Fatal(err)
			}
			dir.Close()
			path := filepath.Join(setupPolicyRoot, "justvoxel.conf")
			if err = f.backend.writePrivate(path, []byte("unexpected"), false); err != nil {
				t.Fatal(err)
			}
			if _, err = runSetupInstanceAction(context.Background(), "rollback", r); err == nil {
				t.Fatal("rollback removed unproven policy")
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != "unexpected" {
				t.Fatal(raw, err)
			}
		})
	}
}

func TestWritePrivateStillRejectsMissingParent(t *testing.T) {
	f := newInstanceActivationFixture(t)
	parent := filepath.Join(t.TempDir(), "missing")
	if err := f.backend.writePrivate(filepath.Join(parent, "policy"), []byte("value"), false); !os.IsNotExist(err) {
		t.Fatal("generic publication created parent", err)
	}
	if _, err := os.Lstat(parent); !os.IsNotExist(err) {
		t.Fatal("missing parent was created", err)
	}
}
