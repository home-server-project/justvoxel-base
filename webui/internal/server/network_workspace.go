package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type networkAPI interface {
	NetworkStatus(ctx context.Context, session string) (api.NetworkStatus, error)
	WiFiNetworks(ctx context.Context, session, interfaceName string) (api.WiFiNetworksResponse, error)
	RequestWiFiScan(ctx context.Context, session, interfaceName string) error
}

func (a *App) registerNetworkWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network", a.networkStatus)
	mux.HandleFunc("GET /api/network/wifi/{interface}/networks", a.networkWiFiNetworks)
	mux.HandleFunc("POST /api/network/wifi/{interface}/scan", a.networkWiFiScan)
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
	if message, ok := api.ErrorMessage(err); ok {
		writeNetworkWebError(w, http.StatusBadRequest, message)
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
