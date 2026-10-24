package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func (f *addServerTestAPI) AdminSetupNativeBedrockPreview(_ context.Context, _ string, players int) (api.AdminVersionStatus, error) {
	return api.AdminVersionStatus{ServerType: "bedrock", ServerSupported: true, SelectedCandidate: "1.26.50.1", CandidateChannel: "RELEASE", ImageTag: "stable", BedrockTCPPort: 19132, BedrockUDPStart: 19140, BedrockUDPEnd: 19140 + players - 1}, nil
}
func TestSimpleNativeBedrockStatesAndReview(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	input := regexp.MustCompile(`<input[^>]+name="family"[^>]+value="original"[^>]*>`).FindString(body)
	if input == "" || strings.Contains(input, "disabled") || strings.Contains(body, "Not implemented") {
		t.Fatal("Bedrock family unavailable")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"family": {"original"}}))
	body = addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	if strings.Count(body, `name="server_type"`) != 2 || !strings.Contains(body, "Native Bedrock server · no plugins or mods") {
		t.Fatal("native software card missing")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"server_type": {"bedrock"}}))
	body = addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	for _, field := range []string{"timezone", "java_port", "bedrock_tcp_port", "bedrock_udp_start", "image_tag", "version", "java_memory"} {
		if strings.Contains(body, `name="`+field+`"`) {
			t.Fatal("Simple exposed internal setting", field)
		}
	}
	if strings.Contains(body, `value="spectator"`) {
		t.Fatal("Bedrock offered Spectator")
	}
	for _, mode := range []string{"survival", "creative", "adventure"} {
		if !strings.Contains(body, `value="`+mode+`"`) {
			t.Fatal("Bedrock game mode missing", mode)
		}
	}
	if rr := addWizardRequest(app, http.MethodPost, "/simple", url.Values{"display_name": {"Bedrock family"}, "game_mode": {"spectator"}}); rr.Code != 400 {
		t.Fatal("Bedrock Spectator accepted")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"display_name": {"Bedrock family"}, "game_mode": {"survival"}}))
	draft, _ := addServerDrafts.get(app, "session-token")
	if draft.CurrentStep != setupReviewStep || draft.Minecraft.Version != "1.26.50.1" || draft.Minecraft.VersionPolicy != "pinned" || draft.Server.BedrockEnabled {
		t.Fatal("native runtime resolution lost", draft.Minecraft)
	}
	request, err := setupPlanRequestFromDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(request)
	if strings.Contains(string(raw), `"timezone"`) {
		t.Fatal("independent timezone in plan")
	}
	rr := addWizardRequest(app, http.MethodGet, "/review", nil)
	body = rr.Body.String()
	if rr.Code != 200 {
		t.Fatal(rr.Code, body)
	}
	for _, want := range []string{"Bedrock", "1.26.50.1", "Native Bedrock connection", "TCP signaling port", "UDP gameplay range", "Start on Boot", "Start after setup"} {
		if !strings.Contains(body, want) {
			t.Fatal("native Review missing", want)
		}
	}
	for _, forbidden := range []string{"<dt>Timezone</dt>", "<dt>Java</dt>", "Minecraft game memory", "Server update channel", "Geyser", "Floodgate"} {
		if strings.Contains(body, forbidden) {
			t.Fatal("native Review exposes Java/timezone", forbidden)
		}
	}
}
func TestAdvancedNativeBedrockSixStepsAndValidation(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	server := validServerValues()
	server.Set("server_type", "bedrock")
	server.Set("display_name", "Bedrock world")
	server.Set("game_mode", "spectator")
	if rr := addWizardRequest(app, http.MethodPost, "/server", server); rr.Code != 400 {
		t.Fatal("Advanced Bedrock Spectator accepted", rr.Code)
	}
	server.Set("game_mode", "adventure")
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/server", server))
	body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	for _, want := range []string{"Native Bedrock connection", "TCP signaling port", "UDP gameplay range", "1.26.50.1"} {
		if !strings.Contains(body, want) {
			t.Fatal("native Step 2 missing", want)
		}
	}
	for _, forbidden := range []string{`name="timezone"`, `name="java_port"`, `name="bedrock_enabled"`, `name="image_tag"`, `java21`, `Geyser`, `Floodgate`} {
		if strings.Contains(body, forbidden) {
			t.Fatal("Java field in native Step 2", forbidden)
		}
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/connections", url.Values{"direction": {"next"}}))
	body = addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	if strings.Contains(body, `name="java_memory"`) || strings.Contains(body, "Java heap") || !strings.Contains(body, `name="container_memory"`) {
		t.Fatal("Bedrock memory uses Java heap")
	}
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/resources", url.Values{"container_memory": {"2G"}, "direction": {"next"}}))
	draft, _ := addServerDrafts.get(app, "session-token")
	if draft.CurrentStep != setupStorageStep || draft.Minecraft.JavaMemory != "" || len(setupWizardSteps) != 6 {
		t.Fatal("native Advanced step progression changed")
	}
}
