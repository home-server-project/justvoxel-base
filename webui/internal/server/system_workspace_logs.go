package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type systemWorkspaceLogsAPI interface {
	AdminDiagnosticLogs(context.Context, string) ([]api.DiagnosticLogEntry, error)
	AdminDiagnosticLog(context.Context, string, string, string) ([]byte, error)
}

type systemWorkspaceFailedUnitsAPI interface {
	AdminFailedUnits(context.Context, string) ([]api.FailedUnit, error)
	AdminFailedUnitJournal(context.Context, string, string) ([]byte, error)
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
	units := make([]api.FailedUnit, 0)
	unavailable := ""
	if live, supported := a.api.(systemWorkspaceFailedUnitsAPI); supported {
		units, err = live.AdminFailedUnits(r.Context(), session)
		if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
			a.writeSystemWorkspaceAPIError(w, err, "Failed systemd units are unavailable.")
			return
		}
		if err != nil {
			unavailable = "Failed systemd units are unavailable."
			units = nil
		}
	} else {
		unavailable = "Failed systemd units are unavailable."
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": logs, "failed_units": units, "failed_units_error": unavailable})
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
	case "setup", "migration", "restore", "plugins", "reset", "operation":
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

func (a *App) systemWorkspaceFailedUnitJournal(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	unit := r.PathValue("unit")
	if !api.ValidFailedUnitName(unit) {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Invalid failed unit identifier.")
		return
	}
	client, ok := a.api.(systemWorkspaceFailedUnitsAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Failed unit journal is unavailable.")
		return
	}
	data, err := client.AdminFailedUnitJournal(r.Context(), session, unit)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, api.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		if errors.Is(err, api.ErrPasswordChangeRequired) {
			status = http.StatusForbidden
		}
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode <= 599 {
			status = responseErr.StatusCode
		}
		writeSystemWorkspaceError(w, status, "Failed unit journal is unavailable.")
		return
	}
	if len(data) > 256*1024 {
		writeSystemWorkspaceError(w, http.StatusBadGateway, "Failed unit journal is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="justvoxel-failed-unit.log"`)
	}
	_, _ = w.Write(data)
}
