package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOperationStoreSetupLockIsRootOnly(t *testing.T) {
	base := filepath.Join(t.TempDir(), "management")
	store, err := openOperationStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()

	info, err := os.Stat(filepath.Join(base, "setup.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("setup lock mode = %o, want 600", got)
	}
}
