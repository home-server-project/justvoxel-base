package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeMinecraftWorkspaceAPI struct {
	fakeDiscoveryAPI
	planned          api.AdminConfigurationChangeRequest
	applied          api.AdminConfigurationChangeRequest
	planResult       api.AdminConfigurationChangeResponse
	applyResult      api.AdminConfigurationChangeResponse
	whitelist        string
	whitelistChange  api.TextOutputResponse
	whitelistRequest [3]string
	logs             []string
}

func (f *fakeMinecraftWorkspaceAPI) AdminConfigurationPlan(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.planned = request
	if f.planResult.OK || len(f.planResult.Changes) > 0 || f.planResult.ConfirmationRequired {
		return f.planResult, nil
	}
	return api.AdminConfigurationChangeResponse{OK: true, Proposed: f.configuration}, nil
}

func (f *fakeMinecraftWorkspaceAPI) AdminConfigurationApply(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.applied = request
	if f.applyResult.OK || f.applyResult.Applied || f.applyResult.ConfirmationRequired {
		return f.applyResult, nil
	}
	return api.AdminConfigurationChangeResponse{OK: true, Applied: true, Proposed: f.configuration}, nil
}

func (f *fakeMinecraftWorkspaceAPI) Whitelist(_ context.Context, session string) (api.TextOutputResponse, error) {
	if session != "session-token" {
		return api.TextOutputResponse{}, api.ErrUnauthorized
	}
	return api.TextOutputResponse{Output: f.whitelist}, nil
}

func (f *fakeMinecraftWorkspaceAPI) WhitelistChange(_ context.Context, session, platform, action, name string) (api.TextOutputResponse, error) {
	if session != "session-token" {
		return api.TextOutputResponse{}, api.ErrUnauthorized
	}
	f.whitelistRequest = [3]string{platform, action, name}
	return f.whitelistChange, nil
}

func (f *fakeMinecraftWorkspaceAPI) MinecraftLogs(_ context.Context, session string, limit int) (api.LogsResponse, error) {
	if session != "session-token" {
		return api.LogsResponse{}, api.ErrUnauthorized
	}
	if limit != 100 {
		return api.LogsResponse{}, &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "unexpected log limit"}
	}
	return api.LogsResponse{Lines: f.logs}, nil
}

func configuredMinecraftWorkspaceAPI() *fakeMinecraftWorkspaceAPI {
	client := &fakeMinecraftWorkspaceAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.JavaMemory = "4G"
	client.configuration.Minecraft.ContainerMemory = "6G"
	client.configuration.Minecraft.JavaPort = 25565
	client.configuration.Minecraft.BedrockEnabled = true
	client.configuration.Minecraft.BedrockPort = 19132
	client.configuration.Minecraft.Timezone = "America/Toronto"
	client.configuration.Minecraft.MaxPlayers = 10
	client.configuration.Minecraft.MOTD = "JustVoxel"
	client.configuration.Minecraft.ImageTag = "stable"
	client.configuration.Minecraft.VersionMode = "pinned"
	client.configuration.Minecraft.Version = "1.21.8"
	client.configuration.Backup.Keep = 7
	client.configuration.Backup.Schedule = "*-*-* 04:30:00"
	client.configuration.Backup.TimerEnabled = true
	client.defaults.SystemMemoryMiB = 16384
	client.defaults.SystemReserveMinimumMiB = 1024
	client.defaults.SystemReserveRecommendedMiB = 2048
	return client
}

func TestMinecraftWorkspaceSettingsUsesNativeJSONDiscovery(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/settings", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"configured":true`, `"java_memory":"4G"`, `"max_players":10`, `"system_memory_mib":16384`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("settings response missing %q: %s", want, rr.Body.String())
		}
	}
	if client.configurationHit != 1 || client.defaultsHit != 1 {
		t.Fatalf("configuration calls=%d defaults calls=%d", client.configurationHit, client.defaultsHit)
	}
}

