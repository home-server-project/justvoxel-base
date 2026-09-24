package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestWebUIStore(t *testing.T) (*webUIStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webui.db")
	store, err := openWebUIStore(path)
	if err != nil {
		t.Fatalf("open WebUI store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Errorf("close WebUI store: %v", err)
		}
	})
	return store, path
}

func TestWebUIStoreStartsWithoutWebUsers(t *testing.T) {
	store, path := openTestWebUIStore(t)
	users, err := store.listWebUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("fresh database unexpectedly contains %d WebUI users", len(users))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %04o, want 0600", got)
	}
}

func TestWebUIStoreAllowsMultipleOperatorAndViewerAccounts(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	base := time.Date(2026, time.September, 17, 1, 2, 3, 0, time.UTC)
	store.now = func() time.Time { return base }

	accounts := []struct {
		username string
		role     webUserRole
	}{
		{"Ilya", webUserRoleOperator},
		{"Alex", webUserRoleOperator},
		{"Maya", webUserRoleViewer},
		{"Noah", webUserRoleViewer},
		{"Sofia", webUserRoleViewer},
	}
	for i, account := range accounts {
		if _, err := store.createWebUser(account.username, account.role, "web-password-"+account.username); err != nil {
			t.Fatalf("create account %d (%s): %v", i, account.username, err)
		}
	}

	users, err := store.listWebUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != len(accounts) {
		t.Fatalf("got %d WebUI users, want %d", len(users), len(accounts))
	}
}

func TestWebUIStoreReservesVoxelForPrimaryAdministrator(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	for _, username := range []string{"voxel", "Voxel", " VOXEL "} {
		if _, err := store.createWebUser(username, webUserRoleOperator, "secret-password"); !errors.Is(err, errWebUserReserved) {
			t.Fatalf("create %q returned %v, want reserved-name error", username, err)
		}
	}
}

func TestWebUIStoreAcceptsOnlyFixedWebUserRoles(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	if _, err := store.createWebUser("alex", webUserRole("administrator"), "secret-password"); !errors.Is(err, errWebUserInvalidRole) {
		t.Fatalf("administrator WebUI row returned %v, want invalid-role error", err)
	}
	if _, err := store.createWebUser("custom", webUserRole("custom"), "secret-password"); !errors.Is(err, errWebUserInvalidRole) {
		t.Fatalf("custom WebUI role returned %v, want invalid-role error", err)
	}
}

func TestWebUIStorePasswordLifecycleAndDisable(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("Ilya", webUserRoleOperator, "first-password")
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.checkWebUserPassword("ilya", "first-password")
	if err != nil || !ok || got.ID != user.ID {
		t.Fatalf("initial credential failed: user=%#v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := store.checkWebUserPassword("Ilya", "wrong-password"); err != nil || ok {
		t.Fatalf("wrong credential result ok=%v err=%v", ok, err)
	}

	if err := store.setWebUserPassword(user.ID, "second-password"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.checkWebUserPassword("Ilya", "first-password"); err != nil || ok {
		t.Fatalf("old credential still accepted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.checkWebUserPassword("Ilya", "second-password"); err != nil || !ok {
		t.Fatalf("new credential rejected: ok=%v err=%v", ok, err)
	}

	if err := store.setWebUserEnabled(user.ID, false); err != nil {
		t.Fatal(err)
	}
	if disabled, ok, err := store.checkWebUserPassword("Ilya", "second-password"); err != nil || ok || disabled.Enabled {
		t.Fatalf("disabled account authenticated: user=%#v ok=%v err=%v", disabled, ok, err)
	}
}

func TestWebUIStoreRoleChangeAndDelete(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("Maya", webUserRoleViewer, "viewer-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.setWebUserRole(user.ID, webUserRoleOperator); err != nil {
		t.Fatal(err)
	}
	updated, err := store.webUserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != webUserRoleOperator {
		t.Fatalf("role = %q, want operator", updated.Role)
	}
	if err := store.deleteWebUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.webUserByID(user.ID); !errors.Is(err, errWebUserNotFound) {
		t.Fatalf("deleted user lookup returned %v", err)
	}
}

func TestWebUIStoreMigrationPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui.db")
	store, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.createWebUser("Alex", webUserRoleViewer, "viewer-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openWebUIStore(path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer reopened.close()
	users, err := reopened.listWebUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "Alex" {
		t.Fatalf("persisted users = %#v", users)
	}
}

func TestWebUIStoreRejectsUnknownFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES(999, 'future', '2026-09-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if store, err := openWebUIStore(path); err == nil {
		_ = store.close()
		t.Fatal("future schema unexpectedly opened")
	}
}

func TestWebUIStoreFoundationTablesExist(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	for _, table := range []string{
		"web_users",
		"operator_usage",
		"operator_global_state",
		"audit_events",
		"notifications",
		"system_monitor_profile",
		"schema_migrations",
	} {
		var name string
		err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
