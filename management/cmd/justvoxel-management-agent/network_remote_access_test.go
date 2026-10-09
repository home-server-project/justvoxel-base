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

func remoteFixture(t *testing.T) *[]string {
	t.Helper()
	oldRun, oldStat, oldStatus, oldTailscaleLogin, oldNetbirdStatus, oldNetbirdLogin := remoteSystemctl, remoteStat, tailscaleStatus, tailscaleLogin, netbirdStatus, netbirdLogin
	netbirdStatus = func(context.Context) (bool, string) { return false, "" }
	netbirdLogin = func() (string, error) { return "https://app.netbird.io/login?code=example", nil }
	tailscaleStatus = func(context.Context) (bool, string, string) { return false, "Not configured", "" }
	tailscaleLogin = func() (string, error) { return "https://login.tailscale.com/a/0123456789abcdef", nil }
	calls := []string{}
	remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "show" {
			return "LoadState=loaded\nUnitFileState=enabled\nActiveState=active", nil
		}
		return "", nil
	}
	remoteStat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { remoteSystemctl = oldRun; remoteStat = oldStat; tailscaleStatus = oldStatus; tailscaleLogin = oldTailscaleLogin; netbirdStatus = oldNetbirdStatus; netbirdLogin = oldNetbirdLogin })
	return &calls
}

func TestRemoteAccessExactlyThreeProvidersAndUnconfiguredPlayitRunning(t *testing.T) {
	remoteFixture(t)
	s := roleServerForTest(roleOperator)
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodGet, "/v1/network/remote-access", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if len(remoteProviders) != 3 {
		t.Fatal("unexpected provider count")
	}
	for _, id := range []string{"tailscale", "netbird", "playit"} {
		if !strings.Contains(rr.Body.String(), `"id":"`+id+`"`) {
			t.Fatalf("missing %s", id)
		}
	}
	provider, _ := remoteProviderByID("playit")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || !status.Installed || !status.Enabled || !status.Active || status.Configured || status.ServiceState == "failed" {
		t.Fatalf("fresh Playit status %+v, err %v", status, err)
	}
}

