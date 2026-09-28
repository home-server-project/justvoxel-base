package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/home-server-project/justvoxel/management/internal/systemauth"
)

const validFactoryResetPlanHelper = `{"ok":true,"schema_version":"v1","mode":"factory","minecraft_configured":true,"data_path":"/var/lib/justvoxel/minecraft","data_scope":"internal","data_action":"delete","backup_path":"/var/lib/justvoxel/backups","backup_scope":"internal","backup_action":"delete","config_backups_action":"delete","storage_layout_action":"preserve","external_storage_action":"preserve","network_storage_action":"preserve","authentication_action":"reset_to_system","webui_users_action":"delete","password_action":"restore_default_and_expire","sessions_action":"invalidate","players_online":0,"players":[],"warnings":[]}`

func exactFactoryResetFingerprint(t *testing.T) string {
	t.Helper()
	var plan adminFactoryResetPlanResponse
	if err := decodeAdminFactoryResetPlanResponse([]byte(validFactoryResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := factoryResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func TestAdminFactoryResetPlanComputesFingerprintAndPreservesExternalStorage(t *testing.T) {
	old := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = old }()
	runAdminFactoryResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "plan" {
			t.Fatalf("unexpected factory reset helper args: %#v", args)
		}
		return []byte(validFactoryResetPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminFactoryResetPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/plan", "{}"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"\"plan_fingerprint\":\"sha256:",
		"\"backup_action\":\"delete\"",
		"\"config_backups_action\":\"delete\"",
		"\"storage_layout_action\":\"preserve\"",
		"\"external_storage_action\":\"preserve\"",
		"\"network_storage_action\":\"preserve\"",
		"\"authentication_action\":\"reset_to_system\"",
		"\"password_action\":\"restore_default_and_expire\"",
		"\"sessions_action\":\"invalidate\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("factory reset plan missing %q: %s", want, body)
		}
	}
}

func TestAdminFactoryResetPlanRejectsChangedPreservationContract(t *testing.T) {
	old := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = old }()
	runAdminFactoryResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(strings.Replace(validFactoryResetPlanHelper, "\"external_storage_action\":\"preserve\"", "\"external_storage_action\":\"delete\"", 1)), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminFactoryResetPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/plan", "{}"))
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "\"code\":\"invalid_plan\"") {
		t.Fatalf("changed preservation contract status=%d: %s", rr.Code, rr.Body.String())
	}
}

func TestFactoryResetStorageActionsPreserveExternalAndNetwork(t *testing.T) {
	for _, scope := range []string{"external", "network", "unknown"} {
		if !validFactoryResetStorageAction(scope, "preserve") {
			t.Fatalf("%s storage was not accepted for preservation", scope)
		}
		if validFactoryResetStorageAction(scope, "delete") {
			t.Fatalf("%s storage deletion unexpectedly accepted", scope)
		}
	}
	if !validFactoryResetStorageAction("internal", "delete") {
		t.Fatal("internal storage deletion was rejected")
	}
	if validFactoryResetStorageAction("internal", "preserve") {
		t.Fatal("internal storage preservation unexpectedly accepted for full factory reset")
	}
}

func TestAdminFactoryResetApplyRequiresRealSystemPasswordBeforePlanning(t *testing.T) {
	oldReadMode := readAuthMode
	defer func() { readAuthMode = oldReadMode }()
	readAuthMode = func() (authMode, error) { return authModeSeparate, nil }
	oldAuth := systemAuthenticate
	defer func() { systemAuthenticate = oldAuth }()
	systemAuthenticate = func(username, password string) (systemauth.AuthResult, error) {
		if username != systemAdminUsername || password != "wrong-password" {
			t.Fatalf("unexpected system authentication request username=%q password=%q", username, password)
		}
		return systemauth.AuthResult{}, systemauth.ErrInvalidCredentials
	}

	oldHelper := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = oldHelper }()
	helperCalled := false
	runAdminFactoryResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		helperCalled = true
		return []byte(validFactoryResetPlanHelper), nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, _ := json.Marshal(adminFactoryResetApplyRequest{
		PlanFingerprint: exactFactoryResetFingerprint(t),
		SystemPassword:  "wrong-password",
	})
	rr := httptest.NewRecorder()
	s.adminFactoryResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/apply", string(body)))
	if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "\"code\":\"system_password_incorrect\"") {
		t.Fatalf("system password rejection status=%d: %s", rr.Code, rr.Body.String())
	}
	if helperCalled {
		t.Fatal("factory reset planner ran before system password proof")
	}
	if current, err := store.currentFactoryReset(); err != nil || current != nil {
		t.Fatalf("factory reset operation created without valid system password: %#v err=%v", current, err)
	}
}

