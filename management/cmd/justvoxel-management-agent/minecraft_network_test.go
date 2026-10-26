package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMinecraftNetworkOptionalAndStandaloneOverlay(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	configs[0].BedrockEnabled = true
	configs[0].BedrockPort = 19133
	original, err := store.renderInstanceRuntimes(ctx, configs)
	if err != nil {
		t.Fatal(err)
	}
	before := configs[0]
	standalone, err := networkOverlay(original[0], instanceRuntimeRecord{Runtime: before})
	if err != nil || !reflect.DeepEqual(standalone, original[0]) {
		t.Fatal("standalone definition changed", err)
	}
	member, err := networkOverlay(original[0], instanceRuntimeRecord{Runtime: before, NetworkMember: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(member.Quadlet), "PublishPort=") {
		t.Fatal("member publishes a host port")
	}
	for _, want := range []string{"Network=justvoxel-minecraft-network.network", "NetworkAlias=" + before.ID, "Volume=" + member.DataPath + ":/data:Z"} {
		if !strings.Contains(string(member.Quadlet), want) {
			t.Fatal("missing private backend integration", want)
		}
	}
	if !strings.Contains(string(member.Environment), "ONLINE_MODE=FALSE") || !strings.Contains(string(member.Environment), "PATCH_DEFINITIONS=") {
		t.Fatal("modern forwarding overlay absent")
	}
	if !reflect.DeepEqual(before, configs[0]) {
		t.Fatal("stored standalone settings changed")
	}
	// Disable/remove regenerates the complete prior standalone definition.
	restored, err := networkOverlay(original[0], instanceRuntimeRecord{Runtime: before})
	if err != nil || !bytes.Equal(restored.Quadlet, standalone.Quadlet) || !bytes.Equal(restored.Environment, standalone.Environment) {
		t.Fatal("standalone publication was not restored")
	}
	if networkDefaultState().Config.Enabled || len(networkQuadlets(networkDefaultState())) != 0 {
		t.Fatal("infrastructure is mandatory")
	}
}
func TestMinecraftNetworkSoftwareEligibility(t *testing.T) {
	for _, software := range []string{"paper", "purpur", "fabric", "neoforge", "spigot", "vanilla", "forge", "quilt", "bedrock"} {
		want := software == "paper" || software == "purpur"
		if networkEligible(software) != want {
			t.Fatal(software)
		}
		if !want {
			if _, err := networkOverlay(minecraftInstanceRuntimeFiles{}, instanceRuntimeRecord{Runtime: minecraftInstanceRuntimeConfig{Software: software}, NetworkMember: true}); err == nil {
				t.Fatal("unsupported software enrolled", software)
			}
		}
	}
}
func TestMinecraftNetworkWhitelistUnionAndIndependence(t *testing.T) {
	a := minecraftNetworkPlayer{UUID: "11111111-1111-1111-1111-111111111111", Name: "Alex"}
	b := minecraftNetworkPlayer{UUID: "00000000-0000-0000-0009-000000000001", Name: ".Bedrock"}
	c := minecraftNetworkPlayer{UUID: "33333333-3333-3333-3333-333333333333", Name: "Steve"}
	backend := []minecraftNetworkPlayer{a, b}
	union, err := networkMergePlayers(backend, []minecraftNetworkPlayer{a, c})
	if err != nil || len(union) != 3 {
		t.Fatal("initial union", union, err)
	}
	later, err := networkMergePlayers(union, []minecraftNetworkPlayer{b, c})
	if err != nil || len(later) != 3 {
		t.Fatal("later enrollment merge", err)
	}
	later[0].Name = "Changed"
	if backend[1].Name != ".Bedrock" {
		t.Fatal("network and backend share mutable whitelist state")
	}
	n := networkDefaultState()
	n.Config.Members = []string{"jv-a1b2c3"}
	n.Whitelist = union
	n.Imported = []string{"jv-a1b2c3"}
	n.Config.Members = nil
	if len(n.Whitelist) != 3 {
		t.Fatal("leaving backend removed network players")
	}
	if !networkContains(n.Imported, "jv-a1b2c3") {
		t.Fatal("import-once evidence was discarded")
	}
	if _, err = networkMergePlayers(union, []minecraftNetworkPlayer{{UUID: "invented", Name: "Steve"}}); err == nil {
		t.Fatal("unverified identity accepted")
	}
}
func TestMinecraftNetworkImmutableRoutingAndOnePrivateMount(t *testing.T) {
	n := networkDefaultState()
	n.Config.Enabled = true
	n.Config.Members = []string{"jv-a1b2c3"}
	n.ForwardingSecret = strings.Repeat("a", 64)
	n.FloodgateKey = bytes.Repeat([]byte{1}, 16)
	files := networkInfrastructure(n, map[string]string{"jv-a1b2c3": "Family Survival"})
	velocity := string(files["velocity/velocity.toml"])
	lobby := string(files["velocity/plugins/justvoxel-lobby/lobby.conf"])
	for _, want := range []string{`config-version = "2.9"`, `try = []`, `player-info-forwarding-mode = "modern"`, `forwarding-secret-file = "forwarding.secret"`} {
		if !strings.Contains(velocity, want) {
			t.Fatal("missing Velocity configuration", want)
		}
	}
	root := strings.SplitN(velocity, "[servers]", 2)[0]
	if !strings.Contains(root, "\nforced-hosts = {}\n") || strings.Contains(root, "[") {
		t.Fatal("forced-hosts must be an explicit empty root map", velocity)
	}
	for _, host := range []string{"lobby.example.com", "factions.example.com", "minigames.example.com"} {
		if strings.Contains(velocity, host) {
			t.Fatal("example forced host present", host)
		}
	}
	for _, forbidden := range []string{"jv-lobby", "justvoxel-picolobby", "Family Survival", `try = ["jv-a1b2c3"]`} {
		if strings.Contains(velocity, forbidden) {
			t.Fatal("invalid lobby routing", forbidden)
		}
	}
	if !strings.Contains(velocity, `jv-a1b2c3 = "jv-a1b2c3:25565"`) || strings.Contains(velocity, "Family Survival") {
		t.Fatal("friendly name used as routing identity")
	}
	for _, want := range []string{"schema=1\n", "welcome=" + base64.StdEncoding.EncodeToString([]byte(n.Config.WelcomeMessage)), "server=jv-a1b2c3:" + base64.StdEncoding.EncodeToString([]byte("Family Survival"))} {
		if !strings.Contains(lobby, want) {
			t.Fatal("missing virtual lobby configuration", want)
		}
	}
	for _, forbidden := range []string{n.ForwardingSecret, "floodgate", "whitelist", "/etc/", "secret"} {
		if strings.Contains(lobby, forbidden) {
			t.Fatal("private material in lobby configuration", forbidden)
		}
	}
	if string(files["velocity/plugins/global-whitelist/config.properties"]) != "white-list=true\nenforce-whitelist=true\n" {
		t.Fatal("front gate is not enforced")
	}
	count := 0
	for name, data := range networkQuadlets(n) {
		count += strings.Count(string(data), ":Z")
		if strings.Contains(name, "picolobby") || strings.Contains(string(data), "picolobby") {
			t.Fatal("legacy lobby generated normally")
		}
		if strings.Contains(string(data), ":z") {
			t.Fatal("shared SELinux label")
		}
	}
	if count != 1 || len(networkQuadlets(n)) != 2 {
		t.Fatal("expected one infrastructure mount and only Velocity plus network", count)
	}
	public, err := json.Marshal(n.Config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte(n.ForwardingSecret)) || bytes.Contains(public, []byte("floodgate_key")) {
		t.Fatal("private material in public configuration")
	}
}
func TestMinecraftNetworkVelocityFloodgateSchema(t *testing.T) {
	n := networkDefaultState()
	n.Config.Enabled = true
	n.FloodgateKey = bytes.Repeat([]byte{42}, 16)
	key := append([]byte(nil), n.FloodgateKey...)
	files := networkInfrastructure(n, nil)
	config := string(files["velocity/plugins/floodgate/config.yml"])
	// Check whole sections so a false value in another section cannot hide an
	// enabled linking or metrics object. No YAML dependency is needed here.
	for _, want := range []string{
		"key-file-name: key.pem\n", `username-prefix: "."`, "replace-spaces: true\n",
		"disconnect:\n  invalid-key: \"Please connect through the official Geyser\"\n  invalid-arguments-length: \"Expected {} arguments, got {}. Is Geyser up-to-date?\"\n",
		"player-link:\n  enabled: false\n  require-link: false\n  enable-own-linking: false\n  allowed: false\n  link-code-timeout: 300\n  type: sqlite\n  enable-global-linking: false\n",
		"metrics:\n  enabled: false\n  uuid: \"00000000-0000-0000-0000-000000000000\"\n",
		"send-floodgate-data: true\n", "config-version: 3\n",
	} {
		if strings.Count(config, want) != 1 {
			t.Fatalf("missing or duplicated Floodgate setting %q in %s", want, config)
		}
	}
	for _, forbidden := range []string{"enabled: true", "require-link: true", "enable-own-linking: true", "allowed: true", "enable-global-linking: true"} {
		if strings.Contains(config, forbidden) {
			t.Fatal("linking or telemetry enabled", forbidden)
		}
	}
	if !regexp.MustCompile(`(?m)^  uuid: "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"$`).MatchString(config) {
		t.Fatal("metrics UUID has invalid syntax")
	}
	if !bytes.Equal(files["velocity/plugins/floodgate/key.pem"], key) || !bytes.Equal(n.FloodgateKey, key) {
		t.Fatal("authoritative Floodgate key changed")
	}
	if !strings.Contains(string(files["velocity/plugins/Geyser-Velocity/config.yml"]), "  auth-type: floodgate\n") {
		t.Fatal("Geyser Floodgate authentication changed")
	}
	for name := range files {
		if strings.Contains(strings.ToLower(name), "floodgate") && name != "velocity/plugins/floodgate/config.yml" && name != "velocity/plugins/floodgate/key.pem" {
			t.Fatal("infrastructure generator wrote backend Floodgate configuration", name)
		}
	}
}

func TestMinecraftNetworkValidation(t *testing.T) {
	c := networkDefaultState().Config
	if err := networkValidateConfig(c); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []minecraftNetworkConfig{{JavaPort: 25565, BedrockPort: 25565}, {JavaPort: 25565, BedrockPort: 19132, Members: []string{"Friendly Name"}}, {JavaPort: 25565, BedrockPort: 19132, Members: []string{"jv-a1b2c3", "jv-a1b2c3"}}} {
		if networkValidateConfig(bad) == nil {
			t.Fatal("invalid network accepted", bad)
		}
	}
}

func TestMinecraftNetworkAdministrationPermissions(t *testing.T) {
	for _, role := range []principalRole{roleOperator, roleViewer} {
		mux := http.NewServeMux()
		registerMinecraftNetworkRoutes(mux, roleServerForTest(role))
		for _, route := range []struct{ method, path string }{{"GET", "/v1/minecraft/network"}, {"PUT", "/v1/minecraft/network"}, {"GET", "/v1/minecraft/network/whitelist"}, {"POST", "/v1/minecraft/network/whitelist"}, {"POST", "/v1/minecraft/network/recover"}} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest(route.method, route.path, `{}`))
			if rr.Code != http.StatusForbidden {
				t.Fatal("network administration permitted", role, route, rr.Code)
			}
		}
	}
}
func TestMinecraftNetworkRecoveryCannotTargetUnrelatedFiles(t *testing.T) {
	b := &instanceRuntimeBackend{root: "/etc/justvoxel/instances", quadlets: "/etc/containers/systemd"}
	j := minecraftNetworkJournal{Schema: 1, Before: networkDefaultState(), After: networkDefaultState()}
	j.Files = []minecraftNetworkFile{{Path: "/etc/shadow", Before: []byte("secret"), Existed: true}}
	if networkValidateJournal(b, j) == nil {
		t.Fatal("network recovery accepts an unrelated path")
	}
}

