package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type addServerTestAPI struct {
	*fakeDiscoveryAPI
	instances    []api.RegisteredMinecraftInstance
	runtimes     map[string]api.InstanceRuntimeSettings
	backups      map[string]api.InstanceBackupSettings
	created      []api.CreateMinecraftInstanceRequest
	backupWrites []api.InstanceBackupSettings
	backupIDs    []string
	createResult api.CreateMinecraftInstanceResponse
	createErr    error
	backupErr    error
}

func (f *addServerTestAPI) MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error) {
	return f.instances, nil
}
func (f *addServerTestAPI) MinecraftInstanceRuntimes(context.Context, string) ([]string, error) {
	ids := []string{}
	for id := range f.runtimes {
		ids = append(ids, id)
	}
	return ids, nil
}
func (f *addServerTestAPI) MinecraftInstanceConfiguration(_ context.Context, _, id string) (api.InstanceRuntimeSettings, error) {
	return f.runtimes[id], nil
}
func (f *addServerTestAPI) MinecraftInstanceBackupSettings(_ context.Context, _, id string) (api.InstanceBackupSettings, error) {
	return f.backups[id], nil
}
func (f *addServerTestAPI) CreateMinecraftInstance(_ context.Context, _ string, request api.CreateMinecraftInstanceRequest) (api.CreateMinecraftInstanceResponse, error) {
	f.created = append(f.created, request)
	if f.createErr != nil {
		return f.createResult, f.createErr
	}
	f.instances = append(f.instances, api.RegisteredMinecraftInstance{ID: f.createResult.ID, DisplayName: request.DisplayName})
	return f.createResult, nil
}
func (f *addServerTestAPI) SetMinecraftInstanceBackupSettings(_ context.Context, _, id string, settings api.InstanceBackupSettings) error {
	f.backupIDs = append(f.backupIDs, id)
	f.backupWrites = append(f.backupWrites, settings)
	return f.backupErr
}
func newAddServerTestApp(t *testing.T) (*App, *addServerTestAPI) {
	t.Helper()
	discovery := setupWizardClient()
	discovery.configuration.Configured = true
	discovery.configuration.Minecraft = api.AdminMinecraftConfiguration{JavaPort: 25565, BedrockEnabled: true, BedrockPort: 19132, ContainerMemory: "2G", DataPath: "/private/minecraft"}
	discovery.configuration.Backup.Path = "/private/backups"
	discovery.configuration.Backup.TimerEnabled = true
	discovery.configuration.Backup.Schedule = "*-*-* 04:30:00"
	client := &addServerTestAPI{fakeDiscoveryAPI: discovery,
		instances:    []api.RegisteredMinecraftInstance{{ID: "jv-legacy1", DisplayName: "Original"}},
		runtimes:     map[string]api.InstanceRuntimeSettings{"jv-other01": {JavaPort: 25566, BedrockEnabled: true, BedrockPort: 19133, ContainerMemory: "1G"}},
		backups:      map[string]api.InstanceBackupSettings{"jv-other01": {Enabled: true, Time: "05:00", Retention: 7}},
		createResult: api.CreateMinecraftInstanceResponse{OK: true, ID: "jv-created1", State: "stopped"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		addServerDrafts.delete(app, "session-token")
		addServerReviews.delete(app, "session-token")
		firstRunSetupDrafts.delete(app, "session-token")
		firstRunSetupReviews.delete(app, "session-token")
	})
	return app, client
}
func addWizardRequest(app *App, method, suffix string, values url.Values) *httptest.ResponseRecorder {
	if values == nil {
		values = url.Values{}
	}
	if method == http.MethodPost {
		values.Set("csrf", "csrf-token")
	}
	return httptestResponse(app, authenticatedAdminRequest(method, "http://example"+addServerBasePath+suffix, values.Encode()))
}
func requireAddRedirect(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", response.Code, response.Body.String())
	}
}
func recommendedAddReview(t *testing.T, app *App, software string) setupReviewState {
	t.Helper()
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/recommended", url.Values{"display_name": {"Family Survival"}, "server_type": {software}}))
	page := addWizardRequest(app, http.MethodGet, "/review", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("review: %d %s", page.Code, page.Body.String())
	}
	state, ok := addServerReviews.get(app, "session-token")
	if !ok {
		t.Fatal("missing independent review")
	}
	return state
}
func addCreateValues(state setupReviewState) url.Values {
	return url.Values{"plan_fingerprint": {state.Plan.PlanFingerprint}, "eula_accepted": {"on"}}
}

