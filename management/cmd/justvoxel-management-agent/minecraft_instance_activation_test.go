package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type instanceTestExit struct{}

func (instanceTestExit) Error() string { return "mock exit 1" }
func (instanceTestExit) ExitCode() int { return 1 }

type instanceActivationFixture struct {
	t           *testing.T
	backend     *instanceRuntimeBackend
	snapshot    minecraftInstanceRuntimeContext
	configs     []minecraftInstanceRuntimeConfig
	states      map[string]string
	containers  map[string]string
	firewall    map[instanceFirewallRule]bool
	calls       []string
	failReload  int
	failMount   bool
	players     string
	failPlayers bool
}

func instanceTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
func newInstanceActivationFixture(t *testing.T) *instanceActivationFixture {
	t.Helper()
	return newInstanceActivationFixtureFor(t, "", "")
}

func newInstanceActivationFixtureFor(t *testing.T, software, version string) *instanceActivationFixture {
	t.Helper()
	store, snapshot, configs := instanceRuntimeFixture(t)
	if software != "" {
		configs[0].Software = software
	}
	if version != "" {
		configs[0].Version = version
	}
	// Runtime paths must remain valid even when subtest names contain '=' or '#'.
	root, err := os.MkdirTemp("/tmp", "justvoxel-instance-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	snapshot.StorageRoot = filepath.Join(root, "storage")
	snapshot.BackupPath = filepath.Join(snapshot.StorageRoot, "backups")
	if err := os.Mkdir(snapshot.StorageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	f := &instanceActivationFixture{t: t, snapshot: snapshot, configs: configs, states: map[string]string{}, containers: map[string]string{}, firewall: map[instanceFirewallRule]bool{}, players: "There are 0 of a max of 20 players online:"}
	b := &instanceRuntimeBackend{root: filepath.Join(root, "private"), quadlets: filepath.Join(root, "quadlets"), staging: filepath.Join(root, "staging"), store: store}
	if err := os.Mkdir(b.quadlets, 0755); err != nil {
		t.Fatal(err)
	}
	b.command = f.command
	b.snapshot = func(context.Context) (minecraftInstanceRuntimeContext, uint32, uint32, error) {
		return f.snapshot, 1000, 1000, nil
	}
	f.backend = b
	// Dynamic host ports prevent these source tests depending on port availability.
	f.configs[0].JavaPort = instanceTestPort(t)
	for f.configs[1].JavaPort = f.configs[0].JavaPort; f.configs[1].JavaPort == f.configs[0].JavaPort; {
		f.configs[1].JavaPort = instanceTestPort(t)
	}
	dir, err := b.privateDirectory(b.root, true)
	if err != nil {
		t.Fatal(err)
	}
	dir.Close()
	for _, c := range f.configs {
		dir, _, _ := b.paths(c.ID)
		d, err := b.privateDirectory(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
		record := instanceRuntimeRecord{SchemaVersion: 1, Runtime: c, Storage: instanceStorageFor(snapshot, c.ID), Firewall: []instanceFirewallRule{}}
		if err = b.writeJSON(filepath.Join(dir, "config.json"), record, false); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *instanceActivationFixture) command(ctx context.Context, command string, args ...string) ([]byte, error) {
	f.t.Helper()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.calls = append(f.calls, command+" "+strings.Join(args, " "))
	switch command {
	case instanceRuntimeHelper:
		if len(args) > 0 && args[0] == "channel" {
			return []byte("default"), nil
		}
		if len(args) > 0 && args[0] == "version" {
			return nil, nil
		}
	case "nsenter":
		if len(args) > 3 && args[2] == instanceDeleteHelper {
			if len(args) != 13 {
				f.t.Fatal("unsafe data identity argv", args)
			}
			if args[3] == "inspect" {
				return []byte(`{"identity":"1:2"}`), nil
			}
			if args[3] == "remove" && args[12] == "1:2" {
				return nil, os.RemoveAll(args[6])
			}
			f.t.Fatal("unexpected data identity action", args)
		}
		if len(args) != 12 || args[0] != "--mount=/proc/1/ns/mnt" || args[1] != "--" || args[2] != instanceRuntimeHelper {
			f.t.Fatalf("unsafe storage argv: %v", args)
		}
		if f.failMount {
			return nil, errors.New("mock unavailable mount")
		}
		action, data := args[3], args[6]
		switch action {
		case "preflight-new":
			if _, err := os.Lstat(filepath.Dir(data)); !errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("existing data tree")
			}
		case "provision":
			if err := os.MkdirAll(filepath.Dir(data), 0700); err != nil {
				f.t.Fatal(err)
			}
			if err := os.MkdirAll(data, 0750); err != nil {
				f.t.Fatal(err)
			}
		case "version-empty":
			entries, err := os.ReadDir(data)
			if err != nil || len(entries) != 0 {
				return nil, errors.New("nonempty world")
			}
		case "validate":
			if info, err := os.Stat(data); err != nil || !info.IsDir() {
				return nil, errors.New("missing data directory")
			}
		case "preflight":
		default:
			f.t.Fatalf("unexpected storage action %q", action)
		}
		return nil, nil
	case "env":
		if len(args) != 3 || args[2] != "--dryrun" || !strings.HasPrefix(args[0], "QUADLET_UNIT_DIRS=") {
			f.t.Fatalf("unsafe generator argv %v", args)
		}
		path := strings.TrimPrefix(args[0], "QUADLET_UNIT_DIRS=")
		entries, err := os.ReadDir(path)
		if err != nil {
			f.t.Fatal(err)
		}
		var units []string
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".container") {
				units = append(units, strings.TrimSuffix(entry.Name(), ".container")+".service")
			}
		}
		return []byte(strings.Join(units, "\n")), nil
	case "systemctl":
		if len(args) == 1 && args[0] == "daemon-reload" {
			if f.failReload > 0 {
				f.failReload--
				return nil, errors.New("mock daemon reload failure")
			}
			entries, err := os.ReadDir(f.backend.quadlets)
			if err != nil {
				f.t.Fatal(err)
			}
			units := map[string]bool{}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".container") {
					id := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "minecraft-"), ".container")
					units[id] = true
					if f.states[id] == "" {
						f.states[id] = "inactive"
					}
				}
			}
			for id := range f.states {
				if !units[id] {
					delete(f.states, id)
				}
			}
			return nil, nil
		}
		id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "minecraft-"), ".service")
		if !instanceIDPattern.MatchString(id) {
			f.t.Fatalf("unsafe service target: %v", args)
		}
		if args[0] == "show" {
			state := f.states[id]
			load := "loaded"
			if state == "" {
				load = "not-found"
				state = "inactive"
			}
			return []byte("LoadState=" + load + "\nActiveState=" + state + "\nSubState=mock\n"), nil
		}
		if reflect.DeepEqual(args, []string{"stop", "--", "minecraft-" + id + ".service"}) {
			f.states[id] = "inactive"
			f.containers[id] = "exited"
			return nil, nil
		}
		if reflect.DeepEqual(args, []string{"--no-block", "start", "--", "minecraft-" + id + ".service"}) {
			record, err := f.backend.readRecord(id)
			if errors.Is(err, errInstanceNotFound) {
				var permit struct {
					ID          string `json:"id"`
					Schema      int    `json:"schema"`
					OperationID string `json:"operation_id"`
					Fingerprint string `json:"plan_fingerprint"`
					Evidence    string `json:"evidence_sha256"`
					PID         int    `json:"agent_pid"`
					Start       string `json:"agent_start"`
				}
				raw, permitErr := f.backend.readPrivate(setupPermitPath(id))
				if permitErr != nil || instanceDecode(raw, &permit) != nil || permit.ID != id {
					f.t.Fatal("provisional start without exact permit")
				}
				record, err = f.backend.readRuntimeConfig(id)
			}
			if err != nil || f.backend.verifyLive(record) != nil {
				f.t.Fatal("start before complete committed configuration")
			}
			f.states[id] = "active"
			f.containers[id] = "running"
			return nil, nil
		}
	case "podman":
		if len(args) == 6 && args[0] == "inspect" && args[1] == "--type" && args[2] == "container" && args[3] == "--format" {
			id := strings.TrimPrefix(args[len(args)-1], "minecraft-")
			return []byte(f.containers[id] + " 0"), nil
		}
		if len(args) >= 1 && args[0] == "ps" {
			id := strings.TrimSuffix(strings.TrimPrefix(args[3], "name=^minecraft-"), "$")
			if state := f.containers[id]; state != "" {
				return []byte("minecraft-" + id + "\n"), nil
			}
			return nil, nil
		}
		if len(args) == 4 && args[0] == "exec" && args[2] == "rcon-cli" && args[3] == "list" {
			if !instanceIDPattern.MatchString(strings.TrimPrefix(args[1], "minecraft-")) {
				f.t.Fatal("wrong RCON container")
			}
			if f.failPlayers {
				return nil, errors.New("mock RCON failure")
			}
			return []byte(f.players), nil
		}
	case "firewall-cmd":
		if reflect.DeepEqual(args, []string{"--get-default-zone"}) {
			return []byte("public\n"), nil
		}
		var rule instanceFirewallRule
		var action string
		for _, arg := range args {
			switch {
			case strings.HasPrefix(arg, "--zone="):
				rule.Zone = strings.TrimPrefix(arg, "--zone=")
			case arg == "--permanent":
				rule.Permanent = true
			case strings.Contains(arg, "-port="):
				_, value, _ := strings.Cut(arg, "=")
				port, protocol, _ := strings.Cut(value, "/")
				rule.Port, _ = strconv.Atoi(port)
				if protocol == "udp" {
					rule.Protocol = "udp"
				}
			}
			if strings.HasPrefix(arg, "--query-port=") {
				action = "query"
			}
			if strings.HasPrefix(arg, "--add-port=") {
				action = "add"
			}
			if strings.HasPrefix(arg, "--remove-port=") {
				action = "remove"
			}
		}
		if rule.Zone != "public" || rule.Port < 1 {
			f.t.Fatalf("unsafe firewall argv: %v", args)
		}
		switch action {
		case "query":
			if f.firewall[rule] {
				return []byte("yes\n"), nil
			}
			return []byte("no\n"), instanceTestExit{}
		case "add":
			f.firewall[rule] = true
			return nil, nil
		case "remove":
			delete(f.firewall, rule)
			return nil, nil
		}
	}
	f.t.Fatalf("unexpected command %s %v", command, args)
	return nil, errors.New("unexpected command")
}
func (f *instanceActivationFixture) apply(id string) error {
	records, err := f.backend.records()
	if err != nil {
		return err
	}
	old, err := f.backend.readRecord(id)
	if err != nil {
		return err
	}
	return f.backend.apply(context.Background(), old, records, f.snapshot)
}
func (f *instanceActivationFixture) activateBoth() {
	f.t.Helper()
	for _, c := range f.configs {
		if err := f.apply(c.ID); err != nil {
			f.t.Fatal(err)
		}
	}
}

