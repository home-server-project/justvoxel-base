package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeValidationAPI struct {
	fakeAPI
	role            string
	result          api.AdminValidationResponse
	validationErr   error
	validationCalls int
}

func (f *fakeValidationAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeValidationAPI) AdminValidation(_ context.Context, session string) (api.AdminValidationResponse, error) {
	if session != "session-token" {
		return api.AdminValidationResponse{}, api.ErrUnauthorized
	}
	f.validationCalls++
	if f.validationErr != nil {
		return api.AdminValidationResponse{}, f.validationErr
	}
	return f.result, nil
}

func TestAdminValidationPageShowsPassedResult(t *testing.T) {
	client := &fakeValidationAPI{result: api.AdminValidationResponse{
		OK: true, ExitCode: 0, Output: "OK: runtime files are healthy\n\nJustVoxel validation passed.\n",
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/validation", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("validation page returned %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"System validation", "Validation passed", "OK: runtime files are healthy", "JustVoxel validation passed.", "Backend result code: 0"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("validation page missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminValidationPageShowsDetectedProblemsWithoutReinterpretingOutput(t *testing.T) {
	client := &fakeValidationAPI{result: api.AdminValidationResponse{
		OK: false, ExitCode: 1, Output: "FAIL: minecraft.service is not active\n\nJustVoxel validation failed with 1 problem(s).\n",
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/validation", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("validation problems page returned %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Validation completed with detected problems", "FAIL: minecraft.service is not active", "JustVoxel validation failed with 1 problem(s).", "Backend result code: 1"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("validation problems page missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminValidationPageKeepsUnavailableSeparateFromValidationFailure(t *testing.T) {
	client := &fakeValidationAPI{validationErr: &api.ResponseError{StatusCode: http.StatusServiceUnavailable, Message: "JustVoxel validation is unavailable"}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/validation", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("validation unavailable page returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Validation service unavailable", "No validation result was produced."} {
		if !strings.Contains(body, want) {
			t.Fatalf("validation unavailable page missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"Validation passed", "Validation completed with detected problems"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("validation unavailable page incorrectly reported %q: %s", forbidden, body)
		}
	}
}

func TestAdminValidationPageRejectsOperatorBeforeValidation(t *testing.T) {
	client := &fakeValidationAPI{role: "operator"}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/validation", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator validation page status = %d, want 403", rr.Code)
	}
	if client.validationCalls != 0 {
		t.Fatalf("validation API ran for operator %d time(s)", client.validationCalls)
	}
}
