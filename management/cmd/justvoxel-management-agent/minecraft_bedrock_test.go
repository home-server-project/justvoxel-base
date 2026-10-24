package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func nativeBedrockFixture(c minecraftInstanceRuntimeConfig) minecraftInstanceRuntimeConfig {
	c.Software = "bedrock"
	c.JavaPort = 0
	c.JavaMemory = ""
	c.BedrockEnabled = false
	c.BedrockPort = 0
	c.BedrockTCPPort = 19132
	c.BedrockUDPStart = 19140
	c.BedrockUDPEnd = 19159
	c.Version = "1.26.50.1"
	c.VersionPolicy = "pinned"
	c.ImageTag = "stable"
	c.LoaderVersion = ""
	c.Timezone = ""
	c.GameMode = "survival"
	c.Whitelist = false
	c.EnforceWhitelist = false
	return c
}
func TestNativeBedrockRuntimeAndApplianceTimezone(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	configs[0] = nativeBedrockFixture(configs[0])
	files, err := store.renderInstanceRuntimes(ctx, configs)
	if err != nil {
		t.Fatal(err)
	}
	env, q := string(files[0].Environment), string(files[0].Quadlet)
	for _, value := range []string{"EULA=TRUE", "VERSION=1.26.50.1", "TRANSPORT=nethernet", "SERVER_PORT=19132", "SERVER_UDP_PORTS=19140-19159", "SERVER_NAME=", "MAX_PLAYERS=20", "GAMEMODE=survival", "ALLOW_LIST=FALSE", "ENABLE_LAN_VISIBILITY=FALSE"} {
		if !strings.Contains(env, value) {
			t.Fatal("missing native environment", value)
		}
	}
	for _, value := range []string{"Image=docker.io/itzg/minecraft-bedrock-server:stable", "Network=host", "Volume=" + files[0].DataPath + ":/data:Z", "Volume=/etc/localtime:/etc/localtime:ro", "validate-instance-start-memory"} {
		if !strings.Contains(q, value) {
			t.Fatal("missing native runtime", value)
		}
	}
	for _, value := range []string{"TZ=", "TYPE=", "RCON_PASSWORD=", "MODRINTH", "FABRIC", "FORGE", "MEMORY=", "LATEST", "geyser", "floodgate", "viaversion"} {
		if strings.Contains(env, value) {
			t.Fatal("Java or independent timezone in Bedrock", value)
		}
	}
	if strings.Contains(q, "PublishPort=") || strings.Contains(q, "instance-crossplay-start") {
		t.Fatal("native networking uses Java/Geyser setup")
	}
	// A deployed Java record's old timezone is accepted for decoding but ignored.
	configs[1].Timezone = "Unknown/DeployedSetting"
	files, err = store.renderInstanceRuntimes(ctx, configs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files[1].Environment), "TZ=") || !strings.Contains(string(files[1].Quadlet), "/etc/localtime:/etc/localtime:ro") {
		t.Fatal("Java timezone did not inherit appliance")
	}
	raw, _ := json.Marshal(configs[0])
	if strings.Contains(string(raw), `"timezone"`) {
		t.Fatal("independent timezone persisted")
	}
}
func TestNativeBedrockRejectsInvalidRuntimeAndConflicts(t *testing.T) {
	for _, change := range []func(*minecraftInstanceRuntimeConfig){
		func(c *minecraftInstanceRuntimeConfig) { c.GameMode = "spectator" },
		func(c *minecraftInstanceRuntimeConfig) { c.Version = "LATEST" },
		func(c *minecraftInstanceRuntimeConfig) { c.Version = "1.26.50-preview" },
		func(c *minecraftInstanceRuntimeConfig) { c.JavaPort = 25565 },
		func(c *minecraftInstanceRuntimeConfig) { c.LoaderVersion = "0.18.0" },
		func(c *minecraftInstanceRuntimeConfig) { c.ImageTag = "latest" },
		func(c *minecraftInstanceRuntimeConfig) { c.BedrockEnabled = true },
		func(c *minecraftInstanceRuntimeConfig) { c.BedrockUDPEnd = c.BedrockUDPStart + 18 },
		func(c *minecraftInstanceRuntimeConfig) { c.BedrockTCPPort = c.BedrockUDPStart },
		func(c *minecraftInstanceRuntimeConfig) { c.JavaMemory = "1G" },
	} {
		store, ctx, configs := instanceRuntimeFixture(t)
		configs[0] = nativeBedrockFixture(configs[0])
		change(&configs[0])
		if _, err := store.renderInstanceRuntimes(ctx, configs); err == nil {
			t.Fatal("invalid native runtime accepted")
		}
	}
	for _, port := range []int{19132, 19140, 19159} {
		store, ctx, configs := instanceRuntimeFixture(t)
		configs[0] = nativeBedrockFixture(configs[0])
		configs[1].JavaPort = port
		if _, err := store.renderInstanceRuntimes(ctx, configs); err == nil {
			t.Fatal("Java overlap accepted", port)
		}
		configs[1].JavaPort = 25567
		configs[1].Software = "paper"
		configs[1].BedrockEnabled = true
		configs[1].BedrockPort = port
		if _, err := store.renderInstanceRuntimes(ctx, configs); err == nil {
			t.Fatal("Geyser overlap accepted", port)
		}
	}
}
func TestNativeBedrockAllocationReservesWholeRange(t *testing.T) {
	records := []instanceRuntimeRecord{{Runtime: minecraftInstanceRuntimeConfig{JavaPort: 19132, BedrockEnabled: true, BedrockPort: 19142}}, {Runtime: nativeBedrockFixture(minecraftInstanceRuntimeConfig{MaxPlayers: 20})}}
	tcp, start, end, err := allocateNativeBedrock(records, 10, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tcp == 19132 || start <= 19159 || end-start+1 < 10 {
		t.Fatal("overlap or incomplete range", tcp, start, end)
	}
	for _, ports := range [][3]int{{19132, 20000, 20009}, {20000, 19140, 19149}, {20000, 20001, 20002}} {
		if _, _, _, err := allocateNativeBedrock(records, 10, ports[0], ports[1], ports[2]); err == nil {
			t.Fatal("conflicting native reservation accepted", ports)
		}
	}
}
func TestNativeBedrockFirewallOwnershipAndRollback(t *testing.T) {
	f := newInstanceActivationFixture(t)
	c := nativeBedrockFixture(f.configs[0])
	c.MaxPlayers = 2
	c.BedrockUDPEnd = c.BedrockUDPStart + 1
	old, next := instanceRuntimeRecord{}, instanceRuntimeRecord{Runtime: c}
	preexisting := instanceFirewallRule{Zone: "public", Port: c.BedrockUDPStart, Protocol: "udp", Permanent: true}
	f.firewall[preexisting] = true
	changes, owned, err := f.backend.planFirewall(context.Background(), old, next)
	if err != nil {
		t.Fatal(err)
	}
	if instanceRuleContains(owned, preexisting) {
		t.Fatal("unrelated firewall rule adopted")
	}
	for _, change := range changes {
		if err := f.backend.changeFirewall(context.Background(), change); err != nil {
			t.Fatal(err)
		}
	}
	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		change.Add = !change.Add
		if err := f.backend.changeFirewall(context.Background(), change); err != nil {
			t.Fatal(err)
		}
	}
	if !f.firewall[preexisting] {
		t.Fatal("rollback removed unrelated rule")
	}
	for _, rule := range nativeBedrockRules(c, "public", true) {
		if !instanceRuleForRuntime(rule, c) {
			t.Fatal("native range ownership missing")
		}
	}
	c.BedrockUDPStart += 10
	c.BedrockUDPEnd += 10
	if instanceRuleForRuntime(preexisting, c) {
		t.Fatal("another instance's range adopted")
	}
}
func TestNativeBedrockLifecycleUsesSelectedInstanceAndGracefulStop(t *testing.T) {
	f := newInstanceActivationFixture(t)
	c := nativeBedrockFixture(f.configs[0])
	c.BedrockTCPPort = instanceTestPort(t)
	tcp, start, end, err := allocateNativeBedrock(nil, c.MaxPlayers, c.BedrockTCPPort, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.BedrockTCPPort, c.BedrockUDPStart, c.BedrockUDPEnd = tcp, start, end
	dir, _, _ := f.backend.paths(c.ID)
	record := instanceRuntimeRecord{SchemaVersion: 1, Runtime: c, Storage: instanceStorageFor(f.snapshot, c.ID), Firewall: []instanceFirewallRule{}}
	if err = f.backend.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
		t.Fatal(err)
	}
	f.configs[0] = c
	base := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "podman" && len(args) >= 4 && args[0] == "exec" && args[2] == "send-command" {
			return nil, nil
		}
		if command == "podman" && len(args) > 0 && args[0] == "logs" {
			return []byte("[INFO] There are 0/20 players online:"), nil
		}
		return base(ctx, command, args...)
	}
	f.activateBoth()
	oldBackend := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = oldBackend })
	for _, action := range []string{"start", "stop", "start", "restart", "stop"} {
		if err = f.backend.actionWithWarnings(context.Background(), c.ID, action, true); err != nil {
			t.Fatal(action, err)
		}
	}
	state, err := f.backend.observe(context.Background(), c.ID)
	if err != nil || state.State != "stopped" || !state.StartOnBoot {
		t.Fatal(state, err)
	}
	current, err := f.backend.readRecord(c.ID)
	if err != nil || current.Runtime.Version != c.Version {
		t.Fatal("identity or exact version changed", err)
	}
	// Changing edition or pin is refused even when a direct caller forges settings.
	next := current
	next.Runtime.Version = "1.26.51.1"
	records, _ := f.backend.records()
	if err = f.backend.apply(context.Background(), current, instanceReplaceRecord(records, next), f.snapshot); err == nil {
		t.Fatal("Bedrock pin changed outside supported setup")
	}
	if javaServerAllowsSwitch("bedrock", "paper") || javaServerAllowsSwitch("paper", "bedrock") {
		t.Fatal("edition conversion permitted")
	}
}

