package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMinecraftInstanceRetirement(t *testing.T) {
	store := testMinecraftInstances(t)
	if err := store.retire(); err != nil {
		t.Fatalf("missing directory: %v", err)
	}
	if _, err := os.Stat(store.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retirement created the instances directory")
	}
	store.random = bytes.NewReader(append(make([]byte, 16), bytes.Repeat([]byte{1}, 16)...))
	first, err := store.register("", false)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(store.directory, "other.json")
	data := []byte(`{"id":"jv-other1"}`)
	if err := os.WriteFile(other, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.retire(); err != nil {
		t.Fatal(err)
	}
	if err := store.retire(); err != nil {
		t.Fatalf("missing record: %v", err)
	}
	if identity, err := store.read(); err != nil || identity.Status != "unavailable" {
		t.Fatalf("retired identity=%+v err=%v", identity, err)
	}
	if info, err := os.Stat(store.directory); err != nil || !store.private(info, 0700) {
		t.Fatalf("instances directory changed: %v", err)
	}
	if got, err := os.ReadFile(other); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("unrelated identity changed: %s err=%v", got, err)
	}
	second, err := store.register("", false)
	if err != nil || second.Status != "available" || second.ID == first.ID {
		t.Fatalf("next registration=%+v previous=%+v err=%v", second, first, err)
	}
}

func TestMinecraftInstanceRetirementDoesNotFollowSymlinks(t *testing.T) {
	for _, directoryLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "record", true: "directory"}[directoryLink], func(t *testing.T) {
			store := testMinecraftInstances(t)
			if _, err := store.register("old123", true); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.directory, "minecraft.json")
			outside := filepath.Join(t.TempDir(), "outside")
			if directoryLink {
				if err := os.Rename(store.directory, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, store.directory); err != nil {
					t.Fatal(err)
				}
				outside = filepath.Join(outside, "minecraft.json")
			} else {
				if err := os.Rename(path, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			err = store.retire()
			if (err != nil) != directoryLink {
				t.Fatalf("retirement error=%v directory symlink=%t", err, directoryLink)
			}
			if got, err := os.ReadFile(outside); err != nil || !bytes.Equal(got, before) {
				t.Fatalf("symlink target changed: %s err=%v", got, err)
			}
			if !directoryLink {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("record symlink was not retired: %v", err)
				}
			}
		})
	}
}

