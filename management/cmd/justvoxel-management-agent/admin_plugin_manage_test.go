package main

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type pluginManageFixture struct {
	*pluginInstallFixture
	entry          pluginManifestEntry
	failNextVerify bool
	duringVerify   func()
}

func newPluginManageFixture(t *testing.T, state string) *pluginManageFixture {
	t.Helper()
	return newPluginManageFixtureFor(t, state, "paper")
}
func newPluginManageFixtureFor(t *testing.T, state, software string) *pluginManageFixture {
	t.Helper()
	f := &pluginManageFixture{pluginInstallFixture: newPluginInstallFixtureFor(t, software, "1.21.8")}
	if javaServerContentKind(software) == "mods" {
		if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
			t.Fatal(err)
		}
		raw := componentModJar(t, software, "root_mod")
		hash := sha512.Sum512(raw)
		f.entry = pluginManifestEntry{Target: "mods", ProjectID: "root", VersionID: "release", VersionNumber: "1.0", Filename: "root.jar", SHA512: hex.EncodeToString(hash[:]), PluginName: "Root", Requested: true, RequiredBy: []string{}}
		f.jars[f.entry.Filename] = raw
	} else {
		a := f.plan(t).Artifacts[0]
		f.entry = pluginManifestArtifact(a)
		f.entry.PluginName = "Root"
		f.entry.Main = "example.Root"
	}
	if err := os.WriteFile(filepath.Join(f.data, pluginTarget(f.entry.Target), f.entry.Filename), f.jars[f.entry.Filename], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitPluginManifest(f.data, nil, pluginManifest{"v1", []pluginManifestEntry{f.entry}}); err != nil {
		t.Fatal(err)
	}
	if state == "disabled" {
		active, _ := openPluginDirectory(f.data)
		disabled, err := openDisabledPluginDirectory(f.data, true)
		if err != nil {
			t.Fatal(err)
		}
		_, err = checkedPluginMove(active, disabled, f.entry)
		active.Close()
		disabled.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(f.data, "plugins", "Root")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte("keep configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	pluginInstallCommand = func(_ context.Context, command string, args ...string) ([]byte, error) {
		f.commands = append(f.commands, command+" "+strings.Join(args, " "))
		switch command {
		case "systemctl":
			switch args[0] {
			case "is-active":
				if f.active {
					return []byte("active\n"), nil
				}
				return []byte("inactive\n"), errors.New("inactive")
			case "start":
				testPluginStartupPrerequisite(t)
				f.active = true
				return nil, nil
			case "stop":
				f.active = false
				return nil, nil
			}
		case "/usr/libexec/justvoxel/minecraft-backup":
			t.Fatal("legacy global backup invoked")
			return nil, errors.New("legacy backup")
		case "podman":
			if args[len(args)-1] != "list" {
				t.Fatal("RCON plugin verifier used")
			}
			return []byte("ready"), nil
		case "/usr/libexec/justvoxel/mjust/verify-minecraft-stack":
			want := []string{"--instance", "jv-a1b2c3"}
			if f.entry.Target == "mods" {
				want = append(want, "--content-install")
			}
			want = append(want, "1.21.8")
			if !slices.Equal(args, want) {
				t.Fatalf("lifecycle verifier arguments: got %v, want %v", args, want)
			}
			if f.duringVerify != nil {
				f.duringVerify()
			}
			if f.failNextVerify {
				f.failNextVerify = false
				return nil, errors.New("runtime failed")
			}
			if f.entry.Target == "mods" {
				return nil, nil
			}
			root, err := openPluginDirectory(f.data)
			if err != nil {
				t.Fatal(err)
			}
			names, err := root.Readdirnames(-1)
			if err != nil {
				t.Fatal(err)
			}
			plugins := []string{}
			enabling := ""
			for _, name := range names {
				if !safePluginFilename(name) {
					continue
				}
				jar, err := openPluginAt(root, name)
				if err != nil {
					t.Fatal(err)
				}
				identity, err := inspectStagedPlugin(jar, name)
				jar.Close()
				if err != nil {
					t.Fatal(err)
				}
				plugins = append(plugins, identity.Name+" (1.0)")
				enabling += "[" + identity.Name + "] Enabling " + identity.Name + " v1.0\n"
			}
			root.Close()
			raw := fmt.Sprintf("runtime generation %d\n[PluginInitializerManager] Initializing plugins...\n[PluginInitializerManager] Bukkit plugins (%d):\n", len(f.commands), len(plugins))
			if len(plugins) > 0 {
				raw += " - " + strings.Join(plugins, ", ") + "\n"
			}
			raw += enabling
			writePluginStartup(t, f.data, raw)
			return nil, nil
		}
		t.Fatalf("unexpected command %s %v", command, args)
		return nil, errors.New("unexpected command")
	}
	return f
}
func (f *pluginManageFixture) managePlan(t *testing.T, action string) pluginManagePlan {
	t.Helper()
	p, err := f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), action, f.entry.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *pluginManageFixture) manage(t *testing.T, p pluginManagePlan, confirm bool) operationJournal {
	t.Helper()
	j, created, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, "plugin_"+p.Action, "jv-a1b2c3")
	if err != nil || !created {
		t.Fatal(err)
	}
	_ = executePluginManage(context.Background(), f.s, j.OperationID, p, confirm, session{})
	j, err = f.s.operations.get(j.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func (f *pluginManageFixture) assertManageRuntimeVerifications(t *testing.T, want int) {
	t.Helper()
	command := "/usr/libexec/justvoxel/mjust/verify-minecraft-stack --instance jv-a1b2c3"
	if f.entry.Target == "mods" {
		command += " --content-install"
	}
	command += " 1.21.8"
	count := 0
	for _, got := range f.commands {
		if got == command {
			count++
		}
	}
	if count != want {
		t.Fatalf("runtime verifier invoked %d times, want %d", count, want)
	}
}

func TestManagedPluginLifecycleSuccessAndPreservation(t *testing.T) {
	for _, tc := range []struct{ action, state, want string }{{"enable", "disabled", "enabled"}, {"disable", "enabled", "disabled"}, {"remove", "enabled", ""}, {"remove", "disabled", ""}} {
		for _, running := range []bool{true, false} {
			t.Run(tc.action+tc.state+fmt.Sprint(running), func(t *testing.T) {
				f := newPluginManageFixture(t, tc.state)
				f.active = running
				_, before, _ := readPluginManifest(f.data)
				p := f.managePlan(t, tc.action)
				if tc.action == "remove" {
					f.duringVerify = func() {
						stage, err := openPluginOperationStaging(f.data, f.s.operations.currentMinecraftResetID, false)
						if err != nil {
							t.Fatal(err)
						}
						defer stage.Close()
						if err = verifyManagedPluginAt(stage, f.entry); err != nil {
							t.Fatal("rollback copy removed before commit", err)
						}
						_, raw, _ := readPluginManifest(f.data)
						if !bytes.Equal(raw, before) {
							t.Fatal("ownership committed before verification")
						}
					}
				}
				j := f.manage(t, p, false)
				if j.State != operationSucceeded {
					t.Fatalf("%s: %#v", tc.action, j)
				}
				f.assertManageRuntimeVerifications(t, 1)
				if f.active != running {
					t.Fatal("original service state not restored")
				}
				manifest, after, err := readPluginManifest(f.data)
				if err != nil {
					t.Fatal(err)
				}
				if tc.action == "remove" {
					if len(manifest.Artifacts) != 0 {
						t.Fatal("ownership not removed")
					}
				} else {
					if !bytes.Equal(before, after) {
						t.Fatal("manifest changed by state toggle")
					}
					state, err := resolveManagedPluginLocation(f.data, f.entry)
					if err != nil || state != tc.want {
						t.Fatal(state, err)
					}
				}
				config, err := os.ReadFile(filepath.Join(f.data, "plugins", "Root", "config.yml"))
				if err != nil || string(config) != "keep configuration" {
					t.Fatal("config changed")
				}
			})
		}
	}
}
func TestManagedPluginPlanStateAndOwnershipBoundaries(t *testing.T) {
	for _, state := range []string{"enabled", "disabled"} {
		t.Run(state, func(t *testing.T) {
			f := newPluginManageFixture(t, state)
			for _, action := range []string{"enable", "disable", "remove"} {
				_, err := f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), action, f.entry.ProjectID)
				allowed := action == "remove" || action == "enable" && state == "disabled" || action == "disable" && state == "enabled"
				if (err == nil) != allowed {
					t.Fatalf("%s from %s: %v", action, state, err)
				}
			}
			if _, err := f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), "remove", "manual"); err == nil {
				t.Fatal("adopted manual plugin")
			}
		})
	}
}
func TestManagedPluginDependencySafety(t *testing.T) {
	for _, parentState := range []string{"enabled", "disabled", "missing", "duplicate"} {
		for _, action := range []string{"disable", "remove"} {
			t.Run(parentState+action, func(t *testing.T) {
				f := newPluginManageFixture(t, "enabled")
				f.addJar(t, "parent", "parentRelease", "parent.jar", "Parent", "example.Parent")
				parent := pluginManifestEntry{ProjectID: "parent", VersionID: "parentRelease", VersionNumber: "1.0", Filename: "parent.jar", SHA512: f.versions["parentRelease"].Files[0].Hashes.SHA512, PluginName: "Parent", Main: "example.Parent", Requested: true, RequiredBy: []string{}}
				target := f.entry
				target.RequiredBy = []string{"parent"}
				_, raw, _ := readPluginManifest(f.data)
				if _, err := commitPluginManifest(f.data, raw, pluginManifest{"v1", []pluginManifestEntry{target, parent}}); err != nil {
					t.Fatal(err)
				}
				if parentState != "missing" {
					if err := os.WriteFile(filepath.Join(f.data, "plugins", parent.Filename), f.jars[parent.Filename], 0644); err != nil {
						t.Fatal(err)
					}
				}
				if parentState == "disabled" || parentState == "duplicate" {
					disabled, err := openDisabledPluginDirectory(f.data, true)
					if err != nil {
						t.Fatal(err)
					}
					disabled.Close()
					if parentState == "disabled" {
						if err = os.Rename(filepath.Join(f.data, "plugins", parent.Filename), filepath.Join(f.data, ".justvoxel", "disabled-plugins", parent.Filename)); err != nil {
							t.Fatal(err)
						}
					} else {
						if err = os.WriteFile(filepath.Join(f.data, ".justvoxel", "disabled-plugins", parent.Filename), f.jars[parent.Filename], 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
				_, err := f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), action, target.ProjectID)
				if (err == nil) != (parentState == "disabled") {
					t.Fatalf("dependency state %s: %v", parentState, err)
				}
				if parentState == "enabled" && !strings.Contains(err.Error(), "Parent") {
					t.Fatal("blocking identity missing")
				}
			})
		}
	}
}
func TestManagedPluginRollbackRestoresExactLocationAndManifest(t *testing.T) {
	for _, tc := range []struct{ action, state string }{{"enable", "disabled"}, {"disable", "enabled"}, {"remove", "enabled"}, {"remove", "disabled"}} {
		t.Run(tc.action+tc.state, func(t *testing.T) {
			f := newPluginManageFixture(t, tc.state)
			_, before, _ := readPluginManifest(f.data)
			p := f.managePlan(t, tc.action)
			f.failNextVerify = true
			j := f.manage(t, p, false)
			if j.State != operationRolledBack {
				t.Fatalf("rollback: %#v", j)
			}
			f.assertManageRuntimeVerifications(t, 2)
			state, err := resolveManagedPluginLocation(f.data, f.entry)
			if err != nil || state != tc.state {
				t.Fatal("exact location/hash not restored", state, err)
			}
			_, after, _ := readPluginManifest(f.data)
			if !bytes.Equal(before, after) {
				t.Fatal("manifest changed")
			}
		})
	}
}
func TestManagedPluginStaleFingerprintAndFinalPlayerCheck(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		f := newPluginManageFixture(t, "enabled")
		p := f.managePlan(t, "disable")
		p.Fingerprint = "sha256:" + strings.Repeat("f", 64)
		j := f.manage(t, p, false)
		if j.State != operationRolledBack || f.backup {
			t.Fatal("stale plan mutated runtime", j)
		}
	})
	t.Run("player joined", func(t *testing.T) {
		f := newPluginManageFixture(t, "enabled")
		p := f.managePlan(t, "disable")
		f.joinAt = f.playerChecks + 3
		j := f.manage(t, p, false)
		if j.State != operationRolledBack || f.backup {
			t.Fatal("player confirmation not rechecked", j)
		}
	})
}
func TestManagedPluginEnableIdentityCollision(t *testing.T) {
	f := newPluginManageFixture(t, "disabled")
	if err := os.WriteFile(filepath.Join(f.data, "plugins", "manual.jar"), f.jars[f.entry.Filename], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), "enable", f.entry.ProjectID); err == nil {
		t.Fatal("manual identity collision accepted")
	}
}
func TestManagedPluginStrictAdministratorEndpoints(t *testing.T) {
	pluginTestData(t)
	for _, path := range []string{"manage/plan", "manage", "manage/acknowledge"} {
		s := surfaceTestServer(t, roleAdministrator)
		mux := http.NewServeMux()
		registerAdminPluginRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, pluginTestRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/plugins/"+path, `{"unexpected":true}`))
		if rr.Code != 400 {
			t.Fatal(path, rr.Code)
		}
	}
}
func TestManagedPluginInterruptedEvidenceAndAmbiguousAcknowledge(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	p := f.managePlan(t, "disable")
	j, _, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginDisable, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	tx := pluginManageTransaction{"", "jv-a1b2c3", "v1", j.OperationID, "disable", p.Fingerprint, p.DataPath, p.DataDevice, p.DataInode, p.Server.MinecraftVersion, true, p.Entry, p.State, p.Manifest}
	evidence, err := createPluginManageEvidence(f.s.operations, j.OperationID, tx)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Close()
	if _, err = f.s.operations.transition(j.OperationID, operationValidating, "validating", "Validated."); err != nil {
		t.Fatal(err)
	}
	base := f.s.operations.baseDir
	if err = f.s.operations.close(); err != nil {
		t.Fatal(err)
	}
	store, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	f.s.operations = store
	current, err := store.currentPluginOperation()
	if err != nil || current == nil || current.State != operationNeedsAttention {
		t.Fatal("interruption released exclusion", current, err)
	}
	if _, err = readPluginManageTransaction(store, j.OperationID); err != nil {
		t.Fatal("durable evidence lost", err)
	}
	disabled, err := openDisabledPluginDirectory(f.data, true)
	if err != nil {
		t.Fatal(err)
	}
	disabled.Close()
	if err = os.WriteFile(filepath.Join(f.data, ".justvoxel", "disabled-plugins", f.entry.Filename), f.jars[f.entry.Filename], 0644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(pluginManageAcknowledgeRequest{OperationID: j.OperationID})
	rr := httptest.NewRecorder()
	f.s.adminPluginManageAcknowledge(rr, pluginTestRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/plugins/manage/acknowledge", string(body)))
	if rr.Code != 409 {
		t.Fatal("ambiguous state resolved", rr.Code)
	}
	current, _ = store.currentPluginOperation()
	if current == nil || current.State != operationNeedsAttention {
		t.Fatal("ambiguous state released maintenance")
	}
}

func TestManagedPluginAcknowledgementVerifiesCompleteTargetOrRestoredOriginal(t *testing.T) {
	for _, target := range []bool{true, false} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			f := newPluginManageFixture(t, "enabled")
			p := f.managePlan(t, "disable")
			j, _, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginDisable, "jv-a1b2c3")
			if err != nil {
				t.Fatal(err)
			}
			tx := pluginManageTransaction{"", "jv-a1b2c3", "v1", j.OperationID, "disable", p.Fingerprint, p.DataPath, p.DataDevice, p.DataInode, p.Server.MinecraftVersion, true, p.Entry, p.State, p.Manifest}
			dir, err := createPluginManageEvidence(f.s.operations, j.OperationID, tx)
			if err != nil {
				t.Fatal(err)
			}
			dir.Close()
			if target {
				active, _ := openPluginDirectory(f.data)
				disabled, err := openDisabledPluginDirectory(f.data, true)
				if err != nil {
					t.Fatal(err)
				}
				_, err = checkedPluginMove(active, disabled, p.Entry)
				active.Close()
				disabled.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.s.operations.transition(j.OperationID, operationNeedsAttention, "interrupted", "Interrupted management action."); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(pluginManageAcknowledgeRequest{OperationID: j.OperationID})
			rr := httptest.NewRecorder()
			f.s.adminPluginManageAcknowledge(rr, pluginTestRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/plugins/manage/acknowledge", string(body)))
			if rr.Code != 200 {
				t.Fatal("recovery failed", rr.Code, rr.Body.String())
			}
			finished, _ := f.s.operations.get(j.OperationID)
			if finished.State != operationResolved {
				t.Fatal(finished)
			}
			state, err := resolveManagedPluginLocation(f.data, f.entry)
			wanted := "enabled"
			if target {
				wanted = "disabled"
			}
			if err != nil || state != wanted {
				t.Fatal(state, err)
			}
			if current, _ := f.s.operations.currentPluginOperation(); current != nil {
				t.Fatal("verified recovery retained exclusion")
			}
		})
	}
}
func TestLifecycleStartupAbsenceRejectsMalformedAndEarlierEvidence(t *testing.T) {
	valid := "[PluginInitializerManager] Initializing plugins...\n[PluginInitializerManager] Bukkit plugins (0):\n"
	if !pluginStartupNameAbsent([]byte(valid), "Root") {
		t.Fatal("empty final inventory rejected")
	}
	for _, raw := range []string{valid + "[Root] Enabling Root v1.0\n", valid + "[PluginInitializerManager] Bukkit plugins (x):\n", valid + "[PluginInitializerManager] Bukkit plugins (0):\n", valid + "[PluginInitializerManager] Initializing plugins...\n", strings.Replace(valid, "(0)", "(1)", 1), "[PluginInitializerManager] Bukkit plugins (0):\n"} {
		if pluginStartupNameAbsent([]byte(raw), "Root") {
			t.Fatal("unsafe absence accepted", raw)
		}
	}
}
func TestLifecycleRejectsRetainedStartupSegmentWithUnrelatedAppend(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	snapshot, err := snapshotPluginLifecycleStartup(f.data)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(f.data, "logs", "latest.log"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("[Other] arbitrary background message\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err = verifyPluginLifecycleStartupFresh(f.data, snapshot); err == nil {
		t.Fatal("old startup inventory accepted after unrelated append")
	}
}
func TestGenericLifecycleCannotAdoptApplianceComponent(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	pluginTestJar(t, filepath.Join(f.data, "plugins", "geyser-spigot.jar"), map[string]string{"plugin.yml": "name: Geyser\nmain: org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin\nversion: 1.0\n"})
	items, err := readPluginInventory(f.data)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Filename == "geyser-spigot.jar" {
			found = true
			if item.Management != "justvoxel" || item.State != "" || item.ProjectID != "" {
				t.Fatal("appliance component received generic ownership", item)
			}
		}
	}
	if !found {
		t.Fatal("component missing")
	}
	for _, action := range []string{"enable", "disable", "remove"} {
		if _, err = f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), action, "wKkoqHrH"); err == nil {
			t.Fatal("component received generic action")
		}
	}
}

