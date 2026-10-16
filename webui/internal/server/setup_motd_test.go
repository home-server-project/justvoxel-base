package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// The planner fixture returns an authoritative resolved plan. Generation itself
// is tested at the Management Agent boundary, before plan fingerprinting.
type setupMOTDPlanningClient struct {
	*fakeSetupPlanningAPI
	resolvedMOTD string
}

func (f *setupMOTDPlanningClient) AdminSetupPlan(ctx context.Context, session string, request api.AdminSetupPlanRequest) (api.AdminSetupPlanResponse, error) {
	plan, err := f.fakeSetupPlanningAPI.AdminSetupPlan(ctx, session, request)
	plan.Normalized.Minecraft.ServerType = request.Minecraft.ServerType
	if request.Server.MOTDAutomatic {
		plan.Normalized.Server.MOTD = f.resolvedMOTD
	} else {
		plan.Normalized.Server.MOTD = request.Server.MOTD
	}
	return plan, err
}

func TestRecommendedSetupAutomaticMOTD(t *testing.T) {
	for _, tc := range []struct{ software, version, want string }{
		{"paper", "26.3", "JustVoxel Paper Minecraft 26.3 Server"},
		{"purpur", "26.2", "JustVoxel Purpur Minecraft 26.2 Server"},
		{"vanilla", "26.3", "JustVoxel Vanilla Minecraft 26.3 Server"},
	} {
		t.Run(tc.software, func(t *testing.T) {
			client := &setupMOTDPlanningClient{fakeSetupPlanningAPI: setupReviewClient(), resolvedMOTD: tc.want}
			client.plan.Normalized.Minecraft.Version = tc.version
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			values := url.Values{"csrf": {"csrf-token"}, "server_type": {tc.software}}
			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recommended", values.Encode()))
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("recommended: %d %s", rr.Code, rr.Body.String())
			}
			rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("review: %d %s", rr.Code, rr.Body.String())
			}
			if !client.lastRequest.Server.MOTDAutomatic {
				t.Fatal("recommended setup did not request automatic MOTD")
			}
			rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review/configuration", ""))
			if !strings.Contains(rr.Body.String(), "Welcome message: "+tc.want) || !strings.Contains(rr.Body.String(), "Server software: "+setupServerTypeLabel(tc.software)) {
				t.Fatal("snapshot lost final software or generated MOTD")
			}
			if client.planHit != 1 {
				t.Fatal("review unnecessarily replanned generated MOTD")
			}
		})
	}
}

func TestAdvancedSetupMOTDStateSurvivesSoftwareAndVersionChanges(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		automatic     bool
	}{
		{"automatic", "", true},
		{"empty custom", "", false},
		{"custom", "  Our Minecraft §a server  ", false},
		{"old default custom", "JustVoxel Java and Bedrock Server", false},
		{"generated-looking custom", "JustVoxel Paper Minecraft 26.3 Server", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			custom := tc.message
			client := &setupMOTDPlanningClient{fakeSetupPlanningAPI: setupReviewClient(), resolvedMOTD: "JustVoxel Vanilla Minecraft 26.3 Server"}
			client.plan.Normalized.Minecraft.Version = "26.3"
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			advanceSetupToReview(t, app)
			for _, software := range []string{"paper", "purpur", "vanilla"} {
				values := validServerValues()
				values.Set("server_type", software)
				values.Set("motd", custom)
				values.Set("motd_automatic", strconv.FormatBool(tc.automatic))
				rr := saveServerStep(t, app, values)
				if rr.Code != http.StatusSeeOther {
					t.Fatalf("server: %d %s", rr.Code, rr.Body.String())
				}
				versions := validConnectionValues()
				versions.Set("version_policy", "pinned")
				versions.Set("version", "26.2")
				if software == "vanilla" {
					versions.Del("bedrock_enabled")
					versions.Set("version_policy", "latest")
					versions.Set("version", "")
				}
				rr = saveConnectionsStep(t, app, versions)
				if rr.Code == http.StatusSeeOther {
					rr = saveResourcesStep(t, app, validResourceValues())
				}
				if rr.Code != http.StatusSeeOther {
					t.Fatalf("version: %d %s", rr.Code, rr.Body.String())
				}
				draft, _ := firstRunSetupDrafts.get(app, "session-token")
				if draft.Server.MOTD != custom || draft.Server.MOTDAutomatic != tc.automatic {
					t.Fatalf("MOTD state changed: %#v", draft.Server)
				}
				rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=1"))
				if rr.Code != http.StatusSeeOther {
					t.Fatalf("navigate: %d %s", rr.Code, rr.Body.String())
				}
				rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
				body := rr.Body.String()
				if rr.Code != http.StatusOK || !strings.Contains(body, `type="hidden" name="motd_automatic" value="`+strconv.FormatBool(tc.automatic)+`"`) || !strings.Contains(body, `name="motd" value="`+html.EscapeString(custom)+`"`) {
					t.Fatalf("revisited server step lost MOTD state: %d %s", rr.Code, body)
				}
				if tc.automatic && !strings.Contains(body, `placeholder="Automatic: JustVoxel `+setupServerTypeLabel(software)+` Minecraft &lt;resolved version&gt; Server"`) {
					t.Fatal("revisited automatic placeholder lost selected software")
				}
			}
			draft, _ := firstRunSetupDrafts.get(app, "session-token")
			draft.CurrentStep = setupReviewStep
			firstRunSetupDrafts.save(app, "session-token", draft)
			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
			if rr.Code != http.StatusOK {
				t.Fatalf("review: %d %s", rr.Code, rr.Body.String())
			}
			state, _ := firstRunSetupReviews.get(app, "session-token")
			want := custom
			if tc.automatic {
				want = client.resolvedMOTD
			}
			if state.Plan.Normalized.Server.MOTD != want || !strings.Contains(rr.Body.String(), want) {
				t.Fatalf("final review MOTD = %q, want %q", state.Plan.Normalized.Server.MOTD, want)
			}
			rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review/configuration", ""))
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Welcome message: "+want+"\n") {
				t.Fatal("advanced snapshot lost final MOTD")
			}
			if client.lastRequest.Server.MOTDAutomatic != tc.automatic {
				t.Fatal("explicit MOTD state lost in plan request")
			}
			if client.lastRequest.Minecraft.ServerType != "vanilla" || client.lastRequest.Minecraft.VersionPolicy != "latest" || client.lastRequest.Minecraft.Version != "" {
				t.Fatal("final selection lost")
			}
		})
	}
}

