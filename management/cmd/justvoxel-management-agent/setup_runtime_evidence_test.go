package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Persist real authoritative records without starting runtimes or opening sockets.
func setupRuntimeEvidenceFixture(t *testing.T) (*instanceRuntimeBackend, []instanceRuntimeRecord) {
	t.Helper()
	store, snapshot, configs := instanceRuntimeFixture(t)
	root := t.TempDir()
	backend := &instanceRuntimeBackend{root: filepath.Join(root, "instances"), quadlets: filepath.Join(root, "quadlets"), staging: filepath.Join(root, "staging"), store: store}
	for _, path := range []string{backend.root, backend.quadlets, backend.staging} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var records []instanceRuntimeRecord
	for _, config := range configs {
		dir, _, _ := backend.paths(config.ID)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		record := instanceRuntimeRecord{SchemaVersion: 1, Runtime: config, Storage: instanceStorageFor(snapshot, config.ID), Firewall: []instanceFirewallRule{}}
		if err := backend.writeJSON(filepath.Join(dir, "config.json"), record, false); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.readRecord(config.ID); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	old := instanceRuntimes
	instanceRuntimes = backend
	t.Cleanup(func() { instanceRuntimes = old })
	return backend, records
}

func captureSetupRuntimeEvidenceCommands(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	old := runSetupDiagnosticCommand
	oldBootc := runBootcStatusJSON
	runBootcStatusJSON = func(context.Context) ([]byte, error) { return nil, errors.New("unavailable in diagnostic test") }
	t.Cleanup(func() { runBootcStatusJSON = oldBootc })
	runSetupDiagnosticCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch name {
		case "systemctl":
			if len(args) > 0 && args[0] == "show" {
				return []byte("LoadState=loaded\nActiveState=active\nSubState=running\nResult=success\nExecMainStatus=0\n"), nil
			}
			return []byte("active\n"), nil
		case "ls":
			return []byte("system_u:object_r:container_file_t:s0 path\n"), nil
		case "journalctl":
			return []byte("selected runtime journal\n"), nil
		}
		return []byte("available\n"), nil
	}
	t.Cleanup(func() { runSetupDiagnosticCommand = old })
	return &calls
}

func setupRuntimeEvidencePlan() *adminSetupNormalizedPlan {
	plan := &adminSetupNormalizedPlan{}
	plan.Storage.Path = "/proposed/storage"
	plan.Minecraft.ImageTag = "stable"
	plan.Minecraft.JavaPort = 25565
	plan.Minecraft.BedrockPort = 19132
	plan.Server.BedrockEnabled = true
	return plan
}

func assertSetupRuntimeProbeScope(t *testing.T, calls []string, selected string) {
	t.Helper()
	for _, call := range calls {
		for _, arg := range strings.Fields(call) {
			if arg == "minecraft" || arg == "minecraft.service" || arg == "minecraft-backup.timer" || strings.Contains(arg, "jv-d4e5f6") || strings.Contains(arg, "validate-backend") {
				t.Errorf("forbidden runtime probe: %s", call)
			}
			if strings.HasPrefix(arg, "minecraft-") && (selected == "" || (arg != "minecraft-"+selected && arg != "minecraft-"+selected+".service" && arg != instanceBackupUnit(selected, "timer"))) {
				t.Errorf("unexpected runtime target: %s", call)
			}
		}
		if selected == "" && (strings.HasPrefix(call, "systemctl show ") || strings.HasPrefix(call, "podman container ") || strings.HasPrefix(call, "podman inspect ") || strings.HasPrefix(call, "journalctl -u ")) {
			t.Errorf("runtime command without authority: %s", call)
		}
	}
}

func assertSetupApplianceEvidence(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{"setup_marker_present", "config_present", "network_online_target", "network_connectivity", "podman_network_backend", "podman_default_network", "podman_network_summary", "dns_docker_io", "dns_papermc_io", "dns_geysermc_org", "java_firewall_open", "bedrock_firewall_open", "minecraft_image_digest", "minecraft_image_local_id", "data_selinux_type"} {
		if values[key] == "" || values[key] == "<unavailable>" {
			t.Errorf("missing appliance/plan evidence %s: %q", key, values[key])
		}
	}
}

