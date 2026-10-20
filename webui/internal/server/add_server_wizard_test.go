package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type addServerTestAPI struct {
	states map[string]string
	*fakeSetupExecutionAPI
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
	discovery := setupWizardStorageClient()
	discovery.configuration.Configured = true
	discovery.configuration.Minecraft = api.AdminMinecraftConfiguration{JavaPort: 25565, BedrockEnabled: true, BedrockPort: 19132, ContainerMemory: "2G", DataPath: "/private/minecraft"}
	discovery.configuration.Backup.Path = "/private/backups"
	discovery.configuration.Backup.TimerEnabled = true
	discovery.configuration.Backup.Schedule = "*-*-* 04:30:00"
	client := &addServerTestAPI{fakeSetupExecutionAPI: &fakeSetupExecutionAPI{fakeSetupPlanningAPI: &fakeSetupPlanningAPI{fakeDiscoveryAPI: discovery}},
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
	if state.Plan.Normalized.Minecraft.JavaPort != 25567 || state.Plan.Normalized.Minecraft.BedrockPort != 19134 || !state.Plan.Normalized.Server.BedrockEnabled {
		t.Fatalf("unsafe defaults: %+v", state.Plan.Normalized.Minecraft)
	}
	if !separatedBackupTimes(state.Plan.Normalized.Backups.DailyTime, "04:30") || !separatedBackupTimes(state.Plan.Normalized.Backups.DailyTime, "05:00") {
		t.Fatal("backup default overlaps an existing schedule")
	}
	if state.Plan.Normalized.Minecraft.ImageTag != "stable" || state.Plan.Normalized.Minecraft.Version != "1.21.8" || state.Plan.Normalized.Server.Timezone != client.defaults.Timezone {
		t.Fatal("recommended version/channel/timezone mapping")
	}
	if state.Plan.Normalized.Server.MOTD == "Family Survival" || !strings.Contains(state.Plan.Normalized.Server.MOTD, "Paper Minecraft") {
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
	for _, want := range []string{"Family Survival", "Create Server", "JustVoxel system storage", "Start on Boot", "setup-review-summary"} {
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

func TestAdvancedWizardSystemDefaultsLifecycle(t *testing.T) {
	for _, addServer := range []bool{false, true} {
		name, basePath := "first-run", "/setup"
		if addServer {
			name, basePath = "add-server", addServerBasePath
		}
		t.Run(name, func(t *testing.T) {
			app, client := newAddServerTestApp(t)
			client.configuration.Configured = addServer
			// No existing schedules: the normal default time needs no adjustment.
			client.configuration.Backup.TimerEnabled = false
			client.backups = map[string]api.InstanceBackupSettings{}
			store := &firstRunSetupDrafts
			if addServer {
				store = &addServerDrafts
			}
			assertDefaults := func(draft setupDraft) {
				t.Helper()
				if draft.Storage != initialSetupStorageDraft(draft.Defaults) || draft.Backups != initialSetupBackupDraft(draft.Defaults) {
					t.Fatalf("normal uncompleted defaults lost: storage=%#v backups=%#v", draft.Storage, draft.Backups)
				}
			}
			if addServer {
				draft, err := app.newAddServerDraft(context.Background(), "session-token", client)
				if err != nil {
					t.Fatal(err)
				}
				assertDefaults(draft)
			}
			post := func(suffix string, values url.Values) {
				t.Helper()
				if values == nil {
					values = url.Values{}
				}
				values.Set("csrf", "csrf-token")
				requireAddRedirect(t, httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example"+basePath+suffix, values.Encode())))
			}
			post("/start", nil)
			draft, exists := store.get(app, "session-token")
			if !exists || draft.Mode != "advanced" {
				t.Fatal("Advanced draft was not saved")
			}
			assertDefaults(draft)
			server := validServerValues()
			server.Set("display_name", "Family Survival")
			post("/server", server)
			draft, _ = store.get(app, "session-token")
			assertDefaults(draft)
			post("/connections", url.Values{"java_port": {"25567"}, "bedrock_port": {"19134"}, "image_tag": {"stable"}, "version_policy": {"recommended"}, "bedrock_enabled": {"on"}, "direction": {"next"}})
			draft, _ = store.get(app, "session-token")
			assertDefaults(draft)
			post("/resources", url.Values{"java_memory": {"1G"}, "container_memory": {"2G"}, "direction": {"next"}})
			page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example"+basePath, ""))
			draft, _ = store.get(app, "session-token")
			assertDefaults(draft)
			if page.Code != http.StatusOK || draft.CurrentStep != setupStorageStep {
				t.Fatalf("Storage not reached: %d %s", page.Code, page.Body.String())
			}
			for _, want := range []string{`name="storage_type" value="system"`, `name="storage_path" value="` + draft.Defaults.DataPath + `"`} {
				if !strings.Contains(page.Body.String(), want) {
					t.Fatalf("rendered default missing %q", want)
				}
			}
			post("/storage", url.Values{"storage_type": {draft.Storage.Type}, "storage_path": {draft.Storage.Path}, "direction": {"next"}})
			draft, _ = store.get(app, "session-token")
			if draft.CurrentStep != setupBackupsStep || !draft.Storage.Complete || draft.Backups != initialSetupBackupDraft(draft.Defaults) {
				t.Fatalf("untouched System selection did not advance with backup defaults: %#v", draft)
			}
		})
	}
}

func TestAddServerAdvancedSixStepsAndManualDestinations(t *testing.T) {
	app, client := newAddServerTestApp(t)
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	server := validServerValues()
	server.Set("display_name", "Family Survival")
	server.Set("start_on_boot", "on")
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/server", server))
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/connections", url.Values{"java_port": {"25567"}, "bedrock_port": {"19134"}, "image_tag": {"stable"}, "version_policy": {"recommended"}, "bedrock_enabled": {"on"}, "direction": {"next"}}))
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/resources", url.Values{"java_memory": {"1G"}, "container_memory": {"2G"}, "direction": {"next"}}))
	storage := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	draft, _ := addServerDrafts.get(app, "session-token")
	if draft.Storage.Type != "system" || draft.Storage.Path != draft.Defaults.DataPath || draft.Storage.Complete ||
		draft.Backups.Type != "system" || draft.Backups.Path != draft.Defaults.BackupPath || draft.Backups.Complete ||
		draft.Backups.Keep != strconv.Itoa(draft.Defaults.BackupKeep) || !addServerBackupTimePattern.MatchString(draft.Backups.DailyTime) {
		t.Fatalf("Advanced must retain normal uncompleted defaults: storage=%#v backups=%#v", draft.Storage, draft.Backups)
	}
	if !strings.Contains(storage, `value="system" data-setup-storage-type`) || !strings.Contains(storage, `setup-storage-choice is-selected`) {
		t.Fatal("System storage must initially be selected")
	}
	for _, want := range []string{"data-setup-storage-form", "data-setup-storage-internal", `name="storage_device"`, "data-storage-disk"} {
		if !strings.Contains(storage, want) {
			t.Fatal("shared manual control missing", want)
		}
	}
	if strings.Contains(storage, "Existing JustVoxel Minecraft storage — selected") {
		t.Fatal("fixed data restriction remains")
	}
	if strings.Count(storage, "data-setup-storage-form") != 1 || strings.Count(storage, `class="setup-number"`) != 6 {
		t.Fatal("duplicate controls or changed flow")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/storage", url.Values{"storage_type": {"partition"}, "storage_device": {"/dev/vdb1"}, "storage_mount_point": {"/srv/data"}, "storage_path": {"/srv/data/minecraft"}}))
	backup := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	for _, kind := range []string{"system", "partition", "nfs", "smb"} {
		if !strings.Contains(backup, `data-setup-backup-choice="`+kind+`"`) {
			t.Fatal("backup control missing", kind)
		}
	}
	if strings.Contains(backup, "Existing appliance backup destination") {
		t.Fatal("fixed backup restriction remains")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/backups", url.Values{"backup_type": {"nfs"}, "backup_source": {"nas:/archives"}, "backup_mount_point": {"/var/mnt/family-backups"}, "backup_path": {"/var/mnt/family-backups/backups"}, "backup_daily_time": {"06:00"}, "backup_keep": {"9"}, "backup_automatic": {"on"}}))
	draft, _ = addServerDrafts.get(app, "session-token")
	if draft.Storage.Path != "/srv/data/minecraft" || draft.Backups.Source != "nas:/archives" || draft.CurrentStep != 6 {
		t.Fatal("manual placement lost in draft")
	}
	page := addWizardRequest(app, http.MethodGet, "/review", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), draft.Storage.Path) || !strings.Contains(page.Body.String(), draft.Backups.Path) {
		t.Fatal("manual destinations missing in Review", page.Body.String())
	}
	state, _ := addServerReviews.get(app, "session-token")
	client.applyResponse = api.AdminSetupApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{OperationID: setupExecutionOperationID}}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state)))
	if client.applyRequest.Request.Storage != state.Request.Storage || client.applyRequest.Request.Backups != state.Request.Backups || !client.applyRequest.Request.AddServer || client.applyRequest.Request.MountedOnly {
		t.Fatal("manual selection did not reach shared instance creation")
	}
	if len(client.created) != 0 || len(client.backupWrites) != 0 {
		t.Fatal("duplicate creation backend used")
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

func TestAddServerCreationUsesPersistentSetupAndCannotDuplicate(t *testing.T) {
	app, client := newAddServerTestApp(t)
	state := recommendedAddReview(t, app, "vanilla")
	original := client.runtimes["jv-other01"]
	client.applyResponse = api.AdminSetupApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{OperationID: setupExecutionOperationID}}
	client.applyCurrent = &api.PersistentOperation{OperationID: setupExecutionOperationID, PlanFingerprint: state.Plan.PlanFingerprint, State: "running"}
	response := addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state))
	requireAddRedirect(t, response)
	if response.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID || client.applyCalls != 1 || len(client.created) != 0 {
		t.Fatal("creation did not use durable shared transaction")
	}
	if !client.applyRequest.Request.MountedOnly || !client.applyRequest.Request.AddServer {
		t.Fatal("recommended placement contract missing")
	}
	if !reflect.DeepEqual(original, client.runtimes["jv-other01"]) {
		t.Fatal("sibling changed")
	}
	if response := addWizardRequest(app, http.MethodPost, "/start", nil); response.Code != 409 {
		t.Fatal("submitted draft could create another instance")
	}
}

func TestAddServerUncertainCreationRetainsPersistentRecovery(t *testing.T) {
	app, client := newAddServerTestApp(t)
	state := recommendedAddReview(t, app, "paper")
	client.applyErr = errors.New("connection lost")
	client.applyCurrent = &api.PersistentOperation{OperationID: setupExecutionOperationID, PlanFingerprint: state.Plan.PlanFingerprint, State: "needs_attention", Stage: "runtime_rollback"}
	response := addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state))
	requireAddRedirect(t, response)
	if response.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID {
		t.Fatal("uncertain result lost existing recovery")
	}
	response = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recover", "csrf=csrf-token&operation_id="+setupExecutionOperationID))
	requireAddRedirect(t, response)
	if client.recoverCalls != 1 {
		t.Fatal("configured appliance blocked additional-instance recovery")
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

func TestAddServerWithoutGlobalBackupDestinationResolvesItsOwn(t *testing.T) {
	app, client := newAddServerTestApp(t)
	client.configuration.Backup.Path = ""
	state := recommendedAddReview(t, app, "paper")
	if !state.Plan.Normalized.Backups.Automatic || state.Plan.Normalized.Backups.Path != client.defaults.BackupPath {
		t.Fatal("new instance inherited missing global backup policy")
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

func TestMinecraftServersAddTileUsesApplianceConfiguration(t *testing.T) {
	for _, configured := range []bool{false, true} {
		label, target := "first-setup", "/setup"
		if configured {
			label, target = "configured", addServerBasePath
		}
		t.Run(label, func(t *testing.T) {
			app, client := newAddServerTestApp(t)
			client.configuration.Configured = configured
			client.instances = nil
			client.runtimes = nil
			response := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/minecraft-servers", ""))
			want := `class="panel minecraft-add-server-card" href="` + target + `"`
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) || client.configurationHit == 0 {
				t.Fatalf("tile must use authoritative configuration: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "<!doctype") {
				t.Fatal("Minecraft Servers must remain a dashboard workspace fragment")
			}
		})
	}
}

func TestUnconfiguredAddServerRedirectsGETAndRefusesPOST(t *testing.T) {
	app, client := newAddServerTestApp(t)
	// Preserve a stale reviewed Add Server draft from before the appliance became
	// unconfigured. A POST must never apply it through either creation path.
	state := recommendedAddReview(t, app, "paper")
	before, _ := addServerDrafts.get(app, "session-token")
	client.configuration.Configured = false
	response := addWizardRequest(app, http.MethodGet, "", nil)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/setup" {
		t.Fatalf("first-run GET: %d %v", response.Code, response.Header())
	}
	for _, suffix := range []string{"/start", "/recommended", "/server", "/review/apply", "/cancel"} {
		response := addWizardRequest(app, http.MethodPost, suffix, addCreateValues(state))
		if response.Code != http.StatusConflict || response.Header().Get("Location") != "" || !strings.Contains(response.Body.String(), "Complete appliance setup before adding a server.") {
			t.Fatalf("stale POST %s: %d %s", suffix, response.Code, response.Body.String())
		}
	}
	malformed := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example"+addServerBasePath+"/review/apply", "csrf=invalid&plan_fingerprint=stale"))
	if malformed.Code != http.StatusForbidden || malformed.Header().Get("Location") != "" {
		t.Fatalf("malformed POST did not fail closed: %d", malformed.Code)
	}
	after, _ := addServerDrafts.get(app, "session-token")
	if !reflect.DeepEqual(before, after) || len(client.created) != 0 || len(client.backupWrites) != 0 {
		t.Fatal("unconfigured Add Server mutated state")
	}
	if _, exists := firstRunSetupDrafts.get(app, "session-token"); exists {
		t.Fatal("Add Server POST became first-run setup")
	}
}

func (f *addServerTestAPI) AdminSetupPlan(_ context.Context, _ string, r api.AdminSetupPlanRequest) (api.AdminSetupPlanResponse, error) {
	f.lastRequest = r
	f.planHit++
	if r.Server.MOTDAutomatic {
		r.Server.MOTD = "JustVoxel " + setupServerTypeLabel(r.Minecraft.ServerType) + " Minecraft 1.21.8 Server"
	}
	return api.AdminSetupPlanResponse{OK: true, SchemaVersion: "v1", PlanFingerprint: setupReviewFingerprint, Requirements: api.AdminSetupPlanRequirements{}, Normalized: api.AdminSetupNormalizedPlan{
		AddServer: r.AddServer, StartOnBoot: r.StartOnBoot, StartAfterSetup: r.StartAfterSetup, MountedOnly: r.MountedOnly,
		Server:    api.AdminSetupPlanServer{DisplayName: r.Server.DisplayName, MOTD: r.Server.MOTD, MaxPlayers: r.Server.MaxPlayers, BedrockEnabled: r.Server.BedrockEnabled, Timezone: r.Server.Timezone},
		Minecraft: api.AdminSetupPlanMinecraft{ServerType: r.Minecraft.ServerType, GameMode: r.Minecraft.GameMode, JavaMemory: r.Minecraft.JavaMemory, ContainerMemory: r.Minecraft.ContainerMemory, JavaPort: r.Minecraft.JavaPort, BedrockPort: r.Minecraft.BedrockPort, ImageTag: r.Minecraft.ImageTag, Version: "1.21.8", RequestedVersionPolicy: r.Minecraft.VersionPolicy},
		Storage:   api.AdminSetupPlanStorage{Type: r.Storage.Type, Path: r.Storage.Path, Device: r.Storage.Device, MountPoint: r.Storage.MountPoint},
		Backups:   api.AdminSetupPlanBackups{Type: r.Backups.Type, Path: r.Backups.Path, Device: r.Backups.Device, MountPoint: r.Backups.MountPoint, Source: r.Backups.Source, Automatic: r.Backups.Automatic, DailyTime: r.Backups.DailyTime, Keep: r.Backups.Keep},
	}}, nil
}

func TestAdvancedWizardSharesExactStorageAndBackupMarkup(t *testing.T) {
	app, client := newAddServerTestApp(t)
	draft := draftFromSetupDefaults(client.defaults, client.storage)
	draft.Mode = "advanced"
	for _, step := range []int{setupStorageStep, setupBackupsStep} {
		draft.CurrentStep = step
		normal := httptest.NewRecorder()
		app.renderSetupWizard(normal, api.SessionInfo{}, draft, "csrf-token", "")
		draft.AddServer = true
		additional := httptest.NewRecorder()
		app.renderSetupWizard(additional, api.SessionInfo{}, draft, "csrf-token", "")
		for _, obsolete := range []string{"All servers use the configured JustVoxel Minecraft storage.", "Existing JustVoxel Minecraft storage", "Existing appliance backup destination", "using the appliance backup destination"} {
			if strings.Contains(additional.Body.String(), obsolete) {
				t.Fatalf("obsolete shared-storage wording rendered: %q", obsolete)
			}
		}
		if !strings.Contains(additional.Body.String(), setupWizardSteps[step-1].Description) {
			t.Fatal("Add Server must use the generic step description", step)
		}
		controls := func(markup string) string {
			begin := strings.Index(markup, `<div class="setup-step-content">`)
			end := strings.Index(markup[begin:], `<div class="setup-step-actions">`)
			return markup[begin : begin+end]
		}
		if controls(normal.Body.String()) != controls(additional.Body.String()) {
			t.Fatal("advanced controls diverged for Add Server", step)
		}
		draft.AddServer = false
	}
}

func TestAddServerDefiniteSetupRefusalRemainsEditable(t *testing.T) {
	app, client := newAddServerTestApp(t)
	state := recommendedAddReview(t, app, "paper")
	client.applyErr = &api.ResponseError{StatusCode: 409, Message: "Storage changed; return to Review"}
	response := addWizardRequest(app, http.MethodPost, "/review/apply", addCreateValues(state))
	if response.Code < 400 {
		t.Fatal("refusal claimed success")
	}
	draft, _ := addServerDrafts.get(app, "session-token")
	if draft.CreationAttempted {
		t.Fatal("definite refusal stranded editable setup")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/review/back", nil))
}

func (f *addServerTestAPI) MinecraftInstanceStatus(_ context.Context, _, id string) (api.MinecraftInstanceStatus, error) {
	state := f.states[id]
	if state == "" {
		state = "stopped"
	}
	return api.MinecraftInstanceStatus{ID: id, State: state}, nil
}

func TestAddServerCapacityChoicesAndNoLegacyDoubleCount(t *testing.T) {
	for _, tc := range []struct {
		name, state, existing, candidate string
		boot, afterAllowed, bootAllowed  bool
	}{
		{"VM stopped", "stopped", "6G", "6G", true, true, false},
		{"VM running", "running", "6G", "6G", true, false, false},
		{"VM starting", "starting", "6G", "6G", false, false, true},
		{"safe simultaneous", "running", "4G", "2G", true, true, true},
		{"unsafe boot only", "stopped", "4G", "4G", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, client := newAddServerTestApp(t)
			client.defaults.SystemMemoryMiB = 7867
			client.defaults.JavaMemory, client.defaults.ContainerMemory = "1G", tc.candidate
			client.configuration.Minecraft.ContainerMemory = tc.existing
			client.runtimes = map[string]api.InstanceRuntimeSettings{"jv-other01": {ContainerMemory: tc.existing, StartOnBoot: tc.boot, JavaPort: 25565}}
			client.states = map[string]string{"jv-other01": tc.state}
			draft, err := app.newAddServerDraft(context.Background(), "session-token", client)
			if err != nil {
				t.Fatal(err)
			}
			expected, _ := setupMemoryMiB(tc.existing)
			if draft.OtherMemoryMiB != expected {
				t.Fatalf("first server counted twice: %d, want %d", draft.OtherMemoryMiB, expected)
			}
			if draft.Minecraft.ContainerMemory != tc.candidate {
				t.Fatal("installed server memory reduced candidate")
			}
			if draft.StartAfterSetup != tc.afterAllowed || draft.StartOnBoot != tc.bootAllowed {
				t.Fatalf("independent choices: after=%v boot=%v", draft.StartAfterSetup, draft.StartOnBoot)
			}
			if draft.StartAfterUnavailable == tc.afterAllowed || draft.StartBootUnavailable == tc.bootAllowed {
				t.Fatal("unavailable choices were not disabled")
			}
			if err := validateSetupResources(draft.Minecraft, draft.Defaults); err != nil {
				t.Fatal("installed memory blocked configuration", err)
			}
		})
	}
}

func TestAddServerLegacyMemoryFallbackOnlyWhenConfigured(t *testing.T) {
	for _, configured := range []bool{false, true} {
		app, client := newAddServerTestApp(t)
		client.configuration.Configured = configured
		client.runtimes = nil
		draft, err := app.newAddServerDraft(context.Background(), "session-token", client)
		if err != nil {
			t.Fatal(err)
		}
		expected := 0
		if configured {
			expected = 2048
		}
		if draft.OtherMemoryMiB != expected {
			t.Fatal("incorrect legacy fallback", draft.OtherMemoryMiB)
		}
	}
}

func TestAddServerDisabledStartupSwitchesUseExistingWarnings(t *testing.T) {
	app, client := newAddServerTestApp(t)
	client.defaults.SystemMemoryMiB = 7867
	client.defaults.JavaMemory, client.defaults.ContainerMemory = "4G", "6G"
	client.runtimes = map[string]api.InstanceRuntimeSettings{"jv-other01": {JavaPort: 25565, ContainerMemory: "6G", StartOnBoot: true}}
	client.states = map[string]string{"jv-other01": "running"}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	page := addWizardRequest(app, http.MethodGet, "", nil)
	if page.Code != http.StatusOK {
		t.Fatal(page.Code, page.Body.String())
	}
	body := page.Body.String()
	after, boot := strings.Index(body, "<span>Start after setup</span>"), strings.Index(body, "<span>Start on Boot</span>")
	if after < 0 || boot <= after {
		t.Fatal("incorrect switch order")
	}
	for _, name := range []string{"start_after_setup", "start_on_boot"} {
		start := strings.Index(body, `name="`+name+`"`)
		if start < 0 {
			t.Fatal("missing switch", name)
		}
		input := body[start : start+strings.Index(body[start:], ">")]
		if !strings.Contains(input, "disabled") || strings.Contains(input, "checked") {
			t.Fatal("unavailable switch not disabled/off", input)
		}
	}
	if strings.Count(body, `class="memory-status danger"`) != 2 {
		t.Fatal("missing existing red framed explanations")
	}
}
