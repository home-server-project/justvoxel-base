package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configureAdminMutationStore(t *testing.T, s *server) {
	t.Helper()
	oldExclusion, oldMaintenance := instanceRestoreExclusionPath, pluginRuntimeLockPath
	instanceRestoreExclusionPath = filepath.Join(t.TempDir(), "exclusion.lock")
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() {
		instanceRestoreExclusionPath, pluginRuntimeLockPath = oldExclusion, oldMaintenance
	})
	s.operations = openTestOperationStore(t)
}

func configurationApplyResponse(s *server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.adminConfigurationApply(w, surfaceRequest("POST", "/v1/admin/configuration/apply", body))
	return w
}

func assertConfigurationAudit(t *testing.T, s *server, success, failure int) {
	t.Helper()
	var successes, failures int
	err := s.store.db.QueryRow(`SELECT COALESCE(SUM(success),0), COALESCE(SUM(1-success),0) FROM audit_events WHERE action='update_minecraft_configuration'`).Scan(&successes, &failures)
	if err != nil || successes != success || failures != failure {
		t.Fatalf("audit successes=%d failures=%d: %v", successes, failures, err)
	}
}

func TestDeleteMutationExclusionBlocksLegacyConfigurationApply(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	configureAdminMutationStore(t, s)
	old := runAdminConfigurationHelper
	t.Cleanup(func() { runAdminConfigurationHelper = old })
	// Any helper invocation could mutate all three resources. Busy must refuse
	// before calling it, preserving their actual temporary filesystem contents.
	paths := []string{}
	for _, name := range []string{"configuration", "runtime", "firewall"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	calls := 0
	runAdminConfigurationHelper = func(context.Context, string, []byte) ([]byte, error) {
		calls++
		for _, path := range paths {
			if err := os.WriteFile(path, []byte("mutated"), 0600); err != nil {
				return nil, err
			}
		}
		return []byte(`{"ok":true,"applied":true}`), nil
	}
	// This is the exact orchestration exclusion acquired by instanceDelete.
	release, err := s.claimMinecraftMutation()
	if err != nil {
		t.Fatal(err)
	}
	w := configurationApplyResponse(s, `{}`)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || calls != 0 {
		t.Fatal(w.Code, calls, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Another Minecraft maintenance operation is active.") {
		t.Fatal(w.Body.String())
	}
	for _, path := range paths {
		v, err := os.ReadFile(path)
		if err != nil || string(v) != "unchanged" {
			t.Fatal("mutation on conflict", path, err)
		}
	}
	for _, private := range []string{instanceRestoreExclusionPath, pluginRuntimeLockPath, "operation-store", "systemctl"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private conflict details")
		}
	}
	assertConfigurationAudit(t, s, 0, 1)
}

func TestLegacyConfigurationApplyBlocksIndependentDelete(t *testing.T) {
	d := newDeleteFixture(t)
	s := surfaceTestServer(t, roleAdministrator)
	configureAdminMutationStore(t, s)
	oldBackend, oldHelper := instanceRuntimes, runAdminConfigurationHelper
	instanceRuntimes = d.f.backend
	t.Cleanup(func() { instanceRuntimes, runAdminConfigurationHelper = oldBackend, oldHelper })
	id := d.f.configs[0].ID
	dir, env, quadlet := d.f.backend.paths(id)
	world := filepath.Join(instanceStorageFor(d.f.snapshot, id).DataPath, "level.dat")
	if err := os.WriteFile(world, []byte("world"), 0600); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, path := range []string{world, env, quadlet, filepath.Join(dir, "config.json"), filepath.Join(d.f.backend.store.directory, id+".json")} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = string(raw)
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	runAdminConfigurationHelper = func(ctx context.Context, action string, _ []byte) ([]byte, error) {
		if action != "apply" {
			return nil, errors.New("unexpected action")
		}
		close(entered)
		select {
		case <-finish:
			return []byte(`{"ok":true,"applied":true}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- configurationApplyResponse(s, `{}`) }()
	defer func() {
		close(finish)
		select {
		case w := <-done:
			if w.Code != 200 {
				t.Error(w.Code, w.Body.String())
			}
		case <-time.After(3 * time.Second):
			t.Error("Apply did not release exclusion")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Apply never entered helper")
	}
	commands := len(d.f.calls)
	r := surfaceRequest("POST", "/", `{"confirm_name":"JV Paper"}`)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.instanceDelete(w, r)
	if w.Code != 409 || d.removed || len(d.f.calls) != commands {
		t.Fatal("Delete passed exclusion", w.Code)
	}
	for path, expected := range before {
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != expected {
			t.Fatal("selected instance changed", path, err)
		}
	}
	if _, err := os.Lstat(d.f.backend.deletePath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Delete published intent", err)
	}
}

func TestLegacyConfigurationNormalApplyAndFailureRelease(t *testing.T) {
	for _, result := range []string{"success", "failure", "rejected", "invalid", "confirmation"} {
		t.Run(result, func(t *testing.T) {
			s := surfaceTestServer(t, roleAdministrator)
			configureAdminMutationStore(t, s)
			old := runAdminConfigurationHelper
			t.Cleanup(func() { runAdminConfigurationHelper = old })
			calls := 0
			runAdminConfigurationHelper = func(_ context.Context, action string, payload []byte) ([]byte, error) {
				calls++
				if action != "apply" || !strings.Contains(string(payload), `"whitelist_enabled":false`) {
					t.Fatal(action, string(payload))
				}
				switch result {
				case "failure":
					return nil, errors.New("private failure")
				case "rejected":
					return []byte(`{"ok":false,"error":"Settings rejected"}`), nil
				case "invalid":
					return []byte(`{`), nil
				case "confirmation":
					return []byte(`{"ok":true,"applied":false,"confirmation_required":true}`), nil
				}
				return []byte(`{"ok":true,"applied":true,"restarted":true,"memory_restart_required":true,"message":"Memory settings saved"}`), nil
			}
			w := configurationApplyResponse(s, `{"whitelist_enabled":false}`)
			want := map[string]int{"success": 200, "failure": 503, "rejected": 400, "invalid": 500, "confirmation": 200}[result]
			if w.Code != want || calls != 1 {
				t.Fatal(w.Code, calls, w.Body.String())
			}
			if result == "success" {
				for _, field := range []string{`"applied":true`, `"restarted":true`, `"memory_restart_required":true`, `"message":"Memory settings saved"`} {
					if !strings.Contains(w.Body.String(), field) {
						t.Fatal(w.Body.String())
					}
				}
				assertConfigurationAudit(t, s, 1, 0)
			} else {
				var successes int
				if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE success=1 AND action='update_minecraft_configuration'`).Scan(&successes); err != nil || successes != 0 {
					t.Fatal("failed Apply audited as success", err)
				}
			}
			// A real later Apply must enter; checking only a boolean is insufficient.
			runAdminConfigurationHelper = func(context.Context, string, []byte) ([]byte, error) {
				return []byte(`{"ok":true,"applied":true}`), nil
			}
			if next := configurationApplyResponse(s, `{}`); next.Code != 200 {
				t.Fatal("exclusion leaked", next.Code, next.Body.String())
			}
		})
	}
}

