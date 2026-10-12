package main

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type pluginInstallFixture struct {
	s                  *server
	data               string
	versions           map[string]pluginCatalogVersion
	slugs              map[string]string
	jars               map[string][]byte
	commands           []string
	active             bool
	players            int
	playerChecks       int
	playerUnavailable  bool
	joinAt             int
	failDownload       bool
	badDownload        bool
	failBackup         bool
	failVerify         bool
	failRollbackVerify bool
	failedPlugin       bool
	missingPlugin      bool
	mutateOnVerify     bool
	backup             bool
}

func newPluginInstallFixture(t *testing.T) *pluginInstallFixture {
	t.Helper()
	f := &pluginInstallFixture{data: t.TempDir(), versions: map[string]pluginCatalogVersion{}, slugs: map[string]string{}, jars: map[string][]byte{}, active: true}
	if err := os.Mkdir(filepath.Join(f.data, "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	writePluginStartup(t, f.data, pluginFixtureStartup)
	f.s = surfaceTestServer(t, roleAdministrator)
	store, err := openOperationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.s.operations = store
	oldCatalog, oldDownload, oldDiscovery, oldRuntime, oldPlayers, oldCommand, oldLock := pluginCatalogClient, pluginDownloadClient, runAdminDiscoveryHelper, runPluginRuntimeStatus, pluginInstallPlayers, pluginInstallCommand, pluginRuntimeLockPath
	t.Cleanup(func() {
		store.close()
		pluginCatalogClient = oldCatalog
		pluginDownloadClient = oldDownload
		runAdminDiscoveryHelper = oldDiscovery
		runPluginRuntimeStatus = oldRuntime
		pluginInstallPlayers = oldPlayers
		pluginInstallCommand = oldCommand
		pluginRuntimeLockPath = oldLock
	})
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return json.Marshal(map[string]any{"configured": true, "minecraft": map[string]string{"server_type": "paper", "data_path": f.data}})
	}
	runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
		return []byte(`{"minecraft":{"server_type":"paper","version":"1.21.8"}}`), nil
	}
	pluginInstallPlayers = func(context.Context) (int, []string, error) {
		f.playerChecks++
		if f.playerUnavailable {
			return 0, nil, errors.New("unavailable")
		}
		if f.joinAt > 0 && f.playerChecks >= f.joinAt {
			f.players = 1
		}
		names := []string{}
		if f.players > 0 {
			names = append(names, "Alex")
		}
		return f.players, names, nil
	}
	pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.modrinth.com" {
			t.Fatalf("unsafe metadata URL: %s", r.URL)
		}
		var payload any
		path := strings.TrimPrefix(r.URL.Path, "/v2/")
		if strings.HasPrefix(path, "version/") {
			v, ok := f.versions[strings.TrimPrefix(path, "version/")]
			if !ok {
				return nil, errors.New("missing release")
			}
			payload = v
		} else if strings.HasSuffix(path, "/version") {
			id := strings.TrimSuffix(strings.TrimPrefix(path, "project/"), "/version")
			versions := []pluginCatalogVersion{}
			for _, v := range f.versions {
				if v.ProjectID == id {
					versions = append(versions, v)
				}
			}
			payload = versions
		} else if strings.HasPrefix(path, "project/") {
			id := strings.TrimPrefix(path, "project/")
			loaders := []string{}
			for _, version := range f.versions {
				if version.ProjectID == id {
					loaders = append(loaders, version.Loaders...)
				}
			}
			payload = map[string]any{"id": id, "slug": f.slugs[id], "title": id, "description": "Publisher <script>description</script>", "loaders": loaders}
		} else {
			t.Fatalf("unexpected API request %s", path)
		}
		raw, err := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, err
	})}
	pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if f.failDownload {
			return nil, errors.New("download unavailable")
		}
		name := filepath.Base(r.URL.Path)
		raw := f.jars[name]
		if f.badDownload {
			raw = slices.Clone(raw)
			raw[0] ^= 1
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(raw)), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})}
	pluginInstallCommand = func(_ context.Context, command string, args ...string) ([]byte, error) {
		action := command + " " + strings.Join(args, " ")
		f.commands = append(f.commands, action)
		switch {
		case command == "systemctl" && args[0] == "is-active":
			if f.active {
				return []byte("active\n"), nil
			}
			return []byte("inactive\n"), errors.New("inactive")
		case command == "systemctl" && args[0] == "start":
			f.active = true
			return nil, nil
		case command == "systemctl" && args[0] == "stop":
			f.active = false
			return nil, nil
		case command == "/usr/libexec/justvoxel/minecraft-backup":
			if args[0] != "--leave-stopped" {
				t.Fatal("backup contract")
			}
			entries, _ := os.ReadDir(filepath.Join(f.data, "plugins"))
			for _, entry := range entries {
				if entry.Name() == "root.jar" {
					t.Fatal("placement before cold backup")
				}
			}
			if f.failBackup {
				return []byte("Verifying new archive\n"), errors.New("backup failed")
			}
			f.backup = true
			f.active = false
			return nil, nil
		case command == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack":
			if f.mutateOnVerify {
				if err := os.WriteFile(filepath.Join(f.data, "plugins", "root.jar"), []byte("changed manually"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			entries, _ := os.ReadDir(filepath.Join(f.data, "plugins"))
			hasNew := false
			for _, entry := range entries {
				if entry.Name() == "root.jar" {
					hasNew = true
				}
			}
			if f.backup && hasNew {
				m, _, err := readPluginManifest(f.data)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range m.Artifacts {
					if entry.ProjectID == "root" {
						t.Fatal("manifest committed before runtime verification")
					}
				}
				raw := pluginFixtureStartup
				if f.missingPlugin {
					raw = "[PluginInitializerManager] Initializing plugins...\n[PluginInitializerManager] Bukkit plugins (0):\n"
				}
				if f.failedPlugin {
					raw += "[Root] Error occurred while enabling Root v1.0\n"
				}
				writePluginStartup(t, f.data, raw)
			}
			if (f.failVerify && hasNew) || (f.failRollbackVerify && !hasNew) {
				return nil, errors.New("verify failed")
			}
			return nil, nil
		case command == "podman" && args[len(args)-1] == "list":
			return []byte("There are 0 of a max of 20 players online:"), nil
		case command == "podman" && args[len(args)-1] == "plugins":
			t.Fatal("plugin identity must not use the RCON plugins command")
			return nil, errors.New("unexpected RCON plugins command")
		default:
			t.Fatalf("unexpected privileged command %s", action)
			return nil, errors.New("unexpected")
		}
	}
	f.addJar(t, "root", "release", "root.jar", "Root", "example.Root")
	return f
}
func (f *pluginInstallFixture) addJar(t *testing.T, project, version, filename, name, main string) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), filename)
	pluginTestJar(t, tmp, map[string]string{"plugin.yml": "name: " + name + "\nmain: " + main + "\nversion: 1.0\n"})
	raw, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(raw)
	file := pluginArtifactFile{URL: "https://cdn.modrinth.com/data/" + project + "/versions/" + version + "/" + filename, Filename: filename, Primary: true, Size: int64(len(raw))}
	file.Hashes.SHA512 = hex.EncodeToString(sum[:])
	f.versions[version] = pluginCatalogVersion{ID: version, ProjectID: project, Number: "1.0", Type: "release", Published: "2026-01-01T00:00:00Z", GameVersions: []string{"1.21.8"}, Loaders: []string{"paper"}, Files: []pluginArtifactFile{file}}
	f.jars[filename] = raw
}
func (f *pluginInstallFixture) plan(t *testing.T) pluginInstallPlan {
	t.Helper()
	plan, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Configured: true, ServerType: "paper", ServerSoftware: "Paper", MinecraftVersion: "1.21.8", Supported: true}, f.data, "root", "release")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func (f *pluginInstallFixture) execute(t *testing.T, plan pluginInstallPlan, confirm bool) operationJournal {
	t.Helper()
	journal, created, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
	if err != nil || !created {
		t.Fatalf("begin: %v", err)
	}
	_ = executePluginInstall(context.Background(), f.s, journal.OperationID, plan, confirm, session{})
	for _, command := range f.commands {
		if strings.HasSuffix(command, "rcon-cli plugins") {
			t.Fatal("RCON plugins used for identity")
		}
	}
	journal, err = f.s.operations.get(journal.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}
func (f *pluginInstallFixture) assertUntouched(t *testing.T, journal operationJournal) {
	t.Helper()
	assertPluginStaging(t, f, journal, false)
	if journal.State != operationRolledBack {
		t.Fatalf("state %s: %s", journal.State, journal.Status)
	}
	if len(f.commands) != 0 || f.backup {
		t.Fatalf("Minecraft touched: %v", f.commands)
	}
	if _, err := os.Stat(filepath.Join(f.data, "plugins", "root.jar")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("root JAR placed")
	}
	if _, err := os.Stat(filepath.Join(f.data, ".justvoxel")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("manifest state mutated")
	}
}

func TestPluginInstallAdministratorAndStrictRequests(t *testing.T) {
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		for _, route := range []string{"plan", "apply", "current"} {
			rr := httptest.NewRecorder()
			r := surfaceRequest(http.MethodPost, "/", `{}`)
			switch route {
			case "plan":
				s.adminPluginInstallPlan(rr, r)
			case "apply":
				s.adminPluginInstall(rr, r)
			case "current":
				s.adminPluginInstallCurrent(rr, r)
			}
			if rr.Code != 403 {
				t.Fatalf("%s %s: %d", role, route, rr.Code)
			}
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	for _, body := range []string{`{"project_id":"root","version_id":"release","url":"https://cdn.modrinth.com/data/x"}`, `{"project_id":"root","version_id":"release","filename":"x.jar"}`, `{"project_id":"root","version_id":"release","hash":"abc"}`, `{"project_id":"root","version_id":"release","target_path":"/tmp"}`, `{"project_id":"root","version_id":"release","dependencies":[]}`, `{"project_id":"root","version_id":"release"} {}`, `null`, `{"project_id":"../root","version_id":"release"}`} {
		for _, apply := range []bool{false, true} {
			rr := httptest.NewRecorder()
			r := surfaceRequest(http.MethodPost, "/", body)
			if apply {
				s.adminPluginInstall(rr, r)
			} else {
				s.adminPluginInstallPlan(rr, r)
			}
			if rr.Code != 400 {
				t.Fatalf("accepted %s: %d %s", body, rr.Code, rr.Body.String())
			}
		}
	}
}
func TestPluginInstallArtifactMetadataSafety(t *testing.T) {
	f := newPluginInstallFixture(t)
	valid := f.versions["release"].Files[0]
	cases := map[string][]pluginArtifactFile{"ambiguous": {valid, valid}, "missing": {}}
	noPrimary := valid
	noPrimary.Primary = false
	cases["multiple without primary"] = []pluginArtifactFile{noPrimary, noPrimary}
	for _, change := range []string{"hash", "zero size", "oversized", "traversal", "backslash", "non JAR primary", "http", "other CDN", "credentials", "redirect origin"} {
		v := valid
		switch change {
		case "hash":
			v.Hashes.SHA512 = ""
		case "zero size":
			v.Size = 0
		case "oversized":
			v.Size = pluginMaxJar + 1
		case "traversal":
			v.Filename = "../x.jar"
		case "backslash":
			v.Filename = `..\x.jar`
		case "non JAR primary":
			v.Filename = "readme.txt"
		case "http":
			v.URL = "http://cdn.modrinth.com/data/x.jar"
		case "other CDN":
			v.URL = "https://example.com/data/x.jar"
		case "credentials":
			v.URL = "https://user@cdn.modrinth.com/data/x.jar"
		case "redirect origin":
			v.URL = "https://cdn.modrinth.com.evil/data/x.jar"
		}
		cases[change] = []pluginArtifactFile{v}
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := selectPluginArtifact(files); err == nil {
				t.Fatal("unsafe metadata accepted")
			}
		})
	}
	if _, err := selectPluginArtifact([]pluginArtifactFile{noPrimary}); err != nil {
		t.Fatal("single nonprimary rejected", err)
	}
	other := noPrimary
	other.Filename = "other.jar"
	if got, err := selectPluginArtifact([]pluginArtifactFile{valid, other}); err != nil || got.Filename != valid.Filename {
		t.Fatal("unambiguous primary rejected")
	}
}
func TestPluginInstallExactReleaseCompatibility(t *testing.T) {
	for _, change := range []string{"project", "version", "beta", "alpha", "Minecraft", "loader"} {
		t.Run(change, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			v := f.versions["release"]
			switch change {
			case "project":
				v.ProjectID = "other"
			case "version":
				v.ID = "other"
			case "beta", "alpha":
				v.Type = change
			case "Minecraft":
				v.GameVersions = []string{"1.20"}
			case "loader":
				v.Loaders = []string{"fabric"}
			}
			f.versions["release"] = v
			_, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Supported: true, ServerType: "paper", MinecraftVersion: "1.21.8"}, f.data, "root", "release")
			if err == nil {
				t.Fatal("unsafe release accepted")
			}
		})
	}
}
func TestPluginInstallDependenciesAndFingerprint(t *testing.T) {
	f := newPluginInstallFixture(t)
	f.addJar(t, "dependency", "depRelease", "dependency.jar", "Dependency", "example.Dependency")
	v := f.versions["release"]
	v.Dependencies = []pluginDependency{{ProjectID: "dependency", Type: "required"}, {ProjectID: "displayOnly", Type: "optional"}, {ProjectID: "incompatible", Type: "incompatible"}, {Filename: "embedded.jar", Type: "embedded"}}
	f.versions["release"] = v
	plan := f.plan(t)
	if len(plan.Artifacts) != 2 || len(plan.DisplayDependencies) != 3 {
		t.Fatalf("dependencies: %#v", plan)
	}
	again := f.plan(t)
	if again.Fingerprint != plan.Fingerprint {
		t.Fatal("nondeterministic fingerprint")
	}
	reverse := plan
	reverse.Artifacts = slices.Clone(plan.Artifacts)
	slices.Reverse(reverse.Artifacts)
	reverse.DisplayDependencies = slices.Clone(plan.DisplayDependencies)
	slices.Reverse(reverse.DisplayDependencies)
	reverse.Online = 2
	reverse.Players = []string{"Alex"}
	if fingerprintPluginPlan(reverse) != plan.Fingerprint {
		t.Fatal("player state or collection order affected fingerprint")
	}
	mutated := plan
	mutated.DataPath += "changed"
	if fingerprintPluginPlan(mutated) == plan.Fingerprint {
		t.Fatal("path not fingerprinted")
	}
	encoded, _ := json.Marshal(plan)
	if bytes.Contains(encoded, []byte("cdn.modrinth.com")) || bytes.Contains(encoded, []byte(plan.Artifacts[0].SHA512)) {
		t.Fatal("private artifact metadata exposed")
	}
	journal := f.execute(t, plan, false)
	if journal.State != operationSucceeded {
		t.Fatalf("required installation failed: %s", journal.Status)
	}
}
func TestPluginInstallRequiredDependencyFailures(t *testing.T) {
	for _, kind := range []string{"filename only", "identity mismatch", "cycle", "depth", "count", "combined size"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			v := f.versions["release"]
			switch kind {
			case "filename only":
				v.Dependencies = []pluginDependency{{Filename: "arbitrary.jar", Type: "required"}}
			case "identity mismatch":
				v.Dependencies = []pluginDependency{{ProjectID: "wrong", VersionID: "release", Type: "required"}}
			case "cycle":
				v.Dependencies = []pluginDependency{{ProjectID: "root", VersionID: "release", Type: "required"}}
			case "depth":
				parent := "release"
				for i := 0; i < 8; i++ {
					project := "p" + strings.Repeat("x", i+1)
					version := "v" + strings.Repeat("x", i+1)
					f.addJar(t, project, version, project+".jar", project, "example."+project)
					previous := f.versions[parent]
					previous.Dependencies = []pluginDependency{{ProjectID: project, VersionID: version, Type: "required"}}
					f.versions[parent] = previous
					parent = version
				}
				v = f.versions["release"]
			case "count", "combined size":
				for i := 0; i < 17; i++ {
					project := "p" + strings.Repeat("x", i+1)
					version := "v" + strings.Repeat("x", i+1)
					f.addJar(t, project, version, project+".jar", project, "example."+project)
					if kind == "combined size" {
						dep := f.versions[version]
						dep.Files[0].Size = pluginMaxJar
						f.versions[version] = dep
					}
					v.Dependencies = append(v.Dependencies, pluginDependency{ProjectID: project, VersionID: version, Type: "required"})
				}
			}
			f.versions["release"] = v
			_, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Supported: true, ServerType: "paper", MinecraftVersion: "1.21.8"}, f.data, "root", "release")
			if err == nil {
				t.Fatal("unsafe graph accepted")
			}
		})
	}
}
func TestPluginInstallManagedComponentBoundary(t *testing.T) {
	for _, main := range []string{"org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin", "org.geysermc.floodgate.SpigotPlugin", "com.viaversion.viaversion.ViaVersionPlugin", "us.myles.ViaVersion.ViaVersionPlugin"} {
		t.Run(main, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.addJar(t, "root", "release", "root.jar", "Root", main)
			plan := f.plan(t)
			journal := f.execute(t, plan, false)
			f.assertUntouched(t, journal)
		})
	}
	for _, available := range []bool{false, true} {
		t.Run("dependency", func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.addJar(t, "builtin", "builtinRelease", "viaversion.jar", "ViaVersion", "com.viaversion.viaversion.ViaVersionPlugin")
			f.slugs["builtin"] = "viaversion"
			root := f.versions["release"]
			root.Dependencies = []pluginDependency{{ProjectID: "builtin", VersionID: "builtinRelease", Type: "required"}}
			f.versions["release"] = root
			if available {
				pluginTestJar(t, filepath.Join(f.data, "plugins", "viaversion.jar"), map[string]string{"plugin.yml": "name: ViaVersion\nmain: com.viaversion.viaversion.ViaVersionPlugin\n"})
			}
			plan, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Supported: true, ServerType: "paper", MinecraftVersion: "1.21.8"}, f.data, "root", "release")
			if !available {
				if err == nil {
					t.Fatal("missing component accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Artifacts) != 2 || plan.Artifacts[0].Satisfied != "JustVoxel managed" {
				t.Fatalf("builtin not satisfied: %#v", plan.Artifacts)
			}
			journal := f.execute(t, plan, false)
			if journal.State != operationSucceeded {
				t.Fatal(journal.Status)
			}
			m, _, err := readPluginManifest(f.data)
			if err != nil || len(m.Artifacts) != 1 || m.Artifacts[0].ProjectID != "root" {
				t.Fatal("builtin entered generic manifest", err, m)
			}
		})
	}
}
func TestPluginInstallUntouchedPreparationFailures(t *testing.T) {
	for _, kind := range []string{"download", "hash", "lock", "manual identity", "context changed"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			plan := f.plan(t)
			switch kind {
			case "download":
				f.failDownload = true
			case "hash":
				f.badDownload = true
			case "lock":
				fd, err := unix.Open(pluginRuntimeLockPath, unix.O_CREAT|unix.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer unix.Close(fd)
				if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			case "manual identity":
				pluginTestJar(t, filepath.Join(f.data, "plugins", "manual.jar"), map[string]string{"plugin.yml": "name: Root\nmain: example.Root\n"})
			case "context changed":
				runPluginRuntimeStatus = func(context.Context) ([]byte, error) {
					return []byte(`{"minecraft":{"server_type":"paper","version":"1.20"}}`), nil
				}
			}
			journal := f.execute(t, plan, false)
			f.assertUntouched(t, journal)
			if kind == "manual identity" {
				if _, err := os.Stat(filepath.Join(f.data, "plugins", "manual.jar")); err != nil {
					t.Fatal("manual file touched")
				}
			}
		})
	}
}
func TestPluginInstallFinalPlayerCheck(t *testing.T) {
	for _, kind := range []string{"unavailable", "online", "joined"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			plan := f.plan(t)
			switch kind {
			case "unavailable":
				f.playerUnavailable = true
			case "online":
				f.players = 1
			case "joined":
				if _, _, err := pluginInstallPlayers(context.Background()); err != nil {
					t.Fatal(err)
				}
				f.joinAt = 2
			}
			journal := f.execute(t, plan, false)
			assertPluginStaging(t, f, journal, false)
			if journal.State != operationRolledBack || f.backup {
				t.Fatalf("interruption not protected: %s", journal.Status)
			}
			for _, command := range f.commands {
				if !strings.HasPrefix(command, "systemctl is-active") {
					t.Fatalf("mutation before final player check: %s", command)
				}
			}
			if _, err := os.Stat(filepath.Join(f.data, "plugins", "root.jar")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("plugin placed")
			}
		})
	}
}
func TestPluginInstallSuccessAndOriginalServiceState(t *testing.T) {
	for _, running := range []bool{true, false} {
		t.Run("service state", func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.active = running
			plan := f.plan(t)
			journal := f.execute(t, plan, false)
			assertPluginStaging(t, f, journal, false)
			if journal.State != operationSucceeded || f.active != running || !f.backup {
				t.Fatalf("state %s active %v: %s", journal.State, f.active, journal.Status)
			}
			if !running && !strings.Contains(journal.Status, "stopped state") {
				t.Fatal("incorrect completion")
			}
			m, _, err := readPluginManifest(f.data)
			if err != nil || len(m.Artifacts) != 1 {
				t.Fatal("manifest not committed", err)
			}
			inventory, err := readPluginInventory(f.data)
			if err != nil || len(inventory) != 1 || inventory[0].Management != "plugins" {
				t.Fatalf("inventory: %v %#v", err, inventory)
			}
			if err := os.WriteFile(filepath.Join(f.data, "plugins", "root.jar"), []byte("manual edit"), 0600); err != nil {
				t.Fatal(err)
			}
			inventory, err = readPluginInventory(f.data)
			if err != nil || inventory[0].Management != "manual" || !strings.Contains(inventory[0].UpdateStatus, "Attention:") {
				t.Fatal("modified JAR adopted")
			}
		})
	}
}
func TestPluginInstallBackupAndRuntimeFailuresRollback(t *testing.T) {
	for _, kind := range []string{"backup", "base verify", "missing plugin", "red plugin", "changed rollback file", "failed old runtime"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			plan := f.plan(t)
			switch kind {
			case "backup":
				f.failBackup = true
			case "base verify":
				f.failVerify = true
			case "missing plugin":
				f.missingPlugin = true
			case "red plugin":
				f.failedPlugin = true
			case "changed rollback file":
				f.mutateOnVerify = true
				f.failVerify = true
			case "failed old runtime":
				f.failVerify = true
				f.failRollbackVerify = true
			}
			journal := f.execute(t, plan, false)
			want := operationRolledBack
			if kind == "changed rollback file" || kind == "failed old runtime" {
				want = operationNeedsAttention
			}
			assertPluginStaging(t, f, journal, want == operationNeedsAttention)
			if journal.State != want {
				t.Fatalf("state %s want %s: %s", journal.State, want, journal.Status)
			}
			_, err := os.Stat(filepath.Join(f.data, "plugins", "root.jar"))
			if kind == "changed rollback file" {
				if err != nil {
					t.Fatal("changed file deleted")
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal("transaction file not removed")
			}
			if _, err := os.Stat(filepath.Join(f.data, ".justvoxel", "plugins.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed manifest committed")
			}
			if want == operationRolledBack && !f.active {
				t.Fatal("original running state lost")
			}
		})
	}
}
func TestPluginInstallPlacementNeverReplaces(t *testing.T) {
	f := newPluginInstallFixture(t)
	a := f.plan(t).Artifacts[0]
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, a.Filename), f.jars[a.Filename], 0600); err != nil {
		t.Fatal(err)
	}
	manual := []byte("manual bytes")
	target := filepath.Join(f.data, "plugins", a.Filename)
	if err := os.WriteFile(target, manual, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := openPluginDirectory(f.data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	placed, err := placePluginArtifact(root, directory, "transaction", a)
	if err == nil || placed {
		t.Fatal("overwritten")
	}
	raw, _ := os.ReadFile(target)
	if !bytes.Equal(raw, manual) {
		t.Fatal("manual file changed")
	}
	if err := removePluginTransactionArtifact(root, a); err == nil {
		t.Fatal("manual file deleted by rollback")
	}
}
func TestPluginInstallManifestSafety(t *testing.T) {
	for _, kind := range []string{"directory symlink", "file symlink", "malformed", "unknown schema", "builtin ownership"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			directory := filepath.Join(f.data, ".justvoxel")
			if kind == "directory symlink" {
				if err := os.Symlink(t.TempDir(), directory); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(directory, "plugins.json")
				if kind == "file symlink" {
					if err := os.Symlink(filepath.Join(t.TempDir(), "manifest"), path); err != nil {
						t.Fatal(err)
					}
				} else {
					raw := []byte(`{`)
					if kind == "unknown schema" {
						raw = []byte(`{"schema_version":"v2","artifacts":[]}`)
					}
					if kind == "builtin ownership" {
						raw, _ = json.Marshal(pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{{ProjectID: "builtin", VersionID: "v", VersionNumber: "1", Filename: "viaversion.jar", SHA512: strings.Repeat("a", 128), PluginName: "ViaVersion"}}})
					}
					if err := os.WriteFile(path, raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, _, err := readPluginManifest(f.data); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}
func TestPluginInstallStoreExclusionProgressAndInterruption(t *testing.T) {
	f := newPluginInstallFixture(t)
	plan := f.plan(t)
	journal, created, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
	if err != nil || !created {
		t.Fatal(err)
	}
	duplicate, created, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
	if err != nil || created || duplicate.OperationID != journal.OperationID {
		t.Fatal("duplicate worker")
	}
	for _, kind := range []string{operationTypeMinecraftUpdate, operationTypeMinecraftReset} {
		if _, _, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, kind); err == nil {
			t.Fatal("maintenance exclusion bypassed")
		}
	}
	if current, err := f.s.operations.currentMinecraftReset(); err != nil || current != nil {
		t.Fatal("reset confused plugin operation")
	}
	if _, err := f.s.operations.transition(journal.OperationID, operationValidating, "validating", "Validating plugin."); err != nil {
		t.Fatal(err)
	}
	if err := f.s.operations.pluginProgress(journal.OperationID, "downloading", "Downloading plugin."); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.operations.get(journal.OperationID)
	if err != nil || current.State != operationValidating || current.Stage != "downloading" {
		t.Fatal("progress changed state")
	}
	base := f.s.operations.baseDir
	f.s.operations.close()
	reloaded, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.close()
	current, err = reloaded.get(journal.OperationID)
	if err != nil || current.State != operationNeedsAttention || current.Stage != "interrupted" || !strings.Contains(current.Status, "Plugin installation was interrupted") {
		t.Fatalf("restart: %#v %v", current, err)
	}
}
func TestPluginInstallQueuedRestartCancelsUntouched(t *testing.T) {
	for _, kind := range []string{"worker never started", "durable preflight", "cleanup failure", "redirected staging root"} {
		for _, nextType := range []string{operationTypeMinecraftUpdate, operationTypePluginInstall} {
			t.Run(kind+"/"+nextType, func(t *testing.T) {
				f := newPluginInstallFixture(t)
				plan := f.plan(t)
				journal, created, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
				if err != nil || !created {
					t.Fatal("begin", err)
				}
				base := f.s.operations.baseDir
				stageRoot := filepath.Join(base, "plugin-install")
				directory := filepath.Join(stageRoot, journal.OperationID)
				// Existing plugin and cold-backup bytes must survive all cleanup.
				pluginPath := filepath.Join(f.data, "plugins", "manual.jar")
				backupPath := filepath.Join(base, "cold-backup.zip")
				for _, path := range []string{pluginPath, backupPath} {
					if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if kind != "worker never started" {
					if err := ensurePrivateDirectory(stageRoot); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(directory, 0700); err != nil {
						t.Fatal(err)
					}
					d, err := openPluginEvidenceDirectory(directory)
					if err != nil {
						t.Fatal(err)
					}
					evidence := pluginPreflight{SchemaVersion: "v1", OperationID: journal.OperationID, PlanFingerprint: plan.Fingerprint, DataPath: f.data, MinecraftVersion: plan.Server.MinecraftVersion, ProjectID: plan.ProjectID, VersionID: plan.VersionID}
					err = publishPluginEvidence(d, "preflight.json", evidence)
					d.Close()
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(directory, "partial.jar"), []byte("partial"), 0600); err != nil {
						t.Fatal(err)
					}
					for _, path := range []string{stageRoot, base} {
						d, err := openPluginEvidenceDirectory(path)
						if err != nil {
							t.Fatal(err)
						}
						err = d.Sync()
						d.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				if kind == "cleanup failure" {
					oldRemove := removePluginStaging
					t.Cleanup(func() { removePluginStaging = oldRemove })
					removePluginStaging = func(string) error { return errors.New("injected cleanup failure") }
				}
				if kind == "redirected staging root" {
					outside := t.TempDir()
					if err := os.Rename(stageRoot, filepath.Join(outside, "staging")); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(outside, "staging"), stageRoot); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.s.operations.close(); err != nil {
					t.Fatal(err)
				}
				reloaded, err := openOperationStore(base)
				if err != nil {
					t.Fatal(err)
				}
				defer reloaded.close()
				f.s.operations = reloaded
				current, err := reloaded.get(journal.OperationID)
				if err != nil || current.State != operationRolledBack || current.Stage != "complete" || current.FinishedAt == "" {
					t.Fatal("queued restart not safely cancelled", current, err)
				}
				warning := kind == "cleanup failure" || kind == "redirected staging root"
				if strings.Contains(current.Status, "Warning:") != warning {
					t.Fatal("cleanup warning", current.Status)
				}
				assertPluginStaging(t, f, current, warning)
				if len(f.commands) != 0 || f.playerChecks != 0 || f.backup {
					t.Fatal("queued recovery touched Minecraft", f.commands)
				}
				for _, path := range []string{pluginPath, backupPath} {
					if raw, err := os.ReadFile(path); err != nil || string(raw) != "preserve" {
						t.Fatal("existing files changed", path, err)
					}
				}
				entries, err := os.ReadDir(filepath.Join(f.data, "plugins"))
				if err != nil || len(entries) != 1 || entries[0].Name() != "manual.jar" {
					t.Fatal("plugin directory changed", err)
				}
				if _, err := os.Stat(filepath.Join(f.data, ".justvoxel")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("ownership manifest changed", err)
				}
				if current, err := reloaded.currentPluginInstall(); err != nil || current != nil {
					t.Fatal("queued recovery retained exclusion", current, err)
				}
				if _, created, err := reloaded.beginMinecraftMaintenance("sha256:"+strings.Repeat("c", 64), nextType); err != nil || !created {
					t.Fatal("next maintenance blocked", err)
				}
			})
		}
	}
}

func TestPluginInstallApplyStaleAndDuplicate(t *testing.T) {
	f := newPluginInstallFixture(t)
	plan := f.plan(t)
	oldWorker := startPluginInstallWorker
	calls := 0
	startPluginInstallWorker = func(*server, string, pluginInstallPlan, bool, session) { calls++ }
	defer func() { startPluginInstallWorker = oldWorker }()
	request := pluginInstallApplyRequest{ProjectID: "root", VersionID: "release", PlanFingerprint: "sha256:" + strings.Repeat("0", 64)}
	send := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(request)
		rr := httptest.NewRecorder()
		f.s.adminPluginInstall(rr, surfaceRequest(http.MethodPost, "/", string(raw)))
		return rr
	}
	if rr := send(); rr.Code != 409 || calls != 0 {
		t.Fatal("stale plan accepted")
	}
	request.PlanFingerprint = plan.Fingerprint
	f.players = 1
	if rr := send(); rr.Code != 409 || calls != 0 || !strings.Contains(rr.Body.String(), "confirmation_required") {
		t.Fatal("players not protected")
	}
	request.ConfirmPlayers = true
	first := send()
	second := send()
	if first.Code != 202 || second.Code != 202 || calls != 1 || first.Body.String() != second.Body.String() {
		t.Fatalf("duplicate apply: %d %d calls %d", first.Code, second.Code, calls)
	}
}
func TestPluginInstallManualFilenameBlocksPlan(t *testing.T) {
	f := newPluginInstallFixture(t)
	manual := []byte("manual plugin")
	path := filepath.Join(f.data, "plugins", "root.jar")
	if err := os.WriteFile(path, manual, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Supported: true, ServerType: "paper", MinecraftVersion: "1.21.8"}, f.data, "root", "release")
	if err == nil {
		t.Fatal("manual filename accepted")
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Equal(raw, manual) || f.backup {
		t.Fatal("manual JAR touched")
	}
}
func TestPluginInstallManagedDependencyReuse(t *testing.T) {
	for _, kind := range []string{"exact", "different version", "modified"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.addJar(t, "dependency", "depRelease", "dependency.jar", "Dependency", "example.Dependency")
			root := f.versions["release"]
			root.Dependencies = []pluginDependency{{ProjectID: "dependency", VersionID: "depRelease", Type: "required"}}
			f.versions["release"] = root
			dep := f.versions["depRelease"]
			if err := os.WriteFile(filepath.Join(f.data, "plugins", "dependency.jar"), f.jars["dependency.jar"], 0600); err != nil {
				t.Fatal(err)
			}
			entry := pluginManifestEntry{ProjectID: "dependency", VersionID: dep.ID, VersionNumber: dep.Number, Filename: "dependency.jar", SHA512: dep.Files[0].Hashes.SHA512, PluginName: "Dependency", Main: "example.Dependency", RequiredBy: []string{"previous"}}
			if kind == "different version" {
				entry.VersionID = "old"
			}
			if kind == "modified" {
				entry.SHA512 = strings.Repeat("a", 128)
			}
			if _, err := commitPluginManifest(f.data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{entry}}); err != nil {
				t.Fatal(err)
			}
			plan, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Supported: true, ServerType: "paper", MinecraftVersion: "1.21.8"}, f.data, "root", "release")
			if kind != "exact" {
				if err == nil {
					t.Fatal("unsafe reuse")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.Artifacts[0].Satisfied != "Managed by Plugins" {
				t.Fatal("exact dependency not reused")
			}
		})
	}
}
func TestPluginInstallCatalogAvailabilityAndNoURLs(t *testing.T) {
	for _, kind := range []string{"eligible", "managed", "beta", "incompatible"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			v := f.versions["release"]
			if kind == "beta" {
				v.Type = "beta"
			}
			if kind == "incompatible" {
				v.GameVersions = []string{"1.20"}
			}
			slug := "example"
			if kind == "managed" {
				slug = "viaversion"
			}
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				var raw []byte
				if r.URL.Path == "/v2/search" {
					raw, _ = json.Marshal(map[string]any{"hits": []map[string]string{{"project_id": "root", "title": "Root", "slug": slug}}, "total_hits": 1})
				} else {
					raw, _ = json.Marshal([]pluginCatalogVersion{v})
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}
			rr := httptest.NewRecorder()
			f.s.adminPluginsDiscover(rr, surfaceRequest(http.MethodGet, "/v1/admin/plugins/discover?"+url.Values{"compatible_only": {"false"}, "release_only": {"false"}}.Encode(), ""))
			var catalog pluginCatalog
			if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &catalog) != nil || len(catalog.Projects) != 1 {
				t.Fatal(rr.Body.String())
			}
			if catalog.Projects[0].InstallAvailable != (kind == "eligible") || strings.Contains(rr.Body.String(), "cdn.modrinth.com") {
				t.Fatal("unsafe availability or URL disclosure")
			}
		})
	}
}

