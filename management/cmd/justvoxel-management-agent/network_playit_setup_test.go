package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setupFixture(t *testing.T) string {
	t.Helper()
	remoteFixture(t)
	oldRun, oldStat, oldTimeout, oldCommand := playitSetupRun, playitExecutableStat, playitSetupTimeout, playitSetupCommand
	playitSetup.mu.Lock()
	playitSetup.generation++
	playitSetup.cancel, playitSetup.done = nil, nil
	playitSetup.state = playitSetupState{State: "idle"}
	playitSetup.mu.Unlock()
	executable := filepath.Join(t.TempDir(), "executable-evidence")
	if err := os.WriteFile(executable, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	playitExecutableStat = func(path string) (os.FileInfo, error) {
		if path != "/usr/bin/playit" {
			t.Errorf("unexpected executable %s", path)
		}
		return os.Stat(executable)
	}
	t.Cleanup(func() {
		playitSetupRun, playitExecutableStat, playitSetupTimeout, playitSetupCommand = oldRun, oldStat, oldTimeout, oldCommand
		playitSetup.mu.Lock()
		playitSetup.state = playitSetupState{State: "idle"}
		playitSetup.mu.Unlock()
	})
	return executable
}

func setupRequest(s *server, method string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(method, "/v1/admin/network/remote-access/playit/setup", ""))
	return rr
}

func waitSetup(t *testing.T, state string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		playitSetup.mu.Lock()
		current := playitSetup.state.State
		playitSetup.mu.Unlock()
		if current == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("setup did not reach %s", state)
}

func TestPlayitSetupAuthorization(t *testing.T) {
	setupFixture(t)
	playitSetup.mu.Lock()
	playitSetup.state = playitSetupState{State: "waiting", ClaimURL: "https://playit.gg/claim/0123abcdef"}
	playitSetup.mu.Unlock()
	for _, role := range []principalRole{roleOperator, roleViewer} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rr := setupRequest(roleServerForTest(role), method)
			if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), "0123abcdef") {
				t.Fatalf("claim authorization: %d %s", rr.Code, rr.Body.String())
			}
		}
	}
}

func TestPlayitSetupSingleProcessCompletesWithoutOutputOrAuditSecrets(t *testing.T) {
	path := setupFixture(t)
	store, _ := openTestWebUIStore(t)
	s := adminServerForTest()
	s.store = store
	var configured atomic.Bool
	remoteStat = func(string) (os.FileInfo, error) {
		if configured.Load() {
			return os.Stat(path)
		}
		return nil, os.ErrNotExist
	}
	var starts atomic.Int32
	release := make(chan struct{})
	playitSetupRun = func(ctx context.Context, output io.Writer) error {
		starts.Add(1)
		fmt.Fprint(output, "secret=private-secret https://evil.test/claim/abcdef\nhttps://playit.gg/claim/0123a")
		fmt.Fprint(output, "bcdef\nraw sensitive stderr\n")
		select {
		case <-release:
			configured.Store(true)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusAccepted {
		t.Fatal(rr.Body.String())
	}
	waitSetup(t, "waiting")
	var requests sync.WaitGroup
	for i := 0; i < 5; i++ {
		requests.Add(1)
		go func() {
			defer requests.Done()
			rr := setupRequest(s, http.MethodPost)
			if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), "https://playit.gg/claim/0123abcdef") || strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "stderr") {
				t.Error(rr.Body.String())
			}
		}()
	}
	requests.Wait()
	if starts.Load() != 1 {
		t.Fatal("concurrent setup processes")
	}
	if rr := networkAdminRequest(s, "/v1/admin/network/remote-access/playit", `{"action":"activate"}`); rr.Code != http.StatusConflict {
		t.Fatal("setup allowed lifecycle change")
	}
	close(release)
	waitSetup(t, "complete")
	if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusConflict {
		t.Fatal("configured identity replaced")
	}
	provider, _ := remoteProviderByID("playit")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || !status.Configured {
		t.Fatal("setup did not become configured")
	}
	events, err := store.listAuditEvents(10)
	if err != nil || len(events) != 2 {
		t.Fatalf("events %v %v", events, err)
	}
	payload, _ := json.Marshal(events)
	for _, sensitive := range []string{"private-secret", "0123abcdef", "claim", "stderr"} {
		if strings.Contains(string(payload), sensitive) {
			t.Fatalf("audit contains %s", sensitive)
		}
	}
	if events[0].Action != "network_playit_setup_complete" || events[1].Action != "network_playit_setup_start" {
		t.Fatal("missing lifecycle audit")
	}
}