func TestSetupMOTDPlaceholderAndExplicitCustomState(t *testing.T) {
	source, err := assets.ReadFile("static/setup-motd.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `const assert = require('node:assert/strict');
const vm = require('node:vm');
const source = ` + strconv.Quote(string(source)) + `;
function open(value = '', mode = 'true', software = 'paper') {
  const message = { value, placeholder: '', events: {}, addEventListener(event, handler) { this.events[event] = handler; } };
  const automatic = { value: mode };
  const choices = ['paper', 'purpur', 'vanilla'].map(value => ({ value, checked: value === software, events: {}, addEventListener(event, handler) { this.events[event] = handler; } }));
  const document = {
    querySelector: selector => selector === '[data-setup-motd]' ? message : automatic,
    querySelectorAll: selector => { assert.equal(selector, 'input[name="server_type"]'); return choices; }
  };
  vm.runInNewContext(source, { document });
  return { message, automatic, select(value) {
    for (const choice of choices) choice.checked = choice.value === value;
    choices.find(choice => choice.checked).events.change();
  } };
}
const page = open();
for (const [software, label] of [['paper', 'Paper'], ['purpur', 'Purpur'], ['vanilla', 'Vanilla']]) {
  page.select(software);
  assert.equal(page.message.placeholder, 'Automatic: JustVoxel ' + label + ' Minecraft <resolved version> Server');
  assert.equal(page.message.value, '');
  assert.equal(page.automatic.value, 'true');
  assert.equal(open('', 'true', software).message.placeholder, page.message.placeholder);
}
for (const custom of ['', '  Our §a server  ', 'JustVoxel Paper Minecraft 26.3 Server']) {
  const edited = open();
  edited.message.value = custom;
  edited.message.events.input();
  assert.equal(edited.automatic.value, 'false');
  assert.equal(edited.message.placeholder, '');
  for (const software of ['paper', 'purpur', 'vanilla']) {
    edited.select(software);
    assert.equal(edited.message.value, custom);
    assert.equal(edited.automatic.value, 'false');
    assert.equal(edited.message.placeholder, '');
    const revisited = open(custom, 'false', software);
    revisited.select('paper');
    assert.equal(revisited.message.value, custom);
    assert.equal(revisited.automatic.value, 'false');
    assert.equal(revisited.message.placeholder, '');
  }
}
`
	if output, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("MOTD input behavior: %v\n%s", err, output)
	}
}

func TestSetupMOTDHasNoVisibleAutomaticControl(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	landing := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if landing.Code != http.StatusOK || strings.Contains(landing.Body.String(), `name="motd"`) || strings.Contains(landing.Body.String(), `name="motd_automatic"`) {
		t.Fatal("setup landing added a MOTD prompt")
	}
	startSetup(t, app)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := rr.Body.String()
	for _, forbidden := range []string{"Use automatic welcome message", `type="checkbox" name="motd_automatic"`, "JustVoxel Java and Bedrock Server"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Advanced setup contains %q", forbidden)
		}
	}
	for _, required := range []string{`type="hidden" name="motd_automatic" value="true"`, `name="motd" value=""`, `placeholder="Automatic: JustVoxel Paper Minecraft &lt;resolved version&gt; Server"`} {
		if rr.Code != http.StatusOK || !strings.Contains(body, required) {
			t.Fatalf("Advanced setup missing %q: %d %s", required, rr.Code, body)
		}
	}
	if client.planHit != 0 {
		t.Fatal("Step 1 requested a resolved setup plan")
	}
}

// Keep actual instance state separate from the retired fixed-server discovery.
type postSetupMOTDClient struct {
	*fakeMinecraftWorkspaceAPI
	runtime                  api.InstanceRuntimeSettings
	fixedPlans, fixedApplies int
}

func (f *postSetupMOTDClient) MinecraftInstanceConfiguration(_ context.Context, _, id string) (api.InstanceRuntimeSettings, error) {
	f.instanceReads = append(f.instanceReads, id)
	if id != "jv-family1" {
		return api.InstanceRuntimeSettings{}, &api.ResponseError{StatusCode: http.StatusNotFound}
	}
	return f.runtime, nil
}

func (f *postSetupMOTDClient) SetMinecraftInstanceConfiguration(ctx context.Context, session, id string, settings api.InstanceRuntimeSettings) error {
	if id != "jv-family1" {
		return &api.ResponseError{StatusCode: http.StatusNotFound}
	}
	if err := f.fakeMinecraftWorkspaceAPI.SetMinecraftInstanceConfiguration(ctx, session, id, settings); err != nil {
		return err
	}
	f.runtime = settings
	return nil
}

func (f *postSetupMOTDClient) AdminConfigurationPlan(context.Context, string, api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	f.fixedPlans++
	return api.AdminConfigurationChangeResponse{}, &api.ResponseError{StatusCode: http.StatusGone}
}

func (f *postSetupMOTDClient) AdminConfigurationApply(context.Context, string, api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	f.fixedApplies++
	return api.AdminConfigurationChangeResponse{}, &api.ResponseError{StatusCode: http.StatusGone}
}

func TestPostSetupMOTDRemainsExplicitConfiguration(t *testing.T) {
	client := &postSetupMOTDClient{fakeMinecraftWorkspaceAPI: configuredMinecraftWorkspaceAPI()}
	client.configuration.Minecraft.ServerType = "paper"
	client.configuration.Minecraft.Version = "26.3"
	client.configuration.Minecraft.MOTD = "JustVoxel Paper Minecraft 26.3 Server"
	before, err := client.fakeMinecraftWorkspaceAPI.MinecraftInstanceConfiguration(context.Background(), "session-token", "jv-family1")
	if err != nil {
		t.Fatal(err)
	}
	client.runtime = before
	values := settingsFormValues()
	values.Set("motd", "My new welcome message")
	values.Set("max_players", "10")
	values.Set("tab", "gameplay")
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/plan", values.Encode()))
	var plan struct {
		Review  string                         `json:"review"`
		Changes []api.AdminConfigurationChange `json:"changes"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &plan) != nil || plan.Review == "" || len(plan.Changes) != 1 || plan.Changes[0].Field != "motd" || len(client.instanceWrites) != 0 {
		t.Fatalf("post-setup MOTD review: %d %s", rr.Code, rr.Body.String())
	}
	values.Set("review", plan.Review)
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/minecraft/workspace/settings/apply", values.Encode()))
	want := configurationRequestFromDiscovery(client.configuration)
	want.MOTD = "My new welcome message"
	if rr.Code != http.StatusOK || !reflect.DeepEqual(client.instanceWrites, []string{"jv-family1"}) || !reflect.DeepEqual(client.applied, want) {
		t.Fatalf("post-setup MOTD apply: %d writes=%v request=%#v", rr.Code, client.instanceWrites, client.applied)
	}
	wantRuntime := before
	wantRuntime.MOTD = "My new welcome message"
	if !reflect.DeepEqual(client.runtime, wantRuntime) || client.fixedPlans != 0 || client.fixedApplies != 0 {
		t.Fatal("MOTD edit changed other settings or used fixed-server configuration")
	}
	for _, path := range []string{"/setup/start", "/setup/recommended"} {
		rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example"+path, "csrf=csrf-token&server_type=purpur"))
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
			t.Fatal("configured server entered first-run setup")
		}
	}
	if client.configuration.Minecraft.MOTD != before.MOTD || len(client.instanceWrites) != 1 {
		t.Fatal("first-run setup changed existing server MOTD")
	}
}