// Minimal synthetic fixture preserving TAB OzomWbbH's identity syntax and CRLF.
// The real TAB archive has 4451 ZIP entries, above the former 4096 boundary
// and intentionally accepted under the current 8192 hard limit.
func TestPluginInstallStagedDescriptorTABCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tab.jar")
	pluginTestJar(t, path, map[string]string{
		"plugin.yml": "main: me.neznamy.tab.platforms.bukkit.BukkitTAB\r\nname: TAB\r\nversion: 6.2.0\r\n",
	})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	identity, err := inspectStagedPlugin(f, "tab.jar")
	if err != nil || identity.Name != "TAB" || identity.Version != "6.2.0" || identity.Main != "me.neznamy.tab.platforms.bukkit.BukkitTAB" {
		t.Fatalf("CRLF descriptor identity: %#v, %v", identity, err)
	}
}

func TestPluginInstallStagedZipEntryBound(t *testing.T) {
	for _, count := range []int{4451, 8193} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			fields := map[string]string{
				"plugin.yml": "name: TAB\nmain: me.neznamy.tab.platforms.bukkit.BukkitTAB\nversion: 6.2.0\n",
			}
			for i := 1; i < count; i++ {
				fields["entry-"+strconv.Itoa(i)] = ""
			}
			path := filepath.Join(t.TempDir(), "tab.jar")
			pluginTestJar(t, path, fields)
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			stat, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if stat.Size() >= 1<<20 {
				t.Fatal("synthetic JAR must remain below 1 MiB")
			}
			var end [22]byte
			if _, err := f.ReadAt(end[:], stat.Size()-int64(len(end))); err != nil {
				t.Fatal(err)
			}
			if string(end[:4]) != "PK\x05\x06" || int(binary.LittleEndian.Uint16(end[10:12])) != count {
				t.Fatal("synthetic JAR must have the expected standard ZIP entry count")
			}
			if binary.LittleEndian.Uint32(end[12:16]) >= 1<<20 {
				t.Fatal("synthetic central directory must remain below 1 MiB")
			}
			identity, err := inspectStagedPlugin(f, "tab.jar")
			if count == 8193 {
				if err == nil || err.Error() != "Malformed or oversized plugin JAR" {
					t.Fatalf("ZIP entry ceiling was not enforced: %v", err)
				}
				return
			}
			if err != nil || identity.Name != "TAB" || identity.Version != "6.2.0" || identity.Main != "me.neznamy.tab.platforms.bukkit.BukkitTAB" {
				t.Fatalf("larger plugin JAR identity: %#v, %v", identity, err)
			}
		})
	}
}

