package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func operatorServerForStore(store *webUIStore, user webUser) *server {
	now := time.Now()
	return &server{
		store: store,
		sessions: map[string]session{
			"token": {
				Username:   user.Username,
				Role:       roleOperator,
				AuthSource: authSourceWebUI,
				WebUserID:  user.ID,
				Created:    now,
				LastSeen:   now,
			},
		},
	}
}

func instanceRestartFixture(t *testing.T) (*server, *instanceActivationFixture, *webUIStore, webUser) {
	t.Helper()
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	old, oldLock := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes = old; pluginRuntimeLockPath = oldLock })
	if err := f.backend.action(context.Background(), f.configs[0].ID, "start"); err != nil {
		t.Fatal(err)
	}
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("RestartOperator", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	s := operatorServerForStore(store, user)
	s.operations = openTestOperationStore(t)
	return s, f, store, user
}
func instanceRestartRequest(s *server, id, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/restart", body))
	return rr
}
func TestOperatorRestartConsumesAllowanceAndCreatesNotification(t *testing.T) {
	s, f, store, user := instanceRestartFixture(t)
	id, sibling := f.configs[0].ID, f.configs[1].ID
	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }
	f.calls = nil
	for i, want := range []int{200, 429, 200, 409} {
		if i >= 2 {
			now = now.Add(16 * time.Minute)
		}
		rr := instanceRestartRequest(s, id, `{}`)
		if rr.Code != want {
			t.Fatalf("step %d: %d %s", i, rr.Code, rr.Body.String())
		}
	}
	used, _, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 2 {
		t.Fatal(used, err)
	}
	var audits, notifications, badTargets int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='minecraft_restart' AND target=? AND success=1`, id).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='minecraft_restart' AND target<>?`, id).Scan(&badTargets); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind='operator_restart_limit' AND user_id=? AND resolved_at IS NULL`, user.ID).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if audits != 2 || notifications != 1 || badTargets != 0 {
		t.Fatal(audits, notifications, badTargets)
	}
	assertSiblingAdmissionObservations(t, f.calls, sibling, 2)
	starts := 0
	for _, call := range f.calls {
		if strings.Contains(call, "systemctl --no-block start -- minecraft-"+id+".service") {
			starts++
		}
	}
	if starts != 2 {
		t.Fatal("accepted starts", starts, f.calls)
	}
}
func TestOperatorRestartConfirmationDoesNotConsumeAllowance(t *testing.T) {
	s, f, store, user := instanceRestartFixture(t)
	id := f.configs[0].ID
	if id != "jv-a1b2c3" {
		t.Fatal(id)
	}
	f.players = "There are 1 of a max of 20 players online: Alex"
	f.calls = nil
	rr := instanceRestartRequest(s, id, `{"confirm_players":false}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"confirmation_required":true`) || !strings.Contains(rr.Body.String(), `"reason":"players_online"`) || !strings.Contains(rr.Body.String(), `"Alex"`) || !strings.Contains(rr.Body.String(), `"id":"jv-a1b2c3"`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	used, last, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 0 || last != nil {
		t.Fatal(used, last, err)
	}
	var notifications int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind='operator_restart_limit'`).Scan(&notifications); err != nil || notifications != 0 {
		t.Fatal(notifications, err)
	}
	var globalLast string
	if err := store.db.QueryRow(`SELECT COALESCE(last_restart_at, '') FROM operator_global_state WHERE id=1`).Scan(&globalLast); err != nil || globalLast != "" {
		t.Fatal(globalLast, err)
	}
	queriedSelected := false
	for _, call := range f.calls {
		if call == "podman exec minecraft-"+id+" rcon-cli list" {
			queriedSelected = true
		}
		if strings.Contains(call, "say Server shutting down") || strings.Contains(call, "systemctl stop ") || strings.Contains(call, "systemctl restart ") || strings.Contains(call, "systemctl --no-block stop") || strings.Contains(call, "systemctl --no-block restart") || strings.Contains(call, "systemctl --no-block start") || strings.Contains(call, "minecraft-"+f.configs[1].ID) || strings.Contains(call, "minecraft.service") {
			t.Error("unconfirmed restart issued a mutation or sibling command", call)
		}
	}
	if !queriedSelected {
		t.Error("selected instance player query was not issued", f.calls)
	}
}

