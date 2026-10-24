package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func terminalTestServer(t *testing.T, username string, role principalRole) *server {
	t.Helper()
	s, _ := systemMonitorProfileTestServer(t, role)
	sess := s.sessions["token"]
	sess.Username = username
	s.sessions["token"] = sess
	return s
}
func TestTerminalPrimaryAdministratorLifecycle(t *testing.T) {
	oldCommand, oldHealth := terminalCommand, terminalHealth
	t.Cleanup(func() { terminalCommand, terminalHealth = oldCommand, oldHealth })
	var calls []string
	terminalCommand = func(_ context.Context, command string, args ...string) ([]byte, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		return []byte("active\n"), nil
	}
	terminalHealth = func(context.Context) bool { return true }
	for _, tc := range []struct {
		username string
		role     principalRole
		want     int
	}{
		{systemAdminUsername, roleAdministrator, 200}, {"secondary", roleAdministrator, 403},
		{systemAdminUsername, roleOperator, 403}, {systemAdminUsername, roleViewer, 403},
	} {
		s := terminalTestServer(t, tc.username, tc.role)
		mux := http.NewServeMux()
		registerTerminalRoutes(mux, s)
		for _, action := range []string{"", "/start", "/stop", "/start", "/stop"} {
			method := "POST"
			if action == "" {
				method = "GET"
			}
			before := len(calls)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, systemMonitorProfileRequest(method, "/v1/admin/terminal"+action, struct{}{}))
			if rr.Code != tc.want {
				t.Fatalf("%s %s: %d %s", tc.username, action, rr.Code, rr.Body.String())
			}
			if tc.want == 403 && len(calls) != before {
				t.Fatal("unauthorized service control")
			}
			if tc.want == 200 && action == "" {
				var status terminalStatus
				if json.Unmarshal(rr.Body.Bytes(), &status) != nil || !status.Ready || status.State != "active" {
					t.Fatalf("bad status: %s", rr.Body.String())
				}
			}
		}
	}
	want := []string{"systemctl show --property=ActiveState --value " + terminalUnit, "systemctl --no-block start " + terminalUnit, "systemctl stop " + terminalUnit, "systemctl --no-block start " + terminalUnit, "systemctl stop " + terminalUnit}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands: %v", calls)
	}
}
func TestTerminalRejectsClientCommandArguments(t *testing.T) {
	s := terminalTestServer(t, systemAdminUsername, roleAdministrator)
	old := terminalCommand
	t.Cleanup(func() { terminalCommand = old })
	terminalCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unexpected command")
		return nil, nil
	}
	mux := http.NewServeMux()
	registerTerminalRoutes(mux, s)
	for _, action := range []string{"start", "stop"} {
		for _, key := range []string{"unit", "command", "user", "path", "port", "environment"} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, systemMonitorProfileRequest("POST", "/v1/admin/terminal/"+action, map[string]string{key: "untrusted"}))
			if rr.Code != 400 {
				t.Fatalf("%s %s: %d", action, key, rr.Code)
			}
		}
	}
}
func TestTerminalStatusRequiresReadiness(t *testing.T) {
	s := terminalTestServer(t, systemAdminUsername, roleAdministrator)
	oldCommand, oldHealth := terminalCommand, terminalHealth
	t.Cleanup(func() { terminalCommand, terminalHealth = oldCommand, oldHealth })
	for _, state := range []string{"inactive", "activating", "active", "failed"} {
		for _, healthy := range []bool{false, true} {
			terminalCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte(state), nil }
			probes := 0
			terminalHealth = func(context.Context) bool { probes++; return healthy }
			rr := httptest.NewRecorder()
			s.terminalGet(rr, systemMonitorProfileRequest("GET", "/v1/admin/terminal", nil))
			var got terminalStatus
			if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &got) != nil || got.State != state || got.Ready != (state == "active" && healthy) {
				t.Fatalf("status %s", rr.Body.String())
			}
			if state != "active" && probes != 0 {
				t.Fatal("probed inactive service")
			}
		}
	}
}

type terminalRoundTripper func(*http.Request) (*http.Response, error)

