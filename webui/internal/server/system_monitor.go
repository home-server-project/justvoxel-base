package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const systemMonitorGlancesURL = "http://127.0.0.1:61208/api/4/all"

type systemMonitorProfileAPI interface {
	SystemMonitorProfile(ctx context.Context, session string) (api.SystemMonitorProfile, error)
	AdminSetSystemMonitorProfile(ctx context.Context, session string, profile api.SystemMonitorProfile) (api.SystemMonitorProfile, error)
}

func (a *App) registerSystemMonitorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system-monitor", a.systemMonitor)
	mux.HandleFunc("GET /api/system-monitor/profile", a.systemMonitorProfile)
	mux.HandleFunc("POST /api/system-monitor/profile", a.systemMonitorProfileSave)
}

func (a *App) systemMonitor(w http.ResponseWriter, r *http.Request) {
	_, _, ok := a.systemMonitorIdentity(w, r)
	if !ok {
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, systemMonitorGlancesURL, nil)
	if err != nil {
		http.Error(w, "system monitor unavailable", http.StatusBadGateway)
		return
	}
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		http.Error(w, "system monitor unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "system monitor unavailable", http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil || !json.Valid(body) {
		http.Error(w, "invalid system monitor response", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *App) systemMonitorProfile(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemMonitorIdentity(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(systemMonitorProfileAPI)
	if !ok {
		http.Error(w, "system monitor profile unavailable", http.StatusServiceUnavailable)
		return
	}
	profile, err := client.SystemMonitorProfile(r.Context(), session)
	if !a.handleSystemMonitorAPIError(w, r, err) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(profile)
}

func (a *App) systemMonitorProfileSave(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	session, identity, ok := a.systemMonitorIdentity(w, r)
	if !ok {
		return
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}

	readBool := func(name string) (bool, error) {
		return strconv.ParseBool(r.FormValue(name))
	}
	system, err := readBool("system")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	cpu, err := readBool("cpu")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	memory, err := readBool("memory")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	load, err := readBool("load")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	filesystem, err := readBool("filesystem")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	diskIO, err := readBool("diskio")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	network, err := readBool("network")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	processes, err := readBool("processes")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	containers, err := readBool("containers")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	sensors, err := readBool("sensors")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	alerts, err := readBool("alerts")
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}
	processCount, err := strconv.Atoi(r.FormValue("process_count"))
	if err != nil {
		http.Error(w, "invalid system monitor profile", http.StatusBadRequest)
		return
	}

	client, ok := a.api.(systemMonitorProfileAPI)
	if !ok {
		http.Error(w, "system monitor profile unavailable", http.StatusServiceUnavailable)
		return
	}
	profile, err := client.AdminSetSystemMonitorProfile(r.Context(), session, api.SystemMonitorProfile{
		System: system, CPU: cpu, Memory: memory, Load: load, Filesystem: filesystem,
		DiskIO: diskIO, Network: network, Processes: processes, Containers: containers,
		Sensors: sensors, Alerts: alerts, ProcessCount: processCount,
	})
	if !a.handleSystemMonitorAPIError(w, r, err) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(profile)
}

func (a *App) systemMonitorIdentity(w http.ResponseWriter, r *http.Request) (string, api.SessionInfo, bool) {
	if mustChange(r) {
		http.Error(w, "password change required", http.StatusForbidden)
		return "", api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return "", api.SessionInfo{}, false
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		http.Error(w, "session service unavailable", http.StatusServiceUnavailable)
		return "", api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if !a.handleSystemMonitorAPIError(w, r, err) {
		return "", api.SessionInfo{}, false
	}
	switch identity.Role {
	case "administrator", "operator", "viewer":
		return session, identity, true
	default:
		http.Error(w, "system monitor access denied", http.StatusForbidden)
		return "", api.SessionInfo{}, false
	}
}

func (a *App) handleSystemMonitorAPIError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return false
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Error(w, "password change required", http.StatusForbidden)
		return false
	}
	http.Error(w, "system monitor service unavailable", http.StatusBadGateway)
	return false
}
