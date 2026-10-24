package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type terminalTestAPI struct {
	filesTestAPI
	actions []string
}

func (f *terminalTestAPI) AdminTerminal(context.Context, string) (api.TerminalStatus, error) {
	return api.TerminalStatus{State: "active", Ready: true}, nil
}
func (f *terminalTestAPI) AdminTerminalAction(_ context.Context, _ string, action string) error {
	f.actions = append(f.actions, action)
	return nil
}

// Exercise the real API proxy rewrite while substituting only its transport.
func (f *terminalTestAPI) TerminalProxy(session string) *httputil.ReverseProxy {
	proxy := api.NewClient("/unused-management.sock").TerminalProxy(session)
	proxy.Transport = http.DefaultTransport
	return proxy
}

func TestTerminalWebUIPrimaryAdministratorAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		username, role string
		mustChange     bool
		want           int
	}{
		{"voxel", "administrator", false, 200}, {"secondary", "administrator", false, 403},
		{"voxel", "operator", false, 403}, {"voxel", "viewer", false, 403}, {"voxel", "administrator", true, 403},
	} {
		client := &terminalTestAPI{filesTestAPI: filesTestAPI{username: tc.username, role: tc.role, mustChange: tc.mustChange}}
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"", "/start", "/stop"} {
			method := "POST"
			if action == "" {
				method = "GET"
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedSystemActionRequest(method, "/api/system/workspace/terminal"+action, "csrf=token&unit=untrusted&command=untrusted"))
			if rr.Code != tc.want {
				t.Fatalf("%s %s: %d %s", tc.username, action, rr.Code, rr.Body.String())
			}
		}
		if tc.want == 200 && !reflect.DeepEqual(client.actions, []string{"start", "stop"}) {
			t.Fatalf("actions %v", client.actions)
		}
		if tc.want == 403 && len(client.actions) != 0 {
			t.Fatal("unauthorized lifecycle call")
		}
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedSystemActionRequest("POST", "/api/system/workspace/terminal/start", "csrf=wrong"))
		if rr.Code != 403 {
			t.Fatal("missing CSRF enforcement")
		}
	}
}

func TestTerminalProxyIdentityHostCookiesAndFraming(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	calls := 0
	http.DefaultTransport = filesRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer session-token" || r.Header.Get("Origin") != "http://justvoxel.test" {
			t.Fatal("missing Agent session or public Origin")
		}
		if r.URL.Host != "unix" || r.URL.Path != "/v1/admin/terminal/proxy/" || r.Host != "justvoxel.test" {
			t.Fatalf("bad target/Host: %s %s", r.URL, r.Host)
		}
		if r.Header.Get("X-JustVoxel-Terminal") != "" {
			t.Fatal("spoofed auth header")
		}
		for _, name := range []string{"Cookie", "Proxy-Authorization", "X-JustVoxel-User", "X-Forwarded-User", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "Forwarded"} {
			if r.Header.Get(name) != "" {
				t.Fatalf("forwarded %s", name)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("terminal"))}, nil
	})
	for _, tc := range []struct {
		username, role, origin, site string
		mustChange, session          bool
		want                         int
	}{
		{"voxel", "administrator", "http://justvoxel.test", "same-origin", false, true, 200},
		{"secondary", "administrator", "", "", false, true, 403},
		{"voxel", "operator", "", "", false, true, 403}, {"voxel", "viewer", "", "", false, true, 403},
		{"voxel", "administrator", "", "", true, true, 403}, {"voxel", "administrator", "", "", false, false, 401},
		{"voxel", "administrator", "http://evil.test", "", false, true, 403},
		{"voxel", "administrator", "", "cross-site", false, true, 403},
	} {
		app, err := New(&terminalTestAPI{filesTestAPI: filesTestAPI{username: tc.username, role: tc.role, mustChange: tc.mustChange}}, Config{ExternalScheme: "http"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("GET", "http://justvoxel.test/terminal/", nil)
		if tc.session {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		}
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "secret"})
		req.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "secret"})
		for _, name := range []string{"X-JustVoxel-Terminal", "X-JustVoxel-User", "X-Forwarded-User", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "Forwarded", "Authorization"} {
			req.Header.Set(name, "spoof")
		}
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Sec-Fetch-Site", tc.site)
		before := calls
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Fatalf("%+v: %d", tc, rr.Code)
		}
		if tc.want == 200 {
			if calls != before+1 || rr.Header().Get("X-Frame-Options") != "SAMEORIGIN" || len(rr.Header().Values("Content-Security-Policy")) != 1 || !strings.Contains(rr.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
				t.Fatal("bad proxy/frame headers")
			}
		} else if calls != before || rr.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatal("unauthorized proxy/framing exception")
		}
		for _, path := range []string{"/healthz", "/api/system/workspace/terminal"} {
			rr = httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
			if rr.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rr.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("global framing weakened")
			}
		}
	}
}

