package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminConfigurationAPI interface {
	adminDiscoveryAPI
	AdminConfigurationPlan(ctx context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error)
	AdminConfigurationApply(ctx context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error)
}

func (a *App) serverSettingsPlan(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.adminConfigurationRequest(w, r)
	if !ok {
		return
	}
	request, err := parseServerSettingsForm(r)
	if err != nil {
		a.renderServerSettingsFormError(w, r, client, session, identity, request, err.Error())
		return
	}
	plan, err := client.AdminConfigurationPlan(r.Context(), session, request)
	if err != nil {
		a.renderServerSettingsFormError(w, r, client, session, identity, request, apiMessage(err, "Could not validate these settings."))
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	defaults, err := client.AdminSetupDefaults(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	data := a.buildServerSettingsPageData(identity, configuration, defaults, request, &plan, "", "")
	data.CSRF = csrfFromRequest(r)
	a.renderAdminDiscovery(w, "server_settings.html", data)
}

func (a *App) serverSettingsApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.adminConfigurationRequest(w, r)
	if !ok {
		return
	}
	request, err := parseServerSettingsForm(r)
	if err != nil {
		a.renderServerSettingsFormError(w, r, client, session, identity, request, err.Error())
		return
	}
	result, err := client.AdminConfigurationApply(r.Context(), session, request)
	if err != nil {
		a.renderServerSettingsFormError(w, r, client, session, identity, request, apiMessage(err, "Could not apply these settings."))
		return
	}
	if result.ConfirmationRequired {
		configuration, err := client.AdminConfiguration(r.Context(), session)
		if err != nil {
			a.handleAdminDiscoveryError(w, r, err)
			return
		}
		defaults, err := client.AdminSetupDefaults(r.Context(), session)
		if err != nil {
			a.handleAdminDiscoveryError(w, r, err)
			return
		}
		data := a.buildServerSettingsPageData(identity, configuration, defaults, request, &result, "", "")
		data.CSRF = csrfFromRequest(r)
		a.renderAdminDiscovery(w, "server_settings.html", data)
		return
	}
	if !result.Applied {
		a.renderServerSettingsFormError(w, r, client, session, identity, request, "Settings were not applied.")
		return
	}
	location := "/settings/server?result=saved"
	if result.Restarted {
		location += "&memory_restart=1"
	} else if result.MemoryRestartRequired && result.RestartDeferred {
		location += "&next_start=1"
	} else if result.RestartRequired && result.RestartDeferred {
		location += "&restart=1"
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (a *App) adminConfigurationRequest(w http.ResponseWriter, r *http.Request) (string, adminConfigurationAPI, api.SessionInfo, bool) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, _, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminConfigurationAPI)
	if !ok {
		http.Error(w, "server configuration is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) renderServerSettingsFormError(w http.ResponseWriter, r *http.Request, client adminConfigurationAPI, session string, identity api.SessionInfo, request api.AdminConfigurationChangeRequest, message string) {
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	defaults, err := client.AdminSetupDefaults(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	data := a.buildServerSettingsPageData(identity, configuration, defaults, request, nil, message, "")
	data.CSRF = csrfFromRequest(r)
	w.WriteHeader(http.StatusBadRequest)
	a.renderAdminDiscovery(w, "server_settings.html", data)
}

func (a *App) buildServerSettingsPageData(identity api.SessionInfo, configuration api.AdminConfigurationDiscovery, defaults api.AdminSetupDefaults, form api.AdminConfigurationChangeRequest, plan *api.AdminConfigurationChangeResponse, errorMessage, message string) serverSettingsPageData {
	return serverSettingsPageData{
		Title: "Minecraft settings", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		Identity: identity, Configuration: configuration, Defaults: defaults, Form: form, Plan: plan,
		Error: errorMessage, Message: message,
		SystemMemory:             formatMemoryMiB(defaults.SystemMemoryMiB),
		SystemReserveMinimum:     formatMemoryMiB(defaults.SystemReserveMinimumMiB),
		SystemReserveRecommended: formatMemoryMiB(defaults.SystemReserveRecommendedMiB),
	}
}

func configurationRequestFromDiscovery(configuration api.AdminConfigurationDiscovery) api.AdminConfigurationChangeRequest {
	if !configuration.Configured {
		return api.AdminConfigurationChangeRequest{}
	}
	return api.AdminConfigurationChangeRequest{
		JavaMemory: configuration.Minecraft.JavaMemory, ContainerMemory: configuration.Minecraft.ContainerMemory,
		JavaPort: configuration.Minecraft.JavaPort, BedrockEnabled: configuration.Minecraft.BedrockEnabled,
		BedrockPort: configuration.Minecraft.BedrockPort, Timezone: configuration.Minecraft.Timezone,
		MaxPlayers: configuration.Minecraft.MaxPlayers, MOTD: configuration.Minecraft.MOTD,
		ImageTag: configuration.Minecraft.ImageTag, VersionPolicy: configuration.Minecraft.VersionMode,
		Version: configuration.Minecraft.Version, BackupKeep: configuration.Backup.Keep,
		BackupSchedule: configuration.Backup.Schedule, BackupTimerEnabled: configuration.Backup.TimerEnabled,
	}
}

func parseServerSettingsForm(r *http.Request) (api.AdminConfigurationChangeRequest, error) {
	var out api.AdminConfigurationChangeRequest
	if err := r.ParseForm(); err != nil {
		return out, errors.New("Could not read the settings form.")
	}
	out.JavaMemory = strings.TrimSpace(r.FormValue("java_memory"))
	out.ContainerMemory = strings.TrimSpace(r.FormValue("container_memory"))
	out.Timezone = strings.TrimSpace(r.FormValue("timezone"))
	out.MOTD = strings.TrimSpace(r.FormValue("motd"))
	out.ImageTag = strings.TrimSpace(r.FormValue("image_tag"))
	out.VersionPolicy = strings.TrimSpace(r.FormValue("version_policy"))
	out.Version = strings.TrimSpace(r.FormValue("version"))
	out.BackupSchedule = strings.TrimSpace(r.FormValue("backup_schedule"))
	out.BedrockEnabled = r.FormValue("bedrock_enabled") == "on"
	out.BackupTimerEnabled = r.FormValue("backup_timer_enabled") == "on"
	out.ConfirmPlayers = r.FormValue("confirm_players") == "yes"

	var err error
	if out.JavaPort, err = parsePositiveFormInt(r.FormValue("java_port"), "Minecraft Java port"); err != nil {
		return out, err
	}
	if out.BedrockPort, err = parsePositiveFormInt(r.FormValue("bedrock_port"), "Bedrock port"); err != nil {
		return out, err
	}
	if out.MaxPlayers, err = parsePositiveFormInt(r.FormValue("max_players"), "Maximum players"); err != nil {
		return out, err
	}
	if out.BackupKeep, err = parsePositiveFormInt(r.FormValue("backup_keep"), "Backup retention"); err != nil {
		return out, err
	}
	return out, nil
}

func parsePositiveFormInt(value, label string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive number.", label)
	}
	return parsed, nil
}

func formatMemoryMiB(value int) string {
	if value <= 0 {
		return "Unknown"
	}
	return humanBytes(uint64(value) * 1024 * 1024)
}
