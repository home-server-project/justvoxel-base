package server

import (
	"context"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type systemWorkspaceLogsAPI interface {
	AdminDiagnosticLogs(context.Context, string) ([]api.DiagnosticLogEntry, error)
	AdminDiagnosticLog(context.Context, string, string, string) ([]byte, error)
}

func (a *App) systemWorkspaceLogs(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(systemWorkspaceLogsAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Diagnostic logs are unavailable.")
		return
	}
	logs, err := client.AdminDiagnosticLogs(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Diagnostic logs are unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": logs})
}

func (a *App) systemWorkspaceLog(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	category, id := r.PathValue("category"), r.PathValue("id")
	if !validWorkspaceLogID(category, id) {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Invalid diagnostic log identifier.")
		return
	}
	client, ok := a.api.(systemWorkspaceLogsAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Diagnostic logs are unavailable.")
		return
	}
	data, err := client.AdminDiagnosticLog(r.Context(), session, category, id)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Diagnostic log is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename=justvoxel-"+category+"-"+id+".log")
	}
	_, _ = w.Write(data)
}

func validWorkspaceLogID(category, id string) bool {
	switch category {
	case "setup", "migration", "restore", "reset", "operation":
	default:
		return false
	}
	if len(id) != 36 {
		return false
	}
	for index, char := range id {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return id[14] == '4' && (id[19] == '8' || id[19] == '9' || id[19] == 'a' || id[19] == 'b')
}
