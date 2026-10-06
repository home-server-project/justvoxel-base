package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlusFactoryResetWorkerUsesSharedIdentityAndOperationTracking(t *testing.T) {
	p, _ := plusResetFixture(t, false)
	oldPaths, oldCommand, oldAuth, oldCredential, oldMode, oldHelper := plusFactoryResetPaths, runPlusDeploymentCommand, resetFactoryAuthenticationState, restoreSystemAdministratorFactoryCredential, readAuthMode, runAdminFactoryResetHelper
	t.Cleanup(func() {
		plusFactoryResetPaths = oldPaths
		runPlusDeploymentCommand = oldCommand
		resetFactoryAuthenticationState = oldAuth
		restoreSystemAdministratorFactoryCredential = oldCredential
		readAuthMode = oldMode
		runAdminFactoryResetHelper = oldHelper
	})
	plusFactoryResetPaths = func() plusDeploymentPaths { return p }
	runPlusDeploymentCommand = func(context.Context, string, []string, []byte) ([]byte, error) { return nil, nil }
	runAdminFactoryResetHelper = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("Plus called inherited Minecraft cleanup")
		return nil, nil
	}
	authReset, credentialReset := false, false
	resetFactoryAuthenticationState = func() error { authReset = true; return nil }
	restoreSystemAdministratorFactoryCredential = func(context.Context) error { credentialReset = true; return nil }
	readAuthMode = func() (authMode, error) { return authModeSystem, nil }
	s := surfaceTestServer(t, roleAdministrator)
	s.plus = true
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	if _, err := s.store.createWebUser("PlusOperator", webUserRoleOperator, "operator-password"); err != nil {
		t.Fatal(err)
	}
	plan, code := s.factoryResetPlan(context.Background())
	if code != 0 || !plan.Plus {
		t.Fatal("Plus plan unavailable")
	}
	operation, _, err := store.beginFactoryReset(plan.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	if !s.plusResetBlocksSetup(rr, httptest.NewRequest(http.MethodPost, "/v1/admin/plus/setup/deploy", nil)) || rr.Code != http.StatusConflict {
		t.Fatal("setup was not blocked by reset")
	}
	if err := executeFactoryReset(context.Background(), s, operation.OperationID, plan.PlanFingerprint); err != nil {
		t.Fatal(err)
	}
	finished, err := store.get(operation.OperationID)
	if err != nil || finished.State != operationSucceeded || !authReset || !credentialReset {
		t.Fatal("shared identity reset did not complete")
	}
	users, err := s.store.listWebUsers()
	if err != nil || len(users) != 0 {
		t.Fatal("WebUI users survived reset")
	}
	rr = httptest.NewRecorder()
	if s.plusResetBlocksSetup(rr, httptest.NewRequest(http.MethodPost, "/v1/admin/plus/setup/deploy", nil)) {
		t.Fatal("completed reset blocked fresh setup")
	}
}

func plusResetFixture(t *testing.T, external bool) (plusDeploymentPaths, string) {
	t.Helper()
	p := plusDeploymentFixture(t)
	root := p.DefaultData
	if external {
		root = filepath.Join(t.TempDir(), "second-drive", "justvoxel-plus")
	}
	for _, dir := range []string{p.Configuration, p.Preparation, p.UnitOverride, root} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(p.Configuration, "stack.env"):    "PLUS_DATA_ROOT=" + root + "\nDATABASE_PASSWORD=private\n",
		filepath.Join(p.Configuration, "compose.yaml"): "services: {}\n",
		filepath.Join(p.Configuration, "deployed"):     "{}",
		filepath.Join(p.UnitOverride, "storage.conf"):  "fixture",
		filepath.Join(p.Preparation, "setup.json"):     "private setup",
		filepath.Join(root, "precious"):                "keep or explicitly erase"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldMounts := plusResetMounts
	plusResetMounts = func() ([]string, error) { return nil, nil }
	t.Cleanup(func() { plusResetMounts = oldMounts })
	return p, root
}

