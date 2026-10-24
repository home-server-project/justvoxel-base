package server

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type terminalProxyAPI interface {
	TerminalProxy(string) *httputil.ReverseProxy
}

type terminalAPI interface {
	AdminTerminal(context.Context, string) (api.TerminalStatus, error)
	AdminTerminalAction(context.Context, string, string) error
}

func (a *App) registerTerminalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system/workspace/terminal", a.terminalStatus)
	mux.HandleFunc("POST /api/system/workspace/terminal/start", a.terminalStart)
	mux.HandleFunc("POST /api/system/workspace/terminal/stop", a.terminalStop)
}

func (a *App) terminalStatus(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.fileBrowserIdentity(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(terminalAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Terminal unavailable. Retry.")
		return
	}
	status, err := client.AdminTerminal(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Terminal unavailable. Retry.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, status)
}
func (a *App) terminalStart(w http.ResponseWriter, r *http.Request) { a.terminalAction(w, r, "start") }
func (a *App) terminalStop(w http.ResponseWriter, r *http.Request)  { a.terminalAction(w, r, "stop") }
func (a *App) terminalAction(w http.ResponseWriter, r *http.Request, action string) {
	session, _, ok := a.fileBrowserIdentity(w, r)
	if !ok {
		return
	}
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	client, ok := a.api.(terminalAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Terminal unavailable. Retry.")
		return
	}
	if err := client.AdminTerminalAction(r.Context(), session, action); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Terminal operation could not be completed. Retry.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true})
}

func terminalFrameHeaders(h http.Header) {
	h.Set("X-Frame-Options", "SAMEORIGIN")
	// ttyd ships a single HTML document with inline JS/CSS; xterm creates inline
	// styles. This policy is confined to the authenticated terminal application.
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func (a *App) terminalProxy(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.fileBrowserIdentity(w, r)
	if !ok {
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || u.Scheme != a.config.ExternalScheme {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	client, ok := a.api.(terminalProxyAPI)
	if !ok {
		http.Error(w, "Terminal unavailable. Use Refresh to retry.", http.StatusBadGateway)
		return
	}
	proxy := client.TerminalProxy(session)
	proxy.ModifyResponse = func(resp *http.Response) error {
		terminalFrameHeaders(resp.Header)
		resp.Header.Del("Set-Cookie")
		for _, name := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control"} {
			w.Header().Del(name)
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Terminal unavailable. Use Refresh to retry.", http.StatusBadGateway)
	}
	terminalFrameHeaders(w.Header())
	// ReverseProxy preserves HTTP Upgrade and tunnels the WebSocket connection.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		// Hijacked connections can retain server deadlines. Clearing is best effort
		// because synthetic ResponseWriters may not support deadline control.
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Time{})
		_ = controller.SetWriteDeadline(time.Time{})
	}
	proxy.ServeHTTP(w, r)
}
