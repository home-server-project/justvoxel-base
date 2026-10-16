package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func instanceRuntimeFixture(t *testing.T) (*minecraftInstanceStore, minecraftInstanceRuntimeContext, []minecraftInstanceRuntimeConfig) {
	t.Helper()
	store := testMinecraftInstances(t)
	_, err := store.register("legacy1", true)
	if err != nil {
		t.Fatal(err)
	}
	registerLogicalForTest(t, store, "a1b2c3", "paper", "JV Paper")
	registerLogicalForTest(t, store, "d4e5f6", "vanilla", "JV Vanilla")
	root := filepath.Join(t.TempDir(), "storage")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	templates, err := filepath.Abs("../../../templates")
	if err != nil {
		t.Fatal(err)
	}
	ctx := minecraftInstanceRuntimeContext{
		BackupPath:  filepath.Join(root, "backups"),
		StorageRoot: root, TotalMemoryMiB: 8192, TemplateRoot: templates,
	}
	a := minecraftInstanceRuntimeConfig{ID: "jv-a1b2c3", JavaPort: 25566, JavaMemory: "1G", ContainerMemory: "2G", Software: "paper", Version: "1.21.4", PaperChannel: "default", ImageTag: "stable", StartOnBoot: true, EULAAccepted: true, UID: 1000, GID: 1000, Timezone: "UTC", GameMode: "survival", Difficulty: "normal", MaxPlayers: 20, Whitelist: true, EnforceWhitelist: true, MOTD: `Family "Survival" \ world`, RCONPassword: strings.Repeat("a", 48)}
	b := a
	b.ID = "jv-d4e5f6"
	b.JavaPort = 25567
	b.Software = "vanilla"
	b.StartOnBoot = false
	b.Version = "LATEST"
	b.ImageTag = "release_1.2-3"
	b.RCONPassword = strings.Repeat("b", 48)
	return store, ctx, []minecraftInstanceRuntimeConfig{a, b}
}

func TestInstanceRuntimeDistinctAndDeterministic(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	before := registrySnapshot(t, store)
	files, err := store.renderInstanceRuntimes(ctx, configs)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatal("expected two runtimes")
	}
	for i, f := range files {
		c := configs[i]
		if f.ID != c.ID || f.ContainerName != "minecraft-"+c.ID || f.UnitName != f.ContainerName+".service" || f.QuadletPath != "/etc/containers/systemd/"+f.ContainerName+".container" || f.EnvironmentPath != "/etc/justvoxel/instances/"+c.ID+"/minecraft.env" || f.DataPath != filepath.Join(ctx.StorageRoot, "minecraft-instances", c.ID, "data") {
			t.Fatalf("wrong layout for %s", c.ID)
		}
		for _, want := range []string{"Volume=" + f.DataPath + ":/data:Z", "RequiresMountsFor=" + f.DataPath, "ContainerName=" + f.ContainerName, "EnvironmentFile=" + f.EnvironmentPath, "StopTimeout=180", "TimeoutStartSec=900", "TimeoutStopSec=240", "Restart=always", "RestartSec=10", "LogDriver=journald", "Image=docker.io/itzg/minecraft-server:" + c.ImageTag, "Memory=2G", "PublishPort=" + map[int]string{25566: "25566", 25567: "25567"}[c.JavaPort] + ":25565/tcp", "validate-instance-data-mount " + c.ID} {
			if !strings.Contains(string(f.Quadlet), want) {
				t.Fatalf("%s missing %s", c.ID, want)
			}
		}
		for _, forbidden := range []string{"managed-crossplay-start", "/validate-data-mount ", "systemctl", "19132/udp", "JV Paper", "JV Vanilla", c.RCONPassword} {
			if strings.Contains(string(f.Quadlet), forbidden) {
				t.Fatalf("unsafe Quadlet content: %s", forbidden)
			}
		}
		for _, want := range []string{"TYPE=" + strings.ToUpper(c.Software), "VERSION=" + c.Version, "MEMORY=1G", "UID=1000", "GID=1000", "RCON_PASSWORD=" + c.RCONPassword, "STOP_SERVER_ANNOUNCE_DELAY=60", `MOTD="Family \"Survival\" \\ world"`} {
			if !strings.Contains(string(f.Environment), want) {
				t.Fatalf("%s missing environment setting %s", c.ID, want)
			}
		}
		if _, err := os.Lstat(f.DataPath); !os.IsNotExist(err) {
			t.Fatal("render created world directory")
		}
	}
	if files[0].EnvironmentPath == files[1].EnvironmentPath || files[0].DataPath == files[1].DataPath || files[0].ContainerName == files[1].ContainerName {
		t.Fatal("shared instance runtime")
	}
	if !strings.Contains(string(files[0].Environment), "MODRINTH_PROJECTS=viaversion") || strings.Contains(string(files[1].Environment), "MODRINTH_PROJECTS=") || strings.Contains(string(files[1].Environment), "PAPER_CHANNEL=") {
		t.Fatal("software capabilities changed")
	}
	reversed, err := store.renderInstanceRuntimes(ctx, []minecraftInstanceRuntimeConfig{configs[1], configs[0]})
	if err != nil || !reflect.DeepEqual(files, reversed) {
		t.Fatal("output is not deterministic")
	}
	if !reflect.DeepEqual(before, registrySnapshot(t, store)) {
		t.Fatal("preview modified registry")
	}
	if _, err := store.renameInstance(configs[0].ID, "Family Survival", "paper"); err != nil {
		t.Fatal(err)
	}
	renamed, err := store.renderInstanceRuntimes(ctx, configs)
	if err != nil || !reflect.DeepEqual(files, renamed) {
		t.Fatal("pretty name changed runtime")
	}
}

