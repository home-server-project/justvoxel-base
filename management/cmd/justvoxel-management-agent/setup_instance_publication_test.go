package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The command fixture models only source behavior. It never invokes Podman,
// systemd, storage scripts, or the production runtime guard.
func setupProvisionalTestHelpers(t *testing.T, f *instanceActivationFixture) {
	t.Helper()
	oldUnits, oldTemplates, oldPermit := instanceBackupUnits, instanceBackupTemplates, setupInstancePermitRoot
	instanceBackupUnits = t.TempDir()
	instanceBackupTemplates = t.TempDir()
	setupInstancePermitRoot = filepath.Join(t.TempDir(), "permits")
	t.Cleanup(func() {
		instanceBackupUnits, instanceBackupTemplates, setupInstancePermitRoot = oldUnits, oldTemplates, oldPermit
	})
	for suffix, text := range map[string]string{"service": "[Service]\nExecStart=/example @@INSTANCE_ID@@\n", "timer": "[Timer]\nOnCalendar=*-*-* @@TIME@@:00\n"} {
		if err := os.WriteFile(filepath.Join(instanceBackupTemplates, "minecraft-instance-backup."+suffix+".in"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := f.backend.command
	f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
		if cmd == "podman" && len(args) == 4 && reflect.DeepEqual(args[:3], []string{"rm", "--ignore", "--"}) {
			id := strings.TrimPrefix(args[3], "minecraft-")
			if !instanceIDPattern.MatchString(id) {
				t.Fatal("invalid provisional removal", args)
			}
			f.calls = append(f.calls, cmd+" "+strings.Join(args, " "))
			delete(f.containers, id)
			return nil, nil
		}
		if cmd == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" {
			if len(args) != 3 || args[0] != "--instance" {
				t.Fatal(args)
			}
			f.calls = append(f.calls, cmd+" "+strings.Join(args, " "))
			return nil, nil
		}
		if cmd == "systemctl" && len(args) > 1 && strings.HasPrefix(args[len(args)-1], "minecraft-backup-") {
			f.calls = append(f.calls, cmd+" "+strings.Join(args, " "))
			return nil, nil
		}
		return base(ctx, cmd, args...)
	}
}
func newProvisionalSetupFixture(t *testing.T, add bool) (*instanceActivationFixture, setupRuntimeTransactionRequest) {
	t.Helper()
	f := newInstanceActivationFixture(t)
	setupProvisionalTestHelpers(t, f)
	if !add {
		f.backend.store = testMinecraftInstances(t)
		dir, err := f.backend.store.openDirectory(true)
		if err != nil {
			t.Fatal(err)
		}
		dir.Close()
		for _, c := range f.configs {
			dir, _, _ := f.backend.paths(c.ID)
			if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		// The generic rendering fixture includes a registry-only legacy server.
		// Add Server starts with fully committed logical runtimes instead.
		f.backend.store = testMinecraftInstances(t)
		for _, c := range f.configs {
			registerLogicalForTest(t, f.backend.store, strings.TrimPrefix(c.ID, "jv-"), c.Software, "Existing "+c.Software)
		}
		f.activateBoth()
	}
	old, oldPolicy, oldMarker := instanceRuntimes, setupPolicyRoot, setupInstanceMarker
	instanceRuntimes = f.backend
	setupPolicyRoot = filepath.Join(filepath.Dir(f.backend.root), "policy")
	setupInstanceMarker = filepath.Join(filepath.Dir(f.backend.root), "setup-marker")
	t.Cleanup(func() { instanceRuntimes, setupPolicyRoot, setupInstanceMarker = old, oldPolicy, oldMarker })
	r := setupRuntimeTransactionRequest{InstanceOnly: true, AddServer: add, SchemaVersion: operationSchemaVersion, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint,
		Server:    setupRuntimeServer{DisplayName: "Family", MOTD: "Family", MaxPlayers: 20, Timezone: "UTC"},
		Minecraft: setupRuntimeMinecraft{ServerType: "paper", GameMode: "survival", JavaMemory: "1G", ContainerMemory: "2G", JavaPort: instanceTestPort(t), ImageTag: "stable", VersionPolicy: "pinned", Version: "1.21.4", MinecraftUID: 1000, MinecraftGID: 1000},
		Storage:   setupRuntimeStorage{Type: "system", Path: filepath.Join(f.snapshot.StorageRoot, "minecraft")}, Backups: setupRuntimeBackups{Type: "system", Path: f.snapshot.BackupPath, Keep: 7, Schedule: "*-*-* 04:30:00"}}
	return f, r
}
func setupActions(t *testing.T, r setupRuntimeTransactionRequest, actions ...string) setupInstanceEvidence {
	t.Helper()
	for _, action := range actions {
		out, err := runSetupInstanceAction(context.Background(), action, r)
		if err != nil || !out.OK {
			t.Fatal(action, out, err)
		}
	}
	e, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func assertNoSetupDelete(t *testing.T, b *instanceRuntimeBackend, id string) {
	t.Helper()
	if _, err := os.Lstat(b.deletePath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Setup created ordinary Delete evidence", err)
	}
}
func TestProvisionalSetupPublicationAndAddServer(t *testing.T) {
	for _, add := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "add"}[add], func(t *testing.T) {
			f, r := newProvisionalSetupFixture(t, add)
			b := f.backend
			before, err := b.identities().listInstances("")
			if err != nil {
				t.Fatal(err)
			}
			if add && len(before) != len(f.configs) {
				t.Fatal("Add Server fixture is missing committed siblings", before)
			}
			siblings := map[string][]byte{}
			siblingStates := map[string]string{}
			siblingContainers := map[string]string{}
			siblingData := map[string]map[string]string{}
			for _, instance := range before {
				record, err := b.readRecord(instance.ID)
				if err != nil {
					t.Fatal("sibling has no committed runtime", err)
				}
				if err := b.verifyLive(record); err != nil {
					t.Fatal("sibling runtime is incomplete", err)
				}
				if err := b.storage(context.Background(), "validate", record); err != nil {
					t.Fatal("sibling storage is incomplete", err)
				}
				world := filepath.Join(record.Storage.DataPath, "world.dat")
				if err := os.WriteFile(world, []byte("existing world "+instance.ID), 0600); err != nil {
					t.Fatal(err)
				}
				siblingData[record.Storage.DataPath] = registrySnapshot(t, &minecraftInstanceStore{directory: record.Storage.DataPath})
				siblingStates[instance.ID] = f.states[instance.ID]
				siblingContainers[instance.ID] = f.containers[instance.ID]
				dir, env, q := b.paths(instance.ID)
				for _, p := range []string{filepath.Join(dir, "config.json"), env, q, filepath.Join(b.store.directory, instance.ID+".json"), filepath.Join(b.store.directory, "names", instance.ID+".json")} {
					raw, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					siblings[p] = raw
				}
			}
			assertSiblingsUnchanged := func() {
				t.Helper()
				for path, want := range siblings {
					got, err := os.ReadFile(path)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatal("sibling changed", path, err)
					}
				}
				for path, want := range siblingData {
					if got := registrySnapshot(t, &minecraftInstanceStore{directory: path}); !reflect.DeepEqual(got, want) {
						t.Fatal("sibling data changed", path)
					}
				}
				for id, state := range siblingStates {
					if f.states[id] != state || f.containers[id] != siblingContainers[id] {
						t.Fatal("sibling runtime state changed", id)
					}
				}
			}
			e := setupActions(t, r, "validate", "apply")
			id := e.Record.Runtime.ID
			if e.Schema != 2 || e.Publication.Stage != "runtime_prepared" || id == "" {
				t.Fatal(e)
			}
			for _, action := range []string{"apply", "verify"} {
				if action == "verify" {
					e = setupActions(t, r, "verify")
				}
				got, err := b.identities().listInstances("")
				if err != nil || !reflect.DeepEqual(got, before) {
					t.Fatal("provisional inventory", got, err)
				}
				if _, err = b.readRecord(id); !errors.Is(err, errInstanceNotFound) {
					t.Fatal("ordinary lifecycle accepted provisional instance", err)
				}
				assertSiblingsUnchanged()
			}
			e = setupActions(t, r, "commit")
			if e.Record.Runtime.ID != id || e.Publication.Stage != "finalized" {
				t.Fatal(e)
			}
			got, err := b.identities().listInstances("")
			if err != nil || len(got) != len(before)+1 {
				t.Fatal(got, err)
			}
			remaining := []minecraftRegisteredInstance{}
			published := false
			for _, instance := range got {
				if instance.ID == id {
					published = true
				} else {
					remaining = append(remaining, instance)
				}
			}
			if !published || !reflect.DeepEqual(remaining, before) {
				t.Fatal("publication changed sibling inventory or exposed another ID", got)
			}
			names, err := b.identities().readNames()
			if err != nil || names[id] != (minecraftInstanceNameRecord{ID: id, DisplayName: "Family", Software: "paper"}) {
				t.Fatal(names, err)
			}
			if record, err := b.readRecord(id); err != nil || !reflect.DeepEqual(record, e.Record) || b.verifyLive(record) != nil {
				t.Fatal("published runtime changed", err)
			}
			assertSiblingsUnchanged()
			if _, err = os.Lstat(setupPermitPath(id)); !os.IsNotExist(err) {
				t.Fatal("permit survived", err)
			}
			assertNoSetupDelete(t, b, id)
		})
	}
}
func TestProvisionalSetupFailureRollbackNeverDeletesPublishedSibling(t *testing.T) {
	f, r := newProvisionalSetupFixture(t, true)
	b := f.backend
	e := setupActions(t, r, "validate", "apply")
	id := e.Record.Runtime.ID
	before, err := b.identities().listInstances("")
	if err != nil {
		t.Fatal(err)
	}
	// Inject failure after preparation, before publication. Recovery uses only
	// operation evidence and never adopts a normal Delete journal.
	base := b.command
	b.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
		if cmd == "systemctl" && len(args) > 1 && args[0] == "show" && strings.Contains(args[len(args)-1], id) {
			return nil, errors.New("injected after preparation")
		}
		return base(ctx, cmd, args...)
	}
	if _, err := runSetupInstanceAction(context.Background(), "verify", r); err == nil {
		t.Fatal("failure not injected")
	}
	b.command = base
	setupActions(t, r, "rollback", "rollback")
	got, err := b.identities().listInstances("")
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("sibling registry changed", got, err)
	}
	dir, env, q := b.paths(id)
	for _, p := range []string{dir, env, q, e.Record.Storage.DataPath, filepath.Join(b.store.directory, id+".json")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatal("provisional resource survived", p, err)
		}
	}
	assertNoSetupDelete(t, b, id)
}
func prepareSetupCommitCrash(t *testing.T, f *instanceActivationFixture, r setupRuntimeTransactionRequest) setupInstanceEvidence {
	t.Helper()
	e := setupActions(t, r, "validate", "apply", "verify")
	save := func() error { return f.backend.writeJSON(setupInstancePath(r.OperationID), e, true) }
	if err := f.backend.prepareSetupBackups(context.Background(), &e, save); err != nil {
		t.Fatal(err)
	}
	e.Publication.Stage = "commit_ready"
	if err := save(); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestSetupCrashPublicationCommitsForwardWithoutStorageRollback(t *testing.T) {
	for _, mode := range []string{"name-only", "identity-published", "evidence-published", "finalized"} {
		t.Run(mode, func(t *testing.T) {
			f, r := newProvisionalSetupFixture(t, false)
			b := f.backend
			e := prepareSetupCommitCrash(t, f, r)
			id := e.Record.Runtime.ID
			if mode == "name-only" {
				d, err := b.identities().openNames(true)
				if err != nil {
					t.Fatal(err)
				}
				err = b.identities().publishRegistryRecord(d, id+".json", ".name-*", minecraftInstanceNameRecord{ID: id, DisplayName: e.Publication.Name, Software: e.Record.Runtime.Software}, false)
				d.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err := b.identities().publishSetupInstance(id, e.Publication.Name, e.Record.Runtime.Software); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "evidence-published" || mode == "finalized" {
				e.Publication.Stage = map[string]string{"evidence-published": "published", "finalized": "finalized"}[mode]
				if err := b.writeJSON(setupInstancePath(r.OperationID), e, true); err != nil {
					t.Fatal(err)
				}
			}
			dir, env, q := b.paths(id)
			preserved := map[string][]byte{}
			for _, p := range []string{filepath.Join(dir, "config.json"), env, q} {
				raw, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				preserved[p] = raw
			}
			oldStorage := runAdminSetupStorageTransactionHelper
			defer func() { runAdminSetupStorageTransactionHelper = oldStorage }()
			runAdminSetupStorageTransactionHelper = func(context.Context, string, []byte) ([]byte, error) {
				t.Fatal("commit-forward entered storage rollback")
				return nil, nil
			}
			// Use an actual recovery journal with matching immutable operation binding.
			_, store, op := setupRecoveryOperation(t, operationNeedsAttention, "runtime_rollback")
			e.Request.OperationID, e.Request.PlanFingerprint = op.OperationID, op.PlanFingerprint
			if err := b.writeJSON(setupInstancePath(op.OperationID), e, true); err != nil {
				t.Fatal(err)
			}
			if _, err := store.transition(op.OperationID, operationRollingBack, "runtime_recovery", "Retrying setup recovery."); err != nil {
				t.Fatal(err)
			}
			// Rebind the marker exactly as it would be persisted by this operation.
			if err := b.writePrivate(setupInstanceMarker, []byte(op.OperationID+" "+op.PlanFingerprint+"\n"), true); err != nil {
				t.Fatal(err)
			}
			op.Stage = "runtime_rollback"
			result := recoverSetupTransaction(context.Background(), store, op)
			if result.State != operationSucceeded {
				t.Fatal(result)
			}
			for p, want := range preserved {
				raw, err := os.ReadFile(p)
				if err != nil || !reflect.DeepEqual(raw, want) {
					t.Fatal("published runtime erased", p, err)
				}
			}
			if _, err := os.Stat(e.Record.Storage.DataPath); err != nil {
				t.Fatal("published data erased", err)
			}
			if _, err := b.readRecord(id); err != nil {
				t.Fatal("published identity erased", err)
			}
			assertNoSetupDelete(t, b, id)
			if f.containers[id] != "running" {
				t.Fatal("published container interrupted")
			}
		})
	}
}
func TestSetupPublicationMismatchFailsClosed(t *testing.T) {
	for _, mode := range []string{"name", "software", "runtime", "placement", "identity", "delete-conflict"} {
		t.Run(mode, func(t *testing.T) {
			f, r := newProvisionalSetupFixture(t, false)
			b := f.backend
			e := prepareSetupCommitCrash(t, f, r)
			id := e.Record.Runtime.ID
			if err := b.identities().publishSetupInstance(id, e.Publication.Name, e.Record.Runtime.Software); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "name", "software":
				names, err := b.identities().openNames(false)
				if err != nil {
					t.Fatal(err)
				}
				n := minecraftInstanceNameRecord{ID: id, DisplayName: e.Publication.Name, Software: e.Record.Runtime.Software}
				if mode == "name" {
					n.DisplayName = "Wrong"
				} else {
					n.Software = "vanilla"
				}
				err = b.identities().publishRegistryRecord(names, id+".json", ".name-*", n, true)
				names.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "runtime", "placement":
				record := e.Record
				if mode == "runtime" {
					record.Runtime.MOTD = "Wrong"
				} else {
					record.Storage.UUID = "wrong"
				}
				dir, _, _ := b.paths(id)
				if err := b.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
					t.Fatal(err)
				}
			case "identity":
				if err := b.writeJSON(filepath.Join(b.store.directory, id+".json"), minecraftInstanceRecord{ID: "jv-wrong1"}, true); err != nil {
					t.Fatal(err)
				}
			case "delete-conflict":
				if err := b.writeJSON(b.deletePath(id), map[string]string{"id": id}, false); err != nil {
					t.Fatal(err)
				}
			}
			f.calls = nil
			out, err := runSetupInstanceAction(context.Background(), "rollback", r)
			if err == nil || out.OK {
				t.Fatal("mismatch adopted", out, err)
			}
			for _, call := range f.calls {
				if strings.Contains(call, "podman rm") || strings.Contains(call, "systemctl stop") || strings.Contains(call, "systemctl --no-block stop") {
					t.Fatal("mismatch destroyed resources", call)
				}
			}
			for _, p := range []string{e.Record.Storage.DataPath, filepath.Join(b.store.directory, id+".json")} {
				if _, err := os.Stat(p); err != nil {
					t.Fatal("mismatch erased evidence", p, err)
				}
			}
		})
	}
}

