package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func backupsSafetyRequest(t *testing.T, client *fakeNewBackupsAPI, method, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return httptestResponse(app, authenticatedAdminRequest(method, path, values.Encode()))
}

func backupsSafetyReview(t *testing.T, response *httptest.ResponseRecorder, evidence ...string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("review status=%d: %s", response.Code, response.Body.String())
	}
	for _, want := range append([]string{"data-backups-workspace-root"}, evidence...) {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("workspace review missing %q", want)
		}
	}
}

func backupsSafetyAutomaticForm() url.Values {
	return url.Values{"csrf": {"csrf-token"}, "automatic_enabled": {"on"}, "daily_time": {"05:15"}, "backup_keep": {"12"}}
}

func backupsSafetyConfigurationRequest(configuration api.AdminConfigurationDiscovery) api.AdminConfigurationChangeRequest {
	// Specify the expected request independently of the production request builder.
	current := configuration.Minecraft
	return api.AdminConfigurationChangeRequest{
		JavaMemory: current.JavaMemory, ContainerMemory: current.ContainerMemory,
		JavaPort: current.JavaPort, BedrockEnabled: current.BedrockEnabled, BedrockPort: current.BedrockPort,
		Timezone: current.Timezone, MaxPlayers: current.MaxPlayers, MOTD: current.MOTD,
		ImageTag: current.ImageTag, VersionPolicy: current.VersionMode, Version: current.Version,
		BackupKeep: 12, BackupSchedule: "*-*-* 05:15:00", BackupTimerEnabled: true,
	}
}

func TestBackupsWorkspaceAutomaticPlanPreservesMinecraftConfiguration(t *testing.T) {
	configuration := defaultNewBackupsConfiguration()
	configuration.Minecraft.BedrockEnabled = true
	configuration.Minecraft.MOTD = "Keep this server welcome message"
	client := &fakeNewBackupsAPI{configuration: configuration, configurationPlan: api.AdminConfigurationChangeResponse{
		OK: true, Changes: []api.AdminConfigurationChange{{Field: "backup_schedule"}, {Field: "backup_keep"}},
	}}
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/automatic/plan", backupsSafetyAutomaticForm())
	backupsSafetyReview(t, response, `action="/workspace/backups/automatic/apply"`)
	if client.configurationPlanCalls != 1 || client.configurationApplyCalls != 0 {
		t.Fatalf("plan calls=%d apply calls=%d", client.configurationPlanCalls, client.configurationApplyCalls)
	}
	if want := backupsSafetyConfigurationRequest(configuration); !reflect.DeepEqual(client.plannedConfiguration, want) {
		t.Fatalf("automatic request=%+v, want preserved configuration %+v", client.plannedConfiguration, want)
	}
}

func TestBackupsWorkspaceAutomaticApplyRevalidatesBeforeApply(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/automatic/apply", backupsSafetyAutomaticForm())
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/workspace/backups?result=automatic" {
		t.Fatalf("apply status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if client.configurationPlanCalls != 1 || client.configurationApplyCalls != 1 || client.configurationPlanCallsAtApply != 1 {
		t.Fatalf("plan=%d apply=%d plans before apply=%d", client.configurationPlanCalls, client.configurationApplyCalls, client.configurationPlanCallsAtApply)
	}
	want := backupsSafetyConfigurationRequest(defaultNewBackupsConfiguration())
	if !reflect.DeepEqual(client.appliedConfiguration, want) || !reflect.DeepEqual(client.plannedConfiguration, want) {
		t.Fatalf("planned=%+v applied=%+v want=%+v", client.plannedConfiguration, client.appliedConfiguration, want)
	}
}

