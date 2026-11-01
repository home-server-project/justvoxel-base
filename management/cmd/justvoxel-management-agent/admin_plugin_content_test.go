package main

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContentCapabilities(t *testing.T) {
	for _, software := range []string{"paper", "purpur", "pufferfish", "leaf", "spigot", "fabric", "forge", "neoforge", "quilt", "vanilla", "bedrock"} {
		c := pluginContentContext(software)
		target := "plugins"
		if software == "fabric" || software == "forge" || software == "neoforge" || software == "quilt" {
			target = "mods"
		}
		if software == "vanilla" || software == "bedrock" {
			target = ""
		}
		if c.ContentKind != target || c.Supported != (target != "") || c.Providers.Modrinth != c.Supported || c.Providers.CurseForge != c.Supported {
			t.Fatal(software, c)
		}
		if c.Providers.Hangar != (target == "plugins" && software != "spigot") {
			t.Fatal("Hangar mapping", software, c)
		}
		if target == "mods" && !reflect.DeepEqual(pluginLoaders(software), []string{software}) {
			t.Fatal("loader mapping", software)
		}
	}
}
func TestModInstallAndInventory(t *testing.T) {
	for _, loader := range []string{"fabric", "forge", "neoforge", "quilt"} {
		t.Run(loader, func(t *testing.T) {
			f := newPluginInstallFixtureFor(t, loader, "1.21.8")
			if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
				t.Fatal(err)
			}
			raw := resetModJar(t, loader)
			f.jars["root.jar"] = raw
			v := f.versions["release"]
			v.Loaders = []string{loader}
			v.Files[0].Size = int64(len(raw))
			sum := sha512.Sum512(raw)
			v.Files[0].Hashes.SHA512 = hex.EncodeToString(sum[:])
			f.versions["release"] = v
			c := pluginContentContext(loader)
			c.InstanceID = "jv-a1b2c3"
			c.MinecraftVersion = "1.21.8"
			c.Configured = true
			plan, err := resolvePluginInstallPlan(context.Background(), c, f.data, "root", "release")
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Artifacts) != 1 || plan.Artifacts[0].Target != "mods" || plan.Location != filepath.Join(f.data, "mods") {
				t.Fatal(plan)
			}
			journal := f.execute(t, plan, false)
			if journal.State != operationSucceeded {
				t.Fatal(journal.State, journal.Status)
			}
			m, _, err := readPluginManifest(f.data)
			if err != nil || len(m.Artifacts) != 1 || m.Artifacts[0].Target != "mods" || m.Artifacts[0].Main != "" {
				t.Fatal(m, err)
			}
			inventory, err := readModInventory(f.data, loader)
			if err != nil || len(inventory) != 1 || inventory[0].Management != "plugins" {
				t.Fatal(inventory, err)
			}
			if _, err := resolveManagedPluginLocation(f.data, m.Artifacts[0]); err == nil {
				t.Fatal("mod gained plugin lifecycle")
			}
			if _, err := os.Stat(filepath.Join(f.data, "plugins", "root.jar")); !os.IsNotExist(err) {
				t.Fatal("mod placed in plugins", err)
			}
			if !strings.Contains(journal.Status, "runtime health verified") {
				t.Fatal("mod verification overstated", journal.Status)
			}
		})
	}
}
func TestModDescriptorAndHashVerification(t *testing.T) {
	for _, loader := range []string{"fabric", "forge", "neoforge", "quilt"} {
		data := t.TempDir()
		if err := os.Mkdir(filepath.Join(data, "mods"), 0700); err != nil {
			t.Fatal(err)
		}
		raw := resetModJar(t, loader)
		sum := sha512.Sum512(raw)
		e := pluginManifestEntry{Target: "mods", Filename: "example.jar", SHA512: hex.EncodeToString(sum[:])}
		path := filepath.Join(data, "mods", e.Filename)
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
		root, err := openContentDirectory(data, "mods")
		if err != nil {
			t.Fatal(err)
		}
		if err = verifyManagedArtifactAt(root, e); err != nil {
			t.Fatal(loader, err)
		}
		e.SHA512 = strings.Repeat("0", 128)
		if verifyManagedArtifactAt(root, e) == nil {
			t.Fatal("hash mismatch accepted")
		}
		root.Close()
	}
	for _, raw := range []string{"{}", "null", "[]", "invalid"} {
		if validModDescriptor([]byte(raw), "fabric") {
			t.Fatal("invalid descriptor accepted", raw)
		}
	}
}
func TestContentManifestTargetsAndFingerprint(t *testing.T) {
	f := newPluginManageFixture(t, "enabled")
	addResetMod(t, f)
	m, _, err := readPluginManifest(f.data)
	if err != nil || len(m.Artifacts) != 2 {
		t.Fatal(m, err)
	}
	bad := m
	bad.Artifacts = append([]pluginManifestEntry(nil), m.Artifacts...)
	bad.Artifacts[1].Target = "other"
	if validatePluginManifest(bad) == nil {
		t.Fatal("invalid target accepted")
	}
	tx := resetContentTransaction(t, f)
	before, _ := minecraftResetPlanFingerprint(tx.Plan)
	tx.Plan.PluginEvidence.Entries[1].Entry.Target = "plugins"
	after, _ := minecraftResetPlanFingerprint(tx.Plan)
	if before == after {
		t.Fatal("target omitted from reset fingerprint")
	}
}
func TestThreeProviderCursorFairness(t *testing.T) {
	pages := []pluginCatalogPage{}
	for _, source := range []string{"Modrinth", "Hangar", "CurseForge"} {
		pages = append(pages, pluginCatalogPage{Projects: []pluginCatalogProject{{Source: source}, {Source: source}}, Positions: []int{1, 2}, End: 2, NextOffset: -1})
	}
	cursor := pluginCatalogCursor{}
	for _, want := range []string{"Modrinth", "Hangar", "CurseForge"} {
		positions := []int{cursor.ModrinthOffset, cursor.HangarOffset, cursor.CurseForgeOffset}
		current := append([]pluginCatalogPage(nil), pages...)
		for i, pos := range positions {
			current[i].Offset = pos
			current[i].Projects = pages[i].Projects[pos:]
			current[i].Positions = pages[i].Positions[pos:]
		}
		items, next := mergeContentCatalogPages(current, cursor, 1)
		if len(items) != 1 || items[0].Source != want || next == nil {
			t.Fatal(items, next, want)
		}
		cursor = *next
	}
	if cursor.ModrinthOffset != 1 || cursor.HangarOffset != 1 || cursor.CurseForgeOffset != 1 {
		t.Fatal("unconsumed provider positions lost", cursor)
	}
}

