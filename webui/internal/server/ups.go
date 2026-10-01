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
	AdminForgetUPSSource(ctx context.Context, session string) (api.UPSStatus, error)
	AdminSetUPSShutdown(ctx context.Context, session string, request api.UPSShutdownRequest) (api.UPSStatus, error)
	AdminSetUPSSharing(ctx context.Context, session string, request api.UPSSharingRequest) (api.UPSStatus, error)
}

func (a *App) registerUPSRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ups", a.upsStatus)
	mux.HandleFunc("POST /api/ups/source", a.upsSourceSave)
	mux.HandleFunc("POST /api/ups/source/forget", a.upsSourceForget)
	mux.HandleFunc("POST /api/ups/shutdown", a.upsShutdownSave)
	mux.HandleFunc("POST /api/ups/sharing", a.upsSharingSave)
}

func (a *App) upsAdminClient(w http.ResponseWriter, r *http.Request) (upsAPI, string, bool) {
	if !a.validCSRF(r) {
		writeUPSWebError(w, http.StatusForbidden, "invalid CSRF token")
		return nil, "", false
	}
	session, identity, ok := a.upsIdentity(w, r)
	if !ok {
		return nil, "", false
	}
	if identity.Role != "administrator" {
		writeUPSWebError(w, http.StatusForbidden, "Administrator access required")
		return nil, "", false
	}
	client, ok := a.api.(upsAPI)
	if !ok {
		writeUPSWebError(w, http.StatusServiceUnavailable, "UPS service unavailable")
		return nil, "", false
	}
	return client, session, true
}

func (a *App) upsShutdownSave(w http.ResponseWriter, r *http.Request) {
	client, session, ok := a.upsAdminClient(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUPSWebError(w, http.StatusBadRequest, "invalid UPS shutdown settings")
		return
	}
	request := api.UPSShutdownRequest{Enabled: r.FormValue("enabled") == "true"}
	if request.Enabled {
		delay, err := strconv.Atoi(strings.TrimSpace(r.FormValue("delay_seconds")))
		if err != nil {
			writeUPSWebError(w, http.StatusBadRequest, "Shutdown delay is invalid")
			return
		}
		request.DelaySeconds = delay
		request.MonitorUsername = strings.TrimSpace(r.FormValue("monitor_username"))
		request.MonitorPassword = r.FormValue("monitor_password")
	}
	status, err := client.AdminSetUPSShutdown(r.Context(), session, request)
	if !a.handleUPSAPIError(w, r, err) {
		return
	}
	writeUPSWebJSON(w, http.StatusOK, status)
}

func (a *App) upsSharingSave(w http.ResponseWriter, r *http.Request) {
	client, session, ok := a.upsAdminClient(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUPSWebError(w, http.StatusBadRequest, "invalid UPS sharing settings")
		return
	}
	request := api.UPSSharingRequest{Enabled: r.FormValue("enabled") == "true"}
	if request.Enabled {
		port, err := strconv.Atoi(strings.TrimSpace(r.FormValue("listen_port")))
		if err != nil {
			writeUPSWebError(w, http.StatusBadRequest, "Sharing port is invalid")
			return
		}
		request.ListenAddress = strings.TrimSpace(r.FormValue("listen_address"))
		request.ListenPort = port
		request.ClientUsername = strings.TrimSpace(r.FormValue("client_username"))
		request.ClientPassword = r.FormValue("client_password")
	}
	status, err := client.AdminSetUPSSharing(r.Context(), session, request)
	if !a.handleUPSAPIError(w, r, err) {
		return
	}
	writeUPSWebJSON(w, http.StatusOK, status)
}

func (a *App) upsSourceForget(w http.ResponseWriter, r *http.Request) {
	client, session, ok := a.upsAdminClient(w, r)
	if !ok {
		return
	}
	status, err := client.AdminForgetUPSSource(r.Context(), session)
	if !a.handleUPSAPIError(w, r, err) {
		return
	}
	writeUPSWebJSON(w, http.StatusOK, status)
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
	client, session, ok := a.upsAdminClient(w, r)
	if !ok {
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

	status, err := client.AdminSetUPSSource(r.Context(), session, api.UPSSource{
		Mode: mode, Host: host, Port: port,
		UPSName:    strings.TrimSpace(r.FormValue("ups_name")),
		Driver:     strings.TrimSpace(r.FormValue("driver")),
		DevicePort: strings.TrimSpace(r.FormValue("device_port")),
	})
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