func TestAdminFactoryResetApplySystemModeDoesNotRequireDuplicatePassword(t *testing.T) {
	oldReadMode := readAuthMode
	defer func() { readAuthMode = oldReadMode }()
	readAuthMode = func() (authMode, error) { return authModeSystem, nil }

	oldAuth := systemAuthenticate
	defer func() { systemAuthenticate = oldAuth }()
	systemAuthenticate = func(string, string) (systemauth.AuthResult, error) {
		t.Fatal("system password authentication must not run in System WebUI mode")
		return systemauth.AuthResult{}, nil
	}

	oldHelper := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = oldHelper }()
	runAdminFactoryResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(validFactoryResetPlanHelper), nil
	}

	oldWorker := startFactoryResetWorker
	defer func() { startFactoryResetWorker = oldWorker }()
	startFactoryResetWorker = func(_ *server, _ string, _ string) {}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, _ := json.Marshal(adminFactoryResetApplyRequest{PlanFingerprint: exactFactoryResetFingerprint(t)})
	rr := httptest.NewRecorder()
	s.adminFactoryResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/apply", string(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("system-mode factory reset status=%d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminFactoryResetApplyCreatesQueuedOperationFromExactPlan(t *testing.T) {
	oldAuth := systemAuthenticate
	defer func() { systemAuthenticate = oldAuth }()
	systemAuthenticate = func(username, password string) (systemauth.AuthResult, error) {
		if username != systemAdminUsername || password != "system-secret" {
			t.Fatalf("unexpected system authentication request username=%q password=%q", username, password)
		}
		return systemauth.AuthResult{}, nil
	}

	oldHelper := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = oldHelper }()
	runAdminFactoryResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(validFactoryResetPlanHelper), nil
	}

	oldWorker := startFactoryResetWorker
	defer func() { startFactoryResetWorker = oldWorker }()
	workerCalls := 0
	startFactoryResetWorker = func(_ *server, _ string, fingerprint string) {
		workerCalls++
		if fingerprint != exactFactoryResetFingerprint(t) {
			t.Fatalf("worker fingerprint = %q", fingerprint)
		}
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, err := json.Marshal(adminFactoryResetApplyRequest{
		PlanFingerprint: exactFactoryResetFingerprint(t),
		SystemPassword:  "system-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.adminFactoryResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/apply", string(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response adminFactoryResetApplyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Created || response.Operation == nil ||
		response.Operation.OperationType != operationTypeFactoryReset ||
		response.Operation.State != operationQueued {
		t.Fatalf("unexpected factory reset apply response: %#v", response)
	}
	if workerCalls != 1 {
		t.Fatalf("worker calls = %d, want 1", workerCalls)
	}
	if strings.Contains(rr.Body.String(), "system-secret") {
		t.Fatalf("factory reset response leaked system password: %s", rr.Body.String())
	}
}

func TestAdminFactoryResetApplyRequiresPlayerConfirmation(t *testing.T) {
	var plan adminFactoryResetPlanResponse
	if err := decodeAdminFactoryResetPlanResponse([]byte(validFactoryResetPlanHelper), &plan); err != nil {
		t.Fatal(err)
	}
	plan.PlayersOnline = 1
	plan.Players = []string{"Alex"}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := factoryResetPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}

	oldAuth := systemAuthenticate
	defer func() { systemAuthenticate = oldAuth }()
	systemAuthenticate = func(string, string) (systemauth.AuthResult, error) {
		return systemauth.AuthResult{}, nil
	}
	oldHelper := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = oldHelper }()
	runAdminFactoryResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		return payload, nil
	}

	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	body, _ := json.Marshal(adminFactoryResetApplyRequest{
		PlanFingerprint: fingerprint,
		SystemPassword:  "system-secret",
	})
	rr := httptest.NewRecorder()
	s.adminFactoryResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/apply", string(body)))
	if rr.Code != http.StatusConflict ||
		!strings.Contains(rr.Body.String(), "\"code\":\"players_online\"") ||
		!strings.Contains(rr.Body.String(), "Alex") {
		t.Fatalf("player confirmation status=%d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentFactoryReset(); err != nil || current != nil {
		t.Fatalf("factory reset operation created without player confirmation: %#v err=%v", current, err)
	}
}

func TestAdminFactoryResetApplySameFingerprintReconnectsWithoutPasswordOrPlanning(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)
	fingerprint := exactFactoryResetFingerprint(t)
	operation, _, err := store.beginFactoryReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}

	oldAuth := systemAuthenticate
	defer func() { systemAuthenticate = oldAuth }()
	authCalled := false
	systemAuthenticate = func(string, string) (systemauth.AuthResult, error) {
		authCalled = true
		return systemauth.AuthResult{}, errors.New("must not run")
	}
	oldHelper := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = oldHelper }()
	helperCalled := false
	runAdminFactoryResetHelper = func(_ context.Context, _ ...string) ([]byte, error) {
		helperCalled = true
		return nil, errors.New("must not run")
	}

	body, _ := json.Marshal(adminFactoryResetApplyRequest{PlanFingerprint: fingerprint})
	rr := httptest.NewRecorder()
	s.adminFactoryResetApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/apply", string(body)))
	if rr.Code != http.StatusOK ||
		!strings.Contains(rr.Body.String(), operation.OperationID) ||
		!strings.Contains(rr.Body.String(), "\"created\":false") {
		t.Fatalf("reconnect status=%d: %s", rr.Code, rr.Body.String())
	}
	if authCalled || helperCalled {
		t.Fatalf("reconnect repeated sensitive work: auth=%t helper=%t", authCalled, helperCalled)
	}
}

