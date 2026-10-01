package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupNativeUPSTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldSource, oldShutdown, oldSharing, oldDir := upsSourcePath, upsShutdownPath, upsSharingPath, upsConfigDir
	oldRead, oldLook, oldSystemctl, oldGroup, oldActive := upsReadFile, upsLookPath, upsSystemctl, upsSetNUTGroup, upsServiceActive
	upsSourcePath = filepath.Join(dir, "source.json")
	upsShutdownPath = filepath.Join(dir, "shutdown.json")
	upsSharingPath = filepath.Join(dir, "sharing.json")
	upsConfigDir = filepath.Join(dir, "nut")
	upsReadFile = func(path string) ([]byte, error) {
		if path == upsVariantPath {
			return []byte("justvoxel-hws"), nil
		}
		return os.ReadFile(path)
	}
	upsLookPath = func(string) (string, error) { return "/usr/bin/upsc", nil }
	upsSystemctl = func(context.Context, ...string) error { return nil }
	upsSetNUTGroup = func(string) error { return nil }
	upsServiceActive = func(context.Context, string) bool { return false }
	t.Cleanup(func() {
		upsSourcePath, upsShutdownPath, upsSharingPath, upsConfigDir = oldSource, oldShutdown, oldSharing, oldDir
		upsReadFile, upsLookPath, upsSystemctl, upsSetNUTGroup, upsServiceActive = oldRead, oldLook, oldSystemctl, oldGroup, oldActive
	})
	return dir
}

func upsRequest(t *testing.T, s *server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerUPSRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "http://unix"+path, body))
	return rr
}

