package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type minecraftStackUpdateRequest struct {
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}

var runMinecraftStackUpdate = func(ctx context.Context, fingerprint string, confirmPlayers bool) error {
	args := []string{"--reviewed-stack", fingerprint}
	if confirmPlayers {
		args = append(args, "--confirm-players")
	}
	// Output stays privileged; the operation journal exposes appliance messages.
	output, err := exec.CommandContext(ctx, "/usr/libexec/justvoxel/mjust/update-minecraft-backend", args...).CombinedOutput()
	if err == nil {
		return nil
	}
	message := "Server update stopped. Review Minecraft state before continuing."
	text := string(output)
	switch {
	case strings.Contains(text, "download verification failed"):
		message = "Cross-play download verification failed. Installed plugins were preserved."
	case strings.Contains(text, "update download failed"):
		message = "Cross-play update download failed. Installed plugins were preserved."
	case strings.Contains(text, "release information is unavailable"):
		message = "Cross-play release information is unavailable. Installed plugins were preserved."
	case strings.Contains(text, "Update cancelled"):
		message = "Update cancelled because player interruption was not confirmed. Minecraft was left untouched."
	case strings.Contains(text, "review again"):
		message = "Server update information changed. Review updates again before applying."
	}
	return &minecraftStackMaintenanceError{message: message}
}

func (s *server) adminMinecraftStackUpdate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request minecraftStackUpdateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeError(w, http.StatusBadRequest, "Invalid reviewed server update")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "Invalid reviewed server update")
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "Server update tracking is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	data, err := runAdminVersionStatus(ctx, "", "")
	var plan adminVersionStatus
	if err != nil || json.Unmarshal(data, &plan) != nil || plan.StackState != "updates_available" || !plan.UpdateAvailable {
		writeError(w, http.StatusServiceUnavailable, "A safe server update could not be verified")
		return
	}
	if plan.PlanFingerprint != request.PlanFingerprint {
		writeError(w, http.StatusConflict, "Server updates changed; review again")
		return
	}
	journal, created, err := s.operations.beginMinecraftMaintenance(request.PlanFingerprint, operationTypeMinecraftUpdate)
	if err != nil {
		writeError(w, http.StatusConflict, "Another appliance maintenance operation requires attention")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "operation": journal})
	if !created {
		return
	}
	startMinecraftStackUpdateWorker(s, journal.OperationID, request, actor)
}

var startMinecraftStackUpdateWorker = func(s *server, operationID string, request minecraftStackUpdateRequest, actor session) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		_ = executeMinecraftStackUpdate(ctx, s, operationID, request, actor)
	}()
}

func executeMinecraftStackUpdate(ctx context.Context, s *server, operationID string, request minecraftStackUpdateRequest, actor session) error {
	if _, err := s.operations.transition(operationID, operationValidating, "validating", "Revalidating the reviewed server stack."); err != nil {
		return err
	}
	if _, err := s.operations.transition(operationID, operationRunning, "updating", "Preparing verified updates, a cold backup and a safe restart."); err != nil {
		return err
	}
	err := runMinecraftStackUpdate(ctx, request.PlanFingerprint, request.ConfirmPlayers)
	success := err == nil
	if err != nil {
		message := "Server update stopped. Review Minecraft state before continuing."
		var safe *minecraftStackMaintenanceError
		if errors.As(err, &safe) {
			message = safe.message
		}
		if _, transitionErr := s.operations.transition(operationID, operationNeedsAttention, "update_failed", message); transitionErr != nil {
			return errors.Join(err, transitionErr)
		}
	} else {
		if _, err := s.operations.transition(operationID, operationVerifying, "verifying", "Server runtime verification completed."); err != nil {
			return err
		}
		if _, err := s.operations.transition(operationID, operationSucceeded, "complete", "Server update completed."); err != nil {
			return err
		}
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "minecraft_update", "minecraft", success, "Managed server stack maintenance")
	}
	return err
}

type minecraftStackMaintenanceError struct{ message string }

func (e *minecraftStackMaintenanceError) Error() string { return e.message }

func (s *server) adminMinecraftStackUpdateCurrent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "Server update tracking is unavailable")
		return
	}
	journal, err := s.operations.currentMinecraftMaintenance()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Server update tracking is unavailable")
		return
	}
	if journal != nil && journal.OperationType != operationTypeMinecraftUpdate {
		journal = nil
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: journal})
}

var verifyMinecraftStack = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "/usr/libexec/justvoxel/mjust/verify-minecraft-stack").Run()
}

// Acknowledging a failed update keeps the current verified runtime. It never retries.
func (s *server) adminMinecraftStackUpdateAcknowledge(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "Server update tracking is unavailable")
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || !validOperationID(request.OperationID) {
		writeError(w, http.StatusBadRequest, "Invalid server update acknowledgement")
		return
	}
	journal, err := s.operations.currentMinecraftMaintenance()
	if err != nil || journal == nil || journal.OperationID != request.OperationID || journal.OperationType != operationTypeMinecraftUpdate || journal.State != operationNeedsAttention {
		writeError(w, http.StatusConflict, "No failed server update can be acknowledged")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if verifyMinecraftStack(ctx) != nil {
		writeError(w, http.StatusConflict, "Minecraft runtime could not be verified. Keep this update for review.")
		return
	}
	resolved, err := s.operations.transition(journal.OperationID, operationResolved, "resolved", "Current Minecraft runtime verified and kept. No update was retried.")
	if err != nil {
		writeError(w, http.StatusConflict, "Server update could not be acknowledged")
		return
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: &resolved})
}
