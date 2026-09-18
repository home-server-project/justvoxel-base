package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeAdminAPI struct {
	fakeAPI
	users       []api.AdminUser
	createdUser string
	createdRole string
}

func (f *fakeAdminAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	return api.SessionInfo{Username: "voxel", Role: "administrator", AuthSource: "system"}, nil
}

func (f *fakeAdminAPI) AdminUsers(_ context.Context, session string) (api.AdminUsersResponse, error) {
	if session != "session-token" {
		return api.AdminUsersResponse{}, api.ErrUnauthorized
	}
	var out api.AdminUsersResponse
	out.PrimaryAdministrator.Username = "voxel"
	out.PrimaryAdministrator.Role = "administrator"
	out.Users = append([]api.AdminUser(nil), f.users...)
	out.MinimumPasswordLen = 8
	return out, nil
}

func (f *fakeAdminAPI) AdminCreateUser(_ context.Context, session, username, role, _ string) (api.AdminUser, error) {
	if session != "session-token" {
		return api.AdminUser{}, api.ErrUnauthorized
	}
	f.createdUser = username
	f.createdRole = role
	user := api.AdminUser{ID: 7, Username: username, Role: role, Enabled: true, RestartLimit: 2, BackupLimit: 2}
	f.users = append(f.users, user)
	return user, nil
}

func (f *fakeAdminAPI) AdminSetUserRole(_ context.Context, _ string, _ int64, _ string) (api.AdminUser, error) {
	return api.AdminUser{}, nil
}
func (f *fakeAdminAPI) AdminSetUserEnabled(_ context.Context, _ string, _ int64, _ bool) (api.AdminUser, error) {
	return api.AdminUser{}, nil
}
func (f *fakeAdminAPI) AdminSetUserPassword(_ context.Context, _ string, _ int64, _ string) error {
	return nil
}
func (f *fakeAdminAPI) AdminDeleteUser(_ context.Context, _ string, _ int64) error { return nil }
func (f *fakeAdminAPI) AdminResetRestartAllowance(_ context.Context, _ string, _ int64) (api.AdminUser, error) {
	return api.AdminUser{}, nil
}
func (f *fakeAdminAPI) AdminResetBackupAllowance(_ context.Context, _ string, _ int64) (api.AdminUser, error) {
	return api.AdminUser{}, nil
}

func authenticatedAdminRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func TestAdminUsersPageShowsPrimaryAdminAndWebUsers(t *testing.T) {
	client := &fakeAdminAPI{users: []api.AdminUser{{ID: 1, Username: "Ilya", Role: "operator", Enabled: true, RestartUsed: 1, RestartLimit: 2, BackupUsed: 0, BackupLimit: 2}}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/users", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("users page returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Primary administrator", "voxel", "Ilya", "1 / 2", "Create user"} {
		if !strings.Contains(body, want) {
			t.Fatalf("users page missing %q", want)
		}
	}
}

func TestAdminUsersCreateForwardsOperatorIdentity(t *testing.T) {
	client := &fakeAdminAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&username=Alex&role=operator&password=StrongPass123%21&confirm_password=StrongPass123%21"
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/settings/users/create", body))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.createdUser != "Alex" || client.createdRole != "operator" {
		t.Fatalf("created user = %q role=%q", client.createdUser, client.createdRole)
	}
	if rr.Header().Get("Location") != "/settings/users?result=created" {
		t.Fatalf("unexpected redirect %q", rr.Header().Get("Location"))
	}
}

func TestAdminUsersCreateRejectsPasswordMismatchBeforeAPI(t *testing.T) {
	client := &fakeAdminAPI{}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&username=Alex&role=viewer&password=one&confirm_password=two"
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/settings/users/create", body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("mismatch returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.createdUser != "" {
		t.Fatalf("API was called for mismatched passwords: %q", client.createdUser)
	}
	if !strings.Contains(rr.Body.String(), "Passwords must match") {
		t.Fatalf("missing password mismatch message: %s", rr.Body.String())
	}
}
