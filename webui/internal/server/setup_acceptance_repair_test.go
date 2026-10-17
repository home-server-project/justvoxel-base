package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestAdvancedFirstRunFriendlyNameValidationAndReviewedRequest(t *testing.T) {
	app, client := newAddServerTestApp(t)
	client.configuration.Configured = false
	startSetup(t, app)
	defer firstRunSetupDrafts.delete(app, "session-token")
	for _, name := range []string{"", ".", "..", "Bad\nName", "Bad\u200bName", strings.Repeat("a", 65)} {
		values := validServerValues()
		values.Set("display_name", name)
		response := saveServerStep(t, app, values)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("name %q accepted: %d", name, response.Code)
		}
	}
	values := validServerValues()
	values.Set("display_name", "  Family Survival  ")
	response := saveServerStep(t, app, values)
	if response.Code != http.StatusSeeOther {
		t.Fatal(response.Code, response.Body.String())
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.DisplayName != "Family Survival" || draft.Server.MOTD != values.Get("motd") {
		t.Fatal("name and MOTD were not stored separately", draft)
	}
	request, err := setupPlanRequestFromDraft(draft)
	if err != nil || request.Server.DisplayName != "Family Survival" {
		t.Fatal(request, err)
	}
	client.instances = append(client.instances, api.RegisteredMinecraftInstance{ID: "jv-exist01", DisplayName: "Existing server"})
	values.Set("display_name", "Existing server")
	if response := saveServerStep(t, app, values); response.Code != http.StatusBadRequest {
		t.Fatal("duplicate accepted")
	}
	draft.Mode = "recommended"
	draft.DisplayName = ""
	request, err = setupPlanRequestFromDraft(draft)
	if err != nil || request.Server.DisplayName != "" {
		t.Fatal("unnamed Recommended request gained a name")
	}
}

func TestAdvancedFirstRunReviewAndDownloadShowReviewedName(t *testing.T) {
	client := setupReviewClient()
	client.plan.Normalized.Server.DisplayName = "Family Survival"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.renderSetupReview(response, api.SessionInfo{}, setupReviewState{Plan: client.plan, SetupMode: "advanced", DisplayName: client.plan.Normalized.Server.DisplayName}, "csrf-token", "")
	if !strings.Contains(response.Body.String(), "<dt>Server name</dt><dd>Family Survival</dd>") {
		t.Fatal(response.Body.String())
	}
	snapshot := setupConfigurationSnapshot(client.plan)
	if !strings.Contains(snapshot, "Server name: Family Survival\n") || !strings.Contains(snapshot, "Welcome message:") {
		t.Fatal(snapshot)
	}
}
