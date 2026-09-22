package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

const adminBackupDeleteHelper = "/usr/libexec/justvoxel/mjust/admin-backup-delete-json"

type adminBackupDeleteRequest struct {
	BackupIDs    []string `json:"backup_ids"`
	Confirmation string   `json:"confirmation,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
}

type adminBackupDeleteItem struct {
	ID        string `json:"id"`
	SizeBytes uint64 `json:"size_bytes"`
}

type adminBackupDeletePlan struct {
	Backups        []adminBackupDeleteItem `json:"backups"`
	Count          int                     `json:"count"`
	TotalSizeBytes uint64                  `json:"total_size_bytes"`
	Confirmation   string                  `json:"confirmation"`
	Fingerprint    string                  `json:"fingerprint"`
}

type adminBackupDeleteResponse struct {
	OK       bool                  `json:"ok"`
	Error    string                `json:"error,omitempty"`
	Warnings []string              `json:"warnings"`
	Proposed adminBackupDeletePlan `json:"proposed"`
	Deleted  int                   `json:"deleted,omitempty"`
	Applied  bool                  `json:"applied"`
}

var runAdminBackupDeleteHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminBackupDeleteHelper, action)
	cmd.Stdin = bytes.NewReader(request)
	return cmd.CombinedOutput()
}

func registerAdminBackupDeleteRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/backups/delete/plan", s.adminBackupDeletePlan)
	mux.HandleFunc("POST /v1/admin/backups/delete/apply", s.adminBackupDeleteApply)
}

func (s *server) adminBackupDeletePlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.runAdminBackupDelete(w, r, "plan", session{})
}

func (s *server) adminBackupDeleteApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	s.runAdminBackupDelete(w, r, "apply", actor)
}

func (s *server) runAdminBackupDelete(w http.ResponseWriter, r *http.Request, action string, actor session) {
	var request adminBackupDeleteRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.BackupIDs) < 1 || len(request.BackupIDs) > 100 {
		writeError(w, http.StatusBadRequest, "choose between 1 and 100 backups")
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup delete request could not be prepared")
		return
	}

	timeout := 20 * time.Second
	if action == "apply" {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	output, err := runAdminBackupDeleteHelper(ctx, action, payload)
	if err != nil {
		if action == "apply" && s.store != nil {
			_ = s.store.recordAuditEvent(actor, "backup_delete", "backup-library", false, "backup delete helper failed")
		}
		writeError(w, http.StatusServiceUnavailable, "backup deletion is unavailable")
		return
	}

	var result adminBackupDeleteResponse
	if err := json.Unmarshal(output, &result); err != nil {
		writeError(w, http.StatusInternalServerError, "backup deletion returned invalid data")
		return
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	if !result.OK {
		if result.Error == "" {
			result.Error = "backup deletion could not be validated"
		}
		if action == "apply" && s.store != nil {
			_ = s.store.recordAuditEvent(actor, "backup_delete", "backup-library", false, result.Error)
		}
		writeJSON(w, http.StatusBadRequest, result)
		return
	}
	if action == "apply" && s.store != nil {
		_ = s.store.recordAuditEvent(actor, "backup_delete", "backup-library", true, "deleted backups from backup library")
	}
	writeJSON(w, http.StatusOK, result)
}
