package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addonTestFile(t *testing.T, raw []byte) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "upload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(raw); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
func addonTestJar(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, content := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, content)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func addonTestHost(t *testing.T, data string, d addonDefinition) {
	t.Helper()
	os.MkdirAll(filepath.Join(data, "plugins", d.Host), 0755)
	name, main := d.Host, "example."+d.Host
	if d.Kind == "geyser_extension" {
		name = "Geyser-Spigot"
		main = "org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin"
	}
	pluginTestJar(t, filepath.Join(data, "plugins", d.Host+".jar"), map[string]string{"plugin.yml": "name: " + name + "\nversion: 1\nmain: " + main + "\n"})
}
func addonTestContext() pluginServerContext {
	return pluginServerContext{InstanceID: "jv-a1b2c3", Configured: true, ServerType: "paper", ServerSoftware: "Paper", MinecraftVersion: "1.21.8", Supported: true}
}
func TestAddonHostsAndAuthoritativeTargets(t *testing.T) {
	for _, d := range addonDefinitions {
		t.Run(d.Kind, func(t *testing.T) {
			data := t.TempDir()
			os.MkdirAll(filepath.Join(data, "plugins", d.Host), 0755)
			inv, err := readPluginInventory(data)
			if err != nil {
				t.Fatal(err)
			}
			h, _, err := resolveAddonHost(data, d, inv)
			if err != nil || h.Available {
				t.Fatal("directory inferred host", h, err)
			}
			addonTestHost(t, data, d)
			inv, err = readPluginInventory(data)
			if err != nil {
				t.Fatal(err)
			}
			h, dest, err := resolveAddonHost(data, d, inv)
			if err != nil || !h.Available || dest != "plugins/"+d.Host+"/"+d.Directory {
				t.Fatal(h, dest, err)
			}
			inv[0].State = "disabled"
			h, _, err = resolveAddonHost(data, d, inv)
			if err != nil || h.Available || h.Status != "disabled" {
				t.Fatal("disabled host", h, err)
			}
			inv[0].State = ""
			os.Remove(filepath.Join(data, "plugins", d.Host))
			os.Symlink(t.TempDir(), filepath.Join(data, "plugins", d.Host))
			h, _, err = resolveAddonHost(data, d, inv)
			if h.Available {
				t.Fatal("symlinked host accepted", err)
			}
		})
	}
}
func TestAddonWorldEditSaveDir(t *testing.T) {
	for _, test := range []struct {
		raw, want string
		bad       bool
	}{{"", "schematics", false}, {"other: true\n", "schematics", false}, {"saveDir: custom/maps\n", "custom/maps", false}, {"saveDir: 'custom/maps' # fine\n", "custom/maps", false}, {"saveDir: \"schematics\"\n", "schematics", false}, {"saveDir: /tmp\n", "", true}, {"saveDir: ../maps\n", "", true}, {"saveDir: a/../maps\n", "", true}, {"saveDir: \n", "", true}, {"saveDir: a\nsaveDir: b\n", "", true}, {"saveDir: a\x00\n", "", true}, {"saveDir: C:\\maps\n", "", true}, {"saveDir: \"a\" garbage\n", "", true}} {
		got, err := worldEditSaveDir([]byte(test.raw))
		if (err != nil) != test.bad || !test.bad && got != test.want {
			t.Fatalf("%q: %q %v", test.raw, got, err)
		}
	}
	data := t.TempDir()
	d, _ := addonDefinitionFor("worldedit_schematic")
	addonTestHost(t, data, d)
	os.WriteFile(filepath.Join(data, "plugins/WorldEdit/config.yml"), []byte("saveDir: custom/maps\n"), 0644)
	inv, _ := readPluginInventory(data)
	h, dest, err := resolveAddonHost(data, d, inv)
	if err != nil || !h.Available || dest != "plugins/WorldEdit/custom/maps" {
		t.Fatal(h, dest, err)
	}
	os.Symlink(t.TempDir(), filepath.Join(data, "plugins/WorldEdit/custom"))
	if _, _, err = resolveAddonHost(data, d, inv); err == nil {
		t.Fatal("symlink target parent accepted")
	}
}
func TestAddonValidators(t *testing.T) {
	jar := func(entries map[string]string) []byte { return addonTestJar(t, entries) }
	cases := []struct {
		kind, name string
		raw        []byte
		bad        bool
	}{
		{"placeholderapi_expansion", "exp.jar", jar(map[string]string{"Example.class": "\xca\xfe\xba\xbe\x00\x00\x00\x34"}), false},
		{"placeholderapi_expansion", "empty.jar", jar(map[string]string{"README": "hi"}), true},
		{"placeholderapi_expansion", "bad.jar", []byte("bad"), true},
		{"geyser_extension", "ext.jar", jar(map[string]string{"extension.yml": "id: test\nname: Test\nmain: example.Extension\napi: 2.0.0\nversion: 1.0\n"}), false},
		{"geyser_extension", "ext.jar", jar(map[string]string{"Example.class": "bytes"}), true},
		{"geyser_extension", "ext.jar", jar(map[string]string{"extension.yml": "id: []\nname: Test\nmain: example.Main\napi: 2\nversion: 1\n"}), true},
		{"geyser_extension", "ext.jar", jar(map[string]string{"extension.yml": "id: test\nname: Test\nmain: example.Main\napi: 2\nversion: 1\nid: other\n"}), true},
		{"bluemap_addon", "map.jar", jar(map[string]string{"bluemap.addon.json": `{"id":"test","entrypoint":"example.Main","dependencies":["base"]}`}), false},
		{"bluemap_addon", "map.jar", jar(map[string]string{"Example.class": "bytes"}), true},
		{"bluemap_addon", "map.jar", jar(map[string]string{"bluemap.addon.json": `{"id":"test"}`}), true},
		{"bluemap_addon", "map.jar", jar(map[string]string{"bluemap.addon.json": `{"id":"test","entrypoint":[]}`}), true},
		{"skript_script", "script.sk", []byte("on join:\n  send \"Hi\"\n"), false},
		{"skript_script", "script.sk", []byte("\x00text"), true},
		{"skript_script", "script.sk", []byte{0xff, 0xfe}, true},
		{"skript_script", "script.txt", []byte("text"), true},
		{"skript_script", "script.sk", []byte(" \n\t"), true},
		{"worldedit_schematic", "castle.schem", []byte{1, 2, 3}, false},
		{"worldedit_schematic", "castle.schematic", []byte{1}, false},
		{"worldedit_schematic", "castle.zip", []byte{1}, true},
		{"worldedit_schematic", "castle.schem", nil, true},
	}
	for _, wrong := range []string{"plugin.yml", "paper-plugin.yml", "velocity-plugin.json", "extension.yml", "bluemap.addon.json"} {
		cases = append(cases, struct {
			kind, name string
			raw        []byte
			bad        bool
		}{"placeholderapi_expansion", "wrong.jar", jar(map[string]string{"Example.class": "\xca\xfe\xba\xbe\x00\x00\x00\x34", wrong: "{}"}), true})
	}
	for _, test := range cases {
		t.Run(test.kind+"/"+test.name, func(t *testing.T) {
			_, _, _, err := validateAddon(addonTestFile(t, test.raw), test.kind, test.name)
			if (err != nil) != test.bad {
				t.Fatal("validation", err)
			}
		})
	}
	for _, name := range []string{"", ".", "..", "../x.sk", "x/y.sk", "x\\y.sk", ".hidden.sk", "x\n.sk", "x\x7f.sk", strings.Repeat("a", 181) + ".sk"} {
		if safeAddonFilename(name) {
			t.Fatal("unsafe filename", name)
		}
	}
}
func TestAddonFingerprintCollisionAndOwnership(t *testing.T) {
	data := t.TempDir()
	d, _ := addonDefinitionFor("worldedit_schematic")
	addonTestHost(t, data, d)
	file := addonTestFile(t, []byte("schematic"))
	u := addonUpload{Kind: d.Kind, Filename: "castle.schem", File: file}
	p, err := resolveAddonPlan(addonTestContext(), data, u)
	if err != nil {
		t.Fatal(err)
	}
	same, err := resolveAddonPlan(addonTestContext(), data, u)
	if err != nil || same.Fingerprint != p.Fingerprint || p.RestartRequired {
		t.Fatal("plan unstable", err)
	}
	file.WriteAt([]byte("X"), 0)
	changed, err := resolveAddonPlan(addonTestContext(), data, u)
	if err != nil || changed.Fingerprint == p.Fingerprint {
		t.Fatal("bytes not bound", err)
	}
	file.WriteAt([]byte("s"), 0)
	os.WriteFile(filepath.Join(data, "plugins/WorldEdit/config.yml"), []byte("saveDir: custom\n"), 0644)
	changed, err = resolveAddonPlan(addonTestContext(), data, u)
	if err != nil || changed.Fingerprint == p.Fingerprint {
		t.Fatal("saveDir not bound", err)
	}
	os.Remove(filepath.Join(data, "plugins/WorldEdit/config.yml"))
	pluginTestJar(t, filepath.Join(data, "plugins/WorldEdit.jar"), map[string]string{"plugin.yml": "name: WorldEdit\nversion: 2\nmain: example.WorldEdit\n"})
	changed, err = resolveAddonPlan(addonTestContext(), data, u)
	if err != nil || changed.Fingerprint == p.Fingerprint {
		t.Fatal("host not bound", err)
	}
	root, err := openAddonDirectory(data, p.Destination, true)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	os.WriteFile(filepath.Join(data, p.Target), []byte("schematic"), 0644)
	if _, err = resolveAddonPlan(addonTestContext(), data, u); err == nil {
		t.Fatal("unmanaged collision accepted")
	}
	oldPluginManifest := []byte(`{"schema_version":"v1","artifacts":[]}`)
	os.Mkdir(filepath.Join(data, ".justvoxel"), 0700)
	os.WriteFile(filepath.Join(data, ".justvoxel/plugins.json"), oldPluginManifest, 0600)
	m := addonManifest{"v1", []addonEntry{p.addonEntry}}
	if _, err = commitAddonManifest(data, nil, m); err != nil {
		t.Fatal(err)
	}
	other, _ := os.ReadFile(filepath.Join(data, ".justvoxel/plugins.json"))
	if !bytes.Equal(other, oldPluginManifest) {
		t.Fatal("Plugins manifest changed")
	}
	if err = verifyAddonEntry(data, p.addonEntry); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(data, p.Target), []byte("drift"), 0644)
	if err = verifyAddonEntry(data, p.addonEntry); err == nil {
		t.Fatal("drift trusted")
	}
	if _, err = resolveAddonPlan(addonTestContext(), data, u); err == nil || !strings.Contains(err.Error(), "attention") {
		t.Fatal("drift overwrite", err)
	}
	os.Remove(filepath.Join(data, p.Target))
	if err = verifyAddonEntry(data, p.addonEntry); err == nil {
		t.Fatal("missing trusted")
	}
	m.SchemaVersion = "v2"
	if validateAddonManifest(m) == nil {
		t.Fatal("new schema accepted")
	}
	m.SchemaVersion = "v1"
	m.Artifacts[0].Target = "/absolute"
	if validateAddonManifest(m) == nil {
		t.Fatal("absolute manifest accepted")
	}
}
func addonMultipartRequest(t *testing.T, fields [][2]string, filename string, raw []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range fields {
		writer.WriteField(field[0], field[1])
	}
	if filename != "" {
		part, _ := writer.CreateFormFile("file", filename)
		part.Write(raw)
	}
	writer.Close()
	r := httptest.NewRequest("POST", "/", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r
}
func TestAddonMultipartStrictContract(t *testing.T) {
	for _, fields := range [][][2]string{{{"type", "skript_script"}, {"path", "plugins"}}, {{"type", "skript_script"}, {"url", "https://example.test"}}, {{"type", "skript_script"}, {"type", "skript_script"}}, {{"type", "unsupported"}}, {{"type", strings.Repeat("x", 513)}}} {
		u, err := parseAddonUpload(httptest.NewRecorder(), addonMultipartRequest(t, fields, "script.sk", []byte("text")), false)
		if err == nil {
			u.close()
			t.Fatal("invalid field accepted", fields)
		}
	}
	for _, name := range []string{"../script.sk", "a\\b.sk"} {
		if u, err := parseAddonUpload(httptest.NewRecorder(), addonMultipartRequest(t, [][2]string{{"type", "skript_script"}}, name, []byte("text")), false); err == nil {
			u.close()
			t.Fatal("path basename normalization accepted")
		}
	}
	u, err := parseAddonUpload(httptest.NewRecorder(), addonMultipartRequest(t, [][2]string{{"type", "skript_script"}}, "script.sk", []byte("text")), false)
	if err != nil {
		t.Fatal(err)
	}
	path := u.File.Name()
	u.close()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temporary upload retained")
	}
	// Stream a large body without allocating the upload limit in test memory.
	var prefix bytes.Buffer
	writer := multipart.NewWriter(&prefix)
	writer.WriteField("type", "worldedit_schematic")
	writer.CreateFormFile("file", "large.schem")
	r := httptest.NewRequest("POST", "/", io.MultiReader(&prefix, io.LimitReader(addonZeroReader{}, pluginMaxJar+1)))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if u, err := parseAddonUpload(httptest.NewRecorder(), r, false); err == nil {
		u.close()
		t.Fatal("oversized artifact accepted")
	}
}

