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
	writes int
}

func (f *fakeVersionStackAPI) AdminMinecraftStackUpdate(_ context.Context, _ string, request api.MinecraftStackUpdateRequest) (api.PersistentOperationResponse, error) {
	f.writes++
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationType: "minecraft_update", PlanFingerprint: request.PlanFingerprint}}, nil
}
func (f *fakeVersionStackAPI) AdminOperation(_ context.Context, _, _ string) (api.PersistentOperationResponse, error) {
	return api.PersistentOperationResponse{}, nil
}
func (f *fakeVersionStackAPI) AdminCurrentMinecraftStackUpdate(_ context.Context, _ string) (api.PersistentOperationResponse, error) {
	return api.PersistentOperationResponse{}, nil
}
func (f *fakeVersionStackAPI) AdminAcknowledgeMinecraftStackUpdate(_ context.Context, _, _ string) (api.PersistentOperationResponse, error) {
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
	for _, want := range []string{`line("Overall", stackLabel(status.stack_state))`, "Waiting for cross-play support", "Review updates", "Apply update", "Managed automatically", "plan.installed} → ${plan.selected_candidate", "plan.stack_state !== \"updates_available\"", "csrf, plan_fingerprint", "Verify and keep current server", `status.update_available && status.stack_state === "updates_available"`, `up_to_date: "Up to date"`, `waiting_for_compatibility: "Waiting for compatibility"`, "may start temporarily for verification and will be stopped again afterward"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stack UI missing %q", want)
		}
	}
	for _, forbidden := range []string{`status.update_available ? "Available"`, "Update Geyser", "Update Floodgate", "Update ViaVersion", "sha256", "A stopped server will start for verification.", "BEDROCK_MANAGED_PLUGINS", "MODRINTH_PROJECTS", "PLUGINS="} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe or technical normal UI: %q", forbidden)
		}
	}
}
