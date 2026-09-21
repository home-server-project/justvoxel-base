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


func TestSetupDiagnosticEnvironmentSnapshotIsPrivacySafe(t *testing.T) {
	originalBootc := runBootcStatusJSON
	originalCommand := runSetupDiagnosticCommand
	originalRead := readSetupDiagnosticFile
	runBootcStatusJSON = func(context.Context) ([]byte, error) {
		return []byte(`{"status":{"booted":{"image":{"image":"ghcr.io/home-server-project/justvoxel-base:mjust-testing"},"imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`), nil
	}
	runSetupDiagnosticCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "uname -r":
			return []byte("6.12.0-test\n"), nil
		case "systemd --version":
			return []byte("systemd 257\n+PAM\n"), nil
		case "podman --version":
			return []byte("podman version 5.8.4\n"), nil
		case "podman info --format {{.Host.NetworkBackend}}":
			return []byte("netavark\n"), nil
		case "getenforce ":
			return []byte("Enforcing\n"), nil
		case "systemctl is-active firewalld.service":
			return []byte("active\n"), nil
		case "systemctl is-active NetworkManager.service":
			return []byte("active\n"), nil
		case "systemd-detect-virt --container":
			return nil, errors.New("not a container")
		case "systemd-detect-virt --vm":
			return []byte("kvm\n"), nil
		default:
			return nil, errors.New("unexpected diagnostic command: " + key)
		}
	}
	readSetupDiagnosticFile = func(path string) ([]byte, error) {
		switch path {
		case "/usr/lib/justvoxel/variant":
			return []byte("justvoxel-vm\n"), nil
		case "/proc/meminfo":
			return []byte("MemTotal:       8388608 kB\nMemAvailable:   4194304 kB\n"), nil
		default:
			return nil, os.ErrNotExist
		}
	}
	t.Cleanup(func() {
		runBootcStatusJSON = originalBootc
		runSetupDiagnosticCommand = originalCommand
		readSetupDiagnosticFile = originalRead
	})

	store := openTestOperationStore(t)
	id, err := store.beginSetupDiagnostic("webui")
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.readSetupDiagnostic(id)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"ENV setup environment snapshot",
		"image_variant=\"justvoxel-vm\"",
		"runtime_class=\"virtual_machine\"",
		"virtualization=\"kvm\"",
		"kernel=\"6.12.0-test\"",
		"podman=\"podman version 5.8.4\"",
		"podman_network=\"netavark\"",
		"selinux=\"Enforcing\"",
		"firewalld=\"active\"",
		"network_manager=\"active\"",
		"memory_total_mib=\"8192\"",
		"memory_avail_mib=\"4096\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostic environment snapshot missing %q: %s", want, text)
		}
	}
}