func TestLegacyConfigurationMalformedAndUnauthorizedNeverClaim(t *testing.T) {
	for _, scenario := range []string{"malformed", "unknown_field", "unauthorized"} {
		t.Run(scenario, func(t *testing.T) {
			role, body, want := roleAdministrator, `{`, 400
			if scenario == "unknown_field" {
				body = `{"path":"/private"}`
			}
			if scenario == "unauthorized" {
				role, body, want = roleViewer, `{}`, 403
			}
			s := surfaceTestServer(t, role)
			configureAdminMutationStore(t, s)
			old := runAdminConfigurationHelper
			t.Cleanup(func() { runAdminConfigurationHelper = old })
			runAdminConfigurationHelper = func(context.Context, string, []byte) ([]byte, error) {
				t.Error("invalid request reached helper")
				return nil, errors.New("unexpected helper")
			}
			// While held, these failures must still be validation/auth responses,
			// proving they never attempt to acquire the mutation exclusion.
			release, err := s.claimMinecraftMutation()
			if err != nil {
				t.Fatal(err)
			}
			w := configurationApplyResponse(s, body)
			if err := release(); err != nil {
				t.Fatal(err)
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			release, err = s.claimMinecraftMutation()
			if err != nil {
				t.Fatal("invalid request leaked lock", err)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLegacyConfigurationPlanDoesNotClaimMutationExclusion(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	configureAdminMutationStore(t, s)
	old := runAdminConfigurationHelper
	t.Cleanup(func() { runAdminConfigurationHelper = old })
	calls := 0
	runAdminConfigurationHelper = func(_ context.Context, stringAction string, _ []byte) ([]byte, error) {
		calls++
		if stringAction != "plan" {
			t.Fatal(stringAction)
		}
		return []byte(`{"ok":true,"applied":false}`), nil
	}
	release, err := s.claimMinecraftMutation()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	w := httptest.NewRecorder()
	s.adminConfigurationPlan(w, surfaceRequest("POST", "/v1/admin/configuration/plan", `{}`))
	if w.Code != 200 || calls != 1 {
		t.Fatal("Plan reserved destructive exclusion", w.Code, calls)
	}
	assertConfigurationAudit(t, s, 0, 0)
}

func TestLegacyApplyDoesNotReuseAnotherOperationsProcessTrackedClaims(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	configureAdminMutationStore(t, s)
	old := runAdminConfigurationHelper
	t.Cleanup(func() { runAdminConfigurationHelper = old })
	calls := 0
	runAdminConfigurationHelper = func(context.Context, string, []byte) ([]byte, error) {
		calls++
		return []byte(`{"ok":true,"applied":true}`), nil
	}
	o := s.operations
	o.mu.Lock()
	err := o.acquireMinecraftResetLocks()
	if err != nil {
		o.mu.Unlock()
		t.Fatal(err)
	}
	o.currentSetupID = "another-active-operation"
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.currentSetupID = ""
		if err := o.releaseMinecraftResetLocks(); err != nil {
			t.Error(err)
		}
		o.mu.Unlock()
	}()
	w := configurationApplyResponse(s, `{}`)
	if w.Code != 409 || calls != 0 {
		t.Fatal("Apply reused process-tracked claims", w.Code, calls)
	}
	o.mu.Lock()
	stillOwned := o.setupLockHeld && o.restoreLockHeld && o.dataMigrationLockHeld && o.migrationLockHeld
	o.mu.Unlock()
	if !stillOwned {
		t.Fatal("busy Apply released another operation's claims")
	}
}
