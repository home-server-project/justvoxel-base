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
	current       *api.PersistentOperation
	operation     *api.PersistentOperation
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
		`<button type="submit" disabled>Configure JustVoxel</button>`,
		"Progress and rollback status will remain available",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pre-EULA review missing %q: %s", want, body)
		}
	}

	acceptSetupEULA(t, app)
	page = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	body = page.Body.String()
	if strings.Contains(body, `<button type="submit" disabled>Configure JustVoxel</button>`) {
		t.Fatalf("Configure JustVoxel remained disabled after EULA acceptance: %s", body)
	}
	if !strings.Contains(body, ">Configure JustVoxel</button>") {
		t.Fatalf("enabled Configure JustVoxel button missing: %s", body)
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

func TestSetupProgressPageAndJSONTrackSamePersistentOperation(t *testing.T) {
	client := setupExecutionClient()
	client.operation = &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup",
		PlanFingerprint: setupReviewFingerprint, State: "running", Stage: "runtime_config",
		Status: "Writing transactional Minecraft runtime configuration.",
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
		"Refreshing or reopening this operation URL does not start setup again.",
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

func TestSetupEntryResumesCurrentPersistentOperation(t *testing.T) {
	client := setupExecutionClient()
	client.current = &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: setupExecutionOperationID, OperationType: "setup",
		PlanFingerprint: setupReviewFingerprint, State: "verifying", Stage: "minecraft_verify",
		Status: "Starting Minecraft and verifying runtime readiness.",
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