// The existing activation fixture mocks all host commands. These tests never
// start containers/services; running this suite still requires human direction.
func networkTransactionFixture(t *testing.T, twoSupported ...bool) (*instanceActivationFixture, minecraftNetworkConfig) {
	t.Helper()
	f := newInstanceActivationFixture(t)
	oldProbe := networkTCPReady
	networkTCPReady = func(context.Context, int) bool { return true }
	t.Cleanup(func() { networkTCPReady = oldProbe })
	if len(twoSupported) > 0 && twoSupported[0] {
		id := f.configs[1].ID
		r, err := f.backend.readRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		r.Runtime.Software = "purpur"
		r.Runtime.Version = f.configs[0].Version
		f.configs[1] = r.Runtime
		if err = f.backend.writeJSON(filepath.Join(f.backend.root, id, "config.json"), r, true); err != nil {
			t.Fatal(err)
		}
	}
	f.activateBoth()
	// Let production create the private network root with its required owner and
	// mode; t.TempDir() itself uses 0777 subject to umask, which may not be 0700.
	f.backend.networkRoot = filepath.Join(filepath.Dir(f.backend.root), "network")
	original := f.backend.command
	var commands sync.Mutex
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		commands.Lock()
		defer commands.Unlock()
		if command == minecraftNetworkHelper {
			return nil, nil
		}
		if command == "systemctl" {
			for _, arg := range args {
				if strings.HasPrefix(arg, "justvoxel-velocity") || strings.HasPrefix(arg, "justvoxel-picolobby") || arg == "justvoxel-minecraft-network-network.service" {
					if args[0] == "show" {
						return []byte("LoadState=not-found\nActiveState=inactive\nSubState=dead\n"), nil
					}
					return nil, nil
				}
			}
		}
		if command == "podman" {
			for _, arg := range args {
				if strings.Contains(arg, "justvoxel-velocity") || strings.Contains(arg, "justvoxel-picolobby") {
					return nil, nil
				}
			}
		}
		return original(ctx, command, args...)
	}
	c := networkDefaultState().Config
	c.Enabled = true
	c.JavaPort = f.configs[0].JavaPort
	c.BedrockPort = instanceTestPort(t)
	for c.BedrockPort == c.JavaPort || c.BedrockPort == f.configs[1].JavaPort {
		c.BedrockPort = instanceTestPort(t)
	}
	c.Members = []string{f.configs[0].ID}
	return f, c
}

func TestMinecraftNetworkRemovedFailurePolicy(t *testing.T) {
	units := []string{"justvoxel-velocity.service", "justvoxel-minecraft-network-network.service"}
	for _, tc := range []struct {
		name, state string
		reset       bool
	}{
		{"stale", "LoadState=not-found\nActiveState=failed\n", true},
		{"real-failure", "LoadState=loaded\nActiveState=failed\n", false},
		{"absent-inactive", "LoadState=not-found\nActiveState=inactive\n", false},
		{"loaded-active", "LoadState=loaded\nActiveState=active\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inspected, reset []string
			b := &instanceRuntimeBackend{}
			b.command = func(_ context.Context, command string, args ...string) ([]byte, error) {
				if command != "systemctl" || len(args) == 0 {
					t.Fatal("unexpected command", command, args)
				}
				unit := args[len(args)-1]
				if !networkContains(units, unit) {
					t.Fatal("inspected or reset unrelated service", args)
				}
				switch args[0] {
				case "show":
					if !reflect.DeepEqual(args, []string{"show", "--property=LoadState,ActiveState", "--", unit}) {
						t.Fatal("missing authoritative state inspection", args)
					}
					inspected = append(inspected, unit)
					return []byte(tc.state), nil
				case "reset-failed":
					if !reflect.DeepEqual(args, []string{"reset-failed", "--", unit}) || !networkContains(inspected, unit) {
						t.Fatal("global, implicit, or uninspected failure reset", args)
					}
					reset = append(reset, unit)
					return nil, nil
				default:
					t.Fatal("unexpected systemctl operation", args)
					return nil, nil
				}
			}
			if err := networkClearRemovedFailures(context.Background(), b); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inspected, units) || (tc.reset && !reflect.DeepEqual(reset, units)) || (!tc.reset && len(reset) != 0) {
				t.Fatal("incorrect failure cleanup", inspected, reset)
			}
		})
	}
}

