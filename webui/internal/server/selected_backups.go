package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type selectedBackupsAPI interface {
	MinecraftInstanceBackups(context.Context, string, string, string, any) (json.RawMessage, error)
}

// The physical backup destination belongs to the appliance, independently of
// the selected server's backup execution, schedule, and restore operations.
type selectedBackupDestinationAPI interface {
	storageDestinationAPI
	AdminBackupStoragePlan(context.Context, string, api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
	AdminBackupStorageApply(context.Context, string, api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
}

type selectedBackupMetadata struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Created string `json:"created"`
	Archive string `json:"archive"`
	Size    int64  `json:"size"`
}
type selectedRestorePlan struct {
	PlanID                      string                 `json:"plan_id"`
	Metadata                    selectedBackupMetadata `json:"metadata"`
	CurrentVersion              string                 `json:"current_version"`
	Integrity                   string                 `json:"integrity"`
	RuntimeState                string                 `json:"runtime_state"`
	Players                     api.InstancePlayers    `json:"players"`
	PlayersConfirmationRequired bool                   `json:"players_confirmation_required"`
}

func (a *App) selectedBackupsWorkspace(w http.ResponseWriter, r *http.Request) bool {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return true
	}
	if r.Method != http.MethodGet && !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, 403, "Invalid CSRF token.")
		return true
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return true
	}
	client, ok := a.api.(selectedBackupsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Server backups are unavailable.")
		return true
	}
	identityClient, ok := a.api.(sessionIdentityAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Session unavailable.")
		return true
	}
	identity, err := identityClient.Session(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Session unavailable.")
		return true
	}
	data := backupsWorkspaceData{CSRF: csrfFromRequest(r), Identity: identity, SelectedServer: selected.ID, SelectedName: selected.Name, RestoreScope: "Restore this backup to the same server identity only. The complete server data is replaced.", RestoreModeTitle: "Restore full Minecraft data"}
	destinationClient, hasDestination := a.api.(selectedBackupDestinationAPI)
	if hasDestination {
		destination, err := a.storageDestinationData(r, session, identity, data.CSRF, nil, nil)
		if err != nil {
			data.Error = apiMessage(err, "Backup destination is unavailable.")
		} else {
			data = destination
			data.SelectedServer, data.SelectedName = selected.ID, selected.Name
			data.RestoreScope = "Restore this backup to the same server identity only. The complete server data is replaced."
			data.RestoreModeTitle = "Restore full Minecraft data"
		}
		data.DestinationWorkspace = "/workspace/backups"
	}
	if r.URL.Query().Get("result") == "destination" {
		data.Message = "Backup destination updated. New manual and automatic backups will use it."
	}
	call := func(action string, request any, target any) bool {
		raw, e := client.MinecraftInstanceBackups(r.Context(), session, selected.ID, action, request)
		if e != nil {
			data.Error = apiMessage(e, "Selected server backup operation did not complete.")
			return false
		}
		if target != nil && json.Unmarshal(raw, target) != nil {
			data.Error = "Selected server backup response is unavailable."
			return false
		}
		return true
	}
	path := r.URL.Path
	if r.Method == http.MethodPost {
		switch path {
		case "/workspace/backups/destination/plan", "/workspace/backups/destination/apply":
			if !hasDestination {
				data.Error = "This operation is unavailable for the selected server."
				break
			}
			apply := strings.HasSuffix(path, "/apply")
			request, err := parseBackupsWorkspaceStorageForm(r, apply)
			data.DestinationForm = request
			if err != nil {
				data.Error = err.Error()
				break
			}
			var result api.AdminBackupStorageResponse
			if apply {
				result, err = destinationClient.AdminBackupStorageApply(r.Context(), session, request)
			} else {
				result, err = destinationClient.AdminBackupStoragePlan(r.Context(), session, request)
			}
			if err != nil {
				data.Error = apiMessage(err, "Backup destination could not be updated.")
				break
			}
			if apply && result.Applied {
				target := "/workspace/backups?result=destination"
				if r.URL.Query().Has("server") {
					target += "&server=" + selected.ID
				}
				http.Redirect(w, r, target, http.StatusSeeOther)
				return true
			}
			data.DestinationPlan = &result
			data.ProposedDestinationAvailable = backupsWorkspaceFormatOptionalBytes(result.Proposed.AvailableBytes)
			data.ProposedDestinationSize = backupsWorkspaceFormatOptionalBytes(result.Proposed.FilesystemBytes)
			if apply {
				data.Error = "Backup destination was not applied."
			}

		case "/workspace/backups/backup":
			if call("now", struct{}{}, nil) {
				data.Message = "Backup requested for this server."
			}
		case "/workspace/backups/automatic/plan", "/workspace/backups/automatic/apply":
			form, e := parseBackupsWorkspaceAutomaticForm(r)
			if e != nil {
				data.Error = e.Error()
				break
			}
			data.Automatic = form
			if strings.HasSuffix(path, "/apply") {
				if call("configure", api.InstanceBackupSettings{Enabled: form.Enabled, Time: form.DailyTime, Retention: form.Keep}, nil) {
					data.Message = "Automatic backup settings saved."
				}
			} else {
				data.AutomaticPlan = &api.AdminConfigurationChangeResponse{OK: true, Changes: []api.AdminConfigurationChange{{Field: "backup"}}}
			}
		case "/workspace/backups/restore/plan":
			var plan selectedRestorePlan
			archive := r.FormValue("backup_id")
			if call("restore/plan", map[string]any{"archive": archive}, &plan) {
				if plan.Metadata.ID != selected.ID || plan.Metadata.Archive != archive || plan.Integrity != "verified" {
					data.Error = "Backup identity or integrity could not be verified."
					break
				}
				data.RestoreRequest = backupsRestoreRequest{BackupID: archive, Mode: "full"}
				data.RestorePlan = &backupsRestorePlan{OK: true, PlanFingerprint: plan.PlanID, Normalized: &backupsRestoreNormalized{BackupID: archive, CreatedAt: plan.Metadata.Created, Current: backupsRestoreCurrent{Version: plan.CurrentVersion}, VersionRelation: "Same server identity and compatible version verified"}, Requirements: &backupsRestoreRequirements{DestructiveConfirmationRequired: true, PlayersConfirmationRequired: plan.PlayersConfirmationRequired, Online: plan.Players.Online}}
			}
		case "/workspace/backups/restore/apply":
			if r.FormValue("confirmation") != "RESTORE" {
				data.Error = "Confirm Restore before applying."
				break
			}
			if call("restore/apply", map[string]any{"archive": r.FormValue("backup_id"), "plan_id": r.FormValue("plan_fingerprint"), "confirm_destructive": true, "confirm_players": r.FormValue("players_confirmed") == "yes"}, nil) {
				data.Message = "Restore accepted. Progress will update automatically."
				progress := presentSelectedRestore(selectedRestoreProgress{ID: selected.ID, State: "validating"})
				data.SelectedRestoreProgress = &progress
			}
		case "/workspace/backups/restore/recover":
			if r.FormValue("confirm_players") != "yes" {
				data.Error = "Confirm safe recovery interruption first."
				break
			}
			if call("restore/recover", map[string]any{"confirm_players": true}, nil) {
				data.Message = "Restore Recovery accepted. Progress will update automatically."
				progress := presentSelectedRestore(selectedRestoreProgress{ID: selected.ID, State: "recovering"})
				data.SelectedRestoreProgress = &progress
			}
		default:
			data.Error = "This operation is unavailable for the selected server."
		}
	}
	var settings api.InstanceBackupSettings
	if call("settings", nil, &settings) && data.AutomaticPlan == nil {
		data.Automatic = backupsWorkspaceAutomaticForm{Enabled: settings.Enabled, DailyTime: settings.Time, Keep: settings.Retention}
	}
	var inventory struct {
		ID      string                   `json:"id"`
		Backups []selectedBackupMetadata `json:"backups"`
	}
	if call("inventory", nil, &inventory) {
		if inventory.ID != selected.ID {
			data.Error = "Backup identity could not be verified."
		} else {
			var total uint64
			for _, item := range inventory.Backups {
				if item.ID != selected.ID || item.Size < 0 {
					data.Error = "Backup identity could not be verified."
					data.Backups = nil
					break
				}
				size := uint64(item.Size)
				total += size
				data.Backups = append(data.Backups, backupsWorkspaceBackupView{ID: item.Archive, DisplayName: item.Archive, SizeBytes: size, Size: humanBytes(size), CreatedLabel: item.Created, Version: item.Version})
			}
			data.BackupCount = len(data.Backups)
			data.TotalSize = humanBytes(total)
		}
	}
	var status struct {
		State   string `json:"state"`
		Message string `json:"message"`
	}
	if call("status", nil, &status) {
		data.SelectedBackupStatus = status.State
		if status.Message != "" {
			data.SelectedBackupStatus += " · " + status.Message
		}
	}
	if progress, err := readSelectedRestore(r.Context(), client, session, selected.ID); err != nil {
		data.Error = "Restore status or identity could not be verified."
	} else {
		data.SelectedRestoreStatus = progress.StateLabel
		data.SelectedRestoreRecovery = progress.State != "idle" && progress.RecoveryRequired
		if progress.State != "idle" {
			data.SelectedRestoreProgress = &progress
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "backups_workspace.html", data); err != nil {
		http.Error(w, fmt.Sprint("Backups could not be rendered."), 500)
	}
	return true
}
