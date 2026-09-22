package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const adminStorageMountsHelper = "/usr/libexec/justvoxel/mjust/admin-storage-mounts-json"

type adminStorageMountRequest struct {
	Operation   string `json:"operation"`
	Device      string `json:"device"`
	MountPoint  string `json:"mount_point,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type adminStorageMountPlan struct {
	Operation         string `json:"operation,omitempty"`
	Device            string `json:"device"`
	Filesystem        string `json:"filesystem,omitempty"`
	UUID              string `json:"uuid,omitempty"`
	MountPoint        string `json:"mount_point,omitempty"`
	CurrentMountPoint string `json:"current_mount_point,omitempty"`
	Role              string `json:"role,omitempty"`
	Persistence       string `json:"persistence,omitempty"`
	Fingerprint       string `json:"fingerprint,omitempty"`
	SizeBytes         uint64 `json:"size_bytes,omitempty"`
	Mounted           bool   `json:"mounted,omitempty"`
	Changed           bool   `json:"changed,omitempty"`
	DirectoryCreated  bool   `json:"directory_created,omitempty"`
}

type adminStorageMountResponse struct {
	OK                bool                  `json:"ok"`
	Error             string                `json:"error,omitempty"`
	Warnings          []string              `json:"warnings"`
	Proposed          adminStorageMountPlan `json:"proposed"`
	Applied           bool                  `json:"applied"`
	MountPointRemoved bool                  `json:"mount_point_removed,omitempty"`
}

var runAdminStorageMountsHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminStorageMountsHelper, action)
	if len(request) > 0 {
		cmd.Stdin = bytes.NewReader(request)
	}
	return cmd.CombinedOutput()
}

func registerAdminStorageMountRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/storage-mounts/status", s.adminStorageMountStatus)
	mux.HandleFunc("POST /v1/admin/storage-mounts/plan", s.adminStorageMountPlan)
	mux.HandleFunc("POST /v1/admin/storage-mounts/apply", s.adminStorageMountApply)
}

func (s *server) adminStorageMountStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("device"))
	if device == "" {
		writeError(w, http.StatusBadRequest, "choose a storage partition")
		return
	}
	payload, err := json.Marshal(map[string]string{"device": device})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "permanent mount status request could not be prepared")
		return
	}
	s.runAdminStorageMount(w, r, "status", payload, adminStorageMountRequest{Device: device}, session{})
}

func (s *server) adminStorageMountPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.adminStorageMountChange(w, r, "plan", session{})
}

func (s *server) adminStorageMountApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	s.adminStorageMountChange(w, r, "apply", actor)
}

func (s *server) adminStorageMountChange(w http.ResponseWriter, r *http.Request, action string, actor session) {
	var request adminStorageMountRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "permanent mount request could not be prepared")
		return
	}
	s.runAdminStorageMount(w, r, action, payload, request, actor)
}

func (s *server) runAdminStorageMount(w http.ResponseWriter, r *http.Request, action string, payload []byte, request adminStorageMountRequest, actor session) {
	timeout := 15 * time.Second
	if action == "apply" {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	output, err := runAdminStorageMountsHelper(ctx, action, payload)
	if err != nil {
		if action == "apply" && s.store != nil {
			_ = s.store.recordAuditEvent(actor, "storage_mount_"+request.Operation, request.Device, false, "permanent mount helper failed")
		}
		writeError(w, http.StatusServiceUnavailable, "permanent mount operation failed")
		return
	}

	var out adminStorageMountResponse
	if err := json.Unmarshal(output, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "permanent mount operation returned invalid data")
		return
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if !out.OK {
		if out.Error == "" {
			out.Error = "permanent mount could not be validated"
		}
		if action == "apply" && s.store != nil {
			_ = s.store.recordAuditEvent(actor, "storage_mount_"+request.Operation, request.Device, false, out.Error)
		}
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	if action == "apply" && s.store != nil {
		context := fmt.Sprintf("operation=%s mount=%s persistence=%s", out.Proposed.Operation, out.Proposed.MountPoint, out.Proposed.Persistence)
		_ = s.store.recordAuditEvent(actor, "storage_mount_"+out.Proposed.Operation, out.Proposed.Device, true, context)
	}
	writeJSON(w, http.StatusOK, out)
}
