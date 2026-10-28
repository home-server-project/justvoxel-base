package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type phase7API struct {
	*serverInventoryTestAPI
	writes            []string
	players           []string
	backupCalls       []string
	whitelistCalls    []string
	versionCalls      []string
	renames           []string
	deleted           []string
	backupInventoryID string
	putError          error
	deletions         []api.RegisteredMinecraftInstance
	backupRequests    map[string]json.RawMessage
	playerSnapshot    *api.InstancePlayers
	settingsEvents    []string
	stopConfirmation  int
	lifecycleError    error
	confirmations     []bool
}

func (f *phase7API) MinecraftInstanceDeletions(context.Context, string) ([]api.RegisteredMinecraftInstance, error) {
	return f.deletions, nil
}

func (f *phase7API) SetMinecraftInstanceConfiguration(_ context.Context, _, id string, v api.InstanceRuntimeSettings) error {
	f.settingsEvents = append(f.settingsEvents, "apply:"+id)
	f.writes = append(f.writes, id)
	if f.putError != nil {
		return f.putError
	}
	f.runtimes[id] = v
	return nil
}
func (f *phase7API) MinecraftInstancePlayers(_ context.Context, _, id string) (api.InstancePlayers, error) {
	f.players = append(f.players, id)
	if f.playerSnapshot != nil {
		return *f.playerSnapshot, nil
	}
	return api.InstancePlayers{ID: id, State: "offline"}, nil
}
func (f *phase7API) MinecraftInstanceActionConfirmed(_ context.Context, _, id, action string, confirm bool) (api.MinecraftActionResponse, error) {
	f.actions = append(f.actions, id+":"+action)
	f.settingsEvents = append(f.settingsEvents, action+":"+id)
	f.confirmations = append(f.confirmations, confirm)
	if f.lifecycleError != nil {
		return api.MinecraftActionResponse{}, f.lifecycleError
	}
	if action == "stop" && f.stopConfirmation > 0 && !confirm {
		return api.MinecraftActionResponse{ConfirmationRequired: true, Online: f.stopConfirmation}, nil
	}
	state := "running"
	if action == "stop" {
		state = "stopped"
	}
	f.states[id] = api.MinecraftInstanceStatus{ID: id, State: state}
	return api.MinecraftActionResponse{OK: true}, nil
}
func (f *phase7API) MinecraftInstanceLogs(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *phase7API) RenameMinecraftInstance(_ context.Context, _, id, name string) error {
	f.renames = append(f.renames, id)
	for i := range f.instances {
		if f.instances[i].ID == id {
			f.instances[i].DisplayName = name
		}
	}
	return nil
}
func (f *phase7API) DeleteMinecraftInstance(_ context.Context, _, id, name string) error {
	f.deleted = append(f.deleted, id)
	for i, v := range f.instances {
		if v.ID == id {
			f.instances = append(f.instances[:i], f.instances[i+1:]...)
			break
		}
	}
	delete(f.runtimes, id)
	return nil
}
func (f *phase7API) MinecraftInstanceBackups(_ context.Context, _, id, action string, request any) (json.RawMessage, error) {
	f.backupCalls = append(f.backupCalls, id+":"+action)
	if request != nil {
		if f.backupRequests == nil {
			f.backupRequests = make(map[string]json.RawMessage)
		}
		f.backupRequests[action], _ = json.Marshal(request)
	}
	switch action {
	case "restore/plan":
		var input struct {
			Archive string `json:"archive"`
		}
		_ = json.Unmarshal(f.backupRequests[action], &input)
		raw, _ := json.Marshal(map[string]any{"plan_id": strings.Repeat("a", 64), "metadata": map[string]any{"id": id, "archive": input.Archive}, "integrity": "verified", "players_confirmation_required": true, "players": map[string]any{"id": id, "online": 2}})
		return raw, nil
	case "settings":
		return json.RawMessage(`{"enabled":false,"time":"04:00","retention":7}`), nil
	case "inventory":
		owner := id
		if f.backupInventoryID != "" {
			owner = f.backupInventoryID
		}
		b, _ := json.Marshal(map[string]any{"id": id, "backups": []map[string]any{{"id": owner, "archive": "minecraft-family-2026-01-01-1200.tar.gz", "size": 1}}})
		return b, nil
	case "restore/status":
		b, _ := json.Marshal(map[string]any{"id": id, "state": "idle"})
		return b, nil
	default:
		return json.RawMessage(`{"ok":true,"state":"idle"}`), nil
	}
}
func phase7App(t *testing.T) (*App, *phase7API) {
	t.Helper()
	_, base := newServerInventoryApp(t)
	f := &phase7API{serverInventoryTestAPI: base}
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "stopped"}
	app, e := New(f, Config{Version: "test", ManagementAPI: "v1"})
	if e != nil {
		t.Fatal(e)
	}
	return app, f
}
func phase7Request(app *App, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, authenticatedAdminRequest(method, "http://example"+path, body))
	return w
}
func TestSelectedSettingsReviewedOverlayAndIsolation(t *testing.T) {
	for _, body := range []string{"tab=memory&java_memory=5G&container_memory=7G", "tab=gameplay&motd=Family&max_players=12&game_mode=creative", "tab=crossplay&java_port=25578&bedrock_port=19146&bedrock_enabled=on", "tab=overview&start_on_boot=false"} {
		t.Run(body, func(t *testing.T) {
			app, f := phase7App(t)
			before := f.runtimes["jv-family1"]
			before.Timezone = "America/Toronto"
			before.ImageTag = "stable"
			before.PaperChannel = "default"
			before.EULAAccepted = true
			before.Difficulty = "hard"
			f.runtimes["jv-family1"] = before
			sibling := f.runtimes["jv-kids001"]
			body += "&csrf=csrf-token&software=vanilla&version=LATEST&image_tag=bad&timezone=bad"
			plan := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
			if plan.Code != 200 {
				t.Fatal(plan.Code, plan.Body.String())
			}
			if !strings.HasPrefix(body, "tab=overview") && !strings.Contains(plan.Body.String(), "The server stays stopped. Start it normally when ready.") {
				t.Fatal("stopped review message missing", plan.Body.String())
			}
			var review struct {
				Review string `json:"review"`
			}
			if json.Unmarshal(plan.Body.Bytes(), &review) != nil || review.Review == "" {
				t.Fatal("missing review")
			}
			apply := phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+url.QueryEscape(review.Review))
			if apply.Code != 200 {
				t.Fatal(apply.Code, apply.Body.String())
			}
			got := f.runtimes["jv-family1"]
			switch {
			case strings.HasPrefix(body, "tab=memory"):
				if got.JavaMemory != "5G" || got.ContainerMemory != "7G" {
					t.Fatal("memory overlay lost")
				}
			case strings.HasPrefix(body, "tab=gameplay"):
				if got.MOTD != "Family" || got.MaxPlayers != 12 || got.GameMode != "creative" {
					t.Fatal("gameplay overlay lost")
				}
			case strings.HasPrefix(body, "tab=crossplay"):
				if got.JavaPort != 25578 || got.BedrockPort != 19146 || !got.BedrockEnabled {
					t.Fatal("cross-play overlay lost")
				}
			case strings.HasPrefix(body, "tab=overview"):
				if got.StartOnBoot {
					t.Fatal("boot overlay lost")
				}
			}

			if got.Software != before.Software || got.Version != before.Version || got.ImageTag != before.ImageTag || got.PaperChannel != before.PaperChannel || got.Timezone != before.Timezone || got.Difficulty != before.Difficulty || got.EULAAccepted != before.EULAAccepted || got.Whitelist != before.Whitelist {
				t.Fatal("unrelated fields changed")
			}
			if !reflect.DeepEqual(f.writes, []string{"jv-family1"}) || !reflect.DeepEqual(sibling, f.runtimes["jv-kids001"]) || len(f.actions) != 0 {
				t.Fatal("wrong target or automatic lifecycle")
			}
		})
	}
}
func TestSelectedInvalidAndVersionNeverFallBack(t *testing.T) {
	app, f := phase7App(t)
	for _, target := range []string{"bad", "jv-absent1", "", "original&server=jv-family1"} {
		w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server="+target, "csrf=csrf-token&tab=version&version=LATEST")
		if w.Code < 400 {
			t.Fatal(target, w.Body.String())
		}
	}
	if len(f.writes) != 0 || len(f.actions) != 0 {
		t.Fatal("invalid selection mutated runtime")
	}
}
func TestSelectedRunningUnknownPlayersRetainsReview(t *testing.T) {
	app, f := phase7App(t)
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", "csrf=csrf-token&tab=memory&java_memory=4G&container_memory=6G")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"apply_blocked":true`) || !strings.Contains(w.Body.String(), `"review":`) || len(f.actions) != 0 || len(f.writes) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestSelectedPlayersAndWhitelistExactIdentity(t *testing.T) {
	app, f := phase7App(t)
	for _, item := range []struct{ method, path, body string }{{"GET", "snapshot", ""}, {"GET", "whitelist", ""}, {"POST", "whitelist", "csrf=csrf-token&platform=java&action=add&name=Alex"}} {
		w := phase7Request(app, item.method, "/api/minecraft/workspace/"+item.path+"?server=jv-family1", item.body)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if !reflect.DeepEqual(f.players, []string{"jv-family1"}) || !reflect.DeepEqual(f.whitelistCalls, []string{"jv-family1", "jv-family1"}) {
		t.Fatal(f.players, f.whitelistCalls)
	}
}

func TestSelectedRenameKeepsTargetAndCSRF(t *testing.T) {
	app, f := phase7App(t)
	w := phase7Request(app, "POST", "/api/minecraft/workspace/rename?server=jv-family1", "csrf=csrf-token&display_name=Renamed")
	if w.Code != 200 || !reflect.DeepEqual(f.renames, []string{"jv-family1"}) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = phase7Request(app, "POST", "/api/minecraft/workspace/delete?server=jv-family1", "confirm_name=Renamed")
	if w.Code != http.StatusForbidden || len(f.deleted) != 0 {
		t.Fatal("delete missing CSRF accepted")
	}
}
func TestSelectedBackupsReuseAndTarget(t *testing.T) {
	app, f := phase7App(t)
	for _, path := range []string{"/workspace/backups", "/workspace/backups/backup", "/workspace/backups/automatic/apply"} {
		method := "POST"
		if path == "/workspace/backups" {
			method = "GET"
		}
		w := phase7Request(app, method, path+"?server=jv-family1", "csrf=csrf-token&daily_time=04%3A00&backup_keep=7")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "data-backup-delete-selected") || strings.Contains(w.Body.String(), "Change destination") {
			t.Fatal("legacy privileged controls escaped")
		}
	}
	for _, want := range []string{"jv-family1:inventory", "jv-family1:configure", "jv-family1:now", "jv-family1:restore/status"} {
		if !strings.Contains(strings.Join(f.backupCalls, "|"), want) {
			t.Fatal(want)
		}
	}
	f.backupInventoryID = "jv-kids001"
	w := phase7Request(app, "GET", "/workspace/backups?server=jv-family1", "")
	if strings.Contains(w.Body.String(), "data-backup-card ") {
		t.Fatal("foreign backup exposed")
	}
}

