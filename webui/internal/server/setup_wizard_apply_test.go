package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const setupExecutionOperationID = "01234567-89ab-4cde-8f01-23456789abcd"

type fakeSetupExecutionAPI struct {
	*fakeSetupPlanningAPI
	applyResponse api.AdminSetupApplyResponse
	applyErr      error
	applyRequest  api.AdminSetupApplyRequest
	applyCalls    int
	applyCurrent  *api.PersistentOperation
	currentErr    error
	currentCalls  int
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
	if f.applyCurrent != nil {
		f.current = f.applyCurrent
	}
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
	f.currentCalls++
	if f.currentErr != nil {
		return api.PersistentOperationResponse{}, f.currentErr
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
	assertSetupApplicationLogo(t, page.Body.String())
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
		{"queued", []string{`id="setup-diagnostic-log-view" type="button">Live Console</button>`}, []string{`id="setup-dashboard-link" href="/" hidden`, `id="setup-diagnostic-log-link" href="/setup/progress/` + setupExecutionOperationID + `/log" hidden`}},
		{"validating", []string{`id="setup-diagnostic-log-view" type="button">Live Console</button>`}, []string{`id="setup-dashboard-link" href="/" hidden`, `id="setup-diagnostic-log-link" href="/setup/progress/` + setupExecutionOperationID + `/log" hidden`}},
		{"running", []string{`id="setup-diagnostic-log-view" type="button">Live Console</button>`}, []string{`id="setup-dashboard-link" href="/" hidden`, `id="setup-diagnostic-log-link" href="/setup/progress/` + setupExecutionOperationID + `/log" hidden`}},
		{"verifying", []string{`id="setup-diagnostic-log-view" type="button">Live Console</button>`}, []string{`id="setup-dashboard-link" href="/" hidden`, `id="setup-diagnostic-log-link" href="/setup/progress/` + setupExecutionOperationID + `/log" hidden`}},
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

func TestSetupRecoveryActionAtRuntimeAndStorageRollback(t *testing.T) {
	for _, tc := range []struct {
		state, stage string
		available    bool
	}{
		{"needs_attention", "storage_rollback", true},
		{"needs_attention", "runtime_rollback", true},
		{"rolled_back", "setup_rolled_back", false},
		{"needs_attention", "interrupted", false},
		{"running", "runtime_rollback", false},
		{"rolling_back", "storage_rollback", false},
		{"succeeded", "completed", false},
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
			} else {
				form += " >"
			}
			if !strings.Contains(page.Body.String(), form) {
				t.Fatalf("recovery action visibility incorrect: %s", page.Body.String())
			}
			body := page.Body.String()
			for _, forbidden := range []string{`name="recovery_confirmed"`, `type="checkbox"`, "I understand recovery", "confirm removal of data"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("recovery retains acknowledgement gate %q", forbidden)
				}
			}
			if !strings.Contains(body, `<button class="setup-recovery-action" type="submit">Retry recovery</button>`) {
				t.Fatal("recovery must use an immediately enabled submit button")
			}
			startForm := `id="setup-start-over-form" `
			if tc.state != "rolled_back" {
				startForm += "hidden"
			} else {
				startForm += ">"
			}
			if !strings.Contains(body, startForm) || !strings.Contains(body, `<button type="button" disabled aria-describedby="setup-start-over-help">Start setup over</button>`) {
				t.Fatal("start over must remain disabled until rolled_back")
			}
			disabledStart := `aria-describedby="setup-start-over-help" `
			if tc.state == "needs_attention" {
				disabledStart += ">"
			} else {
				disabledStart += "hidden"
			}
			if !strings.Contains(body, disabledStart) {
				t.Fatal("disabled start-over control visibility is incorrect")
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
	for _, mode := range []string{"advanced", "recommended"} {
		t.Run(mode, func(t *testing.T) {
			client := setupExecutionClient()
			client.current = &api.PersistentOperation{InstanceSetup: true, OperationID: setupExecutionOperationID, OperationType: "setup", State: "rolled_back"}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			path, form, location := "/setup/start", "csrf=csrf-token", "/setup"
			if mode == "recommended" {
				path, form, location = "/setup/recommended", "csrf=csrf-token&server_type=paper", "/setup/review"
			}
			start := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example"+path, form))
			if start.Code != http.StatusSeeOther || start.Header().Get("Location") != location {
				t.Fatalf("start over returned %d %q", start.Code, start.Header().Get("Location"))
			}
			draft, exists := firstRunSetupDrafts.get(app, "session-token")
			if !exists || !draft.Started || draft.Mode != mode {
				t.Fatalf("fresh %s draft unavailable: %#v", mode, draft)
			}
			wizard := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example"+location, ""))
			if wizard.Code != http.StatusOK {
				t.Fatalf("fresh setup unavailable: %d", wizard.Code)
			}
			if mode == "advanced" && !strings.Contains(wizard.Body.String(), "<h2>Server</h2>") {
				t.Fatal("fresh advanced draft did not open the Server step")
			}
			if client.applyCalls != 0 {
				t.Fatal("starting a fresh draft launched another setup operation")
			}
		})
	}
}

func TestNeedsAttentionSetupCannotStartFreshDraft(t *testing.T) {
	for _, entry := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/setup"},
		{http.MethodPost, "/setup/start"},
		{http.MethodPost, "/setup/recommended"},
		{http.MethodGet, "/setup/review"},
	} {
		t.Run(entry.path, func(t *testing.T) {
			client := &fakeSetupEntryDiagnosticAPI{fakeSetupExecutionAPI: setupExecutionClient()}
			client.current = &api.PersistentOperation{
				InstanceSetup: true, OperationID: setupExecutionOperationID, OperationType: "setup",
				State: "needs_attention", Stage: "runtime_rollback",
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			firstRunSetupReviews.save(app, "session-token", setupReviewState{EULAAccepted: true, DraftUpdatedAt: time.Now()})
			response := httptestResponse(app, authenticatedAdminRequest(entry.method, "http://example"+entry.path, "csrf=csrf-token&server_type=paper"))
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID {
				t.Fatalf("recovery lock was bypassed: %d %q", response.Code, response.Header().Get("Location"))
			}
			if client.currentCalls != 1 || client.diagnosticCalls != 0 || client.defaultsHit != 0 || client.storageHit != 0 || client.planHit != 0 || client.applyCalls != 0 {
				t.Fatalf("entry bypassed recovery discovery: %#v", client)
			}
			if _, exists := firstRunSetupDrafts.get(app, "session-token"); exists {
				t.Fatal("recovery lock created a fresh setup draft")
			}
			if review, exists := firstRunSetupReviews.get(app, "session-token"); !exists || !review.EULAAccepted {
				t.Fatal("recovery lock discarded the existing review")
			}
		})
	}
}

type fakeSetupEntryDiagnosticAPI struct {
	*fakeSetupExecutionAPI
	diagnosticCalls int
}

func (f *fakeSetupEntryDiagnosticAPI) AdminSetupDiagnosticSession(context.Context, string, string) (api.AdminSetupDiagnosticSessionResponse, error) {
	f.diagnosticCalls++
	return api.AdminSetupDiagnosticSessionResponse{}, nil
}

func (f *fakeSetupEntryDiagnosticAPI) AdminSetupDiagnosticEvent(context.Context, string, string, string, map[string]string) error {
	f.diagnosticCalls++
	return nil
}

func TestSetupEntryCurrentOperationErrorsFailClosed(t *testing.T) {
	for _, failure := range []struct {
		name     string
		err      error
		status   int
		location string
	}{
		{"lookup", errors.New("management API unavailable"), http.StatusBadGateway, ""},
		{"decode", errors.New("management API returned invalid operation response: json: unknown field"), http.StatusBadGateway, ""},
		{"API", &api.ResponseError{StatusCode: http.StatusInternalServerError, Message: "unavailable"}, http.StatusBadGateway, ""},
		{"unauthorized", api.ErrUnauthorized, http.StatusSeeOther, "/login"},
		{"password change", api.ErrPasswordChangeRequired, http.StatusSeeOther, "/password"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			for _, path := range []string{"/setup", "/setup/start", "/setup/recommended", "/setup/review"} {
				t.Run(path, func(t *testing.T) {
					client := &fakeSetupEntryDiagnosticAPI{fakeSetupExecutionAPI: setupExecutionClient()}
					client.currentErr = failure.err
					app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
					if err != nil {
						t.Fatal(err)
					}
					defer firstRunSetupDrafts.delete(app, "session-token")
					defer firstRunSetupReviews.delete(app, "session-token")
					method := http.MethodGet
					if path == "/setup/start" || path == "/setup/recommended" {
						method = http.MethodPost
					}
					response := httptestResponse(app, authenticatedAdminRequest(method, "http://example"+path, "csrf=csrf-token&server_type=paper"))
					if response.Code != failure.status || response.Header().Get("Location") != failure.location {
						t.Fatalf("current operation error returned %d %q: %s", response.Code, response.Header().Get("Location"), response.Body.String())
					}
					if failure.status == http.StatusBadGateway && strings.TrimSpace(response.Body.String()) != "setup operation status is unavailable" {
						t.Fatalf("expected existing Setup progress error, got %q", response.Body.String())
					}
					if client.currentCalls != 1 || client.diagnosticCalls != 0 || client.defaultsHit != 0 || client.storageHit != 0 || client.planHit != 0 || client.applyCalls != 0 {
						t.Fatalf("current operation error allowed setup entry: %#v", client)
					}
					if _, exists := firstRunSetupDrafts.get(app, "session-token"); exists {
						t.Fatal("current operation error created a fresh setup draft")
					}
				})
			}
		})
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

func TestSetupRecoveryNeedsNoAcknowledgement(t *testing.T) {
	client := setupExecutionClient()
	client.operation = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: "needs_attention", Stage: "runtime_rollback"}
	client.current = client.operation
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recover", "csrf=csrf-token&operation_id="+setupExecutionOperationID))
	if response.Code != http.StatusSeeOther || client.recoverCalls != 1 {
		t.Fatalf("recovery without acknowledgement: %d calls=%d", response.Code, client.recoverCalls)
	}
}

func TestRuntimeRecoveryCompletionOffersFreshSetup(t *testing.T) {
	client := setupExecutionClient()
	client.operation = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: "needs_attention", Stage: "runtime_rollback"}
	client.current = client.operation
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	// The WebUI consumes operation state, so legacy and durable manifests use
	// the same recovery action. Incomplete runtime verification stays retryable.
	client.operation.Status = "Runtime recovery failed: The Minecraft container remains or its absence could not be verified."
	before := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/progress/"+setupExecutionOperationID, ""))
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), `id="setup-recover-form" >`) || !strings.Contains(before.Body.String(), `id="setup-start-over-form" hidden`) || !strings.Contains(before.Body.String(), "The Minecraft container remains") {
		t.Fatalf("unsafe legacy recovery actions: %d %s", before.Code, before.Body.String())
	}
	blockedStart := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/start", "csrf=csrf-token"))
	if blockedStart.Code != http.StatusSeeOther || blockedStart.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID || client.applyCalls != 0 {
		t.Fatalf("start over bypassed recovery: %d %q apply=%d", blockedStart.Code, blockedStart.Header().Get("Location"), client.applyCalls)
	}
	retry := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recover", "csrf=csrf-token&operation_id="+setupExecutionOperationID))
	if retry.Code != http.StatusSeeOther || client.recoverCalls != 1 {
		t.Fatalf("retry: %d calls=%d", retry.Code, client.recoverCalls)
	}
	// Simulate the authoritative completed operation returned by subsequent polling.
	client.operation.State = "rolled_back"
	client.operation.Stage = "setup_rolled_back"
	client.current = nil
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/progress/"+setupExecutionOperationID, ""))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `id="setup-start-over-form" >`) || !strings.Contains(page.Body.String(), "Start setup over") {
		t.Fatalf("restart action unavailable: %d %s", page.Code, page.Body.String())
	}
	start := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/start", "csrf=csrf-token"))
	if start.Code != http.StatusSeeOther || start.Header().Get("Location") != "/setup" {
		t.Fatalf("start over: %d %q", start.Code, start.Header().Get("Location"))
	}
	wizard := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if wizard.Code != http.StatusOK || !strings.Contains(wizard.Body.String(), "<h2>Server</h2>") || client.applyCalls != 0 {
		t.Fatalf("fresh setup unavailable: %d apply=%d", wizard.Code, client.applyCalls)
	}
}

