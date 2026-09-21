package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminRestoreAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminRestoreBackups(ctx context.Context, session string) (api.AdminRestoreBackupsResponse, error)
	AdminRestorePlan(ctx context.Context, session string, request api.AdminRestorePlanRequest) (api.AdminRestorePlanResponse, error)
	AdminRestoreApply(ctx context.Context, session string, request api.AdminRestoreApplyRequest) (api.AdminRestoreApplyResponse, error)
	AdminCurrentRestoreOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
}

type restoreBackupView struct {
	Backup api.AdminRestoreBackup
	Size   string
}

type restorePageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Backups       []restoreBackupView
	Error         string
}

type restoreReviewPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Request       api.AdminRestorePlanRequest
	Plan          api.AdminRestorePlanResponse
	Size          string
	ModeTitle     string
	Scope         string
	Error         string
}

type restoreProgressPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Operation     api.PersistentOperation
	StateLabel    string
	StageLabel    string
}

func (a *App) registerAdminRestorePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/restore", a.restorePage)
	mux.HandleFunc("GET /settings/restore/review", a.restoreReviewPage)
	mux.HandleFunc("POST /settings/restore/apply", a.restoreApply)
	mux.HandleFunc("GET /settings/restore/progress/{id}", a.restoreProgressPage)
	mux.HandleFunc("GET /api/restore/progress/{id}", a.restoreProgressStatus)
}

func (a *App) restoreRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminRestoreAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminRestoreAPI)
	if !ok {
		http.Error(w, "Restore management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleRestoreAuthError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) restorePage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.restoreRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentRestoreOperation(w, r, session, client) {
		return
	}
	backups, err := client.AdminRestoreBackups(r.Context(), session)
	if err != nil {
		a.handleRestoreRequestError(w, r, err, "Restore backup discovery is unavailable.")
		return
	}
	views := make([]restoreBackupView, 0, len(backups.Backups))
	for _, backup := range backups.Backups {
		views = append(views, restoreBackupView{Backup: backup, Size: humanBytes(backup.SizeBytes)})
	}
	a.renderRestore(w, "restore.html", http.StatusOK, restorePageData{
		Title: "Restore Minecraft", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Backups: views,
	})
}

func (a *App) restoreReviewPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.restoreRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentRestoreOperation(w, r, session, client) {
		return
	}
	request := api.AdminRestorePlanRequest{
		BackupID: strings.TrimSpace(r.URL.Query().Get("backup_id")),
		Mode:     strings.TrimSpace(r.URL.Query().Get("mode")),
	}
	if request.BackupID == "" || (request.Mode != "world" && request.Mode != "full") {
		http.Redirect(w, r, "/settings/restore", http.StatusSeeOther)
		return
	}
	plan, err := client.AdminRestorePlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderRestoreReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Restore planning was rejected."))
			return
		}
		a.handleRestoreRequestError(w, r, err, "Restore planning is unavailable.")
		return
	}
	a.renderRestoreReview(w, http.StatusOK, identity, request, plan, csrfFromRequest(r), "")
}

func (a *App) restoreApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.restoreRequest(w, r, true)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid Restore request", http.StatusBadRequest)
		return
	}
	request := api.AdminRestorePlanRequest{
		BackupID: strings.TrimSpace(r.FormValue("backup_id")),
		Mode:     strings.TrimSpace(r.FormValue("mode")),
	}
	plan, err := client.AdminRestorePlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderRestoreReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Restore planning was rejected."))
			return
		}
		a.handleRestoreRequestError(w, r, err, "Restore planning is unavailable.")
		return
	}
	submittedFingerprint := strings.TrimSpace(r.FormValue("plan_fingerprint"))
	if submittedFingerprint == "" || submittedFingerprint != plan.PlanFingerprint {
		a.renderRestoreReview(w, http.StatusConflict, identity, request, plan, csrfFromRequest(r), "This Restore changed since it was reviewed. Review the current plan before continuing.")
		return
	}
	if strings.TrimSpace(r.FormValue("confirmation")) != "RESTORE" {
		a.renderRestoreReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), "Type RESTORE exactly to confirm this destructive operation.")
		return
	}
	playersConfirmed := r.FormValue("players_confirmed") == "yes"
	if plan.Requirements != nil && plan.Requirements.PlayersConfirmationRequired && !playersConfirmed {
		a.renderRestoreReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), "Confirm that the online players may be interrupted before starting Restore.")
		return
	}
	result, err := client.AdminRestoreApply(r.Context(), session, api.AdminRestoreApplyRequest{
		PlanFingerprint: plan.PlanFingerprint,
		Request: request, DestructiveConfirmed: true, PlayersConfirmed: playersConfirmed,
	})
	if err != nil {
		a.renderRestoreReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Could not start Restore."))
		return
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "restore" {
		a.renderRestoreReview(w, http.StatusBadGateway, identity, request, plan, csrfFromRequest(r), "Restore did not return a valid persistent operation.")
		return
	}
	http.Redirect(w, r, "/settings/restore/progress/"+result.Operation.OperationID, http.StatusSeeOther)
}