// These plan and file-transaction checks deliberately run without root, sockets,
// an activated instance, provider calls or privileged ownership fixtures.
func modManageFiles(t *testing.T) (string, pluginServerContext, pluginManifestEntry) {
	t.Helper()
	data := t.TempDir()
	if err := os.Mkdir(filepath.Join(data, "mods"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := componentModJar(t, "fabric", "example_mod")
	hash := sha512.Sum512(raw)
	entry := pluginManifestEntry{Target: "mods", ProjectID: "root", VersionID: "release", VersionNumber: "1.0", Filename: "root.jar", SHA512: hex.EncodeToString(hash[:]), PluginName: "Root", Requested: true, RequiredBy: []string{}}
	if err := os.WriteFile(filepath.Join(data, "mods", entry.Filename), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitPluginManifest(data, nil, pluginManifest{"v1", []pluginManifestEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	oldRuntime, oldPlayers := instanceRuntimes, pluginInstallPlayers
	instanceRuntimes = &instanceRuntimeBackend{root: t.TempDir()}
	pluginInstallPlayers = func(context.Context, string) (int, []string, error) { return 0, []string{}, nil }
	t.Cleanup(func() { instanceRuntimes, pluginInstallPlayers = oldRuntime, oldPlayers })
	server := pluginContentContext("fabric")
	server.InstanceID, server.MinecraftVersion = "jv-a1b2c3", "1.21.8"
	return data, server, entry
}

func TestManagedModPlanBoundariesWithoutRoot(t *testing.T) {
	for _, scenario := range []string{"remove", "enable", "disable", "manual", "runtime", "hash", "loader", "provider", "symlink", "unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			data, server, e := modManageFiles(t)
			action, project, source := "remove", e.ProjectID, "Modrinth"
			switch scenario {
			case "enable", "disable":
				action = scenario
			case "manual", "runtime":
				_, raw, _ := readPluginManifest(data)
				if _, err := commitPluginManifest(data, raw, pluginManifest{"v1", []pluginManifestEntry{}}); err != nil {
					t.Fatal(err)
				}
			case "hash":
				// A different valid ZIP, never bytes appended after EOCD.
				if err := os.WriteFile(filepath.Join(data, "mods", e.Filename), componentModJar(t, "fabric", "changed_mod"), 0644); err != nil {
					t.Fatal(err)
				}
			case "loader":
				server = pluginContentContext("forge")
				server.InstanceID, server.MinecraftVersion = "jv-a1b2c3", "1.21.8"
			case "provider":
				source = "Hangar"
			case "symlink":
				path := filepath.Join(data, "mods", e.Filename)
				if err := os.Rename(path, filepath.Join(data, "original.jar")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(data, "original.jar"), path); err != nil {
					t.Fatal(err)
				}
			case "unsupported":
				server = pluginContentContext("vanilla")
				server.MinecraftVersion = "1.21.8"
			}
			p, err := resolvePluginManagePlan(context.Background(), server, data, action, project, source)
			if scenario == "remove" {
				if err != nil || p.Entry.Target != "mods" || p.Entry.Main != "" || p.State != "enabled" || p.Fingerprint == "" {
					t.Fatal(p, err)
				}
			} else if err == nil {
				t.Fatal("unsafe mod lifecycle plan accepted", scenario)
			}
		})
	}
}

func TestManagedModProviderAwareDependencyBlockingWithoutRoot(t *testing.T) {
	for _, parentSource := range []string{"Modrinth", "CurseForge"} {
		t.Run(parentSource, func(t *testing.T) {
			data, server, e := modManageFiles(t)
			parent := e
			parent.Source, parent.ProjectID, parent.VersionID, parent.PluginName, parent.Filename = parentSource, "1234", "5678", "DependentMod", "parent.jar"
			raw, err := os.ReadFile(filepath.Join(data, "mods", e.Filename))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(data, "mods", parent.Filename), raw, 0644); err != nil {
				t.Fatal(err)
			}
			e.RequiredBy = []string{"1234"} // Deployed unqualified relationship means Modrinth.
			_, before, _ := readPluginManifest(data)
			if _, err = commitPluginManifest(data, before, pluginManifest{"v1", []pluginManifestEntry{e, parent}}); err != nil {
				t.Fatal(err)
			}
			_, err = resolvePluginManagePlan(context.Background(), server, data, "remove", e.ProjectID)
			if parentSource == "Modrinth" {
				if err == nil || !strings.Contains(err.Error(), "DependentMod") {
					t.Fatal("dependent identity not blocked", err)
				}
			} else if err != nil {
				t.Fatal("wrong provider blocked removal", err)
			}
		})
	}
}

func TestManagedModFileTransactionAndRestorationWithoutRoot(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			data, server, e := modManageFiles(t)
			other := e
			other.ProjectID, other.VersionID, other.Filename = "unrelated", "otherRelease", "other.jar"
			otherRaw := componentModJar(t, "fabric", "other_mod")
			sum := sha512.Sum512(otherRaw)
			other.SHA512 = hex.EncodeToString(sum[:])
			if err := os.WriteFile(filepath.Join(data, "mods", other.Filename), otherRaw, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, "mods", "manual.jar"), otherRaw, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(data, "config"), 0700); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(data, "config", "mod.conf")
			if err := os.WriteFile(config, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			_, original, _ := readPluginManifest(data)
			if _, err := commitPluginManifest(data, original, pluginManifest{"v1", []pluginManifestEntry{e, other}}); err != nil {
				t.Fatal(err)
			}
			p, err := resolvePluginManagePlan(context.Background(), server, data, "remove", e.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			tx := pluginManageTransaction{Loader: "fabric", InstanceID: server.InstanceID, SchemaVersion: "v1", OperationID: "12345678-1234-4123-8123-123456789abc", Action: "remove", DataPath: data, DataDevice: p.DataDevice, DataInode: p.DataInode, Entry: e, OriginalState: "enabled", Manifest: p.Manifest}
			root, err := openContentDirectory(data, "mods")
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			stage, err := openPluginOperationStaging(data, tx.OperationID, true)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, err = checkedPluginMove(root, stage, e); err != nil {
				t.Fatal(err)
			}
			if err = verifyModRemovalFiles(tx); err != nil {
				t.Fatal(err)
			}
			_, raw, _ := readPluginManifest(data)
			if !bytes.Equal(raw, p.Manifest) {
				t.Fatal("ownership changed before verification")
			}
			if committed {
				if _, err = commitPluginManifest(data, p.Manifest, pluginManifest{"v1", []pluginManifestEntry{other}}); err != nil {
					t.Fatal(err)
				}
				if err = pluginManageTargetState(tx); err != nil {
					t.Fatal("completed removal not proven", err)
				}
			} else if pluginManageTargetState(tx) == nil {
				t.Fatal("uncommitted removal treated as success")
			}
			if err = restorePluginManageFiles(tx); err != nil {
				t.Fatal("interrupted removal not restored", err)
			}
			if err = verifyManageModAt(root, e, "fabric"); err != nil {
				t.Fatal(err)
			}
			_, raw, _ = readPluginManifest(data)
			if !bytes.Equal(raw, p.Manifest) {
				t.Fatal("original ownership not restored")
			}
			if err = verifyManageModAt(root, other, "fabric"); err != nil {
				t.Fatal("unrelated artifact changed", err)
			}
			manual, err := os.ReadFile(filepath.Join(data, "mods", "manual.jar"))
			if err != nil || !bytes.Equal(manual, otherRaw) {
				t.Fatal("manual mod changed", err)
			}
			configRaw, err := os.ReadFile(config)
			if err != nil || string(configRaw) != "preserve" {
				t.Fatal("config changed", err)
			}
			if _, err = os.Stat(filepath.Join(data, ".justvoxel", "disabled-plugins")); !os.IsNotExist(err) {
				t.Fatal("mod recovery created disabled storage", err)
			}
		})
	}
}