type addonZeroReader struct{}

func (addonZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestAddonOperationMaintenanceAndRollback(t *testing.T) {
	for _, kind := range []string{"skript_script", "worldedit_schematic"} {
		for _, failBackup := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/backup_failure=%t", kind, failBackup), func(t *testing.T) {
				fixture := newPluginInstallFixture(t)
				d, _ := addonDefinitionFor(kind)
				addonTestHost(t, fixture.data, d)
				filename := "file.sk"
				if kind == "worldedit_schematic" {
					filename = "file.schem"
				}
				file := addonTestFile(t, []byte("content"))
				plan, err := resolveAddonPlan(addonTestContext(), fixture.data, addonUpload{Kind: kind, Filename: filename, File: file})
				if err != nil {
					t.Fatal(err)
				}
				fixture.failBackup = failBackup
				journal, created, err := fixture.s.operations.beginMinecraftMaintenance(plan.Fingerprint, operationTypePluginAddonInstall, "jv-a1b2c3")
				if err != nil || !created {
					t.Fatal(err)
				}
				executeAddonInstall(fixture.s, journal.OperationID, plan, file, false, session{})
				result, err := fixture.s.operations.get(journal.OperationID)
				if err != nil {
					t.Fatal(err)
				}
				want := operationSucceeded
				if failBackup && kind != "worldedit_schematic" {
					want = operationRolledBack
				}
				if result.State != want {
					t.Fatal(result.State, result.Status)
				}
				if kind == "worldedit_schematic" {
					if len(fixture.commands) != 0 || fixture.backup {
						t.Fatal("passive schematic changed runtime", fixture.commands)
					}
				} else if !failBackup && !fixture.backup {
					t.Fatal("executable omitted backup")
				}
				if want == operationSucceeded {
					if err = verifyAddonEntry(fixture.data, plan.addonEntry); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err = os.Stat(filepath.Join(fixture.data, plan.Target)); !os.IsNotExist(err) {
						t.Fatal("failed target retained")
					}
				}
				active, _ := fixture.s.operations.currentPluginOperation()
				if active != nil {
					t.Fatal("terminal operation retained exclusion")
				}
			})
		}
	}
}
func TestAddonExecutablePlayerGateAndStoppedState(t *testing.T) {
	for _, players := range []int{0, 1} {
		fixture := newPluginInstallFixture(t)
		fixture.active = false
		fixture.players = players
		d, _ := addonDefinitionFor("skript_script")
		addonTestHost(t, fixture.data, d)
		file := addonTestFile(t, []byte("on join: stop"))
		p, err := resolveAddonPlan(addonTestContext(), fixture.data, addonUpload{Kind: d.Kind, Filename: "test.sk", File: file})
		if err != nil {
			t.Fatal(err)
		}
		journal, _, err := fixture.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginAddonInstall, "jv-a1b2c3")
		if err != nil {
			t.Fatal(err)
		}
		executeAddonInstall(fixture.s, journal.OperationID, p, file, false, session{})
		result, _ := fixture.s.operations.get(journal.OperationID)
		if players > 0 {
			if result.State != operationRolledBack || fixture.backup {
				t.Fatal("player gate bypassed", result)
			}
		} else if result.State != operationSucceeded || fixture.active {
			t.Fatal("original stopped state lost", result)
		}
	}
}
func TestAddonRollbackRefusesChangedFile(t *testing.T) {
	fixture := newPluginInstallFixture(t)
	d, _ := addonDefinitionFor("worldedit_schematic")
	addonTestHost(t, fixture.data, d)
	file := addonTestFile(t, []byte("content"))
	p, err := resolveAddonPlan(addonTestContext(), fixture.data, addonUpload{Kind: d.Kind, Filename: "file.schem", File: file})
	if err != nil {
		t.Fatal(err)
	}
	j, _, _ := fixture.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginAddonInstall, "jv-a1b2c3")
	executeAddonInstall(fixture.s, j.OperationID, p, file, false, session{})
	tx, err := readAddonTransaction(fixture.s, j.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	// Original manifest comparison also fails closed after a completed commit.
	if err = rollbackAddon(context.Background(), tx); err == nil {
		t.Fatal("committed ownership removed by rollback")
	}
	os.WriteFile(filepath.Join(fixture.data, p.Target), []byte("changed"), 0644)
	if err = verifyAddonEntry(fixture.data, p.addonEntry); err == nil {
		t.Fatal("changed file accepted")
	}
}
func TestAddonManifestStrictOwnership(t *testing.T) {
	data := t.TempDir()
	os.Mkdir(filepath.Join(data, ".justvoxel"), 0700)
	h := sha512.Sum512([]byte("x"))
	entry := addonEntry{Kind: "skript_script", Filename: "test.sk", Target: "plugins/Skript/scripts/test.sk", SHA512: hex.EncodeToString(h[:])}
	for _, raw := range []string{`{"schema_version":"v2","artifacts":[]}`, `{"schema_version":"v1","artifacts":[],"unknown":true}`, `{"schema_version":"v1","artifacts":[]} {}`} {
		os.WriteFile(filepath.Join(data, ".justvoxel", addonManifestName), []byte(raw), 0600)
		if _, _, err := loadAddonManifest(data); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	raw, _ := json.Marshal(addonManifest{"v1", []addonEntry{entry}})
	os.WriteFile(filepath.Join(data, ".justvoxel", addonManifestName), raw, 0600)
	if _, _, err := loadAddonManifest(data); err != nil {
		t.Fatal(err)
	}
	entry.Target = "plugins/BlueMap/packs/test.sk"
	if validateAddonManifest(addonManifest{"v1", []addonEntry{entry}}) == nil {
		t.Fatal("kind destination mismatch")
	}
}

func TestAddonEveryExecutableTypeRequiresRestart(t *testing.T) {
	for _, test := range []struct {
		kind, filename string
		entries        map[string]string
		raw            []byte
	}{
		{"placeholderapi_expansion", "exp.jar", map[string]string{"Expansion.class": "\xca\xfe\xba\xbe\x00\x00\x00\x34"}, nil},
		{"geyser_extension", "ext.jar", map[string]string{"extension.yml": "id: test\nname: Test\nmain: example.Main\napi: 2.0.0\nversion: 1.0\n"}, nil},
		{"bluemap_addon", "native.jar", map[string]string{"bluemap.addon.json": `{"id":"test","entrypoint":"example.Main"}`}, nil},
		{"skript_script", "test.sk", nil, []byte("on join: stop")},
	} {
		t.Run(test.kind, func(t *testing.T) {
			data := t.TempDir()
			d, _ := addonDefinitionFor(test.kind)
			addonTestHost(t, data, d)
			raw := test.raw
			if test.entries != nil {
				raw = addonTestJar(t, test.entries)
			}
			p, err := resolveAddonPlan(addonTestContext(), data, addonUpload{Kind: test.kind, Filename: test.filename, File: addonTestFile(t, raw)})
			if err != nil || !p.RestartRequired {
				t.Fatal(p, err)
			}
		})
	}
}
func TestAddonSafeParentAndDataRoot(t *testing.T) {
	data := t.TempDir()
	d, _ := addonDefinitionFor("skript_script")
	addonTestHost(t, data, d)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(data, "plugins/Skript/scripts"))
	u := addonUpload{Kind: d.Kind, Filename: "test.sk", File: addonTestFile(t, []byte("on join: stop"))}
	if _, err := resolveAddonPlan(addonTestContext(), data, u); err == nil {
		t.Fatal("symlink parent accepted")
	}
	linked := filepath.Join(t.TempDir(), "data")
	os.Symlink(data, linked)
	if _, err := readAddonHostInventory(linked); err == nil {
		t.Fatal("symlink data root followed")
	}
}
func TestAddonRollbackAfterRuntimeFailureAndDiagnostics(t *testing.T) {
	fixture := newPluginInstallFixture(t)
	d, _ := addonDefinitionFor("skript_script")
	addonTestHost(t, fixture.data, d)
	file := addonTestFile(t, []byte("on join: stop"))
	p, err := resolveAddonPlan(addonTestContext(), fixture.data, addonUpload{Kind: d.Kind, Filename: "test.sk", File: file})
	if err != nil {
		t.Fatal(err)
	}
	originalCommand := pluginInstallCommand
	pluginInstallCommand = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
		if cmd == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" {
			if _, e := os.Stat(filepath.Join(fixture.data, p.Target)); e == nil {
				return nil, io.ErrUnexpectedEOF
			}
		}
		return originalCommand(ctx, cmd, args...)
	}
	j, _, err := fixture.s.operations.beginMinecraftMaintenance(p.Fingerprint, operationTypePluginAddonInstall, "jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	executeAddonInstall(fixture.s, j.OperationID, p, file, false, session{})
	result, _ := fixture.s.operations.get(j.OperationID)
	if result.State != operationRolledBack || !fixture.active {
		t.Fatal("runtime rollback", result)
	}
	if _, err = os.Stat(filepath.Join(fixture.data, p.Target)); !os.IsNotExist(err) {
		t.Fatal("new executable retained")
	}
	if diagnosticCategoryForOperationType(operationTypePluginAddonInstall) != "plugins" || !validDiagnosticOperationType(operationTypePluginAddonInstall) {
		t.Fatal("missing Plugins diagnostic evidence")
	}
}

