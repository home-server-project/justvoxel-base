package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSpigotPluginInventory(t *testing.T) {
	for _, version := range []string{"1.21.8", "26.3", "99.1-test"} {
		t.Run(version, func(t *testing.T) {
			newPluginInstallFixtureFor(t, "spigot", "1.21.8")
			server := pluginContentContext("spigot")
			server.InstanceID, server.MinecraftVersion = "jv-a1b2c3", version
			for _, tc := range []struct {
				name, raw string
				expected  []string
				ok        bool
			}{
				{"ordinary", "Plugins (1): ExamplePlugin\n", []string{"ExamplePlugin"}, true},
				{"normalized", "Plugins (1): exampleplugin", []string{"ExamplePlugin"}, true},
				{"multiple", "Plugins (2): ExamplePlugin, Dependency", []string{"ExamplePlugin", "Dependency"}, true},
				{"all required", "Plugins (1): ExamplePlugin", []string{"ExamplePlugin", "Dependency"}, false},
				{"absent", "Plugins (1): OtherPlugin", []string{"ExamplePlugin"}, false},
				{"prefix", "Plugins (1): ExamplePluginExtra", []string{"ExamplePlugin"}, false},
				{"short", "Plugins (1): Example", []string{"ExamplePlugin"}, false},
				{"minecraft colors", "§fPlugins (1): §aExamplePlugin§r", []string{"ExamplePlugin"}, true},
				{"ansi colors", "\x1b[0mPlugins (1): \x1b[32mExamplePlugin\x1b[0m\r\n", []string{"ExamplePlugin"}, true},
				{"combined ansi", "Plugins (1): \x1b[0;32mExamplePlugin\x1b[0m", []string{"ExamplePlugin"}, true},
				{"combined ansi disabled", "Plugins (1): \x1b[0;31mExamplePlugin\x1b[0m", []string{"ExamplePlugin"}, false},
				{"disabled", "Plugins (1): §cExamplePlugin§r", []string{"ExamplePlugin"}, false},
				{"ansi disabled", "Plugins (1): \x1b[31mExamplePlugin\x1b[0m", []string{"ExamplePlugin"}, false},
				{"inherited disabled", "Plugins (2): §cOtherPlugin, ExamplePlugin", []string{"ExamplePlugin"}, false},
				{"ansi inherited disabled", "Plugins (2): \x1b[31mOtherPlugin, ExamplePlugin", []string{"ExamplePlugin"}, false},
				{"explicit enabled after disabled", "Plugins (2): §cOtherPlugin, §aExamplePlugin", []string{"ExamplePlugin"}, true},
				{"red header", "§cPlugins (1): ExamplePlugin", []string{"ExamplePlugin"}, false},
				{"unrelated disabled", "Plugins (2): §aExamplePlugin§r, §cOtherPlugin§r", []string{"ExamplePlugin"}, true},
				{"embedded formatting", "Plugins (1): Example§aPlugin", []string{"ExamplePlugin"}, false},
				{"ansi embedded", "Plugins (1): Example\x1b[32mPlugin", []string{"ExamplePlugin"}, false},
				{"cursor escape", "Plugins (1): ExamplePlugin\x1b[2J", []string{"ExamplePlugin"}, false},
				{"duplicate", "Plugins (2): ExamplePlugin, exampleplugin", []string{"ExamplePlugin"}, false},
				{"duplicate wanted", "Plugins (1): ExamplePlugin", []string{"ExamplePlugin", "exampleplugin"}, false},
				{"no wanted", "Plugins (1): ExamplePlugin", nil, false},
				{"empty", "", []string{"ExamplePlugin"}, false},
				{"empty inventory", "Plugins (0):", []string{"ExamplePlugin"}, false},
				{"bad count", "Plugins (x): ExamplePlugin", []string{"ExamplePlugin"}, false},
				{"count mismatch", "Plugins (2): ExamplePlugin", []string{"ExamplePlugin"}, false},
				{"extra message", "Plugins (1): ExamplePlugin\nhello\n", []string{"ExamplePlugin"}, false},
				{"repeated inventory", "Plugins (1): ExamplePlugin\nPlugins (1): ExamplePlugin", []string{"ExamplePlugin"}, false},
				{"chat", "<Player> Plugins (1): ExamplePlugin", []string{"ExamplePlugin"}, false},
				{"unbounded", "Plugins (1): ExamplePlugin" + strings.Repeat(" ", spigotPluginListLimit), []string{"ExamplePlugin"}, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					old := pluginInstallCommand
					t.Cleanup(func() { pluginInstallCommand = old })
					pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
						if command != "podman" || !reflect.DeepEqual(args, []string{"exec", "minecraft-jv-a1b2c3", "rcon-cli", "plugins"}) {
							t.Fatal(command, args)
						}
						if _, ok := ctx.Deadline(); !ok {
							t.Fatal("unbounded command")
						}
						return []byte(tc.raw), nil
					}
					// No log directory exists: Spigot must never depend on Paper log evidence.
					err := verifyContentStartup(context.Background(), server.InstanceID, t.TempDir(), tc.expected, "plugins", server)
					if (err == nil) != tc.ok {
						t.Fatalf("proof=%v want success=%v", err, tc.ok)
					}
				})
			}
		})
	}
}

