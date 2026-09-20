package main

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"time"
)

const adminValidationHelper = "/usr/libexec/justvoxel/mjust/validate-backend"

var runAdminValidationHelper = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, adminValidationHelper).CombinedOutput()
}

type adminValidationResponse struct {
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

func registerAdminValidationRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/validation", s.adminValidation)
}

func (s *server) adminValidation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	output, err := runAdminValidationHelper(ctx)
	if err == nil {
		writeJSON(w, http.StatusOK, adminValidationResponse{
			OK: true, Output: string(output), ExitCode: 0,
		})
		return
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		writeJSON(w, http.StatusOK, adminValidationResponse{
			OK: false, Output: string(output), ExitCode: 1,
		})
		return
	}

	writeError(w, http.StatusServiceUnavailable, "JustVoxel validation is unavailable")
}