func TestAddServerEntryAndSharedRecommended(t *testing.T) {
	app, client := newAddServerTestApp(t)
	workspace := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/minecraft-servers", ""))
	body := workspace.Body.String()
	if workspace.Code != http.StatusOK || !strings.Contains(body, `class="panel minecraft-add-server-card" href="/minecraft/servers/add" aria-label="Add Minecraft Server"`) {
		t.Fatalf("entry missing: %d %s", workspace.Code, body)
	}
	if strings.Count(body, "minecraft-add-server-card") != 1 || strings.Contains(body, "data-minecraft-action") {
		t.Fatal("entry must be the only Phase 5 card")
	}
	legacy := draftFromSetupDefaults(client.defaults, api.AdminStorageDiscovery{})
	firstRunSetupDrafts.save(app, "session-token", legacy)
	before, _ := firstRunSetupDrafts.get(app, "session-token")
	landing := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	for _, want := range []string{"Recommended setup", "Advanced setup", `name="display_name"`, `value="paper" checked`, "server-software-card", `action="/minecraft/servers/add/recommended"`} {
		if !strings.Contains(landing, want) {
			t.Fatalf("shared landing missing %q", want)
		}
	}
	state := recommendedAddReview(t, app, "paper")
	if state.InstanceRuntime.JavaPort != 25567 || state.InstanceRuntime.BedrockPort != 19134 || !state.InstanceRuntime.BedrockEnabled {
		t.Fatalf("unsafe defaults: %+v", state.InstanceRuntime)
	}
	if !separatedBackupTimes(state.InstanceBackup.Time, "04:30") || !separatedBackupTimes(state.InstanceBackup.Time, "05:00") {
		t.Fatal("backup default overlaps an existing schedule")
	}
	if state.InstanceRuntime.ImageTag != "stable" || state.InstanceRuntime.Version != "1.21.8" || state.InstanceRuntime.Timezone != client.defaults.Timezone {
		t.Fatal("recommended version/channel/timezone mapping")
	}
	if state.InstanceRuntime.MOTD == "Family Survival" || !strings.Contains(state.InstanceRuntime.MOTD, "Paper Minecraft") {
		t.Fatal("friendly name used as MOTD")
	}
	after, _ := firstRunSetupDrafts.get(app, "session-token")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Add Server changed the first-run draft")
	}
	page := addWizardRequest(app, http.MethodGet, "/review", nil).Body.String()
	for _, forbidden := range []string{"/private/", "RCON", "Quadlet", "data-instance-preflight", "Download configuration", "jv-created1"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("private or legacy controls exposed: %s", forbidden)
		}
	}
	for _, want := range []string{"Family Survival", "Create Server", "Existing JustVoxel Minecraft storage", "Start on Boot", "setup-review-summary"} {
		if !strings.Contains(page, want) {
			t.Fatalf("review missing %s", want)
		}
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/cancel", nil))
	after, _ = firstRunSetupDrafts.get(app, "session-token")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Cancel changed the first-run draft")
	}
}

