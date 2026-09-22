package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func withBackupTestDependencies(t *testing.T) {
	t.Helper()
	oldState := backupServiceState
	oldRequest := requestManualBackup
	t.Cleanup(func() {
		backupServiceState = oldState
		requestManualBackup = oldRequest
	})
	backupServiceState = func(context.Context) (string, error) { return "inactive", nil }
}

func TestLocalRootCanRequestManualBackupWithoutBearerSession(t *testing.T) {
	withBackupTestDependencies(t)
	s := &server{sessions: make(map[string]session)}
	calls := 0
	requestManualBackup = func(context.Context) error {
		calls++
		return nil
	}

	req := requestWithPeerUID(http.MethodPost, "http://unix/v1/backups/manual", 0)
	rr := httptest.NewRecorder()
	s.manualBackup(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("local root manual backup returned %d: %s", rr.Code, rr.Body.String())
	}
	if calls != 1 {
		t.Fatalf("local root backup service calls = %d, want 1", calls)
	}
	if !strings.Contains(rr.Body.String(), `"ok":true`) || !strings.Contains(rr.Body.String(), "Manual backup requested.") {
		t.Fatalf("unexpected local root backup response: %s", rr.Body.String())
	}
}

func TestOperatorManualBackupConsumesAllowanceAndCreatesNotification(t *testing.T) {
	withBackupTestDependencies(t)
	store, _ := openTestWebUIStore(t)
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	user, err := store.createWebUser("BackupOperator", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	s := operatorServerForStore(store, user)

	calls := 0
	requestManualBackup = func(context.Context) error {
		calls++
		return nil
	}

	rr := httptest.NewRecorder()
	s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("first backup returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"backup_used":1`) {
		t.Fatalf("first backup response missing usage: %s", rr.Body.String())
	}
	used, _, err := store.operatorBackupUsage(user.ID)
	if err != nil || used != 1 {
		t.Fatalf("usage after first backup = %d, err=%v", used, err)
	}

	rr = httptest.NewRecorder()
	s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("cooldown backup returned %d: %s", rr.Code, rr.Body.String())
	}
	if calls != 1 {
		t.Fatalf("backup service calls during cooldown = %d, want 1", calls)
	}

	store.now = func() time.Time { return base.Add(16 * time.Minute) }
	rr = httptest.NewRecorder()
	s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("second accepted backup returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"backup_used":2`) {
		t.Fatalf("second backup response missing 2/2 usage: %s", rr.Body.String())
	}
	if calls != 2 {
		t.Fatalf("accepted backup service calls = %d, want 2", calls)
	}

	var openNotifications int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind = 'operator_backup_limit' AND user_id = ? AND resolved_at IS NULL`, user.ID).Scan(&openNotifications); err != nil {
		t.Fatal(err)
	}
	if openNotifications != 1 {
		t.Fatalf("open backup-limit notifications = %d, want 1", openNotifications)
	}

	store.now = func() time.Time { return base.Add(32 * time.Minute) }
	rr = httptest.NewRecorder()
	s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusConflict {
		t.Fatalf("exhausted backup returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"administrator_reset_required":true`) {
		t.Fatalf("exhausted response missing reset requirement: %s", rr.Body.String())
	}
	if calls != 2 {
		t.Fatalf("backup service started after allowance exhaustion: %d calls", calls)
	}
}

func TestOperatorManualBackupFailureDoesNotConsumeAllowance(t *testing.T) {
	withBackupTestDependencies(t)
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("FailedBackupOperator", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	s := operatorServerForStore(store, user)
	requestManualBackup = func(context.Context) error { return errors.New("systemd rejected request") }

	rr := httptest.NewRecorder()
	s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed backup returned %d: %s", rr.Code, rr.Body.String())
	}
	used, last, err := store.operatorBackupUsage(user.ID)
	if err != nil || used != 0 || last != nil {
		t.Fatalf("failed backup consumed allowance: used=%d last=%v err=%v", used, last, err)
	}
}

func TestOperatorManualBackupGlobalCooldownAppliesAcrossUsers(t *testing.T) {
	withBackupTestDependencies(t)
	store, _ := openTestWebUIStore(t)
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	first, err := store.createWebUser("BackupOne", webUserRoleOperator, "first-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.createWebUser("BackupTwo", webUserRoleOperator, "second-password")
	if err != nil {
		t.Fatal(err)
	}
	firstServer := operatorServerForStore(store, first)
	secondServer := operatorServerForStore(store, second)
	calls := 0
	requestManualBackup = func(context.Context) error {
		calls++
		return nil
	}

	rr := httptest.NewRecorder()
	firstServer.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("first operator backup returned %d: %s", rr.Code, rr.Body.String())
	}

	store.now = func() time.Time { return base.Add(5 * time.Minute) }
	rr = httptest.NewRecorder()
	secondServer.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second operator bypassed global cooldown: %d %s", rr.Code, rr.Body.String())
	}
	if calls != 1 {
		t.Fatalf("backup service calls = %d, want 1", calls)
	}
	used, _, err := store.operatorBackupUsage(second.ID)
	if err != nil || used != 0 {
		t.Fatalf("global cooldown consumed second user's allowance: used=%d err=%v", used, err)
	}
}

