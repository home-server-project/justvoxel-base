package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const (
	sessionCookie    = "jv_session"
	csrfCookie       = "jv_csrf"
	mustChangeCookie = "jv_must_change"
)

type API interface {
	Login(ctx context.Context, username, password string) (api.LoginResponse, error)
	Logout(ctx context.Context, session string) error
	ChangePassword(ctx context.Context, session, currentPassword, newPassword string) error
	Status(ctx context.Context, session string) (api.Status, error)
	Players(ctx context.Context, session string) (api.Players, error)
	MinecraftAction(ctx context.Context, session, action string, confirmPlayers bool) (api.MinecraftActionResponse, error)
}

type sessionIdentityAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
}

type Config struct {
	Version        string
	Commit         string
	ManagementAPI  string
	ExternalScheme string
	SecureCookies  bool
}

type App struct {
	api       API
	config    Config
	templates *template.Template
	static    http.Handler
}

type dashboardAttention struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	Action string `json:"action,omitempty"`
	Label  string `json:"label,omitempty"`
}

type dashboardOperationAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminCurrentMigrationOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminCurrentRestoreOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminCurrentFactoryResetOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
}

type pageData struct {
	Title            string
	Version          string
	Commit           string
	ManagementAPI    string
	Error            string
	Message          string
	CSRF             string
	Status           api.Status
	Players          api.Players
	PendingAction    string
	ConfirmAction    string
	ConfirmLabel     string
	ConfirmOperation string
	ConfirmProgress  string
	ConfirmOnline    int
	ConfirmPlayers   []string
	Attention        *dashboardAttention
}

type dashboardSnapshot struct {
	Status    api.Status          `json:"status"`
	Players   api.Players         `json:"players"`
	Attention *dashboardAttention `json:"attention,omitempty"`
}

type aboutPageData struct {
	Title         string
	Version       string
	Commit        string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Status        api.Status
}

func New(client API, cfg Config) (*App, error) {
	if cfg.ExternalScheme == "" {
		cfg.ExternalScheme = "http"
	}
	t, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	return &App{api: client, config: cfg, templates: t, static: http.FileServer(http.FS(staticFS))}, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", a.static))
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /login", a.providerLoginPage)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /password", a.providerPasswordPage)
	mux.HandleFunc("POST /password", a.providerPasswordChange)
	mux.HandleFunc("GET /settings/authentication", a.authenticationPage)
	mux.HandleFunc("POST /settings/authentication", a.authenticationChange)
	a.registerAdminUsersRoutes(mux)
	a.registerAdminDiscoveryPages(mux)
	a.registerAdminValidationPages(mux)
	a.registerAdminRestorePages(mux)
	a.registerNewBackupsPages(mux)
	a.registerBackupsWorkspaceRoutes(mux)
	a.registerNewBackupsDeleteRoutes(mux)
	a.registerAdminDataMigrationPages(mux)
	a.registerAdminServerMigrationPages(mux)
	a.registerAdminMigrationWorkspacePages(mux)
	a.registerAdminSystemActionPages(mux)
	a.registerAdminSystemUpdatePages(mux)
	a.registerSystemMonitorRoutes(mux)
	a.registerUPSRoutes(mux)
	a.registerNetworkWorkspaceRoutes(mux)
	a.registerMinecraftWorkspaceRoutes(mux)
	a.registerSystemWorkspaceRoutes(mux)
	mux.HandleFunc("GET /api/dashboard-status", a.dashboardStatus)
	mux.HandleFunc("GET /about", a.aboutPage)
	mux.HandleFunc("POST /minecraft/start", a.minecraftAction("start"))
	mux.HandleFunc("POST /minecraft/stop", a.minecraftAction("stop"))
	mux.HandleFunc("POST /minecraft/restart", a.minecraftAction("restart"))
	mux.HandleFunc("GET /", a.dashboard)
	return securityHeaders(mux)
}

func (a *App) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	return srv.ListenAndServe()
}

