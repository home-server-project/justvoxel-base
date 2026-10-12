package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestAdminMinecraftResetPlanComputesFingerprintAndPreservesNonMinecraftState(t *testing.T) {
	old := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = old }()
	runAdminMinecraftResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "plan" {
			t.Fatalf("unexpected reset helper args: %#v", args)
		}
		return []byte(validMinecraftResetPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminMinecraftResetPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/plan", "{}"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"\"plan_fingerprint\":\"sha256:",
		"\"data_action\":\"delete\"",
		"\"backup_action\":\"preserve\"",
		"\"storage_layout_action\":\"preserve\"",
		"\"authentication_action\":\"preserve\"",
		"\"webui_users_action\":\"preserve\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("reset plan missing %q: %s", want, body)
		}
	}
}

func TestAdminMinecraftResetPlanRejectsChangedPreservationContract(t *testing.T) {
	old := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = old }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(strings.Replace(validMinecraftResetPlanHelper, "\"backup_action\":\"preserve\"", "\"backup_action\":\"delete\"", 1)), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminMinecraftResetPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/plan", "{}"))
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "\"code\":\"invalid_plan\"") {
		t.Fatalf("changed preservation contract status=%d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminMinecraftResetPlanAllowsExternalMinecraftDataOnlyWhenPreserved(t *testing.T) {
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.DataScope = "external"
	plan.DataAction = "preserve"
	if err := validateAdminMinecraftResetPlan(plan); err != nil {
		t.Fatalf("external preserved data rejected: %v", err)
	}
	plan.DataAction = "delete"
	if err := validateAdminMinecraftResetPlan(plan); err == nil {
		t.Fatal("external Minecraft data deletion unexpectedly accepted")
	}
}

func TestAdminMinecraftResetApplyCreatesQueuedOperationFromExactPlan(t *testing.T) {
	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(validMinecraftResetPlanHelper), nil
	}

	oldWorker := startMinecraftResetWorker
	defer func() { startMinecraftResetWorker = oldWorker }()
	workerCalls := 0
	startMinecraftResetWorker = func(_ *server, _ string, fingerprint string, _ session) {
		workerCalls++
		if fingerprint != exactMinecraftResetFingerprint(t) {
			t.Fatalf("worker fingerprint = %q", fingerprint)
		}
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, err := json.Marshal(adminMinecraftResetApplyRequest{
		PlanFingerprint: exactMinecraftResetFingerprint(t),
		ConfirmPlayers:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminMinecraftResetApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Created || response.Operation == nil ||
		response.Operation.OperationType != operationTypeMinecraftReset ||
		response.Operation.State != operationQueued {
		t.Fatalf("unexpected reset apply response: %#v", response)
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want 1", workerCalls)
	}
}

func TestAdminMinecraftResetApplyRequiresPlayerConfirmation(t *testing.T) {
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.PlayersOnline = 2
	plan.Players = []string{"Alex", "Steve"}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := minecraftResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}

	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return payload, nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: fingerprint})
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusConflict ||
		!strings.Contains(rr.Body.String(), "\"code\":\"players_online\"") ||
		!strings.Contains(rr.Body.String(), "Alex") {
		t.Fatalf("player confirmation status=%d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentMinecraftReset(); err != nil || current != nil {
		t.Fatalf("reset operation created without player confirmation: %#v err=%v", current, err)
	}
}

func TestAdminMinecraftResetApplyRejectsStalePlan(t *testing.T) {
	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(validMinecraftResetPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	stale := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	body, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: stale})
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "\"code\":\"stale_plan\"") {
		t.Fatalf("stale reset status=%d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentMinecraftReset(); err != nil || current != nil {
		t.Fatalf("reset operation created from stale plan: %#v err=%v", current, err)
	}
}

func TestAdminMinecraftResetApplySameFingerprintReconnectsWithoutPlanning(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactMinecraftResetFingerprint(t)
	operation, _, err := store.beginMinecraftReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	called := false
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		called = true
		return nil, errors.New("must not run")
	}

	body, _ := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: fingerprint})
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusOK ||
		!strings.Contains(rr.Body.String(), operation.OperationID) ||
		!strings.Contains(rr.Body.String(), "\"created\":false") {
		t.Fatalf("reconnect status=%d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("reset planner ran while reconnecting to current operation")
	}
}

