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
		}
		if len(args) == 3 && args[0] == "login" && args[1] == "--token" {
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

func TestNordTokenValidationAndRejectedActions(t *testing.T) {
	calls := nordFixture(t)
	for _, token := range []string{"", strings.Repeat("a", 4097), "a b", "a\tb", "a\rb", "a\nb", "a\x00b", "a\x1fb", "a\x7fb", "é", string([]byte{0xff})} {
		if validNordAccessToken(token) {
			t.Fatal("invalid token accepted")
		}
		body, _ := json.Marshal(map[string]string{"action": "token-login", "token": token})
		rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", string(body))
		if rr.Code != 400 || len(*calls) != 0 {
			t.Fatal("invalid token reached command")
		}
	}
	for _, token := range []string{"opaque+/_-.~=token", strings.Repeat("a", 4096), "!#$%&'()*,:;<=>?@[\\]^\x60{|}"} {
		if !validNordAccessToken(token) {
			t.Fatal("visible ASCII rejected")
		}
	}
	for _, action := range []string{"login", "complete-login"} {
		rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", `{"action":"`+action+`"}`)
		if rr.Code != 400 || len(*calls) != 0 {
			t.Fatal("obsolete action accepted")
		}
	}
}

func TestNordActivateLoginRequired(t *testing.T) {
	calls := nordFixture(t)
	runNordCommand = func(_ context.Context, args ...string) (string, error) {
		*calls = append(*calls, append([]string(nil), args...))
		return "You are not logged in.", errors.New("exit")
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", `{"action":"activate"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"login_required":true`) ||
		!reflect.DeepEqual(*calls, [][]string{{"account"}}) {
		t.Fatal("logged-out activation changed", rr.Code)
	}
}

func TestNordTokenLoginSequenceAndPrivacy(t *testing.T) {
	for _, stage := range []string{"success", "login", "account", "account unknown", "enable", "enable error but enabled", "verify"} {
		t.Run(stage, func(t *testing.T) {
			calls := nordFixture(t)
			native := runNordCommand
			const token = "never_log_this+/_-.~=opaque"
			const privateOutput = "private vendor output " + token
			runNordCommand = func(ctx context.Context, args ...string) (string, error) {
				fail := (stage == "login" && args[0] == "login") ||
					(stage == "account" && args[0] == "account") ||
					((stage == "enable" || stage == "enable error but enabled") && args[0] == "set") ||
					(stage == "verify" && args[0] == "settings")
				if stage == "account unknown" && args[0] == "account" {
					*calls = append(*calls, append([]string(nil), args...))
					return privateOutput, nil
				}
				if stage == "enable error but enabled" && args[0] == "settings" {
					*calls = append(*calls, append([]string(nil), args...))
					return "Meshnet: enabled", nil
				}
				if fail {
					*calls = append(*calls, append([]string(nil), args...))
					return privateOutput, errors.New(privateOutput)
				}
				return native(ctx, args...)
			}
			store, _ := openTestWebUIStore(t)
			s := adminServerForTest()
			s.store = store
			body, _ := json.Marshal(map[string]string{"action": "token-login", "token": token})
			rr := networkAdminRequest(s, "/v1/admin/network/remote-access/nordvpn", string(body))
			wantCode := 503
			want := [][]string{{"login", "--token", token}, {"account"}, {"set", "meshnet", "on"}, {"settings"}}
			switch stage {
			case "success", "enable error but enabled":
				wantCode = 200
			case "login":
				want = want[:1]
			case "account", "account unknown":
				want = want[:2]
			}
			if rr.Code != wantCode || !reflect.DeepEqual(*calls, want) {
				t.Fatal("token argv or verified sequence changed", rr.Code)
			}
			if strings.Contains(rr.Body.String(), token) || strings.Contains(rr.Body.String(), "private vendor") {
				t.Fatal("response leaked credential/vendor output")
			}
			if wantCode != http.StatusOK {
				wantMessage := "NordVPN login could not be completed. Check the access token and retry."
				if stage == "enable" || stage == "verify" {
					wantMessage = "NordVPN login succeeded, but Meshnet could not be activated. Retry Activate or open Troubleshoot."
				}
				var response struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Error != wantMessage {
					t.Fatal("incorrect login stage message", response.Error)
				}
			} else if !strings.Contains(rr.Body.String(), `"ok":true`) {
				t.Fatal("missing success response")
			}
			events, err := store.listAuditEvents(10)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(events)
			if len(events) != 1 || strings.Contains(string(encoded), token) || strings.Contains(string(encoded), "private vendor") {
				t.Fatal("audit leaked credential/vendor output")
			}
		})
	}
}

func nordNativeFixture(t *testing.T) {
	t.Helper()
	oldCommand, oldHome := nordCommand, nordCLIHome
	nordCLIHome = filepath.Join(t.TempDir(), "nordvpn-cli")
	t.Cleanup(func() { nordCommand, nordCLIHome = oldCommand, oldHome })
}

func TestNordNativePrivateHome(t *testing.T) {
	if nordCommandTimeout != 10*time.Second {
		t.Fatal("Nord command timeout changed", nordCommandTimeout)
	}
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
		if remaining := time.Until(deadline); !ok || remaining < 9*time.Second || remaining > 10*time.Second {
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
		rr := networkAdminRequest(roleServerForTest(role), "/v1/admin/network/remote-access/nordvpn", `{"action":"token-login","token":"opaque"}`)
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
	for _, action := range []string{"token-login", "activate", "deactivate"} {
		body := `{"action":"` + action + `"}`
		if action == "token-login" {
			body = `{"action":"token-login","token":"opaque"}`
		}
		rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", body)
		if rr.Code != 200 {
			t.Fatal(action, rr.Code)
		}
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"enable --now nordvpnd.socket nordvpnd.service", "stop nordvpnd.socket nordvpnd.service", "disable nordvpnd.socket nordvpnd.service"} {
		if !strings.Contains(joined, want) {
			t.Fatal("persistent lifecycle missing", want)
		}
	}
}

func TestNordDeactivateVerifiesMeshnetBeforeStopping(t *testing.T) {
	for _, tc := range []struct {
		name, initial, followup      string
		readErr, setErr, followupErr error
		setOff, success              bool
	}{
		{name: "already disabled", initial: "Meshnet: disabled", success: true},
		{name: "enabled", initial: "Meshnet: enabled", followup: "Meshnet: disabled", setOff: true, success: true},
		{name: "concurrent disable", initial: "Meshnet: enabled", followup: "Meshnet: disabled", setErr: errors.New("private vendor error"), setOff: true, success: true},
		{name: "unknown", initial: "Meshnet: maybe"},
		{name: "ambiguous", initial: "Meshnet: enabled\nMeshnet: disabled"},
		{name: "settings error", initial: "Meshnet: disabled", readErr: errors.New("private settings error")},
		{name: "still enabled", initial: "Meshnet: enabled", followup: "Meshnet: enabled", setOff: true},
		{name: "set failed still enabled", initial: "Meshnet: enabled", followup: "Meshnet: enabled", setErr: errors.New("private vendor error"), setOff: true},
		{name: "followup unknown", initial: "Meshnet: enabled", followup: "Meshnet: maybe", setOff: true},
		{name: "followup failed", initial: "Meshnet: enabled", followup: "Meshnet: disabled", followupErr: errors.New("private followup error"), setOff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nordFixture(t)
			var calls []string
			reads := 0
			runNordCommand = func(ctx context.Context, args ...string) (string, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded Nord operation")
				}
				call := strings.Join(args, " ")
				calls = append(calls, call)
				switch call {
				case "settings":
					reads++
					if reads == 1 {
						return tc.initial, tc.readErr
					}
					return tc.followup, tc.followupErr
				case "set meshnet off":
					return "private vendor output", tc.setErr
				default:
					t.Fatal("unexpected Nord command", args)
					return "", errors.New("unexpected command")
				}
			}
			remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
				calls = append(calls, strings.Join(args, " "))
				return "", nil
			}
			rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", `{"action":"deactivate"}`)
			want := []string{"start nordvpnd.socket nordvpnd.service", "settings"}
			if tc.setOff {
				want = append(want, "set meshnet off", "settings")
			}
			if tc.success {
				want = append(want, "stop nordvpnd.socket nordvpnd.service", "disable nordvpnd.socket nordvpnd.service")
				if rr.Code != http.StatusOK {
					t.Fatal(rr.Code, rr.Body.String())
				}
			} else {
				var response struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if rr.Code != http.StatusServiceUnavailable || response.Error != "NordVPN could not be deactivated. Open Troubleshoot and generate a Network report before retrying." {
					t.Fatal(rr.Code, rr.Body.String())
				}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("commands %v, want %v", calls, want)
			}
		})
	}
}

