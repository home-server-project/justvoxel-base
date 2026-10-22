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
	"os"
	"path/filepath"
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
	DashboardURL        string
	ConnectionGuide     *connectionGuide
	ConnectionServers   []selectedServerContext
	Title               string
	Version             string
	Commit              string
	ManagementAPI       string
	Error               string
	Message             string
	CSRF                string
	Username            string
	Status              api.Status
	Players             api.Players
	PendingAction       string
	SelectedMinecraftID string
	ConfirmAction       string
	ConfirmLabel        string
	ConfirmOperation    string
	ConfirmProgress     string
	ConfirmOnline       int
	ConfirmPlayers      []string
	Administrator       bool
	InstanceRequired    bool
	Attention           *dashboardAttention
}

type dashboardSnapshot struct {
	Status    api.Status          `json:"status"`
	Players   api.Players         `json:"players"`
	Attention *dashboardAttention `json:"attention,omitempty"`
}

// Read the appliance timezone for every page render, including non-admin clocks.
// This is read-only presentation data; date/time changes still use the Management API.
func systemTimezone() string {
	path, err := filepath.EvalSymlinks("/etc/localtime")
	if err == nil {
		if _, zone, ok := strings.Cut(path, "/zoneinfo/"); ok {
			if _, err := time.LoadLocation(zone); err == nil {
				return zone
			}
		}
	}
	// Some systems provide /etc/timezone instead of a zoneinfo symlink.
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		zone := strings.TrimSpace(string(data))
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	return "UTC"
}

func New(client API, cfg Config) (*App, error) {
	if cfg.ExternalScheme == "" {
		cfg.ExternalScheme = "http"
	}
	t, err := template.New("pages").Funcs(template.FuncMap{"systemTimezone": systemTimezone, "systemNow": func() int64 { return time.Now().UnixMilli() }}).ParseFS(assets, "templates/*.html")
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
	mux.HandleFunc("GET /factory-reset-complete", a.factoryResetCompletePage)
	a.registerRolePages(mux)
	a.registerAdminDiscoveryPages(mux)
	a.registerBackupsWorkspaceRoutes(mux)
	a.registerNewBackupsDeleteRoutes(mux)
	a.registerAdminMigrationWorkspacePages(mux)
	a.registerAdminSystemActionPages(mux)
	a.registerAdminSystemUpdatePages(mux)
	a.registerSystemMonitorRoutes(mux)
	a.registerUPSRoutes(mux)
	a.registerNetworkWorkspaceRoutes(mux)
	a.registerMinecraftWorkspaceRoutes(mux)
	a.registerPluginsWorkspaceRoutes(mux)
	a.registerSystemWorkspaceRoutes(mux)
	mux.HandleFunc("GET /minecraft/how-to-connect", a.howToConnect)
	mux.HandleFunc("GET /api/minecraft/servers/invitation", a.minecraftServersInvitation)
	mux.HandleFunc("POST /api/minecraft/servers/lifecycle", a.minecraftServersLifecycle)
	mux.HandleFunc("GET /api/dashboard-status", a.dashboardStatus)
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
	originalQuery := r.URL.RawQuery
	selected, ok := a.dashboardBrowserSelection(w, r, session)
	if !ok {
		return
	}
	data, err := a.dashboardData(r.Context(), session, csrfFromRequest(r), selected)
	if err != nil {
		a.handleDashboardError(w, r, err)
		return
	}
	if originalQuery != r.URL.RawQuery {
		data.DashboardURL = r.URL.RequestURI()
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
	selected, ok := a.dashboardMinecraftSelection(w, r, session)
	if !ok {
		return
	}
	data, err := a.dashboardData(r.Context(), session, "", selected)
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
		// Reuse the authoritative resolver; a confirmation form carries the
		// resolved identity rather than resolving a new selection on retry.
		selectionRequest := r.Clone(r.Context())
		selectionURL := *r.URL
		selectionRequest.URL = &selectionURL
		if values, exists := r.PostForm["server"]; exists {
			if len(values) != 1 || !addServerIDPattern.MatchString(values[0]) {
				http.Error(w, "Invalid server selection.", http.StatusBadRequest)
				return
			}
			query := selectionURL.Query()
			if existing, exists := query["server"]; exists && (len(existing) != 1 || existing[0] != values[0]) {
				http.Error(w, "Invalid server selection.", http.StatusBadRequest)
				return
			}
			query.Set("server", values[0])
			selectionURL.RawQuery = query.Encode()
		}
		selected, ok := a.selectedMinecraft(w, selectionRequest, session)
		if !ok {
			return
		}
		selectedID := selected.ID
		client, ok := a.api.(interface {
			MinecraftInstanceActionConfirmed(context.Context, string, string, string, bool) (api.MinecraftActionResponse, error)
		})
		if !ok {
			http.Error(w, "Minecraft control is unavailable.", http.StatusServiceUnavailable)
			return
		}
		result, actionErr := client.MinecraftInstanceActionConfirmed(r.Context(), session, selectedID, action, confirmPlayers)
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
			data, err := a.dashboardData(r.Context(), session, csrfFromRequest(r), selectedServerContext{ID: selectedID})
			if err != nil {
				a.handleDashboardError(w, r, err)
				return
			}
			data.SelectedMinecraftID = selectedID
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
			data, err := a.dashboardData(r.Context(), session, csrfFromRequest(r), selectedServerContext{ID: selectedID})
			if err != nil {
				a.handleDashboardError(w, r, err)
				return
			}
			data.SelectedMinecraftID = selectedID
			if result.Message != "" {
				data.Error = result.Message
			} else {
				data.Error = "Minecraft operation failed."
			}
			w.WriteHeader(http.StatusBadRequest)
			a.render(w, "dashboard.html", data)
			return
		}
		destination := "/?result=" + action
		if selectedID != "" {
			destination += "&server=" + selectedID
		}
		http.Redirect(w, r, destination, http.StatusSeeOther)
	}
}