func (f terminalRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTerminalProbeFixedEndpointAndHeader(t *testing.T) {
	old := terminalProbeTransport
	t.Cleanup(func() { terminalProbeTransport = old })
	for _, code := range []int{200, 302, 403, 503} {
		terminalProbeTransport = terminalRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != terminalURL || r.Host != "terminal" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Method != "GET" || r.Header.Get("X-JustVoxel-Terminal") != "1" {
				t.Fatalf("bad probe: %v", r)
			}
			if _, ok := r.Context().Deadline(); !ok {
				t.Fatal("unbounded probe")
			}
			return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		if probeTerminal(context.Background()) != (code == 200) {
			t.Fatalf("probe accepted status %d", code)
		}
	}
}

func TestTerminalMissingSessionAndPasswordChangeRejected(t *testing.T) {
	for _, mustChange := range []bool{false, true} {
		s := terminalTestServer(t, systemAdminUsername, roleAdministrator)
		sess := s.sessions["token"]
		sess.MustChange = mustChange
		s.sessions["token"] = sess
		mux := http.NewServeMux()
		registerTerminalRoutes(mux, s)
		for _, action := range []string{"", "/start", "/stop", "/proxy/", "/proxy/ws"} {
			method := "POST"
			if action == "" {
				method = "GET"
			}
			req := systemMonitorProfileRequest(method, "/v1/admin/terminal"+action, struct{}{})
			want := 403
			if !mustChange {
				req.Header.Del("Authorization")
				want = 401
			}
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != want {
				t.Fatalf("%s password change=%t: %d", action, mustChange, rr.Code)
			}
		}
	}
}

func TestTerminalTransportsDialOnlyPrivateSocket(t *testing.T) {
	for _, transport := range []*http.Transport{newTerminalTransport(), terminalProbeTransport.(*http.Transport), terminalProxyTransport.(*http.Transport)} {
		if transport.Proxy != nil {
			t.Fatal("proxy environment could redirect terminal transport")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		conn, err := transport.DialContext(ctx, "tcp", "browser-selected.invalid:1234")
		if conn != nil {
			conn.Close()
			t.Fatal("unexpected connection")
		}
		var op *net.OpError
		if !errors.As(err, &op) || op.Net != "unix" || op.Addr == nil || op.Addr.String() != "/run/justvoxel-terminal/ttyd.sock" {
			t.Fatalf("dial did not target fixed private Unix socket: %v", err)
		}
	}
}

func TestTerminalProxyAuthorizationAndFixedBackend(t *testing.T) {
	old := terminalProxyTransport
	t.Cleanup(func() { terminalProxyTransport = old })
	calls := 0
	terminalProxyTransport = terminalRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "http" || r.URL.Host != "terminal" || r.URL.Path != "/terminal/ws" || r.Host != "public.test" || r.Header.Get("Origin") != "https://public.test" {
			t.Fatalf("bad ttyd target/public origin: %s %s %s", r.URL, r.Host, r.Header.Get("Origin"))
		}
		if r.Header.Get("X-JustVoxel-Terminal") != "1" || len(r.Header.Values("X-JustVoxel-Terminal")) != 1 {
			t.Fatal("Agent did not replace browser terminal header")
		}
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
				t.Fatalf("forwarded spoofed header %s", name)
			}
		}
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-JustVoxel-User", "X-Real-IP", "Forwarded"} {
			if r.Header.Get(name) != "" {
				t.Fatalf("leaked %s", name)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("terminal"))}, nil
	})
	for _, tc := range []struct {
		username string
		role     principalRole
		want     int
	}{
		{systemAdminUsername, roleAdministrator, 200}, {"secondary", roleAdministrator, 403},
		{systemAdminUsername, roleOperator, 403}, {systemAdminUsername, roleViewer, 403},
	} {
		s := terminalTestServer(t, tc.username, tc.role)
		mux := http.NewServeMux()
		registerTerminalRoutes(mux, s)
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"} {
			req := systemMonitorProfileRequest(method, terminalProxyPrefix+"ws?backend=http://evil.test&socket=/tmp/evil.sock", nil)
			req.Host = "public.test"
			req.Header.Set("Origin", "https://public.test")
			for _, name := range []string{"Proxy-Authorization", "Cookie", "X-JustVoxel-Terminal", "X-JustVoxel-User", "X-Real-IP", "Forwarded", "X-Forwarded-For", "X-Forwarded-Custom"} {
				req.Header.Set(name, "spoof")
			}
			before := calls
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("%s %s: %d %s", tc.username, method, rr.Code, rr.Body.String())
			}
			if (calls == before+1) != (tc.want == 200) {
				t.Fatal("unauthorized or missing proxy call")
			}
		}
	}
}

