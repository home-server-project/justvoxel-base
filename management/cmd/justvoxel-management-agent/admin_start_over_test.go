package main

// Source fixtures only. These tests deliberately never operate host runtimes.
import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type startOverFixture struct {
	f       *factoryResetFixture
	s       *server
	players string
	fail    string
}

func newStartOverFixture(t *testing.T, n int) *startOverFixture {
	t.Helper()
	f := newFactoryResetFixture(t, n)
	x := &startOverFixture{f: f, s: factoryFixtureServer(t), players: "There are 0 of a max of 20 players online:"}
	oldPolicy, oldArchive, oldFstab := startOverPolicyRoot, startOverArchiveRoot, startOverFstabPath
	startOverPolicyRoot = t.TempDir()
	startOverArchiveRoot = filepath.Join(t.TempDir(), "archives")
	startOverFstabPath = filepath.Join(t.TempDir(), "fstab")
	t.Cleanup(func() {
		startOverPolicyRoot, startOverArchiveRoot, startOverFstabPath = oldPolicy, oldArchive, oldFstab
	})
	for _, p := range []string{filepath.Join(startOverPolicyRoot, "justvoxel.conf"), filepath.Join(startOverPolicyRoot, "minecraft-backup.env"), filepath.Join(startOverPolicyRoot, "smb-backup.credentials"), filepath.Join(startOverPolicyRoot, "storage-mounts"), startOverFstabPath} {
		if err := os.WriteFile(p, []byte("preserve-test-evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := f.x.b.command
	f.x.b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		call := command + " " + strings.Join(args, " ")
		if x.fail != "" && strings.Contains(call, x.fail) {
			return nil, errors.New("private /host/path RCON_PASSWORD=secret")
		}
		if command == "nsenter" && len(args) > 6 && args[2] == instanceDeleteHelper {
			if args[3] != "inspect" {
				t.Fatal("Start Over attempted data deletion", args)
			}
			var st syscall.Stat_t
			if err := syscall.Stat(args[6], &st); err != nil {
				return nil, err
			}
			return []byte(fmt.Sprintf(`{"identity":"%d:%d"}`, st.Dev, st.Ino)), nil
		}
		if command == "podman" && args[0] == "exec" {
			f.x.calls = append(f.x.calls, call)
			return []byte(x.players), nil
		}
		if command == "systemctl" && args[0] == "--no-block" && args[1] == "stop" {
			id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "minecraft-"), ".service")
			f.x.states[id] = "stopped"
			f.x.calls = append(f.x.calls, call)
			return nil, nil
		}
		if command == "systemctl" && args[0] == "show" && args[1] == "--property=LoadState,ActiveState,SubState" {
			id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "minecraft-"), ".service")
			state := f.x.states[id]
			if state == "failed" || state == "activating" || state == "deactivating" {
				return []byte("LoadState=loaded\nActiveState=" + state + "\n"), nil
			}
		}
		if command == "podman" && args[0] == "inspect" {
			id := strings.TrimPrefix(args[len(args)-1], "minecraft-")
			if f.x.states[id] == "failed" {
				return []byte("exited 1"), nil
			}
		}
		return base(ctx, command, args...)
	}
	return x
}
func (x *startOverFixture) plan(t *testing.T) startOverPlan {
	t.Helper()
	p, err := x.f.x.b.planStartOver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (x *startOverFixture) apply(t *testing.T, p startOverPlan, confirm, backup bool) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(startOverRequest{p.PlanFingerprint, confirm, backup})
	r := httptest.NewRequest("POST", "/v1/admin/start-over/apply", strings.NewReader(string(raw)))
	r = r.WithContext(context.WithValue(r.Context(), peerUIDKey{}, uint32(0)))
	w := httptest.NewRecorder()
	x.s.adminStartOverApply(w, r)
	return w
}
func startOverMutations(calls []string) []string {
	out := []string{}
	for _, c := range calls {
		if strings.HasPrefix(c, "podman rm ") || strings.Contains(c, " stop ") || strings.Contains(c, " disable ") || strings.Contains(c, "--remove-port=") || strings.Contains(c, "instance-delete remove ") {
			out = append(out, c)
		}
	}
	return out
}
func TestStartOverParticipantSetsAndPreservation(t *testing.T) {
	for _, states := range [][]string{{}, {"stopped"}, {"running"}, {"running", "stopped"}, {"failed"}} {
		t.Run(fmt.Sprint(states), func(t *testing.T) {
			x := newStartOverFixture(t, len(states))
			b := x.f.x.b
			identities := map[string]syscall.Stat_t{}
			for i, state := range states {
				r := x.f.x.records[i]
				x.f.x.states[r.Runtime.ID] = state
				var st syscall.Stat_t
				if err := syscall.Stat(r.Storage.DataPath, &st); err != nil {
					t.Fatal(err)
				}
				identities[r.Storage.DataPath] = st
				if err := os.WriteFile(filepath.Join(r.Storage.DataPath, "world"), []byte("world"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			backup := filepath.Join(x.f.x.snapshot.BackupPath, "archive.tar.gz")
			if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(backup, []byte("archive"), 0600); err != nil {
				t.Fatal(err)
			}
			metadata := backup + ".json"
			if err := os.WriteFile(metadata, []byte("metadata"), 0600); err != nil {
				t.Fatal(err)
			}
			preserved := map[string][]byte{}
			for _, path := range []string{backup, metadata, startOverFstabPath, filepath.Join(startOverPolicyRoot, "storage-mounts"), filepath.Join(startOverPolicyRoot, "smb-backup.credentials")} {
				preserved[path], _ = os.ReadFile(path)
			}
			p := x.plan(t)
			if len(p.Participants) != len(states) {
				t.Fatal(p.Participants)
			}
			for i, r := range x.f.x.records {
				if p.Participants[i].ID != r.Runtime.ID {
					t.Fatal("wrong immutable participant", p.Participants)
				}
			}
			response := x.apply(t, p, false, false)
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			for path, want := range identities {
				var got syscall.Stat_t
				if err := syscall.Stat(path, &got); err != nil || got.Dev != want.Dev || got.Ino != want.Ino {
					t.Fatal("data identity changed", path, err)
				}
				if raw, err := os.ReadFile(filepath.Join(path, "world")); err != nil || string(raw) != "world" {
					t.Fatal("world changed", err)
				}
			}
			for path, want := range preserved {
				got, err := os.ReadFile(path)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("preservation failed", path, err)
				}
			}
			for _, r := range x.f.x.records {
				for _, path := range []string{filepath.Join(b.root, r.Runtime.ID), filepath.Join(b.store.directory, r.Runtime.ID+".json"), b.deletePath(r.Runtime.ID), filepath.Join(b.quadlets, "minecraft-"+r.Runtime.ID+".container"), filepath.Join(instanceBackupUnits, instanceBackupUnit(r.Runtime.ID, "service")), filepath.Join(instanceBackupUnits, instanceBackupUnit(r.Runtime.ID, "timer"))} {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("runtime ownership survived", path, err)
					}
				}
			}
			for _, name := range []string{"justvoxel.conf", "minecraft-backup.env"} {
				if _, err := os.Stat(filepath.Join(startOverPolicyRoot, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("active policy survived")
				}
			}
			if len(x.f.removals) != 0 {
				t.Fatal("data deletion invoked")
			}
			for _, call := range x.f.x.calls {
				if strings.Contains(call, "prune") || strings.Contains(call, "systemctl start") || strings.Contains(call, "podman rm --ignore -- minecraft ") {
					t.Fatal("unrelated mutation", call)
				}
			}
		})
	}
}
func TestStartOverPlayersAndStaleReviewBlockBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"online", "unknown", "stale", "activation", "transition", "backup", "private"} {
		t.Run(scenario, func(t *testing.T) {
			x := newStartOverFixture(t, 2)
			id := x.f.x.records[1].Runtime.ID
			p := x.plan(t)
			switch scenario {
			case "online":
				x.f.x.states[id] = "running"
				x.players = "There are 1 of a max of 20 players online: Alex"
			case "unknown":
				x.f.x.states[id] = "running"
				x.players = "unknown"
			case "stale":
				if err := os.WriteFile(filepath.Join(startOverPolicyRoot, "justvoxel.conf"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "activation":
				dir, _, _ := x.f.x.b.paths(id)
				if err := os.WriteFile(filepath.Join(dir, "pending.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "transition":
				x.f.x.states[id] = "activating"
			case "backup":
				x.fail = "--property=LoadState,ActiveState,Job"
			case "private":
				x.fail = "name=^minecraft-" + id + "$"
			}
			x.f.x.calls = nil
			w := x.apply(t, p, false, false)
			if w.Code != 409 || len(startOverMutations(x.f.x.calls)) != 0 {
				t.Fatal(w.Code, w.Body.String(), x.f.x.calls)
			}
			if strings.Contains(w.Body.String(), "/host/path") || strings.Contains(w.Body.String(), "RCON_PASSWORD") {
				t.Fatal("private evidence leaked")
			}
		})
	}
}
func TestStartOverConfirmedPlayersExactGracefulStop(t *testing.T) {
	x := newStartOverFixture(t, 2)
	id := x.f.x.records[1].Runtime.ID
	x.f.x.states[id] = "running"
	x.players = "There are 1 of a max of 20 players online: Alex"
	x.f.x.b.pause = func(context.Context, time.Duration) error { return nil }
	p := x.plan(t)
	if p.TotalPlayersOnline != 1 {
		t.Fatal(p)
	}
	x.f.x.calls = nil
	if w := x.apply(t, p, true, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	calls := strings.Join(x.f.x.calls, "\n")
	if !strings.Contains(calls, "minecraft-"+id+" rcon-cli say Server shutting down in 60 seconds") || !strings.Contains(calls, "systemctl --no-block stop -- minecraft-"+id+".service") {
		t.Fatal("exact instance graceful warning missing", calls)
	}
}
func TestStartOverJournalPolicyAndExactRetry(t *testing.T) {
	for _, policy := range []string{"", "delete", "preserve", "start_over"} {
		t.Run("policy-"+policy, func(t *testing.T) {
			x := newStartOverFixture(t, 2)
			b := x.f.x.b
			p := x.plan(t)
			j := p.entries[1]
			j.ResetPolicy = policy
			if err := b.writeJSON(b.deletePath(j.ID), j, false); err != nil {
				t.Fatal(err)
			}
			review, err := b.planStartOver(context.Background())
			if policy != "start_over" {
				if err == nil {
					t.Fatal("unrelated retirement accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = b.requireNoStartOver(); err == nil {
				t.Fatal("creation permitted during recovery")
			}
			if w := x.apply(t, review, false, false); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if len(x.f.removals) != 0 {
				t.Fatal("retry removed data")
			}
		})
	}
}
func TestStartOverRetirementInterruptionStages(t *testing.T) {
	for _, stage := range []string{"intent", "runtime_retired", "data_removed", "identity_retired"} {
		t.Run(stage, func(t *testing.T) {
			x := newStartOverFixture(t, 1)
			b := x.f.x.b
			j := x.plan(t).entries[0]
			if stage != "intent" {
				if err := b.retireInstanceRuntime(context.Background(), j); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "identity_retired" {
				if err := b.retireInstanceStateAndIdentity(j); err != nil {
					t.Fatal(err)
				}
			}
			j.Stage = stage
			if err := b.writeJSON(b.deletePath(j.ID), j, false); err != nil {
				t.Fatal(err)
			}
			p := x.plan(t)
			if len(p.Participants) != 1 || p.Participants[0].ID != j.ID {
				t.Fatal(p.Participants)
			}
			if w := x.apply(t, p, false, false); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if _, err := os.Stat(j.Record.Storage.DataPath); err != nil {
				t.Fatal("data gone", err)
			}
		})
	}
}
func TestStartOverDataIdentityChangeRetainsJournal(t *testing.T) {
	x := newStartOverFixture(t, 1)
	b := x.f.x.b
	j := x.plan(t).entries[0]
	j.DataIdentity = "9:99"
	if err := b.writeJSON(b.deletePath(j.ID), j, false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.planStartOver(context.Background()); err == nil {
		t.Fatal("changed data identity accepted")
	}
	if _, err := os.Stat(b.deletePath(j.ID)); err != nil {
		t.Fatal("recovery evidence lost", err)
	}
}
func TestStartOverArchivePublicationAndFailure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only archive ownership requires root fixture runner")
	}
	x := newStartOverFixture(t, 1)
	p := x.plan(t)
	old := filepath.Join(startOverArchiveRoot, "existing.tar.gz")
	if err := os.Mkdir(startOverArchiveRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// A runtime failure happens only after the verified archive is published.
	base := x.f.x.b.command
	x.f.x.b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if len(startOverMutations([]string{command + " " + strings.Join(args, " ")})) > 0 {
			files, e := os.ReadDir(startOverArchiveRoot)
			if e != nil {
				t.Fatal("archive missing before mutation", e)
			}
			published := false
			for _, file := range files {
				if strings.HasSuffix(file.Name(), ".partial") {
					t.Fatal("partial exists at mutation")
				}
				if file.Name() != "existing.tar.gz" {
					published = true
				}
			}
			if !published {
				t.Fatal("runtime mutation before configuration archive")
			}
		}
		return base(ctx, command, args...)
	}
	w := x.apply(t, p, false, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	} // stopped participant needs no stop command
	files, err := os.ReadDir(startOverArchiveRoot)
	if err != nil {
		t.Fatal(err)
	}
	archive := ""
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".partial") {
			t.Fatal("partial published")
		}
		if f.Name() != "existing.tar.gz" {
			archive = filepath.Join(startOverArchiveRoot, f.Name())
		}
	}
	if archive == "" {
		t.Fatal("no archive")
	}
	info, err := os.Stat(archive)
	if err != nil || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatal("archive not root-private", err)
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	sawConfig := false
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasSuffix(h.Name, "config.json") {
			sawConfig = true
		}
		if strings.Contains(h.Name, "/data/") || strings.Contains(h.Name, "archive.tar.gz") {
			t.Fatal("non-configuration archive member", h.Name)
		}
	}
	if !sawConfig {
		t.Fatal("archive omitted participant config")
	}
	if raw, err := os.ReadFile(old); err != nil || string(raw) != "keep" {
		t.Fatal("existing archive changed")
	}
}
func TestStartOverArchiveFailureZeroMutation(t *testing.T) {
	x := newStartOverFixture(t, 1)
	p := x.plan(t)
	if err := os.WriteFile(startOverArchiveRoot, []byte("block directory"), 0600); err != nil {
		t.Fatal(err)
	}
	x.f.x.calls = nil
	w := x.apply(t, p, false, true)
	if w.Code != 500 || len(startOverMutations(x.f.x.calls)) != 0 {
		t.Fatal(w.Code, w.Body.String(), x.f.x.calls)
	}
	if _, err := os.Stat(filepath.Join(startOverPolicyRoot, "justvoxel.conf")); err != nil {
		t.Fatal("policy mutated", err)
	}
}
func TestStartOverRootAuthorizationStrictJSONAndPublicShape(t *testing.T) {
	x := newStartOverFixture(t, 1)
	p := x.plan(t)
	raw, _ := json.Marshal(p)
	for _, secret := range []string{x.f.x.records[0].Runtime.RCONPassword, "RCONPassword", "environment_sha256", "uid", "gid", x.f.x.snapshot.StorageRoot} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private plan evidence exposed", secret)
		}
	}
	for _, uid := range []any{nil, uint32(1000)} {
		r := httptest.NewRequest("GET", "/v1/admin/start-over/plan", nil)
		if uid != nil {
			r = r.WithContext(context.WithValue(r.Context(), peerUIDKey{}, uid))
		}
		w := httptest.NewRecorder()
		x.s.adminStartOverPlan(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatal("non-root authorized")
		}
	}
	for _, body := range []string{`null`, `{"plan_fingerprint":"x","unknown":true}`, `{} {}`, `{"plan_fingerprint":"x"}`} {
		r := httptest.NewRequest("POST", "/v1/admin/start-over/apply", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), peerUIDKey{}, uint32(0)))
		w := httptest.NewRecorder()
		x.s.adminStartOverApply(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, body)
		}
	}
}