func TestExecuteMinecraftResetCompletesPersistentOperation(t *testing.T) {
	useTestMinecraftInstances(t)
	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "plan" {
			return []byte(validMinecraftResetPlanHelper), nil
		}
		if len(args) == 2 && args[0] == "apply" && args[1] == "--confirm-players" {
			return []byte(`{"ok":true,"schema_version":"v1","mode":"minecraft","data_path":"/var/lib/justvoxel/minecraft","data_scope":"internal","data_action":"delete","backup_path":"/var/lib/justvoxel/backups","backup_action":"preserve","authentication_action":"preserve","webui_users_action":"preserve","message":"Minecraft reset completed."}`), nil
		}
		t.Fatalf("unexpected helper args: %#v", args)
		return nil, nil
	}

	oldDiscovery := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = oldDiscovery }()
	runAdminDiscoveryHelper = func(_ context.Context, kind string) ([]byte, error) {
		if kind != "configuration" {
			t.Fatalf("unexpected discovery kind %q", kind)
		}
		return []byte(`{"configured":false}`), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactMinecraftResetFingerprint(t)
	operation, _, err := store.beginMinecraftReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := executeMinecraftReset(context.Background(), s, operation.OperationID, fingerprint, session{}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != operationSucceeded || finished.Stage != "complete" || finished.FinishedAt == "" {
		t.Fatalf("reset operation did not complete: %#v", finished)
	}
	if current, err := store.currentMinecraftReset(); err != nil || current != nil {
		t.Fatalf("reset remained current after success: %#v err=%v", current, err)
	}
}

func TestAdminMinecraftResetApplyRetriesNeedsAttentionOperation(t *testing.T) {
	oldHelper := runAdminMinecraftResetHelper
	defer func() { runAdminMinecraftResetHelper = oldHelper }()
	runAdminMinecraftResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(validMinecraftResetPlanHelper), nil
	}

	oldWorker := startMinecraftResetWorker
	defer func() { startMinecraftResetWorker = oldWorker }()
	workerCalls := 0
	var workerOperationID string
	startMinecraftResetWorker = func(_ *server, operationID, fingerprint string, _ session) {
		workerCalls++
		workerOperationID = operationID
		if fingerprint != exactMinecraftResetFingerprint(t) {
			t.Fatalf("worker fingerprint = %q", fingerprint)
		}
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactMinecraftResetFingerprint(t)
	operation, _, err := store.beginMinecraftReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "reset_failed", "Minecraft reset stopped before completion."); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(adminMinecraftResetApplyRequest{PlanFingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.adminMinecraftResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/minecraft/apply", string(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("retry status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminMinecraftResetApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Created || response.Operation == nil ||
		response.Operation.OperationID != operation.OperationID ||
		response.Operation.State != operationQueued ||
		response.Operation.Stage != "queued" {
		t.Fatalf("unexpected retry response: %#v", response)
	}
	if workerCalls != 1 || workerOperationID != operation.OperationID {
		t.Fatalf("retry worker calls=%d operation=%q, want one call for %q", workerCalls, workerOperationID, operation.OperationID)
	}
	current, err := store.currentMinecraftReset()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.OperationID != operation.OperationID || current.State != operationQueued {
		t.Fatalf("retry did not requeue current operation: %#v", current)
	}
}

func TestPreservedMinecraftResetExactManagedPluginCleanupAndRetry(t *testing.T) {
	for _, state := range []string{"enabled", "disabled"} {
		for _, failFirst := range []bool{false, true} {
			t.Run(state+map[bool]string{true: "retry", false: "success"}[failFirst], func(t *testing.T) {
				useTestMinecraftInstances(t)
				f := newPluginManageFixture(t, state)
				if err := os.WriteFile(filepath.Join(f.data, "plugins", "manual.jar"), f.jars[f.entry.Filename], 0644); err != nil {
					t.Fatal(err)
				}
				var helperPlan adminMinecraftResetPlanResponse
				if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &helperPlan); err != nil {
					t.Fatal(err)
				}
				helperPlan.DataPath = f.data
				helperPlan.DataScope = "network"
				helperPlan.DataAction = "preserve"
				oldHelper := runAdminMinecraftResetHelper
				t.Cleanup(func() { runAdminMinecraftResetHelper = oldHelper })
				configured := true
				attempts := 0
				runAdminMinecraftResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
					if args[0] == "plan" {
						return json.Marshal(helperPlan)
					}
					attempts++
					source := filepath.Join(f.data, "plugins", f.entry.Filename)
					if state == "disabled" {
						source = filepath.Join(f.data, ".justvoxel", "disabled-plugins", f.entry.Filename)
					}
					if _, err := os.Stat(source); !os.IsNotExist(err) {
						t.Fatal("managed JAR not staged before reset helper")
					}
					id := f.s.operations.currentMinecraftResetID
					stage, err := openPluginOperationStaging(f.data, id, false)
					if err != nil {
						t.Fatal(err)
					}
					if err = verifyManagedPluginAt(stage, f.entry); err != nil {
						t.Fatal("exact recovery copy unavailable", err)
					}
					stage.Close()
					if failFirst && attempts == 1 {
						return []byte(`{"ok":false,"code":"container_cleanup_failed","error":"Reset helper failed"}`), errors.New("helper failed")
					}
					configured = false
					return json.Marshal(adminMinecraftResetHelperApplyResponse{OK: true, SchemaVersion: "v1", Mode: "minecraft", DataPath: f.data, DataScope: "network", DataAction: "preserve", BackupAction: "preserve", AuthenticationAction: "preserve", WebUIUsersAction: "preserve"})
				}
				runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
					if !configured {
						return []byte(`{"configured":false}`), nil
					}
					return json.Marshal(map[string]any{"configured": true, "minecraft": map[string]string{"server_type": "paper", "data_path": f.data}})
				}
				plan, status := authoritativeAdminMinecraftResetPlan(context.Background())
				if status != 0 {
					t.Fatal(plan)
				}
				if plan.PluginsAction != "remove_managed_jars" {
					t.Fatal("missing cleanup action")
				}
				j, _, err := f.s.operations.beginMinecraftReset(plan.PlanFingerprint)
				if err != nil {
					t.Fatal(err)
				}
				err = executeMinecraftReset(context.Background(), f.s, j.OperationID, plan.PlanFingerprint, session{})
				if failFirst {
					if err == nil {
						t.Fatal("helper failure ignored")
					}
					current, _ := f.s.operations.currentMinecraftReset()
					if current == nil || current.State != operationNeedsAttention {
						t.Fatal("failed reset lost recovery")
					}
					restored, restoreErr := resolveManagedPluginLocation(f.data, f.entry)
					if restoreErr != nil || restored != state {
						t.Fatal("helper failure did not restore exact JAR", restored, restoreErr)
					}
					recoveryPlan, status := f.s.minecraftResetPlanWithRecovery(context.Background())
					if status != 0 || recoveryPlan.PlanFingerprint != plan.PlanFingerprint {
						t.Fatal("recovery plan lost", recoveryPlan)
					}
					if _, err = f.s.operations.retryMinecraftReset(j.OperationID, plan.PlanFingerprint); err != nil {
						t.Fatal(err)
					}
					err = executeMinecraftReset(context.Background(), f.s, j.OperationID, plan.PlanFingerprint, session{})
				}
				if err != nil {
					t.Fatal(err)
				}
				finished, _ := f.s.operations.get(j.OperationID)
				if finished.State != operationSucceeded {
					t.Fatal(finished)
				}
				if _, err = os.Stat(filepath.Join(f.data, "plugins", "manual.jar")); err != nil {
					t.Fatal("manual JAR changed", err)
				}
				config, err := os.ReadFile(filepath.Join(f.data, "plugins", "Root", "config.yml"))
				if err != nil || string(config) != "keep configuration" {
					t.Fatal("config folder changed", err)
				}
				if _, err = os.Lstat(filepath.Join(f.data, ".justvoxel", "plugins.json")); !os.IsNotExist(err) {
					t.Fatal("plugin manifest survived reset")
				}
				if _, err = os.Lstat(filepath.Join(f.data, ".justvoxel", "disabled-plugins")); !os.IsNotExist(err) {
					t.Fatal("disabled ownership store survived reset")
				}
				tx, err := f.s.readResetPluginTransaction(j.OperationID)
				if err != nil {
					t.Fatal(err)
				}
				if err = commitResetPluginCleanup(*tx); err != nil {
					t.Fatal("cleanup retry was not idempotent", err)
				}
			})
		}
	}
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

