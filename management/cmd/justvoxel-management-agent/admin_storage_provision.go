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

const adminStorageProvisionHelper = "/usr/libexec/justvoxel/mjust/admin-storage-provision-json"

type adminStorageProvisionRequest struct {
	Operation    string `json:"operation"`
	Device       string `json:"device"`
	MountPoint   string `json:"mount_point"`
	Path         string `json:"path"`
	SizeGiB      string `json:"size_gib,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
}

type adminStorageProvisionCandidate struct {
	Path       string `json:"path"`
	Parent     string `json:"parent,omitempty"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Filesystem string `json:"filesystem,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	SizeBytes  uint64 `json:"size_bytes"`
	FreeBytes  uint64 `json:"free_bytes,omitempty"`
	SystemDisk bool   `json:"system_disk,omitempty"`
}

type adminStorageProvisionPlan struct {
	Operation          string `json:"operation"`
	Device             string `json:"device"`
	Model              string `json:"model,omitempty"`
	Transport          string `json:"transport,omitempty"`
	SizeBytes          uint64 `json:"size_bytes"`
	MountPoint         string `json:"mount_point"`
	Path               string `json:"path"`
	SizeGiB            string `json:"size_gib,omitempty"`
	FreeStart          string `json:"free_start,omitempty"`
	FreeEnd            string `json:"free_end,omitempty"`
	PlannedEnd         string `json:"planned_end,omitempty"`
	FreeMiB            uint64 `json:"free_mib,omitempty"`
	PlannedMiB         uint64 `json:"planned_mib,omitempty"`
	Confirmation       string `json:"confirmation"`
	Fingerprint        string `json:"fingerprint"`
	SystemDisk         bool   `json:"system_disk,omitempty"`
	Partition          string `json:"partition,omitempty"`
	Filesystem         string `json:"filesystem,omitempty"`
	ExpectedUUID       string `json:"expected_uuid,omitempty"`
	ExpectedSource     string `json:"expected_source,omitempty"`
	PartitionSizeBytes uint64 `json:"partition_size_bytes,omitempty"`
}

type adminStorageProvisionResponse struct {
	OK              bool                             `json:"ok"`
	Error           string                           `json:"error,omitempty"`
	Warnings        []string                         `json:"warnings"`
	WholeDisks      []adminStorageProvisionCandidate `json:"whole_disks,omitempty"`
	BlankPartitions []adminStorageProvisionCandidate `json:"blank_partitions,omitempty"`
	FreeSpaceDisks  []adminStorageProvisionCandidate `json:"free_space_disks,omitempty"`
	Proposed        adminStorageProvisionPlan        `json:"proposed"`
	Applied         bool                             `json:"applied"`
}

var runAdminStorageProvisionHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminStorageProvisionHelper, action)
	if len(request) > 0 {
		cmd.Stdin = bytes.NewReader(request)
	}
	return cmd.CombinedOutput()
}

func registerAdminStorageProvisionRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/storage-provision", s.adminStorageProvisionDiscover)
	mux.HandleFunc("POST /v1/admin/storage-provision/plan", s.adminStorageProvisionPlan)
	mux.HandleFunc("POST /v1/admin/storage-provision/apply", s.adminStorageProvisionApply)
}

func (s *server) adminStorageProvisionDiscover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, err := runAdminStorageProvisionHelper(ctx, "discover", nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "advanced storage discovery is unavailable")
		return
	}
	var out adminStorageProvisionResponse
	if err := json.Unmarshal(output, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "advanced storage discovery returned invalid data")
		return
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if out.WholeDisks == nil {
		out.WholeDisks = []adminStorageProvisionCandidate{}
	}
	if out.BlankPartitions == nil {
		out.BlankPartitions = []adminStorageProvisionCandidate{}
	}
	if out.FreeSpaceDisks == nil {
		out.FreeSpaceDisks = []adminStorageProvisionCandidate{}
	}
	if !out.OK {
		if out.Error == "" {
			out.Error = "advanced storage discovery is unavailable"
		}
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) adminStorageProvisionPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.runAdminStorageProvisionChange(w, r, "plan", session{})
}

func (s *server) adminStorageProvisionApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	s.runAdminStorageProvisionChange(w, r, "apply", actor)
}

func (s *server) runAdminStorageProvisionChange(w http.ResponseWriter, r *http.Request, action string, actor session) {
	var request adminStorageProvisionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage provisioning request could not be prepared")
		return
	}
	timeout := 15 * time.Second
	if action == "apply" {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	var output []byte
	if action == "apply" {
		output, err = runHostStorageMutation(ctx, adminStorageProvisionHelper, action, payload)
	} else {
		output, err = runAdminStorageProvisionHelper(ctx, action, payload)
	}
	if err != nil {
		if action == "apply" {
			_ = s.store.recordAuditEvent(actor, "provision_backup_storage", request.Device, false, "storage provisioning helper failed")
		}
		writeError(w, http.StatusServiceUnavailable, "storage provisioning operation failed")
		return
	}
	var out adminStorageProvisionResponse
	if err := json.Unmarshal(output, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "storage provisioning operation returned invalid data")
		return
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if !out.OK {
		if out.Error == "" {
			out.Error = "storage provisioning could not be validated"
		}
		if action == "apply" {
			_ = s.store.recordAuditEvent(actor, "provision_backup_storage", request.Device, false, "storage provisioning rejected or failed")
		}
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	if action == "apply" {
		context := fmt.Sprintf("operation=%s mount=%s", out.Proposed.Operation, out.Proposed.MountPoint)
		_ = s.store.recordAuditEvent(actor, "provision_backup_storage", out.Proposed.Device, true, context)
	}
	writeJSON(w, http.StatusOK, out)
}