// Browser navigation must never use the selected-server API error writer.
func (a *App) dashboardBrowserSelection(w http.ResponseWriter, r *http.Request, session string) (selectedServerContext, bool) {
	instances, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		a.handleDashboardError(w, r, err)
		return selectedServerContext{}, false
	}
	query := r.URL.Query()
	originalQuery := query.Encode()
	values, explicit := query["server"]
	selected := selectedServerContext{}
	if explicit {
		if len(values) == 1 && addServerIDPattern.MatchString(values[0]) {
			for _, instance := range instances {
				if instance.ID == values[0] {
					selected = selectedServerContext{ID: instance.ID, Name: instance.DisplayName}
					break
				}
			}
		}
		if selected.ID == "" {
			query.Del("server")
		}
	} else if len(instances) == 1 {
		selected = selectedServerContext{ID: instances[0].ID, Name: instances[0].DisplayName}
	}
	if query.Get("workspace") == "version" && query.Get("tab") == "software" {
		query.Set("tab", "switch")
	}
	// Render stale selections unselected, without resolving a different server.
	// The dashboard passes this canonical URL to its existing browser script.
	if query.Encode() != originalQuery {
		r.URL.RawQuery = query.Encode()
	}
	return selected, true
}

// Preserve the empty-inventory setup experience, including the connection guide.
// Only the appliance dashboard may leave a multi-server inventory unselected.
func (a *App) dashboardMinecraftSelection(w http.ResponseWriter, r *http.Request, session string) (selectedServerContext, bool) {
	if _, explicit := r.URL.Query()["server"]; !explicit {
		if r.URL.Path == "/api/dashboard-status" && r.URL.Query().Get("selection") == "none" {
			return selectedServerContext{}, true
		}
		instances, err := a.independentServerInventory(r.Context(), session)
		if err != nil {
			a.handleDashboardError(w, r, err)
			return selectedServerContext{}, false
		}
		if len(instances) == 0 {
			return selectedServerContext{}, true
		}
		if len(instances) > 1 && (r.URL.Path == "/" || r.URL.Path == "/api/dashboard-status") {
			return selectedServerContext{}, true
		}
	}
	return a.selectedMinecraft(w, r, session)
}

// Translate verified instance configuration into the existing presentation contract.
// The configuration client verifies the response identity before returning settings.
func instancePresentation(configuration api.InstanceRuntimeSettings) api.Status {
	var status api.Status
	mc := &status.Minecraft
	mc.Configured = true
	mc.State = "Unavailable"
	mc.ServerSoftware = serverSoftware(configuration.Software)
	mc.ServerType = configuration.Software
	mc.Version = configuration.Version
	mc.MaxPlayers = configuration.MaxPlayers
	mc.JavaPort = configuration.JavaPort
	mc.Bedrock = configuration.BedrockEnabled
	mc.BedrockPort = configuration.BedrockPort
	return status
}