func TestPlayitClaimParserOnlyAcceptsCompleteFixedHexURL(t *testing.T) {
	for _, input := range []string{
		"https://playit.gg/claim/0123abcdef",
		"https://playit.gg/claim/A1B2C3D4E5",
		"https://playit.gg/claim/0123abcde",
		"https://playit.gg/claim/0123abcdef0",
		"https://playit.gg/claim/0123abcdeg",
		"https://playit.gg/claim/0123abcdef?secret=x",
		"https://playit.gg/claim/0123abcdef/path",
		"http://playit.gg/claim/0123abcdef",
		"https://evil.test/claim/0123abcdef",
		"https://playit.gg/claim/",
		"prefixhttps://playit.gg/claim/0123abcdef",
		"https://playit.gg/claim/0123abcdefsuffix",
		"https://playit.gg/claim/" + strings.Repeat("a", 300),
	} {
		var got string
		w := &playitClaimWriter{onClaim: func(url string) { got = url }}
		for _, b := range []byte(input) {
			_, _ = w.Write([]byte{b})
		}
		if got != "" {
			t.Fatal("accepted unterminated token")
		}
		_, _ = w.Write([]byte("\n"))
		want := input == "https://playit.gg/claim/0123abcdef" || input == "https://playit.gg/claim/A1B2C3D4E5"
		if (got != "") != want {
			t.Fatalf("parser %q -> %q", input, got)
		}
		if len(w.token) != 0 {
			t.Fatal("retained process output")
		}
	}
}

func TestPlayitSetupSubprocessHelper(t *testing.T) {
	if os.Getenv("JUSTVOXEL_PLAYIT_TEST_HELPER") != "1" {
		return
	}
	fmt.Println("https://playit.gg/claim/0123abcdef")
	time.Sleep(time.Hour)
	os.Exit(0)
}

func TestPlayitSetupTimeoutKillsFixedCommandAndAllowsRetry(t *testing.T) {
	setupFixture(t)
	playitSetupTimeout = 100 * time.Millisecond
	var command *exec.Cmd
	playitSetupCommand = func(ctx context.Context, executable string, args ...string) *exec.Cmd {
		if executable != "/usr/bin/playit" || strings.Join(args, " ") != "setup" {
			t.Errorf("unexpected command %s %v", executable, args)
		}
		command = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPlayitSetupSubprocessHelper$")
		command.Env = append(os.Environ(), "JUSTVOXEL_PLAYIT_TEST_HELPER=1")
		return command
	}
	s := adminServerForTest()
	for i := 0; i < 2; i++ {
		if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusAccepted {
			t.Fatal(rr.Body.String())
		}
		waitSetup(t, "failed")
		if command.ProcessState == nil || command.ProcessState.Success() {
			t.Fatal("timeout did not terminate subprocess")
		}
		rr := setupRequest(s, http.MethodGet)
		if strings.Contains(rr.Body.String(), "claim") {
			t.Fatal("failed attempt retained claim URL")
		}
	}
}

func TestPlayitSetupRequiresDaemonAndExecutable(t *testing.T) {
	setupFixture(t)
	s := adminServerForTest()
	for _, state := range []string{"activating", "deactivating", "reloading"} {
		remoteSystemctl = func(context.Context, ...string) (string, error) {
			return "LoadState=loaded\nUnitFileState=enabled\nActiveState=" + state, nil
		}
		if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusConflict {
			t.Fatal("transitional service accepted")
		}
	}
	remoteSystemctl = func(context.Context, ...string) (string, error) {
		return "LoadState=not-found\nUnitFileState=disabled\nActiveState=inactive", nil
	}
	if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusServiceUnavailable {
		t.Fatal("missing unit accepted")
	}
	remoteSystemctl = func(context.Context, ...string) (string, error) {
		return "LoadState=loaded\nUnitFileState=enabled\nActiveState=active", nil
	}
	playitExecutableStat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusServiceUnavailable {
		t.Fatal("missing executable accepted")
	}
}

