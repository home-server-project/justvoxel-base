package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type adminDateTimeState struct {
	Timezone string `json:"timezone"`
	LocalDate string `json:"local_date"`
	LocalTime string `json:"local_time"`
	Automatic bool `json:"automatic"`
	Synchronized bool `json:"synchronized"`
}

type adminDateTimeChange struct {
	Timezone string `json:"timezone"`
	Automatic bool `json:"automatic"`
	Date string `json:"date,omitempty"`
	Time string `json:"time,omitempty"`
}

var dateTimeZonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+.-]*(/[A-Za-z0-9_+.-]+)*$`)
var runTimedatectl = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/bin/timedatectl", args...).CombinedOutput()
}

func registerAdminDateTimeRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/date-time", s.adminDateTimeRead)
	mux.HandleFunc("POST /v1/admin/date-time", s.adminDateTimeApply)
}

func validSystemTimezone(zone string) bool {
	if zone == "UTC" { return true }
	if len(zone) > 128 || !dateTimeZonePattern.MatchString(zone) || strings.Contains(zone, "..") { return false }
	path := filepath.Join("/usr/share/zoneinfo", zone)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() { return false }
	_, err = time.LoadLocation(zone)
	return err == nil
}

func systemDateTime(ctx context.Context) (adminDateTimeState, error) {
	output, err := runTimedatectl(ctx, "show", "--property=Timezone", "--property=NTP", "--property=NTPSynchronized")
	if err != nil { return adminDateTimeState{}, err }
	state := adminDateTimeState{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok { continue }
		switch key {
		case "Timezone": state.Timezone = value
		case "NTP": state.Automatic = value == "yes"
		case "NTPSynchronized": state.Synchronized = value == "yes"
		}
	}
	if state.Timezone == "" { return state, errors.New("host timezone is unavailable") }
	location, err := time.LoadLocation(state.Timezone)
	if err != nil { return state, err }
	now := time.Now().In(location)
	state.LocalDate, state.LocalTime = now.Format("2006-01-02"), now.Format("15:04")
	return state, nil
}

func (s *server) adminDateTimeRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok { return }
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	state, err := systemDateTime(ctx)
	if err != nil { writeError(w, http.StatusServiceUnavailable, "System date and time are unavailable"); return }
	writeJSON(w, http.StatusOK, state)
}

func (s *server) adminDateTimeApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok { return }
	var request adminDateTimeChange
	if !decodeJSON(w, r, &request) { return }
	if !validSystemTimezone(request.Timezone) {
		writeError(w, http.StatusBadRequest, "Choose a timezone from the system list")
		return
	}
	var manual string
	if request.Automatic {
		if request.Date != "" || request.Time != "" { writeError(w, http.StatusBadRequest, "Manual time requires automatic time to be off"); return }
	} else if request.Date != "" || request.Time != "" {
		if len(request.Date) != 10 || len(request.Time) != 5 {
			writeError(w, http.StatusBadRequest, "Enter a valid date and time")
			return
		}
		parsed, err := time.Parse("2006-01-02 15:04", request.Date+" "+request.Time)
		if err != nil || parsed.Format("2006-01-02 15:04") != request.Date+" "+request.Time {
			writeError(w, http.StatusBadRequest, "Enter a valid date and time")
			return
		}
		manual = request.Date+" "+request.Time+":00"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	current, err := systemDateTime(ctx)
	if err != nil { writeError(w, http.StatusServiceUnavailable, "System date and time are unavailable"); return }
	if current.Timezone != request.Timezone {
		if _, err := runTimedatectl(ctx, "set-timezone", request.Timezone); err != nil { writeError(w, http.StatusServiceUnavailable, "Could not change timezone"); return }
	}
	if current.Automatic != request.Automatic {
		value := "false"
		if request.Automatic { value = "true" }
		if _, err := runTimedatectl(ctx, "set-ntp", value); err != nil { writeError(w, http.StatusServiceUnavailable, "Could not change automatic time"); return }
	}
	if manual != "" {
		if _, err := runTimedatectl(ctx, "set-time", manual); err != nil { writeError(w, http.StatusServiceUnavailable, "Could not set system time"); return }
	}
	state, err := systemDateTime(ctx)
	if err != nil { writeError(w, http.StatusServiceUnavailable, "System date and time could not be read back"); return }
	_ = s.store.recordAuditEvent(actor, "change_system_date_time", "system clock", true, "System date and time settings changed")
	writeJSON(w, http.StatusOK, state)
}
