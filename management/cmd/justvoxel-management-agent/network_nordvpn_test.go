package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func nordFixture(t *testing.T) *[][]string {
	t.Helper()
	remoteFixture(t)
	oldRun, oldStat := runNordCommand, remoteStat
	remoteStat = func(string) (os.FileInfo, error) { return os.Stat("/bin/sh") }
	calls := [][]string{}
	mesh := "disabled"
	runNordCommand = func(ctx context.Context, args ...string) (string, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded Nord operation")
		}
		calls = append(calls, append([]string(nil), args...))
		switch strings.Join(args, " ") {
		case "account":
			return "Account information\nEmail address: private@example.test\n", nil
		case "settings":
			return "Meshnet: " + mesh + "\n", nil
		case "set meshnet on":
			mesh = "enabled"
			return "", nil
		case "set meshnet off":
			mesh = "disabled"
			return "", nil
		case "meshnet peer list":
			return "This device:\nHostname: appliance-everest.nord\nIP: 100.64.0.2\nLocal Peers:\nHostname: peer-everest.nord\nIP: 100.64.0.3", nil
		case "login":
			return "Continue in the browser: https://api.nordvpn.com/v1/users/oauth/login-redirect?attempt=example", nil
		}
		if len(args) == 3 && args[0] == "login" && args[1] == "--callback" {
			return "", nil
		}
		t.Fatal("unexpected Nord command", args)
		return "", errors.New("unexpected")
	}
	t.Cleanup(func() { runNordCommand, remoteStat = oldRun, oldStat })
	return &calls
}

