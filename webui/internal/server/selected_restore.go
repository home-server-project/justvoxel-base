package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Only public presentation fields leave the WebUI. Journal evidence and backend
// diagnostic strings are deliberately not part of this contract.
type selectedRestoreProgress struct {
	ID               string `json:"id"`
	State            string `json:"state"`
	Stage            string `json:"stage"`
	RecoveryRequired bool   `json:"recovery_required"`
	StateLabel       string `json:"state_label"`
	StageLabel       string `json:"stage_label"`
	Status           string `json:"status"`
	Complete         bool   `json:"complete"`
	RolledBack       bool   `json:"rolled_back"`
	Attention        bool   `json:"attention"`
	Terminal         bool   `json:"terminal"`
}

func (p selectedRestoreProgress) Busy() bool {
	return p.State != "idle" && !p.Complete && !p.RolledBack
}

func restoreProgressLabel(state string) string {
	switch state {
	case "idle":
		return "Ready"
	case "accepted", "validating":
		return "Validating"
	case "staging", "ready_to_apply":
		return "Preparing Restore data"
	case "shutdown":
		return "Stopping Minecraft safely"
	case "preserving_original", "replacement_activated", "original_preserved":
		return "Applying Restore data"
	case "runtime_verification":
		return "Runtime validation"
	case "rollback_required", "recovering":
		return "Preparing recovery"
	case "original_restored":
		return "Restoring previous Minecraft data"
	case "rollback_verification":
		return "Rolling back"
	case "rolled_back":
		return "Rolled back"
	case "completed":
		return "Complete"
	case "recovery_required", "interrupted", "failed":
		return "Needs attention"
	default:
		return "Checking Restore status"
	}
}

func presentSelectedRestore(p selectedRestoreProgress) selectedRestoreProgress {
	p.StateLabel = restoreProgressLabel(p.State)
	p.StageLabel = restoreProgressLabel(p.Stage)
	if p.Stage == "" {
		p.StageLabel = p.StateLabel
	}
	p.Complete = p.State == "completed" && !p.RecoveryRequired
	p.RolledBack = p.State == "rolled_back" && !p.RecoveryRequired
	p.Attention = p.RecoveryRequired || p.State == "interrupted" || p.State == "failed"
	p.Terminal = p.Complete || p.RolledBack || p.Attention || p.State == "idle"
	p.Status = "Restore is in progress for this Minecraft server."
	// Active Recovery can still have a recovery_required journal stage while
	// the Agent explicitly reports recovery_required=false. Keep polling.
	if p.State == "recovery_required" && !p.RecoveryRequired {
		p.StateLabel = "Preparing recovery"
		if p.Stage == "recovery_required" || p.Stage == "" {
			p.StageLabel = "Preparing recovery"
		}
	}
	switch {
	case p.Attention:
		p.StateLabel = "Needs attention"
		p.Status = "Restore needs administrator attention. Review recovery before starting another Restore."
	case p.Complete:
		p.Status = "Minecraft Restore completed successfully. The restored runtime passed validation."
	case p.RolledBack:
		p.Status = "Restore was rolled back safely. The previous Minecraft data was restored and verified."
	case p.State == "idle":
		p.Status = "No Restore is currently running for this server."
	}
	return p
}

func readSelectedRestore(ctx context.Context, client selectedBackupsAPI, session, id string) (selectedRestoreProgress, error) {
	raw, err := client.MinecraftInstanceBackups(ctx, session, id, "restore/status", nil)
	if err != nil {
		return selectedRestoreProgress{}, err
	}
	var p selectedRestoreProgress
	// Decode only authoritative backend fields, never backend-supplied labels.
	var status struct {
		ID               string `json:"id"`
		State            string `json:"state"`
		Stage            string `json:"stage"`
		RecoveryRequired bool   `json:"recovery_required"`
	}
	if json.Unmarshal(raw, &status) != nil || status.ID != id {
		return p, fmt.Errorf("Restore identity could not be verified.")
	}
	p.ID, p.State, p.Stage, p.RecoveryRequired = status.ID, status.State, status.Stage, status.RecoveryRequired
	return presentSelectedRestore(p), nil
}

func (a *App) selectedRestoreStatus(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	client, ok := a.api.(selectedBackupsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Restore status is unavailable.")
		return
	}
	progress, err := readSelectedRestore(r.Context(), client, session, selected.ID)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Restore status or identity could not be verified.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, progress)
}
