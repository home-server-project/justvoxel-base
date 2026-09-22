package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupStorageEvidenceDoesNotExposeExpectedIdentity(t *testing.T) {
	originalCommand := runSetupDiagnosticCommand
	runSetupDiagnosticCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "findmnt -n -o FSTYPE --target /var/lib/justvoxel/minecraft":
			return []byte("xfs\n"), nil
		case "findmnt -n -o UUID --target /var/lib/justvoxel/minecraft":
			return []byte("private-uuid\n"), nil
		case "df -Pm --output=size,avail -- /var/lib/justvoxel/minecraft":
			return []byte("1M-blocks Available\n102400 51200\n"), nil
		case "stat -c %a:%u:%g -- /var/lib/justvoxel/minecraft":
			return []byte("750:1000:1000\n"), nil
		case "ls -Zd -- /var/lib/justvoxel/minecraft":
			return []byte("system_u:object_r:container_file_t:s0 /var/lib/justvoxel/minecraft\n"), nil
		default:
			return nil, errors.New("unavailable: " + key)
		}
	}
	t.Cleanup(func() { runSetupDiagnosticCommand = originalCommand })

	values := setupStorageTargetEvidence("data", "/var/lib/justvoxel/minecraft", "", "private-uuid", "")
	if values["data_uuid_match"] != "yes" {
		t.Fatalf("uuid match = %q, want yes", values["data_uuid_match"])
	}
	if values["data_filesystem"] != "xfs" {
		t.Fatalf("filesystem = %q, want xfs", values["data_filesystem"])
	}
	if values["data_available_mib"] != "51200" {
		t.Fatalf("available MiB = %q, want 51200", values["data_available_mib"])
	}
	for key, value := range values {
		if strings.Contains(value, "private-uuid") {
			t.Fatalf("%s leaked raw storage identity: %q", key, value)
		}
	}
}

func TestSetupStorageManifestEvidenceSummarizesWithoutRawValues(t *testing.T) {
	originalRoot := setupStorageEvidenceTransactionRoot
	setupStorageEvidenceTransactionRoot = t.TempDir()
	t.Cleanup(func() { setupStorageEvidenceTransactionRoot = originalRoot })

	operationID := "12345678-1234-4123-8123-123456789abc"
	dir := filepath.Join(setupStorageEvidenceTransactionRoot, operationID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "phase":"storage_verified",
	  "fstab_changed":true,
	  "credentials_changed":false,
	  "mounts_by_transaction":[{"mountpoint":"/private","uuid":"private-uuid","source":"//private-host/share"}],
	  "directories_created":["/private/path"],
	  "rollback":{"state":"not_started","result":""}
	}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	values := setupStorageManifestEvidence(operationID)
	for key, want := range map[string]string{
		"transaction_manifest":    "present",
		"transaction_phase":       "storage_verified",
		"fstab_changed":           "true",
		"credentials_changed":     "false",
		"transaction_mount_count": "1",
		"created_directory_count": "1",
		"rollback_state":          "not_started",
	} {
		if values[key] != want {
			t.Fatalf("%s = %q, want %q", key, values[key], want)
		}
	}
	for key, value := range values {
		for _, forbidden := range []string{"private-uuid", "private-host", "/private"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("%s leaked raw manifest data %q: %q", key, forbidden, value)
			}
		}
	}
}
