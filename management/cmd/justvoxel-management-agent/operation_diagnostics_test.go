package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOperationDiagnosticUsesCanonicalPrivateLogAndOperationID(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginMigrationImport("jv-a1b2c3", testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.transition(operation.OperationID, operationValidating, "import_preflight", "Preflight accepted."); err != nil {
		t.Fatal(err)
	}
	if err := store.appendOperationDiagnostic(operation.OperationID, operationTypeMigrationImport, "ERROR", "backend failed", map[string]string{
		"reason": "Minecraft version was rejected", "smb_password": "do-not-log-me",
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.baseDir, "logs", "migration-logs", operation.OperationID+".log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log permissions = %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions = %o", dirInfo.Mode().Perm())
	}
	data, err := store.readDiagnosticLog("operation", operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{operation.OperationID, "Preflight accepted.", "Minecraft version was rejected", "<REDACTED>"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("log missing %q", want)
		}
	}
	if strings.Contains(string(data), "do-not-log-me") {
		t.Fatal("secret was logged")
	}
	entries, err := store.listDiagnosticLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Category != "migration" || entries[0].OperationID != operation.OperationID || entries[0].OperationType != operationTypeMigrationImport {
		t.Fatalf("unexpected log index: %#v", entries)
	}
}

func TestOperationDiagnosticRejectsPathsAndSymlinks(t *testing.T) {
	store := openTestOperationStore(t)
	for _, id := range []string{"../secret", "/etc/passwd", "12345678-1234-4123-8123-123456789abc/other"} {
		if _, err := store.readDiagnosticLog("operation", id); err == nil {
			t.Fatalf("accepted unsafe id %q", id)
		}
	}
	operation, _, err := store.beginMigrationImport("jv-a1b2c3", testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.logsDir, "migration-logs")
	if err := validatePrivateDiagnosticDirectory(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, operation.OperationID+".log")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := store.appendOperationDiagnostic(operation.OperationID, operationTypeMigrationImport, "INFO", "test", nil); err == nil {
		t.Fatal("accepted log symlink")
	}
	if _, err := store.readDiagnosticLog("operation", operation.OperationID); err == nil {
		t.Fatal("read log symlink")
	}
	if _, err := store.readDiagnosticLog("migration", operation.OperationID); err == nil {
		t.Fatal("read category log symlink")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "private" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
	if _, err := store.readDiagnosticLog("unknown", operation.OperationID); err == nil {
		t.Fatal("accepted unknown category")
	}
	if _, err := store.readDiagnosticLog("operation", "12345678-1234-4123-8123-123456789abc"); !errors.Is(err, errOperationNotFound) {
		t.Fatalf("unknown operation error = %v", err)
	}
}

func TestMigrationDiagnosticRetainsBoundedFailureTailWithoutSecrets(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginMigrationImport("jv-a1b2c3", testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	var output boundedMigrationDiagnostic
	_, _ = output.Write([]byte(strings.Repeat("staging output\n", 4000)))
	_, _ = output.Write([]byte("ERROR: imported server version rejected\nERROR: Minecraft did not become ready through RCON after restore.\nRCON_PASSWORD=private-rcon-password-value\nAuthorization: Bearer hidden-token\nsecret password value\n"))
	details := migrationImportDiagnosticContext{store: store, id: operation.OperationID, secrets: []string{"hidden-token"}}
	details.recordOutput(output)
	data, err := store.readDiagnosticLog("operation", operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "imported server version rejected") || !strings.Contains(string(data), "ERROR: Minecraft did not become ready through RCON after restore.") || !strings.Contains(string(data), "truncated") {
		t.Fatalf("failure evidence missing: %s", data)
	}
	if !strings.Contains(string(data), "<sensitive backend detail redacted>") {
		t.Fatalf("credential line was not redacted: %s", data)
	}
	if strings.Contains(string(data), "private-rcon-password-value") || strings.Contains(string(data), "hidden-token") || strings.Contains(string(data), "secret password value") {
		t.Fatal("sensitive output was logged")
	}
	if len(data) > operationDiagnosticLimit {
		t.Fatalf("diagnostic size = %d", len(data))
	}
}

func TestPluginOperationDiagnosticsPrivateCategoryAndExactJournal(t *testing.T) {
	for _, kind := range []string{operationTypePluginInstall, operationTypePluginEnable, operationTypePluginDisable, operationTypePluginRemove} {
		t.Run(kind, func(t *testing.T) {
			store := openTestOperationStore(t)
			j, _, err := store.beginMinecraftMaintenance(testSetupFingerprint, kind)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.transition(j.OperationID, operationValidating, "validating", "Preparing plugin."); err != nil {
				t.Fatal(err)
			}
			if diagnosticCategoryForOperationType(kind) != "plugins" {
				t.Fatal("wrong plugin diagnostic category")
			}
			info, err := os.Stat(filepath.Join(store.logsDir, "plugins-logs"))
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatal("plugin diagnostics not private", err)
			}
			data, err := store.readDiagnosticLog("plugins", j.OperationID)
			if err != nil || !strings.Contains(string(data), "Preparing plugin.") {
				t.Fatal("canonical plugin journal unavailable", err)
			}
			if _, err = store.readDiagnosticLog("restore", j.OperationID); err == nil {
				t.Fatal("wrong journal category accepted")
			}
			entries, err := store.listDiagnosticLogs()
			if err != nil || len(entries) != 1 || entries[0].Category != "plugins" || entries[0].OperationType != kind {
				t.Fatal(entries, err)
			}
		})
	}
}
func TestPluginStartupEvidenceBoundedSanitizedFinalSegment(t *testing.T) {
	raw := "[Root] earlier arbitrary log\n[PluginInitializerManager] Initializing plugins...\n[Root] previous startup failure\n[PluginInitializerManager] Initializing plugins...\n[Root] Storage initialization failed\n[Root] password=hunter2\n[Root] token abc\n[Root] https://example.invalid/private\n[Root] at example.Main.run(Main.java:4)\n[Other] unrelated\n"
	raw += strings.Repeat("[Root] Could not initialize storage\n", 200) + "[Root] Storage initialization failed\n[Root] password=hunter2\n"
	lines := pluginStartupDiagnosticExcerpt([]byte(raw), []string{"Root"}, "private-host")
	if len(lines) > 64 || len(strings.Join(lines, "\n")) > 16<<10 {
		t.Fatal("evidence limit exceeded")
	}
	excerpt := strings.Join(lines, "\n")
	for _, unsafe := range []string{"hunter2", "token abc", "https://", "Main.java", "earlier", "previous startup", "unrelated"} {
		if strings.Contains(excerpt, unsafe) {
			t.Fatal("unsafe or non-final log exposed", unsafe)
		}
	}
	if !strings.Contains(excerpt, "Storage initialization failed") || !strings.Contains(excerpt, "redacted") {
		t.Fatal("useful evidence missing", excerpt)
	}
	for _, corrupt := range []string{"\x00", "\x01", "\x7f"} {
		if lines := pluginStartupDiagnosticExcerpt([]byte("[PluginInitializerManager] Initializing plugins...\n[Root] failure"+corrupt+"\n"), []string{"Root"}, ""); lines != nil {
			t.Fatal("control corruption accepted")
		}
	}
}

func TestPluginStartupEvidenceRedactsAllSensitiveFamiliesAndHostIdentity(t *testing.T) {
	for _, family := range []string{"password", "secret", "token", "cookie", "authorization", "credential", "api-key", "api_key", "apikey", "API key"} {
		raw := "[PluginInitializerManager] Initializing plugins...\n[Root] " + family + ": do-not-expose\n"
		lines := pluginStartupDiagnosticExcerpt([]byte(raw), []string{"Root"}, "")
		if len(lines) != 1 || strings.Contains(strings.Join(lines, "\n"), "do-not-expose") || !strings.Contains(lines[0], "redacted") {
			t.Fatal("sensitive startup detail escaped", family, lines)
		}
	}
	raw := "[PluginInitializerManager] Initializing plugins...\n[Root] Storage failed on private-host 10.20.30.40\n"
	lines := pluginStartupDiagnosticExcerpt([]byte(raw), []string{"Root"}, "private-host")
	if len(lines) != 1 || strings.Contains(lines[0], "private-host") || strings.Contains(lines[0], "10.20.30.40") {
		t.Fatal("host identity escaped startup sanitation", lines)
	}
}

func TestSupportReportPrivateStorageAndExistingLogFlow(t *testing.T) {
	store := openTestOperationStore(t)
	id, err := newOperationID()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.storeSupportReport(id, []byte("support evidence\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !validDiagnosticCategory("support") || !validOperationID(entry.LogID) || entry.Category != "support" || entry.OperationID != "" {
		t.Fatal(entry)
	}
	path := filepath.Join(store.logsDir, "support", id+".log")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		t.Fatal("unsafe support file")
	}
	if _, err := os.Stat(filepath.Join(store.logsDir, "support-logs")); !os.IsNotExist(err) {
		t.Fatal("parallel support directory")
	}
	if _, err := store.storeSupportReport(id, []byte("collision")); err == nil {
		t.Fatal("appended collided ID")
	}
	for _, bad := range []string{"../private", id + ".log"} {
		if _, err := store.storeSupportReport(bad, nil); err == nil {
			t.Fatal("unsafe identity")
		}
	}
	data, err := store.readDiagnosticLog("support", id)
	if err != nil || string(data) != "support evidence\n" {
		t.Fatal(string(data), err)
	}
	entries, err := store.listDiagnosticLogs()
	if err != nil || len(entries) != 1 || entries[0].LogID != id || entries[0].Timestamp == "" {
		t.Fatal(entries, err)
	}
	if _, err := store.storeSupportReport(id, make([]byte, supportReportLimit+1)); err == nil {
		t.Fatal("oversized report")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.storeSupportReport(id, nil); err == nil {
		t.Fatal("created through symlink")
	}
	if _, err := store.readDiagnosticLog("support", id); err == nil {
		t.Fatal("read symlink")
	}
	dir := filepath.Join(store.logsDir, "support")
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.storeSupportReport(id, nil); err == nil {
		t.Fatal("unsafe directory")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.storeSupportReport(id, nil); err == nil {
		t.Fatal("followed support directory symlink")
	}
}

func TestSupportReportBoundedRedactedFixedCommandsAndFailure(t *testing.T) {
	old := runSupportCommand
	defer func() { runSupportCommand = old }()
	store := openTestOperationStore(t)
	store.diagnosticHostname = "private-appliance"
	calls := 0
	runSupportCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		joined := strings.Join(args, " ")
		unit := "justvoxel-management.service"
		if calls == 2 || calls == 4 {
			unit = "justvoxel-webui.service"
		}
		want := "show " + unit + " --no-pager --property=ActiveState,SubState,Result,NRestarts,ExecMainCode,ExecMainStatus"
		wantName := "systemctl"
		if calls > 2 {
			wantName = "journalctl"
			lines := "300"
			if calls == 4 {
				lines = "150"
			}
			want = "-b -u " + unit + " --no-pager -n " + lines + " -o cat"
		}
		if name != wantName || joined != want {
			t.Fatal("unexpected command", name, joined)
		}
		if calls == 2 {
			return []byte("unsafe raw output"), errors.New("internal password=unsafe")
		}
		return []byte("failure in justvoxel-management.service on private-appliance 10.20.30.40 2001:db8::1\npassword=hidden\nTOKEN hidden\nCookie hidden\nAuthorization: Bearer hidden\ncredential hidden\ncsrf hidden\nAPI key hidden\nSMB secret hidden\nRCON_PASSWORD hidden\n"), nil
	}
	entry, err := store.generateSupportReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.readDiagnosticLog("support", entry.LogID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(data) > supportReportLimit {
		t.Fatal("collection bounds", calls, len(data))
	}
	for _, bad := range []string{"hidden", "private-appliance", "10.20.30.40", "2001:db8::1", "unsafe", "internal password"} {
		if strings.Contains(string(data), bad) {
			t.Fatal("privacy leak", bad)
		}
	}
	for _, want := range []string{"justvoxel-management.service", "<section unavailable>", "<sensitive diagnostic detail redacted>", "MANAGEMENT STATE INVENTORY", "REPORT METADATA"} {
		if !strings.Contains(string(data), want) {
			t.Fatal("missing evidence", want)
		}
	}
	var report supportReport
	for i := 0; i < 20; i++ {
		report.section("bounded", []byte(strings.Repeat("a", 1500)+"\n"+strings.Repeat("line\n", 20000)))
	}
	if report.Len() > supportReportLimit || !strings.Contains(report.String(), "truncated") {
		t.Fatal("report bound")
	}
	if got := sanitizeSupportLine(strings.Repeat("x", supportLineLimit+1), ""); !strings.Contains(got, "truncated") {
		t.Fatal("line bound")
	}
	var output supportCommandOutput
	_, _ = output.Write(make([]byte, supportSectionLimit*2))
	if output.Len() != supportSectionLimit || !output.truncated {
		t.Fatal("command output bound")
	}
}

func TestSupportInventoryDepthLimitAndNoSymlinkTraversal(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "must-not-appear"), []byte("secret contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "one", "two"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "one", "two", "too-deep"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	data := string(supportInventory(base))
	if strings.Contains(data, "must-not-appear") || strings.Contains(data, "too-deep") || strings.Contains(data, "secret contents") || !strings.Contains(data, "link") || !strings.Contains(data, "one/two") {
		t.Fatal(data)
	}
	for i := 0; i < supportInventoryLimit+10; i++ {
		if err := os.WriteFile(filepath.Join(base, fmt.Sprintf("entry-%03d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	data = string(supportInventory(base))
	if strings.Count(data, "exists type=") > supportInventoryLimit || !strings.Contains(data, "inventory truncated") {
		t.Fatal("inventory not bounded")
	}
}

func TestSupportGenerationRouteAdministratorOnlyAndNoInput(t *testing.T) {
	old := runSupportCommand
	defer func() { runSupportCommand = old }()
	calls := 0
	runSupportCommand = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("internal secret")
	}
	for _, role := range []principalRole{roleViewer, roleOperator, roleAdministrator} {
		server := surfaceTestServer(t, role)
		server.operations = openTestOperationStore(t)
		mux := http.NewServeMux()
		registerAdminDiagnosticLogRoutes(mux, server)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, surfaceRequest(http.MethodPost, "/v1/admin/logs/support", ""))
		want := http.StatusForbidden
		if role == roleAdministrator {
			want = http.StatusOK
		}
		if response.Code != want || strings.Contains(response.Body.String(), "internal secret") {
			t.Fatal(role, response.Code, response.Body.String())
		}
	}
	if calls != 4 {
		t.Fatal("unauthorized collection", calls)
	}
	server := surfaceTestServer(t, roleAdministrator)
	server.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerAdminDiagnosticLogRoutes(mux, server)
	for _, path := range []string{"/v1/admin/logs/support?unit=other.service", "/v1/admin/logs/support?path=/etc/passwd"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, surfaceRequest(http.MethodPost, path, ""))
		if response.Code != 400 {
			t.Fatal("accepted input", response.Code)
		}
	}
	if calls != 4 {
		t.Fatal("input reached collector")
	}
}
