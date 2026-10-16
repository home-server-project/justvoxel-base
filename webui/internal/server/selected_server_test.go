package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type phase7API struct {
	*serverInventoryTestAPI
	writes            []string
	players           []string
	backupCalls       []string
	renames           []string
	deleted           []string
	backupInventoryID string
	putError          error
	deletions         []api.RegisteredMinecraftInstance
	backupRequests    map[string]json.RawMessage
}

func (f *phase7API) MinecraftInstanceDeletions(context.Context, string) ([]api.RegisteredMinecraftInstance, error) {
	return f.deletions, nil
}

func (f *phase7API) SetMinecraftInstanceConfiguration(_ context.Context, _, id string, v api.InstanceRuntimeSettings) error {
	f.writes = append(f.writes, id)
	if f.putError != nil {
		return f.putError
	}
	f.runtimes[id] = v
	return nil
}
func (f *phase7API) MinecraftInstancePlayers(_ context.Context, _, id string) (api.InstancePlayers, error) {
	f.players = append(f.players, id)
	return api.InstancePlayers{ID: id, State: "offline"}, nil
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
func TestSelectedStoppedRefusalAndStaleReview(t *testing.T) {
	app, f := phase7App(t)
	f.states["jv-family1"] = api.MinecraftInstanceStatus{ID: "jv-family1", State: "running"}
	w := phase7Request(app, "POST", "/api/minecraft/workspace/settings/plan?server=jv-family1", "csrf=csrf-token&tab=memory&java_memory=4G&container_memory=6G")
	if w.Code != 409 || len(f.actions) != 0 || len(f.writes) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestSelectedPlayersAndWhitelistBoundary(t *testing.T) {
	app, f := phase7App(t)
	w := phase7Request(app, "GET", "/api/minecraft/workspace/whitelist?server=jv-family1", "")
	if w.Code != 200 || !reflect.DeepEqual(f.players, []string{"jv-family1"}) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, route := range []string{"whitelist", "whitelist/state"} {
		w = phase7Request(app, "POST", "/api/minecraft/workspace/"+route+"?server=jv-family1", "csrf=csrf-token&name=Player&action=add&platform=java&enabled=true")
		if w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
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

func TestPhase7OriginalSettingsAndPlayersKeepLegacyContracts(t *testing.T) {
	f := configuredMinecraftWorkspaceAPI()
	app, e := New(f, Config{Version: "test", ManagementAPI: "v1"})
	if e != nil {
		t.Fatal(e)
	}
	for _, selection := range []string{"", "?server=original"} {
		w := phase7Request(app, "GET", "/api/minecraft/workspace/settings"+selection, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"java_memory":"4G"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		w = phase7Request(app, "POST", "/api/minecraft/workspace/settings/apply"+selection, "csrf=csrf-token&tab=memory&java_memory=5G&container_memory=7G")
		if w.Code != 200 || f.applied.JavaMemory != "5G" {
			t.Fatal("Original lost legacy apply", w.Code)
		}
		w = phase7Request(app, "POST", "/api/minecraft/workspace/whitelist"+selection, "csrf=csrf-token&platform=java&action=add&name=Alex")
		if w.Code != 200 || f.whitelistRequest != [3]string{"java", "add", "Alex"} {
			t.Fatal("Original whitelist changed", w.Code)
		}
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
func TestPhase7NoIndependentVersionMutation(t *testing.T) {
	app, f := phase7App(t)
	for _, route := range []string{"settings/apply", "settings/plan"} {
		w := phase7Request(app, "POST", "/api/minecraft/workspace/"+route+"?server=jv-family1", "csrf=csrf-token&tab=version&version=LATEST&image_tag=latest")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := phase7Request(app, "POST", "/api/version/workspace/update?server=jv-family1", "csrf=csrf-token")
	if w.Code != 409 || len(f.actions) != 0 || len(f.writes) != 0 {
		t.Fatal("independent Version reached mutation", w.Code)
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
func TestPhase7OriginalHasNoIndependentDeleteOrRename(t *testing.T) {
	app, f := phase7App(t)
	for _, action := range []string{"rename", "delete"} {
		w := phase7Request(app, "POST", "/api/minecraft/workspace/"+action+"?server=original", "csrf=csrf-token&confirm_name=Original&display_name=Other")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(f.deleted) != 0 || len(f.renames) != 0 {
		t.Fatal("Original reached independent action")
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
