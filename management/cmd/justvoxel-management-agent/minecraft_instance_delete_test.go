package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type deleteFixture struct {
	f           *instanceActivationFixture
	failRemove  bool
	backupState string
	removed     bool
}

func newDeleteFixture(t *testing.T) *deleteFixture {
	t.Helper()
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	d := &deleteFixture{f: f, backupState: "inactive"}
	oldUnits := instanceBackupUnits
	instanceBackupUnits = t.TempDir()
	t.Cleanup(func() { instanceBackupUnits = oldUnits })
	id := f.configs[0].ID
	for _, selected := range []string{id, f.configs[1].ID} {
		for _, suffix := range []string{"timer", "service"} {
			if e := os.WriteFile(filepath.Join(instanceBackupUnits, instanceBackupUnit(selected, suffix)), []byte("unit fixture"), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	base := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "nsenter" && len(args) > 3 && args[2] == instanceDeleteHelper {
			if args[4] != id {
				t.Fatal("wrong deletion target", args)
			}
			if args[3] == "inspect" {
				return []byte(`{"identity":"1:2"}`), nil
			}
			if args[3] != "remove" {
				t.Fatal(args)
			}
			if d.failRemove {
				return nil, errors.New("private /host/world credential=secret")
			}
			if _, e := os.Stat(filepath.Join(f.backend.store.directory, id+".json")); e != nil && !d.removed {
				t.Fatal("identity removed before verified data removal", e)
			}
			_, env, q := f.backend.paths(id)
			for _, p := range []string{env, q, filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "timer")), filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "service"))} {
				if _, e := os.Lstat(p); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("runtime not retired first", p)
				}
			}
			if e := os.RemoveAll(instanceStorageFor(f.snapshot, id).DataPath); e != nil {
				t.Fatal(e)
			}
			d.removed = true
			return nil, nil
		}
		if command == "systemctl" && len(args) > 1 && args[0] == "show" && args[1] == "--property=Job" {
			return []byte("0\n"), nil
		}
		if command == "systemctl" && len(args) > 1 && strings.Contains(strings.Join(args, " "), "minecraft-backup-") {
			if args[0] == "show" {
				return []byte("LoadState=loaded\nActiveState=" + d.backupState + "\nJob=0\n"), nil
			}
			if args[0] == "disable" {
				return nil, nil
			}
			t.Fatal("unexpected backup mutation", args)
		}
		if command == "podman" && len(args) > 0 && args[0] == "rm" {
			if strings.Contains(strings.Join(args, " "), "force") {
				t.Fatal("force removal")
			}
			delete(f.containers, id)
			return nil, nil
		}
		return base(ctx, command, args...)
	}
	return d
}
func TestInstanceDeleteExactScopeAndRetainedIdentity(t *testing.T) {
	d := newDeleteFixture(t)
	f, b := d.f, d.f.backend
	id := f.configs[0].ID
	sibling := f.configs[1].ID
	namesBefore, e := b.readPrivate(filepath.Join(b.store.directory, "names", id+".json"))
	if e != nil {
		t.Fatal(e)
	}
	_, siblingEnv, siblingQuadlet := b.paths(sibling)
	preserved := []string{filepath.Join(b.store.directory, "minecraft.json"), filepath.Join(b.store.directory, sibling+".json"), siblingEnv, siblingQuadlet, filepath.Join(instanceBackupUnits, instanceBackupUnit(sibling, "timer")), filepath.Join(instanceBackupUnits, instanceBackupUnit(sibling, "service"))}
	for _, path := range []string{filepath.Join(f.snapshot.StorageRoot, "minecraft", "world"), filepath.Join(f.snapshot.BackupPath, id, "archive.tar.gz"), filepath.Join(f.snapshot.BackupPath, id, "archive.json"), filepath.Join(instanceStorageFor(f.snapshot, sibling).DataPath, "world")} {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte("preserved"), 0600); e != nil {
			t.Fatal(e)
		}
		preserved = append(preserved, path)
	}
	before := map[string]string{}
	for _, path := range preserved {
		v, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		before[path] = string(v)
	}
	if e = b.deleteInstance(context.Background(), id, "JV Paper"); e != nil {
		t.Fatal(e)
	}
	if !d.removed {
		t.Fatal("data not removed")
	}
	for _, path := range []string{instanceStorageFor(f.snapshot, id).DataPath, filepath.Join(b.root, id), filepath.Join(b.store.directory, id+".json"), b.deletePath(id)} {
		if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("selected state retained", path, e)
		}
	}
	for path, want := range before {
		v, e := os.ReadFile(path)
		if e != nil || string(v) != want {
			t.Fatal("preserved state changed", path, e)
		}
	}
	namesAfter, e := b.readPrivate(filepath.Join(b.store.directory, "names", id+".json"))
	if e != nil || string(namesBefore) != string(namesAfter) {
		t.Fatal("historical name lost")
	}
	active, e := b.store.listInstances("paper")
	if e != nil {
		t.Fatal(e)
	}
	for _, i := range active {
		if i.ID == id {
			t.Fatal("deleted ID still active")
		}
	}
	if _, e = b.store.registerLogicalInstance(strings.TrimPrefix(id, "jv-"), true, "paper", "New server", ""); !errors.Is(e, errInstanceDuplicate) {
		t.Fatal("retained ID reused", e)
	}
}
func TestInstanceDeleteRefusesBeforeDurableIntent(t *testing.T) {
	for _, scenario := range []string{"invalid", "legacy", "wrong_name", "stale_name", "running", "starting", "stopping", "unknown", "backup_active", "backup_job", "backup_recovery", "activation_recovery", "schedule_recovery", "restore_recovery"} {
		t.Run(scenario, func(t *testing.T) {
			d := newDeleteFixture(t)
			f, b := d.f, d.f.backend
			id, name := f.configs[0].ID, "JV Paper"
			switch scenario {
			case "invalid":
				id = "../legacy"
			case "legacy":
				id = "jv-legacy1"
			case "wrong_name":
				name = "Other"
			case "stale_name":
				if _, e := b.store.renameInstance(id, "Renamed", ""); e != nil {
					t.Fatal(e)
				}
			case "running":
				f.states[id] = "active"
				f.containers[id] = "running"
			case "starting":
				f.states[id] = "activating"
			case "stopping":
				f.states[id] = "deactivating"
			case "unknown":
				f.states[id] = "unknown"
			case "backup_active":
				d.backupState = "active"
			case "backup_job":
				base := b.command
				b.command = func(ctx context.Context, c string, a ...string) ([]byte, error) {
					if c == "systemctl" && strings.Contains(strings.Join(a, " "), "minecraft-backup-") {
						return []byte("LoadState=loaded\nActiveState=inactive\nJob=123\n"), nil
					}
					return base(ctx, c, a...)
				}
			case "backup_recovery":
				if e := b.writeJSON(b.backupPath(id, "status"), instanceBackupStatus{State: "failed", RestartRequired: true}, false); e != nil {
					t.Fatal(e)
				}
			case "activation_recovery":
				dir, _, _ := b.paths(id)
				if e := b.writeJSON(filepath.Join(dir, "pending.json"), map[string]any{}, false); e != nil {
					t.Fatal(e)
				}
			case "schedule_recovery":
				if e := b.writeJSON(b.backupPath(id, "schedule-pending"), map[string]any{}, false); e != nil {
					t.Fatal(e)
				}
			case "restore_recovery":
				dir, _, _ := b.paths(id)
				if e := b.writeJSON(filepath.Join(dir, "restore-journal.json"), map[string]any{}, false); e != nil {
					t.Fatal(e)
				}
			}
			if e := b.deleteInstance(context.Background(), id, name); e == nil {
				t.Fatal("unsafe deletion accepted")
			}
			if d.removed {
				t.Fatal("world removal before refusal")
			}
			if _, e := os.Stat(b.deletePath(f.configs[0].ID)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("intent published on failed preflight")
			}
		})
	}
}
func TestInstanceDeleteInterruptedRetrySameID(t *testing.T) {
	d := newDeleteFixture(t)
	b := d.f.backend
	id := d.f.configs[0].ID
	d.failRemove = true
	if e := b.deleteInstance(context.Background(), id, "JV Paper"); e == nil {
		t.Fatal("interruption reported success")
	}
	j, e := b.deleteJournal(id)
	if e != nil || j.Stage != "runtime_retired" {
		t.Fatal("retry evidence missing", j.Stage, e)
	}
	if _, e := os.Stat(filepath.Join(b.store.directory, id+".json")); e != nil {
		t.Fatal("identity retired early")
	}
	if e := b.deleteInstance(context.Background(), d.f.configs[1].ID, "JV Vanilla"); e == nil {
		t.Fatal("retry affected another ID")
	}
	if e := b.deleteInstance(context.Background(), id, "Changed name"); e == nil {
		t.Fatal("retry bypassed confirmation")
	}
	d.failRemove = false
	if e := b.deleteInstance(context.Background(), id, "JV Paper"); e != nil {
		t.Fatal(e)
	}
}
func TestInstanceDeleteAuthorizationAndStrictJSON(t *testing.T) {
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		r := surfaceRequest("POST", "/v1/minecraft/instances/jv-a1b2c3/delete", `{"confirm_name":"JV Paper"}`)
		r.SetPathValue("id", "jv-a1b2c3")
		w := httptest.NewRecorder()
		s.instanceDelete(w, r)
		if w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	// A valid request reaches the busy gate; every malformed request must be
	// rejected first, even while another operation owns mutation exclusion.
	instanceBackupWorkerMu.Lock()
	defer instanceBackupWorkerMu.Unlock()
	for _, body := range []string{
		`{"confirm_name":"JV Paper","path":"/private"}`,
		`{"confirm_name":"JV Paper"} {}`,
		`{}`, `{"confirm_name":null}`, `{"confirm_name":4}`, `{"confirm_name":true}`, `{"confirm_name":[]}`,
		`{"confirm_name":"JV Paper"} true`, `{"confirm_name":"JV Paper"} trailing`,
		`{"confirm_name":"JV Paper"} !`, `null`, `[]`, `"name"`, ``, `{`,
		`{"confirm_name":"` + strings.Repeat("a", 16*1024) + `"}`,
		`{"confirm_name":"JV Paper"}` + strings.Repeat(" ", 16*1024),
	} {
		t.Run(body[:min(len(body), 80)], func(t *testing.T) {
			s := surfaceTestServer(t, roleAdministrator)
			r := surfaceRequest("POST", "/", body)
			r.SetPathValue("id", "jv-a1b2c3")
			w := httptest.NewRecorder()
			s.instanceDelete(w, r)
			if w.Code != 400 {
				t.Fatal("malformed request reached mutation gate", w.Code, w.Body.String())
			}
		})
	}
	for _, body := range []string{`{"confirm_name":"JV Paper"}`, "{\"confirm_name\":\"JV Paper\"} \n\t"} {
		s := surfaceTestServer(t, roleAdministrator)
		r := surfaceRequest("POST", "/", body)
		r.SetPathValue("id", "jv-a1b2c3")
		w := httptest.NewRecorder()
		s.instanceDelete(w, r)
		if w.Code != 409 {
			t.Fatal("valid request did not reach mutation gate", w.Code, w.Body.String())
		}
	}
}
func TestInstanceDeleteLockConflictDoesNotMutate(t *testing.T) {
	d := newDeleteFixture(t)
	old := instanceRuntimes
	instanceRuntimes = d.f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	store.mu.Lock()
	defer store.mu.Unlock()
	r := surfaceRequest("POST", "/", `{"confirm_name":"JV Paper"}`)
	r.SetPathValue("id", d.f.configs[0].ID)
	w := httptest.NewRecorder()
	s.instanceDelete(w, r)
	if w.Code != 409 || d.removed {
		t.Fatal("lock contention mutated selected server", w.Code)
	}
	for _, secret := range []string{"/host/", "credential=", "rcon_password"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private details leaked")
		}
	}
}

