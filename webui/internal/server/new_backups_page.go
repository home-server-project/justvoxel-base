package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type newBackupsAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	ManualBackup(ctx context.Context, session string) (api.ManualBackupResponse, error)
	AdminRestoreBackups(ctx context.Context, session string) (api.AdminRestoreBackupsResponse, error)
}

type newBackupView struct {
	ID             string
	CreatedAt      string
	CreatedLabel   string
	Size           string
	MetadataStatus string
	Version        string
	Bedrock        bool
	Variant        string
}

type newBackupsPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Backups       []newBackupView
	BackupCount   int
	TotalSize     string
	Message       string
	Error         string
}

func (a *App) registerNewBackupsPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/new-backups", a.newBackupsPage)
	mux.HandleFunc("POST /settings/new-backups/backup", a.newBackupsNow)
	a.registerNewBackupsDeleteRoutes(mux)
}

func (a *App) newBackupsRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, newBackupsAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(newBackupsAPI)
	if !ok {
		http.Error(w, "Backup management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleNewBackupsError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) newBackupsPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, false)
	if !ok {
		return
	}
	message := ""
	if r.URL.Query().Get("result") == "backup" {
		message = "Backup started. It will appear in the library when the backup service finishes."
	}
	a.renderNewBackupsPage(w, r, session, client, identity, message, "")
}

func (a *App) newBackupsNow(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, true)
	if !ok {
		return
	}
	result, err := client.ManualBackup(r.Context(), session)
	if err != nil {
		message := apiMessage(err, "Manual backup could not be started.")
		if result.Reason == "backup_cooldown" && result.RetryAfterSeconds > 0 {
			message = "Backup available in " + formatRemaining(result.RetryAfterSeconds) + "."
		} else if result.AdministratorResetRequired || result.Reason == "backup_limit_reached" {
			message = "Backup unavailable. Administrator reset required."
		}
		w.WriteHeader(http.StatusBadRequest)
		a.renderNewBackupsPage(w, r, session, client, identity, "", message)
		return
	}
	http.Redirect(w, r, "/settings/new-backups?result=backup", http.StatusSeeOther)
}

func (a *App) renderNewBackupsPage(w http.ResponseWriter, r *http.Request, session string, client newBackupsAPI, identity api.SessionInfo, message, pageError string) {
	backups, err := client.AdminRestoreBackups(r.Context(), session)
	if err != nil {
		a.handleNewBackupsError(w, r, err)
		return
	}
	views := make([]newBackupView, 0, len(backups.Backups))
	var totalBytes uint64
	for _, backup := range backups.Backups {
		totalBytes += backup.SizeBytes
		view := newBackupView{
			ID:             backup.ID,
			CreatedAt:      backup.CreatedAt,
			CreatedLabel:   formatBackupLibraryTime(backup.CreatedAt),
			Size:           humanBytes(backup.SizeBytes),
			MetadataStatus: backup.MetadataStatus,
		}
		if backup.Metadata != nil {
			view.Version = backup.Metadata.Minecraft.ServerReportedVersion
			if view.Version == "" {
				view.Version = backup.Metadata.Minecraft.ConfiguredVersion
			}
			view.Bedrock = backup.Metadata.Bedrock.Enabled
			view.Variant = backup.Metadata.JustVoxel.Variant
		}
		views = append(views, view)
	}
	data := newBackupsPageData{
		Title: "New Backups", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Backups: views,
		BackupCount: len(views), TotalSize: humanBytes(totalBytes), Message: message, Error: pageError,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "new_backups.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func formatBackupLibraryTime(raw string) string {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format("Jan 2, 2006 · 15:04 UTC")
}

func (a *App) handleNewBackupsError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	http.Error(w, "backup management service unavailable", http.StatusBadGateway)
}
