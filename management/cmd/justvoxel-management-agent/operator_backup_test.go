package main

import (
	"testing"
	"time"
)

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
	if _, err := guard.acceptTarget(operator, "jv-a1b2c3"); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return base.Add(16 * time.Minute) }
	guard, availability, err = store.beginOperatorBackup(user.ID)
	if err != nil || !availability.Allowed {
		t.Fatalf("second allowance unavailable: %#v err=%v", availability, err)
	}
	if _, err := guard.acceptTarget(operator, "jv-a1b2c3"); err != nil {
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

func TestOperatorBackupInvalidTargetDoesNotConsumeAllowance(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("InvalidBackupTarget", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	actor := session{Username: user.Username, Role: roleOperator, AuthSource: authSourceWebUI, WebUserID: user.ID}
	for _, target := range []string{"", "minecraft-backup.service", "jv-short", "jv-A1B2C3", "../jv-a1b2c3", "jv-a1b2c3.service"} {
		guard, available, err := store.beginOperatorBackup(user.ID)
		if err != nil || !available.Allowed {
			t.Fatalf("availability=%+v err=%v", available, err)
		}
		if _, err := guard.acceptTarget(actor, target); err == nil {
			t.Fatal("invalid target accepted", target)
		}
		guard.cancel()
		used, last, err := store.operatorBackupUsage(user.ID)
		if err != nil || used != 0 || last != nil {
			t.Fatalf("target=%q used=%d last=%v err=%v", target, used, last, err)
		}
	}
	var audits, notifications int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='instance_backup'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatal(audits, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind='operator_backup_limit'`).Scan(&notifications); err != nil || notifications != 0 {
		t.Fatal(notifications, err)
	}
	var lastGlobal *string
	if err := store.db.QueryRow(`SELECT last_backup_at FROM operator_global_state WHERE id=1`).Scan(&lastGlobal); err != nil || (lastGlobal != nil && *lastGlobal != "") {
		t.Fatal(lastGlobal, err)
	}
}

func TestOperatorBackupGlobalCooldownAppliesAcrossUsers(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	first, err := store.createWebUser("BackupOne", webUserRoleOperator, "first-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.createWebUser("BackupTwo", webUserRoleOperator, "second-password")
	if err != nil {
		t.Fatal(err)
	}
	guard, available, err := store.beginOperatorBackup(first.ID)
	if err != nil || !available.Allowed {
		t.Fatal(available, err)
	}
	if _, err := guard.acceptTarget(session{Username: first.Username, Role: roleOperator, WebUserID: first.ID}, "jv-a1b2c3"); err != nil {
		t.Fatal(err)
	}
	guard, available, err = store.beginOperatorBackup(second.ID)
	if guard != nil {
		guard.cancel()
	}
	if err != nil || available.Allowed || available.Reason != "backup_cooldown" {
		t.Fatal(available, err)
	}
	used, last, err := store.operatorBackupUsage(second.ID)
	if err != nil || used != 0 || last != nil {
		t.Fatal(used, last, err)
	}
}
