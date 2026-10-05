package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// Plus keeps the inherited implementation available in source, but does not
// expose normal JustVoxel game operations through the host interface.
func (a *App) plusRoutes(next http.Handler) http.Handler {
	if !a.config.Plus {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" && r.Method == http.MethodGet {
			_, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
			if !ok {
				return
			}
			a.render(w, "plus-setup-pending.html", pageData{Title: "JustVoxel Plus setup", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrfFromRequest(r)})
			return
		}
		for _, prefix := range []string{
			"/minecraft", "/api/minecraft", "/api/version", "/operations",
			"/setup", "/api/setup", "/settings/server", "/settings/server-migration", "/settings/restore",
			"/settings/data-migration", "/settings/backup-storage", "/settings/new-backups",
			"/api/data-migration", "/api/server-migration", "/api/restore",
			"/workspace/backups", "/workspace/migration", "/api/workspace/migration", "/api/new-backups",
			"/api/new-storage/backup-partition", "/api/new-storage/minecraft-data", "/api/new-storage/whole-disk",
			"/settings/storage-provision",
			"/api/system/workspace/reset", "/factory-reset-complete",
		} {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.NotFound(w, r)
				return
			}
		}
		if strings.Contains(r.URL.Path, "-allowance/reset") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type plusHostLogsAPI interface {
	PlusHostLogs(context.Context, string, string) (api.PlusHostLogs, error)
}

func (a *App) plusHostLogs(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(plusHostLogsAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Host logs are unavailable.")
		return
	}
	logs, err := client.PlusHostLogs(r.Context(), session, r.URL.Query().Get("service"))
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Host logs are unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, logs)
}
