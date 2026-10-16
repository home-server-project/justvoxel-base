package main

import (
	"bytes"
	"encoding/json"
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