func TestInstanceRuntimeStartOnBoot(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	for _, enabled := range []bool{false, true} {
		configs[0].StartOnBoot = enabled
		files, err := store.renderInstanceRuntimes(ctx, configs)
		if err != nil {
			t.Fatal(err)
		}
		quadlet := string(files[0].Quadlet)
		if strings.Contains(quadlet, "[Install]\nWantedBy=multi-user.target") != enabled {
			t.Fatal("incorrect boot dependency")
		}
		if !enabled && (strings.Contains(quadlet, "WantedBy=") || strings.Contains(quadlet, "[Install]")) {
			t.Fatal("disabled autostart still wanted")
		}
		// OFF changes only boot integration. The complete container definition and
		// generated service name remain available; no current-running-state input.
		if !strings.Contains(quadlet, "[Container]") || files[0].UnitName != "minecraft-jv-a1b2c3.service" {
			t.Fatal("off removed manually startable runtime")
		}
	}
}

func TestInstanceRuntimeValidation(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(*minecraftInstanceRuntimeContext, []minecraftInstanceRuntimeConfig)
	}{
		{"missing ID", "Invalid Instance ID", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].ID = "" }},
		{"unsafe ID", "Invalid Instance ID", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].ID = "jv-../../etc" }},
		{"unregistered ID", "already be registered", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].ID = "jv-abcdef" }},
		{"non-instance identity", "already be registered", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].ID = "jv-legacy1" }},
		{"duplicate ID", "duplicate", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[1].ID = c[0].ID }},
		{"Java collision", "conflicting", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[1].JavaPort = c[0].JavaPort
			c[1].StartOnBoot = false
		}},
		{"UDP collision", "conflicting", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].BedrockEnabled = true
			c[1].BedrockEnabled = true
			c[0].BedrockPort = 19133
			c[1].BedrockPort = 19133
			c[1].Software = "purpur"
		}},
		{"low TCP", "port must", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].JavaPort = 0 }},
		{"high TCP", "port must", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].JavaPort = 65536 }},
		{"invalid UDP", "port must", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].BedrockEnabled = true
			c[0].BedrockPort = -1
		}},
		{"disabled UDP", "disabled Bedrock", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].BedrockPort = 19133 }},
		{"unsupported crossplay", "unsupported", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].Software = "vanilla"
			c[0].BedrockEnabled = true
			c[0].BedrockPort = 19133
		}},
		{"malformed heap", "memory must", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].JavaMemory = "1G\nEULA=TRUE"
		}},
		{"tiny heap", "between 512", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].JavaMemory = "511M" }},
		{"unbounded memory", "memory must", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].ContainerMemory = "99999999999999G"
		}},
		{"no headroom", "512 MiB", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].ContainerMemory = "1G"
		}},
		{"overcommit including stopped", "overcommits", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) { x.TotalMemoryMiB = 4096 }},
		{"missing RAM", "physical memory", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) { x.TotalMemoryMiB = 0 }},
		{"path traversal", "unsafe", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) {
			x.StorageRoot += "/../world"
		}},
		{"backup overlap", "overlaps", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) {
			x.BackupPath = filepath.Join(x.StorageRoot, "minecraft-instances")
		}},
		{"wrong mount", "outside", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) {
			x.MountPoint = filepath.Join(x.StorageRoot, "other")
			x.ExpectedUUID = "uuid"
		}},
		{"mount without evidence", "identity evidence", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) {
			x.MountPoint = x.StorageRoot
		}},
		{"evidence without mount", "mount point", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) { x.ExpectedUUID = "uuid" }},
		{"mount injection", "invalid mount UUID", func(x *minecraftInstanceRuntimeContext, _ []minecraftInstanceRuntimeConfig) {
			x.MountPoint = x.StorageRoot
			x.ExpectedUUID = "uuid%u"
		}},
		{"bad software", "software", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].Software = "forge" }},
		{"bad version", "version", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].Version = "1.21\nTYPE=VANILLA"
		}},
		{"image digest", "image tag", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].ImageTag = "sha256:deadbeef"
		}},
		{"latest Paper channel", "latest Paper", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].Version = "LATEST" }},
		{"EULA required", "EULA", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].EULAAccepted = false
		}},
		{"root user", "non-root", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) { c[0].UID = 0 }},
		{"startup injection", "startup", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].MOTD = "hello\nRCON_PASSWORD=leak"
		}},
		{"unknown timezone", "timezone", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].Timezone = "Unknown/Timezone"
		}},
		{"invalid password", "startup", func(_ *minecraftInstanceRuntimeContext, c []minecraftInstanceRuntimeConfig) {
			c[0].RCONPassword = "short"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ctx, configs := instanceRuntimeFixture(t)
			before := registrySnapshot(t, store)
			tc.mutate(&ctx, configs)
			output, err := store.renderInstanceRuntimes(ctx, configs)
			if err == nil || !strings.Contains(err.Error(), tc.reason) || output != nil {
				t.Fatalf("want %s rejection without output, got %v", tc.reason, err)
			}
			if !reflect.DeepEqual(before, registrySnapshot(t, store)) {
				t.Fatal("failed rendering mutated registry")
			}
		})
	}
}