func TestInstanceActivationIndependentAndPersistent(t *testing.T) {
	f := newInstanceActivationFixture(t)
	legacyBefore := registrySnapshot(t, f.backend.store)
	if err := f.apply(f.configs[0].ID); err != nil {
		t.Fatal(err)
	}
	_, _, secondQuadlet := f.backend.paths(f.configs[1].ID)
	if _, err := os.Lstat(secondQuadlet); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("activation published another instance")
	}
	if _, err := os.Lstat(instanceStorageFor(f.snapshot, f.configs[1].ID).DataPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("activation created another instance world")
	}
	if err := f.apply(f.configs[1].ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.configs {
		record, err := f.backend.readRecord(c.ID)
		if err != nil || f.backend.verifyLive(record) != nil {
			t.Fatal("missing committed runtime", err)
		}
		dir, env, q := f.backend.paths(c.ID)
		for _, path := range []string{filepath.Join(dir, "config.json"), env, q} {
			info, err := os.Stat(path)
			if err != nil || !f.backend.store.private(info, 0600) {
				t.Fatal("runtime file is not private", err)
			}
		}
		data, err := os.ReadFile(q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "WantedBy=multi-user.target") != c.StartOnBoot {
			t.Fatal("wrong boot preference")
		}
		if strings.Contains(string(data), c.RCONPassword) || !strings.Contains(string(data), "validate-instance-runtime-commit "+c.ID) {
			t.Fatal("unsafe runtime guard")
		}
	}
	restarted := *f.backend
	records, err := restarted.records()
	if err != nil || len(records) != 2 {
		t.Fatal("persistent configured set lost", err)
	}
	if !reflect.DeepEqual(legacyBefore, registrySnapshot(t, f.backend.store)) {
		t.Fatal("runtime changed legacy identities/names")
	}
	if _, err := os.Lstat(filepath.Join(f.snapshot.StorageRoot, "minecraft")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("runtime created legacy world")
	}
	for _, call := range f.calls {
		if strings.Contains(call, "enable") || strings.Contains(call, "minecraft.service") || strings.Contains(call, "backup") || strings.Contains(call, "restart") {
			t.Fatalf("activation touched legacy/lifecycle: %s", call)
		}
	}
}
func TestInstanceLifecycleTargetsAndPlayerSafety(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	a, b := f.configs[0].ID, f.configs[1].ID
	if err := f.backend.action(context.Background(), a, "start"); err != nil {
		t.Fatal(err)
	}
	first, err := f.backend.observe(context.Background(), a)
	if err != nil || first.State != "running" {
		t.Fatal(first, err)
	}
	second, err := f.backend.observe(context.Background(), b)
	if err != nil || second.State != "stopped" {
		t.Fatal(second, err)
	}
	for _, players := range []string{"There are 1 of a max of 20 players online: Alex", "garbage", "There are 0 of a max of 20 players online: Alex", "There are 0 of a max of 0 players online:"} {
		f.players = players
		for _, action := range []string{"stop", "restart"} {
			before := len(f.calls)
			if err := f.backend.action(context.Background(), a, action); err == nil {
				t.Fatal("unsafe player interrupt accepted")
			}
			for _, call := range f.calls[before:] {
				if strings.HasPrefix(call, "systemctl stop") {
					t.Fatal("players check did not prevent stop")
				}
			}
		}
	}
	f.failPlayers = true
	if err := f.backend.action(context.Background(), a, "stop"); err == nil {
		t.Fatal("failed RCON was ignored")
	}
	f.failPlayers = false
	f.players = "There are 0 of a max of 20 players online:"
	if err := f.backend.action(context.Background(), a, "restart"); err != nil {
		t.Fatal(err)
	}
	if f.states[a] != "active" || f.states[b] != "inactive" {
		t.Fatal("restart changed another instance")
	}
	if err := f.backend.action(context.Background(), a, "stop"); err != nil {
		t.Fatal(err)
	}
	if f.states[a] != "inactive" || f.states[b] != "inactive" {
		t.Fatal("stop changed another instance")
	}
	f.states[a] = "active"
	f.containers[a] = "exited"
	status, err := f.backend.observe(context.Background(), a)
	if err != nil || status.State != "failed" {
		t.Fatal("exited container counted as running", status, err)
	}
	for _, state := range []string{"activating", "deactivating", "failed"} {
		f.states[a] = state
		status, err = f.backend.observe(context.Background(), a)
		want := map[string]string{"activating": "starting", "deactivating": "stopping", "failed": "failed"}[state]
		if err != nil || status.State != want {
			t.Fatal(status, err)
		}
	}
}
func TestInstanceBootApplyDoesNotChangeRunningState(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	if err := f.backend.action(context.Background(), id, "start"); err != nil {
		t.Fatal(err)
	}
	old, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.Runtime.StartOnBoot = false
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	if err = f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err != nil {
		t.Fatal(err)
	}
	if f.states[id] != "active" {
		t.Fatal("boot change stopped instance")
	}
	for _, call := range f.calls[before:] {
		if strings.HasPrefix(call, "systemctl stop") || strings.Contains(call, " start ") || strings.Contains(call, "firewall-cmd") {
			t.Fatal("boot change changed running state/firewall", call)
		}
	}
	current, err := f.backend.readRecord(id)
	if err != nil || current.Runtime.StartOnBoot {
		t.Fatal("boot preference was not persisted", err)
	}
}
func TestInstanceActivationRollbackAndRetry(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		t.Run(strconv.FormatBool(incomplete), func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			id := f.configs[0].ID
			f.failReload = 1
			if incomplete {
				f.failReload = 2
			}
			err := f.apply(id)
			if err == nil {
				t.Fatal("failed activation claimed success")
			}
			if errors.Is(err, errInstanceRecovery) != incomplete {
				t.Fatal("wrong rollback result", err)
			}
			pending, e := f.backend.pending(id)
			if e != nil || pending != incomplete {
				t.Fatal("wrong recovery evidence", pending, e)
			}
			if _, e = os.Stat(instanceStorageFor(f.snapshot, id).DataPath); e != nil {
				t.Fatal("rollback removed data", e)
			}
			if len(f.firewall) != 0 {
				t.Fatal("owned firewall additions survived rollback")
			}
			if incomplete {
				status, e := f.backend.observe(context.Background(), id)
				if e != nil || status.State != "recovery_required" {
					t.Fatal(status, e)
				}
				if e = f.backend.action(context.Background(), id, "start"); !errors.Is(e, errInstanceRecovery) {
					t.Fatal("uncertain runtime started", e)
				}
				if e = f.backend.recover(context.Background(), id); e != nil {
					t.Fatal("bounded recovery retry failed", e)
				}
			}
			if err = f.apply(id); err != nil {
				t.Fatal("retry activation failed", err)
			}
		})
	}
}