func TestManagedModRemovalEvidenceRejectsChangesWithoutRoot(t *testing.T) {
	for _, scenario := range []string{"hash", "loader", "manifest", "target"} {
		t.Run(scenario, func(t *testing.T) {
			data, server, entry := modManageFiles(t)
			p, err := resolvePluginManagePlan(context.Background(), server, data, "remove", entry.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			tx := pluginManageTransaction{Loader: "fabric", InstanceID: server.InstanceID, SchemaVersion: "v1", OperationID: "12345678-1234-4123-8123-123456789abc", Action: "remove", DataPath: data, DataDevice: p.DataDevice, DataInode: p.DataInode, Entry: entry, OriginalState: "enabled", Manifest: p.Manifest}
			root, err := openContentDirectory(data, "mods")
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			stage, err := openPluginOperationStaging(data, tx.OperationID, true)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, err = checkedPluginMove(root, stage, entry); err != nil {
				t.Fatal(err)
			}
			if _, err = commitPluginManifest(data, p.Manifest, pluginManifest{"v1", []pluginManifestEntry{}}); err != nil {
				t.Fatal(err)
			}
			if err = pluginManageTargetState(tx); err != nil {
				t.Fatal("valid removal rejected", err)
			}
			switch scenario {
			case "hash":
				tx.Entry.SHA512 = strings.Repeat("0", 128)
			case "loader":
				// The exact Fabric JAR and hash cannot prove a NeoForge descriptor.
				tx.Loader = "neoforge"
			case "manifest":
				_, raw, err := readPluginManifest(data)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = commitPluginManifest(data, raw, pluginManifest{"v1", []pluginManifestEntry{entry}}); err != nil {
					t.Fatal(err)
				}
			case "target":
				if _, err = checkedPluginMove(stage, root, entry); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "hash" || scenario == "loader" {
				if err = verifyModRemovalFiles(tx); err == nil {
					t.Fatal("changed staged artifact evidence accepted", scenario)
				}
			}
			if err = pluginManageTargetState(tx); err == nil {
				t.Fatal("changed removal evidence accepted", scenario)
			}
		})
	}
}

func TestManagedModRemoveContentRuntimeAndRollback(t *testing.T) {
	for _, running := range []bool{true, false} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprint(running, fail), func(t *testing.T) {
				f := newPluginManageFixtureFor(t, "enabled", "fabric")
				f.active = running
				p := f.managePlan(t, "remove")
				_, before, _ := readPluginManifest(f.data)
				f.failNextVerify = fail
				firstVerify := true
				f.duringVerify = func() {
					// Rollback verifies the restored active artifact separately.
					if !firstVerify {
						return
					}
					firstVerify = false
					stage, err := openPluginOperationStaging(f.data, f.s.operations.currentMinecraftResetID, false)
					if err != nil {
						t.Fatal(err)
					}
					defer stage.Close()
					if err = verifyManageModAt(stage, f.entry, "fabric"); err != nil {
						t.Fatal(err)
					}
					_, raw, _ := readPluginManifest(f.data)
					if !bytes.Equal(before, raw) {
						t.Fatal("manifest committed before runtime verification")
					}
				}
				j := f.manage(t, p, false)
				want := operationSucceeded
				if fail {
					want = operationRolledBack
				}
				if j.State != want || f.active != running {
					t.Fatal(j, f.active)
				}
				m, raw, err := readPluginManifest(f.data)
				if err != nil {
					t.Fatal(err)
				}
				if fail {
					if !bytes.Equal(before, raw) {
						t.Fatal("manifest not restored")
					}
					if _, err = resolveManagedContentLocation(f.data, f.entry); err != nil {
						t.Fatal(err)
					}
				} else if len(m.Artifacts) != 0 {
					t.Fatal("removal not committed")
				}
				wantVerifications := 1
				if fail {
					wantVerifications++
				}
				f.assertManageRuntimeVerifications(t, wantVerifications)
			})
		}
	}
}