func TestCheckOwnedNUTFileAdoptsOnlyPackagedContent(t *testing.T) {
	const stock = "MODE=none\n"
	stockSum := sha256.Sum256([]byte(stock))
	stockDigest := hex.EncodeToString(stockSum[:])
	for _, tc := range []struct {
		name       string
		content    string
		metadata   bool
		queryError bool
		wantError  bool
		wantQuery  bool
	}{
		{name: "missing file"},
		{name: "comment only", content: "\n # stock comment\n\t\n"},
		{name: "JustVoxel owned", content: upsOwnedHeader + "MODE=netclient\n"},
		{name: "untouched package stock", content: stock, wantQuery: true},
		{name: "metadata only differences", content: stock, metadata: true, wantQuery: true},
		{name: "modified content", content: "MODE=netclient\n", wantError: true, wantQuery: true},
		{name: "unverifiable content", content: stock, queryError: true, wantError: true, wantQuery: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupNativeUPSTest(t)
			path := filepath.Join(upsConfigDir, "nut.conf")
			if tc.content != "" {
				if err := os.MkdirAll(upsConfigDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.metadata {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				when := time.Unix(123456789, 0)
				if err := os.Chtimes(path, when, when); err != nil {
					t.Fatal(err)
				}
			}
			oldQuery := upsRPMQuery
			t.Cleanup(func() { upsRPMQuery = oldQuery })
			queries := 0
			upsRPMQuery = func(_ context.Context, gotPath string) ([]byte, error) {
				queries++
				if gotPath != path {
					t.Fatalf("RPM queried %q, want %q", gotPath, path)
				}
				if tc.queryError {
					return nil, errors.New("RPM database unavailable")
				}
				return []byte(fmt.Sprintf("nut\n8\n%s\t%s\n", path, stockDigest)), nil
			}
			err := checkOwnedNUTFile("nut.conf")
			if tc.wantError {
				if err == nil || err.Error() != "nut.conf contains administrator configuration" {
					t.Fatalf("got error %v, want administrator-configuration error", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantQueries := 0
			if tc.wantQuery {
				wantQueries = 1
			}
			if queries != wantQueries {
				t.Fatalf("RPM queries = %d, want %d", queries, wantQueries)
			}
		})
	}
}

func TestUPSNativeRemoteStatusOnlyAndSecretResponse(t *testing.T) {
	setupNativeUPSTest(t)
	var calls []string
	upsSystemctl = func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	oldRun := runUPSC
	defer func() { runUPSC = oldRun }()
	runUPSC = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "-l" {
			return []byte("ups1\n"), nil
		}
		return []byte("ups.status: OL\n"), nil
	}
	if rr := upsRequest(t, adminServerForTest(), "/v1/admin/ups/source", `{"mode":"remote","host":"nut.example.test","port":3493}`); rr.Code != http.StatusOK {
		t.Fatalf("remote status-only source = %d: %s", rr.Code, rr.Body.String())
	}
	config, err := os.ReadFile(filepath.Join(upsConfigDir, "nut.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "MODE=netclient") {
		t.Fatal("remote source must use netclient")
	}
	for _, call := range calls {
		if strings.Contains(call, "enable") && strings.Contains(call, "nut-server.service") {
			t.Fatal("netclient enabled local upsd")
		}
	}
	shutdown, err := os.ReadFile(filepath.Join(upsConfigDir, "upsmon.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shutdown), "MONITOR ") {
		t.Fatal("status-only source must not start protection")
	}
	if rr := upsRequest(t, adminServerForTest(), "/v1/admin/ups/shutdown", `{"enabled":true,"delay_seconds":120,"monitor_username":"monitor","monitor_password":"example-secret"}`); rr.Code != http.StatusOK {
		t.Fatalf("enable protection = %d: %s", rr.Code, rr.Body.String())
	} else if strings.Contains(rr.Body.String(), "example-secret") {
		t.Fatal("response exposed monitor password")
	}
	shutdown, err = os.ReadFile(filepath.Join(upsConfigDir, "upsmon.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shutdown), "MONITOR ups1@nut.example.test:3493") {
		t.Fatal("remote monitor target missing")
	}
	for _, call := range calls {
		if call == "enable --now nut-server.service" || call == "enable --now nut-driver.target" {
			t.Fatalf("remote protection activated local unit: %s", call)
		}
	}
	if !containsUPSCall(calls, "mask nut-server.service nut-driver.target nut-driver-enumerator.service nut-driver-enumerator.path") {
		t.Fatal("remote protection did not mask local units")
	}
	if rr := upsRequest(t, adminServerForTest(), "/v1/admin/ups/source/forget", ""); rr.Code != http.StatusOK {
		t.Fatalf("forget = %d: %s", rr.Code, rr.Body.String())
	}
	shutdown, err = os.ReadFile(filepath.Join(upsConfigDir, "upsmon.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shutdown), "MONITOR ") {
		t.Fatal("forget retained remote protection")
	}
}

func TestUPSNativeAuthorizationAndValidation(t *testing.T) {
	setupNativeUPSTest(t)
	for _, path := range []string{"/v1/admin/ups/shutdown", "/v1/admin/ups/sharing"} {
		if rr := upsRequest(t, roleServerForTest(roleOperator), path, `{}`); rr.Code != http.StatusForbidden {
			t.Fatalf("operator %s = %d", path, rr.Code)
		}
	}
	oldRead := upsReadFile
	upsReadFile = func(path string) ([]byte, error) {
		if path == upsVariantPath {
			return []byte("justvoxel-vm"), nil
		}
		return oldRead(path)
	}
	if rr := upsRequest(t, adminServerForTest(), "/v1/admin/ups/shutdown", `{}`); rr.Code != http.StatusConflict {
		t.Fatalf("VM shutdown = %d", rr.Code)
	}
	upsReadFile = oldRead
	if rr := upsRequest(t, adminServerForTest(), "/v1/admin/ups/shutdown", `{"enabled":true,"delay_seconds":0,"monitor_username":"monitor","monitor_password":"example-secret"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid delay = %d", rr.Code)
	}
	if err := writeUPSSource(upsSourceConfig{Mode: "remote", Host: "nut.example.test", Port: 3493, UPSName: "ups1"}); err != nil {
		t.Fatal(err)
	}
	if rr := upsRequest(t, adminServerForTest(), "/v1/admin/ups/sharing", `{"enabled":true,"listen_address":"192.0.2.4","listen_port":3493,"client_username":"client","client_password":"example-secret"}`); rr.Code != http.StatusConflict {
		t.Fatalf("remote sharing = %d", rr.Code)
	}
}

func TestUPSNativeRenderAndMalformedConfiguration(t *testing.T) {
	setupNativeUPSTest(t)
	remote := upsSourceConfig{Mode: "remote", Host: "nut.example.test", Port: 3493, UPSName: "ups1"}
	shutdown := upsShutdownConfig{Enabled: true, DelaySeconds: 45, MonitorUsername: "monitor", MonitorPassword: "example-secret"}
	if err := renderUPSNative(remote, shutdown, upsSharingConfig{}); err != nil {
		t.Fatal(err)
	}
	schedule, err := os.ReadFile(filepath.Join(upsConfigDir, "upssched.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schedule), "START-TIMER justvoxel-onbatt-secondary 45") || !strings.Contains(string(schedule), "CANCEL-TIMER justvoxel-onbatt-secondary") {
		t.Fatal("remote timer or cancellation missing")
	}
	if strings.Contains(string(schedule), "AT LOWBATT") {
		t.Fatal("LOWBATT has a duplicate scheduled poweroff")
	}
	monitor, err := os.ReadFile(filepath.Join(upsConfigDir, "upsmon.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(monitor), "SHUTDOWNCMD \"/usr/bin/sudo -n /usr/libexec/justvoxel/ups-emergency-poweroff\"") {
		t.Fatal("JustVoxel shutdown endpoint helper missing")
	}
	local := upsSourceConfig{Mode: "local", UPSName: "ups1", Driver: "usbhid-ups", DevicePort: "auto"}
	if err := renderUPSNative(local, shutdown, upsSharingConfig{}); err != nil {
		t.Fatal(err)
	}
	schedule, err = os.ReadFile(filepath.Join(upsConfigDir, "upssched.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schedule), "START-TIMER justvoxel-onbatt-primary 45") || !strings.Contains(string(schedule), "CANCEL-TIMER justvoxel-onbatt-primary") {
		t.Fatal("local FSD timer or cancellation missing")
	}
	if strings.Contains(string(schedule), "AT LOWBATT") {
		t.Fatal("local LOWBATT has a duplicate scheduled poweroff")
	}
	mode, err := os.ReadFile(filepath.Join(upsConfigDir, "nut.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mode), "MODE=standalone") {
		t.Fatal("local source must use standalone")
	}
	sharing := upsSharingConfig{Enabled: true, ListenAddress: "192.0.2.4", ListenPort: 3493, ClientUsername: "client", ClientPassword: "example-client-secret"}
	if err := renderUPSNative(local, shutdown, sharing); err != nil {
		t.Fatal(err)
	}
	mode, err = os.ReadFile(filepath.Join(upsConfigDir, "nut.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mode), "MODE=netserver") {
		t.Fatal("shared local source must use netserver")
	}
	if err := renderUPSNative(remote, shutdown, sharing); err == nil {
		t.Fatal("remote sharing accepted")
	}
	if err := os.WriteFile(upsShutdownPath, []byte(`{"enabled":true,"delay_seconds":-1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUPSShutdown(); err == nil {
		t.Fatal("malformed protection configuration accepted")
	}
	if err := os.WriteFile(filepath.Join(upsConfigDir, "ups.conf"), []byte("[administrator]\n  driver = dummy-ups\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := renderUPSNative(local, shutdown, sharing); err == nil {
		t.Fatal("administrator NUT configuration overwritten")
	}
}

func containsUPSCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func countUPSCall(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}

func upsCallIndex(calls []string, want string) int {
	for i, call := range calls {
		if call == want {
			return i
		}
	}
	return -1
}

func TestUPSNativeRoleServiceDecisions(t *testing.T) {
	setupNativeUPSTest(t)
	local := upsSourceConfig{Mode: "local", UPSName: "ups1", Driver: "usbhid-ups", DevicePort: "auto"}
	remote := upsSourceConfig{Mode: "remote", Host: "nut.example.test", Port: 3493, UPSName: "ups1"}
	for _, tc := range []struct {
		name       string
		source     upsSourceConfig
		protected  bool
		wantMask   string
		wantTarget bool
	}{
		{"remote status only", remote, false, "mask nut-monitor.service", false},
		{"remote protected", remote, true, "mask nut-server.service nut-driver.target nut-driver-enumerator.service nut-driver-enumerator.path", true},
		{"local without driver", upsSourceConfig{Mode: "local"}, false, "mask nut-server.service nut-driver.target nut-driver-enumerator.service nut-driver-enumerator.path", false},
		{"local unprotected", local, false, "mask nut-monitor.service", true},
		{"local protected", local, true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			upsSystemctl = func(_ context.Context, args ...string) error {
				calls = append(calls, strings.Join(args, " "))
				return nil
			}
			if err := reconcileUPSServices(context.Background(), tc.source, upsShutdownConfig{Enabled: tc.protected}); err != nil {
				t.Fatal(err)
			}
			if tc.wantMask != "" && !containsUPSCall(calls, tc.wantMask) {
				t.Fatalf("missing %q in %v", tc.wantMask, calls)
			}
			if got := containsUPSCall(calls, "enable --now nut.target"); got != tc.wantTarget {
				t.Fatalf("target enabled = %t, want %t", got, tc.wantTarget)
			}
			if !tc.wantTarget && containsUPSCall(calls, "enable nut.target") {
				t.Fatal("unprotected role enabled nut.target")
			}
			if !containsUPSCall(calls, "disable --now nut-driver-enumerator.path") {
				t.Fatal("enumerator path was not disabled")
			}
			for _, call := range calls {
				if (strings.HasPrefix(call, "enable ") || strings.HasPrefix(call, "start ") || strings.HasPrefix(call, "restart ")) && strings.Contains(call, "nut-driver-enumerator.path") {
					t.Fatalf("enumerator path activated: %s", call)
				}
			}
			if tc.source.Mode == "remote" || tc.source.Driver == "" {
				mask := upsCallIndex(calls, "mask nut-server.service nut-driver.target nut-driver-enumerator.service nut-driver-enumerator.path")
				if mask < 0 || (tc.wantTarget && upsCallIndex(calls, "enable --now nut.target") <= mask) {
					t.Fatal("local units were not masked before the remote target started")
				}
				for _, unit := range []string{"nut-server.service", "nut-driver.target", "nut-driver-enumerator.service", "nut-driver-enumerator.path"} {
					if !containsUPSCall(calls, "disable --now "+unit) {
						t.Fatalf("local unit was not stopped: %s", unit)
					}
					for _, call := range calls {
						if strings.Contains(call, unit) && (strings.HasPrefix(call, "enable ") || strings.HasPrefix(call, "start ") || strings.HasPrefix(call, "restart ")) {
							t.Fatalf("remote or driverless role activated %s: %s", unit, call)
						}
					}
				}
			}
			if tc.source.Mode == "local" && tc.source.Driver != "" {
				unmask := upsCallIndex(calls, "unmask nut-server.service nut-driver.target nut-driver-enumerator.service")
				enable := upsCallIndex(calls, "enable nut-driver-enumerator.service")
				restart := upsCallIndex(calls, "restart nut-driver-enumerator.service")
				if unmask < 0 || enable <= unmask || restart <= enable {
					t.Fatal("local enumerator was not unmasked and enabled before restart")
				}
				if countUPSCall(calls, "restart nut-driver-enumerator.service") != 1 || containsUPSCall(calls, "enable --now nut-driver-enumerator.service") || containsUPSCall(calls, "start nut-driver-enumerator.service") {
					t.Fatal("local enumerator was invoked more than once")
				}
				if containsUPSCall(calls, "enable --now nut-driver.target") || containsUPSCall(calls, "enable nut-driver.target") {
					t.Fatal("local driver target was persistently enabled")
				}
				if !containsUPSCall(calls, "enable --now nut-server.service") {
					t.Fatal("local server was not enabled and started")
				}
			}
			if tc.protected {
				unmask := upsCallIndex(calls, "unmask nut-monitor.service")
				enable := upsCallIndex(calls, "enable --now nut-monitor.service")
				if unmask < 0 || enable <= unmask {
					t.Fatal("monitor was not unmasked before enabling")
				}
			} else {
				mask := upsCallIndex(calls, "mask nut-monitor.service")
				if mask < 0 || (tc.wantTarget && upsCallIndex(calls, "enable --now nut.target") <= mask) {
					t.Fatal("disabled monitor could be pulled in by stock target")
				}
			}
		})
	}
}

func TestUPSNativeEnumeratesAfterLocalConfigRender(t *testing.T) {
	setupNativeUPSTest(t)
	local := upsSourceConfig{Mode: "local", UPSName: "ups1", Driver: "usbhid-ups", DevicePort: "auto"}
	restarts := 0
	upsSystemctl = func(_ context.Context, args ...string) error {
		if strings.Join(args, " ") == "restart nut-driver-enumerator.service" {
			restarts++
			data, err := os.ReadFile(filepath.Join(upsConfigDir, "ups.conf"))
			if err != nil {
				return err
			}
			if !strings.Contains(string(data), "[ups1]") || !strings.Contains(string(data), "driver = usbhid-ups") {
				t.Fatal("local driver was enumerated before ups.conf was rendered")
			}
		}
		return nil
	}
	if err := saveUPSSource(context.Background(), local); err != nil {
		t.Fatal(err)
	}
	if restarts != 1 {
		t.Fatalf("local enumerator restarts = %d, want 1", restarts)
	}
}

func TestUPSNativeStopsLocalDriverBeforeRemoteRender(t *testing.T) {
	setupNativeUPSTest(t)
	local := upsSourceConfig{Mode: "local", UPSName: "ups1", Driver: "usbhid-ups", DevicePort: "auto"}
	remote := upsSourceConfig{Mode: "remote", Host: "nut.example.test", Port: 3493, UPSName: "ups1"}
	if err := writeUPSSource(local); err != nil {
		t.Fatal(err)
	}
	if err := renderUPSNative(local, upsShutdownConfig{}, upsSharingConfig{}); err != nil {
		t.Fatal(err)
	}
	stoppedWithLocalConfig := false
	upsSystemctl = func(_ context.Context, args ...string) error {
		if strings.Join(args, " ") == "disable --now nut-driver.target" && !stoppedWithLocalConfig {
			data, err := os.ReadFile(filepath.Join(upsConfigDir, "ups.conf"))
			if err != nil {
				return err
			}
			stoppedWithLocalConfig = strings.Contains(string(data), "[ups1]")
		}
		return nil
	}
	if err := saveUPSSource(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	if !stoppedWithLocalConfig {
		t.Fatal("local driver was not stopped before its configuration was removed")
	}
	data, err := os.ReadFile(filepath.Join(upsConfigDir, "ups.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[ups1]") {
		t.Fatal("remote role retained a local driver")
	}
}

func TestUPSNativeHelpersAndFilePermissions(t *testing.T) {
	setupNativeUPSTest(t)
	var grouped []string
	upsSetNUTGroup = func(path string) error { grouped = append(grouped, path); return nil }
	local := upsSourceConfig{Mode: "local", UPSName: "ups1", Driver: "usbhid-ups", DevicePort: "auto"}
	shutdown := upsShutdownConfig{Enabled: true, DelaySeconds: 45, MonitorUsername: "monitor", MonitorPassword: "example-secret"}
	if err := renderUPSNative(local, shutdown, upsSharingConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"upsd.conf", "upsd.users", "upsmon.conf"} {
		path := filepath.Join(upsConfigDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("%s mode = %o", name, info.Mode().Perm())
		}
		if !containsUPSCall(grouped, path) {
			t.Fatalf("%s was not assigned the nut group", name)
		}
	}
	if err := writeUPSJSON(upsShutdownPath, shutdown); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(upsShutdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential state mode = %o", info.Mode().Perm())
	}
	root := filepath.Join("..", "..", "..", "system_files", "usr", "libexec", "justvoxel")
	event, err := os.ReadFile(filepath.Join(root, "ups-schedule-event"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(event), "justvoxel-onbatt-primary)\n        exec /usr/bin/sudo -n /usr/libexec/justvoxel/ups-primary-fsd") || !strings.Contains(string(event), "justvoxel-onbatt-secondary)\n        exec /usr/bin/sudo -n /usr/libexec/justvoxel/ups-emergency-poweroff") {
		t.Fatal("scheduled role actions are incorrect")
	}
	fsd, err := os.ReadFile(filepath.Join(root, "ups-primary-fsd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fsd), "EUID != 0 || $# != 0") || !strings.Contains(string(fsd), "exec /usr/sbin/upsmon -c fsd") {
		t.Fatal("primary helper must invoke fixed-purpose FSD")
	}
	poweroff, err := os.ReadFile(filepath.Join(root, "ups-emergency-poweroff"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"EUID != 0 || $# != 0", `"action_confirmed":true,"confirm_players":true`, "POST /v1/admin/system/poweroff --data"} {
		if !strings.Contains(string(poweroff), want) {
			t.Fatalf("poweroff helper missing %q", want)
		}
	}
}

func TestUPSNativeStatusDoesNotReturnStoredPasswords(t *testing.T) {
	setupNativeUPSTest(t)
	if err := writeUPSSource(upsSourceConfig{Mode: "remote", Host: "nut.example.test", Port: 3493, UPSName: "ups1"}); err != nil {
		t.Fatal(err)
	}
	if err := writeUPSJSON(upsShutdownPath, upsShutdownConfig{Enabled: true, DelaySeconds: 120, MonitorUsername: "monitor", MonitorPassword: "example-secret"}); err != nil {
		t.Fatal(err)
	}
	view := collectUPSStatus(context.Background())
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "example-secret") {
		t.Fatal("status returned a stored password")
	}
}
