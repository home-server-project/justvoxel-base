package server

import (
	"context"
	"fmt"
	"github.com/home-server-project/justvoxel-webui/internal/api"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func sharedWizardRequest(app *App, method, base, suffix string, values url.Values) *httptest.ResponseRecorder {
	if values == nil {
		values = url.Values{}
	}
	if method == http.MethodPost {
		values.Set("csrf", "csrf-token")
	}
	return httptestResponse(app, authenticatedAdminRequest(method, "http://example"+base+suffix, values.Encode()))
}

func completeSimpleSetup(t *testing.T, app *App, base, software string) *httptest.ResponseRecorder {
	t.Helper()
	family := ""
	for _, candidate := range []string{"plugins", "mods", "original"} {
		for _, choice := range simpleSoftware(candidate) {
			if choice.ID == software {
				family = candidate
			}
		}
	}
	for _, values := range []url.Values{{"family": {family}}, {"server_type": {software}}} {
		rr := sharedWizardRequest(app, http.MethodPost, base, "/simple", values)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("Simple step: %d %s", rr.Code, rr.Body.String())
		}
	}
	return sharedWizardRequest(app, http.MethodPost, base, "/simple", url.Values{"display_name": {"Family"}, "game_mode": {"survival"}})
}

func TestSharedWizardPresentationAndAllSoftware(t *testing.T) {
	var advanced string
	for _, addServer := range []bool{false, true} {
		for _, software := range []string{"paper", "purpur", "spigot", "fabric", "neoforge", "forge", "quilt", "vanilla", "bedrock"} {
			t.Run(fmt.Sprintf("add=%t/%s", addServer, software), func(t *testing.T) {
				app, client := newAddServerTestApp(t)
				client.configuration.Configured = addServer
				loader := ""
				if setupNeedsExactRuntime(software) && software != "spigot" && software != "bedrock" {
					loader = "0.18.0"
				}
				preview := &exactSetupPreviewAPI{addServerTestAPI: client, status: api.AdminVersionStatus{SelectedCandidate: "1.21.8", ImageTag: setupRuntimeImage(software, "1.21.8"), LoaderVersion: loader}}
				var err error
				app, err = New(preview, Config{Version: "test", ManagementAPI: "v1"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { setupDrafts.delete(app, "session-token"); setupReviews.delete(app, "session-token") })
				base := "/setup"
				if addServer {
					base = addServerBasePath
				}
				// Exact preview fixtures support all nine software choices in both contexts.
				body := sharedWizardRequest(app, http.MethodGet, base, "", nil).Body.String()
				families := regexp.MustCompile(`name="family" value="([^"]+)"`).FindAllStringSubmatch(body, -1)
				var got []string
				for _, family := range families {
					got = append(got, family[1])
				}
				if !reflect.DeepEqual(got, []string{"plugins", "mods", "original"}) {
					t.Fatalf("families: %v", got)
				}
				for _, want := range []string{"Simple setup", "setup-entry-wizard", "setup-simple-families", "server-software-card"} {
					if !strings.Contains(body, want) {
						t.Fatalf("missing shared class/copy %s", want)
					}
				}
				rr := completeSimpleSetup(t, app, base, software)
				if rr.Code != http.StatusSeeOther {
					t.Fatalf("final Simple step: %d %s", rr.Code, rr.Body.String())
				}
				draft, _ := setupDrafts.get(app, "session-token")
				if !setupDraftReadyForReview(draft) || draft.AddServer != addServer || draft.DisplayName != "Family" || draft.Minecraft.ServerType != software {
					t.Fatalf("wrong ready draft: %+v", draft)
				}
				page := sharedWizardRequest(app, http.MethodGet, base, "/review", nil)
				if page.Code != http.StatusOK {
					t.Fatalf("Review: %d %s", page.Code, page.Body.String())
				}
				if client.lastRequest.AddServer != addServer || !client.lastRequest.MountedOnly {
					t.Fatal("backend context lost")
				}
				if !addServer && (draft.Storage.Type != "system" || draft.Backups.Type != "system" || !draft.Backups.Automatic) {
					t.Fatal("initial placement lost")
				}
				back := sharedWizardRequest(app, http.MethodPost, base, "/review/back", nil)
				if back.Code != http.StatusSeeOther {
					t.Fatal("Review Back failed")
				}
				body = sharedWizardRequest(app, http.MethodGet, base, "", nil).Body.String()
				if !strings.Contains(body, `name="display_name"`) {
					t.Fatal("Back lost final Simple choices")
				}
				cancel := sharedWizardRequest(app, http.MethodPost, base, "/cancel", nil)
				expectedCancel := http.StatusSeeOther
				if addServer {
					expectedCancel = http.StatusOK
				}
				if cancel.Code != expectedCancel {
					t.Fatalf("Cancel: %d", cancel.Code)
				}
				if _, ok := setupDrafts.get(app, "session-token"); ok {
					t.Fatal("Cancel retained shared draft")
				}
				if _, ok := setupReviews.get(app, "session-token"); ok {
					t.Fatal("Cancel retained shared Review")
				}
				rr = sharedWizardRequest(app, http.MethodPost, base, "/start", nil)
				if rr.Code != http.StatusSeeOther {
					t.Fatal("Advanced start failed")
				}
				body = sharedWizardRequest(app, http.MethodGet, base, "", nil).Body.String()
				lanes := regexp.MustCompile(`(?s)<div class="setup-software-groups setup-software-lanes">.*?</fieldset>\s*</div>`).FindString(body)
				if lanes == "" {
					t.Fatal("shared Advanced lanes missing")
				}
				if advanced == "" {
					advanced = lanes
				} else if lanes != advanced {
					t.Fatal("Advanced software layout differs between contexts")
				}
				inputs := regexp.MustCompile(`name="server_type" value="([^"]+)"`).FindAllStringSubmatch(lanes, -1)
				got = nil
				for _, input := range inputs {
					got = append(got, input[1])
				}
				if !reflect.DeepEqual(got, []string{"paper", "purpur", "spigot", "fabric", "neoforge", "forge", "quilt", "vanilla", "bedrock"}) {
					t.Fatalf("Advanced order: %v", got)
				}
				for _, field := range []string{"display_name", "start_after_setup", "start_on_boot"} {
					if !strings.Contains(body, `name="`+field+`"`) {
						t.Fatalf("server control missing: %s", field)
					}
				}
			})
		}
	}
}

type sharedSetupDiagnosticClient struct {
	*addServerTestAPI
	starts int
	events []string
}

func (f *sharedSetupDiagnosticClient) AdminSetupDiagnosticSession(context.Context, string, string) (api.AdminSetupDiagnosticSessionResponse, error) {
	f.starts++
	return api.AdminSetupDiagnosticSessionResponse{SessionID: setupExecutionOperationID}, nil
}
func (f *sharedSetupDiagnosticClient) AdminSetupDiagnosticEvent(_ context.Context, _, _, event string, _ map[string]string) error {
	f.events = append(f.events, event)
	return nil
}

func TestSharedWizardFirstSetupDiagnosticsAndStartup(t *testing.T) {
	for _, simple := range []bool{false, true} {
		_, base := newAddServerTestApp(t)
		base.configuration.Configured = false
		client := &sharedSetupDiagnosticClient{addServerTestAPI: base}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { setupDrafts.delete(app, "session-token"); setupReviews.delete(app, "session-token") })
		var rr *httptest.ResponseRecorder
		if simple {
			rr = completeSimpleSetup(t, app, "/setup", "paper")
		} else {
			rr = sharedWizardRequest(app, http.MethodPost, "/setup", "/start", nil)
		}
		if rr.Code != http.StatusSeeOther {
			t.Fatal(rr.Code, rr.Body.String())
		}
		draft, _ := setupDrafts.get(app, "session-token")
		if client.starts != 1 || len(client.events) < 2 || draft.DiagnosticSessionID != setupExecutionOperationID {
			t.Fatal("First Setup diagnostics lost")
		}
		if !draft.StartOnBoot || !draft.StartAfterSetup {
			t.Fatal("fresh First Setup startup defaults lost")
		}
		if !simple {
			// Both controls persist through the same Advanced handler and plan contract.
			values := validServerValues()
			values.Set("start_after_setup", "on")
			rr = sharedWizardRequest(app, http.MethodPost, "/setup", "/server", values)
			if rr.Code != http.StatusSeeOther {
				t.Fatal(rr.Code, rr.Body.String())
			}
			draft, _ = setupDrafts.get(app, "session-token")
		}
		request, err := setupPlanRequestFromDraft(draft)
		if err != nil || request.AddServer || request.DiagnosticSessionID != setupExecutionOperationID || !request.StartAfterSetup || request.StartOnBoot != simple {
			t.Fatalf("First Setup plan context/startup lost: %+v %v", request, err)
		}
	}
}

