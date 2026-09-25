package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

const adminStorageActionsHelper = "/usr/libexec/justvoxel/mjust/admin-storage-actions-json"

type adminStorageActionRequest struct {
	Operation    string `json:"operation"`
	Device       string `json:"device"`
	MountPoint   string `json:"mount_point,omitempty"`
	SizeGiB      string `json:"size_gib,omitempty"`
	FreeStart    string `json:"free_start,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
}

type adminStorageActionPlan struct {
	Operation         string `json:"operation"`
	Device            string `json:"device"`
	Filesystem        string `json:"filesystem,omitempty"`
	TargetFilesystem  string `json:"target_filesystem,omitempty"`
	UUID              string `json:"uuid,omitempty"`
	MountPoint        string `json:"mount_point,omitempty"`
	CurrentMountPoint string `json:"current_mount_point,omitempty"`
	Role              string `json:"role,omitempty"`
	Confirmation      string `json:"confirmation,omitempty"`
	Fingerprint       string `json:"fingerprint"`
	SizeGiB           string `json:"size_gib,omitempty"`
	FreeStart         string `json:"free_start,omitempty"`
	FreeEnd           string `json:"free_end,omitempty"`
	PlannedEnd        string `json:"planned_end,omitempty"`
	CreatedDevice     string `json:"created_device,omitempty"`
	SizeBytes         uint64 `json:"size_bytes,omitempty"`
	Destructive       bool   `json:"destructive,omitempty"`
}

type adminStorageActionResponse struct {
	OK       bool                   `json:"ok"`
	Error    string                 `json:"error,omitempty"`
	Warnings []string               `json:"warnings"`
	Proposed adminStorageActionPlan `json:"proposed"`
	Applied  bool                   `json:"applied"`
}

var runAdminStorageActionsHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminStorageActionsHelper, action)
	if len(request) > 0 {
		cmd.Stdin = bytes.NewReader(request)
	}
	return cmd.CombinedOutput()
}

func registerAdminStorageActionRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/storage-actions/plan", s.adminStorageActionPlan)
	mux.HandleFunc("POST /v1/admin/storage-actions/apply", s.adminStorageActionApply)
}

func (s *server) adminStorageActionPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.runAdminStorageAction(w, r, "plan", session{})
}

func (s *server) adminStorageActionApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	s.runAdminStorageAction(w, r, "apply", actor)
}

func (s *server) runAdminStorageAction(w http.ResponseWriter, r *http.Request, action string, actor session) {
	var request adminStorageActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage action request could not be prepared")
		return
	}

	timeout := 15 * time.Second
	if action == "apply" {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	output, err := runAdminStorageActionsHelper(ctx, action, payload)
	if err != nil {
		if action == "apply" {
			_ = s.store.recordAuditEvent(actor, "storage_"+request.Operation, request.Device, false, "storage action helper failed")
		}
		writeError(w, http.StatusServiceUnavailable, "storage action failed")
		return
	}

	var out adminStorageActionResponse
	if err := json.Unmarshal(output, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "storage action returned invalid data")
		return
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if !out.OK {
		if out.Error == "" {
			out.Error = "storage action could not be validated"
		}
		if action == "apply" {
			_ = s.store.recordAuditEvent(actor, "storage_"+request.Operation, request.Device, false, out.Error)
		}
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	if action == "apply" {
		context := fmt.Sprintf("operation=%s role=%s mount=%s", out.Proposed.Operation, out.Proposed.Role, out.Proposed.CurrentMountPoint)
		_ = s.store.recordAuditEvent(actor, "storage_"+out.Proposed.Operation, out.Proposed.Device, true, context)
	}
	writeJSON(w, http.StatusOK, out)
}