func TestMinecraftNetworkRemovedFailureTransactionBoundaries(t *testing.T) {
	for _, mode := range []string{"disable", "failed-disable", "rollback-disabled", "recover-disabled", "recover-enabled"} {
		t.Run(mode, func(t *testing.T) {
			f, c := networkTransactionFixture(t)
			b := f.backend
			ctx := context.Background()
			beforeEnabled := mode == "disable" || mode == "failed-disable" || mode == "recover-enabled"
			if beforeEnabled {
				if err := networkApply(ctx, b, c); err != nil {
					t.Fatal(err)
				}
				c.Enabled = false
			}
			switch mode {
			case "failed-disable", "rollback-disabled":
				f.failReload = 1
			case "recover-disabled", "recover-enabled":
				f.failReload = 2
			}
			var inspected, reset []string
			reloaded := false
			original := b.command
			b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "systemctl" && len(args) > 0 {
					if args[0] == "show" && len(args) > 1 && args[1] == "--property=LoadState,ActiveState" {
						if !reloaded || networkPending(b) != nil {
							t.Fatal("cleanup before successful reload and pending-state clearance")
						}
						n, err := networkRead(b)
						if err != nil || n.Config.Enabled {
							t.Fatal("cleanup before disabled state was saved/restored", err)
						}
						for _, name := range []string{"justvoxel-velocity.container", "justvoxel-minecraft-network.network"} {
							if _, err := os.Stat(filepath.Join(b.quadlets, name)); !errors.Is(err, os.ErrNotExist) {
								t.Fatal("cleanup before infrastructure removal", name, err)
							}
						}
						unit := args[len(args)-1]
						if unit != "justvoxel-velocity.service" && unit != "justvoxel-minecraft-network-network.service" {
							t.Fatal("unrelated service inspected", args)
						}
						inspected = append(inspected, unit)
						return []byte("LoadState=not-found\nActiveState=failed\n"), nil
					}
					if args[0] == "reset-failed" {
						unit := args[len(args)-1]
						if !reflect.DeepEqual(args, []string{"reset-failed", "--", unit}) || !networkContains(inspected, unit) {
							t.Fatal("global, unrelated, or premature reset", args)
						}
						reset = append(reset, unit)
						return nil, nil
					}
					// Recovery to an enabled Network must preserve real failures.
					if mode == "recover-enabled" && args[0] == "show" && args[len(args)-1] == "justvoxel-velocity.service" {
						return []byte("LoadState=loaded\nActiveState=failed\nSubState=failed\n"), nil
					}
				}
				output, err := original(ctx, command, args...)
				if command == "systemctl" && reflect.DeepEqual(args, []string{"daemon-reload"}) && err == nil {
					reloaded = true
				}
				return output, err
			}
			err := networkApply(ctx, b, c)
			if (err == nil) != (mode == "disable") {
				t.Fatal("unexpected transaction result", err)
			}
			if mode == "recover-disabled" || mode == "recover-enabled" {
				if len(inspected) != 0 || len(reset) != 0 || networkPending(b) == nil {
					t.Fatal("incomplete rollback cleared failure evidence")
				}
				if err := networkRecover(ctx, b); err != nil {
					t.Fatal(err)
				}
			}
			var want []string
			if mode == "disable" || mode == "rollback-disabled" || mode == "recover-disabled" {
				want = []string{"justvoxel-velocity.service", "justvoxel-minecraft-network-network.service"}
			}
			if !reflect.DeepEqual(inspected, want) || !reflect.DeepEqual(reset, want) {
				t.Fatal("incorrect cleanup at durable transaction boundary", inspected, reset)
			}
			if networkPending(b) != nil {
				t.Fatal("completed transaction retained recovery guard")
			}
		})
	}
}

func TestMinecraftNetworkRemovedFailurePostCommitErrors(t *testing.T) {
	for _, mode := range []string{"inspect", "missing-load", "missing-active", "reset", "inspect-start", "missing-load-start", "missing-active-start", "reset-start"} {
		t.Run(mode, func(t *testing.T) {
			cleanupMode := strings.TrimSuffix(mode, "-start")
			f, c := networkTransactionFixture(t)
			b := f.backend
			ctx := context.Background()
			if err := networkApply(ctx, b, c); err != nil {
				t.Fatal(err)
			}
			id := c.Members[0]
			f.states[id], f.containers[id] = "active", "running"
			h := networkLifecycleControls(f)
			h.failStart = !strings.HasSuffix(mode, "-start")
			original := b.command
			b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "systemctl" && len(args) > 1 && args[0] == "show" && args[1] == "--property=LoadState,ActiveState" &&
					(args[len(args)-1] == "justvoxel-velocity.service" || args[len(args)-1] == "justvoxel-minecraft-network-network.service") {
					switch cleanupMode {
					case "inspect":
						return nil, errors.New("mock inspection failure")
					case "missing-load":
						return []byte("ActiveState=failed\n"), nil
					case "missing-active":
						return []byte("LoadState=not-found\n"), nil
					default:
						return []byte("LoadState=not-found\nActiveState=failed\n"), nil
					}
				}
				if command == "systemctl" && len(args) > 0 && args[0] == "reset-failed" {
					if cleanupMode != "reset" || !reflect.DeepEqual(args, []string{"reset-failed", "--", "justvoxel-velocity.service"}) {
						t.Fatal("unsafe failure reset", args)
					}
					return nil, errors.New("mock reset failure")
				}
				return original(ctx, command, args...)
			}
			c.Enabled = false
			err := networkApply(ctx, b, c)
			if err == nil || !strings.Contains(err.Error(), "configuration was saved, but stale Network service failure state could not be cleared") {
				t.Fatal("missing post-commit cleanup error", err)
			}
			wantCleanupError := "incomplete systemd state for justvoxel-velocity.service"
			if cleanupMode == "inspect" {
				wantCleanupError = "mock inspection failure"
			} else if cleanupMode == "reset" {
				wantCleanupError = "mock reset failure"
			}
			if !strings.Contains(err.Error(), wantCleanupError) {
				t.Fatal("cleanup diagnosis was lost", err)
			}
			restorationMessage := "Minecraft Network configuration was saved, but one or more previously running servers could not be started. Use their normal Start controls after checking server status."
			if strings.Contains(err.Error(), restorationMessage) != h.failStart || strings.Contains(err.Error(), "mock start failure") {
				t.Fatal("incorrect joined server restoration error", err)
			}
			joined, ok := err.(interface{ Unwrap() []error })
			if !ok {
				t.Fatal("post-commit errors were not joined", err)
			}
			parts := joined.Unwrap()
			if h.failStart {
				if len(parts) != 2 || !strings.Contains(parts[0].Error(), wantCleanupError) || parts[1].Error() != restorationMessage {
					t.Fatal("cleanup and generic restoration errors were not both retained", err)
				}
			} else if len(parts) != 1 || !strings.Contains(parts[0].Error(), wantCleanupError) || err.Error() != parts[0].Error() {
				t.Fatal("successful restoration returned an extra failure", err)
			}
			n, readErr := networkRead(b)
			if readErr != nil || n.Config.Enabled || networkPending(b) != nil {
				t.Fatal("cleanup failure rolled back or stranded committed disable", readErr)
			}
			if !reflect.DeepEqual(h.starts, []string{id}) || (!h.failStart && f.states[id] != "active") {
				t.Fatal("post-commit error prevented normal server restoration", h.starts)
			}
		})
	}
}