func TestSetupPermitBindsImmutableOperationEvidence(t *testing.T) {
	f, r := newProvisionalSetupFixture(t, false)
	b := f.backend
	e := setupActions(t, r, "validate", "apply")
	if err := b.permitSetupStart(e); err != nil {
		t.Fatal(err)
	}
	raw, err := b.readPrivate(setupPermitPath(e.Record.Runtime.ID))
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Schema      int    `json:"schema"`
		ID          string `json:"id"`
		Operation   string `json:"operation_id"`
		Fingerprint string `json:"plan_fingerprint"`
		Evidence    string `json:"evidence_sha256"`
		PID         int    `json:"agent_pid"`
		Start       string `json:"agent_start"`
	}
	if err = instanceDecode(raw, &p); err != nil {
		t.Fatal(err)
	}
	evidence, err := b.readPrivate(setupInstancePath(r.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	if p.Schema != 1 || p.ID != e.Record.Runtime.ID || p.Operation != r.OperationID || p.Fingerprint != r.PlanFingerprint || p.Evidence != instanceHash(evidence) || p.PID != os.Getpid() || p.Start == "" {
		t.Fatal("unbound Setup permit", p)
	}
	if err = b.removePrivate(setupPermitPath(e.Record.Runtime.ID)); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"legacy", "rolled-back", "verified"} {
		candidate := e
		publication := *e.Publication
		candidate.Publication = &publication
		switch mode {
		case "legacy":
			candidate.Schema = 1
		case "rolled-back":
			candidate.RolledBack = true
		case "verified":
			candidate.Publication.Stage = "verified"
		}
		if err = b.permitSetupStart(candidate); err == nil {
			t.Fatal("invalid evidence permitted startup", mode)
		}
	}
}
func TestSetupRetainedNameMismatchCannotBeAdoptedOrDeleted(t *testing.T) {
	f, r := newProvisionalSetupFixture(t, false)
	b := f.backend
	e := prepareSetupCommitCrash(t, f, r)
	id := e.Record.Runtime.ID
	d, err := b.identities().openNames(true)
	if err != nil {
		t.Fatal(err)
	}
	err = b.identities().publishRegistryRecord(d, id+".json", ".name-*", minecraftInstanceNameRecord{ID: id, DisplayName: "Other", Software: e.Record.Runtime.Software}, false)
	d.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"commit", "rollback"} {
		if out, err := runSetupInstanceAction(context.Background(), action, r); err == nil || out.OK {
			t.Fatal("retained name adopted", action, out, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(b.store.directory, id+".json")); !os.IsNotExist(err) {
		t.Fatal("identity invented", err)
	}
	if _, err := os.Stat(e.Record.Storage.DataPath); err != nil {
		t.Fatal("mismatch deleted data", err)
	}
	assertNoSetupDelete(t, b, id)
}
func TestSetupSchemaOneEvidenceRetainsLegacyRecoveryContract(t *testing.T) {
	d := newDeleteFixture(t)
	r := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: "12345678-1234-4123-8123-123456789abc", PlanFingerprint: testSetupFingerprint}
	original := bindSetupRuntimeFixture(t, d.f, r)
	loaded, err := setupInstanceRead(r.OperationID, r.PlanFingerprint)
	if err != nil || loaded.Schema != 1 || loaded.Publication != nil || !reflect.DeepEqual(loaded, original) {
		t.Fatal("legacy evidence reinterpreted", loaded, err)
	}
	out, err := runSetupInstanceAction(context.Background(), "rollback", r)
	if err != nil || !out.OK || out.Phase != "runtime_rolled_back" || !d.removed {
		t.Fatal("legacy compatibility path lost", out, err)
	}
}