func TestBackupsWorkspaceAutomaticRejectsInvalidOrUnrelatedChanges(t *testing.T) {
	for _, action := range []string{"plan", "apply"} {
		t.Run("invalid time/"+action, func(t *testing.T) {
			client := &fakeNewBackupsAPI{}
			values := backupsSafetyAutomaticForm()
			values.Set("daily_time", "25:99")
			response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/automatic/"+action, values)
			backupsSafetyReview(t, response, "valid 24-hour time")
			if client.configurationPlanCalls != 0 || client.configurationApplyCalls != 0 {
				t.Fatal("invalid time reached configuration planning/apply")
			}
		})
	}
	t.Run("unrelated revalidation change", func(t *testing.T) {
		client := &fakeNewBackupsAPI{configurationPlan: api.AdminConfigurationChangeResponse{
			OK: true, Changes: []api.AdminConfigurationChange{{Field: "backup_keep"}, {Field: "motd"}},
		}}
		response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/automatic/apply", backupsSafetyAutomaticForm())
		backupsSafetyReview(t, response, "Configuration changed unexpectedly", "Nothing was applied")
		if client.configurationPlanCalls != 1 || client.configurationApplyCalls != 0 {
			t.Fatalf("plan=%d apply=%d", client.configurationPlanCalls, client.configurationApplyCalls)
		}
	})
}

func backupsSafetySMBForm() url.Values {
	return url.Values{
		"csrf": {"csrf-token"}, "type": {"smb"}, "path": {"/var/mnt/justvoxel-backup/backups"},
		"mount_point": {"/var/mnt/justvoxel-backup"}, "source": {"//nas/backups"},
		"username": {"backup-user"}, "domain": {"HOME"}, "password": {"distinctive-SMB-secret-10A"},
	}
}

func TestBackupsWorkspaceDestinationKeepsSMBPasswordOutOfReview(t *testing.T) {
	client := &fakeNewBackupsAPI{destinationPlan: api.AdminBackupStorageResponse{
		OK: true, Changed: true, Proposed: api.AdminBackupStorageTarget{
			Type: "smb", Path: "/var/mnt/justvoxel-backup/backups", MountPoint: "/var/mnt/justvoxel-backup",
			ExpectedSource: "//nas/backups", CredentialsNeeded: true,
		},
	}}
	values := backupsSafetySMBForm()
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/destination/plan", values)
	backupsSafetyReview(t, response, `class="backup-destination-review"`, "//nas/backups", "/var/mnt/justvoxel-backup/backups", `action="/workspace/backups/destination/apply"`)
	if client.destinationPlanCalls != 1 || client.destinationApplyCalls != 0 || client.plannedDestination.Password != "" {
		t.Fatalf("plan=%d apply=%d password present=%v", client.destinationPlanCalls, client.destinationApplyCalls, client.plannedDestination.Password != "")
	}
	if client.plannedDestination.Type != "smb" || client.plannedDestination.Source != "//nas/backups" {
		t.Fatal("SMB request did not reach planning")
	}
	if strings.Contains(response.Body.String(), values.Get("password")) {
		t.Fatal("SMB password leaked into review")
	}
}