func TestInstanceActivationScopedStagingCleanup(t *testing.T) {
	for _, kind := range []string{"generator", "existing-runtime", "firewall-plan", "success", "rollback", "incomplete-rollback"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			f.activateBoth()
			entries, err := os.ReadDir(f.backend.staging)
			if err != nil || len(entries) != 0 {
				t.Fatal("initial successful activations left staging bundles")
			}
			// A real older bundle includes credentials for the complete instance set.
			if err = f.backend.store.stageInstanceRuntimes(f.snapshot, f.configs, f.backend.staging, "apply-existing"); err != nil {
				t.Fatal(err)
			}
			id := f.configs[0].ID
			old, err := f.backend.readRecord(id)
			if err != nil {
				t.Fatal(err)
			}
			next := old
			next.Runtime.MOTD = "Updated instance"
			// Preserve actual world files as well as the older bundle and other runtime.
			for _, c := range f.configs {
				path := filepath.Join(instanceStorageFor(f.snapshot, c.ID).DataPath, "world.dat")
				if err = os.WriteFile(path, []byte("existing world "+c.ID), 0600); err != nil {
					t.Fatal(err)
				}
			}
			protected := map[string][]byte{}
			otherDir, _, otherQuadlet := f.backend.paths(f.configs[1].ID)
			for _, root := range []string{filepath.Join(f.backend.staging, "apply-existing"), otherDir, otherQuadlet, f.snapshot.StorageRoot} {
				if err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return err
					}
					data, err := os.ReadFile(path)
					protected[path] = data
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "success" {
				selectedDir, selectedEnv, selectedQuadlet := f.backend.paths(id)
				for _, path := range []string{selectedEnv, selectedQuadlet, filepath.Join(selectedDir, "config.json")} {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					protected[path] = data
				}
			}
			injected := false
			secretDiagnostic := "private diagnostic: " + f.configs[0].RCONPassword + " " + f.configs[1].RCONPassword
			f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				output, err := f.command(ctx, command, args...)
				if kind == "existing-runtime" && command == instanceRuntimeHelper && args[0] == "version" {
					// Corrupt after observe's verification, before staging and verifyLive.
					_, env, _ := f.backend.paths(id)
					if err := os.WriteFile(env, []byte(secretDiagnostic), 0600); err != nil {
						t.Fatal(err)
					}
					protected[env] = []byte(secretDiagnostic)
					injected = true
				}
				if (kind == "generator" && command == "env") || (kind == "firewall-plan" && command == "firewall-cmd" && reflect.DeepEqual(args, []string{"--get-default-zone"})) {
					injected = true
					return []byte(secretDiagnostic), errors.New(secretDiagnostic)
				}
				return output, err
			}
			if kind == "rollback" {
				f.failReload = 1
			}
			if kind == "incomplete-rollback" {
				f.failReload = 2
			}
			var logs bytes.Buffer
			oldLog := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(oldLog) })
			oldBackend, oldLock := instanceRuntimes, pluginRuntimeLockPath
			instanceRuntimes = f.backend
			pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
			t.Cleanup(func() {
				instanceRuntimes = oldBackend
				pluginRuntimeLockPath = oldLock
			})
			s := adminServerForTest()
			s.operations = openTestOperationStore(t)
			mux := http.NewServeMux()
			registerMinecraftRoutes(mux, s)
			payload, err := json.Marshal(map[string]any{"runtime": instancePublicSettings(next.Runtime)})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest("PUT", "/v1/minecraft/instances/"+id+"/configuration", string(payload)))
			wantCode := http.StatusConflict
			if kind == "success" {
				wantCode = http.StatusOK
			}
			if rr.Code != wantCode {
				t.Fatalf("unexpected activation response status: %d", rr.Code)
			}
			if (kind == "generator" || kind == "existing-runtime" || kind == "firewall-plan") && !injected {
				t.Fatal("intended pre-journal failure was not reached")
			}
			for _, c := range f.configs {
				if strings.Contains(rr.Body.String(), c.RCONPassword) || strings.Contains(logs.String(), c.RCONPassword) || strings.Contains(strings.Join(f.calls, "\n"), c.RCONPassword) {
					t.Fatal("configuration secret exposed in API response, logs, or command arguments")
				}
			}
			for path, want := range protected {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("cleanup changed a previous bundle, another runtime, or world data")
				}
			}
			entries, err = os.ReadDir(f.backend.staging)
			if err != nil {
				t.Fatal(err)
			}
			incomplete := kind == "incomplete-rollback"
			wantBundles := 1
			if incomplete {
				wantBundles = 2
			}
			if len(entries) != wantBundles {
				t.Fatalf("unexpected remaining staging bundle count: %d", len(entries))
			}
			pending, err := f.backend.pending(id)
			if err != nil || pending != incomplete {
				t.Fatal("incorrect recovery evidence retention")
			}
			if incomplete {
				dir, _, _ := f.backend.paths(id)
				data, err := f.backend.readPrivate(filepath.Join(dir, "pending.json"))
				var evidence instanceActivationEvidence
				if err != nil || instanceDecode(data, &evidence) != nil || !reflect.DeepEqual(evidence.Previous, old) || instanceHash(evidence.Environment) != old.EnvironmentSHA256 || instanceHash(evidence.Quadlet) != old.QuadletSHA256 {
					t.Fatal("incomplete rollback lost previous runtime recovery evidence")
				}
				for _, entry := range entries {
					if entry.Name() == "apply-existing" {
						continue
					}
					data, err := os.ReadFile(filepath.Join(f.backend.staging, entry.Name(), id+".env"))
					if err != nil || instanceHash(data) != evidence.Target.EnvironmentSHA256 {
						t.Fatal("incomplete rollback lost its staged target")
					}
				}
				if err = f.backend.recover(context.Background(), id); err != nil {
					t.Fatal("retained recovery evidence was unusable")
				}
			}
		})
	}
}

