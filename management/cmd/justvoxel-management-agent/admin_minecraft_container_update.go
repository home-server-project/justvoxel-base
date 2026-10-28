package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Strict object decoding additionally rejects duplicate keys: a reviewed digest
// or interruption choice must never have two meanings across the boundary.
func decodeContainerJSON(body io.Reader, out any) error {
	data, err := io.ReadAll(io.LimitReader(body, 4097))
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return errors.New("container request too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("invalid container request")
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || seen[strings.ToLower(name)] {
			return errors.New("ambiguous container request")
		}
		seen[strings.ToLower(name)] = true
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return err
		}
	}
	if _, err = d.Token(); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid container request")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

const operationTypeMinecraftContainerUpdate = "minecraft_container_update"

type minecraftContainerStatus struct {
	InstanceID         string                   `json:"instance_id"`
	Image              string                   `json:"image"`
	State              string                   `json:"state"`
	Running            bool                     `json:"running"`
	PlanFingerprint    string                   `json:"plan_fingerprint"`
	RestoreFingerprint string                   `json:"restore_fingerprint"`
	KeepPreviousImages int                      `json:"keep_previous_images"`
	PreviousImages     []retainedContainerImage `json:"previous_images"`
}
type minecraftContainerUpdateRequest struct {
	InstanceID          string `json:"-"`
	PlanFingerprint     string `json:"plan_fingerprint"`
	RestartNow          bool   `json:"restart_now"`
	BackupBeforeRestart bool   `json:"backup_before_restart"`
	ConfirmPlayers      bool   `json:"confirm_players"`
	RestoreDigest       string `json:"restore_digest,omitempty"`
}

const minecraftContainerHelper = "/usr/libexec/justvoxel/mjust/minecraft-container-update"

var runMinecraftContainerStatus = func(ctx context.Context, id string) (minecraftContainerStatus, error) {
	var out minecraftContainerStatus
	data, err := exec.CommandContext(ctx, minecraftContainerHelper, "--instance", id, "status").Output()
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	if err == nil && out.InstanceID != id {
		err = errInstanceID
	}
	return out, err
}
var runMinecraftContainerUpdate = func(ctx context.Context, request minecraftContainerUpdateRequest) error {
	mode := "update"
	args := []string{"--instance", request.InstanceID, mode, request.PlanFingerprint, strconv.FormatBool(request.RestartNow), strconv.FormatBool(request.BackupBeforeRestart), strconv.FormatBool(request.ConfirmPlayers)}
	if request.RestoreDigest != "" {
		args[2] = "restore"
		args = append(args, request.RestoreDigest)
	}
	cmd := exec.CommandContext(ctx, minecraftContainerHelper, args...)
	finish, err := attachOperationTrace(ctx, cmd)
	if err != nil {
		return err
	}
	err = cmd.Run()
	finish(err)
	return err
}
var verifyMinecraftContainerCurrent = func(ctx context.Context, id string) error {
	return exec.CommandContext(ctx, minecraftContainerHelper, "--instance", id, "verify").Run()
}

func (s *server) adminMinecraftContainerStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := validatedVersionInstance(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "Container status accepts no image selection")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	out, err := runMinecraftContainerStatus(ctx, id)
	if err != nil {
		writeError(w, 503, "Container image information is unavailable")
		return
	}
	writeJSON(w, 200, out)
}
func (s *server) adminMinecraftContainerUpdate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	id, ok := validatedVersionInstance(w, r)
	if !ok {
		return
	}
	var request minecraftContainerUpdateRequest
	if len(r.URL.Query()) != 0 || decodeContainerJSON(r.Body, &request) != nil || !operationFingerprintPattern.MatchString(request.PlanFingerprint) || (request.BackupBeforeRestart && !request.RestartNow) || (request.RestoreDigest != "" && !operationFingerprintPattern.MatchString(request.RestoreDigest)) {
		writeError(w, 400, "Invalid reviewed container update")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Container update tracking is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	plan, err := runMinecraftContainerStatus(ctx, id)
	if err != nil || plan.InstanceID != id || (plan.State == "unavailable" && request.RestoreDigest == "") {
		writeError(w, 503, "Container image could not be verified")
		return
	}
	reviewed := plan.PlanFingerprint
	if request.RestoreDigest != "" {
		reviewed = plan.RestoreFingerprint
		found := false
		for _, image := range plan.PreviousImages {
			if image.Digest == request.RestoreDigest {
				found = true
			}
		}
		if !found || request.RestartNow != plan.Running {
			writeError(w, 409, "The previous image or server state changed; refresh and review again")
			return
		}
	}
	if reviewed != request.PlanFingerprint {
		writeError(w, 409, "Container image or runtime changed; refresh and review again")
		return
	}
	if request.RestoreDigest == "" && ((request.RestartNow && !plan.Running) || (plan.State != "update_available" && plan.State != "image_missing" && !(plan.State == "restart_needed" && request.RestartNow))) {
		writeError(w, 409, "No safe container action is available for these choices")
		return
	}
	request.InstanceID = id
	journal, created, err := s.operations.beginMinecraftMaintenance(request.PlanFingerprint, operationTypeMinecraftContainerUpdate, id)
	if err != nil {
		writeError(w, 409, "Another appliance maintenance operation requires attention")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "operation": journal})
	if created {
		startMinecraftContainerUpdateWorker(s, journal.OperationID, request, actor)
	}
}

