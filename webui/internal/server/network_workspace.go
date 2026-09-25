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

type networkAPI interface {
	NetworkStatus(ctx context.Context, session string) (api.NetworkStatus, error)
	WiFiNetworks(ctx context.Context, session, interfaceName string) (api.WiFiNetworksResponse, error)
	RequestWiFiScan(ctx context.Context, session, interfaceName string) error
	CreateNetworkCheckpoint(ctx context.Context, session string, interfaces []string, timeout uint32) (api.NetworkCheckpoint, error)
	NetworkCheckpoint(ctx context.Context, session, id string) (api.NetworkCheckpoint, error)
	ConfirmNetworkCheckpoint(ctx context.Context, session, id string) error
	RollbackNetworkCheckpoint(ctx context.Context, session, id string) (api.NetworkCheckpointRollback, error)
}

func (a *App) registerNetworkWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network", a.networkStatus)
	mux.HandleFunc("GET /api/network/wifi/{interface}/networks", a.networkWiFiNetworks)
	mux.HandleFunc("POST /api/network/wifi/{interface}/scan", a.networkWiFiScan)
	mux.HandleFunc("POST /api/network/checkpoints", a.networkCheckpointCreate)
	mux.HandleFunc("GET /api/network/checkpoints/{id}", a.networkCheckpointStatus)
	mux.HandleFunc("POST /api/network/checkpoints/{id}/confirm", a.networkCheckpointConfirm)
	mux.HandleFunc("POST /api/network/checkpoints/{id}/rollback", a.networkCheckpointRollback)
}

func (a *App) networkStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := a.networkSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	status, err := client.NetworkStatus(r.Context(), session)
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, status)
}

func (a *App) networkWiFiNetworks(w http.ResponseWriter, r *http.Request) {
	session, ok := a.networkSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	networks, err := client.WiFiNetworks(r.Context(), session, r.PathValue("interface"))
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, networks)
}

func (a *App) networkWiFiScan(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	if err := client.RequestWiFiScan(r.Context(), session, r.PathValue("interface")); !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (a *App) networkCheckpointCreate(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeNetworkWebError(w, http.StatusBadRequest, "invalid network checkpoint request")
		return
	}

	interfaces := append([]string(nil), r.Form["interface"]...)
	if len(interfaces) == 0 {
		for _, value := range strings.Split(r.FormValue("interfaces"), ",") {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				interfaces = append(interfaces, trimmed)
			}
		}
	}
	var timeout uint32
	if raw := strings.TrimSpace(r.FormValue("rollback_timeout_seconds")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			writeNetworkWebError(w, http.StatusBadRequest, "invalid rollback timeout")
			return
		}
		timeout = uint32(parsed)
	}

	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	checkpoint, err := client.CreateNetworkCheckpoint(r.Context(), session, interfaces, timeout)
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusCreated, checkpoint)
}

func (a *App) networkCheckpointStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	checkpoint, err := client.NetworkCheckpoint(r.Context(), session, r.PathValue("id"))
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, checkpoint)
}

func (a *App) networkCheckpointConfirm(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	if err := client.ConfirmNetworkCheckpoint(r.Context(), session, r.PathValue("id")); !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"id":     r.PathValue("id"),
		"status": "confirmed",
	})
}

func (a *App) networkCheckpointRollback(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	result, err := client.RollbackNetworkCheckpoint(r.Context(), session, r.PathValue("id"))
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, result)
}

func (a *App) networkAdministratorSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	session, ok := a.networkSession(w, r)
	if !ok {
		return "", false
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Session service unavailable")
		return "", false
	}
	identity, err := client.Session(r.Context(), session)
	if !a.handleNetworkAPIError(w, err) {
		return "", false
	}
	if identity.Role != "administrator" {
		writeNetworkWebError(w, http.StatusForbidden, "Administrator access required")
		return "", false
	}
	return session, true
}

func (a *App) networkSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	if mustChange(r) {
		writeNetworkWebError(w, http.StatusForbidden, "password change required")
		return "", false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		writeNetworkWebError(w, http.StatusUnauthorized, "authentication required")
		return "", false
	}
	return session, true
}

func (a *App) handleNetworkAPIError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, api.ErrUnauthorized) {
		writeNetworkWebError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		writeNetworkWebError(w, http.StatusForbidden, "password change required")
		return false
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) {
		status := responseErr.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		message := responseErr.Message
		if strings.TrimSpace(message) == "" {
			message = "Network service request failed"
		}
		writeNetworkWebError(w, status, message)
		return false
	}
	writeNetworkWebError(w, http.StatusBadGateway, "Network service unavailable")
	return false
}

func writeNetworkWebJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeNetworkWebError(w http.ResponseWriter, status int, message string) {
	writeNetworkWebJSON(w, status, map[string]string{"error": message})
}