func TestBackupsWorkspaceDestinationPassesSMBPasswordOnlyAtApply(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	values := backupsSafetySMBForm()
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/destination/apply", values)
	if client.destinationApplyCalls != 1 || client.appliedDestination.Password != values.Get("password") {
		t.Fatal("submitted SMB password did not reach Apply exactly once")
	}
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/workspace/backups?result=destination" {
		t.Fatalf("apply status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
}

func backupsSafetyRestoreForm() url.Values {
	return url.Values{
		"csrf": {"csrf-token"}, "backup_id": {"minecraft-2026-09-20-043000.tar.gz"}, "mode": {"world"},
		"plan_fingerprint": {restorePageFingerprint}, "confirmation": {"RESTORE"},
	}
}

func TestBackupsWorkspaceRestorePlanUsesAuthoritativeReview(t *testing.T) {
	plan := restorePlan(true)
	// These values exist only in the backend response, not in the submitted form.
	plan.Normalized.Current.Version = "26.4-authoritative"
	plan.Warnings = append(plan.Warnings, api.AdminRestorePlanWarning{Code: "review_warning", Message: "Backend review requires player-aware shutdown."})
	client := &fakeNewBackupsAPI{restorePlanResult: plan}
	values := backupsSafetyRestoreForm()
	values.Del("plan_fingerprint")
	values.Del("confirmation")
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/restore/plan", values)
	backupsSafetyReview(t, response, "26.4-authoritative", plan.Normalized.CreatedAt, plan.Normalized.VersionRelation,
		"Backend review requires player-aware shutdown.", "PlayerOne", `name="players_confirmed"`,
		`data-confirm-value="RESTORE"`, `data-destructive-submit disabled`,
		`name="plan_fingerprint" value="`+plan.PlanFingerprint+`"`, `action="/workspace/backups/restore/apply"`)
	if client.restorePlanCalls != 1 || client.restoreApplyCalls != 0 {
		t.Fatalf("plan=%d apply=%d", client.restorePlanCalls, client.restoreApplyCalls)
	}
	if client.restorePlanRequest != (api.AdminRestorePlanRequest{BackupID: values.Get("backup_id"), Mode: "world"}) {
		t.Fatalf("unexpected plan request: %+v", client.restorePlanRequest)
	}
}

func TestBackupsWorkspaceRestoreApplyRejectsStaleReviewedPlan(t *testing.T) {
	client := &fakeNewBackupsAPI{restorePlanResult: restorePlan(false)}
	values := backupsSafetyRestoreForm()
	values.Set("plan_fingerprint", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/restore/apply", values)
	backupsSafetyReview(t, response, "changed since it was reviewed", `class="backup-restore-review"`, `name="plan_fingerprint" value="`+restorePageFingerprint+`"`)
	if client.restorePlanCalls != 1 || client.restoreApplyCalls != 0 {
		t.Fatalf("plan=%d apply=%d", client.restorePlanCalls, client.restoreApplyCalls)
	}
}

func TestBackupsWorkspaceRestoreApplyRejectsInvalidConfirmation(t *testing.T) {
	client := &fakeNewBackupsAPI{restorePlanResult: restorePlan(false)}
	values := backupsSafetyRestoreForm()
	values.Set("confirmation", "restore")
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/restore/apply", values)
	backupsSafetyReview(t, response, "Restore confirmation is incomplete", `data-confirm-value="RESTORE"`)
	if client.restorePlanCalls != 1 || client.restoreApplyCalls != 0 {
		t.Fatalf("plan=%d apply=%d", client.restorePlanCalls, client.restoreApplyCalls)
	}
}

func TestBackupsWorkspaceRestoreApplyRequiresPlayersConfirmation(t *testing.T) {
	client := &fakeNewBackupsAPI{restorePlanResult: restorePlan(true)}
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/restore/apply", backupsSafetyRestoreForm())
	backupsSafetyReview(t, response, "Confirm that the online players may be interrupted", `name="players_confirmed"`)
	if client.restorePlanCalls != 1 || client.restoreApplyCalls != 0 {
		t.Fatalf("plan=%d apply=%d", client.restorePlanCalls, client.restoreApplyCalls)
	}
	// The confirmed path is exercised by TestBackupsWorkspaceRestoreApplyStartsPersistentOperation.
}

func TestBackupsWorkspaceRestoreApplyStartsPersistentOperation(t *testing.T) {
	for _, players := range []bool{false, true} {
		t.Run(map[bool]string{false: "no players", true: "players confirmed"}[players], func(t *testing.T) {
			const id = "87654321-4321-4abc-8def-123456789abc"
			client := &fakeNewBackupsAPI{restorePlanResult: restorePlan(players), restoreApplyResult: api.AdminRestoreApplyResponse{
				OK: true, Created: true, Operation: &api.PersistentOperation{SchemaVersion: "v1", OperationID: id, OperationType: "restore", State: "queued", Stage: "queued"},
			}}
			values := backupsSafetyRestoreForm()
			if players {
				values.Set("players_confirmed", "yes")
			}
			response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/restore/apply", values)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/workspace/backups?result=restore&restore_operation="+id {
				t.Fatalf("apply status=%d location=%q", response.Code, response.Header().Get("Location"))
			}
			want := api.AdminRestoreApplyRequest{PlanFingerprint: restorePageFingerprint, Request: api.AdminRestorePlanRequest{BackupID: values.Get("backup_id"), Mode: "world"}, DestructiveConfirmed: true, PlayersConfirmed: players}
			if client.restorePlanCalls != 1 || client.restoreApplyCalls != 1 || !reflect.DeepEqual(client.restoreApplyRequest, want) {
				t.Fatalf("plan=%d apply=%d request=%+v want=%+v", client.restorePlanCalls, client.restoreApplyCalls, client.restoreApplyRequest, want)
			}
		})
	}
}

func TestBackupsWorkspaceReconnectsToCurrentRestoreOperation(t *testing.T) {
	const id = "87654321-4321-4abc-8def-123456789abc"
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "current operation", true: "explicit operation"}[explicit], func(t *testing.T) {
			operation := &api.PersistentOperation{SchemaVersion: "v1", OperationID: id, OperationType: "restore", State: "running", Stage: "staging", Status: "Preparing verified Restore data on the Minecraft data filesystem."}
			client := &fakeNewBackupsAPI{}
			path := "/workspace/backups"
			if explicit {
				client.restoreOperation = operation
				path += "?restore_operation=" + id
			} else {
				client.currentRestoreOperation = operation
			}
			response := backupsSafetyRequest(t, client, http.MethodGet, path, nil)
			backupsSafetyReview(t, response, "Restore progress", "Restoring", "Preparing Restore data", operation.Status, `data-restore-busy="1"`, `/api/restore/progress/`+id)
			wantCalls := 0
			if explicit {
				wantCalls = 1
			}
			if client.restoreOperationCalls != wantCalls {
				t.Fatalf("AdminOperation calls=%d want=%d", client.restoreOperationCalls, wantCalls)
			}
			if client.restorePlanCalls != 0 || client.restoreApplyCalls != 0 {
				t.Fatal("reconnection attempted to start another Restore")
			}
		})
	}
}