func TestSingleRealInstanceMayResolveImplicitSelection(t *testing.T) {
	app, f := phase7App(t)
	f.instances = []api.RegisteredMinecraftInstance{{ID: "jv-family1", DisplayName: "Family"}}
	delete(f.runtimes, "jv-kids001")
	for _, path := range []string{"settings", "snapshot", "whitelist", "selection"} {
		w := phase7Request(app, "GET", "/api/minecraft/workspace/"+path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "original") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if len(f.whitelistCalls) != 1 || f.whitelistCalls[0] != "jv-family1" {
		t.Fatal(f.whitelistCalls)
	}
}

func TestPhase7BackendStoppedOnlyRefusalIsSurfaced(t *testing.T) {
	app, f := phase7App(t)
	body := "csrf=csrf-token&tab=memory&java_memory=4G&container_memory=6G"
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
	var plan struct {
		Review string `json:"review"`
	}
	if json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	f.putError = &api.ResponseError{StatusCode: 409, Message: "Instance must be stopped."}
	w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+plan.Review)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "must be stopped") || len(f.actions) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestVersionReviewAndApplyUseExactSelectedIdentity(t *testing.T) {
	app, f := phase7App(t)
	sibling := f.runtimes["jv-kids001"]
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", "csrf=csrf-token&tab=version&version_policy=pinned&version=1.21.9&image_tag=stable")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = phase7Request(app, "POST", "/api/version/workspace/update?server=jv-family1", "csrf=csrf-token&plan_fingerprint=reviewed&policy=pinned&version=1.21.9")
	if w.Code != 202 || !reflect.DeepEqual(f.versionCalls, []string{"jv-family1:status", "jv-family1:apply"}) || !reflect.DeepEqual(sibling, f.runtimes["jv-kids001"]) {
		t.Fatal(w.Code, w.Body.String(), f.versionCalls)
	}
}

