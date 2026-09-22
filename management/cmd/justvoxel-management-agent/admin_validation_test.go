package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type validationTestExitError struct {
	code int
}

func (e validationTestExitError) Error() string {
	return "validation helper exited"
}

func (e validationTestExitError) ExitCode() int {
	return e.code
}

func TestLocalRootCanValidateWithoutBearerSession(t *testing.T) {
	s := surfaceTestServer(t, roleViewer)
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()

	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		return []byte("OK: runtime files are healthy\n\nJustVoxel validation passed.\n"), nil
	}

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/validation", 0)
	rr := httptest.NewRecorder()
	s.adminValidation(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root validation returned %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"ok":true`, `"exit_code":0`, "OK: runtime files are healthy", "JustVoxel validation passed."} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("local-root validation response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestValidationFailurePreservesOutputAndExitCode(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()

	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		return []byte("FAIL: minecraft.service is not active\n\nJustVoxel validation failed with 1 problem(s).\n"), validationTestExitError{code: 1}
	}

	rr := httptest.NewRecorder()
	s.adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("validation failure returned HTTP %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"ok":false`, `"exit_code":1`, "FAIL: minecraft.service is not active", "JustVoxel validation failed with 1 problem(s)."} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("validation failure response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestValidationRejectsNonAdministratorBeforeHelper(t *testing.T) {
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()
	called := false
	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		called = true
		return nil, nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s validation status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("validation helper ran for non-Administrator")
	}
}

func TestValidationUnexpectedHelperFailureIsSanitized(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminValidationHelper
	defer func() { runAdminValidationHelper = old }()

	runAdminValidationHelper = func(_ context.Context) ([]byte, error) {
		return []byte("sensitive helper output"), validationTestExitError{code: 2}
	}

	rr := httptest.NewRecorder()
	s.adminValidation(rr, surfaceRequest(http.MethodGet, "/v1/admin/validation", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected validation helper failure status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "sensitive helper output") || !strings.Contains(rr.Body.String(), "JustVoxel validation is unavailable") {
		t.Fatalf("validation helper failure leaked details or missed bounded error: %s", rr.Body.String())
	}
}