func TestMinecraftNetworkRemovedFailurePostRollbackErrors(t *testing.T) {
	for _, mode := range []string{"inspect", "missing-load", "missing-active", "reset"} {
		t.Run(mode, func(t *testing.T) {
			f, c := networkTransactionFixture(t)
			b := f.backend
			id := c.Members[0]
			f.states[id], f.containers[id] = "active", "running"
			h := networkLifecycleControls(f)
			f.failReload = 1
			original := b.command
			b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "systemctl" && len(args) > 1 && args[len(args)-1] == "justvoxel-velocity.service" {
					if args[0] == "show" && args[1] == "--property=LoadState,ActiveState" {
						if networkPending(b) != nil {
							t.Fatal("cleanup preceded completed rollback")
						}
						switch mode {
						case "inspect":
							return nil, errors.New("mock inspection failure")
						case "missing-load":
							return []byte("ActiveState=failed\n"), nil
						case "missing-active":
							return []byte("LoadState=not-found\n"), nil
						default:
							return []byte("LoadState=not-found\nActiveState=failed\n"), nil
						}
					}
					if args[0] == "reset-failed" {
						return nil, errors.New("mock reset failure")
					}
				}
				return original(ctx, command, args...)
			}
			err := networkApply(context.Background(), b, c)
			if err == nil || !strings.Contains(err.Error(), "changes failed and were rolled back") ||
				!strings.Contains(err.Error(), "configuration was restored, but stale Network service failure state could not be cleared") ||
				strings.Contains(err.Error(), "needs recovery") || strings.Contains(err.Error(), "could not be restored") {
				t.Fatal("cleanup diagnosis obscured successful rollback or restoration", err)
			}
			n, readErr := networkRead(b)
			if readErr != nil || n.Config.Enabled || networkPending(b) != nil || !reflect.DeepEqual(h.starts, []string{id}) || f.states[id] != "active" {
				t.Fatal("cleanup failure stranded rolled-back server", readErr, h.starts)
			}
		})
	}
}