func TestSetupRuntimeFailureEvidenceCapturesPodmanWithoutLeakingIdentity(t *testing.T) {
	originalBootc := runBootcStatusJSON
	originalCommand := runSetupDiagnosticCommand
	originalRead := readSetupDiagnosticFile
	runBootcStatusJSON = func(context.Context) ([]byte, error) { return nil, errors.New("bootc unavailable") }
	runSetupDiagnosticCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "systemctl show minecraft.service --property=LoadState,ActiveState,SubState,Result,ExecMainStatus --no-pager":
			return []byte("LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\nExecMainStatus=125\n"), nil
		case "podman info --format {{.Host.NetworkBackend}}":
			return []byte("netavark\n"), nil
		case "podman network exists podman":
			return nil, nil
		case "podman network inspect podman --format {{.Name}}|{{.Driver}}|{{.NetworkInterface}}|{{.DNSEnabled}}":
			return []byte("podman|bridge|podman0|true\n"), nil
		case "podman container exists minecraft":
			return nil, nil
		case "podman inspect minecraft --format {{.State.Status}}":
			return []byte("configured\n"), nil
		case "podman inspect minecraft --format {{.State.ExitCode}}":
			return []byte("125\n"), nil
		case "podman inspect minecraft --format {{.Image}}":
			return []byte("sha256:containerimage\n"), nil
		case "podman inspect minecraft --format {{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}":
			return []byte("podman\n"), nil
		case "podman image exists docker.io/itzg/minecraft-server:stable":
			return nil, nil
		case "podman image inspect docker.io/itzg/minecraft-server:stable --format {{.Digest}}":
			return []byte("sha256:registrydigest\n"), nil
		case "podman image inspect docker.io/itzg/minecraft-server:stable --format {{.Id}}":
			return []byte("sha256:localimage\n"), nil
		case "firewall-cmd --permanent --query-port=25565/tcp":
			return []byte("yes\n"), nil
		case "firewall-cmd --permanent --query-port=19132/udp":
			return []byte("yes\n"), nil
		case "ls -Zd -- /var/lib/justvoxel/minecraft":
			return []byte("system_u:object_r:container_file_t:s0 /var/lib/justvoxel/minecraft\n"), nil
		case "systemctl is-enabled minecraft-backup.timer":
			return []byte("enabled\n"), nil
		case "systemctl is-active minecraft-backup.timer":
			return []byte("inactive\n"), errors.New("inactive")
		case "systemctl is-active network-online.target":
			return []byte("active\n"), nil
		case "nmcli -t -f CONNECTIVITY general":
			return []byte("full\n"), nil
		case "journalctl -u minecraft.service --no-pager -n 80 -o short-iso":
			return []byte("podman failed to connect network at 192.168.0.59 on private-vm-host RCON_PASSWORD=supersecret\n"), nil
		case "journalctl -k --no-pager -n 300 -o short-iso":
			return []byte("kernel: avc: denied for private-vm-host 192.168.0.59\n"), nil
		case "/usr/libexec/justvoxel/mjust/validate-backend ":
			return []byte("FAIL: minecraft.service is not active\n"), errors.New("validation failed")
		case "getent ahosts docker.io", "getent ahosts fill.papermc.io", "getent ahosts download.geysermc.org":
			return []byte("203.0.113.10 STREAM example\n"), nil
		default:
			return nil, errors.New("unavailable: " + key)
		}
	}
	readSetupDiagnosticFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() {
		runBootcStatusJSON = originalBootc
		runSetupDiagnosticCommand = originalCommand
		readSetupDiagnosticFile = originalRead
	})

	store := openTestOperationStore(t)
	store.diagnosticHostname = "private-vm-host"
	id, err := store.beginSetupDiagnostic("webui")
	if err != nil {
		t.Fatal(err)
	}
	plan := setupPlanForRuntimeTest(t)
	store.appendSetupRuntimeFailureBundleBestEffort(id, "verify", plan, errors.New("Minecraft runtime verification failed"))
	data, err := store.readSetupDiagnostic(id)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	for _, want := range []string{
		"FAILURE runtime failure evidence",
		"podman_network_backend=\"netavark\"",
		"podman_network_summary=\"podman|bridge|podman0|true\"",
		"service_result=\"exit-code\"",
		"service_exit_status=\"125\"",
		"minecraft_image_digest=\"sha256:registrydigest\"",
		"dns_docker_io=\"ok\"",
		"container_networks=\"podman\"",
		"container_file_t",
		"<IP-REDACTED>",
		"<HOSTNAME-REDACTED>",
		"<REDACTED>",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("runtime failure evidence missing %q: %s", want, logText)
		}
	}
	for _, forbidden := range []string{"192.168.0.59", "private-vm-host", "supersecret"} {
		if strings.Contains(strings.ToLower(logText), strings.ToLower(forbidden)) {
			t.Fatalf("runtime failure evidence leaked %q: %s", forbidden, logText)
		}
	}
}


func TestSetupDiagnosticPreservesSystemdUnitNamesWhileRedactingHosts(t *testing.T) {
	value := sanitizeSetupDiagnosticText("minecraft.service failed on private.example at 192.168.1.20", "")
	if !strings.Contains(value, "minecraft.service") {
		t.Fatalf("systemd unit name was redacted: %s", value)
	}
	if strings.Contains(value, "private.example") || strings.Contains(value, "192.168.1.20") {
		t.Fatalf("network identity was not redacted: %s", value)
	}
}