func TestBackupsWorkspaceDestinationOffersOnlySafePartitions(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	response := backupsSafetyRequest(t, client, http.MethodGet, "/workspace/backups", nil)
	backupsSafetyReview(t, response, `value="/dev/vdb1"`, "Same physical disk")
	for _, unsafe := range []string{"/dev/vda1", "/dev/vdc1"} {
		if strings.Contains(response.Body.String(), unsafe) {
			t.Errorf("unsafe/unformatted partition offered: %s", unsafe)
		}
	}
}

func TestBackupsWorkspaceDestinationPlanRejectsOperator(t *testing.T) {
	client := &fakeNewBackupsAPI{role: "operator"}
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/destination/plan", backupsSafetySMBForm())
	if response.Code != http.StatusForbidden || client.destinationStatusCalls != 0 || client.destinationPlanCalls != 0 || client.destinationApplyCalls != 0 {
		t.Fatalf("status=%d storage status=%d plan=%d apply=%d", response.Code, client.destinationStatusCalls, client.destinationPlanCalls, client.destinationApplyCalls)
	}
}

func TestBackupsWorkspaceManualBackupRejectsBadCSRF(t *testing.T) {
	client := &fakeNewBackupsAPI{}
	response := backupsSafetyRequest(t, client, http.MethodPost, "/workspace/backups/backup", url.Values{"csrf": {"wrong"}})
	if response.Code != http.StatusForbidden || client.backupCalls != 0 {
		t.Fatalf("status=%d backup calls=%d", response.Code, client.backupCalls)
	}
}