func TestMinecraftWorkspaceMemoryPlanPreservesOtherCurrentSettings(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.planResult = api.AdminConfigurationChangeResponse{OK: true}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&tab=memory&java_memory=6G&container_memory=8G"
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.planned.JavaMemory != "6G" || client.planned.ContainerMemory != "8G" {
		t.Fatalf("memory fields not applied to request: %#v", client.planned)
	}
	if client.planned.MaxPlayers != 10 || client.planned.MOTD != "JustVoxel" || client.planned.JavaPort != 25565 {
		t.Fatalf("unrelated Minecraft settings were not preserved: %#v", client.planned)
	}
	if client.planned.BackupKeep != 7 || client.planned.BackupSchedule != "*-*-* 04:30:00" || !client.planned.BackupTimerEnabled {
		t.Fatalf("backup settings were not preserved: %#v", client.planned)
	}
}

func TestMinecraftWorkspaceCrossplayPlanCanDisableBedrockWithoutChangingOtherTabs(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.planResult = api.AdminConfigurationChangeResponse{OK: true}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&tab=crossplay&java_port=25566&bedrock_port=19133"
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.planned.JavaPort != 25566 || client.planned.BedrockPort != 19133 || client.planned.BedrockEnabled {
		t.Fatalf("cross-play request not mapped correctly: %#v", client.planned)
	}
	if client.planned.JavaMemory != "4G" || client.planned.ImageTag != "stable" || client.planned.MaxPlayers != 10 {
		t.Fatalf("cross-play update changed unrelated fields: %#v", client.planned)
	}
}

func TestMinecraftWorkspaceSettingsRejectOperatorBeforeAdminDiscovery(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.role = "operator"
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/settings", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator settings status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.configurationHit != 0 || client.defaultsHit != 0 {
		t.Fatalf("privileged discovery ran for operator: configuration=%d defaults=%d", client.configurationHit, client.defaultsHit)
	}
}

func TestMinecraftWorkspaceOperatorWhitelistAndLogsAreNativeJSON(t *testing.T) {
	client := configuredMinecraftWorkspaceAPI()
	client.role = "operator"
	client.whitelist = "Alex\nSteve"
	client.whitelistChange = api.TextOutputResponse{Output: "updated"}
	client.logs = []string{"line one", "line two"}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/whitelist", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Alex") {
		t.Fatalf("whitelist status=%d body=%s", rr.Code, rr.Body.String())
	}

	body := "csrf=csrf-token&platform=bedrock&action=add&name=PlayerOne"
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/whitelist", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("whitelist change status=%d body=%s", rr.Code, rr.Body.String())
	}
	if client.whitelistRequest != [3]string{"bedrock", "add", "PlayerOne"} {
		t.Fatalf("unexpected whitelist request: %#v", client.whitelistRequest)
	}

	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/logs", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "line one") {
		t.Fatalf("logs status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMinecraftWorkspaceClientDoesNotRenderLegacyPages(t *testing.T) {
	sourceBytes, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	start := strings.Index(source, `const minecraftOpen = document.querySelector("[data-minecraft-open]");`)
	end := strings.Index(source, `const systemWorkspaceOpen = document.querySelector("[data-system-open]");`)
	if start < 0 || end <= start {
		t.Fatal("Minecraft workspace source block not found")
	}
	block := source[start:end]

	for _, forbidden := range []string{"/settings/server", "/operations", "DOMParser", "settings.css", "settings.js"} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("Minecraft workspace still depends on legacy rendering path %q", forbidden)
		}
	}
	for _, required := range []string{
		"/api/dashboard-status",
		"/api/minecraft/workspace/settings",
		"/api/minecraft/workspace/settings/plan",
		"/api/minecraft/workspace/settings/apply",
		"/api/minecraft/workspace/whitelist",
		"/api/minecraft/workspace/logs",
	} {
		if !strings.Contains(block, required) {
			t.Fatalf("Minecraft workspace missing native endpoint %q", required)
		}
	}

	headerBytes, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(headerBytes), "data-minecraft-workspace-csrf") {
		t.Fatal("Minecraft workspace is missing its explicit CSRF source")
	}
}