func TestSetupProgressActionsUseNormalButtonLayout(t *testing.T) {
	markup := correctionSource(t, "templates/setup_progress.html")
	for _, want := range []string{
		`class="action-row setup-operation-actions"`,
		`class="button-link secondary" id="setup-diagnostic-log-view"`,
		`class="button-link setup-log-download" id="setup-diagnostic-log-link"`,
		`class="button-link" id="setup-dashboard-link"`,
		`<button class="setup-recovery-action" type="submit">Retry recovery</button>`,
		`<button type="submit">Start setup over</button>`,
		`<button type="button" disabled aria-describedby="setup-start-over-help">Start setup over</button>`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("normal setup action structure missing %q", want)
		}
	}
	css := correctionSource(t, "static/setup-review.css")
	if !strings.Contains(css, `.setup-operation-actions{display:flex;align-items:center;justify-content:flex-end;gap:.65rem;flex-wrap:wrap}`) {
		t.Fatal("setup actions must wrap and keep their natural height on desktop and mobile")
	}
}

func TestSetupApplyErrorReconcilesOnlyExactReviewedOperation(t *testing.T) {
	for _, mode := range []string{"matching", "different", "none", "unauthorized", "password-change", "lookup-unauthorized", "lookup-password-change", "lookup-error", "invalid-id"} {
		t.Run(mode, func(t *testing.T) {
			client := setupExecutionClient()
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			advanceSetupToReview(t, app)
			acceptSetupEULA(t, app)
			client.applyErr = errors.New("response connection lost")
			client.applyCurrent = &api.PersistentOperation{OperationID: setupExecutionOperationID, PlanFingerprint: setupReviewFingerprint, State: "running"}
			switch mode {
			case "different":
				client.applyCurrent.PlanFingerprint = strings.Repeat("a", 64)
			case "lookup-error":
				client.currentErr = errors.New("status unavailable")
			case "invalid-id":
				client.applyCurrent.OperationID = "invalid"
			case "none":
				client.applyCurrent = nil
			case "unauthorized":
				client.applyErr = api.ErrUnauthorized
			case "password-change":
				client.applyErr = api.ErrPasswordChangeRequired
			case "lookup-unauthorized":
				client.currentErr = api.ErrUnauthorized
			case "lookup-password-change":
				client.currentErr = api.ErrPasswordChangeRequired
			}
			before := client.currentCalls
			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/apply", "csrf=csrf-token&plan_fingerprint="+setupReviewFingerprint))
			if client.applyCalls != 1 {
				t.Fatal("Apply was retried", client.applyCalls)
			}
			switch mode {
			case "matching":
				if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID {
					t.Fatal(rr.Code, rr.Header(), rr.Body.String())
				}
			case "different", "none", "lookup-error", "invalid-id":
				if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Could not start JustVoxel setup.") {
					t.Fatal(rr.Code, rr.Body.String())
				}
			default:
				want := "/login"
				if strings.Contains(mode, "password-change") {
					want = "/password"
				}
				if rr.Header().Get("Location") != want || rr.Code != http.StatusSeeOther {
					t.Fatal("authentication handling overridden", rr.Code, rr.Header())
				}
				if (mode == "unauthorized" || mode == "password-change") && client.currentCalls != before {
					t.Fatal("looked up operation after authoritative auth error")
				}
			}
		})
	}
}