func TestMinecraftNetworkPublicationAndStandaloneRestoration(t *testing.T) {
	f, c := networkTransactionFixture(t)
	b := f.backend
	ctx := context.Background()
	id := f.configs[0].ID
	before, err := b.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	a := minecraftNetworkPlayer{UUID: "11111111-1111-1111-1111-111111111111", Name: "Alex"}
	players, _ := json.Marshal([]minecraftNetworkPlayer{a})
	whitelist := filepath.Join(before.Storage.DataPath, "whitelist.json")
	if err = os.WriteFile(whitelist, players, 0600); err != nil {
		t.Fatal(err)
	}
	if err = networkApply(ctx, b, c); err != nil {
		t.Fatal(err)
	}
	member, err := b.readRecord(id)
	if err != nil || !member.NetworkMember || member.Runtime != before.Runtime || len(member.Firewall) != 0 {
		t.Fatal("member standalone configuration changed", err)
	}
	n, err := networkRead(b)
	if err != nil || len(n.Whitelist) != 1 || !networkContains(n.Imported, id) {
		t.Fatal("initial import not committed", err)
	}
	a.Name = "LaterBackendName"
	later, _ := json.Marshal([]minecraftNetworkPlayer{a})
	if err = os.WriteFile(whitelist, later, 0600); err != nil {
		t.Fatal(err)
	}
	if err = networkApply(ctx, b, c); err != nil {
		t.Fatal(err)
	}
	n, err = networkRead(b)
	if err != nil || n.Whitelist[0].Name != "Alex" {
		t.Fatal("backend whitelist was continuously synchronized", err)
	}
	c.Enabled = false
	if err = networkApply(ctx, b, c); err != nil {
		t.Fatal(err)
	}
	restored, err := b.readRecord(id)
	if err != nil || restored.NetworkMember || restored.Runtime != before.Runtime || restored.EnvironmentSHA256 != before.EnvironmentSHA256 || restored.QuadletSHA256 != before.QuadletSHA256 {
		t.Fatal("standalone runtime did not restore", err)
	}
	contents, _ := os.ReadFile(whitelist)
	if !bytes.Equal(contents, later) {
		t.Fatal("network changed the backend whitelist")
	}
	n, err = networkRead(b)
	if err != nil || len(n.Whitelist) != 1 {
		t.Fatal("disable removed network players", err)
	}
	if _, err = os.Stat(filepath.Join(b.quadlets, "justvoxel-velocity.container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled proxy retained an active definition")
	}
}
func TestMinecraftNetworkInterruptedPublicationRetainsUsableRecovery(t *testing.T) {
	f, c := networkTransactionFixture(t)
	b := f.backend
	f.failReload = 2 // First publication and its immediate rollback both lose reload.
	if err := networkApply(context.Background(), b, c); err == nil {
		t.Fatal("failed publication claimed success")
	}
	if networkPending(b) == nil {
		t.Fatal("recovery evidence lost")
	}
	if _, err := b.records(); err != nil {
		t.Fatal("intermediate hashes prevent recovery lock acquisition", err)
	}
	old := instanceRuntimes
	instanceRuntimes = b
	t.Cleanup(func() { instanceRuntimes = old })
	rr := httptest.NewRecorder()
	s := roleServerForTest(roleAdministrator)
	s.minecraftNetworkStatus(rr, authorizedRequest("GET", "/v1/minecraft/network", ""))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"recovery_required":true`) {
		t.Fatal("recovery UI cannot discover pending publication", rr.Code, rr.Body.String())
	}
	if err := networkRecover(context.Background(), b); err != nil {
		t.Fatal("retry recovery stranded the network", err)
	}
	if networkPending(b) != nil {
		t.Fatal("successful recovery did not release startup guard")
	}
	n, err := networkRead(b)
	if err != nil || n.Config.Enabled {
		t.Fatal("recovery did not restore optional standalone mode", err)
	}
}
func TestMinecraftNetworkPublicStatusOmitsSecrets(t *testing.T) {
	f, c := networkTransactionFixture(t)
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	n, err := networkRead(f.backend)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	roleServerForTest(roleAdministrator).minecraftNetworkStatus(rr, authorizedRequest("GET", "/v1/minecraft/network", ""))
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	for _, private := range []string{n.ForwardingSecret, "forwarding_secret", "floodgate_key", "RCON_PASSWORD", minecraftNetworkRoot} {
		if strings.Contains(rr.Body.String(), private) {
			t.Fatal("private state in network response", private)
		}
	}
}

func TestMinecraftNetworkLaterEnrollmentImportsExactlyOnce(t *testing.T) {
	a := minecraftNetworkPlayer{UUID: "11111111-1111-1111-1111-111111111111", Name: "Alex"}
	b := minecraftNetworkPlayer{UUID: "22222222-2222-2222-2222-222222222222", Name: "Steve"}
	state := networkDefaultState()
	reads := 0
	load := func(players ...minecraftNetworkPlayer) func() ([]minecraftNetworkPlayer, error) {
		return func() ([]minecraftNetworkPlayer, error) { reads++; return players, nil }
	}
	var err error
	state, err = networkEnrollWhitelist(state, "jv-a1b2c3", load(a))
	if err != nil {
		t.Fatal(err)
	}
	state, err = networkEnrollWhitelist(state, "jv-d4e5f6", load(a, b))
	if err != nil || len(state.Whitelist) != 2 || reads != 2 {
		t.Fatal("later member did not merge its whitelist", err)
	}
	state.Whitelist = []minecraftNetworkPlayer{b} // Explicit front-gate removal.
	state, err = networkEnrollWhitelist(state, "jv-a1b2c3", load(a))
	if err != nil || reads != 2 || len(state.Whitelist) != 1 || state.Whitelist[0] != b {
		t.Fatal("re-enrollment resurrected a removed player", err)
	}
}

// Serialize mock host state while allowing independent lifecycle countdowns.
// No real timer or host service is used by these tests.
type networkLifecycleFixture struct {
	mu                     sync.Mutex
	queued                 map[string]bool
	lastWarning            map[string]bool
	warnings               map[string][]string
	stops, starts          []string
	published              bool
	failPrepare, failStart bool
}

func networkLifecycleControls(f *instanceActivationFixture) *networkLifecycleFixture {
	h := &networkLifecycleFixture{queued: map[string]bool{}, lastWarning: map[string]bool{}, warnings: map[string][]string{}}
	original := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if command == minecraftNetworkHelper {
			if args[0] == "prepare" && h.failPrepare {
				return nil, errors.New("mock prepare failure")
			}
			if args[0] == "publish" {
				h.published = true
			}
		}
		if command == "podman" && len(args) == 4 && args[0] == "exec" && strings.HasPrefix(args[3], "say ") {
			id := strings.TrimPrefix(args[1], "minecraft-")
			h.warnings[id] = append(h.warnings[id], args[3])
			if args[3] == "say Server shutting down in 1 second" {
				h.lastWarning[id] = true
			}
			return nil, nil
		}
		if command == "systemctl" {
			id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "minecraft-"), ".service")
			if instanceIDPattern.MatchString(id) {
				if args[0] == "show" && h.queued[id] && h.lastWarning[id] {
					f.states[id], f.containers[id] = "inactive", "exited"
				}
				if args[0] == "stop" {
					h.stops = append(h.stops, id)
				}
				if len(args) > 1 && args[0] == "--no-block" {
					if args[1] == "stop" {
						h.stops = append(h.stops, id)
						h.queued[id] = true
						return nil, nil // Stay running until the final normal warning.
					}
					if args[1] == "start" {
						h.starts = append(h.starts, id)
						delete(h.queued, id)
						if h.failStart {
							return nil, errors.New("mock start failure")
						}
					}
				}
			}
		}
		return original(ctx, command, args...)
	}
	return h
}

// Explicit regression: zero players == immediate stop, with no announcement or pause.
func TestMinecraftNetworkZeroPlayersImmediateStopAndRestart(t *testing.T) {
	f, c := networkTransactionFixture(t)
	id, unrelated := f.configs[0].ID, f.configs[1].ID
	f.states[id], f.containers[id] = "active", "running"
	f.states[unrelated], f.containers[unrelated] = "active", "running"
	h := networkLifecycleControls(f)
	f.backend.pause = func(context.Context, time.Duration) error { t.Error("zero players must never pause"); return nil }
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.stops, []string{id}) || !reflect.DeepEqual(h.starts, []string{id}) || len(h.warnings[id]) != 0 {
		t.Fatal("zero-player lifecycle or unrelated isolation changed", h.stops, h.starts, h.warnings)
	}
	if f.states[id] != "active" || f.states[unrelated] != "active" {
		t.Fatal("running state not preserved")
	}
	r, err := f.backend.readRecord(id)
	if err != nil || !r.NetworkMember {
		t.Fatal("new member not published", err)
	}
}

func TestMinecraftNetworkOccupiedNormalCountdownAndRestart(t *testing.T) {
	f, c := networkTransactionFixture(t)
	id := f.configs[0].ID
	f.states[id], f.containers[id] = "active", "running"
	f.players = "There are 1 of a max of 20 players online: Alex"
	h := networkLifecycleControls(f)
	var paused time.Duration
	f.backend.pause = func(_ context.Context, d time.Duration) error { paused += d; return nil }
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, seconds := range []string{"60 seconds", "30 seconds", "15 seconds", "10 seconds", "5 seconds", "4 seconds", "3 seconds", "2 seconds", "1 second"} {
		want = append(want, "say Server shutting down in "+seconds)
	}
	if !reflect.DeepEqual(h.warnings[id], want) || paused != time.Minute || !reflect.DeepEqual(h.starts, []string{id}) {
		t.Fatal("normal countdown changed", h.warnings, paused, h.starts)
	}
}

func TestMinecraftNetworkUnknownPlayersPreservesConfiguration(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		f, c := networkTransactionFixture(t)
		id := f.configs[0].ID
		f.states[id], f.containers[id] = "active", "running"
		f.players, f.failPlayers = "unknown", unavailable
		h := networkLifecycleControls(f)
		before, err := f.backend.readRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		if networkApply(context.Background(), f.backend, c) == nil {
			t.Fatal("unknown players interrupted")
		}
		after, err := f.backend.readRecord(id)
		if err != nil || !reflect.DeepEqual(before, after) || h.published || len(h.stops) != 0 || len(h.starts) != 0 {
			t.Fatal("unknown players mutated configuration or lifecycle", err)
		}
		if n, err := networkRead(f.backend); err != nil || n.Config.Enabled {
			t.Fatal("network configuration mutated", err)
		}
	}
}

func TestMinecraftNetworkStoppedAndSafelyFailedMembersRemainNonRunning(t *testing.T) {
	for _, state := range []string{"inactive", "failed"} {
		f, c := networkTransactionFixture(t)
		id := f.configs[0].ID
		f.states[id], f.containers[id] = state, "exited"
		h := networkLifecycleControls(f)
		if err := networkApply(context.Background(), f.backend, c); err != nil {
			t.Fatal(err)
		}
		if len(h.stops) != 0 || len(h.starts) != 0 || f.states[id] != state {
			t.Fatal("non-running member was started")
		}
	}
}

func TestMinecraftNetworkRunningMemberLeavesAndRestartsStandalone(t *testing.T) {
	f, c := networkTransactionFixture(t)
	id := f.configs[0].ID
	standalone, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	f.states[id], f.containers[id] = "active", "running"
	h := networkLifecycleControls(f)
	c.Members = nil
	// Keep the front gate off the returning standalone server's reserved port.
	for c.JavaPort == f.configs[0].JavaPort || c.JavaPort == f.configs[1].JavaPort || c.JavaPort == c.BedrockPort {
		c.JavaPort = instanceTestPort(t)
	}
	if err = networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	r, err := f.backend.readRecord(id)
	if err != nil || r.NetworkMember || r.EnvironmentSHA256 != standalone.EnvironmentSHA256 || r.QuadletSHA256 != standalone.QuadletSHA256 || !reflect.DeepEqual(h.stops, []string{id}) || !reflect.DeepEqual(h.starts, []string{id}) {
		t.Fatal("leaving member did not resume standalone", err)
	}
}

func TestMinecraftNetworkFailureRestoresOnlyWhenRecoverySafe(t *testing.T) {
	for _, mode := range []string{"prepare", "rollback", "pending"} {
		t.Run(mode, func(t *testing.T) {
			f, c := networkTransactionFixture(t)
			id := f.configs[0].ID
			f.states[id], f.containers[id] = "active", "running"
			h := networkLifecycleControls(f)
			switch mode {
			case "prepare":
				h.failPrepare = true
			case "rollback":
				f.failReload = 1
			case "pending":
				f.failReload = 2
			}
			if networkApply(context.Background(), f.backend, c) == nil {
				t.Fatal("failed transaction claimed success")
			}
			if mode == "pending" {
				if networkPending(f.backend) == nil || len(h.starts) != 0 {
					t.Fatal("pending recovery restarted a backend")
				}
				if err := networkRecover(context.Background(), f.backend); err != nil {
					t.Fatal("existing retry recovery failed", err)
				}
			} else {
				if networkPending(f.backend) != nil || !reflect.DeepEqual(h.starts, []string{id}) {
					t.Fatal("safe failure did not restore running backend")
				}
				r, err := f.backend.readRecord(id)
				if err != nil || r.NetworkMember {
					t.Fatal("old runtime not restored", err)
				}
			}
		})
	}
}

func TestMinecraftNetworkConcurrentOccupiedStopWave(t *testing.T) {
	f, c := networkTransactionFixture(t, true)
	c.Members = []string{f.configs[0].ID, f.configs[1].ID}
	for _, id := range c.Members {
		f.states[id], f.containers[id] = "active", "running"
	}
	f.players = "There are 1 of a max of 20 players online: Alex"
	h := networkLifecycleControls(f)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.backend.pause = func(ctx context.Context, d time.Duration) error {
		if d == 30*time.Second {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- networkApply(ctx, f.backend, c) }()
	for range c.Members {
		select {
		case <-entered:
		case <-ctx.Done():
			cancel()
			<-done
			t.Fatal("occupied countdowns serialized")
		}
	}
	h.mu.Lock()
	published := h.published
	h.mu.Unlock()
	close(release)
	err := <-done
	if published {
		t.Fatal("publication preceded safe stops")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(h.stops) != 2 || !reflect.DeepEqual(h.starts, c.Members) {
		t.Fatal("stop wave did not resume all members", h.stops, h.starts)
	}
	for _, id := range c.Members {
		if len(h.warnings[id]) != 9 {
			t.Fatal("occupied warnings lost", id, h.warnings[id])
		}
	}
}

func TestMinecraftNetworkCommittedStartFailuresDoNotRollbackAndAttemptAll(t *testing.T) {
	f, c := networkTransactionFixture(t, true)
	c.Members = []string{f.configs[0].ID, f.configs[1].ID}
	for _, id := range c.Members {
		f.states[id], f.containers[id] = "active", "running"
	}
	h := networkLifecycleControls(f)
	h.failStart = true
	err := networkApply(context.Background(), f.backend, c)
	if err == nil || !strings.Contains(err.Error(), "configuration was saved") || !reflect.DeepEqual(h.starts, c.Members) {
		t.Fatal("missing actionable committed start failure", err, h.starts)
	}
	if networkPending(f.backend) != nil {
		t.Fatal("start failure created configuration recovery")
	}
	for _, id := range c.Members {
		r, err := f.backend.readRecord(id)
		if err != nil || !r.NetworkMember {
			t.Fatal("committed configuration rolled back", err)
		}
	}
}

func TestMinecraftNetworkUnsafeAffectedStateDoesNotInterrupt(t *testing.T) {
	for _, state := range []string{"activating", "deactivating", "unknown", "failed"} {
		f, c := networkTransactionFixture(t)
		id := f.configs[0].ID
		f.states[id], f.containers[id] = state, "running"
		h := networkLifecycleControls(f)
		if networkApply(context.Background(), f.backend, c) == nil {
			t.Fatal("unsafe state accepted", state)
		}
		if h.published || len(h.stops) != 0 || len(h.starts) != 0 {
			t.Fatal("unsafe state interrupted or published", state)
		}
	}
}

func TestMinecraftNetworkIncompleteStopNeverStartsConflictingLifecycle(t *testing.T) {
	f, c := networkTransactionFixture(t)
	id := f.configs[0].ID
	f.states[id], f.containers[id] = "active", "running"
	f.players = "There are 1 of a max of 20 players online: Alex"
	h := networkLifecycleControls(f)
	f.backend.pause = func(context.Context, time.Duration) error {
		h.mu.Lock()
		f.states[id], f.containers[id] = "deactivating", "stopping"
		h.mu.Unlock()
		return context.DeadlineExceeded
	}
	if networkApply(context.Background(), f.backend, c) == nil {
		t.Fatal("incomplete shutdown accepted")
	}
	if h.published || len(h.starts) != 0 {
		t.Fatal("incomplete stop published or issued conflicting start")
	}
}

func TestMinecraftNetworkPartialStopFailureRestoresSafePeer(t *testing.T) {
	f, c := networkTransactionFixture(t, true)
	c.Members = []string{f.configs[0].ID, f.configs[1].ID}
	for _, id := range c.Members {
		f.states[id], f.containers[id] = "active", "running"
	}
	h := networkLifecycleControls(f)
	original := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "podman" && len(args) == 4 && args[0] == "exec" && args[1] == "minecraft-"+c.Members[1] && args[3] == "list" {
			return nil, errors.New("unverifiable players")
		}
		return original(ctx, command, args...)
	}
	if networkApply(context.Background(), f.backend, c) == nil {
		t.Fatal("failed stop wave published")
	}
	if h.published || !reflect.DeepEqual(h.starts, c.Members[:1]) || !reflect.DeepEqual(h.stops, c.Members[:1]) {
		t.Fatal("safe stopped peer was not restored", h.stops, h.starts)
	}
}

func TestMinecraftNetworkRunningExistingMemberResumesAfterConfigurationChange(t *testing.T) {
	f, c := networkTransactionFixture(t)
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	id := f.configs[0].ID
	f.states[id], f.containers[id] = "active", "running"
	h := networkLifecycleControls(f)
	c.WelcomeMessage = "Updated welcome"
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	n, err := networkRead(f.backend)
	if err != nil || n.Config.WelcomeMessage != c.WelcomeMessage || !reflect.DeepEqual(h.stops, []string{id}) || !reflect.DeepEqual(h.starts, []string{id}) {
		t.Fatal("existing running member did not resume with committed configuration", err, h.stops, h.starts)
	}
}

func TestMinecraftNetworkJavaStartupReadinessStopsBothMembers(t *testing.T) {
	f, c := networkTransactionFixture(t, true)
	c.Members = []string{f.configs[0].ID, f.configs[1].ID}
	// A third running server must remain outside the selected stop/restart wave.
	unrelated := f.configs[0]
	unrelated.ID, unrelated.StartOnBoot = "jv-112233", false
	for {
		unrelated.JavaPort = instanceTestPort(t)
		if unrelated.JavaPort != c.JavaPort && unrelated.JavaPort != c.BedrockPort && unrelated.JavaPort != f.configs[1].JavaPort {
			break
		}
	}
	registerLogicalForTest(t, f.backend.store, "112233", unrelated.Software, "Unrelated")
	dir, _, _ := f.backend.paths(unrelated.ID)
	private, err := f.backend.privateDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	private.Close()
	r := instanceRuntimeRecord{SchemaVersion: 1, Runtime: unrelated, Storage: instanceStorageFor(f.snapshot, unrelated.ID), Firewall: []instanceFirewallRule{}}
	if err := f.backend.writeJSON(filepath.Join(dir, "config.json"), r, true); err != nil {
		t.Fatal(err)
	}
	if err := f.apply(unrelated.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range append(append([]string{}, c.Members...), unrelated.ID) {
		f.states[id], f.containers[id] = "active", "running"
	}
	h := networkLifecycleControls(f)
	original := f.backend.command
	var mu sync.Mutex
	attempts := map[string]int{}
	pauses := 0
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if command == "podman" && len(args) == 4 && args[0] == "exec" && args[3] == "list" {
			id := strings.TrimPrefix(args[1], "minecraft-")
			if !networkContains(c.Members, id) {
				t.Fatal("queried unrelated console", id)
			}
			attempts[id]++
			if attempts[id] <= 2 {
				return nil, errors.New("console not ready")
			}
			return []byte("There are 0 of a max of 10 players online:"), nil
		}
		if command == "systemctl" && (args[0] == "stop" || args[0] == "--no-block") {
			id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "minecraft-"), ".service")
			if instanceIDPattern.MatchString(id) && (!networkContains(c.Members, id) || attempts[id] != 3) {
				t.Fatal("interruption without verified selected players", id, attempts)
			}
		}
		return original(ctx, command, args...)
	}
	f.backend.pause = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		if d != 2*time.Second {
			t.Error("unexpected occupied countdown", d)
		}
		pauses++
		return nil
	}
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	if pauses != 4 || len(h.stops) != 2 || !reflect.DeepEqual(h.starts, c.Members) || len(h.warnings) != 0 {
		t.Fatal("startup lifecycle changed", pauses, h.stops, h.starts, h.warnings)
	}
	for _, id := range c.Members {
		stops := 0
		for _, stopped := range h.stops {
			if stopped == id {
				stops++
			}
		}
		r, err := f.backend.readRecord(id)
		if stops != 1 || attempts[id] != 3 || f.states[id] != "active" || f.containers[id] != "running" || err != nil || !r.NetworkMember {
			t.Fatal("member was not safely stopped, published and restarted", id, stops, attempts[id], err)
		}
	}
	if f.states[unrelated.ID] != "active" || f.containers[unrelated.ID] != "running" {
		t.Fatal("unrelated server interrupted")
	}
}

func TestMinecraftNetworkRuntimeStatus(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		enabled, proxy, listener, recovery, want bool
	}{
		{"disabled", false, true, true, false, false},
		{"ready", true, true, true, false, true},
		{"proxy unavailable", true, false, true, false, false},
		{"listener unavailable", true, true, false, false, false},
		{"recovery required", true, true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := networkTransactionFixture(t)
			if err := networkApply(context.Background(), f.backend, c); err != nil {
				t.Fatal(err)
			}
			n, err := networkRead(f.backend)
			if err != nil {
				t.Fatal(err)
			}
			n.Config.Enabled = tc.enabled
			if err := networkWriteState(f.backend, n); err != nil {
				t.Fatal(err)
			}
			if tc.recovery {
				if err := f.backend.writeJSON(filepath.Join(f.backend.networkDirectory(), "pending.json"), minecraftNetworkJournal{}, false); err != nil {
					t.Fatal(err)
				}
			}
			original := f.backend.command
			f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
				if cmd == "systemctl" && len(args) == 3 && args[0] == "is-active" && args[1] == "--quiet" {
					if args[2] != "justvoxel-velocity.service" {
						t.Fatal("unexpected infrastructure readiness probe", args)
					}
					if !tc.proxy {
						return nil, errors.New("private runtime error")
					}
				}
				return original(ctx, cmd, args...)
			}
			probes := 0
			networkTCPReady = func(_ context.Context, port int) bool {
				probes++
				if port != c.JavaPort {
					t.Fatal("wrong Java entrance", port)
				}
				return tc.listener
			}
			old := instanceRuntimes
			instanceRuntimes = f.backend
			t.Cleanup(func() { instanceRuntimes = old })
			rr := httptest.NewRecorder()
			roleServerForTest(roleAdministrator).minecraftNetworkStatus(rr, authorizedRequest("GET", "/v1/minecraft/network", ""))
			var status struct {
				RuntimeReady bool                   `json:"runtime_ready"`
				Config       minecraftNetworkConfig `json:"config"`
			}
			if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &status) != nil || status.RuntimeReady != tc.want || status.Config.Enabled != tc.enabled {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `"runtime_ready":`) || strings.Contains(rr.Body.String(), "private runtime error") {
				t.Fatal(rr.Body.String())
			}
			if (!tc.enabled || tc.recovery || !tc.proxy) && probes != 0 {
				t.Fatal("unnecessary listener probe")
			}
		})
	}
}

func TestMinecraftNetworkReadinessTimeoutKeepsCommitAndRestartsBackends(t *testing.T) {
	f, c := networkTransactionFixture(t, true)
	c.Members = []string{f.configs[0].ID, f.configs[1].ID}
	for _, id := range c.Members {
		f.states[id], f.containers[id] = "active", "running"
	}
	h := networkLifecycleControls(f)
	probes, waits := 0, 0
	networkTCPReady = func(_ context.Context, port int) bool {
		probes++
		n, err := networkRead(f.backend)
		if err != nil || !n.Config.Enabled || networkPending(f.backend) != nil || !h.published || port != c.JavaPort {
			t.Fatal("readiness probed before durable commit", err)
		}
		return false
	}
	f.backend.pause = func(ctx context.Context, d time.Duration) error {
		if d != time.Second {
			t.Fatal("unexpected cadence", d)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("missing bounded readiness context")
		}
		waits++
		return nil
	}
	err := networkApply(context.Background(), f.backend, c)
	if err == nil || err.Error() != networkNotReadyMessage || waits != 30 || probes != 31 || !reflect.DeepEqual(h.starts, c.Members) {
		t.Fatal(err, waits, probes, h.starts)
	}
	if networkPending(f.backend) != nil {
		t.Fatal("runtime failure created recovery")
	}
	for _, id := range c.Members {
		r, err := f.backend.readRecord(id)
		if err != nil || !r.NetworkMember {
			t.Fatal("committed membership rolled back", err)
		}
	}
}

func TestMinecraftNetworkReadinessWaitAndParentCancellation(t *testing.T) {
	f, c := networkTransactionFixture(t)
	probes, waits := 0, 0
	networkTCPReady = func(context.Context, int) bool { probes++; return probes == 3 }
	f.backend.pause = func(context.Context, time.Duration) error { waits++; return nil }
	if !networkWaitReady(context.Background(), f.backend, c.JavaPort) || waits != 2 {
		t.Fatal("delayed readiness was not observed", waits)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if networkWaitReady(ctx, f.backend, c.JavaPort) || probes != 3 {
		t.Fatal("parent cancellation ignored")
	}
}

func TestMinecraftNetworkLobbyTextRoundTrip(t *testing.T) {
	n := networkDefaultState()
	n.Config.Enabled = true
	n.Config.Members = []string{"jv-a1b2c3"}
	n.Config.WelcomeMessage = `Welcome : = "friends" \ ☃`
	for _, name := range []string{"Family's Survival", `Quotes " : = \ — 世界`, "Name\nserver=jv-evil00:injection"} {
		files := networkInfrastructure(n, map[string]string{"jv-a1b2c3": name})
		config := string(files["velocity/plugins/justvoxel-lobby/lobby.conf"])
		lines := strings.Split(strings.TrimSuffix(config, "\n"), "\n")
		if len(lines) != 3 || lines[0] != "schema=1" {
			t.Fatal("text changed line grammar", config)
		}
		for encoded, expected := range map[string]string{
			strings.TrimPrefix(lines[1], "welcome="):          n.Config.WelcomeMessage,
			strings.TrimPrefix(lines[2], "server=jv-a1b2c3:"): name,
		} {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || string(decoded) != expected {
				t.Fatal("lobby text did not round trip", err)
			}
		}
	}
}