func TestResetMinecraftInstanceRetirement(t *testing.T) {
	for _, mode := range []string{"minecraft", "factory"} {
		for _, outcome := range []string{"success", "preserved_data", "helper_failed", "contract_failed", "verification_failed", "verification_error", "missing_record", "missing_directory", "retirement_failed", "authentication_failed"} {
			if mode == "minecraft" && outcome == "authentication_failed" {
				continue
			}
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				useTestMinecraftInstances(t)
				var original minecraftInstanceIdentity
				var err error
				if outcome != "missing_directory" {
					original, err = minecraftInstances.register("old123", true)
					if err != nil {
						t.Fatal(err)
					}
				}
				record := filepath.Join(minecraftInstances.directory, "minecraft.json")
				if outcome == "missing_record" || outcome == "retirement_failed" {
					if err := os.Remove(record); err != nil {
						t.Fatal(err)
					}
				}
				if outcome == "retirement_failed" {
					// Even an empty directory must never be removed as an identity record.
					if err := os.Mkdir(record, 0700); err != nil {
						t.Fatal(err)
					}
				}

				oldMinecraft, oldFactory := runAdminMinecraftResetHelper, runAdminFactoryResetHelper
				oldDiscovery := runAdminDiscoveryHelper
				oldResetAuth, oldCredential, oldMode := resetFactoryAuthenticationState, restoreSystemAdministratorFactoryCredential, readAuthMode
				t.Cleanup(func() {
					runAdminMinecraftResetHelper, runAdminFactoryResetHelper = oldMinecraft, oldFactory
					runAdminDiscoveryHelper = oldDiscovery
					resetFactoryAuthenticationState, restoreSystemAdministratorFactoryCredential, readAuthMode = oldResetAuth, oldCredential, oldMode
				})
				runAdminDiscoveryHelper = func(_ context.Context, kind string) ([]byte, error) {
					if kind != "configuration" {
						t.Fatalf("unexpected discovery kind: %s", kind)
					}
					if original.ID != "" && outcome != "missing_record" && outcome != "retirement_failed" {
						identity, err := minecraftInstances.read()
						if err != nil || identity != original {
							t.Fatalf("identity retired before verification: %+v err=%v", identity, err)
						}
					}
					if outcome == "verification_error" {
						return nil, errors.New("discovery failed")
					}
					if outcome == "verification_failed" {
						return []byte(`{"configured":true}`), nil
					}
					return []byte(`{"configured":false}`), nil
				}
				resetFactoryAuthenticationState = func() error {
					if outcome == "authentication_failed" {
						return errors.New("authentication reset failed")
					}
					return nil
				}
				restoreSystemAdministratorFactoryCredential = func(context.Context) error { return nil }
				readAuthMode = func() (authMode, error) { return authModeSystem, nil }

				planJSON := validMinecraftResetPlanHelper
				if mode == "factory" {
					planJSON = validFactoryResetPlanHelper
				}
				if outcome == "preserved_data" {
					planJSON = strings.Replace(planJSON, `"data_scope":"internal"`, `"data_scope":"external"`, 1)
					planJSON = strings.Replace(planJSON, `"data_action":"delete"`, `"data_action":"preserve"`, 1)
				}
				var fingerprint string
				var applied any
				if mode == "minecraft" {
					var plan adminMinecraftResetPlanResponse
					if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
						t.Fatal(err)
					}
					fingerprint, err = minecraftResetPlanFingerprint(plan)
					result := adminMinecraftResetHelperApplyResponse{OK: true, SchemaVersion: "v1", Mode: mode, DataPath: plan.DataPath, DataScope: plan.DataScope, DataAction: plan.DataAction, BackupPath: plan.BackupPath, BackupAction: plan.BackupAction, AuthenticationAction: plan.AuthenticationAction, WebUIUsersAction: plan.WebUIUsersAction}
					if outcome == "contract_failed" {
						result.BackupAction = "delete"
					}
					applied = result
				} else {
					var plan adminFactoryResetPlanResponse
					if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
						t.Fatal(err)
					}
					fingerprint, err = factoryResetPlanFingerprint(plan)
					result := adminFactoryResetHelperApplyResponse{OK: true, SchemaVersion: "v1", Mode: mode, MinecraftWasConfigured: true, DataPath: plan.DataPath, DataScope: plan.DataScope, DataAction: plan.DataAction, BackupPath: plan.BackupPath, BackupScope: plan.BackupScope, BackupAction: plan.BackupAction, ConfigBackupsAction: plan.ConfigBackupsAction, StorageLayoutAction: plan.StorageLayoutAction, ExternalStorageAction: plan.ExternalStorageAction, NetworkStorageAction: plan.NetworkStorageAction}
					if outcome == "contract_failed" {
						result.ExternalStorageAction = "delete"
					}
					applied = result
				}
				if err != nil {
					t.Fatal(err)
				}
				applyJSON, err := json.Marshal(applied)
				if err != nil {
					t.Fatal(err)
				}
				helper := func(_ context.Context, args ...string) ([]byte, error) {
					if len(args) == 1 && args[0] == "plan" {
						return []byte(planJSON), nil
					}
					if len(args) != 2 || args[0] != "apply" || args[1] != "--confirm-players" {
						t.Fatalf("unexpected helper args: %v", args)
					}
					if outcome == "helper_failed" {
						return []byte(`{"ok":false,"error":"cleanup failed"}`), errors.New("cleanup failed")
					}
					return applyJSON, nil
				}
				runAdminMinecraftResetHelper, runAdminFactoryResetHelper = helper, helper
				s := surfaceTestServer(t, roleAdministrator)
				operations := openTestOperationStore(t)
				attachTestOperationStore(t, s, operations)
				var operation operationJournal
				if mode == "minecraft" {
					operation, _, err = operations.beginMinecraftReset(fingerprint)
				} else {
					operation, _, err = operations.beginFactoryReset(fingerprint)
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode == "minecraft" {
					err = executeMinecraftReset(context.Background(), s, operation.OperationID, fingerprint, session{})
				} else {
					err = executeFactoryReset(context.Background(), s, operation.OperationID, fingerprint)
				}
				succeeded := outcome == "success" || outcome == "preserved_data" || outcome == "missing_record" || outcome == "missing_directory"
				if (err == nil) != succeeded {
					t.Fatalf("reset outcome=%s err=%v", outcome, err)
				}
				finished, readErr := operations.get(operation.OperationID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if succeeded {
					if finished.State != operationSucceeded {
						t.Fatalf("reset did not succeed: %+v", finished)
					}
					if identity, err := minecraftInstances.read(); err != nil || identity.Status != "unavailable" {
						t.Fatalf("reset retained identity: %+v err=%v", identity, err)
					}
					minecraftInstances.random = bytes.NewReader(make([]byte, 16))
					if next, err := minecraftInstances.register("", false); err != nil || next.ID == original.ID {
						t.Fatalf("next setup identity=%+v old=%+v err=%v", next, original, err)
					}
				} else {
					if finished.State != operationNeedsAttention {
						t.Fatalf("failure not reported: %+v", finished)
					}
					if outcome == "retirement_failed" {
						if finished.Stage != "instance_retirement_failed" || finished.Status != err.Error() || !strings.Contains(finished.Status, "Instance ID retirement failed:") {
							t.Fatalf("missing specific retirement failure: %+v err=%v", finished, err)
						}
						if info, err := os.Stat(record); err != nil || !info.IsDir() {
							t.Fatalf("retirement removed unexpected directory: %v", err)
						}
					} else if identity, err := minecraftInstances.read(); err != nil || identity != original {
						t.Fatalf("failed reset changed identity: %+v want=%+v err=%v", identity, original, err)
					}
				}
			})
		}
	}
}
