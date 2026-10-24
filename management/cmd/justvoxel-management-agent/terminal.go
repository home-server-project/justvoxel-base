package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

const terminalUnit = "justvoxel-terminal.service"
const terminalSocket = "/run/justvoxel-terminal/ttyd.sock"
const terminalURL = "http://terminal/terminal/"
const terminalProxyPrefix = "/v1/admin/terminal/proxy/"

var terminalCommand = instanceRuntimeCommand

// One bounded probe per status request; the browser owns startup waiting.
var terminalHealth = probeTerminal

// Both transports dial the same fixed socket; request URLs cannot select a backend.
func newTerminalTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", terminalSocket)
		},
	}
}

var terminalProbeTransport http.RoundTripper = func() *http.Transport {
	transport := newTerminalTransport()
	transport.DisableKeepAlives = true
	return transport
}()
var terminalProxyTransport http.RoundTripper = newTerminalTransport()

func probeTerminal(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, terminalURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-JustVoxel-Terminal", "1")
	resp, err := (&http.Client{Timeout: 2 * time.Second, Transport: terminalProbeTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type terminalStatus struct {
	State string `json:"state"`
	Ready bool   `json:"ready"`
}

func registerTerminalRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc(terminalProxyPrefix, s.terminalProxy)
	mux.HandleFunc("GET /v1/admin/terminal", s.terminalGet)
	mux.HandleFunc("POST /v1/admin/terminal/start", s.terminalStart)
	mux.HandleFunc("POST /v1/admin/terminal/stop", s.terminalStop)
}

func (s *server) terminalGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePrimaryAdministrator(w, r); !ok {
		return
	}
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	out, err := terminalCommand(ctx, "systemctl", "show", "--property=ActiveState", "--value", terminalUnit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Terminal status unavailable. Retry.")
		return
	}
	status := terminalStatus{State: strings.TrimSpace(string(out))}
	if status.State == "active" {
		status.Ready = terminalHealth(ctx)
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) terminalStart(w http.ResponseWriter, r *http.Request) { s.terminalChange(w, r, true) }
func (s *server) terminalStop(w http.ResponseWriter, r *http.Request)  { s.terminalChange(w, r, false) }
func (s *server) terminalChange(w http.ResponseWriter, r *http.Request, start bool) {
	if _, ok := s.requirePrimaryAdministrator(w, r); !ok {
		return
	}
	// No client-selected command, unit, user, or environment is accepted.
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	args := []string{"stop", terminalUnit}
	if start {
		args = []string{"--no-block", "start", terminalUnit}
	}
	if _, err := terminalCommand(ctx, "systemctl", args...); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Terminal operation could not be completed. Retry.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// The protected Management socket is the only frontend for the private ttyd socket.
func (s *server) terminalProxy(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePrimaryAdministrator(w, r); !ok {
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: terminalProxyTransport,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme = "http"
			p.Out.URL.Host = "terminal"
			p.Out.URL.Path = "/terminal/" + strings.TrimPrefix(p.In.URL.Path, terminalProxyPrefix)
			p.Out.URL.RawPath = ""
			p.Out.URL.User = nil
			p.Out.Host = p.In.Host
			if origin := p.In.Header.Values("Origin"); len(origin) > 0 {
				p.Out.Header["Origin"] = append([]string(nil), origin...)
			}
			// Origin and WebSocket Upgrade remain intact for ttyd's origin validation.
			for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-JustVoxel-Terminal", "X-JustVoxel-User", "X-Real-IP", "Forwarded"} {
				p.Out.Header.Del(name)
			}
			for name := range p.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
					p.Out.Header.Del(name)
				}
			}
			p.Out.Header.Set("X-JustVoxel-Terminal", "1")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Terminal unavailable. Use Refresh to retry.", http.StatusBadGateway)
		},
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		// Hijacked connections can retain server deadlines. Clearing is best effort
		// because synthetic ResponseWriters may not support deadline control.
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Time{})
		_ = controller.SetWriteDeadline(time.Time{})
	}
	proxy.ServeHTTP(w, r)
}