func TestTerminalAgentWebSocketUpgrade(t *testing.T) {
	old := terminalProxyTransport
	t.Cleanup(func() { terminalProxyTransport = old })
	backend, peer := net.Pipe()
	defer peer.Close()
	terminalProxyTransport = terminalRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/terminal/ws" || r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Authorization") != "" || r.Header.Get("X-JustVoxel-Terminal") != "1" || r.Header.Get("Origin") != "http://"+r.Host {
			t.Errorf("bad ttyd upgrade: %v", r)
		}
		return &http.Response{StatusCode: 101, Header: http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}}, Body: backend}, nil
	})
	mux := http.NewServeMux()
	registerTerminalRoutes(mux, terminalTestServer(t, systemAdminUsername, roleAdministrator))
	front := httptest.NewServer(mux)
	defer front.Close()
	u, _ := url.Parse(front.URL)
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = io.WriteString(conn, "GET "+terminalProxyPrefix+"ws HTTP/1.1\r\nHost: "+u.Host+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nOrigin: "+front.URL+"\r\nAuthorization: Bearer token\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 101 {
		t.Fatalf("upgrade: %d", resp.StatusCode)
	}
	go func() { _, _ = peer.Write([]byte("hello")) }()
	buf := make([]byte, 5)
	if _, err := io.ReadFull(reader, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("backend tunnel: %q %v", buf, err)
	}
	go func() { _, _ = conn.Write([]byte("input")) }()
	if _, err := io.ReadFull(peer, buf); err != nil || string(buf) != "input" {
		t.Fatalf("browser tunnel: %q %v", buf, err)
	}
}

type terminalDeadlineWriter struct {
	*httptest.ResponseRecorder
	readDeadlines  []time.Time
	writeDeadlines []time.Time
	deadlineError  error
}

func (w *terminalDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadlines = append(w.readDeadlines, deadline)
	return w.deadlineError
}

func (w *terminalDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadlines = append(w.writeDeadlines, deadline)
	return w.deadlineError
}

func TestTerminalProxyWebSocketDeadlines(t *testing.T) {
	old := terminalProxyTransport
	t.Cleanup(func() { terminalProxyTransport = old })
	for _, tc := range []struct {
		name, upgrade, username string
		deadlineError           error
		wantClears, wantStatus  int
	}{
		{"websocket", "websocket", "voxel", nil, 1, http.StatusOK},
		{"mixed case", "WebSocket", "voxel", nil, 1, http.StatusOK},
		{"unsupported deadlines", "websocket", "voxel", http.ErrNotSupported, 1, http.StatusOK},
		{"normal HTTP", "", "voxel", nil, 0, http.StatusOK},
		{"other upgrade", "other", "voxel", nil, 0, http.StatusOK},
		{"unauthorized websocket", "websocket", "secondary", http.ErrNotSupported, 0, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := terminalTestServer(t, tc.username, roleAdministrator)
			req := systemMonitorProfileRequest("GET", terminalProxyPrefix+"ws", nil)
			if tc.upgrade != "" {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", tc.upgrade)
			}
			w := &terminalDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), deadlineError: tc.deadlineError}
			checkDeadlines := func() {
				t.Helper()
				if len(w.readDeadlines) != tc.wantClears || len(w.writeDeadlines) != tc.wantClears {
					t.Fatalf("deadline calls: read=%v write=%v, want %d each", w.readDeadlines, w.writeDeadlines, tc.wantClears)
				}
				for _, deadlines := range [][]time.Time{w.readDeadlines, w.writeDeadlines} {
					for _, deadline := range deadlines {
						if !deadline.IsZero() {
							t.Fatalf("deadline was not cleared: %v", deadline)
						}
					}
				}
			}
			calls := 0
			terminalProxyTransport = terminalRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				checkDeadlines() // Clearing must happen before proxying.
				if r.Header.Get("Upgrade") != tc.upgrade || (tc.upgrade != "" && !strings.EqualFold(r.Header.Get("Connection"), "Upgrade")) {
					t.Fatal("proxy changed Upgrade headers")
				}
				if r.URL.Host != "terminal" || r.URL.Path != "/terminal/ws" || r.Header.Get("Authorization") != "" || r.Header.Get("X-JustVoxel-Terminal") != "1" {
					t.Fatal("Agent backend or authentication headers changed")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("terminal"))}, nil
			})
			s.terminalProxy(w, req)
			checkDeadlines()
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if (calls == 1) != (tc.wantStatus == http.StatusOK) {
				t.Fatalf("proxy calls = %d", calls)
			}
		})
	}
}