func TestStartOverInterruptedRuntimeCleanupRetriesWithoutRegistryGuessing(t *testing.T) {
	x := newStartOverFixture(t, 2)
	p := x.plan(t)
	x.fail = "systemctl daemon-reload"
	w := x.apply(t, p, false, false)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	b := x.f.x.b
	j, err := b.deleteJournal(p.entries[0].ID)
	if err != nil || j.ResetPolicy != "start_over" || j.Stage != "intent" {
		t.Fatal(j.Stage, err)
	}
	if _, err = os.Stat(filepath.Join(startOverPolicyRoot, "justvoxel.conf")); err != nil {
		t.Fatal("policy cleaned before all retirements", err)
	}
	x.fail = ""
	review := x.plan(t)
	if w = x.apply(t, review, false, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestStartOverRejectsForeignJournalRestoreScheduleAndFixedArtifacts(t *testing.T) {
	for _, scenario := range []string{"journal-id", "restore", "schedule", "fixed", "unregistered", "missing-runtime"} {
		t.Run(scenario, func(t *testing.T) {
			x := newStartOverFixture(t, 1)
			b := x.f.x.b
			j := x.plan(t).entries[0]
			dir, _, _ := b.paths(j.ID)
			var err error
			switch scenario {
			case "journal-id":
				j.Record.Runtime.ID = "jv-other1"
				err = b.writeJSON(b.deletePath(j.ID), j, false)
			case "restore":
				err = os.WriteFile(filepath.Join(dir, "restore-journal.json"), []byte(`{"stage":"swapped"}`), 0600)
			case "schedule":
				err = os.WriteFile(b.backupPath(j.ID, "schedule-pending"), []byte(`{}`), 0600)
			case "fixed":
				err = os.WriteFile(filepath.Join(b.quadlets, "minecraft.container"), []byte("unowned fixed runtime"), 0600)
			case "unregistered":
				err = os.Remove(filepath.Join(b.store.directory, j.ID+".json"))
			case "missing-runtime":
				err = os.Remove(filepath.Join(dir, "config.json"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = b.planStartOver(context.Background()); err == nil {
				t.Fatal("unsafe evidence accepted", scenario)
			}
		})
	}
}
func TestStartOverArchiveRejectsConfigurationSymlink(t *testing.T) {
	x := newStartOverFixture(t, 0)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(startOverPolicyRoot, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := x.f.x.b.startOverConfiguration(nil); err == nil {
		t.Fatal("archive followed configuration symlink")
	}
}

func TestStartOverOwnedFirewallOnlyAndDisplayNamesDoNotDefineScope(t *testing.T) {
	x := newStartOverFixture(t, 2)
	b := x.f.x.b
	owned := map[string]bool{}
	for i := range x.f.x.records {
		r := &x.f.x.records[i]
		r.Firewall = []instanceFirewallRule{{Zone: "public", Port: r.Runtime.JavaPort}}
		owned[fmt.Sprint(r.Runtime.JavaPort)+"/tcp"] = true
		dir, _, _ := b.paths(r.Runtime.ID)
		if err := b.writeJSON(filepath.Join(dir, "config.json"), r, true); err != nil {
			t.Fatal(err)
		}
	}
	owned["9999/tcp"] = true // unrelated host/container opening
	base := b.command
	b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "firewall-cmd" {
			arg := args[len(args)-1]
			_, port, _ := strings.Cut(arg, "=")
			if strings.HasPrefix(arg, "--query-port=") {
				if owned[port] {
					return []byte("yes"), nil
				}
				return []byte("no"), instanceTestExit{}
			}
			if strings.HasPrefix(arg, "--remove-port=") {
				if port == "9999/tcp" {
					t.Fatal("unrelated rule removed")
				}
				delete(owned, port)
				return nil, nil
			}
			t.Fatal("unexpected firewall command", args)
		}
		return base(ctx, command, args...)
	}
	p := x.plan(t)
	if _, err := b.store.renameInstance(p.Participants[0].ID, "Renamed display only", ""); err != nil {
		t.Fatal(err)
	}
	renamed := x.plan(t)
	if renamed.PlanFingerprint != p.PlanFingerprint {
		t.Fatal("display-only rename changed ownership review")
	}
	if w := x.apply(t, renamed, false, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(owned) != 1 || !owned["9999/tcp"] {
		t.Fatal("owned firewall boundary failed", owned)
	}
}

func TestStartOverJournalIsExcludedFromDeleteRecoveryInventory(t *testing.T) {
	x := newStartOverFixture(t, 1)
	b := x.f.x.b
	j := x.plan(t).entries[0]
	if err := b.writeJSON(b.deletePath(j.ID), j, false); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/minecraft/instances/deletions", nil)
	r = r.WithContext(context.WithValue(r.Context(), peerUIDKey{}, uint32(0)))
	w := httptest.NewRecorder()
	x.s.instanceDeletionInventory(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), j.ID) {
		t.Fatal("Start Over exposed as ordinary Delete", w.Code, w.Body.String())
	}
}
