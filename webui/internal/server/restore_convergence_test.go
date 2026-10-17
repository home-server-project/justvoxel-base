package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/home-server-project/justvoxel-webui/internal/api"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type restoreIdentityResponseAPI struct {
	*phase7API
	foreignPlan   bool
	foreignStatus bool
	recovery      bool
	state         string
	stage         string
	statusError   bool
}

func (f *restoreIdentityResponseAPI) MinecraftInstanceBackups(ctx context.Context, session, id, action string, request any) (json.RawMessage, error) {
	raw, err := f.phase7API.MinecraftInstanceBackups(ctx, session, id, action, request)
	if err != nil {
		return raw, err
	}
	if action == "restore/plan" && f.foreignPlan {
		return json.RawMessage(`{"plan_id":"reviewed","metadata":{"id":"jv-kids001","archive":"archive.tar.gz"},"integrity":"verified"}`), nil
	}
	if action == "restore/status" {
		if f.statusError {
			return nil, errors.New("unavailable")
		}
		owner := id
		if f.foreignStatus {
			owner = "jv-kids001"
			if id == owner {
				owner = "jv-family1"
			}
		}
		state := f.state
		if state == "" {
			state = "interrupted"
		}
		raw, _ = json.Marshal(map[string]any{"id": owner, "state": state, "stage": f.stage, "recovery_required": f.recovery, "internal_path": "/private/evidence"})
	}
	return raw, nil
}
func TestSelectedRestoreRejectsForeignPlanAndRecoveryStatus(t *testing.T) {
	for _, action := range []string{"plan", "status"} {
		t.Run(action, func(t *testing.T) {
			app, base := phase7App(t)
			app.api = &restoreIdentityResponseAPI{phase7API: base, foreignPlan: action == "plan", foreignStatus: action == "status", recovery: true}
			method, path, body := http.MethodGet, "/workspace/backups?server=jv-family1", ""
			if action == "plan" {
				method, path, body = http.MethodPost, "/workspace/backups/restore/plan?server=jv-family1", "csrf=csrf-token&backup_id=archive.tar.gz"
			}
			w := phase7Request(app, method, path, body)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "identity") {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "backup-restore-apply") || (action == "status" && strings.Contains(w.Body.String(), "Retry Restore Recovery")) {
				t.Fatal("foreign evidence offered Restore or recovery")
			}
		})
	}
}
func TestSelectedRestoreRecoveryKeepsSameIDAndSibling(t *testing.T) {
	app, base := phase7App(t)
	app.api = &restoreIdentityResponseAPI{phase7API: base, recovery: true}
	sibling := base.runtimes["jv-kids001"]
	w := phase7Request(app, http.MethodGet, "/workspace/backups?server=jv-family1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `action="/workspace/backups/restore/recover?server=jv-family1"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	base.backupCalls = nil
	w = phase7Request(app, http.MethodPost, "/workspace/backups/restore/recover?server=jv-family1", "csrf=csrf-token&confirm_players=yes")
	if w.Code != 200 || base.backupRequests["restore/recover"] == nil {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, call := range base.backupCalls {
		if !strings.HasPrefix(call, "jv-family1:") {
			t.Fatal(call)
		}
	}
	if !reflect.DeepEqual(sibling, base.runtimes["jv-kids001"]) || len(base.writes) > 0 || len(base.actions) > 0 {
		t.Fatal("Restore recovery altered sibling")
	}
}
func TestLegacyRestoreAttentionNeverGuessesInstance(t *testing.T) {
	client := &fakeDashboardOperationAPI{}
	client.restore = &api.PersistentOperation{OperationType: "restore", State: "needs_attention", Status: "Preserved legacy evidence."}
	attention := dashboardCurrentAttention(context.Background(), client, "session-token")
	if attention == nil || attention.Action != "" || attention.Label != "" || attention.Status != "Preserved legacy evidence." {
		t.Fatalf("unsafe legacy attention: %#v", attention)
	}
}

func TestSelectedRestoreStatusPollingIdentityAndSelection(t *testing.T) {
	for _, tc := range []struct {
		query   string
		foreign bool
		code    int
	}{
		{"?server=jv-family1", false, 200}, {"?server=jv-kids001", false, 200},
		{"", false, 409}, {"?server=jv-missing1", false, 404},
		{"?server=jv-family1&server=jv-kids001", false, 400},
		{"?server=jv-family1", true, 502},
	} {
		t.Run(tc.query+fmt.Sprint(tc.foreign), func(t *testing.T) {
			app, base := phase7App(t)
			app.api = &restoreIdentityResponseAPI{phase7API: base, foreignStatus: tc.foreign, recovery: true}
			w := phase7Request(app, "GET", "/workspace/backups/restore/status"+tc.query, "")
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.code == 200 {
				var p selectedRestoreProgress
				if json.Unmarshal(w.Body.Bytes(), &p) != nil || !p.RecoveryRequired || p.ID != strings.TrimPrefix(tc.query, "?server=") {
					t.Fatal(w.Body.String())
				}
				if len(base.backupCalls) != 1 || base.backupCalls[0] != p.ID+":restore/status" {
					t.Fatal(base.backupCalls)
				}
			} else if strings.Contains(w.Body.String(), `"recovery_required":true`) {
				t.Fatal("foreign recovery exposed")
			}
			if strings.Contains(w.Body.String(), "/private/evidence") {
				t.Fatal("internal evidence exposed")
			}
			if tc.code == 409 && len(base.backupCalls) != 0 {
				t.Fatal("guessed instance")
			}
		})
	}
}

func TestSelectedRestoreApplyAndRecoveryActivateProgress(t *testing.T) {
	for _, action := range []string{"apply", "recover"} {
		app, base := phase7App(t)
		app.api = &restoreIdentityResponseAPI{phase7API: base, state: "validating"}
		body := "csrf=csrf-token&backup_id=archive.tar.gz&plan_fingerprint=reviewed&confirmation=RESTORE&confirm_players=yes"
		w := phase7Request(app, "POST", "/workspace/backups/restore/"+action+"?server=jv-kids001", body)
		for _, want := range []string{`data-selected-restore`, `data-restore-busy="1"`, `data-selected-server-id="jv-kids001"`, `data-status-url="/workspace/backups/restore/status?server=jv-kids001"`, `Restore progress`, `Validating`} {
			if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
				t.Fatal(action, w.Code, want, w.Body.String())
			}
		}
		for _, forbidden := range []string{"Refresh to check", "Operation <code>", "/api/restore/progress/", "minecraft.service"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatal(forbidden)
			}
		}
	}
}

