package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeDateTimeVersionAPI struct {
	*fakeAPI
	role         string
	dateWrites   int
	versionReads int
}

func (f *fakeDateTimeVersionAPI) Session(ctx context.Context, session string) (api.SessionInfo, error) {
	identity, err := f.fakeAPI.Session(ctx, session)
	identity.Role = f.role
	return identity, err
}

func (f *fakeDateTimeVersionAPI) AdminDateTime(_ context.Context, _ string) (api.AdminDateTimeState, error) {
	return api.AdminDateTimeState{Timezone: "UTC", LocalDate: "2026-09-27", LocalTime: "12:34", Automatic: true}, nil
}

func (f *fakeDateTimeVersionAPI) AdminDateTimeApply(_ context.Context, _ string, change api.AdminDateTimeChange) (api.AdminDateTimeState, error) {
	f.dateWrites++
	return api.AdminDateTimeState{Timezone: change.Timezone, Automatic: change.Automatic}, nil
}

func (f *fakeDateTimeVersionAPI) AdminVersionStatus(_ context.Context, _, policy, version string) (api.AdminVersionStatus, error) {
	f.versionReads++
	return api.AdminVersionStatus{ServerSoftware: "Paper", Installed: "26.2", Available: "26.3", Recommended: "26.2", SelectedCandidate: "26.2", Policy: "recommended", UpdateAvailable: true, PaperSupported: true}, nil
}

func TestDateTimeAndVersionWorkspaceRequireAdministrator(t *testing.T) {
	client := &fakeDateTimeVersionAPI{fakeAPI: &fakeAPI{}, role: "viewer"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/system/workspace/date-time", "/api/version/workspace/status"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example"+path, ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s: %d", path, rr.Code)
		}
	}
	if client.versionReads != 0 {
		t.Fatal("viewer reached version resolver")
	}
	client.role = "administrator"
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/system/workspace/date-time", "csrf=wrong&timezone=UTC&automatic=on"))
	if rr.Code != http.StatusForbidden || client.dateWrites != 0 {
		t.Fatalf("CSRF: %d, writes=%d", rr.Code, client.dateWrites)
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/api/system/workspace/date-time", "csrf=csrf-token&timezone=UTC&automatic=on"))
	if rr.Code != http.StatusOK || client.dateWrites != 1 {
		t.Fatalf("date change: %d, writes=%d", rr.Code, client.dateWrites)
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/version/workspace/status", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("version: %d %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"available":"26.3"`, `"recommended":"26.2"`, `"selected_candidate":"26.2"`, `"policy":"recommended"`, `"update_available":true`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("version missing %s: %s", want, rr.Body.String())
		}
	}
}