func (a *App) restoreProgressPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.restoreRequest(w, r, false)
	if !ok {
		return
	}
	response, err := client.AdminOperation(r.Context(), session, r.PathValue("id"))
	if err != nil {
		a.handleRestoreProgressError(w, r, err)
		return
	}
	if response.Operation == nil || response.Operation.OperationType != "restore" {
		http.Error(w, "Restore operation not found", http.StatusNotFound)
		return
	}
	a.renderRestore(w, "restore_progress.html", http.StatusOK, restoreProgressPageData{
		Title: "Restore progress", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Operation: *response.Operation,
		StateLabel: restoreOperationStateLabel(response.Operation.State),
		StageLabel: restoreOperationStageLabel(response.Operation.Stage),
	})
}

func (a *App) restoreProgressStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	client, ok := a.api.(adminRestoreAPI)
	if !ok {
		http.Error(w, "Restore operation tracking is unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, err := client.Session(r.Context(), session)
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
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	response, err := client.AdminOperation(r.Context(), session, r.PathValue("id"))
	if err != nil {
		a.handleRestoreProgressAPIError(w, err)
		return
	}
	if response.Operation == nil || response.Operation.OperationType != "restore" {
		http.Error(w, "Restore operation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "could not encode Restore operation", http.StatusInternalServerError)
	}
}

func (a *App) redirectCurrentRestoreOperation(w http.ResponseWriter, r *http.Request, session string, client adminRestoreAPI) bool {
	response, err := client.AdminCurrentRestoreOperation(r.Context(), session)
	if err != nil {
		a.handleRestoreRequestError(w, r, err, "Restore operation status is unavailable.")
		return true
	}
	if response.Operation == nil {
		return false
	}
	if response.Operation.OperationType != "restore" {
		http.Error(w, "Restore operation status is invalid", http.StatusBadGateway)
		return true
	}
	http.Redirect(w, r, "/settings/restore/progress/"+response.Operation.OperationID, http.StatusSeeOther)
	return true
}

func (a *App) renderRestoreReview(w http.ResponseWriter, status int, identity api.SessionInfo, request api.AdminRestorePlanRequest, plan api.AdminRestorePlanResponse, csrf, errorMessage string) {
	size := ""
	if plan.Normalized != nil {
		size = humanBytes(plan.Normalized.SizeBytes)
	}
	a.renderRestore(w, "restore_review.html", status, restoreReviewPageData{
		Title: "Review Restore", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrf, Identity: identity, Request: request, Plan: plan, Size: size,
		ModeTitle: restoreModeTitle(request.Mode), Scope: restoreModeScope(request.Mode), Error: errorMessage,
	})
}

func (a *App) renderRestore(w http.ResponseWriter, name string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil && status == http.StatusOK {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func restoreModeTitle(mode string) string {
	if mode == "full" {
		return "Restore full Minecraft data"
	}
	return "Restore world"
}

func restoreModeScope(mode string) string {
	if mode == "full" {
		return "Replaces the complete Minecraft persistent data directory, including worlds, plugins, plugin data, server.properties, whitelist/ops, and other Minecraft-side persistent files. JustVoxel system configuration, container policy, Minecraft version policy, and the operating-system deployment remain unchanged."
	}
	return "Replaces the primary Minecraft world and its standard Nether/End world directories. World-based player data rolls back with the world. Plugins, plugin data outside the world directories, server.properties, whitelist/ops, JustVoxel configuration, container policy, Minecraft version policy, and the operating-system deployment remain unchanged."
}

func restoreOperationStateLabel(state string) string {
	switch state {
	case "queued":
		return "Queued"
	case "validating":
		return "Validating"
	case "running":
		return "Restoring"
	case "verifying":
		return "Runtime validation"
	case "failed":
		return "Recovering"
	case "rolling_back":
		return "Rolling back"
	case "rolled_back":
		return "Rolled back"
	case "needs_attention":
		return "Needs attention"
	case "succeeded":
		return "Complete"
	default:
		return "Working"
	}
}

func restoreOperationStageLabel(stage string) string {
	switch stage {
	case "queued":
		return "Waiting to start"
	case "restore_preflight", "backup_source":
		return "Checking backup storage"
	case "archive_integrity":
		return "Checking archive integrity"
	case "archive_safety":
		return "Checking archive safety"
	case "staging_space":
		return "Checking staging space"
	case "staging":
		return "Preparing Restore data"
	case "minecraft_stop":
		return "Stopping Minecraft safely"
	case "switching":
		return "Applying Restore data"
	case "minecraft_runtime":
		return "Validating restored Minecraft"
	case "restore_failed":
		return "Preparing recovery"
	case "rollback":
		return "Restoring previous Minecraft data"
	case "restore_rolled_back":
		return "Rolled back"
	case "restore_needs_attention", "interrupted":
		return "Administrator attention required"
	case "completed":
		return "Complete"
	default:
		return "Working"
	}
}

func (a *App) handleRestoreAuthError(w http.ResponseWriter, r *http.Request, err error) {
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

func (a *App) handleRestoreRequestError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleRestoreAuthError(w, r, err)
		return
	}
	http.Error(w, fallback, http.StatusServiceUnavailable)
}

func (a *App) handleRestoreProgressError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleRestoreAuthError(w, r, err)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		http.Error(w, "Restore operation not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Restore operation status is unavailable", http.StatusBadGateway)
}

func (a *App) handleRestoreProgressAPIError(w http.ResponseWriter, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Error(w, "password change required", http.StatusForbidden)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		http.Error(w, "Restore operation not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Restore operation status is unavailable", http.StatusBadGateway)
}

func restoreReviewURL(request api.AdminRestorePlanRequest) string {
	return "/settings/restore/review?" + url.Values{"backup_id": {request.BackupID}, "mode": {request.Mode}}.Encode()
}
