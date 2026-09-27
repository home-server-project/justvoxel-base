package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const setupExecutionOperationID = "01234567-89ab-4cde-8f01-23456789abcd"

type fakeSetupExecutionAPI struct {
	*fakeSetupPlanningAPI
	applyResponse api.AdminSetupApplyResponse
	applyErr      error
	applyRequest  api.AdminSetupApplyRequest
	applyCalls    int
	recoverCalls  int
	recoverID     string
	current       *api.PersistentOperation
	operation     *api.PersistentOperation
}

func (f *fakeSetupExecutionAPI) AdminSetupRecover(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	f.recoverCalls++
	f.recoverID = id
	return api.PersistentOperationResponse{Operation: f.operation}, nil
}

func setupExecutionClient() *fakeSetupExecutionAPI {
	return &fakeSetupExecutionAPI{fakeSetupPlanningAPI: setupReviewClient()}
}

func (f *fakeSetupExecutionAPI) AdminSetupApply(_ context.Context, session string, request api.AdminSetupApplyRequest) (api.AdminSetupApplyResponse, error) {
	if session != "session-token" {
		return api.AdminSetupApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyRequest = request
	return f.applyResponse, f.applyErr
}

func (f *fakeSetupExecutionAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.operation == nil || f.operation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	copy := *f.operation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func (f *fakeSetupExecutionAPI) AdminCurrentSetupOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.current == nil {
		return api.PersistentOperationResponse{}, nil
	}
	copy := *f.current
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func acceptSetupEULA(t *testing.T, app *App) {
	t.Helper()
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/eula", eulaForm(true, setupReviewFingerprint)))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("EULA acceptance returned %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSetupReviewConfigureActionRequiresAcceptedEULA(t *testing.T) {
	client := setupExecutionClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	body := page.Body.String()
	for _, want := range []string{
		`action="/setup/review/apply"`,
		`data-eula-accepted="false"`,
		`data-setup-eula-dialog`,
		`name="eula_accepted" value="on"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pre-EULA review missing %q: %s", want, body)
		}
	}

	acceptSetupEULA(t, app)
	page = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	body = page.Body.String()
	if !strings.Contains(body, `data-eula-accepted="true"`) {
		t.Fatalf("Configure state did not reflect EULA acceptance: %s", body)
	}
	if !strings.Contains(body, `<button type="submit" form="setup-apply-form" data-setup-configure>Configure JustVoxel</button>`) || strings.Contains(body, "setup-configure-disabled") {
		t.Fatalf("enabled primary Configure JustVoxel button missing: %s", body)
	}
}

func TestSetupReviewRequestsSMBPasswordOnlyAtExecution(t *testing.T) {
	client := setupExecutionClient()
	client.plan.Requirements.SMBPasswordRequired = true
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if !strings.Contains(page.Body.String(), `name="smb_password"`) || !strings.Contains(page.Body.String(), "It is not stored in the setup draft or operation journal.") {
		t.Fatalf("SMB execution secret control missing: %s", page.Body.String())
	}
	applyStart := strings.Index(page.Body.String(), `id="setup-apply-form"`)
	if applyStart < 0 {
		t.Fatal("review Apply form missing")
	}
	applyEnd := strings.Index(page.Body.String()[applyStart:], `</form>`)
	if applyEnd < 0 || !strings.Contains(page.Body.String(), `data-setup-password-dialog`) || strings.Contains(page.Body.String()[applyStart:applyStart+applyEnd], `name="smb_password"`) {
		t.Fatal("SMB password must be requested in the modal after Review")
	}

	client.plan.Requirements.SMBPasswordRequired = false
	firstRunSetupReviews.delete(app, "session-token")
	page = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if strings.Contains(page.Body.String(), `name="smb_password"`) {
		t.Fatal("SMB password field rendered when the reviewed plan does not require it")
	}
}

func TestSetupReviewApplyStartsExactReviewedOperation(t *testing.T) {
	client := setupExecutionClient()
	client.plan.Requirements.SMBPasswordRequired = true
	client.applyResponse = api.AdminSetupApplyResponse{
		OK: true, Created: true,
		Operation: &api.PersistentOperation{
			SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup",
			PlanFingerprint: setupReviewFingerprint, State: "queued", Stage: "queued", Status: "Setup operation queued.",
			StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:00:00Z",
			Rollback: api.PersistentOperationRollback{State: "not_started"},
		},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	acceptSetupEULA(t, app)

	values := url.Values{
		"csrf":             {"csrf-token"},
		"plan_fingerprint": {setupReviewFingerprint},
		"smb_password":     {"family-secret"},
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/apply", values.Encode()))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID {
		t.Fatalf("setup apply returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	if client.applyCalls != 1 {
		t.Fatalf("apply calls = %d, want 1", client.applyCalls)
	}
	if !client.applyRequest.EULAAccepted || client.applyRequest.PlanFingerprint != setupReviewFingerprint || client.applyRequest.SMBPassword != "family-secret" {
		t.Fatalf("unexpected setup apply request: %#v", client.applyRequest)
	}
	state, ok := firstRunSetupReviews.get(app, "session-token")
	if !ok || client.applyRequest.Request != state.Request {
		t.Fatalf("apply did not use the exact reviewed request: %#v vs %#v", client.applyRequest.Request, state.Request)
	}
}

func TestSetupReviewApplyDoesNotMutateBeforeEULA(t *testing.T) {
	client := setupExecutionClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))

	values := url.Values{"csrf": {"csrf-token"}, "plan_fingerprint": {setupReviewFingerprint}}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/apply", values.Encode()))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Accept the Minecraft End User License Agreement") {
		t.Fatalf("apply without EULA returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.applyCalls != 0 {
		t.Fatalf("setup apply API called %d times before EULA acceptance", client.applyCalls)
	}
}

func TestSetupReviewAcceptAndContinueBindsEULAToCurrentPlan(t *testing.T) {
	client := setupExecutionClient()
	client.applyResponse = api.AdminSetupApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{OperationID: setupExecutionOperationID}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	stale := url.Values{"csrf": {"csrf-token"}, "plan_fingerprint": {"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, "eula_accepted": {"on"}}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/apply", stale.Encode()))
	if rr.Code != http.StatusConflict || client.applyCalls != 0 {
		t.Fatalf("stale EULA started setup: %d, calls=%d", rr.Code, client.applyCalls)
	}
	state, _ := firstRunSetupReviews.get(app, "session-token")
	if state.EULAAccepted {
		t.Fatal("stale plan retained EULA acceptance")
	}

	current := url.Values{"csrf": {"csrf-token"}, "plan_fingerprint": {setupReviewFingerprint}, "eula_accepted": {"on"}}
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/apply", current.Encode()))
	if rr.Code != http.StatusSeeOther || client.applyCalls != 1 || !client.applyRequest.EULAAccepted {
		t.Fatalf("accept and continue failed: %d, calls=%d", rr.Code, client.applyCalls)
	}
	state, _ = firstRunSetupReviews.get(app, "session-token")
	if !state.EULAAccepted {
		t.Fatal("current plan EULA acceptance was not recorded")
	}
}

func TestSetupProgressPageAndJSONTrackSamePersistentOperation(t *testing.T) {
	client := setupExecutionClient()
	client.operation = &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup",
		PlanFingerprint: setupReviewFingerprint, State: "running", Stage: "runtime_config",
		Status:    "Writing transactional Minecraft runtime configuration.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:01:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/progress/"+setupExecutionOperationID, ""))
	if page.Code != http.StatusOK {
		t.Fatalf("progress page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Configuring JustVoxel",
		"Writing transactional Minecraft runtime configuration.",
		"/api/setup/progress/" + setupExecutionOperationID,
		"/static/setup-operation.js",
		"Reconnecting to JustVoxel",
		"Refreshing or reopening this page will not restart setup.",
		`aria-label="Setup stages"`, `data-setup-stage`, `data-started-at="2026-09-20T12:00:00Z"`,
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("progress page missing %q: %s", want, page.Body.String())
		}
	}

	status := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/setup/progress/"+setupExecutionOperationID, ""))
	if status.Code != http.StatusOK {
		t.Fatalf("progress API returned %d: %s", status.Code, status.Body.String())
	}
	var response api.PersistentOperationResponse
	if err := json.Unmarshal(status.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.OperationID != setupExecutionOperationID || response.Operation.State != "running" {
		t.Fatalf("unexpected progress response: %#v", response)
	}
}

func TestSetupProgressTerminalActions(t *testing.T) {
	for _, tc := range []struct {
		state   string
		visible []string
		hidden  []string
	}{
		{"succeeded", []string{`id="setup-dashboard-link" href="/"`, `id="setup-diagnostic-log-view"`, `id="setup-diagnostic-log-link"`}, []string{`id="setup-start-over-form" hidden`, `id="setup-start-over-disabled" role="group" aria-disabled="true" tabindex="0" aria-describedby="setup-start-over-help" hidden`}},
		{"rolled_back", []string{`id="setup-dashboard-link" href="/"`, `id="setup-review-link" href="/setup/review"`, `id="setup-start-over-form"`, `action="/setup/start"`}, []string{`id="setup-start-over-disabled" role="group" aria-disabled="true" tabindex="0" aria-describedby="setup-start-over-help" hidden`}},
		{"needs_attention", []string{`id="setup-dashboard-link" href="/"`, `id="setup-start-over-disabled" role="group"`, `Resolve the recovery issue before starting setup again.`}, []string{`id="setup-start-over-form" hidden`, `id="setup-review-link" href="/setup/review" hidden`}},
	} {
		t.Run(tc.state, func(t *testing.T) {
			client := setupExecutionClient()
			client.operation = &api.PersistentOperation{SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup", State: tc.state, StartedAt: "2026-09-20T12:00:00Z", FinishedAt: "2026-09-20T12:01:00Z"}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/progress/"+setupExecutionOperationID, ""))
			if page.Code != http.StatusOK {
				t.Fatalf("progress status %d: %s", page.Code, page.Body.String())
			}
			body := page.Body.String()
			for _, want := range tc.visible {
				if !strings.Contains(body, want) {
					t.Fatalf("missing %q", want)
				}
			}
			for _, want := range tc.hidden {
				if !strings.Contains(body, want) {
					t.Fatalf("missing hidden state %q", want)
				}
			}
		})
	}
}

func TestSetupRecoveryActionOnlyAtStorageRollback(t *testing.T) {
	for _, tc := range []struct {
		state, stage string
		available    bool
	}{
		{"needs_attention", "storage_rollback", true},
		{"needs_attention", "runtime_rollback", false},
		{"rolled_back", "setup_rolled_back", false},
	} {
		t.Run(tc.state+"_"+tc.stage, func(t *testing.T) {
			client := setupExecutionClient()
			client.operation = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: tc.state, Stage: tc.stage}
			client.current = client.operation
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/progress/"+setupExecutionOperationID, ""))
			if page.Code != http.StatusOK {
				t.Fatalf("page status = %d", page.Code)
			}
			form := `id="setup-recover-form"`
			if !tc.available {
				form += " hidden"
			}
			if !strings.Contains(page.Body.String(), form) {
				t.Fatalf("recovery action visibility incorrect: %s", page.Body.String())
			}
			post := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recover", "csrf=csrf-token&operation_id="+setupExecutionOperationID))
			if tc.available {
				if post.Code != http.StatusSeeOther || post.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID || client.recoverCalls != 1 || client.recoverID != setupExecutionOperationID {
					t.Fatalf("recovery POST = %d, calls = %d", post.Code, client.recoverCalls)
				}
			} else if post.Code != http.StatusConflict || client.recoverCalls != 0 {
				t.Fatalf("unavailable recovery POST = %d, calls = %d", post.Code, client.recoverCalls)
			}
		})
	}
}

func TestSetupElapsedTimerSource(t *testing.T) {
	js, err := assets.ReadFile("static/setup-operation.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, want := range []string{`window.setInterval(renderElapsed, 1000)`, `window.clearInterval(elapsedInterval)`, `minutes === 0 ?`, `remainder === 0 ?`, `padStart(2, "0")`, `operation.started_at`, `operation.finished_at`} {
		if !strings.Contains(source, want) {
			t.Fatalf("elapsed timer missing %q", want)
		}
	}
}

func TestRolledBackSetupCanStartFreshDraft(t *testing.T) {
	client := setupExecutionClient()
	client.current = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: "rolled_back"}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	start := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/start", "csrf=csrf-token"))
	if start.Code != http.StatusSeeOther || start.Header().Get("Location") != "/setup" {
		t.Fatalf("start over returned %d %q", start.Code, start.Header().Get("Location"))
	}
	wizard := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if wizard.Code != http.StatusOK || !strings.Contains(wizard.Body.String(), "Step 1 of 7") {
		t.Fatalf("fresh draft unavailable: %d", wizard.Code)
	}
	if client.applyCalls != 0 {
		t.Fatal("starting a fresh draft launched another setup operation")
	}
}

func TestNeedsAttentionSetupCannotStartFreshDraft(t *testing.T) {
	client := setupExecutionClient()
	client.current = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: "needs_attention"}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	start := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/start", "csrf=csrf-token"))
	if start.Code != http.StatusSeeOther || start.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID {
		t.Fatalf("recovery lock was bypassed: %d %q", start.Code, start.Header().Get("Location"))
	}
	if client.applyCalls != 0 {
		t.Fatal("recovery lock launched another setup operation")
	}
}

func TestSetupProgressRefreshDoesNotStartAnotherApply(t *testing.T) {
	client := setupExecutionClient()
	client.operation = &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup",
		PlanFingerprint: setupReviewFingerprint, State: "running", Stage: "runtime_config",
		Status:    "Writing transactional Minecraft runtime configuration.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:01:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/progress/"+setupExecutionOperationID, ""))
		if page.Code != http.StatusOK {
			t.Fatalf("progress refresh returned %d: %s", page.Code, page.Body.String())
		}
		status := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/setup/progress/"+setupExecutionOperationID, ""))
		if status.Code != http.StatusOK {
			t.Fatalf("progress status refresh returned %d: %s", status.Code, status.Body.String())
		}
	}
	if client.applyCalls != 0 {
		t.Fatalf("refreshing persistent setup progress started Apply %d times", client.applyCalls)
	}
}

func TestSetupReviewResumesCurrentPersistentOperation(t *testing.T) {
	client := setupExecutionClient()
	client.current = &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup",
		PlanFingerprint: setupReviewFingerprint, State: "verifying", Stage: "minecraft_verify",
		Status:    "Starting Minecraft and verifying runtime readiness.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:02:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID {
		t.Fatalf("active setup Review did not reconnect to persistent operation: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if client.applyCalls != 0 {
		t.Fatalf("reconnecting from Review started Apply %d times", client.applyCalls)
	}
}

func TestSetupEntryResumesCurrentPersistentOperation(t *testing.T) {
	client := setupExecutionClient()
	client.current = &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup",
		PlanFingerprint: setupReviewFingerprint, State: "verifying", Stage: "minecraft_verify",
		Status:    "Starting Minecraft and verifying runtime readiness.",
		StartedAt: "2026-09-20T12:00:00Z", UpdatedAt: "2026-09-20T12:02:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID {
		t.Fatalf("active setup was not resumed: %d %q", rr.Code, rr.Header().Get("Location"))
	}
}