func networkLegacyFixture(t *testing.T) (*instanceActivationFixture, minecraftNetworkConfig, []byte) {
	t.Helper()
	f, c := networkTransactionFixture(t)
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	state, err := networkRead(f.backend)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte("[Container]\nImage=ghcr.io/home-server-project/picolobby:stable\n[Service]\nExecStartPre=" + minecraftNetworkHelper + " start-picolobby\n")
	if err := f.backend.writePrivate(filepath.Join(f.backend.quadlets, "justvoxel-picolobby.container"), legacy, false); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.writeJSON(filepath.Join(f.backend.networkDirectory(), "generated.json"), networkLegacyInfrastructure(state, map[string]string{c.Members[0]: "Family"}), true); err != nil {
		t.Fatal(err)
	}
	return f, c, legacy
}

func TestMinecraftNetworkMigratesLegacyQuadletAndStartsOnlyVelocity(t *testing.T) {
	f, c, _ := networkLegacyFixture(t)
	original := f.backend.command
	stoppedLegacy, reloadAfterRemoval, startedVelocity := false, false, false
	f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
		if cmd == "systemctl" {
			if len(args) > 0 && args[0] == "show" && args[len(args)-1] == "justvoxel-picolobby.service" {
				return []byte("LoadState=loaded\nActiveState=active\nSubState=running\n"), nil
			}
			if len(args) > 0 && args[0] == "stop" && args[len(args)-1] == "justvoxel-picolobby.service" {
				stoppedLegacy = true
			}
			if len(args) > 0 && args[0] == "daemon-reload" {
				_, err := os.Stat(filepath.Join(f.backend.quadlets, "justvoxel-picolobby.container"))
				reloadAfterRemoval = errors.Is(err, os.ErrNotExist)
			}
			if len(args) > 1 && args[0] == "--no-block" && args[1] == "start" {
				if !reflect.DeepEqual(args, []string{"--no-block", "start", "--", "justvoxel-velocity.service"}) {
					t.Fatal("legacy infrastructure restarted", args)
				}
				if !stoppedLegacy || !reloadAfterRemoval {
					t.Fatal("started before legacy cleanup")
				}
				startedVelocity = true
			}
		}
		return original(ctx, cmd, args...)
	}
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	if !stoppedLegacy || !reloadAfterRemoval || !startedVelocity {
		t.Fatal("migration lifecycle incomplete")
	}
	for _, path := range []string{filepath.Join(f.backend.quadlets, "justvoxel-picolobby.container"), filepath.Join(f.backend.networkDirectory(), "legacy-rollback.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("legacy state retained", path, err)
		}
	}
	generated, err := f.backend.readPrivate(filepath.Join(f.backend.networkDirectory(), "generated.json"))
	if err != nil || bytes.Contains(generated, []byte("picolobby")) {
		t.Fatal("legacy generated configuration retained", err)
	}
}

