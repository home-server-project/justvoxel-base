package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const validMinecraftResetPlanHelper = `{"ok":true,"schema_version":"v1","mode":"minecraft","data_path":"/var/lib/justvoxel/minecraft","data_scope":"internal","data_action":"delete","backup_path":"/var/lib/justvoxel/backups","backup_action":"preserve","storage_layout_action":"preserve","authentication_action":"preserve","webui_users_action":"preserve","players_online":0,"players":[],"warnings":[]}`

func exactMinecraftResetFingerprint(t *testing.T) string {
	t.Helper()
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := minecraftResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func TestPreservedResetBlocksUnsafeManagedPluginEvidence(t *testing.T) {
	for _, kind := range []string{"hash", "descriptor", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginManageFixture(t, "disabled")
			switch kind {
			case "hash":
				if err := os.WriteFile(filepath.Join(f.data, ".justvoxel", "disabled-plugins", f.entry.Filename), []byte("wrong"), 0644); err != nil {
					t.Fatal(err)
				}
			case "descriptor":
				m, raw, _ := readPluginManifest(f.data)
				m.Artifacts[0].Main = "other.Plugin"
				if _, err := commitPluginManifest(f.data, raw, m); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				if err := os.WriteFile(filepath.Join(f.data, "plugins", f.entry.Filename), f.jars[f.entry.Filename], 0644); err != nil {
					t.Fatal(err)
				}
			}
			plan := adminMinecraftResetPlanResponse{DataPath: f.data, DataAction: "preserve"}
			if err := prepareMinecraftResetPlugins(&plan); err == nil {
				t.Fatal("unsafe cleanup evidence accepted", kind)
			}
			if f.backup || !f.active {
				t.Fatal("planning changed runtime")
			}
		})
	}
}
func TestMinecraftResetFingerprintIncludesPluginsActionAndExactEvidence(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.DataPath = f.data
	plan.DataScope = "external"
	plan.DataAction = "preserve"
	if err := prepareMinecraftResetPlugins(&plan); err != nil {
		t.Fatal(err)
	}
	before, _ := minecraftResetPlanFingerprint(plan)
	plan.PluginsAction = "preserve"
	after, _ := minecraftResetPlanFingerprint(plan)
	if before == after {
		t.Fatal("plugins_action outside fingerprint")
	}
	plan.PluginsAction = "remove_managed_jars"
	plan.PluginEvidence.Entries[0].State = "disabled"
	after, _ = minecraftResetPlanFingerprint(plan)
	if before == after {
		t.Fatal("exact cleanup location outside fingerprint")
	}
}
func TestInternalResetStillOwnsWholeDataDeletion(t *testing.T) {
	data := t.TempDir()
	owned := filepath.Join(data, ".justvoxel")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "plugins.json"), []byte("ownership"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyResetInternalPluginAbsence(data); err == nil {
		t.Fatal("surviving internal ownership ignored")
	}
	// Model the existing helper's whole-tree deletion, which remains authoritative.
	if err := os.RemoveAll(owned); err != nil {
		t.Fatal(err)
	}
	if err := verifyResetInternalPluginAbsence(data); err != nil {
		t.Fatal(err)
	}
	plan := adminMinecraftResetPlanResponse{DataPath: data, DataAction: "delete"}
	if err := prepareMinecraftResetPlugins(&plan); err != nil || plan.PluginsAction != "delete_with_internal_data" || plan.PluginEvidence != nil {
		t.Fatal("internal whole-tree deletion changed", plan, err)
	}
}
func TestResetPluginStagingReplaysIncompleteAtomicMoves(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	plan := adminMinecraftResetPlanResponse{DataPath: f.data, DataAction: "preserve"}
	if err := prepareMinecraftResetPlugins(&plan); err != nil {
		t.Fatal(err)
	}
	id, err := newOperationID()
	if err != nil {
		t.Fatal(err)
	}
	tx := minecraftResetPluginTransaction{SchemaVersion: "v1", OperationID: id, Plan: plan, Plugins: *plan.PluginEvidence}
	_, before, _ := readPluginManifest(f.data)
	if err = stageResetPluginJars(tx); err != nil {
		t.Fatal(err)
	}
	if err = stageResetPluginJars(tx); err != nil {
		t.Fatal("staging retry duplicated JAR", err)
	}
	if err = restoreResetPluginJars(tx); err != nil {
		t.Fatal(err)
	}
	if err = restoreResetPluginJars(tx); err != nil {
		t.Fatal("recovery retry lost JAR", err)
	}
	_, after, _ := readPluginManifest(f.data)
	if !bytes.Equal(before, after) {
		t.Fatal("ownership changed during reset staging recovery")
	}
}

