package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminServerMigrationAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminMigrationExportDiscovery(ctx context.Context, session string) (api.AdminMigrationExportDiscoveryResponse, error)
	AdminMigrationImportDiscovery(ctx context.Context, session string) (api.AdminMigrationImportDiscoveryResponse, error)
	AdminMigrationRecoveryDiscovery(ctx context.Context, session string) (api.AdminMigrationRecoveryDiscoveryResponse, error)
	AdminCurrentMigrationOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
}

type serverMigrationPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Export        api.AdminMigrationExportDiscoveryResponse
	Import        api.AdminMigrationImportDiscoveryResponse
	Recovery      api.AdminMigrationRecoveryDiscoveryResponse
	ExportTargets string
	ImportSources string
}

type serverMigrationProgressPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Operation     api.PersistentOperation
	OperationName string
	StateLabel    string
	StageLabel       string
	CanReviewRecovery bool
}

func (a *App) registerAdminServerMigrationPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/server-migration", a.serverMigrationPage)
	mux.HandleFunc("GET /settings/server-migration/progress/{id}", a.serverMigrationProgressPage)
	a.registerAdminServerExportPages(mux)
	a.registerAdminServerImportPages(mux)
	a.registerAdminServerRecoveryPages(mux)
}

func (a *App) serverMigrationRequest(w http.ResponseWriter, r *http.Request) (string, adminServerMigrationAPI, api.SessionInfo, bool) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminServerMigrationAPI)
	if !ok {
		http.Error(w, "Server Migration management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleServerMigrationAuthError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) serverMigrationPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.serverMigrationRequest(w, r)
	if !ok {
		return
	}
	current, err := client.AdminCurrentMigrationOperation(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return
	}
	if current.Operation != nil {
		http.Redirect(w, r, "/settings/server-migration/progress/"+current.Operation.OperationID, http.StatusSeeOther)
		return
	}

	exportDiscovery, err := client.AdminMigrationExportDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Export discovery is unavailable.")
		return
	}
	importDiscovery, err := client.AdminMigrationImportDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Import discovery is unavailable.")
		return
	}
	recoveryDiscovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Migration Recovery discovery is unavailable.")
		return
	}

	a.renderServerMigration(w, "server_migration.html", http.StatusOK, serverMigrationPageData{
		Title:         "Server Migration",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrfFromRequest(r),
		Identity:      identity,
		Export:        exportDiscovery,
		Import:        importDiscovery,
		Recovery:      recoveryDiscovery,
		ExportTargets: strings.Join(exportDiscovery.TargetKinds, ", "),
		ImportSources: strings.Join(importDiscovery.SourceKinds, ", "),
	})
}

func (a *App) serverMigrationProgressPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.serverMigrationRequest(w, r)
	if !ok {
		return
	}
	response, err := client.AdminOperation(r.Context(), session, r.PathValue("id"))
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
			http.Error(w, "Server Migration operation not found", http.StatusNotFound)
			return
		}
		a.handleServerMigrationRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return
	}
	if response.Operation == nil || !serverMigrationOperationType(response.Operation.OperationType) {
		http.Error(w, "Server Migration operation not found", http.StatusNotFound)
		return
	}
	a.renderServerMigration(w, "server_migration_progress.html", http.StatusOK, serverMigrationProgressPageData{
		Title:         "Server Migration progress",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrfFromRequest(r),
		Identity:      identity,
		Operation:     *response.Operation,
		OperationName: serverMigrationOperationName(response.Operation.OperationType),
		StateLabel:    serverMigrationStateLabel(response.Operation.State),
		StageLabel:       serverMigrationStageLabel(response.Operation.OperationType, response.Operation.Stage),
		CanReviewRecovery: response.Operation.OperationType == "migration_import" && response.Operation.State == "needs_attention",
	})
}

func serverMigrationOperationType(value string) bool {
	switch value {
	case "migration_export", "migration_import", "migration_recovery":
		return true
	default:
		return false
	}
}

func serverMigrationOperationName(value string) string {
	switch value {
	case "migration_export":
		return "Server Export"
	case "migration_import":
		return "Server Import"
	case "migration_recovery":
		return "Migration Recovery"
	default:
		return "Server Migration"
	}
}

func serverMigrationStateLabel(value string) string {
	switch value {
	case "queued":
		return "Queued"
	case "validating":
		return "Validating"
	case "running":
		return "Running"
	case "verifying":
		return "Verifying"
	case "rolling_back":
		return "Rolling back"
	case "succeeded":
		return "Succeeded"
	case "rolled_back":
		return "Rolled back"
	case "needs_attention":
		return "Needs attention"
	default:
		return friendlyMigrationToken(value)
	}
}

func friendlyMigrationToken(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return "Unknown"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func (a *App) handleServerMigrationAuthError(w http.ResponseWriter, r *http.Request, err error) {
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

func (a *App) handleServerMigrationRequestError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleServerMigrationAuthError(w, r, err)
		return
	}
	http.Error(w, fallback, http.StatusBadGateway)
}

func (a *App) renderServerMigration(w http.ResponseWriter, templateName string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := a.templates.ExecuteTemplate(w, templateName, data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