func TestManualBackupAlreadyRunningBlocksWithoutQuotaConsumption(t *testing.T) {
	withBackupTestDependencies(t)
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("BusyBackupOperator", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	s := operatorServerForStore(store, user)
	backupServiceState = func(context.Context) (string, error) { return "active", nil }
	called := false
	requestManualBackup = func(context.Context) error { called = true; return nil }

	rr := httptest.NewRecorder()
	s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"reason":"backup_in_progress"`) {
		t.Fatalf("busy backup response = %d %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("backup service start was attempted while backup was already running")
	}
	used, _, err := store.operatorBackupUsage(user.ID)
	if err != nil || used != 0 {
		t.Fatalf("busy backup consumed allowance: used=%d err=%v", used, err)
	}
}

func TestViewerCannotRequestManualBackup(t *testing.T) {
	withBackupTestDependencies(t)
	s := roleServerForTest(roleViewer)
	called := false
	requestManualBackup = func(context.Context) error { called = true; return nil }

	rr := httptest.NewRecorder()
	s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer manual backup returned %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("viewer caused backup service start")
	}
}

func TestAdministratorManualBackupIsNotQuotaLimited(t *testing.T) {
	withBackupTestDependencies(t)
	store, _ := openTestWebUIStore(t)
	s := adminServerForTest()
	s.store = store
	calls := 0
	requestManualBackup = func(context.Context) error { calls++; return nil }

	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		s.manualBackup(rr, authorizedRequest(http.MethodPost, "/v1/backups/manual", ""))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("administrator backup %d returned %d: %s", i+1, rr.Code, rr.Body.String())
		}
	}
	if calls != 3 {
		t.Fatalf("administrator backup service calls = %d, want 3", calls)
	}
}

func TestAdministratorResetRestoresBackupAllowanceButNotCooldown(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	user, err := store.createWebUser("ResetBackupOperator", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	operator := session{Username: user.Username, Role: roleOperator, AuthSource: authSourceWebUI, WebUserID: user.ID}
	admin := session{Username: systemAdminUsername, Role: roleAdministrator, AuthSource: authSourceSystem}

	guard, availability, err := store.beginOperatorBackup(user.ID)
	if err != nil || !availability.Allowed {
		t.Fatalf("first allowance unavailable: %#v err=%v", availability, err)
	}
	if _, err := guard.accept(operator); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return base.Add(16 * time.Minute) }
	guard, availability, err = store.beginOperatorBackup(user.ID)
	if err != nil || !availability.Allowed {
		t.Fatalf("second allowance unavailable: %#v err=%v", availability, err)
	}
	if _, err := guard.accept(operator); err != nil {
		t.Fatal(err)
	}
	_, lastBefore, err := store.operatorBackupUsage(user.ID)
	if err != nil || lastBefore == nil {
		t.Fatalf("missing last backup before reset: %v %v", lastBefore, err)
	}

	store.now = func() time.Time { return base.Add(17 * time.Minute) }
	if err := store.resetOperatorBackupAllowance(user.ID, admin); err != nil {
		t.Fatal(err)
	}
	used, lastAfter, err := store.operatorBackupUsage(user.ID)
	if err != nil || used != 0 || lastAfter == nil || !lastAfter.Equal(*lastBefore) {
		t.Fatalf("reset state used=%d lastBefore=%v lastAfter=%v err=%v", used, lastBefore, lastAfter, err)
	}
	var openNotifications int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind = 'operator_backup_limit' AND user_id = ? AND resolved_at IS NULL`, user.ID).Scan(&openNotifications); err != nil {
		t.Fatal(err)
	}
	if openNotifications != 0 {
		t.Fatalf("backup-limit notification remained open after reset: %d", openNotifications)
	}

	guard, availability, err = store.beginOperatorBackup(user.ID)
	if guard != nil {
		guard.cancel()
	}
	if err != nil || availability.Reason != "backup_cooldown" {
		t.Fatalf("reset unexpectedly cleared backup cooldown: %#v err=%v", availability, err)
	}
}