func TestSharedWizardFreshAddLaunchPreservesFirstSetupOperation(t *testing.T) {
	for _, configured := range []bool{false, true} {
		app, client := newAddServerTestApp(t)
		client.configuration.Configured = configured
		client.current = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: "needs_attention", InstanceSetup: true}
		setupDrafts.save(app, "session-token", setupDraft{Started: true, DisplayName: "First server"})
		before, _ := setupDrafts.get(app, "session-token")
		setupReviews.save(app, "session-token", setupReviewState{DraftUpdatedAt: before.UpdatedAt})
		rr := addWizardRequest(app, http.MethodGet, "?new=1", nil)
		want := "/setup"
		if configured {
			want = "/setup/progress/" + setupExecutionOperationID
		}
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != want {
			t.Fatalf("fresh Add launch bypassed first operation: %d %s", rr.Code, rr.Body.String())
		}
		if after, ok := setupDrafts.get(app, "session-token"); !ok || !reflect.DeepEqual(before, after) {
			t.Fatal("first operation draft erased")
		}
		if _, ok := setupReviews.get(app, "session-token"); !ok {
			t.Fatal("first operation Review erased")
		}
	}
}

func TestSharedWizardStaleFirstSetupTabPreservesAddServer(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	_ = recommendedAddReview(t, app, "paper")
	before, _ := setupDrafts.get(app, "session-token")
	review, _ := setupReviews.get(app, "session-token")
	for _, suffix := range []string{"", "/start", "/simple", "/cancel", "/review/back", "/review/cancel", "/review/apply"} {
		method := http.MethodPost
		if suffix == "" {
			method = http.MethodGet
		}
		rr := sharedWizardRequest(app, method, "/setup", suffix, nil)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
			t.Fatal("stale first setup tab did not leave wizard")
		}
		if after, ok := setupDrafts.get(app, "session-token"); !ok || !reflect.DeepEqual(before, after) {
			t.Fatal("stale first setup tab altered Add Server draft")
		}
		if after, ok := setupReviews.get(app, "session-token"); !ok || !reflect.DeepEqual(review, after) {
			t.Fatal("stale first setup tab altered Add Server Review")
		}
	}
}
