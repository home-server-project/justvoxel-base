package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticCategoryMapping(t *testing.T) {
	for _, check := range []struct{ operationType, category string }{
		{operationTypeSetup, "setup"}, {operationTypeDataMigration, "migration"},
		{operationTypeMigrationExport, "migration"}, {operationTypeMigrationImport, "migration"},
		{operationTypeMigrationRecovery, "migration"}, {operationTypeRestore, "restore"},
		{operationTypeMinecraftReset, "reset"}, {operationTypeFactoryReset, "reset"},
	} {
		if got := diagnosticCategoryForOperationType(check.operationType); got != check.category {
			t.Fatalf("%s: got %q, want %q", check.operationType, got, check.category)
		}
	}
}

func TestOldOperationLogsMoveIntoCategoryWithoutDiscard(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	old := filepath.Join(base, "logs", "operation-logs", operationTypeMigrationImport)
	if err := os.MkdirAll(old, 0o700); err != nil { t.Fatal(err) }
	id := "12345678-1234-4123-8123-123456789abc"
	if err := os.WriteFile(filepath.Join(old, id+".log"), []byte("old import evidence"), 0o600); err != nil { t.Fatal(err) }
	store, err := openOperationStore(base)
	if err != nil { t.Fatal(err) }
	defer store.close()
	data, err := os.ReadFile(filepath.Join(base, "logs", "migration-logs", id+".log"))
	if err != nil || string(data) != "old import evidence" { t.Fatalf("migrated log = %q, %v", data, err) }
	if _, err := os.Lstat(filepath.Join(base, "logs", "operation-logs")); !os.IsNotExist(err) { t.Fatalf("old directory remains: %v", err) }
}

func TestMultipleOldOperationTypesMoveIntoSharedMigrationCategory(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	for _, item := range []struct{kind, id string}{
		{operationTypeDataMigration, "12345678-1234-4123-8123-123456789abc"},
		{operationTypeMigrationImport, "12345678-1234-4123-8123-123456789abd"},
	} {
		old := filepath.Join(base, "logs", "operation-logs", item.kind)
		if err := os.MkdirAll(old, 0o700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(old, item.id+".log"), []byte(item.kind), 0o600); err != nil { t.Fatal(err) }
	}
	store, err := openOperationStore(base)
	if err != nil { t.Fatal(err) }
	defer store.close()
	entries, err := store.listDiagnosticLogs()
	if err != nil || len(entries) != 2 { t.Fatalf("migration entries = %#v, %v", entries, err) }
}

func TestOldOperationLogSymlinkIsRejected(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	old := filepath.Join(base, "logs", "operation-logs", operationTypeMigrationImport)
	if err := os.MkdirAll(old, 0o700); err != nil { t.Fatal(err) }
	id := "12345678-1234-4123-8123-123456789abc"
	if err := os.Symlink("/etc/passwd", filepath.Join(old, id+".log")); err != nil { t.Fatal(err) }
	if store, err := openOperationStore(base); err == nil { store.close(); t.Fatal("accepted symlink") }
}

func TestOldOperationLogsConflictDoesNotOverwrite(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	id := "12345678-1234-4123-8123-123456789abc"
	old := filepath.Join(base, "logs", "operation-logs", operationTypeMigrationImport)
	newDir := filepath.Join(base, "logs", "migration-logs")
	for _, dir := range []string{old, newDir} { if err := os.MkdirAll(dir, 0o700); err != nil { t.Fatal(err) } }
	if err := os.WriteFile(filepath.Join(old, id+".log"), []byte("old"), 0o600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(newDir, id+".log"), []byte("new"), 0o600); err != nil { t.Fatal(err) }
	if store, err := openOperationStore(base); err == nil { store.close(); t.Fatal("accepted conflicting logs") }
	for _, check := range []struct{ dir, want string }{{old, "old"}, {newDir, "new"}} {
		data, err := os.ReadFile(filepath.Join(check.dir, id+".log"))
		if err != nil || string(data) != check.want { t.Fatalf("%s changed: %q, %v", check.dir, data, err) }
	}
}

func TestCategoryListingRetainsLogWithoutJournal(t *testing.T) {
	store := openTestOperationStore(t)
	id := "12345678-1234-4123-8123-123456789abc"
	path := filepath.Join(store.logsDir, "migration-logs", id+".log")
	if err := os.WriteFile(path, []byte("retained evidence"), 0o600); err != nil { t.Fatal(err) }
	entries, err := store.listDiagnosticLogs()
	if err != nil { t.Fatal(err) }
	if len(entries) != 1 || entries[0].Category != "migration" || entries[0].LogID != id { t.Fatalf("category listing = %#v", entries) }
	data, err := store.readDiagnosticLog("migration", id)
	if err != nil || string(data) != "retained evidence" { t.Fatalf("category read = %q, %v", data, err) }
}
