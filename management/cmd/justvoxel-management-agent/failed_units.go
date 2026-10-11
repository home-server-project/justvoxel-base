package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

const failedUnitCountLimit = 100
const failedUnitOutputLimit = 256 * 1024
const failedUnitCommandTimeout = 6 * time.Second

// A unit identity is never decoded into a path. Wildcards are excluded even
// though fresh exact membership is also required. Instances may contain @.
var failedUnitNamePattern = regexp.MustCompile(`^(?:[A-Za-z0-9_:.-]|\\x[0-9A-Fa-f]{2})+(?:@(?:[A-Za-z0-9_:.@-]|\\x[0-9A-Fa-f]{2})*)?\.(?:service|timer|mount|socket|path|target|automount|swap|device|scope|slice)$`)

func validFailedUnitName(unit string) bool {
	return len(unit) <= 255 && failedUnitNamePattern.MatchString(unit)
}

type failedUnit struct {
	Unit        string `json:"unit"`
	Description string `json:"description"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
}

// Drain command stdout while keeping a fixed memory bound. Overflow is an
// unavailable response, never a successful truncated enumeration or journal.
type failedUnitOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *failedUnitOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := failedUnitOutputLimit - b.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

var runFailedUnitCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	var output failedUnitOutput
	command.Stdout = &output
	// Stderr is deliberately discarded, never returned to the browser.
	if err := command.Run(); err != nil {
		return nil, err
	}
	if output.overflow {
		return nil, errors.New("failed unit output exceeds limit")
	}
	return output.Bytes(), nil
}

func enumerateFailedUnits(ctx context.Context) ([]failedUnit, error) {
	ctx, cancel := context.WithTimeout(ctx, failedUnitCommandTimeout)
	defer cancel()
	data, err := runFailedUnitCommand(ctx, "systemctl", "list-units", "--failed", "--all", "--no-pager", "--output=json")
	if err != nil {
		return nil, err
	}
	if len(data) > failedUnitOutputLimit {
		return nil, errors.New("failed unit output exceeds limit")
	}
	var rows []struct {
		Unit        string `json:"unit"`
		Description string `json:"description"`
		Active      string `json:"active"`
		Sub         string `json:"sub"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	// systemctl emits [] when no units failed. Null is not a valid enumeration.
	if rows == nil {
		return nil, errors.New("invalid failed unit enumeration")
	}
	units := make([]failedUnit, 0)
	seen := make(map[string]bool)
	for _, row := range rows {
		if !validFailedUnitName(row.Unit) || row.Active != "failed" || len(row.Description) > 4096 || len(row.Sub) > 64 {
			return nil, errors.New("invalid failed unit enumeration")
		}
		if !seen[row.Unit] && len(units) < failedUnitCountLimit {
			units = append(units, failedUnit{row.Unit, row.Description, row.Active, row.Sub})
			seen[row.Unit] = true
		}
	}
	return units, nil
}

func registerAdminFailedUnitRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/systemd/failed", s.adminFailedUnits)
	mux.HandleFunc("GET /v1/admin/systemd/failed/{unit}/journal", s.adminFailedUnitJournal)
}

func (s *server) adminFailedUnits(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), failedUnitCommandTimeout)
	defer cancel()
	units, err := enumerateFailedUnits(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Failed systemd units are unavailable.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"failed_units": units})
}

func (s *server) adminFailedUnitJournal(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	unit := r.PathValue("unit")
	if !validFailedUnitName(unit) {
		writeError(w, http.StatusBadRequest, "Invalid failed unit identifier.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), failedUnitCommandTimeout)
	defer cancel()
	units, err := enumerateFailedUnits(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Failed systemd units are unavailable.")
		return
	}
	verified := false
	for _, entry := range units {
		if entry.Unit == unit {
			verified = true
			break
		}
	}
	if !verified {
		writeError(w, http.StatusNotFound, "This unit is no longer in the failed unit list.")
		return
	}
	data, err := runFailedUnitCommand(ctx, "journalctl", "-b", "-u", unit, "--no-pager", "-n", strconv.Itoa(boundedQueryLimit(r, 60, 200)), "-o", "short-iso")
	if err != nil || len(data) > failedUnitOutputLimit {
		writeError(w, http.StatusServiceUnavailable, "Failed unit journal is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