func TestExecuteFactoryResetReturnsToFreshFirstUseState(t *testing.T) {
	oldHelper := runAdminFactoryResetHelper
	defer func() { runAdminFactoryResetHelper = oldHelper }()
	runAdminFactoryResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "plan" {
			return []byte(validFactoryResetPlanHelper), nil
		}
		if len(args) == 2 && args[0] == "apply" && args[1] == "--confirm-players" {
			return []byte(`{"ok":true,"schema_version":"v1","mode":"factory","minecraft_was_configured":true,"data_path":"/var/lib/justvoxel/minecraft","data_scope":"internal","data_action":"delete","backup_path":"/var/lib/justvoxel/backups","backup_scope":"internal","backup_action":"delete","config_backups_action":"delete","storage_layout_action":"preserve","external_storage_action":"preserve","network_storage_action":"preserve","message":"Local JustVoxel runtime state was cleared."}`), nil
		}
		t.Fatalf("unexpected helper args: %#v", args)
		return nil, nil
	}

	oldResetAuth := resetFactoryAuthenticationState
	defer func() { resetFactoryAuthenticationState = oldResetAuth }()
	oldRestore := restoreSystemAdministratorFactoryCredential
	defer func() { restoreSystemAdministratorFactoryCredential = oldRestore }()
	oldReadMode := readAuthMode
	defer func() { readAuthMode = oldReadMode }()
	order := make([]string, 0, 2)
	resetFactoryAuthenticationState = func() error {
		order = append(order, "auth")
		return nil
	}
	restoreSystemAdministratorFactoryCredential = func(context.Context) error {
		order = append(order, "restore_default_password_and_expire")
		return nil
	}
	readAuthMode = func() (authMode, error) {
		return authModeSystem, nil
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
	if s.store == nil {
		t.Fatal("surface test server has no WebUI store")
	}
	if _, err := s.store.createWebUser("Alex", webUserRoleOperator, "operator-password"); err != nil {
		t.Fatal(err)
	}
	s.sessions["factory-session"] = session{
		Username: systemAdminUsername, Role: roleAdministrator,
		Created: time.Now(), LastSeen: time.Now(),
	}
	s.failures = []time.Time{time.Now()}
	s.lockTill = time.Now().Add(time.Minute)

	fingerprint := exactFactoryResetFingerprint(t)
	operation, _, err := store.beginFactoryReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := executeFactoryReset(context.Background(), s, operation.OperationID, fingerprint); err != nil {
		t.Fatal(err)
	}
	finished, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != operationSucceeded || finished.Stage != "complete" || finished.FinishedAt == "" {
		t.Fatalf("factory reset operation did not complete: %#v", finished)
	}
	if current, err := store.currentFactoryReset(); err != nil || current != nil {
		t.Fatalf("factory reset remained current after success: %#v err=%v", current, err)
	}
	if strings.Join(order, ",") != "auth,restore_default_password_and_expire" {
		t.Fatalf("identity reset order = %q, want auth,restore_default_password_and_expire", strings.Join(order, ","))
	}
	users, err := s.store.listWebUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("factory reset left WebUI users: %#v", users)
	}
	if len(s.sessions) != 0 || len(s.failures) != 0 || !s.lockTill.IsZero() {
		t.Fatalf("factory reset did not invalidate login state: sessions=%d failures=%d lock=%v", len(s.sessions), len(s.failures), s.lockTill)
	}
}