func TestAddonApplyRejectsChangedReviewedFileAndHost(t *testing.T) {
	fixture := newPluginInstallFixture(t)
	d, _ := addonDefinitionFor("worldedit_schematic")
	addonTestHost(t, fixture.data, d)
	file := addonTestFile(t, []byte("content"))
	p, err := resolveAddonPlan(addonTestContext(), fixture.data, addonUpload{Kind: d.Kind, Filename: "file.schem", File: file})
	if err != nil {
		t.Fatal(err)
	}
	fields := [][2]string{{"type", d.Kind}, {"plan_fingerprint", p.Fingerprint}, {"confirm_players", "false"}}
	request := addonMultipartRequest(t, fields, "file.schem", []byte("changed"))
	request.SetPathValue("id", "jv-a1b2c3")
	request = request.WithContext(withPluginInstance(request.Context(), "jv-a1b2c3"))
	request.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()
	fixture.s.adminAddonInstall(rr, request)
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), "review again") {
		t.Fatal("changed bytes applied", rr.Code, rr.Body.String())
	}
	os.WriteFile(filepath.Join(fixture.data, "plugins/WorldEdit/config.yml"), []byte("saveDir: other\n"), 0644)
	request = addonMultipartRequest(t, fields, "file.schem", []byte("content"))
	request.SetPathValue("id", "jv-a1b2c3")
	request = request.WithContext(withPluginInstance(request.Context(), "jv-a1b2c3"))
	request.Header.Set("Authorization", "Bearer token")
	rr = httptest.NewRecorder()
	fixture.s.adminAddonInstall(rr, request)
	if rr.Code != 409 {
		t.Fatal("changed destination applied", rr.Code, rr.Body.String())
	}
	if current, _ := fixture.s.operations.currentPluginOperation(); current != nil {
		t.Fatal("rejected apply started maintenance")
	}
	if len(fixture.commands) != 0 {
		t.Fatal("rejected upload changed runtime")
	}
}
