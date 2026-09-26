package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminMigrationWorkspaceAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminMigrationExportDiscovery(ctx context.Context, session string) (api.AdminMigrationExportDiscoveryResponse, error)
	AdminMigrationImportDiscovery(ctx context.Context, session string) (api.AdminMigrationImportDiscoveryResponse, error)
	AdminMigrationRecoveryDiscovery(ctx context.Context, session string) (api.AdminMigrationRecoveryDiscoveryResponse, error)
	AdminCurrentMigrationOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
}

func splitMigrationSMBLocation(raw string) (string, string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	raw = strings.TrimRight(raw, "/")
	if !strings.HasPrefix(raw, "//") {
		return "", "", errors.New("enter the SMB location as //server/share or //server/share/folder")
	}
	parts := strings.Split(strings.TrimPrefix(raw, "//"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", errors.New("enter the SMB location as //server/share or //server/share/folder")
	}
	server := strings.TrimSpace(parts[0])
	share := strings.TrimSpace(parts[1])
	clean := make([]string, 0, len(parts)-2)
	for _, part := range parts[2:] {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", "", errors.New("SMB folder path cannot contain ..")
		}
		clean = append(clean, part)
	}
	return "//" + server + "/" + share, strings.Join(clean, "/"), nil
}

type migrationWorkspacePageData struct {
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

type migrationWorkspaceProgressPageData struct {
	Title             string
	Version           string
	ManagementAPI     string
	CSRF              string
	Identity          api.SessionInfo
	Operation         api.PersistentOperation
	OperationName     string
	StateLabel        string
	StageLabel        string
	CanReviewRecovery bool
}

func (a *App) registerAdminMigrationWorkspacePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspace/migration", a.migrationWorkspacePage)
	mux.HandleFunc("GET /workspace/migration/progress/{id}", a.migrationWorkspaceProgressPage)
	a.registerAdminMigrationWorkspaceExportPages(mux)
	a.registerAdminMigrationWorkspaceImportPages(mux)
	a.registerAdminMigrationWorkspaceRecoveryPages(mux)
}

func (a *App) migrationWorkspaceRequest(w http.ResponseWriter, r *http.Request) (string, adminMigrationWorkspaceAPI, api.SessionInfo, bool) {
	if mustChange(r) {
		http.Error(w, "Password change required.", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminMigrationWorkspaceAPI)
	if !ok {
		http.Error(w, "Server Migration management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceAuthError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) migrationWorkspacePage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.migrationWorkspaceRequest(w, r)
	if !ok {
		return
	}
	current, err := client.AdminCurrentMigrationOperation(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return
	}
	if current.Operation != nil {
		http.Redirect(w, r, "/workspace/migration/progress/"+current.Operation.OperationID, http.StatusSeeOther)
		return
	}

	exportDiscovery, err := client.AdminMigrationExportDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Export discovery is unavailable.")
		return
	}
	importDiscovery, err := client.AdminMigrationImportDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Import discovery is unavailable.")
		return
	}
	recoveryDiscovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Migration Recovery discovery is unavailable.")
		return
	}

	a.renderMigrationWorkspace(w, "migration_workspace.html", http.StatusOK, migrationWorkspacePageData{
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

func (a *App) migrationWorkspaceProgressPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.migrationWorkspaceRequest(w, r)
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
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return
	}
	if response.Operation == nil || !migrationWorkspaceOperationType(response.Operation.OperationType) {
		http.Error(w, "Server Migration operation not found", http.StatusNotFound)
		return
	}
	a.renderMigrationWorkspace(w, "migration_workspace_progress.html", http.StatusOK, migrationWorkspaceProgressPageData{
		Title:             "Server Migration progress",
		Version:           a.config.Version,
		ManagementAPI:     a.config.ManagementAPI,
		CSRF:              csrfFromRequest(r),
		Identity:          identity,
		Operation:         *response.Operation,
		OperationName:     migrationWorkspaceOperationName(response.Operation.OperationType),
		StateLabel:        migrationWorkspaceStateLabel(response.Operation.State),
		StageLabel:        migrationWorkspaceStageLabel(response.Operation.OperationType, response.Operation.Stage),
		CanReviewRecovery: response.Operation.OperationType == "migration_import" && response.Operation.State == "needs_attention",
	})
}

func migrationWorkspaceOperationType(value string) bool {
	switch value {
	case "migration_export", "migration_import", "migration_recovery":
		return true
	default:
		return false
	}
}

func migrationWorkspaceOperationName(value string) string {
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

func migrationWorkspaceStateLabel(value string) string {
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
		return migrationWorkspaceFriendlyToken(value)
	}
}

func migrationWorkspaceFriendlyToken(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return "Unknown"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func (a *App) handleMigrationWorkspaceAuthError(w http.ResponseWriter, r *http.Request, err error) {
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

func (a *App) handleMigrationWorkspaceRequestError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleMigrationWorkspaceAuthError(w, r, err)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) {
		message := strings.TrimSpace(responseErr.Message)
		if message == "" {
			message = fallback
		}
		status := responseErr.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		http.Error(w, message, status)
		return
	}
	message := fallback
	if detail := strings.TrimSpace(err.Error()); detail != "" {
		message += " " + detail
	}
	http.Error(w, message, http.StatusBadGateway)
}

func (a *App) renderMigrationWorkspace(w http.ResponseWriter, templateName string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := a.templates.ExecuteTemplate(w, templateName, data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
