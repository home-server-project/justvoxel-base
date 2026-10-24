package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type filesTestAPI struct {
	fakeAPI
	role       string
	username   string
	mustChange bool
	actions    []string
}

func (f *filesTestAPI) Session(_ context.Context, token string) (api.SessionInfo, error) {
	if token != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	username := f.username
	if username == "" {
		username = "voxel"
	}
	return api.SessionInfo{Username: username, Role: f.role, MustChange: f.mustChange}, nil
}
func (f *filesTestAPI) AdminFileBrowser(context.Context, string) (api.FileBrowserStatus, error) {
	return api.FileBrowserStatus{Availability: "on-demand", Ready: true, State: "active"}, nil
}
func (f *filesTestAPI) AdminFileBrowserAction(_ context.Context, _ string, action string, _ string) error {
	f.actions = append(f.actions, action)
	return nil
}

type filesRoundTripper func(*http.Request) (*http.Response, error)

func (f filesRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFilesAndDashboardRoutesCoexist(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	var proxied []string
	http.DefaultTransport = filesRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "127.0.0.1:61209" || r.URL.Path != "/files/api/resources" {
			t.Fatalf("unexpected proxy target: %s", r.URL)
		}
		proxied = append(proxied, r.Method)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("filebrowser"))}, nil
	})
	app, err := New(&filesTestAPI{role: "administrator"}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "data-quick-look") || len(proxied) != 0 {
		t.Fatalf("dashboard route returned %d, proxy calls = %v", rr.Code, proxied)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/settings/server/plan", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("retired POST route returned %d, want 405", rr.Code)
	}
	// These are regression samples; the proxy dispatch accepts every method.
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "CUSTOM"} {
		req := httptest.NewRequest(method, "/files/api/resources", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		rr := httptest.NewRecorder()
		before := len(proxied)
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || rr.Body.String() != "filebrowser" || len(proxied) != before+1 || proxied[before] != method {
			t.Fatalf("%s proxy route returned %d, calls = %v", method, rr.Code, proxied)
		}
	}
}

func TestFilesProxyIdentityAndFraming(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	calls := 0
	http.DefaultTransport = filesRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "127.0.0.1:61209" || r.URL.Path != "/files/api/resources" {
			t.Fatalf("bad target %s", r.URL)
		}
		if r.Header.Get("X-JustVoxel-User") != "voxel" {
			t.Fatal("spoofed identity forwarded")
		}
		for _, name := range []string{"X-Forwarded-User", "X-Forwarded-For", "X-Real-IP", "Forwarded"} {
			if r.Header.Get(name) != "" {
				t.Fatalf("forwarded %s", name)
			}
		}
		if r.Header.Get("X-Forwarded-Host") != "justvoxel.test" || r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Fatal("spoofed forwarding metadata")
		}
		if strings.Contains(r.Header.Get("Cookie"), "jv_") {
			t.Fatal("JustVoxel cookies leaked")
		}
		h := http.Header{}
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-upstream'; frame-ancestors 'none'")
		h.Add("Set-Cookie", "jv_session=spoof; Path=/")
		h.Add("Set-Cookie", "filebrowser_token=legitimate; Path=/")
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	app, err := New(&filesTestAPI{role: "administrator"}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://justvoxel.test/files/api/resources", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	for _, name := range []string{"X-JustVoxel-User", "X-Forwarded-User", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "Forwarded"} {
		req.Header.Set(name, "spoof")
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 || calls != 1 {
		t.Fatalf("proxy returned %d calls=%d", rr.Code, calls)
	}
	if rr.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatal("Files framing denied")
	}
	policy := rr.Header().Values("Content-Security-Policy")
	if len(policy) != 1 || !strings.Contains(policy[0], "frame-ancestors 'self'") || !strings.Contains(policy[0], "'nonce-upstream'") || strings.Contains(policy[0], "frame-ancestors 'none'") {
		t.Fatalf("bad framing policy %v", policy)
	}
	if cookies := rr.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "filebrowser_token" || cookies[0].Path != "/files/" {
		t.Fatalf("unsafe cookies %v", cookies)
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rr.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("other routes lost framing protection")
	}
}

func TestFilesAdministratorAndSessionBoundary(t *testing.T) {
	for _, tc := range []struct {
		role       string
		token      string
		mustChange bool
		want       int
	}{
		{"viewer", "session-token", false, 403}, {"operator", "session-token", false, 403}, {"administrator", "", false, 401}, {"administrator", "session-token", true, 403},
	} {
		app, err := New(&filesTestAPI{role: tc.role, mustChange: tc.mustChange}, Config{})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/files/", "/files/api/login", "/api/files", "/api/files/start", "/api/files/stop", "/api/files/refresh", "/api/files/availability"} {
			method := "GET"
			if strings.HasPrefix(path, "/api/files/") {
				method = "POST"
			}
			req := httptest.NewRequest(method, path, nil)
			if tc.token != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.token})
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("%s %s = %d, want %d", tc.role, path, rr.Code, tc.want)
			}
		}
	}
}

func TestFilesMutationRequiresCSRF(t *testing.T) {
	app, err := New(&filesTestAPI{role: "administrator"}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/files/start", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("CSRF missing: %d", rr.Code)
	}
}

func TestFilesSecondaryAdministratorDenied(t *testing.T) {
	app, err := New(&filesTestAPI{role: "administrator", username: "second-admin"}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/files/", "/files/api/resources", "/api/files", "/api/files/start", "/api/files/stop", "/api/files/refresh", "/api/files/availability"} {
		method := "GET"
		if strings.HasPrefix(path, "/api/files/") {
			method = "POST"
		}
		req := authenticatedSystemActionRequest(method, path, "csrf=token")
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s = %d", path, rr.Code)
		}
	}
}

func TestFilesRefreshActionBoundary(t *testing.T) {
	client := &filesTestAPI{role: "administrator"}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		action, body string
		want         int
	}{
		{"refresh", "csrf=token", 200},
		{"refresh", "", 403},
		{"restart", "csrf=token", 404},
		{"other.service", "csrf=token", 404},
	} {
		before := len(client.actions)
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedSystemActionRequest("POST", "/api/files/"+tc.action, tc.body))
		if rr.Code != tc.want {
			t.Fatalf("%s = %d, want %d", tc.action, rr.Code, tc.want)
		}
		if tc.want == 200 {
			if len(client.actions) != before+1 || client.actions[before] != "refresh" {
				t.Fatal(client.actions)
			}
		} else if len(client.actions) != before {
			t.Fatal("rejected action forwarded")
		}
	}
}