func TestManagedModDurableRecovery(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			f := newPluginManageFixtureFor(t, "enabled", "fabric")
			p := f.managePlan(t, "remove")
			j, _, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginRemove, "jv-a1b2c3")
			if err != nil {
				t.Fatal(err)
			}
			tx := pluginManageTransaction{Loader: "fabric", InstanceID: p.Server.InstanceID, SchemaVersion: "v1", OperationID: j.OperationID, Action: "remove", PlanFingerprint: p.Fingerprint, DataPath: p.DataPath, DataDevice: p.DataDevice, DataInode: p.DataInode, MinecraftVersion: p.Server.MinecraftVersion, OriginallyRunning: false, Entry: p.Entry, OriginalState: p.State, Manifest: p.Manifest}
			evidence, err := createPluginManageEvidence(f.s.operations, j.OperationID, tx)
			if err != nil {
				t.Fatal(err)
			}
			evidence.Close()
			root, err := openContentDirectory(f.data, "mods")
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			stage, err := openPluginOperationStaging(f.data, j.OperationID, true)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, err = checkedPluginMove(root, stage, p.Entry); err != nil {
				t.Fatal(err)
			}
			if committed {
				if _, err = commitPluginManifest(f.data, p.Manifest, pluginManifest{"v1", []pluginManifestEntry{}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.s.operations.transition(j.OperationID, operationValidating, "validating", "Validated."); err != nil {
				t.Fatal(err)
			}
			base := f.s.operations.baseDir
			if err = f.s.operations.close(); err != nil {
				t.Fatal(err)
			}
			store, err := openOperationStore(base)
			if err != nil {
				t.Fatal(err)
			}
			defer store.close()
			f.s.operations = store
			recovered, err := readPluginManageTransaction(store, j.OperationID)
			if err != nil || recovered.Loader != "fabric" {
				t.Fatal(recovered, err)
			}
			body, _ := json.Marshal(pluginManageAcknowledgeRequest{OperationID: j.OperationID})
			rr := httptest.NewRecorder()
			f.s.adminPluginManageAcknowledge(rr, pluginTestRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/plugins/manage/acknowledge", string(body)))
			if rr.Code != 200 {
				t.Fatal(rr.Code, rr.Body.String())
			}
			f.assertManageRuntimeVerifications(t, 1)
			finished, err := store.get(j.OperationID)
			if err != nil || finished.State != operationResolved {
				t.Fatal("recovery did not resolve needs_attention", finished, err)
			}
			if f.active {
				t.Fatal("original stopped state not restored")
			}
			if committed {
				if err = pluginManageTargetState(tx); err != nil {
					t.Fatal(err)
				}
			} else if err = verifyManageModAt(root, p.Entry, "fabric"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagedModCannotRemoveRuntimeOwnership(t *testing.T) {
	f := newPluginManageFixtureFor(t, "enabled", "fabric")
	recordRuntimeComponent(t, f.pluginInstallFixture, "fabric", f.entry.Filename, f.jars[f.entry.Filename])
	if _, err := f.s.resolvePluginManage(withPluginInstance(context.Background(), "jv-a1b2c3"), "remove", f.entry.ProjectID); err == nil || !strings.Contains(err.Error(), "runtime-owned") {
		t.Fatal("runtime ownership gained lifecycle", err)
	}
}