func TestMinecraftNetworkLegacyRollbackAndRetryRecovery(t *testing.T) {
	for _, reloadFailures := range []int{1, 2} {
		t.Run(fmt.Sprint(reloadFailures), func(t *testing.T) {
			f, c, legacy := networkLegacyFixture(t)
			f.failReload = reloadFailures
			if err := networkApply(context.Background(), f.backend, c); err == nil {
				t.Fatal("failed migration claimed success")
			}
			if reloadFailures == 2 {
				if networkPending(f.backend) == nil {
					t.Fatal("lost interrupted legacy recovery")
				}
				if err := networkRecover(context.Background(), f.backend); err != nil {
					t.Fatal("legacy recovery stranded", err)
				}
			}
			if networkPending(f.backend) != nil {
				t.Fatal("legacy rollback did not release guard")
			}
			quadlet, err := f.backend.readPrivate(filepath.Join(f.backend.quadlets, "justvoxel-picolobby.container"))
			expected := strings.ReplaceAll(string(legacy), " start-picolobby", " check")
			if err != nil || string(quadlet) != expected {
				t.Fatal("legacy unit not safely restored", err)
			}
			marker, err := f.backend.readPrivate(filepath.Join(f.backend.networkDirectory(), "legacy-rollback.json"))
			var evidence struct {
				Schema int    `json:"schema"`
				Hash   string `json:"generated_sha256"`
			}
			generated, readErr := f.backend.readPrivate(filepath.Join(f.backend.networkDirectory(), "generated.json"))
			if err != nil || readErr != nil || json.Unmarshal(marker, &evidence) != nil || evidence.Schema != 1 || evidence.Hash != instanceHash(generated) {
				t.Fatal("missing hash-bound legacy rollback configuration", err, readErr)
			}
			if err := networkApply(context.Background(), f.backend, c); err != nil {
				t.Fatal("cannot retry migration after rollback", err)
			}
		})
	}
}