func TestInstanceRuntimeProtocolsAndMountEvidence(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	// Equal numeric ports on different protocols are not a collision.
	configs[0].JavaPort = 19133
	configs[1].Software = "purpur"
	configs[1].BedrockEnabled = true
	configs[1].BedrockPort = 19133
	ctx.MountPoint = ctx.StorageRoot
	ctx.ExpectedUUID = "1234-abcd"
	ctx.ExpectedSource = "/dev/mapper/data"
	files, err := store.renderInstanceRuntimes(ctx, configs)
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{`"` + ctx.MountPoint + `"`, `"1234-abcd"`, `"/dev/mapper/data"`} {
		if !strings.Contains(string(files[0].Quadlet), literal) {
			t.Fatal("mount evidence omitted")
		}
	}
	configs[0].JavaMemory = "1024m"
	configs[0].ContainerMemory = "1536m"
	if _, err := store.renderInstanceRuntimes(ctx, configs); err != nil {
		t.Fatal("valid MiB headroom rejected", err)
	}
}

func TestInstanceRuntimeSymlinksAndNonDirectories(t *testing.T) {
	for _, kind := range []string{"parent symlink", "dangling symlink", "file", "record symlink"} {
		t.Run(kind, func(t *testing.T) {
			store, ctx, configs := instanceRuntimeFixture(t)
			path := filepath.Join(ctx.StorageRoot, "minecraft-instances")
			switch kind {
			case "parent symlink":
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			case "dangling symlink":
				if err := os.Symlink(filepath.Join(ctx.StorageRoot, "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			case "record symlink":
				path = filepath.Join(store.directory, configs[0].ID+".json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(store.directory, "minecraft.json"), path); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := store.renderInstanceRuntimes(ctx, configs); err == nil || result != nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
}

func TestInstanceRuntimeStrictConfiguration(t *testing.T) {
	_, _, configs := instanceRuntimeFixture(t)
	data, err := json.Marshal(configs)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodeMinecraftInstanceRuntime(bytes.NewReader(data)); err != nil || !reflect.DeepEqual(got, configs) {
		t.Fatal("valid config rejected", err)
	}
	for _, data := range []string{`null`, `[]`, `{}`, `[{"id":"jv-a1b2c3","pretty_name":"Family"}]`, `[{"java_port":"25566"}]`, `[] []`, `[`, strings.Repeat(" ", 1024*1024+1)} {
		if _, err := decodeMinecraftInstanceRuntime(strings.NewReader(data)); err == nil {
			t.Fatal("malformed config accepted")
		}
	}
}

func TestInstanceRuntimeAtomicInactiveStaging(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	before := registrySnapshot(t, store)
	if err := store.stageInstanceRuntimes(ctx, configs, parent, "review"); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(parent, "review")
	info, err := os.Stat(bundle)
	if err != nil || !store.private(info, 0700) {
		t.Fatal("bundle not private")
	}
	entries, err := os.ReadDir(bundle)
	if err != nil || len(entries) != 4 {
		t.Fatal("incomplete bundle")
	}
	snapshot := map[string]string{}
	for _, entry := range entries {
		path := filepath.Join(bundle, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !store.private(info, 0600) {
			t.Fatal("runtime configuration not private")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		snapshot[entry.Name()] = string(data)
	}
	configs[0].MOTD = "changed"
	if err := store.stageInstanceRuntimes(ctx, configs, parent, "review"); err == nil {
		t.Fatal("replaced previous working bundle")
	}
	configs[1].JavaPort = configs[0].JavaPort
	if err := store.stageInstanceRuntimes(ctx, configs, parent, "failed"); err == nil {
		t.Fatal("invalid rendering published")
	}
	for name, want := range snapshot {
		data, err := os.ReadFile(filepath.Join(bundle, name))
		if err != nil || string(data) != want {
			t.Fatal("failure changed previous bundle")
		}
	}
	entries, err = os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "review" {
		t.Fatal("failure left partial output")
	}
	if !reflect.DeepEqual(before, registrySnapshot(t, store)) {
		t.Fatal("staging mutated identities")
	}
	for _, path := range []string{filepath.Join(ctx.StorageRoot, "minecraft-instances"), filepath.Join(ctx.StorageRoot, "minecraft"), ctx.BackupPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("staging created data or backup directories")
		}
	}
}

func TestInstanceRuntimeTemplateFailureNoPartialOutput(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	templates := t.TempDir()
	for _, dir := range []string{"config", "quadlets"} {
		if err := os.Mkdir(filepath.Join(templates, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"config/minecraft.env.in", "quadlets/minecraft-instance.container.in"} {
		data, err := os.ReadFile(filepath.Join(ctx.TemplateRoot, file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(file, "quadlets/") {
			data = append(data, []byte("@@UNKNOWN_TOKEN@@\n")...)
		}
		if err := os.WriteFile(filepath.Join(templates, file), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx.TemplateRoot = templates
	if err := store.stageInstanceRuntimes(ctx, configs, parent, "failed"); err == nil {
		t.Fatal("bad template accepted")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("template failure left partial output")
	}
}

func TestInstanceRuntimeLegacyCompatibility(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	legacyTemplate, err := os.ReadFile(filepath.Join(ctx.TemplateRoot, "quadlets", "minecraft.container.in"))
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"ContainerName=minecraft", "EnvironmentFile=/etc/justvoxel/minecraft.env", "ExecStartPre=/usr/libexec/justvoxel/mjust/validate-data-mount", "Volume=@@DATA_PATH@@:/data:Z", "[Install]\nWantedBy=multi-user.target"} {
		if !bytes.Contains(legacyTemplate, []byte(literal)) {
			t.Fatal("legacy contract changed")
		}
	}
	for _, path := range []string{filepath.Join(ctx.StorageRoot, "minecraft"), ctx.BackupPath} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "existing.dat"), []byte("existing protected data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	legacy := filepath.Join(t.TempDir(), "legacy")
	if err := os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minecraft.container", "minecraft.env", "justvoxel.conf"} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte("legacy sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := registrySnapshot(t, store)
	if _, err := store.renderInstanceRuntimes(ctx, configs); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, registrySnapshot(t, store)) {
		t.Fatal("legacy identity changed")
	}
	for _, path := range []string{filepath.Join(ctx.StorageRoot, "minecraft"), ctx.BackupPath} {
		data, err := os.ReadFile(filepath.Join(path, "existing.dat"))
		if err != nil || string(data) != "existing protected data" {
			t.Fatal("render modified existing world or backup")
		}
	}
	for _, name := range []string{"minecraft.container", "minecraft.env", "justvoxel.conf"} {
		data, err := os.ReadFile(filepath.Join(legacy, name))
		if err != nil || string(data) != "legacy sentinel" {
			t.Fatal("legacy runtime mutated")
		}
	}
}

// Optional generator-only verification, never invokes Podman containers or
// systemctl. Enable explicitly when test execution and this tool are approved.
func TestInstanceRuntimeQuadletGenerator(t *testing.T) {
	generator := os.Getenv("JUSTVOXEL_QUADLET_GENERATOR")
	if generator == "" {
		t.Skip("set JUSTVOXEL_QUADLET_GENERATOR to an approved installed Quadlet generator")
	}
	store, ctx, configs := instanceRuntimeFixture(t)
	files, err := store.renderInstanceRuntimes(ctx, configs)
	if err != nil {
		t.Fatal(err)
	}
	units := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(units, filepath.Base(f.QuadletPath)), f.Quadlet, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(generator, "--dryrun")
	// Override rather than append a potentially pre-existing directory selection.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "QUADLET_UNIT_DIRS=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "QUADLET_UNIT_DIRS="+units)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generator rejected Quadlets: %v: %s", err, output)
	}
	for _, f := range files {
		if !bytes.Contains(output, []byte(f.UnitName)) {
			t.Fatalf("missing generated unit %s", f.UnitName)
		}
	}
}

func TestInstanceRuntimeStagingRejectsUnsafeDestination(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../outside", "minecraft.container", "name/child"} {
		if err := store.stageInstanceRuntimes(ctx, configs, parent, name); err == nil {
			t.Fatal("unsafe bundle name accepted")
		}
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := store.stageInstanceRuntimes(ctx, configs, link, "review"); err == nil {
		t.Fatal("staging parent symlink accepted")
	}
	// A root-owned private Quadlet search directory must still never be used as
	// staging, since generators recurse through their configured directories.
	search := filepath.Join(parent, "containers", "systemd")
	if err := os.MkdirAll(search, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.stageInstanceRuntimes(ctx, configs, search, "review"); err == nil {
		t.Fatal("published preview into Quadlet search path")
	}
	if err := store.stageInstanceRuntimes(ctx, configs, ctx.StorageRoot, "review"); err == nil {
		t.Fatal("staged inside Minecraft storage")
	}
	badMode := filepath.Join(parent, "public")
	if err := os.Mkdir(badMode, 0755); err != nil {
		t.Fatal(err)
	}
	if err := store.stageInstanceRuntimes(ctx, configs, badMode, "review"); err == nil {
		t.Fatal("public staging accepted")
	}
	entries, err := os.ReadDir(badMode)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsafe staging left output")
	}
}

func TestInstanceRuntimeFirstInstancePlanning(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	// The image supplies an empty private registry directory, not a server.
	if err := os.RemoveAll(store.directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.directory, 0700); err != nil {
		t.Fatal(err)
	}
	c := configs[0]
	c.ID, c.JavaPort, c.BedrockEnabled, c.BedrockPort = "jv-first123", 25565, true, 19132
	ctx.TotalMemoryMiB = 3072 // 2 GiB container + the existing 1 GiB appliance reserve.
	files, err := store.renderInstanceRuntimesCandidate(ctx, []minecraftInstanceRuntimeConfig{c}, c.ID)
	if err != nil || len(files) != 1 {
		t.Fatal("first-instance planning requires a fixed server", err)
	}
	if files[0].UnitName != "minecraft-"+c.ID+".service" || files[0].ContainerName != "minecraft-"+c.ID || files[0].DataPath != instanceStorageFor(ctx, c.ID).DataPath {
		t.Fatal("first server uses different identity/path rules")
	}
	if _, err := os.Lstat(filepath.Join(store.directory, "minecraft.json")); !os.IsNotExist(err) {
		t.Fatal("planning wrote fixed identity")
	}
	ctx.TotalMemoryMiB--
	if _, err := store.renderInstanceRuntimesCandidate(ctx, []minecraftInstanceRuntimeConfig{c}, c.ID); err == nil {
		t.Fatal("first instance bypassed appliance memory reservation")
	}
}

func TestInstanceRuntimeRejectsDuplicateRegistryIdentity(t *testing.T) {
	store, ctx, configs := instanceRuntimeFixture(t)
	if err := os.WriteFile(filepath.Join(store.directory, "jv-abcdef.json"), []byte(`{"id":"jv-a1b2c3"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.renderInstanceRuntimes(ctx, configs); err == nil {
		t.Fatal("duplicate/mismatched registry identity accepted")
	}
}