func TestInstanceDeleteRetiresOnlyOwnedFirewall(t *testing.T) {
	d := newDeleteFixture(t)
	b := d.f.backend
	selected, e := b.readRecord(d.f.configs[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	sibling, e := b.readRecord(d.f.configs[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.deleteInstance(context.Background(), selected.Runtime.ID, "JV Paper"); e != nil {
		t.Fatal(e)
	}
	for _, rule := range selected.Firewall {
		if d.f.firewall[rule] {
			t.Fatal("selected owned rule retained")
		}
	}
	for _, rule := range sibling.Firewall {
		if !d.f.firewall[rule] {
			t.Fatal("sibling firewall rule removed")
		}
	}
}
func TestInstanceDeleteRecoveryInhibitsRenameAndMutations(t *testing.T) {
	d := newDeleteFixture(t)
	b := d.f.backend
	old := instanceRuntimes
	instanceRuntimes = b
	t.Cleanup(func() { instanceRuntimes = old })
	d.failRemove = true
	id := d.f.configs[0].ID
	if e := b.deleteInstance(context.Background(), id, "JV Paper"); e == nil {
		t.Fatal("missing interruption")
	}
	if _, e := b.store.renameInstance(id, "Renamed", ""); e == nil {
		t.Fatal("rename stranded retry")
	}
	if e := b.requireNoInstanceRestore(); e == nil {
		t.Fatal("pending deletion allowed unrelated runtime mutation")
	}
	pending, e := b.pending(id)
	if !pending || e == nil {
		t.Fatal("pending deletion did not inhibit startup")
	}
}

func TestInstanceDeleteRetryAfterRuntimeAndIdentityRetirement(t *testing.T) {
	for _, interruption := range []string{"config_removed", "directory_removed", "identity_removed", "identity_retired"} {
		t.Run(interruption, func(t *testing.T) {
			d := newDeleteFixture(t)
			b, id := d.f.backend, d.f.configs[0].ID
			d.failRemove = true
			if err := b.deleteInstance(context.Background(), id, "JV Paper"); err == nil {
				t.Fatal("expected interruption")
			}
			j, err := b.deleteJournal(id)
			if err != nil {
				t.Fatal(err)
			}
			d.failRemove = false
			if _, err := b.deleteStorage(context.Background(), "remove", j); err != nil {
				t.Fatal(err)
			}
			j.Stage = "data_removed"
			if err := b.writeJSON(b.deletePath(id), j, true); err != nil {
				t.Fatal(err)
			}
			dir, _, _ := b.paths(id)
			if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
				t.Fatal(err)
			}
			if interruption != "config_removed" {
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
			}
			if interruption == "identity_removed" || interruption == "identity_retired" {
				if err := os.Remove(filepath.Join(b.store.directory, id+".json")); err != nil {
					t.Fatal(err)
				}
			}
			if interruption == "identity_retired" {
				j.Stage = "identity_retired"
				if err := b.writeJSON(b.deletePath(id), j, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.deleteInstance(context.Background(), id, "JV Paper"); err != nil {
				t.Fatal("late retry stranded", err)
			}
			for _, path := range []string{dir, filepath.Join(b.store.directory, id+".json"), b.deletePath(id)} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("retry incomplete", path, err)
				}
			}
		})
	}
}