func TestContentRuntimeSelectsSoftware(t *testing.T) {
	old := pluginInstallCommand
	t.Cleanup(func() { pluginInstallCommand = old })
	pluginInstallCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("Paper/mod proof invoked RCON inventory")
		return nil, errors.New("unexpected command")
	}
	for _, version := range []string{"1.21.8", "99.1-test"} {
		for _, software := range []string{"paper", "purpur", "pufferfish", "leaf", "fabric", "forge", "neoforge", "quilt"} {
			server := pluginContentContext(software)
			server.InstanceID, server.MinecraftVersion = "jv-a1b2c3", version
			path := t.TempDir()
			if server.ContentKind == "plugins" {
				if verifyContentStartup(context.Background(), server.InstanceID, path, []string{"Root"}, "plugins", server) == nil {
					t.Fatal("missing Paper proof accepted")
				}
				writePluginStartup(t, path, pluginFixtureStartup)
			}
			if err := verifyContentStartup(context.Background(), server.InstanceID, path, []string{"Root"}, server.ContentKind, server); err != nil {
				t.Fatal(software, version, err)
			}
		}
	}
}

func TestSpigotInstallAndRecheckUseLiveInventory(t *testing.T) {
	for _, version := range []string{"1.21.8", "26.3"} {
		for _, recheck := range []bool{false, true} {
			for _, present := range []bool{false, true} {
				t.Run(version+"/"+strings.Join([]string{strconv.FormatBool(recheck), strconv.FormatBool(present)}, "/"), func(t *testing.T) {
					f := newPluginInstallFixtureFor(t, "spigot", version)
					// The generic fixture's releases must carry exact Bukkit evidence.
					for id, v := range f.versions {
						v.Loaders = []string{"bukkit"}
						v.GameVersions = []string{version}
						f.versions[id] = v
					}
					old := pluginInstallCommand
					calls := 0
					pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
						if command == "podman" && args[len(args)-1] == "plugins" {
							calls++
							if present {
								return []byte("Plugins (2): Root, Dependency"), nil
							}
							return []byte("Plugins (1): OtherPlugin"), nil
						}
						raw, err := old(ctx, command, args...)
						if command == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" {
							_ = os.Remove(filepath.Join(f.data, "logs", "latest.log"))
						}
						return raw, err
					}
					if recheck {
						journal, tx := pluginAttentionFixture(t, f)
						if err := os.WriteFile(filepath.Join(f.data, "plugins", "root.jar"), f.jars["root.jar"], 0644); err != nil {
							t.Fatal(err)
						}
						if _, err := commitPluginManifest(f.data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: tx.Artifacts}); err != nil {
							t.Fatal(err)
						}
						rr := httptest.NewRecorder()
						f.s.adminPluginInstallAcknowledge(rr, pluginTestRequest(http.MethodPost, "/", `{"operation_id":"`+journal.OperationID+`"}`))
						want := http.StatusConflict
						if present {
							want = http.StatusOK
						}
						if rr.Code != want {
							t.Fatal(rr.Code, rr.Body.String())
						}
					} else {
						journal := f.execute(t, f.plan(t), false)
						want := operationRolledBack
						if present {
							want = operationSucceeded
						}
						if journal.State != want {
							t.Fatal(journal.State, journal.Status)
						}
					}
					if calls != 1 {
						t.Fatal("live inventory calls", calls)
					}
				})
			}
		}
	}
}

func TestSpigotInventoryCommandFailure(t *testing.T) {
	f := newPluginInstallFixtureFor(t, "spigot", "1.21.8")
	server := pluginContentContext("spigot")
	server.InstanceID = "jv-a1b2c3"
	pluginInstallCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Plugins (1): Root"), errors.New("RCON failed")
	}
	if verifyContentStartup(context.Background(), server.InstanceID, f.data, []string{"Root"}, "plugins", server) == nil {
		t.Fatal("failed command accepted")
	}
}
