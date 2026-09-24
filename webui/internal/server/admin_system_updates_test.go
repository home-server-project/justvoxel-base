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
	role          string
	status        api.AdminSystemUpdateStatus
	statusCalls   int
	checkCalls    int
	updateCalls   int
	rebootStatus  api.AdminSystemUpdateRebootStatus
	rebootCalls   int
	rebootOptions api.AdminSystemUpdateRebootOptions
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

func (f *fakeSystemUpdatesAPI) AdminSystemUpdateCheck(_ context.Context, session string) (api.AdminSystemUpdateStatus, error) {
	if session != "session-token" {
		return api.AdminSystemUpdateStatus{}, api.ErrUnauthorized
	}
	f.checkCalls++
	return f.status, nil
}

func (f *fakeSystemUpdatesAPI) AdminSystemUpdate(_ context.Context, session string) (api.AdminSystemUpdateStatus, error) {
	if session != "session-token" {
		return api.AdminSystemUpdateStatus{}, api.ErrUnauthorized
	}
	f.updateCalls++
	return f.status, nil
}

func (f *fakeSystemUpdatesAPI) AdminSystemUpdateRebootStatus(_ context.Context, session string) (api.AdminSystemUpdateRebootStatus, error) {
	if session != "session-token" {
		return api.AdminSystemUpdateRebootStatus{}, api.ErrUnauthorized
	}
	return f.rebootStatus, nil
}

func (f *fakeSystemUpdatesAPI) AdminSystemUpdateReboot(_ context.Context, session string, options api.AdminSystemUpdateRebootOptions) (api.AdminSystemUpdateRebootStatus, error) {
	if session != "session-token" {
		return api.AdminSystemUpdateRebootStatus{}, api.ErrUnauthorized
	}
	f.rebootCalls++
	f.rebootOptions = options
	return f.rebootStatus, nil
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

func TestSystemUpdateCheckIsAdministratorOnlyAndForwardsRequest(t *testing.T) {
	client := &fakeSystemUpdatesAPI{status: systemUpdateFixture()}
	client.status.Checked = true
	client.status.CheckState = "update_available"
	client.status.Available = &api.AdminSystemUpdateDeployment{
		Image:   "ghcr.io/home-server-project/justvoxel-vm:testing",
		Version: "12",
		Digest:  "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	req := authenticatedSystemUpdateRequest(http.MethodPost, "http://example/api/system-updates/check", "csrf=token")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("system update check returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.checkCalls != 1 {
		t.Fatalf("system update check calls = %d, want 1", client.checkCalls)
	}
	if !strings.Contains(rr.Body.String(), `"check_state":"update_available"`) {
		t.Fatalf("unexpected update check response: %s", rr.Body.String())
	}

	client.role = "operator"
	denied := authenticatedSystemUpdateRequest(http.MethodPost, "http://example/api/system-updates/check", "csrf=token")
	deniedRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(deniedRR, denied)
	if deniedRR.Code != http.StatusForbidden {
		t.Fatalf("operator system update check = %d, want 403", deniedRR.Code)
	}
	if client.checkCalls != 1 {
		t.Fatalf("Agent check called for non-administrator; calls=%d", client.checkCalls)
	}
}

func TestSystemUpdateCheckRequiresCSRFBeforeAgentCall(t *testing.T) {
	client := &fakeSystemUpdatesAPI{status: systemUpdateFixture()}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/api/system-updates/check", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing update-check CSRF returned %d, want 403", rr.Code)
	}
	if client.checkCalls != 0 {
		t.Fatalf("system update check reached Agent without CSRF; calls=%d", client.checkCalls)
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

func TestSystemUpdateRebootForwardsBackupQuickAndPlayerConfirmation(t *testing.T) {
	client := &fakeSystemUpdatesAPI{
		status: systemUpdateFixture(),
		rebootStatus: api.AdminSystemUpdateRebootStatus{
			OK: true, State: "queued", Accepted: true, WarningSeconds: 10, BackupMinecraft: true,
		},
	}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := authenticatedSystemUpdateRequest(
		http.MethodPost,
		"http://example/api/system-updates/reboot",
		"csrf=token&backup_minecraft=yes&quick_reboot=yes&confirm_players=yes",
	)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("update reboot returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.rebootCalls != 1 {
		t.Fatalf("update reboot calls = %d, want 1", client.rebootCalls)
	}
	if !client.rebootOptions.BackupMinecraft || !client.rebootOptions.ConfirmPlayers || client.rebootOptions.WarningSeconds != 10 {
		t.Fatalf("unexpected reboot options: %#v", client.rebootOptions)
	}
}

func TestSystemUpdateRebootRequiresCSRF(t *testing.T) {
	client := &fakeSystemUpdatesAPI{status: systemUpdateFixture()}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/api/system-updates/reboot", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing reboot CSRF returned %d, want 403", rr.Code)
	}
	if client.rebootCalls != 0 {
		t.Fatalf("update reboot reached Agent without CSRF; calls=%d", client.rebootCalls)
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
