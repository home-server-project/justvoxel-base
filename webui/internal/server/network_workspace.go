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
	SetWiFiRadio(ctx context.Context, session string, enabled bool, checkpointID string) (api.NetworkWiFiMutation, error)
	ConnectWiFi(ctx context.Context, session, interfaceName string, request api.NetworkWiFiConnectRequest) (api.NetworkWiFiMutation, error)
	DisconnectWiFi(ctx context.Context, session, interfaceName, checkpointID string) (api.NetworkWiFiMutation, error)
	ForgetWiFiProfile(ctx context.Context, session, profileUUID string) (api.NetworkWiFiMutation, error)
}

func (a *App) registerNetworkWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network", a.networkStatus)
	mux.HandleFunc("GET /api/network/remote-access/playit/setup", a.networkPlayitSetup)
	mux.HandleFunc("POST /api/network/remote-access/playit/setup", a.networkPlayitSetup)
	mux.HandleFunc("GET /api/network/remote-access", a.networkRemoteAccessStatus)
	mux.HandleFunc("POST /api/network/remote-access/{provider}", a.networkConfigurationChange)
	mux.HandleFunc("POST /api/network/ethernet/{interface}", a.networkConfigurationChange)
	mux.HandleFunc("POST /api/network/reconnect/{interface}", a.networkConfigurationChange)
	mux.HandleFunc("POST /api/network/connectivity-check", a.networkConfigurationChange)
	mux.HandleFunc("GET /api/network/wifi/{interface}/networks", a.networkWiFiNetworks)
	mux.HandleFunc("POST /api/network/wifi/{interface}/scan", a.networkWiFiScan)
	mux.HandleFunc("POST /api/network/checkpoints", a.networkCheckpointCreate)
	mux.HandleFunc("GET /api/network/checkpoints/{id}", a.networkCheckpointStatus)
	mux.HandleFunc("POST /api/network/checkpoints/{id}/confirm", a.networkCheckpointConfirm)
	mux.HandleFunc("POST /api/network/checkpoints/{id}/rollback", a.networkCheckpointRollback)
	mux.HandleFunc("POST /api/network/wifi/radio", a.networkWiFiRadioChange)
	mux.HandleFunc("POST /api/network/wifi/{interface}/connect", a.networkWiFiConnectChange)
	mux.HandleFunc("POST /api/network/wifi/{interface}/disconnect", a.networkWiFiDisconnectChange)
	mux.HandleFunc("POST /api/network/wifi/profiles/{uuid}/forget", a.networkWiFiForgetChange)
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
	identityClient, ok := a.api.(sessionIdentityAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Session service unavailable")
		return
	}
	identity, err := identityClient.Session(r.Context(), session)
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	status.WorkspaceTabs = networkWorkspaceTabs(identity.Role)
	if identity.Role != "administrator" {
		status.PendingCheckpoints = nil
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

func (a *App) networkWiFiRadioChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeNetworkWebError(w, http.StatusBadRequest, "invalid Wi-Fi request")
		return
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(r.FormValue("enabled")))
	if err != nil {
		writeNetworkWebError(w, http.StatusBadRequest, "invalid Wi-Fi radio state")
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	result, err := client.SetWiFiRadio(r.Context(), session, enabled, strings.TrimSpace(r.FormValue("checkpoint_id")))
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, result)
}

func (a *App) networkWiFiConnectChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeNetworkWebError(w, http.StatusBadRequest, "invalid Wi-Fi request")
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	request := api.NetworkWiFiConnectRequest{
		CheckpointID:  strings.TrimSpace(r.FormValue("checkpoint_id")),
		ProfileUUID:   strings.TrimSpace(r.FormValue("profile_uuid")),
		SSID:          r.FormValue("ssid"),
		BSSID:         strings.TrimSpace(r.FormValue("bssid")),
		KeyManagement: strings.TrimSpace(r.FormValue("key_management")),
		Password:      r.FormValue("password"),
		Hidden:        r.FormValue("hidden") == "true",
	}
	result, err := client.ConnectWiFi(r.Context(), session, r.PathValue("interface"), request)
	request.Password = ""
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, result)
}

func (a *App) networkWiFiDisconnectChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeNetworkWebError(w, http.StatusBadRequest, "invalid Wi-Fi request")
		return
	}
	client, ok := a.api.(networkAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	result, err := client.DisconnectWiFi(
		r.Context(),
		session,
		r.PathValue("interface"),
		strings.TrimSpace(r.FormValue("checkpoint_id")),
	)
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, result)
}

func (a *App) networkWiFiForgetChange(w http.ResponseWriter, r *http.Request) {
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
	result, err := client.ForgetWiFiProfile(r.Context(), session, r.PathValue("uuid"))
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

// Extensions use the existing API client and Unix-socket transport.
type networkConfigurationAPI interface {
	RemoteAccessStatus(context.Context, string) (api.RemoteAccessStatus, error)
	ChangeRemoteAccess(context.Context, string, string, string) (api.RemoteAccessChange, error)
	ConfigureEthernet(context.Context, string, string, api.EthernetSettings) (api.NetworkWiFiMutation, error)
	CheckNetworkConnectivity(context.Context, string) error
	ReconnectNetwork(context.Context, string, string, string, string) (api.NetworkWiFiMutation, error)
}

func (a *App) networkRemoteAccessStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := a.networkSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkConfigurationAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	status, err := client.RemoteAccessStatus(r.Context(), session)
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, status)
}

func (a *App) networkConfigurationChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeNetworkWebError(w, http.StatusBadRequest, "invalid network request")
		return
	}
	client, ok := a.api.(networkConfigurationAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Network service unavailable")
		return
	}
	var result any = map[string]bool{"ok": true}
	var err error
	switch {
	case r.PathValue("provider") != "":
		result, err = client.ChangeRemoteAccess(r.Context(), session, r.PathValue("provider"), r.FormValue("action"))
	case strings.HasPrefix(r.URL.Path, "/api/network/ethernet/"):
		prefix, prefixErr := strconv.ParseUint(r.FormValue("prefix"), 10, 32)
		mtu, mtuErr := strconv.ParseUint(r.FormValue("mtu"), 10, 32)
		autoconnect, autoErr := strconv.ParseBool(r.FormValue("autoconnect"))
		if prefixErr != nil || mtuErr != nil || autoErr != nil {
			writeNetworkWebError(w, http.StatusBadRequest, "invalid Ethernet numeric or autoconnect value")
			return
		}
		settings := api.EthernetSettings{
			CheckpointID: r.FormValue("checkpoint_id"), ProfileUUID: r.FormValue("profile_uuid"), Method: r.FormValue("method"),
			Address: r.FormValue("address"), Prefix: uint32(prefix), Gateway: r.FormValue("gateway"),
			DNS: strings.FieldsFunc(r.FormValue("dns"), func(c rune) bool { return c == ',' || c == ' ' || c == '\n' || c == '\t' }),
			MTU: uint32(mtu), Autoconnect: autoconnect,
		}
		result, err = client.ConfigureEthernet(r.Context(), session, r.PathValue("interface"), settings)
	case strings.HasPrefix(r.URL.Path, "/api/network/reconnect/"):
		result, err = client.ReconnectNetwork(r.Context(), session, r.PathValue("interface"), r.FormValue("profile_uuid"), r.FormValue("checkpoint_id"))
	default:
		err = client.CheckNetworkConnectivity(r.Context(), session)
	}
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, result)
}

func networkWorkspaceTabs(role string) []string {
	if role == "administrator" {
		return []string{"Overview", "Ethernet", "Wi-Fi", "Troubleshoot", "Remote Access"}
	}
	return []string{"Overview"}
}

type networkPlayitSetupAPI interface {
	PlayitSetup(context.Context, string, bool) (api.PlayitSetupState, error)
}

func (a *App) networkPlayitSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && !a.validCSRF(r) {
		writeNetworkWebError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, ok := a.networkAdministratorSession(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(networkPlayitSetupAPI)
	if !ok {
		writeNetworkWebError(w, http.StatusServiceUnavailable, "Playit setup unavailable")
		return
	}
	status, err := client.PlayitSetup(r.Context(), session, r.Method == http.MethodPost)
	if !a.handleNetworkAPIError(w, err) {
		return
	}
	writeNetworkWebJSON(w, http.StatusOK, status)
}