var startMinecraftContainerUpdateWorker = func(s *server, id string, request minecraftContainerUpdateRequest, actor session) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		_ = executeMinecraftContainerUpdate(ctx, s, id, request, actor)
	}()
}

func executeMinecraftContainerUpdate(ctx context.Context, s *server, id string, request minecraftContainerUpdateRequest, actor session) error {
	journal, err := s.operations.get(id)
	if err != nil {
		return err
	}
	if !instanceIDPattern.MatchString(request.InstanceID) || journal.InstanceID != request.InstanceID || journal.OperationType != operationTypeMinecraftContainerUpdate || journal.PlanFingerprint != request.PlanFingerprint {
		return errInstanceID
	}
	if _, err = s.operations.transition(id, operationValidating, "validating", "Revalidating the reviewed container image."); err != nil {
		return err
	}
	if _, err = s.operations.transition(id, operationRunning, "updating", "Preparing the selected container image."); err != nil {
		return err
	}
	ctx = withOperationTrace(ctx, s.operations, id)
	err = runMinecraftContainerUpdate(ctx, request)
	if err != nil {
		_, transitionErr := s.operations.transition(id, operationNeedsAttention, "update_failed", "Update stopped. Check the details below. You can keep the current version or try again.")
		err = errors.Join(err, transitionErr)
	} else {
		if _, err = s.operations.transition(id, operationVerifying, "verifying", "Selected image verification completed."); err != nil {
			return err
		}
		_, err = s.operations.transition(id, operationSucceeded, "complete", "Container update completed.")
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, operationTypeMinecraftContainerUpdate, request.InstanceID, err == nil, "Selected container image maintenance")
	}
	return err
}
func (s *server) adminMinecraftContainerUpdateCurrent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := validatedVersionInstance(w, r, false)
	if !ok {
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Container update tracking is unavailable")
		return
	}
	journal, err := s.operations.currentMinecraftMaintenance()
	if operationID := r.URL.Query().Get("operation_id"); operationID != "" {
		if !validOperationID(operationID) {
			writeError(w, 400, "Invalid operation identifier")
			return
		}
		var found operationJournal
		found, err = s.operations.get(operationID)
		journal = &found
	}
	if err != nil {
		writeError(w, 503, "Container update tracking is unavailable")
		return
	}
	if journal != nil && (journal.OperationType != operationTypeMinecraftContainerUpdate || journal.InstanceID != id) {
		journal = nil
	}
	writeJSON(w, 200, adminOperationResponse{Operation: journal})
}
func (s *server) adminMinecraftContainerUpdateAcknowledge(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := validatedVersionInstance(w, r, false)
	if !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
	}
	if decodeContainerJSON(r.Body, &request) != nil || !validOperationID(request.OperationID) {
		writeError(w, 400, "Invalid container update acknowledgement")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Container update tracking is unavailable")
		return
	}
	journal, err := s.operations.currentMinecraftMaintenance()
	if err != nil || journal == nil || journal.InstanceID != id || journal.OperationType != operationTypeMinecraftContainerUpdate || journal.OperationID != request.OperationID || journal.State != operationNeedsAttention {
		writeError(w, 409, "No failed container update can be acknowledged")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	pending, recoveryErr := instanceRuntimes.pending(id)
	if recoveryErr == nil && pending {
		recoveryErr = instanceRuntimes.recover(ctx, id)
	}
	if recoveryErr != nil {
		writeError(w, 409, "Selected runtime recovery could not be verified. Keep this operation for review.")
		return
	}
	if verifyMinecraftContainerCurrent(ctx, id) != nil {
		writeError(w, 409, "Current container runtime could not be verified. Keep this operation for review.")
		return
	}
	resolved, err := s.operations.transition(journal.OperationID, operationResolved, "resolved", "Current version kept. No restart or update was retried.")
	if err != nil {
		writeError(w, 409, "Container update could not be acknowledged")
		return
	}
	writeJSON(w, 200, adminOperationResponse{Operation: &resolved})
}

// Retention is a small per-instance setting. The helper serializes it with all
// appliance maintenance, and atomically publishes the list before untagging.
var runMinecraftContainerRetention = func(ctx context.Context, id string, n int) error {
	return exec.CommandContext(ctx, minecraftContainerHelper, "--instance", id, "retention", strconv.Itoa(n)).Run()
}

func (s *server) adminMinecraftContainerRetention(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	id, ok := validatedVersionInstance(w, r)
	if !ok {
		return
	}
	var request struct {
		Keep *int `json:"keep_previous_images"`
	}
	if len(r.URL.Query()) != 0 || decodeContainerJSON(r.Body, &request) != nil || (request.Keep != nil && *request.Keep < 0) {
		writeError(w, 400, "Keep previous images must be a non-negative integer")
		return
	}
	n := 1
	if request.Keep != nil {
		n = *request.Keep
	}
	if s.operations != nil {
		current, err := s.operations.currentMinecraftMaintenance()
		if err != nil || current != nil {
			writeError(w, 409, "Finish the current maintenance operation before changing image retention")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	err := runMinecraftContainerRetention(ctx, id, n)
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "minecraft_container_retention", id, err == nil, "Previous container image retention")
	}
	if err != nil {
		writeError(w, 409, "Previous image retention could not be saved. Try again when maintenance is finished.")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "keep_previous_images": n})
}