func TestPhase7StaleReviewRequiresReviewAgain(t *testing.T) {
	app, f := phase7App(t)
	body := "csrf=csrf-token&tab=memory&java_memory=4G&container_memory=6G"
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
	var plan struct {
		Review string `json:"review"`
	}
	if json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" {
		t.Fatal("review unavailable")
	}
	c := f.runtimes["jv-family1"]
	c.MOTD = "Another administrator changed this"
	f.runtimes["jv-family1"] = c
	w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+plan.Review)
	if w.Code != 409 || len(f.writes) != 0 {
		t.Fatal("stale review applied", w.Code)
	}
}
func TestDeleteLastServerLeavesZeroServerInventory(t *testing.T) {
	app, f := phase7App(t)
	f.instances = []api.RegisteredMinecraftInstance{{ID: "jv-family1", DisplayName: "Family"}}
	delete(f.runtimes, "jv-kids001")
	w := phase7Request(app, "POST", "/api/minecraft/workspace/delete?server=jv-family1", "csrf=csrf-token&confirm_name=Family")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = phase7Request(app, "GET", "/api/minecraft/workspace/selection", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"servers":[]`) || strings.Contains(w.Body.String(), "original") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPhase7DeleteRetryReachableAfterIdentityRetirement(t *testing.T) {
	app, f := phase7App(t)
	delete(f.runtimes, "jv-family1")
	f.instances = []api.RegisteredMinecraftInstance{{ID: "jv-kids001", DisplayName: "Kids"}}
	f.deletions = []api.RegisteredMinecraftInstance{{ID: "jv-family1", DisplayName: "Family Survival"}}
	f.configurationErrors["jv-family1"] = true
	w := inventoryRequest(app, "GET", "/view/jv-family1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-delete-name="Family Survival"`) || !strings.Contains(w.Body.String(), "/api/minecraft/workspace/delete?server=jv-family1") {
		t.Fatal("Delete retry stranded", w.Code, w.Body.String())
	}
	w = phase7Request(app, "POST", "/api/minecraft/workspace/delete?server=jv-family1", "csrf=csrf-token&confirm_name=Family+Survival")
	if w.Code != 200 || !reflect.DeepEqual(f.deleted, []string{"jv-family1"}) {
		t.Fatal("same-ID retry unavailable", w.Code, w.Body.String())
	}
}

