package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupDiagnosticRedactsSecretsNetworkIdentityAndHostname(t *testing.T) {
	originalBootc := runBootcStatusJSON
	runBootcStatusJSON = func(context.Context) ([]byte, error) {
		return []byte(`{"status":{"booted":{"image":{"image":"ghcr.io/home-server-project/justvoxel-base:mjust-testing"},"imageDigest":"sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"}}}`), nil
	}
	t.Cleanup(func() { runBootcStatusJSON = originalBootc })

	store := openTestOperationStore(t)
	store.diagnosticHostname = "private-vm-host"
	sessionID, err := store.beginSetupDiagnostic("webui")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.appendSetupDiagnostic(sessionID, "USER", "wizard choice changed on private-vm-host at 192.168.50.9 and 2001:db8::1234", map[string]string{
		"backup_source": "//nas.private.example/backups",
		"smb_password":  "super-secret-password",
		"timezone":      "America/Toronto",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := store.readSetupDiagnostic(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"private-vm-host", "192.168.50.9", "2001:db8::1234", "nas.private.example", "super-secret-password"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("diagnostic log leaked %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{
		"<HOSTNAME-REDACTED>", "<IP-REDACTED>", "<REDACTED>",
		"ghcr.io/home-server-project/justvoxel-base:mjust-testing",
		"sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostic log missing %q: %s", want, text)
		}
	}
}

func TestSetupDiagnosticBindsWizardHistoryToOperationAndTracksStages(t *testing.T) {
	originalBootc := runBootcStatusJSON
	runBootcStatusJSON = func(context.Context) ([]byte, error) { return nil, errors.New("bootc unavailable") }
	t.Cleanup(func() { runBootcStatusJSON = originalBootc })

	store := openTestOperationStore(t)
	sessionID, err := store.beginSetupDiagnostic("mjust")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.appendSetupDiagnostic(sessionID, "USER", "setup choice changed", map[string]string{"java_memory": "4G"}); err != nil {
		t.Fatal(err)
	}
	operation, created, err := store.beginSetupWithDiagnostic(testSetupFingerprint, sessionID)
	if err != nil || !created {
		t.Fatalf("begin setup: created=%t err=%v", created, err)
	}
	if _, err := os.Stat(filepath.Join(store.setupLogsDir, sessionID+".log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wizard session log still exists after binding: %v", err)
	}
	if _, err := store.transition(operation.OperationID, operationValidating, "storage_preflight", "Revalidating reviewed storage."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.updateProgress(operation.OperationID, operationValidating, "storage_snapshot", "Preparing reversible storage transaction."); err != nil {
		t.Fatal(err)
	}
	if err := store.appendSetupDiagnostic(operation.OperationID, "ERROR", "setup helper failed at 10.20.30.40", map[string]string{"error": "podman network connect failed"}); err != nil {
		t.Fatal(err)
	}
	data, err := store.readSetupDiagnostic(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"setup choice changed", "java_memory=\"4G\"", "storage_preflight", "storage_snapshot", "setup helper failed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("bound diagnostic log missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "10.20.30.40") {
		t.Fatalf("bound diagnostic log leaked IP: %s", text)
	}
}

func TestSetupDiagnosticFilesArePrivateAndUnknownIDsAreRejected(t *testing.T) {
	store := openTestOperationStore(t)
	sessionID, err := store.beginSetupDiagnostic("webui")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(store.setupLogsDir, sessionID+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("diagnostic log mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(store.setupLogsDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("diagnostic directory mode = %o, want 700", got)
	}
	if _, err := store.readSetupDiagnostic("not-an-operation-id"); err == nil {
		t.Fatal("invalid diagnostic id unexpectedly accepted")
	}
}

func TestSetupHelperFailureDetailIsSanitizedAndNotExposedByError(t *testing.T) {
	originalBootc := runBootcStatusJSON
	runBootcStatusJSON = func(context.Context) ([]byte, error) { return nil, errors.New("bootc unavailable") }
	t.Cleanup(func() { runBootcStatusJSON = originalBootc })

	store := openTestOperationStore(t)
	store.diagnosticHostname = "private-vm-host"
	id, err := store.beginSetupDiagnostic("webui")
	if err != nil { t.Fatal(err) }
	helperErr := newSetupHelperExecutionError("runtime", "verify", errors.New("exit status 1"), []byte("podman network connect failed on private-vm-host 192.168.0.59 via nas.private.example password=do-not-leak"))
	if strings.Contains(helperErr.Error(), "192.168.0.59") || strings.Contains(helperErr.Error(), "do-not-leak") { t.Fatalf("helper Error() exposed diagnostic detail: %v", helperErr) }
	store.appendSetupHelperFailureBestEffort(id, "runtime", "verify", helperErr)
	data, err := store.readSetupDiagnostic(id)
	if err != nil { t.Fatal(err) }
	text := string(data)
	for _, forbidden := range []string{"private-vm-host", "192.168.0.59", "nas.private.example", "do-not-leak"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) { t.Fatalf("diagnostic helper output leaked %q: %s", forbidden, text) }
	}
	for _, want := range []string{"podman network connect failed", "<HOSTNAME-REDACTED>", "<IP-REDACTED>", "password=<REDACTED>"} {
		if !strings.Contains(text, want) { t.Fatalf("diagnostic helper output missing %q: %s", want, text) }
	}
}