func TestSetupRecoveryRetryReconnectsToSameOperation(t *testing.T) {
	for _, stage := range []string{"runtime_recovery", "storage_recovery"} {
		t.Run(stage, func(t *testing.T) {
			client := setupExecutionClient()
			client.operation = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: "needs_attention", Stage: "runtime_rollback"}
			if stage == "storage_recovery" {
				client.operation.Stage = "storage_rollback"
			}
			client.current = client.operation
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			post := func() {
				t.Helper()
				response := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recover", "csrf=csrf-token&operation_id="+setupExecutionOperationID))
				if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/setup/progress/"+setupExecutionOperationID || client.recoverCalls != 1 || client.recoverID != setupExecutionOperationID {
					t.Fatalf("recovery retry: %d location=%q calls=%d id=%q", response.Code, response.Header().Get("Location"), client.recoverCalls, client.recoverID)
				}
			}
			post()
			client.current.State = "rolling_back"
			client.current.Stage = stage
			post()
		})
	}
}

func TestSetupRecoveryRetryRefusesOtherOperationsAndStates(t *testing.T) {
	for _, tc := range []struct {
		state string
		stage string
		id    string
	}{
		{"rolling_back", "runtime_recovery", "11234567-89ab-4cde-8f01-23456789abcd"},
		{"rolling_back", "storage_recovery", "11234567-89ab-4cde-8f01-23456789abcd"},
		{"needs_attention", "runtime_rollback", "11234567-89ab-4cde-8f01-23456789abcd"},
		{"running", "runtime_recovery", setupExecutionOperationID},
		{"rolling_back", "runtime_rollback", setupExecutionOperationID},
		{"rolling_back", "storage_rollback", setupExecutionOperationID},
		{"needs_attention", "runtime_recovery", setupExecutionOperationID},
		{"rolled_back", "storage_recovery", setupExecutionOperationID},
	} {
		t.Run(tc.state+"/"+tc.stage+"/"+tc.id, func(t *testing.T) {
			client := setupExecutionClient()
			client.current = &api.PersistentOperation{OperationID: setupExecutionOperationID, OperationType: "setup", State: tc.state, Stage: tc.stage}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recover", "csrf=csrf-token&operation_id="+tc.id))
			if response.Code != http.StatusConflict || response.Header().Get("Location") != "" || client.recoverCalls != 0 {
				t.Fatalf("invalid recovery accepted: %d calls=%d", response.Code, client.recoverCalls)
			}
		})
	}
}