func TestInternalResetHelperStillDeletesEntireMinecraftDataTree(t *testing.T) {
	useTestMinecraftInstances(t)
	f := newPluginManageFixture(t, "enabled")
	if err := os.Mkdir(filepath.Join(f.data, "world"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.data, "world", "level.dat"), []byte("world"), 0600); err != nil {
		t.Fatal(err)
	}
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse([]byte(validMinecraftResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.DataPath = f.data
	old := runAdminMinecraftResetHelper
	t.Cleanup(func() { runAdminMinecraftResetHelper = old })
	configured := true
	applied := false
	runAdminMinecraftResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "plan" {
			return json.Marshal(plan)
		}
		if len(args) != 2 || args[1] != "--confirm-players" {
			t.Fatal("reset helper contract changed")
		}
		applied = true
		entries, err := os.ReadDir(f.data)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if err = os.RemoveAll(filepath.Join(f.data, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
		configured = false
		return json.Marshal(adminMinecraftResetHelperApplyResponse{OK: true, SchemaVersion: "v1", Mode: "minecraft", DataPath: f.data, DataScope: "internal", DataAction: "delete", BackupAction: "preserve", AuthenticationAction: "preserve", WebUIUsersAction: "preserve"})
	}
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return json.Marshal(map[string]bool{"configured": configured})
	}
	authoritative, status := authoritativeAdminMinecraftResetPlan(context.Background())
	if status != 0 || authoritative.PluginsAction != "delete_with_internal_data" {
		t.Fatal(authoritative)
	}
	journal, _, err := f.s.operations.beginMinecraftReset(authoritative.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err = executeMinecraftReset(context.Background(), f.s, journal.OperationID, authoritative.PlanFingerprint, session{}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(f.data)
	if !applied || err != nil || len(entries) != 0 {
		t.Fatal("whole-data deletion was replaced by partial plugin cleanup", entries, err)
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
	tx := &minecraftResetPluginTransaction{"v1", id, plan, *plan.PluginEvidence, false}
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
