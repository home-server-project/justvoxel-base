package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionStatusIsAdministratorOnlyAndValidatesInputs(t *testing.T) {
	old := runAdminVersionStatus
	defer func() { runAdminVersionStatus = old }()
	called := false
	runAdminVersionStatus = func(_ context.Context, policy, version string) ([]byte, error) {
		called = true
		return []byte(`{"server_software":"Paper","installed":"26.2","available":"26.3","recommended":"26.2","selected_candidate":"26.2","policy":"recommended","update_available":true,"paper_supported":true}`), nil
	}
	viewer := surfaceTestServer(t, roleViewer)
	rr := httptest.NewRecorder()
	viewer.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/admin/version/status", ""))
	if rr.Code != http.StatusForbidden || called {
		t.Fatalf("viewer: %d, helper=%v", rr.Code, called)
	}
	admin := surfaceTestServer(t, roleAdministrator)
	rr = httptest.NewRecorder()
	admin.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/admin/version/status?policy=pinned&version=26.3%3Bbad", ""))
	if rr.Code != http.StatusBadRequest || called {
		t.Fatalf("unsafe version: %d, helper=%v", rr.Code, called)
	}
	rr = httptest.NewRecorder()
	admin.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/admin/version/status?policy=recommended", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"available":"26.3"`, `"recommended":"26.2"`, `"selected_candidate":"26.2"`, `"policy":"recommended"`, `"update_available":true`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("status missing %s: %s", want, rr.Body.String())
		}
	}
}

func TestSetupVersionPreviewUsesAdministratorAndExplicitBedrockChoice(t *testing.T) {
	old := runSetupVersionPreview
	defer func() { runSetupVersionPreview = old }()
	called := false
	runSetupVersionPreview = func(_ context.Context, policy, version, bedrock string) ([]byte, error) {
		called = true
		if policy != "pinned" || version != "1.21.8" || bedrock != "yes" {
			t.Fatalf("unexpected preview request: %q %q %q", policy, version, bedrock)
		}
		return []byte(`{"selected_candidate":"1.21.8","candidate_channel":"ALPHA","paper_supported":true}`), nil
	}
	viewer := surfaceTestServer(t, roleViewer)
	rr := httptest.NewRecorder()
	viewer.adminSetupVersionPreview(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup/version-preview?policy=pinned&version=1.21.8&bedrock=yes", ""))
	if rr.Code != http.StatusForbidden || called {
		t.Fatalf("viewer: %d, helper=%v", rr.Code, called)
	}
	admin := surfaceTestServer(t, roleAdministrator)
	rr = httptest.NewRecorder()
	admin.adminSetupVersionPreview(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup/version-preview?policy=pinned&version=1.21.8&bedrock=yes", ""))
	if rr.Code != http.StatusOK || !called || !strings.Contains(rr.Body.String(), `"candidate_channel":"ALPHA"`) {
		t.Fatalf("preview: %d %s", rr.Code, rr.Body.String())
	}
}