func TestTerminalProxyWebSocketUpgrade(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	backend, peer := net.Pipe()
	defer peer.Close()
	http.DefaultTransport = filesRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "unix" || r.URL.Path != "/v1/admin/terminal/proxy/ws" || r.Header.Get("Authorization") != "Bearer session-token" || r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("Upgrade") != "websocket" || r.Header.Get("X-JustVoxel-Terminal") != "" {
			t.Errorf("bad upgrade request: %v", r)
		}
		return &http.Response{StatusCode: 101, Header: http.Header{"Connection": []string{"Upgrade"}, "Upgrade": []string{"websocket"}}, Body: backend}, nil
	})
	app, err := New(&terminalTestAPI{filesTestAPI: filesTestAPI{role: "administrator"}}, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	u, _ := url.Parse(server.URL)
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = io.WriteString(conn, "GET /terminal/ws HTTP/1.1\r\nHost: "+u.Host+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nOrigin: "+server.URL+"\r\nCookie: "+sessionCookie+"=session-token\r\n\r\n")
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

func TestTerminalWebUIRequiresSession(t *testing.T) {
	app, err := New(&terminalTestAPI{filesTestAPI: filesTestAPI{role: "administrator"}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"", "/start", "/stop"} {
		method := "POST"
		if action == "" {
			method = "GET"
		}
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, httptest.NewRequest(method, "/api/system/workspace/terminal"+action, nil))
		if rr.Code != 401 {
			t.Fatalf("%s: %d", action, rr.Code)
		}
	}
}

func TestTerminalProxyPreservesOuterRouting(t *testing.T) {
	app, err := New(&terminalTestAPI{filesTestAPI: filesTestAPI{role: "administrator"}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	// Terminal stays outside ServeMux so its methodless subtree cannot change
	// the established GET dashboard catch-all and retired POST route behavior.
	for _, path := range []string{"/settings/users/create", "/settings/authentication", "/settings/restore/apply"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest("POST", "http://example"+path, "csrf=csrf-token"))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("retired %s: %d", path, rr.Code)
		}
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
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
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
			app, err := New(&terminalTestAPI{filesTestAPI: filesTestAPI{username: tc.username, role: "administrator"}}, Config{})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "http://justvoxel.test/terminal/ws", nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
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
			http.DefaultTransport = filesRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				checkDeadlines() // Clearing must happen before proxying.
				if r.Header.Get("Upgrade") != tc.upgrade || (tc.upgrade != "" && !strings.EqualFold(r.Header.Get("Connection"), "Upgrade")) {
					t.Fatal("proxy changed Upgrade headers")
				}
				if r.URL.Host != "unix" || r.URL.Path != "/v1/admin/terminal/proxy/ws" || r.Header.Get("Authorization") != "Bearer session-token" || r.Header.Get("X-JustVoxel-Terminal") != "" {
					t.Fatal("Management backend or authentication headers changed")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("terminal"))}, nil
			})
			app.Handler().ServeHTTP(w, req)
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