func TestNordActionFailuresAreSafeAndActionable(t *testing.T) {
	for _, tc := range []struct{ action, message string }{
		{"activate", "NordVPN could not be activated. Open Troubleshoot and generate a Network report before retrying."},
		{"deactivate", "NordVPN could not be deactivated. Open Troubleshoot and generate a Network report before retrying."},
		{"token-login", "NordVPN login could not be completed. Check the access token and retry."},
	} {
		t.Run(tc.action, func(t *testing.T) {
			nordFixture(t)
			remoteSystemctl = func(context.Context, ...string) (string, error) {
				return "private vendor output", errors.New("private command error")
			}
			body := `{"action":"` + tc.action + `"}`
			if tc.action == "token-login" {
				body = `{"action":"token-login","token":"private_token"}`
			}
			rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", body)
			var response struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rr.Code != http.StatusServiceUnavailable || response.Error != tc.message || strings.Contains(rr.Body.String(), "private") || strings.Contains(rr.Body.String(), "NordVPN operation failed; refresh provider status and retry") {
				t.Fatal(rr.Code, rr.Body.String())
			}
		})
	}
}

func TestNordNativeTokenLoginDeclinesAnalytics(t *testing.T) {
	nordNativeFixture(t)
	const token = "opaque+/_-.~=token"
	for _, args := range [][]string{{"login"}, {"login", "--token", token}, {"settings"}} {
		var command *exec.Cmd
		nordCommand = func(ctx context.Context, name string, got ...string) *exec.Cmd {
			if name != "/usr/bin/nordvpn" || !reflect.DeepEqual(got, args) {
				t.Fatal("direct executable/argv changed")
			}
			command = exec.CommandContext(ctx, "/bin/cat")
			return command
		}
		output, err := runNordCommand(context.Background(), args...)
		want := ""
		if args[0] == "login" {
			want = "no\n"
		}
		if err != nil || output != want || (args[0] != "login" && command.Stdin != nil) {
			t.Fatal("login analytics input changed")
		}
	}
}

