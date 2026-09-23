package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeSystemUpdatesAPI struct {
	fakeAPI
	role        string
	status      api.AdminSystemUpdateStatus
	statusCalls int
	updateCalls int
}

func (f *fakeSystemUpdatesAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeSystemUpdatesAPI) AdminSystemUpdateStatus(_ context.Context, session string) (api.AdminSystemUpdateStatus, error) {
	if session != "session-token" {
		return api.AdminSystemUpdateStatus{}, api.ErrUnauthorized
	}
	f.statusCalls++
	return f.status, nil
}

func (f *fakeSystemUpdatesAPI) AdminSystemUpdate(_ context.Context, session string) (api.AdminSystemUpdateStatus, error) {
	if session != "session-token" {
		return api.AdminSystemUpdateStatus{}, api.ErrUnauthorized
	}
	f.updateCalls++
	return f.status, nil
}

func systemUpdateFixture() api.AdminSystemUpdateStatus {
	return api.AdminSystemUpdateStatus{
		OK:             true,
		RebootRequired: true,
		Running: api.AdminSystemUpdateDeployment{
			Image:   "ghcr.io/home-server-project/justvoxel-vm:testing",
			Version: "10",
			Digest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Staged: &api.AdminSystemUpdateDeployment{
			Image:   "ghcr.io/home-server-project/justvoxel-vm:testing",
			Version: "11",
			Digest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		Message: "JustVoxel OS update is staged. Reboot when convenient to use it.",
	}
}

func TestSystemUpdateStatusIsAdministratorOnly(t *testing.T) {
	client := &fakeSystemUpdatesAPI{status: systemUpdateFixture()}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	req := authenticatedSystemUpdateRequest(http.MethodGet, "http://example/api/system-updates", "")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("system update status returned %d: %s", rr.Code, rr.Body.String())
	}
	var status api.AdminSystemUpdateStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.RebootRequired || status.Staged == nil || status.Staged.Version != "11" {
		t.Fatalf("unexpected system update status: %#v", status)
	}

	client.role = "operator"
	denied := authenticatedSystemUpdateRequest(http.MethodGet, "http://example/api/system-updates", "")
	deniedRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(deniedRR, denied)
	if deniedRR.Code != http.StatusForbidden {
		t.Fatalf("operator system update status = %d, want 403", deniedRR.Code)
	}
	if client.statusCalls != 1 {
		t.Fatalf("Agent status called for non-administrator; calls=%d", client.statusCalls)
	}
}

func TestSystemUpdateRequiresCSRFBeforeAgentCall(t *testing.T) {
	client := &fakeSystemUpdatesAPI{status: systemUpdateFixture()}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/api/system-updates", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF returned %d, want 403", rr.Code)
	}
	if client.updateCalls != 0 {
		t.Fatalf("system update reached Agent without CSRF; calls=%d", client.updateCalls)
	}
}

func TestSystemUpdateForwardsApprovedRequest(t *testing.T) {
	client := &fakeSystemUpdatesAPI{status: systemUpdateFixture()}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := authenticatedSystemUpdateRequest(http.MethodPost, "http://example/api/system-updates", "csrf=token")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("system update returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.updateCalls != 1 {
		t.Fatalf("system update calls = %d, want 1", client.updateCalls)
	}
}

func authenticatedSystemUpdateRequest(method, target, form string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(form))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	return req
}