func TestModInstallRollbackPreservesOtherContent(t *testing.T) {
	f := newPluginInstallFixtureFor(t, "fabric", "1.21.8")
	if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.data, "mods", "manual.jar"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	raw := resetModJar(t, "fabric")
	f.jars["root.jar"] = raw
	v := f.versions["release"]
	v.Loaders = []string{"fabric"}
	v.Files[0].Size = int64(len(raw))
	sum := sha512.Sum512(raw)
	v.Files[0].Hashes.SHA512 = hex.EncodeToString(sum[:])
	f.versions["release"] = v
	c := pluginContentContext("fabric")
	c.InstanceID = "jv-a1b2c3"
	c.MinecraftVersion = "1.21.8"
	plan, err := resolvePluginInstallPlan(context.Background(), c, f.data, "root", "release")
	if err != nil {
		t.Fatal(err)
	}
	previous := pluginInstallCommand
	failed := false
	pluginInstallCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "/usr/libexec/justvoxel/mjust/verify-minecraft-stack" && !failed {
			if _, err := os.Stat(filepath.Join(f.data, "mods", "root.jar")); err == nil {
				failed = true
				return nil, errors.New("mod runtime failed")
			}
		}
		return previous(ctx, command, args...)
	}
	journal := f.execute(t, plan, false)
	if !failed || journal.State != operationRolledBack {
		t.Fatal(journal.State, journal.Status)
	}
	if _, err := os.Lstat(filepath.Join(f.data, "mods", "root.jar")); !os.IsNotExist(err) {
		t.Fatal("mod rollback lost identity", err)
	}
	if raw, err := os.ReadFile(filepath.Join(f.data, "mods", "manual.jar")); err != nil || string(raw) != "keep" {
		t.Fatal("unmanaged mod altered", err)
	}
}