func TestPlusFactoryResetInternalExternalScopeAndOwnership(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "internal", true: "external"}[external], func(t *testing.T) {
			p, root := plusResetFixture(t, external)
			plan, err := planPlusFactoryReset(p)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Plus || plan.MinecraftConfigured || strings.Contains(plan.RuntimeFingerprint, "private") {
				t.Fatal("invalid Plus plan")
			}
			if external && plan.DataAction != "preserve" || !external && plan.DataAction != "delete" {
				t.Fatal("wrong data scope")
			}
			old := runPlusDeploymentCommand
			t.Cleanup(func() { runPlusDeploymentCommand = old })
			owned, unrelated := strings.Repeat("a", 64), strings.Repeat("b", 64)
			calls := []string{}
			runPlusDeploymentCommand = func(_ context.Context, command string, args []string, _ []byte) ([]byte, error) {
				call := command + " " + strings.Join(args, " ")
				calls = append(calls, call)
				if strings.Contains(call, "is-active") {
					return nil, errors.New("inactive")
				}
				if strings.Contains(call, "ps --all") {
					return []byte(owned + "\n" + unrelated), nil
				}
				if len(args) > 1 && args[0] == "inspect" {
					source := filepath.Join(root, "wings", "data", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
					if args[1] == unrelated {
						source = "/unrelated/data/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
					}
					return []byte(`[{"Config":{"Labels":{"Service":"Pterodactyl"}},"Mounts":[{"Source":"` + source + `"}]}]`), nil
				}
				return nil, nil
			}
			output, err := applyPlusFactoryReset(context.Background(), p, plan)
			if err != nil {
				t.Fatal(err)
			}
			var result adminFactoryResetHelperApplyResponse
			if json.Unmarshal(output, &result) != nil || validateFactoryResetApplyResult(plan, result) != nil {
				t.Fatal("invalid reset result")
			}
			if _, err := os.Stat(filepath.Join(root, "precious")); external && err != nil || !external && !os.IsNotExist(err) {
				t.Fatal("data preservation contract failed")
			}
			for _, dir := range []string{p.Configuration, p.UnitOverride} {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("configuration remains")
				}
			}
			if _, err := os.Stat(filepath.Join(p.Preparation, "setup.json")); !os.IsNotExist(err) {
				t.Fatal("setup credentials remain")
			}
			joined := strings.Join(calls, "\n")
			if !strings.Contains(joined, "docker rm --force "+owned) || strings.Contains(joined, "docker rm --force "+unrelated) {
				t.Fatal("wrong game container removed")
			}
			for _, absent := range []string{"prune", "volume rm", "tailscale", "netbird", "playit", "minecraft.service", "firewall"} {
				if strings.Contains(joined, absent) {
					t.Fatalf("unexpected host change: %s", absent)
				}
			}
		})
	}
}

func TestPlusFactoryResetRejectsChangedConfigurationActiveSetupAndFailures(t *testing.T) {
	for _, kind := range []string{"changed", "active", "stop-failure"} {
		t.Run(kind, func(t *testing.T) {
			p, root := plusResetFixture(t, false)
			plan, err := planPlusFactoryReset(p)
			if err != nil {
				t.Fatal(err)
			}
			old := runPlusDeploymentCommand
			t.Cleanup(func() { runPlusDeploymentCommand = old })
			runPlusDeploymentCommand = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
				if len(args) > 0 && args[0] == "is-active" {
					if kind == "active" {
						return []byte("active"), nil
					}
					return nil, errors.New("inactive")
				}
				return nil, errors.New("private diagnostics must not escape")
			}
			if kind == "changed" {
				if err := os.WriteFile(filepath.Join(p.Configuration, "stack.env"), []byte("PLUS_DATA_ROOT="+root+"\nIMAGE=changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := applyPlusFactoryReset(context.Background(), p, plan); err == nil || strings.Contains(err.Error(), "private diagnostics") {
				t.Fatal("unsafe reset accepted or diagnostics leaked")
			}
			if _, err := os.Stat(filepath.Join(root, "precious")); err != nil {
				t.Fatal("failed reset deleted data")
			}
		})
	}
}

func TestPlusFactoryResetRefusesSymlinksNestedMountsAndPreservesMountedRoot(t *testing.T) {
	p, root := plusResetFixture(t, false)
	plusResetMounts = func() ([]string, error) { return []string{root}, nil }
	plan, err := planPlusFactoryReset(p)
	if err != nil || plan.DataAction != "preserve" {
		t.Fatal("mounted root would be erased")
	}
	for _, mount := range []string{filepath.Join(root, "nested"), filepath.Join(p.Configuration, "nested"), p.UnitOverride} {
		plusResetMounts = func() ([]string, error) { return []string{mount}, nil }
		if _, err := planPlusFactoryReset(p); err == nil {
			t.Fatal("nested filesystem accepted")
		}
	}
	plusResetMounts = func() ([]string, error) { return nil, nil }
	other := t.TempDir()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, root); err != nil {
		t.Fatal(err)
	}
	if _, err := planPlusFactoryReset(p); err == nil {
		t.Fatal("symlink data root accepted")
	}
}
