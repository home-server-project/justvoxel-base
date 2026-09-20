package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfiguredApplianceCannotReuseFirstRunReviewState(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")

	advanceSetupToReview(t, app)
	validated := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if validated.Code != http.StatusOK {
		t.Fatalf("initial review returned %d: %s", validated.Code, validated.Body.String())
	}
	if _, ok := firstRunSetupReviews.get(app, "session-token"); !ok {
		t.Fatal("validated review state was not cached")
	}

	client.configuration.Configured = true
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("configured appliance review returned %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if _, ok := firstRunSetupDrafts.get(app, "session-token"); ok {
		t.Fatal("configured appliance retained first-run draft state")
	}
	if _, ok := firstRunSetupReviews.get(app, "session-token"); ok {
		t.Fatal("configured appliance retained reviewed plan/EULA state")
	}
	if client.planHit != 1 {
		t.Fatalf("configured appliance unexpectedly replanned setup: calls=%d", client.planHit)
	}
}

func TestSetupReviewMutationsRequireCSRF(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")

	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))

	for _, path := range []string{"/setup/review/eula", "/setup/review/apply", "/setup/review/back", "/setup/review/cancel"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example"+path, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s without CSRF status = %d, want 403", path, rr.Code)
		}
	}

	if _, ok := firstRunSetupDrafts.get(app, "session-token"); !ok {
		t.Fatal("CSRF rejection unexpectedly discarded setup draft")
	}
	if state, ok := firstRunSetupReviews.get(app, "session-token"); !ok || state.EULAAccepted {
		t.Fatalf("CSRF rejection changed reviewed plan state: %#v", state)
	}
}

func TestSetupReviewExecutionSurfaceKeepsSMBSecretTransientAndManagementAPIPrivate(t *testing.T) {
	client := setupExecutionClient()
	client.plan.Requirements.SMBPasswordRequired = true
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")

	advanceSetupToReview(t, app)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("review returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`action="/setup/review/apply"`,
		`type="password" name="smb_password"`,
		"SMB password required during execution",
		"It is not stored in the setup draft or operation journal.",
		`<button type="submit" disabled>Configure JustVoxel</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("review execution surface missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{`name="password"`, `name="backup_password"`, `action="/setup/apply"`, `action="/v1/admin/setup/apply"`, "family-secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("review exposed forbidden management/secret surface %q: %s", forbidden, body)
		}
	}

	legacy := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/apply", "csrf=csrf-token"))
	if legacy.Code < 400 {
		t.Fatalf("legacy WebUI setup Apply path unexpectedly executable: status=%d body=%s", legacy.Code, legacy.Body.String())
	}
}