func TestAddServerAdvancedSixStepsAndFixedDestinations(t *testing.T) {
	app, client := newAddServerTestApp(t)
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	server := validServerValues()
	server.Set("display_name", "Family Survival")
	server.Set("motd", "Welcome, friends!")
	server.Set("motd_automatic", "false")
	server.Set("start_on_boot", "on")
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/server", server))
	connections := url.Values{"java_port": {"25567"}, "bedrock_port": {"19134"}, "image_tag": {"stable"}, "version_policy": {"recommended"}, "bedrock_enabled": {"on"}, "direction": {"next"}}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/connections", connections))
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/resources", url.Values{"java_memory": {"1G"}, "container_memory": {"2G"}, "direction": {"next"}}))
	draft, _ := addServerDrafts.get(app, "session-token")
	if draft.CurrentStep != 4 || len(setupWizardSteps) != 6 || draft.OtherMemoryMiB != 3072 {
		t.Fatal("six-step flow or configured memory changed")
	}
	storage := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	if !strings.Contains(storage, "Existing JustVoxel Minecraft storage — selected") || strings.Contains(storage, "data-setup-storage-form") || strings.Contains(storage, "storage_device") || strings.Contains(storage, "data-storage-disk") {
		t.Fatal("independent storage is editable or lacks selected text")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/storage", url.Values{"direction": {"back"}}))
	draft, _ = addServerDrafts.get(app, "session-token")
	if draft.CurrentStep != 3 {
		t.Fatal("Storage Back")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/resources", url.Values{"java_memory": {"1G"}, "container_memory": {"2G"}, "direction": {"next"}}))
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/storage", url.Values{"storage_path": {"/malicious"}, "direction": {"next"}}))
	backup := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	if !strings.Contains(backup, `name="backup_daily_time"`) || strings.Contains(backup, "data-setup-backup-choice") || strings.Contains(backup, "backup_path") {
		t.Fatal("backup settings not shared or destination provisioning exposed")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/backups", url.Values{"backup_daily_time": {"06:00"}, "backup_keep": {"9"}, "backup_automatic": {"on"}, "direction": {"next"}}))
	review := addWizardRequest(app, http.MethodGet, "/review", nil)
	if review.Code != 200 {
		t.Fatalf("review: %s", review.Body.String())
	}
	state, _ := addServerReviews.get(app, "session-token")
	if state.InstanceRuntime.MOTD != "Welcome, friends!" || state.DisplayName != "Family Survival" || state.InstanceBackup.Retention != 9 {
		t.Fatal("name/MOTD/backup mapping")
	}
	if client.storageHit != 0 {
		t.Fatal("Add Server queried the first-run partition inventory")
	}
}

func TestAddServerNamesRejectedBeforeCreation(t *testing.T) {
	for _, name := range []string{"", "Original", "original", "..", "Bad/name", "Bad\nname", "Bad\u200bname", strings.Repeat("x", 65)} {
		t.Run(name, func(t *testing.T) {
			app, client := newAddServerTestApp(t)
			response := addWizardRequest(app, http.MethodPost, "/recommended", url.Values{"display_name": {name}, "server_type": {"paper"}})
			if response.Code != http.StatusBadRequest || len(client.created) != 0 {
				t.Fatalf("unsafe/duplicate name accepted: %d", response.Code)
			}
		})
	}
}

func TestAddServerCreateAndBackupOnlyRetry(t *testing.T) {
	app, client := newAddServerTestApp(t)
	state := recommendedAddReview(t, app, "vanilla")
	if state.InstanceRuntime.BedrockEnabled || state.InstanceRuntime.BedrockPort != 0 {
		t.Fatal("Vanilla must not reserve Bedrock port")
	}
	original := client.runtimes["jv-other01"]
	client.backupErr = &api.ResponseError{StatusCode: 409, Message: "Daily backups must start at least 30 minutes apart."}
	result := addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state))
	if result.Code != 409 || !strings.Contains(result.Body.String(), "Server created and left stopped") || !strings.Contains(result.Body.String(), "30 minutes apart") {
		t.Fatalf("partial result hidden: %s", result.Body.String())
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/review/back", nil))
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/backups", url.Values{"backup_daily_time": {"07:30"}, "backup_keep": {"8"}, "backup_automatic": {"on"}, "direction": {"next"}}))
	page := addWizardRequest(app, http.MethodGet, "/review", nil)
	if page.Code != 200 {
		t.Fatalf("backup retry review: %s", page.Body.String())
	}
	state, _ = addServerReviews.get(app, "session-token")
	client.backupErr = nil
	result = addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state))
	requireAddRedirect(t, result)
	if result.Header().Get("Location") != minecraftServersPath+"?created=yes" || len(client.created) != 1 || len(client.backupWrites) != 2 {
		t.Fatal("retry duplicated creation or did not return to servers")
	}
	if client.backupWrites[1].Time != "07:30" || client.backupWrites[1].Retention != 8 || client.backupIDs[1] != "jv-created1" {
		t.Fatal("retry modified another target or lost new backup settings")
	}
	if !reflect.DeepEqual(original, client.runtimes["jv-other01"]) {
		t.Fatal("another server changed")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state)))
	if len(client.created) != 1 {
		t.Fatal("double submit created another server")
	}
}