func TestNordFixedProviderStatusAndMeshnetLifecycle(t *testing.T) {
	calls := nordFixture(t)
	p, ok := remoteProviderByID("nordvpn")
	if !ok || p.name != "NordVPN Meshnet" || p.unit != "nordvpnd.service" || len(p.config) != 0 {
		t.Fatal(p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	status, err := remoteProviderStatus(ctx, p)
	if err != nil || !status.Installed || status.Account != "Logged in" || status.Meshnet != "Disabled" || status.Connected {
		t.Fatal(status, err)
	}
	for _, action := range []string{"activate", "deactivate"} {
		*calls = nil
		rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", `{"action":"`+action+`"}`)
		if rr.Code != http.StatusOK {
			t.Fatal(rr.Code, rr.Body.String())
		}
		want := []string{"set", "meshnet", "on"}
		if action == "deactivate" {
			want[2] = "off"
		}
		found := false
		for _, call := range *calls {
			if reflect.DeepEqual(call, want) {
				found = true
			}
			if call[0] == "connect" || call[0] == "logout" {
				t.Fatal("commercial VPN or logout", call)
			}
		}
		if !found {
			t.Fatal("Meshnet action missing", *calls)
		}
		if action == "activate" {
			active, statusErr := remoteProviderStatus(ctx, p)
			if statusErr != nil || !active.Connected || active.Meshnet != "Enabled" || active.NordName != "appliance-everest.nord" || active.IP != "100.64.0.2" {
				t.Fatal("active Meshnet status", active, statusErr)
			}
		}
	}
}

func TestNordStatusParsingNeverGuessesOrExposesAccount(t *testing.T) {
	if nordAccount("Account information\nEmail address: secret", nil) != "Logged in" || nordAccount("You are not logged in.", errors.New("exit")) != "Not logged in" || nordAccount("Disconnected", nil) != "Unknown" {
		t.Fatal("account evidence")
	}
	if nordMeshnet("Meshnet: enabled", nil) != "Enabled" || nordMeshnet("Meshnet: disabled", nil) != "Disabled" || nordMeshnet("Meshnet: maybe", nil) != "Unknown" || nordMeshnet("Meshnet: enabled", errors.New("exit")) != "Unknown" {
		t.Fatal("Meshnet evidence")
	}
	name, ip := nordIdentity("This device:\nHostname: local-everest.nord\nIP: 100.64.0.1\nLocal Peers:\nHostname: other-everest.nord\nIP: 100.64.0.2")
	if name != "local-everest.nord" || ip != "100.64.0.1" {
		t.Fatal(name, ip)
	}
	name, ip = nordIdentity("Hostname: peer-everest.nord\nIP: 100.64.0.2")
	if name != "" || ip != "" {
		t.Fatal("invented local identity")
	}
}

func TestNordStrictURLs(t *testing.T) {
	for _, good := range []string{"nordvpn://login?exchange_token=YWJj%2B%2F%3D", "nordvpn://login?exchange_token=abc_123-DEF", "nordvpn://login?action=login&exchange_token=abc&status=done"} {
		if !validNordCallback(good) {
			t.Fatal("valid callback rejected")
		}
	}
	for _, bad := range []string{"https://login?exchange_token=abc", "nordvpn://evil?exchange_token=abc", "nordvpn://login/path?exchange_token=abc", "nordvpn://user@login?exchange_token=abc", "nordvpn://login?exchange_token=abc&exchange_token=def", "nordvpn://login?exchange_token=abc&action=logout", "nordvpn://login?exchange_token=abc&url=https://evil.test", "nordvpn://login?exchange_token=abc#fragment", "nordvpn://login?exchange_token=abc%0a", strings.Repeat("x", 2049)} {
		if validNordCallback(bad) {
			t.Fatal("unsafe callback accepted")
		}
	}
	if validNordLoginURL("https://api.nordvpn.com/v1/users/oauth/login-redirect?attempt=example") == "" {
		t.Fatal("native login link rejected")
	}
	for _, bad := range []string{"http://api.nordvpn.com/v1/users/oauth/login", "https://api.nordvpn.com.evil.test/v1/users/oauth/login", "https://api.nordvpn.com:443/v1/users/oauth/login", "https://user@api.nordvpn.com/v1/users/oauth/login", "https://api.nordvpn.com/v1/users/oauth/login\n", "javascript:alert(1)", "https://evil.test/"} {
		if validNordLoginURL(bad) != "" {
			t.Fatal("untrusted login link accepted")
		}
	}
}

func TestNordLoginRequiredAndCallbackAuditPrivacy(t *testing.T) {
	calls := nordFixture(t)
	native := runNordCommand
	runNordCommand = func(ctx context.Context, args ...string) (string, error) {
		if strings.Join(args, " ") == "account" {
			return "You are not logged in.", errors.New("exit")
		}
		return native(ctx, args...)
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", `{"action":"activate"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"login_required":true`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, call := range *calls {
		if reflect.DeepEqual(call, []string{"set", "meshnet", "on"}) {
			t.Fatal("enabled while logged out")
		}
	}
	runNordCommand = native
	store, _ := openTestWebUIStore(t)
	s := adminServerForTest()
	s.store = store
	const sensitive = "never_log_this_exchange"
	for _, failure := range []bool{false, true} {
		runNordCommand = func(ctx context.Context, args ...string) (string, error) {
			if failure && len(args) == 3 && args[1] == "--callback" {
				return sensitive, errors.New(sensitive)
			}
			return native(ctx, args...)
		}
		rr = networkAdminRequest(s, "/v1/admin/network/remote-access/nordvpn", `{"action":"complete-login","callback":"nordvpn://login?exchange_token=`+sensitive+`"}`)
		want := 200
		if failure {
			want = 503
		}
		if rr.Code != want || strings.Contains(rr.Body.String(), sensitive) {
			t.Fatal("callback response privacy", rr.Code)
		}
	}
	events, err := store.listAuditEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(events)
	if len(events) != 2 || strings.Contains(string(encoded), sensitive) || strings.Contains(string(encoded), "nordvpn://") {
		t.Fatal("callback audit privacy")
	}
	before := len(*calls)
	rr = networkAdminRequest(s, "/v1/admin/network/remote-access/nordvpn", `{"action":"complete-login","callback":"https://evil.test"}`)
	if rr.Code != 400 || len(*calls) != before {
		t.Fatal("invalid callback reached command")
	}
}

func nordNativeFixture(t *testing.T) {
	t.Helper()
	oldCommand, oldHome := nordCommand, nordCLIHome
	nordCLIHome = filepath.Join(t.TempDir(), "nordvpn-cli")
	t.Cleanup(func() { nordCommand, nordCLIHome = oldCommand, oldHome })
}

func TestNordNativePrivateHome(t *testing.T) {
	nordNativeFixture(t)
	if _, err := os.Stat(nordCLIHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private HOME already exists", err)
	}
	var command *exec.Cmd
	nordCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != "/usr/bin/nordvpn" || !reflect.DeepEqual(args, []string{"settings"}) {
			t.Fatal("unexpected Nord command", name, args)
		}
		deadline, ok := ctx.Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > 4*time.Second {
			t.Fatal("Nord command deadline", deadline)
		}
		command = exec.CommandContext(ctx, "/bin/sh", "-c", `printf '%s' "$HOME"`)
		return command
	}
	output, err := runNordCommand(context.Background(), "settings")
	if err != nil || output != nordCLIHome {
		t.Fatal("subprocess HOME", output, err)
	}
	info, err := os.Stat(nordCLIHome)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatal("private HOME permissions", info.Mode())
	}
	wantEnv := []string{"PATH=/usr/sbin:/usr/bin", "HOME=" + nordCLIHome, "LANG=C", "LC_ALL=C", "NO_COLOR=1", "TERM=dumb"}
	if !reflect.DeepEqual(command.Env, wantEnv) {
		t.Fatal("restricted Nord environment changed")
	}
}