func TestPluginInstallStagedDescriptorSafety(t *testing.T) {
	for _, fields := range []map[string]string{
		{"plugin.yml": ""},
		{"paper-plugin.yml": ""},
		{"paper-plugin.yml": "name: Root\nmain: invalid main class\n"},
		{"plugin.yml": "name: Root\nname: Other\n"},
		{"plugin.yml": "version: 1\n"},
		{"plugin.yml": "name: Root\nmain: example.Root\nmain: example.Other\n"},
		{"plugin.yml": "name: Root\n", "paper-plugin.yml": "name: Other\n"},
		{"plugin.yml": strings.Repeat("x", pluginMetadataLimit+1)},
	} {
		path := filepath.Join(t.TempDir(), "test.jar")
		pluginTestJar(t, path, fields)
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = inspectStagedPlugin(f, "test.jar")
		f.Close()
		if err == nil {
			t.Fatal("ambiguous descriptor accepted")
		}
	}
}
func TestPluginInstallCurrentRoutesStayDistinct(t *testing.T) {
	f := newPluginInstallFixture(t)
	plan := f.plan(t)
	journal, _, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	f.s.adminMinecraftStackUpdateCurrent(rr, surfaceRequest(http.MethodGet, "/", ""))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"operation":null`) {
		t.Fatal("update endpoint reported plugin install")
	}
	rr = httptest.NewRecorder()
	f.s.adminPluginInstallCurrent(rr, surfaceRequest(http.MethodGet, "/", ""))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatal("plugin current missing")
	}
	for _, next := range []operationState{operationFailed, operationRollingBack, operationRolledBack} {
		if _, err := f.s.operations.transition(journal.OperationID, next, "rollback", "Untouched plugin rollback."); err != nil {
			t.Fatal(err)
		}
	}
	update, _, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypeMinecraftUpdate)
	if err != nil {
		t.Fatal("plugin locks not released", err)
	}
	current, err := f.s.operations.currentPluginInstall()
	if err != nil || current != nil {
		t.Fatal("plugin current reported update", update)
	}
}
func TestPluginInstallDuplicateAfterPlacementIsLookup(t *testing.T) {
	f := newPluginInstallFixture(t)
	plan := f.plan(t)
	journal, _, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.data, "plugins", "root.jar"), f.jars["root.jar"], 0644); err != nil {
		t.Fatal(err)
	}
	oldWorker := startPluginInstallWorker
	defer func() { startPluginInstallWorker = oldWorker }()
	startPluginInstallWorker = func(*server, string, pluginInstallPlan, bool, session) { t.Fatal("duplicate launched worker") }
	raw, _ := json.Marshal(pluginInstallApplyRequest{ProjectID: "root", VersionID: "release", PlanFingerprint: plan.Fingerprint})
	rr := httptest.NewRecorder()
	f.s.adminPluginInstall(rr, surfaceRequest(http.MethodPost, "/", string(raw)))
	if rr.Code != 202 || !strings.Contains(rr.Body.String(), journal.OperationID) {
		t.Fatalf("placed transaction confused with collision: %d %s", rr.Code, rr.Body.String())
	}
}
func TestPluginInstallManagedDependencyRelationshipCommit(t *testing.T) {
	f := newPluginInstallFixture(t)
	f.addJar(t, "dependency", "depRelease", "dependency.jar", "Dependency", "example.Dependency")
	dep := f.versions["depRelease"]
	if err := os.WriteFile(filepath.Join(f.data, "plugins", "dependency.jar"), f.jars["dependency.jar"], 0644); err != nil {
		t.Fatal(err)
	}
	entry := pluginManifestEntry{ProjectID: "dependency", VersionID: dep.ID, VersionNumber: dep.Number, Filename: "dependency.jar", SHA512: dep.Files[0].Hashes.SHA512, PluginName: "Dependency", Main: "example.Dependency", RequiredBy: []string{"previous"}}
	if _, err := commitPluginManifest(f.data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	v := f.versions["release"]
	v.Dependencies = []pluginDependency{{ProjectID: "dependency", VersionID: dep.ID, Type: "required"}}
	f.versions["release"] = v
	journal := f.execute(t, f.plan(t), false)
	if journal.State != operationSucceeded {
		t.Fatal(journal.Status)
	}
	manifest, _, err := readPluginManifest(f.data)
	if err != nil || len(manifest.Artifacts) != 2 || !slices.Contains(manifest.Artifacts[0].RequiredBy, "root") {
		t.Fatal("dependency relationship not committed", err, manifest)
	}
	if err := os.Remove(filepath.Join(f.data, "plugins", "dependency.jar")); err != nil {
		t.Fatal(err)
	}
	inventory, err := readPluginInventory(f.data)
	if err != nil || inventory[0].Management != "manual" || !strings.Contains(inventory[0].UpdateStatus, "Attention:") {
		t.Fatal("missing managed JAR ownership claimed", err, inventory)
	}
}

func assertPluginStaging(t *testing.T, f *pluginInstallFixture, journal operationJournal, preserved bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(f.s.operations.baseDir, "plugin-install", journal.OperationID))
	if preserved && err != nil {
		t.Fatal("attention staging lost", err)
	}
	if !preserved && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("terminal staging retained", err)
	}
}

func TestPluginInstallReservedCollisionException(t *testing.T) {
	for _, name := range []string{"Geyser-Spigot.jar", "floodgate-spigot.jar", "viaversion.jar", "manual.jar"} {
		t.Run(name, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			raw := []byte("complex metadata unavailable to bounded parser")
			path := filepath.Join(f.data, "plugins", name)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			journal := f.execute(t, f.plan(t), false)
			reserved := name != "manual.jar"
			if reserved && journal.State != operationSucceeded {
				t.Fatal(journal.Status)
			}
			if !reserved && (journal.State != operationRolledBack || f.backup) {
				t.Fatal("unknown manual identity did not block before mutation")
			}
			current, _ := os.ReadFile(path)
			if !bytes.Equal(current, raw) {
				t.Fatal("existing JAR changed")
			}
			manifest, _, err := readPluginManifest(f.data)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range manifest.Artifacts {
				if entry.Filename == name {
					t.Fatal("existing JAR adopted")
				}
			}
		})
	}
}

func TestPluginInstallReservedPathsRemainSafe(t *testing.T) {
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			plan := f.plan(t)
			target := filepath.Join(f.data, "plugins", "geyser-spigot.jar")
			if kind == "symlink" {
				if err := os.Symlink(filepath.Join(t.TempDir(), "other.jar"), target); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			journal := f.execute(t, plan, false)
			if journal.State != operationRolledBack || f.backup {
				t.Fatal("unsafe reserved entry accepted")
			}
		})
	}
}

func TestPluginInstallStagedManagedDescriptorCannotInstall(t *testing.T) {
	for _, main := range []string{"org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin", "org.geysermc.floodgate.SpigotPlugin", "com.viaversion.viaversion.ViaVersionPlugin"} {
		t.Run(main, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.addJar(t, "root", "release", "root.jar", "Root", main)
			journal := f.execute(t, f.plan(t), false)
			if journal.State != operationRolledBack || f.backup {
				t.Fatal("managed staged root accepted")
			}
			assertPluginStaging(t, f, journal, false)
		})
	}
}

func TestPluginInstallTerminalCleanupFailureKeepsSuccessUnlocked(t *testing.T) {
	f := newPluginInstallFixture(t)
	oldRemove := removePluginStaging
	removePluginStaging = func(string) error { return errors.New("cleanup denied") }
	t.Cleanup(func() { removePluginStaging = oldRemove })
	journal := f.execute(t, f.plan(t), false)
	if journal.State != operationSucceeded || !strings.Contains(journal.Status, "staging cleanup failed") {
		t.Fatal(journal.Status)
	}
	assertPluginStaging(t, f, journal, true)
	if _, _, err := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("a", 64), operationTypeMinecraftUpdate); err != nil {
		t.Fatal("cleanup failure retained exclusion", err)
	}
}

func pluginAttentionFixture(t *testing.T, f *pluginInstallFixture) (operationJournal, pluginTransaction) {
	t.Helper()
	plan := f.plan(t)
	a := plan.Artifacts[0]
	a.PluginName, a.Main = "Root", "example.Root"
	journal, _, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []operationState{operationValidating, operationRunning, operationNeedsAttention} {
		journal, err = f.s.operations.transition(journal.OperationID, state, "interrupted", "Plugin installation requires attention.")
		if err != nil {
			t.Fatal(err)
		}
	}
	stageRoot := filepath.Join(f.s.operations.baseDir, "plugin-install")
	if err := ensurePrivateDirectory(stageRoot); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(stageRoot, journal.OperationID)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	tx := pluginTransaction{SchemaVersion: "v1", OperationID: journal.OperationID, PlanFingerprint: plan.Fingerprint, DataPath: f.data, MinecraftVersion: plan.Server.MinecraftVersion, OriginallyRunning: f.active, Artifacts: []pluginManifestEntry{pluginManifestArtifact(a)}}
	if err := persistPluginTransaction(directory, tx); err != nil {
		t.Fatal(err)
	}
	return journal, tx
}

func TestPluginInstallAcknowledgeProofs(t *testing.T) {
	for _, kind := range []string{"installed running", "installed stopped", "previous running", "previous stopped", "changed JAR", "missing manifest", "unknown JAR", "claimed missing JAR", "service mismatch", "runtime failure", "plugin inactive", "wrong context", "legacy evidence"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.active = !strings.Contains(kind, "stopped")
			journal, tx := pluginAttentionFixture(t, f)
			installed := strings.HasPrefix(kind, "installed") || kind == "changed JAR" || kind == "missing manifest" || kind == "plugin inactive"
			if installed || kind == "unknown JAR" {
				raw := f.jars["root.jar"]
				if kind == "changed JAR" || kind == "unknown JAR" {
					raw = []byte("unknown manual file")
				}
				if err := os.WriteFile(filepath.Join(f.data, "plugins", "root.jar"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if (installed && kind != "missing manifest") || kind == "claimed missing JAR" {
				if _, err := commitPluginManifest(f.data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: tx.Artifacts}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "service mismatch" {
				f.active = !f.active
			}
			if kind == "runtime failure" {
				f.failRollbackVerify = true
			}
			if kind == "wrong context" || kind == "legacy evidence" {
				if kind == "wrong context" {
					tx.DataPath = t.TempDir()
				} else {
					tx.DataPath, tx.MinecraftVersion = "", ""
				}
				evidence := filepath.Join(f.s.operations.baseDir, "plugin-install", journal.OperationID, "transaction.json")
				raw, _ := json.Marshal(tx)
				if err := os.WriteFile(evidence, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "plugin inactive" {
				writePluginStartup(t, f.data, pluginFixtureStartup+"[Root] Error occurred while enabling Root v1.0\n")
			}
			raw, _ := json.Marshal(map[string]string{"operation_id": journal.OperationID})
			rr := httptest.NewRecorder()
			f.s.adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", string(raw)))
			safe := strings.HasPrefix(kind, "installed") || strings.HasPrefix(kind, "previous") || kind == "legacy evidence"
			if safe {
				if rr.Code != 200 {
					t.Fatal(rr.Code, rr.Body.String())
				}
				current, err := f.s.operations.get(journal.OperationID)
				if err != nil || current.State != operationResolved {
					t.Fatal("not resolved", err)
				}
				assertPluginStaging(t, f, current, false)
				if _, _, err := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("b", 64), operationTypeMinecraftUpdate); err != nil {
					t.Fatal("resolution did not release exclusion", err)
				}
			} else {
				if rr.Code != 409 {
					t.Fatal(rr.Code, rr.Body.String())
				}
				current, _ := f.s.operations.get(journal.OperationID)
				if current.State != operationNeedsAttention {
					t.Fatal("unsafe resolution")
				}
				assertPluginStaging(t, f, current, true)
				if _, _, err := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("b", 64), operationTypeMinecraftUpdate); err == nil {
					t.Fatal("conflict released maintenance exclusion")
				}
			}
			for _, command := range f.commands {
				if strings.HasPrefix(command, "systemctl start") || strings.HasPrefix(command, "systemctl stop") || strings.Contains(command, "minecraft-backup") {
					t.Fatal("re-check mutated service", command)
				}
			}
		})
	}
}

func TestPluginInstallAcknowledgeStrictIdentityAndAuthorization(t *testing.T) {
	f := newPluginInstallFixture(t)
	journal, _ := pluginAttentionFixture(t, f)
	for _, body := range []string{`null`, `{}`, `{"operation_id":"invalid"}`, `{"operation_id":"` + journal.OperationID + `","force":true}`, `{"operation_id":"` + journal.OperationID + `"} {}`} {
		rr := httptest.NewRecorder()
		f.s.adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", body))
		if rr.Code != 400 {
			t.Fatal("invalid acknowledge accepted", rr.Code)
		}
	}
	for _, role := range []principalRole{roleViewer, roleOperator} {
		rr := httptest.NewRecorder()
		surfaceTestServer(t, role).adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", `{"operation_id":"`+journal.OperationID+`"}`))
		if rr.Code != 403 {
			t.Fatal("unauthorized acknowledge")
		}
	}
	rr := httptest.NewRecorder()
	f.s.adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", `{"operation_id":"12345678-1234-4123-8123-123456789abc"}`))
	if rr.Code != 409 {
		t.Fatal("wrong operation accepted")
	}
}

func TestPluginInstallInterruptedStagingPreserved(t *testing.T) {
	for _, state := range []operationState{operationValidating, operationRunning} {
		t.Run(string(state), func(t *testing.T) {
			f := newPluginInstallFixture(t)
			journal, _ := pluginAttentionFixture(t, f)
			base := f.s.operations.baseDir
			staged := filepath.Join(base, "plugin-install", journal.OperationID, "root.jar")
			if err := os.WriteFile(staged, f.jars["root.jar"], 0600); err != nil {
				t.Fatal(err)
			}
			// Simulate an interrupted journal, keeping the already-written evidence.
			f.s.operations.mu.Lock()
			stored := f.s.operations.operations[journal.OperationID]
			stored.State = state
			if err := f.s.operations.persist(stored); err != nil {
				t.Fatal(err)
			}
			f.s.operations.operations[journal.OperationID] = stored
			f.s.operations.mu.Unlock()
			f.s.operations.close()
			reloaded, err := openOperationStore(base)
			if err != nil {
				t.Fatal(err)
			}
			defer reloaded.close()
			current, err := reloaded.get(journal.OperationID)
			if err != nil || current.State != operationNeedsAttention {
				t.Fatal("interruption not retained", err)
			}
			if _, err := readPluginTransaction(base, journal.OperationID); err != nil {
				t.Fatal("interrupted evidence lost", err)
			}
			if raw, err := os.ReadFile(staged); err != nil || !bytes.Equal(raw, f.jars["root.jar"]) {
				t.Fatal("interrupted download lost", err)
			}
			if _, err := os.Stat(filepath.Join(base, "plugin-install", journal.OperationID)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := reloaded.beginMinecraftMaintenance("sha256:"+strings.Repeat("c", 64), operationTypeMinecraftUpdate); err == nil {
				t.Fatal("interrupted operation lost exclusion")
			}
		})
	}
}

func TestPluginInstallAcknowledgeUnsafeEvidenceAndOwnership(t *testing.T) {
	for _, kind := range []string{"missing evidence", "missing service evidence", "evidence symlink", "malformed manifest", "managed ownership", "wrong release", "wrong hash", "transaction residue"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			journal, tx := pluginAttentionFixture(t, f)
			evidence := filepath.Join(f.s.operations.baseDir, "plugin-install", journal.OperationID, "transaction.json")
			switch kind {
			case "missing evidence":
				if err := os.Remove(evidence); err != nil {
					t.Fatal(err)
				}
			case "missing service evidence":
				raw, _ := json.Marshal(tx)
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				delete(fields, "originally_running")
				raw, _ = json.Marshal(fields)
				if err := os.WriteFile(evidence, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "evidence symlink":
				target := filepath.Join(t.TempDir(), "transaction.json")
				raw, _ := json.Marshal(tx)
				if err := os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(evidence); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, evidence); err != nil {
					t.Fatal(err)
				}
			case "transaction residue":
				if err := os.WriteFile(filepath.Join(f.data, "plugins", ".justvoxel-"+journal.OperationID+"-root.jar.tmp"), []byte("unproven placement"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(filepath.Join(f.data, "plugins", "root.jar"), f.jars["root.jar"], 0600); err != nil {
					t.Fatal(err)
				}
				manifest := pluginManifest{SchemaVersion: "v1", Artifacts: slices.Clone(tx.Artifacts)}
				switch kind {
				case "managed ownership":
					manifest.Artifacts[0].Main = "org.geysermc.floodgate.SpigotPlugin"
				case "wrong release":
					manifest.Artifacts[0].VersionID = "other"
				case "wrong hash":
					manifest.Artifacts[0].SHA512 = strings.Repeat("a", 128)
				}
				if _, err := commitPluginManifest(f.data, nil, manifest); err != nil {
					t.Fatal(err)
				}
				if kind == "malformed manifest" {
					if err := os.WriteFile(filepath.Join(f.data, ".justvoxel", "plugins.json"), []byte("{"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			rr := httptest.NewRecorder()
			f.s.adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", `{"operation_id":"`+journal.OperationID+`"}`))
			if rr.Code != 409 {
				t.Fatal("unsafe evidence accepted", rr.Code, rr.Body.String())
			}
			current, _ := f.s.operations.get(journal.OperationID)
			if current.State != operationNeedsAttention {
				t.Fatal("unsafe evidence released operation")
			}
			assertPluginStaging(t, f, current, true)
		})
	}
}

func TestPluginInstallAcknowledgeRequiresCurrentTypeAndState(t *testing.T) {
	for _, kind := range []string{operationTypeMinecraftUpdate, operationTypePluginInstall} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			journal, _, err := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("a", 64), kind)
			if err != nil {
				t.Fatal(err)
			}
			if kind != operationTypePluginInstall {
				if _, err := f.s.operations.transition(journal.OperationID, operationNeedsAttention, "interrupted", "Update needs review."); err != nil {
					t.Fatal(err)
				}
			}
			rr := httptest.NewRecorder()
			f.s.adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", `{"operation_id":"`+journal.OperationID+`"}`))
			if rr.Code != 409 {
				t.Fatal("wrong type/state acknowledged", rr.Code)
			}
		})
	}
}

func TestPluginInstallRenamedStagedManagedDependencyUsesExistingComponent(t *testing.T) {
	for _, component := range []struct{ filename, name, main string }{
		{"geyser-spigot.jar", "Geyser-Spigot", "org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin"},
		{"floodgate-spigot.jar", "floodgate", "org.geysermc.floodgate.SpigotPlugin"},
		{"viaversion.jar", "ViaVersion", "com.viaversion.viaversion.ViaVersionPlugin"},
	} {
		t.Run(component.name, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.addJar(t, "builtin", "builtinRelease", "renamed.jar", component.name, component.main)
			root := f.versions["release"]
			root.Dependencies = []pluginDependency{{ProjectID: "builtin", VersionID: "builtinRelease", Type: "required"}}
			f.versions["release"] = root
			path := filepath.Join(f.data, "plugins", component.filename)
			pluginTestJar(t, path, map[string]string{"plugin.yml": "name: " + component.name + "\nmain: " + component.main + "\n"})
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			journal := f.execute(t, f.plan(t), false)
			if journal.State != operationSucceeded {
				t.Fatal(journal.Status)
			}
			if _, err := os.Stat(filepath.Join(f.data, "plugins", "renamed.jar")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("duplicate managed JAR placed")
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, current) {
				t.Fatal("managed JAR modified")
			}
			manifest, _, err := readPluginManifest(f.data)
			if err != nil || len(manifest.Artifacts) != 1 || manifest.Artifacts[0].ProjectID != "root" {
				t.Fatal("managed component adopted", err, manifest)
			}
		})
	}
}

func TestPluginInstallPreflightRestartAcknowledgement(t *testing.T) {
	for _, kind := range []string{"running", "stopped", "malformed preflight", "malformed transaction", "residue", "wrong path", "wrong version", "wrong fingerprint", "unknown service", "runtime failure", "unsafe preflight"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.active = kind != "stopped"
			plan := f.plan(t)
			journal, _, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
			if err != nil {
				t.Fatal(err)
			}
			base := f.s.operations.baseDir
			stageRoot := filepath.Join(base, "plugin-install")
			if err := ensurePrivateDirectory(stageRoot); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(stageRoot, journal.OperationID)
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			d, err := openPluginEvidenceDirectory(directory)
			if err != nil {
				t.Fatal(err)
			}
			evidence := pluginPreflight{SchemaVersion: "v1", OperationID: journal.OperationID, PlanFingerprint: plan.Fingerprint, DataPath: f.data, MinecraftVersion: plan.Server.MinecraftVersion, ProjectID: plan.ProjectID, VersionID: plan.VersionID}
			switch kind {
			case "wrong path":
				evidence.DataPath = t.TempDir()
			case "wrong version":
				evidence.MinecraftVersion = "1.20"
			case "wrong fingerprint":
				evidence.PlanFingerprint = "sha256:" + strings.Repeat("b", 64)
			}
			if err := publishPluginEvidence(d, "preflight.json", evidence); err != nil {
				t.Fatal(err)
			}
			d.Close()
			if err := os.WriteFile(filepath.Join(directory, "partial.jar"), []byte("partial download"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "malformed preflight" || kind == "malformed transaction" {
				name := "preflight.json"
				if kind == "malformed transaction" {
					name = "transaction.json"
				}
				if err := os.WriteFile(filepath.Join(directory, name), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "unsafe preflight" {
				if err := os.Chmod(filepath.Join(directory, "preflight.json"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "residue" {
				if err := os.WriteFile(filepath.Join(f.data, "plugins", ".justvoxel-"+journal.OperationID+"-root.jar.tmp"), []byte("residue"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.s.operations.transition(journal.OperationID, operationValidating, "validating", "Validating plugin."); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.operations.transition(journal.OperationID, operationRunning, "downloading", "Downloading plugin."); err != nil {
				t.Fatal(err)
			}
			if err := f.s.operations.close(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := openOperationStore(base)
			if err != nil {
				t.Fatal(err)
			}
			defer reloaded.close()
			f.s.operations = reloaded
			current, err := reloaded.get(journal.OperationID)
			if err != nil || current.State != operationNeedsAttention {
				t.Fatal("restart not retained", current, err)
			}
			before, err := os.ReadDir(filepath.Join(f.data, "plugins"))
			if err != nil {
				t.Fatal(err)
			}
			originalCommand := pluginInstallCommand
			pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if kind == "unknown service" && command == "systemctl" {
					return []byte("unknown"), errors.New("unknown")
				}
				return originalCommand(ctx, command, args...)
			}
			f.failRollbackVerify = kind == "runtime failure"
			rr := httptest.NewRecorder()
			f.s.adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", `{"operation_id":"`+journal.OperationID+`"}`))
			safe := kind == "running" || kind == "stopped"
			current, err = reloaded.get(journal.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if safe {
				if rr.Code != 200 || current.State != operationResolved {
					t.Fatal(rr.Code, rr.Body.String(), current)
				}
			} else if rr.Code != 409 || current.State != operationNeedsAttention {
				t.Fatal("unsafe resolution", rr.Code, current)
			}
			assertPluginStaging(t, f, current, !safe)
			_, _, exclusionErr := reloaded.beginMinecraftMaintenance("sha256:"+strings.Repeat("c", 64), operationTypeMinecraftUpdate)
			if (exclusionErr == nil) != safe {
				t.Fatal("incorrect maintenance exclusion", exclusionErr)
			}
			after, err := os.ReadDir(filepath.Join(f.data, "plugins"))
			if err != nil || len(after) != len(before) {
				t.Fatal("plugin directory changed", err)
			}
			for i := range before {
				if before[i].Name() != after[i].Name() {
					t.Fatal("plugin entry changed")
				}
			}
			verified := false
			for _, command := range f.commands {
				if strings.HasPrefix(command, "systemctl start") || strings.HasPrefix(command, "systemctl stop") || strings.Contains(command, "minecraft-backup") || strings.HasSuffix(command, "rcon-cli plugins") {
					t.Fatal("preflight acknowledgement mutated or retried installation", command)
				}
				if strings.Contains(command, "verify-minecraft-stack") {
					verified = true
				}
			}
			if safe && verified != f.active {
				t.Fatal("runtime verification must occur only for running Minecraft")
			}
			if f.active != (kind != "stopped") {
				t.Fatal("service state changed")
			}
		})
	}
}

func TestPluginInstallQueuedUntilPreflightDurable(t *testing.T) {
	for _, failure := range []string{"", "preflight file", "transaction directory", "stage root", "store base"} {
		t.Run("failure="+failure, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			plan := f.plan(t)
			journal, created, err := f.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginInstall)
			if err != nil || !created {
				t.Fatal(err)
			}
			base := f.s.operations.baseDir
			stageRoot := filepath.Join(base, "plugin-install")
			directory := filepath.Join(stageRoot, journal.OperationID)
			fileSynced, directorySynced, parentSynced, baseSynced := false, false, false, false
			durable := func() bool { return fileSynced && directorySynced && parentSynced && baseSynced }
			oldSync := pluginEvidenceSync
			t.Cleanup(func() { pluginEvidenceSync = oldSync })
			pluginEvidenceSync = func(file *os.File) error {
				if durable() {
					return oldSync(file) // Later transaction evidence retains its own checks.
				}
				current, err := f.s.operations.get(journal.OperationID)
				persisted, readErr := readOperationJournal(filepath.Join(f.s.operations.operationsDir, journal.OperationID+".json"))
				if err != nil || readErr != nil || current.State != operationQueued || persisted.State != operationQueued {
					t.Fatal("worker left queued before preflight durability", current, persisted, err, readErr)
				}
				var point string
				var flag *bool
				switch {
				case strings.HasPrefix(filepath.Base(file.Name()), ".preflight.json-"):
					point, flag = "preflight file", &fileSynced
				case file.Name() == directory:
					point, flag = "transaction directory", &directorySynced
					if _, err := readPluginPreflight(base, journal.OperationID); err != nil {
						t.Fatal("preflight not published", err)
					}
				case file.Name() == stageRoot:
					point, flag = "stage root", &parentSynced
				case file.Name() == base:
					point, flag = "store base", &baseSynced
				default:
					t.Fatal("unexpected preflight sync", file.Name())
				}
				if failure == point {
					return errors.New("injected durability failure")
				}
				if err := oldSync(file); err != nil {
					return err
				}
				*flag = true
				return nil
			}
			// now is called under the store mutex after transition validation;
			// inspect the prior journal directly without re-entering that mutex.
			oldNow := f.s.operations.now
			sawValidating, leftQueued := false, false
			f.s.operations.now = func() time.Time {
				state := f.s.operations.operations[journal.OperationID].State
				if state == operationValidating {
					sawValidating = true
				}
				if state != operationQueued {
					leftQueued = true
					if !durable() {
						t.Fatal("nonqueued worker without durable preflight", state)
					}
				}
				return oldNow()
			}
			downloaded := false
			oldDownload := pluginDownloadClient.Transport
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				downloaded = true
				if !durable() || !sawValidating {
					t.Fatal("download before durable preflight and validating")
				}
				return oldDownload.RoundTrip(r)
			})
			executeErr := executePluginInstall(context.Background(), f.s, journal.OperationID, plan, false, session{})
			f.s.operations.now = oldNow
			current, err := f.s.operations.get(journal.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "" {
				if executeErr != nil || current.State != operationSucceeded || !sawValidating || !downloaded || !durable() {
					t.Fatal("durable worker did not proceed", current, executeErr)
				}
				return
			}
			if executeErr == nil || leftQueued || sawValidating || downloaded || f.playerChecks != 0 {
				t.Fatal("preflight failure crossed queued boundary", current, executeErr)
			}
			f.assertUntouched(t, current)
			if _, created, err := f.s.operations.beginMinecraftMaintenance("sha256:"+strings.Repeat("c", 64), operationTypeMinecraftUpdate); err != nil || !created {
				t.Fatal("preflight failure retained exclusion", err)
			}
		})
	}
}

func TestPluginInstallDurableEvidenceBeforeMutation(t *testing.T) {
	for _, kind := range []string{"success", "preflight sync failure", "transaction file sync failure", "transaction directory sync failure", "existing transaction"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			plan := f.plan(t)
			oldSync := pluginEvidenceSync
			t.Cleanup(func() { pluginEvidenceSync = oldSync })
			transactionFileSynced, transactionDirectorySynced := false, false
			pluginEvidenceSync = func(file *os.File) error {
				name := filepath.Base(file.Name())
				if strings.HasPrefix(name, ".preflight.json-") && kind == "preflight sync failure" {
					return errors.New("injected file sync failure")
				}
				if strings.HasPrefix(name, ".transaction.json-") {
					if kind == "transaction file sync failure" {
						return errors.New("injected file sync failure")
					}
					if err := oldSync(file); err != nil {
						return err
					}
					transactionFileSynced = true
					return nil
				}
				if transactionFileSynced && validOperationID(name) {
					if kind == "transaction directory sync failure" {
						return errors.New("injected directory sync failure")
					}
					if err := oldSync(file); err != nil {
						return err
					}
					transactionDirectorySynced = true
					return nil
				}
				return oldSync(file)
			}
			oldDownload := pluginDownloadClient.Transport
			downloaded := false
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				downloaded = true
				journal, err := f.s.operations.currentPluginInstall()
				if err != nil || journal == nil {
					t.Fatal("missing operation", err)
				}
				if _, err := readPluginPreflight(f.s.operations.baseDir, journal.OperationID); err != nil {
					t.Fatal("download before preflight", err)
				}
				if kind == "existing transaction" {
					if err := os.WriteFile(filepath.Join(f.s.operations.baseDir, "plugin-install", journal.OperationID, "transaction.json"), []byte("existing evidence"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return oldDownload.RoundTrip(r)
			})
			oldCommand := pluginInstallCommand
			pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if strings.Contains(command, "minecraft-backup") {
					if !transactionFileSynced || !transactionDirectorySynced || f.playerChecks == 0 {
						t.Fatal("backup before durable evidence or player check")
					}
					journal, err := f.s.operations.currentPluginInstall()
					if err != nil || journal == nil {
						t.Fatal(err)
					}
					if _, err := readPluginTransaction(f.s.operations.baseDir, journal.OperationID); err != nil {
						t.Fatal("invalid evidence before backup", err)
					}
				}
				return oldCommand(ctx, command, args...)
			}
			journal := f.execute(t, plan, false)
			if kind == "success" {
				if journal.State != operationSucceeded || !f.backup {
					t.Fatal(journal)
				}
				return
			}
			if kind == "preflight sync failure" && downloaded {
				t.Fatal("download before durable preflight")
			}
			if journal.State != operationRolledBack || f.backup || !f.active {
				t.Fatal("evidence failure mutated runtime", journal, f.commands)
			}
			for _, command := range f.commands {
				if strings.Contains(command, "minecraft-backup") || strings.HasPrefix(command, "systemctl start") || strings.HasPrefix(command, "systemctl stop") {
					t.Fatal("mutation before durable evidence", command)
				}
			}
			if _, err := os.Stat(filepath.Join(f.data, "plugins", "root.jar")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("placed despite evidence failure")
			}
			if _, err := os.Stat(filepath.Join(f.data, ".justvoxel")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("ownership mutated despite evidence failure")
			}
		})
	}
}

func TestPluginInstallCanonicalManagedDependencies(t *testing.T) {
	for _, component := range []struct{ slug, filename, main string }{
		{"geyser", "geyser-spigot.jar", "org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin"},
		{"floodgate", "floodgate-spigot.jar", "org.geysermc.floodgate.SpigotPlugin"},
		{"viaversion", "viaversion.jar", "com.viaversion.viaversion.ViaVersionPlugin"},
	} {
		for _, kind := range []string{"canonical", "uppercase", "renamed", "unreadable renamed", "display name only", "duplicate"} {
			t.Run(component.slug+"/"+kind, func(t *testing.T) {
				f := newPluginInstallFixture(t)
				f.addJar(t, "builtin", "builtinRelease", "dependency.jar", "Builtin", component.main)
				f.slugs["builtin"] = component.slug
				rootVersion := f.versions["release"]
				rootVersion.Dependencies = []pluginDependency{{ProjectID: "builtin", VersionID: "builtinRelease", Type: "required"}}
				f.versions["release"] = rootVersion
				name := component.filename
				if kind == "uppercase" {
					name = strings.ToUpper(name)
				}
				if kind == "renamed" || kind == "unreadable renamed" || kind == "display name only" {
					name = "renamed.jar"
				}
				path := filepath.Join(f.data, "plugins", name)
				// A canonical JAR need not be readable by the generic descriptor parser.
				if err := os.WriteFile(path, []byte("opaque existing managed component"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "renamed" || kind == "duplicate" {
					pluginTestJar(t, filepath.Join(f.data, "plugins", "renamed.jar"), map[string]string{"plugin.yml": "name: Builtin\nmain: " + component.main + "\n"})
				}
				if kind == "display name only" {
					pluginTestJar(t, path, map[string]string{"plugin.yml": "name: " + component.slug + "\nmain: example.Unowned\n"})
				}
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				plan, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Supported: true, ServerType: "paper", MinecraftVersion: "1.21.8"}, f.data, "root", "release")
				if kind == "unreadable renamed" || kind == "display name only" || kind == "duplicate" {
					if err == nil {
						t.Fatal("unproven or duplicate ownership accepted")
					}
					if kind == "duplicate" && err.Error() != "Multiple copies of a JustVoxel component require attention" {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				sum := sha512.Sum512(original)
				found := false
				for _, artifact := range plan.Artifacts {
					if artifact.ProjectID == "builtin" {
						found = true
						if artifact.Satisfied != "JustVoxel managed" || artifact.ExistingFilename != name || artifact.ExistingHash != hex.EncodeToString(sum[:]) {
							t.Fatal("missing exact reuse evidence", artifact)
						}
					}
				}
				if !found {
					t.Fatal("missing dependency")
				}
				oldDownload := pluginDownloadClient.Transport
				pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
					if filepath.Base(r.URL.Path) == "dependency.jar" {
						t.Fatal("managed dependency downloaded")
					}
					return oldDownload.RoundTrip(r)
				})
				journal := f.execute(t, plan, false)
				if journal.State != operationSucceeded {
					t.Fatal(journal.Status)
				}
				current, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(current, original) {
					t.Fatal("managed component modified", err)
				}
				if _, err := os.Stat(filepath.Join(f.data, "plugins", "dependency.jar")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("managed dependency placed generically")
				}
				manifest, _, err := readPluginManifest(f.data)
				if err != nil || len(manifest.Artifacts) != 1 || manifest.Artifacts[0].ProjectID != "root" {
					t.Fatal("managed component adopted", manifest, err)
				}
			})
		}
	}
}

// Umask is process-global: this regression must never run in parallel.
func TestPluginPlacementReadableWithRestrictiveUmask(t *testing.T) {
	f := newPluginInstallFixture(t)
	artifact := f.plan(t).Artifacts[0]
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, artifact.Filename), f.jars[artifact.Filename], 0600); err != nil {
		t.Fatal(err)
	}
	root, err := openPluginDirectory(f.data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	previous := unix.Umask(0007)
	defer unix.Umask(previous)
	placed, err := placePluginArtifact(root, staging, "readability", artifact)
	if err != nil || !placed {
		t.Fatalf("placement: %v %v", placed, err)
	}
	path := filepath.Join(f.data, "plugins", artifact.Filename)
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0644 {
		t.Fatalf("published mode: %o", stat.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, f.jars[artifact.Filename]) {
		t.Fatal("published content changed", err)
	}
	hash, err := hashPluginAt(root, artifact.Filename)
	if err != nil || hash != artifact.SHA512 {
		t.Fatal("published hash changed", err)
	}
}

func TestPluginPlanDirectArtifactReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		entries   map[string]string
		rejection string
	}{
		{name: "valid"},
		{name: "unknown JAR", entries: map[string]string{"random.txt": "unknown"}, rejection: pluginDirectReviewRejection},
		{name: "empty descriptor", entries: map[string]string{"paper-plugin.yml": ""}, rejection: pluginDirectReviewRejection},
		{name: "loader with empty descriptor", entries: map[string]string{"paper-plugin.yml": "", "fabric.mod.json": `{ "id": "slumber" }`}, rejection: pluginLoaderReviewRejection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			reviewTemp := t.TempDir()
			t.Setenv("TMPDIR", reviewTemp)
			if tc.entries != nil {
				path := filepath.Join(t.TempDir(), "slumber.jar")
				pluginTestJar(t, path, tc.entries)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				version := f.versions["release"]
				sum := sha512.Sum512(raw)
				version.Files[0].Size = int64(len(raw))
				version.Files[0].Hashes.SHA512 = hex.EncodeToString(sum[:])
				f.versions["release"] = version
				f.jars["root.jar"] = raw
			}
			rr := httptest.NewRecorder()
			f.s.adminPluginInstallPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/plugins/install/plan", `{"project_id":"root","version_id":"release"}`))
			if tc.rejection == "" && rr.Code != http.StatusOK {
				t.Fatalf("valid review: %d %s", rr.Code, rr.Body.String())
			}
			if tc.rejection != "" && (rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), tc.rejection)) {
				t.Fatalf("invalid review: %d %s", rr.Code, rr.Body.String())
			}
			temporary, err := os.ReadDir(reviewTemp)
			if err != nil || len(temporary) != 0 {
				t.Fatal("review staging was not removed", err)
			}
			operation, err := f.s.operations.currentPluginInstall()
			if err != nil || operation != nil {
				t.Fatal("review started operation", err)
			}
			if len(f.commands) != 0 || f.backup {
				t.Fatal("review mutated Minecraft", f.commands)
			}
			entries, err := os.ReadDir(filepath.Join(f.data, "plugins"))
			if err != nil || len(entries) != 0 {
				t.Fatal("review placed files", err)
			}
			if _, err := os.Stat(filepath.Join(f.data, ".justvoxel")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("review created ownership evidence")
			}
		})
	}
}

func TestPluginRequiredDependencyLoaderScoping(t *testing.T) {
	for _, exact := range []bool{false, true} {
		t.Run(strconv.FormatBool(exact), func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.addJar(t, "fabricapi", "fabricrelease", "fabric-api.jar", "FabricAPI", "fabric.API")
			fabric := f.versions["fabricrelease"]
			fabric.Loaders = []string{"fabric"}
			f.versions["fabricrelease"] = fabric
			dependency := pluginDependency{ProjectID: "fabricapi", Type: "required"}
			if exact {
				dependency.VersionID = "fabricrelease"
			}
			root := f.versions["release"]
			root.Loaders = []string{"bukkit", "fabric", "paper", "purpur", "quilt", "spigot"}
			root.Dependencies = []pluginDependency{dependency, {ProjectID: "modmenu", Type: "optional"}, {ProjectID: "yacl", Type: "optional"}}
			f.versions["release"] = root
			plan := f.plan(t)
			if len(plan.Artifacts) != 1 || !plan.Artifacts[0].Requested {
				t.Fatal("cross-loader dependency installed", plan.Artifacts)
			}
			if !slices.Contains(plan.DisplayDependencies, dependency) || len(plan.DisplayDependencies) != 3 {
				t.Fatal("publisher metadata lost", plan.DisplayDependencies)
			}
			if err := reviewRootPluginArtifact(context.Background(), plan); err != nil {
				t.Fatal("Paper root review rejected", err)
			}
		})
	}
}

func TestPluginRequiredDependencyNoCompatibleReleaseAndIdentityMismatch(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(strconv.FormatBool(corrupt), func(t *testing.T) {
			f := newPluginInstallFixture(t)
			f.addJar(t, "dependency", "dependencyrelease", "dependency.jar", "Dependency", "example.Dependency")
			root := f.versions["release"]
			dependency := pluginDependency{ProjectID: "dependency", Type: "required"}
			if corrupt {
				dependency.VersionID = "dependencyrelease"
			}
			root.Dependencies = []pluginDependency{dependency}
			f.versions["release"] = root
			version := f.versions["dependencyrelease"]
			if corrupt {
				version.ProjectID = "wrong"
			} else {
				version.GameVersions = []string{"1.20"}
			}
			f.versions["dependencyrelease"] = version
			_, err := resolvePluginInstallPlan(context.Background(), pluginServerContext{Supported: true, ServerType: "paper", MinecraftVersion: "1.21.8"}, f.data, "root", "release")
			if err == nil {
				t.Fatal("unsafe dependency plan accepted")
			}
			if corrupt && !strings.Contains(err.Error(), "identity mismatch") {
				t.Fatal("identity corruption misreported", err)
			}
			if !corrupt && (!strings.Contains(err.Error(), "Required dependency dependency has no compatible Paper release for Minecraft 1.21.8") || strings.Contains(err.Error(), "project mismatch")) {
				t.Fatal("compatibility failure misreported", err)
			}
		})
	}
}

const pluginFixtureStartup = "[PluginInitializerManager] Initializing plugins...\n[PluginInitializerManager] Bukkit plugins (2):\n - Root (1.0), Dependency (1.0)\n[Root] Enabling Root v1.0\n[Dependency] Enabling Dependency v1.0\n"

func writePluginStartup(t *testing.T, dataPath, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dataPath, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataPath, "logs", "latest.log"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPluginStartupNamesLoaded(t *testing.T) {
	const bukkit = `[PluginInitializerManager] Initializing plugins...
[PluginInitializerManager] Initialized 5 plugins
[PluginInitializerManager] Bukkit plugins (5):
 - Geyser-Spigot (2.11.3-SNAPSHOT), ViaBackwards (5.12.1), ViaRewind (4.2.0), ViaVersion (5.12.1), floodgate (2.2.5-SNAPSHOT (b141-81b65cc))
[ViaRewind] Loading server plugin ViaRewind v4.2.0
[ViaRewind] Enabling ViaRewind v4.2.0
`
	const paper = `[PluginInitializerManager] Initializing plugins...
[PluginInitializerManager] Initialized 4 plugins
[PluginInitializerManager] Paper plugins (1):
 - NeoSpeedZero (6.4.1)
[PluginInitializerManager] Bukkit plugins (3):
 - Geyser-Spigot (...), ViaVersion (...), floodgate (...)
[NeoSpeedZero] Loading server plugin NeoSpeedZero v6.4.1
[NeoSpeedZero] Enabling NeoSpeeZero v6.4.1
`
	for _, tc := range []struct {
		name, raw string
		expected  []string
		ok        bool
	}{
		{"real Bukkit", bukkit, []string{"ViaRewind"}, true},
		{"real Paper logger", paper, []string{"NeoSpeedZero"}, true},
		{"timestamped", strings.ReplaceAll(bukkit, "[", "[12:34:56 INFO]: ["), []string{"ViaRewind"}, true},
		{"thread timestamped", strings.ReplaceAll(bukkit, "[", "[12:34:56] [Server thread/INFO]: ["), []string{"ViaRewind"}, true},
		{"incomplete line", strings.TrimSuffix(bukkit, "\n"), []string{"ViaRewind"}, false},
		{"stale startup", bukkit + paper, []string{"ViaRewind"}, false},
		{"earlier malformed startup", "[PluginInitializerManager] Initializing plugins...\n[PluginInitializerManager] Bukkit plugins (x):\n" + bukkit, []string{"ViaRewind"}, true},
		{"no marker", strings.ReplaceAll(bukkit, "[PluginInitializerManager] Initializing plugins...\n", ""), []string{"ViaRewind"}, false},
		{"no enable", strings.ReplaceAll(bukkit, "[ViaRewind] Enabling ViaRewind v4.2.0\n", ""), []string{"ViaRewind"}, false},
		{"no inventory", strings.ReplaceAll(bukkit, "ViaRewind (4.2.0)", "Other (4.2.0)"), []string{"ViaRewind"}, false},
		{"enable failure", bukkit + "[Server] Error occurred while enabling ViaRewind v4.2.0 (Is it up to date?)\n", []string{"ViaRewind"}, false},
		{"logger failure", bukkit + "[ViaRewind] Failed to enable plugin\n", []string{"ViaRewind"}, false},
		{"load failure", bukkit + "[Server] Could not load plugin 'ViaRewind'\n", []string{"ViaRewind"}, false},
		{"malformed count", strings.Replace(bukkit, "plugins (5):", "plugins (x):", 1), []string{"ViaRewind"}, false},
		{"count mismatch", strings.Replace(bukkit, "plugins (5):", "plugins (6):", 1), []string{"ViaRewind"}, false},
		{"malformed entry", strings.ReplaceAll(bukkit, "ViaRewind (4.2.0)", "ViaRewind"), []string{"ViaRewind"}, false},
		{"nested version comma", strings.ReplaceAll(bukkit, "ViaBackwards (5.12.1)", "ViaBackwards (5.12.1 (build 1, commit abc))"), []string{"ViaRewind"}, true},
		{"outer version comma", strings.ReplaceAll(bukkit, "ViaBackwards (5.12.1)", "ViaBackwards (5.12.1, build 1)"), []string{"ViaRewind"}, true},
		{"missing nested close", strings.ReplaceAll(bukkit, "(b141-81b65cc))", "(b141-81b65cc)"), []string{"ViaRewind"}, false},
		{"extra nested close", strings.ReplaceAll(bukkit, "(b141-81b65cc))", "(b141-81b65cc)))"), []string{"ViaRewind"}, false},
		{"reversed nested parentheses", strings.ReplaceAll(bukkit, "(b141-81b65cc))", ")b141-81b65cc()"), []string{"ViaRewind"}, false},
		{"trailing version group", strings.ReplaceAll(bukkit, "(b141-81b65cc))", "(b141-81b65cc)) (extra)"), []string{"ViaRewind"}, false},
		{"empty version", strings.ReplaceAll(bukkit, "ViaRewind (4.2.0)", "ViaRewind ()"), []string{"ViaRewind"}, false},
		{"oversized nested version", strings.ReplaceAll(bukkit, "(b141-81b65cc)", "("+strings.Repeat("x", 256)+")"), []string{"ViaRewind"}, false},
		{"unsafe inventory name", strings.ReplaceAll(bukkit, "ViaRewind (4.2.0)", "../ViaRewind (4.2.0)"), []string{"ViaRewind"}, false},
		{"inventory name prefix", strings.ReplaceAll(bukkit, "ViaRewind (4.2.0)", "ViaRewindExtra (4.2.0)"), []string{"ViaRewind"}, false},
		{"duplicate inventory", strings.ReplaceAll(bukkit, "ViaBackwards (5.12.1)", "ViaRewind (4.2.0)"), []string{"ViaRewind"}, false},
		{"duplicate enable", bukkit + "[ViaRewind] Enabling ViaRewind v4.2.0\n", []string{"ViaRewind"}, false},
		{"duplicate group", bukkit + "[PluginInitializerManager] Bukkit plugins (0):\n", []string{"ViaRewind"}, false},
		{"multiline inventory", strings.ReplaceAll(bukkit, ", ViaRewind", "\n - ViaRewind"), []string{"ViaRewind"}, true},
		{"unrelated plugin list", bukkit + "[Other] Features:\n - unrelated feature\n", []string{"ViaRewind"}, true},
		{"error word in identity", strings.ReplaceAll(pluginFixtureStartup, "Root", "ErrorPlugin"), []string{"ErrorPlugin"}, true},
		{"all artifacts", pluginFixtureStartup, []string{"Root", "Dependency"}, true},
		{"missing dependency enable", strings.ReplaceAll(pluginFixtureStartup, "[Dependency] Enabling Dependency v1.0\n", ""), []string{"Root", "Dependency"}, false},
		{"missing dependency inventory", strings.ReplaceAll(pluginFixtureStartup, "Dependency (1.0)", "Other (1.0)"), []string{"Root", "Dependency"}, false},
		{"unsafe expected", bukkit, []string{"../ViaRewind"}, false},
		{"duplicate expected", bukkit, []string{"ViaRewind", "ViaRewind"}, false},
		{"no expected", bukkit, nil, false},
		{"oversized line", bukkit + strings.Repeat("x", pluginStartupLineLimit) + "\n", []string{"ViaRewind"}, false},
		{"oversized log", bukkit + strings.Repeat("x", pluginStartupLogLimit), []string{"ViaRewind"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pluginStartupNamesLoaded([]byte(tc.raw), tc.expected); got != tc.ok {
				t.Fatalf("loaded=%v, want %v", got, tc.ok)
			}
		})
	}
}

func TestPluginStartupSnapshotStable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(initial, current, opened *unix.Stat_t, snapshotSize *int64)
		ok     bool
	}{
		{"unchanged regular file", func(_, _, _ *unix.Stat_t, _ *int64) {}, true},
		{"append after snapshot", func(_, current, opened *unix.Stat_t, _ *int64) { current.Size++; opened.Size += 2 }, true},
		{"append during snapshot", func(_, current, opened *unix.Stat_t, snapshotSize *int64) {
			*snapshotSize++
			current.Size++
			opened.Size++
		}, true},
		{"opened truncated", func(_, _, opened *unix.Stat_t, _ *int64) { opened.Size-- }, false},
		{"path truncated", func(_, current, _ *unix.Stat_t, _ *int64) { current.Size-- }, false},
		{"truncated after growth", func(_, current, opened *unix.Stat_t, _ *int64) { current.Size += 2; opened.Size++ }, false},
		{"truncated during read", func(initial, _, _ *unix.Stat_t, _ *int64) { initial.Size++ }, false},
		{"changed path device", func(_, current, _ *unix.Stat_t, _ *int64) { current.Dev++ }, false},
		{"changed path inode", func(_, current, _ *unix.Stat_t, _ *int64) { current.Ino++ }, false},
		{"changed opened device", func(_, _, opened *unix.Stat_t, _ *int64) { opened.Dev++ }, false},
		{"changed opened inode", func(_, _, opened *unix.Stat_t, _ *int64) { opened.Ino++ }, false},
		{"both replaced", func(_, current, opened *unix.Stat_t, _ *int64) { current.Ino++; opened.Ino++ }, false},
		{"path symlink", func(_, current, _ *unix.Stat_t, _ *int64) { current.Mode = unix.S_IFLNK }, false},
		{"opened nonregular", func(_, _, opened *unix.Stat_t, _ *int64) { opened.Mode = unix.S_IFIFO }, false},
		{"oversized path", func(_, current, _ *unix.Stat_t, _ *int64) { current.Size = pluginStartupLogLimit + 1 }, false},
		{"oversized opened", func(_, _, opened *unix.Stat_t, _ *int64) { opened.Size = pluginStartupLogLimit + 1 }, false},
		{"oversized snapshot", func(_, _, _ *unix.Stat_t, snapshotSize *int64) { *snapshotSize = pluginStartupLogLimit + 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := unix.Stat_t{Dev: 1, Ino: 2, Mode: unix.S_IFREG | 0600, Size: 100}
			current, opened, snapshotSize := initial, initial, initial.Size
			tc.mutate(&initial, &current, &opened, &snapshotSize)
			if got := pluginStartupSnapshotStable(initial, current, opened, snapshotSize); got != tc.ok {
				t.Fatalf("stable=%v, want %v", got, tc.ok)
			}
		})
	}
}

func TestVerifyPluginStartupFilesystem(t *testing.T) {
	for _, kind := range []string{"regular", "missing", "symlink file", "symlink logs", "symlink data", "directory", "fifo", "oversized", "historical only"} {
		t.Run(kind, func(t *testing.T) {
			data := t.TempDir()
			writePluginStartup(t, data, pluginFixtureStartup)
			path := filepath.Join(data, "logs", "latest.log")
			switch kind {
			case "missing", "historical only":
				if err := os.Rename(path, filepath.Join(data, "logs", "old.log")); err != nil {
					t.Fatal(err)
				}
			case "symlink file":
				target := filepath.Join(t.TempDir(), "log")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "symlink logs":
				target := filepath.Join(t.TempDir(), "logs")
				if err := os.Rename(filepath.Join(data, "logs"), target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(data, "logs")); err != nil {
					t.Fatal(err)
				}
			case "symlink data":
				link := filepath.Join(t.TempDir(), "data")
				if err := os.Symlink(data, link); err != nil {
					t.Fatal(err)
				}
				data = link
			case "directory", "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Truncate(path, pluginStartupLogLimit+1); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyPluginStartup(data, []string{"Root"}); (err == nil) != (kind == "regular") {
				t.Fatalf("unexpected proof: %v", err)
			}
		})
	}
}

func TestPluginInstallUsesLatestStartupProof(t *testing.T) {
	for _, kind := range []string{"normal", "normal missing log", "recheck", "recheck missing log", "recheck stale startup"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginInstallFixture(t)
			original := pluginInstallCommand
			pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				raw, err := original(ctx, command, args...)
				if command == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" {
					if strings.Contains(kind, "missing log") {
						_ = os.Remove(filepath.Join(f.data, "logs", "latest.log"))
					}
					if strings.Contains(kind, "stale startup") {
						writePluginStartup(t, f.data, pluginFixtureStartup+"[PluginInitializerManager] Initializing plugins...\n[PluginInitializerManager] Bukkit plugins (0):\n")
					}
				}
				return raw, err
			}
			if strings.HasPrefix(kind, "normal") {
				journal := f.execute(t, f.plan(t), false)
				want := operationSucceeded
				if strings.Contains(kind, "missing log") {
					want = operationRolledBack
				}
				if journal.State != want {
					t.Fatal(journal.State, journal.Status)
				}
			} else {
				journal, tx := pluginAttentionFixture(t, f)
				if err := os.WriteFile(filepath.Join(f.data, "plugins", "root.jar"), f.jars["root.jar"], 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := commitPluginManifest(f.data, nil, pluginManifest{SchemaVersion: "v1", Artifacts: tx.Artifacts}); err != nil {
					t.Fatal(err)
				}
				rr := httptest.NewRecorder()
				f.s.adminPluginInstallAcknowledge(rr, surfaceRequest(http.MethodPost, "/", `{"operation_id":"`+journal.OperationID+`"}`))
				want := http.StatusConflict
				if kind == "recheck" {
					want = http.StatusOK
				}
				if rr.Code != want {
					t.Fatal(rr.Code, rr.Body.String())
				}
			}
			readiness, stack := false, false
			for _, command := range f.commands {
				if strings.HasSuffix(command, "rcon-cli plugins") {
					t.Fatal("RCON plugins command used")
				}
				readiness = readiness || strings.HasSuffix(command, "rcon-cli list")
				stack = stack || strings.Contains(command, "verify-minecraft-stack")
			}
			if !readiness || !stack {
				t.Fatal("runtime readiness or stack verifier removed")
			}
		})
	}
}

func TestStagedPluginLoaderClassificationPreservesDescriptorValidation(t *testing.T) {
	for _, metadata := range []string{"fabric.mod.json", "quilt.mod.json", "META-INF/mods.toml", "META-INF/neoforge.mods.toml", "META-INF/sponge_plugins.json"} {
		for _, descriptor := range []string{"absent", "valid", "valid Paper", "empty", "malformed", "valid plus malformed"} {
			t.Run(metadata+"/"+descriptor, func(t *testing.T) {
				entries := map[string]string{metadata: "not parsed or executed"}
				switch descriptor {
				case "valid":
					entries["plugin.yml"] = "name: Example\nversion: 1\nmain: example.Main\n"
				case "valid Paper":
					entries["paper-plugin.yml"] = "name: Example\nmain: example.Main\n"
				case "empty":
					entries["paper-plugin.yml"] = ""
				case "malformed":
					entries["plugin.yml"] = "name: Example\nmain: invalid main class\n"
				case "valid plus malformed":
					entries["paper-plugin.yml"] = ""
					entries["plugin.yml"] = "name: Example\nmain: example.Main\n"
				}
				path := filepath.Join(t.TempDir(), "artifact.jar")
				pluginTestJar(t, path, entries)
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				identity, err := inspectStagedPlugin(f, "artifact.jar")
				if descriptor == "valid" || descriptor == "valid Paper" {
					if err != nil || identity.Name != "Example" {
						t.Fatalf("direct plugin lost authority: %#v %v", identity, err)
					}
				} else if descriptor == "valid plus malformed" {
					if err == nil || errors.Is(err, errPluginLoaderArtifact) {
						t.Fatalf("loader classification bypassed descriptor validation: %v", err)
					}
				} else if !errors.Is(err, errPluginLoaderArtifact) {
					t.Fatalf("loader evidence not classified: %v", err)
				}
			})
		}
	}
	for _, entry := range []string{"random.txt", "nested/fabric.mod.json", "FABRIC.MOD.JSON"} {
		path := filepath.Join(t.TempDir(), "random.jar")
		pluginTestJar(t, path, map[string]string{entry: "unknown"})
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = inspectStagedPlugin(f, "random.jar")
		f.Close()
		if err == nil || errors.Is(err, errPluginLoaderArtifact) || err.Error() != "JAR has no Paper/Bukkit plugin descriptor" {
			t.Fatalf("unknown JAR classification changed: %v", err)
		}
	}
}

func TestPluginLoaderRejectionStableWording(t *testing.T) {
	if pluginLoaderReviewRejection != "Loader-based server mod - not supported by the current version of JustVoxel Plugin installer. Should be available in future updates." {
		t.Fatal("loader rejection wording changed")
	}
}