func TestPhase7SelectedRestoreUsesSameIdentityContract(t *testing.T) {
	app, f := phase7App(t)
	archive := "instance-backup-paper-2026-10-15-1200.tar.gz"
	w := phase7Request(app, "POST", "/workspace/backups/restore/plan?server=jv-family1", "csrf=csrf-token&backup_id="+archive+"&mode=full")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "same server identity only") || !strings.Contains(w.Body.String(), strings.Repeat("a", 64)) {
		t.Fatal(w.Code, w.Body.String())
	}
	body := "csrf=csrf-token&backup_id=" + archive + "&plan_fingerprint=" + strings.Repeat("a", 64) + "&players_confirmed=yes"
	w = phase7Request(app, "POST", "/workspace/backups/restore/apply?server=jv-family1", body)
	if _, exists := f.backupRequests["restore/apply"]; exists {
		t.Fatal("Restore applied without destructive confirmation")
	}
	w = phase7Request(app, "POST", "/workspace/backups/restore/apply?server=jv-family1", body+"&confirmation=RESTORE")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var applied struct {
		Archive            string `json:"archive"`
		PlanID             string `json:"plan_id"`
		ConfirmDestructive bool   `json:"confirm_destructive"`
		ConfirmPlayers     bool   `json:"confirm_players"`
	}
	if json.Unmarshal(f.backupRequests["restore/apply"], &applied) != nil || applied.Archive != archive || applied.PlanID != strings.Repeat("a", 64) || !applied.ConfirmDestructive || !applied.ConfirmPlayers {
		t.Fatal("Phase 4B request changed", applied)
	}
	for _, call := range f.backupCalls {
		if !strings.HasPrefix(call, "jv-family1:") {
			t.Fatal("foreign restore target", call)
		}
	}
}

