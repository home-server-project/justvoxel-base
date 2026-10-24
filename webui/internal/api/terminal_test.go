package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTerminalClientFixedContract(t *testing.T) {
	var calls []string
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatal("session not forwarded")
		}
		if r.Method == "POST" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 0 {
				t.Fatalf("nonempty command body: %v %v", body, err)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"state":"active","ready":true}`))}, nil
	})}}
	status, err := client.AdminTerminal(context.Background(), "session-token")
	if err != nil || !status.Ready || status.State != "active" {
		t.Fatalf("status: %+v %v", status, err)
	}
	for _, action := range []string{"start", "stop"} {
		if err := client.AdminTerminalAction(context.Background(), "session-token", action); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.AdminTerminalAction(context.Background(), "session-token", "arbitrary"); err == nil {
		t.Fatal("accepted arbitrary action")
	}
	if strings.Join(calls, ";") != "GET /v1/admin/terminal;POST /v1/admin/terminal/start;POST /v1/admin/terminal/stop" {
		t.Fatalf("calls: %v", calls)
	}
}

func TestTerminalProxyUsesExistingAgentTransport(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "http" || r.URL.Host != "unix" || r.URL.Path != "/v1/admin/terminal/proxy/ws" || r.Host != "public.test" || r.Header.Get("Origin") != "https://public.test" {
			t.Fatalf("bad Agent proxy request: %s %s %v", r.URL, r.Host, r.Header)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatal("browser Authorization not replaced with current session")
		}
		for _, name := range []string{"Proxy-Authorization", "Cookie", "X-JustVoxel-Terminal", "X-JustVoxel-User", "X-Real-IP", "Forwarded", "X-Forwarded-For", "X-Forwarded-Custom"} {
			if r.Header.Get(name) != "" {
				t.Fatalf("forwarded browser header %s", name)
			}
		}
		if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Connection") != "Upgrade" {
			t.Fatal("lost WebSocket Upgrade")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("terminal"))}, nil
	})
	client := &Client{http: &http.Client{Transport: transport}}
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"} {
		proxy := client.TerminalProxy("session-token")
		req := httptest.NewRequest(method, "https://public.test/terminal/ws?backend=http://evil.test", nil)
		req.Header.Set("Origin", "https://public.test")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-JustVoxel-Terminal", "X-JustVoxel-User", "X-Real-IP", "Forwarded", "X-Forwarded-For", "X-Forwarded-Custom"} {
			req.Header.Set(name, "browser-spoof")
		}
		rr := httptest.NewRecorder()
		proxy.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("%s: %d", method, rr.Code)
		}
	}
	if calls != 7 {
		t.Fatalf("proxy calls: %d", calls)
	}
}
