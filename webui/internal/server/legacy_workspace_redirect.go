package server

import (
	"net/http"
	"net/url"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// legacyWorkspaceRedirect keeps bookmarked page URLs usable without rendering a
// second product surface. Action and progress endpoints remain registered separately.
func (a *App) legacyWorkspaceRedirect(role, workspace, tab string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mustChange(r) {
			http.Redirect(w, r, "/password", http.StatusSeeOther)
			return
		}
		identity, ok := a.workspaceRedirectIdentity(w, r)
		if !ok {
			return
		}
		if role == "administrator" && identity.Role != "administrator" ||
			role == "operator" && identity.Role != "administrator" && identity.Role != "operator" {
			http.Error(w, "access required for this workspace", http.StatusForbidden)
			return
		}
		query := url.Values{"workspace": {workspace}}
		if tab != "" {
			query.Set("tab", tab)
		}
		for _, key := range []string{"result", "count", "restore_operation", "memory_restart", "next_start", "restart"} {
			if value := r.URL.Query().Get(key); value != "" {
				query.Set(key, value)
			}
		}
		http.Redirect(w, r, "/?"+query.Encode(), http.StatusSeeOther)
	}
}

func (a *App) workspaceRedirectIdentity(w http.ResponseWriter, r *http.Request) (api.SessionInfo, bool) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return api.SessionInfo{}, false
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		http.Error(w, "session information is unavailable", http.StatusServiceUnavailable)
		return api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleRolePageError(w, r, err)
		return api.SessionInfo{}, false
	}
	return identity, true
}