func TestRestoreSystemAdministratorFactoryCredential(t *testing.T) {
	for _, test := range []struct {
		name      string
		failCall  int
		wantCalls int
	}{
		{name: "locked bootstrap installation fails", failCall: 1, wantCalls: 1},
		{name: "expiration fails", failCall: 2, wantCalls: 2},
		{name: "unlock fails", failCall: 3, wantCalls: 3},
		{name: "success", wantCalls: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldRun := runFactoryCredentialCommand
			defer func() { runFactoryCredentialCommand = oldRun }()
			calls := 0
			runFactoryCredentialCommand = func(_ context.Context, path string, args ...string) error {
				calls++
				switch calls {
				case 1:
					if path != "/usr/sbin/usermod" || len(args) != 3 || args[0] != "--password" || args[1] != "!"+factoryBootstrapPasswordHash || args[2] != systemAdminUsername {
						t.Fatal("locked factory password installation command changed")
					}
				case 2:
					if path != "/usr/bin/chage" || len(args) != 3 || args[0] != "-d" || args[1] != "0" || args[2] != systemAdminUsername {
						t.Fatal("factory password expiration command changed")
					}
				case 3:
					if path != "/usr/sbin/usermod" || len(args) != 2 || args[0] != "--unlock" || args[1] != systemAdminUsername {
						t.Fatal("factory password unlock command changed")
					}
				default:
					t.Fatal("unexpected factory credential command")
				}
				if calls == test.failCall {
					return errors.New("command failed")
				}
				return nil
			}
			err := restoreSystemAdministratorFactoryCredential(context.Background())
			if (err != nil) != (test.failCall != 0) {
				t.Fatalf("credential restoration error = %v", err)
			}
			if calls != test.wantCalls {
				t.Fatalf("credential command calls = %d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestExecuteFactoryResetCredentialFailureKeepsSessions(t *testing.T) {
	for _, failure := range []string{"locked bootstrap installation", "expiration", "unlock"} {
		t.Run(failure, func(t *testing.T) {
			oldHelper := runAdminFactoryResetHelper
			defer func() { runAdminFactoryResetHelper = oldHelper }()
			runAdminFactoryResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
				if len(args) == 1 && args[0] == "plan" {
					return []byte(validFactoryResetPlanHelper), nil
				}
				if len(args) == 2 && args[0] == "apply" && args[1] == "--confirm-players" {
					return []byte(`{"ok":true,"schema_version":"v1","mode":"factory","minecraft_was_configured":true,"data_path":"/var/lib/justvoxel/minecraft","data_scope":"internal","data_action":"delete","backup_path":"/var/lib/justvoxel/backups","backup_scope":"internal","backup_action":"delete","config_backups_action":"delete","storage_layout_action":"preserve","external_storage_action":"preserve","network_storage_action":"preserve"}`), nil
				}
				t.Fatalf("unexpected factory reset helper args: %#v", args)
				return nil, nil
			}
			oldResetAuth := resetFactoryAuthenticationState
			defer func() { resetFactoryAuthenticationState = oldResetAuth }()
			resetFactoryAuthenticationState = func() error { return nil }
			oldRestore := restoreSystemAdministratorFactoryCredential
			defer func() { restoreSystemAdministratorFactoryCredential = oldRestore }()
			restoreSystemAdministratorFactoryCredential = func(context.Context) error {
				return errors.New("factory password " + failure + " failed")
			}

			s := surfaceTestServer(t, roleAdministrator)
			store := openTestOperationStore(t)
			attachTestOperationStore(t, s, store)
			s.sessions["factory-session"] = session{
				Username: systemAdminUsername, Role: roleAdministrator,
				Created: time.Now(), LastSeen: time.Now(),
			}
			s.failures = []time.Time{time.Now()}
			s.lockTill = time.Now().Add(time.Minute)
			baselineSessionCount := len(s.sessions)
			baselineFailureCount := len(s.failures)
			baselineLockTill := s.lockTill
			fingerprint := exactFactoryResetFingerprint(t)
			operation, _, err := store.beginFactoryReset(fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if err := executeFactoryReset(context.Background(), s, operation.OperationID, fingerprint); err == nil {
				t.Fatal("factory reset succeeded after credential failure")
			}
			finished, err := store.get(operation.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if finished.State != operationNeedsAttention || finished.Stage != "password_reset_failed" {
				t.Fatalf("factory reset credential failure state = %#v", finished)
			}
			if len(s.sessions) != baselineSessionCount {
				t.Fatalf("factory reset changed session count after credential failure: got %d, want %d", len(s.sessions), baselineSessionCount)
			}
			if _, ok := s.sessions["token"]; !ok {
				t.Fatal("factory reset removed the existing session after credential failure")
			}
			if _, ok := s.sessions["factory-session"]; !ok {
				t.Fatal("factory reset removed the factory session after credential failure")
			}
			if len(s.failures) != baselineFailureCount || s.lockTill.IsZero() || !s.lockTill.Equal(baselineLockTill) {
				t.Fatal("factory reset invalidated sessions or cleared failures before success")
			}
		})
	}
}

func TestAdminFactoryResetResolveKeepsCurrentStateAndReleasesLocks(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	operation, _, err := store.beginFactoryReset(exactFactoryResetFingerprint(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "reset_failed", "Factory reset stopped before completion."); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(adminFactoryResetResolveRequest{OperationID: operation.OperationID, KeepCurrentState: true})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.adminFactoryResetResolve(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/resolve", string(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve status=%d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"state":"resolved"`) {
		t.Fatalf("resolve response did not report resolved state: %s", rr.Body.String())
	}
	if current, err := store.currentFactoryReset(); err != nil || current != nil {
		t.Fatalf("factory reset remained current after resolve: %#v err=%v", current, err)
	}
	if _, _, err := store.beginMigrationExport(testSetupFingerprint); err != nil {
		t.Fatalf("migration lock remained held after resolve: %v", err)
	}
}

func TestAdminFactoryResetResolveRequiresExplicitKeepCurrentState(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	store := openTestOperationStore(t)
	attachTestOperationStore(t, s, store)

	operation, _, err := store.beginFactoryReset(exactFactoryResetFingerprint(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationNeedsAttention, "reset_failed", "Factory reset stopped before completion."); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(adminFactoryResetResolveRequest{OperationID: operation.OperationID})
	rr := httptest.NewRecorder()
	s.adminFactoryResetResolve(rr, surfaceRequest(http.MethodPost, "/v1/admin/reset/factory/resolve", string(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing keep-current confirmation status=%d: %s", rr.Code, rr.Body.String())
	}
	if current, err := store.currentFactoryReset(); err != nil || current == nil {
		t.Fatalf("failed reset was released without explicit confirmation: %#v err=%v", current, err)
	}
}
