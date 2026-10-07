package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeConfigurationAPI struct {
	fakeDiscoveryAPI
	plannedRequest api.AdminConfigurationChangeRequest
	appliedRequest api.AdminConfigurationChangeRequest
	planResponse   api.AdminConfigurationChangeResponse
	applyResponse  api.AdminConfigurationChangeResponse
	planCalls      int
	applyCalls     int
}

func (f *fakeConfigurationAPI) AdminConfigurationPlan(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.plannedRequest = request
	return f.planResponse, nil
}

func (f *fakeConfigurationAPI) AdminConfigurationApply(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.appliedRequest = request
	return f.applyResponse, nil
}

func configuredSettingsFake() *fakeConfigurationAPI {
	client := &fakeConfigurationAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.DataPath = "/var/lib/justvoxel/minecraft"
	client.configuration.Minecraft.JavaMemory = "4G"
	client.configuration.Minecraft.ContainerMemory = "6G"
	client.configuration.Minecraft.JavaPort = 25565
	client.configuration.Minecraft.BedrockPort = 19132
	client.configuration.Minecraft.Timezone = "America/Toronto"
	client.configuration.Minecraft.MaxPlayers = 10
	client.configuration.Minecraft.MOTD = "JustVoxel"
	client.configuration.Minecraft.ImageTag = "stable"
	client.configuration.Minecraft.VersionMode = "pinned"
	client.configuration.Minecraft.Version = "1.21.8"
	client.configuration.Minecraft.GameMode = "survival"
	client.configuration.Minecraft.Difficulty = "normal"
	client.configuration.Backup.Path = "/var/lib/justvoxel/backups"
	client.configuration.Backup.Keep = 7
	client.configuration.Backup.Schedule = "*-*-* 04:30:00"
	client.configuration.Backup.TimerEnabled = true
	client.defaults.SystemMemoryMiB = 8192
	client.defaults.SystemReserveMinimumMiB = 1024
	client.defaults.SystemReserveRecommendedMiB = 2048
	client.defaults.JavaMemory = "4G"
	client.defaults.ContainerMemory = "6G"
	return client
}

func settingsFormValues() url.Values {
	return url.Values{
		"csrf":                 {"csrf-token"},
		"java_memory":          {"4G"},
		"container_memory":     {"6G"},
		"java_port":            {"25565"},
		"bedrock_enabled":      {"on"},
		"bedrock_port":         {"19132"},
		"timezone":             {"America/Toronto"},
		"max_players":          {"20"},
		"motd":                 {"Family Minecraft"},
		"image_tag":            {"stable"},
		"version_policy":       {"pinned"},
		"version":              {"1.21.8"},
		"game_mode":            {"survival"},
		"backup_keep":          {"7"},
		"backup_schedule":      {"*-*-* 04:30:00"},
		"backup_timer_enabled": {"on"},
	}
}

func httptestResponse(app *App, request *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, request)
	return rr
}

// Shared response helpers for page tests.
func legacyPageTestResponse(app *App, request *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	legacyPageTestServe(app, rr, request)
	return rr
}

func legacyPageTestServe(app *App, rr *httptest.ResponseRecorder, request *http.Request) {
	app.Handler().ServeHTTP(rr, request)
}
