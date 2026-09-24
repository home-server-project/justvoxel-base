package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type systemMonitorProfile struct {
	System       bool `json:"system"`
	CPU          bool `json:"cpu"`
	Memory       bool `json:"memory"`
	Load         bool `json:"load"`
	Filesystem   bool `json:"filesystem"`
	DiskIO       bool `json:"diskio"`
	Network      bool `json:"network"`
	Processes    bool `json:"processes"`
	Containers   bool `json:"containers"`
	Sensors      bool `json:"sensors"`
	Alerts       bool `json:"alerts"`
	ProcessCount int  `json:"process_count"`
}

func defaultSystemMonitorProfile() systemMonitorProfile {
	return systemMonitorProfile{
		System: true, CPU: true, Memory: true, Load: true, Filesystem: true,
		DiskIO: true, Network: true, Processes: true, Containers: true, Sensors: true, Alerts: true,
		ProcessCount: 10,
	}
}

func validSystemMonitorProfile(profile systemMonitorProfile) bool {
	switch profile.ProcessCount {
	case 5, 10, 20:
		return true
	default:
		return false
	}
}

func (s *webUIStore) readSystemMonitorProfile() (systemMonitorProfile, error) {
	var payload string
	if err := s.db.QueryRow(`SELECT profile_json FROM system_monitor_profile WHERE id = 1`).Scan(&payload); err != nil {
		return systemMonitorProfile{}, fmt.Errorf("read system monitor profile: %w", err)
	}
	var profile systemMonitorProfile
	if err := json.Unmarshal([]byte(payload), &profile); err != nil {
		return systemMonitorProfile{}, fmt.Errorf("decode system monitor profile: %w", err)
	}
	if !validSystemMonitorProfile(profile) {
		return systemMonitorProfile{}, errors.New("stored system monitor profile is invalid")
	}
	return profile, nil
}

func (s *webUIStore) writeSystemMonitorProfile(profile systemMonitorProfile) error {
	if !validSystemMonitorProfile(profile) {
		return errors.New("system monitor profile is invalid")
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("encode system monitor profile: %w", err)
	}
	result, err := s.db.Exec(
		`UPDATE system_monitor_profile SET profile_json = ?, updated_at = ? WHERE id = 1`,
		string(payload),
		formatWebUIStoreTime(s.now()),
	)
	if err != nil {
		return fmt.Errorf("write system monitor profile: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read system monitor profile update result: %w", err)
	}
	if count != 1 {
		return errors.New("system monitor profile row is missing")
	}
	return nil
}

func registerSystemMonitorProfileRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/system-monitor/profile", s.systemMonitorProfileGet)
	mux.HandleFunc("POST /v1/admin/system-monitor/profile", s.systemMonitorProfileSet)
}

func (s *server) systemMonitorProfileGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	profile, err := s.store.readSystemMonitorProfile()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "system monitor profile is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *server) systemMonitorProfileSet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var profile systemMonitorProfile
	if !decodeJSON(w, r, &profile) {
		return
	}
	if !validSystemMonitorProfile(profile) {
		writeError(w, http.StatusBadRequest, "process_count must be 5, 10, or 20")
		return
	}
	if err := s.store.writeSystemMonitorProfile(profile); err != nil {
		writeError(w, http.StatusServiceUnavailable, "system monitor profile could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}