func TestSelectedRestorePresentationContract(t *testing.T) {
	labels := map[string]string{
		"idle": "Ready", "validating": "Validating", "staging": "Preparing Restore data", "ready_to_apply": "Preparing Restore data",
		"shutdown": "Stopping Minecraft safely", "preserving_original": "Applying Restore data", "replacement_activated": "Applying Restore data", "original_preserved": "Applying Restore data",
		"runtime_verification": "Runtime validation", "rollback_required": "Preparing recovery", "recovery_required": "Needs attention",
		"original_restored": "Restoring previous Minecraft data", "rollback_verification": "Rolling back", "rolled_back": "Rolled back", "completed": "Complete", "interrupted": "Needs attention", "failed": "Needs attention",
	}
	for state, label := range labels {
		app, base := phase7App(t)
		app.api = &restoreIdentityResponseAPI{phase7API: base, state: state, stage: state, recovery: state == "recovery_required"}
		w := phase7Request(app, "GET", "/workspace/backups/restore/status?server=jv-family1", "")
		var p selectedRestoreProgress
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.StateLabel != label || p.StageLabel != label {
			t.Fatal(state, w.Body.String())
		}
		if p.Complete != (state == "completed") || p.RolledBack != (state == "rolled_back") {
			t.Fatal(state, p)
		}
	}
}

func TestSelectedRestoreDashboardAttention(t *testing.T) {
	for _, id := range []string{"jv-family1", "jv-kids001"} {
		for _, foreign := range []bool{false, true} {
			app, base := phase7App(t)
			app.api = &restoreIdentityResponseAPI{phase7API: base, recovery: true, foreignStatus: foreign}
			data, err := app.dashboardData(context.Background(), "session-token", "csrf", selectedServerContext{ID: id})
			if err != nil {
				t.Fatal(err)
			}
			if len(base.backupCalls) != 1 || base.backupCalls[0] != id+":restore/status" {
				t.Fatal(base.backupCalls)
			}
			if foreign {
				if data.Attention != nil {
					t.Fatal("foreign attention", data.Attention)
				}
				continue
			}
			if data.Attention == nil || data.Attention.Title != "Restore needs attention" || data.Attention.Action != "/workspace/backups?server="+id || data.Attention.Label != "Review Restore" {
				t.Fatal(data.Attention)
			}
		}
	}
}

func TestSelectedRestoreActiveRecoveryKeepsPolling(t *testing.T) {
	p := presentSelectedRestore(selectedRestoreProgress{ID: "jv-family1", State: "recovery_required", Stage: "recovery_required", RecoveryRequired: false})
	if p.Terminal || p.Attention || p.StateLabel != "Preparing recovery" {
		t.Fatal(p)
	}
}