func (f *phase7API) MinecraftInstanceWhitelist(_ context.Context, _, id string, request any) (api.TextOutputResponse, error) {
	f.whitelistCalls = append(f.whitelistCalls, id)
	enabled := f.runtimes[id].Whitelist
	return api.TextOutputResponse{Output: "There are 0 whitelisted player(s):", WhitelistEnabled: &enabled}, nil
}
func (f *phase7API) MinecraftInstanceVersionStatus(_ context.Context, _, id, policy, version, software string, imageTag ...string) (api.AdminVersionStatus, error) {
	f.versionCalls = append(f.versionCalls, id+":status")
	return api.AdminVersionStatus{PlanFingerprint: "sha256:" + strings.Repeat("a", 64), StackState: "updates_available", ServerSupported: true, UpdateAvailable: true, Installed: "1.21.8", SelectedCandidate: "1.21.9", Policy: policy, ImageTag: "stable"}, nil
}
func (f *phase7API) MinecraftInstanceStackUpdate(_ context.Context, _, id string, v api.MinecraftStackUpdateRequest) (api.PersistentOperationResponse, error) {
	f.versionCalls = append(f.versionCalls, id+":apply")
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: id, OperationType: "minecraft_update", OperationID: "12345678-1234-4123-8123-123456789abc"}}, nil
}
func (f *phase7API) MinecraftInstanceStackUpdateOperation(_ context.Context, _, id, op string) (api.PersistentOperationResponse, error) {
	f.versionCalls = append(f.versionCalls, id+":operation")
	return api.PersistentOperationResponse{}, nil
}
func (f *phase7API) MinecraftInstanceAcknowledgeStackUpdate(_ context.Context, _, id, op string) (api.PersistentOperationResponse, error) {
	f.versionCalls = append(f.versionCalls, id+":acknowledge")
	return api.PersistentOperationResponse{}, nil
}

