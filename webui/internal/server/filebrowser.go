package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const fileBrowserBackend = "http://127.0.0.1:61209"

type fileBrowserAPI interface {
	AdminFileBrowser(context.Context, string) (api.FileBrowserStatus, error)
	AdminFileBrowserAction(context.Context, string, string, string) error
}

func (a *App) registerFileBrowserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files", a.fileBrowserStatus)
	mux.HandleFunc("POST /api/files/{action}", a.fileBrowserAction)
}

func (a *App) fileBrowserIdentity(w http.ResponseWriter, r *http.Request) (string, api.SessionInfo, bool) {
	session, identity, ok := a.systemMonitorIdentity(w, r)
	if !ok {
		return "", api.SessionInfo{}, false
	}
	if identity.MustChange || identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", api.SessionInfo{}, false
	}
	return session, identity, true
}

func (a *App) fileBrowserStatus(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.fileBrowserIdentity(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(fileBrowserAPI)
	if !ok {
		http.Error(w, "File Browser unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := client.AdminFileBrowser(r.Context(), session)
	if !a.handleSystemMonitorAPIError(w, r, err) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (a *App) fileBrowserAction(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.fileBrowserIdentity(w, r)
	if !ok {
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	client, ok := a.api.(fileBrowserAPI)
	if !ok {
		http.Error(w, "File Browser unavailable", http.StatusServiceUnavailable)
		return
	}
	action := r.PathValue("action")
	switch action {
	case "start", "stop", "availability":
	default:
		http.NotFound(w, r)
		return
	}
	if err := client.AdminFileBrowserAction(r.Context(), session, action, r.FormValue("availability")); !a.handleSystemMonitorAPIError(w, r, err) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func fileBrowserFrameHeaders(h http.Header) {
	h.Set("X-Frame-Options", "SAMEORIGIN")
	// Preserve upstream nonce/hash and download policies, changing only its
	// framing directive. When absent, retain JustVoxel's other CSP directives.
	policy := h.Get("Content-Security-Policy")
	if policy == "" {
		policy = "default-src 'self'; style-src 'self' 'sha256-ypuHOrHdFK0ra+bQlE+zbe2vHCx4oWqJLuhMbpi6gms='; script-src 'self'; img-src 'self' http: https: blob:; form-action 'self'"
	}
	directives := []string{}
	for _, directive := range strings.Split(policy, ";") {
		directive = strings.TrimSpace(directive)
		fields := strings.Fields(directive)
		if len(fields) > 0 && !strings.EqualFold(fields[0], "frame-ancestors") {
			directives = append(directives, directive)
		}
	}
	directives = append(directives, "frame-ancestors 'self'")
	h.Set("Content-Security-Policy", strings.Join(directives, "; "))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func (a *App) fileBrowserProxy(w http.ResponseWriter, r *http.Request) {
	_, identity, ok := a.fileBrowserIdentity(w, r)
	if !ok {
		return
	}
	// Embedded API writes keep the upstream application's CSRF contract, while
	// cross-origin browser requests are rejected at the authenticated proxy.
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
	target, _ := url.Parse(fileBrowserBackend)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			// Never trust a browser identity or forwarding chain.
			for _, name := range []string{"X-JustVoxel-User", "X-Forwarded-User", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "Forwarded", "Cookie"} {
				p.Out.Header.Del(name)
			}
			for name := range p.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
					p.Out.Header.Del(name)
				}
			}
			for _, cookie := range r.Cookies() {
				if cookie.Name != sessionCookie && cookie.Name != csrfCookie && cookie.Name != mustChangeCookie {
					p.Out.AddCookie(cookie)
				}
			}
			p.Out.Header.Set("X-JustVoxel-User", identity.Username)
			p.Out.Header.Set("X-Forwarded-Host", r.Host)
			p.Out.Header.Set("X-Forwarded-Proto", a.config.ExternalScheme)
		},
		ModifyResponse: func(resp *http.Response) error {
			fileBrowserFrameHeaders(resp.Header)
			// Scope upstream cookies to Files and protect JustVoxel's session cookies.
			cookies := resp.Cookies()
			resp.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if cookie.Name == sessionCookie || cookie.Name == csrfCookie || cookie.Name == mustChangeCookie {
					continue
				}
				cookie.Path = "/files/"
				cookie.Domain = ""
				resp.Header.Add("Set-Cookie", cookie.String())
			}
			// ReverseProxy adds response headers. Remove the outer copies so they
			// cannot impose a second, conflicting CSP on the embedded application.
			for _, name := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control"} {
				w.Header().Del(name)
			}
			if location := resp.Header.Get("Location"); location != "" {
				u, err := url.Parse(location)
				if err != nil || u.IsAbs() || u.Host != "" || (strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(u.Path, "/files/")) {
					resp.Header.Del("Location")
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "File Browser unavailable. Close Files and retry.", http.StatusBadGateway)
		},
	}
	fileBrowserFrameHeaders(w.Header())
	proxy.ServeHTTP(w, r)
}