func TestMinecraftNetworkWhitelistEnforcementCompatibility(t *testing.T) {
	if !networkDefaultState().Config.whitelistEnforced() {
		t.Fatal("default whitelist must remain enforced")
	}
	for _, setting := range []string{"legacy", "true", "false"} {
		t.Run(setting, func(t *testing.T) {
			f, c := networkTransactionFixture(t)
			if err := networkApply(context.Background(), f.backend, c); err != nil {
				t.Fatal(err)
			}
			n, err := networkRead(f.backend)
			if err != nil {
				t.Fatal(err)
			}
			n.Whitelist = []minecraftNetworkPlayer{{UUID: "11111111-1111-1111-1111-111111111111", Name: "Alex"}}
			want := setting != "false"
			n.Config.WhitelistEnforced = nil
			if setting != "legacy" {
				n.Config.WhitelistEnforced = &want
			}
			if err := networkWriteState(f.backend, n); err != nil {
				t.Fatal(err)
			}
			data, err := f.backend.readPrivate(filepath.Join(f.backend.networkDirectory(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if setting == "legacy" && bytes.Contains(data, []byte("whitelist_enforced")) {
				t.Fatal("legacy fixture must omit enforcement")
			}
			n, err = networkRead(f.backend)
			if err != nil || n.Config.whitelistEnforced() != want || len(n.Whitelist) != 1 {
				t.Fatal("state lost enforcement or saved players", err, n)
			}
			if setting == "false" && (n.Config.WhitelistEnforced == nil || *n.Config.WhitelistEnforced) {
				t.Fatal("explicit false was lost")
			}
			files := networkInfrastructure(n, nil)
			if got := string(files["velocity/plugins/global-whitelist/config.properties"]); got != fmt.Sprintf("white-list=%t\nenforce-whitelist=%t\n", want, want) {
				t.Fatal("incorrect generated enforcement", got)
			}
			var players []minecraftNetworkPlayer
			if json.Unmarshal(files["velocity/plugins/global-whitelist/whitelist.json"], &players) != nil || !reflect.DeepEqual(players, n.Whitelist) {
				t.Fatal("generated whitelist lost players")
			}
			old := instanceRuntimes
			instanceRuntimes = f.backend
			t.Cleanup(func() { instanceRuntimes = old })
			rr := httptest.NewRecorder()
			roleServerForTest(roleAdministrator).minecraftNetworkStatus(rr, authorizedRequest("GET", "/v1/minecraft/network", ""))
			var status struct {
				Config struct {
					WhitelistEnforced *bool `json:"whitelist_enforced"`
				} `json:"config"`
				WhitelistEnforced *bool `json:"whitelist_enforced"`
			}
			if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &status) != nil || status.Config.WhitelistEnforced == nil || status.WhitelistEnforced == nil || *status.Config.WhitelistEnforced != want || *status.WhitelistEnforced != want {
				t.Fatal("status must expose matching explicit booleans", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestMinecraftNetworkConfigurePersistsEnforcementWithoutDeletingPlayers(t *testing.T) {
	f, c := networkTransactionFixture(t)
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	n, err := networkRead(f.backend)
	if err != nil {
		t.Fatal(err)
	}
	n.Whitelist = []minecraftNetworkPlayer{{UUID: "11111111-1111-1111-1111-111111111111", Name: "Alex"}}
	if err := networkWriteState(f.backend, n); err != nil {
		t.Fatal(err)
	}
	s, _ := networkLockFixture(t, f)
	for _, setting := range []string{"false", "true", "legacy"} {
		c.WhitelistEnforced = nil
		want := setting != "false"
		if setting != "legacy" {
			c.WhitelistEnforced = &want
		}
		body, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		s.minecraftNetworkConfigure(rr, authorizedRequest("PUT", "/v1/minecraft/network", string(body)))
		if rr.Code != 200 {
			t.Fatal(rr.Code, rr.Body.String())
		}
		after, err := networkRead(f.backend)
		if err != nil || after.Config.WhitelistEnforced == nil || after.Config.whitelistEnforced() != want || !reflect.DeepEqual(after.Whitelist, n.Whitelist) {
			t.Fatal("configure lost setting or players", err, after)
		}
		generated, err := f.backend.readPrivate(filepath.Join(f.backend.networkDirectory(), "generated.json"))
		var files map[string][]byte
		if err != nil || json.Unmarshal(generated, &files) != nil || string(files["velocity/plugins/global-whitelist/config.properties"]) != fmt.Sprintf("white-list=%t\nenforce-whitelist=%t\n", want, want) {
			t.Fatal("configure did not publish enforcement", err)
		}
	}
}
