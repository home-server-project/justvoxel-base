package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestBackupsSelectionNeverFallsBack(t *testing.T) {
	for _, selection := range []string{"", "?server=original", "?server=bad", "?server=jv-absent1", "?server=jv-family1&server=jv-kids001"} {
		app, f := phase7App(t)
		for _, path := range []string{"/workspace/backups/backup", "/workspace/backups/restore/apply"} {
			w := phase7Request(app, http.MethodPost, path+selection, "csrf=csrf-token&confirmation=RESTORE&backup_id=archive.tar.gz&plan_fingerprint=reviewed")
			if w.Code < 400 || len(f.backupCalls) != 0 {
				t.Fatal(selection, w.Code, w.Body.String(), f.backupCalls)
			}
		}
	}
}
func TestBackupsSingleRealInstanceSelection(t *testing.T) {
	app, f := phase7App(t)
	f.instances = []api.RegisteredMinecraftInstance{{ID: "jv-family1", DisplayName: "Family"}}
	delete(f.runtimes, "jv-kids001")
	w := phase7Request(app, http.MethodGet, "/workspace/backups", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `?server=jv-family1`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, call := range f.backupCalls {
		if !strings.HasPrefix(call, "jv-family1:") {
			t.Fatal(call)
		}
	}
}
func TestBackupsAutomaticChangesOnlySelectedSchedule(t *testing.T) {
	app, f := phase7App(t)
	before := f.runtimes["jv-kids001"]
	w := phase7Request(app, http.MethodPost, "/workspace/backups/automatic/apply?server=jv-family1", "csrf=csrf-token&automatic_enabled=on&daily_time=05%3A15&backup_keep=12")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got api.InstanceBackupSettings
	if json.Unmarshal(f.backupRequests["configure"], &got) != nil || !got.Enabled || got.Time != "05:15" || got.Retention != 12 {
		t.Fatal(string(f.backupRequests["configure"]))
	}
	if len(f.writes) != 0 || !reflect.DeepEqual(before, f.runtimes["jv-kids001"]) {
		t.Fatal("backup schedule altered server configuration")
	}
}
func TestBackupsRestorePreservesReviewedPlanAndPlayerConfirmation(t *testing.T) {
	app, f := phase7App(t)
	w := phase7Request(app, "POST", "/workspace/backups/restore/apply?server=jv-family1", "csrf=csrf-token&backup_id=archive.tar.gz&confirmation=RESTORE&plan_fingerprint=reviewed&players_confirmed=yes")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got struct {
		Archive     string `json:"archive"`
		Plan        string `json:"plan_id"`
		Destructive bool   `json:"confirm_destructive"`
		Players     bool   `json:"confirm_players"`
	}
	if json.Unmarshal(f.backupRequests["restore/apply"], &got) != nil || got.Archive != "archive.tar.gz" || got.Plan != "reviewed" || !got.Destructive || !got.Players {
		t.Fatal(string(f.backupRequests["restore/apply"]))
	}
	for _, call := range f.backupCalls {
		if !strings.HasPrefix(call, "jv-family1:") {
			t.Fatal(call)
		}
	}
}
func TestBackupsRestoreAndRecoveryRequireConfirmation(t *testing.T) {
	for _, path := range []string{"restore/apply", "restore/recover"} {
		app, f := phase7App(t)
		w := phase7Request(app, "POST", "/workspace/backups/"+path+"?server=jv-family1", "csrf=csrf-token&backup_id=archive.tar.gz&plan_fingerprint=reviewed")
		if w.Code != 200 || f.backupRequests[path] != nil || !strings.Contains(w.Body.String(), "Confirm") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
func TestBackupsWorkspaceManualBackupRejectsBadCSRF(t *testing.T) {
	app, f := phase7App(t)
	w := phase7Request(app, "POST", "/workspace/backups/backup?server=jv-family1", "csrf=wrong")
	if w.Code != 403 || len(f.backupCalls) != 0 {
		t.Fatal(w.Code, f.backupCalls)
	}
}
func TestBackupsDoNotExposeFixedServerDestinationOrResetActions(t *testing.T) {
	app, f := phase7App(t)
	for _, path := range []string{"destination/plan", "destination/apply", "restore/resolve-reset"} {
		w := phase7Request(app, "POST", "/workspace/backups/"+path+"?server=jv-family1", "csrf=csrf-token")
		if !strings.Contains(w.Body.String(), "This operation is unavailable") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if len(f.writes) != 0 || len(f.actions) != 0 {
		t.Fatal("unrelated server operation")
	}
}
