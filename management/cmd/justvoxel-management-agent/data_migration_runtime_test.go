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

const migrationRuntimeTestID = "f06104cc-06b8-49d2-9061-6431a3a47e57"

type physicalMigrationFixture struct {
	b        *instanceRuntimeBackend
	snapshot minecraftInstanceRuntimeContext
	states   map[string]string
	calls    []string
	records  []instanceRuntimeRecord
}

// File/model fixture: no host services, containers, mounts or listening sockets.
func physicalMigrationTestFixture(t *testing.T, n int) *physicalMigrationFixture {
	t.Helper()
	store, snapshot, configs := instanceRuntimeFixture(t)
	for _, c := range configs[n:] {
		if err := os.Remove(filepath.Join(store.directory, c.ID+".json")); err != nil {
			t.Fatal(err)
		}
	}
	configs = configs[:n]
	root := t.TempDir()
	x := &physicalMigrationFixture{snapshot: snapshot, states: map[string]string{}}
	x.b = &instanceRuntimeBackend{store: store, root: filepath.Join(root, "runtime"), quadlets: filepath.Join(root, "quadlets"), staging: filepath.Join(root, "staging")}
	for _, p := range []string{x.b.root, x.b.quadlets, x.b.staging} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	oldRecovery, oldPermits := dataMigrationRecoveryRoot, dataMigrationPermitRoot
	dataMigrationRecoveryRoot = t.TempDir()
	dataMigrationPermitRoot = t.TempDir()
	oldBackup, oldBackend := instanceBackupStorageCommand, instanceRuntimes
	instanceRuntimes = x.b
	instanceBackupStorageCommand = func(_ context.Context, command string, args ...string) ([]byte, error) { return []byte(`{}`), nil }
	t.Cleanup(func() {
		dataMigrationRecoveryRoot = oldRecovery
		dataMigrationPermitRoot = oldPermits
		instanceBackupStorageCommand = oldBackup
		instanceRuntimes = oldBackend
	})
	x.b.snapshot = func(context.Context) (minecraftInstanceRuntimeContext, uint32, uint32, error) {
		return x.snapshot, 1000, 1000, nil
	}
	x.b.command = func(_ context.Context, command string, args ...string) ([]byte, error) {
		call := command + " " + strings.Join(args, " ")
		x.calls = append(x.calls, call)
		if command == "nsenter" {
			if len(args) != 12 {
				return nil, errors.New("unexpected storage arguments")
			}
			_, e := os.Stat(args[6])
			return nil, e
		}
		if command == "env" {
			if len(args) == 3 && strings.HasPrefix(args[0], "QUADLET_UNIT_DIRS=") && args[2] == "--dryrun" {
				units := []string{}
				for _, r := range x.records {
					units = append(units, "minecraft-"+r.Runtime.ID+".service")
				}
				return []byte(strings.Join(units, "\n")), nil
			}
			if len(args) != 5 || args[2] != "--instance" {
				t.Fatalf("unexpected verifier: %v", args)
			}
			return nil, nil
		}
		if command == "systemctl" {
			if reflect.DeepEqual(args, []string{"show", "--property=ActiveState", "--value", "--", "minecraft.service"}) {
				return []byte("inactive\n"), nil
			}
			if reflect.DeepEqual(args, []string{"daemon-reload"}) {
				return nil, nil
			}
			id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "minecraft-"), ".service")
			state, ok := x.states[id]
			if !ok {
				return nil, errors.New("foreign unit")
			}
			switch args[0] {
			case "show":
				active := "inactive"
				if state == "running" {
					active = "active"
				}
				return []byte("LoadState=loaded\nActiveState=" + active + "\nSubState=mock\n"), nil
			case "stop":
				x.states[id] = "stopped"
				return nil, nil
			case "start":
				x.states[id] = "running"
				return nil, nil
			}
		}
		if command == "podman" {
			switch args[0] {
			case "ps":
				filter := args[3]
				if filter == "name=^minecraft$" {
					return nil, nil
				}
				id := strings.TrimSuffix(strings.TrimPrefix(filter, "name=^minecraft-"), "$")
				if _, ok := x.states[id]; !ok {
					return nil, errors.New("foreign container")
				}
				return []byte("minecraft-" + id + "\n"), nil
			case "inspect":
				id := strings.TrimPrefix(args[len(args)-1], "minecraft-")
				if x.states[id] == "running" {
					return []byte("running 0"), nil
				}
				return []byte("exited 0"), nil
			case "exec":
				return []byte("There are 0 of a max of 20 players online:"), nil
			}
		}
		return nil, errors.New("unexpected command: " + call)
	}
	if n > 0 {
		files, err := store.renderInstanceRuntimes(snapshot, configs)
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range configs {
			dir, env, q := x.b.paths(c.ID)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			r := instanceRuntimeRecord{SchemaVersion: 1, Runtime: c, Storage: instanceStorageFor(snapshot, c.ID), EnvironmentSHA256: instanceHash(files[i].Environment), QuadletSHA256: instanceHash(files[i].Quadlet), Firewall: []instanceFirewallRule{}}
			if err = x.b.writePrivate(env, files[i].Environment, false); err != nil {
				t.Fatal(err)
			}
			if err = x.b.writePrivate(q, files[i].Quadlet, false); err != nil {
				t.Fatal(err)
			}
			if err = x.b.writeJSON(filepath.Join(dir, "config.json"), r, false); err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(r.Storage.DataPath, 0700); err != nil {
				t.Fatal(err)
			}
			x.records = append(x.records, r)
			x.states[c.ID] = "stopped"
		}
	}
	if err := os.Mkdir(filepath.Join(dataMigrationRecoveryRoot, "data-migration-"+migrationRuntimeTestID), 0700); err != nil {
		t.Fatal(err)
	}
	return x
}
func TestPhysicalMigrationZeroInstancesRefused(t *testing.T) {
	x := physicalMigrationTestFixture(t, 0)
	if _, _, err := x.b.migrationParticipants(context.Background()); err == nil {
		t.Fatal("empty appliance accepted")
	}
}
func TestPhysicalMigrationAllInstanceStates(t *testing.T) {
	for _, states := range [][]string{{"running"}, {"stopped"}, {"running", "stopped"}, {"running", "running"}} {
		t.Run(strings.Join(states, "-"), func(t *testing.T) {
			x := physicalMigrationTestFixture(t, len(states))
			ctx := context.Background()
			for i, s := range states {
				x.states[x.records[i].Runtime.ID] = s
			}
			ps, _, err := x.b.migrationParticipants(ctx)
			if err != nil || len(ps) != len(states) {
				t.Fatalf("participants: %v %v", ps, err)
			}
			if err = x.b.captureMigration(ctx, migrationRuntimeTestID, ps); err != nil {
				t.Fatal(err)
			}
			j, err := x.b.loadMigration(migrationRuntimeTestID)
			if err != nil {
				t.Fatal(err)
			}
			if err = x.b.quiesceMigration(ctx, j, false); err != nil {
				t.Fatal(err)
			}
			for id, s := range x.states {
				if s != "stopped" {
					t.Fatalf("not quiesced: %s", id)
				}
			}
			x.snapshot.StorageRoot = filepath.Join(t.TempDir(), "destination")
			if err = os.Mkdir(x.snapshot.StorageRoot, 0700); err != nil {
				t.Fatal(err)
			}
			for _, r := range x.records {
				if err = os.MkdirAll(instanceStorageFor(x.snapshot, r.Runtime.ID).DataPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err = x.b.relocateMigration(ctx, &j); err != nil {
				t.Fatal(err)
			}
			// Restart the backend from disk evidence after publication, not selected ID.
			reloaded, err := x.b.loadMigration(migrationRuntimeTestID)
			if err != nil {
				t.Fatal(err)
			}
			if err = x.b.restoreMigrationStates(ctx, reloaded); err != nil {
				t.Fatal(err)
			}
			for i, original := range x.records {
				r, err := x.b.readRecord(original.Runtime.ID)
				if err != nil {
					t.Fatal(err)
				}
				if x.states[r.Runtime.ID] != states[i] || !reflect.DeepEqual(r.Runtime, original.Runtime) || r.Storage != instanceStorageFor(x.snapshot, r.Runtime.ID) {
					t.Fatal("sibling identity/configuration/state changed")
				}
			}
			if err = x.b.finishMigration(reloaded); err != nil {
				t.Fatal(err)
			}
			for _, call := range x.calls {
				if (strings.Contains(call, "minecraft.service") && !strings.Contains(call, "show --property=ActiveState")) || strings.Contains(call, "web-status-json") {
					t.Fatalf("fixed execution: %s", call)
				}
			}
		})
	}
}
func TestPhysicalMigrationStaleIdentityAndSiblingMismatch(t *testing.T) {
	for _, drift := range []string{"state", "configuration", "registry", "wrong-journal", "foreign-marker"} {
		t.Run(drift, func(t *testing.T) {
			x := physicalMigrationTestFixture(t, 2)
			ctx := context.Background()
			ps, _, err := x.b.migrationParticipants(ctx)
			if err != nil {
				t.Fatal(err)
			}
			id := x.records[1].Runtime.ID
			switch drift {
			case "state":
				x.states[id] = "running"
			case "configuration":
				r := x.records[1]
				r.Runtime.MOTD = "changed"
				dir, _, _ := x.b.paths(id)
				if err = x.b.writeJSON(filepath.Join(dir, "config.json"), r, true); err != nil {
					t.Fatal(err)
				}
			case "registry":
				if err = os.WriteFile(filepath.Join(x.b.store.directory, id+".json"), []byte(`{"id":"jv-ffffff"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-journal":
				if err = x.b.captureMigration(ctx, migrationRuntimeTestID, ps); err != nil {
					t.Fatal(err)
				}
				j, err := x.b.loadMigration(migrationRuntimeTestID)
				if err != nil {
					t.Fatal(err)
				}
				j.Entries[1].Participant.ID = ps[0].ID
				if err = x.b.saveMigration(j); err != nil {
					t.Fatal(err)
				}
				if _, err = x.b.loadMigration(migrationRuntimeTestID); err == nil {
					t.Fatal("reconnected wrong ID")
				}
				return
			case "foreign-marker":
				if err = x.b.writeJSON(x.b.migrationMarker(id), map[string]any{"schema": 1, "id": id, "operation_id": "foreign"}, false); err != nil {
					t.Fatal(err)
				}
			}
			if err = x.b.captureMigration(ctx, migrationRuntimeTestID, ps); err == nil {
				t.Fatal("stale/sibling evidence accepted")
			}
		})
	}
}
func TestPhysicalMigrationPartialPublicationRollback(t *testing.T) {
	x := physicalMigrationTestFixture(t, 2)
	ctx := context.Background()
	x.states[x.records[0].Runtime.ID] = "running"
	ps, _, err := x.b.migrationParticipants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = x.b.captureMigration(ctx, migrationRuntimeTestID, ps); err != nil {
		t.Fatal(err)
	}
	j, err := x.b.loadMigration(migrationRuntimeTestID)
	if err != nil {
		t.Fatal(err)
	}
	if err = x.b.quiesceMigration(ctx, j, false); err != nil {
		t.Fatal(err)
	}
	old := x.snapshot
	x.snapshot.StorageRoot = filepath.Join(t.TempDir(), "new")
	if err = os.Mkdir(x.snapshot.StorageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	for _, r := range x.records {
		if err = os.MkdirAll(instanceStorageFor(x.snapshot, r.Runtime.ID).DataPath, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = x.b.relocateMigration(ctx, &j); err != nil {
		t.Fatal(err)
	}
	// Model a crash between environment and config publication for a sibling.
	_, env, _ := x.b.paths(j.Entries[1].Participant.ID)
	if err = x.b.writePrivate(env, j.Entries[1].Environment, true); err != nil {
		t.Fatal(err)
	}
	x.snapshot = old
	if err = x.b.publishMigration(ctx, j, true); err != nil {
		t.Fatal(err)
	}
	if err = x.b.restoreMigrationStates(ctx, j); err != nil {
		t.Fatal(err)
	}
	for _, r := range x.records {
		actual, err := x.b.readRecord(r.Runtime.ID)
		if err != nil || !reflect.DeepEqual(actual, r) {
			t.Fatal("rollback changed sibling runtime")
		}
	}
}
func TestPhysicalMigrationLegacyEvidenceReadOnly(t *testing.T) {
	x := physicalMigrationTestFixture(t, 1)
	if err := x.b.writePrivate(x.b.migrationJournalPath(migrationRuntimeTestID), []byte(`{"schema_version":"v1","phase":"switched"}`), false); err != nil {
		t.Fatal(err)
	}
	if _, err := x.b.loadMigration(migrationRuntimeTestID); err == nil {
		t.Fatal("legacy evidence adopted a current instance")
	}
	if len(x.calls) != 0 {
		t.Fatal("legacy read-only review performed runtime actions")
	}
}

func TestPhysicalMigrationUnmanagedFixedRuntimeRefused(t *testing.T) {
	for _, kind := range []string{"unit", "container"} {
		t.Run(kind, func(t *testing.T) {
			x := physicalMigrationTestFixture(t, 2)
			base := x.b.command
			x.b.command = func(ctx context.Context, c string, args ...string) ([]byte, error) {
				call := strings.Join(args, " ")
				if kind == "unit" && c == "systemctl" && strings.Contains(call, "minecraft.service") {
					return []byte("active\n"), nil
				}
				if kind == "container" && c == "podman" && strings.Contains(call, "name=^minecraft$") {
					return []byte("minecraft\n"), nil
				}
				return base(ctx, c, args...)
			}
			if _, _, err := x.b.migrationParticipants(context.Background()); err == nil {
				t.Fatal("unmanaged fixed runtime accepted")
			}
		})
	}
}
