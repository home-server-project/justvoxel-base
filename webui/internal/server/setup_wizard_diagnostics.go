package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type setupDiagnosticAPI interface {
	AdminSetupDiagnosticSession(ctx context.Context, session, interfaceName string) (api.AdminSetupDiagnosticSessionResponse, error)
	AdminSetupDiagnosticEvent(ctx context.Context, session, id, event string, values map[string]string) error
}

type setupDiagnosticDownloadAPI interface {
	AdminSetupDiagnosticLog(ctx context.Context, session, id string) ([]byte, error)
}

func (a *App) beginSetupDiagnosticBestEffort(ctx context.Context, client adminDiscoveryAPI, session string) string {
	diagnostics, ok := client.(setupDiagnosticAPI)
	if !ok {
		return ""
	}
	response, err := diagnostics.AdminSetupDiagnosticSession(ctx, session, "webui")
	if err != nil || !setupOperationIDPattern.MatchString(response.SessionID) {
		return ""
	}
	_ = diagnostics.AdminSetupDiagnosticEvent(ctx, session, response.SessionID, "WebUI setup wizard started", map[string]string{})
	return response.SessionID
}

func (a *App) recordSetupDiagnosticIDBestEffort(ctx context.Context, client adminDiscoveryAPI, session, diagnosticID, event string, values map[string]string) {
	if !setupOperationIDPattern.MatchString(diagnosticID) {
		return
	}
	diagnostics, ok := client.(setupDiagnosticAPI)
	if !ok {
		return
	}
	_ = diagnostics.AdminSetupDiagnosticEvent(ctx, session, diagnosticID, event, values)
}

func (a *App) recordSetupDraftDiagnosticBestEffort(ctx context.Context, client adminDiscoveryAPI, session, event string, draft setupDraft) {
	a.recordSetupDiagnosticIDBestEffort(ctx, client, session, draft.DiagnosticSessionID, event, setupDraftDiagnosticValues(draft))
}

func setupDraftDiagnosticValues(draft setupDraft) map[string]string {
	return map[string]string{
		"wizard_step":         strconv.Itoa(draft.CurrentStep),
		"motd":                draft.Server.MOTD,
		"max_players":         draft.Server.MaxPlayers,
		"bedrock_enabled":     strconv.FormatBool(draft.Server.BedrockEnabled),
		"timezone":            draft.Server.Timezone,
		"java_memory":         draft.Minecraft.JavaMemory,
		"container_memory":    draft.Minecraft.ContainerMemory,
		"java_port":           draft.Minecraft.JavaPort,
		"bedrock_port":        draft.Minecraft.BedrockPort,
		"minecraft_image":     draft.Minecraft.ImageTag,
		"version_policy":      draft.Minecraft.VersionPolicy,
		"minecraft_version":   draft.Minecraft.Version,
		"storage_type":        draft.Storage.Type,
		"storage_path":        draft.Storage.Path,
		"storage_device":      draft.Storage.Device,
		"storage_mount_point": draft.Storage.MountPoint,
		"backup_automatic":    strconv.FormatBool(draft.Backups.Automatic),
		"backup_daily_time":   draft.Backups.DailyTime,
		"backup_keep":         draft.Backups.Keep,
		"backup_type":         draft.Backups.Type,
		"backup_path":         draft.Backups.Path,
		"backup_device":       draft.Backups.Device,
		"backup_mount_point":  draft.Backups.MountPoint,
		"backup_source":       draft.Backups.Source,
		"backup_username":     draft.Backups.Username,
		"backup_domain":       draft.Backups.Domain,
	}
}

func (a *App) setupWizardDiagnosticDownload(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !setupOperationIDPattern.MatchString(id) {
		http.Error(w, "invalid setup operation", http.StatusBadRequest)
		return
	}
	downloader, ok := client.(setupDiagnosticDownloadAPI)
	if !ok {
		http.Error(w, "setup diagnostic download is unavailable", http.StatusServiceUnavailable)
		return
	}
	data, err := downloader.AdminSetupDiagnosticLog(r.Context(), session, id)
	if err != nil {
		a.handleSetupProgressError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"justvoxel-setup-%s.log\"", id))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