// JSON escaping must not reduce the accepted 4096-byte opaque token bound.
func TestNordTokenLoginMaximumEscapedToken(t *testing.T) {
	calls := nordFixture(t)
	token := strings.Repeat("<", 4096)
	body, err := json.Marshal(map[string]string{"action": "token-login", "token": token})
	if err != nil {
		t.Fatal(err)
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", string(body))
	if rr.Code != 200 || len(*calls) != 4 || !reflect.DeepEqual((*calls)[0], []string{"login", "--token", token}) {
		t.Fatal("escaped token bound or opaque argv changed", rr.Code)
	}
}

func TestNordOperationTimeoutRemains25Seconds(t *testing.T) {
	nordFixture(t)
	native := runNordCommand
	runNordCommand = func(ctx context.Context, args ...string) (string, error) {
		deadline, ok := ctx.Deadline()
		if remaining := time.Until(deadline); !ok || remaining < 24*time.Second || remaining > 25*time.Second {
			t.Fatal("Nord operation deadline changed", deadline)
		}
		return native(ctx, args...)
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/nordvpn", `{"action":"token-login","token":"opaque"}`)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
}

func TestNordEnableVerifiesSettingsAfterSetFailure(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		settingsErr    error
		success        bool
	}{
		{name: "enabled", settings: "Meshnet: enabled", success: true},
		{name: "disabled", settings: "Meshnet: disabled"},
		{name: "unknown", settings: "private vendor output"},
		{name: "error", settings: "Meshnet: enabled", settingsErr: errors.New("private settings error")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nordFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			var calls []string
			runNordCommand = func(gotCtx context.Context, args ...string) (string, error) {
				if gotCtx != ctx {
					t.Fatal("follow-up must use the parent operation context")
				}
				call := strings.Join(args, " ")
				calls = append(calls, call)
				switch call {
				case "set meshnet on":
					return "private vendor output", errors.New("private set error")
				case "settings":
					return tc.settings, tc.settingsErr
				default:
					t.Fatal("unexpected Nord command", call)
					return "", errors.New("unexpected command")
				}
			}
			err := nordEnableMeshnet(ctx)
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != "Meshnet activation could not be verified" {
				t.Fatal("unsafe activation failure", err)
			}
			if !reflect.DeepEqual(calls, []string{"set meshnet on", "settings"}) {
				t.Fatal("authoritative verification sequence", calls)
			}
		})
	}
}