func TestSelectedRestorePollingAuthorization(t *testing.T) {
	app, base := phase7App(t)
	app.api = &restoreIdentityResponseAPI{phase7API: base, recovery: true}
	for _, role := range []string{"viewer", "operator"} {
		base.role = role
		w := phase7Request(app, "GET", "/workspace/backups/restore/status?server=jv-family1", "")
		if w.Code != 403 || len(base.backupCalls) != 0 {
			t.Fatal(role, w.Code, base.backupCalls)
		}
	}
}

func TestSelectedRestoreAcceptedKeepsProgressOnStatusReadFailure(t *testing.T) {
	for _, action := range []string{"apply", "recover"} {
		app, base := phase7App(t)
		app.api = &restoreIdentityResponseAPI{phase7API: base, statusError: true}
		w := phase7Request(app, "POST", "/workspace/backups/restore/"+action+"?server=jv-family1", "csrf=csrf-token&backup_id=archive.tar.gz&plan_fingerprint=reviewed&confirmation=RESTORE&confirm_players=yes")
		for _, want := range []string{`data-status-url="/workspace/backups/restore/status?server=jv-family1"`, `Restore progress`, `data-restore-busy="1"`} {
			if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
				t.Fatal(action, w.Code, want, w.Body.String())
			}
		}
	}
}

func TestSelectedRestoreWorkspaceProgressAndBusy(t *testing.T) {
	for _, tc := range []struct {
		state    string
		recovery bool
		busy     bool
	}{
		{"idle", false, false},
		{"idle", true, false},
		{"validating", false, true},
		{"recovering", false, true},
		{"recovery_required", true, true},
		{"recovery_required", false, true},
		{"interrupted", false, true},
		{"failed", false, true},
		{"completed", false, false},
		{"rolled_back", false, false},
	} {
		t.Run(tc.state+fmt.Sprint(tc.recovery), func(t *testing.T) {
			app, base := phase7App(t)
			app.api = &restoreIdentityResponseAPI{phase7API: base, state: tc.state, recovery: tc.recovery}
			w := phase7Request(app, "GET", "/workspace/backups?server=jv-family1", "")
			body := w.Body.String()
			busy := "0"
			if tc.busy {
				busy = "1"
			}
			if w.Code != 200 || !strings.Contains(body, `data-restore-busy="`+busy+`"`) {
				t.Fatal(w.Code, body)
			}
			for _, marker := range []string{"Restore progress", "data-selected-restore", "data-status-url=", "data-restore-recovery"} {
				if strings.Contains(body, marker) != (tc.state != "idle") {
					t.Fatalf("%s: unexpected progress/polling markup: %s", marker, body)
				}
			}
			if tc.state == "idle" && !tc.recovery && !strings.Contains(body, "Restore: Ready") {
				t.Fatal("idle summary missing", body)
			}
		})
	}
}

type selectedRestoreDashboardAttentionAPI struct {
	*restoreIdentityResponseAPI
	operations *fakeDashboardOperationAPI
}

func (f *selectedRestoreDashboardAttentionAPI) AdminCurrentMigrationOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error) {
	return f.operations.AdminCurrentMigrationOperation(ctx, session)
}

func (f *selectedRestoreDashboardAttentionAPI) AdminCurrentRestoreOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error) {
	return f.operations.AdminCurrentRestoreOperation(ctx, session)
}

func (f *selectedRestoreDashboardAttentionAPI) AdminCurrentFactoryResetOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error) {
	return f.operations.AdminCurrentFactoryResetOperation(ctx, session)
}

func TestSelectedRestorePreservesDashboardApplianceAttention(t *testing.T) {
	for _, kind := range []string{"migration", "legacy_restore", "factory_reset"} {
		t.Run(kind, func(t *testing.T) {
			app, base := phase7App(t)
			operations := &fakeDashboardOperationAPI{}
			op := &api.PersistentOperation{State: "needs_attention", Status: "Preserved appliance attention."}
			switch kind {
			case "migration":
				operations.migration = op
			case "legacy_restore":
				operations.restore = op
			case "factory_reset":
				operations.reset = op
			}
			app.api = &selectedRestoreDashboardAttentionAPI{
				restoreIdentityResponseAPI: &restoreIdentityResponseAPI{phase7API: base, state: "recovery_required", recovery: true},
				operations:                 operations,
			}
			want := dashboardCurrentAttention(context.Background(), operations, "session-token")
			data, err := app.dashboardData(context.Background(), "session-token", "csrf", selectedServerContext{ID: "jv-family1"})
			if err != nil || want == nil || !reflect.DeepEqual(data.Attention, want) {
				t.Fatalf("attention = %#v, want %#v; error = %v", data.Attention, want, err)
			}
			if len(base.backupCalls) != 1 || base.backupCalls[0] != "jv-family1:restore/status" {
				t.Fatal("selected recovery status was not read", base.backupCalls)
			}
		})
	}
}
