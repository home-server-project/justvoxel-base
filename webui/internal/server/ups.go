package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type upsAPI interface {
	UPSStatus(ctx context.Context, session string) (api.UPSStatus, error)
	AdminSetUPSSource(ctx context.Context, session string, source api.UPSSource) (api.UPSStatus, error)
}

func (a *App) registerUPSRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ups", a.upsStatus)
	mux.HandleFunc("POST /api/ups/source", a.upsSourceSave)
}

func (a *App) upsStatus(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.upsIdentity(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(upsAPI)
	if !ok {
		writeUPSWebError(w, http.StatusServiceUnavailable, "UPS service unavailable")
		return
	}
	status, err := client.UPSStatus(r.Context(), session)
	if !a.handleUPSAPIError(w, r, err) {
		return
	}
	writeUPSWebJSON(w, http.StatusOK, status)
}

func (a *App) upsSourceSave(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeUPSWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, identity, ok := a.upsIdentity(w, r)
	if !ok {
		return
	}
	if identity.Role != "administrator" {
		writeUPSWebError(w, http.StatusForbidden, "Administrator access required")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUPSWebError(w, http.StatusBadRequest, "invalid UPS source")
		return
	}
	mode := strings.TrimSpace(r.FormValue("mode"))
	host := strings.TrimSpace(r.FormValue("host"))
	port := 0
	if raw := strings.TrimSpace(r.FormValue("port")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeUPSWebError(w, http.StatusBadRequest, "Remote NUT port is invalid")
			return
		}
		port = parsed
	}

	client, ok := a.api.(upsAPI)
	if !ok {
		writeUPSWebError(w, http.StatusServiceUnavailable, "UPS service unavailable")
		return
	}
	status, err := client.AdminSetUPSSource(r.Context(), session, api.UPSSource{Mode: mode, Host: host, Port: port})
	if !a.handleUPSAPIError(w, r, err) {
		return
	}
	writeUPSWebJSON(w, http.StatusOK, status)
}

func (a *App) upsIdentity(w http.ResponseWriter, r *http.Request) (string, api.SessionInfo, bool) {
	if mustChange(r) {
		writeUPSWebError(w, http.StatusForbidden, "password change required")
		return "", api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		writeUPSWebError(w, http.StatusUnauthorized, "authentication required")
		return "", api.SessionInfo{}, false
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		writeUPSWebError(w, http.StatusServiceUnavailable, "session service unavailable")
		return "", api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if !a.handleUPSAPIError(w, r, err) {
		return "", api.SessionInfo{}, false
	}
	switch identity.Role {
	case "administrator", "operator", "viewer":
		return session, identity, true
	default:
		writeUPSWebError(w, http.StatusForbidden, "UPS monitoring access denied")
		return "", api.SessionInfo{}, false
	}
}

func (a *App) handleUPSAPIError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		writeUPSWebError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		writeUPSWebError(w, http.StatusForbidden, "password change required")
		return false
	}
	if message, ok := api.ErrorMessage(err); ok {
		writeUPSWebError(w, http.StatusBadRequest, message)
		return false
	}
	writeUPSWebError(w, http.StatusBadGateway, "UPS service unavailable")
	return false
}

func writeUPSWebJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeUPSWebError(w http.ResponseWriter, status int, message string) {
	writeUPSWebJSON(w, status, map[string]string{"error": message})
}