func (a *App) dashboardData(ctx context.Context, session, csrf string, selected selectedServerContext) (pageData, error) {
	status, err := a.api.Status(ctx, session)
	if err != nil {
		return pageData{}, err
	}
	// Appliance status supplies host and JustVoxel information only.
	status.Backup = (api.Status{}).Backup
	status.Minecraft = (api.Status{}).Minecraft
	status.Minecraft.State = "Not configured"
	players := api.Players{State: "not_configured"}
	if selected.ID == "" {
		instances, inventoryErr := a.independentServerInventory(ctx, session)
		if inventoryErr != nil {
			return pageData{}, inventoryErr
		}
		if len(instances) > 0 {
			status.Minecraft.Configured = true
			status.Minecraft.State = "Choose server"
			players = api.Players{Configured: true, State: "selection_required"}
		}
	}
	if selected.ID != "" {
		if client, ok := a.api.(interface {
			MinecraftInstanceBackupSettings(context.Context, string, string) (api.InstanceBackupSettings, error)
		}); ok {
			settings, backupErr := client.MinecraftInstanceBackupSettings(ctx, session, selected.ID)
			if errors.Is(backupErr, api.ErrUnauthorized) || errors.Is(backupErr, api.ErrPasswordChangeRequired) {
				return pageData{}, backupErr
			}
			if backupErr == nil {
				status.Backup.Enabled = settings.Enabled
			}
		}
		status.Minecraft.State = "Unavailable"
		players = api.Players{State: "unavailable", Error: "Player information is temporarily unavailable."}
		if client, ok := a.api.(interface {
			MinecraftInstanceConfiguration(context.Context, string, string) (api.InstanceRuntimeSettings, error)
			MinecraftInstanceStatus(context.Context, string, string) (api.MinecraftInstanceStatus, error)
			MinecraftInstancePlayers(context.Context, string, string) (api.InstancePlayers, error)
		}); ok {
			configuration, configErr := client.MinecraftInstanceConfiguration(ctx, session, selected.ID)
			if configErr == nil {
				status.Minecraft = instancePresentation(configuration).Minecraft
				players.Configured, players.Max = true, configuration.MaxPlayers
			}
			runtime, runtimeErr := client.MinecraftInstanceStatus(ctx, session, selected.ID)
			if runtimeErr == nil && runtime.ID == selected.ID {
				summary := minecraftServerSummary{}
				summary.setState(runtime.State)
				status.Minecraft.State = summary.State
			}
			currentPlayers, playersErr := client.MinecraftInstancePlayers(ctx, session, selected.ID)
			if playersErr == nil && currentPlayers.ID == selected.ID {
				players.State, players.Online, players.Max = currentPlayers.State, currentPlayers.Online, currentPlayers.Maximum
				players.Error = ""
				status.Minecraft.Players = currentPlayers.Online
			}
			for _, readErr := range []error{configErr, runtimeErr, playersErr} {
				if errors.Is(readErr, api.ErrUnauthorized) || errors.Is(readErr, api.ErrPasswordChangeRequired) {
					return pageData{}, readErr
				}
			}
		}
	}
	data := pageData{
		SelectedMinecraftID: selected.ID,
		Title:               "Dashboard",
		Version:             a.config.Version,
		Commit:              a.config.Commit,
		ManagementAPI:       a.config.ManagementAPI,
		CSRF:                csrf,
		Status:              status,
		Players:             players,
	}
	if identityClient, ok := a.api.(sessionIdentityAPI); ok {
		identity, identityErr := identityClient.Session(ctx, session)
		if identityErr == nil {
			data.Username = identity.Username
			data.Administrator = identity.Role == "administrator"
			if operations, ok := a.api.(dashboardOperationAPI); ok && identity.Role == "administrator" {
				data.Attention = dashboardCurrentAttention(ctx, operations, session)
			}
			if identity.Role == "administrator" && selected.ID != "" {
				if client, ok := a.api.(selectedBackupsAPI); ok {
					progress, restoreErr := readSelectedRestore(ctx, client, session, selected.ID)
					if data.Attention == nil && restoreErr == nil && progress.RecoveryRequired {
						data.Attention = &dashboardAttention{Title: "Restore needs attention", Status: progress.Status, Action: "/workspace/backups?server=" + url.QueryEscape(selected.ID), Label: "Review Restore"}
					}
				}
			}
		}
	}
	return data, nil
}

func dashboardCurrentAttention(ctx context.Context, operations dashboardOperationAPI, session string) *dashboardAttention {
	if current, err := operations.AdminCurrentMigrationOperation(ctx, session); err == nil && current.Operation != nil && current.Operation.State == "needs_attention" {
		action := "/?workspace=migration"
		label := "Open Server Migration"
		title := "Server Migration needs attention"
		if current.Operation.OperationType == "migration_import" || current.Operation.OperationType == "migration_recovery" {
			action = "/?workspace=migration&tab=recovery"
			label = "Review Migration Recovery"
		}
		if current.Operation.OperationType == "migration_recovery" {
			title = "Migration Recovery needs attention"
		}
		return &dashboardAttention{
			Title:  title,
			Status: current.Operation.Status,
			Action: action,
			Label:  label,
		}
	}
	// Legacy journals have no authoritative instance ID. Keep attention visible;
	// TODO: offer recovery only when original target ownership can be proven.
	if current, err := operations.AdminCurrentRestoreOperation(ctx, session); err == nil && current.Operation != nil && current.Operation.State == "needs_attention" {
		return &dashboardAttention{
			Title:  "Restore needs attention",
			Status: current.Operation.Status,
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'sha256-ypuHOrHdFK0ra+bQlE+zbe2vHCx4oWqJLuhMbpi6gms='; script-src 'self'; img-src 'self' http: https: blob:; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
