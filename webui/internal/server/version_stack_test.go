package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeVersionStackAPI struct {
	*fakeDateTimeVersionAPI
	writes     int
	lastUpdate api.MinecraftStackUpdateRequest
}

func (f *fakeVersionStackAPI) MinecraftInstanceStackUpdate(_ context.Context, _, id string, request api.MinecraftStackUpdateRequest) (api.PersistentOperationResponse, error) {
	f.writes++
	f.lastUpdate = request
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: id, OperationType: "minecraft_update", PlanFingerprint: request.PlanFingerprint}}, nil
}
func (f *fakeVersionStackAPI) AdminOperation(_ context.Context, _, _ string) (api.PersistentOperationResponse, error) {
	return api.PersistentOperationResponse{}, nil
}
func (f *fakeVersionStackAPI) MinecraftInstanceStackUpdateOperation(_ context.Context, _, _, _ string) (api.PersistentOperationResponse, error) {
	return api.PersistentOperationResponse{}, nil
}
func (f *fakeVersionStackAPI) MinecraftInstanceAcknowledgeStackUpdate(_ context.Context, _, _, _ string) (api.PersistentOperationResponse, error) {
	f.writes++
	return api.PersistentOperationResponse{}, nil
}

func TestVersionStackUpdateRequiresAdministratorAndCSRF(t *testing.T) {
	client := &fakeVersionStackAPI{fakeDateTimeVersionAPI: &fakeDateTimeVersionAPI{fakeAPI: &fakeAPI{}, role: "administrator"}}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"viewer", "operator", "administrator"} {
		client.role = role
		for _, csrf := range []string{"wrong", "csrf-token"} {
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/version/workspace/update", "csrf="+csrf+"&plan_fingerprint=reviewed"))
			want := http.StatusForbidden
			if role == "administrator" && csrf == "csrf-token" {
				want = http.StatusAccepted
			}
			if rr.Code != want {
				t.Fatalf("role=%s csrf=%s: %d %s", role, csrf, rr.Code, rr.Body.String())
			}
		}
	}
	if client.writes != 1 {
		t.Fatalf("unauthorized writes: %d", client.writes)
	}
}

func TestVersionStackUIUsesSafeStackStateAndSingleReview(t *testing.T) {
	data, err := os.ReadFile("static/version-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`line("Overall", stackLabel(status.stack_state))`,
		"Waiting for cross-play support",
		"Review changes",
		"Apply changes",
		"Managed automatically",
		"status.installed} → ${status.selected_candidate",
		`needsUpdate && !channelChange && status.stack_state !== "updates_available"`,
		"csrf, plan_fingerprint: status.plan_fingerprint",
		"Verify and keep current server",
		`up_to_date: "Up to date"`,
		`waiting_for_compatibility: "Waiting for compatibility"`,
		"may start temporarily for verification and will be stopped again afterward",
		`(!plan.changes?.length && status.update_available`,
		`status.installed !== status.selected_candidate`,
		"Disable Cross-play first",
		`needsUpdate && Number(players?.online || 0) > 0`,
		`if (confirmationRequired && !blocked)`,
		`minecraft-native-toggle system-ups-shutdown-switch`,
		`checkbox.setAttribute("role", "switch")`,
		`confirm_players: checkbox.checked ? "yes" : "no"`,
		`watchUpdate(result.operation.operation_id)`,
		`/api/version/workspace/update-operation?`,
		`setTimeout(() => watchUpdate(id), 2000)`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stack UI missing %q", want)
		}
	}
	for _, forbidden := range []string{"Review updates", "Apply update", `status.update_available ? "Available"`, "Update Geyser", "Update Floodgate", "Update ViaVersion", "sha256", "A stopped server will start for verification.", "BEDROCK_MANAGED_PLUGINS", "MODRINTH_PROJECTS", "PLUGINS="} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe or technical normal UI: %q", forbidden)
		}
	}
}

func TestVersionStackUpdatePreservesPlayerConfirmation(t *testing.T) {
	client := &fakeVersionStackAPI{fakeDateTimeVersionAPI: &fakeDateTimeVersionAPI{fakeAPI: &fakeAPI{}, role: "administrator"}}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []string{"yes", "no"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/version/workspace/update", "csrf=csrf-token&server_type=purpur&image_tag=release_1.2-3&plan_fingerprint=refreshed&confirm_players="+choice))
		if rr.Code != http.StatusAccepted || client.lastUpdate.ServerType != "purpur" || client.lastUpdate.ImageTag != "release_1.2-3" || client.lastUpdate.PlanFingerprint != "refreshed" || client.lastUpdate.ConfirmPlayers != (choice == "yes") {
			t.Fatalf("update request lost reviewed fingerprint or player confirmation: status=%d request=%+v", rr.Code, client.lastUpdate)
		}
	}
}

func TestVersionCombinedRequestRejectsUnknownAndDuplicateFields(t *testing.T) {
	client := &fakeVersionStackAPI{fakeDateTimeVersionAPI: &fakeDateTimeVersionAPI{fakeAPI: &fakeAPI{}, role: "administrator"}}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"&image_tag=stable&image_tag=latest", "&server_type=paper&server_type=purpur", "&command=pull", "&confirm_players=maybe", "&acknowledge=yes&operation_id=id&image_tag=latest"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/version/workspace/update", "csrf=csrf-token&plan_fingerprint=reviewed"+suffix))
		if rr.Code != http.StatusBadRequest || client.writes != 0 {
			t.Fatalf("ambiguous combined request forwarded: %d %s", rr.Code, rr.Body.String())
		}
	}
}