func TestServerSelectorListsOnlyRealImmutableIdentitiesWithReadOnlyDisplayMetadata(t *testing.T) {
	app, f := phase7App(t)
	w := phase7Request(app, "GET", "/api/minecraft/workspace/selection", "")
	var out struct {
		Selected selectedServerContext   `json:"selected"`
		Servers  []selectedServerContext `json:"servers"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Selected.ID != "" || len(out.Servers) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, choice := range out.Servers {
		if choice.ID != "jv-family1" && choice.ID != "jv-kids001" {
			t.Fatal("synthetic selector identity", choice)
		}
	}
	if len(f.writes) != 0 {
		t.Fatal("selection changed server runtime", f.writes)
	}
	reads := append([]string(nil), f.reads...)
	sort.Strings(reads)
	wantReads := []string{"configuration:jv-family1", "configuration:jv-kids001"}
	if !reflect.DeepEqual(reads, wantReads) {
		t.Fatalf("selection reads = %v, want only read-only display metadata lookups %v", reads, wantReads)
	}
}

func TestBackupNowRequiresSelectedImmutableInstance(t *testing.T) {
	for _, path := range []string{"/api/backups/manual", "/workspace/backups/backup"} {
		t.Run(path, func(t *testing.T) {
			app, f := phase7App(t)
			w := phase7Request(app, "POST", path, "csrf=csrf-token")
			if w.Code != http.StatusConflict || len(f.backupCalls) != 0 {
				t.Fatal(w.Code, w.Body.String(), f.backupCalls)
			}
			for _, id := range []string{"jv-family1", "jv-kids001"} {
				f.backupCalls = nil
				w = phase7Request(app, "POST", path+"?server="+id, "csrf=csrf-token")
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				nowCalls := 0
				for _, call := range f.backupCalls {
					if !strings.HasPrefix(call, id+":") {
						t.Fatal("backup escaped selected target", call)
					}
					if call == id+":now" {
						nowCalls++
					}
				}
				if nowCalls != 1 {
					t.Fatal("Backup Now calls", f.backupCalls)
				}
			}
		})
	}
}

func TestSelectedMinecraftRunningStartOnBootReviewedChanges(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			app, f := phase7App(t)
			before := f.runtimes["jv-family1"]
			before.StartOnBoot = !enabled
			f.runtimes["jv-family1"] = before
			f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
			body := "csrf=csrf-token&tab=overview&start_on_boot=" + fmt.Sprint(enabled)
			plan := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
			var review struct {
				Review string `json:"review"`
			}
			if plan.Code != 200 || json.Unmarshal(plan.Body.Bytes(), &review) != nil || review.Review == "" || len(f.writes) != 0 {
				t.Fatal(plan.Code, plan.Body.String())
			}
			if strings.Contains(plan.Body.String(), "stays stopped") || !strings.Contains(plan.Body.String(), "state is preserved") {
				t.Fatal("misleading boot-only review", plan.Body.String())
			}
			for _, token := range []string{"", "stale"} {
				w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+token)
				if w.Code != 409 || len(f.writes) != 0 {
					t.Fatal("unreviewed apply accepted", w.Code)
				}
			}
			w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+review.Review)
			want := before
			want.StartOnBoot = enabled
			if w.Code != 200 || !reflect.DeepEqual(f.writes, []string{"jv-family1"}) || f.runtimes["jv-family1"] != want || len(f.actions) != 0 || f.states["jv-family1"].State != "running" {
				t.Fatal(w.Code, w.Body.String(), f.writes, f.actions)
			}
			if strings.Contains(w.Body.String(), "stays stopped") {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestSelectedMinecraftRunningOtherSettingsUnknownPlayersFailClosed(t *testing.T) {
	for _, body := range []string{
		"tab=memory&java_memory=5G&container_memory=7G",
		"tab=gameplay&motd=Changed&max_players=12&game_mode=creative",
		"tab=crossplay&java_port=25578&bedrock_port=19146&bedrock_enabled=on",
	} {
		t.Run(body, func(t *testing.T) {
			app, f := phase7App(t)
			body += "&csrf=csrf-token"
			// Obtain a real review while stopped, then exercise both running gates.
			w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
			var plan struct {
				Review string `json:"review"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" {
				t.Fatal(w.Code, w.Body.String())
			}
			f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
			for _, action := range []string{"plan", "apply"} {
				w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/"+action+"?server=jv-family1", body+"&review="+plan.Review)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"apply_blocked":true`) || !strings.Contains(w.Body.String(), "Player information is unavailable") || len(f.writes) != 0 || len(f.actions) != 0 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestSelectedMinecraftStartOnBootUnsafeStatesAndCapacityRefusal(t *testing.T) {
	for _, state := range []string{"starting", "stopping", "recovery_required", "invalid_configuration", "degraded", "unavailable"} {
		t.Run(state, func(t *testing.T) {
			app, f := phase7App(t)
			f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: state}
			w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", "csrf=csrf-token&tab=overview&start_on_boot=false")
			if w.Code != 409 || len(f.writes) != 0 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	app, f := phase7App(t)
	c := f.runtimes["jv-family1"]
	c.StartOnBoot = false
	f.runtimes["jv-family1"] = c
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
	body := "csrf=csrf-token&tab=overview&start_on_boot=true"
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
	var plan struct {
		Review string `json:"review"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	f.putError = &api.ResponseError{StatusCode: 409, Message: "Boot-enabled configured memory exceeds capacity."}
	w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+plan.Review)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "memory exceeds capacity") || !reflect.DeepEqual(f.writes, []string{"jv-family1"}) || f.runtimes["jv-family1"].StartOnBoot || len(f.actions) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSelectedMinecraftStartOnBootStaleDefinitionReview(t *testing.T) {
	app, f := phase7App(t)
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
	body := "csrf=csrf-token&tab=overview&start_on_boot=false"
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
	var plan struct {
		Review string `json:"review"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	c := f.runtimes["jv-family1"]
	c.MOTD = "Changed after review"
	f.runtimes["jv-family1"] = c
	w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+plan.Review)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Settings changed since Review") || len(f.writes) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSelectedMinecraftStoppedStartOnBootMessage(t *testing.T) {
	app, _ := phase7App(t)
	body := "csrf=csrf-token&tab=overview&start_on_boot=false"
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
	var plan struct {
		Review string `json:"review"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+plan.Review)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "state is preserved") || strings.Contains(w.Body.String(), "restarted") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSelectedServerSelectionSoftwareMetadata(t *testing.T) {
	for _, software := range []string{"paper", "purpur", "spigot", "vanilla", "fabric", "neoforge", "forge", "quilt", "bedrock", "unknown", "read-failure"} {
		t.Run(software, func(t *testing.T) {
			app, f := phase7App(t)
			config := f.runtimes["jv-family1"]
			config.Software = software
			f.runtimes["jv-family1"] = config
			if software == "read-failure" {
				f.configurationErrors = map[string]bool{"jv-family1": true}
			}
			w := phase7Request(app, "GET", "/api/minecraft/workspace/selection?selection=none", "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var response struct {
				Servers []map[string]string `json:"servers"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Servers) != 2 {
				t.Fatal("inventory lost a server", w.Body.String())
			}
			found := false
			for _, row := range response.Servers {
				for field := range row {
					if field != "id" && field != "name" && field != "software" && field != "software_icon" {
						t.Fatalf("private field %q", field)
					}
				}
				if row["id"] == "jv-kids001" && row["software_icon"] != "/static/server-software/purpur.svg" {
					t.Fatal("sibling metadata lost", row)
				}
				if row["id"] != "jv-family1" {
					continue
				}
				found = true
				if row["name"] != "family Survival" {
					t.Fatal("friendly name lost", row)
				}
				wantIcon := ""
				if software != "unknown" && software != "read-failure" {
					wantIcon = "/static/server-software/" + software + ".svg"
				}
				if row["software_icon"] != wantIcon {
					t.Fatal(row)
				}
				if wantIcon == "" && row["software"] != "" {
					t.Fatal("fabricated software", row)
				}
				if wantIcon != "" && row["software"] != serverSoftware(software) {
					t.Fatal(row)
				}
			}
			if !found || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "/host/") {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestSelectedSettingsSharedRunningLifecycle(t *testing.T) {
	for _, body := range []string{
		"tab=memory&java_memory=5G&container_memory=7G",
		"tab=gameplay&motd=Changed&max_players=12&game_mode=creative",
		"tab=crossplay&java_port=25578&bedrock_port=19146&bedrock_enabled=on",
	} {
		for _, online := range []int{0, 3} {
			t.Run(fmt.Sprintf("%s/players=%d", body, online), func(t *testing.T) {
				app, f := phase7App(t)
				f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
				f.playerSnapshot = &api.InstancePlayers{ID: "jv-family1", State: "online", Online: online}
				body := body + "&csrf=csrf-token"
				w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
				var plan struct {
					Review       string `json:"review"`
					Confirmation bool   `json:"confirmation_required"`
					Restart      bool   `json:"restart_required"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" || !plan.Restart || plan.Confirmation != (online > 0) || !strings.Contains(w.Body.String(), fmt.Sprintf("Players online: %d", online)) || len(f.settingsEvents) != 0 {
					t.Fatal("running review", w.Code, w.Body.String(), f.settingsEvents)
				}
				body += "&review=" + plan.Review
				if online > 0 {
					w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body)
					if w.Code != 200 || !strings.Contains(w.Body.String(), `"confirmation_required":true`) || len(f.settingsEvents) != 0 {
						t.Fatal("unconfirmed apply interrupted server", w.Body.String(), f.settingsEvents)
					}
					body += "&confirm_players=yes"
				}
				w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"restarted":true`) || !reflect.DeepEqual(f.settingsEvents, []string{"stop:jv-family1", "apply:jv-family1", "start:jv-family1"}) || f.states["jv-family1"].State != "running" || !reflect.DeepEqual(f.confirmations, []bool{online > 0, false}) {
					t.Fatal("graceful reviewed lifecycle", w.Code, w.Body.String(), f.settingsEvents, f.confirmations)
				}
				if len(f.players) < 2 {
					t.Fatal("players not rechecked on apply")
				}
			})
		}
	}
}

func TestSelectedSettingsApplyRacesAndFailures(t *testing.T) {
	for _, scenario := range []string{"joined", "joined-at-stop", "unknown", "wrong-player-id", "stopped", "stop-failed", "apply-failed", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			app, f := phase7App(t)
			f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
			f.playerSnapshot = &api.InstancePlayers{ID: "jv-family1", State: "online"}
			body := "csrf=csrf-token&tab=memory&java_memory=5G&container_memory=7G"
			w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", body)
			var plan struct {
				Review string `json:"review"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Review == "" {
				t.Fatal(w.Body.String())
			}
			switch scenario {
			case "joined":
				f.playerSnapshot.Online = 2
			case "joined-at-stop":
				f.stopConfirmation = 2
			case "unknown":
				f.playerSnapshot.State = "unknown"
			case "wrong-player-id":
				f.playerSnapshot.ID = "jv-kids001"
			case "stopped":
				f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "stopped"}
			case "stop-failed":
				f.lifecycleError = fmt.Errorf("graceful stop failed")
			case "apply-failed":
				f.putError = fmt.Errorf("configuration rollback completed")
			case "stale":
				c := f.runtimes["jv-family1"]
				c.MOTD = "Changed externally"
				f.runtimes["jv-family1"] = c
			}
			w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply?server=jv-family1", body+"&review="+plan.Review)
			switch scenario {
			case "joined", "joined-at-stop":
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"confirmation_required":true`) || !strings.Contains(w.Body.String(), "Players online: 2") || len(f.writes) != 0 || f.states["jv-family1"].State != "running" {
					t.Fatal(w.Code, w.Body.String(), f.settingsEvents)
				}
				if scenario == "joined" && len(f.actions) != 0 {
					t.Fatal("known players were interrupted")
				}
			case "unknown", "wrong-player-id":
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"apply_blocked":true`) || len(f.settingsEvents) != 0 {
					t.Fatal(w.Code, w.Body.String(), f.settingsEvents)
				}
			case "stopped":
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"applied":true`) || f.states["jv-family1"].State != "stopped" || !reflect.DeepEqual(f.settingsEvents, []string{"apply:jv-family1"}) {
					t.Fatal(w.Code, w.Body.String(), f.settingsEvents)
				}
			case "stop-failed":
				if w.Code < 400 || len(f.writes) != 0 || !reflect.DeepEqual(f.settingsEvents, []string{"stop:jv-family1"}) {
					t.Fatal(w.Code, w.Body.String(), f.settingsEvents)
				}
			case "apply-failed":
				if w.Code < 400 || f.states["jv-family1"].State != "stopped" || !reflect.DeepEqual(f.settingsEvents, []string{"stop:jv-family1", "apply:jv-family1"}) {
					t.Fatal(w.Code, w.Body.String(), f.settingsEvents)
				}
			case "stale":
				if w.Code != 409 || !strings.Contains(w.Body.String(), "Settings changed since Review. Review this server again.") || len(f.settingsEvents) != 0 {
					t.Fatal(w.Code, w.Body.String(), f.settingsEvents)
				}
			}
		})
	}
}