func TestAddServerCreationFailurePreservesRegistrationAndBlocksDuplicate(t *testing.T) {
	for _, registered := range []bool{false, true} {
		app, client := newAddServerTestApp(t)
		state := recommendedAddReview(t, app, "paper")
		client.createErr = errors.New("connection lost")
		client.createResult = api.CreateMinecraftInstanceResponse{}
		if registered {
			client.createResult.ID = "jv-created1"
		}
		response := addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state))
		if response.Code != 409 || strings.Contains(response.Body.String(), "Minecraft server created.") || len(client.backupWrites) != 0 {
			t.Fatal("failed creation claimed success")
		}
		response = addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state))
		if response.Code != 409 || len(client.created) != 1 {
			t.Fatal("uncertain creation was repeated")
		}
		draft, _ := addServerDrafts.get(app, "session-token")
		if registered && draft.CreatedID != "jv-created1" {
			t.Fatal("registered identity lost")
		}
	}
}

func TestAddServerAuthCSRFAndReviewRequired(t *testing.T) {
	for _, role := range []string{"viewer", "operator"} {
		app, client := newAddServerTestApp(t)
		client.role = role
		response := addWizardRequest(app, http.MethodPost, "/recommended", url.Values{"display_name": {"Family"}, "server_type": {"paper"}})
		if response.Code != 403 || len(client.created) != 0 {
			t.Fatal("non-administrator accessed creation")
		}
	}
	app, client := newAddServerTestApp(t)
	state := recommendedAddReview(t, app, "paper")
	request := authenticatedAdminRequest(http.MethodPost, "http://example"+addServerBasePath+"/review/apply", "plan_fingerprint="+state.Plan.PlanFingerprint)
	if response := httptestResponse(app, request); response.Code != 403 {
		t.Fatal("CSRF not required")
	}
	request = httptest.NewRequest(http.MethodPost, "http://example"+addServerBasePath+"/review/apply", strings.NewReader("csrf=csrf-token"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
	if response := httptestResponse(app, request); response.Code != 303 || response.Header().Get("Location") != "/login" {
		t.Fatal("anonymous request accepted")
	}
	values := addCreateValues(state)
	values.Set("plan_fingerprint", "stale")
	if response := addWizardRequest(app, http.MethodPost, "/review/apply", values); response.Code != 409 {
		t.Fatal("stale review accepted")
	}
	values = addCreateValues(state)
	values.Del("eula_accepted")
	if response := addWizardRequest(app, http.MethodPost, "/review/apply", values); response.Code != 400 {
		t.Fatal("EULA not required")
	}
	if len(client.created) != 0 {
		t.Fatal("unauthorized/unreviewed request mutated instance")
	}
}

func TestAddServerWithoutBackupDestination(t *testing.T) {
	app, client := newAddServerTestApp(t)
	client.configuration.Backup.Path = ""
	state := recommendedAddReview(t, app, "paper")
	if state.InstanceBackup.Enabled {
		t.Fatal("automatic backups falsely configured")
	}
	draft, _ := addServerDrafts.get(app, "session-token")
	draft.Backups.Automatic = true
	if validateAddServerBackups(draft) == nil {
		t.Fatal("unconfigured backup destination allowed")
	}
}

func TestLegacyWizardKeepsEditableDestinationsAndIndependentDraft(t *testing.T) {
	app, err := New(setupWizardStorageClient(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		firstRunSetupDrafts.delete(app, "session-token")
		firstRunSetupReviews.delete(app, "session-token")
		addServerDrafts.delete(app, "session-token")
	})
	independent := setupDraft{AddServer: true, DisplayName: "Independent draft"}
	addServerDrafts.save(app, "session-token", independent)
	before, _ := addServerDrafts.get(app, "session-token")
	advanceToStorage(t, app)
	storage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", "")).Body.String()
	for _, want := range []string{`action="/setup/storage"`, "data-setup-storage-form", "data-setup-storage-internal", "storage_device"} {
		if !strings.Contains(storage, want) {
			t.Fatalf("legacy storage lost %s", want)
		}
	}
	response := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", "csrf=csrf-token&storage_type=system&direction=next"))
	requireAddRedirect(t, response)
	backups := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", "")).Body.String()
	for _, want := range []string{`action="/setup/backups"`, "data-setup-backup-choice", "NFS source", "SMB share"} {
		if !strings.Contains(backups, want) {
			t.Fatalf("legacy backups lost %s", want)
		}
	}
	after, _ := addServerDrafts.get(app, "session-token")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("legacy setup changed the independent draft")
	}
}
