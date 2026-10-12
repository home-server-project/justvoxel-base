package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	f := &pluginManageFixture{pluginInstallFixture: newPluginInstallFixture(t)}
	a := f.plan(t).Artifacts[0]
	f.entry = pluginManifestArtifact(a)
	f.entry.PluginName = "Root"
	f.entry.Main = "example.Root"
	if err := os.WriteFile(filepath.Join(f.data, "plugins", f.entry.Filename), f.jars[f.entry.Filename], 0644); err != nil {
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
				f.active = true
				return nil, nil
			case "stop":
				f.active = false
				return nil, nil
			}
		case "/usr/libexec/justvoxel/minecraft-backup":
			if len(args) != 1 || args[0] != "--leave-stopped" {
				t.Fatal("cold backup contract")
			}
			f.backup = true
			f.active = false
			return nil, nil
		case "podman":
			if args[len(args)-1] != "list" {
				t.Fatal("RCON plugin verifier used")
			}
			return []byte("ready"), nil
		case "/usr/libexec/justvoxel/mjust/verify-minecraft-stack":
			if f.duringVerify != nil {
				f.duringVerify()
			}
			if f.failNextVerify {
				f.failNextVerify = false
				return nil, errors.New("runtime failed")
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
	p, err := f.s.resolvePluginManage(context.Background(), action, f.entry.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *pluginManageFixture) manage(t *testing.T, p pluginManagePlan, confirm bool) operationJournal {
	t.Helper()
	j, created, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, "plugin_"+p.Action)
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
				_, err := f.s.resolvePluginManage(context.Background(), action, f.entry.ProjectID)
				allowed := action == "remove" || action == "enable" && state == "disabled" || action == "disable" && state == "enabled"
				if (err == nil) != allowed {
					t.Fatalf("%s from %s: %v", action, state, err)
				}
			}
			if _, err := f.s.resolvePluginManage(context.Background(), "remove", "manual"); err == nil {
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
				_, err := f.s.resolvePluginManage(context.Background(), action, target.ProjectID)
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
	if _, err := f.s.resolvePluginManage(context.Background(), "enable", f.entry.ProjectID); err == nil {
		t.Fatal("manual identity collision accepted")
	}
}
func TestManagedPluginStrictAdministratorEndpoints(t *testing.T) {
	for _, path := range []string{"manage/plan", "manage", "manage/acknowledge"} {
		s := surfaceTestServer(t, roleAdministrator)
		mux := http.NewServeMux()
		registerAdminPluginRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodPost, "/v1/admin/plugins/"+path, `{"unexpected":true}`))
		if rr.Code != 400 {
			t.Fatal(path, rr.Code)
		}
	}
}
func TestManagedPluginInterruptedEvidenceAndAmbiguousAcknowledge(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	p := f.managePlan(t, "disable")
	j, _, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginDisable)
	if err != nil {
		t.Fatal(err)
	}
	tx := pluginManageTransaction{"v1", j.OperationID, "disable", p.Fingerprint, p.DataPath, p.DataDevice, p.DataInode, p.Server.MinecraftVersion, true, p.Entry, p.State, p.Manifest}
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
	f.s.adminPluginManageAcknowledge(rr, surfaceRequest(http.MethodPost, "/v1/admin/plugins/manage/acknowledge", string(body)))
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
			j, _, err := f.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginDisable)
			if err != nil {
				t.Fatal(err)
			}
			tx := pluginManageTransaction{"v1", j.OperationID, "disable", p.Fingerprint, p.DataPath, p.DataDevice, p.DataInode, p.Server.MinecraftVersion, true, p.Entry, p.State, p.Manifest}
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
			f.s.adminPluginManageAcknowledge(rr, surfaceRequest(http.MethodPost, "/v1/admin/plugins/manage/acknowledge", string(body)))
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
		if _, err = f.s.resolvePluginManage(context.Background(), action, "wKkoqHrH"); err == nil {
			t.Fatal("component received generic action")
		}
	}
}