func TestRemoteProviderLifecycleUsesFixedUnitsAndAudits(t *testing.T) {
	calls := remoteFixture(t)
	store, _ := openTestWebUIStore(t)
	s := adminServerForTest()
	s.store = store
	for _, provider := range remoteProviders {
		for _, action := range []string{"activate", "deactivate"} {
			*calls = nil
			rr := networkAdminRequest(s, "/v1/admin/network/remote-access/"+provider.id, `{"action":"`+action+`"}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s %s: %s", provider.id, action, rr.Body.String())
			}
			joined := strings.Join(*calls, "\n")
			if action == "activate" {
				if provider.id == "netbird" || provider.id == "tailscale" {
					if !strings.Contains(joined, "enable "+provider.unit) || !strings.Contains(joined, "start --no-block "+provider.unit) {
						t.Fatalf("%s activation blocked login", provider.id)
					}
				} else if !strings.Contains(joined, "enable --now "+provider.unit) {
					t.Fatal("activate did not enable and start fixed unit")
				}
			}
			if action == "deactivate" && (!strings.Contains(joined, "stop "+provider.unit) || !strings.Contains(joined, "disable "+provider.unit)) {
				t.Fatal("deactivate did not stop and disable fixed unit")
			}
		}
	}
	remoteSystemctl = func(context.Context, ...string) (string, error) { return "", errors.New("systemctl failed") }
	if rr := networkAdminRequest(s, "/v1/admin/network/remote-access/playit", `{"action":"activate"}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatal("failed lifecycle was reported successful")
	}
	events, err := store.listAuditEvents(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 || events[0].Success || events[0].Action != "network_remote_access_activate" {
		t.Fatalf("lifecycle audit events %+v", events)
	}
	for _, event := range events {
		if !strings.HasPrefix(event.Action, "network_remote_access_") {
			t.Fatalf("unexpected action %s", event.Action)
		}
	}
}


func TestNetBirdActivationReturnsCLILoginURL(t *testing.T) {
	remoteFixture(t)
	calls := 0
	netbirdLogin = func() (string, error) {
		calls++
		return "https://login.netbird.io/device?user_code=abcd", nil
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/netbird", `{"action":"activate"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"login_url":"https://login.netbird.io/device?user_code=abcd"`) || calls != 1 {
		t.Fatalf("NetBird login did not return CLI URL: code=%d body=%s calls=%d", rr.Code, rr.Body.String(), calls)
	}
}

func TestNetBirdConnectedShowsAddressWithoutStartingLogin(t *testing.T) {
	remoteFixture(t)
	path := filepath.Join(t.TempDir(), "netbird-state")
	if err := os.WriteFile(path, []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteStat = func(string) (os.FileInfo, error) { return os.Stat(path) }
	netbirdStatus = func(context.Context) (bool, string) { return true, "100.90.1.4/16" }
	netbirdLogin = func() (string, error) { t.Fatal("login must not start when connected"); return "", nil }
	netbird, _ := remoteProviderByID("netbird")
	status, err := remoteProviderStatus(context.Background(), netbird)
	if err != nil || !status.Connected || !status.Configured || status.Summary != "Connected" || status.IP != "100.90.1.4/16" {
		t.Fatalf("NetBird status: %+v, %v", status, err)
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/netbird", `{"action":"activate"}`)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "login.netbird.io") {
		t.Fatalf("connected NetBird unexpectedly requested login: %d %s", rr.Code, rr.Body.String())
	}
}

func TestNetBirdLoginURLRejectsUnsafeLinks(t *testing.T) {
	for _, candidate := range []string{"http://app.netbird.io/", "javascript:alert(1)", "https://evil.test\nhttps://app.netbird.io/", "https://user:pass@app.netbird.io/", "https://app.netbird.io/ invalid"} {
		if validNetbirdLoginURL(candidate) != "" {
			t.Fatalf("accepted unsafe URL %q", candidate)
		}
	}
	if validNetbirdLoginURL("https://app.netbird.io/device?user_code=1234") == "" {
		t.Fatal("rejected valid NetBird URL")
	}
}

func TestRemoteProviderRejectsArbitraryNamesAndReadOnlyMutations(t *testing.T) {
	calls := remoteFixture(t)
	for _, id := range []string{"sshd.service", "unknown", "tailscaled.service"} {
		if rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/"+id, `{"action":"activate"}`); rr.Code != http.StatusBadRequest {
			t.Fatalf("arbitrary provider %s accepted", id)
		}
	}
	for _, role := range []principalRole{roleOperator, roleViewer} {
		if rr := networkAdminRequest(roleServerForTest(role), "/v1/admin/network/remote-access/playit", `{"action":"activate"}`); rr.Code != http.StatusForbidden {
			t.Fatalf("read-only role %s accepted", role)
		}
	}
	if len(*calls) != 0 {
		t.Fatal("rejected lifecycle reached systemctl")
	}
}

func TestPlayitConfigurationEvidenceOnlyUsesExpectedFile(t *testing.T) {
	remoteFixture(t)
	path := filepath.Join(t.TempDir(), "evidence")
	if err := os.WriteFile(path, []byte("private-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	var checked []string
	remoteStat = func(name string) (os.FileInfo, error) { checked = append(checked, name); return os.Stat(path) }
	provider, _ := remoteProviderByID("playit")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || !status.Configured {
		t.Fatalf("configured status %+v, %v", status, err)
	}
	if strings.Join(checked, ",") != "/etc/playit/playit.toml" {
		t.Fatalf("unexpected configuration paths %v", checked)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "private-secret") {
		t.Fatal("secret leaked")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if remoteConfigured(provider) {
		t.Fatal("empty configuration classified as configured")
	}
}

func TestRemoteProviderTransitionalLifecycle(t *testing.T) {
	remoteFixture(t)
	for _, state := range []string{"activating", "deactivating", "reloading", "refreshing"} {
		var mutations int
		remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
			if args[0] == "show" {
				return "LoadState=loaded\nUnitFileState=enabled\nActiveState=" + state, nil
			}
			mutations++
			return "", nil
		}
		for _, action := range []string{"activate", "deactivate"} {
			rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/netbird", `{"action":"`+action+`"}`)
			want := http.StatusConflict
			if (action == "deactivate" && state != "deactivating") || (action == "activate" && state == "activating") {
				want = http.StatusOK
			}
			if rr.Code != want {
				t.Fatalf("%s %s: %d", state, action, rr.Code)
			}
		}
		want := 2
		if state == "deactivating" {
			want = 0
		} else if state == "activating" {
			want = 4
		}
		if mutations != want {
			t.Fatalf("unexpected mutations: %d", mutations)
		}
	}
}

func TestTailscaleStatusHelperProcess(t *testing.T) {
	if os.Getenv("JUSTVOXEL_TAILSCALE_STATUS_FIXTURE") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(os.Getenv("JUSTVOXEL_TAILSCALE_JSON"))
	os.Exit(0)
}

func TestTailscaleFixedStatusIdentityEvidence(t *testing.T) {
	oldCommand := tailscaleStatusCommand
	t.Cleanup(func() { tailscaleStatusCommand = oldCommand })
	for _, test := range []struct {
		name, payload, summary, ip string
		configured                 bool
	}{
		{"needs-login", `{"BackendState":"NeedsLogin","HaveNodeKey":true,"CurrentTailnet":{}}`, "Not configured", "", false},
		{"no-state", `{"BackendState":"NoState","HaveNodeKey":true,"CurrentTailnet":{}}`, "Not configured", "", false},
		{"running", `{"BackendState":"Running","HaveNodeKey":true,"TailscaleIPs":["fd7a:115c:a1e0::5","100.111.12.13"],"CurrentTailnet":{"Name":"private-account"}}`, "Connected", "100.111.12.13", true},
		{"approval", `{"BackendState":"NeedsMachineAuth"}`, "Awaiting approval", "", true},
		{"starting-no-identity", `{"BackendState":"Starting"}`, "Not configured", "", false},
		{"starting-key", `{"BackendState":"Starting","HaveNodeKey":true}`, "Starting", "", true},
		{"stopped-no-identity", `{"BackendState":"Stopped","CurrentTailnet":null}`, "Not configured", "", false},
		{"stopped-tailnet", `{"BackendState":"Stopped","CurrentTailnet":{}}`, "Stopped", "", true},
		{"unknown", `{"BackendState":"Unknown","HaveNodeKey":true}`, "Not configured", "", false},
		{"invalid", `{`, "Not configured", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tailscaleStatusCommand = func(ctx context.Context, executable string, args ...string) *exec.Cmd {
				if executable != "/usr/bin/tailscale" || !reflect.DeepEqual(args, []string{"status", "--json"}) {
					t.Fatalf("nonfixed command: %s %v", executable, args)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Fatal("status query is not bounded")
				}
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTailscaleStatusHelperProcess$")
				cmd.Env = append(os.Environ(), "JUSTVOXEL_TAILSCALE_STATUS_FIXTURE=1", "JUSTVOXEL_TAILSCALE_JSON="+test.payload)
				return cmd
			}
			configured, summary, ip := tailscaleStatus(context.Background())
			if configured != test.configured || summary != test.summary || ip != test.ip {
				t.Fatalf("configured=%v summary=%q ip=%q", configured, summary, ip)
			}
		})
	}
	tailscaleStatusCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "/nonexistent/justvoxel-test-tailscale")
	}
	if configured, summary, ip := tailscaleStatus(context.Background()); configured || summary != "Not configured" || ip != "" {
		t.Fatal("command failure claimed configuration")
	}
}

func TestTailscaleActivationReturnsMachineAuthURL(t *testing.T) {
	remoteFixture(t)
	calls := 0
	tailscaleLogin = func() (string, error) {
		calls++
		return "https://login.tailscale.com/a/test1234", nil
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/tailscale", `{"action":"activate"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"login_url":"https://login.tailscale.com/a/test1234"`) || calls != 1 {
		t.Fatalf("Tailscale link: code=%d body=%s calls=%d", rr.Code, rr.Body.String(), calls)
	}
}

func TestTailscaleAlreadyConnectedDoesNotRequestLogin(t *testing.T) {
	remoteFixture(t)
	tailscaleStatus = func(context.Context) (bool, string, string) { return true, "Connected", "100.101.102.103" }
	tailscaleLogin = func() (string, error) { t.Fatal("connected Tailscale should not request login"); return "", nil }
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/tailscale", `{"action":"activate"}`)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "login.tailscale.com") {
		t.Fatalf("connected Tailscale login: code=%d body=%s", rr.Code, rr.Body.String())
	}
	provider, _ := remoteProviderByID("tailscale")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || !status.Connected || status.IP != "100.101.102.103" {
		t.Fatalf("Tailscale IP: %+v err=%v", status, err)
	}
}

func TestTailscaleAuthURLIsMachineSpecific(t *testing.T) {
	for _, candidate := range []string{
		"https://console.tailscale.com/admin/", "https://login.tailscale.com/",
		"https://evil.test/a/test123", "http://login.tailscale.com/a/test123",
		"https://evil.test@login.tailscale.com/a/test123", "javascript:alert(1)",
		"https://login.tailscale.com/a/invalid value",
	} {
		if validTailscaleLoginURL(candidate) != "" {
			t.Fatalf("accepted non-machine auth URL %q", candidate)
		}
	}
	if validTailscaleLoginURL("https://login.tailscale.com/a/0123456789abcdef") == "" {
		t.Fatal("valid machine auth URL rejected")
	}
}

func TestTailscaleUpCLIHelper(t *testing.T) {
	if os.Getenv("JUSTVOXEL_TEST_TAILSCALE_UP") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("{\"AuthURL\":\"https://login.tailscale.com/a/0123456789abcdef\",\"BackendState\":\"NeedsLogin\"}\n")
	time.Sleep(40 * time.Millisecond)
	_, _ = os.Stdout.WriteString("{\"BackendState\":\"Running\"}\n")
	os.Exit(0)
}

func TestTailscaleUpReturnsLinkBeforeLoginFinishes(t *testing.T) {
	old := tailscaleUpCommand
	t.Cleanup(func() { tailscaleUpCommand = old })
	tailscaleUpCommand = func(ctx context.Context, executable string, args ...string) *exec.Cmd {
		if executable != "/usr/bin/tailscale" || !reflect.DeepEqual(args, []string{"up", "--json"}) {
			t.Fatalf("unexpected command %s %v", executable, args)
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTailscaleUpCLIHelper$")
		cmd.Env = append(os.Environ(), "JUSTVOXEL_TEST_TAILSCALE_UP=1")
		return cmd
	}
	url, err := tailscaleLogin()
	if err != nil || url != "https://login.tailscale.com/a/0123456789abcdef" {
		t.Fatalf("login result %q, %v", url, err)
	}
}

func TestTailscaleStateFileAloneIsNotConfiguration(t *testing.T) {
	remoteFixture(t)
	path := filepath.Join(t.TempDir(), "tailscaled.state")
	if err := os.WriteFile(path, []byte("fresh daemon state"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteStat = func(string) (os.FileInfo, error) { return os.Stat(path) }
	provider, _ := remoteProviderByID("tailscale")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || status.Configured || status.Connected || remoteConfigured(provider) {
		t.Fatalf("state file claimed configuration: %+v %v", status, err)
	}
}

func TestNetBirdActivatingDeactivateUsesOnlyFixedLifecycle(t *testing.T) {
	remoteFixture(t)
	var mutations []string
	remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "show" {
			return "LoadState=loaded\nUnitFileState=enabled\nActiveState=activating", nil
		}
		mutations = append(mutations, strings.Join(args, " "))
		return "", nil
	}
	rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/netbird", `{"action":"deactivate"}`)
	if rr.Code != http.StatusOK || !reflect.DeepEqual(mutations, []string{"stop netbird.service", "disable netbird.service"}) {
		t.Fatalf("deactivate: %d %v", rr.Code, mutations)
	}
}

func TestCanonicalProviderSummariesFollowConfigurationAndService(t *testing.T) {
	remoteFixture(t)
	path := filepath.Join(t.TempDir(), "configuration")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id, state, summary    string
		configured, connected bool
	}{
		{"netbird", "activating", "Not configured", false, false},
		{"netbird", "active", "Not connected", true, false},
		{"netbird", "inactive", "Stopped", true, false},
		{"playit", "active", "Not configured", false, false},
		{"playit", "active", "Running", true, true},
		{"playit", "inactive", "Configured", true, false},
	} {
		remoteStat = func(string) (os.FileInfo, error) {
			if test.configured {
				return os.Stat(path)
			}
			return nil, os.ErrNotExist
		}
		remoteSystemctl = func(context.Context, ...string) (string, error) {
			return "LoadState=loaded\nUnitFileState=enabled\nActiveState=" + test.state, nil
		}
		provider, _ := remoteProviderByID(test.id)
		status, err := remoteProviderStatus(context.Background(), provider)
		if err != nil || status.Configured != test.configured || status.Connected != test.connected || status.Summary != test.summary {
			t.Fatalf("%s %s: %+v %v", test.id, test.state, status, err)
		}
	}
	tailscaleStatus = func(context.Context) (bool, string, string) { return true, "Connected", "100.100.1.2" }
	remoteSystemctl = func(context.Context, ...string) (string, error) {
		return "LoadState=loaded\nUnitFileState=enabled\nActiveState=inactive", nil
	}
	provider, _ := remoteProviderByID("tailscale")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || !status.Configured || status.Connected || status.Summary != "Stopped" {
		t.Fatalf("stopped Tailscale: %+v %v", status, err)
	}
}