func TestInstanceApplyPreservesPreviousFilesOnFailure(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	old, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	_, env, q := f.backend.paths(id)
	oldEnv, err := os.ReadFile(env)
	if err != nil {
		t.Fatal(err)
	}
	oldQ, err := os.ReadFile(q)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.Runtime.MOTD = "Updated instance"
	next.Runtime.JavaPort = instanceTestPort(t)
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	f.failReload = 1
	if err = f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err == nil {
		t.Fatal("failure claimed success")
	}
	current, err := f.backend.readRecord(id)
	if err != nil || !reflect.DeepEqual(current, old) {
		t.Fatal("previous config not restored", err)
	}
	restoredEnv, err := os.ReadFile(env)
	if err != nil {
		t.Fatal(err)
	}
	restoredQ, err := os.ReadFile(q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(oldEnv, restoredEnv) || !reflect.DeepEqual(oldQ, restoredQ) {
		t.Fatal("previous live runtime was damaged")
	}
	for _, rule := range old.Firewall {
		if !f.firewall[rule] {
			t.Fatal("previous firewall not restored")
		}
	}
}
func TestInstanceReservationsMountAndUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"stopped-port", "overcommit", "mount", "storage-change", "symlink", "permissions", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			id := f.configs[0].ID
			old, err := f.backend.readRecord(id)
			if err != nil {
				t.Fatal(err)
			}
			next := old
			records, err := f.backend.records()
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "stopped-port":
				next.Runtime.JavaPort = f.configs[1].JavaPort
			case "overcommit":
				next.Runtime.ContainerMemory = "8G"
			case "mount":
				f.failMount = true
			case "storage-change":
				next.Storage.DataPath = filepath.Join(next.Storage.Root, "wrong-instance", "data")
			case "symlink":
				dir, _, _ := f.backend.paths(id)
				p := filepath.Join(dir, "config.json")
				if err = os.Rename(p, p+".original"); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(p+".original", p); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				dir, _, _ := f.backend.paths(id)
				if err = os.Chmod(filepath.Join(dir, "config.json"), 0644); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				next.Runtime.ID = "jv-../etc"
			}
			if kind == "symlink" || kind == "permissions" {
				if _, err = f.backend.records(); err == nil {
					t.Fatal("unsafe private configuration read")
				}
				return
			}
			if err = f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err == nil {
				t.Fatal("unsafe apply accepted")
			}
			if _, err = os.Lstat(instanceStorageFor(f.snapshot, id).DataPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed preflight created data")
			}
			for _, call := range f.calls {
				if strings.HasPrefix(call, "firewall-cmd --zone=") || strings.Contains(call, "daemon-reload") {
					t.Fatal("failed validation mutated runtime", call)
				}
			}
		})
	}
}
func TestInstanceOccupiedHostPort(t *testing.T) {
	f := newInstanceActivationFixture(t)
	old, err := f.backend.readRecord(f.configs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	next := old
	next.Runtime.JavaPort = l.Addr().(*net.TCPAddr).Port
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err == nil {
		t.Fatal("occupied host port was accepted")
	}
}
func TestInstanceRuntimeAPIAuthAndSecretBoundary(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	id := f.configs[0].ID
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		s := roleServerForTest(role)
		mux := http.NewServeMux()
		registerMinecraftRoutes(mux, s)
		for _, path := range []string{"/v1/minecraft/instances/" + id + "/configuration", "/v1/minecraft/instances/" + id + "/status"} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest("GET", path, ""))
			if rr.Code != 200 {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), f.configs[0].RCONPassword) || strings.Contains(rr.Body.String(), "rcon_password") {
				t.Fatal("private runtime secret returned")
			}
		}
		if role != roleAdministrator {
			for _, test := range []struct{ method, path string }{{"POST", "/v1/minecraft/instances"}, {"PUT", "/v1/minecraft/instances/" + id + "/configuration"}, {"POST", "/v1/minecraft/instances/" + id + "/activate"}, {"POST", "/v1/minecraft/instances/" + id + "/recover"}, {"POST", "/v1/minecraft/instances/" + id + "/stop"}} {
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, authorizedRequest(test.method, test.path, `{}`))
				if rr.Code != 403 {
					t.Fatal("unauthorized mutation", rr.Code, rr.Body.String())
				}
			}
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/minecraft/instances/"+id+"/start", strings.NewReader(`{}`)))
		if rr.Code != 401 {
			t.Fatal("unauthenticated lifecycle accepted")
		}
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("GET", "/v1/minecraft/instances/invalid/status", ""))
		if rr.Code != 400 {
			t.Fatal("malformed ID accepted")
		}
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/start", `{"unit":"minecraft.service"}`))
		if role != roleViewer && rr.Code != 400 {
			t.Fatal("raw unit accepted")
		}
	}
	payload, _ := json.Marshal(map[string]any{"runtime": instancePublicSettings(f.configs[0]), "path": "/etc"})
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, adminServerForTest())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances", string(payload)))
	if rr.Code != 400 {
		t.Fatal("arbitrary path accepted")
	}
}
func TestInstanceRuntimeLocks(t *testing.T) {
	old := pluginRuntimeLockPath
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { pluginRuntimeLockPath = old })
	s := adminServerForTest()
	s.operations = openTestOperationStore(t)
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.lockInstanceRuntime(); !errors.Is(err, errInstanceBusy) {
		t.Fatal("concurrent runtime lock accepted", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	release, err := s.lockInstanceRuntime()
	if err != nil {
		t.Fatal(err)
	}
	other := adminServerForTest()
	other.operations = openTestOperationStore(t)
	if _, err = other.lockInstanceRuntime(); !errors.Is(err, errInstanceBusy) {
		t.Fatal("concurrent Agent activation accepted", err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	s.operations.currentRestoreID = "active"
	if _, err = s.lockInstanceRuntime(); !errors.Is(err, errInstanceBusy) {
		t.Fatal("restore did not inhibit activation", err)
	}
}

func TestInstanceCreationRegistersAndActivatesTwoNewWorlds(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.snapshot.TotalMemoryMiB = 16384
	if err := os.Mkdir(filepath.Join(f.snapshot.StorageRoot, "minecraft"), 0700); err != nil {
		t.Fatal(err)
	}
	legacyWorld := filepath.Join(filepath.Join(f.snapshot.StorageRoot, "minecraft"), "world.dat")
	if err := os.WriteFile(legacyWorld, []byte("existing world"), 0600); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i, name := range []string{"Family Survival", "Creative World"} {
		c := f.configs[i]
		c.JavaPort = instanceTestPort(t)
		id, err := f.backend.create(context.Background(), c, name, f.snapshot)
		if err != nil {
			t.Fatal(id, err)
		}
		if !instanceIDPattern.MatchString(id) || id == c.ID {
			t.Fatal("new immutable ID was not generated")
		}
		ids = append(ids, id)
		record, err := f.backend.readRecord(id)
		if err != nil || f.backend.verifyLive(record) != nil {
			t.Fatal("new runtime not committed", err)
		}
		if f.states[id] != "inactive" || f.containers[id] != "" {
			t.Fatal("creation started container")
		}
		if _, err = os.Stat(record.Storage.DataPath); err != nil {
			t.Fatal("new instance directory not provisioned", err)
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("creation reused identity")
	}
	data, err := os.ReadFile(legacyWorld)
	if err != nil || string(data) != "existing world" {
		t.Fatal("creation changed legacy world", err)
	}
	records, err := f.backend.records()
	if err != nil || len(records) != 4 {
		t.Fatal("complete configured set missing", err)
	}
}
func TestInstanceFirewallPreservesExistingAccessAndOtherPorts(t *testing.T) {
	f := newInstanceActivationFixture(t)
	port := f.configs[0].JavaPort
	for _, permanent := range []bool{false, true} {
		f.firewall[instanceFirewallRule{Zone: "public", Port: port, Permanent: permanent}] = true
	}
	unrelated := instanceFirewallRule{Zone: "public", Port: 443, Permanent: true}
	f.firewall[unrelated] = true
	f.failReload = 1
	if err := f.apply(f.configs[0].ID); err == nil {
		t.Fatal("failed activation was ignored")
	}
	if !f.firewall[unrelated] {
		t.Fatal("unrelated firewall access removed")
	}
	for _, permanent := range []bool{false, true} {
		if !f.firewall[instanceFirewallRule{Zone: "public", Port: port, Permanent: permanent}] {
			t.Fatal("pre-existing firewall access was claimed/removed")
		}
	}
}
func TestInstancePendingApplyBlocksConflictingPublication(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.failReload = 2
	if err := f.apply(f.configs[0].ID); !errors.Is(err, errInstanceRecovery) {
		t.Fatal(err)
	}
	before := len(f.calls)
	if err := f.apply(f.configs[1].ID); !errors.Is(err, errInstanceRecovery) {
		t.Fatal("pending recovery allowed another apply", err)
	}
	if len(f.calls) != before {
		t.Fatal("pending recovery invoked host mutations")
	}
}
func TestInstanceRuntimeReadPurityAndUnknownFileRecovery(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	_, env, _ := f.backend.paths(id)
	if err := os.WriteFile(env, []byte("unknown private configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	state, err := f.backend.observe(context.Background(), id)
	if err != nil || state.State != "invalid_configuration" {
		t.Fatal(state, err)
	}
	if len(f.calls) != before {
		t.Fatal("invalid configuration status tried to repair or start runtime")
	}
	data, err := os.ReadFile(env)
	if err != nil || string(data) != "unknown private configuration" {
		t.Fatal("status rewrote private runtime", err)
	}
}

func TestInstanceOrphanQuadletCannotEscapeReservations(t *testing.T) {
	f := newInstanceActivationFixture(t)
	orphan := filepath.Join(f.backend.quadlets, "minecraft-jv-abcdef.container")
	if err := os.WriteFile(orphan, []byte("[Container]\nContainerName=minecraft-jv-abcdef\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.backend.records(); !errors.Is(err, errInstanceRecovery) {
		t.Fatal("orphan instance runtime escaped configured-set validation", err)
	}
}

func TestInstanceRuntimeAPILifecycleAndList(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	oldBackend, oldLock, oldDiscovery := instanceRuntimes, pluginRuntimeLockPath, runAdminDiscoveryHelper
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return []byte(`{"configured":true,"minecraft":{"server_type":"paper"}}`), nil
	}
	t.Cleanup(func() {
		instanceRuntimes = oldBackend
		pluginRuntimeLockPath = oldLock
		runAdminDiscoveryHelper = oldDiscovery
	})
	s := roleServerForTest(roleOperator)
	s.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, s)
	id := f.configs[0].ID
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/start", `{}`))
	if rr.Code != 200 || f.states[id] != "active" || f.states[f.configs[1].ID] != "inactive" {
		t.Fatal("operator start targeted wrong instance", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("GET", "/v1/minecraft/instances/runtime", ""))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"running"`) || !strings.Contains(rr.Body.String(), `"stopped"`) {
		t.Fatal("independent runtime list failed", rr.Code, rr.Body.String())
	}
	for _, c := range f.configs {
		if strings.Contains(rr.Body.String(), c.RCONPassword) {
			t.Fatal("runtime list leaked a secret")
		}
	}
	admin := adminServerForTest()
	admin.operations = s.operations
	adminMux := http.NewServeMux()
	registerMinecraftRoutes(adminMux, admin)
	rr = httptest.NewRecorder()
	adminMux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/stop", `{}`))
	if rr.Code != 200 || f.states[id] != "inactive" {
		t.Fatal("administrator stop failed", rr.Code, rr.Body.String())
	}
}

func TestInstanceSafeStopRemainsAvailableAfterResourceChange(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	if err := f.backend.action(context.Background(), id, "start"); err != nil {
		t.Fatal(err)
	}
	f.snapshot.TotalMemoryMiB = 4096
	if err := f.backend.action(context.Background(), id, "restart"); err == nil {
		t.Fatal("overcommitted restart accepted")
	}
	if err := f.backend.action(context.Background(), id, "stop"); err != nil {
		t.Fatal("resource change stranded safe graceful stop", err)
	}
	if f.states[id] != "inactive" {
		t.Fatal("safe stop did not stop selected instance")
	}
}

func TestInstanceCreationWithoutFixedServer(t *testing.T) {
	f := newInstanceActivationFixture(t)
	// Empty appliance identity/config stores; no fixed server files or state.
	for _, path := range []string{f.backend.store.directory, f.backend.root} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	old, oldLock, oldDiscovery := instanceRuntimes, pluginRuntimeLockPath, runAdminDiscoveryHelper
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		t.Fatal("first-instance creation queried fixed-server discovery")
		return nil, errors.New("fixed server unavailable")
	}
	t.Cleanup(func() {
		instanceRuntimes, pluginRuntimeLockPath, runAdminDiscoveryHelper = old, oldLock, oldDiscovery
	})
	s := adminServerForTest()
	s.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, s)
	c := f.configs[0]
	// The normal first-server ports have no special reservation.
	c.BedrockEnabled, c.BedrockPort = true, 19132
	body, err := json.Marshal(map[string]any{"runtime": instancePublicSettings(c), "display_name": "First server"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances", string(body)))
	if rr.Code != http.StatusCreated {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(rr.Body.Bytes(), &result) != nil || !instanceIDPattern.MatchString(result.ID) {
		t.Fatal("missing immutable identity", rr.Body.String())
	}
	r, err := f.backend.readRecord(result.ID)
	if err != nil || f.backend.verifyLive(r) != nil {
		t.Fatal("first instance lacks committed runtime", err)
	}
	if r.Runtime.UID != 1000 || r.Runtime.GID != 1000 || r.Storage != instanceStorageFor(f.snapshot, result.ID) || len(r.Firewall) == 0 {
		t.Fatal("missing ownership, derived storage, or firewall evidence")
	}
	if _, err = os.Stat(r.Storage.DataPath); err != nil {
		t.Fatal(err)
	}
	if pending, err := f.backend.pending(result.ID); err != nil || pending {
		t.Fatal("activation journal did not finish", err)
	}
	if _, err := os.Lstat(filepath.Join(f.backend.store.directory, "minecraft.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("creation required or wrote fixed identity", err)
	}
	for _, id := range []string{"", "original", "minecraft", "jv-../../etc", "jv-abcdef"} {
		calls := len(f.calls)
		if _, err := f.backend.readRecord(id); err == nil {
			t.Fatal("invalid/stale identity accepted", id)
		}
		if err := f.backend.action(context.Background(), id, "start"); err == nil {
			t.Fatal("invalid/stale lifecycle target accepted", id)
		}
		if len(f.calls) != calls {
			t.Fatal("invalid/stale identity reached runtime commands", id)
		}
	}
}

func TestInstanceRuntimeSnapshotApplianceOnly(t *testing.T) {
	data := []byte(`{"storage":{"root":"/var/lib/justvoxel","mount_point":"","uuid":"","source":""},"backup_path":"/var/lib/justvoxel/backups","memory_mib":4096,"uid":1234,"gid":2345}`)
	ctx, uid, gid, err := decodeInstanceRuntimeSnapshot(data)
	if err != nil || uid != 1234 || gid != 2345 || ctx.StorageRoot != "/var/lib/justvoxel" || ctx.TotalMemoryMiB != 4096 {
		t.Fatal("appliance-only snapshot rejected", err)
	}
	for _, data := range []string{
		`{}`, string(bytes.ReplaceAll(data, []byte(`"uid":1234`), []byte(`"uid":0`))),
		string(bytes.ReplaceAll(data, []byte(`"gid":2345`), []byte(`"gid":4294967295`))),
		string(bytes.ReplaceAll(data, []byte(`"source":""`), []byte(`"source":"","data_path":"/arbitrary"`))),
	} {
		if _, _, _, err := decodeInstanceRuntimeSnapshot([]byte(data)); err == nil {
			t.Fatal("unsafe appliance snapshot accepted")
		}
	}
}

func TestInstanceRuntimeCreateRejectsTargetOverrides(t *testing.T) {
	_, _, configs := instanceRuntimeFixture(t)
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, adminServerForTest())
	for _, field := range []string{"id", "unit", "container_name", "data_path", "environment_path", "uid", "gid"} {
		t.Run(field, func(t *testing.T) {
			data, _ := json.Marshal(instancePublicSettings(configs[0]))
			var runtime map[string]any
			if err := json.Unmarshal(data, &runtime); err != nil {
				t.Fatal(err)
			}
			runtime[field] = "arbitrary-target"
			payload, _ := json.Marshal(map[string]any{"runtime": runtime})
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances", string(payload)))
			if rr.Code != http.StatusBadRequest {
				t.Fatal("caller runtime target override accepted", rr.Code, rr.Body.String())
			}
		})
	}
}