func TestNativeBedrockCreateRegistersExactPin(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	old := runAdminDiscoveryHelper
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return []byte(`{"configured":true,"minecraft":{"java_port":25565,"bedrock_enabled":false}}`), nil
	}
	t.Cleanup(func() { runAdminDiscoveryHelper = old })
	base := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == instanceRuntimeHelper && len(args) == 2 && args[0] == "bedrock-release" && args[1] == "1.26.50.1" {
			return nil, nil
		}
		return base(ctx, command, args...)
	}
	c := nativeBedrockFixture(f.configs[0])
	c.ID = ""
	c.BedrockTCPPort = instanceTestPort(t)
	tcp, start, end, err := allocateNativeBedrock(nil, c.MaxPlayers, c.BedrockTCPPort, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.BedrockTCPPort, c.BedrockUDPStart, c.BedrockUDPEnd = tcp, start, end
	id, err := f.backend.create(context.Background(), c, "Native family", f.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.backend.readRecord(id)
	if err != nil || record.Runtime.Software != "bedrock" || record.Runtime.Version != "1.26.50.1" || record.Runtime.ID != id {
		t.Fatal("native identity or pin lost", err)
	}
	if err = f.backend.verifyLive(record); err != nil {
		t.Fatal(err)
	}
	identities, err := f.backend.identities().listInstances("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range identities {
		if entry.ID == id {
			found = entry.DisplayName == "Native family"
		}
	}
	if !found {
		t.Fatal("native identity not registered")
	}
}

func TestNativeBedrockChecksLastUDPPortBeforeReservation(t *testing.T) {
	tcp, start, end, err := allocateNativeBedrock(nil, 4, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	occupied, err := instanceHoldUDP(end)
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, _, _, err = allocateNativeBedrock(nil, 4, tcp, start, end); err == nil {
		t.Fatal("occupied final UDP port was ignored")
	}
}