func TestResetPluginTransactionSizeBoundary(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	manifestPath := filepath.Join(f.data, ".justvoxel", "plugins.json")
	_, original, err := readPluginManifest(f.data)
	if err != nil {
		t.Fatal(err)
	}
	// Valid JSON whitespace brings real ownership evidence to its existing bound.
	if pluginManifestLimit != 1<<20 {
		t.Fatalf("original manifest limit changed: %d", pluginManifestLimit)
	}
	original = append(original, bytes.Repeat([]byte(" "), pluginManifestLimit-len(original))...)
	if err := os.WriteFile(manifestPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.InstanceID = "jv-a1b2c3"
	plan.DataPath, plan.DataScope, plan.DataAction = f.data, "external", "preserve"
	if err := prepareMinecraftResetPlugins(&plan); err != nil {
		t.Fatal(err)
	}
	plan.PlanFingerprint, err = minecraftResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newOperationID()
	if err != nil {
		t.Fatal(err)
	}
	tx := &minecraftResetPluginTransaction{"jv-a1b2c3", "JV Paper", "v1", id, plan, *plan.PluginEvidence, false}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= pluginManifestLimit || len(raw) > resetPluginTransactionLimit {
		t.Fatalf("expected transaction between manifest and reset bounds, got %d bytes", len(raw))
	}
	dir, err := f.s.resetPluginEvidenceDirectory(id, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := publishResetPluginTransaction(dir, tx); err != nil {
		t.Fatal(err)
	}
	read, err := f.s.readResetPluginTransaction(id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read.Plugins.Manifest, original) || len(read.Plugins.Entries) != 1 || !reflect.DeepEqual(read.Plugins.Entries[0].Entry, f.entry) {
		t.Fatal("original manifest or exact plugin entry evidence changed")
	}
	if read.Plugins.Entries[0].State != "enabled" || read.Plan.PlanFingerprint != plan.PlanFingerprint {
		t.Fatal("plugin location or fingerprint changed")
	}

	t.Run("oversized writer publishes nothing", func(t *testing.T) {
		oversizedID, err := newOperationID()
		if err != nil {
			t.Fatal(err)
		}
		dir, err := f.s.resetPluginEvidenceDirectory(oversizedID, true)
		if err != nil {
			t.Fatal(err)
		}
		defer dir.Close()
		oversized := *tx
		oversized.OperationID = oversizedID
		oversized.Plan.Warnings = []string{strings.Repeat("x", resetPluginTransactionLimit)}
		if err := publishResetPluginTransaction(dir, &oversized); err == nil || err.Error() != "Transaction evidence too large" {
			t.Fatalf("oversized writer result: %v", err)
		}
		names, err := dir.Readdirnames(-1)
		if err != nil || len(names) != 0 {
			t.Fatalf("oversized writer left evidence: %v, %v", names, err)
		}
	})

	t.Run("reader enforces dedicated bound", func(t *testing.T) {
		path := filepath.Join(f.s.operations.baseDir, "reset-plugins", id, "transaction.json")
		bounded := append(bytes.Clone(raw), bytes.Repeat([]byte(" "), resetPluginTransactionLimit-len(raw))...)
		if err := os.WriteFile(path, bounded, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.readResetPluginTransaction(id); err != nil {
			t.Fatalf("transaction at dedicated bound rejected: %v", err)
		}
		if err := os.WriteFile(path, append(bounded, ' '), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.readResetPluginTransaction(id); err == nil || err.Error() != "Unsafe private plugin evidence" {
			t.Fatalf("oversized reader result: %v", err)
		}
	})

	t.Run("original manifest bound remains enforced", func(t *testing.T) {
		if err := os.WriteFile(manifestPath, append(bytes.Clone(original), ' '), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readPluginManifest(f.data); err == nil {
			t.Fatal("ownership manifest beyond original 1 MiB bound accepted")
		}
		if err := prepareMinecraftResetPlugins(&plan); err == nil {
			t.Fatal("reset accepted oversized original ownership manifest")
		}
	})
}

// These fixtures deliberately share a basename across targets. A plugin remains
// in the deployed flat namespace while its mod peer uses the private child.
func resetModJar(t *testing.T, loader string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	f, err := w.Create(modDescriptor(loader))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := `{"schemaVersion":1,"id":"example","version":"1"}`
	if loader == "quilt" {
		descriptor = `{"schema_version":1,"quilt_loader":{"id":"example","version":"1"}}`
	}
	if loader == "forge" || loader == "neoforge" {
		descriptor = "modLoader=\"javafml\"\nloaderVersion=\"[1,)\"\nlicense=\"MIT\"\n[[mods]]\nmodId=\"example\"\nversion=\"1\"\n"
	}
	if _, err = f.Write([]byte(descriptor)); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func addResetMod(t *testing.T, f *pluginManageFixture) pluginManifestEntry {
	t.Helper()
	if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
		t.Fatal(err)
	}
	jar := resetModJar(t, "fabric")
	sum := sha512.Sum512(jar)
	e := pluginManifestEntry{Target: "mods", ProjectID: "modproject", VersionID: "modrelease", VersionNumber: "1", Filename: f.entry.Filename, SHA512: hex.EncodeToString(sum[:])}
	if err := os.WriteFile(filepath.Join(f.data, "mods", e.Filename), jar, 0644); err != nil {
		t.Fatal(err)
	}
	m, raw, err := readPluginManifest(f.data)
	if err != nil {
		t.Fatal(err)
	}
	m.Artifacts = append(m.Artifacts, e)
	if _, err = commitPluginManifest(f.data, raw, m); err != nil {
		t.Fatal(err)
	}
	return e
}
func resetContentTransaction(t *testing.T, f *pluginManageFixture) minecraftResetPluginTransaction {
	t.Helper()
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.InstanceID = "jv-a1b2c3"
	plan.DataPath = f.data
	plan.DataAction = "preserve"
	plan.DataScope = "external"
	if err := prepareMinecraftResetPlugins(&plan); err != nil {
		t.Fatal(err)
	}
	var err error
	plan.PlanFingerprint, err = minecraftResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newOperationID()
	if err != nil {
		t.Fatal(err)
	}
	return minecraftResetPluginTransaction{InstanceID: plan.InstanceID, InstanceName: "JV Paper", SchemaVersion: "v1", OperationID: id, Plan: plan, Plugins: *plan.PluginEvidence}
}
func TestResetPluginsAndModsSameBasenameRecoveryAndCommit(t *testing.T) {
	for _, state := range []string{"enabled", "disabled"} {
		t.Run(state, func(t *testing.T) {
			f := newPluginManageFixture(t, state)
			mod := addResetMod(t, f)
			for _, target := range []string{"plugins", "mods"} {
				if err := os.WriteFile(filepath.Join(f.data, target, "unmanaged.jar"), []byte("untouched"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			tx := resetContentTransaction(t, f)
			dir, err := f.s.resetPluginEvidenceDirectory(tx.OperationID, true)
			if err != nil {
				t.Fatal(err)
			}
			err = publishResetPluginTransaction(dir, &tx)
			dir.Close()
			if err != nil {
				t.Fatal(err)
			}
			// Model interruption after only the mod rename, then reload durable v1 evidence.
			staging, err := openPluginOperationStaging(f.data, tx.OperationID, true)
			if err != nil {
				t.Fatal(err)
			}
			err = moveResetMod(tx, mod, staging, false)
			staging.Close()
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := f.s.readResetPluginTransaction(tx.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			tx = *recovered
			if err = commitResetPluginCleanup(tx); err == nil {
				t.Fatal("ownership committed with plugin still active")
			}
			_, rawBefore, _ := readPluginManifest(f.data)
			if !bytes.Equal(rawBefore, tx.Plugins.Manifest) {
				t.Fatal("partial staging changed ownership")
			}
			if err = restoreResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if err = stageResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if err = stageResetPluginJars(tx); err != nil {
				t.Fatal("staging replay", err)
			}
			staging, err = openPluginOperationStaging(f.data, tx.OperationID, false)
			if err != nil {
				t.Fatal(err)
			}
			if err = verifyManagedPluginAt(staging, f.entry); err != nil {
				t.Fatal("legacy flat plugin staging", err)
			}
			mods, err := openPrivatePluginChild(staging, "mods", false)
			if err != nil {
				t.Fatal(err)
			}
			if err = verifyManagedArtifactAt(mods, mod); err != nil {
				t.Fatal(err)
			}
			mods.Close()
			staging.Close()
			if err = restoreResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if err = restoreResetPluginJars(tx); err != nil {
				t.Fatal("rollback replay", err)
			}
			if got, err := resolveManagedPluginLocation(f.data, f.entry); err != nil || got != state {
				t.Fatal(got, err)
			}
			if _, err = resolveResetManagedLocation(f.data, mod); err != nil {
				t.Fatal(err)
			}
			if err = commitResetPluginCleanup(tx); err == nil {
				t.Fatal("ownership committed before staging")
			}
			if err = stageResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if err = commitResetPluginCleanup(tx); err != nil {
				t.Fatal(err)
			}
			if err = commitResetPluginCleanup(tx); err != nil {
				t.Fatal("commit replay", err)
			}
			_, raw, err := readPluginManifest(f.data)
			if err != nil || raw != nil {
				t.Fatal("ownership survived", err)
			}
			for _, target := range []string{"plugins", "mods"} {
				if _, err = os.Lstat(filepath.Join(f.data, target, mod.Filename)); !os.IsNotExist(err) {
					t.Fatal("managed artifact survived", target, err)
				}
				raw, err := os.ReadFile(filepath.Join(f.data, target, "unmanaged.jar"))
				if err != nil || string(raw) != "untouched" {
					t.Fatal("unmanaged content changed", target, err)
				}
			}
		})
	}
}
func TestResetModsRejectUnsafeOrWrongTarget(t *testing.T) {
	for _, kind := range []string{"hash", "jar-symlink", "directory-symlink", "missing", "mod-in-plugins", "plugin-in-mods", "invalid-target"} {
		t.Run(kind, func(t *testing.T) {
			f := newPluginManageFixture(t, "enabled")
			mod := addResetMod(t, f)
			path := filepath.Join(f.data, "mods", mod.Filename)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "hash":
				must(os.WriteFile(path, []byte("changed"), 0644))
			case "missing":
				must(os.Remove(path))
			case "jar-symlink":
				must(os.Rename(path, path+".real"))
				must(os.Symlink(path+".real", path))
			case "directory-symlink":
				must(os.Rename(filepath.Join(f.data, "mods"), filepath.Join(f.data, "real-mods")))
				must(os.Symlink(filepath.Join(f.data, "real-mods"), filepath.Join(f.data, "mods")))
			case "mod-in-plugins":
				must(os.Rename(path, filepath.Join(f.data, "plugins", "mod-only.jar")))
				m, raw, _ := readPluginManifest(f.data)
				m.Artifacts[1].Filename = "mod-only.jar"
				_, err := commitPluginManifest(f.data, raw, m)
				must(err)
			case "plugin-in-mods":
				must(os.Rename(filepath.Join(f.data, "plugins", f.entry.Filename), filepath.Join(f.data, "mods", "plugin-only.jar")))
				m, raw, _ := readPluginManifest(f.data)
				m.Artifacts[0].Filename = "plugin-only.jar"
				_, err := commitPluginManifest(f.data, raw, m)
				must(err)
			case "invalid-target":
				m, _, _ := readPluginManifest(f.data)
				m.Artifacts[1].Target = "other"
				raw, err := json.Marshal(m)
				must(err)
				must(os.WriteFile(filepath.Join(f.data, ".justvoxel", "plugins.json"), raw, 0600))
			}
			plan := adminMinecraftResetPlanResponse{DataPath: f.data, DataAction: "preserve"}
			if err := prepareMinecraftResetPlugins(&plan); err == nil {
				t.Fatal("unsafe reset accepted")
			}
		})
	}
}
func TestResetLegacyOmittedTargetEvidence(t *testing.T) {
	for _, state := range []string{"enabled", "disabled"} {
		t.Run(state, func(t *testing.T) {
			f := newPluginManageFixture(t, state)
			m, raw, err := readPluginManifest(f.data)
			if err != nil {
				t.Fatal(err)
			}
			m.Artifacts[0].Target = ""
			if _, err = commitPluginManifest(f.data, raw, m); err != nil {
				t.Fatal(err)
			}
			tx := resetContentTransaction(t, f)
			if tx.Plugins.Entries[0].Entry.Target != "" {
				t.Fatal("legacy target rewritten")
			}
			dir, err := f.s.resetPluginEvidenceDirectory(tx.OperationID, true)
			if err != nil {
				t.Fatal(err)
			}
			err = publishResetPluginTransaction(dir, &tx)
			dir.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.readResetPluginTransaction(tx.OperationID); err != nil {
				t.Fatal(err)
			}
			if err = stageResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if err = restoreResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestResetWholeDataDeleteDoesNotInspectMods(t *testing.T) {
	data := t.TempDir()
	if err := os.Symlink("/unavailable", filepath.Join(data, "mods")); err != nil {
		t.Fatal(err)
	}
	plan := adminMinecraftResetPlanResponse{DataPath: data, DataAction: "delete"}
	if err := prepareMinecraftResetPlugins(&plan); err != nil || plan.PluginEvidence != nil || plan.PluginsAction != "delete_with_internal_data" {
		t.Fatal(plan, err)
	}
}

func TestResetModOnlyAndChangedStaging(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "mods-only", true: "changed-staged-hash"}[corrupt], func(t *testing.T) {
			f := newPluginManageFixture(t, "enabled")
			mod := addResetMod(t, f)
			m, raw, err := readPluginManifest(f.data)
			if err != nil {
				t.Fatal(err)
			}
			m.Artifacts = []pluginManifestEntry{mod}
			if _, err = commitPluginManifest(f.data, raw, m); err != nil {
				t.Fatal(err)
			}
			if err = os.RemoveAll(filepath.Join(f.data, "plugins")); err != nil {
				t.Fatal(err)
			}
			tx := resetContentTransaction(t, f)
			if len(tx.Plugins.Entries) != 1 || tx.Plugins.Entries[0].Entry.Target != "mods" {
				t.Fatal(tx.Plugins)
			}
			if err = stageResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				parent, err := openPluginOperationStaging(f.data, tx.OperationID, false)
				if err != nil {
					t.Fatal(err)
				}
				child, err := openPrivatePluginChild(parent, "mods", false)
				parent.Close()
				if err != nil {
					t.Fatal(err)
				}
				fd, err := unix.Openat(int(child.Fd()), mod.Filename, unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				child.Close()
				if err != nil {
					t.Fatal(err)
				}
				jar := os.NewFile(uintptr(fd), mod.Filename)
				_, err = jar.WriteAt([]byte("corrupt"), 0)
				jar.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err = commitResetPluginCleanup(tx); err == nil {
					t.Fatal("changed staged mod deleted")
				}
				_, raw, _ = readPluginManifest(f.data)
				if !bytes.Equal(raw, tx.Plugins.Manifest) {
					t.Fatal("ownership committed after corruption")
				}
				return
			}
			if err = restoreResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if err = stageResetPluginJars(tx); err != nil {
				t.Fatal(err)
			}
			if err = commitResetPluginCleanup(tx); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(filepath.Join(f.data, "mods")); err != nil || !info.IsDir() {
				t.Fatal("real mods directory removed", err)
			}
		})
	}
}