func TestOperatorConfirmedRestartConsumesAllowanceOnce(t *testing.T) {
	s, f, store, user := instanceRestartFixture(t)
	id := f.configs[0].ID
	f.players = "There are 1 of a max of 20 players online: Alex"
	base := f.backend.command
	announcements := 0
	elapsed := time.Duration(0)
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "podman" && len(args) == 4 && strings.HasPrefix(args[3], "say ") {
			if args[1] != "minecraft-"+id {
				return nil, fmt.Errorf("wrong countdown target")
			}
			announcements++
			return nil, nil
		}
		if command == "systemctl" && len(args) == 4 && args[0] == "--no-block" && args[1] == "stop" {
			if args[3] != "minecraft-"+id+".service" {
				return nil, fmt.Errorf("wrong stop target")
			}
			f.states[id] = "deactivating"
			return nil, nil
		}
		return base(ctx, command, args...)
	}
	f.calls = nil
	// Model elapsed countdown time without altering production timing.
	f.backend.pause = func(_ context.Context, duration time.Duration) error {
		elapsed += duration
		if elapsed >= 60*time.Second {
			f.states[id] = "inactive"
			f.containers[id] = "exited"
		}
		return nil
	}
	rr := instanceRestartRequest(s, id, `{"confirm_players":true}`)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	used, last, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 1 || last == nil || (announcements != 9 || elapsed != 60*time.Second) {
		t.Fatal(used, last, announcements, err)
	}
	var audits int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='minecraft_restart' AND target=? AND success=1`, id).Scan(&audits); err != nil || audits != 1 {
		t.Fatal(audits, err)
	}
	assertSiblingAdmissionObservations(t, f.calls, f.configs[1].ID, 1)
	rr = instanceRestartRequest(s, id, `{"confirm_players":true}`)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatal(rr.Code, rr.Body.String())
	}
	used, _, err = store.operatorRestartUsage(user.ID)
	if err != nil || used != 1 {
		t.Fatal(used, err)
	}
}

func TestOperatorFailedPresenceCheckDoesNotConsumeAllowance(t *testing.T) {
	s, f, store, user := instanceRestartFixture(t)
	f.failPlayers = true
	rr := instanceRestartRequest(s, f.configs[0].ID, `{"confirm_players":true}`)
	if rr.Code < 400 {
		t.Fatal(rr.Code)
	}
	used, last, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 0 || last != nil {
		t.Fatal(used, last, err)
	}
}

func TestOperatorRestartGlobalCooldownAppliesAcrossUsers(t *testing.T) {
	s, f, store, _ := instanceRestartFixture(t)
	id := f.configs[0].ID
	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }
	if rr := instanceRestartRequest(s, id, `{}`); rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	second, err := store.createWebUser("SecondOperator", webUserRoleOperator, "second-password")
	if err != nil {
		t.Fatal(err)
	}
	other := operatorServerForStore(store, second)
	other.operations = s.operations
	now = now.Add(5 * time.Minute)
	if rr := instanceRestartRequest(other, id, `{}`); rr.Code != 429 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	used, _, err := store.operatorRestartUsage(second.ID)
	if err != nil || used != 0 {
		t.Fatal(used, err)
	}
}
func TestInstanceRestartRejectsInvalidSelectionBeforeAccounting(t *testing.T) {
	s, f, store, user := instanceRestartFixture(t)
	f.calls = nil
	for _, tc := range []struct {
		id   string
		code int
	}{{"original", http.StatusBadRequest}, {"invalid", http.StatusBadRequest}, {"jv-absent1", http.StatusNotFound}} {
		if rr := instanceRestartRequest(s, tc.id, `{}`); rr.Code != tc.code {
			t.Fatal(tc.id, rr.Code, rr.Body.String())
		}
	}
	if len(f.calls) != 0 {
		t.Fatal(f.calls)
	}
	used, last, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 0 || last != nil {
		t.Fatal(used, last, err)
	}
	var audits int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='minecraft_restart' AND success=1`).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("invalid selection recorded successful restart", audits, err)
	}
}
func TestAdministratorInstanceRestartDoesNotConsumeOperatorAllowance(t *testing.T) {
	s, f, store, user := instanceRestartFixture(t)
	s.sessions["token"] = session{Username: systemAdminUsername, Role: roleAdministrator, AuthSource: authSourceSystem, Created: time.Now(), LastSeen: time.Now()}
	if rr := instanceRestartRequest(s, f.configs[0].ID, `{}`); rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	used, last, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 0 || last != nil {
		t.Fatal(used, last, err)
	}
}

func TestAdministratorResetRestoresAllowanceButNotCooldown(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	user, err := store.createWebUser("Maya", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	operator := session{Username: user.Username, Role: roleOperator, AuthSource: authSourceWebUI, WebUserID: user.ID}
	admin := session{Username: systemAdminUsername, Role: roleAdministrator, AuthSource: authSourceSystem}

	guard, availability, err := store.beginOperatorRestart(user.ID)
	if err != nil || !availability.Allowed {
		t.Fatalf("first allowance unavailable: %#v err=%v", availability, err)
	}
	if _, err := guard.acceptTarget(operator, "jv-a1b2c3"); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return base.Add(16 * time.Minute) }
	guard, availability, err = store.beginOperatorRestart(user.ID)
	if err != nil || !availability.Allowed {
		t.Fatalf("second allowance unavailable: %#v err=%v", availability, err)
	}
	if _, err := guard.acceptTarget(operator, "jv-a1b2c3"); err != nil {
		t.Fatal(err)
	}
	_, lastBefore, err := store.operatorRestartUsage(user.ID)
	if err != nil || lastBefore == nil {
		t.Fatalf("missing last restart before reset: %v %v", lastBefore, err)
	}

	store.now = func() time.Time { return base.Add(17 * time.Minute) }
	if err := store.resetOperatorRestartAllowance(user.ID, admin); err != nil {
		t.Fatal(err)
	}
	used, lastAfter, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 0 || lastAfter == nil || !lastAfter.Equal(*lastBefore) {
		t.Fatalf("reset state used=%d lastBefore=%v lastAfter=%v err=%v", used, lastBefore, lastAfter, err)
	}
	var openNotifications int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind = 'operator_restart_limit' AND user_id = ? AND resolved_at IS NULL`, user.ID).Scan(&openNotifications); err != nil {
		t.Fatal(err)
	}
	if openNotifications != 0 {
		t.Fatalf("restart-limit notification remained open after reset: %d", openNotifications)
	}

	guard, availability, err = store.beginOperatorRestart(user.ID)
	if guard != nil {
		guard.cancel()
	}
	if err != nil || availability.Reason != "restart_cooldown" {
		t.Fatalf("reset unexpectedly cleared cooldown: %#v err=%v", availability, err)
	}
}
