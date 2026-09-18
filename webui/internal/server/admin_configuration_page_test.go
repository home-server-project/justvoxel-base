package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
		"backup_keep":          {"7"},
		"backup_schedule":      {"*-*-* 04:30:00"},
		"backup_timer_enabled": {"on"},
	}
}

func TestMinecraftSettingsUsesUserFriendlyMemoryControls(t *testing.T) {
	client := configuredSettingsFake()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/server", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("settings page returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Minecraft game memory", "Technical name: Java heap", "Maximum Minecraft memory", "container memory limit",
		"8.0 GiB detected", "2.0 GiB", "1.0 GiB", "Light", "Recommended", "High memory", "Custom",
		"additional block of 10 players", "/static/settings.js", "Review changes",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings page missing %q", want)
		}
	}
}

func TestMinecraftSettingsPlanShowsReviewWithoutApplying(t *testing.T) {
	client := configuredSettingsFake()
	request := api.AdminConfigurationChangeRequest{
		JavaMemory: "4G", ContainerMemory: "6G", JavaPort: 25565, BedrockEnabled: true, BedrockPort: 19132,
		Timezone: "America/Toronto", MaxPlayers: 20, MOTD: "Family Minecraft", ImageTag: "stable",
		VersionPolicy: "pinned", Version: "1.21.8", BackupKeep: 7, BackupSchedule: "*-*-* 04:30:00", BackupTimerEnabled: true,
	}
	client.planResponse = api.AdminConfigurationChangeResponse{
		OK: true, RestartRequired: true, MemoryRemainingMiB: 2048,
		Changes:  []api.AdminConfigurationChange{{Field: "max_players", Label: "Maximum players", Before: "10", After: "20", RestartRequired: true}},
		Warnings: []string{"This is a tight memory configuration."},
		Proposed: api.AdminConfigurationDiscovery{Configured: true, Minecraft: client.configuration.Minecraft, Backup: client.configuration.Backup},
	}
	client.planResponse.Proposed.Minecraft.MaxPlayers = 20
	client.planResponse.Proposed.Minecraft.BedrockEnabled = true

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := settingsFormValues().Encode()
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/server/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("plan calls=%d apply calls=%d", client.planCalls, client.applyCalls)
	}
	if client.plannedRequest != request {
		t.Fatalf("planned request = %#v, want %#v", client.plannedRequest, request)
	}
	for _, want := range []string{"Proposed changes", "Maximum players", "10", "20", "Minecraft will not restart automatically", "Apply changes"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("review missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestMinecraftSettingsApplyRedirectsWithRestartNotice(t *testing.T) {
	client := configuredSettingsFake()
	client.applyResponse = api.AdminConfigurationChangeResponse{OK: true, Applied: true, RestartRequired: true}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := settingsFormValues().Encode()
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/server/apply", body))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/settings/server?result=saved&restart=1" {
		t.Fatalf("unexpected redirect %q", rr.Header().Get("Location"))
	}
	if client.applyCalls != 1 {
		t.Fatalf("apply calls = %d, want 1", client.applyCalls)
	}
}

func TestMinecraftSettingsOperatorCannotPlanChanges(t *testing.T) {
	client := configuredSettingsFake()
	client.role = "operator"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/server/plan", settingsFormValues().Encode()))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator plan status = %d, want 403", rr.Code)
	}
	if client.planCalls != 0 {
		t.Fatalf("plan API called for operator %d times", client.planCalls)
	}
}

func httptestResponse(app *App, request *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, request)
	return rr
}