func (a *App) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if _, err := a.api.Status(r.Context(), c.Value); err == nil {
			if mustChange(r) {
				http.Redirect(w, r, "/password", http.StatusSeeOther)
			} else {
				http.Redirect(w, r, "/", http.StatusSeeOther)
			}
			return
		}
	}
	a.render(w, "login.html", pageData{Title: "Sign in", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	result, err := a.api.Login(r.Context(), username, password)
	if err != nil {
		status := http.StatusBadGateway
		msg := "Management service unavailable"
		if errors.Is(err, api.ErrUnauthorized) {
			status = http.StatusUnauthorized
			msg = "Invalid administrator credentials"
		}
		w.WriteHeader(status)
		a.render(w, "login.html", pageData{Title: "Sign in", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, Error: msg})
		return
	}
	if result.ManagementAPI != "" && result.ManagementAPI != a.config.ManagementAPI {
		http.Error(w, "management API compatibility mismatch", http.StatusServiceUnavailable)
		return
	}
	csrf, err := randomToken(32)
	if err != nil {
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	a.setCookie(w, sessionCookie, result.Session, true)
	a.setCookie(w, csrfCookie, csrf, false)
	if result.MustChange {
		a.setCookie(w, mustChangeCookie, "1", true)
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	a.clearCookie(w, mustChangeCookie, true)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = a.api.Logout(r.Context(), c.Value)
	}
	a.clearSessionCookies(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) passwordPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := sessionFromRequest(r); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	csrf := csrfFromRequest(r)
	a.render(w, "password.html", pageData{Title: "Change password", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrf})
}

func (a *App) passwordChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")
	if newPassword != confirmPassword || len(newPassword) < 12 || len(newPassword) > 128 {
		w.WriteHeader(http.StatusBadRequest)
		a.render(w, "password.html", pageData{Title: "Change password", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrfFromRequest(r), Error: "New password must match and be 12 to 128 characters."})
		return
	}
	if err := a.api.ChangePassword(r.Context(), session, currentPassword, newPassword); err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			a.clearSessionCookies(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		a.render(w, "password.html", pageData{Title: "Change password", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrfFromRequest(r), Error: "Password change was rejected."})
		return
	}
	a.clearSessionCookies(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) aboutPage(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	identityClient, ok := a.api.(sessionIdentityAPI)
	if !ok {
		http.Error(w, "session information is unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, err := identityClient.Session(r.Context(), session)
	if err != nil {
		a.handleRolePageError(w, r, err)
		return
	}
	status, err := a.api.Status(r.Context(), session)
	if err != nil {
		a.handleRolePageError(w, r, err)
		return
	}
	a.renderAbout(w, aboutPageData{
		Title:         "About",
		Version:       a.config.Version,
		Commit:        a.config.Commit,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrfFromRequest(r),
		Identity:      identity,
		Status:        status,
	})
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data, err := a.dashboardData(r.Context(), session, csrfFromRequest(r))
	if err != nil {
		a.handleDashboardError(w, r, err)
		return
	}
	result := r.URL.Query().Get("result")
	data.Message = actionResultMessage(result)
	data.PendingAction = pendingAction(result)
	a.render(w, "dashboard.html", data)
}

func (a *App) dashboardStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if mustChange(r) {
		http.Error(w, "password change required", http.StatusForbidden)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	data, err := a.dashboardData(r.Context(), session, "")
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			a.clearSessionCookies(w)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if errors.Is(err, api.ErrPasswordChangeRequired) {
			http.Error(w, "password change required", http.StatusForbidden)
			return
		}
		http.Error(w, "management service unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(dashboardSnapshot{Status: data.Status, Players: data.Players, Attention: data.Attention}); err != nil {
		http.Error(w, "could not encode dashboard status", http.StatusInternalServerError)
	}
}

func (a *App) minecraftAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.validCSRF(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		session, ok := sessionFromRequest(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		confirmPlayers := r.FormValue("confirm_players") == "yes"
		result, actionErr := a.api.MinecraftAction(r.Context(), session, action, confirmPlayers)
		if errors.Is(actionErr, api.ErrUnauthorized) {
			a.clearSessionCookies(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if errors.Is(actionErr, api.ErrPasswordChangeRequired) {
			http.Redirect(w, r, "/password", http.StatusSeeOther)
			return
		}
		if actionErr == nil && result.ConfirmationRequired {
			data, err := a.dashboardData(r.Context(), session, csrfFromRequest(r))
			if err != nil {
				a.handleDashboardError(w, r, err)
				return
			}
			data.ConfirmAction = action
			data.ConfirmLabel = actionLabel(action)
			data.ConfirmOperation = actionOperation(action)
			data.ConfirmProgress = actionProgress(action)
			data.ConfirmOnline = result.Online
			data.ConfirmPlayers = result.Players
			a.render(w, "dashboard.html", data)
			return
		}
		if actionErr != nil {
			data, err := a.dashboardData(r.Context(), session, csrfFromRequest(r))
			if err != nil {
				a.handleDashboardError(w, r, err)
				return
			}
			if result.Message != "" {
				data.Error = result.Message
			} else {
				data.Error = "Minecraft operation failed."
			}
			w.WriteHeader(http.StatusBadRequest)
			a.render(w, "dashboard.html", data)
			return
		}
		http.Redirect(w, r, "/?result="+action, http.StatusSeeOther)
	}
}

func (a *App) dashboardData(ctx context.Context, session, csrf string) (pageData, error) {
	status, err := a.api.Status(ctx, session)
	if err != nil {
		return pageData{}, err
	}
	players, err := a.api.Players(ctx, session)
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
			return pageData{}, err
		}
		players = api.Players{Configured: status.Minecraft.Configured, State: "unavailable", Max: status.Minecraft.MaxPlayers, Error: "Player information is temporarily unavailable."}
	}
	data := pageData{
		Title:         "Dashboard",
		Version:       a.config.Version,
		Commit:        a.config.Commit,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrf,
		Status:        status,
		Players:       players,
	}
	if operations, ok := a.api.(dashboardOperationAPI); ok {
		identity, identityErr := operations.Session(ctx, session)
		if identityErr == nil && identity.Role == "administrator" {
			data.Attention = dashboardCurrentAttention(ctx, operations, session)
		}
	}
	return data, nil
}

func dashboardCurrentAttention(ctx context.Context, operations dashboardOperationAPI, session string) *dashboardAttention {
	if current, err := operations.AdminCurrentMigrationOperation(ctx, session); err == nil && current.Operation != nil && current.Operation.State == "needs_attention" {
		action := "/workspace/migration"
		label := "Open Server Migration"
		if current.Operation.OperationType == "migration_import" {
			action = "/workspace/migration/recovery"
			label = "Review Migration Recovery"
		}
		return &dashboardAttention{
			Title:  "Server Migration needs attention",
			Status: current.Operation.Status,
			Action: action,
			Label:  label,
		}
	}
	if current, err := operations.AdminCurrentRestoreOperation(ctx, session); err == nil && current.Operation != nil && current.Operation.State == "needs_attention" {
		return &dashboardAttention{
			Title:  "Restore needs attention",
			Status: current.Operation.Status,
			Action: "/workspace/backups?restore_operation=" + current.Operation.OperationID,
			Label:  "Review Restore",
		}
	}
	if current, err := operations.AdminCurrentFactoryResetOperation(ctx, session); err == nil && current.Operation != nil && current.Operation.State == "needs_attention" {
		return &dashboardAttention{
			Title:  "Full Factory Reset needs attention",
			Status: current.Operation.Status,
		}
	}
	return nil
}

func (a *App) handleDashboardError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	http.Error(w, "management service unavailable", http.StatusBadGateway)
}

func actionResultMessage(action string) string {
	switch action {
	case "start":
		return "Minecraft start requested."
	case "stop":
		return "Minecraft stop requested."
	case "restart":
		return "Minecraft restart requested."
	default:
		return ""
	}
}

func pendingAction(action string) string {
	switch action {
	case "start", "stop", "restart":
		return action
	default:
		return ""
	}
}

func actionLabel(action string) string {
	switch action {
	case "stop":
		return "Stop anyway"
	case "restart":
		return "Restart anyway"
	default:
		return "Continue"
	}
}

func actionOperation(action string) string {
	switch action {
	case "stop":
		return "Stopping Minecraft"
	case "restart":
		return "Restarting Minecraft"
	default:
		return "Continuing"
	}
}

func actionProgress(action string) string {
	switch action {
	case "start":
		return "Starting…"
	case "stop":
		return "Stopping…"
	case "restart":
		return "Restarting…"
	default:
		return "Working…"
	}
}

func (a *App) renderAbout(w http.ResponseWriter, data aboutPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "about.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (a *App) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func sessionFromRequest(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

func csrfFromRequest(r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil {
		return c.Value
	}
	return ""
}

func mustChange(r *http.Request) bool {
	c, err := r.Cookie(mustChangeCookie)
	return err == nil && c.Value == "1"
}

func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (a *App) setCookie(w http.ResponseWriter, name, value string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: a.config.SecureCookies, HttpOnly: httpOnly, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
}

func (a *App) clearCookie(w http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", Secure: a.config.SecureCookies, HttpOnly: httpOnly, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (a *App) clearSessionCookies(w http.ResponseWriter) {
	a.clearCookie(w, sessionCookie, true)
	a.clearCookie(w, csrfCookie, false)
	a.clearCookie(w, mustChangeCookie, true)
}

func (a *App) validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" || r.FormValue("csrf") != cookie.Value {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host != r.Host {
		return false
	}
	return u.Scheme == a.config.ExternalScheme
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