func TestSetupRuntimeEvidenceAuthoritativeInstanceOnly(t *testing.T) {
	backend, records := setupRuntimeEvidenceFixture(t)
	calls := captureSetupRuntimeEvidenceCommands(t)
	id := records[0].Runtime.ID
	// A sibling's artifacts must not satisfy presence checks for the selection.
	_, siblingEnv, siblingQuadlet := backend.paths(records[1].Runtime.ID)
	for _, path := range []string{siblingEnv, siblingQuadlet} {
		if err := os.WriteFile(path, []byte("sibling"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	values := collectSetupRuntimeEvidence(setupRuntimeEvidencePlan(), id)
	if values["minecraft_env_present"] != "absent" || values["quadlet_present"] != "absent" {
		t.Fatal(values)
	}
	_, env, quadlet := backend.paths(id)
	for _, path := range []string{env, quadlet} {
		if err := os.WriteFile(path, []byte("selected"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	values = collectSetupRuntimeEvidence(setupRuntimeEvidencePlan(), id)
	for key, want := range map[string]string{"instance_id": id, "minecraft_env_present": "present", "quadlet_present": "present", "container_present": "present", "service_load_state": "loaded", "service_active_state": "active"} {
		if values[key] != want {
			t.Errorf("%s = %q, want %q", key, values[key], want)
		}
	}
	assertSetupApplianceEvidence(t, values)
	assertSetupRuntimeProbeScope(t, *calls, id)
	joined := "\n" + strings.Join(*calls, "\n") + "\n"
	for _, want := range []string{"systemctl show minecraft-" + id + ".service ", "podman container exists minecraft-" + id + "\n", "podman inspect minecraft-" + id + " ", "systemctl is-enabled " + instanceBackupUnit(id, "timer") + "\n", "systemctl is-active " + instanceBackupUnit(id, "timer") + "\n", "ls -Zd -- " + records[0].Storage.DataPath + "\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, "ls -Zd -- /proposed/storage") {
		t.Fatal("selected instance used proposed storage")
	}
}

func TestSetupRuntimeEvidenceWithoutAuthority(t *testing.T) {
	for _, id := range []string{"", "jv-cccccc", "jv-a1b2c3", "invalid"} {
		t.Run("id_"+id, func(t *testing.T) {
			backend, _ := setupRuntimeEvidenceFixture(t)
			if id == "jv-a1b2c3" {
				dir, _, _ := backend.paths(id)
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := captureSetupRuntimeEvidenceCommands(t)
			values := collectSetupRuntimeEvidence(setupRuntimeEvidencePlan(), id)
			want := "<unavailable>"
			if id == "" {
				want = "<not_created>"
			}
			for _, key := range []string{"container_present", "container_state", "minecraft_env_present", "quadlet_present", "service_load_state", "backup_timer_enabled", "backup_timer_active"} {
				if values[key] != want {
					t.Errorf("%s = %q, want %q", key, values[key], want)
				}
			}
			if values["instance_id"] != "" {
				t.Fatal("unverified ID exposed as authority", values)
			}
			assertSetupApplianceEvidence(t, values)
			assertSetupRuntimeProbeScope(t, *calls, "")
		})
	}
}

func TestSetupRuntimeEvidenceHelpersRequireExplicitTarget(t *testing.T) {
	calls := captureSetupRuntimeEvidenceCommands(t)
	for _, unit := range []string{"", "  "} {
		for key, value := range setupDiagnosticMinecraftServiceState(unit) {
			if value != "<not_created>" {
				t.Errorf("%s = %q", key, value)
			}
		}
		if got := setupDiagnosticRuntimeJournal(unit); got != "<not_created>" {
			t.Fatal(got)
		}
	}
	if len(*calls) != 0 {
		t.Fatal("empty target executed commands", *calls)
	}
}

func TestSetupRuntimeEvidenceFailureBundle(t *testing.T) {
	for _, mode := range []string{"selected", "pending", "unresolved", "stale", "session"} {
		t.Run(mode, func(t *testing.T) {
			backend, records := setupRuntimeEvidenceFixture(t)
			calls := captureSetupRuntimeEvidenceCommands(t)
			store := openTestOperationStore(t)
			var id string
			if mode == "session" {
				var err error
				id, err = store.beginSetupDiagnostic("webui")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				operation, _, err := store.beginSetup(testSetupFingerprint, true)
				if err != nil {
					t.Fatal(err)
				}
				id = operation.OperationID
				if mode != "unresolved" {
					request := setupRuntimeTransactionRequest{InstanceOnly: true, OperationID: id, PlanFingerprint: testSetupFingerprint}
					evidence := setupInstanceEvidence{Schema: 1, Request: request, Policy: setupInstancePolicy(request)}
					if mode != "pending" {
						evidence.Record = records[0]
					}
					if err := backend.writeJSON(setupInstancePath(id), evidence, false); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "stale" {
					dir, _, _ := backend.paths(records[0].Runtime.ID)
					if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := store.ensureSetupOperationDiagnostic(id); err != nil {
				t.Fatal(err)
			}
			*calls = nil
			values := store.setupRuntimeEvidenceForOperation(id, setupRuntimeEvidencePlan())
			assertSetupApplianceEvidence(t, values)
			store.appendSetupRuntimeFailureBundleBestEffort(id, "verify", setupRuntimeEvidencePlan(), errors.New("test failure"))
			data, err := store.readSetupDiagnostic(id)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			selected := ""
			wantJournal := "<not_created>"
			if mode == "selected" {
				selected = records[0].Runtime.ID
				wantJournal = "selected runtime journal"
				journal := "journalctl -u minecraft-" + selected + ".service --no-pager -n 80 -o short-iso"
				count := 0
				for _, call := range *calls {
					if call == journal {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("journal count = %d: %v", count, *calls)
				}
			} else if mode == "stale" {
				wantJournal = "<unavailable>"
			}
			assertSetupRuntimeProbeScope(t, *calls, selected)
			if !strings.Contains(text, "minecraft_journal=\""+wantJournal+"\"") {
				t.Fatal("incorrect journal evidence", text)
			}
			if !strings.Contains(text, "validation_snapshot=\"") || !strings.Contains(text, "not applicable") {
				t.Fatal("missing validation explanation", text)
			}
		})
	}
}