func TestPlayitSetupActivatesInactiveUsingProviderLifecycle(t *testing.T) {
	setupFixture(t)
	var calls []string
	remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "show" {
			return "LoadState=loaded\nUnitFileState=disabled\nActiveState=inactive", nil
		}
		return "", nil
	}
	playitSetupRun = func(context.Context, io.Writer) error { return os.ErrInvalid }
	if rr := setupRequest(adminServerForTest(), http.MethodPost); rr.Code != http.StatusAccepted {
		t.Fatal(rr.Body.String())
	}
	waitSetup(t, "failed")
	if strings.Join(calls, "|") != "show --property=LoadState,UnitFileState,ActiveState playit.service|enable --now playit.service" {
		t.Fatalf("unexpected service calls %v", calls)
	}
	remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "show" {
			return "LoadState=loaded\nUnitFileState=disabled\nActiveState=inactive", nil
		}
		return "", os.ErrPermission
	}
	if rr := setupRequest(adminServerForTest(), http.MethodPost); rr.Code != http.StatusServiceUnavailable {
		t.Fatal("activation failure accepted")
	}
}

func TestPlayitDeactivateCancelsAndAllowsFreshSetup(t *testing.T) {
	for _, result := range []error{nil, os.ErrInvalid} {
		t.Run(fmt.Sprint(result), func(t *testing.T) {
			setupFixture(t)
			store, _ := openTestWebUIStore(t)
			s := adminServerForTest()
			s.store = store
			var mutations []string
			remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
				if args[0] == "show" {
					return "LoadState=loaded\nUnitFileState=enabled\nActiveState=active", nil
				}
				mutations = append(mutations, strings.Join(args, " "))
				return "", nil
			}
			cancelled, lateClaim, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			playitSetupRun = func(ctx context.Context, output io.Writer) error {
				fmt.Fprintln(output, "https://playit.gg/claim/0123abcdef")
				<-ctx.Done()
				close(cancelled)
				// A buffered output callback and completion must both be ignored.
				fmt.Fprintln(output, "https://playit.gg/claim/abcdef0123")
				close(lateClaim)
				<-release
				return result
			}
			if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusAccepted {
				t.Fatal(rr.Body.String())
			}
			waitSetup(t, "waiting")
			deactivated := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				deactivated <- networkAdminRequest(s, "/v1/admin/network/remote-access/playit", `{"action":"deactivate"}`)
			}()
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("setup command was not cancelled")
			}
			select {
			case <-lateClaim:
			case <-time.After(3 * time.Second):
				t.Fatal("late claim callback blocked")
			}
			playitSetup.mu.Lock()
			state := playitSetup.state
			playitSetup.mu.Unlock()
			close(release)
			rr := <-deactivated
			if rr.Code != http.StatusOK || state.State != "idle" || state.ClaimURL != "" {
				t.Fatalf("deactivate: %d state=%+v", rr.Code, state)
			}
			if !reflect.DeepEqual(mutations, []string{"stop playit.service", "disable playit.service"}) {
				t.Fatalf("unexpected lifecycle %v", mutations)
			}
			rr = setupRequest(s, http.MethodGet)
			if strings.Contains(rr.Body.String(), "claim") || !strings.Contains(rr.Body.String(), `"state":"idle"`) {
				t.Fatalf("cancelled completion overwrote state: %s", rr.Body.String())
			}
			events, err := store.listAuditEvents(20)
			if err != nil || len(events) != 2 {
				t.Fatalf("cancellation audit: %v %v", events, err)
			}
			for _, event := range events {
				if event.Action == "network_playit_setup_failed" || event.Action == "network_playit_setup_complete" {
					t.Fatal("cancellation audited as setup result")
				}
			}
			freshRelease := make(chan struct{})
			playitSetupRun = func(ctx context.Context, output io.Writer) error {
				fmt.Fprintln(output, "https://playit.gg/claim/9876543210")
				<-freshRelease
				return os.ErrInvalid
			}
			if rr := setupRequest(s, http.MethodPost); rr.Code != http.StatusAccepted {
				t.Fatalf("fresh setup rejected: %s", rr.Body.String())
			}
			waitSetup(t, "waiting")
			rr = setupRequest(s, http.MethodGet)
			if !strings.Contains(rr.Body.String(), "9876543210") {
				t.Fatal("fresh claim state unavailable")
			}
			close(freshRelease)
			waitSetup(t, "failed")
		})
	}
}

func TestReadOnlyPlayitPendingStatusNeverIncludesClaim(t *testing.T) {
	setupFixture(t)
	playitSetup.mu.Lock()
	playitSetup.state = playitSetupState{State: "waiting", ClaimURL: "https://playit.gg/claim/0123abcdef"}
	playitSetup.mu.Unlock()
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, roleServerForTest(roleViewer))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodGet, "/v1/network/remote-access", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"summary":"Setup pending"`) || strings.Contains(rr.Body.String(), "claim") || strings.Contains(rr.Body.String(), "0123abcdef") {
		t.Fatalf("read-only pending status: %d %s", rr.Code, rr.Body.String())
	}
}