func TestNordNativePrivateHomeCreationFailure(t *testing.T) {
	nordNativeFixture(t)
	if err := os.WriteFile(nordCLIHome, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	nordCommand = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("Nord command reached after HOME creation failed")
		return nil
	}
	output, err := runNordCommand(context.Background(), "settings")
	if output != "" || err == nil || err.Error() != "NordVPN output unavailable" {
		t.Fatal("HOME creation failure must return a generic error", output, err)
	}
}

func TestNordNativeOutputAndTimeoutBounded(t *testing.T) {
	nordNativeFixture(t)
	nordCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != "/usr/bin/nordvpn" {
			t.Fatal(name)
		}
		return exec.CommandContext(ctx, "/bin/sh", "-c", "printf '%070000d' 1")
	}
	output, err := runNordCommand(context.Background(), "settings")
	if err == nil || output != "" {
		t.Fatal("oversized output accepted")
	}
	nordCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "/bin/sleep", "10")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	output, err = runNordCommand(ctx, "settings")
	if err == nil || output != "" {
		t.Fatal("timeout accepted")
	}
}

func TestNordAdministratorOnlyAndUnavailableDoesNotHideOtherProviders(t *testing.T) {
	calls := nordFixture(t)
	for _, role := range []principalRole{roleViewer, roleOperator} {
		rr := networkAdminRequest(roleServerForTest(role), "/v1/admin/network/remote-access/nordvpn", `{"action":"login"}`)
		if rr.Code != 403 || len(*calls) != 0 {
			t.Fatal("non-administrator reached Nord")
		}
	}
	original := remoteSystemctl
	remoteSystemctl = func(ctx context.Context, args ...string) (string, error) {
		for _, arg := range args {
			if arg == "nordvpnd.service" {
				return "", errors.New("unavailable")
			}
		}
		return original(ctx, args...)
	}
	s := roleServerForTest(roleOperator)
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodGet, "/v1/network/remote-access", ""))
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	for _, id := range []string{"tailscale", "netbird", "playit", "nordvpn"} {
		if !strings.Contains(rr.Body.String(), `"id":"`+id+`"`) {
			t.Fatal("provider hidden", id)
		}
	}
	if !strings.Contains(rr.Body.String(), `"status_unavailable":true`) {
		t.Fatal("unavailable Nord state missing")
	}
}

func TestNordLoginStartAndPersistentSocketLifecycle(t *testing.T) {
	nordFixture(t)
	var calls []string
	remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}
	for _, action := range []string{"login", "activate", "deactivate"} {
		rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", `{"action":"`+action+`"}`)
		if rr.Code != 200 {
			t.Fatal(action, rr.Code)
		}
		if action == "login" && !strings.Contains(rr.Body.String(), "https://api.nordvpn.com/v1/users/oauth/login-redirect?attempt=example") {
			t.Fatal("login URL missing")
		}
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"enable --now nordvpnd.socket nordvpnd.service", "stop nordvpnd.socket nordvpnd.service", "disable nordvpnd.socket nordvpnd.service"} {
		if !strings.Contains(joined, want) {
			t.Fatal("persistent lifecycle missing", want)
		}
	}
}
